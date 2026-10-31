package dev

import (
	"context"
	"path"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/tailwind"
)

// Tailwind in the dev loop (PLAN.md §5d, Q4). The bundler runs Tailwind
// inputs in esbuild's load step and caches each output. At the start of a
// batch the pipeline decides what to rerun:
//   - a .css file, tsconfig.json or package.json changed: every input
//     (Lyr doesn't track Tailwind's import graph); esbuild runs anyway,
//     and reruns them on its critical path;
//   - another changed file brings a class candidate the token gate hasn't
//     seen: the root inputs (those that scan the sources), started at
//     once, in parallel with regenerating and go build. When an output
//     changed and esbuild didn't run in the batch, it runs after them.
//
// A block input whose text changed is a new cache entry: esbuild runs for
// it like for any <style> edit. Nothing here runs before the project has
// a Tailwind input.

// twRun is a batch's early Tailwind reruns.
type twRun struct {
	pend *tailwind.Pending
	// done is closed when the bundle after the reruns is done, if it was
	// started (afterAssets); failure and err are its outcome.
	done    chan struct{}
	bundled bool
	failure *failure
	err     error
}

// tailwindStart applies a batch to the Tailwind cache and starts the
// reruns it needs; nil when none.
func (pl *pipeline) tailwindStart(ctx context.Context, c Change) *twRun {
	if !pl.tw.Active() || pl.gate == nil {
		return nil
	}
	css := false
	var others []string
	for _, p := range c.Paths {
		switch base := path.Base(p); {
		case path.Ext(p) == ".css", p == "tsconfig.json", p == "package.json":
			css = true
		case base == "lyr.json", base == "go.sum":
		default:
			others = append(others, p)
		}
	}
	// The gate always sees the batch, so that its set stays current.
	sources := pl.gate.Changed(others)
	if css {
		pl.tw.Invalidate(true, false)
		return nil
	}
	if !sources {
		pl.tw.DropErrors()
		return nil
	}
	pl.tw.Invalidate(false, true)
	pl.tr("tailwind")
	return &twRun{pend: pl.tw.Rerun(ctx)}
}

// tailwindBundled is called after each successful bundle: the first one
// with a Tailwind input makes the token gate, seeded in the background
// from the files as Tailwind just saw them.
func (pl *pipeline) tailwindBundled() {
	if pl.gate == nil && pl.tw.Active() {
		pl.gate = tailwind.NewGate(pl.root, skipDir)
	}
}

// tailwindAfterAssets runs after the batch's asset step: when esbuild
// didn't run, the bundles are rebuilt in the background once the reruns
// are done, if an output changed.
func (pl *pipeline) tailwindAfterAssets(tr *twRun, ran bool, in *assets.Input, key string) {
	if tr == nil || ran || tr.done != nil {
		return
	}
	tr.done = make(chan struct{})
	go func() {
		defer close(tr.done)
		if !tr.pend.Wait() {
			return
		}
		pl.tr("esbuild-tailwind")
		tr.failure, tr.err = pl.bundle(in, key)
		tr.bundled = tr.err == nil && tr.failure == nil
	}()
}

// tailwindFinish waits for the batch's Tailwind work before the run
// returns, so the reload shows the new CSS.
func (pl *pipeline) tailwindFinish(tr *twRun, out *outcome, err *error) {
	if tr == nil {
		return
	}
	if tr.done == nil {
		// The run ended before its asset step (a watched file only, or a
		// failure): bundle from the last input when it can, else make the
		// next run bundle.
		if !tr.pend.Wait() {
			return
		}
		if *err == nil && out.Failure == nil && pl.lastInput != nil && !out.Assets {
			pl.tr("esbuild-tailwind")
			f, e := pl.bundle(pl.lastInput, pl.assetKey)
			if e != nil {
				*err = e
			} else if f != nil {
				out.Failure = f
			} else {
				out.Assets = true
			}
			return
		}
		pl.assetKey = ""
		return
	}
	<-tr.done
	switch {
	case tr.err != nil && *err == nil:
		*err = tr.err
	case tr.failure != nil && out.Failure == nil:
		out.Failure = tr.failure
	case tr.bundled:
		out.Assets = true
	}
}
