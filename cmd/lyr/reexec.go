package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/orbit-z/lyr/internal/config"
	"github.com/orbit-z/lyr/internal/pin"
)

// exemptFromPin are the commands a global CLI always runs itself: lyr new
// has no project yet, and lyr doctor must work when the re-exec or the
// versions are what's broken (it reports both).
var exemptFromPin = map[string]bool{"new": true, "doctor": true}

// pinned applies the version pinning rule (PLAN.md §6) before a command
// runs in dir. It returns done=false when this binary runs the command
// itself; otherwise the command ran as the pinned CLI (or the versions
// mismatch) and code is the exit code.
func pinned(args []string, dir string, stderr io.Writer) (code int, done bool) {
	if os.Getenv(pin.ReexecEnv) == "1" || len(args) > 0 && exemptFromPin[args[0]] {
		return 0, false
	}
	self := pin.Current()
	if self.Pinned {
		return 0, false
	}
	root, err := config.FindRoot(dir)
	if err != nil {
		return 0, false // not in a module: the command says so
	}
	mod, err := config.ReadGoMod(root)
	if err != nil {
		return 0, false // the command reports a broken go.mod
	}
	d := pin.Decide(mod, self)
	switch {
	case d.Err != nil:
		fmt.Fprintln(stderr, "lyr:", d.Err)
		return exitFail, true
	case d.Reexec:
		return reexec(args, dir, stderr), true
	}
	return 0, false
}

// reexec runs the project's pinned CLI with args in dir, with
// LYR_REEXEC=1, and returns its exit code.
//
// It's go tool lyr <args> without the go command in between: go tool -n
// lyr builds the pinned CLI (or finds it in the build cache) and prints
// its path, and the CLI then runs that binary itself, with the PATH go tool
// would give it (the selected toolchain's bin first). On Unix it replaces
// this process (exec), so signals and the exit code are the pinned CLI's
// own: a Ctrl-C on lyr dev exits 0, where go tool lyr dev exits 1 (the
// tool gets the terminal's SIGINT and the one go forwards, and the second
// kills it).
func reexec(args []string, dir string, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "lyr: running the CLI version pinned in go.mod: "+format+"\n", a...)
		return exitFail
	}
	var out bytes.Buffer
	build := exec.Command("go", "tool", "-n", "lyr")
	build.Dir = dir
	build.Stdout, build.Stderr = &out, stderr
	if err := build.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode() // go printed why
		}
		return fail("go tool -n lyr: %v", err)
	}
	bin := strings.TrimSpace(out.String())
	if bin == "" || !filepath.IsAbs(bin) {
		return fail("go tool -n lyr printed %q, not the path of a binary", bin)
	}
	gorootCmd := exec.Command("go", "env", "GOROOT")
	gorootCmd.Dir = dir
	goroot, err := gorootCmd.Output()
	if err != nil {
		return fail("go env GOROOT: %v", err)
	}
	env := withEnv(os.Environ(), pin.ReexecEnv, "1")
	if r := strings.TrimSpace(string(goroot)); r != "" {
		env = withEnv(env, "PATH", filepath.Join(r, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return execCLI(bin, append([]string{"lyr"}, args...), env, dir, stderr)
}

// withEnv returns env with key set to value, replacing any earlier
// setting (exec's environment isn't deduplicated; keys compare without
// case, for Windows' Path).
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, key) {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}
