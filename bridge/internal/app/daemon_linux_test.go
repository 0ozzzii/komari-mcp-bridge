//go:build linux

package app

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/testutil"
)

func TestStandaloneDaemonStartsAndExecutesThroughMCP(t *testing.T) {
	binary := os.Getenv("KOMARI_MCP_TEST_BINARY")
	if binary == "" {
		t.Skip("set KOMARI_MCP_TEST_BINARY to the built daemon to run standalone smoke test")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	h := testutil.NewHost(t)
	port := func() string {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		address := listener.Addr().String()
		listener.Close()
		return address
	}
	mcpAddress, controlAddress := port(), port()
	control := strings.Repeat("fixture-control", 3)
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "KOMARI_BASE_URL="+h.Server.URL, "KOMARI_API_KEY="+testutil.AdminKey, "BRIDGE_CONTROL_TOKEN="+control, "BRIDGE_BIND_ADDRESS="+mcpAddress, "BRIDGE_CONTROL_ADDRESS="+controlAddress, "BRIDGE_STATE_DIR="+t.TempDir())
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, e := http.Get("http://" + mcpAddress + "/healthz")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("standalone daemon did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	controlCall := func(action string, value any) map[string]any {
		data, _ := json.Marshal(value)
		req, _ := http.NewRequest("POST", "http://"+controlAddress+"/control/"+action, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+control)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("control failed: %d", resp.StatusCode)
		}
		var result map[string]any
		if json.NewDecoder(resp.Body).Decode(&result) != nil {
			t.Fatal("control returned invalid JSON")
		}
		return result
	}
	created := controlCall("keys/create", map[string]any{"name": "smoke", "nodes": []string{"node-a"}, "permissions": []string{"terminal"}})
	controlCall("enabled", map[string]any{"enabled": true})
	client := connect(t, "http://"+mcpAddress, created["token"].(string))
	call(t, client, "komari_execution_policy", map[string]any{"action": "set", "parallel": 1, "node_uuid": "node-a", "work_id": "daemon-smoke"})
	opened := call(t, client, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "daemon-smoke"})
	id := opened["session_id"].(string)
	deadline = time.Now().Add(5 * time.Second)
	for {
		status := call(t, client, "komari_session_status", map[string]any{"session_id": id})
		if status["connection_state"] == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("standalone daemon PTY never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result := call(t, client, "komari_command_run", map[string]any{"session_id": id, "command_id": "daemon-command", "command": "printf daemon-ok", "wait_timeout_ms": 1000})
	if result["command_state"] != "completed" || result["output"] != "daemon-ok" {
		t.Fatal("daemon did not execute through MCP")
	}
}
