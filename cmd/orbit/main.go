// Command orbit is the Orbit CLI: it compiles .orbit components to Go,
// builds and runs Orbit sites (PLAN.md §6).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/bertrandkhe/orbit/internal/config"
	"github.com/bertrandkhe/orbit/internal/project"
)

// Exit codes (PLAN.md §6), following Go's flag package.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// command is one orbit subcommand.
type command struct {
	name  string
	args  string // usage after the name, e.g. "[--embed]"
	short string
	// run runs the command with its arguments (flags not yet parsed).
	run func(env *env, args []string) int
}

// env is what commands print to and run in.
type env struct {
	stdout, stderr io.Writer
	dir            string // working directory
	ctx            context.Context
}

var commands []*command

func init() {
	commands = []*command{
		{name: "generate", short: "generate the Go code in orbit_gen/", run: runGenerate},
		{name: "build", args: "[--embed]", short: "build the server and its assets into dist/", run: notYet("build")},
		{name: "dev", args: "[--port 4321] [--host]", short: "run the dev server", run: notYet("dev")},
		{name: "preview", short: "run the built server", run: notYet("preview")},
		{name: "new", args: "<dir> [--module path]", short: "create a project", run: notYet("new")},
		{name: "check", short: "type-check Go and TypeScript", run: notYet("check")},
		{name: "doctor", args: "[--json]", short: "check the environment and project", run: notYet("doctor")},
		{name: "create-key", short: "print a new island key for ORBIT_KEY", run: notYet("create-key")},
		{name: "lsp", short: "language server over stdio", run: notYet("lsp")},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orbit:", err)
		os.Exit(exitFail)
	}
	code := run(&env{stdout: os.Stdout, stderr: os.Stderr, dir: dir, ctx: ctx}, os.Args[1:])
	stop()
	os.Exit(code)
}

// run dispatches to a command and returns the exit code.
func run(e *env, args []string) int {
	if len(args) == 0 {
		usage(e.stderr)
		return exitUsage
	}
	name := args[0]
	switch name {
	case "help", "-h", "-help", "--help":
		if len(args) > 1 {
			if c := lookup(args[1]); c != nil {
				fs := c.flagSet(e)
				fs.SetOutput(e.stdout)
				fs.Usage()
				return exitOK
			}
			fmt.Fprintf(e.stderr, "orbit help %s: unknown command\n", args[1])
			return exitUsage
		}
		usage(e.stdout)
		return exitOK
	}
	c := lookup(name)
	if c == nil {
		fmt.Fprintf(e.stderr, "orbit %s: unknown command\nRun 'orbit help' for usage.\n", name)
		return exitUsage
	}
	return c.run(e, args[1:])
}

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, "Orbit compiles .orbit components to Go and serves them.\n\nUsage:\n\n\torbit <command> [arguments]\n\nThe commands are:\n\n")
	for _, c := range commands {
		fmt.Fprintf(w, "\t%-12s %s\n", c.name, c.short)
	}
	fmt.Fprint(w, "\nUse \"orbit help <command>\" for more information about a command.\n")
}

// flagSet returns the command's flag set, with a usage line. Parse errors
// print the usage and give exit code 2, as flag.ExitOnError does, but
// without exiting so run stays testable.
func (c *command) flagSet(e *env) *flag.FlagSet {
	fs := flag.NewFlagSet("orbit "+c.name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: orbit %s", c.name)
		if c.args != "" {
			fmt.Fprintf(fs.Output(), " %s", c.args)
		}
		fmt.Fprintf(fs.Output(), "\n\n%s%s.\n", strings.ToUpper(c.short[:1]), c.short[1:])
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args with fs. It returns -1 to continue, or the exit code:
// 0 for -h, 2 for a bad flag.
func parse(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	return -1
}

func notYet(name string) func(*env, []string) int {
	return func(e *env, _ []string) int {
		fmt.Fprintf(e.stderr, "orbit %s: not implemented yet\n", name)
		return exitFail
	}
}

// runGenerate is orbit generate: generate orbit_gen/ for the project around
// the working directory.
func runGenerate(e *env, args []string) int {
	c := lookup("generate")
	fs := c.flagSet(e)
	if code := parse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.stderr, "orbit generate: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	g := &project.Generator{Root: root}
	res, err := g.Run(e.ctx)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	return report(e, res)
}

// report prints a generate result's diagnostics and warnings to stderr and
// returns the exit code.
func report(e *env, res *project.Result) int {
	for _, d := range res.Diagnostics {
		fmt.Fprintln(e.stderr, d)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(e.stderr, w)
	}
	if res.HasErrors() {
		return exitFail
	}
	return exitOK
}
