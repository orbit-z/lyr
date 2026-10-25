//go:build !unix

package dev

import (
	"os"
	"syscall"
)

func appProcAttr() *syscall.SysProcAttr { return nil }

// interrupt can't send SIGINT on this platform: the app is killed.
func interrupt(p *os.Process) error { return p.Kill() }
