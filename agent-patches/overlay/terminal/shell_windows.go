//go:build windows

package terminal

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
)

// Resolve using the probe's service environment, not the interactive user's
// PATH. The normal PowerShell 7 installation may not be on a service PATH yet.
// Existing human terminals retain their original shell selection.
func selectWindowsShell(managed bool, lookup func(string) (string, error), programFiles string) (string, error) {
	if managed {
		candidates := []string{"pwsh.exe"}
		if programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "PowerShell", "7", "pwsh.exe"))
		}
		candidates = append(candidates, "powershell.exe")
		for _, candidate := range candidates {
			if path, err := lookup(candidate); err == nil && path != "" {
				return path, nil
			}
		}
		return "", fmt.Errorf("managed Windows terminal requires pwsh.exe or powershell.exe")
	}
	if path, err := lookup("powershell.exe"); err == nil && path != "" {
		return path, nil
	}
	return "cmd.exe", nil
}

func windowsShellCommandLine(shell string, managed bool) string {
	line := syscall.EscapeArg(shell)
	name := filepath.Base(shell)
	if managed && (strings.EqualFold(name, "powershell.exe") || strings.EqualFold(name, "pwsh.exe")) {
		// These options apply only to this owned session; no profile, execution
		// policy, system environment or service configuration is modified.
		line += ` -NoLogo -NoProfile -NoExit -Command "Remove-Module PSReadLine -ErrorAction SilentlyContinue"`
	}
	return line
}
