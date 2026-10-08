package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
)

const auditScanBudget = 2 << 20

var errLogCursor = errors.New("log snapshot expired; refresh the log list")
var logFields = map[string]bool{
	"shell_type": true,
	"time":       true, "event": true, "tool": true, "action": true, "key_id": true,
	"node_uuid": true, "session_id": true, "command_id": true, "operation_id": true,
	"success": true, "tool_success": true, "execution_state": true, "execution_uncertain": true,
	"exit_code": true, "connection_state": true, "generation": true, "error_code": true,
	"context_verified": true, "context_lost": true, "output_gap": true, "output_log_truncated": true,
	"scope": true, "target_id": true, "timed_out": true, "execution_timeout_ms": true, "execution_deadline": true,
	"attempt": true, "http_status": true, "duration_ms": true,
}

type logPart struct {
	Size      int64  `json:"size"`
	Position  int64  `json:"position"`
	HeadBytes int    `json:"head_bytes"`
	Hash      string `json:"hash"`
}
type logCursor struct {
	Parts  []logPart `json:"parts"`
	Index  int       `json:"index"`
	Filter string    `json:"filter"`
}
type logQuery struct {
	Node, Key, Event, Status string
	From, To                 time.Time
	Limit                    int
}
type logPage struct {
	Records []map[string]any `json:"records"`
	Next    string           `json:"next_cursor"`
	More    bool             `json:"has_more"`
	Scanned int              `json:"scanned_bytes"`
	Skipped int              `json:"skipped_records"`
	Budget  int              `json:"scan_limit_bytes"`
}

func parseLogQuery(v url.Values) (logQuery, error) {
	q := logQuery{Node: v.Get("node_uuid"), Key: v.Get("key_id"), Event: v.Get("event"), Status: v.Get("status"), Limit: 50}
	for name, values := range v {
		if len(values) != 1 {
			return q, errors.New("invalid log query")
		}
		switch name {
		case "node_uuid", "key_id", "event", "status", "from", "to", "limit", "cursor":
		default:
			return q, errors.New("invalid log query")
		}
	}
	if q.Node != "" && !upstream.ValidNode(q.Node) {
		return q, errors.New("invalid node")
	}
	if len(q.Key) > 128 || len(q.Event) > 64 || (q.Status != "" && q.Status != "problem") {
		return q, errors.New("invalid log filter")
	}
	for name, target := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if s := v.Get(name); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return q, errors.New("invalid log time")
			}
			*target = t
		}
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return q, errors.New("invalid log time range")
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			return q, errors.New("log page limit must be 1..100")
		}
		q.Limit = n
	}
	return q, nil
}
func logProblem(v map[string]any) bool {
	for _, key := range []string{"execution_uncertain", "context_lost", "output_gap", "output_log_truncated", "timed_out"} {
		if v[key] == true {
			return true
		}
	}
	for _, key := range []string{"success", "tool_success"} {
		if v[key] == false {
			return true
		}
	}
	if n, ok := v["exit_code"].(float64); ok && n != 0 {
		return true
	}
	if code, ok := v["error_code"].(string); ok && code != "" {
		return true
	}
	s, _ := v["connection_state"].(string)
	return s == "expired" || s == "context_lost" || strings.HasSuffix(s, "_failed") || strings.HasSuffix(s, "_uncertain")
}
func (q logQuery) matches(v map[string]any) bool {
	if q.Node != "" && v["node_uuid"] != q.Node {
		return false
	}
	if q.Key != "" && v["key_id"] != q.Key {
		return false
	}
	if q.Event != "" && v["event"] != q.Event {
		return false
	}
	if q.Status == "problem" && !logProblem(v) {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, stringField(v, "time"))
	if err != nil {
		return false
	}
	return (q.From.IsZero() || !t.Before(q.From)) && (q.To.IsZero() || !t.After(q.To))
}
func stringField(v map[string]any, key string) string { s, _ := v[key].(string); return s }
func cleanLogRecord(raw []byte) map[string]any {
	if len(raw) > 16384 {
		return nil
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	result := map[string]any{}
	for k, v := range input {
		if !logFields[k] {
			continue
		}
		switch value := v.(type) {
		case string:
			if len(value) <= 256 {
				result[k] = value
			}
		case float64, bool:
			result[k] = value
		case nil:
			result[k] = nil
		}
	}
	if result["event"] == "execution_policy_update" && result["scope"] == "node" {
		result["node_uuid"] = result["target_id"]
	}
	return result
}
func openLogFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("invalid log file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("log file changed")
	}
	return f, nil
}
func logHead(f *os.File, n int) (string, error) {
	b := make([]byte, n)
	_, err := f.ReadAt(b, 0)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func logFingerprint(q logQuery) string {
	q.Limit = 0
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (a *auditLog) snapshot(q logQuery) (logCursor, error) {
	// Capture sizes and identity prefixes under the writer lock. Scanning uses
	// short bounded reads under that lock; JSON filtering happens outside it.
	a.mu.Lock()
	defer a.mu.Unlock()
	c := logCursor{Parts: []logPart{}, Filter: logFingerprint(q)}
	for _, name := range []string{a.path, a.path + ".1"} {
		f, err := openLogFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return c, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return c, err
		}
		if info.Size() == 0 {
			f.Close()
			continue
		}
		if info.Size() > 8<<20 {
			f.Close()
			return c, errors.New("audit file exceeds read limit")
		}
		part := logPart{Size: info.Size(), Position: info.Size(), HeadBytes: int(min(info.Size(), 256))}
		part.Hash, err = logHead(f, part.HeadBytes)
		f.Close()
		if err != nil {
			return c, err
		}
		c.Parts = append(c.Parts, part)
	}
	return c, nil
}
func (a *auditLog) locate(part logPart) (*os.File, error) {
	for _, name := range []string{a.path, a.path + ".1"} {
		f, err := openLogFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err == nil && info.Size() >= part.Size {
			if hash, e := logHead(f, part.HeadBytes); e == nil && hash == part.Hash {
				return f, nil
			}
		}
		f.Close()
	}
	return nil, errLogCursor
}
func (a *auditLog) readPage(ctx context.Context, q logQuery, token string) (logPage, error) {
	p := logPage{Records: []map[string]any{}, Budget: auditScanBudget}
	var c logCursor
	if token == "" {
		var err error
		c, err = a.snapshot(q)
		if err != nil {
			return p, err
		}
	} else {
		if len(token) > 2048 {
			return p, errLogCursor
		}
		b, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || json.Unmarshal(b, &c) != nil {
			return p, errLogCursor
		}
		// Page size may change; all filters remain bound to the cursor.
		if c.Filter != logFingerprint(q) || len(c.Parts) > 2 || c.Index < 0 || c.Index > len(c.Parts) {
			return p, errLogCursor
		}
		for _, part := range c.Parts {
			if part.Size < 1 || part.Size > 8<<20 || part.Position < 0 || part.Position > part.Size || part.HeadBytes < 1 || part.HeadBytes > 256 || int64(part.HeadBytes) > part.Size || len(part.Hash) != 64 {
				return p, errLogCursor
			}
		}
	}
	for c.Index < len(c.Parts) && len(p.Records) < q.Limit && p.Scanned < auditScanBudget {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		part := &c.Parts[c.Index]
		if part.Position == 0 {
			c.Index++
			continue
		}
		n := min(int64(64<<10), part.Position, int64(auditScanBudget-p.Scanned))
		start := part.Position - n
		block := make([]byte, n)
		// A short read lock avoids Windows rotation races with open readers.
		// JSON parsing and filtering stay outside the writer lock.
		a.mu.Lock()
		f, err := a.locate(*part)
		if err == nil {
			_, err = f.ReadAt(block, start)
			f.Close()
		}
		a.mu.Unlock()
		if err != nil {
			return p, err
		}
		p.Scanned += len(block)
		end := len(block)
		// Ignore an incomplete trailing record in the captured snapshot.
		if part.Position == part.Size && block[end-1] != '\n' {
			i := bytes.LastIndexByte(block, '\n')
			if i < 0 {
				part.Position = start
				p.Skipped++
				continue
			}
			end = i + 1
			p.Skipped++
		}
		if end > 0 && block[end-1] == '\n' {
			end--
		}
		for end > 0 && len(p.Records) < q.Limit {
			i := bytes.LastIndexByte(block[:end], '\n')
			if i < 0 && start > 0 {
				if end == len(block) {
					part.Position = start
					p.Skipped++
				} else {
					part.Position = start + int64(end)
				}
				break
			}
			raw := block[i+1 : end]
			part.Position = start + int64(i+1)
			end = max(i, 0)
			v := cleanLogRecord(raw)
			if v == nil {
				p.Skipped++
				continue
			}
			if q.matches(v) {
				p.Records = append(p.Records, v)
			}
		}
		if end == 0 {
			part.Position = start
		}
	}
	for c.Index < len(c.Parts) && c.Parts[c.Index].Position == 0 {
		c.Index++
	}
	p.More = c.Index < len(c.Parts)
	if p.More {
		raw, _ := json.Marshal(c)
		p.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return p, nil
}
func (a *App) logList(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 4096 {
		jsonReply(w, 400, map[string]string{"error": "log query too large"})
		return
	}
	q, err := parseLogQuery(r.URL.Query())
	if err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	select {
	case a.audit.readSlots <- struct{}{}:
		defer func() { <-a.audit.readSlots }()
	default:
		jsonReply(w, 429, map[string]string{"error": "log query busy; retry shortly"})
		return
	}
	p, err := a.audit.readPage(r.Context(), q, r.URL.Query().Get("cursor"))
	if err != nil {
		status := 503
		message := "log records unavailable"
		if errors.Is(err, errLogCursor) {
			status = 409
			message = errLogCursor.Error()
		}
		jsonReply(w, status, map[string]string{"error": message})
		return
	}
	jsonReply(w, 200, p)
}

// Kept private: filesystem state is never served through the public MCP URL.
func (a *App) outputLog(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 4096 {
		jsonReply(w, 400, map[string]string{"error": "output query too large"})
		return
	}
	v := r.URL.Query()
	for key, values := range v {
		if len(values) != 1 || (key != "session_id" && key != "offset" && key != "limit") {
			jsonReply(w, 400, map[string]string{"error": "invalid output query"})
			return
		}
	}
	offset := int64(0)
	limit := 16384
	var err error
	if s := v.Get("offset"); s != "" {
		offset, err = strconv.ParseInt(s, 10, 64)
		if err != nil || offset < 0 {
			jsonReply(w, 400, map[string]string{"error": "invalid output offset"})
			return
		}
	}
	if s := v.Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 256 || limit > 32768 {
			jsonReply(w, 400, map[string]string{"error": "invalid output limit"})
			return
		}
	}
	page, err := a.Terminal.SavedOutput(v.Get("session_id"), offset, limit)
	if err != nil {
		jsonReply(w, 404, map[string]string{"error": "saved output unavailable or expired"})
		return
	}
	jsonReply(w, 200, page)
}
