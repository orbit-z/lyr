package project

import (
	"bytes"
	"context"
	"fmt"

	"github.com/orbit-z/orbit/internal/compiler"
)

// Incremental is the resident typed pass of orbit dev (PLAN.md §3.3 Dev):
// it keeps one compiler.Checker between edits and re-checks only the files
// whose pass-1 output changed (and the importers of a changed decl
// package), reusing the previous output of every other file. It is not
// safe for concurrent use.
type Incremental struct {
	checker *compiler.Checker
	prev    map[string]*prevFile
	checked bool // the first Check ran
}

// prevFile is what Incremental remembers about a file between edits.
type prevFile struct {
	pass1Decl, pass1Impl []byte
	out                  *compiler.Output
	typed                bool
	writes               compiler.TypedWrites
	diags                []compiler.Diagnostic
}

// NewIncremental returns an Incremental around checker.
func NewIncremental(checker *compiler.Checker) *Incremental {
	return &Incremental{checker: checker, prev: map[string]*prevFile{}}
}

// Invalidate drops the loaded export data: call it when a user .go file,
// go.mod or orbit.json changed, so the next Typecheck reruns go list
// -export and re-checks every file.
func (in *Incremental) Invalidate() { in.checker.Invalidate() }

// Changed reports whether p (after Front) differs from the last Typecheck
// in anything the generated Go code depends on: a file added or removed,
// or a different pass-1 output. When it reports false, regenerating would
// write nothing for the .orbit files.
func (in *Incremental) Changed(p *Project) bool {
	if !in.checked || len(p.Files) != len(in.prev) {
		return true
	}
	for _, f := range p.Files {
		old := in.prev[f.Path]
		if old == nil || !bytes.Equal(old.pass1Decl, f.Pass1.Decl.Content) || !bytes.Equal(old.pass1Impl, f.Pass1.Impl.Content) {
			return true
		}
	}
	return false
}

// Typecheck is Project.Typecheck for orbit dev: the first call checks
// every file; later ones Update the checker with the files whose pass-1
// output changed and the removed ones. It sets every File's Output, adds
// the typed pass's diagnostics to p.Diagnostics, and returns the
// checker's warnings and the number of files re-checked. The error is
// only for ctx cancellation.
func (in *Incremental) Typecheck(ctx context.Context, p *Project) (warnings []string, rechecked int, err error) {
	var res *compiler.CheckResult
	if !in.checked {
		res, err = in.checker.Check(ctx, p.Units())
	} else {
		var changed []compiler.Unit
		seen := make(map[string]bool, len(p.Files))
		for _, f := range p.Files {
			seen[f.Path] = true
			old := in.prev[f.Path]
			if old == nil || !bytes.Equal(old.pass1Decl, f.Pass1.Decl.Content) || !bytes.Equal(old.pass1Impl, f.Pass1.Impl.Content) {
				changed = append(changed, p.unit(f))
			}
		}
		var removed []string
		for path := range in.prev {
			if !seen[path] {
				removed = append(removed, path)
				delete(in.prev, path)
			}
		}
		res, err = in.checker.Update(ctx, changed, removed)
	}
	if err != nil {
		return nil, 0, err
	}
	in.checked = true
	byImpl := make(map[string]compiler.TypedUnit, len(res.Units))
	for _, u := range res.Units {
		byImpl[u.Output.Impl.Path] = u
	}
	var ds []Diagnostic
	for _, f := range p.Files {
		old := in.prev[f.Path]
		var diags []compiler.Diagnostic
		if u, ok := byImpl[f.Pass1.Impl.Path]; ok {
			f.Output, f.Typed, f.Writes, diags = u.Output, u.Checked, u.Writes, u.Diagnostics
		} else if old != nil {
			f.Output, f.Typed, f.Writes, diags = old.out, old.typed, old.writes, old.diags
		} else {
			return nil, 0, fmt.Errorf("orbit: %s was not type-checked", f.Path)
		}
		for _, d := range diags {
			ds = append(ds, fromCompiler(f, d))
		}
		in.prev[f.Path] = &prevFile{pass1Decl: f.Pass1.Decl.Content, pass1Impl: f.Pass1.Impl.Content, out: f.Output, typed: f.Typed, writes: f.Writes, diags: diags}
	}
	if len(ds) > 0 {
		p.Diagnostics = append(p.Diagnostics, ds...)
		sortDiagnostics(p.Diagnostics)
	}
	return res.Warnings, len(res.Units), nil
}
