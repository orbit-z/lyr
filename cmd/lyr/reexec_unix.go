//go:build unix

package main

import (
	"fmt"
	"io"
	"syscall"
)

// execCLI replaces this process with bin: the pinned CLI then gets the
// terminal's signals itself and its exit code is the command's. dir is the
// working directory already (main's).
func execCLI(bin string, argv, env []string, dir string, stderr io.Writer) int {
	_ = dir
	err := syscall.Exec(bin, argv, env)
	fmt.Fprintf(stderr, "lyr: running the CLI version pinned in go.mod: %v\n", err)
	return exitFail
}
