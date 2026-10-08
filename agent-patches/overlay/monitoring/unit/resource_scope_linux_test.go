//go:build linux

package monitoring

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func fixtureReader(membership, mountinfo string, files map[string]string) *cgroupReader {
	locations, err := resolveCgroups(membership, mountinfo)
	if err != nil {
		panic(err)
	}
	return &cgroupReader{locations: locations, logicalCPUs: 16, read: func(path string) ([]byte, error) {
		if text, ok := files[path]; ok {
			return []byte(text), nil
		}
		return nil, os.ErrNotExist
	}}
}

func TestCgroupV2ContainerMemoryAndParentQuota(t *testing.T) {
	r := fixtureReader("0::/tenant/work\n", "29 23 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", map[string]string{
		"/sys/fs/cgroup/tenant/work/memory.current":        "800",
		"/sys/fs/cgroup/tenant/work/memory.max":            "max",
		"/sys/fs/cgroup/tenant/memory.max":                 "1000",
		"/sys/fs/cgroup/memory.max":                        "4000",
		"/sys/fs/cgroup/tenant/work/memory.stat":           "inactive_file 200\n",
		"/sys/fs/cgroup/tenant/work/cpu.max":               "max 100000",
		"/sys/fs/cgroup/tenant/cpu.max":                    "50000 100000",
		"/sys/fs/cgroup/tenant/work/cpuset.cpus.effective": "0-3",
		"/sys/fs/cgroup/tenant/work/cpu.stat":              "usage_usec 1234\nuser_usec 1000\n",
	})
	ram, warning := r.memory(false)
	if ram.Total != 1000 || ram.Used != 600 || warning != "" {
		t.Fatalf("memory: %+v %q", ram, warning)
	}
	withCache, _ := r.memory(true)
	if withCache.Used != 800 {
		t.Fatal(withCache)
	}
	if capacity := r.cpuCapacity(); capacity != 0.5 {
		t.Fatalf("capacity=%v", capacity)
	}
	usage, group, err := r.cpuTime()
	if err != nil || usage != 1234000 || group != "/sys/fs/cgroup/tenant/work" {
		t.Fatalf("usage=%d group=%q err=%v", usage, group, err)
	}
}

func TestCgroupNamespaceRootDoesNotAppendHostPath(t *testing.T) {
	r := fixtureReader("0::/\n", "29 23 0:26 /docker/id /sys/fs/cgroup rw - cgroup2 cgroup rw\n", map[string]string{
		"/sys/fs/cgroup/memory.current": "500", "/sys/fs/cgroup/memory.max": "600", "/sys/fs/cgroup/memory.stat": "inactive_file 100\n",
	})
	ram, _ := r.memory(false)
	if ram.Total != 600 || ram.Used != 400 {
		t.Fatal(ram)
	}
}

func TestCgroupV1MemorySwapAndCPU(t *testing.T) {
	r := fixtureReader("4:memory:/docker/id\n5:cpu,cpuacct:/docker/id\n6:cpuset:/docker/id\n", strings.Join([]string{
		"29 23 0:26 /docker/id /cg/memory rw - cgroup cgroup rw,memory",
		"30 23 0:27 /docker/id /cg/cpu rw - cgroup cgroup rw,cpu,cpuacct",
		"31 23 0:28 /docker/id /cg/cpuset rw - cgroup cgroup rw,cpuset",
	}, "\n"), map[string]string{
		"/cg/memory/memory.usage_in_bytes": "700", "/cg/memory/memory.limit_in_bytes": "1000", "/cg/memory/memory.stat": "inactive_file 10\ntotal_inactive_file 100\n",
		"/cg/memory/memory.memsw.usage_in_bytes": "750", "/cg/memory/memory.memsw.limit_in_bytes": "1200",
		"/cg/cpu/cpu.cfs_quota_us": "200000", "/cg/cpu/cpu.cfs_period_us": "100000", "/cg/cpu/cpuacct.usage": "10000", "/cg/cpuset/cpuset.cpus": "1-2,4",
	})
	ram, _ := r.memory(false)
	if ram.Total != 1000 || ram.Used != 600 {
		t.Fatal(ram)
	}
	swap := r.swap()
	if swap.Total != 200 || swap.Used != 50 {
		t.Fatal(swap)
	}
	if n := r.cpuCapacity(); n != 2 {
		t.Fatal(n)
	}
	u, _, err := r.cpuTime()
	if err != nil || u != 10000 {
		t.Fatalf("usage=%d err=%v", u, err)
	}
}

func TestUnlimitedAndUnreadableCgroupsNeverUseHostMemory(t *testing.T) {
	for _, limit := range []string{"max", "9223372036854771712", "garbage"} {
		t.Run(limit, func(t *testing.T) {
			r := fixtureReader("0::/", "29 23 0:26 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.current": "50", "/cg/memory.max": limit})
			m, w := r.memory(false)
			if m.Total != 0 || m.Used != 50 || w == "" {
				t.Fatalf("memory=%+v warning=%q", m, w)
			}
		})
	}
	r := fixtureReader("0::/", "29 23 0:26 / /cg rw - cgroup2 cgroup rw", nil)
	m, w := r.memory(false)
	if m.Total != 0 || m.Used != 0 || w == "" {
		t.Fatalf("%+v %q", m, w)
	}
}

func TestCgroupWorkingSetUnderflowAndUsageClamp(t *testing.T) {
	r := fixtureReader("0::/", "29 23 0:26 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.current": "100", "/cg/memory.max": "80", "/cg/memory.stat": "inactive_file 200\n"})
	m, _ := r.memory(false)
	if m.Used != 0 {
		t.Fatal(m)
	}
	m, _ = r.memory(true)
	if m.Used != 80 {
		t.Fatal(m)
	}
}

func TestCPUSetValidation(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int
		bad   bool
	}{{"0-3,2,5", 5, false}, {"2", 1, false}, {"3-1", 0, true}, {"-1", 0, true}, {"0-999999999", 0, true}, {"", 0, true}, {"1,foo", 0, true}} {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			n, e := cpusetCount(tc.input)
			if (e != nil) != tc.bad || (!tc.bad && n != tc.want) {
				t.Fatalf("n=%d err=%v", n, e)
			}
		})
	}
}

func TestCgroupMembershipTraversalRejected(t *testing.T) {
	if _, err := resolveCgroups("0::/../../host", "29 23 0:26 / /cg rw - cgroup2 cgroup rw"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestResourceScopeOverride(t *testing.T) {
	oldScope, oldProc := flags.ResourceScope, flags.HostProc
	defer func() { flags.ResourceScope, flags.HostProc = oldScope, oldProc }()
	flags.ResourceScope = "container"
	flags.HostProc = "/host/proc"
	if !ContainerResourceScope() {
		t.Fatal("explicit container ignored")
	}
	flags.ResourceScope = "host"
	if ContainerResourceScope() {
		t.Fatal("explicit host ignored")
	}
	flags.ResourceScope = "auto"
	if ContainerResourceScope() {
		t.Fatal("HOST_PROC host intent ignored")
	}
}
