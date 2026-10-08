package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/terminal"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Service struct {
	Policy   *access.Store
	Terminal *terminal.Manager
	Upstream *upstream.Client
	Dir      string
	Audit    func(map[string]any)
	mu       sync.Mutex
}
type Arguments struct {
	SessionName      string   `json:"session_name"`
	Parallel         int      `json:"parallel"`
	ExecutionTimeout int      `json:"execution_timeout_ms"`
	Maintenance      bool     `json:"maintenance"`
	Node             string   `json:"node_uuid"`
	Work             string   `json:"work_id"`
	Session          string   `json:"session_id"`
	CommandID        string   `json:"command_id"`
	Command          string   `json:"command"`
	Argv             []string `json:"argv"`
	Wait             int      `json:"wait_timeout_ms"`
	MaxBytes         int      `json:"max_output_bytes"`
	Cursor           string   `json:"cursor"`
	Text             string   `json:"text"`
	Base64           string   `json:"base64"`
	Cols             int      `json:"cols"`
	Rows             int      `json:"rows"`
	Action           string   `json:"action"`
	Path             string   `json:"path"`
	Source           string   `json:"source"`
	Destination      string   `json:"destination"`
	Mode             string   `json:"mode"`
	Offset           int64    `json:"offset"`
	OperationID      string   `json:"operation_id"`
	Subject          string   `json:"subject"`
	Query            string   `json:"query"`
	Content          bool     `json:"content"`
}
type Definition struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	ReadOnly    bool
	Destructive bool
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func number(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
func field(names ...string) map[string]any {
	all := map[string]any{
		"session_name": text("Independent terminal within a shared work policy; default main"),
		"parallel":     number("Requested concurrency for this caller/node/work, bounded by administrator limits"), "execution_timeout_ms": number("Remote execution deadline: default 1800000ms (30min), maximum from live policy, normally 21600000ms (6h). Explicitly set a longer duration BEFORE dispatch for long tasks. Separate from wait_timeout_ms."), "maintenance": map[string]any{"type": "boolean", "description": "Administrator-authorized separate maintenance slot; default false"},
		"query": text("Search text for the search action"), "content": map[string]any{"type": "boolean", "description": "Search file contents rather than names"},
		"node_uuid": text("Allowed Komari node UUID"), "work_id": text("Caller-owned work identifier; reused only for this caller and node"), "session_id": text("Bridge session ID, not Komari request_id"), "command_id": text("Caller-generated command ID; repeat with identical command to query instead of dispatch"), "command": text("Shell source for session shell_type (POSIX or PowerShell), executed in persistent parent scope"), "argv": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"wait_timeout_ms": number("Tool wait only, 0..10000 ms; does not kill the remote command"), "max_output_bytes": number("256..131072 bytes; default 32768"), "cursor": text("Exclusive output cursor returned by the previous call, generation:byte-offset"), "text": text("Literal interactive input; newline must be explicit"), "base64": text("Base64 encoded raw bytes"), "cols": number("20..500 columns"), "rows": number("5..300 rows"), "action": text("Explicit action"), "path": text("Remote path, not a path on the bridge machine"), "source": text("Remote source path"), "destination": text("Remote destination path"), "mode": text("Octal file mode"), "offset": number("Remote file byte offset, >=0"), "operation_id": text("Caller-generated ID for a mutating operation; ambiguous operations are never automatically replayed"), "subject": text("system, processes, or capabilities"),
	}
	out := map[string]any{}
	for _, name := range names {
		out[name] = all[name]
	}
	return out
}
func enum(properties map[string]any, name string, values ...string) map[string]any {
	v := properties[name].(map[string]any)
	v["enum"] = values
	return properties
}
func Definitions() []Definition {
	return []Definition{
		{"komari_execution_policy", "Inspect resources or explicitly negotiate temporary per-work concurrency. Only NEW forward execution/input renews the 10min work lease; polling, pings and retries never renew.", enum(field("node_uuid", "work_id", "action", "parallel"), "action", "inspect", "set"), []string{"node_uuid", "work_id", "action"}, false, false},
		{"komari_nodes_list", "List only nodes allowed for this key; never includes client tokens", field(), nil, true, false},
		{"komari_capabilities", "Report implemented bridge capabilities and version-dependent limits", field(), nil, true, false},
		{"komari_session_open", "Create or reuse a caller/node/work-scoped persistent terminal; inspect status until shell ready", field("node_uuid", "work_id", "maintenance", "session_name"), []string{"node_uuid", "work_id"}, false, true},
		{"komari_session_status", "Observe connection, shell continuity, command states and output gaps", field("session_id"), []string{"session_id"}, true, false},
		{"komari_sessions_list", "List caller-owned bridge sessions", field(), nil, true, false},
		{"komari_command_run", "Run shell source OR quoted argv in the same shell. No auto-replay; exit code can be unknown", field("session_id", "command_id", "command", "argv", "wait_timeout_ms", "max_output_bytes", "execution_timeout_ms"), []string{"session_id", "command_id"}, false, true},
		{"komari_output_read", "Read bounded merged PTY stdout/stderr incrementally; tool timeout does not cancel command", field("session_id", "command_id", "cursor", "wait_timeout_ms", "max_output_bytes"), []string{"session_id"}, true, false},
		{"komari_terminal_input", "Send literal text OR raw base64 bytes to the current foreground command", field("session_id", "command_id", "text", "base64"), []string{"session_id", "command_id"}, false, true},
		{"komari_command_interrupt", "Request Ctrl+C; signal delivery and command termination are not guaranteed", field("session_id", "command_id"), []string{"session_id", "command_id"}, false, true},
		{"komari_terminal_resize", "Resize terminal cells; does not create a new shell", field("session_id", "cols", "rows"), []string{"session_id", "cols", "rows"}, false, false},
		{"komari_session_close", "Request upstream PTY close and release bridge resources; remote termination has no ACK", field("session_id"), []string{"session_id"}, false, true},
		{"komari_filesystem", "Native file operations; write accepts text/base64 up to 128 KiB. Mutating actions require operation_id", enum(field("node_uuid", "action", "path", "source", "destination", "mode", "operation_id", "query", "content", "text", "base64"), "action", "list", "roots", "stat", "mkdir", "delete", "move", "copy", "chmod", "search", "write"), []string{"node_uuid", "action"}, false, true},
		{"komari_file_read", "Read a bounded remote file chunk through native HTTP transfer; returns exact bytes as base64", field("node_uuid", "path", "offset", "max_output_bytes"), []string{"node_uuid", "path"}, true, false},
		{"komari_host_inspect", "Run a bounded standard host inspection command in a work session; actual tools may be unavailable", enum(field("session_id", "command_id", "subject", "wait_timeout_ms", "max_output_bytes", "execution_timeout_ms"), "subject", "system", "processes", "capabilities"), []string{"session_id", "command_id", "subject"}, false, true},
	}
}
func (s *Service) Server(key access.Key) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "komari-mcp-bridge", Version: "0.2.0"}, &mcp.ServerOptions{Instructions: execution.Instructions})
	for _, d := range Definitions() {
		if d.Required == nil {
			d.Required = []string{}
		}
		name := d.Name
		readOnly := d.ReadOnly
		destructive := d.Destructive
		server.AddTool(&mcp.Tool{Name: name, Description: d.Description, InputSchema: map[string]any{"type": "object", "properties": d.Properties, "required": d.Required, "additionalProperties": false}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args Arguments
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return failure("INVALID_ARGUMENTS"), nil
			}
			if args.MaxBytes == 0 {
				args.MaxBytes = 32768
			}
			// Recheck the live key on every call; tool/session IDs never supply identity.
			enabled, keys := s.Policy.List()
			if !enabled {
				return failure("MCP_DISABLED"), nil
			}
			live := false
			var currentKey access.Key
			for _, k := range keys {
				if k.ID == key.ID && k.Enabled && (k.ExpiresAt == nil || time.Now().Before(*k.ExpiresAt)) {
					currentKey = k
					live = true
					break
				}
			}
			if !live {
				return failure("UNAUTHORIZED"), nil
			}
			started := time.Now()
			value, err := s.call(ctx, currentKey, name, args)
			if s.Audit != nil {
				event := map[string]any{"event": "tool", "tool": name, "key_id": currentKey.ID, "node_uuid": args.Node, "session_id": args.Session, "command_id": args.CommandID, "operation_id": args.OperationID, "tool_success": err == nil}
				if args.Node == "" && args.Session != "" {
					event["node_uuid"] = s.Terminal.SessionNode(currentKey.ID, args.Session)
				}
				event["duration_ms"] = time.Since(started).Milliseconds()
				if err != nil {
					code := "tool_error"
					switch {
					case errors.Is(err, access.ErrDenied):
						code = "access_denied"
					case errors.Is(err, terminal.ErrBusy):
						code = "session_busy"
					case errors.Is(err, context.DeadlineExceeded):
						code = "call_wait_timeout"
					case errors.Is(err, context.Canceled):
						code = "caller_disconnected"
					}
					event["error_code"] = code
				}
				if r, ok := value.(terminal.Result); ok {
					event["execution_state"] = r.State
					event["execution_uncertain"] = r.Uncertain
					event["exit_code"] = r.ExitCode
					event["timed_out"] = r.TimedOut
					event["execution_timeout_ms"] = r.TimeoutMS
					event["execution_deadline"] = r.Deadline
				} else if v, ok := value.(map[string]any); ok {
					if state, ok := v["state"]; ok {
						event["execution_state"] = state
					}
					if uncertain, ok := v["execution_uncertain"]; ok {
						event["execution_uncertain"] = uncertain
					}
				}
				s.Audit(event)
			}
			if err != nil {
				return failure(err.Error()), nil
			}
			b, err := json.Marshal(value)
			if err != nil {
				return failure("RESPONSE_ENCODING_FAILED"), nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: value}, nil
		})
	}
	return server
}
func failure(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}
func shellQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }
func (s *Service) call(ctx context.Context, key access.Key, name string, a Arguments) (any, error) {
	switch name {
	case "komari_execution_policy":
		if err := s.Policy.Require(key.ID, a.Node, "terminal"); err != nil {
			return nil, err
		}
		if !upstream.ValidNode(a.Node) || !validWork(a.Work) {
			return nil, errors.New("valid node_uuid and work_id required")
		}
		r := s.Upstream.Resource(ctx, a.Node)
		p, hard := s.Terminal.ExecutionPolicy().Effective(key.ID, a.Node, r)
		value := map[string]any{"resource": r, "settings": p, "hard_limit": hard, "instructions": execution.Instructions, "suggested_parallel": 1}
		switch a.Action {
		case "inspect":
			l, e := s.Terminal.ExecutionPolicy().Get(key.ID, a.Node, a.Work)
			value["policy_required"] = e != nil
			value["lease"] = l
		case "set":
			l, e := s.Terminal.ExecutionPolicy().Set(key.ID, a.Node, a.Work, a.Parallel, r)
			if e != nil {
				return nil, e
			}
			value["lease"] = l
		default:
			return nil, errors.New("invalid policy action")
		}
		return value, nil
	case "komari_nodes_list":
		raw, err := s.Upstream.RPC(ctx, "admin:listClients", map[string]any{})
		if err != nil {
			return nil, err
		}
		var nodes []map[string]any
		if err = json.Unmarshal(raw, &nodes); err != nil {
			return nil, err
		}
		result := []map[string]any{}
		for _, n := range nodes {
			uuid, _ := n["uuid"].(string)
			if !slices.Contains(key.Nodes, uuid) {
				continue
			}
			safe := map[string]any{}
			for _, f := range []string{"uuid", "name", "os", "arch", "version", "virtualization", "cpu_cores", "mem_total", "group"} {
				if v, ok := n[f]; ok {
					safe[f] = v
				}
			}
			r := s.Upstream.Resource(ctx, uuid)
			safe["resource"] = r
			p, hard := s.Terminal.ExecutionPolicy().Effective(key.ID, uuid, r)
			safe["hard_limit"] = hard
			safe["default_timeout_ms"] = p.DefaultTimeoutSeconds * 1000
			safe["max_timeout_ms"] = p.MaxTimeoutSeconds * 1000
			safe["suggested_parallel"] = 1
			safe["terminal_availability"] = "unknown_until_open"
			result = append(result, safe)
		}
		return result, nil
	case "komari_capabilities":
		return map[string]any{"persistent_shell": "POSIX /bin/sh or Windows PowerShell; verify session shell_type", "shell_types": []string{"posix", "powershell"}, "tool_count": len(Definitions()), "merged_pty_output": true, "normal_exit_code": "marker_confirmed", "interrupt": "best_effort_ctrl_c", "reconnect": "best_effort_with_continuity_probe_when_idle", "upstream_output_replay": false, "durable_remote_jobs": false, "native_files": "requires compatible Komari Server and Agent; verified on use", "full_tui": false, "privilege": "remote Agent OS user and namespace", "execution_timeout": "probe-owned deadline, default 30min / maximum 6h configurable; wait_timeout never kills command", "instructions": execution.Instructions}, nil
	case "komari_session_open":
		if a.SessionName == "" {
			a.SessionName = "main"
		}
		session, err := s.Terminal.OpenNamed(key.ID, a.Node, a.Work, a.SessionName, a.Maintenance)
		if err != nil {
			return nil, err
		}
		return s.Terminal.Status(key.ID, session.ID)
	case "komari_session_status":
		return s.Terminal.Status(key.ID, a.Session)
	case "komari_sessions_list":
		return s.Terminal.List(key.ID), nil
	case "komari_command_run":
		if (a.Command != "") == (len(a.Argv) > 0) {
			return nil, errors.New("provide exactly one of command or argv")
		}
		command := a.Command
		if len(a.Argv) > 0 {
			if len(a.Argv) > 128 {
				return nil, errors.New("too many argv entries")
			}
			parts := make([]string, len(a.Argv))
			for i, v := range a.Argv {
				if strings.ContainsRune(v, 0) {
					return nil, errors.New("NUL in argv")
				}
				parts[i] = shellQuote(v)
			}
			command = strings.Join(parts, " ")
		}
		return s.Terminal.RunTimed(ctx, key.ID, a.Session, a.CommandID, command, a.Wait, a.MaxBytes, a.ExecutionTimeout)
	case "komari_output_read":
		return s.Terminal.Read(ctx, key.ID, a.Session, a.CommandID, a.Cursor, a.MaxBytes, a.Wait)
	case "komari_terminal_input":
		if (a.Text != "") == (a.Base64 != "") {
			return nil, errors.New("provide exactly one of text or base64")
		}
		data := []byte(a.Text)
		if a.Base64 != "" {
			var err error
			data, err = base64.StdEncoding.DecodeString(a.Base64)
			if err != nil {
				return nil, err
			}
		}
		err := s.Terminal.Input(key.ID, a.Session, a.CommandID, data)
		return map[string]any{"input_requested": err == nil}, err
	case "komari_command_interrupt":
		err := s.Terminal.Interrupt(key.ID, a.Session, a.CommandID)
		return map[string]any{"interrupt_requested": err == nil, "termination_confirmed": false, "exit_code": nil}, err
	case "komari_terminal_resize":
		err := s.Terminal.Resize(key.ID, a.Session, a.Cols, a.Rows)
		return map[string]any{"resize_requested": err == nil}, err
	case "komari_session_close":
		err := s.Terminal.Close(key.ID, a.Session)
		return map[string]any{"bridge_closed": err == nil, "remote_close_requested": err == nil, "remote_termination_confirmed": false}, err
	case "komari_filesystem":
		return s.filesystem(ctx, key, a)
	case "komari_file_read":
		if err := s.Policy.Require(key.ID, a.Node, "file.read"); err != nil {
			return nil, err
		}
		b, more, err := s.Upstream.ReadFile(ctx, a.Node, a.Path, a.Offset, a.MaxBytes)
		return map[string]any{"base64": base64.StdEncoding.EncodeToString(b), "text": strings.ToValidUTF8(string(b), "�"), "next_offset": a.Offset + int64(len(b)), "may_have_more": more}, err
	case "komari_host_inspect":
		command := ""
		switch a.Subject {
		case "system":
			command = "uname -a; id; pwd"
		case "processes":
			command = "ps -eo pid,ppid,comm | head -n 101"
		case "capabilities":
			command = "id; for x in sh bash python3 systemctl ps sha256sum tar; do command -v \"$x\" || :; done"
		default:
			return nil, errors.New("unsupported inspection")
		}
		record, err := s.Terminal.Status(key.ID, a.Session)
		if err != nil {
			return nil, err
		}
		if record.Shell == terminal.ShellPowerShell {
			switch a.Subject {
			case "system":
				command = "$PSVersionTable; whoami; Get-Location"
			case "processes":
				command = "Get-Process | Select-Object -First 100 Id,ProcessName,CPU,WorkingSet64"
			case "capabilities":
				command = "$PSVersionTable; Get-Command powershell.exe,pwsh.exe,cmd.exe,Get-Process,Get-ChildItem -ErrorAction SilentlyContinue | Select-Object Name,CommandType"
			}
		}
		return s.Terminal.RunTimed(ctx, key.ID, a.Session, a.CommandID, command, a.Wait, a.MaxBytes, a.ExecutionTimeout)
	}
	return nil, errors.New("unknown tool")
}

type mutation struct {
	Owner  string          `json:"owner"`
	Hash   string          `json:"hash"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (s *Service) filesystem(ctx context.Context, key access.Key, a Arguments) (any, error) {
	methods := map[string]string{"list": "fileList", "roots": "fileListRoots", "stat": "fileStat", "mkdir": "fileMkdir", "delete": "fileDelete", "move": "fileMove", "copy": "fileCopy", "chmod": "fileChmod", "search": "fileSearch", "write": "upload"}
	method := methods[a.Action]
	if method == "" {
		return nil, errors.New("unsupported file action")
	}
	if !upstream.ValidNode(a.Node) {
		return nil, errors.New("invalid node_uuid")
	}
	write := !slices.Contains([]string{"list", "roots", "stat", "search"}, a.Action)
	permission := "file.read"
	if write {
		permission = "file.write"
	}
	if err := s.Policy.Require(key.ID, a.Node, permission); err != nil {
		return nil, err
	}
	params := map[string]any{"uuid": a.Node}
	if a.Action != "roots" {
		if a.Action == "move" || a.Action == "copy" {
			if a.Source == "" || a.Destination == "" {
				return nil, errors.New("source and destination required")
			}
			params["source"], params["destination"] = a.Source, a.Destination
		} else {
			if a.Path == "" {
				return nil, errors.New("remote path required")
			}
			params["path"] = a.Path
		}
	}
	if a.Action == "chmod" || a.Action == "mkdir" {
		params["mode"] = a.Mode
	}
	if a.Action == "search" {
		if strings.TrimSpace(a.Query) == "" {
			return nil, errors.New("query required")
		}
		params["query"] = a.Query
		params["content"] = a.Content
	}
	var fileData []byte
	if a.Action == "write" {
		if a.Text != "" && a.Base64 != "" {
			return nil, errors.New("provide text or base64, not both")
		}
		fileData = []byte(a.Text)
		if a.Base64 != "" {
			var err error
			fileData, err = base64.StdEncoding.DecodeString(a.Base64)
			if err != nil {
				return nil, errors.New("invalid base64")
			}
		}
		if len(fileData) > 131072 {
			return nil, errors.New("file write is limited to 128 KiB")
		}
		h := sha256.Sum256(fileData)
		params["content_sha256"] = hex.EncodeToString(h[:])
		params["size"] = len(fileData)
	}
	if !write {
		raw, err := s.Upstream.RPC(ctx, "admin:"+method, params)
		if err != nil {
			return nil, err
		}
		var value any
		err = json.Unmarshal(raw, &value)
		return value, err
	}
	if len(a.OperationID) < 1 || len(a.OperationID) > 128 {
		return nil, errors.New("mutating file action requires operation_id")
	}
	encoded, _ := json.Marshal(struct {
		Method string
		Params any
	}{method, params})
	hash := sha256.Sum256(encoded)
	operationKey := sha256.Sum256([]byte(key.ID + ":" + a.OperationID))
	path := filepath.Join(s.Dir, hex.EncodeToString(operationKey[:])+".json")
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	var record mutation
	if b, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(b, &record); err != nil {
			return nil, err
		}
		if record.Hash != hex.EncodeToString(hash[:]) {
			return nil, errors.New("operation_id reused with a different request")
		}
		if record.State != "completed" {
			return map[string]any{"state": record.State, "execution_uncertain": true, "message": "existing mutation is not automatically replayed"}, nil
		}
		var value any
		err = json.Unmarshal(record.Result, &value)
		return value, err
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	if len(entries) >= 10000 {
		return nil, errors.New("mutation history limit reached")
	}
	var historyBytes int64
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return nil, e
		}
		historyBytes += info.Size()
	}
	if historyBytes >= 64<<20 {
		return nil, errors.New("mutation history byte limit reached")
	}
	record = mutation{Owner: key.ID, Hash: hex.EncodeToString(hash[:]), State: "dispatched_unconfirmed"}
	if err = access.AtomicJSON(path, record); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if a.Action == "write" {
		raw, err = s.Upstream.WriteFile(ctx, a.Node, a.Path, fileData)
	} else {
		raw, err = s.Upstream.RPC(ctx, "admin:"+method, params)
	}
	if err != nil {
		record.State = "uncertain"
		record.Error = "upstream call failed; outcome may be unknown"
		access.AtomicJSON(path, record)
		return map[string]any{"state": "uncertain", "execution_uncertain": true, "message": record.Error}, nil
	}
	record.State = "completed"
	if len(raw) > 65536 {
		raw = json.RawMessage(`{"upstream_confirmed":true,"response_omitted":true,"reason":"response exceeds history record limit"}`)
	}
	record.Result = raw
	if err = access.AtomicJSON(path, record); err != nil {
		return nil, err
	}
	var value any
	err = json.Unmarshal(raw, &value)
	return value, err
}

// Give HTTP middleware an inexpensive scoped caller identity without forwarding
// raw credentials to tool arguments or upstream nodes.
func (s *Service) Describe() string {
	return fmt.Sprintf("%d tools; persistent POSIX shell and native file RPC", len(Definitions()))
}

func validWork(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
