package project

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/bertrandkhe/orbit/internal/assets"
	"github.com/bertrandkhe/orbit/internal/compiler"
)

// Generator runs the whole generate pipeline (PLAN.md §6 orbit build steps
// 1, 2 and 4): orbit generate runs it once; orbit build runs it before
// bundling and building.
type Generator struct {
	// Root is the module root.
	Root string
	// BuildFlags are passed to go list -export (-tags, -mod, …), and Env is
	// appended to the environment of the go command (GOFLAGS, GOOS, … pass
	// through from the process environment anyway).
	BuildFlags []string
	Env        []string

	checker *compiler.Checker
	golist  atomic.Int64
}

// Timings is the time spent in each stage of one Run.
type Timings struct {
	Load   time.Duration // orbit.json, go.mod, discovery, reading sources
	Front  time.Duration // parse, analyze, index, project checks, pass 1
	Assets time.Duration // esbuild: the page bundles, for the manifest
	GoList time.Duration // go list -export (the typed pass's Load)
	Check  time.Duration // go/types checks and pass 2, without GoList
	Extras time.Duration // app.go, manifest.go, the default server
	Write  time.Duration // writing orbit_gen/ and removing stale files
	Total  time.Duration
}

// Result is the outcome of a Run.
type Result struct {
	Project *Project
	// Diagnostics are every error and warning about .orbit files, sorted.
	// With any error, nothing was written.
	Diagnostics []Diagnostic
	// Warnings are the typed pass's warnings that aren't about a position
	// (unreadable export data).
	Warnings []string
	// Write says what changed on disk; nil when nothing was written.
	Write   *WriteResult
	Timings Timings
}

// HasErrors reports whether the run found errors (and so wrote nothing).
func (r *Result) HasErrors() bool { return HasErrors(r.Diagnostics) }

// Run loads the project, compiles every file with typed writes and writes
// orbit_gen/. Problems in the user's files are Result diagnostics; the error
// is for problems that stop the run early (no go.mod, an invalid
// orbit.json, I/O errors, ctx cancellation).
func (g *Generator) Run(ctx context.Context) (*Result, error) {
	start := time.Now()
	res := &Result{}
	t := &res.Timings
	defer func() { t.Total = time.Since(start) }()

	p, err := Load(g.Root)
	if err != nil {
		return nil, err
	}
	res.Project = p
	t.Load = time.Since(start)

	mark := time.Now()
	hasErrors := p.Front()
	res.Diagnostics = p.Diagnostics
	t.Front = time.Since(mark)
	if hasErrors {
		return res, nil
	}

	// PLAN.md §6 orbit build step 3: bundle before generating, since the
	// manifest lists the bundles. orbit generate bundles in memory too, so
	// that it writes the same orbit_gen/ as orbit build.
	mark = time.Now()
	b := assets.New(assets.Options{Root: p.Root, Sourcemap: p.Config.Sourcemap})
	ds, err := p.Bundle(b)
	b.Close()
	if err != nil {
		return nil, err
	}
	t.Assets = time.Since(mark)
	if len(ds) > 0 {
		res.Diagnostics = append(res.Diagnostics, ds...)
		sortDiagnostics(res.Diagnostics)
		p.Diagnostics = res.Diagnostics
		if HasErrors(ds) {
			return res, nil
		}
	}

	mark = time.Now()
	if g.checker == nil {
		loader := &compiler.GoListLoader{Dir: p.Root, BuildFlags: g.BuildFlags, Env: g.Env}
		g.checker = compiler.NewChecker(timedLoader{loader: loader, d: &g.golist})
	}
	g.golist.Store(0)
	res.Warnings, err = p.Typecheck(ctx, g.checker)
	if err != nil {
		return nil, err
	}
	t.GoList = time.Duration(g.golist.Load())
	t.Check = time.Since(mark) - t.GoList
	res.Diagnostics = p.Diagnostics
	if HasErrors(res.Diagnostics) {
		return res, nil
	}

	mark = time.Now()
	files := p.GenFiles()
	t.Extras = time.Since(mark)

	mark = time.Now()
	res.Write, err = WriteGenerated(p.Root, files)
	if err != nil {
		return nil, err
	}
	t.Write = time.Since(mark)
	return res, nil
}
