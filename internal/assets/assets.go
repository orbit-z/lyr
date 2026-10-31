// Package assets bundles the processed <script> and <style> blocks of a
// project's .lyr files into one stylesheet and one ES module per page
// (PLAN.md §5), with the modules that several entries import, and the
// targets of dynamic import(), split into shared chunks (§5e). The default bundler is esbuild, through its Go API, in the
// lyr process; the rest of Lyr only sees the Bundler interface.
//
// The package doesn't know about projects: internal/project builds the
// Input (every file's blocks and every page's import graph, in
// module-evaluation order) and turns the Result into the generated manifest
// (lyr build) or the dev manifest (lyr dev, WriteDev).
package assets

import (
	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/tailwind"
)

// URLPrefix is the URL path every bundle is served under (PLAN.md §4).
const URLPrefix = "/_lyr/"

// Bundler bundles the assets of a project's pages.
//
// Bundle may be called again with a new Input of the same project: an
// implementation reuses what it can (esbuild's incremental context), which
// is how lyr dev rebuilds after a <style> or <script> edit. A Bundler is
// not safe for concurrent use. Close releases it.
type Bundler interface {
	Bundle(in *Input) (*Result, error)
	Close()
}

// Options configure a Bundler.
type Options struct {
	// Root is the absolute project root: relative paths in Input and in
	// diagnostics are relative to it, and bare imports resolve from its
	// node_modules.
	Root string
	// Dev builds for lyr dev: not minified, with linked source maps.
	Dev bool
	// Sourcemap writes external source maps in a production build (lyr.json
	// "sourcemap": true). They aren't linked from the bundles (no
	// sourceMappingURL comment). Dev builds always have linked maps.
	Sourcemap bool
	// Tailwind runs the Tailwind inputs (PLAN.md §5d). nil: the bundler
	// makes its own when it meets one. lyr dev passes the one whose
	// runs it starts early.
	Tailwind *tailwind.Processor
}

// Input is everything a bundle needs.
type Input struct {
	// Sources are the .lyr files that have processed scripts or styles,
	// by project-relative path. Files without any may be left out.
	Sources map[string]*Source
	// Pages are the pages to bundle, each with its asset graph. A page
	// whose graph has no scripts and no styles gets no entry and no assets.
	Pages []Page
	// HighlightCSS is the code-highlighting stylesheet of Markdown files
	// (PLAN.md §2.4): the virtual module lyr:style/markdown-highlight.css,
	// first in the stylesheet of every page with Highlight set. "" for
	// none.
	HighlightCSS string
}

// Source is one .lyr file's processed blocks.
type Source struct {
	// Path is project-relative, "/"-separated ("src/components/Card.lyr").
	Path string
	// Src is the whole file, for source maps (sourcesContent).
	Src string
	// Lines indexes Src, to place each block at its .lyr line and column.
	Lines   *compiler.LineIndex
	Scripts []compiler.ScriptBlock
	// SrcSpans holds, for each script with a Src, the span of the src
	// value (inside the quotes), so that an unresolved src is reported
	// there; the zero Span when unknown. It is indexed like Scripts.
	SrcSpans []compiler.Span
	Styles   []compiler.StyleBlock
}

// Page is one page and its static asset graph.
type Page struct {
	// File is the page's project-relative path, the key of Result.Pages.
	File string
	// Modules are the .lyr files whose assets the page includes, in
	// module-evaluation order: a component's imports first, in import
	// order, then the component itself; each file once; the page last
	// (PLAN.md §5). Files that aren't in Input.Sources are skipped.
	//
	// The scripts of the files listed in Lazy aren't in the page bundle;
	// their styles are.
	Modules []string
	// Lazy are the page's lazy call-site lists (client:idle, client:visible
	// and client:media, PLAN.md §5), one per tagged component, sorted by
	// ID.
	Lazy []Lazy
	// Highlight is set when the page's graph has a Markdown file with a
	// highlighted code block: its stylesheet starts with
	// Input.HighlightCSS.
	Highlight bool
}

// Lazy is the list of entries a lazy marker loads on one page.
type Lazy struct {
	// ID is the component ID of the tagged component, the marker's
	// component attribute.
	ID string
	// Modules are the files whose lazy entries the marker imports: the
	// tagged component and every component in its static subtree that is
	// lazy on this page (nested lazy directives excluded), in
	// module-evaluation order. Files without scripts are skipped. A file
	// listed here is lazy on the page: never in its bundle.
	Modules []string
}

// Result is the outcome of a Bundle.
type Result struct {
	// Pages maps a page file to its assets, in <head> order: the
	// stylesheets, then the module, then the lazy entries, then the chunks
	// the module imports (modulepreload). Pages without assets aren't
	// listed.
	Pages map[string][]Asset
	// Files are the output files (entries, shared and dynamic import()
	// chunks, source maps, files referenced from CSS), sorted by Name.
	// Nil when there are errors.
	Files []File
	// Diagnostics are esbuild's and the CSS scoper's errors and warnings,
	// at positions in the user's files.
	Diagnostics []Diagnostic
}

// HasErrors reports whether the bundle failed.
func (r *Result) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if !d.Warning {
			return true
		}
	}
	return false
}

// File returns the output file served at url ("/_lyr/x.css"), or nil.
func (r *Result) File(url string) *File {
	for i := range r.Files {
		if URLPrefix+r.Files[i].Name == url {
			return &r.Files[i]
		}
	}
	return nil
}

// Asset is a page asset, as in lyr.Asset: Kind "css", "js", "lazy" or
// "modulepreload", URL under /_lyr/. A "lazy" asset is one entry of a
// lazy marker's list: ID is the tagged component's ID, URL the entry of one
// component in that list. A marker that loads several entries has several
// "lazy" assets with the same ID, and an entry can be listed under several
// IDs. The CSS that a lazy entry's scripts import is a "css" asset of the
// page. A "modulepreload" asset is a chunk in the static import closure of
// the page's "js" module (PLAN.md §5e), in breadth-first import order;
// lazy entries' and dynamic import() chunks aren't listed.
type Asset struct {
	Kind string
	URL  string
	ID   string
}

// File is one output file.
type File struct {
	// Name is the path under /_lyr/ ("index-5RXDJ3BM.js").
	Name     string
	Contents []byte
}

// Diagnostic is an error or warning in a user file.
type Diagnostic struct {
	// File is project-relative ("src/components/Card.lyr",
	// "src/scripts/util.ts"); "" when esbuild gave no location.
	File string
	// Line and Col are 1-based (Col in bytes, as in Lyr's other
	// errors); 0 when there is no position.
	Line, Col int
	Warning   bool
	Message   string
}

// SourceOf returns the Source of a parsed .lyr file at the
// project-relative path, or nil when it has no processed script or style.
func SourceOf(path string, f *compiler.File) *Source {
	scripts, styles := compiler.Scripts(f), compiler.Styles(f)
	if len(scripts) == 0 && len(styles) == 0 {
		return nil
	}
	s := &Source{Path: path, Src: f.Src, Lines: f.Lines, Scripts: scripts, Styles: styles}
	// The src spans, in the walk order of compiler.Scripts.
	if f.Template != nil {
		compiler.Walk(f.Template, func(n compiler.Node) bool {
			if sc, ok := n.(*compiler.Script); ok && sc.Processed {
				var span compiler.Span
				if a := sc.Attr("src"); a != nil {
					span = a.ValueSpan
				}
				s.SrcSpans = append(s.SrcSpans, span)
			}
			return true
		})
	}
	if len(s.SrcSpans) != len(s.Scripts) {
		s.SrcSpans = nil
	}
	return s
}
