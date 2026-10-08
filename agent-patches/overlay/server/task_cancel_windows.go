//go:build windows

package server

import "os/exec"

// CommandContext kills its owned process; detached children are not guaranteed
// without Windows Job Objects, intentionally not claimed by this increment.
func taskProcessGroup(cmd *exec.Cmd) {}
