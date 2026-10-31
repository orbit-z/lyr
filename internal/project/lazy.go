package project

import (
	"slices"
	"strings"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/compiler"
)

// The static graphs of milestone 8 (PLAN.md §3.4, §5): the island table,
// Route.Islands, and each page's lazy entries with the eager-wins rule.

// calls returns the component call sites of f, computed once.
func (f *File) calls() []compiler.ComponentCall {
	if f.callsDone {
		return f.callList
	}
	if f.MD != nil {
		// A Markdown file renders its layout, eagerly.
		f.callList, f.callsDone = nil, true
		if f.MD.Layout != "" {
			f.callList = []compiler.ComponentCall{{Target: f.MD.Layout, Lazy: compiler.NotDirective, In: -1}}
		}
		return f.callList
	}
	f.callList, f.callsDone = compiler.ComponentCalls(f.AST, f.Analysis), true
	return f.callList
}

// Islands returns the files used with server:defer anywhere in the
// project, sorted by path: the island table of lyr_gen/app.
func (p *Project) Islands() []*File {
	byPath := p.byPath()
	seen := map[string]bool{}
	var out []*File
	for _, f := range p.Files {
		for _, c := range f.calls() {
			if g := byPath[c.Target]; c.Defer && g != nil && !seen[g.Path] {
				seen[g.Path] = true
				out = append(out, g)
			}
		}
	}
	slices.SortFunc(out, func(a, b *File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// HasIslands reports whether the page's static graph (every file it
// imports, transitively, deferred components included) has a server:defer
// call site: the page then needs the islands loader (Route.Islands).
func (p *Project) HasIslands(page *File) bool {
	byPath := p.byPath()
	for _, path := range p.AssetGraph(page) {
		if f := byPath[path]; f != nil {
			for _, c := range f.calls() {
				if c.Defer {
					return true
				}
			}
		}
	}
	return false
}

func (p *Project) byPath() map[string]*File {
	m := make(map[string]*File, len(p.Files))
	for _, f := range p.Files {
		m[f.Path] = f
	}
	return m
}

// eagerEdge reports whether a call site renders its target eagerly: no
// lazy directive on it, and not inside the slot content of a lazy call.
func eagerEdge(c compiler.ComponentCall) bool {
	return c.Lazy == compiler.NotDirective && c.In < 0
}

// LazyLists returns the page's lazy call-site lists (PLAN.md §5 "Lazy
// entries"), sorted by the tagged component's ID:
//   - a component is eager on the page if the page reaches it through call
//     sites that cross no lazy directive (deferred ones included); its
//     scripts go in the page bundle;
//   - each lazy call site (client:idle, client:visible, client:media) in a
//     file of the page's graph lists the tagged component plus every
//     component reached from it, or from the calls in its slot content,
//     through eager edges, minus the eager ones and the files without
//     scripts. A nested lazy directive starts its own list.
//
// Lists of call sites that tag the same component are merged, since the
// marker names only the component. Modules are in module-evaluation order
// (AssetGraph).
func (p *Project) LazyLists(page *File) []assets.Lazy {
	byPath := p.byPath()
	graph := p.AssetGraph(page)
	order := make(map[string]int, len(graph))
	for i, path := range graph {
		order[path] = i
	}
	// reach adds to seen every file reached from start through eager edges.
	reach := func(start []string, seen map[string]bool) {
		stack := slices.Clone(start)
		for len(stack) > 0 {
			path := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f := byPath[path]
			if f == nil || seen[path] {
				continue
			}
			seen[path] = true
			for _, c := range f.calls() {
				if eagerEdge(c) && c.Target != "" {
					stack = append(stack, c.Target)
				}
			}
		}
	}
	eager := map[string]bool{}
	reach([]string{page.Path}, eager)

	lists := map[string]map[string]bool{} // tagged ID → lazy files
	for _, path := range graph {
		f := byPath[path]
		if f == nil {
			continue
		}
		calls := f.calls()
		for i, c := range calls {
			if c.Lazy == compiler.NotDirective || byPath[c.Target] == nil {
				continue
			}
			start := []string{c.Target}
			for _, d := range calls {
				if d.In == i && d.Lazy == compiler.NotDirective && d.Target != "" {
					start = append(start, d.Target)
				}
			}
			seen := map[string]bool{}
			reach(start, seen)
			id := compiler.ComponentID(c.Target)
			set := lists[id]
			if set == nil {
				set = map[string]bool{}
				lists[id] = set
			}
			for file := range seen {
				if !eager[file] && hasScripts(byPath[file]) {
					set[file] = true
				}
			}
		}
	}
	var out []assets.Lazy
	for id, set := range lists {
		if len(set) == 0 {
			continue
		}
		l := assets.Lazy{ID: id}
		for file := range set {
			l.Modules = append(l.Modules, file)
		}
		slices.SortFunc(l.Modules, func(a, b string) int { return order[a] - order[b] })
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b assets.Lazy) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// hasScripts reports whether f has a processed <script>.
func hasScripts(f *File) bool {
	return f != nil && len(compiler.Scripts(f.AST)) > 0
}
