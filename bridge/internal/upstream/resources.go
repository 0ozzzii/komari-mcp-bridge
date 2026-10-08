package upstream

import (
	"context"
	"encoding/json"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/execution"
	"io"
)

func (c *Client) Resource(ctx context.Context, node string) execution.Resource {
	var out execution.Resource
	if !ValidNode(node) {
		return out
	}
	resp, e := c.Request(ctx, "GET", "/api/admin/mcp/runtime/node/"+node, nil, nil)
	if e != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out) != nil {
			return execution.Resource{}
		}
	}
	return out
}
