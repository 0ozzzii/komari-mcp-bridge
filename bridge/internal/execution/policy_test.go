package execution

import (
	"testing"
	"time"
)

func TestTiersLeaseExpiryAndRestart(t *testing.T) {
	for _, v := range []struct {
		bytes uint64
		want  int
	}{{256 << 20, 5}, {1 << 30, 8}, {2 << 30, 10}, {3 << 30, 12}, {0, 1}} {
		if n := Capacity(Resource{MemoryTotal: v.bytes, Fresh: true}); n != v.want {
			t.Fatalf("%d: %d", v.bytes, n)
		}
	}
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	r := Resource{MemoryTotal: 256 << 20, Fresh: true}
	l, e := s.Set("key", "node", "work", 2, r)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(9 * time.Minute)
	for range 20 {
		s.Get("key", "node", "work")
		s.Effective("key", "node", r)
		s.Snapshot()
	}
	after, _ := s.Get("key", "node", "work")
	if !after.ExpiresAt.Equal(l.ExpiresAt) {
		t.Fatal("read renewed lease")
	}
	s.Renew("key", "node", "work")
	renewed, _ := s.Get("key", "node", "work")
	if renewed.LastDispatch == nil || !renewed.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatal("forward dispatch failed to renew")
	}
	now = now.Add(11 * time.Minute)
	s.Renew("key", "node", "work")
	if _, e = s.Get("key", "node", "work"); e == nil {
		t.Fatal("expired lease resurrected")
	}
	restored, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restored.Get("key", "node", "work"); e == nil {
		t.Fatal("restart restored authorization lease")
	}
}
func TestOverridesClampAndPersist(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	node := Defaults()
	node.MaxParallel = 5
	node.AllowMaintenance = true
	key := Defaults()
	key.MaxParallel = 2
	key.MaxTimeoutSeconds = 3600
	key.AllowMaintenance = true
	if e := s.Update("node", "n", &node); e != nil {
		t.Fatal(e)
	}
	if e := s.Update("key", "k", &key); e != nil {
		t.Fatal(e)
	}
	p, hard := s.Effective("k", "n", Resource{})
	if hard != 2 || p.MaxTimeoutSeconds != 3600 || !p.AllowMaintenance {
		t.Fatal(p, hard)
	}
	if _, e := s.Set("k", "n", "w", 3, Resource{}); e == nil {
		t.Fatal("key bypassed device policy")
	}
	s2, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, hard = s2.Effective("other", "n", Resource{})
	if hard != 5 {
		t.Fatal("saved device override missing")
	}
	bad := node
	bad.MaxTimeoutSeconds = 1
	if e = s.Update("node", "n", &bad); e == nil {
		t.Fatal("bad bounds accepted")
	}
	if !LowMemory(Resource{Fresh: true, MemoryTotal: 256 << 20, MemoryUsed: 250 << 20}, node) {
		t.Fatal("memory pressure missed")
	}
}

func TestForceCloseRequiresNodeAndKeyExplicitAuthorization(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	node := Defaults()
	node.ForceCloseOnTimeout = true
	node.AllowMaintenance = true
	if e = s.Update("node", "n", &node); e != nil {
		t.Fatal(e)
	}
	p, _ := s.Effective("unconfigured-key", "n", Resource{})
	if p.ForceCloseOnTimeout || p.AllowMaintenance {
		t.Fatal("inheriting Key gained privileged close or maintenance")
	}
	key := Defaults()
	key.ForceCloseOnTimeout = true
	if e = s.Update("key", "k", &key); e != nil {
		t.Fatal(e)
	}
	p, _ = s.Effective("k", "n", Resource{})
	if !p.ForceCloseOnTimeout {
		t.Fatal("explicit dual authorization lost")
	}
	remote, _ := s.NodeSettings("n", Resource{})
	if !remote.ForceCloseOnTimeout {
		t.Fatal("node-side capability incorrectly inherited caller restrictions")
	}
	node.ForceCloseOnTimeout = false
	s.Update("node", "n", &node)
	p, _ = s.Effective("k", "n", Resource{})
	if p.ForceCloseOnTimeout {
		t.Fatal("Key overrode node denial")
	}
}
