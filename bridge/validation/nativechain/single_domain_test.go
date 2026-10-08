//go:build linux

package nativechain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/app"
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
	panelrouter "github.com/komari-monitor/komari/web/router"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type publicHostTransport struct{ token string }

func (a publicHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Host = "komari.example.com"
	if a.token != "" {
		c.Header.Set("Authorization", "Bearer "+a.token)
	}
	return http.DefaultTransport.RoundTrip(c)
}

// Uses the actual panel router, bridge scoped-key authentication and official
// SDK. No Cloudflare tunnel, Windows service or remote probe is involved.
func TestSingleDomainActualRouterAndMCP(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := app.New(ctx, app.Config{BaseURL: "http://127.0.0.1:1", APIKey: "unused-local-fixture", ControlToken: strings.Repeat("x", 32), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, token, err := a.Policy.Create("single-domain", []string{"isolated-test-node"}, []string{"terminal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Policy.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(a.Handler())
	defer daemon.Close()
	t.Setenv("KOMARI_MCP_UPSTREAM_URL", daemon.URL+"/mcp")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	panelrouter.Register(r)
	front := httptest.NewServer(r)
	defer front.Close()
	for _, credential := range []string{"", "invalid-key", "unused-local-fixture"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", front.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Host = "komari.example.com"
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("unauthorized or administrator credential accepted: HTTP %d", resp.StatusCode)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", front.URL+"/mcp", strings.NewReader(strings.Repeat("x", (256<<10)+1)))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("oversized MCP body not rejected: HTTP %d", resp.StatusCode)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "single-domain-test", Version: "1"}, nil)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: front.URL + "/mcp", HTTPClient: &http.Client{Transport: publicHostTransport{token}}}, nil)
	if err != nil {
		t.Fatalf("single-domain MCP handshake (SDK localhost protection remains enabled): %v", err)
	}
	defer s.Close()
	list, err := s.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 15 {
		t.Fatalf("single-domain tools/list: %v %v", list, err)
	}
	// The convenience endpoint is a complete credential: the client adds no
	// Authorization header. Both variants exercise the actual panel middleware.
	for _, path := range []string{"/mcp/" + token, "/mcp?key=" + token} {
		urlSession, connectErr := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: front.URL + path, HTTPClient: &http.Client{Transport: publicHostTransport{}}}, nil)
		if connectErr != nil {
			t.Fatal("URL-only MCP handshake failed")
		}
		urlList, listErr := urlSession.ListTools(ctx, nil)
		if listErr != nil || len(urlList.Tools) != 15 {
			t.Fatal("URL-only MCP tools/list failed")
		}
		urlSession.Close()
	}
	if err = a.Policy.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	result, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "komari_capabilities", Arguments: map[string]any{}})
	if err != nil || !result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "MCP_DISABLED" {
		t.Fatalf("MCP toggle did not deny tool execution through panel proxy: %v %v", result, err)
	}
	t.Log("LOCAL INTEGRATION: actual panel /mcp + daemon key authentication + official SDK; external Host, 15 tools and disable switch verified; no remote execution")
}
