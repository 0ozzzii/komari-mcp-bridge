// Package mcpbridge provides the optional Komari administrator surface. Remote
// terminal ownership stays in the independent bridge daemon, not in the panel.
package mcpbridge

import (
	"bytes"
	_ "embed"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed management.html
var page []byte

//go:embed log-viewer.js
var logViewer []byte

// Register receives the panel's existing sensitive-operation middleware so
// Cookie administrators and API-key administrators keep official 2FA behavior.
func Register(admin *gin.RouterGroup, sensitive2FA gin.HandlerFunc) {
	admin.GET("/mcp", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
		c.Data(200, "text/html; charset=utf-8", page)
	})
	admin.GET("/mcp/log-viewer.js", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(200, "text/javascript; charset=utf-8", logViewer)
	})
	admin.GET("/mcp/control/nodes", listNodes)
	admin.GET("/mcp/runtime/node/:uuid", nodeResource)
	for _, action := range []string{"status", "keys", "policy", "logs", "logs/output"} {
		admin.GET("/mcp/control/"+action, proxy)
	}
	for _, action := range []string{"enabled", "keys/create", "keys/update", "policy/update"} {
		admin.POST("/mcp/control/"+action, protectMutation, sensitive2FA, proxy)
	}
}
func protectMutation(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	if c.GetHeader("X-Komari-MCP-Action") != "1" {
		c.AbortWithStatusJSON(403, gin.H{"error": "management action header required"})
		return
	}
	if origin := c.GetHeader("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, c.Request.Host) || (u.Scheme != "https" && u.Scheme != "http") {
			c.AbortWithStatusJSON(403, gin.H{"error": "same-origin management request required"})
			return
		}
	}
	c.Next()
}
func proxy(c *gin.Context) {
	base, err := url.Parse(os.Getenv("KOMARI_MCP_CONTROL_URL"))
	token := os.Getenv("KOMARI_MCP_CONTROL_TOKEN")
	if err != nil || base == nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") || len(token) < 32 {
		c.AbortWithStatusJSON(503, gin.H{"error": "MCP management daemon is not configured"})
		return
	}
	action := strings.TrimPrefix(c.FullPath(), "/api/admin/mcp/control/")
	if action == c.FullPath() {
		c.AbortWithStatus(404)
		return
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/control/" + action
	if action == "logs" || action == "logs/output" {
		if len(c.Request.URL.RawQuery) > 4096 {
			c.AbortWithStatusJSON(400, gin.H{"error": "log query too large"})
			return
		}
		base.RawQuery = c.Request.URL.RawQuery
	}
	var body []byte
	if c.Request.Method == "POST" {
		body, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(413, gin.H{"error": "management request too large"})
			return
		}
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, base.String(), bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(502, gin.H{"error": "MCP management daemon is unreachable"})
		return
	}
	defer resp.Body.Close()
	output, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(output) > 2<<20 {
		c.AbortWithStatusJSON(502, gin.H{"error": "invalid management response"})
		return
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		c.AbortWithStatusJSON(502, gin.H{"error": "management redirects are not followed"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(resp.StatusCode, "application/json", output)
}
