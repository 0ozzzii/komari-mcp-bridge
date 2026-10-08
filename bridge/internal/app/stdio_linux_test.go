//go:build linux

package app

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioAdapterExitDoesNotCloseDaemonTerminal(t *testing.T) {
	binary := os.Getenv("KOMARI_MCP_TEST_BINARY")
	if binary == "" {
		t.Skip("build the daemon and set KOMARI_MCP_TEST_BINARY for stdio smoke test")
	}
	binary, _ = filepath.Abs(binary)
	host := testutil.NewHost(t)
	a, err := New(context.Background(), Config{BaseURL: host.Server.URL, APIKey: testutil.AdminKey, ControlToken: strings.Repeat("stdio-control", 3), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	key, token, _ := a.Policy.Create("stdio", []string{"node-a"}, []string{"terminal"}, nil)
	a.Policy.SetEnabled(true)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	connectStdio := func() *mcp.ClientSession {
		command := exec.Command(binary, "stdio")
		command.Env = append(os.Environ(), "BRIDGE_MCP_URL="+server.URL+"/mcp", "KOMARI_MCP_CALLER_KEY="+token)
		client := mcp.NewClient(&mcp.Implementation{Name: "stdio-fixture", Version: "1"}, nil)
		session, e := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	first := connectStdio()
	call(t, first, "komari_execution_policy", map[string]any{"action": "set", "parallel": 1, "node_uuid": "node-a", "work_id": "stdio-work"})
	opened := call(t, first, "komari_session_open", map[string]any{"node_uuid": "node-a", "work_id": "stdio-work"})
	id := opened["session_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := a.Terminal.Status(key.ID, id)
		if status.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stdio PTY not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call(t, first, "komari_command_run", map[string]any{"session_id": id, "command_id": "set", "command": "cd /tmp; export STDIO_KEEP=alive", "wait_timeout_ms": 1000})
	first.Close()
	second := connectStdio()
	r := call(t, second, "komari_command_run", map[string]any{"session_id": id, "command_id": "get", "command": "printf '%s:' \"$STDIO_KEEP\"; pwd", "wait_timeout_ms": 1000})
	if !strings.Contains(r["output"].(string), "alive:/tmp") || host.Opens.Load() != 1 {
		t.Fatal("stdio process lifecycle destroyed remote context")
	}
}
