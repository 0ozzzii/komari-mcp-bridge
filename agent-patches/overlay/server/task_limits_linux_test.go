//go:build linux

package server

import (
	"github.com/komari-monitor/komari-agent/executionguard"
	"strings"
	"testing"
	"time"
)

func TestTaskDeadlineAndActualLargeOutput(t *testing.T) {
	old := executionguard.Global.Settings()
	defer executionguard.Global.Configure(old)
	p := executionguard.DefaultOptions()
	p.DefaultTimeoutSeconds = 1
	p.OutputBytes = 32768
	executionguard.Global.Configure(p)
	start := time.Now()
	result, code := runTaskCommand("sleep 20")
	if code == 0 || !strings.Contains(result, "execution timeout") || time.Since(start) > 5*time.Second {
		t.Fatalf("deadline: %q %d", result, code)
	}
	result, code = runTaskCommand("yes x | head -c 200000")
	if code != 0 || !strings.Contains(result, "output truncated") || len(result) > 33000 {
		t.Fatalf("output not bounded: len=%d exit=%d", len(result), code)
	}
}
