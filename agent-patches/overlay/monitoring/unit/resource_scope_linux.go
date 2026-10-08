//go:build linux

package monitoring

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContainerResourceScope chooses the metric boundary, not execution privileges.
// HOST_PROC is an existing explicit request to monitor a host mounted into Docker.
func ContainerResourceScope() bool {
	switch strings.ToLower(flags.ResourceScope) {
	case "host":
		return false
	case "container":
		return true
	default:
		return flags.HostProc == "" && detectContainer() != ""
	}
}

type cgroupLocation struct{ dir, mount string }
type cgroupReader struct {
	locations   map[string]cgroupLocation
	read        func(string) ([]byte, error)
	logicalCPUs int
}

// resolveCgroups combines membership and mount roots; a cgroup namespace may
// expose membership '/' while mountinfo exposes the host-side subtree root.
func resolveCgroups(membership, mountinfo string) (map[string]cgroupLocation, error) {
	groups := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(membership))
	for s.Scan() {
		p := strings.SplitN(s.Text(), ":", 3)
		if len(p) != 3 {
			continue
		}
		if !filepath.IsAbs(p[2]) || strings.Contains(p[2], "/../") || strings.HasSuffix(p[2], "/..") {
			return nil, fmt.Errorf("invalid cgroup membership")
		}
		if p[1] == "" {
			groups["unified"] = p[2]
		} else {
			for _, c := range strings.Split(p[1], ",") {
				groups[c] = p[2]
			}
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	result := map[string]cgroupLocation{}
	s = bufio.NewScanner(strings.NewReader(mountinfo))
	unescape := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	for s.Scan() {
		f := strings.Fields(s.Text())
		sep := -1
		for i, v := range f {
			if v == "-" {
				sep = i
				break
			}
		}
		if len(f) < 6 || sep < 6 || len(f) <= sep+3 {
			continue
		}
		kind := f[sep+1]
		root, mount := filepath.Clean(unescape.Replace(f[3])), filepath.Clean(unescape.Replace(f[4]))
		if !filepath.IsAbs(root) || !filepath.IsAbs(mount) {
			continue
		}
		controllers := strings.Split(f[sep+3], ",")
		if kind == "cgroup2" {
			controllers = []string{"unified"}
		} else if kind != "cgroup" {
			continue
		}
		for _, c := range controllers {
			group, ok := groups[c]
			if !ok {
				continue
			}
			rel, err := filepath.Rel(root, filepath.Clean(group))
			if group == "/" && root != "/" {
				rel, err = ".", nil
			}
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
				continue
			}
			candidate := cgroupLocation{dir: filepath.Join(mount, rel), mount: mount}
			if old, exists := result[c]; !exists || len(candidate.mount) > len(old.mount) {
				result[c] = candidate
			}
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("current cgroup controllers are unavailable")
	}
	return result, nil
}

func currentCgroups() (*cgroupReader, error) {
	groups, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	loc, err := resolveCgroups(string(groups), string(mounts))
	if err != nil {
		return nil, err
	}
	return &cgroupReader{locations: loc, read: os.ReadFile, logicalCPUs: runtime.NumCPU()}, nil
}

func (r *cgroupReader) location(controller string) (cgroupLocation, bool) {
	if p, ok := r.locations["unified"]; ok {
		return p, true
	}
	p, ok := r.locations[controller]
	return p, ok
}

func (r *cgroupReader) value(controller, name string) (string, error) {
	p, ok := r.location(controller)
	if !ok {
		return "", fmt.Errorf("controller unavailable: %s", controller)
	}
	b, err := r.read(filepath.Join(p.dir, name))
	return strings.TrimSpace(string(b)), err
}

func (r *cgroupReader) hierarchy(controller, name string) []string {
	p, ok := r.location(controller)
	if !ok {
		return nil
	}
	var result []string
	for dir := p.dir; ; dir = filepath.Dir(dir) {
		if b, err := r.read(filepath.Join(dir, name)); err == nil {
			result = append(result, strings.TrimSpace(string(b)))
		}
		if dir == p.mount || dir == filepath.Dir(dir) {
			break
		}
	}
	return result
}

func finiteLimit(text string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(text), 10, 64)
	// cgroup v1 encodes 'unlimited' as a very large page-aligned integer.
	return n, err == nil && n < uint64(1)<<60
}

func minimumLimit(values []string) (uint64, bool) {
	var limit uint64
	known := false
	for _, text := range values {
		if n, ok := finiteLimit(text); ok && (!known || n < limit) {
			limit, known = n, true
		}
	}
	return limit, known
}

func statFields(text string) map[string]uint64 {
	result := map[string]uint64{}
	s := bufio.NewScanner(strings.NewReader(text))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 2 {
			if n, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				result[f[0]] = n
			}
		}
	}
	return result
}

func (r *cgroupReader) memory(includeCache bool) (RamInfo, string) {
	_, v2 := r.locations["unified"]
	usageName, limitName := "memory.usage_in_bytes", "memory.limit_in_bytes"
	if v2 {
		usageName, limitName = "memory.current", "memory.max"
	}
	usageText, err := r.value("memory", usageName)
	used, parseErr := strconv.ParseUint(usageText, 10, 64)
	if err != nil || parseErr != nil {
		return RamInfo{Mode: "cgroup-unavailable"}, "container memory usage unavailable"
	}
	limit, known := minimumLimit(r.hierarchy("memory", limitName))
	if !includeCache {
		if text, err := r.value("memory", "memory.stat"); err == nil {
			stat := statFields(text)
			inactive := stat["inactive_file"]
			if !v2 {
				if n, exists := stat["total_inactive_file"]; exists {
					inactive = n
				}
			}
			if inactive < used {
				used -= inactive
			} else {
				used = 0
			}
		}
	}
	if known && used > limit {
		used = limit
	}
	mode := "cgroup-v1-working-set"
	if v2 {
		mode = "cgroup-v2-working-set"
	}
	if includeCache {
		mode += "-include-cache"
	}
	if !known {
		return RamInfo{Total: 0, Used: used, Mode: mode}, "container memory limit unknown/unlimited; host total omitted"
	}
	return RamInfo{Total: limit, Used: used, Mode: mode}, ""
}

func (r *cgroupReader) swap() RamInfo {
	if _, v2 := r.locations["unified"]; v2 {
		u, err := r.value("memory", "memory.swap.current")
		used, parseErr := strconv.ParseUint(u, 10, 64)
		if err != nil || parseErr != nil {
			return RamInfo{Mode: "cgroup-swap-unavailable"}
		}
		limit, _ := minimumLimit(r.hierarchy("memory", "memory.swap.max"))
		if limit > 0 && used > limit {
			used = limit
		}
		return RamInfo{Total: limit, Used: used, Mode: "cgroup-v2-swap"}
	}
	memory, memoryOK := minimumLimit(r.hierarchy("memory", "memory.limit_in_bytes"))
	combined, combinedOK := minimumLimit(r.hierarchy("memory", "memory.memsw.limit_in_bytes"))
	memUsage, e1 := r.value("memory", "memory.usage_in_bytes")
	bothUsage, e2 := r.value("memory", "memory.memsw.usage_in_bytes")
	m, e3 := strconv.ParseUint(memUsage, 10, 64)
	both, e4 := strconv.ParseUint(bothUsage, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return RamInfo{Mode: "cgroup-swap-unavailable"}
	}
	var total, used uint64
	if memoryOK && combinedOK && combined >= memory {
		total = combined - memory
	}
	if both >= m {
		used = both - m
	}
	return RamInfo{Total: total, Used: used, Mode: "cgroup-v1-swap"}
}

func cpusetCount(text string) (int, error) {
	if strings.TrimSpace(text) == "" {
		return 0, fmt.Errorf("empty cpuset")
	}
	seen := make(map[int]bool)
	for _, item := range strings.Split(strings.TrimSpace(text), ",") {
		p := strings.Split(item, "-")
		if len(p) > 2 {
			return 0, fmt.Errorf("invalid cpuset")
		}
		first, e := strconv.Atoi(p[0])
		if e != nil || first < 0 || first > 65535 {
			return 0, fmt.Errorf("invalid cpu")
		}
		last := first
		if len(p) == 2 {
			last, e = strconv.Atoi(p[1])
			if e != nil || last < first || last > 65535 {
				return 0, fmt.Errorf("invalid range")
			}
		}
		for n := first; n <= last; n++ {
			seen[n] = true
		}
	}
	return len(seen), nil
}

func (r *cgroupReader) cpuCapacity() float64 {
	capacity := float64(r.logicalCPUs)
	if capacity <= 0 {
		capacity = 1
	}
	if _, v2 := r.locations["unified"]; v2 {
		for _, q := range r.hierarchy("cpu", "cpu.max") {
			f := strings.Fields(q)
			if len(f) != 2 {
				continue
			}
			quota, e1 := strconv.ParseFloat(f[0], 64)
			period, e2 := strconv.ParseFloat(f[1], 64)
			if e1 == nil && e2 == nil && quota > 0 && period > 0 {
				capacity = math.Min(capacity, quota/period)
			}
		}
	} else if p, ok := r.location("cpu"); ok {
		for dir := p.dir; ; dir = filepath.Dir(dir) {
			q, e1 := r.read(filepath.Join(dir, "cpu.cfs_quota_us"))
			per, e2 := r.read(filepath.Join(dir, "cpu.cfs_period_us"))
			quota, e3 := strconv.ParseFloat(strings.TrimSpace(string(q)), 64)
			period, e4 := strconv.ParseFloat(strings.TrimSpace(string(per)), 64)
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil && quota > 0 && period > 0 {
				capacity = math.Min(capacity, quota/period)
			}
			if dir == p.mount || dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, name := range []string{"cpuset.cpus.effective", "cpuset.cpus"} {
		for _, text := range r.hierarchy("cpuset", name) {
			if n, e := cpusetCount(text); e == nil && n > 0 {
				capacity = math.Min(capacity, float64(n))
			}
		}
	}
	return capacity
}

func (r *cgroupReader) cpuTime() (uint64, string, error) {
	if p, v2 := r.locations["unified"]; v2 {
		text, err := r.value("cpu", "cpu.stat")
		fields := statFields(text)
		n, ok := fields["usage_usec"]
		if err != nil || !ok || n > math.MaxUint64/1000 {
			return 0, "", fmt.Errorf("container cpu usage unavailable")
		}
		return n * 1000, p.dir, nil
	}
	text, err := r.value("cpuacct", "cpuacct.usage")
	n, parseErr := strconv.ParseUint(text, 10, 64)
	p, _ := r.location("cpuacct")
	if err != nil || parseErr != nil {
		return 0, "", fmt.Errorf("container cpu usage unavailable")
	}
	return n, p.dir, nil
}

var cgroupCPU struct {
	sync.Mutex
	group string
	usage uint64
	at    time.Time
}

func containerCPU() (int, float64, bool) {
	if !ContainerResourceScope() {
		return 0, 0, false
	}
	r, err := currentCgroups()
	if err != nil {
		return 0, 0, true
	}
	capacity := r.cpuCapacity()
	cores := int(math.Ceil(capacity))
	cgroupCPU.Lock()
	defer cgroupCPU.Unlock()
	usage, group, err := r.cpuTime()
	now := time.Now()
	percentage := 0.0
	if err == nil && group == cgroupCPU.group && !cgroupCPU.at.IsZero() && usage >= cgroupCPU.usage {
		delta := now.Sub(cgroupCPU.at).Seconds()
		if delta > 0 {
			percentage = float64(usage-cgroupCPU.usage) / 1e9 / delta / capacity * 100
			percentage = math.Min(100, math.Max(0, percentage))
		}
	}
	if err == nil {
		cgroupCPU.group, cgroupCPU.usage, cgroupCPU.at = group, usage, now
	}
	return cores, percentage, true
}

func containerCPUCores() (int, bool) {
	if !ContainerResourceScope() {
		return 0, false
	}
	r, err := currentCgroups()
	if err != nil {
		return 0, true
	}
	return int(math.Ceil(r.cpuCapacity())), true
}

func containerRam() (RamInfo, bool) {
	if !ContainerResourceScope() {
		return RamInfo{}, false
	}
	r, err := currentCgroups()
	if err != nil {
		return RamInfo{Mode: "cgroup-unavailable"}, true
	}
	v, _ := r.memory(flags.MemoryIncludeCache)
	return v, true
}

func containerSwap() (RamInfo, bool) {
	if !ContainerResourceScope() {
		return RamInfo{}, false
	}
	r, err := currentCgroups()
	if err != nil {
		return RamInfo{Mode: "cgroup-swap-unavailable"}, true
	}
	return r.swap(), true
}

func ResourceScopeInfo() map[string]string {
	if !ContainerResourceScope() {
		return map[string]string{"mode": "host"}
	}
	info := map[string]string{"mode": "container", "cpu": "cgroup-quota-and-cpuset", "memory": "cgroup-working-set", "load": "unavailable", "disk": "quota-unavailable", "uptime": "host-boot", "network": "network-namespace"}
	if flags.IncludeMountpoints != "" {
		info["disk"] = "explicit-filesystem-capacity-not-container-quota"
	}
	r, err := currentCgroups()
	if err != nil {
		info["warning"] = err.Error()
		return info
	}
	_, warning := r.memory(flags.MemoryIncludeCache)
	if warning != "" {
		info["warning"] = warning
	}
	if _, _, err := r.cpuTime(); err != nil {
		info["cpu"] = "unavailable"
	}
	return info
}
