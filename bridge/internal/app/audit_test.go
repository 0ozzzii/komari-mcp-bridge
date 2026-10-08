package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRotatesTwiceWithoutLosingCurrentLog(t *testing.T) {
	dir := t.TempDir()
	audit, err := openAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.close()
	for i := 0; i < 3; i++ {
		audit.size = 8 << 20
		audit.record(map[string]any{"event": "test", "success": true})
	}
	for _, name := range []string{"audit.jsonl", "audit.jsonl.1"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(b), `"event":"test"`) {
			t.Fatal("audit rotation failed", name, err)
		}
	}
	if audit.file == nil {
		t.Fatal("audit silently disabled after repeated rotation")
	}
}
