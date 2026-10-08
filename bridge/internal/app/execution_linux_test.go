//go:build linux

package app

import (
	"context"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNegotiationLeaseSharedSlotsAndReservedControl(t *testing.T) {
	h := testutil.NewHost(t)
	a, e := New(context.Background(), Config{BaseURL: h.Server.URL, APIKey: testutil.AdminKey, ControlToken: strings.Repeat("c", 32), StateDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	a.Execution.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	k, token, _ := a.Policy.Create("execution", []string{"node-a"}, []string{"terminal"}, nil)
	a.Policy.SetEnabled(true)
	p := execution.Defaults()
	p.MaxParallel = 1
	p.AllowMaintenance = true
	if e = a.Execution.Update("node", "node-a", &p); e != nil {
		t.Fatal(e)
	}
	if e = a.Execution.Update("key", k.ID, &p); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	sdk := connect(t, server.URL, token)
	if !strings.Contains(sdk.InitializeResult().Instructions, "execution_timeout_ms") {
		t.Fatal("initial rules absent")
	}
	reject := func(name string, args map[string]any) {
		t.Helper()
		r, e := sdk.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		if !r.IsError {
			t.Fatalf("%s was accepted without required admission", name)
		}
	}
	reject("komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "work"})
	if h.Opens.Load() != 0 {
		t.Fatal("missing policy created PTY")
	}
	call(t, sdk, "komari_execution_policy", map[string]any{"node_uuid": "node-a", "work_id": "work", "action": "set", "parallel": 1})
	opened := call(t, sdk, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "work"})
	id := opened["session_id"].(string)
	until := time.Now().Add(3 * time.Second)
	for call(t, sdk, "komari_session_status", map[string]any{"session_id": id})["connection_state"] != "ready" {
		if time.Now().After(until) {
			t.Fatal("fixture shell not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reject("komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "work", "session_name": "second"})

	_, token2, _ := a.Policy.Create("second caller", []string{"node-a"}, []string{"terminal"}, nil)
	sdk2 := connect(t, server.URL, token2)
	call(t, sdk2, "komari_execution_policy", map[string]any{"node_uuid": "node-a", "work_id": "other", "action": "set", "parallel": 1})
	blocked, e := sdk2.CallTool(context.Background(), &mcp.CallToolParams{Name: "komari_session_open", Arguments: map[string]any{"node_uuid": "node-a", "work_id": "other"}})
	if e != nil || !blocked.IsError {
		t.Fatal("second Key bypassed shared device cap", e)
	}
	maintenance := call(t, sdk, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "repair", "maintenance": true})
	call(t, sdk, "komari_session_close", map[string]any{"session_id": maintenance["session_id"]})
	reject("komari_command_run", map[string]any{"session_id": id, "command_id": "too-long", "command": "printf unsafe", "execution_timeout_ms": 21600001})
	call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "one", "command": "printf one", "wait_timeout_ms": 1000, "execution_timeout_ms": 3600000})
	before, _ := a.Execution.Get(k.ID, "node-a", "work")
	call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "one", "command": "printf one", "wait_timeout_ms": 1000})
	after, _ := a.Execution.Get(k.ID, "node-a", "work")
	if !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatal("duplicate command renewed work lease")
	}

	clock.Add(int64(11 * time.Minute))
	call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "one", "command": "printf one"})
	reject("komari_command_run", map[string]any{"session_id": id, "command_id": "new-after-expiry", "command": "printf should-not-run"})
	call(t, sdk, "komari_execution_policy", map[string]any{"node_uuid": "node-a", "work_id": "work", "action": "set", "parallel": 1})
	call(t, sdk, "komari_command_run", map[string]any{"session_id": id, "command_id": "quiet", "command": "sleep 20", "wait_timeout_ms": 20})
	clock.Add(int64(11 * time.Minute))
	r := call(t, sdk, "komari_output_read", map[string]any{"session_id": id, "command_id": "quiet"})
	if r["command_state"] == "completed" {
		t.Fatal("lease expiry stopped still-running command")
	}
	if _, e := a.Execution.Get(k.ID, "node-a", "work"); e == nil {
		t.Fatal("output read renewed expired lease")
	}
	// Saturate ordinary HTTP calls directly; the control lane remains separately
	// bounded and cannot execute ordinary commands even when body contains their text.
	for range cap(a.slots) {
		a.slots <- struct{}{}
	}
	call(t, sdk, "komari_session_status", map[string]any{"session_id": id})
	call(t, sdk, "komari_command_interrupt", map[string]any{"session_id": id, "command_id": "quiet"})
	call(t, sdk, "komari_session_close", map[string]any{"session_id": id})
	req, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"komari_session_open","arguments":{"node_uuid":"node-a","work_id":"work"}}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatal("ordinary call bypassed saturated lane")
	}
	for range cap(a.slots) {
		<-a.slots
	}
}
