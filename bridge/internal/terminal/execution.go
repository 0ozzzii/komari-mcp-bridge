package terminal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/gorilla/websocket"
	"strings"
	"time"
)

func alive(r Record) bool {
	return r.State != "closed" && r.State != "expired" && r.State != "context_lost" && r.State != "remote_admission_rejected" && r.State != "unsupported_shell"
}

// Caller holds m.mu; each Session is counted even if its work lease expired.
func (m *Manager) admitOpen(owner, node, work string, maintenance bool, r execution.Resource) error {
	if m.execution == nil {
		return nil
	}
	p, keyHard := m.execution.Effective(owner, node, r)
	_, nodeHard := m.execution.Effective("", node, r)
	var lease execution.Lease
	var e error
	if maintenance {
		if !p.AllowMaintenance {
			return errors.New("maintenance_not_authorized")
		}
	} else {
		lease, e = m.execution.Get(owner, node, work)
		if e != nil {
			return e
		}
		if execution.LowMemory(r, p) {
			return errors.New("resource_pressure: new ordinary work paused")
		}
	}
	count, owned, working, maint := 0, 0, 0, 0
	for _, s := range m.sessions {
		s.mu.Lock()
		if s.Node == node && alive(s.Record) {
			if s.Maintenance {
				maint++
			} else {
				count++
				if s.Owner == owner {
					owned++
					if s.Work == work {
						working++
					}
				}
			}
		}
		s.mu.Unlock()
	}
	if maintenance {
		if maint >= 1 {
			return errors.New("maintenance_slot_busy")
		}
		return nil
	}
	if count >= nodeHard || owned >= keyHard || working >= min(lease.Parallel, keyHard) {
		return errors.New("execution_slots_busy: read, interrupt and close remain available")
	}
	return nil
}
func (s *Session) remoteOptions() string {
	if s.manager.execution == nil {
		return ""
	}
	p, revision := s.manager.execution.NodeSettings(s.Node, execution.Resource{})
	ownerSettings, _ := s.manager.execution.Effective(s.Owner, s.Node, execution.Resource{})
	b, _ := json.Marshal(map[string]any{"managed": true, "maintenance": s.Maintenance, "session_maintenance_seconds": ownerSettings.MaintenanceSeconds, "revision": revision, "max_parallel": p.MaxParallel, "max_timeout_seconds": p.MaxTimeoutSeconds, "default_timeout_seconds": p.DefaultTimeoutSeconds, "output_bytes": p.OutputBytes, "min_free_percent": p.MinFreePercent, "maintenance_seconds": p.MaintenanceSeconds, "force_close_on_timeout": p.ForceCloseOnTimeout})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Session) sendHello() {
	var options any
	raw, _ := base64.RawURLEncoding.DecodeString(s.remoteOptions())
	_ = json.Unmarshal(raw, &options)
	b, _ := json.Marshal(map[string]any{"type": "mcp_hello", "nonce": s.Nonce, "execution": options})
	if s.send(websocket.TextMessage, b) != nil {
		s.fail("guard_unavailable", false)
	}
}
func (s *Session) handleGuard(data []byte, fresh bool) bool {
	var v struct {
		Type      string             `json:"type"`
		OS        string             `json:"os"`
		Shell     string             `json:"shell"`
		Marker    string             `json:"marker_protocol"`
		Nonce     string             `json:"nonce"`
		Revision  uint64             `json:"revision"`
		Protocol  int                `json:"protocol"`
		CommandID string             `json:"command_id"`
		Error     string             `json:"error"`
		Deadline  *time.Time         `json:"deadline"`
		Resource  execution.Resource `json:"resource"`
	}
	if json.Unmarshal(data, &v) != nil || len(v.Type) < 4 || v.Type[:4] != "mcp_" {
		return false
	}
	if s.manager.execution == nil {
		return true
	}
	s.mu.Lock()
	switch v.Type {
	case "mcp_ready":
		if v.Protocol != 1 || v.Nonce != s.Nonce {
			s.mu.Unlock()
			s.fail("guard_unsupported", false)
			return true
		}
		kind := s.Shell
		if v.OS != "" {
			kind = shellForOS(v.OS)
		}
		if kind != "" {
			if kind == ShellPowerShell && (!strings.EqualFold(v.Shell, "powershell.exe") && !strings.EqualFold(v.Shell, "pwsh.exe") || v.Marker != "printable-v1") {
				conn := s.conn
				s.mu.Unlock()
				s.fail("unsupported_shell", false)
				if fresh {
					_ = s.send(websocket.TextMessage, []byte(`{"type":"close"}`))
				}
				s.cancel()
				if conn != nil {
					conn.Close()
				}
				return true
			}
			if !fresh && s.Shell != "" && s.Shell != kind {
				conn := s.conn
				s.mu.Unlock()
				s.fail("context_lost", true)
				s.cancel()
				if conn != nil {
					conn.Close()
				}
				return true
			}
			s.Shell = kind
			if kind == ShellPowerShell {
				s.ShellExecutable = strings.ToLower(v.Shell)
			}
		}
		s.RemoteGuard = true
		s.RemoteRevision = v.Revision
		s.resource = v.Resource
		s.mu.Unlock()
		if fresh {
			s.bootstrap()
		} else {
			s.resumeProbe()
		}
		return true
	case "mcp_config_ack":
		if v.Error == "" {
			s.RemoteRevision = v.Revision
		}
	case "mcp_arm_ack":
		if v.Resource.Fresh {
			s.resource = v.Resource
		}
		s.armID = v.CommandID
		s.armError = v.Error
		s.armDeadline = v.Deadline
		if c := s.Commands[v.CommandID]; c != nil {
			c.Deadline = v.Deadline
		}
	case "mcp_timeout":
		if c := s.Commands[v.CommandID]; c != nil && c.State != "completed" {
			c.TimedOut = true
			c.State = "timeout_unconfirmed"
			c.Uncertain = true
		}
	case "mcp_error":
		s.LastConnectionError = "remote_execution_guard_rejected"
		if s.State == "waiting_agent" && s.Active == "" {
			s.State = "remote_admission_rejected"
			if s.conn != nil {
				s.conn.Close()
			}
		}
	}
	s.saveLocked()
	s.signalLocked()
	s.mu.Unlock()
	return true
}

// Called while s.mu is held. Only NEW commands need a live lease; retries
// are handled before this method and do not renew or extend their deadline.
func (m *Manager) dispatchPolicy(s *Session, timeoutMS int) (int, error) {
	if m.execution == nil {
		return timeoutMS, nil
	}
	if !s.RemoteGuard {
		return 0, errors.New("remote execution guard unavailable; upgrade compatible probe first")
	}
	p, hard := m.execution.Effective(s.Owner, s.Node, s.resource)
	if s.Maintenance && !p.AllowMaintenance {
		return 0, errors.New("maintenance_not_authorized")
	}
	if !s.Maintenance {
		l, e := m.execution.Get(s.Owner, s.Node, s.Work)
		if e != nil {
			return 0, e
		}
		if l.Parallel > hard {
			return 0, errors.New("policy_required: administrator limit lowered; renegotiate")
		}
		if execution.LowMemory(s.resource, p) {
			return 0, errors.New("resource_pressure")
		}
	}
	if s.Maintenance {
		p.MaxTimeoutSeconds = min(p.MaxTimeoutSeconds, p.MaintenanceSeconds)
		p.DefaultTimeoutSeconds = min(p.DefaultTimeoutSeconds, p.MaxTimeoutSeconds)
	}
	if timeoutMS == 0 {
		timeoutMS = p.DefaultTimeoutSeconds * 1000
	}
	if timeoutMS < 1 || timeoutMS > p.MaxTimeoutSeconds*1000 {
		return 0, errors.New("execution_timeout_ms exceeds allowed maximum; long commands must specify duration before dispatch")
	}
	return timeoutMS, nil
}
func (s *Session) arm(id string, timeoutMS int) error {
	s.mu.Lock()
	s.armID = ""
	s.armError = ""
	s.armDeadline = nil
	resource := s.resource
	s.mu.Unlock()
	p, _ := s.manager.execution.Effective(s.Owner, s.Node, resource)
	b, _ := json.Marshal(map[string]any{"type": "mcp_arm", "command_id": id, "nonce": s.Nonce, "execution_timeout_ms": timeoutMS, "force_close_on_timeout": p.ForceCloseOnTimeout})
	if e := s.send(websocket.TextMessage, b); e != nil {
		return e
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.armID == id {
			e := s.armError
			deadline := s.armDeadline
			s.mu.Unlock()
			if e != "" || deadline == nil {
				return &guardRejected{reason: e}
			}
			return nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return errors.New("guard acknowledgement missing; command not replayed")
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
}

func (m *Manager) ExecutionPolicy() *execution.Store { return m.execution }

func (m *Manager) checkDispatchCapacity(target *Session) error {
	if m.execution == nil || target.Maintenance {
		return nil
	}
	target.mu.Lock()
	resource := target.resource
	target.mu.Unlock()
	l, e := m.execution.Get(target.Owner, target.Node, target.Work)
	if e != nil {
		return e
	}
	p, keyHard := m.execution.Effective(target.Owner, target.Node, resource)
	_, nodeHard := m.execution.Effective("", target.Node, resource)
	if execution.LowMemory(resource, p) {
		return errors.New("resource_pressure")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count, owned, work := 0, 0, 0
	for _, s := range m.sessions {
		s.mu.Lock()
		if s.Node == target.Node && alive(s.Record) && !s.Maintenance {
			count++
			if s.Owner == target.Owner {
				owned++
				if s.Work == target.Work {
					work++
				}
			}
		}
		s.mu.Unlock()
	}
	if count > nodeHard || owned > keyHard || work > min(l.Parallel, keyHard) {
		return errors.New("execution limit lowered; close excess idle sessions before new dispatch")
	}
	return nil
}

type guardRejected struct{ reason string }

func (e *guardRejected) Error() string {
	return "remote guard rejected command before dispatch: " + e.reason
}

// Saving config immediately clamps bridge admission. Existing connections receive
// node settings; status exposes acknowledgement revision rather than guessing.
func (m *Manager) RefreshPolicy() {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		s.mu.Lock()
		ready := s.RemoteGuard && alive(s.Record)
		s.mu.Unlock()
		if !ready {
			continue
		}
		var options any
		b, _ := base64.RawURLEncoding.DecodeString(s.remoteOptions())
		_ = json.Unmarshal(b, &options)
		data, _ := json.Marshal(map[string]any{"type": "mcp_configure", "execution": options})
		_ = s.send(websocket.TextMessage, data)
	}
}
