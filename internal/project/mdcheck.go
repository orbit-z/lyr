package project

import (
	"context"
	goparser "go/parser"
	gotoken "go/token"
	"slices"
	"strconv"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// MarkdownChecker is the language server's typed pass while the user types
// (PLAN.md §9, §9b): it type-checks only the Markdown files that have a
// layout and, with TypecheckScope's Data and Security scopes, the .orbit
// files with orbit.JSON or set:json sites (whose Types the editor's
// TypeScript reads) and those with style or on* warning candidates (whose
// warnings the editor shows, PLAN.md §2.7), plus the files whose generated decl packages they import (transitively),
// and keeps its
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
	return m.TypecheckScope(ctx, p, CheckScope{Markdown: true})
}

// CheckScope is what a MarkdownChecker run type-checks (with the files
// those depend on).
type CheckScope struct {
	Markdown bool // the Markdown files with a layout
	Data     bool // the .orbit files with a data contract (HasData)
	// Security is the .orbit files with style or on* warning candidates
	// (HasSecurity), with spreads on component tags (HasSpread) or with
	// set:html={expr} values (HasSetHTML), whose
	// errors only the typed pass finds (PLAN.md §2.11 (Q5)).
	Security bool
}

// TypecheckScope is Typecheck over the files of scope and their
// dependencies. The files checked get their Types set too.
func (m *MarkdownChecker) TypecheckScope(ctx context.Context, p *Project, scope CheckScope) (checked int, loaded bool, err error) {
	files := markdownUnits(p, scope)
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
		f.Output, f.Typed, f.Writes, f.Types = u.Output, u.Checked, u.Writes, u.Types
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

// HasData reports whether the .orbit file f (after Front) has a data
// contract to type-check (PLAN.md §5c): a set:json block or a direct
// orbit.JSON call in a data-* attribute, or another data-* attribute in a
// file that calls JSON (its value may be an orbit.JSONValue from a
// variable). Other data-* attributes carry no keys.
func HasData(f *File) bool {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return false
	}
	for _, s := range f.Pass1.JSONSites {
		if s.Block || s.Direct {
			return true
		}
	}
	return len(f.Pass1.JSONSites) > 0 && strings.Contains(f.Src, "JSON(")
}

// HasSecurity reports whether the .orbit file f (after Front) has a
// candidate for a style or on* security warning (PLAN.md §2.7): such an
// attribute with an expression value on an HTML element, or passed through
// to a component's orbit.RestAttrs field (its pass-1 call sites). Only then
// can the typed pass warn in f.
func HasSecurity(f *File) bool {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return false
	}
	for i := range f.Pass1.CallSites {
		cs := &f.Pass1.CallSites[i]
		if (cs.Kind == compiler.SiteAttr || cs.Kind == compiler.SiteRest || cs.Kind == compiler.SiteLit) && cs.Attr != nil && compiler.SecurityWarning(cs.AttrName) != "" {
			return true
		}
	}
	return false
}

// HasSpread reports whether the .orbit file f (after Front) has a spread
// on a component tag (PLAN.md §2.11): only the typed pass can tell whether
// it sets props or pass-through attributes, and find its errors.
func HasSpread(f *File) bool {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return false
	}
	for i := range f.Pass1.CallSites {
		if f.Pass1.CallSites[i].Kind == compiler.SiteSpread {
			return true
		}
	}
	return false
}

// HasSetHTML reports whether the .orbit file f (after Front) has a
// set:html={expr} value that the typed pass checks (PLAN.md §2.6): only it
// reports a value that isn't an orbit.HTML or an orbit.Opt[orbit.HTML].
func HasSetHTML(f *File) bool {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return false
	}
	for i := range f.Pass1.CallSites {
		if f.Pass1.CallSites[i].Kind == compiler.SiteHTML {
			return true
		}
	}
	return false
}

// root reports whether f is in scope itself (not as a dependency).
func (s CheckScope) root(f *File) bool {
	return s.Markdown && f.MD != nil && f.MD.Layout != "" || s.Data && HasData(f) || s.Security && (HasSecurity(f) || HasSpread(f) || HasSetHTML(f))
}

// markdownUnits returns, in p.Files order, the files of scope and every
// file whose generated decl package they import, directly or through other
// decl packages: what their Retype or impl check needs.
func markdownUnits(p *Project, scope CheckScope) []*File {
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
		if f.Pass1 == nil || f.Pass1.Decl == nil || byDecl[f.Pass1.Decl.ImportPath] != f {
			continue
		}
		if scope.root(f) {
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
		if f.MD != nil || HasData(f) || HasSecurity(f) || HasSpread(f) || HasSetHTML(f) {
			imports(f.Pass1.Impl) // the layout's (or the components') decl packages
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
