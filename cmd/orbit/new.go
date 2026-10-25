package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bertrandkhe/orbit/internal/project"
	"github.com/bertrandkhe/orbit/internal/scaffold"
)

// runNew is orbit new <dir> [--module path]: write the starter, generate
// orbit_gen/, then go mod tidy (PLAN.md §6). Generate comes first because
// cmd/server imports orbit_gen/app. On a failure after the files are
// written, the directory is left for inspection.
func runNew(e *env, args []string) int {
	c := lookup("new")
	fs := c.flagSet(e)
	module := fs.String("module", "", "the Go module path (default: the directory name)")
	// Flags may come before or after <dir>, as in orbit new blog --module x.
	var dir string
	for {
		if code := parse(fs, args); code >= 0 {
			return code
		}
		if fs.NArg() == 0 {
			break
		}
		if dir != "" {
			fmt.Fprintf(e.stderr, "orbit new: unexpected argument %q\n", fs.Arg(0))
			fs.Usage()
			return exitUsage
		}
		dir, args = fs.Arg(0), fs.Args()[1:]
	}
	if dir == "" {
		fmt.Fprintln(e.stderr, "orbit new: missing directory")
		fs.Usage()
		return exitUsage
	}
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.dir, dir)
	}
	abs = filepath.Clean(abs)
	if *module == "" {
		*module = filepath.Base(abs)
	}
	fail := func(err error) int {
		fmt.Fprintln(e.stderr, "orbit new:", err)
		return exitFail
	}
	if err := scaffold.CheckModulePath(*module); err != nil {
		return fail(err)
	}
	if err := scaffold.CheckDir(abs); err != nil {
		return fail(err)
	}
	src, err := scaffold.ResolveSource(os.Getenv(scaffold.ReplaceEnv))
	if err != nil {
		return fail(err)
	}
	opts := scaffold.Options{Module: *module, Toolchain: scaffold.Toolchain(), Version: src.Version, Replace: src.Replace}
	if err := scaffold.Create(abs, opts); err != nil {
		return fail(fmt.Errorf("%v (the files written so far are in %s)", err, dir))
	}

	// Before go mod tidy, go.mod has no requirements for the CLI's
	// dependencies and there's no go.sum, so the typed pass's go list runs
	// with -mod=mod.
	g := &project.Generator{Root: abs, Env: []string{"GOFLAGS=" + strings.TrimSpace(os.Getenv("GOFLAGS")+" -mod=mod")}}
	res, err := g.Run(e.ctx)
	if err == nil && report(e, res) != exitOK {
		err = errors.New("generating orbit_gen/ failed")
	}
	if err != nil {
		return fail(fmt.Errorf("orbit generate: %v; the project is in %s", err, dir))
	}
	tidy := exec.CommandContext(e.ctx, "go", "mod", "tidy")
	tidy.Dir = abs
	tidy.Stdout, tidy.Stderr = e.stderr, e.stderr
	if err := tidy.Run(); err != nil {
		return fail(fmt.Errorf("go mod tidy: %v; the project is in %s", err, dir))
	}

	fmt.Fprintf(e.stdout, "Created %s (module %s).\n\nNext steps:\n\n\tcd %s\n\tgo tool orbit dev\n", dir, *module, dir)
	if src.Replace != "" {
		fmt.Fprintf(e.stdout, "\ngo.mod replaces %s with your local checkout, %s.\n", scaffold.OrbitModule, src.Replace)
	}
	return exitOK
}
