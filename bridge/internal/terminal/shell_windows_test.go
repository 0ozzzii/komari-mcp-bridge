//go:build windows

package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
)

// This is a real Windows ConPTY/PowerShell test, not a pipe-based shell mock.
// The production Server/Agent relay and a particular Windows host build remain separate
// acceptance targets. Any changed marker bytes fail rather than faking ready.
var requirePwsh = flag.Bool("require-pwsh", false, "require an installed PowerShell 7 for both native ConPTY suites")

func TestWindowsConPTYPowerShellPersistentProtocol(t *testing.T) {
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil && shell == "pwsh.exe" && os.Getenv("ProgramFiles") != "" {
				path, err = exec.LookPath(filepath.Join(os.Getenv("ProgramFiles"), "PowerShell", "7", shell))
			}
			if err != nil {
				if shell == "pwsh.exe" && !*requirePwsh {
					t.Skip("PowerShell 7 not installed; its runtime behavior is unverified")
				}
				t.Fatal(err)
			}
			t.Logf("diagnostic_shell=%s", shell)
			testPowerShellConPTY(t, path, shell == "pwsh.exe")
		})
	}

}

func testPowerShellConPTY(t *testing.T, path string, isPwsh bool) {
	t.Helper()
	pty, err := conpty.Start(syscall.EscapeArg(path)+` -NoLogo -NoProfile -NoExit -Command "Remove-Module PSReadLine -ErrorAction SilentlyContinue"`, conpty.ConPtyDimensions(80, 24))
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	t.Logf("temporary_powershell_pid=%d", pty.Pid())
	const nonce = "0123456789abcdef0123456789abcdef"
	events := make(chan []string, 128)
	updates := make(chan struct{}, 1)
	var mu sync.Mutex
	var raw, output []byte
	var atEnd []byte
	bootReady := false
	go func() {
		prefix, end := shellFraming(ShellPowerShell, nonce)
		p := parser{prefix: prefix, end: end}
		b := make([]byte, 4096)
		for {
			n, e := pty.Read(b)
			if n > 0 {
				mu.Lock()
				raw = append(raw, b[:n]...)
				if len(raw) > 65536 {
					raw = raw[len(raw)-65536:]
				}
				p.feed(b[:n], func(v []byte) {
					output = append(output, v...)
					if len(output) > 65536 {
						output = output[len(output)-65536:]
					}
					select {
					case updates <- struct{}{}:
					default:
					}
				}, func(v []string) {
					if v[0] == "READY" {
						bootReady = true
					}
					if v[0] == "BEGIN" {
						output = nil
					}
					if v[0] == "END" {
						atEnd = append([]byte(nil), output...)
					}
					events <- v
				})
				mu.Unlock()
			}
			if e != nil {
				return
			}
		}
	}()
	wait := func(kind string) []string {
		t.Helper()
		limit := 12 * time.Second
		if kind == "READY" {
			limit = 60 * time.Second // Same readiness budget as the bridge.
		}
		timer := time.NewTimer(limit)
		defer timer.Stop()
		for {
			select {
			case ev := <-events:
				if ev[0] == kind {
					return ev
				}
			case <-timer.C:
				mu.Lock()
				tail := append([]byte(nil), raw[max(0, len(raw)-2048):]...)
				mu.Unlock()
				t.Fatalf("no %s marker from real ConPTY; raw tail=%q hex=%s", kind, tail, hex.EncodeToString(tail))
			}
		}
	}
	write := func(v string) {
		t.Helper()
		if _, e := pty.Write([]byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	// Production bootstrap resizes its own managed terminal first; 80-column
	// ConPTY wrapping inserts cursor sequences and duplicates edge glyphs.
	if err := pty.Resize(256, 40); err != nil {
		t.Fatal(err)
	}
	bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBootstrap()
	bootstrapDone := make(chan error, 1)
	go func() {
		bootstrapDone <- retryBootstrap(bootstrapCtx, time.Second, func() (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if bootReady {
				return false, nil
			}
			_, err := pty.Write([]byte(shellBootstrap(ShellPowerShell, nonce)))
			return true, err
		})
	}()
	ready := wait("READY")
	if err := <-bootstrapDone; err != nil {
		t.Fatal("Windows bootstrap failed", err)
	}
	if len(ready) != 2 || ready[1] == "" {
		t.Fatal("missing shell PID", ready)
	}
	readyFrame := []byte("~KMB:" + nonce + ":READY:" + ready[1] + "~")
	mu.Lock()
	readyPreserved := bytes.Contains(raw, readyFrame)
	mu.Unlock()
	if !readyPreserved {
		t.Fatal("parsed READY was not present as contiguous raw bytes")
	}
	t.Logf("ready_marker_hex=%s ready_marker_base64=%s", hex.EncodeToString(readyFrame), base64.StdEncoding.EncodeToString(readyFrame))
	command := func(id, text, code, want string) {
		t.Helper()
		mu.Lock()
		output = nil
		mu.Unlock()
		for _, line := range shellCommand(ShellPowerShell, nonce, id, text) {
			write(line)
		}
		begin := wait("BEGIN")
		end := wait("END")
		if len(begin) != 2 || begin[1] != id || len(end) != 3 || end[1] != id || end[2] != code {
			t.Fatalf("wrong command events %v %v", begin, end)
		}
		mu.Lock()
		received := string(atEnd)
		mu.Unlock()
		if want != "" && !strings.Contains(received, want) {
			t.Fatalf("missing real output %q in %q", want, received)
		}
	}
	versionCommand := "[Console]::WriteLine('diagnostic-shell-version:'+ $PSVersionTable.PSVersion.ToString())"
	if isPwsh {
		versionCommand += "; if ($PSVersionTable.PSVersion.Major -lt 7) {throw 'PowerShell 7 required'}"
	}
	command("version", versionCommand, "0", "diagnostic-shell-version:")
	command("set-context", "Set-Location $env:TEMP; $env:KOMARI_HANDOFF_TEST_VAR='hello'; $handoffVar='retained'; function handoffFunc { 'function-kept' }", "0", "")
	command("context", "[Console]::Write($env:KOMARI_HANDOFF_TEST_VAR+':'+$handoffVar+':'+(handoffFunc)); if ((Get-Location).Path -ne (Get-Item $env:TEMP).FullName) {throw 'cwd lost'}", "0", "hello:retained:function-kept")
	command("nonzero", "cmd.exe /d /c exit 7", "7", "")
	command("success-after-native-error", "Write-Output 'success-after-error'", "0", "success-after-error")
	command("native-full-code", "cmd.exe /d /c exit 3010", "3010", "")
	command(strings.Repeat("long-id-", 16), "Write-Output 'long-id-completed'", "0", "long-id-completed")
	command("throw", "throw 'controlled-test-error'", "1", "controlled-test-error")
	command("nonterminating-error", "Write-Error 'controlled-nonterminating-error'", "1", "controlled-nonterminating-error")
	command("unicode", "@'\n中文 'quotes' \"double\" $literal\n'@", "0", "中文 'quotes' \"double\" $literal")
	command("processes", "Get-Process | Select-Object -First 5 Id,ProcessName", "0", "ProcessName")
	// Ensure output arrives before completion, rather than only checking the
	// complete output after END. Console output bypasses delayed object tables.
	for _, line := range shellCommand(ShellPowerShell, nonce, "stream", "[Console]::WriteLine('synthetic-stream-start'); Start-Sleep -Milliseconds 1500; [Console]::WriteLine('synthetic-stream-end')") {
		write(line)
	}
	wait("BEGIN")
	streamTimer := time.NewTimer(5 * time.Second)
	defer streamTimer.Stop()
streamWait:
	for {
		mu.Lock()
		hasStart := bytes.Contains(output, []byte("synthetic-stream-start"))
		mu.Unlock()
		if hasStart {
			break
		}
		select {
		case ev := <-events:
			t.Fatalf("completion preceded first streaming output: %v", ev)
		case <-updates:
		case <-streamTimer.C:
			t.Fatal("no streaming output received")
			break streamWait
		}
	}
	select {
	case ev := <-events:
		t.Fatalf("stream command already ended when first output was observed: %v", ev)
	default:
	}
	end := wait("END")
	if len(end) != 3 || end[1] != "stream" || end[2] != "0" {
		t.Fatal("stream command did not finish correctly", end)
	}
	write(shellResume(ShellPowerShell, nonce))
	resume := wait("RESUME")
	if len(resume) != 3 || resume[1] != nonce || resume[2] != ready[1] {
		t.Fatalf("parent context or PID changed: %v %v", ready, resume)
	}
	mu.Lock()
	hasRaw := bytes.Contains(raw, []byte("~KMB:"))
	mu.Unlock()
	if !hasRaw {
		t.Fatal("printable marker was not preserved by ConPTY")
	}
}
