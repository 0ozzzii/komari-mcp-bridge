//go:build !windows

package terminal

import (
	"golang.org/x/sys/unix"
	"syscall"
)

// Signal the actual terminal foreground process group without waiting for a
// raw-mode program to consume bytes. This is still a request, not completion.
func (t *unixTerminal) Interrupt() error {
	pgid, e := unix.IoctlGetInt(int(t.tty.Fd()), unix.TIOCGPGRP)
	if e != nil || pgid <= 0 {
		return e
	}
	return syscall.Kill(-pgid, syscall.SIGINT)
}
