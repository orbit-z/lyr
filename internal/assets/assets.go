// Package assets bundles the processed <script> and <style> blocks of a
// project's .orbit files into one stylesheet and one ES module per page
// (PLAN.md §5). The default bundler is esbuild, through its Go API, in the
// orbit process; the rest of Orbit only sees the Bundler interface.
//
// The package doesn't know about projects: internal/project builds the
// Input (every file's blocks and every page's import graph, in
// module-evaluation order) and turns the Result into the generated manifest
// (orbit build) or the dev manifest (orbit dev, WriteDev).
package assets

import (
	"github.com/bertrandkhe/orbit/internal/compiler"
)

// URLPrefix is the URL path every bundle is served under (PLAN.md §4).
const URLPrefix = "/_orbit/"

// Bundler bundles the assets of a project's pages.
//
// Bundle may be called again with a new Input of the same project: an
// implementation reuses what it can (esbuild's incremental context), which
// is how orbit dev rebuilds after a <style> or <script> edit. A Bundler is
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
	// Dev builds for orbit dev: not minified, with linked source maps.
	Dev bool
	// Sourcemap writes external source maps in a production build (orbit.json
	// "sourcemap": true). They aren't linked from the bundles (no
	// sourceMappingURL comment). Dev builds always have linked maps.
	Sourcemap bool
}

// Input is everything a bundle needs.
type Input struct {
	// Sources are the .orbit files that have processed scripts or styles,
	// by project-relative path. Files without any may be left out.
	Sources map[string]*Source
	// Pages are the pages to bundle, each with its asset graph. A page
	// whose graph has no scripts and no styles gets no entry and no assets.
	Pages []Page
}

// Source is one .orbit file's processed blocks.
type Source struct {
	// Path is project-relative, "/"-separated ("src/components/Card.orbit").
	Path string
	// Src is the whole file, for source maps (sourcesContent).
	Src string
	// Lines indexes Src, to place each block at its .orbit line and column.
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
	// Modules are the .orbit files whose assets the page includes, in
	// module-evaluation order: a component's imports first, in import
	// order, then the component itself; each file once; the page last
	// (PLAN.md §5). Files that aren't in Input.Sources are skipped.
	//
	// Milestone 8 adds the lazy (client:*) entries next to this list.
	Modules []string
}

// Result is the outcome of a Bundle.
type Result struct {
	// Pages maps a page file to its assets, in <head> order: the
	// stylesheet, then the module. Pages without assets aren't listed.
	Pages map[string][]Asset
	// Files are the output files (bundles, source maps, files referenced
	// from CSS), sorted by Name. Nil when there are errors.
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

// File returns the output file served at url ("/_orbit/x.css"), or nil.
func (r *Result) File(url string) *File {
	for i := range r.Files {
		if URLPrefix+r.Files[i].Name == url {
			return &r.Files[i]
		}
	}
	return nil
}

// Asset is a page asset, as in orbit.Asset: Kind "css" or "js" (and "lazy"
// from milestone 8), URL under /_orbit/.
type Asset struct {
	Kind string
	URL  string
	ID   string
}

// File is one output file.
type File struct {
	// Name is the path under /_orbit/ ("index-5RXDJ3BM.js").
	Name     string
	Contents []byte
}

// Diagnostic is an error or warning in a user file.
type Diagnostic struct {
	// File is project-relative ("src/components/Card.orbit",
	// "src/scripts/util.ts"); "" when esbuild gave no location.
	File string
	// Line and Col are 1-based (Col in bytes, as in Orbit's other
	// errors); 0 when there is no position.
	Line, Col int
	Warning   bool
	Message   string
}

// SourceOf returns the Source of a parsed .orbit file at the
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
