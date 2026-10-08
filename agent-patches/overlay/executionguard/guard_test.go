package executionguard

import (
	"sync"
	"testing"
)

func TestSharedSlotsMaintenanceAndPressure(t *testing.T) {
	c := New()
	c.Resource = func() Resource { return Resource{MemoryTotal: 256 << 20, MemoryUsed: 100 << 20} }
	var mu sync.Mutex
	releases := []func(){}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, e := c.Reserve(string(rune('A'+i)), false)
			if e == nil {
				mu.Lock()
				releases = append(releases, release)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(releases) != 5 {
		t.Fatalf("shared cap not enforced: %d", len(releases))
	}
	repair, e := c.Reserve("repair", true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Reserve("repair2", true); e == nil {
		t.Fatal("maintenance unbounded")
	}
	repair()
	p := DefaultOptions()
	p.MaxParallel = 1
	c.Configure(p)
	if _, e = c.Reserve("new", false); e == nil {
		t.Fatal("lowered cap freed occupied slots")
	}
	for _, f := range releases {
		f()
		f()
	}
	c.Resource = func() Resource { return Resource{MemoryTotal: 256 << 20, MemoryUsed: 250 << 20} }
	if _, e = c.Reserve("low", false); e == nil {
		t.Fatal("pressure admitted new work")
	}
	if f, e := c.Reserve("rescue", true); e != nil {
		t.Fatal(e)
	} else {
		f()
	}
}

func TestOldConnectionCannotRestoreOlderPolicy(t *testing.T) {
	c := New()
	p := DefaultOptions()
	p.Revision = 5
	p.MaxParallel = 1
	if e := c.Configure(p); e != nil {
		t.Fatal(e)
	}
	stale := p
	stale.Revision = 4
	stale.MaxParallel = 12
	if e := c.Configure(stale); e != nil {
		t.Fatal(e)
	}
	if c.Settings().MaxParallel != 1 {
		t.Fatal("old reconnect raised hard maximum")
	}
}
