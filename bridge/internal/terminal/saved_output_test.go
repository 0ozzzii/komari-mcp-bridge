package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavedOutputUTF8LimitsAndReadOnlyAttribution(t *testing.T) {
	dir := t.TempDir()
	last := time.Now().Add(-time.Minute)
	s := &Session{Record: Record{ID: "session-a", Owner: "owner-a", Node: "node-a", LastCall: last, LogTruncated: true, Gap: true}}
	m := &Manager{dir: dir, sessions: map[string]*Session{"session-a": s}}
	data := strings.Repeat("a", 254) + "中文" + string([]byte{0xff})
	if err := os.WriteFile(filepath.Join(dir, "session-a.log"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := m.SavedOutput("session-a", 0, 256)
	if err != nil || len(p.Output) != 254 || p.Next != 254 || !p.More || !p.Truncated || !p.Gap {
		t.Fatal("UTF-8 boundary or flags lost", p, err)
	}
	p, err = m.SavedOutput("session-a", p.Next, 256)
	if err != nil || p.Output != "中文�" || p.More || !p.Replaced || p.Next != int64(len(data)) {
		t.Fatal("output decoding lost bytes", p, err)
	}
	if m.SessionNode("owner-a", "session-a") != "node-a" || m.SessionNode("owner-b", "session-a") != "" || !s.LastCall.Equal(last) {
		t.Fatal("audit attribution changed idle time or crossed owners")
	}
	for _, id := range []string{"../keys", "missing", "session-a/other"} {
		if _, err = m.SavedOutput(id, 0, 256); err == nil {
			t.Fatal("unknown path accepted", id)
		}
	}
	for _, offset := range []int64{-1, int64(len(data) + 1)} {
		if _, err = m.SavedOutput("session-a", offset, 256); err == nil {
			t.Fatal("invalid offset accepted")
		}
	}
	if _, err = m.SavedOutput("session-a", 0, 32769); err == nil {
		t.Fatal("unbounded output accepted")
	}
}
