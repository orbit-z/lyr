package dev

import (
	"os"
	"syscall"
)

// appProcAttr puts the app in its own process group, so a Ctrl-C in the
// terminal reaches it once, through lyr dev (which stops it with SIGINT),
// and has the kernel stop it if lyr dev dies without stopping it.
func appProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// interrupt asks the app to shut down gracefully.
func interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
