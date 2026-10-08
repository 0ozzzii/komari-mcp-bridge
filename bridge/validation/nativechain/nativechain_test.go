//go:build linux

// Local source integration. Authentication and node discovery are fixtures;
// forwarding, probe PTY, bridge sessions and MCP calls use actual source code.
package nativechain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/komari-monitor/komari-agent/executionguard"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/app"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	probe "github.com/komari-monitor/komari-agent/terminal"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/web/api"
	panel "github.com/komari-monitor/komari/web/api/terminal"
	"github.com/komari-monitor/komari/web/connection"
	panelrouter "github.com/komari-monitor/komari/web/router"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatal(e)
	}
	if r.IsError {
		t.Fatalf("%s: %v", name, r.Content)
	}
	b, _ := json.Marshal(r.StructuredContent)
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func eventually(t *testing.T, limit time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("local source chain did not reach expected state")
}
func TestActualForwarderProbePTYAndMCPRecovery(t *testing.T) {
	// Audit storage is confined to this temporary test process and directory.
	t.Chdir(t.TempDir())
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = filepath.Join(t.TempDir(), "audit.db")
	if e := dbcore.Initialize(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { dbcore.Close() })
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var fixture *httptest.Server
	var probeCalls sync.WaitGroup
	router := gin.New()
	router.GET("/api/clients/terminal", func(c *gin.Context) { c.Set("client_uuid", "node-a"); panel.EstablishConnection(c) })
	router.GET("/api/admin/client/node-a/terminal", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer fixture-admin" {
			c.Status(401)
			return
		}
		raw, e := upgrader.Upgrade(c.Writer, c.Request, nil)
		if e != nil {
			return
		}
		conn := connection.NewSafeConn(raw)
		id := c.Query("request_id")
		if id == "" {
			id = access.RandomID()
		}
		panel.TerminalSessionsMutex.Lock()
		s := panel.TerminalSessions[id]
		if s == nil {
			s = &panel.TerminalSession{UUID: "node-a", UserUUID: "fixture", RequesterIp: "127.0.0.1"}
			panel.TerminalSessions[id] = s
		}
		oldBrowser, oldAgent := s.Browser, s.Agent
		s.Browser = conn
		s.Agent = nil
		s.Forwarding = false
		panel.TerminalSessionsMutex.Unlock()
		if oldBrowser != nil {
			oldBrowser.Close()
		}
		if oldAgent != nil {
			oldAgent.Close()
		}
		if e := conn.WriteJSON(map[string]string{"request_id": id}); e != nil {
			return
		}
		policyQuery := c.Query("mcp_policy")
		probeCalls.Add(1)
		go func() {
			defer probeCalls.Done()
			ws, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(fixture.URL, "http")+"/api/clients/terminal?id="+id+"&token=fixture-probe", nil)
			if e == nil {
				options := executionguard.DefaultOptions()
				if b, e := base64.RawURLEncoding.DecodeString(policyQuery); e == nil {
					json.Unmarshal(b, &options)
				}
				probe.StartTerminalWithOptions(ws, id, options)
			}
		}()
	})
	fixture = httptest.NewServer(router)
	defer fixture.Close()
	a, e := app.New(context.Background(), app.Config{BaseURL: fixture.URL, APIKey: "fixture-admin", ControlToken: strings.Repeat("x", 32), StateDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	key, token, e := a.Policy.Create("native-chain", []string{"node-a"}, []string{"terminal"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Policy.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	_ = key
	endpoint := httptest.NewServer(a.Handler())
	defer endpoint.Close()
	// Route MCP through the actual single-domain panel entry as well. The
	// terminal fixture above still provides auth, node identity and dispatch.
	t.Setenv("KOMARI_MCP_UPSTREAM_URL", endpoint.URL+"/mcp")
	frontRouter := gin.New()
	frontRouter.Use(api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	panelrouter.Register(frontRouter)
	front := httptest.NewServer(frontRouter)
	defer front.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "native-chain", Version: "1"}, nil)
	sdk, e := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: front.URL + "/mcp", HTTPClient: &http.Client{Transport: publicHostTransport{token}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer sdk.Close()
	call(t, sdk, "komari_execution_policy", map[string]any{"action": "set", "parallel": 1, "node_uuid": "node-a", "work_id": "native"})
	opened := call(t, sdk, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "native"})
	id := opened["session_id"].(string)
	status := func() map[string]any { return call(t, sdk, "komari_session_status", map[string]any{"session_id": id}) }
	eventually(t, 8*time.Second, func() bool {
		v := status()
		return v["connection_state"] == "attached_context_unverified" || v["connection_state"] == "ready" && v["context_verified"] == true
	})
	first := status()
	pid := first["shell_pid"]
	upstream := first["upstream_request_id"].(string)
	r := call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "set", "command": "cd /tmp; export KEEP_NATIVE=hello", "wait_timeout_ms": 1000})
	if r["command_state"] != "completed" {
		t.Fatalf("setup command: %v", r)
	}
	// Quiet for longer than the real 20-second heartbeat interval. Neither tool
	// wait expiry nor lack of logs should close the shell or finish the command.
	r = call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "quiet", "command": "sleep 22; printf quiet-done", "wait_timeout_ms": 50})
	if r["command_state"] == "completed" {
		t.Fatal("quiet task ended prematurely")
	}
	cursor := r["next_cursor"]
	eventually(t, 30*time.Second, func() bool {
		r = call(t, sdk, "komari_output_read", map[string]any{"session_id": id, "command_id": "quiet", "cursor": cursor, "wait_timeout_ms": 500})
		cursor = r["next_cursor"]
		return r["command_state"] == "completed"
	})
	if v := status(); v["last_admin_pong"] == "0001-01-01T00:00:00Z" {
		t.Fatal("no real heartbeat observed")
	}
	panel.TerminalSessionsMutex.Lock()
	browser := panel.TerminalSessions[upstream].Browser
	panel.TerminalSessionsMutex.Unlock()
	browser.Close()
	eventually(t, 8*time.Second, func() bool {
		v := status()
		return v["connection_state"] == "ready" && v["context_verified"] == true && v["output_gap"] == true
	})
	if v := status(); v["shell_pid"] != pid || v["upstream_request_id"] != upstream {
		t.Fatal("original PTY not recovered")
	}
	r = call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "check", "command": "printf '%s:' \"$KEEP_NATIVE\"; pwd; (exit 7)", "wait_timeout_ms": 1000})
	if r["command_state"] != "completed" || r["exit_code"] != float64(7) || !strings.Contains(fmt.Sprint(r["output"]), "hello:/tmp") {
		t.Fatalf("context/exit code not preserved: %v", r)
	}

	// Deadline is owned by the ACTUAL probe, not this HTTP request or bridge.
	childPID := filepath.Join(t.TempDir(), "child.pid")
	r = call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "deadline", "command": "sh -c 'echo $$ > " + childPID + "; exec sleep 20'", "execution_timeout_ms": 600, "wait_timeout_ms": 50})
	eventually(t, time.Second, func() bool { _, e := os.Stat(childPID); return e == nil })
	rawPID, e := os.ReadFile(childPID)
	if e != nil {
		t.Fatal(e)
	}
	child, e := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if e != nil {
		t.Fatal(e)
	}
	panel.TerminalSessionsMutex.Lock()
	agentConn := panel.TerminalSessions[upstream].Agent
	panel.TerminalSessionsMutex.Unlock()
	agentConn.Close()
	eventually(t, 4*time.Second, func() bool { return syscall.Kill(child, 0) == syscall.ESRCH })
	eventually(t, 8*time.Second, func() bool {
		v := status()
		return v["connection_state"] == "attached_context_unverified" || v["connection_state"] == "ready" && v["context_verified"] == true
	})
	uncertain := call(t, sdk, "komari_output_read", map[string]any{"session_id": id, "command_id": "deadline"})
	if uncertain["command_state"] != "completed" && uncertain["execution_uncertain"] != true {
		t.Fatal("lost deadline outcome was not marked uncertain", uncertain)
	}
	// The exact same command ID must return its history, never create another PID.
	before, _ := os.ReadFile(childPID)
	call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "deadline", "command": "sh -c 'echo $$ > " + childPID + "; exec sleep 20'", "execution_timeout_ms": 600})
	after, _ := os.ReadFile(childPID)
	if string(before) != string(after) {
		t.Fatal("uncertain command replayed")
	}
	call(t, sdk, "komari_session_close", map[string]any{"session_id": id})
	done := make(chan struct{})
	go func() { probeCalls.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("probe input loop did not stop")
	}
	t.Log("LOCAL SOURCE INTEGRATION: actual panel forwarder + actual probe PTY + bridge + official MCP; 22-second silence, PTY reattach, disconnected probe deadline and no replay verified")
}
