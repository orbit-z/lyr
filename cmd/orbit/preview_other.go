//go:build !unix

package main

import (
	"os"
	"syscall"
)

// forwardSignals is empty: the console delivers Ctrl-C to the server
// directly (and main ignores it for the CLI itself).
var forwardSignals []os.Signal

func previewProcAttr() *syscall.SysProcAttr { return nil }
