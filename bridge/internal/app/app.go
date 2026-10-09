package app

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/terminal"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/tools"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Config struct {
	Version      string
	Commit       string
	BaseURL      string
	APIKey       string
	ControlToken string
	StateDir     string
	Terminal     terminal.Options
}
type App struct {
	Execution    *execution.Store
	Policy       *access.Store
	Terminal     *terminal.Manager
	Upstream     *upstream.Client
	service      *tools.Service
	controlToken string
	mu           sync.Mutex
	servers      map[string]*mcp.Server
	controlSlots chan struct{}
	slots        chan struct{}
	callerSlots  map[string]chan struct{}
	audit        *auditLog
}

func New(ctx context.Context, c Config) (*App, error) {
	if len(c.ControlToken) < 32 {
		return nil, errors.New("BRIDGE_CONTROL_TOKEN must contain at least 32 characters")
	}
	if c.StateDir == "" {
		return nil, errors.New("state directory is required")
	}
	policy, err := access.Open(c.StateDir)
	if err != nil {
		return nil, err
	}
	up, err := upstream.New(c.BaseURL, c.APIKey)
	if err != nil {
		return nil, err
	}
	audit, err := openAudit(c.StateDir)
	if err != nil {
		return nil, err
	}
	execPolicy, err := execution.Open(c.StateDir)
	if err != nil {
		audit.close()
		return nil, err
	}
	c.Terminal.Execution = execPolicy
	c.Terminal.Event = audit.record
	manager, err := terminal.NewWithOptions(ctx, up, policy, filepath.Join(c.StateDir, "sessions"), c.Terminal)
	if err != nil {
		audit.close()
		return nil, err
	}
	a := &App{Execution: execPolicy, Policy: policy, Terminal: manager, Upstream: up, controlToken: c.ControlToken, servers: map[string]*mcp.Server{}, slots: make(chan struct{}, 64), controlSlots: make(chan struct{}, 8), callerSlots: map[string]chan struct{}{}, audit: audit}
	if c.Version == "" {
		c.Version = "development"
	}
	if c.Commit == "" {
		c.Commit = "unknown"
	}
	a.service = &tools.Service{Version: c.Version, Commit: c.Commit, Policy: policy, Terminal: manager, Upstream: up, Dir: filepath.Join(c.StateDir, "mutations"), Audit: audit.record}
	return a, nil
}
func (a *App) Close() { a.Terminal.Shutdown(); a.audit.close() }
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (a *App) Handler() http.Handler {
	stream := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		key, err := a.Policy.Authenticate(bearer(r))
		if err != nil {
			return nil
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		server := a.servers[key.ID]
		if server == nil {
			server = a.service.Server(key)
			a.servers[key.ID] = server
		}
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]any{"service": "komari-mcp-bridge", "ready": true, "upstream_verified": false})
	})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := a.Policy.Authenticate(bearer(r))
		if err != nil {
			jsonReply(w, 401, map[string]string{"error": "UNAUTHORIZED"})
			return
		}
		// A bounded control lane cannot be consumed by long-poll output reads.
		reserved := false
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
			data, e := io.ReadAll(r.Body)
			if e != nil {
				jsonReply(w, 413, map[string]string{"error": "REQUEST_TOO_LARGE"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			var rpc struct {
				Method string `json:"method"`
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			if json.Unmarshal(data, &rpc) == nil && rpc.Method == "tools/call" {
				switch rpc.Params.Name {
				case "komari_command_interrupt", "komari_session_close", "komari_session_status":
					reserved = true
				}
			}
		}
		slots := a.slots
		callerID := key.ID
		callerCapacity := 8
		if reserved {
			slots = a.controlSlots
			callerID += "/control"
			callerCapacity = 2
		}
		a.mu.Lock()
		caller := a.callerSlots[callerID]
		if caller == nil {
			caller = make(chan struct{}, callerCapacity)
			a.callerSlots[callerID] = caller
		}
		a.mu.Unlock()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			jsonReply(w, 429, map[string]string{"error": "TOO_MANY_REQUESTS"})
			return
		}
		select {
		case caller <- struct{}{}:
			defer func() { <-caller }()
		default:
			jsonReply(w, 429, map[string]string{"error": "TOO_MANY_REQUESTS"})
			return
		}
		// Authenticated, stateless transport: no HTTP request owns a remote shell.
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		stream.ServeHTTP(w, r)
	}))
	return mux
}

// ControlHandler must be mounted on a separate loopback/container-internal
// listener. Normal scoped MCP keys never authenticate to this interface.
func (a *App) ControlHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(bearer(r)), []byte(a.controlToken)) != 1 {
			jsonReply(w, 401, map[string]string{"error": "UNAUTHORIZED"})
			return
		}
		if r.Header.Get("Origin") != "" {
			jsonReply(w, 403, map[string]string{"error": "browser control access must use the Komari administrator proxy"})
			return
		}
		enabled, keys := a.Policy.List()
		switch r.Method + " " + r.URL.Path {
		case "GET /control/status":
			jsonReply(w, 200, map[string]any{"enabled": enabled, "key_count": len(keys), "tool_count": len(tools.Definitions())})
		case "GET /control/policy":
			jsonReply(w, 200, a.Execution.Snapshot())
		case "GET /control/logs":
			a.logList(w, r)
		case "GET /control/logs/output":
			a.outputLog(w, r)
		case "POST /control/policy/update":
			var b struct {
				Scope    string              `json:"scope"`
				ID       string              `json:"id"`
				Settings *execution.Settings `json:"settings"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if d.Decode(&b) != nil {
				jsonReply(w, 400, map[string]string{"error": "invalid policy request"})
				return
			}
			if b.Scope == "node" && !upstream.ValidNode(b.ID) {
				jsonReply(w, 400, map[string]string{"error": "invalid node"})
				return
			}
			if b.Scope == "key" {
				found := false
				for _, k := range keys {
					if k.ID == b.ID {
						found = true
					}
				}
				if !found {
					jsonReply(w, 400, map[string]string{"error": "unknown key"})
					return
				}
			}
			e := a.Execution.Update(b.Scope, b.ID, b.Settings)
			a.audit.record(map[string]any{"event": "execution_policy_update", "scope": b.Scope, "target_id": b.ID, "success": e == nil})
			if e != nil {
				jsonReply(w, 400, map[string]string{"error": e.Error()})
				return
			}
			a.Terminal.RefreshPolicy()
			jsonReply(w, 200, a.Execution.Snapshot())
		case "GET /control/keys":
			jsonReply(w, 200, map[string]any{"enabled": enabled, "keys": keys})
		case "GET /control/nodes":
			raw, err := a.Upstream.RPC(r.Context(), "admin:listClients", map[string]any{})
			if err != nil {
				jsonReply(w, 502, map[string]string{"error": "upstream node listing failed"})
				return
			}
			var input []map[string]any
			if json.Unmarshal(raw, &input) != nil {
				jsonReply(w, 502, map[string]string{"error": "invalid upstream nodes"})
				return
			}
			output := []map[string]any{}
			for _, n := range input {
				output = append(output, map[string]any{"uuid": n["uuid"], "name": n["name"], "version": n["version"]})
			}
			jsonReply(w, 200, output)
		default:
			var body struct {
				ID          string     `json:"id"`
				Name        string     `json:"name"`
				Nodes       []string   `json:"nodes"`
				Permissions []string   `json:"permissions"`
				Enabled     bool       `json:"enabled"`
				ExpiresAt   *time.Time `json:"expires_at"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 65536)
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				jsonReply(w, 400, map[string]string{"error": "invalid control request"})
				return
			}
			for _, n := range body.Nodes {
				if !upstream.ValidNode(n) {
					jsonReply(w, 400, map[string]string{"error": "invalid node UUID"})
					return
				}
			}
			var err error
			var value any
			switch r.Method + " " + r.URL.Path {
			case "POST /control/enabled":
				err = a.Policy.SetEnabled(body.Enabled)
				value = map[string]bool{"enabled": body.Enabled}
			case "POST /control/keys/create":
				var key access.Key
				var token string
				key, token, err = a.Policy.Create(body.Name, body.Nodes, body.Permissions, body.ExpiresAt)
				value = map[string]any{"key": key, "token": token, "display_once": true}
			case "POST /control/keys/update":
				err = a.Policy.Update(body.ID, body.Nodes, body.Permissions, body.Enabled)
				value = map[string]bool{"updated": err == nil}
			default:
				jsonReply(w, 404, map[string]string{"error": "NOT_FOUND"})
				return
			}
			a.audit.record(map[string]any{"event": "control", "action": r.URL.Path, "key_id": body.ID, "success": err == nil})
			if err != nil {
				jsonReply(w, 400, map[string]string{"error": err.Error()})
				return
			}
			jsonReply(w, 200, value)
		}
	})
}

type auditLog struct {
	readSlots   chan struct{}
	mu          sync.Mutex
	path        string
	file        *os.File
	size        int64
	lastWarning time.Time
	lastAttempt time.Time
	closed      bool
}

func openAudit(dir string) (*auditLog, error) {
	a := &auditLog{path: filepath.Join(dir, "audit.jsonl"), readSlots: make(chan struct{}, 1)}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	a.file = f
	s, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	a.size = s.Size()
	return a, nil
}
func (a *auditLog) record(v map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	if a.file == nil {
		if time.Since(a.lastAttempt) < time.Minute {
			return
		}
		a.lastAttempt = time.Now()
		f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			a.warnLocked("audit_unavailable")
			return
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			a.warnLocked("audit_unavailable")
			return
		}
		a.file, a.size = f, info.Size()
	}
	for name, value := range v {
		if text, ok := value.(string); ok && len(text) > 256 {
			v[name] = "[oversized metadata omitted]"
		}
	}
	v["time"] = time.Now().UTC()
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	if a.size+int64(len(b)) > 8<<20 {
		a.file.Close()
		// Windows rename cannot replace an existing backup. Remove only our
		// oldest rotated file; the current log remains if the rotation fails.
		if err := os.Remove(a.path + ".1"); err != nil && !os.IsNotExist(err) {
			a.file = nil
			a.warnLocked("audit_rotation_failed")
			return
		}
		if err := os.Rename(a.path, a.path+".1"); err != nil {
			a.file = nil
			a.warnLocked("audit_rotation_failed")
			return
		}
		f, err := os.OpenFile(a.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			a.file = nil
			a.warnLocked("audit_open_failed")
			return
		}
		a.file = f
		a.size = 0
	}
	n, err := a.file.Write(b)
	a.size += int64(n)
	if err != nil {
		a.warnLocked("audit_write_failed")
	}
}
func (a *auditLog) warnLocked(code string) {
	if time.Since(a.lastWarning) >= time.Minute {
		a.lastWarning = time.Now()
		log.Printf("diagnostics warning: %s; check state directory permissions and available storage", code)
	}
}
func (a *auditLog) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.file != nil {
		a.file.Close()
		a.file = nil
	}
}
func (a *App) Description() string {
	return fmt.Sprintf("%s; control listener must remain private", a.service.Describe())
}
