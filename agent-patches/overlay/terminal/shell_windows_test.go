//go:build windows

package terminal

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestManagedWindowsShellPreferenceAndServicePath(t *testing.T) {
	const installed = `C:\Program Files\PowerShell\7\pwsh.exe`
	for _, tc := range []struct {
		name, programFiles, want string
		managed                  bool
		available                map[string]string
		calls                    []string
	}{
		{"prefer-pwsh", `C:\Program Files`, installed, true, map[string]string{"pwsh.exe": installed, "powershell.exe": "powershell.exe"}, []string{"pwsh.exe"}},
		{"service-path-without-pwsh", `C:\Program Files`, installed, true, map[string]string{installed: installed, "powershell.exe": "powershell.exe"}, []string{"pwsh.exe", installed}},
		{"powershell-fallback", "", "powershell.exe", true, map[string]string{"powershell.exe": "powershell.exe"}, []string{"pwsh.exe", "powershell.exe"}},
		{"no-managed-cmd", "", "", true, map[string]string{}, []string{"pwsh.exe", "powershell.exe"}},
		{"human-unchanged", `C:\Program Files`, "powershell.exe", false, map[string]string{"pwsh.exe": installed, "powershell.exe": "powershell.exe"}, []string{"powershell.exe"}},
		{"human-cmd-fallback", "", "cmd.exe", false, map[string]string{}, []string{"powershell.exe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			got, err := selectWindowsShell(tc.managed, func(name string) (string, error) {
				calls = append(calls, name)
				if path, ok := tc.available[name]; ok {
					return path, nil
				}
				return "", fmt.Errorf("not installed")
			}, tc.programFiles)
			if got != tc.want || (err != nil) != (tc.want == "") || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("shell=%q err=%v calls=%v", got, err, calls)
			}
		})
	}
}

func TestManagedWindowsShellArguments(t *testing.T) {
	for _, shell := range []string{`C:\Program Files\PowerShell\7\pwsh.exe`, `C:\Windows\System32\WindowsPowerShell\v1.0\POWERSHELL.EXE`} {
		line := windowsShellCommandLine(shell, true)
		if !strings.Contains(line, " -NoLogo -NoProfile -NoExit -Command ") || strings.Contains(line, "ExecutionPolicy") || strings.Contains(line, "NonInteractive") {
			t.Fatal("unsafe or missing managed shell arguments", line)
		}
		if strings.Contains(windowsShellCommandLine(shell, false), "NoProfile") {
			t.Fatal("human terminal changed")
		}
	}
}
