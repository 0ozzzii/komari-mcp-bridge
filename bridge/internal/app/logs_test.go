package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const logNodeA = "11111111-1111-4111-8111-111111111111"
const logNodeB = "22222222-2222-4222-8222-222222222222"

func TestLogPagesFiltersRedactionAndStableSnapshot(t *testing.T) {
	dir := t.TempDir()
	a, err := openAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	for i := 0; i < 900; i++ {
		node := logNodeA
		if i%2 == 0 {
			node = logNodeB
		}
		a.record(map[string]any{"event": "command_state", "node_uuid": node, "key_id": "key-a", "command_id": fmt.Sprint(i), "execution_state": "completed", "exit_code": i % 3, "command": "secret-command", "Authorization": "Bearer private-value", "cookie": "private-cookie", "padding": strings.Repeat("x", 256)})
	}
	q := logQuery{Node: logNodeA, Key: "key-a", Event: "command_state", Status: "problem", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second), Limit: 17}
	page, err := a.readPage(context.Background(), q, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 17 || !page.More {
		t.Fatalf("first page: %+v", page)
	}
	// New appends do not move the captured snapshot between pages.
	a.record(map[string]any{"event": "command_state", "node_uuid": logNodeA, "key_id": "key-a", "command_id": "new", "exit_code": 1})
	seen := map[string]bool{}
	total := 0
	for {
		for _, record := range page.Records {
			id := stringField(record, "command_id")
			if id == "new" || seen[id] || record["node_uuid"] != logNodeA || !logProblem(record) {
				t.Fatal("snapshot, filter or duplicate error", id)
			}
			seen[id] = true
			total++
			for _, secret := range []string{"command", "Authorization", "cookie", "padding"} {
				if _, ok := record[secret]; ok {
					t.Fatal("non-allowlisted data exposed", secret)
				}
			}
		}
		if !page.More {
			break
		}
		page, err = a.readPage(context.Background(), q, page.Next)
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 300 {
		t.Fatalf("missing records across chunk/page boundaries: %d", total)
	}
	q.Node = logNodeB
	if _, err = a.readPage(context.Background(), q, "invalid"); !errors.Is(err, errLogCursor) {
		t.Fatal("bad cursor accepted")
	}
}

func TestLogCursorSurvivesOneRotationAndReportsEviction(t *testing.T) {
	a, err := openAudit(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	for i := 0; i < 6; i++ {
		a.record(map[string]any{"event": "tool", "command_id": fmt.Sprint(i)})
	}
	q := logQuery{Limit: 2}
	p, err := a.readPage(context.Background(), q, "")
	if err != nil || !p.More {
		t.Fatal("missing first page", err)
	}
	a.size = 8 << 20
	a.record(map[string]any{"event": "tool", "command_id": "rotated"})
	next, err := a.readPage(context.Background(), q, p.Next)
	if err != nil || stringField(next.Records[0], "command_id") != "3" {
		t.Fatal("rotation lost snapshot", err)
	}
	q.Node = logNodeA
	if _, err = a.readPage(context.Background(), q, p.Next); !errors.Is(err, errLogCursor) {
		t.Fatal("cursor filter not bound")
	}
	q.Node = ""
	a.size = 8 << 20
	a.record(map[string]any{"event": "tool", "command_id": "rotated-again"})
	if _, err = a.readPage(context.Background(), q, p.Next); !errors.Is(err, errLogCursor) {
		t.Fatal("evicted snapshot silently accepted", err)
	}
}

func TestLogSearchBudgetAndMalformedTail(t *testing.T) {
	dir := t.TempDir()
	record := `{"event":"tool","time":"2026-10-08T04:00:00Z","node_uuid":"` + logNodeA + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(record+strings.Repeat("x", auditScanBudget+128)+"\n{partial"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := openAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	q := logQuery{Node: logNodeA, Limit: 50}
	p, err := a.readPage(context.Background(), q, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scanned > auditScanBudget || !p.More || p.Skipped == 0 || len(p.Records) != 0 {
		t.Fatalf("unbounded or inaccurate search: %+v", p)
	}
	p, err = a.readPage(context.Background(), q, p.Next)
	if err != nil || len(p.Records) != 1 || p.More {
		t.Fatal("older valid record not recovered", err, p)
	}
}

func TestLogControlAuthenticationValidationAndQueryLane(t *testing.T) {
	control := strings.Repeat("c", 32)
	a, err := New(context.Background(), Config{BaseURL: "http://127.0.0.1:1", APIKey: "private-upstream-key", ControlToken: control, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.audit.record(map[string]any{"event": "execution_policy_update", "scope": "node", "target_id": logNodeA, "success": true, "command": "not-exported"})
	request := func(path, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.ControlHandler().ServeHTTP(w, r)
		return w
	}
	if w := request("/control/logs", "caller-key", ""); w.Code != 401 {
		t.Fatal("scoped caller accessed logs")
	}
	if w := request("/control/logs", control, "https://browser.example"); w.Code != 403 {
		t.Fatal("browser bypassed administrator proxy")
	}
	for _, query := range []string{"limit=101", "limit=-1", "node_uuid=..%2Fkeys", "from=not-a-date", "from=2026-10-09T00:00:00Z&to=2026-10-08T00:00:00Z", "status=invalid", "path=../keys.json", "event=tool&event=control"} {
		if w := request("/control/logs?"+query, control, ""); w.Code != 400 {
			t.Fatal("invalid filter accepted", query, w.Code)
		}
	}
	w := request("/control/logs?node_uuid="+url.QueryEscape(logNodeA), control, "")
	var p logPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Records) != 1 || p.Records[0]["node_uuid"] != logNodeA {
		t.Fatal("device policy attribution missing", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "not-exported") {
		t.Fatal("secret field escaped projection")
	}
	a.audit.readSlots <- struct{}{}
	if w := request("/control/logs", control, ""); w.Code != 429 {
		t.Fatal("query lane unbounded")
	}
	<-a.audit.readSlots
	if w := request("/control/status", control, ""); w.Code != 200 {
		t.Fatal("viewer blocked other controls")
	}
	public := httptest.NewRecorder()
	a.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/control/logs", nil))
	if public.Code != 404 {
		t.Fatal("public MCP listener exposed logs")
	}
}
