package terminal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
)

func TestWindowsRejectsOldGuardBeforeBootstrapOrCommand(t *testing.T) {
	for _, meta := range []map[string]any{
		{}, // Old protocol-1 probe has no OS, shell or framing fields.
		{"os": "windows", "shell": "powershell.exe", "marker_protocol": "c0-v1"},
		{"os": "windows", "shell": "cmd.exe", "marker_protocol": "printable-v1"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		m := &Manager{dir: t.TempDir(), execution: &execution.Store{}}
		s := &Session{manager: m, ctx: ctx, cancel: cancel, changed: make(chan struct{}), Record: Record{ID: "test", Shell: ShellPowerShell, Nonce: "nonce", State: "waiting_agent", Commands: map[string]*Command{}}}
		meta["type"] = "mcp_ready"
		meta["protocol"] = 1
		meta["nonce"] = "nonce"
		b, _ := json.Marshal(meta)
		if !s.handleGuard(b, true) || s.State != "unsupported_shell" || s.Verified || len(s.Commands) != 0 || ctx.Err() == nil {
			t.Fatal("incompatible probe accepted or bootstrapped", meta, s.Record)
		}
		cancel()
	}
}

func TestWindowsActualShellIsReturnedAndAudited(t *testing.T) {
	for _, executable := range []string{"powershell.exe", "pwsh.exe"} {
		ctx, cancel := context.WithCancel(context.Background())
		events := []map[string]any{}
		m := &Manager{dir: t.TempDir(), execution: &execution.Store{}, event: func(v map[string]any) { events = append(events, v) }}
		s := &Session{manager: m, ctx: ctx, cancel: cancel, changed: make(chan struct{}), Record: Record{ID: "test", Shell: ShellPowerShell, Nonce: "nonce", State: "waiting_agent", Active: "running", Commands: map[string]*Command{}}}
		b, _ := json.Marshal(map[string]any{"type": "mcp_ready", "protocol": 1, "nonce": "nonce", "os": "windows", "shell": executable, "marker_protocol": "printable-v1"})
		if !s.handleGuard(b, false) || !s.RemoteGuard || s.Verified || s.State != "attached_context_unverified" {
			t.Fatal("guard metadata was mistaken for shell readiness", s.Record)
		}
		s.mu.Lock()
		s.saveLocked()
		result := s.resultLocked(nil, 0, 32768)
		s.mu.Unlock()
		if s.ShellExecutable != executable || result.ShellExecutable != executable || len(events) == 0 || events[len(events)-1]["shell_executable"] != executable {
			t.Fatal("actual shell lost from metadata/result/audit", s.Record, result, events)
		}
		cancel()
	}
}
