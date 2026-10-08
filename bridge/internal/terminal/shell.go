package terminal

import (
	"encoding/base64"
	"fmt"
	"strings"
)

type ShellType string

const (
	ShellPOSIX      ShellType = "posix"
	ShellPowerShell ShellType = "powershell"
)

func shellForOS(os string) ShellType {
	if strings.Contains(strings.ToLower(os), "windows") {
		return ShellPowerShell
	}
	return ShellPOSIX
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Some ConPTY versions remove C0 RS/US characters. Windows uses printable framing;
// its leading/trailing '~' are constructed at runtime and absent from input
// echo. Concatenation avoids ambiguous $variable:scope interpolation.
func psMarker(nonce, kind, suffix string) string {
	return "[Console]::WriteLine(); [Console]::Write([char]126 + " + psQuote("KMB:"+nonce+":"+kind+":") + " + " + suffix + " + [char]126); [Console]::WriteLine()"
}

func shellFraming(kind ShellType, nonce string) ([]byte, byte) {
	if kind == ShellPowerShell {
		return []byte("~KMB:" + nonce + ":"), '~'
	}
	return []byte("\x1eKMB:" + nonce + ":"), 0x1f
}

func shellBootstrap(kind ShellType, nonce string) string {
	if kind == ShellPowerShell {
		return "Remove-Module PSReadLine -ErrorAction SilentlyContinue; $ProgressPreference='SilentlyContinue'; function global:prompt { '' }; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; $global:__kmb_ctx=" + psQuote(nonce) + "; " + psMarker(nonce, "READY", "[string]$PID") + "\r"
	}
	// Only initialization changes the shell. Resolve sh via the probe's PATH;
	// Android's system shell is a fallback when PATH omits it. The initial
	// shell must understand POSIX syntax, as with the original bootstrap.
	launch := "stty -echo -onlcr 2>/dev/null; __kmb_boot_failed=; __kmb_shell=$(command -v sh 2>/dev/null); if [ -z \"$__kmb_shell\" ] && [ -x /system/bin/sh ]; then __kmb_shell=/system/bin/sh; fi; if [ -n \"$__kmb_shell\" ]; then PS1='' PS2='' ENV='' exec \"$__kmb_shell\" -i; fi; __kmb_boot_failed=1; " + marker(nonce, "BOOTERR", "shell") + "\n"
	// Keep the resolved, absolute directory in the persistent shell. Never
	// reuse an existing nonce directory (including a symlink), and test actual
	// mkdir success instead of relying only on writable permission bits.
	init := "if [ -z \"${__kmb_boot_failed-}\" ]; then __kmb_ctx=" + quote(nonce) + "; __kmb_dir=; __kmb_umask=$(umask); umask 077; for __kmb_base in \"${TMPDIR-}\" /data/local/tmp /tmp \"${HOME-}\" \"${PWD-}\"; do [ -n \"$__kmb_base\" ] && [ -d \"$__kmb_base\" ] && [ -w \"$__kmb_base\" ] || continue; case $__kmb_base in /*) ;; *) __kmb_base=$PWD/$__kmb_base ;; esac; __kmb_base=$(CDPATH= cd -P \"$__kmb_base\" 2>/dev/null && pwd -P) || continue; if command mkdir -m 700 \"$__kmb_base/.komari-mcp-$__kmb_ctx\" 2>/dev/null; then __kmb_dir=$__kmb_base/.komari-mcp-$__kmb_ctx; break; fi; done; umask \"$__kmb_umask\"; if [ -n \"$__kmb_dir\" ]; then " + marker(nonce, "READY", "%s") + " \"$$\"; else " + marker(nonce, "BOOTERR", "temp_directory") + "; fi; fi\n"
	return launch + init
}

func shellResume(kind ShellType, nonce string) string {
	if kind == ShellPowerShell {
		return psMarker(nonce, "RESUME", "([string]$global:__kmb_ctx + ':' + [string]$PID)") + "\r"
	}
	return "if [ \"${__kmb_ctx-}\" = " + quote(nonce) + " ] && { [ -z \"${__kmb_dir-}\" ] || [ ! -d \"$__kmb_dir\" ] || [ ! -w \"$__kmb_dir\" ]; }; then " + marker(nonce, "BOOTERR", "temp_directory") + "; else " + marker(nonce, "RESUME", "%s:%s") + " \"${__kmb_ctx-}\" \"$$\"; fi\n"
}

// Source exact text in the persistent parent scope. A ScriptBlock avoids the
// .ps1 execution-policy gate without changing system policy or starting another
// PowerShell. Base64 input is staged in bounded ASCII lines (not an OS command line).
func shellCommand(kind ShellType, nonce, id, command string) []string {
	if kind == ShellPowerShell {
		payload := base64.StdEncoding.EncodeToString([]byte(command + "\n$global:__kmb_command_ok=$?\n"))
		lines := []string{"$global:__kmb_payload=''\r"}
		for len(payload) > 0 {
			chunk := payload[:min(len(payload), 640)]
			payload = payload[len(chunk):]
			lines = append(lines, "$global:__kmb_payload += "+psQuote(chunk)+"\r")
		}
		line := psMarker(nonce, "BEGIN", psQuote(id)) + "; try { $global:LASTEXITCODE=$null; $global:__kmb_command_ok=$null; . ([ScriptBlock]::Create([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($global:__kmb_payload)))) | Out-Default; $__kmb_pipeline_ok=$?; $__kmb_ok=if ($null -ne $global:__kmb_command_ok) { $global:__kmb_command_ok } else { $__kmb_pipeline_ok }; $__kmb_native=$global:LASTEXITCODE; $__kmb_rc=if ($null -ne $__kmb_native) { [int]$__kmb_native } elseif ($__kmb_ok) { 0 } else { 1 } } catch { $__kmb_rc=1; [Console]::Error.WriteLine($_.ToString()) }; " + psMarker(nonce, "END", "("+psQuote(id+":")+" + [string]$__kmb_rc)") + "; Remove-Variable __kmb_payload -Scope Global -ErrorAction SilentlyContinue\r"
		return append(lines, line)
	}
	path := "\"$__kmb_dir/" + id + ".sh\""
	raw := []byte(command + "\n")
	lines := []string{"__kmb_stage_ok=0; if [ \"${__kmb_ctx-}\" = " + quote(nonce) + " ] && [ -n \"${__kmb_dir-}\" ] && [ -d \"$__kmb_dir\" ] && [ -w \"$__kmb_dir\" ]; then __kmb_stage_ok=1; fi; __kmb_stage_umask=$(umask); umask 077\n"}
	first := true
	for len(raw) > 0 {
		chunk := raw[:min(len(raw), 360)]
		raw = raw[len(chunk):]
		var encoded strings.Builder
		for _, b := range chunk {
			fmt.Fprintf(&encoded, "\\0%03o", b)
		}
		redirect := ">>"
		if first {
			redirect = ">"
			first = false
		}
		lines = append(lines, "if [ \"$__kmb_stage_ok\" = 1 ]; then if command printf '%b' "+quote(encoded.String())+" "+redirect+" "+path+"; then :; else __kmb_stage_ok=0; fi; fi\n")
	}
	return append(lines, "umask \"$__kmb_stage_umask\"; "+marker(nonce, "BEGIN", id)+"; if [ \"$__kmb_stage_ok\" = 1 ]; then . "+path+"; __kmb_rc=$?; else __kmb_rc=125; command printf '%s\\n' 'Komari MCP script staging failed' >&2; fi; if [ -n \"${__kmb_dir-}\" ]; then command rm -f "+path+"; fi; "+marker(nonce, "END", id+":%s")+" \"$__kmb_rc\"\n")
}

func validShellExit(kind ShellType, rc int) bool {
	if kind == ShellPowerShell {
		return int64(rc) >= -2147483648 && int64(rc) <= 2147483647
	}
	return rc >= 0 && rc <= 255
}
