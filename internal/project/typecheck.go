package project

import (
	"context"
	gotoken "go/token"
	"go/types"
	"sync/atomic"
	"time"

	"github.com/orbit-z/orbit/internal/compiler"
)

// Units returns the typed-pass units of every file, from its pass-1 output.
func (p *Project) Units() []compiler.Unit {
	units := make([]compiler.Unit, len(p.Files))
	for i, f := range p.Files {
		units[i] = compiler.Unit{Input: f.Input, Pass1: f.Pass1}
	}
	return units
}

// Typecheck runs the typed-write pass (PLAN.md §3.3 Engine) with checker
// over every file's pass-1 output and sets each File's Output to its pass-2
// output (pass 1 when the file kept its fallbacks). The typed pass's own
// errors (server:defer Props that aren't JSON-serializable, PLAN.md §3.4)
// are added to p.Diagnostics. It returns the checker's warnings. The error
// is only for ctx cancellation.
func (p *Project) Typecheck(ctx context.Context, checker *compiler.Checker) ([]string, error) {
	res, err := checker.Check(ctx, p.Units())
	if err != nil {
		return nil, err
	}
	var ds []Diagnostic
	for i, u := range res.Units {
		f := p.Files[i]
		f.Output, f.Typed, f.Writes = u.Output, u.Checked, u.Writes
		for _, d := range u.Diagnostics {
			ds = append(ds, fromCompiler(f, d))
		}
	}
	if len(ds) > 0 {
		p.Diagnostics = append(p.Diagnostics, ds...)
		sortDiagnostics(p.Diagnostics)
	}
	return res.Warnings, nil
}

// timedLoader wraps an ExportLoader and adds the time spent in Load (the
// go list -export run) to d.
type timedLoader struct {
	loader compiler.ExportLoader
	d      *atomic.Int64
}

func (l timedLoader) Load(ctx context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error) {
	start := time.Now()
	defer func() { l.d.Add(int64(time.Since(start))) }()
	return l.loader.Load(ctx, fset, paths)
}
