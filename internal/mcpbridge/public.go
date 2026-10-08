package mcpbridge

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterPublic exposes only the daemon's MCP endpoint on the panel domain.
// Caller keys are verified by the daemon; panel login cookies and control
// credentials cannot authorize this endpoint. The daemon retains terminal
// connections independently of these HTTP requests.
func RegisterPublic(r *gin.Engine) {
	handler := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPost && c.Request.Method != http.MethodDelete {
			c.Header("Allow", "GET, POST, DELETE")
			c.AbortWithStatus(http.StatusMethodNotAllowed)
			return
		}
		auth, ok := callerAuthorization(c.Request)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "MCP caller key required"})
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !strings.EqualFold(u.Host, c.Request.Host) || (u.Scheme != "https" && u.Scheme != "http") {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "same-origin MCP request required"})
				return
			}
		}
		upstream, err := url.Parse(os.Getenv("KOMARI_MCP_UPSTREAM_URL"))
		if err != nil || upstream == nil || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || upstream.Path != "/mcp" || upstream.RawPath != "" || (upstream.Scheme != "http" && upstream.Scheme != "https") {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "MCP endpoint is not configured"})
			return
		}
		if c.Request.ContentLength > 256<<10 {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "MCP request too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		proxy := &httputil.ReverseProxy{
			Rewrite: func(p *httputil.ProxyRequest) {
				// The public convenience URL carries a scoped caller credential.
				// Normalize it to Bearer and never relay the secret query upstream.
				query := p.Out.URL.Query()
				query.Del("key")
				p.Out.URL.RawQuery = query.Encode()
				p.Out.Header.Set("Authorization", auth)
				p.Out.URL.Scheme = upstream.Scheme
				p.Out.URL.Host = upstream.Host
				p.Out.URL.Path = "/mcp"
				p.Out.URL.RawPath = ""
				// Keep the SDK's localhost DNS-rebinding guard enabled. Only the
				// internal MCP hop changes Host; panel routes keep their public Host.
				p.Out.Host = upstream.Host
				p.SetXForwarded()
				p.Out.Header.Del("Cookie")
				p.Out.Header.Del("X-2FA-Code")
				p.Out.Header.Del("X-Komari-MCP-Action")
			},
			FlushInterval: -1, // Stream SSE/chunks without buffering whole responses.
			ModifyResponse: func(resp *http.Response) error {
				if resp.StatusCode >= 300 && resp.StatusCode < 400 {
					return errors.New("MCP redirects are not allowed")
				}
				resp.Header.Del("Set-Cookie")
				resp.Header.Set("Cache-Control", "no-store")
				resp.Header.Set("Referrer-Policy", "no-referrer")
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
				http.Error(w, "MCP daemon is unreachable or returned an invalid response", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(c.Writer, c.Request)
	}
	r.Any("/mcp", handler)
	r.Any("/mcp/:caller_key", handler)
}

// Only the public MCP entries accept URL keys. Cookies, Client Tokens and panel
// administrator credentials never substitute for the daemon's scoped key.
// Reject ambiguous credentials rather than quietly choosing a different owner.
func callerAuthorization(r *http.Request) (string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["key"]) > 1 || len(r.Header.Values("Authorization")) > 1 {
		return "", false
	}
	header := r.Header.Get("Authorization")
	key := query.Get("key")
	if _, present := query["key"]; present && key == "" {
		return "", false
	}
	if r.URL.Path != "/mcp" {
		pathKey := strings.TrimPrefix(r.URL.Path, "/mcp/")
		if pathKey == r.URL.Path || len(pathKey) != 47 || !strings.HasPrefix(pathKey, "kmb_") || strings.Contains(pathKey, "/") || (key != "" && key != pathKey) {
			return "", false
		}
		key = pathKey
	}
	if header != "" {
		if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
			return "", false
		}
		if key != "" && header != "Bearer "+key {
			return "", false
		}
		return header, true
	}
	if key == "" {
		return "", false
	}
	return "Bearer " + key, true
}
