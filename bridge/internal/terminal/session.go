package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
	"github.com/gorilla/websocket"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var ErrBusy = errors.New("session has an unfinished or uncertain command; read, input, interrupt or close it")

type Command struct {
	ID            string     `json:"command_id"`
	Hash          string     `json:"command_hash"`
	State         string     `json:"command_state"`
	ExitCode      *int       `json:"exit_code"`
	Start         int64      `json:"start"`
	End           *int64     `json:"end,omitempty"`
	Uncertain     bool       `json:"execution_uncertain"`
	Created       time.Time  `json:"created_at"`
	Deadline      *time.Time `json:"execution_deadline,omitempty"`
	TimeoutMS     int        `json:"execution_timeout_ms,omitempty"`
	TimedOut      bool       `json:"timed_out"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FirstOutputAt *time.Time `json:"first_output_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}
type Record struct {
	Shell               ShellType           `json:"shell_type"`
	ShellExecutable     string              `json:"shell_executable,omitempty"`
	SessionName         string              `json:"session_name"`
	Maintenance         bool                `json:"maintenance"`
	RemoteRevision      uint64              `json:"remote_policy_revision"`
	RemoteGuard         bool                `json:"remote_execution_guard"`
	ID                  string              `json:"session_id"`
	Owner               string              `json:"owner"`
	Node                string              `json:"node_uuid"`
	Work                string              `json:"work_id"`
	RequestID           string              `json:"upstream_request_id"`
	Nonce               string              `json:"continuity_nonce"`
	PID                 string              `json:"shell_pid"`
	Generation          int                 `json:"generation"`
	State               string              `json:"connection_state"`
	Verified            bool                `json:"context_verified"`
	Lost                bool                `json:"context_lost"`
	Gap                 bool                `json:"output_gap"`
	LogTruncated        bool                `json:"output_log_truncated"`
	Created             time.Time           `json:"created_at"`
	LastCall            time.Time           `json:"last_tool_call"`
	LastData            time.Time           `json:"last_data"`
	LastAdminPong       time.Time           `json:"last_admin_pong"`
	LastConnectionError string              `json:"last_connection_error,omitempty"`
	Disconnected        *time.Time          `json:"disconnected_at,omitempty"`
	Commands            map[string]*Command `json:"commands"`
	Active              string              `json:"active_command_id,omitempty"`
	Base                int64               `json:"output_base"`
	End                 int64               `json:"output_end"`
}
type Session struct {
	mu    sync.Mutex
	op    sync.Mutex
	write sync.Mutex
	Record
	conn           *websocket.Conn
	changed        chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	buffer         []byte
	log            *os.File
	logBytes       int64
	manager        *Manager
	diagnosticLast diagnosticState
	persistFailed  bool
	resource       execution.Resource
	armID          string
	armError       string
	armDeadline    *time.Time
	awaitBootstrap bool
}
type Manager struct {
	mu           sync.Mutex
	shutdown     sync.Once
	sessions     map[string]*Session
	up           *upstream.Client
	policy       *access.Store
	dir          string
	ctx          context.Context
	cancel       context.CancelFunc
	idle         time.Duration
	maxSessions  int
	bufferLimit  int
	retention    time.Duration
	readyTimeout time.Duration
	execution    *execution.Store
	event        func(map[string]any)
}
type Result struct {
	Shell           ShellType  `json:"shell_type"`
	ShellExecutable string     `json:"shell_executable,omitempty"`
	Deadline        *time.Time `json:"execution_deadline,omitempty"`
	TimeoutMS       int        `json:"execution_timeout_ms,omitempty"`
	TimedOut        bool       `json:"timed_out"`
	SessionID       string     `json:"session_id"`
	CommandID       string     `json:"command_id,omitempty"`
	State           string     `json:"command_state,omitempty"`
	Connection      string     `json:"connection_state"`
	Output          string     `json:"output"`
	RawBase64       string     `json:"raw_base64"`
	Cursor          string     `json:"next_cursor"`
	More            bool       `json:"has_more"`
	ExitCode        *int       `json:"exit_code"`
	Gap             bool       `json:"output_gap"`
	Uncertain       bool       `json:"execution_uncertain"`
	Lost            bool       `json:"context_lost"`
	Verified        bool       `json:"context_verified"`
	Generation      int        `json:"generation"`
}

func New(ctx context.Context, up *upstream.Client, policy *access.Store, dir string) (*Manager, error) {
	return NewWithOptions(ctx, up, policy, dir, Options{})
}

type Options struct {
	Execution    *execution.Store
	ReadyTimeout time.Duration
	IdleTimeout  time.Duration
	Retention    time.Duration
	MaxSessions  int
	BufferBytes  int
	// Event receives bounded metadata only; never command/input/output bytes.
	Event func(map[string]any)
}

func NewWithOptions(ctx context.Context, up *upstream.Client, policy *access.Store, dir string, o Options) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{sessions: map[string]*Session{}, up: up, policy: policy, dir: dir, ctx: ctx, cancel: cancel, idle: 30 * time.Minute, maxSessions: 64, bufferLimit: 2 << 20, retention: 24 * time.Hour, readyTimeout: 60 * time.Second, event: o.Event, execution: o.Execution}
	if o.ReadyTimeout > 0 {
		m.readyTimeout = o.ReadyTimeout
	}
	if o.IdleTimeout > 0 {
		m.idle = o.IdleTimeout
	}
	if o.Retention > 0 {
		m.retention = o.Retention
	}
	if o.MaxSessions > 0 && o.MaxSessions <= 64 {
		m.maxSessions = o.MaxSessions
	}
	if o.BufferBytes >= 32768 && o.BufferBytes <= 8<<20 {
		m.bufferLimit = o.BufferBytes
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil {
			return nil, e
		}
		var record Record
		if e = json.Unmarshal(b, &record); e != nil {
			return nil, e
		}
		if !validID.MatchString(record.ID) || record.Commands == nil {
			return nil, errors.New("invalid persisted session")
		}
		if len(m.sessions) >= 128 {
			return nil, errors.New("persisted session limit exceeded")
		}
		s, e := m.newSession(record)
		if e != nil {
			return nil, e
		}
		s.Gap = true
		s.Verified = false
		s.buffer = nil
		s.Base = s.End
		if s.State != "closed" && s.State != "expired" && s.State != "context_lost" {
			s.State = "recovering"
			for _, c := range s.Commands {
				if c.State != "completed" {
					c.State = "uncertain"
					c.Uncertain = true
				}
			}
			now := time.Now()
			if s.Disconnected == nil {
				s.Disconnected = &now
			}
		}
		m.sessions[s.ID] = s
		if s.State == "recovering" {
			go s.connectLoop(false)
		}
	}
	go m.janitor()
	return m, nil
}
func (m *Manager) newSession(r Record) (*Session, error) {
	ctx, cancel := context.WithCancel(m.ctx)
	log, err := os.OpenFile(filepath.Join(m.dir, r.ID+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		cancel()
		return nil, err
	}
	stat, err := log.Stat()
	if err != nil {
		log.Close()
		cancel()
		return nil, err
	}
	return &Session{Record: r, ctx: ctx, cancel: cancel, changed: make(chan struct{}), log: log, logBytes: stat.Size(), manager: m}, nil
}
func (s *Session) signalLocked() {
	s.recordDiagnosticsLocked()
	close(s.changed)
	s.changed = make(chan struct{})
}
func (s *Session) saveLocked() error {
	err := access.AtomicJSON(filepath.Join(s.manager.dir, s.ID+".json"), s.Record)
	if failed := err != nil; failed != s.persistFailed {
		s.persistFailed = failed
		fields := map[string]any{"success": !failed}
		if failed {
			fields["error_code"] = "state_write_failed"
		}
		s.eventLocked("session_persistence", fields)
	}
	s.recordDiagnosticsLocked()
	return err
}
func (m *Manager) Open(owner, node, work string) (*Session, error) {
	return m.OpenManaged(owner, node, work, false)
}
func (m *Manager) OpenManaged(owner, node, work string, maintenance bool) (*Session, error) {
	return m.OpenNamed(owner, node, work, "main", maintenance)
}
func (m *Manager) OpenNamed(owner, node, work, name string, maintenance bool) (*Session, error) {
	if !validID.MatchString(name) {
		return nil, errors.New("invalid session_name")
	}
	if !upstream.ValidNode(node) || !validID.MatchString(work) {
		return nil, errors.New("valid node_uuid and work_id required")
	}
	if err := m.policy.Require(owner, node, "terminal"); err != nil {
		return nil, err
	}
	// Do not hold the global session registry while querying the panel: control
	// operations must remain available even during a slow resource request.
	var resource execution.Resource
	if m.execution != nil {
		m.mu.Lock()
		for _, existing := range m.sessions {
			existing.mu.Lock()
			same := existing.Owner == owner && existing.Node == node && existing.Work == work && (existing.SessionName == name || existing.SessionName == "" && name == "main") && existing.Maintenance == maintenance && alive(existing.Record)
			existing.mu.Unlock()
			if same {
				m.mu.Unlock()
				return existing, nil
			}
		}
		m.mu.Unlock()
		resource = m.up.Resource(m.ctx, node)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live := 0
	owned := 0
	for _, s := range m.sessions {
		s.mu.Lock()
		reusable := alive(s.Record)
		same := s.Owner == owner && s.Node == node && s.Work == work && (s.SessionName == name || s.SessionName == "" && name == "main") && s.Maintenance == maintenance
		if reusable {
			live++
			if s.Owner == owner {
				owned++
			}
		}
		s.mu.Unlock()
		if same && reusable {
			return s, nil
		}
	}
	if e := m.admitOpen(owner, node, work, maintenance, resource); e != nil {
		return nil, e
	}
	if live >= m.maxSessions || owned >= 64 || len(m.sessions) >= 128 {
		return nil, errors.New("session limit reached")
	}
	now := time.Now()
	record := Record{SessionName: name, Maintenance: maintenance, ID: access.RandomID(), Owner: owner, Node: node, Work: work, Nonce: access.RandomID(), Generation: 1, State: "connecting", Created: now, LastCall: now, Commands: map[string]*Command{}}
	s, err := m.newSession(record)
	if err != nil {
		return nil, err
	}
	if err = s.saveLocked(); err != nil {
		s.cancel()
		s.log.Close()
		return nil, err
	}
	m.sessions[s.ID] = s
	go s.connectLoop(true)
	return s, nil
}
func (m *Manager) get(owner, id string) (*Session, error) {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil || s.Owner != owner {
		return nil, access.ErrDenied
	}
	if err := m.policy.Require(owner, s.Node, "terminal"); err != nil {
		return nil, err
	}
	return s, nil
}

// SessionNode resolves audit attribution without extending session idle time.
func (m *Manager) SessionNode(owner, id string) string {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Owner != owner {
		return ""
	}
	return s.Node
}
func (m *Manager) Status(owner, id string) (Record, error) {
	s, err := m.get(owner, id)
	if err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastCall = time.Now()
	b, _ := json.Marshal(s.Record)
	var r Record
	json.Unmarshal(b, &r)
	r.Nonce = ""
	r.Owner = ""
	for _, c := range r.Commands {
		c.Hash = ""
	}
	return r, nil
}
func (m *Manager) List(owner string) []Record {
	m.mu.Lock()
	ids := []string{}
	for id, s := range m.sessions {
		if s.Owner == owner {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	var out []Record
	for _, id := range ids {
		if r, e := m.Status(owner, id); e == nil {
			out = append(out, r)
		}
	}
	return out
}

func (s *Session) send(kind int, data []byte) error {
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	conn := s.conn
	state := s.State
	s.mu.Unlock()
	if conn == nil || state == "closed" {
		return errors.New("terminal connection unavailable")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteMessage(kind, data)
}
func quote(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }
func marker(nonce, kind, rest string) string {
	return "command printf '\\036KMB:" + nonce + ":" + kind + ":" + rest + "\\037'"
}

func (s *Session) connectLoop(fresh bool) {
	s.mu.Lock()
	unknown := s.Shell == ""
	s.mu.Unlock()
	if unknown {
		osName, _ := s.manager.up.NodeOS(s.ctx, s.Node)
		s.mu.Lock()
		s.Shell = shellForOS(osName)
		s.mu.Unlock()
	}
	for attempt := 0; ; attempt++ {
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		id := s.RequestID
		disconnected := s.Disconnected
		s.mu.Unlock()
		if !fresh && disconnected != nil && time.Since(*disconnected) > 4*time.Minute+30*time.Second {
			s.fail("expired", true)
			return
		}
		conn, status, err := s.manager.up.TerminalConfigured(s.ctx, s.Node, id, s.remoteOptions())
		if err != nil {
			s.mu.Lock()
			s.eventLocked("connection_attempt_failed", map[string]any{"attempt": attempt + 1, "http_status": status, "error_code": connectionError(err)})
			s.mu.Unlock()
			if status == 401 || status == 403 {
				s.fail("authentication_failed", false)
				return
			}
			if status == 404 || (!fresh && id == "") {
				s.fail("expired", true)
				return
			}
			if fresh && attempt >= 2 {
				s.fail("connection_failed", false)
				return
			}
			if !s.backoff(attempt) {
				return
			}
			continue
		}
		conn.SetReadLimit(1 << 20)
		conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		conn.SetPongHandler(func(string) error {
			s.mu.Lock()
			s.LastAdminPong = time.Now()
			s.mu.Unlock()
			return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		})
		pingDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-pingDone:
					return
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
						conn.Close()
						return
					}
				}
			}
		}()
		// A successful upgrade is not PTY readiness. Bound the initial wait
		// separately from silence during an already running command.
		readyTimer := time.AfterFunc(s.manager.readyTimeout, func() {
			s.mu.Lock()
			waiting := s.conn == conn && s.State == "waiting_agent"
			if waiting {
				s.LastConnectionError = "terminal_ready_timeout"
			}
			s.mu.Unlock()
			if waiting {
				conn.Close()
			}
		})
		s.mu.Lock()
		s.conn = conn
		s.State = "waiting_agent"
		s.awaitBootstrap = fresh
		s.signalLocked()
		s.mu.Unlock()
		s.mu.Lock()
		prefix, delimiter := shellFraming(s.Shell, s.Nonce)
		s.mu.Unlock()
		p := parser{prefix: prefix, end: delimiter}
		requestReceived := false
		var readErr error
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				readErr = err
				break
			}
			conn.SetReadDeadline(time.Now().Add(75 * time.Second))
			if kind == websocket.TextMessage {
				if s.handleGuard(data, fresh) {
					s.mu.Lock()
					p.prefix, p.end = shellFraming(s.Shell, s.Nonce)
					s.mu.Unlock()
					continue
				}
				var control struct {
					ID string `json:"request_id"`
				}
				if json.Unmarshal(data, &control) == nil && control.ID != "" {
					s.mu.Lock()
					if id != "" && control.ID != id {
						s.mu.Unlock()
						s.fail("context_lost", true)
						readyTimer.Stop()
						close(pingDone)
						conn.Close()
						return
					}
					s.RequestID = control.ID
					s.saveLocked()
					s.mu.Unlock()
					if !requestReceived {
						requestReceived = true
						if fresh {
							if s.manager.execution != nil {
								s.sendHello()
							} else {
								s.bootstrap()
							}
						} else {
							if s.manager.execution != nil {
								s.sendHello()
							} else {
								s.resumeProbe()
							}
						}
					}
				}
				continue
			}
			if kind == websocket.BinaryMessage {
				s.consume(&p, data)
			}
		}
		readyTimer.Stop()
		close(pingDone)
		conn.Close()
		s.mu.Lock()
		if len(p.tail) > 0 {
			s.appendLocked(p.tail)
		}
		if s.conn == conn {
			s.conn = nil
		}
		if s.ctx.Err() != nil || s.State == "remote_admission_rejected" {
			s.mu.Unlock()
			return
		}
		now := time.Now()
		s.State = "reconnecting"
		if s.LastConnectionError != "terminal_ready_timeout" {
			s.LastConnectionError = connectionError(readErr)
		}
		s.Verified = false
		s.Gap = true
		if s.Disconnected == nil {
			s.Disconnected = &now
		}
		if c := s.Commands[s.Active]; c != nil && c.State != "completed" {
			c.State = "uncertain"
			c.Uncertain = true
		}
		s.saveLocked()
		s.signalLocked()
		s.mu.Unlock()
		fresh = false
		if !s.backoff(attempt) {
			return
		}
	}
}

func connectionError(err error) string {
	if e, ok := err.(net.Error); ok && e.Timeout() {
		return "transport_timeout"
	}
	var closed *websocket.CloseError
	if errors.As(err, &closed) {
		return fmt.Sprintf("websocket_close_%d", closed.Code)
	}
	return "transport_disconnected"
}
func (s *Session) backoff(attempt int) bool {
	d := min(15*time.Second, 500*time.Millisecond*time.Duration(1<<min(attempt, 5)))
	d += time.Duration(rand.IntN(250)) * time.Millisecond
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Session) bootstrap() {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	data := shellBootstrap(s.Shell, s.Nonce)
	windows := s.Shell == ShellPowerShell
	conn := s.conn
	s.mu.Unlock()
	if windows {
		if err := s.send(websocket.TextMessage, []byte(`{"type":"resize","cols":256,"rows":40}`)); err != nil {
			s.fail("bootstrap_uncertain", false)
			return
		}
		// ConPTY creation does not mean PowerShell has begun accepting input.
		// Only this idempotent initializer may be repeated before READY.
		go s.bootstrapWindows(conn, []byte(data))
		return
	}
	if err := s.send(websocket.BinaryMessage, []byte(data)); err != nil {
		s.fail("bootstrap_uncertain", false)
	}
}
func (s *Session) resumeProbe() {
	s.mu.Lock()
	idle := s.Active == ""
	data := shellResume(s.Shell, s.Nonce)
	windows := s.Shell == ShellPowerShell
	if !idle {
		s.State = "attached_context_unverified"
		s.signalLocked()
	}
	s.mu.Unlock()
	if !idle {
		return
	}
	s.op.Lock()
	defer s.op.Unlock()
	if windows {
		if err := s.send(websocket.TextMessage, []byte(`{"type":"resize","cols":256,"rows":40}`)); err != nil {
			s.fail("attached_context_unverified", false)
			return
		}
	}
	_ = s.send(websocket.BinaryMessage, []byte(data))
}
func (s *Session) fail(state string, lost bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
	s.Lost = lost
	s.Verified = false
	if lost {
		s.Generation++
	}
	if c := s.Commands[s.Active]; c != nil && c.State != "completed" {
		c.State = "uncertain"
		c.Uncertain = true
	}
	s.saveLocked()
	s.signalLocked()
}
func (s *Session) appendLocked(data []byte) {
	bufferLimit := s.manager.bufferLimit
	if s.manager.execution != nil {
		p, _ := s.manager.execution.Effective(s.Owner, s.Node, s.resource)
		bufferLimit = p.OutputBytes
	}
	if len(data) == 0 {
		return
	}
	s.buffer = append(s.buffer, data...)
	s.End += int64(len(data))
	if excess := len(s.buffer) - bufferLimit; excess > 0 {
		s.buffer = append([]byte(nil), s.buffer[excess:]...)
		s.Base += int64(excess)
		s.Gap = true
	}
	if s.logBytes+int64(len(data)) > int64(bufferLimit) {
		s.LogTruncated = true
	}
	if s.logBytes < int64(bufferLimit) {
		keep := min(len(data), bufferLimit-int(s.logBytes))
		n, err := s.log.Write(data[:keep])
		s.logBytes += int64(n)
		if err != nil {
			s.Gap = true
		}
	}
	s.LastData = time.Now()
	if c := s.Commands[s.Active]; c != nil && c.State == "running" && c.FirstOutputAt == nil {
		now := s.LastData
		c.FirstOutputAt = &now
	}
	s.signalLocked()
}
func (s *Session) consume(p *parser, data []byte) {
	probe := false
	s.mu.Lock()
	if s.ctx.Err() != nil || s.State == "closed" {
		s.mu.Unlock()
		return
	}
	p.feed(data, s.appendLocked, func(fields []string) {
		if len(fields) < 2 {
			return
		}
		switch fields[0] {
		case "READY":
			// Queued initializers can produce duplicate READY frames. Only a
			// fresh bootstrap may establish context, never a resumed connection
			// or an already admitted command.
			if !s.awaitBootstrap || s.State != "waiting_agent" || s.Active != "" {
				return
			}
			s.awaitBootstrap = false
			s.LastConnectionError = ""
			s.PID = fields[1]
			s.State = "ready"
			s.Verified = true
			s.Disconnected = nil
		case "BOOTERR":
			s.State = "bootstrap_failed"
			s.Verified = false
			s.LastConnectionError = "bootstrap_failed"
			if fields[1] == "shell" || fields[1] == "temp_directory" || fields[1] == "mkdir" {
				s.LastConnectionError = "bootstrap_" + fields[1]
			}
		case "RESUME":
			if len(fields) != 3 || fields[1] != s.Nonce || fields[2] != s.PID {
				s.State = "context_lost"
				s.Lost = true
				s.Verified = false
				s.Generation++
			} else {
				s.LastConnectionError = ""
				s.State = "ready"
				s.Verified = true
				s.Disconnected = nil
			}
		case "BEGIN":
			if c := s.Commands[fields[1]]; c != nil {
				c.State = "running"
				c.Start = s.End
				now := time.Now()
				c.StartedAt = &now
			}
		case "END":
			if len(fields) != 3 {
				return
			}
			c := s.Commands[fields[1]]
			rc, err := strconv.Atoi(fields[2])
			if c != nil && err == nil && validShellExit(s.Shell, rc) {
				c.State = "completed"
				c.ExitCode = &rc
				c.Uncertain = false
				end := s.End
				c.End = &end
				now := time.Now()
				c.EndedAt = &now
				if s.Active == c.ID {
					s.Active = ""
				}
				if !s.Verified {
					probe = true
				}
			}
		}
		s.saveLocked()
		s.signalLocked()
	})
	s.mu.Unlock()
	if probe {
		go s.resumeProbe()
	}
}

func (m *Manager) Run(ctx context.Context, owner, id, commandID, command string, wait, maxBytes int) (Result, error) {
	return m.RunTimed(ctx, owner, id, commandID, command, wait, maxBytes, 0)
}
func (m *Manager) RunTimed(ctx context.Context, owner, id, commandID, command string, wait, maxBytes, timeoutMS int) (Result, error) {
	if !validID.MatchString(commandID) || strings.TrimSpace(command) == "" || len(command) > 32768 || strings.ContainsRune(command, 0) {
		return Result{}, errors.New("invalid command_id or command text")
	}
	if wait < 0 || wait > 10000 || maxBytes < 256 || maxBytes > 131072 {
		return Result{}, errors.New("invalid wait/output bounds")
	}
	s, err := m.get(owner, id)
	if err != nil {
		return Result{}, err
	}
	hashBytes := sha256.Sum256([]byte(command))
	hash := hex.EncodeToString(hashBytes[:])
	s.op.Lock()
	s.mu.Lock()
	existing := s.Commands[commandID]
	if existing != nil {
		if existing.Hash != hash {
			s.mu.Unlock()
			s.op.Unlock()
			return Result{}, errors.New("command_id reused with different command")
		}
		s.mu.Unlock()
		s.op.Unlock()
		return m.waitCommand(ctx, s, commandID, wait, maxBytes)
	}
	if err = m.policy.Require(owner, s.Node, "terminal"); err != nil {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, err
	}
	if s.State != "ready" || !s.Verified || s.Lost {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, errors.New("shell is not ready or context is unverified")
	}
	if s.Active != "" {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, ErrBusy
	}
	if len(s.Commands) >= 1000 {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, errors.New("command history limit reached; open a new work session")
	}
	s.mu.Unlock()
	capacityErr := m.checkDispatchCapacity(s)
	s.mu.Lock()
	if capacityErr != nil {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, capacityErr
	}
	duration, e := m.dispatchPolicy(s, timeoutMS)
	if e != nil {
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, e
	}
	c := &Command{TimeoutMS: duration, ID: commandID, Hash: hash, State: "dispatched_unconfirmed", Start: s.End, Created: time.Now()}
	s.Commands[c.ID] = c
	s.Active = c.ID
	s.LastCall = time.Now()
	if err = s.saveLocked(); err != nil {
		delete(s.Commands, c.ID)
		s.Active = ""
		s.mu.Unlock()
		s.op.Unlock()
		return Result{}, err
	}
	s.mu.Unlock()
	if m.execution != nil {
		if e := s.arm(commandID, duration); e != nil {
			s.mu.Lock()
			c.State = "uncertain"
			c.Uncertain = true
			var rejected *guardRejected
			if errors.As(e, &rejected) {
				c.State = "rejected"
				c.Uncertain = false
				s.Active = ""
			}
			s.saveLocked()
			s.signalLocked()
			s.mu.Unlock()
			s.op.Unlock()
			return Result{}, e
		}
	}
	s.mu.Lock()
	lines := shellCommand(s.Shell, s.Nonce, commandID, command)
	s.mu.Unlock()
	for _, line := range lines {
		if err = s.send(websocket.BinaryMessage, []byte(line)); err != nil {
			break
		}
	}
	if err != nil {
		s.mu.Lock()
		c.State = "uncertain"
		c.Uncertain = true
		s.saveLocked()
		s.signalLocked()
		s.mu.Unlock()
	}
	if err == nil && m.execution != nil {
		m.execution.Renew(owner, s.Node, s.Work)
	}
	s.op.Unlock()
	return m.waitCommand(ctx, s, commandID, wait, maxBytes)
}
func (m *Manager) waitCommand(ctx context.Context, s *Session, id string, wait, maxBytes int) (Result, error) {
	timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
	defer timer.Stop()
	for {
		s.mu.Lock()
		c := s.Commands[id]
		if c == nil {
			s.mu.Unlock()
			return Result{}, errors.New("unknown command")
		}
		if c.State == "completed" || c.State == "rejected" || c.Uncertain || wait == 0 {
			r := s.resultLocked(c, c.Start, maxBytes)
			s.mu.Unlock()
			return r, nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
			continue
		case <-timer.C:
			s.mu.Lock()
			r := s.resultLocked(c, c.Start, maxBytes)
			s.mu.Unlock()
			return r, nil
		case <-ctx.Done():
			s.mu.Lock()
			r := s.resultLocked(c, c.Start, maxBytes)
			s.mu.Unlock()
			return r, nil
		}
	}
}
func (s *Session) resultLocked(c *Command, offset int64, maxBytes int) Result {
	r := Result{Shell: s.Shell, ShellExecutable: s.ShellExecutable, SessionID: s.ID, Connection: s.State, Gap: s.Gap || offset < s.Base, Lost: s.Lost, Verified: s.Verified, Generation: s.Generation}
	upper := s.End
	if c != nil {
		r.CommandID = c.ID
		r.State = c.State
		r.ExitCode = c.ExitCode
		r.Uncertain = c.Uncertain
		r.Deadline = c.Deadline
		r.TimeoutMS = c.TimeoutMS
		r.TimedOut = c.TimedOut
		if c.End != nil {
			upper = *c.End
		}
	}
	start := max(offset, s.Base)
	if start > upper {
		start = upper
	}
	take := min(int64(maxBytes), upper-start)
	rel := start - s.Base
	if rel >= 0 && rel+take <= int64(len(s.buffer)) {
		data := s.buffer[rel : rel+take]
		if take > 0 && !(c != nil && c.End != nil && start+take == *c.End) {
			for cut := max(0, len(data)-3); cut < len(data); cut++ {
				if utf8.RuneStart(data[cut]) && !utf8.FullRune(data[cut:]) {
					data = data[:cut]
					break
				}
			}
		}
		take = int64(len(data))
		r.Output = strings.ToValidUTF8(string(data), "�")
		r.RawBase64 = base64.StdEncoding.EncodeToString(data)
	}
	r.Cursor = fmt.Sprintf("%d:%d", s.Generation, start+take)
	r.More = start+take < upper
	return r
}
func (m *Manager) Read(ctx context.Context, owner, id, commandID, cursor string, maxBytes, wait int) (Result, error) {
	if maxBytes < 256 || maxBytes > 131072 || wait < 0 || wait > 10000 {
		return Result{}, errors.New("invalid read bounds")
	}
	s, err := m.get(owner, id)
	if err != nil {
		return Result{}, err
	}
	timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
	defer timer.Stop()
	for {
		s.mu.Lock()
		s.LastCall = time.Now()
		var c *Command
		if commandID != "" {
			c = s.Commands[commandID]
			if c == nil {
				s.mu.Unlock()
				return Result{}, errors.New("unknown command_id")
			}
		}
		offset := s.Base
		if c != nil {
			offset = c.Start
		}
		if cursor != "" {
			parts := strings.Split(cursor, ":")
			if len(parts) != 2 {
				s.mu.Unlock()
				return Result{}, errors.New("invalid cursor")
			}
			gen, e1 := strconv.Atoi(parts[0])
			off, e2 := strconv.ParseInt(parts[1], 10, 64)
			if e1 != nil || e2 != nil || off < 0 || gen != s.Generation || off > s.End {
				s.mu.Unlock()
				return Result{}, errors.New("cursor invalid for this generation")
			}
			offset = off
		}
		r := s.resultLocked(c, offset, maxBytes)
		if r.Output != "" || r.RawBase64 != "" || wait == 0 || r.Lost || s.State == "closed" || (c != nil && c.State == "completed") {
			s.mu.Unlock()
			return r, nil
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
			continue
		case <-timer.C:
			s.mu.Lock()
			r = s.resultLocked(c, offset, maxBytes)
			s.mu.Unlock()
			return r, nil
		case <-ctx.Done():
			return r, nil
		}
	}
}
func (m *Manager) Input(owner, id, commandID string, data []byte) error {
	return m.input(owner, id, commandID, data, false)
}
func (m *Manager) input(owner, id, commandID string, data []byte, allowUnverifiedInterrupt bool) error {
	s, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if len(data) > 32768 {
		return errors.New("input too large")
	}
	if err = m.policy.Require(owner, s.Node, "terminal"); err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	valid := s.Active != "" && s.Active == commandID
	state := s.State
	verified := s.Verified
	s.LastCall = time.Now()
	s.mu.Unlock()
	if !valid || !((state == "ready" && verified) || (allowUnverifiedInterrupt && state == "attached_context_unverified")) {
		return errors.New("input requires the current command_id and verified shell continuity")
	}
	e := s.send(websocket.BinaryMessage, data)
	if e == nil && !allowUnverifiedInterrupt && len(data) > 0 && !(len(data) == 1 && data[0] == 3) && m.execution != nil {
		m.execution.Renew(owner, s.Node, s.Work)
	}
	return e
}
func (m *Manager) Resize(owner, id string, cols, rows int) error {
	s, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if err = m.policy.Require(owner, s.Node, "terminal"); err != nil {
		return err
	}
	if cols < 20 || cols > 500 || rows < 5 || rows > 300 {
		return errors.New("invalid terminal size")
	}
	s.mu.Lock()
	windows := s.Shell == ShellPowerShell
	s.mu.Unlock()
	if windows && cols < 256 {
		return errors.New("managed PowerShell requires at least 256 columns to preserve ConPTY markers")
	}
	b, _ := json.Marshal(map[string]any{"type": "resize", "cols": cols, "rows": rows})
	return s.send(websocket.TextMessage, b)
}
func (m *Manager) Interrupt(owner, id, commandID string) error {
	if err := m.input(owner, id, commandID, []byte{3}, true); err != nil {
		return err
	}
	s, err := m.get(owner, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.Commands[commandID]; c != nil && c.State != "completed" {
		c.State = "interrupt_requested"
		c.Uncertain = true
		s.saveLocked()
		s.signalLocked()
	}
	return nil
}
func (m *Manager) Close(owner, id string) error {
	s, err := m.get(owner, id)
	if err != nil {
		return err
	}
	if err = m.policy.Require(owner, s.Node, "terminal"); err != nil {
		return err
	}
	return s.close(true)
}
func (s *Session) close(remote bool) error {
	return s.closeWithCondition(remote, false)
}
func (s *Session) closeWithCondition(remote, idleOnly bool) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	idleLimit := s.manager.idle
	if s.manager.execution != nil {
		p, _ := s.manager.execution.Effective(s.Owner, s.Node, s.resource)
		idleLimit = time.Duration(p.IdleSeconds) * time.Second
	}
	if idleOnly && (s.Active != "" || s.State != "ready" || time.Since(s.LastCall) <= idleLimit) {
		s.mu.Unlock()
		return nil
	}
	if s.State == "closed" {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if remote {
		_ = s.send(websocket.TextMessage, []byte(`{"type":"close"}`))
	}
	s.cancel()
	s.write.Lock()
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	s.State = "closed"
	s.Verified = false
	if c := s.Commands[s.Active]; c != nil && c.State != "completed" {
		c.State = "uncertain"
		c.Uncertain = true
	}
	s.saveLocked()
	s.signalLocked()
	s.log.Close()
	s.mu.Unlock()
	s.write.Unlock()
	return nil
}
func (m *Manager) Shutdown() {
	m.shutdown.Do(func() {
		m.cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.sessions {
			s.write.Lock()
			s.mu.Lock()
			if s.conn != nil {
				s.conn.Close()
				s.conn = nil
			}
			if s.State != "closed" {
				now := time.Now()
				s.Disconnected = &now
				s.Verified = false
				s.Gap = true
				if c := s.Commands[s.Active]; c != nil && c.State != "completed" {
					c.State = "uncertain"
					c.Uncertain = true
				}
				s.saveLocked()
			}
			s.log.Close()
			s.mu.Unlock()
			s.write.Unlock()
		}
	})
}
func (m *Manager) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		list := []*Session{}
		for id, s := range m.sessions {
			s.mu.Lock()
			dead := s.State == "unsupported_shell" || s.State == "remote_admission_rejected" || s.State == "closed" || s.State == "expired" || s.State == "context_lost" || s.State == "authentication_failed" || s.State == "connection_failed" || s.State == "bootstrap_failed" || s.State == "bootstrap_uncertain"
			prune := dead && time.Since(s.LastCall) > m.retention
			idleLimit := m.idle
			if m.execution != nil {
				p, _ := m.execution.Effective(s.Owner, s.Node, s.resource)
				idleLimit = time.Duration(p.IdleSeconds) * time.Second
			}
			idle := s.State == "ready" && s.Active == "" && time.Since(s.LastCall) > idleLimit
			s.mu.Unlock()
			if prune {
				s.close(false)
				os.Remove(filepath.Join(m.dir, id+".json"))
				os.Remove(filepath.Join(m.dir, id+".log"))
				delete(m.sessions, id)
			} else if idle {
				list = append(list, s)
			}
		}
		m.mu.Unlock()
		for _, s := range list {
			_ = s.closeWithCondition(true, true)
		}
	}
}
