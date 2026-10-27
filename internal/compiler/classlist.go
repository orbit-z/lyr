package compiler

// The class:list directive (PLAN.md §2.3): parsing its value as the
// elements of a Go composite literal, and generating its writes on HTML
// elements (static classes folded into Raw, conditions as ifs, dynamic
// items as typed writes) and its class prop on components.

import (
	"errors"
	"go/ast"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"html"
	"strconv"
	"strings"
)

const classListExample = `e.g. class:list={"card", "active": active}`

// parseClassList parses the value of a class:list attribute into its items,
// reporting syntax errors at their .orbit position. It returns nil when the
// value doesn't parse or holds no items.
func (p *parser) parseClassList(a *Attr) []ClassItem {
	code := a.Code
	if strings.TrimSpace(code.Text) == "" {
		return nil
	}
	const prefix = "_{"
	src := prefix + code.Text + "}"
	fset := gotoken.NewFileSet()
	e, err := goparser.ParseExprFrom(fset, "", src, goparser.SkipObjectResolution)
	if err != nil {
		off := 0
		msg := err.Error()
		var el goscanner.ErrorList
		if errors.As(err, &el) && len(el) > 0 {
			off, msg = el[0].Pos.Offset-len(prefix), el[0].Msg
		}
		off = max(0, min(off, len(code.Text)))
		p.errorf(Span{code.Start + off, code.Start + off}, "syntax error in class:list: %s", msg)
		return nil
	}
	tf := fset.File(e.Pos())
	lit, ok := e.(*ast.CompositeLit)
	if !ok || tf.Offset(lit.Rbrace) != len(src)-1 {
		p.errorf(code.Span, "class:list takes a comma-separated list of classes, %s", classListExample)
		return nil
	}
	span := func(n ast.Node) Span {
		return Span{code.Start + tf.Offset(n.Pos()) - len(prefix), code.Start + tf.Offset(n.End()) - len(prefix)}
	}
	gocode := func(n ast.Node) GoCode {
		s := span(n)
		return GoCode{Span: s, Text: p.src[s.Start:s.End]}
	}
	var items []ClassItem
	for _, el := range lit.Elts {
		var it ClassItem
		x := el
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			x = kv.Key
			it.Cond = gocode(kv.Value)
			if c, ok := kv.Value.(*ast.CompositeLit); ok && c.Type == nil {
				p.errorf(span(c), "a class:list condition must be a bool expression, %s", classListExample)
				continue
			}
		}
		if c, ok := x.(*ast.CompositeLit); ok && c.Type == nil {
			p.errorf(span(c), "class:list items can't be nested lists; list the classes directly, or use a []string")
			continue
		}
		it.Span = span(el)
		it.Code = gocode(x)
		if bl, ok := x.(*ast.BasicLit); ok && bl.Kind == gotoken.STRING {
			if v, err := strconv.Unquote(bl.Value); err == nil {
				it.Static, it.Value = true, v
			}
		}
		items = append(items, it)
	}
	return items
}

// isClassSpace reports whether c is HTML whitespace, which separates
// classes (the runtime's rule).
func isClassSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r'
}

// classFields splits s into its classes.
func classFields(s string) []string { return strings.FieldsFunc(s, isClassSpace) }

// clItem is one item of a class list as codegen sees it: a class:list
// item, or the value of a class attribute merged into it.
type clItem struct {
	static bool
	// value is a static item's text: a Go string's value, or, with html
	// set, an attribute value as written (character references kept).
	value string
	html  bool
	// code is a dynamic item's expression; cond a condition, or empty.
	code, cond GoCode
	// span is the item's position, for a static item's type error.
	span Span
	// rest is the operand of a rest spread on the element ("props.Attrs"),
	// whose class is written here (orbit__w.RestClass).
	rest string
}

// htmlText is a static item's classes as they go in the HTML, joined by
// single spaces; "" when it has none.
func (it *clItem) htmlText() string {
	f := classFields(it.value)
	for i, c := range f {
		if it.html {
			f[i] = attrEscaper.Replace(c)
		} else {
			f[i] = attrValueEscaper.Replace(c)
		}
	}
	return strings.Join(f, " ")
}

// plain is a static item's classes as a Go string: an attribute value is
// decoded first, so the runtime's escaping writes it back.
func (it *clItem) plain() string {
	v := it.value
	if it.html {
		v = html.UnescapeString(v)
	}
	return strings.Join(classFields(v), " ")
}

// classAttrs finds a tag's class:list attribute (the first; more are
// reported) and the class attributes merged into it. cl is nil when there
// is none.
func classAttrs(t *Tag) (cl *Attr, merged []*Attr) {
	for _, a := range t.Attrs {
		if a.Directive == ClassList && cl == nil {
			cl = a
		}
	}
	if cl == nil {
		return nil, nil
	}
	for _, a := range t.Attrs {
		if a.Kind != AttrSpread && a.Directive == NotDirective && a.Name == "class" {
			merged = append(merged, a)
		}
	}
	return cl, merged
}

// classItems lists the items of cl, after those of the class attributes
// merged into it (Astro puts the class value first).
func classItems(cl *Attr, merged []*Attr) []clItem {
	var items []clItem
	for _, a := range merged {
		switch {
		case a.IsStatic():
			items = append(items, clItem{static: true, html: true, value: a.Value, span: a.ValueSpan})
		case a.IsExpr() && a.Code.Text != "":
			items = append(items, clItem{code: a.Code, span: a.Code.Span})
		}
	}
	if cl == nil {
		return items
	}
	for _, it := range cl.Classes {
		ci := clItem{cond: it.Cond, span: it.Code.Span}
		if it.Static {
			ci.static, ci.value = true, it.Value
		} else {
			ci.code = it.Code
		}
		items = append(items, ci)
	}
	return items
}

// classList emits an HTML element's class attribute from cl and the class
// attributes merged into it (cl is nil for class attributes merged with a
// rest spread's class alone; at is cl or the first of them), then the
// class of each rest spread. Static classes join the surrounding Raw
// constants; when codegen can't tell whether a class comes first, the
// writes go through ClassStart/ClassEnd, which write no attribute for an
// empty list.
func (g *cgGen) classList(node Node, at, cl *Attr, merged []*Attr, rest []string) {
	o := g.o
	if cl != nil && cl.Classes == nil {
		g.discardClassList(cl)
	}
	items := classItems(cl, merged)
	for _, op := range rest {
		items = append(items, clItem{rest: op})
	}
	known := false // a class has certainly been written
	first := -1    // the item folded into the opening Raw
	writes := false
	for i := range items {
		it := &items[i]
		if it.static && it.htmlText() == "" {
			continue
		}
		if it.static && it.cond.Text == "" && !writes {
			first = i
			o.html(` class="` + it.htmlText())
			known = true
		} else if !writes {
			o.flush()
			o.ln(NameW + ".ClassStart()")
		}
		writes = true
		break
	}
	if !writes && len(items) == 0 {
		return
	}
	for i := range items {
		if i == first {
			continue
		}
		it := &items[i]
		if it.cond.Text != "" {
			o.flush()
			o.ind()
			o.str("if ")
			o.exact(it.cond.Span)
			o.str(" {\n")
			o.depth++
		}
		switch {
		case it.rest != "":
			o.flush()
			o.ln(NameW + ".RestClass(&" + it.rest + ")")
		case it.static && it.htmlText() == "":
		case it.static && known:
			o.html(" " + it.htmlText())
		case it.static:
			o.flush()
			o.ln(NameW + ".Class(" + strconv.Quote(it.plain()) + ")")
			known = known || it.cond.Text == ""
		default:
			g.classSite(SiteClass, node, at, it.code)
		}
		if it.cond.Text != "" {
			o.flush()
			o.depth--
			o.ln("}")
		}
	}
	switch {
	case known:
		o.html(`"`)
	case writes:
		o.flush()
		o.ln(NameW + ".ClassEnd()")
	}
}

// classSite emits the write of a dynamic class:list item: on an element
// (SiteClass) the fallback orbit__w.ClassAny(x) or the typed Class/Classes;
// in a component's class prop (SiteClassProp), an expression of type
// string: orbit__rt.ClassString(x), or string(x) and JoinClasses(x...)
// when typed.
func (g *cgGen) classSite(kind SiteKind, node Node, cl *Attr, code GoCode) {
	o := g.o
	cs := CallSite{Kind: kind, Key: code.Start, Node: node, Attr: cl, Code: code, AttrName: cl.Name}
	tw, typed := g.in.Typed[code.Start]
	typed = typed && (tw.Kind == WriteClass || tw.Kind == WriteClasses)
	if kind == SiteClass && typed && tw.Opt {
		g.classOptSite(cs, tw)
		return
	}
	open, closing := "", ""
	switch {
	case kind == SiteClass && typed:
		open = NameW + "." + tw.Kind.Method() + "("
		if tw.Conv != "" {
			open += tw.Conv + "("
			closing = ")"
		}
	case kind == SiteClass:
		open = NameW + ".ClassAny("
	case typed && tw.Kind == WriteClass:
		open = "string("
	case typed:
		open, closing = NameRuntime+".JoinClasses([]string(", "...)"
	default:
		open = NameRuntime + ".ClassString("
	}
	if kind == SiteClass {
		o.flush()
		o.ind()
	}
	start := len(o.b)
	o.str(open + "\n")
	cs.Arg = o.exact(code.Span)
	o.str(",\n")
	o.ind()
	o.str(")" + closing)
	cs.Call = Span{start, len(o.b)}
	if kind == SiteClass {
		o.str("\n")
	}
	g.sites = append(g.sites, cs)
}

// classOptSite emits the typed write of a class:list item of type
// orbit.Opt[T] on an element (PLAN.md §2.6): T's Class or Classes write of
// its value when it's set.
func (g *cgGen) classOptSite(cs CallSite, tw TypedWrite) {
	o := g.o
	o.flush()
	o.ln("{")
	o.depth++
	o.ind()
	o.str("orbit__o :=\n")
	o.exact(cs.Code.Span)
	o.str("\n")
	o.ln("if orbit__v, orbit__c := orbit__o.Get(); orbit__c {")
	o.depth++
	o.ind()
	start := len(o.b)
	o.str(NameW + "." + tw.Kind.Method() + "(")
	arg := "orbit__v"
	if tw.Conv != "" {
		arg = tw.Conv + "(" + arg + ")"
	}
	argStart := len(o.b)
	o.str(arg)
	cs.Arg = Span{argStart, len(o.b)}
	o.str(")")
	cs.Call = Span{start, len(o.b)}
	o.str("\n")
	o.depth--
	o.ln("}")
	o.depth--
	o.ln("}")
	g.sites = append(g.sites, cs)
}

// classProp emits the value of a component's class prop set by class:list
// (and the class attributes merged into it): a string constant when every
// class is static, else orbit__rt.JoinClasses of the items.
func (g *cgGen) classProp(node Node, cl *Attr, merged []*Attr) {
	o := g.o
	items := classItems(cl, merged)
	static := true
	var parts []string
	for i := range items {
		it := &items[i]
		if !it.static || it.cond.Text != "" {
			static = false
			break
		}
		if s := strings.Join(classFields(it.value), " "); s != "" {
			parts = append(parts, s)
		}
	}
	// A type error (a Class prop that isn't a string) lands on the value.
	vs := cl.ValueSpan
	if static {
		o.inline(vs, strconv.Quote(strings.Join(parts, " ")), SegSpan)
		return
	}
	o.inline(Span{vs.Start, vs.Start + 1}, NameRuntime+".JoinClasses", SegSpan)
	o.str("(")
	o.depth++
	for i := range items {
		it := &items[i]
		if i > 0 {
			o.str(", ")
		}
		if it.cond.Text != "" {
			o.str(NameRuntime + ".ClassIf(\n")
			o.exact(it.cond.Span)
			o.str(",\n")
			o.ind()
		}
		if it.static {
			o.str(strconv.Quote(strings.Join(classFields(it.value), " ")))
		} else {
			g.classSite(SiteClassProp, node, cl, it.code)
		}
		if it.cond.Text != "" {
			o.str(")")
		}
	}
	o.depth--
	o.str(")")
}

// discardClassList emits the Go code of a class:list value that isn't
// generated as a list (Tolerant only): each item and condition as
// "_ = (x)", or, when the value doesn't parse, the whole value as
// "_ = []any{…}", so gopls still completes in it.
func (g *cgGen) discardClassList(a *Attr) {
	if !g.tolerant || strings.TrimSpace(a.Code.Text) == "" || endsInLineComment(a.Code.Text) {
		return
	}
	if a.Classes != nil {
		for _, it := range a.Classes {
			g.discard(it.Code)
			g.discard(it.Cond)
		}
		return
	}
	o := g.o
	o.flush()
	o.ind()
	o.str("_ = []any{")
	o.exact(a.Code.Span)
	o.str("}\n")
}

// discardAttr emits the Go code of an attribute that isn't generated
// (Tolerant only).
func (g *cgGen) discardAttr(a *Attr) {
	if a.Directive == ClassList {
		g.discardClassList(a)
		return
	}
	g.discard(a.Code)
}

// classField matches a component's class:list attribute to its class prop
// (the unknown-prop pre-check), adding the field, or the attribute's code
// to pre when there is no such prop.
func (g *cgGen) classField(n *Component, props *PropsInfo, cl *Attr, merged []*Attr, fields []cgField, pre []GoCode) ([]cgField, []GoCode) {
	key, err := MatchProp(props, "class")
	if err == nil && gotoken.IsIdentifier(key) {
		return append(fields, cgField{key: key, attr: cl, merged: merged, node: n}), pre
	}
	g.errorf(cl.NameSpan, "class:list sets the class prop, which <%s> doesn't have (%s)", n.Name, propList(n.Name, props))
	for _, a := range merged {
		pre = append(pre, a.Code)
	}
	if cl.Classes == nil {
		g.discardClassList(cl)
	}
	for _, it := range cl.Classes {
		pre = append(pre, it.Code, it.Cond)
	}
	return fields, pre
}
