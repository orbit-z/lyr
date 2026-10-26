package project

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/compiler"
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
	golists atomic.Int32
}

// Timings is the time spent in each stage of one Run.
type Timings struct {
	Load   time.Duration // orbit.json, go.mod, discovery, reading sources
	Front  time.Duration // parse, analyze, index, project checks, pass 1
	Assets time.Duration // esbuild: the page bundles, for the manifest
	// GoList is the time go list -export ran (the typed pass's Load). It
	// starts during Front, so it overlaps Front, Assets and Check.
	GoList time.Duration
	// GoLists is the number of go list runs: 1, or 2 when the one started
	// during Front missed a package.
	GoLists int
	Check   time.Duration // go/types checks and pass 2, with any wait for go list
	Extras  time.Duration // app.go, manifest.go, the default server
	Write   time.Duration // writing orbit_gen/ and removing stale files
	Total   time.Duration
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
	res, files, err := g.Plan(ctx)
	if err != nil || files == nil {
		return res, err
	}
	t := &res.Timings
	mark := time.Now()
	res.Write, err = WriteGenerated(res.Project.Root, files)
	if err != nil {
		return nil, err
	}
	t.Write = time.Since(mark)
	t.Total += t.Write
	return res, nil
}

// Plan runs the pipeline as Run does but writes nothing: it returns the
// files Run would write, or nil files when the run found errors (see
// Result.Diagnostics). orbit doctor compares them with orbit_gen/.
func (g *Generator) Plan(ctx context.Context) (*Result, []GenFile, error) {
	start := time.Now()
	res := &Result{}
	t := &res.Timings
	defer func() { t.Total = time.Since(start) }()

	p, err := Load(g.Root)
	if err != nil {
		return nil, nil, err
	}
	res.Project = p
	t.Load = time.Since(start)

	if g.checker == nil {
		loader := &compiler.GoListLoader{Dir: p.Root, BuildFlags: g.BuildFlags, Env: g.Env}
		g.checker = compiler.NewChecker(timedLoader{loader: loader, d: &g.golist, n: &g.golists})
	}
	g.golist.Store(0)
	g.golists.Store(0)
	mark := time.Now()
	// go list -export starts once the files are parsed, and runs during
	// pass 1 and esbuild.
	hasErrors := p.FrontPreload(g.checker)
	res.Diagnostics = p.Diagnostics
	t.Front = time.Since(mark)
	if hasErrors {
		g.checker.Preload(nil)
		return res, nil, nil
	}

	// PLAN.md §6 orbit build step 3: bundle before generating, since the
	// manifest lists the bundles. orbit generate bundles in memory too, so
	// that it writes the same orbit_gen/ as orbit build.
	mark = time.Now()
	b := assets.New(assets.Options{Root: p.Root, Sourcemap: p.Config.Sourcemap})
	ds, err := p.Bundle(b)
	b.Close()
	if err != nil {
		g.checker.Preload(nil)
		return nil, nil, err
	}
	t.Assets = time.Since(mark)
	if len(ds) > 0 {
		res.Diagnostics = append(res.Diagnostics, ds...)
		sortDiagnostics(res.Diagnostics)
		p.Diagnostics = res.Diagnostics
		if HasErrors(ds) {
			g.checker.Preload(nil)
			return res, nil, nil
		}
	}

	mark = time.Now()
	res.Warnings, err = p.Typecheck(ctx, g.checker)
	if err != nil {
		return nil, nil, err
	}
	t.GoList = time.Duration(g.golist.Load())
	t.GoLists = int(g.golists.Load())
	t.Check = time.Since(mark)
	res.Diagnostics = p.Diagnostics
	if HasErrors(res.Diagnostics) {
		return res, nil, nil
	}

	mark = time.Now()
	files := p.GenFiles()
	t.Extras = time.Since(mark)
	return res, files, nil
}
