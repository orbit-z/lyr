package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
)

// The build flavor (PLAN.md §3.5): lyr build calls components directly
// where the import graph allows it, through impl files overlaid on
// lyr_gen/ with go build -overlay. lyr_gen/ itself keeps the hook
// flavor.

// DirectCalls returns the direct calls of every file's build flavor, by
// path, for the files that have one (PLAN.md §3.5, Cycles). The graph has
// an edge X → Y for each component Y that X's template calls with a render
// call site (not server:defer, not <Self>, not a page, not a Markdown
// file). An edge is direct unless X and Y are in the same strongly
// connected component; <Self> is a direct self-call outside pages. A direct
// callee gets a build flavor too, for its exported render function (an
// empty DirectCalls when it calls nothing directly). Markdown files keep
// the hook both ways: their render ignores props and slots, so a direct
// call would keep nothing on the stack, and their call to the layout has
// a static slot.
func (p *Project) DirectCalls() map[string]*compiler.DirectCalls {
	byPath := p.byPath()
	edges := map[string][]declEdge{}
	self := map[string]bool{}
	for _, f := range p.Files {
		if f.MD != nil || f.Analysis == nil {
			continue
		}
		for _, c := range f.calls() {
			g := byPath[c.Target]
			switch {
			case c.Defer || g == nil:
			case g == f:
				self[f.Path] = f.Info.Role != compiler.RolePage
			case g.Info.Role != compiler.RolePage && g.MD == nil:
				edges[f.Path] = append(edges[f.Path], declEdge{to: g.Path})
			}
		}
	}
	scc := map[string]int{}
	for i, c := range stronglyConnected(p.Files, edges) {
		for _, path := range c {
			scc[path] = i + 1
		}
	}
	out := map[string]*compiler.DirectCalls{}
	for _, f := range p.Files {
		d := &compiler.DirectCalls{Callees: map[string]bool{}, Self: self[f.Path]}
		for _, e := range edges[f.Path] {
			if scc[f.Path] == 0 || scc[f.Path] != scc[e.to] {
				d.Callees[e.to] = true
			}
		}
		if len(d.Callees) > 0 || d.Self {
			out[f.Path] = d
		}
	}
	for _, d := range out {
		for to := range d.Callees {
			if out[to] == nil {
				out[to] = &compiler.DirectCalls{}
			}
		}
	}
	return out
}

// BuildFiles returns the build flavor's files (PLAN.md §3.5): the impl file
// of every file in DirectCalls, generated again with the typed
// writes Typecheck chose and the file's DirectCalls, at its BuildDir path
// (compiler.BuildPath), sorted, then the overlay file (OverlayFile) that
// maps each lyr_gen/ path to it. Call it after Typecheck, on a project
// without errors.
func (p *Project) BuildFiles() ([]GenFile, error) {
	direct := p.DirectCalls()
	var files []*File
	for _, f := range p.Files {
		if direct[f.Path] != nil {
			files = append(files, f)
		}
	}
	outs := make([]*compiler.Output, len(files))
	parallelN(len(files), func(i int) {
		in := *files[i].Input
		in.Typed, in.Direct = files[i].Writes, direct[files[i].Path]
		outs[i] = compiler.Generate(&in)
	})
	replace := map[string]string{}
	gen := make([]GenFile, 0, len(files)+1)
	for i, out := range outs {
		for _, d := range out.Diagnostics {
			if d.Severity == compiler.SeverityError {
				return nil, errors.New(files[i].Path + ": build flavor: " + d.Message)
			}
		}
		path := compiler.BuildPath(out.Impl.Path)
		replace[out.Impl.Path] = path
		gen = append(gen, GenFile{Path: path, Content: out.Impl.Content})
	}
	slices.SortFunc(gen, func(a, b GenFile) int { return strings.Compare(a.Path, b.Path) })
	ov, err := json.MarshalIndent(struct{ Replace map[string]string }{replace}, "", "\t")
	if err != nil {
		return nil, err
	}
	gen = append(gen, GenFile{Path: compiler.OverlayFile, Content: append(ov, '\n')})
	return gen, nil
}

// WriteBuildFiles removes BuildDir under root and writes files (from
// BuildFiles) into it.
func WriteBuildFiles(root string, files []GenFile) error {
	dir := filepath.Join(root, filepath.FromSlash(compiler.BuildDir))
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, f := range files {
		name := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
