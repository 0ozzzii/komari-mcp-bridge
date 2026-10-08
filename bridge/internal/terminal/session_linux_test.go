//go:build linux

package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/testutil"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
)

func setup(t *testing.T) (*Manager, *testutil.Host, access.Key) {
	return setupWithEnv(t, nil)
}

func setupWithEnv(t *testing.T, env []string) (*Manager, *testutil.Host, access.Key) {
	t.Helper()
	h := testutil.NewHostWithEnv(t, env)
	policy, err := access.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := policy.Create("test", []string{"node-a"}, []string{"terminal", "file.read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy.SetEnabled(true)
	up, _ := upstream.New(h.Server.URL, testutil.AdminKey)
	m, err := New(context.Background(), up, policy, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	return m, h, key
}
func until(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for fixture state")
}
func ready(t *testing.T, m *Manager, key access.Key, work string) *Session {
	t.Helper()
	s, err := m.Open(key.ID, "node-a", work)
	if err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { r, e := m.Status(key.ID, s.ID); return e == nil && r.State == "ready" && r.Verified })
	return s
}
func run(t *testing.T, m *Manager, key access.Key, s *Session, id, command string) Result {
	t.Helper()
	r, err := m.Run(context.Background(), key.ID, s.ID, id, command, 3000, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "completed" {
		t.Fatalf("command did not complete: %+v", r)
	}
	return r
}
func TestPersistentShellAndCommandSemantics(t *testing.T) {
	m, h, key := setup(t)
	s := ready(t, m, key, "work")
	run(t, m, key, s, "cd", "cd /tmp")
	if r := run(t, m, key, s, "pwd", "pwd"); !strings.Contains(r.Output, "/tmp") {
		t.Fatalf("cwd lost: %q", r.Output)
	}
	run(t, m, key, s, "export", "export KOMARI_HANDOFF_TEST_VAR=hello")
	if r := run(t, m, key, s, "env", "printf '%s\\n' \"$KOMARI_HANDOFF_TEST_VAR\""); !strings.Contains(r.Output, "hello") {
		t.Fatalf("environment lost: %q", r.Output)
	}
	r := run(t, m, key, s, "nonzero", "(exit 7)")
	if r.ExitCode == nil || *r.ExitCode != 7 {
		t.Fatalf("incorrect exit code: %+v", r)
	}
	r = run(t, m, key, s, "multiline", "cat <<'EOF'\n中文 'quotes' \"double\" $literal\nEOF")
	if !strings.Contains(r.Output, "中文 'quotes' \"double\" $literal") {
		t.Fatalf("heredoc broken: %q", r.Output)
	}
	before := h.Inputs.Load()
	r = run(t, m, key, s, "nonzero", "(exit 7)")
	if h.Inputs.Load() != before {
		t.Fatal("duplicate command replayed")
	}
	if _, err := m.Run(context.Background(), key.ID, s.ID, "nonzero", "echo different", 0, 32768); err == nil {
		t.Fatal("conflicting command_id accepted")
	}
	again, _ := m.Open(key.ID, "node-a", "work")
	if again.ID != s.ID || h.Opens.Load() != 1 {
		t.Fatal("work session was not reused")
	}
	run(t, m, key, s, "function", "handoff_func() { printf function-kept; }")
	if v := run(t, m, key, s, "use-function", "handoff_func"); v.Output != "function-kept" {
		t.Fatal("function definition did not persist")
	}
	v := run(t, m, key, s, "non-utf8", "command printf '\\377\\000\\342'")
	raw, err := base64.StdEncoding.DecodeString(v.RawBase64)
	if err != nil || !bytes.Equal(raw, []byte{0xff, 0x00, 0xe2}) {
		t.Fatalf("non-UTF-8 terminal bytes lost: %v", raw)
	}
}
func TestStreamingWaitInputAndIsolation(t *testing.T) {
	m, _, key := setup(t)
	s := ready(t, m, key, "one")
	other := ready(t, m, key, "two")
	run(t, m, key, s, "var", "export ISOLATED=one")
	r := run(t, m, key, other, "var", "printf '%s' \"${ISOLATED-unset}\"")
	if strings.Contains(r.Output, "one") {
		t.Fatal("shell state leaked between work sessions")
	}
	r, err := m.Run(context.Background(), key.ID, s.ID, "slow", "printf first; sleep 0.3; printf last", 30, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if r.State == "completed" {
		t.Fatal("tool blocked until command ended")
	}
	if !strings.Contains(r.Output, "first") {
		t.Fatalf("first output unavailable: %+v", r)
	}
	cursor := r.Cursor
	var tail strings.Builder
	until(t, func() bool {
		next, e := m.Read(context.Background(), key.ID, s.ID, "slow", cursor, 32768, 100)
		if e != nil {
			t.Fatal(e)
		}
		cursor = next.Cursor
		tail.WriteString(next.Output)
		return next.State == "completed"
	})
	if !strings.Contains(tail.String(), "last") {
		t.Fatal("stream tail missing")
	}
	r, err = m.Run(context.Background(), key.ID, s.ID, "quiet", "sleep 0.2", 20, 32768)
	if err != nil || r.State == "completed" {
		t.Fatalf("silence treated as completion: %+v %v", r, err)
	}
	until(t, func() bool {
		v, e := m.Read(context.Background(), key.ID, s.ID, "quiet", "", 32768, 100)
		return e == nil && v.State == "completed"
	})
	r, err = m.Run(context.Background(), key.ID, s.ID, "input", "IFS= read -r ANSWER; printf 'answer:%s' \"$ANSWER\"", 20, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Input(key.ID, s.ID, "wrong", []byte("hello\n")); err == nil {
		t.Fatal("input to wrong command accepted")
	}
	if err = m.Input(key.ID, s.ID, "input", []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool {
		v, e := m.Read(context.Background(), key.ID, s.ID, "input", "", 32768, 100)
		return e == nil && v.State == "completed" && strings.Contains(v.Output, "answer:hello")
	})
	if _, err = m.Status("another-owner", s.ID); err == nil {
		t.Fatal("cross-owner access accepted")
	}
}
func TestReconnectContinuityAndNoReplay(t *testing.T) {
	m, h, key := setup(t)
	s := ready(t, m, key, "reconnect")
	run(t, m, key, s, "set", "cd /tmp; export KEEP=yes")
	r, _ := m.Status(key.ID, s.ID)
	before := h.Opens.Load()
	h.Drop(r.RequestID)
	until(t, func() bool { v, _ := m.Status(key.ID, s.ID); return v.State == "ready" && v.Verified && v.Gap })
	if h.Opens.Load() != before {
		t.Fatal("reconnect created a new PTY")
	}
	if v := run(t, m, key, s, "get", "printf '%s:' \"$KEEP\"; pwd"); !strings.Contains(v.Output, "yes:/tmp") {
		t.Fatalf("context not preserved: %q", v.Output)
	}
	if err := h.Replace(r.RequestID); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool {
		v, _ := m.Status(key.ID, s.ID)
		return v.Lost && v.State == "context_lost" && v.Generation == 2
	})
	if _, err := m.Run(context.Background(), key.ID, s.ID, "never", "printf wrong", 0, 32768); err == nil {
		t.Fatal("dispatched into lost context")
	}
}
func TestUTF8CursorAndOutputLimit(t *testing.T) {
	m, _, key := setup(t)
	s := ready(t, m, key, "utf8")
	r := run(t, m, key, s, "big", "i=0; while [ \"$i\" -lt 700 ]; do printf '中文'; i=$((i+1)); done")
	r, err := m.Read(context.Background(), key.ID, s.ID, "big", "", 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var raw []byte
	for {
		if !utf8.ValidString(r.Output) || strings.Contains(r.Output, "�") {
			t.Fatalf("split Unicode corrupted: %q", r.Output)
		}
		text.WriteString(r.Output)
		b, e := base64.StdEncoding.DecodeString(r.RawBase64)
		if e != nil {
			t.Fatal(e)
		}
		raw = append(raw, b...)
		if !r.More {
			break
		}
		r, err = m.Read(context.Background(), key.ID, s.ID, "big", r.Cursor, 256, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected := strings.Repeat("中文", 700)
	if text.String() != expected || !bytes.Equal(raw, []byte(expected)) {
		t.Fatalf("cursor lost output: %d vs %d", len(raw), len(expected))
	}
}
func TestParserFragmentationEchoAndCoalescing(t *testing.T) {
	prefix := []byte("\x1eKMB:random:")
	p := parser{prefix: prefix}
	var out []byte
	var events [][]string
	input := []byte("printf '\\036KMB:random:END:id:0\\037'\n中文\x1eKMB:random:BEGIN:id\x1fhello\x1eKMB:random:END:id:7\x1ftail")
	for _, b := range input {
		p.feed([]byte{b}, func(v []byte) { out = append(out, v...) }, func(v []string) { events = append(events, v) })
	}
	out = append(out, p.tail...)
	if len(events) != 2 || events[1][2] != "7" {
		t.Fatalf("markers incorrectly parsed: %v", events)
	}
	if !strings.Contains(string(out), "printf '\\036") || !strings.Contains(string(out), "中文hellotail") {
		t.Fatalf("echo or output damaged: %q", out)
	}
}

func TestRestartAndNetworkLossNeverReplayAnUncertainCommand(t *testing.T) {
	m, h, key := setup(t)
	s := ready(t, m, key, "restart")
	file := filepath.Join(t.TempDir(), "side-effect")
	command := "printf x >> " + quote(file) + "; sleep 0.4; printf finished"
	r, err := m.Run(context.Background(), key.ID, s.ID, "once", command, 25, 32768)
	if err != nil || r.State == "completed" {
		t.Fatalf("initial long command: %+v %v", r, err)
	}
	original, _ := m.Status(key.ID, s.ID)
	m.Shutdown()
	restored, err := New(context.Background(), m.up, m.policy, m.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Shutdown()
	until(t, func() bool {
		v, e := restored.Status(key.ID, s.ID)
		return e == nil && (v.State == "attached_context_unverified" || v.State == "ready")
	})
	r, err = restored.Run(context.Background(), key.ID, s.ID, "once", command, 25, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Gap {
		t.Fatal("restart claimed complete output history")
	}
	if r.State != "completed" && !r.Uncertain {
		t.Fatal("restart treated unfinished execution as certain")
	}
	time.Sleep(500 * time.Millisecond)
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "x" {
		t.Fatalf("side effect replayed after restart: %q %v", data, err)
	}
	if h.Opens.Load() != 1 {
		t.Fatal("metadata restoration created an extra PTY")
	}
	v, _ := restored.Status(key.ID, s.ID)
	if v.RequestID != original.RequestID {
		t.Fatal("upstream correlation id lost")
	}
}
func TestOverflowRetentionExpiryAndGeneration(t *testing.T) {
	m, h, key := setup(t)
	m.bufferLimit = 32768
	s := ready(t, m, key, "bounds")
	r := run(t, m, key, s, "large", "i=0; while [ \"$i\" -lt 20000 ]; do printf abc; i=$((i+1)); done")
	if !r.Gap {
		t.Fatal("ring overflow was hidden")
	}
	r, err := m.Read(context.Background(), key.ID, s.ID, "large", "1:0", 32768, 0)
	if err != nil || !r.Gap || len(r.Output) > 32768 {
		t.Fatalf("invalid overflow response: %+v %v", r, err)
	}
	s.mu.Lock()
	requestID := s.RequestID
	expired := time.Now().Add(-5 * time.Minute)
	s.Disconnected = &expired
	s.mu.Unlock()
	h.Drop(requestID)
	until(t, func() bool {
		v, _ := m.Status(key.ID, s.ID)
		return v.State == "expired" && v.Lost && v.Generation == 2
	})
	if _, err = m.Read(context.Background(), key.ID, s.ID, "large", "1:0", 32768, 0); err == nil {
		t.Fatal("old generation cursor accepted")
	}
}
func TestInterruptAndCloseDoNotInventSuccess(t *testing.T) {
	m, _, key := setup(t)
	s := ready(t, m, key, "interrupt")
	r, err := m.Run(context.Background(), key.ID, s.ID, "sleep", "sleep 5", 20, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if r.State == "completed" {
		t.Fatal("unexpected early completion")
	}
	if err = m.Interrupt(key.ID, s.ID, "sleep"); err != nil {
		t.Fatal(err)
	}
	r, err = m.Read(context.Background(), key.ID, s.ID, "sleep", "", 32768, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != nil && *r.ExitCode == 0 {
		t.Fatal("interrupt reported successful completion without evidence")
	}
	if r.ExitCode == nil && !r.Uncertain {
		t.Fatal("unknown interrupt outcome not recorded")
	}
	if err = m.Close(key.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := m.Status(key.ID, s.ID)
	if status.State != "closed" || status.Verified {
		t.Fatal("closed bridge kept live shell claim")
	}
}

// A side effect can finish remotely while its END frame is discarded. The
// bridge must retain uncertainty and must not replay, or start another command.
func TestLostCompletionNeverReplaysOrAllowsNextCommand(t *testing.T) {
	m, h, key := setup(t)
	s := ready(t, m, key, "lost-end")
	file := filepath.Join(t.TempDir(), "side-effect")
	command := "printf x >> " + quote(file) + "; sleep 0.3; printf finished"
	result, err := m.Run(context.Background(), key.ID, s.ID, "once", command, 25, 32768)
	if err != nil || result.State == "completed" {
		t.Fatalf("initial dispatch %+v %v", result, err)
	}
	until(t, func() bool { r, _ := m.Status(key.ID, s.ID); return r.Commands["once"].State == "running" })
	r, _ := m.Status(key.ID, s.ID)
	h.Drop(r.RequestID)
	until(t, func() bool { r, _ := m.Status(key.ID, s.ID); return r.State == "attached_context_unverified" })
	result, err = m.Run(context.Background(), key.ID, s.ID, "once", command, 25, 32768)
	if err != nil || !result.Uncertain || !result.Gap || result.ExitCode != nil {
		t.Fatalf("lost END fabricated certainty %+v %v", result, err)
	}
	if _, err = m.Run(context.Background(), key.ID, s.ID, "new-command", "printf should-not-run", 25, 32768); err == nil {
		t.Fatal("new command accepted while previous result is unknown")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "x" {
		t.Fatalf("command replayed or never executed %q %v", data, err)
	}
}

func TestConfiguredIdleTimeoutIsUsedDuringFinalCloseCheck(t *testing.T) {
	m, _, key := setup(t)
	policies, e := execution.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	p := execution.Defaults()
	p.IdleSeconds = 60
	if e = policies.Update("node", "node-a", &p); e != nil {
		t.Fatal(e)
	}
	// Install the policy before creating a session; the janitor has not ticked.
	m.execution = policies
	if _, e = policies.Set(key.ID, "node-a", "configured-idle", 1, execution.Resource{MemoryTotal: 2 << 30, Fresh: true}); e != nil {
		t.Fatal(e)
	}
	s := ready(t, m, key, "configured-idle")
	s.mu.Lock()
	s.LastCall = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()
	if e = s.closeWithCondition(true, true); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "closed" {
		t.Fatal("30-minute global timeout incorrectly overrode 1-minute device policy")
	}
}
