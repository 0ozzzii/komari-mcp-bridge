//go:build linux

// Package testutil provides an isolated Komari-shaped protocol fixture backed
// by a real local PTY. It is not the official Server/Agent end-to-end stack.
package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

type shell struct {
	mu    sync.Mutex
	write sync.Mutex
	conn  *websocket.Conn
	file  *os.File
	cmd   *exec.Cmd
	node  string
	id    string
}
type Host struct {
	mu            sync.Mutex
	Server        *httptest.Server
	shells        map[string]*shell
	env           []string
	Opens         atomic.Int32
	Inputs        atomic.Int32
	FileMutations atomic.Int32
}

const AdminKey = "isolated-upstream-test-key"

func NewHost(t testing.TB) *Host {
	return NewHostWithEnv(t, nil)
}

// Overrides affect only isolated PTY children, never the test runner's env.
func NewHostWithEnv(t testing.TB, env []string) *Host {
	t.Helper()
	h := &Host{shells: map[string]*shell{}, env: append([]string(nil), env...)}
	h.Server = httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(func() {
		h.Server.Close()
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, s := range h.shells {
			s.stop()
		}
	})
	return h
}
func newShell(node, id string, env []string) (*shell, error) {
	initial := "/bin/sh"
	for _, setting := range env {
		if strings.HasPrefix(setting, "KMB_TEST_INITIAL_SHELL=") {
			initial = strings.TrimPrefix(setting, "KMB_TEST_INITIAL_SHELL=")
		}
	}
	cmd := exec.Command(initial, "-i")
	cmd.Env = append(os.Environ(), "PS1=", "PS2=")
	cmd.Env = append(cmd.Env, env...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	s := &shell{node: node, id: id, file: f, cmd: cmd}
	go cmd.Wait()
	go s.output()
	return s, nil
}
func (s *shell) stop() {
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	s.mu.Unlock()
	s.cmd.Process.Kill()
	s.file.Close()
}
func (s *shell) output() {
	buf := make([]byte, 4096)
	for {
		n, err := s.file.Read(buf)
		if n > 0 {
			s.write.Lock()
			s.mu.Lock()
			conn := s.conn
			s.mu.Unlock()
			if conn != nil {
				// Split markers and UTF-8 deliberately; data boundaries have no semantics.
				for start := 0; start < n; start += 7 {
					end := min(start+7, n)
					if conn.WriteMessage(websocket.BinaryMessage, buf[start:end]) != nil {
						break
					}
				}
			}
			s.write.Unlock()
		}
		if err != nil {
			return
		}
	}
}
func (h *Host) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+AdminKey {
		http.Error(w, "unauthorized", 401)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/mcp/runtime/node/") {
		json.NewEncoder(w).Encode(map[string]any{"memory_total": uint64(2 << 30), "memory_used": 0, "fresh": true, "updated_at": time.Now(), "scope": "local fixture"})
		return
	}
	if r.URL.Path == "/api/rpc2" {
		var body struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		result := any(map[string]any{"success": true})
		if body.Method == "admin:listClients" {
			result = []map[string]any{{"uuid": "node-a", "name": "allowed", "token": "DO-NOT-EXPOSE-CLIENT-TOKEN"}, {"uuid": "node-b", "name": "other"}}
		} else if body.Method == "admin:fileDelete" {
			h.FileMutations.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/file/download") {
		w.Header().Set("Content-Range", "bytes 0-4/5")
		w.WriteHeader(206)
		w.Write([]byte("hello"))
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/client/") || !strings.HasSuffix(r.URL.Path, "/terminal") {
		http.NotFound(w, r)
		return
	}
	node := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/client/"), "/terminal")
	id := r.URL.Query().Get("request_id")
	h.mu.Lock()
	var s *shell
	var err error
	if id != "" {
		s = h.shells[id]
	}
	if id != "" && s == nil {
		h.mu.Unlock()
		http.Error(w, "expired", 404)
		return
	}
	if s == nil {
		id = node + "-" + randomID()
		s, err = newShell(node, id, h.env)
		if err == nil {
			h.shells[id] = s
			h.Opens.Add(1)
		}
	}
	h.mu.Unlock()
	if err != nil {
		http.Error(w, "PTY failed", 500)
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.write.Lock()
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.conn = conn
	s.mu.Unlock()
	conn.WriteJSON(map[string]string{"request_id": id})
	conn.WriteMessage(websocket.TextMessage, []byte("等待探针连接"))
	s.write.Unlock()
	defer func() {
		conn.Close()
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
	}()
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage {
			h.Inputs.Add(1)
			s.file.Write(data)
		} else if kind == websocket.TextMessage {
			var control struct {
				Type      string `json:"type"`
				Nonce     string `json:"nonce"`
				CommandID string `json:"command_id"`
				TimeoutMS int    `json:"execution_timeout_ms"`
				Cols      uint16 `json:"cols"`
				Rows      uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &control) == nil {
				if control.Type == "mcp_hello" {
					s.write.Lock()
					conn.WriteJSON(map[string]any{"type": "mcp_ready", "protocol": 1, "nonce": control.Nonce, "resource": map[string]any{"memory_total": uint64(2 << 30), "memory_used": 0, "fresh": true, "updated_at": time.Now()}})
					s.write.Unlock()
					continue
				}
				if control.Type == "mcp_arm" {
					s.write.Lock()
					conn.WriteJSON(map[string]any{"type": "mcp_arm_ack", "command_id": control.CommandID, "deadline": time.Now().Add(time.Duration(control.TimeoutMS) * time.Millisecond)})
					s.write.Unlock()
					continue
				}
				if control.Type == "close" {
					s.stop()
					return
				}
				if control.Type == "resize" {
					pty.Setsize(s.file, &pty.Winsize{Cols: control.Cols, Rows: control.Rows})
				}
			}
		}
	}
}

var serial atomic.Int64

func randomID() string     { return fmtID(serial.Add(1)) }
func fmtID(n int64) string { return strconv.FormatInt(n, 10) }
func (h *Host) Drop(id string) {
	h.mu.Lock()
	s := h.shells[id]
	h.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		if s.conn != nil {
			s.conn.Close()
		}
		s.mu.Unlock()
	}
}
func (h *Host) Replace(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.shells[id]
	if old == nil {
		return os.ErrNotExist
	}
	old.stop()
	s, err := newShell(old.node, id, h.env)
	if err == nil {
		h.shells[id] = s
	}
	return err
}
