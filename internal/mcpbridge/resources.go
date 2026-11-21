package mcpbridge

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent "github.com/komari-monitor/komari/web/agent"
)

func nodeResource(c *gin.Context) {
	id := c.Param("uuid")
	if _, e := clients.GetClientByUUID(id); e != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	v := nodeResourceView(agent.GetLatestReport()[id], time.Now())
	c.Header("Cache-Control", "no-store")
	c.JSON(200, v)
}

func nodeResourceView(r *v2.Report, now time.Time) gin.H {
	v := gin.H{
		"memory_total": uint64(0),
		"memory_used":  uint64(0),
		"fresh":        false,
		"scope":        "unknown",
	}
	if r == nil {
		return v
	}

	v["memory_total"] = r.Ram.Total
	v["memory_used"] = r.Ram.Used
	v["disk_used"] = r.Disk.Used
	v["disk_total"] = r.Disk.Total
	v["load"] = gin.H{"load1": r.Load.Load1, "load5": r.Load.Load5, "load15": r.Load.Load15}
	v["updated_at"] = r.UpdatedAt
	if !r.UpdatedAt.IsZero() {
		age := now.Sub(r.UpdatedAt)
		v["fresh"] = age >= 0 && age < 30*time.Second
	}
	v["scope"] = "reported-memory-approximation"
	if r.ResourceScope["mode"] == "container" || (r.ResourceScope["mode"] == "" && strings.Contains(r.Message, "resource_scope=container")) {
		v["scope"] = "container-working-set-approximation"
	}
	if len(r.ResourceScope) > 0 {
		scope := make(map[string]string, len(r.ResourceScope))
		for key, value := range r.ResourceScope {
			scope[key] = value
		}
		v["resource_scope"] = scope
	}

	diskUsedStatus := reportMetricStatus(r.ResourceScope, "disk_used_status", "disk_used")
	diskTotalStatus := reportMetricStatus(r.ResourceScope, "disk_total_status", "disk_total")
	loadStatus := reportMetricStatus(r.ResourceScope, "load_status", "load")
	if diskUsedStatus != "" {
		v["disk_used_status"] = diskUsedStatus
	}
	if diskTotalStatus != "" {
		v["disk_total_status"] = diskTotalStatus
	}
	if loadStatus != "" {
		v["load_status"] = loadStatus
	}
	legacyContainerUnknown := strings.Contains(r.Message, "resource_scope=container") && len(r.ResourceScope) == 0
	if metricUnavailable(diskUsedStatus) {
		v["disk_used"] = nil
	}
	if metricUnavailable(diskTotalStatus) || strings.EqualFold(strings.TrimSpace(r.ResourceScope["disk_total"]), "unknown") || (legacyContainerUnknown && r.Disk.Total == 0) {
		v["disk_total"] = nil
	}
	if metricUnavailable(loadStatus) || strings.EqualFold(strings.TrimSpace(r.ResourceScope["load"]), "unknown") || legacyContainerUnknown {
		v["load"] = nil
	}
	return v
}

func reportMetricStatus(scope map[string]string, key, legacyKey string) string {
	if status := strings.TrimSpace(scope[key]); status != "" {
		return status
	}
	return strings.TrimSpace(scope[legacyKey])
}

func metricUnavailable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unknown", "pending", "unavailable", "not-available", "failed", "error", "timeout", "timed-out", "permission-denied", "cgroup-unavailable":
		return true
	default:
		return false
	}
}
