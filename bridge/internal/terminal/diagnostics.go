package terminal

// Changes are recorded even when no AI client is polling. Do not emit one
// event per PTY chunk/Pong, and never pass error strings containing URLs.
type diagnosticState struct {
	state, active, commandState, errorCode    string
	generation                                int
	verified, lost, gap, truncated, uncertain bool
}

func (s *Session) eventLocked(event string, fields map[string]any) {
	if s.manager.event == nil {
		return
	}
	fields["event"] = event
	fields["session_id"] = s.ID
	fields["node_uuid"] = s.Node
	fields["key_id"] = s.Owner
	fields["generation"] = s.Generation
	fields["shell_type"] = s.Shell
	if s.ShellExecutable != "" {
		fields["shell_executable"] = s.ShellExecutable
	}
	s.manager.event(fields)
}

func (s *Session) recordDiagnosticsLocked() {
	if s.manager.event == nil {
		return
	}
	current := diagnosticState{state: s.State, active: s.Active, errorCode: s.LastConnectionError, generation: s.Generation, verified: s.Verified, lost: s.Lost, gap: s.Gap, truncated: s.LogTruncated}
	if command := s.Commands[s.Active]; command != nil {
		current.commandState, current.uncertain = command.State, command.Uncertain
	}
	old := s.diagnosticLast
	if current == old {
		return
	}
	if current.state != old.state || current.errorCode != old.errorCode || current.generation != old.generation || current.verified != old.verified || current.lost != old.lost || current.gap != old.gap || current.truncated != old.truncated {
		s.eventLocked("session_state", map[string]any{"connection_state": s.State, "error_code": s.LastConnectionError, "context_verified": s.Verified, "context_lost": s.Lost, "output_gap": s.Gap, "output_log_truncated": s.LogTruncated})
	}
	command := s.Commands[current.active]
	if command == nil && old.active != "" {
		command = s.Commands[old.active]
	}
	if command != nil && (current.active != old.active || current.commandState != old.commandState || current.uncertain != old.uncertain) {
		s.eventLocked("command_state", map[string]any{"command_id": command.ID, "execution_state": command.State, "execution_uncertain": command.Uncertain, "exit_code": command.ExitCode, "timed_out": command.TimedOut, "execution_timeout_ms": command.TimeoutMS, "execution_deadline": command.Deadline})
	}
	s.diagnosticLast = current
}
