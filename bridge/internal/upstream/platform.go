package upstream

import (
	"context"
	"encoding/json"
	"errors"
)

// NodeOS reads only a selected node's platform; client tokens are not retained.
func (c *Client) NodeOS(ctx context.Context, node string) (string, error) {
	if !ValidNode(node) {
		return "", errors.New("invalid node")
	}
	raw, err := c.RPC(ctx, "admin:listClients", map[string]any{})
	if err != nil {
		return "", err
	}
	var nodes []struct {
		UUID string `json:"uuid"`
		OS   string `json:"os"`
	}
	if err = json.Unmarshal(raw, &nodes); err != nil {
		return "", errors.New("invalid platform metadata")
	}
	for _, n := range nodes {
		if n.UUID == node {
			return n.OS, nil
		}
	}
	return "", errors.New("node platform unavailable")
}
