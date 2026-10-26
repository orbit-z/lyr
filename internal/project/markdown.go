package project

import (
	"fmt"
	"slices"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/markdown"
)

// Markdown files (PLAN.md §2.4). Load reads every Markdown file under src/
// into Project.markdown; Front compiles the pages (Markdown files under
// src/pages, not _-prefixed) and those imported by an .orbit file, as Files
// whose MD is set, with a stand-in AST (an empty template; the file's line index)
// and Analysis (the layout as the only import).

// IsMD reports whether f is a Markdown file.
func (f *File) IsMD() bool { return f.MD != nil || compiler.IsMarkdown(f.Path) }

// SetMarkdown replaces the source of the Markdown file at the
// project-relative path p (adding it when new), or removes it when removed
// is set. It returns false for a path that isn't a Markdown file under
// src/. Call Front afterwards.
func (p *Project) SetMarkdown(path, src string, removed bool) bool {
	if !compiler.IsMarkdown(path) || !strings.HasPrefix(path, SrcDir+"/") {
		return false
	}
	if p.markdown == nil {
		p.markdown = map[string]string{}
	}
	_, had := p.markdown[path]
	switch {
	case removed:
		delete(p.markdown, path)
		p.mdPaths = slices.DeleteFunc(p.mdPaths, func(s string) bool { return s == path })
	case !had:
		p.mdPaths = append(p.mdPaths, path)
		slices.Sort(p.mdPaths)
		fallthrough
	default:
		p.markdown[path] = src
	}
	return true
}

// mdActive returns the paths of the Markdown files to compile: the pages,
// and those that an .orbit file imports.
func (p *Project) mdActive() []string {
	var out []string
	seen := map[string]bool{}
	for _, path := range p.mdPaths {
		if role, _ := RouteFor(path); role == compiler.RolePage {
			out = append(out, path)
			seen[path] = true
		}
	}
	for _, f := range p.Files {
		if f.IsMD() || f.Analysis == nil {
			continue
		}
		for _, im := range f.Analysis.OrbitImports {
			if _, ok := p.markdown[im.Resolved]; ok && !seen[im.Resolved] {
				seen[im.Resolved] = true
				out = append(out, im.Resolved)
			}
		}
	}
	slices.Sort(out)
	return out
}

// resolver tells the Markdown parser about the project's Markdown files.
func (p *Project) resolver() markdown.Resolver {
	return func(target string) (string, bool) {
		if _, ok := p.markdown[target]; !ok {
			return "", false
		}
		role, route := RouteFor(target)
		if role != compiler.RolePage || route == NotFoundPattern || strings.Contains(route, "[") {
			return "", true
		}
		return route, true
	}
}

// frontMarkdown parses the active Markdown files, in parallel, and returns
// their Files (not in p.Files yet).
func (p *Project) frontMarkdown() []*File {
	var files []*File
	for _, path := range p.mdActive() {
		f, err := NewFile(path, p.markdown[path])
		if err != nil {
			continue // Discover only returns clean paths under src/
		}
		files = append(files, f)
	}
	p.parseMarkdown(files)
	return files
}

// parseMarkdown parses the Markdown files, in parallel, and sets their
// stand-in AST and Analysis.
func (p *Project) parseMarkdown(files []*File) {
	resolve := p.resolver()
	parallel(files, func(f *File) {
		f.MD = markdown.Parse(f.Path, f.Src, p.Config.Markdown, resolve)
		f.AST = &compiler.File{Src: f.Src, Lines: f.MD.Lines, Template: &compiler.Template{}, Diagnostics: f.MD.Diagnostics}
		f.Analysis = &compiler.Analysis{Info: f.Info, Props: compiler.PropsInfo{Listable: true}}
		if f.MD.Layout != "" {
			f.Analysis.OrbitImports = []compiler.OrbitImport{{Alias: markdown.LayoutAlias, Path: f.MD.LayoutRaw,
				Resolved: f.MD.Layout, Place: compiler.PlaceImpl,
				Span: compiler.Span{Start: f.MD.LayoutPos, End: f.MD.LayoutPos + len(f.MD.LayoutRaw)}}}
		}
		f.callList, f.callsDone = nil, false
	})
}

// mdGenInput is the markdown codegen input of f.
func (p *Project) mdGenInput(f *File) markdown.GenInput {
	return markdown.GenInput{Info: f.Info, Pkg: f.Pkg, Options: p.Options, Index: p.Index}
}

// pass1 generates f's pass-1 output and sets its Input.
func (p *Project) pass1(f *File) {
	f.Input = &compiler.Input{File: f.AST, Analysis: f.Analysis, Index: p.Index, Options: p.Options}
	if f.MD != nil {
		f.Pass1 = markdown.Generate(f.MD, p.mdGenInput(f))
	} else {
		f.Pass1 = compiler.Generate(f.Input)
	}
	f.Output, f.Typed, f.Writes = f.Pass1, false, nil
}

// checkLinks checks the fragments of the links between Markdown pages
// (PLAN.md §2.4): a fragment that no heading of the target has is a
// warning, since the id may come from the layout or raw HTML.
func checkLinks(files []*File) []Diagnostic {
	byPath := map[string]*File{}
	for _, f := range files {
		if f.MD != nil {
			byPath[f.Path] = f
		}
	}
	var ds []Diagnostic
	for _, f := range files {
		if f.MD == nil {
			continue
		}
		for _, l := range f.MD.Links {
			t := byPath[l.Target]
			if l.Fragment == "" || t == nil {
				continue
			}
			if !slices.ContainsFunc(t.MD.Headings, func(h markdown.Heading) bool { return h.Slug == l.Fragment }) {
				ds = append(ds, Diagnostic{File: f.Path, Pos: f.MD.Lines.Position(l.Pos), Severity: compiler.SeverityWarning,
					Message: fmt.Sprintf("link %s: %s has no heading #%s", l.Dest, l.Target, l.Fragment), off: l.Pos})
			}
		}
	}
	return ds
}

// mdPageError is the error for a Markdown page whose path has route
// params, or "".
func mdPageError(f *File) string {
	if f.MD == nil || f.Info.Role != compiler.RolePage || !strings.Contains(f.Info.Route, "[") {
		return ""
	}
	base := compiler.TrimSourceExt(f.Path)
	if !strings.HasPrefix(base, "[") {
		base = "[slug]"
	}
	return fmt.Sprintf("a Markdown page can't have route params; rename it, or write a %s.orbit page", base)
}

// highlighted reports whether the page's static graph has a Markdown file
// with a highlighted code block: the page's stylesheet then starts with
// the highlight CSS.
func (p *Project) highlighted(page *File) bool {
	byPath := p.byPath()
	for _, path := range p.AssetGraph(page) {
		if f := byPath[path]; f != nil && f.MD != nil && f.MD.Highlighted {
			return true
		}
	}
	return false
}

// MarkdownPaths returns the paths of every Markdown file under src/,
// sorted, whether Front compiles it or not (the language server completes
// them in import paths).
func (p *Project) MarkdownPaths() []string { return slices.Clone(p.mdPaths) }

// UpdateMarkdown is SetMarkdown followed by the part of Front that the
// edit affects, as Reload does it: the file is re-parsed (if Front
// compiles it), the files that depend on it are regenerated, and the link
// checks and diagnostics are recomputed. It returns the Files whose pass-1
// output changed (new Files included), and false when path isn't a
// Markdown file under src/ or nothing changed. The language server runs it
// on every edit of an open Markdown file (PLAN.md §9).
func (p *Project) UpdateMarkdown(path, src string, removed bool) ([]*File, bool) {
	old, had := p.markdown[path]
	if removed && !had || !removed && had && old == src {
		return nil, false
	}
	if p.Index == nil || !p.SetMarkdown(path, src, removed) {
		return nil, false
	}
	before := make(map[*File]*compiler.Output, len(p.Files))
	var orbit []*File
	for _, f := range p.Files {
		before[f] = f.Pass1
		if !f.IsMD() {
			orbit = append(orbit, f)
		}
	}
	p.frontChanged(orbit, map[string]bool{path: true}, removed || !had)
	var out []*File
	for _, f := range p.Files {
		if prev, ok := before[f]; !ok || prev != f.Pass1 {
			out = append(out, f)
		}
	}
	return out, true
}
