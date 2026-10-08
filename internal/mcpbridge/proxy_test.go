package mcpbridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestManagementPreservesAuthentication2FAAndCredentialBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	control := strings.Repeat("c", 32)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+control {
			t.Error("control credential missing")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || r.Header.Get("X-2FA-Code") != "" {
			t.Error("browser credentials forwarded to daemon")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"enabled":true}`)
	}))
	defer daemon.Close()
	t.Setenv("KOMARI_MCP_CONTROL_URL", daemon.URL)
	t.Setenv("KOMARI_MCP_CONTROL_TOKEN", control)
	r := gin.New()
	group := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") != "yes" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	Register(group, func(c *gin.Context) {
		if c.GetHeader("X-2FA-Code") != "123456" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	request := func(method, path, admin, action, otp, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"enabled":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "session_token=not-to-forward")
		req.Header.Set("X-Test-Admin", admin)
		req.Header.Set("X-Komari-MCP-Action", action)
		req.Header.Set("X-2FA-Code", otp)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("GET", "/api/admin/mcp", "", "", "", ""); rec.Code != 401 {
		t.Fatal("anonymous management page accepted")
	}
	if rec := request("GET", "/api/admin/mcp", "yes", "", "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "MCP 工具接入") {
		t.Fatal("management page missing")
	}
	if rec := request("POST", "/api/admin/mcp/control/enabled", "yes", "1", "", ""); rec.Code != 401 || calls.Load() != 0 {
		t.Fatal("2FA verification bypassed")
	}
	if rec := request("POST", "/api/admin/mcp/control/enabled", "yes", "", "123456", ""); rec.Code != 403 {
		t.Fatal("mutation CSRF header not enforced")
	}
	if rec := request("POST", "/api/admin/mcp/control/enabled", "yes", "1", "123456", "https://evil.example"); rec.Code != 403 {
		t.Fatal("cross-origin management accepted")
	}
	if rec := request("POST", "/api/admin/mcp/control/enabled", "yes", "1", "123456", "http://example.com"); rec.Code != 200 || calls.Load() != 1 {
		t.Fatalf("verified administrator request failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request("POST", "/api/admin/mcp/control/policy/update", "yes", "1", "", ""); rec.Code != 401 {
		t.Fatal("execution policy bypassed 2FA")
	}
	if rec := request("POST", "/api/admin/mcp/control/policy/update", "yes", "", "123456", ""); rec.Code != 403 {
		t.Fatal("policy mutation missing CSRF guard")
	}
	if rec := request("POST", "/api/admin/mcp/control/policy/update", "yes", "1", "123456", ""); rec.Code != 200 {
		t.Fatal("verified policy update not forwarded")
	}
	if rec := request("GET", "/api/admin/mcp/control/keys", "yes", "", "", ""); rec.Code != 200 {
		t.Fatal("read-only admin settings failed")
	}
}
func TestControlRedirectAndUnconfiguredDaemon(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("KOMARI_MCP_CONTROL_URL", "")
	t.Setenv("KOMARI_MCP_CONTROL_TOKEN", strings.Repeat("x", 32))
	r := gin.New()
	Register(r.Group("/api/admin"), func(c *gin.Context) { c.Next() })
	req := httptest.NewRequest("GET", "/api/admin/mcp/control/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatal("unconfigured daemon appeared ready")
	}
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer redirect.Close()
	t.Setenv("KOMARI_MCP_CONTROL_URL", redirect.URL)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/admin/mcp/control/status", nil))
	if rec.Code != 502 || leaked.Load() != 0 {
		t.Fatal("control credential followed a redirect")
	}
}

func TestLogReadsKeepAdministratorBoundaryAndForwardOnlyLogQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	control := strings.Repeat("x", 32)
	query := "node_uuid=node-a&event=command_state&cursor=opaque%2Fcursor&limit=50"
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.RawQuery != query || r.Header.Get("Authorization") != "Bearer "+control || r.Header.Get("Cookie") != "" {
			t.Error("log query or credential boundary changed")
		}
		io.WriteString(w, `{"records":[]}`)
	}))
	defer daemon.Close()
	t.Setenv("KOMARI_MCP_CONTROL_URL", daemon.URL)
	t.Setenv("KOMARI_MCP_CONTROL_TOKEN", control)
	r := gin.New()
	Register(r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") != "yes" {
			c.AbortWithStatus(401)
		}
	}), func(c *gin.Context) { t.Error("read invoked mutation 2FA"); c.AbortWithStatus(401) })
	for _, path := range []string{"/api/admin/mcp/log-viewer.js", "/api/admin/mcp/control/logs?" + query, "/api/admin/mcp/control/logs/output?" + query} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatal("anonymous log reader accepted")
		}
		req.Header.Set("X-Test-Admin", "yes")
		req.Header.Set("Cookie", "session_token=private")
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("authenticated log read failed: %d", rec.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected upstream reads")
	}
	req := httptest.NewRequest("GET", "/api/admin/mcp/control/logs?cursor="+url.QueryEscape(strings.Repeat("c", 4097)), nil)
	req.Header.Set("X-Test-Admin", "yes")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 400 || calls.Load() != 2 {
		t.Fatal("oversize log query forwarded")
	}
}
