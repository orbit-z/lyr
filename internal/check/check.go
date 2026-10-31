// Package check is lyr check (PLAN.md §6): type-check a project without
// building it.
//
// The Go half runs the generate pipeline (Lyr's own checks) and then
// go vet ./lyr_gen/..., cleaned like build errors (§3.3). The TypeScript
// half extracts every processed inline <script> to .lyr/check/, mirroring
// the src/ tree so relative imports resolve as from the .lyr file, writes
// a tsconfig.json that extends the user's, runs bun check and maps its
// positions back to the .lyr files. Without Bun ≥ 1.4.4, the TypeScript
// half is skipped with a notice.
package check

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/orbit-z/lyr/internal/build"
	"github.com/orbit-z/lyr/internal/project"
)

// Options configure a check.
type Options struct {
	// Root is the module root.
	Root string
	// Env is appended to the environment of the go and bun commands.
	Env []string
	// Bun is the bun command; "" looks it up on PATH.
	Bun string
}

// Timings is the time spent in each half of one Run.
type Timings struct {
	Generate time.Duration
	Vet      time.Duration // go vet; zero when skipped
	TS       time.Duration // extraction and bun check; zero when skipped
	Total    time.Duration
}

// Result is the outcome of a check. Its output is, in order: Generate's
// diagnostics and warnings, GoOutput, Notice, TSOutput.
type Result struct {
	// Generate is the generate step's result. With errors, go vet didn't
	// run (nothing was written).
	Generate *project.Result
	// GoOutput is go vet's cleaned output, and GoFailed is set when go vet
	// failed.
	GoOutput []byte
	GoFailed bool
	// Notice is set when the TypeScript half was skipped for lack of a
	// usable Bun; it doesn't fail the check.
	Notice string
	// TSOutput is bun check's mapped output, and TSFailed is set when it
	// found errors (or failed).
	TSOutput []byte
	TSFailed bool
	// Scripts are the extracted scripts.
	Scripts []Script
	Timings Timings
}

// Failed reports whether the check found an error.
func (r *Result) Failed() bool {
	return (r.Generate != nil && r.Generate.HasErrors()) || r.GoFailed || r.TSFailed
}

// Run checks the project at o.Root. Problems in the user's files are in
// the Result; the error is for problems that stop the check early (no
// go.mod, an invalid lyr.json, I/O errors, a missing go command, ctx
// cancellation).
func Run(ctx context.Context, o Options) (*Result, error) {
	start := time.Now()
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	defer func() { res.Timings.Total = time.Since(start) }()

	g := &project.Generator{Root: root, Env: o.Env}
	res.Generate, err = g.Run(ctx)
	if err != nil {
		return nil, err
	}
	res.Timings.Generate = time.Since(start)
	p := res.Generate.Project

	// The halves are independent: go vet reads lyr_gen/, bun check reads
	// src/ and .lyr/check/.
	var wg sync.WaitGroup
	var vetErr, tsErr error
	if !res.Generate.HasErrors() {
		wg.Go(func() {
			mark := time.Now()
			vetErr = res.vet(ctx, p, o.Env)
			res.Timings.Vet = time.Since(mark)
		})
	}
	wg.Go(func() {
		mark := time.Now()
		tsErr = res.typescript(ctx, p, o)
		if res.Notice == "" {
			res.Timings.TS = time.Since(mark)
		}
	})
	wg.Wait()
	if err := joinErr(ctx, vetErr, tsErr); err != nil {
		return nil, err
	}
	return res, nil
}

func joinErr(ctx context.Context, errs ...error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(errs...)
}

// vet runs go vet ./lyr_gen/... from the project root.
func (r *Result) vet(ctx context.Context, p *project.Project, env []string) error {
	cmd := exec.CommandContext(ctx, "go", "vet", "./lyr_gen/...")
	cmd.Dir = p.Root
	cmd.Env = append(os.Environ(), env...)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	if b.Len() > 0 {
		r.GoOutput = build.CleanOutput(p, b.Bytes())
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			r.GoFailed = true
			return nil
		}
		return fmt.Errorf("go vet: %w", err)
	}
	return nil
}

// typescript runs the TypeScript half. With nothing to check (no
// processed inline script and no .ts file under src/) it does nothing, and
// needs no Bun.
func (r *Result) typescript(ctx context.Context, p *project.Project, o Options) error {
	r.Scripts = Extract(p)
	if len(r.Scripts) == 0 && !hasTS(filepath.Join(p.Root, "src")) {
		return nil
	}
	bun, notice := findBun(ctx, o.Bun)
	if bun == "" {
		r.Notice = notice
		return nil
	}
	user, err := ReadUserTsconfig(p.Root)
	if err != nil {
		return err
	}
	if err := Sync(p.Root, r.Scripts, Tsconfig(user)); err != nil {
		return err
	}
	// --no-script: a package.json "check" script must not replace the
	// type checker.
	cmd := exec.CommandContext(ctx, bun, "check", "--no-pretty", "--no-script", "-p", Dir)
	cmd.Dir = p.Root
	cmd.Env = append(os.Environ(), o.Env...)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err = cmd.Run()
	out := MapOutput(p.Root, b.Bytes(), r.Scripts)
	r.TSOutput = out.Text
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || ctx.Err() != nil {
			return fmt.Errorf("bun check: %w", err)
		}
		r.TSFailed = true
	}
	if out.Errors > 0 {
		r.TSFailed = true
	}
	return nil
}

// hasTS reports whether dir has a .ts file (a .d.ts counts).
func hasTS(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(name, ".ts") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
