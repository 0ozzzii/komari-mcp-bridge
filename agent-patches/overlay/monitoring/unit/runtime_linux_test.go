//go:build linux

package monitoring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestContainerRootFSUsage(t *testing.T) {
	root := t.TempDir()
	skipped := filepath.Join(root, "proc")
	if err := os.Mkdir(skipped, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "data"), filepath.Join(skipped, "excluded")} {
		if err := os.WriteFile(path, make([]byte, 64*1024), 0600); err != nil {
			t.Fatal(err)
		}
	}
	excluded := map[string]bool{skipped: true}
	want, partial, err := scanRootFS(t.Context(), root, excluded)
	if err != nil || partial || want < 64*1024 || want >= 128*1024 {
		t.Fatalf("rootfs: bytes=%d partial=%t err=%v", want, partial, err)
	}
	if err := os.Link(filepath.Join(root, "data"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if got, partial, err := scanRootFS(t.Context(), root, excluded); err != nil || partial || got != want {
		t.Fatalf("hardlink doubled disk usage: got=%d want=%d partial=%t err=%v", got, want, partial, err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if _, _, err := scanRootFS(ctx, root, excluded); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline ignored: %v", err)
	}
}

func TestContainerDiskCachePreservesLastSampleOnTimeout(t *testing.T) {
	sampler := diskSampler{scan: func(context.Context) (uint64, bool, error) { return 1024, true, nil }}
	sampler.refresh()
	first := sampler.sample(time.Now())
	if first.Total != 0 || first.Used != 1024 || first.Status != "partial" {
		t.Fatalf("partial sample lost: %+v", first)
	}
	sampler.scan = func(context.Context) (uint64, bool, error) { return 0, false, context.DeadlineExceeded }
	sampler.refresh()
	if got := sampler.sample(time.Now()); got.Used != first.Used || got.Total != 0 || got.Status != "stale" {
		t.Fatalf("timeout did not preserve sample: %+v", got)
	}
}

func TestAndroidAndMissingCgroupPolicy(t *testing.T) {
	oldScope, oldProc, oldAndroid := flags.ResourceScope, flags.HostProc, androidRuntime
	t.Cleanup(func() { flags.ResourceScope, flags.HostProc, androidRuntime = oldScope, oldProc, oldAndroid })
	androidRuntime = func() bool { return true }
	flags.HostProc = ""
	flags.ResourceScope = "auto"
	if ContainerResourceScope() || cgroupMetricMode(false) != "host-fallback" {
		t.Fatal("Android auto or missing auto cgroup must use host readings")
	}
	flags.ResourceScope = "container"
	if !ContainerResourceScope() || cgroupMetricMode(false) != "cgroup-unavailable" || cgroupMetricMode(true) != "cgroup" {
		t.Fatal("explicit container mode must preserve its boundary")
	}
}

func TestAndroidLoadMountAndUptime(t *testing.T) {
	oldScope, oldAndroid, oldStart := flags.ResourceScope, androidRuntime, agentStarted
	t.Cleanup(func() { flags.ResourceScope, androidRuntime, agentStarted = oldScope, oldAndroid, oldStart })
	androidRuntime = func() bool { return true }
	flags.ResourceScope = "host"
	agentStarted = time.Now().Add(-40 * time.Second)
	if !LoadIsUnisolated() || Load() != (LoadInfo{}) {
		t.Fatal("Android load must be omitted")
	}
	if got, err := Uptime(); err != nil || got < 40 || got > 42 {
		t.Fatalf("expected the agent process age: got %d, err=%v", got, err)
	}
	if got := diskMountpoints(" / ; /data ; /other ", true); !reflect.DeepEqual(got, []string{"/data", "/other"}) {
		t.Fatalf("Android mounts: %v", got)
	}
	if got := diskMountpoints("/", false); !reflect.DeepEqual(got, []string{"/"}) {
		t.Fatalf("regular Linux root was changed: %v", got)
	}
}

func TestContainerDiskDeadlineWhileWorkerBlocked(t *testing.T) {
	now := time.Now()
	sampler := diskSampler{active: true, deadline: now.Add(-time.Second)}
	if got := sampler.sample(now); got.Status != "unavailable" {
		t.Fatalf("blocked scan should become unavailable: %+v", got)
	}
	sampler.last = DiskInfo{Used: 42, Status: "estimated"}
	sampler.have = true
	if got := sampler.sample(now); got.Status != "stale" || got.Used != 42 {
		t.Fatalf("blocked refresh should preserve the previous sample: %+v", got)
	}
}
