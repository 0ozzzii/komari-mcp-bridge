package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/access"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/upstream"
)

func TestAmbiguousFileMutationDoesNotReplayOrCrossScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer server.Close()
	dir := t.TempDir()
	p, _ := access.Open(dir)
	key, _, _ := p.Create("file", []string{"node-a"}, []string{"file.write"}, nil)
	p.SetEnabled(true)
	up, _ := upstream.New(server.URL, "test-key")
	s := &Service{Policy: p, Upstream: up, Dir: filepath.Join(dir, "mutations")}
	args := Arguments{Node: "node-a", Action: "delete", Path: "/tmp/example", OperationID: "one"}
	for range 2 {
		v, err := s.filesystem(context.Background(), key, args)
		if err != nil {
			t.Fatal(err)
		}
		if !v.(map[string]any)["execution_uncertain"].(bool) {
			t.Fatal("ambiguous deletion reported as certain")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("uncertain file mutation automatically replayed")
	}
	args.Node = "node-b"
	if _, err := s.filesystem(context.Background(), key, args); err == nil {
		t.Fatal("node scope bypassed by reused operation ID")
	}
	args.Node = "node-a"
	args.Path = "/different"
	if _, err := s.filesystem(context.Background(), key, args); err == nil {
		t.Fatal("operation ID accepted conflicting mutation")
	}
}

// Conservative mixed-tool annotations must not replace action-level grants.
func TestReadOnlyFileKeyCannotReachMutatingUpstream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	dir := t.TempDir()
	policy, err := access.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := policy.Create("read-only", []string{"node-a"}, []string{"file.read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy.SetEnabled(true)
	up, err := upstream.New(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Policy: policy, Upstream: up, Dir: filepath.Join(dir, "mutations")}
	for _, action := range []string{"mkdir", "delete", "move", "copy", "chmod", "write"} {
		_, err := s.filesystem(context.Background(), key, Arguments{Node: "node-a", Action: action, Path: "/example", Source: "/example", Destination: "/other", OperationID: action})
		if err == nil {
			t.Fatalf("read-only key accepted %s", action)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("mutation reached upstream before authorization")
	}
	_, _ = s.filesystem(context.Background(), key, Arguments{Node: "node-a", Action: "stat", Path: "/example"})
	if calls.Load() != 1 {
		t.Fatal("authorized read did not reach upstream")
	}
}
