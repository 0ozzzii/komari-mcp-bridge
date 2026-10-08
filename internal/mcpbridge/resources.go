package mcpbridge

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	agent "github.com/komari-monitor/komari/web/agent"
	"strings"
	"time"
)

func nodeResource(c *gin.Context) {
	id := c.Param("uuid")
	if _, e := clients.GetClientByUUID(id); e != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	v := gin.H{"memory_total": uint64(0), "memory_used": uint64(0), "fresh": false, "scope": "unknown"}
	if r := agent.GetLatestReport()[id]; r != nil {
		v["memory_total"] = r.Ram.Total
		v["memory_used"] = r.Ram.Used
		v["updated_at"] = r.UpdatedAt
		v["fresh"] = !r.UpdatedAt.IsZero() && time.Since(r.UpdatedAt) >= 0 && time.Since(r.UpdatedAt) < 30*time.Second
		v["scope"] = "reported-memory-approximation"
		if strings.Contains(r.Message, "resource_scope=container") {
			v["scope"] = "container-working-set-approximation"
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, v)
}
