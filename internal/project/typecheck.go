package project

import (
	"context"
	gotoken "go/token"
	"go/types"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/markdown"
)

// FrontPreload is Front, and starts checker's go list -export
// (Checker.Preload) as soon as the .lyr files are parsed, so that it runs
// during pass 1 and whatever comes before the Typecheck with checker (the
// esbuild bundles, in lyr build). It preloads the packages the generated
// code will import, as far as parsing tells: the runtime, src/locals, every
// .lyr file's Go imports, and time when there are Markdown files (their
// frontmatter dates). When that guess misses a package, Typecheck loads
// again.
func (p *Project) FrontPreload(checker *compiler.Checker) bool {
	return p.front(func() { checker.Preload(p.goImports()) })
}

// goImports is FrontPreload's guess, after parsing, of the packages outside
// lyr_gen/ that the generated code imports.
func (p *Project) goImports() []string {
	set := map[string]bool{compiler.RuntimePath: true}
	if p.Options.Locals != "" {
		set[p.Options.Locals] = true
	}
	if len(p.mdPaths) > 0 {
		set["time"] = true
	}
	for _, f := range p.Files {
		if f.Analysis != nil && f.MD == nil {
			for _, im := range f.Analysis.GoImports {
				set[im.Path] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// Units returns the typed-pass units of every file, from its pass-1 output.
func (p *Project) Units() []compiler.Unit {
	units := make([]compiler.Unit, len(p.Files))
	for i, f := range p.Files {
		units[i] = p.unit(f)
	}
	return units
}

// unit is f's typed-pass unit: a Markdown file converts its frontmatter to
// its layout's Props types instead (PLAN.md §2.4).
func (p *Project) unit(f *File) compiler.Unit {
	u := compiler.Unit{Input: f.Input, Pass1: f.Pass1}
	if f.MD != nil {
		u.Retype = markdown.Retype(f.MD, p.mdGenInput(f), f.Pass1)
	}
	return u
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
		f.Output, f.Typed, f.Writes, f.Types = u.Output, u.Checked, u.Writes, u.Types
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
// go list -export run) to d, and counts the runs in n.
type timedLoader struct {
	loader compiler.ExportLoader
	d      *atomic.Int64
	n      *atomic.Int32
}

func (l timedLoader) Load(ctx context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error) {
	start := time.Now()
	l.n.Add(1)
	defer func() { l.d.Add(int64(time.Since(start))) }()
	return l.loader.Load(ctx, fset, paths)
}
