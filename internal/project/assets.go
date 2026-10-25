package project

import (
	"github.com/bertrandkhe/orbit/internal/assets"
	"github.com/bertrandkhe/orbit/internal/compiler"
)

// AssetInput returns the bundler input of the project (PLAN.md §5): every
// file's processed scripts and styles, and each page's asset graph. It
// needs Front.
func (p *Project) AssetInput() *assets.Input {
	in := &assets.Input{Sources: map[string]*assets.Source{}}
	for _, f := range p.Files {
		if s := assets.SourceOf(f.Path, f.AST); s != nil {
			in.Sources[f.Path] = s
		}
	}
	for _, f := range p.Files {
		if f.Info.Role == compiler.RolePage {
			in.Pages = append(in.Pages, assets.Page{File: f.Path, Modules: p.AssetGraph(f)})
		}
	}
	return in
}

// AssetGraph returns the files whose assets the page includes, in
// module-evaluation order: depth first through the .orbit imports, a
// file's imports first, in import order, then the file itself; each file
// once (so recursive and mutually recursive components are fine), the page
// last. Like Astro, it is static: every imported component counts, whether
// or not it renders.
func (p *Project) AssetGraph(page *File) []string {
	byPath := make(map[string]*File, len(p.Files))
	for _, f := range p.Files {
		byPath[f.Path] = f
	}
	var order []string
	seen := map[string]bool{}
	var visit func(f *File)
	visit = func(f *File) {
		if seen[f.Path] {
			return
		}
		seen[f.Path] = true
		if f.Analysis != nil {
			for _, im := range f.Analysis.OrbitImports {
				if g := byPath[im.Resolved]; g != nil {
					visit(g)
				}
			}
		}
		order = append(order, f.Path)
	}
	visit(page)
	return order
}

// fromAssets converts a bundler diagnostic.
func (p *Project) fromAssets(d assets.Diagnostic) Diagnostic {
	out := Diagnostic{File: d.File, Severity: compiler.SeverityError, Message: d.Message, off: -1}
	if d.Warning {
		out.Severity = compiler.SeverityWarning
	}
	if out.File == "" {
		out.File = "esbuild"
	}
	if d.Line > 0 {
		out.Pos = compiler.Position{Line: d.Line, Col: d.Col}
		out.off = d.Line<<20 | d.Col
		for _, f := range p.Files {
			if f.Path == d.File && f.AST != nil {
				out.off = f.AST.Lines.Offset(out.Pos)
			}
		}
	}
	return out
}

// Bundle runs the bundler on the project and keeps its result for the
// generated manifest (p.Assets). It returns the diagnostics, converted.
func (p *Project) Bundle(b assets.Bundler) ([]Diagnostic, error) {
	res, err := b.Bundle(p.AssetInput())
	if err != nil {
		return nil, err
	}
	p.Assets = res
	var ds []Diagnostic
	for _, d := range res.Diagnostics {
		ds = append(ds, p.fromAssets(d))
	}
	return ds, nil
}
