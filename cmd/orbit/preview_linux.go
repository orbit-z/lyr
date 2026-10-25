package main

import (
	"os"
	"syscall"
)

// forwardSignals are the signals orbit preview forwards to the server.
var forwardSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// previewProcAttr puts the server in its own process group, and has the
// kernel stop it if the CLI dies without forwarding a signal.
func previewProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
