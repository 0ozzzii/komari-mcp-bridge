package access

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"
)

var ErrDenied = errors.New("unauthorized or out of scope")

type Key struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Hash        string     `json:"hash,omitempty"`
	Nodes       []string   `json:"nodes"`
	Permissions []string   `json:"permissions"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}
type state struct {
	Enabled bool  `json:"enabled"`
	Keys    []Key `json:"keys"`
}
type Store struct {
	mu    sync.RWMutex
	path  string
	value state
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "access.json"), value: state{Enabled: false, Keys: []Key{}}}
	b, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(b, &s.value); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func RandomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
func clone(k Key) Key {
	k.Nodes = slices.Clone(k.Nodes)
	k.Permissions = slices.Clone(k.Permissions)
	if k.ExpiresAt != nil {
		v := *k.ExpiresAt
		k.ExpiresAt = &v
	}
	return k
}

func AtomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	// Windows does not support POSIX directory fsync through os.File.Sync.
	// The candidate file was flushed before Rename; Unix additionally flushes
	// the directory entry. Windows power-loss durability is not identical.
	if err == nil && runtime.GOOS != "windows" {
		if d, e := os.Open(filepath.Dir(path)); e == nil {
			err = d.Sync()
			d.Close()
		}
	}
	return err
}

func (s *Store) Create(name string, nodes, permissions []string, expires *time.Time) (Key, string, error) {
	if name == "" || len(nodes) == 0 || len(nodes) > 128 || len(permissions) == 0 {
		return Key{}, "", errors.New("name, nodes, and permissions required")
	}
	for _, p := range permissions {
		if !slices.Contains([]string{"terminal", "file.read", "file.write"}, p) {
			return Key{}, "", errors.New("unsupported permission")
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Key{}, "", err
	}
	token := "kmb_" + base64.RawURLEncoding.EncodeToString(b)
	k := Key{ID: RandomID(), Name: name, Hash: digest(token), Nodes: slices.Clone(nodes), Permissions: slices.Clone(permissions), Enabled: true, ExpiresAt: expires}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.value.Keys) >= 256 {
		return Key{}, "", errors.New("key limit reached")
	}
	s.value.Keys = append(s.value.Keys, k)
	if err := AtomicJSON(s.path, s.value); err != nil {
		s.value.Keys = s.value.Keys[:len(s.value.Keys)-1]
		return Key{}, "", err
	}
	k.Hash = ""
	return k, token, nil
}

func active(k Key) bool { return k.Enabled && (k.ExpiresAt == nil || time.Now().Before(*k.ExpiresAt)) }
func (s *Store) Authenticate(token string) (Key, error) {
	if len(token) != 47 {
		return Key{}, ErrDenied
	}
	hash := digest(token)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.value.Keys {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(k.Hash)) == 1 && active(k) {
			k = clone(k)
			k.Hash = ""
			return k, nil
		}
	}
	return Key{}, ErrDenied
}

func (s *Store) Require(id, node, permission string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.value.Keys {
		if k.ID == id && active(k) {
			if !slices.Contains(k.Nodes, node) {
				return ErrDenied
			}
			if permission != "" && (!s.value.Enabled || !slices.Contains(k.Permissions, permission)) {
				return ErrDenied
			}
			return nil
		}
	}
	return ErrDenied
}
func (s *Store) List() (bool, []Key) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]Key, len(s.value.Keys))
	for i, k := range s.value.Keys {
		keys[i] = clone(k)
		keys[i].Hash = ""
	}
	return s.value.Enabled, keys
}
func (s *Store) SetEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.value.Enabled
	s.value.Enabled = enabled
	if err := AtomicJSON(s.path, s.value); err != nil {
		s.value.Enabled = old
		return err
	}
	return nil
}
func (s *Store) Update(id string, nodes, permissions []string, enabled bool) error {
	if len(nodes) == 0 || len(nodes) > 128 {
		return errors.New("nonempty node scope required")
	}
	for _, p := range permissions {
		if !slices.Contains([]string{"terminal", "file.read", "file.write"}, p) {
			return errors.New("unsupported permission")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.value.Keys {
		if k.ID == id {
			old := clone(k)
			s.value.Keys[i].Nodes = slices.Clone(nodes)
			s.value.Keys[i].Permissions = slices.Clone(permissions)
			s.value.Keys[i].Enabled = enabled
			if err := AtomicJSON(s.path, s.value); err != nil {
				s.value.Keys[i] = old
				return err
			}
			return nil
		}
	}
	return ErrDenied
}
