package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/terminal"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportExcludesCredentialsAndRawOutputAndBoundsHistory(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sessions"), 0700)
	secret := "DO_NOT_EXPORT_PASSWORD_OR_NONCE"
	record := terminal.Record{ID: "safe-session", Node: "node-a", Owner: "key-id", Nonce: secret, Work: secret, Commands: map[string]*terminal.Command{}}
	for i := 0; i < 110; i++ {
		id := time.Unix(int64(i), 0).UTC().Format("150405")
		record.Commands[id] = &terminal.Command{ID: id, Hash: secret, State: "uncertain", TimedOut: true, TimeoutMS: 1800000, Uncertain: true, Created: time.Unix(int64(i), 0)}
	}
	b, _ := json.Marshal(record)
	os.WriteFile(filepath.Join(dir, "sessions", "safe-session.json"), b, 0600)
	os.WriteFile(filepath.Join(dir, "sessions", "safe-session.log"), []byte(secret), 0600)
	os.WriteFile(filepath.Join(dir, "keys.json"), []byte(secret), 0600)
	os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte("{\"event\":\"tool\",\"session_id\":\"safe-session\",\"command\":\""+secret+"\",\"Authorization\":\""+secret+"\"}\nmalformed\n"), 0600)
	out := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := Export(dir, out, "v1.0.0", "commit"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	found := map[string]bool{}
	for _, f := range archive.File {
		reader, _ := f.Open()
		b, _ := io.ReadAll(reader)
		reader.Close()
		found[f.Name] = true
		if strings.Contains(string(b), secret) {
			t.Fatal("diagnostics leaked excluded content", f.Name)
		}
		if f.Name == "sessions/safe-session.json" {
			var s sessionSummary
			json.Unmarshal(b, &s)
			if !s.Commands[0].TimedOut || s.Commands[0].TimeoutMS != 1800000 {
				t.Fatal("diagnostics omitted execution timeout metadata")
			}
			if len(s.Commands) != 100 || s.Omitted != 10 {
				t.Fatal("history limit not enforced")
			}
		}
		if f.Name == "manifest.json" {
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["skipped_or_truncated_items"].(float64) < 1 {
				t.Fatal("malformed audit line was not reported")
			}
		}
	}
	if !found["audit.jsonl"] || !found["manifest.json"] || !found["sessions/safe-session.json"] || found["keys.json"] {
		t.Fatal("unexpected diagnostic archive contents", found)
	}
	if err = Export(dir, out, "v", "c"); err == nil {
		t.Fatal("existing archive overwritten")
	}
	if err = Export(filepath.Join(dir, "absent"), filepath.Join(dir, "missing.zip"), "v", "c"); err == nil {
		t.Fatal("missing state directory reported success")
	}
}
