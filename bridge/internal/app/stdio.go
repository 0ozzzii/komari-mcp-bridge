package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type callerTransport struct{ token string }

func (t callerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(copy)
}

// RunStdio is only an adapter. Its exit closes the MCP HTTP client, while the
// independent daemon continues owning PTYs and reading remote output.
func RunStdio(ctx context.Context, endpoint, key string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || key == "" {
		return errors.New("valid BRIDGE_MCP_URL and KOMARI_MCP_CALLER_KEY required")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "komari-mcp-stdio-adapter", Version: "0.1.0"}, nil)
	httpClient := &http.Client{Transport: callerTransport{key}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return errors.New("MCP daemon connection failed")
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return errors.New("MCP daemon tool discovery failed")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "komari-mcp-stdio", Version: "0.1.0"}, nil)
	for _, tool := range tools.Tools {
		name := tool.Name
		server.AddTool(tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arguments := request.Params.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
		})
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}
