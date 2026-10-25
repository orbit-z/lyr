package assets

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// The virtual modules (PLAN.md §5).
//
// Each page with assets is one esbuild entry, orbit-page:<name>.ts when its
// graph has a script (the CSS that the module imports, its styles included,
// becomes the entry's stylesheet) and orbit-page:<name>.css when it only
// has styles (no empty JS entry). The entry imports the page's blocks in
// graph order, each file's styles then its scripts, as
// orbit:style/<ComponentID>-<i>.css and orbit:script/<ComponentID>-<i>.ts.
//
// A block resolves to its .orbit file itself, in esbuild's file namespace,
// with a "?orbit-script=<i>" or "?orbit-style=<i>" suffix that keeps the
// blocks of a file apart. Its contents are the block's text, preceded by
// newlines and spaces up to the block's line and column in the .orbit file.
// So esbuild's own positions (errors, warnings and source maps) are .orbit
// positions with no mapping step: errors read src/x.orbit:L:C, and a source
// map's original positions are lines and columns of the .orbit file, whose
// real text replaces the padded one in sourcesContent (sourcemap.go).
// Resolving in the file namespace also gives each block the nearest
// tsconfig.json of its file, and the .orbit file's directory as resolveDir.

const (
	pageNamespace = "orbit-page"
	scriptPrefix  = "orbit:script/"
	stylePrefix   = "orbit:style/"
	scriptSuffix  = "?orbit-script="
	styleSuffix   = "?orbit-style="
)

// plan is one Input turned into esbuild entries and module contents.
type plan struct {
	// entries are the entry specifiers ("orbit-page:index.ts"), sorted.
	entries []string
	// pageOf maps an entry specifier to its page file.
	pageOf map[string]string
	// pageSrc holds each entry's contents, by its path in pageNamespace
	// ("index.ts").
	pageSrc map[string]string
	// blocks maps a block specifier ("orbit:style/Card-3fa2c1d0-0.css") to
	// its module.
	blocks map[string]*block
	// byKey maps a block's file and suffix to its module, for onLoad.
	byKey map[string]*block
	// orbitSrc holds the text of every .orbit file with a block, for
	// source maps.
	orbitSrc    map[string]string
	diagnostics []Diagnostic
}

// block is one script or style module.
type block struct {
	file     string // project-relative .orbit path
	suffix   string // "?orbit-script=0"
	css      bool
	contents string
}

// key returns the stable identity of the plan's entries: an esbuild
// context can be reused (Rebuild) as long as it doesn't change.
func (p *plan) key() string { return strings.Join(p.entries, "\n") }

func newPlan(in *Input) *plan {
	p := &plan{
		pageOf:   map[string]string{},
		pageSrc:  map[string]string{},
		blocks:   map[string]*block{},
		byKey:    map[string]*block{},
		orbitSrc: map[string]string{},
	}
	names := map[string]bool{}
	pages := slices.Clone(in.Pages)
	slices.SortFunc(pages, func(a, b Page) int { return strings.Compare(a.File, b.File) })
	for _, pg := range pages {
		var imports []string
		hasScript := false
		for _, file := range pg.Modules {
			src := in.Sources[file]
			if src == nil {
				continue
			}
			if len(src.Styles) > 0 || len(src.Scripts) > 0 {
				p.orbitSrc[file] = src.Src
			}
			for _, st := range src.Styles {
				imports = append(imports, p.style(src, st))
			}
			for i, sc := range src.Scripts {
				imports = append(imports, p.script(src, sc, i))
				hasScript = true
			}
		}
		if len(imports) == 0 {
			continue
		}
		name := uniqueName(entryName(pg.File), names)
		var b strings.Builder
		ext := ".css"
		if hasScript {
			ext = ".ts"
			for _, spec := range imports {
				fmt.Fprintf(&b, "import %s;\n", strconv.Quote(spec))
			}
		} else {
			for _, spec := range imports {
				fmt.Fprintf(&b, "@import %s;\n", strconv.Quote(spec))
			}
		}
		spec := pageNamespace + ":" + name + ext
		p.entries = append(p.entries, spec)
		p.pageOf[spec] = pg.File
		p.pageSrc[name+ext] = b.String()
	}
	slices.Sort(p.entries)
	return p
}

// style registers the module of a style block and returns its specifier.
func (p *plan) style(src *Source, st compiler.StyleBlock) string {
	spec := stylePrefix + compiler.ComponentID(src.Path) + "-" + strconv.Itoa(st.Index) + ".css"
	if p.blocks[spec] != nil {
		return spec
	}
	css := st.Content.Text
	if !st.Global {
		var ds []compiler.Diagnostic
		css, ds = compiler.ScopeCSS(css, compiler.ScopeAttr(src.Path))
		for _, d := range ds {
			pos := src.Lines.Position(st.Content.Start + d.Start)
			p.diagnostics = append(p.diagnostics, Diagnostic{
				File: src.Path, Line: pos.Line, Col: pos.Col,
				Warning: d.Severity == compiler.SeverityWarning, Message: d.Message,
			})
		}
	}
	b := &block{file: src.Path, suffix: styleSuffix + strconv.Itoa(st.Index), css: true,
		contents: pad(src.Lines.Position(st.Content.Start)) + css}
	p.blocks[spec] = b
	p.byKey[b.file+b.suffix] = b
	return spec
}

// script registers the module of a script block and returns its
// specifier. A src script is a module that imports the file, with the
// string literal at the src value's position.
func (p *plan) script(src *Source, sc compiler.ScriptBlock, i int) string {
	spec := scriptPrefix + compiler.ComponentID(src.Path) + "-" + strconv.Itoa(sc.Index) + ".ts"
	if p.blocks[spec] != nil {
		return spec
	}
	var contents string
	if sc.Src != "" {
		const stmt = "import"
		lit := strconv.Quote(sc.Src)
		var span compiler.Span
		if i < len(src.SrcSpans) {
			span = src.SrcSpans[i]
		}
		if span == (compiler.Span{}) {
			contents = pad(src.Lines.Position(sc.Content.Start)) + stmt + lit + ";"
		} else {
			pos := src.Lines.Position(span.Start - 1) // the opening quote
			if pos.Col > len(stmt) {
				pos.Col -= len(stmt)
				contents = pad(pos) + stmt + lit + ";"
			} else {
				contents = pad(compiler.Position{Line: pos.Line, Col: 1}) + stmt + lit + ";"
			}
		}
	} else {
		contents = pad(src.Lines.Position(sc.Content.Start)) + sc.Content.Text
	}
	b := &block{file: src.Path, suffix: scriptSuffix + strconv.Itoa(sc.Index), contents: contents}
	p.blocks[spec] = b
	p.byKey[b.file+b.suffix] = b
	return spec
}

// pad returns the whitespace that puts the next byte at pos.
func pad(pos compiler.Position) string {
	return strings.Repeat("\n", max(pos.Line-1, 0)) + strings.Repeat(" ", max(pos.Col-1, 0))
}

// entryName is the bundle name of a page: its path under src/pages without
// .orbit, "/" turned into "-", and only letters, digits, "-" and "_" kept
// ("src/pages/blog/[slug].orbit" → "blog-slug").
func entryName(file string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(file, "src/pages/"), ".orbit")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "page"
	}
	return b.String()
}

// uniqueName returns name, or name-2, name-3… if it is taken, and marks it
// taken. Pages are visited in path order, so names are deterministic.
func uniqueName(name string, taken map[string]bool) string {
	n := name
	for i := 2; taken[strings.ToLower(n)]; i++ {
		n = name + "-" + strconv.Itoa(i)
	}
	taken[strings.ToLower(n)] = true
	return n
}
