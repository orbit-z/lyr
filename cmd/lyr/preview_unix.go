//go:build unix && !linux

package main

import (
	"os"
	"syscall"
)

// forwardSignals are the signals lyr preview forwards to the server.
var forwardSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// previewProcAttr puts the server in its own process group.
func previewProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
