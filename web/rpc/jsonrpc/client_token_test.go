package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestAdminClientViewsDoNotSerializeToken(t *testing.T) {
	client := models.Client{UUID: "fixture-id", Token: "fixture-private-token", Name: "fixture-name"}
	out, err := json.Marshal([]adminClientView{clientView(client)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "token") || !strings.Contains(string(out), "fixture-name") {
		t.Fatalf("invalid client view: %s", out)
	}
}

func TestClientTokenCannotBypassSensitiveTransport(t *testing.T) {
	req := &rpc.JsonRpcRequest{Method: clientTokenMethod}
	if !rpc.IsSensitive(req.Method) {
		t.Fatal("client token method must be sensitive")
	}
	_, err := adminGetClientToken(context.Background(), req)
	if err == nil || err.Code != rpc.PermissionDenied {
		t.Fatalf("unverified context reached credential retrieval: %v", err)
	}
	res := dispatchWithSensitive(context.Background(), nil, nil, req)
	if res.Error == nil || res.Error.Code != rpc.PermissionDenied {
		t.Fatalf("anonymous credential retrieval reached the router: %v", res.Error)
	}
}

func TestLegacyContainerScopeKeepsKnownDiskCapacity(t *testing.T) {
	report := v2.Report{Message: "resource_scope=container; fixture"}
	scope := reportResourceScope(&report)
	if scope["load_status"] != "unknown" || scope["disk_total_status"] != "unknown" {
		t.Fatalf("legacy zero-capacity report lost its scope: %v", scope)
	}
	report.Disk.Total = 1024
	if reportResourceScope(&report)["disk_total_status"] != "" {
		t.Fatal("the legacy marker hid a known filesystem capacity")
	}
	report.ResourceScope = map[string]string{"mode": "host"}
	if reportResourceScope(&report)["mode"] != "host" {
		t.Fatal("the legacy marker overrode the structured scope")
	}
}
