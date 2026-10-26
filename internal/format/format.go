// Package format formats .orbit files (PLAN.md §6.1): the frontmatter,
// template expressions and control headers with go/format, and <script> and
// <style> content with bun fmt. Template text, tags, attributes and the
// whitespace between them are never changed, so a formatted file renders the
// same bytes.
//
// It is used by orbit fmt and by the language server's
// textDocument/formatting.
package format

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/orbit-z/orbit/internal/compiler"
)

// Options configures formatting.
type Options struct {
	// Path names the file in Bun's --stdin-filepath (with ".<n>.<ext>"
	// appended), so Bun finds the configuration that applies to it. Use the
	// path relative to BunDir, with / separators.
	Path string
	// Bun is the bun command (Bun 1.4.4 or later, see FindBun). Empty leaves
	// script and style content as it is.
	Bun string
	// BunDir is the directory bun runs in: the project root, or the file's
	// directory outside a project.
	BunDir string
	// Context cancels bun runs; nil means context.Background().
	Context context.Context
	// Timeout bounds each bun run; zero means 10 s.
	Timeout time.Duration
}

// DefaultTimeout bounds each bun fmt run.
const DefaultTimeout = 10 * time.Second

// Edit replaces Span of the original source with New.
type Edit struct {
	Span compiler.Span
	New  string
}

// Result is the outcome of formatting one file.
type Result struct {
	// Edits are the changed regions, in source order, not overlapping. After
	// a syntax error there are none; after a Bun error only the failed
	// block's edit is missing.
	Edits []Edit
	// Errors are syntax errors (the file is refused), and Bun errors and
	// timeouts (Bun is set to true then).
	Errors []compiler.Diagnostic
	// BunFailed is true when some of Errors came from Bun.
	BunFailed bool
	// Skipped counts the script and style blocks left as written because
	// Options.Bun is empty.
	Skipped int
	// Warnings are regions left as written because the safety check found
	// that formatting them would change their code.
	Warnings []compiler.Diagnostic
}

// Source formats src. When the file can't be formatted (any error in
// Result.Errors, Bun's included), out is src unchanged.
func Source(src string, opts Options) (out string, res Result) {
	res = Regions(src, opts)
	if len(res.Errors) > 0 {
		return src, res
	}
	return Apply(src, res.Edits), res
}

// Apply applies edits, sorted and not overlapping, to src.
func Apply(src string, edits []Edit) string {
	if len(edits) == 0 {
		return src
	}
	var b strings.Builder
	b.Grow(len(src) + len(src)/8)
	at := 0
	for _, e := range edits {
		b.WriteString(src[at:e.Span.Start])
		b.WriteString(e.New)
		at = e.Span.End
	}
	b.WriteString(src[at:])
	return b.String()
}

// Regions computes the edits that format src, for the language server: one
// per changed region.
func Regions(src string, opts Options) Result {
	f := compiler.Parse(src, compiler.Strict)
	if errs := f.SyntaxErrors(); len(errs) > 0 {
		return Result{Errors: errs}
	}
	fx := &fileFmt{src: src, f: f, opts: opts}
	fx.crlf = isCRLF(src)
	fx.collect()
	fx.unit = fx.detectUnit()
	fx.formatAll()
	if len(fx.errs) > 0 {
		return Result{Errors: fx.errs}
	}
	skipped := 0
	if opts.Bun == "" {
		skipped = fx.countBlocks()
	}
	edits := fx.edits()
	res := Result{Edits: edits, Errors: fx.bunErrs, BunFailed: len(fx.bunErrs) > 0, Skipped: skipped, Warnings: fx.warns}
	// The template must parse to the same tree; otherwise give up on the
	// whole file (this would be an Orbit bug).
	if len(edits) > 0 {
		out := Apply(src, edits)
		g := compiler.Parse(out, compiler.Strict)
		if len(g.SyntaxErrors()) > 0 || treeSig(f) != treeSig(g) {
			res.Edits = nil
			res.Warnings = append(res.Warnings, warning(compiler.Span{}, "formatting this file would change its template; left as is (please report this)"))
		}
	}
	sortDiags(res.Errors)
	sortDiags(res.Warnings)
	return res
}

func sortDiags(ds []compiler.Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Start < ds[j].Start })
}

func warning(s compiler.Span, msg string) compiler.Diagnostic {
	return compiler.Diagnostic{Severity: compiler.SeverityWarning, Span: s, Message: msg}
}

func errorAt(off int, msg string) compiler.Diagnostic {
	return compiler.Diagnostic{Severity: compiler.SeverityError, Span: compiler.Span{Start: off, End: off}, Message: msg}
}

// isCRLF reports whether the file's first line break is \r\n.
func isCRLF(src string) bool {
	i := strings.IndexByte(src, '\n')
	return i > 0 && src[i-1] == '\r'
}

// lf turns \r\n line breaks into \n.
func lf(s string) string {
	if !strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// regionKind is what a region holds, for its formatting and its messages.
type regionKind uint8

const (
	rFrontmatter regionKind = iota
	rExpr                   // {expr} in content
	rAttrExpr               // a={expr}
	rShorthand              // {a}
	rSpread                 // {...attrs}
	rIf                     // {if c}
	rElseIf                 // {else if c}
	rElse                   // {else}
	rEndIf                  // {/if}
	rFor                    // {for clause}
	rEndFor                 // {/for}
	rSwitch                 // {switch clause}
	rCase                   // {case list}
	rDefault                // {default}
	rEndSwitch              // {/switch}
	rScript
	rStyle
)

// region is one span of the file that formatting may replace.
type region struct {
	kind regionKind
	span compiler.Span
	// code is the Go code (expressions and headers) or the raw content
	// (scripts and styles), as written.
	code compiler.Span
	// clause is the {switch} clause of a {case} list, as written: the case
	// list is formatted inside that switch, since a type switch lists types.
	clause compiler.Span
	// node is the Script or Style.
	node compiler.Node
	// index is a block's index among the file's scripts (or styles).
	index int
	// indent is the leading whitespace of the line the region (a block:
	// its start tag) starts on, in the output.
	indent string
	// out is the replacement, with \n line breaks; done is false when the
	// region is left as written.
	out  string
	done bool
}

type fileFmt struct {
	src     string
	f       *compiler.File
	opts    Options
	crlf    bool
	unit    string
	regions []*region
	errs    []compiler.Diagnostic
	bunErrs []compiler.Diagnostic
	warns   []compiler.Diagnostic
}

// collect lists the regions in source order.
func (fx *fileFmt) collect() {
	if fx.f.Frontmatter.Present {
		fm := fx.f.Frontmatter
		fx.regions = append(fx.regions, &region{kind: rFrontmatter, span: compiler.Span{Start: fm.Open.Start, End: fm.Close.End}, code: fm.Code.Span})
	}
	scripts, styles := 0, 0
	add := func(r *region) { fx.regions = append(fx.regions, r) }
	attrs := func(t *compiler.Tag) {
		for _, a := range t.Attrs {
			switch a.Kind {
			case compiler.AttrExpr:
				add(&region{kind: rAttrExpr, span: a.ValueSpan, code: a.Code.Span})
			case compiler.AttrShorthand:
				add(&region{kind: rShorthand, span: a.Span, code: a.Code.Span})
			case compiler.AttrSpread:
				add(&region{kind: rSpread, span: a.Span, code: a.Code.Span})
			}
		}
	}
	compiler.Walk(fx.f.Template, fx.walker(add, attrs, &scripts, &styles))
}

// walker returns the Walk callback that collect uses below an If, For or
// Switch.
func (fx *fileFmt) walker(add func(*region), attrs func(*compiler.Tag), scripts, styles *int) func(compiler.Node) bool {
	var w func(compiler.Node) bool
	w = func(n compiler.Node) bool {
		switch n := n.(type) {
		case *compiler.Expr:
			add(&region{kind: rExpr, span: n.Span, code: n.Code.Span})
			return false
		case *compiler.If:
			for i, b := range n.Branches {
				k := rElseIf
				switch {
				case i == 0:
					k = rIf
				case b.Else:
					k = rElse
				}
				add(&region{kind: k, span: b.Header, code: b.Cond.Span})
				for _, c := range b.Children {
					compiler.Walk(c, w)
				}
			}
			if n.EndTag.Len() > 0 {
				add(&region{kind: rEndIf, span: n.EndTag})
			}
			return false
		case *compiler.For:
			add(&region{kind: rFor, span: n.Header, code: n.Clause.Span})
			for _, c := range n.Children {
				compiler.Walk(c, w)
			}
			if n.EndTag.Len() > 0 {
				add(&region{kind: rEndFor, span: n.EndTag})
			}
			return false
		case *compiler.Switch:
			add(&region{kind: rSwitch, span: n.Header, code: n.Clause.Span})
			for _, c := range n.Pre {
				compiler.Walk(c, w)
			}
			for _, cs := range n.Cases {
				if cs.Default {
					add(&region{kind: rDefault, span: cs.Header})
				} else {
					add(&region{kind: rCase, span: cs.Header, code: cs.List.Span, clause: n.Clause.Span})
				}
				for _, c := range cs.Children {
					compiler.Walk(c, w)
				}
			}
			if n.EndTag.Len() > 0 {
				add(&region{kind: rEndSwitch, span: n.EndTag})
			}
			return false
		}
		return fx.visit(n, add, attrs, scripts, styles)
	}
	return w
}

// visit handles the tag-shaped nodes for collect.
func (fx *fileFmt) visit(n compiler.Node, add func(*region), attrs func(*compiler.Tag), scripts, styles *int) bool {
	switch n := n.(type) {
	case *compiler.Script:
		attrs(&n.Tag)
		add(&region{kind: rScript, span: n.Content.Span, code: n.Content.Span, node: n, index: *scripts})
		*scripts++
		return false
	case *compiler.Style:
		attrs(&n.Tag)
		add(&region{kind: rStyle, span: n.Content.Span, code: n.Content.Span, node: n, index: *styles})
		*styles++
		return false
	}
	if t := compiler.TagOf(n); t != nil {
		attrs(t)
	}
	return true
}

// edits returns the regions whose text changes, as edits on the source.
func (fx *fileFmt) edits() []Edit {
	var out []Edit
	for _, r := range fx.regions {
		if !r.done {
			continue
		}
		s := r.out
		if fx.crlf {
			s = strings.ReplaceAll(s, "\n", "\r\n")
		}
		if r.kind == rFrontmatter {
			// The closing line's \r (and trailing spaces) were in the span;
			// a file ending at the closing --- gets a line break.
			switch {
			case r.span.End == len(fx.src) && fx.crlf:
				s += "\r\n"
			case r.span.End == len(fx.src):
				s += "\n"
			case fx.crlf:
				s += "\r"
			}
		}
		if s == fx.src[r.span.Start:r.span.End] {
			continue
		}
		out = append(out, Edit{Span: r.span, New: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Span.Start < out[j].Span.Start })
	return out
}

// detectUnit returns the template's indentation unit: a tab if its first
// indented line starts with a tab, otherwise the smallest number of leading
// spaces; two spaces when nothing is indented. Lines that start inside Go
// code, script and style content or a <pre> are skipped, so the unit only
// depends on text formatting never changes.
func (fx *fileFmt) detectUnit() string {
	var skip []compiler.Span
	for _, r := range fx.regions {
		if r.kind != rFrontmatter {
			skip = append(skip, r.span)
		}
	}
	compiler.Walk(fx.f.Template, func(n compiler.Node) bool {
		if e, ok := n.(*compiler.Element); ok && e.Name == "pre" {
			skip = append(skip, e.Span)
			return false
		}
		return true
	})
	sort.Slice(skip, func(i, j int) bool { return skip[i].Start < skip[j].Start })
	t := fx.f.Template.Span
	minSpaces := 0
	for pos := t.Start; pos < t.End; {
		end := strings.IndexByte(fx.src[pos:t.End], '\n')
		next := t.End
		if end >= 0 {
			next = pos + end + 1
			end = pos + end
		} else {
			end = t.End
		}
		lineStart := pos
		pos = next
		inside := false
		for _, sp := range skip {
			if sp.Start >= lineStart {
				break
			}
			if lineStart < sp.End {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		l := fx.src[lineStart:end]
		if strings.TrimSpace(l) == "" {
			continue
		}
		switch l[0] {
		case '\t':
			if minSpaces == 0 {
				return "\t"
			}
		case ' ':
			n := len(l) - len(strings.TrimLeft(l, " "))
			if minSpaces == 0 || n < minSpaces {
				minSpaces = n
			}
		}
	}
	if minSpaces == 0 {
		return "  "
	}
	return strings.Repeat(" ", minSpaces)
}
