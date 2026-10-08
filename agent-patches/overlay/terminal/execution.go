package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari-agent/executionguard"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type commandDeadline struct {
	mu        sync.Mutex
	id, nonce string
	timer     *time.Timer
	tail      []byte
	expired   bool
	force     bool
	closing   bool
}

func (s *terminalSession) replyGuard(value any) {
	b, _ := json.Marshal(value)
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return
	}
	s.outputWrite.Lock()
	defer s.outputWrite.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteWait))
	if conn.WriteMessage(websocket.TextMessage, b) != nil {
		s.detach(conn)
	}
}
func (s *terminalSession) guardInput(conn *websocket.Conn, data []byte) bool {
	var v struct {
		Type      string                  `json:"type"`
		ID        string                  `json:"command_id"`
		Nonce     string                  `json:"nonce"`
		TimeoutMS int                     `json:"execution_timeout_ms"`
		Force     bool                    `json:"force_close_on_timeout"`
		Execution *executionguard.Options `json:"execution"`
	}
	if json.Unmarshal(data, &v) != nil || !strings.HasPrefix(v.Type, "mcp_") {
		return false
	}
	s.mu.Lock()
	owned := s.conn == conn && !s.removed && s.options.Managed
	s.mu.Unlock()
	if !owned {
		return true
	}
	switch v.Type {
	case "mcp_hello", "mcp_configure":
		if v.Execution != nil {
			if e := executionguard.Global.Configure(*v.Execution); e != nil {
				s.replyGuard(map[string]any{"type": "mcp_error", "error": "invalid remote policy"})
				return true
			}
		}
		typ := "mcp_ready"
		if v.Type == "mcp_configure" {
			typ = "mcp_config_ack"
		}
		framing := "c0-v1"
		if runtime.GOOS == "windows" {
			framing = "printable-v1"
		}
		s.replyGuard(map[string]any{"type": typ, "protocol": 1, "os": runtime.GOOS, "shell": filepath.Base(s.shell), "marker_protocol": framing, "nonce": v.Nonce, "revision": executionguard.Global.Settings().Revision, "resource": executionguard.Memory()})
	case "mcp_arm":
		deadline, e := s.armDeadline(v.ID, v.Nonce, v.TimeoutMS, v.Force)
		message := ""
		if e != nil {
			message = e.Error()
		}
		s.replyGuard(map[string]any{"type": "mcp_arm_ack", "command_id": v.ID, "deadline": deadline, "error": message, "resource": executionguard.Memory()})
	}
	return true
}
func safeID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (s *terminalSession) armDeadline(id, nonce string, ms int, force bool) (*time.Time, error) {
	p := executionguard.Global.Settings()
	if !safeID(id) || !safeID(nonce) || ms < 1 || ms > p.MaxTimeoutSeconds*1000 || force && !p.ForceCloseOnTimeout {
		return nil, errors.New("invalid command deadline")
	}
	r := executionguard.Memory()
	if !s.options.Maintenance && r.MemoryTotal > 0 && p.MinFreePercent > 0 && (r.MemoryUsed >= r.MemoryTotal || float64(r.MemoryTotal-r.MemoryUsed)/float64(r.MemoryTotal)*100 < float64(p.MinFreePercent)) {
		return nil, errors.New("memory pressure")
	}
	d := &s.deadline
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.id != "" || d.closing {
		return nil, errors.New("foreground command unfinished; interrupt or close")
	}
	at := time.Now().UTC().Add(time.Duration(ms) * time.Millisecond)
	d.id = id
	d.nonce = nonce
	d.expired = false
	d.force = force
	d.tail = nil
	d.timer = time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
		d.mu.Lock()
		if d.id != id || d.nonce != nonce {
			d.mu.Unlock()
			return
		}
		d.expired = true
		if t, ok := s.term.(interface{ Interrupt() error }); ok {
			_ = t.Interrupt()
		} else {
			go s.term.Write([]byte{3})
		}
		d.mu.Unlock()
		s.replyGuard(map[string]any{"type": "mcp_timeout", "command_id": id})
		if force {
			time.AfterFunc(10*time.Second, func() {
				d.mu.Lock()
				still := d.id == id && d.nonce == nonce && d.expired
				if still {
					d.closing = true
				}
				d.mu.Unlock()
				if still {
					closeSession(s.id, s)
				}
			})
		}
	})
	return &at, nil
}

// Observe raw END markers locally before forwarding. Losing the admin socket
// must not leave a completed command's deadline armed against its next shell.
func (s *terminalSession) observeCompletion(data []byte) {
	d := &s.deadline
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.id == "" {
		return
	}
	prefix := []byte("\x1eKMB:" + d.nonce + ":END:" + d.id + ":")
	delimiter := byte(0x1f)
	if runtime.GOOS == "windows" {
		prefix = []byte("~KMB:" + d.nonce + ":END:" + d.id + ":")
		delimiter = '~'
	}
	b := append(d.tail, data...)
	if i := bytes.Index(b, prefix); i >= 0 {
		rest := b[i+len(prefix):]
		if end := bytes.IndexByte(rest, delimiter); end >= 0 {
			code := string(rest[:end])
			rc, e := strconv.ParseInt(code, 10, 32)
			valid := e == nil && len(code) > 0 && len(code) <= 11
			if runtime.GOOS != "windows" {
				valid = valid && rc >= 0 && rc <= 255
			}
			if valid {
				if d.timer != nil {
					d.timer.Stop()
				}
				d.id = ""
				d.tail = nil
				return
			}
		}
	}
	keep := min(len(b), 512)
	d.tail = append([]byte(nil), b[len(b)-keep:]...)
}
func (s *terminalSession) stopDeadline() {
	d := &s.deadline
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.id = ""
	d.tail = nil
	d.mu.Unlock()
}
