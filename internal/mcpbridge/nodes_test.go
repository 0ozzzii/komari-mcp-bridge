package mcpbridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestPickerUsesServerOrderAndPresenceWithoutLeakingCredentials(t *testing.T) {
	stamp := time.Date(2026, 10, 7, 12, 30, 0, 0, time.UTC)
	input := []models.Client{
		{UUID: "late", Name: "A", Weight: 9, Token: "private-probe-token", IPv4: "private-ip"},
		{UUID: "first", Name: "Z", Weight: -1, Region: "KR", Hidden: true},
		{UUID: "tie", Name: "B", Weight: -1},
	}
	nodes := pickerNodes(input, []string{"first"}, map[string]*v2.Report{"late": {UpdatedAt: stamp}})
	if len(nodes) != 3 || nodes[0].UUID != "first" || nodes[1].UUID != "tie" || nodes[2].UUID != "late" {
		t.Fatalf("server weight/tie order changed: %+v", nodes)
	}
	if !nodes[0].Online || nodes[1].Online || nodes[2].Online || nodes[1].LastSeen != nil || nodes[2].LastSeen == nil || !nodes[2].LastSeen.Equal(stamp) {
		t.Fatalf("online state or last report timestamp invented: %+v", nodes)
	}
	encoded, err := json.Marshal(nodes)
	if err != nil || strings.Contains(string(encoded), "private-probe-token") || strings.Contains(string(encoded), "private-ip") {
		t.Fatal("picker leaked probe credentials or unrelated fields")
	}
}
