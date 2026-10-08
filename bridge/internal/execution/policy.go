// Package execution holds administrator policy and short-lived work leases.
// A lease authorizes NEW dispatch; it never owns or cancels an existing task.
package execution

import (
	"errors"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const Instructions = "先用 komari_nodes_list 查看资源，再用 komari_execution_policy 为节点/work_id 协商并发。普通执行默认30分钟；长任务派发前显式填写 execution_timeout_ms，授权上限默认6小时。读输出、状态、心跳和重复 command_id 不续期；结果未知禁止重跑。"

type Settings struct {
	MaxParallel           int  `json:"max_parallel"`
	LeaseSeconds          int  `json:"lease_seconds"`
	DefaultTimeoutSeconds int  `json:"default_timeout_seconds"`
	MaxTimeoutSeconds     int  `json:"max_timeout_seconds"`
	IdleSeconds           int  `json:"idle_seconds"`
	OutputBytes           int  `json:"output_bytes"`
	MinFreePercent        int  `json:"min_free_percent"`
	AllowMaintenance      bool `json:"allow_maintenance"`
	MaintenanceSeconds    int  `json:"maintenance_seconds"`
	ForceCloseOnTimeout   bool `json:"force_close_on_timeout"`
}

func Defaults() Settings {
	return Settings{LeaseSeconds: 600, DefaultTimeoutSeconds: 1800, MaxTimeoutSeconds: 21600, IdleSeconds: 1800, OutputBytes: 2 << 20, MinFreePercent: 10, MaintenanceSeconds: 900}
}
func (p Settings) Validate() error {
	if p.MaxParallel < 0 || p.MaxParallel > 64 || p.LeaseSeconds < 60 || p.LeaseSeconds > 3600 || p.DefaultTimeoutSeconds < 1 || p.MaxTimeoutSeconds < p.DefaultTimeoutSeconds || p.MaxTimeoutSeconds > 7*86400 || p.IdleSeconds < 60 || p.IdleSeconds > 86400 || p.OutputBytes < 32768 || p.OutputBytes > 8<<20 || p.MinFreePercent < 0 || p.MinFreePercent > 50 || p.MaintenanceSeconds < 30 || p.MaintenanceSeconds > 3600 {
		return errors.New("invalid execution policy bounds")
	}
	return nil
}

type Resource struct {
	MemoryTotal uint64    `json:"memory_total"`
	MemoryUsed  uint64    `json:"memory_used"`
	UpdatedAt   time.Time `json:"updated_at"`
	Fresh       bool      `json:"fresh"`
	Scope       string    `json:"scope"`
}

func Capacity(r Resource) int {
	if !r.Fresh || r.MemoryTotal == 0 {
		return 1
	}
	switch {
	case r.MemoryTotal < 1<<30:
		return 5
	case r.MemoryTotal < 2<<30:
		return 8
	case r.MemoryTotal < 3<<30:
		return 10
	default:
		return 12
	}
}
func LowMemory(r Resource, p Settings) bool {
	return r.Fresh && r.MemoryTotal > 0 && p.MinFreePercent > 0 && (r.MemoryUsed >= r.MemoryTotal || float64(r.MemoryTotal-r.MemoryUsed)/float64(r.MemoryTotal)*100 < float64(p.MinFreePercent))
}

type config struct {
	Revision uint64              `json:"revision"`
	Default  Settings            `json:"default"`
	Nodes    map[string]Settings `json:"nodes"`
	Keys     map[string]Settings `json:"keys"`
}
type Lease struct {
	Owner        string     `json:"-"`
	Node         string     `json:"node_uuid"`
	Work         string     `json:"work_id"`
	Parallel     int        `json:"parallel"`
	ExpiresAt    time.Time  `json:"expires_at"`
	LastDispatch *time.Time `json:"last_forward_dispatch,omitempty"`
}
type Store struct {
	mu     sync.Mutex
	path   string
	c      config
	leases map[string]Lease
	Now    func() time.Time
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "execution.json"), c: config{Revision: 1, Default: Defaults(), Nodes: map[string]Settings{}, Keys: map[string]Settings{}}, leases: map[string]Lease{}, Now: time.Now}
	b, e := os.ReadFile(s.path)
	if e == nil {
		if e = unmarshal(b, &s.c); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if e = s.c.Default.Validate(); e != nil {
		return nil, e
	}
	if s.c.Nodes == nil {
		s.c.Nodes = map[string]Settings{}
	}
	if s.c.Keys == nil {
		s.c.Keys = map[string]Settings{}
	}
	for _, m := range []map[string]Settings{s.c.Nodes, s.c.Keys} {
		for _, p := range m {
			if e = p.Validate(); e != nil {
				return nil, e
			}
		}
	}
	return s, nil
}
func (s *Store) Snapshot() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := cloneConfig(s.c)
	leases := make([]Lease, 0, len(s.leases))
	for _, l := range s.leases {
		leases = append(leases, l)
	}
	return map[string]any{"revision": c.Revision, "default": c.Default, "nodes": c.Nodes, "keys": c.Keys, "leases": leases}
}
func (s *Store) Update(scope, id string, p *Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := cloneConfig(s.c)
	if p != nil {
		if e := p.Validate(); e != nil {
			return e
		}
	}
	switch scope {
	case "default":
		if p == nil {
			return errors.New("default cannot be removed")
		}
		s.c.Default = *p
	case "node", "key":
		m := s.c.Nodes
		if scope == "key" {
			m = s.c.Keys
		}
		if len(id) < 1 || len(id) > 128 {
			return errors.New("invalid policy target")
		}
		if p == nil {
			delete(m, id)
		} else {
			if len(m) >= 256 {
				if _, ok := m[id]; !ok {
					return errors.New("policy target limit")
				}
			}
			m[id] = *p
		}
	default:
		return errors.New("invalid policy scope")
	}
	s.c.Revision++
	if e := access.AtomicJSON(s.path, s.c); e != nil {
		s.c = old
		return e
	}
	return nil
}
func (s *Store) Effective(owner, node string, r Resource) (Settings, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effective(owner, node, r)
}
func (s *Store) effective(owner, node string, r Resource) (Settings, int) {
	p := s.c.Default
	if n, ok := s.c.Nodes[node]; ok {
		p = n
	}
	hard := Capacity(r)
	if p.MaxParallel > 0 {
		hard = p.MaxParallel
	}
	if k, ok := s.c.Keys[owner]; ok {
		if k.MaxParallel > 0 {
			hard = min(hard, k.MaxParallel)
		}
		p.MaxTimeoutSeconds = min(p.MaxTimeoutSeconds, k.MaxTimeoutSeconds)
		p.DefaultTimeoutSeconds = min(p.DefaultTimeoutSeconds, k.DefaultTimeoutSeconds, p.MaxTimeoutSeconds)
		p.LeaseSeconds = min(p.LeaseSeconds, k.LeaseSeconds)
		p.IdleSeconds = min(p.IdleSeconds, k.IdleSeconds)
		p.MinFreePercent = max(p.MinFreePercent, k.MinFreePercent)
		p.AllowMaintenance = p.AllowMaintenance && k.AllowMaintenance
		p.MaintenanceSeconds = min(p.MaintenanceSeconds, k.MaintenanceSeconds)
		p.OutputBytes = min(p.OutputBytes, k.OutputBytes)
		p.ForceCloseOnTimeout = p.ForceCloseOnTimeout && k.ForceCloseOnTimeout
	} else {
		p.AllowMaintenance = false
		p.ForceCloseOnTimeout = false
	}
	return p, hard
}
func leaseKey(owner, node, work string) string { return owner + "\x00" + node + "\x00" + work }
func (s *Store) Set(owner, node, work string, parallel int, r Resource) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, hard := s.effective(owner, node, r)
	if parallel < 1 || parallel > hard {
		return Lease{}, errors.New("parallel exceeds current device/key hard limit")
	}
	key := leaseKey(owner, node, work)
	if len(s.leases) >= 1024 {
		for id, l := range s.leases {
			if !s.Now().Before(l.ExpiresAt) {
				delete(s.leases, id)
			}
		}
		if len(s.leases) >= 1024 {
			return Lease{}, errors.New("work policy limit")
		}
	}
	l := Lease{Owner: owner, Node: node, Work: work, Parallel: parallel, ExpiresAt: s.Now().Add(time.Duration(p.LeaseSeconds) * time.Second)}
	s.leases[key] = l
	return l, nil
}
func (s *Store) Get(owner, node, work string) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseKey(owner, node, work)]
	if !ok || !s.Now().Before(l.ExpiresAt) {
		return l, errors.New("policy_required: use komari_execution_policy action=set; querying or running output does not renew")
	}
	return l, nil
}

// Renew is called only AFTER a new dispatch/interactive input was accepted.
func (s *Store) Renew(owner, node, work string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := leaseKey(owner, node, work)
	l, ok := s.leases[id]
	if !ok || !s.Now().Before(l.ExpiresAt) {
		return
	}
	p, _ := s.effective(owner, node, Resource{})
	now := s.Now()
	l.LastDispatch = &now
	l.ExpiresAt = now.Add(time.Duration(p.LeaseSeconds) * time.Second)
	s.leases[id] = l
}

func (s *Store) Revision() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.c.Revision }

func (s *Store) NodeSettings(node string, r Resource) (Settings, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.c.Default
	if n, ok := s.c.Nodes[node]; ok {
		p = n
	}
	return p, s.c.Revision
}
