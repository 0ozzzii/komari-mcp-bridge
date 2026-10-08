//go:build linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authTransport struct{ token string }

func (a authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(copy)
}
func connect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "isolated-test", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: authTransport{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	result, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s: %v", name, result.Content)
	}
	b, _ := json.Marshal(result.StructuredContent)
	var value map[string]any
	if err = json.Unmarshal(b, &value); err != nil {
		t.Fatalf("non-object result for %s: %s", name, b)
	}
	return value
}
func TestMCPCallsReusePTYAcrossClientReconnectAndProtectKeys(t *testing.T) {
	h := testutil.NewHost(t)
	a, err := New(context.Background(), Config{BaseURL: h.Server.URL, APIKey: testutil.AdminKey, ControlToken: strings.Repeat("c", 32), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	key, token, err := a.Policy.Create("MCP", []string{"node-a"}, []string{"terminal", "file.read", "file.write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Policy.SetEnabled(true)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	first := connect(t, server.URL, token)
	list, err := first.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 15 {
		t.Fatalf("tool count %d", len(list.Tools))
	}
	result, err := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "komari_nodes_list", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("nodes: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("DO-NOT-EXPOSE")) || bytes.Contains(encoded, []byte("node-b")) {
		t.Fatal("node list leaked credentials or excluded node")
	}
	call(t, first, "komari_execution_policy", map[string]any{"action": "set", "parallel": 1, "node_uuid": "node-a", "work_id": "same-work"})
	opened := call(t, first, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "same-work"})
	id := opened["session_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		status := call(t, first, "komari_session_status", map[string]any{"session_id": id})
		if status["connection_state"] == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := func(s *mcp.ClientSession, commandID, command string) map[string]any {
		return call(t, s, "komari_command_run", map[string]any{"session_id": id, "command_id": commandID, "command": command, "wait_timeout_ms": 1000})
	}
	r := run(first, "set", "cd /tmp; export MCP_KEEP=hello")
	if r["command_state"] != "completed" {
		t.Fatal(r)
	}
	first.Close()
	second := connect(t, server.URL, token)
	r = run(second, "get", "printf '%s:' \"$MCP_KEEP\"; pwd")
	if !strings.Contains(r["output"].(string), "hello:/tmp") || h.Opens.Load() != 1 {
		t.Fatalf("client reconnect recreated shell: %+v", r)
	}
	args := map[string]any{"node_uuid": "node-a", "action": "delete", "path": "/isolated-fixture-not-a-real-file", "operation_id": "same-delete"}
	call(t, second, "komari_filesystem", args)
	call(t, second, "komari_filesystem", args)
	if h.FileMutations.Load() != 1 {
		t.Fatal("mutating file tool replayed")
	}
	a.Policy.Update(key.ID, []string{"node-a"}, []string{"terminal"}, false)
	result, err = second.CallTool(context.Background(), &mcp.CallToolParams{Name: "komari_sessions_list", Arguments: map[string]any{}})
	if err == nil && !result.IsError {
		t.Fatal("revoked key continued using existing client")
	}
	req, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("anonymous request accepted")
	}
	data, err := os.ReadFile(a.audit.path)
	if err != nil || !bytes.Contains(data, []byte(`"event":"command_state"`)) || !bytes.Contains(data, []byte(`"execution_state":"completed"`)) || !bytes.Contains(data, []byte(`"duration_ms"`)) {
		t.Fatalf("real tool/session diagnostic records missing: %v", err)
	}
	for _, secret := range []string{token, testutil.AdminKey, "export MCP_KEEP=hello"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("audit contains credential or command body")
		}
	}
}
func TestControlSeparationAndGlobalSwitch(t *testing.T) {
	h := testutil.NewHost(t)
	control := strings.Repeat("x", 32)
	a, err := New(context.Background(), Config{BaseURL: h.Server.URL, APIKey: testutil.AdminKey, ControlToken: control, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, token, _ := a.Policy.Create("MCP", []string{"node-a"}, []string{"terminal"}, nil)
	server := httptest.NewServer(a.ControlHandler())
	defer server.Close()
	request := func(secret, method, path, body, origin string) *http.Response {
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return resp
	}
	resp := request(token, "GET", "/control/keys", "", "")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("MCP key controlled administrator settings")
	}
	resp = request(control, "POST", "/control/enabled", `{"enabled":true}`, "https://evil.example")
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin control accepted")
	}
	resp = request(control, "POST", "/control/enabled", `{"enabled":true}`, "")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("control switch failed")
	}
	resp = request(control, "GET", "/control/keys", "", "")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(data, []byte(token)) || bytes.Contains(data, []byte("hash")) {
		t.Fatal("key secret leaked")
	}
}
