//go:build linux

package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These use a real local PTY with restricted mkdir/PATH fixtures. They model
// unavailable /tmp and Android-like TMPDIR; they are not Android device tests.
func restrictedShellEnv(t *testing.T, allowed, tmpdir, home string) []string {
	t.Helper()
	bin := t.TempDir()
	mkdir, err := exec.LookPath("mkdir")
	if err != nil {
		t.Fatal(err)
	}
	// The wrapper denies every candidate except the fixture's private base.
	// No host /tmp, /data/local/tmp or user's real HOME is changed.
	wrapper := "#!/bin/sh\ncase \"$3\" in \"$KMB_TEST_ALLOWED_BASE\"/.komari-mcp-*) exec " + quote(mkdir) + " \"$@\" ;; *) exit 1 ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "mkdir"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	// The adapter must resolve this PATH entry rather than hardcode /bin/sh.
	if err := os.Symlink("/bin/sh", filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	return []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "TMPDIR=" + tmpdir, "HOME=" + home, "KMB_TEST_ALLOWED_BASE=" + allowed}
}

func TestPOSIXAdaptiveTempDirectoryAndPersistentContext(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "android-like-tmpdir"
		if fallback {
			name = "unavailable-tmpdir-and-system-dirs-fall-back-home"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "private dir 'quoted'", "data", "local", "tmp")
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			tmpdir, home := base, filepath.Join(root, "missing-home")
			if fallback {
				tmpdir, home = filepath.Join(root, "missing-tmpdir"), base
			}
			m, h, key := setupWithEnv(t, restrictedShellEnv(t, base, tmpdir, home))
			s := ready(t, m, key, "adaptive")
			dir := filepath.Join(base, ".komari-mcp-"+s.Nonce)
			info, err := os.Stat(dir)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("directory not private: %v %v", info, err)
			}
			run(t, m, key, s, "umask-before", "umask 022")
			permissions := run(t, m, key, s, "permissions", "command stat -c '%a' \"$__kmb_dir/permissions.sh\"; umask")
			if !strings.Contains(permissions.Output, "600") || !strings.Contains(permissions.Output, "0022") {
				t.Fatal("script not private or parent umask changed", permissions.Output)
			}
			r := run(t, m, key, s, "set-context", "cd /; export KOMARI_HANDOFF_TEST_VAR=hello; handoff_func() { printf kept; }")
			if r.ExitCode == nil || *r.ExitCode != 0 {
				t.Fatal(r)
			}
			r = run(t, m, key, s, "read-context", "printf '%s:%s:%s' \"$__kmb_dir\" \"$KOMARI_HANDOFF_TEST_VAR\" \"$(handoff_func)\"; pwd")
			if !strings.Contains(r.Output, dir+":hello:kept/") {
				t.Fatal("directory/context lost across cd", r.Output)
			}
			r = run(t, m, key, s, "payload", "cat <<'EOF'\n中文 'quote' \"double\" $literal\nEOF\n(exit 7)")
			if r.ExitCode == nil || *r.ExitCode != 7 || !strings.Contains(r.Output, "中文 'quote' \"double\" $literal") {
				t.Fatal("multiline payload or nonzero exit lost", r)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("finished script files retained", entries, err)
			}
			before, _ := m.Status(key.ID, s.ID)
			h.Drop(before.RequestID)
			until(t, func() bool { r, _ := m.Status(key.ID, s.ID); return r.State == "ready" && r.Verified && r.Gap })
			after, _ := m.Status(key.ID, s.ID)
			if before.PID != after.PID || before.Generation != after.Generation {
				t.Fatal("reconnect rebuilt parent shell")
			}
			if r := run(t, m, key, s, "after-reconnect", "printf '%s' \"$__kmb_dir\""); !strings.Contains(r.Output, dir) {
				t.Fatal("resolved temp directory was not preserved")
			}
		})
	}
}

func TestPOSIXNoWritableTempBlocksDispatch(t *testing.T) {
	root := t.TempDir()
	m, _, key := setupWithEnv(t, restrictedShellEnv(t, filepath.Join(root, "unavailable"), root, root))
	s, err := m.Open(key.ID, "node-a", "no-writable-directory")
	if err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { r, _ := m.Status(key.ID, s.ID); return r.State == "bootstrap_failed" })
	r, _ := m.Status(key.ID, s.ID)
	if r.Verified || r.LastConnectionError != "bootstrap_temp_directory" {
		t.Fatal("failed bootstrap advertised ready", r)
	}
	if _, err = m.Run(context.Background(), key.ID, s.ID, "blocked", "printf should-not-run", 0, 32768); err == nil {
		t.Fatal("dispatch allowed without a private writable directory")
	}
}

func TestPOSIXStagingFailureNeverSourcesPartialOrMissingScript(t *testing.T) {
	base := t.TempDir()
	m, _, key := setupWithEnv(t, restrictedShellEnv(t, base, base, base))
	s := ready(t, m, key, "staging-failure")
	if err := os.Remove(filepath.Join(base, ".komari-mcp-"+s.Nonce)); err != nil {
		t.Fatal(err)
	}
	r := run(t, m, key, s, "never-run", "printf should-not-run")
	if r.ExitCode == nil || *r.ExitCode != 125 || strings.Contains(r.Output, "should-not-run") || !strings.Contains(r.Output, "script staging failed") {
		t.Fatal("staging error executed payload or reported success", r)
	}
}

func TestPOSIXFailedWriteNeverRunsPartialPayload(t *testing.T) {
	base := t.TempDir()
	m, _, key := setupWithEnv(t, restrictedShellEnv(t, base, base, base))
	s := ready(t, m, key, "failed-write")
	path := filepath.Join(base, ".komari-mcp-"+s.Nonce, "partial.sh")
	// /dev/full returns ENOSPC on write; only a link in our private test dir
	// is created/deleted. This does not fill a disk or alter the device.
	if err := os.Symlink("/dev/full", path); err != nil {
		t.Fatal(err)
	}
	r := run(t, m, key, s, "partial", "printf should-not-run;\n"+strings.Repeat("# padding across staging chunks\n", 200))
	if r.ExitCode == nil || *r.ExitCode != 125 || strings.Contains(r.Output, "should-not-run") {
		t.Fatal("failed write led to partial execution", r)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("failed staging file was not removed", err)
	}
}

// A real mksh PTY is a shell-family compatibility check, not an Android device
// test. CI runs it when mksh is installed; a local fixture can supply its path.
func TestPOSIXBootstrapOnSupportedShells(t *testing.T) {
	shells := []string{"/bin/sh", "/bin/bash"}
	mksh := os.Getenv("KOMARI_TEST_MKSH")
	if mksh == "" {
		mksh, _ = exec.LookPath("mksh")
	}
	if mksh != "" {
		shells = append(shells, mksh)
	}
	for _, shell := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				t.Skip("shell not installed")
			}
			m, _, key := setupWithEnv(t, []string{"KMB_TEST_INITIAL_SHELL=" + shell})
			s := ready(t, m, key, "initial-shell")
			result := run(t, m, key, s, "remember", "cd /; export KMB_CONTEXT_CHECK=kept")
			if result.ExitCode == nil || *result.ExitCode != 0 {
				t.Fatal(result)
			}
			result = run(t, m, key, s, "recall", "printf '%s:' \"$KMB_CONTEXT_CHECK\"; pwd")
			if result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Output, "kept:/") {
				t.Fatal(result)
			}
		})
	}
}
