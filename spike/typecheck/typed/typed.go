// Package typed is the prototype of the pass-2 mapping (PLAN.md §3.3): for
// each orbit__w.Expr(x) call, look at the static type of x and pick the typed
// write that produces byte-identical output to Expr's runtime type switch.
package typed

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

const orbitPath = "github.com/bertrandkhe/orbit"

type Kind int

const (
	Fallback Kind = iota // keep w.Expr(x)
	Skip                 // untyped nil: Expr(nil) writes nothing
	String
	Int
	Uint
	Float
	Bool
	HTML
	Bytes
	Render
	Error
	Stringer
)

var kindNames = [...]string{"Expr", "skip", "String", "Int", "Uint", "Float", "Bool", "HTML", "Bytes", "Render", "Error", "Stringer"}

func (k Kind) String() string { return kindNames[k] }

// Decision is the typed write chosen for one expression. Conv is the Go
// conversion to wrap the expression in ("" for none), e.g. "int64".
type Decision struct {
	Kind Kind
	Conv string
}

// Env holds the runtime types the mapping compares against.
type Env struct {
	html, url, renderer types.Type // orbit.HTML, orbit.URL, orbit.Renderer
	renderFunc          types.Type // func(*orbit.Writer) error
	stringer            *types.Interface
	errorIface          *types.Interface
	byteSlice           types.Type
}

// NewEnv finds the orbit package among pkg's imports (direct or indirect).
func NewEnv(orbit *types.Package) *Env {
	sc := orbit.Scope()
	writer := sc.Lookup("Writer").Type()
	errT := types.Universe.Lookup("error").Type()
	// fmt.Stringer, built structurally so we don't need fmt's *types.Package.
	str := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "String", types.NewSignatureType(nil, nil, nil, nil,
			types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.String])), false)),
	}, nil).Complete()
	return &Env{
		html:     sc.Lookup("HTML").Type(),
		url:      sc.Lookup("URL").Type(),
		renderer: sc.Lookup("Renderer").Type(),
		renderFunc: types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewVar(token.NoPos, nil, "w", types.NewPointer(writer))),
			types.NewTuple(types.NewVar(token.NoPos, nil, "", errT)), false),
		stringer:   str,
		errorIface: errT.Underlying().(*types.Interface),
		byteSlice:  types.NewSlice(types.Typ[types.Byte]),
	}
}

// FindOrbit returns the orbit runtime package as seen from pkg.
func FindOrbit(pkg *types.Package) *types.Package {
	seen := map[*types.Package]bool{}
	var walk func(p *types.Package) *types.Package
	walk = func(p *types.Package) *types.Package {
		if p.Path() == orbitPath {
			return p
		}
		if seen[p] {
			return nil
		}
		seen[p] = true
		for _, q := range p.Imports() {
			if r := walk(q); r != nil {
				return r
			}
		}
		return nil
	}
	return walk(pkg)
}

// Classify mirrors the case order of Writer.Expr (PLAN.md §4). The key fact:
// Expr's type switch matches *exact* dynamic types, so `case string` does not
// match `type Slug string`, which falls through to Stringer (if it has a
// String method) or fmt.Sprint. Classify therefore works on the exact type
// first and only uses the underlying type where fmt.Sprint's output is known
// to equal the typed write.
func (e *Env) Classify(tv types.TypeAndValue) Decision {
	t := types.Unalias(tv.Type) // `type S = string` is plain string to Expr
	if t == nil {
		return Decision{Kind: Fallback}
	}
	if b, ok := t.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return Decision{Kind: Skip}
	}
	if tv.Value != nil {
		// Untyped or typed constant. Real codegen can fold it into the Raw
		// run (escaped at compile time); here it still goes through a typed
		// call so the output is checked at runtime.
		return e.Classify(types.TypeAndValue{Type: types.Default(t)})
	}
	switch {
	case types.Identical(t, e.html):
		return Decision{Kind: HTML}
	case types.Identical(t, types.Typ[types.String]):
		return Decision{Kind: String}
	case types.Identical(t, e.url):
		return Decision{Kind: String, Conv: "string"}
	case types.Identical(t, e.byteSlice):
		return Decision{Kind: Bytes}
	}
	if b, ok := t.(*types.Basic); ok { // exact predeclared basic type (not a named one)
		switch {
		case b.Info()&types.IsInteger != 0 && b.Info()&types.IsUnsigned == 0:
			return Decision{Int, conv(b, "int64")}
		case b.Info()&types.IsUnsigned != 0 && b.Kind() != types.Uintptr:
			return Decision{Uint, conv(b, "uint64")}
		case b.Kind() == types.Float32 || b.Kind() == types.Float64:
			return Decision{Float, conv(b, "float64")}
		case b.Kind() == types.Bool:
			return Decision{Kind: Bool}
		}
		return Decision{Kind: Fallback} // complex, uintptr, unsafe.Pointer
	}
	switch {
	case types.Identical(t, e.renderer):
		return Decision{Kind: Render}
	case types.Identical(t, e.renderFunc):
		return Decision{Kind: Render, Conv: "orbit.Renderer"}
	}
	if types.IsInterface(t) {
		// A static interface type: the dynamic value could be anything that
		// satisfies it. Only `error` is safe: every dynamic value hits Expr's
		// error case (or is nil). A Stringer-only interface is NOT safe: a
		// dynamic value that also implements error would print Error().
		if types.Implements(t, e.errorIface) {
			return Decision{Kind: Error}
		}
		return Decision{Kind: Fallback}
	}
	// Concrete, non-basic or named type: method checks in Expr's order.
	if types.Implements(t, e.errorIface) {
		return Decision{Kind: Error}
	}
	if types.Implements(t, e.stringer) {
		return Decision{Kind: Stringer}
	}
	// No relevant methods: Expr ends in fmt.Sprint. For named types whose
	// underlying type is a string/int/bool/float64, fmt.Sprint equals the
	// typed write (fmt's %v for float64 is strconv 'g', -1, 64). float32 is
	// excluded: fmt formats it with bitSize 32 ("0.1") while Expr's float32
	// case widens to float64 ("0.10000000149011612").
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch {
		case b.Kind() == types.Float64:
			return Decision{Float, "float64"}
		case b.Info()&types.IsString != 0:
			return Decision{String, "string"}
		case b.Info()&types.IsInteger != 0 && b.Info()&types.IsUnsigned == 0:
			return Decision{Int, "int64"}
		case b.Info()&types.IsUnsigned != 0 && b.Kind() != types.Uintptr:
			return Decision{Uint, "uint64"}
		case b.Kind() == types.Bool:
			return Decision{Bool, "bool"}
		}
	}
	return Decision{Kind: Fallback}
}

func conv(b *types.Basic, to string) string {
	if b.Name() == to {
		return ""
	}
	return to
}

// Edit is a byte-range replacement in one file.
type Edit struct {
	Start, End int
	Text       string
}

// Stats counts decisions per kind.
type Stats map[Kind]int

// Rewrite finds every orbit__w.Expr(x) call in files and returns, per file
// name, the source with typed writes substituted.
func Rewrite(fset *token.FileSet, files []*ast.File, info *types.Info, env *Env, src map[string][]byte, stats Stats) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range files {
		name := fset.File(f.Pos()).Name()
		var edits []Edit
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Expr" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "orbit__w" {
				return true
			}
			arg := call.Args[0]
			d := env.Classify(info.Types[arg])
			stats[d.Kind]++
			if d.Kind == Fallback {
				return true
			}
			off := func(p token.Pos) int { return fset.Position(p).Offset }
			argSrc := string(src[name][off(arg.Pos()):off(arg.End())])
			var text string
			switch d.Kind {
			case Skip:
				text = "/* nil */"
			default:
				x := argSrc
				if d.Conv != "" {
					x = d.Conv + "(" + x + ")"
				}
				text = "orbit__w." + d.Kind.String() + "(" + x + ")"
			}
			edits = append(edits, Edit{off(call.Pos()), off(call.End()), text})
			return true
		})
		if len(edits) == 0 {
			continue
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].Start < edits[j].Start })
		s := src[name]
		var b []byte
		last := 0
		for _, e := range edits {
			b = append(b, s[last:e.Start]...)
			b = append(b, e.Text...)
			last = e.End
		}
		b = append(b, s[last:]...)
		out[name] = b
	}
	return out
}
