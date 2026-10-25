package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/bertrandkhe/orbit/internal/build"
	"github.com/bertrandkhe/orbit/internal/config"
)

// parseNoArgs parses a command's flags and rejects positional arguments. It
// returns -1 to continue, or the exit code.
func parseNoArgs(e *env, c *command, fs *flag.FlagSet, args []string) int {
	if code := parse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.stderr, "orbit %s: unexpected argument %q\n", c.name, fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	return -1
}

// runBuild is orbit build: build the server and its assets into dist/.
func runBuild(e *env, args []string) int {
	c := lookup("build")
	fs := c.flagSet(e)
	embed := fs.Bool("embed", false, "embed dist/client into dist/server, for a single-binary deploy")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	res, err := build.Run(e.ctx, build.Options{Root: root, Embed: *embed})
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	report(e, res.Generate)
	for _, p := range res.Public {
		fmt.Fprintln(e.stderr, p)
	}
	e.stderr.Write(res.GoOutput)
	if res.Failed() {
		return exitFail
	}
	return exitOK
}

// runPreview is orbit preview: run dist/server from the project root, with
// the CLI's environment, and exit with its exit code. SIGINT and SIGTERM
// (and SIGHUP on Unix) are forwarded to it. On Unix, the server runs in its
// own process group, so a Ctrl-C in the terminal reaches it once, through
// the CLI, rather than twice (a second SIGINT stops orbit.ListenAndServe at
// once, skipping the graceful shutdown).
func runPreview(e *env, args []string) int {
	c := lookup("preview")
	fs := c.flagSet(e)
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	bin := filepath.Join(root, filepath.FromSlash(build.ServerBin))
	if fi, err := os.Stat(bin); err != nil || fi.IsDir() {
		fmt.Fprintf(e.stderr, "orbit preview: no %s in %s; run orbit build first\n", build.ServerBin, root)
		return exitFail
	}
	cmd := exec.Command(bin)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = e.stdout, e.stderr
	cmd.SysProcAttr = previewProcAttr()

	sigs := make(chan os.Signal, 1)
	if len(forwardSignals) > 0 {
		signal.Notify(sigs, forwardSignals...)
		defer signal.Stop(sigs)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(e.stderr, "orbit preview:", err)
		return exitFail
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
		case err := <-done:
			var exit *exec.ExitError
			switch {
			case err == nil:
				return exitOK
			case errors.As(err, &exit) && exit.ExitCode() >= 0:
				return exit.ExitCode()
			case errors.As(err, &exit):
				// Killed by a signal: no exit code to pass on.
				fmt.Fprintf(e.stderr, "orbit preview: %s: %v\n", build.ServerBin, err)
				return exitFail
			default:
				fmt.Fprintln(e.stderr, "orbit preview:", err)
				return exitFail
			}
		}
	}
}
