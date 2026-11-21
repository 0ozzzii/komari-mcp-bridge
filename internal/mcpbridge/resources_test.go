package mcpbridge

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestNodeResourceViewUsesStructuredScopeAndUnknownStatuses(t *testing.T) {
	report := &v2.Report{
		Ram:  v2.RamReport{Total: 1024, Used: 512},
		Disk: v2.DiskReport{Total: 0, Used: 0},
		Load: v2.LoadReport{Load1: 0, Load5: 0, Load15: 0},
		ResourceScope: map[string]string{
			"mode":              "container",
			"disk_used_status":  "pending",
			"disk_total":        "unknown",
			"disk_total_status": "unknown",
			"load":              "unknown",
			"load_status":       "unknown",
		},
		UpdatedAt: time.Now().UTC(),
	}

	got := nodeResourceView(report, time.Now().UTC())
	if got["scope"] != "container-working-set-approximation" {
		t.Fatalf("scope = %v, want container-working-set-approximation", got["scope"])
	}
	if got["disk_total"] != nil || got["disk_used"] != nil || got["load"] != nil {
		t.Fatalf("unknown or pending values should not appear as zero: %#v", got)
	}
	if got["disk_used_status"] != "pending" || got["disk_total_status"] != "unknown" || got["load_status"] != "unknown" {
		t.Fatalf("metric statuses were not exposed: %#v", got)
	}
	scope, ok := got["resource_scope"].(map[string]string)
	if !ok || scope["mode"] != "container" || scope["load"] != "unknown" {
		t.Fatalf("resource_scope was not forwarded: %#v", got["resource_scope"])
	}
}

func TestNodeResourceViewKeepsLegacyContainerMarker(t *testing.T) {
	report := &v2.Report{
		Disk:    v2.DiskReport{Total: 0, Used: 0},
		Message: "resource_scope=container; load and disk quota unavailable",
	}

	got := nodeResourceView(report, time.Now().UTC())
	if got["scope"] != "container-working-set-approximation" {
		t.Fatalf("legacy scope marker was ignored: %v", got["scope"])
	}
	if got["disk_total"] != nil || got["load"] != nil {
		t.Fatalf("legacy unknown values should not appear as zero: %#v", got)
	}
}

func TestNodeResourceViewPreservesEstimatedMetrics(t *testing.T) {
	report := &v2.Report{
		Disk: v2.DiskReport{Total: 1000, Used: 400},
		Load: v2.LoadReport{Load1: 0.5},
		ResourceScope: map[string]string{
			"disk_used_status":  "estimated",
			"disk_total_status": "estimated",
			"load_status":       "estimated",
		},
	}

	got := nodeResourceView(report, time.Now().UTC())
	if got["disk_total"] != int64(1000) || got["disk_used"] != int64(400) {
		t.Fatalf("estimated disk values should be kept with their status: %#v", got)
	}
	load, ok := got["load"].(gin.H)
	load1, load1OK := load["load1"].(float64)
	if !ok || !load1OK || load1 != 0.5 || got["load_status"] != "estimated" {
		t.Fatalf("estimated load value should be kept with its status: %#v", got)
	}
}
