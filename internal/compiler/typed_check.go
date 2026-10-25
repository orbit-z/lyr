package compiler

// The resident go/types checker behind Checker (PLAN.md §3.3 Engine and
// Dev): decl and impl packages are checked from in-memory pass-1 source,
// behind a memoized importer, and every other package comes from the
// ExportLoader. Owned by agent C.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// tyUnit is the checker's state for one .orbit file.
type tyUnit struct {
	path string // project-relative .orbit path
	unit Unit
	// declPath and implPath are the import paths of its packages.
	declPath, implPath string
	declFile, implFile *ast.File
	parseErr           bool
	// genDecls are the generated decl packages the impl (implGen) and the
	// decl (declGen) import; ext are their non-generated imports.
	implGen, declGen []string
	ext              []string
	result           TypedUnit
}

// tyDecl is one memoized decl package.
type tyDecl struct {
	unit *tyUnit
	once sync.Once
	pkg  *types.Package
	err  error // type errors, an import cycle, a parse error
}

// tyStats counts the type-checks done, for tests.
type tyStats struct {
	decl, impl atomic.Int64
}

// tyState is the Checker's state (the struct in typed.go holds a pointer to
// it, so the contract file stays small).
type tyState struct {
	fset *gotoken.FileSet
	// imp is the external importer from the last Load, nil when not loaded;
	// loaded is the set of paths it was loaded for.
	imp    *tyExtImporter
	loaded map[string]bool
	// loadWarn is set when the last Load failed: every unit falls back.
	loadWarn string
	envOnce  sync.Once
	env      *tyEnv
	module   string
	units    map[string]*tyUnit // by .orbit path
	decls    map[string]*tyDecl // by decl import path
	stats    tyStats
}

// tyExtImporter wraps the loader's importer, recording failures for the
// warnings.
type tyExtImporter struct {
	imp    types.Importer
	mu     sync.Mutex
	failed map[string]string // path → error
}

func (x *tyExtImporter) Import(path string) (*types.Package, error) {
	pkg, err := x.imp.Import(path)
	if err != nil {
		x.mu.Lock()
		if _, ok := x.failed[path]; !ok {
			x.failed[path] = err.Error()
		}
		x.mu.Unlock()
		return nil, err
	}
	return pkg, nil
}

func (x *tyExtImporter) warnings() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	var w []string
	for p, e := range x.failed {
		w = append(w, fmt.Sprintf("orbit: can't read export data for %s: %s; using Expr fallbacks", p, firstLine(e)))
	}
	sort.Strings(w)
	return w
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// tySrcImporter checks generated decl packages from source on first use and
// imports everything else through the external importer.
type tySrcImporter struct {
	s *tyState
}

var errDeclBroken = errors.New("declaration package has errors")

func (si tySrcImporter) Import(path string) (*types.Package, error) {
	d, ok := si.s.decls[path]
	if !ok {
		if isGenPath(path, si.s) {
			return nil, fmt.Errorf("no generated package %s", path)
		}
		return si.s.imp.Import(path)
	}
	d.once.Do(func() {
		if d.err != nil { // a parse error or a cycle, set beforehand
			return
		}
		si.s.stats.decl.Add(1)
		var n int
		conf := types.Config{Importer: si, Error: func(error) { n++ }}
		d.pkg, _ = conf.Check(path, si.s.fset, []*ast.File{d.unit.declFile}, nil)
		if n > 0 {
			d.err = errDeclBroken
		}
	})
	if d.err != nil {
		// No package with the error: go/types would mark a shared package
		// fake.
		return nil, d.err
	}
	return d.pkg, nil
}

// isGenPath reports whether path is under the module's orbit_gen/.
func isGenPath(path string, s *tyState) bool {
	return s.module != "" && strings.HasPrefix(path, s.module+"/"+GenDir+"/")
}

func newTyState() *tyState {
	return &tyState{fset: gotoken.NewFileSet(), units: map[string]*tyUnit{}, decls: map[string]*tyDecl{}}
}

// unitPath is the key of a unit: its .orbit path.
func unitPath(u Unit) string {
	if u.Input != nil && u.Input.Analysis != nil && u.Input.Analysis.Info.Path != "" {
		return u.Input.Analysis.Info.Path
	}
	if u.Pass1 != nil && u.Pass1.Impl != nil {
		if u.Pass1.Impl.Map != nil && u.Pass1.Impl.Map.Orbit != "" {
			return u.Pass1.Impl.Map.Orbit
		}
		return u.Pass1.Impl.Path
	}
	return ""
}

// parseUnit parses a unit's two files and lists its imports.
func (s *tyState) parseUnit(u Unit) *tyUnit {
	tu := &tyUnit{path: unitPath(u), unit: u, result: TypedUnit{Output: u.Pass1}}
	if u.Input == nil || u.Pass1 == nil || u.Pass1.Decl == nil || u.Pass1.Impl == nil {
		tu.parseErr = true
		return tu
	}
	tu.declPath, tu.implPath = u.Pass1.Decl.ImportPath, u.Pass1.Impl.ImportPath
	if u.Input.Options.Mode == Tolerant {
		tu.parseErr = true // Tolerant output is never type-checked
		return tu
	}
	gen := u.Input.Options.Module + "/" + GenDir + "/"
	var err1, err2 error
	tu.declFile, err1 = goparser.ParseFile(s.fset, u.Pass1.Decl.Path, u.Pass1.Decl.Content, goparser.SkipObjectResolution)
	tu.implFile, err2 = goparser.ParseFile(s.fset, u.Pass1.Impl.Path, u.Pass1.Impl.Content, goparser.SkipObjectResolution)
	if err1 != nil || err2 != nil {
		tu.parseErr = true
		return tu
	}
	ext := map[string]bool{}
	collect := func(f *ast.File, into *[]string) {
		for _, im := range f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				continue
			}
			if strings.HasPrefix(p, gen) {
				*into = append(*into, p)
			} else {
				ext[p] = true
			}
		}
	}
	collect(tu.declFile, &tu.declGen)
	collect(tu.implFile, &tu.implGen)
	for p := range ext {
		tu.ext = append(tu.ext, p)
	}
	slices.Sort(tu.ext)
	return tu
}

// extPaths is the sorted union of every unit's non-generated imports.
func (s *tyState) extPaths() []string {
	set := map[string]bool{}
	for _, u := range s.units {
		for _, p := range u.ext {
			set[p] = true
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// load runs the loader over every unit's external imports.
func (s *tyState) load(ctx context.Context, loader ExportLoader) {
	paths := s.extPaths()
	s.loaded = map[string]bool{}
	for _, p := range paths {
		s.loaded[p] = true
	}
	s.loadWarn = ""
	s.envOnce = sync.Once{}
	s.env = nil
	imp, err := loader.Load(ctx, s.fset, paths)
	if err != nil {
		s.imp = &tyExtImporter{imp: failImporter{err}, failed: map[string]string{}}
		s.loadWarn = fmt.Sprintf("orbit: can't load export data: %s; using Expr fallbacks", firstLine(err.Error()))
		return
	}
	s.imp = &tyExtImporter{imp: imp, failed: map[string]string{}}
	// Import every path once, serially, before the parallel checks: the gc
	// importer completes a package that an earlier import only referenced
	// by writing to it (SetImports), which would race with checkers reading
	// it. Afterwards every Import returns a complete, cached package.
	for _, p := range paths {
		s.imp.Import(p)
	}
}

type failImporter struct{ err error }

func (f failImporter) Import(string) (*types.Package, error) { return nil, f.err }

// needsLoad reports whether some unit imports a path the last Load didn't
// cover (a new import is like a go.mod edit: everything is reloaded).
func (s *tyState) needsLoad() bool {
	if s.imp == nil {
		return true
	}
	for _, u := range s.units {
		for _, p := range u.ext {
			if !s.loaded[p] {
				return true
			}
		}
	}
	return false
}

// resetDecls makes fresh decl entries for the given decl paths (all when
// paths is nil), then marks those in import cycles or with parse errors.
func (s *tyState) resetDecls(paths map[string]bool) {
	if paths == nil {
		s.decls = map[string]*tyDecl{}
	}
	for _, u := range s.units {
		if u.declPath == "" || (paths != nil && !paths[u.declPath]) {
			continue
		}
		d := &tyDecl{unit: u}
		if u.parseErr {
			d.err = errDeclBroken
		}
		s.decls[u.declPath] = d
	}
	for p := range paths {
		if _, ok := s.declUnit(p); !ok {
			delete(s.decls, p) // removed
		}
	}
	// Import cycles between decl packages (M5 reports them): a cycle would
	// deadlock the memoized importer, so its members are broken up front.
	for p := range s.cyclic() {
		if d := s.decls[p]; d != nil && (paths == nil || paths[p]) {
			d.err = errDeclBroken
		}
	}
}

func (s *tyState) declUnit(declPath string) (*tyUnit, bool) {
	for _, u := range s.units {
		if u.declPath == declPath {
			return u, true
		}
	}
	return nil, false
}

// cyclic returns the decl packages on an import cycle (Tarjan's SCCs).
func (s *tyState) cyclic() map[string]bool {
	edges := map[string][]string{}
	for _, u := range s.units {
		if u.declPath != "" {
			edges[u.declPath] = u.declGen
		}
	}
	index, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	out := map[string]bool{}
	n := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = n, n
		n++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range edges[v] {
			if _, known := edges[w]; !known {
				continue
			}
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
			if w == v {
				out[v] = true
			}
		}
		if low[v] == index[v] {
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
			if len(scc) > 1 {
				for _, w := range scc {
					out[w] = true
				}
			}
		}
	}
	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if _, seen := index[k]; !seen {
			visit(k)
		}
	}
	return out
}

func (s *tyState) getEnv() *tyEnv {
	s.envOnce.Do(func() {
		if rt, err := s.imp.Import(RuntimePath); err == nil {
			s.env = newTyEnv(rt)
		}
	})
	return s.env
}

// checkUnits type-checks the impl packages of units in parallel (decls on
// demand), picks the typed writes and re-emits.
func (s *tyState) checkUnits(ctx context.Context, units []*tyUnit, generate func(*Input) *Output) error {
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, u := range units {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			u.result = s.checkUnit(u, generate)
		})
	}
	wg.Wait()
	return ctx.Err()
}

func (s *tyState) checkUnit(u *tyUnit, generate func(*Input) *Output) TypedUnit {
	fallback := TypedUnit{Output: u.unit.Pass1}
	if u.parseErr || s.loadWarn != "" {
		return fallback
	}
	si := tySrcImporter{s}
	if _, err := si.Import(u.declPath); err != nil {
		return fallback
	}
	env := s.getEnv()
	if env == nil {
		return fallback
	}
	s.stats.impl.Add(1)
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	var n int
	conf := types.Config{Importer: si, Error: func(error) { n++ }}
	conf.Check(u.implPath, s.fset, []*ast.File{u.implFile}, info)
	if n > 0 {
		return fallback
	}
	writes := siteWrites(s.fset, u.implFile, u.unit.Pass1.CallSites, info, env)
	res := TypedUnit{Output: u.unit.Pass1, Writes: writes, Checked: true}
	if len(writes) > 0 {
		in := *u.unit.Input
		in.Typed = writes
		res.Output = generate(&in)
	}
	return res
}

// siteWrites finds each call site's write call in the impl file by its Go
// offset and classifies its last argument (the expression).
func siteWrites(fset *gotoken.FileSet, f *ast.File, sites []CallSite, info *types.Info, env *tyEnv) TypedWrites {
	byStart := make(map[int]*CallSite, len(sites))
	for i := range sites {
		byStart[sites[i].Call.Start] = &sites[i]
	}
	tf := fset.File(f.Pos())
	writes := TypedWrites{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		site := byStart[tf.Offset(call.Pos())]
		if site == nil {
			return true
		}
		tv, ok := info.Types[call.Args[len(call.Args)-1]]
		if !ok {
			return true
		}
		var w TypedWrite
		if site.Kind == SiteAttr {
			w = env.classifyAttr(tv, site.URLAttr)
		} else {
			w = env.classify(tv)
		}
		if w.Kind != WriteFallback {
			writes[site.Key] = w
		}
		return true
	})
	return writes
}

// warnings collects the load and export-data warnings.
func (s *tyState) warnings() []string {
	var w []string
	if s.loadWarn != "" {
		w = append(w, s.loadWarn)
	}
	if s.imp != nil {
		w = append(w, s.imp.warnings()...)
	}
	return w
}

// check is Checker.Check.
func (c *Checker) check(ctx context.Context, units []Unit) (*CheckResult, error) {
	s := newTyState()
	c.state = s
	tus := s.parseAll(units)
	for _, tu := range tus {
		s.units[tu.path] = tu
	}
	s.load(ctx, c.loader)
	s.resetDecls(nil)
	if err := s.checkUnits(ctx, tus, c.gen()); err != nil {
		return nil, err
	}
	res := &CheckResult{Warnings: s.warnings()}
	for _, tu := range tus {
		res.Units = append(res.Units, tu.result)
	}
	return res, nil
}

func (c *Checker) gen() func(*Input) *Output {
	if c.generate != nil {
		return c.generate
	}
	return Generate
}

// parseAll parses units in parallel, in input order.
func (s *tyState) parseAll(units []Unit) []*tyUnit {
	for _, u := range units {
		if s.module == "" && u.Input != nil {
			s.module = u.Input.Options.Module
		}
	}
	out := make([]*tyUnit, len(units))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, u := range units {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			out[i] = s.parseUnit(u)
		})
	}
	wg.Wait()
	return out
}

// update is Checker.Update.
func (c *Checker) update(ctx context.Context, changed []Unit, removed []string) (*CheckResult, error) {
	s := c.state
	if s == nil {
		return nil, errors.New("orbit: Checker.Update called before Check")
	}
	// Decl packages whose content changed (or that were added or removed).
	dirty := map[string]bool{}
	for _, p := range removed {
		if u := s.units[p]; u != nil {
			if u.declPath != "" {
				dirty[u.declPath] = true
			}
			delete(s.units, p)
		}
	}
	tus := s.parseAll(changed)
	changedSet := map[string]bool{}
	for _, tu := range tus {
		old := s.units[tu.path]
		if old == nil || old.declPath != tu.declPath || old.parseErr || tu.parseErr ||
			!bytes.Equal(old.unit.Pass1.Decl.Content, tu.unit.Pass1.Decl.Content) {
			if tu.declPath != "" {
				dirty[tu.declPath] = true
			}
			if old != nil && old.declPath != "" {
				dirty[old.declPath] = true
			}
		}
		s.units[tu.path] = tu
		changedSet[tu.path] = true
	}
	var order []*tyUnit
	if s.needsLoad() {
		// Invalidated, or a new import: reload and re-check everything.
		s.load(ctx, c.loader)
		s.resetDecls(nil)
		order = s.orderUnits(tus, nil)
	} else {
		// Dependents: decls importing a dirty decl, transitively.
		for grew := true; grew; {
			grew = false
			for _, u := range s.units {
				if u.declPath == "" || dirty[u.declPath] {
					continue
				}
				for _, p := range u.declGen {
					if dirty[p] {
						dirty[u.declPath] = true
						grew = true
						break
					}
				}
			}
		}
		s.resetDecls(dirty)
		order = s.orderUnits(tus, func(u *tyUnit) bool {
			if dirty[u.declPath] {
				return true
			}
			for _, p := range u.implGen {
				if dirty[p] {
					return true
				}
			}
			return false
		})
	}
	if err := s.checkUnits(ctx, order, c.gen()); err != nil {
		return nil, err
	}
	res := &CheckResult{Warnings: s.warnings()}
	for _, tu := range order {
		res.Units = append(res.Units, tu.result)
	}
	return res, nil
}

// orderUnits returns the changed units, then the other units for which
// affected returns true (every other unit when affected is nil), sorted by
// path.
func (s *tyState) orderUnits(changed []*tyUnit, affected func(*tyUnit) bool) []*tyUnit {
	order := append([]*tyUnit(nil), changed...)
	in := map[*tyUnit]bool{}
	for _, u := range changed {
		in[u] = true
	}
	var rest []*tyUnit
	for _, u := range s.units {
		if !in[u] && (affected == nil || affected(u)) {
			rest = append(rest, u)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].path < rest[j].path })
	return append(order, rest...)
}

// invalidate is Checker.Invalidate.
func (c *Checker) invalidate() {
	if c.state != nil {
		c.state.imp = nil
	}
}
