package project

import (
	"slices"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// declEdge is one .orbit import that the declaration package uses: Props
// (or something it depends on) refers to the imported component's
// declaration package (PLAN.md §2.2).
type declEdge struct {
	to  string
	imp *compiler.OrbitImport
}

// checkPropsCycles reports import cycles between declaration packages: two
// (or more) Props types that refer to each other's components. Go can't
// compile them; components may otherwise import each other freely, since
// implementation packages only import declaration packages (PLAN.md §3.1).
// Each cycle is reported once, at the import in its first file (by path)
// that starts the chain, with the full chain.
func checkPropsCycles(files []*File) []Diagnostic {
	byPath := map[string]*File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	edges := map[string][]declEdge{}
	for _, f := range files {
		for i := range f.Analysis.OrbitImports {
			im := &f.Analysis.OrbitImports[i]
			if im.Place&compiler.PlaceDecl == 0 || byPath[im.Resolved] == nil {
				continue
			}
			edges[f.Path] = append(edges[f.Path], declEdge{to: im.Resolved, imp: im})
		}
	}
	var ds []Diagnostic
	for _, scc := range stronglyConnected(files, edges) {
		start := scc[0] // scc is sorted
		chain, first := shortestCycle(start, scc, edges)
		f := byPath[start]
		ds = append(ds, Diagnostic{
			File:     f.Path,
			Pos:      f.AST.Lines.Position(first.imp.AliasSpan.Start),
			Severity: compiler.SeverityError,
			Message: "import cycle not allowed between component Props: " + strings.Join(chain, " → ") +
				"; declare the shared types in a package under internal/",
			Span: first.imp.AliasSpan,
			off:  first.imp.AliasSpan.Start,
		})
	}
	return ds
}

// stronglyConnected returns the cyclic strongly connected components of the
// graph (Tarjan): those with two or more files, or one file that imports
// itself. Each is sorted, and they're in order of their first file.
func stronglyConnected(files []*File, edges map[string][]declEdge) [][]string {
	index, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	n := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = n, n
		n++
		stack = append(stack, v)
		onStack[v] = true
		self := false
		for _, e := range edges[v] {
			w := e.to
			if w == v {
				self = true
			}
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] != index[v] {
			return
		}
		var scc []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			scc = append(scc, w)
			if w == v {
				break
			}
		}
		if len(scc) > 1 || self {
			slices.Sort(scc)
			out = append(out, scc)
		}
	}
	for _, f := range files {
		if _, seen := index[f.Path]; !seen {
			visit(f.Path)
		}
	}
	slices.SortFunc(out, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	return out
}

// shortestCycle finds a shortest path from start back to itself inside scc
// (breadth-first, edges in import order) and returns it as a chain of paths
// beginning and ending with start, plus its first edge.
func shortestCycle(start string, scc []string, edges map[string][]declEdge) ([]string, declEdge) {
	in := map[string]bool{}
	for _, p := range scc {
		in[p] = true
	}
	type step struct {
		prev string
		edge declEdge
	}
	from := map[string]step{}
	queue := []string{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range edges[v] {
			if !in[e.to] {
				continue
			}
			if e.to == start {
				chain := []string{start}
				first := e
				for u := v; u != start; u = from[u].prev {
					chain = append(chain, u)
					first = from[u].edge
				}
				chain = append(chain, start)
				slices.Reverse(chain)
				return chain, first
			}
			if _, seen := from[e.to]; !seen && e.to != start {
				from[e.to] = step{prev: v, edge: e}
				queue = append(queue, e.to)
			}
		}
	}
	return []string{start, start}, edges[start][0] // unreachable for a cyclic SCC
}
