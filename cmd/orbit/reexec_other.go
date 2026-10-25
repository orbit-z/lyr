//go:build !unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
)

// execCLI runs bin as a child and returns its exit code. There's no exec
// here: the console delivers Ctrl-C to the child directly, so this process
// ignores it and waits for the child's own shutdown.
func execCLI(bin string, argv, env []string, dir string, stderr io.Writer) int {
	signal.Ignore(os.Interrupt)
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode()
	}
	fmt.Fprintf(stderr, "orbit: running the CLI version pinned in go.mod: %v\n", err)
	return exitFail
}
