package mcpbridge

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/plugin"
)

func TestPublicMCPStreamsAndSeparatesCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Host, "127.0.0.1:") {
			t.Error("public Host reached the loopback MCP listener")
		}
		if r.Header.Get("Authorization") != "Bearer scoped-key" || r.Header.Get("MCP-Protocol-Version") != "2025-11-25" || r.URL.Path != "/mcp" || r.URL.RawQuery != "cursor=one" {
			t.Error("MCP request metadata changed")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-2FA-Code") != "" || r.Header.Get("X-Komari-MCP-Action") != "" {
			t.Error("panel credentials leaked to public MCP upstream")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "not-a-panel-cookie=1")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
			io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	t.Setenv("KOMARI_MCP_UPSTREAM_URL", upstream.URL+"/mcp")
	r := gin.New()
	RegisterPublic(r)
	panel := httptest.NewServer(plugin.HTMLInjectHandler(plugin.WrapHandler(r)))
	defer panel.Close()

	for _, auth := range []string{"", "Bearer "} {
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Cookie", "session_token=admin-cookie")
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 || calls.Load() != 0 {
			t.Fatal("panel cookie authorized MCP without a caller key")
		}
	}
	req, _ := http.NewRequest("POST", panel.URL+"/mcp?cursor=one", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Host = "komari.example.com"
	req.Header.Set("Authorization", "Bearer scoped-key")
	req.Header.Set("Cookie", "session_token=private")
	req.Header.Set("X-2FA-Code", "123456")
	req.Header.Set("X-Komari-MCP-Action", "1")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("invalid streamed response headers")
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first output not delivered before upstream completion: %q %v", line, err)
	}
}

func TestPublicMCPFailClosedAndDoesNotFollowRedirects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPublic(r)
	request := func(path, origin string) *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := httptest.NewRequest("POST", path, nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer scoped-key")
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	// Synthetic userinfo verifies rejection; it is not a deployable credential.
	credentialURL := (&url.URL{Scheme: "http", Host: "localhost", Path: "/mcp", User: url.UserPassword("user", "secret")}).String()
	for _, base := range []string{"", "http://127.0.0.1:8968/control/status", credentialURL, "http://localhost/mcp?key=secret"} {
		t.Setenv("KOMARI_MCP_UPSTREAM_URL", base)
		if request("/mcp", "").Code != 503 {
			t.Fatal("invalid upstream configuration accepted")
		}
	}
	var calls atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer upstream.Close()
	t.Setenv("KOMARI_MCP_UPSTREAM_URL", upstream.URL+"/mcp")
	if request("/mcp", "https://evil.example").Code != 403 {
		t.Fatal("cross-origin MCP accepted")
	}
	if request("/mcp", "http://example.com").Code != 502 || calls.Load() != 0 {
		t.Fatal("upstream redirect exposed a caller credential")
	}
	for _, path := range []string{"/mcp/control/status", "/control/status"} {
		if request(path, "").Code != 404 {
			t.Fatal("public proxy exposed a control route")
		}
	}
}

func TestPublicMCPURLKeysAreNormalizedAndAmbiguityRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := "kmb_" + strings.Repeat("a", 43)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+key || r.URL.Path != "/mcp" || r.URL.RawQuery != "cursor=one" || strings.Contains(r.RequestURI, key) {
			t.Error("URL key was not normalized to a private Bearer header")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("KOMARI_MCP_UPSTREAM_URL", upstream.URL+"/mcp")
	r := gin.New()
	RegisterPublic(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, path := range []string{"/mcp/" + key + "?cursor=one", "/mcp?key=" + key + "&cursor=one"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil).WithContext(ctx))
		if w.Code != 200 || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("URL-only MCP request rejected")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("URL requests did not reach the daemon")
	}
	for _, path := range []string{"/mcp?key=", "/mcp?key=" + key + "&key=" + key, "/mcp/" + key + "?key=other", "/mcp?key=%GG"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 401 {
			t.Fatal("ambiguous URL credential accepted")
		}
	}
	for _, auth := range [][]string{{"Bearer other"}, {"Bearer " + key, "Bearer " + key}} {
		req := httptest.NewRequest("POST", "/mcp/"+key, nil)
		for _, value := range auth {
			req.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatal("ambiguous header/path credential accepted")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("rejected credentials reached the daemon")
	}
}
