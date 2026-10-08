package terminal

import "testing"

func TestDiagnosticEventsTrackTransitionsWithoutPerChunkNoise(t *testing.T) {
	events := []map[string]any{}
	manager := &Manager{event: func(v map[string]any) { events = append(events, v) }}
	s := &Session{manager: manager, Record: Record{ID: "session", Node: "node", Owner: "key-id", State: "waiting_agent", Generation: 1, Commands: map[string]*Command{}}}
	s.recordDiagnosticsLocked()
	s.recordDiagnosticsLocked()
	if len(events) != 1 {
		t.Fatal("unchanged state emitted duplicate events")
	}
	s.State = "ready"
	s.Verified = true
	s.recordDiagnosticsLocked()
	c := &Command{ID: "once", State: "dispatched_unconfirmed"}
	s.Commands[c.ID] = c
	s.Active = c.ID
	s.recordDiagnosticsLocked()
	c.State = "running"
	s.recordDiagnosticsLocked()
	s.recordDiagnosticsLocked()
	s.State = "reconnecting"
	s.Gap = true
	c.State = "uncertain"
	c.Uncertain = true
	s.recordDiagnosticsLocked()
	rc := 7
	c.State = "completed"
	c.Uncertain = false
	c.ExitCode = &rc
	s.Active = ""
	s.recordDiagnosticsLocked()
	last := events[len(events)-1]
	if last["event"] != "command_state" || last["execution_state"] != "completed" || *last["exit_code"].(*int) != 7 {
		t.Fatal("completion event lost when active ID cleared", last)
	}
	if len(events) != 7 {
		t.Fatalf("unexpected state event count %d", len(events))
	}
	for _, e := range events {
		for _, key := range []string{"command", "input", "output", "continuity_nonce", "Authorization"} {
			if _, ok := e[key]; ok {
				t.Fatal("diagnostic event includes secret payload field")
			}
		}
	}
}
