package dev

import (
	"context"
	"fmt"

	"github.com/orbit-z/lyr/internal/build"
)

// Pipeline is the resident rebuild pipeline of lyr dev without the
// watcher, the proxy and the server swap, so bench/devloop can measure
// exactly what lyr dev runs after a save.
type Pipeline struct{ pl *pipeline }

// Result is the outcome of one Pipeline.Run.
type Result struct {
	// Bin is the new server binary; "" when the running server stays (an
	// asset-only or public/ change).
	Bin string
	// Assets is set when the CSS/JS bundles were rebuilt.
	Assets  bool
	Timings Timings
	// Failure is the error text when the build failed.
	Failure string
}

// NewPipeline returns the pipeline for the module at root. env is
// appended to the go commands' environment.
func NewPipeline(ctx context.Context, root string, env []string) (*Pipeline, error) {
	goflags, err := build.GoFlags(ctx, root, env)
	if err != nil {
		return nil, fmt.Errorf("go env GOFLAGS: %w", err)
	}
	return &Pipeline{pl: newPipeline(root, env, goflags)}, nil
}

// Run rebuilds after the given changed paths (relative to the root,
// slash-separated), as lyr dev does after a debounced batch. The first
// Run builds everything. appRunning says whether a server from an earlier
// Run is still running.
func (p *Pipeline) Run(ctx context.Context, paths []string, appRunning bool) (Result, error) {
	out, err := p.pl.run(ctx, NewChange(paths, nil), appRunning)
	r := Result{Bin: out.Bin, Assets: out.Assets, Timings: out.Timings}
	if out.Failure != nil {
		r.Failure = out.Failure.Text
	}
	return r, err
}

// Close releases the esbuild context.
func (p *Pipeline) Close() { p.pl.close() }
