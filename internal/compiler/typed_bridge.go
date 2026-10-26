package compiler

// The typed server→client bridge in the typed pass (PLAN.md §5c): the TS
// types of a component's data-* keys and set:json blocks, and the
// generate-time check of orbit.JSON arguments.

import (
	"fmt"
	"go/ast"
	gotoken "go/token"
	"go/types"
	"maps"
	"path"
	"slices"
	"strings"
)

// bridgeKey accumulates one key's types and sites.
type bridgeKey struct {
	types, sites []string
}

// bridgeUnit builds a UnitTypes from sites.
type bridgeUnit struct {
	path       string
	data, json map[string]*bridgeKey
}

func newBridgeUnit(orbitPath string) *bridgeUnit {
	return &bridgeUnit{path: orbitPath, data: map[string]*bridgeKey{}, json: map[string]*bridgeKey{}}
}

// add records one site of key s with TS type ts.
func (b *bridgeUnit) add(s *JSONSite, ts string, lines *LineIndex) {
	m := b.data
	if s.Block {
		m = b.json
	}
	k := m[s.Name]
	if k == nil {
		k = &bridgeKey{}
		m[s.Name] = k
	}
	if !slices.Contains(k.types, ts) {
		k.types = append(k.types, ts)
	}
	site := s.Attr
	if s.Block {
		site = "set:json #" + s.Name
	}
	if lines != nil {
		p := lines.Position(s.Pos)
		site += fmt.Sprintf(" (%s:%d:%d)", path.Base(b.path), p.Line, p.Col)
	}
	k.sites = append(k.sites, site)
}

func (b *bridgeUnit) result(decls []TSDecl) *UnitTypes {
	keys := func(m map[string]*bridgeKey) []TSKey {
		var out []TSKey
		for _, name := range slices.Sorted(maps.Keys(m)) {
			k := m[name]
			out = append(out, TSKey{Name: name, Types: k.types, Sites: k.sites})
		}
		return out
	}
	return &UnitTypes{Path: b.path, Data: keys(b.data), JSON: keys(b.json), Decls: decls}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// uncheckedTypes is the contract of a unit without a typed pass (PLAN.md
// §5c "Unchecked units"): its direct orbit.JSON keys and its set:json
// blocks, every one unknown.
func uncheckedTypes(u Unit) *UnitTypes {
	if u.Pass1 == nil || len(u.Pass1.JSONSites) == 0 {
		return nil
	}
	b := newBridgeUnit(unitPath(u))
	var lines *LineIndex
	if u.Input != nil && u.Input.File != nil {
		lines = u.Input.File.Lines
	}
	for i := range u.Pass1.JSONSites {
		s := &u.Pass1.JSONSites[i]
		if s.Block || s.Direct {
			b.add(s, "unknown", lines)
		}
	}
	return b.result(nil)
}

// bridgeTypes computes a checked unit's contract and the generate-time
// errors of its orbit.JSON arguments and set:json values.
func (s *tyState) bridgeTypes(u *tyUnit, info *types.Info, env *tyEnv) (*UnitTypes, []Diagnostic) {
	pass1 := u.unit.Pass1
	names := runtimeNames(u.unit.Input.Analysis)
	tf := s.fset.File(u.implFile.Pos())
	m := newTSMapper(s.nsOf)
	var ds []Diagnostic
	smap := pass1.Impl.Map
	report := func(e ast.Expr, what string, p *jsonProblem) {
		start, ok1 := smap.OrbitOffset(tf.Offset(e.Pos()))
		end, ok2 := smap.OrbitOffset(tf.Offset(e.End()))
		if !ok1 {
			return
		}
		if !ok2 || end < start {
			end = start
		}
		ds = append(ds, Diagnostic{Severity: SeverityError, Span: Span{start, end}, Message: jsonProblemMessage(what, p)})
	}
	// Every direct orbit.JSON call in the impl file, wherever it is.
	// A mapper of its own, so arguments that aren't keys add no
	// declarations.
	chk := newTSMapper(s.nsOf)
	ast.Inspect(u.implFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		arg := directJSONArg(call, names)
		if arg == nil || !identical(info.Types[call].Type, env.jsonValue) {
			return true
		}
		if t, _, ok := tyStatic(info.Types[arg]); ok {
			if _, p := chk.Map(t); p != nil {
				report(arg, "orbit.JSON", p)
			}
		}
		return true
	})

	var b *bridgeUnit
	if len(pass1.JSONSites) > 0 {
		b = newBridgeUnit(u.path)
		exprs := sitesExprs(tf, u.implFile, pass1.JSONSites)
		lines := u.unit.Input.File.Lines
		for i := range pass1.JSONSites {
			site := &pass1.JSONSites[i]
			e := exprs[i]
			if e == nil {
				continue
			}
			t, nilv, ok := tyStatic(info.Types[e])
			if !ok {
				continue
			}
			isJV := !nilv && identical(t, env.jsonValue)
			var ts string
			switch {
			case isJV && directJSONArg(e, names) != nil:
				at, _, ok := tyStatic(info.Types[directJSONArg(e, names)])
				if !ok {
					ts = "unknown"
				} else {
					ts, _ = m.Map(at)
				}
			case isJV:
				ts = "unknown"
			case !site.Block:
				continue // a data-* attribute that isn't JSON
			case nilv:
				ts = "null"
			default:
				var p *jsonProblem
				ts, p = m.Map(t)
				if p != nil {
					report(e, "set:json", p)
				}
			}
			if ts == "" {
				ts = "unknown"
			}
			b.add(site, ts, lines)
		}
	}
	if b == nil {
		return nil, ds
	}
	return b.result(m.Decls()), ds
}

// sitesExprs finds the expression of each JSON site in the impl file: the
// outermost expression spanning exactly its Expr.
func sitesExprs(tf *gotoken.File, f *ast.File, sites []JSONSite) []ast.Expr {
	byStart := map[int][]int{}
	for i, s := range sites {
		byStart[s.Expr.Start] = append(byStart[s.Expr.Start], i)
	}
	out := make([]ast.Expr, len(sites))
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok || n == nil {
			return true
		}
		for _, i := range byStart[tf.Offset(e.Pos())] {
			if out[i] == nil && tf.Offset(e.End()) == sites[i].Expr.End {
				out[i] = e
			}
		}
		return true
	})
	return out
}

// jsonProblemMessage is the generate-time error for a type encoding/json
// can't encode.
func jsonProblemMessage(what string, p *jsonProblem) string {
	t := types.TypeString(p.typ, func(pkg *types.Package) string { return pkg.Name() })
	if p.path == "" {
		return fmt.Sprintf("%s: the value has type %s, which encoding/json can't encode", what, t)
	}
	return fmt.Sprintf("%s: %s has type %s, which encoding/json can't encode", what, p.path, t)
}

// nsOf is the TS namespace of a package's types: a generated package's
// types go in a namespace named after its .orbit file.
func (s *tyState) nsOf(pkg *types.Package) tsNS {
	p := pkg.Path()
	if isGenPath(p, s) {
		if orbitPath, ok := s.genFiles[p]; ok {
			base := strings.TrimSuffix(orbitPath, path.Ext(orbitPath))
			return tsNS{Key: "orbit:" + orbitPath, Base: path.Base(base), Path: base}
		}
	}
	return tsNS{Key: p, Base: pkg.Name(), Path: p}
}
