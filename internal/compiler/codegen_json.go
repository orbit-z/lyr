package compiler

// The writes that give a component's scripts typed data (PLAN.md §5c):
// data-* attribute expressions of HTML elements and set:json blocks with a
// static id, recorded as JSONSites for the typed pass.

import (
	"go/ast"
	goparser "go/parser"
	"path"
	"strconv"
	"strings"
)

// JSONSite is a write that may give the component's scripts a typed key
// (PLAN.md §5c "Client side"): a data-* attribute expression of an HTML
// element (a key of data(el, key) if its value is an orbit.JSONValue), or
// a set:json with a static id (a key of json(id)).
type JSONSite struct {
	// Block is true for set:json, whose key is the static id; false for a
	// data-* attribute, whose key is the dataset name ("authorName").
	Block bool
	Name  string
	// Attr is the attribute as written ("data-author-name", "set:json"),
	// and Pos its offset in the .orbit file.
	Attr string
	Pos  int
	// Direct reports that the expression is a direct call orbit.JSON(arg)
	// of the runtime's function, recognized by the file's import name for
	// it (no types needed, so pass 1 knows it).
	Direct bool
	// Expr is the expression's span in Impl.Content.
	Expr Span
}

// datasetName returns the DOM dataset name of a data-* attribute
// ("data-author-name" → "authorName"), or ok false when name isn't one
// whose remaining characters, lowercased, are all [a-z0-9-].
func datasetName(name string) (string, bool) {
	lower := strings.ToLower(name)
	rest, ok := strings.CutPrefix(lower, "data-")
	if !ok || rest == "" {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == '-' && i+1 < len(rest) && 'a' <= rest[i+1] && rest[i+1] <= 'z':
			b.WriteByte(rest[i+1] - 'a' + 'A')
			i++
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-':
			b.WriteByte(c)
		default:
			return "", false
		}
	}
	return b.String(), true
}

// runtimeNames returns the names under which the file's Go code sees the
// runtime package: the implicit orbit, unless an import takes that name for
// another package, and every import of the runtime.
func runtimeNames(a *Analysis) map[string]bool {
	names := map[string]bool{"orbit": true}
	for _, im := range a.GoImports {
		name := im.Name
		if name == "" {
			name = path.Base(im.Path)
		}
		if im.Path == RuntimePath {
			names[name] = true
		} else if name == "orbit" {
			delete(names, "orbit")
		}
	}
	return names
}

// directJSONArg returns the argument of a direct call <rt>.JSON(arg), rt
// being one of the runtime's names, or nil.
func directJSONArg(e ast.Expr, names map[string]bool) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "JSON" {
		return nil
	}
	if x, ok := sel.X.(*ast.Ident); !ok || !names[x.Name] {
		return nil
	}
	return call.Args[0]
}

// isDirectJSON reports whether code is a direct orbit.JSON(arg) call.
func (g *cgGen) isDirectJSON(code string) bool {
	if !strings.Contains(code, "JSON") {
		return false
	}
	if g.rtNames == nil {
		g.rtNames = runtimeNames(g.a)
	}
	e, err := goparser.ParseExpr(code)
	return err == nil && directJSONArg(e, g.rtNames) != nil
}

// jsonAttrSite records a data-* attribute site whose expression was just
// emitted at expr.
func (g *cgGen) jsonAttrSite(a *Attr, expr Span) {
	name, ok := datasetName(a.Name)
	if !ok || expr.End == expr.Start {
		return
	}
	g.jsonSites = append(g.jsonSites, JSONSite{Name: name, Attr: a.Name, Pos: a.Start,
		Direct: g.isDirectJSON(a.Code.Text), Expr: expr})
}

// setJSON emits a set:json write, orbit__w.JSON("<pos>", <expr>), and
// records it as a JSON site when its element has a static id.
func (g *cgGen) setJSON(set *Attr, node Node) {
	o := g.o
	o.flush()
	o.ln(NameW + ".JSON(" + strconv.Quote(g.position(set.Start)) + ",")
	expr := o.exact(set.Code.Span)
	o.str(",\n")
	o.ln(")")
	t := TagOf(node)
	if t == nil {
		return
	}
	id := t.Attr("id")
	if id == nil || !id.IsStatic() || id.Value == "" {
		return
	}
	g.jsonSites = append(g.jsonSites, JSONSite{Block: true, Name: id.Value, Attr: set.Name, Pos: set.Start,
		Direct: g.isDirectJSON(set.Code.Text), Expr: expr})
}
