//go:build linux

package monitoring

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

func IsAndroid() bool { return androidRuntime() }

var androidRuntime = sync.OnceValue(func() bool {
	if runtime.GOOS == "android" {
		return true
	}
	for _, path := range []string{"/system/build.prop", "/system/bin/app_process", "/system/bin/app_process64"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
})

const (
	containerDiskTimeout = 120 * time.Second
	containerDiskPeriod  = 10 * time.Minute
)

var rootfsDisk = diskSampler{
	scan: func(ctx context.Context) (uint64, bool, error) {
		return scanRootFS(ctx, "/", rootFSMounts())
	},
}

type diskSampler struct {
	mu     sync.Mutex
	active bool
	deadline time.Time
	next   time.Time
	last   DiskInfo
	have   bool
	scan   func(context.Context) (uint64, bool, error)
}

func containerDiskUsage() DiskInfo {
	return rootfsDisk.sample(time.Now())
}

func (s *diskSampler) sample(now time.Time) DiskInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active && !now.Before(s.next) {
		s.active = true
		s.deadline = now.Add(containerDiskTimeout)
		go s.refresh()
	}
	if s.active && !now.Before(s.deadline) {
		// A blocked filesystem syscall cannot always be cancelled. Keep one
		// worker and expose its deadline even if it has not returned.
		result := s.last
		result.Status = "unavailable"
		if s.have {
			result.Status = "stale"
		}
		return result
	}
	if s.last.Status == "" {
		return DiskInfo{Status: "pending"}
	}
	return s.last
}

func (s *diskSampler) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), containerDiskTimeout)
	defer cancel()
	used, partial, err := s.scan(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.next = time.Now().Add(containerDiskPeriod)
	switch {
	case err != nil && s.have:
		s.last.Status = "stale"
	case err != nil || (partial && used == 0):
		s.last = DiskInfo{Status: "unavailable"}
	case partial:
		s.last, s.have = DiskInfo{Used: used, Status: "partial"}, true
	default:
		s.last, s.have = DiskInfo{Used: used, Status: "estimated"}, true
	}
}

func rootFSMounts() map[string]bool {
	excluded := map[string]bool{
		"/proc": true, "/sys": true, "/dev": true, "/run": true,
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return excluded
	}
	defer file.Close()

	unescape := strings.NewReplacer("\\040", " ", "\\011", "\\t", "\\012", "\\n", "\\134", "\\\\")
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 && fields[4] != "/" {
			excluded[unescape.Replace(fields[4])] = true
		}
	}
	return excluded
}

// Count allocated blocks like du. Stay on the root device, omit bind mounts,
// never follow symlinks, and count hard links only once.
func scanRootFS(ctx context.Context, root string, excluded map[string]bool) (uint64, bool, error) {
	var used, device uint64
	var partial bool
	hardlinks := make(map[uint64]struct{})
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if !errors.Is(walkErr, fs.ErrNotExist) {
				partial = true
			}
			return nil
		}
		if excluded[path] && path != root {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if path == root {
				return err
			}
			if !errors.Is(err, fs.ErrNotExist) {
				partial = true
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("rootfs file block information unavailable")
		}
		if path == root {
			device = uint64(stat.Dev)
		}
		if uint64(stat.Dev) != device {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && stat.Nlink > 1 {
			if _, counted := hardlinks[stat.Ino]; counted {
				return nil
			}
			hardlinks[stat.Ino] = struct{}{}
		}
		if stat.Blocks > 0 {
			used += uint64(stat.Blocks) * 512
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	return used, partial, err
}
