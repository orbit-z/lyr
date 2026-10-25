//go:build unix && !linux

package dev

import (
	"os"
	"syscall"
)

// appProcAttr puts the app in its own process group, so a Ctrl-C in the
// terminal reaches it once, through orbit dev (which stops it with SIGINT).
func appProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// interrupt asks the app to shut down gracefully.
func interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
