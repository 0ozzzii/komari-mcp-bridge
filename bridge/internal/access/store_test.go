package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedAuthenticationRevocationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, token, err := s.Create("agent", []string{"node-a"}, []string{"terminal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 47 {
		t.Fatal("unexpected token size")
	}
	if _, err = s.Authenticate("wrong"); err == nil {
		t.Fatal("invalid token authenticated")
	}
	if _, err = s.Authenticate(token); err != nil {
		t.Fatal(err)
	}
	if s.Require(k.ID, "node-a", "terminal") == nil {
		t.Fatal("execution enabled by default")
	}
	if err = s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err = s.Require(k.ID, "node-a", "terminal"); err != nil {
		t.Fatal(err)
	}
	if s.Require(k.ID, "node-b", "terminal") == nil || s.Require(k.ID, "node-a", "file.write") == nil {
		t.Fatal("scope or permission not enforced")
	}
	_, keys := s.List()
	b, _ := json.Marshal(keys)
	if strings.Contains(string(b), token) || strings.Contains(string(b), "hash") {
		t.Fatal("credential exposed")
	}
	disk, err := os.ReadFile(filepath.Join(dir, "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), token) {
		t.Fatal("plaintext MCP key persisted")
	}
	keys[0].Nodes[0] = "node-b"
	if s.Require(k.ID, "node-b", "terminal") == nil {
		t.Fatal("returned policy mutated store")
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = again.Authenticate(token); err != nil {
		t.Fatal("key failed after reload")
	}
	if err = s.Update(k.ID, []string{"node-a"}, []string{"terminal"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(token); err == nil {
		t.Fatal("revoked key accepted")
	}
	expired := time.Now().Add(-time.Second)
	_, old, err := s.Create("expired", []string{"node-a"}, []string{"terminal"}, &expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(old); err == nil {
		t.Fatal("expired key accepted")
	}
}
