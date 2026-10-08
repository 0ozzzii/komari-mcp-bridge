// Package diagnostics exports bounded metadata without credentials or PTY bytes.
package diagnostics

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/terminal"
)

const maxSource = 2 << 20
const maxBundle = 12 << 20

var auditFields = map[string]bool{
	"time": true, "event": true, "tool": true, "action": true, "key_id": true,
	"node_uuid": true, "session_id": true, "command_id": true, "operation_id": true,
	"success": true, "tool_success": true, "execution_state": true, "execution_uncertain": true,
	"exit_code": true, "connection_state": true, "generation": true, "error_code": true,
	"context_verified": true, "context_lost": true, "output_gap": true, "output_log_truncated": true,
	"scope": true, "target_id": true, "timed_out": true, "execution_timeout_ms": true, "execution_deadline": true,
	"attempt": true, "http_status": true, "duration_ms": true,
}

type commandSummary struct {
	Deadline  *time.Time `json:"execution_deadline,omitempty"`
	TimeoutMS int        `json:"execution_timeout_ms,omitempty"`
	TimedOut  bool       `json:"timed_out"`
	ID        string     `json:"command_id"`
	State     string     `json:"command_state"`
	ExitCode  *int       `json:"exit_code"`
	Uncertain bool       `json:"execution_uncertain"`
	Created   time.Time  `json:"created_at"`
	Started   *time.Time `json:"started_at"`
	Finished  *time.Time `json:"ended_at"`
}
type sessionSummary struct {
	ID         string           `json:"session_id"`
	Node       string           `json:"node_uuid"`
	Owner      string           `json:"key_id"`
	State      string           `json:"connection_state"`
	Generation int              `json:"generation"`
	Verified   bool             `json:"context_verified"`
	Lost       bool             `json:"context_lost"`
	Gap        bool             `json:"output_gap"`
	Truncated  bool             `json:"output_log_truncated"`
	LastCall   time.Time        `json:"last_tool_call"`
	LastData   time.Time        `json:"last_data"`
	LastPong   time.Time        `json:"last_admin_pong"`
	LastError  string           `json:"last_connection_error"`
	Active     string           `json:"active_command_id"`
	Commands   []commandSummary `json:"commands"`
	Omitted    int              `json:"older_commands_omitted"`
}

// Export is offline and refuses to replace existing files. A running daemon may
// change metadata during collection, so this is a best-effort diagnostic view.
func Export(dir, output, version, commit string) (err error) {
	if dir == "" || output == "" {
		return errors.New("state directory and output path required")
	}
	if info, e := os.Stat(dir); e != nil || !info.IsDir() {
		return errors.New("state directory unavailable")
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create diagnostic archive (existing files are never replaced)")
	}
	done := false
	defer func() {
		f.Close()
		if !done {
			os.Remove(output)
		}
	}()
	z := zip.NewWriter(f)
	total, skipped := 0, 0
	add := func(name string, content []byte) error {
		if total+len(content) > maxBundle-8192 {
			skipped++
			return nil
		}
		w, e := z.Create(name)
		if e != nil {
			return e
		}
		_, e = w.Write(content)
		total += len(content)
		return e
	}
	for _, name := range []string{"audit.jsonl.1", "audit.jsonl"} {
		content, truncated, e := readFile(filepath.Join(dir, name), true)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			skipped++
			continue
		}
		if truncated {
			skipped++
			if index := bytes.IndexByte(content, '\n'); index >= 0 {
				content = content[index+1:]
			} else {
				continue
			}
		}
		var filtered bytes.Buffer
		scanner := bufio.NewScanner(bytes.NewReader(content))
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				skipped++
				continue
			}
			safe := map[string]any{}
			for field, v := range record {
				if !auditFields[field] {
					continue
				}
				switch x := v.(type) {
				case string:
					if len(x) <= 256 {
						safe[field] = x
					}
				case bool, float64, nil:
					safe[field] = x
				}
			}
			if len(safe) == 0 {
				continue
			}
			b, e := json.Marshal(safe)
			if e != nil {
				skipped++
				continue
			}
			filtered.Write(b)
			filtered.WriteByte('\n')
		}
		if scanner.Err() != nil {
			skipped++
		}
		if err = add(name, filtered.Bytes()); err != nil {
			return err
		}
	}
	entries, e := os.ReadDir(filepath.Join(dir, "sessions"))
	if e != nil && !os.IsNotExist(e) {
		skipped++
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			continue
		}
		if count >= 128 {
			skipped++
			continue
		}
		count++
		content, truncated, e := readFile(filepath.Join(dir, "sessions", entry.Name()), false)
		if e != nil || truncated {
			skipped++
			continue
		}
		var r terminal.Record
		if json.Unmarshal(content, &r) != nil {
			skipped++
			continue
		}
		summary := sessionSummary{ID: r.ID, Node: r.Node, Owner: r.Owner, State: r.State, Generation: r.Generation, Verified: r.Verified, Lost: r.Lost, Gap: r.Gap, Truncated: r.LogTruncated, LastCall: r.LastCall, LastData: r.LastData, LastPong: r.LastAdminPong, LastError: r.LastConnectionError, Active: r.Active, Commands: []commandSummary{}}
		ordered := []*terminal.Command{}
		for _, c := range r.Commands {
			if c != nil {
				ordered = append(ordered, c)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].Created.Equal(ordered[j].Created) {
				return ordered[i].ID < ordered[j].ID
			}
			return ordered[i].Created.After(ordered[j].Created)
		})
		for i, c := range ordered {
			if i >= 100 {
				summary.Omitted++
				continue
			}
			summary.Commands = append(summary.Commands, commandSummary{Deadline: c.Deadline, TimeoutMS: c.TimeoutMS, TimedOut: c.TimedOut, ID: c.ID, State: c.State, ExitCode: c.ExitCode, Uncertain: c.Uncertain, Created: c.Created, Started: c.StartedAt, Finished: c.EndedAt})
		}
		b, e := json.MarshalIndent(summary, "", "  ")
		if e != nil {
			skipped++
			continue
		}
		if err = add("sessions/"+entry.Name(), b); err != nil {
			return err
		}
	}
	metadata := map[string]any{"created_at": time.Now().UTC(), "version": version, "commit": commit, "os": runtime.GOOS, "arch": runtime.GOARCH, "best_effort_snapshot": true, "skipped_or_truncated_items": skipped, "uncompressed_limit_bytes": maxBundle, "excluded": []string{"credentials/config/environment", "probe tokens and URL keys", "command bodies and hashes", "PTY output", "continuity nonce", "cloudflared/system logs"}}
	b, _ := json.MarshalIndent(metadata, "", "  ")
	// Reserve metadata even if data entries exhausted the limit.
	w, e := z.Create("manifest.json")
	if e != nil {
		return e
	}
	if _, e = w.Write(b); e != nil {
		return e
	}
	if err = z.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	done = true
	return nil
}

func readFile(path string, tail bool) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("non-regular diagnostic source")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	truncated := info.Size() > maxSource
	if truncated && tail {
		if _, err = f.Seek(info.Size()-maxSource, io.SeekStart); err != nil {
			return nil, false, err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxSource))
	return b, truncated, err
}
