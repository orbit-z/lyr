package assets

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/tailwind"
)

// The virtual modules (PLAN.md §5).
//
// Each page with assets is one esbuild entry, orbit-page:<name>.ts when its
// graph has a script (the CSS that the module imports, its styles included,
// becomes the entry's stylesheet) and orbit-page:<name>.css when it only
// has styles (no empty JS entry). The entry imports the page's blocks in
// graph order, each file's styles then its scripts, as
// orbit:style/<ComponentID>-<i>@<page>.css and
// orbit:script/<ComponentID>-<i>@<page>.ts, where <page> is the entry's
// bundle name. The page qualifier makes each block one module per page,
// so that with code splitting (PLAN.md §5e) esbuild inlines a page's
// blocks into its own entry, in graph order, and only imported files
// (.ts/.js files, packages, orbit:client, CSS imported from scripts)
// become shared chunks. The copies share one block: its scoped CSS,
// padded contents and Tailwind output are computed once.
//
// A block resolves to its .orbit file itself, in esbuild's file namespace,
// with a "?orbit-script=<i>&page=<page>" or "?orbit-style=<i>&page=<page>"
// suffix that keeps the blocks of a file, and their per-page copies,
// apart (lazy entries use the suffix without "&page="). Its contents are
// the block's text, preceded by newlines and spaces up to the block's line
// and column in the .orbit file.
// So esbuild's own positions (errors, warnings and source maps) are .orbit
// positions with no mapping step: errors read src/x.orbit:L:C, and a source
// map's original positions are lines and columns of the .orbit file, whose
// real text replaces the padded one in sourcesContent (sourcemap.go).
// Resolving in the file namespace also gives each block the nearest
// tsconfig.json of its file, and the .orbit file's directory as resolveDir.

// A lazy entry (client:*, PLAN.md §5) is orbit:lazy/<ComponentID>.ts,
// served from lazyNamespace: it imports the component's script modules.

const (
	lazyPrefix    = "orbit:lazy/"
	lazyNamespace = "orbit-lazy"
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
	// lazyOf maps a lazy entry, as the metafile names it
	// ("orbit-lazy:Counter-1c2d3e4f.ts"), to its component file, and
	// lazySrc holds its contents by path in lazyNamespace.
	lazyOf  map[string]string
	lazySrc map[string]string
	// outName is each entry's bundle name ("blog-slug", "Counter").
	outName map[string]string
	// pages are the input pages, sorted by file.
	pages []Page
	// blocks maps a block specifier ("orbit:style/Card-3fa2c1d0-0@index.css")
	// to its module and the suffix it resolves with.
	blocks map[string]blockCopy
	// byKey maps a block's file and suffix to its module, for onLoad.
	byKey map[string]*block
	// base maps a block's file and unqualified suffix to its module: the
	// one block that its per-page copies share.
	base map[string]*block
	// orbitSrc holds the text of every .orbit file with a block, for
	// source maps.
	orbitSrc map[string]string
	// highlight is the contents of highlightSpec.
	highlight   string
	diagnostics []Diagnostic
}

// highlightSpec is the virtual stylesheet of Markdown code highlighting
// (Input.HighlightCSS), served from highlightNamespace.
const (
	highlightSpec      = stylePrefix + "markdown-highlight.css"
	highlightNamespace = "orbit-highlight"
)

// blockCopy is one copy of a block: the block and the suffix of the copy
// ("?orbit-script=0&page=index").
type blockCopy struct {
	*block
	suffix string
}

// block is one script or style block, shared by its per-page copies.
type block struct {
	file     string // project-relative .orbit path
	css      bool
	contents string
	// For a style block that is a Tailwind input (PLAN.md §5d): its
	// scoped text, unpadded, and its position in the .orbit file.
	// orig is the text before scoping and scope the inserted attribute,
	// to place Tailwind's errors in the user's text.
	tw    tailwind.Info
	text  string
	orig  string
	scope string
	start compiler.Position
	// twOnce guards twRes, the load result of a Tailwind block, computed
	// once per build for all its per-page copies.
	twOnce sync.Once
	twRes  api.OnLoadResult
}

// key returns the stable identity of the plan's entries: an esbuild
// context can be reused (Rebuild) as long as it doesn't change.
func (p *plan) key() string { return strings.Join(p.entries, "\n") }

func newPlan(in *Input) *plan {
	p := &plan{
		pageOf:    map[string]string{},
		pageSrc:   map[string]string{},
		lazyOf:    map[string]string{},
		lazySrc:   map[string]string{},
		outName:   map[string]string{},
		blocks:    map[string]blockCopy{},
		byKey:     map[string]*block{},
		base:      map[string]*block{},
		orbitSrc:  map[string]string{},
		highlight: in.HighlightCSS,
	}
	names := map[string]bool{}
	pages := slices.Clone(in.Pages)
	slices.SortFunc(pages, func(a, b Page) int { return strings.Compare(a.File, b.File) })
	p.pages = pages
	lazyFiles := map[string]bool{} // files lazy on some page
	for _, pg := range pages {
		var imports []string
		if pg.Highlight && p.highlight != "" {
			imports = append(imports, highlightSpec) // first, so user styles win
		}
		// The bundle name, also the qualifier of the page's blocks; taken
		// only if the page gets an entry.
		name := nextName(entryName(pg.File), names)
		hasScript := false
		lazy := map[string]bool{}
		for _, l := range pg.Lazy {
			for _, file := range l.Modules {
				lazy[file] = true
				lazyFiles[file] = true
			}
		}
		for _, file := range pg.Modules {
			src := in.Sources[file]
			if src == nil {
				continue
			}
			if len(src.Styles) > 0 || len(src.Scripts) > 0 {
				p.orbitSrc[file] = src.Src
			}
			for _, st := range src.Styles {
				imports = append(imports, p.style(src, st, name))
			}
			if lazy[file] {
				continue
			}
			for i, sc := range src.Scripts {
				imports = append(imports, p.script(src, sc, i, name))
				hasScript = true
			}
		}
		if len(imports) == 0 {
			continue
		}
		names[strings.ToLower(name)] = true
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
		p.outName[spec] = name
	}
	// One lazy entry per component that is lazy on some page and has
	// scripts (PLAN.md §5), named after the component. Its blocks keep the
	// unqualified specifiers: the entry is per component already.
	files := make([]string, 0, len(lazyFiles))
	for file := range lazyFiles {
		files = append(files, file)
	}
	slices.Sort(files)
	for _, file := range files {
		src := in.Sources[file]
		if src == nil || len(src.Scripts) == 0 {
			continue
		}
		p.orbitSrc[file] = src.Src
		var b strings.Builder
		for i, sc := range src.Scripts {
			fmt.Fprintf(&b, "import %s;\n", strconv.Quote(p.script(src, sc, i, "")))
		}
		id := compiler.ComponentID(file)
		spec := lazyPrefix + id + ".ts"
		p.entries = append(p.entries, spec)
		p.lazyOf[lazyNamespace+":"+id+".ts"] = file
		p.lazySrc[id+".ts"] = b.String()
		p.outName[spec] = uniqueName(entryBase(file), names)
	}
	slices.Sort(p.entries)
	return p
}

// entryBase is the bundle name of a lazy entry: the component's base name,
// with only letters, digits, "-" and "_" kept.
func entryBase(file string) string {
	s := compiler.TrimSourceExt(file)
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "component"
	}
	return b.String()
}

// copyOf returns the specifier and the suffix of the copy of block index
// of the component id for page (its bundle name; "" for a lazy entry,
// unqualified): ("orbit:script/Card-3fa2c1d0-0@index.ts",
// "?orbit-script=0&page=index").
func copyOf(prefix, id string, index int, ext, suffix, page string) (spec, sfx string) {
	spec = prefix + id + "-" + strconv.Itoa(index)
	sfx = suffix + strconv.Itoa(index)
	if page != "" {
		spec += "@" + page
		sfx += "&page=" + page
	}
	return spec + ext, sfx
}

// register records a copy of b and returns its specifier.
func (p *plan) register(spec, suffix string, b *block) string {
	p.blocks[spec] = blockCopy{b, suffix}
	p.byKey[b.file+suffix] = b
	return spec
}

// style registers the copy of a style block for page (its bundle name) and
// returns its specifier. The block is scoped once, however many pages
// include it.
func (p *plan) style(src *Source, st compiler.StyleBlock, page string) string {
	spec, suffix := copyOf(stylePrefix, compiler.ComponentID(src.Path), st.Index, ".css", styleSuffix, page)
	if p.blocks[spec].block != nil {
		return spec
	}
	key := src.Path + styleSuffix + strconv.Itoa(st.Index)
	if b := p.base[key]; b != nil {
		return p.register(spec, suffix, b)
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
	start := src.Lines.Position(st.Content.Start)
	b := &block{file: src.Path, css: true, contents: pad(start) + css}
	// Scoped first, then Tailwind (Astro's order): the Tailwind run is in
	// the load step, in parallel.
	if b.tw = tailwind.Detect(css); b.tw.Input {
		b.text, b.orig, b.start = css, st.Content.Text, start
		if !st.Global {
			b.scope = "[" + compiler.ScopeAttr(src.Path) + "]"
		}
	}
	p.base[key] = b
	return p.register(spec, suffix, b)
}

// script registers the copy of a script block for page (its bundle name;
// "" for a lazy entry) and returns its specifier. A src script is a module
// that imports the file, with the string literal at the src value's
// position.
func (p *plan) script(src *Source, sc compiler.ScriptBlock, i int, page string) string {
	spec, suffix := copyOf(scriptPrefix, compiler.ComponentID(src.Path), sc.Index, ".ts", scriptSuffix, page)
	if p.blocks[spec].block != nil {
		return spec
	}
	key := src.Path + scriptSuffix + strconv.Itoa(sc.Index)
	if b := p.base[key]; b != nil {
		return p.register(spec, suffix, b)
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
	b := &block{file: src.Path, contents: contents}
	p.base[key] = b
	return p.register(spec, suffix, b)
}

// pad returns the whitespace that puts the next byte at pos.
func pad(pos compiler.Position) string {
	return strings.Repeat("\n", max(pos.Line-1, 0)) + strings.Repeat(" ", max(pos.Col-1, 0))
}

// entryName is the bundle name of a page: its path under src/pages without
// .orbit, "/" turned into "-", and only letters, digits, "-" and "_" kept
// ("src/pages/blog/[slug].orbit" → "blog-slug").
func entryName(file string) string {
	s := strings.TrimPrefix(file, "src/pages/")
	s = strings.TrimSuffix(s, path.Base(s)) + compiler.TrimSourceExt(s)
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
	n := nextName(name, taken)
	taken[strings.ToLower(n)] = true
	return n
}

// nextName returns what uniqueName would, without marking it taken.
func nextName(name string, taken map[string]bool) string {
	n := name
	for i := 2; taken[strings.ToLower(n)]; i++ {
		n = name + "-" + strconv.Itoa(i)
	}
	return n
}
