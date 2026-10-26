package project

import (
	"context"
	goparser "go/parser"
	gotoken "go/token"
	"slices"
	"strconv"

	"github.com/orbit-z/orbit/internal/compiler"
)

// MarkdownChecker is the language server's typed pass for Markdown
// frontmatter while the user types (PLAN.md §9): it type-checks only the
// Markdown files that have a layout, plus the files whose generated decl
// packages they import (their layouts, transitively), and keeps its
// compiler.Checker between runs, so the go list -export of the first run
// is reused until Invalidate. It is not safe for concurrent use.
type MarkdownChecker struct {
	loader  compiler.ExportLoader
	checker *compiler.Checker // nil: the next run is a full Check
	units   map[string]bool   // the paths the checker has, after a run
	runs    int               // Updates since the last Check
}

// markdownResetRuns bounds the Updates of one Checker: each one adds the
// re-parsed files to the checker's token.FileSet, which only a new Check
// starts afresh.
const markdownResetRuns = 100

// NewMarkdownChecker returns a MarkdownChecker that loads non-generated
// packages through loader.
func NewMarkdownChecker(loader compiler.ExportLoader) *MarkdownChecker {
	return &MarkdownChecker{loader: loader}
}

// Invalidate drops the loaded export data, so the next Typecheck reruns go
// list -export: call it when a user .go file, go.mod or orbit.json changes.
func (m *MarkdownChecker) Invalidate() { m.checker = nil }

// Typecheck runs the typed pass over p (after Front) for its Markdown files
// with a layout and the files they depend on, setting those Files' Output
// and adding the typed pass's diagnostics to p.Diagnostics, like
// Project.Typecheck. Other files keep their pass-1 Output. It returns the
// number of files checked, and whether go list -export ran. The error is
// only for ctx cancellation; after one, or after load warnings, the next
// run starts afresh.
func (m *MarkdownChecker) Typecheck(ctx context.Context, p *Project) (checked int, loaded bool, err error) {
	files := markdownUnits(p)
	if len(files) == 0 {
		return 0, false, nil
	}
	units := make([]compiler.Unit, len(files))
	in := make(map[string]bool, len(files))
	for i, f := range files {
		units[i] = p.unit(f)
		in[f.Path] = true
	}
	var res *compiler.CheckResult
	if m.checker == nil || m.runs >= markdownResetRuns {
		m.checker, m.runs, loaded = compiler.NewChecker(m.loader), 0, true
		res, err = m.checker.Check(ctx, units)
	} else {
		var removed []string
		for path := range m.units {
			if !in[path] {
				removed = append(removed, path)
			}
		}
		before := m.checker.LoadedPaths()
		res, err = m.checker.Update(ctx, units, removed)
		m.runs++
		loaded = err == nil && !slices.Equal(before, m.checker.LoadedPaths())
	}
	if err != nil || len(res.Warnings) > 0 {
		m.checker = nil // a cancelled or failed load isn't reused
	}
	if err != nil {
		return 0, loaded, err
	}
	m.units = in
	byImpl := make(map[string]compiler.TypedUnit, len(res.Units))
	for _, u := range res.Units {
		byImpl[u.Output.Impl.Path] = u
	}
	var ds []Diagnostic
	for _, f := range files {
		u, ok := byImpl[f.Pass1.Impl.Path]
		if !ok {
			continue
		}
		f.Output, f.Typed, f.Writes = u.Output, u.Checked, u.Writes
		for _, d := range u.Diagnostics {
			ds = append(ds, fromCompiler(f, d))
		}
	}
	if len(ds) > 0 {
		p.Diagnostics = append(p.Diagnostics, ds...)
		sortDiagnostics(p.Diagnostics)
	}
	return len(files), loaded, nil
}

// markdownUnits returns, in p.Files order, the Markdown files with a layout
// and every file whose generated decl package they import, directly or
// through other decl packages: what their Retype needs.
func markdownUnits(p *Project) []*File {
	byDecl := map[string]*File{}
	for _, f := range p.Files {
		if f.Pass1 != nil && f.Pass1.Decl != nil && f.Pass1.Impl != nil {
			byDecl[f.Pass1.Decl.ImportPath] = f
		}
	}
	need := map[string]bool{}
	var queue []*File
	add := func(f *File) {
		if f != nil && !need[f.Path] {
			need[f.Path] = true
			queue = append(queue, f)
		}
	}
	for _, f := range p.Files {
		if f.MD != nil && f.MD.Layout != "" && f.Pass1 != nil && f.Pass1.Decl != nil && byDecl[f.Pass1.Decl.ImportPath] == f {
			add(f)
		}
	}
	fset := gotoken.NewFileSet()
	imports := func(g *compiler.GenFile) {
		af, err := goparser.ParseFile(fset, g.Path, g.Content, goparser.ImportsOnly)
		if err != nil {
			return
		}
		for _, im := range af.Imports {
			if path, err := strconv.Unquote(im.Path.Value); err == nil {
				add(byDecl[path])
			}
		}
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		imports(f.Pass1.Decl)
		if f.MD != nil {
			imports(f.Pass1.Impl) // the layout's decl package
		}
	}
	var out []*File
	for _, f := range p.Files {
		if need[f.Path] {
			out = append(out, f)
		}
	}
	return out
}
