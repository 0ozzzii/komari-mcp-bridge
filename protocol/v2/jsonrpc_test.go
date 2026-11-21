package v2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportParamsPreservesResourceScopeAndLegacyMessage(t *testing.T) {
	input := `{"report":{"resource_scope":{"mode":"container","disk_used_status":"pending","disk_total":"unknown","disk_total_status":"unknown","load":"unknown","load_status":"unknown"},"disk":{"total":0,"used":0},"load":{"load1":0,"load5":0,"load15":0},"message":"resource_scope=container; legacy marker"}}`

	var params ReportParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got := params.Report.ResourceScope["mode"]; got != "container" {
		t.Fatalf("resource_scope.mode = %q, want container", got)
	}
	for key, want := range map[string]string{
		"disk_used_status": "pending",
		"disk_total":      "unknown",
		"disk_total_status": "unknown",
		"load":            "unknown",
		"load_status":     "unknown",
	} {
		if got := params.Report.ResourceScope[key]; got != want {
			t.Errorf("resource_scope.%s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(params.Report.Message, "resource_scope=container") {
		t.Fatalf("legacy message marker was lost: %q", params.Report.Message)
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(encoded), `"resource_scope"`) || !strings.Contains(string(encoded), `"disk_used_status":"pending"`) {
		t.Fatalf("resource scope was not re-emitted: %s", encoded)
	}
}
