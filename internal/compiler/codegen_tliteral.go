package compiler

// Template literals in codegen (PLAN.md §2.10). A literal that is the whole
// value of an HTML element attribute or of a content expression is lowered
// to the writes of its pieces (litWrite: static parts pre-escaped into Raw,
// each piece a SitePiece call site, 0 allocations once typed). Any other
// literal is a string expression: emitted wherever template Go code is
// copied (cgOut.exact's hook, litCode) as a constant, or as a SiteConcat
// call site, orbit__rt.TemplateString(s0, p0, s1, …) in pass 1, which the
// typed pass turns into a concatenation (concat). A component prop or a
// set:html value gets orbit__rt.TemplateAs(witness, …) instead, converted
// to the field's type.

import (
	"strconv"
	"strings"
)

// litCode is cgOut's hook for template Go code: it emits src[s] with each
// template literal rewritten, and returns the Go span from the first to
// the last byte of code (directives at either end excluded).
func (g *cgGen) litCode(s Span) (Span, bool) {
	var diags []Diagnostic
	lits := findLiterals(g.src, s, &diags)
	if len(lits) == 0 {
		return Span{}, false
	}
	return g.codeLits(s, lits), true
}

// codeLits emits the Go code src[s], whose literals (outermost) are lits:
// the code between them copied exactly (each run under its own directive),
// each literal as a string expression.
func (g *cgGen) codeLits(s Span, lits []*TemplateLiteral) Span {
	o := g.o
	out := Span{-1, -1}
	mark := func(sp Span) {
		if out.Start < 0 {
			out.Start = sp.Start
		}
		out.End = sp.End
	}
	cur := s.Start
	copyRun := func(a, b int) {
		// Whitespace at either end is glue, so the segment starts and ends
		// on a token.
		i, j := a, b
		for i < j && isGoSpace(g.src[i]) {
			i++
		}
		for j > i && isGoSpace(g.src[j-1]) {
			j--
		}
		o.str(g.src[a:i])
		if j > i {
			mark(o.inline(Span{i, j}, g.src[i:j], SegExact))
		}
		o.str(g.src[j:b])
	}
	for _, l := range lits {
		if l.Start > cur {
			copyRun(cur, l.Start)
		}
		mark(g.literal(l))
		cur = l.End
	}
	if cur < s.End {
		copyRun(cur, s.End)
	}
	if out.Start < 0 {
		at := len(o.b)
		out = Span{at, at}
	}
	return out
}

// literal emits a template literal that isn't a whole value: its cooked
// text as a constant when it has no piece, else a concatenation site.
func (g *cgGen) literal(l *TemplateLiteral) Span {
	if l.Const() && l.Closed {
		return g.o.inline(l.Span, strconv.Quote(l.Cooked()), SegSpan)
	}
	return g.concat(l, "", false, nil, nil)
}

// piece emits the Go code of a piece: copied exactly, nested literals
// rewritten.
func (g *cgGen) piece(p TLPart) Span {
	if len(p.Lits) > 0 {
		return g.codeLits(p.Span, p.Lits)
	}
	return g.o.inline(p.Span, p.Text, SegExact)
}

// concat emits a SiteConcat (SiteKind doc): pass 1's TemplateString or
// TemplateAs (witness non-empty) call, or the typed concatenation. bytes
// asks for a []byte (ClassBytes) and needs the typed write.
func (g *cgGen) concat(l *TemplateLiteral, witness string, bytes bool, node Node, a *Attr) Span {
	o := g.o
	key := l.End - 1
	cs := CallSite{Kind: SiteConcat, Key: key, Node: node, Attr: a, Code: GoCode{l.Span, g.src[l.Start:l.End]}, Lit: l, As: witness != ""}
	if a != nil {
		cs.AttrName = a.Name
	}
	start := len(o.b)
	if tw, ok := g.in.Typed[key]; ok && tw.Kind == WriteConcat && len(tw.Pieces) == len(l.Parts) {
		g.typedConcat(l, tw, witness, bytes)
	} else {
		// The callee maps to the opening backtick, so Go's errors about
		// the literal's value point at it.
		open := Span{l.Start, l.Start + 1}
		if witness != "" {
			start = o.inline(open, NameRuntime+".TemplateAs", SegSpan).Start
			o.str("(" + witness + ", ")
		} else {
			start = o.inline(open, NameRuntime+".TemplateString", SegSpan).Start
			o.str("(")
		}
		for i, p := range l.Parts {
			if i > 0 {
				o.str(", ")
			}
			if p.Piece {
				// In parentheses, so ${a, b} is Go's error, as it is
				// in the typed forms.
				o.str("(")
				g.piece(p)
				o.str(")")
			} else {
				o.str(strconv.Quote(p.Text))
			}
		}
		o.str(")")
	}
	cs.Call = Span{start, len(o.b)}
	cs.Arg = cs.Call
	g.sites = append(g.sites, cs)
	return cs.Call
}

// tbMethods are the orbit__rt.TB methods of the piece writes.
var tbMethods = map[WriteKind]string{
	WriteString: "S", WriteBytes: "B", WriteInt: "Int", WriteUint: "Uint", WriteFloat: "Float",
	WriteFloat32: "Float32", WriteBool: "Bool", WriteStringer: "Stringer", WriteError: "Error",
	WriteFallback: "Any",
}

// typedConcat emits the typed form of a concatenation: a constant when
// every piece is folded, Go's + when every piece is a string, else a
// stack-buffer TB chain; converted with StringAs when Conv is "as".
func (g *cgGen) typedConcat(l *TemplateLiteral, tw TypedWrite, witness string, bytes bool) {
	o := g.o
	allConst, allStr := true, true
	for i, p := range l.Parts {
		if !p.Piece {
			continue
		}
		switch tw.Pieces[i].Kind {
		case WriteFold, WriteSkip:
		case WriteString:
			allConst = false
		default:
			allConst, allStr = false, false
		}
	}
	as := tw.Conv == "as" && witness != ""
	if as {
		o.str(NameRuntime + ".StringAs(" + witness + ", ")
	}
	// The static text runs, with folded pieces merged in.
	var run strings.Builder
	type term struct {
		text  string // a constant run, quoted
		piece int    // else the part index
	}
	var terms []term
	flush := func(force bool) {
		if run.Len() > 0 || force {
			terms = append(terms, term{text: strconv.Quote(run.String()), piece: -1})
			run.Reset()
		}
	}
	for i, p := range l.Parts {
		switch {
		case !p.Piece:
			run.WriteString(p.Text)
		case tw.Pieces[i].Kind == WriteFold:
			run.WriteString(tw.Pieces[i].Text)
		case tw.Pieces[i].Kind == WriteSkip:
		default:
			flush(false)
			terms = append(terms, term{piece: i})
		}
	}
	flush(false)
	switch {
	case allConst && !bytes:
		text := ""
		if len(terms) > 0 {
			text = terms[0].text
		} else {
			text = `""`
		}
		o.inline(l.Span, text, SegSpan)
	case allStr && !bytes:
		o.str("(")
		for k, t := range terms {
			if k > 0 {
				o.str(" + ")
			}
			if t.piece < 0 {
				o.str(t.text)
				continue
			}
			pw := tw.Pieces[t.piece]
			if pw.Conv != "" {
				o.str(pw.Conv + "(")
			}
			g.piece(l.Parts[t.piece])
			if pw.Conv != "" {
				o.str(")")
			}
		}
		if len(terms) == 0 {
			o.str(`""`)
		}
		o.str(")")
	default:
		o.str(NameRuntime + ".TB(make([]byte, 0, 64))")
		for _, t := range terms {
			if t.piece < 0 {
				o.str(".S(" + t.text + ")")
				continue
			}
			pw := tw.Pieces[t.piece]
			o.str("." + tbMethods[pw.Kind] + "(")
			if pw.Conv != "" {
				o.str(pw.Conv + "(")
			}
			g.piece(l.Parts[t.piece])
			if pw.Conv != "" {
				o.str(")")
			}
			o.str(")")
		}
		if bytes {
			o.str(".Bytes()")
		} else {
			o.str(".String()")
		}
	}
	if as {
		o.str(")")
	}
}

// tlURL is what a URL attribute's template literal needs, decided from its
// static prefix (PLAN.md §2.10).
type tlURL uint8

const (
	tlURLSafe   tlURL = iota // the prefix settles a safe scheme: no check
	tlURLCheck               // undecided: URLMark/URLCheck at run time
	tlURLUnsafe              // a javascript: or vbscript: prefix: the constant
)

// tlURLPrefix decides a URL attribute's literal from the text before its
// first piece, by isUnsafeURL's rules: leading spaces and controls
// skipped, tabs and newlines ignored, ASCII case folded.
func tlURLPrefix(prefix string) tlURL {
	i := 0
	for i < len(prefix) && (prefix[i] <= ' ' || prefix[i] == 0x7f) {
		i++
	}
	p := prefix[i:]
	js, vb := tlScheme(p, "javascript:"), tlScheme(p, "vbscript:")
	switch {
	case js == 2 || vb == 2:
		return tlURLUnsafe
	case js == 1 || vb == 1:
		return tlURLCheck
	}
	return tlURLSafe
}

// tlScheme compares u with scheme as hasSchemeFold does: 0 when they
// diverge, 1 when u is a proper prefix of it, 2 when u starts with it.
func tlScheme(u, scheme string) int {
	j := 0
	for i := 0; i < len(u); i++ {
		if j == len(scheme) {
			return 2
		}
		c := u[i]
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != scheme[j] {
			return 0
		}
		j++
	}
	if j == len(scheme) {
		return 2
	}
	return 1
}

// litWrite emits a template literal that is the whole value of an HTML
// element attribute (kind SiteAttr) or of a content expression or
// set:text (SiteContent): the static parts escaped into the Raw constants,
// each piece a SitePiece write. An attribute is always name="…"; in a URL
// attribute, the javascript:/vbscript: filter applies to the whole value.
func (g *cgGen) litWrite(kind SiteKind, node Node, a *Attr, l *TemplateLiteral) {
	o := g.o
	attr := kind == SiteAttr
	esc := textEscaper.Replace
	url := tlURLSafe
	if attr {
		esc = attrValueEscaper.Replace
		at := Span{len(o.b), len(o.b)}
		g.sites = append(g.sites, CallSite{Kind: SiteLit, Key: l.Start, Node: node, Attr: a, Code: GoCode{l.Span, g.src[l.Start:l.End]},
			AttrName: a.Name, URLAttr: IsURLAttr(a.Name), Lit: l, Call: at, Arg: at})
		o.html(" " + a.Name + `="`)
		switch {
		case !IsURLAttr(a.Name):
		case l.Const():
			if isUnsafeURL(l.Cooked()) {
				url = tlURLUnsafe // filtered at build time, as a "…" value
			}
		default:
			url = tlURLPrefix(l.Parts[0].Text)
		}
	}
	switch url {
	case tlURLUnsafe:
		// The value is the constant; the pieces are still evaluated, in
		// order.
		o.html(invalidURL + `"`)
		o.flush()
		for _, p := range l.Parts {
			if p.Piece && p.Text != "" {
				o.ind()
				o.str("_ = ")
				g.piece(p)
				o.str("\n")
			}
		}
		return
	case tlURLCheck:
		o.flush()
		o.ln("{")
		o.depth++
		o.ln("orbit__m := " + NameW + ".URLMark()")
	}
	for _, p := range l.Parts {
		if !p.Piece {
			o.html(esc(p.Text))
			continue
		}
		g.pieceSite(kind, node, a, l, p)
	}
	if url == tlURLCheck {
		o.flush()
		o.ln(NameW + ".URLCheck(orbit__m)")
		o.depth--
		o.ln("}")
	}
	if attr {
		o.html(`"`)
	}
}

// pieceSite emits one SitePiece: AttrPiece or TextPiece in pass 1, the
// typed write after, or the folded constant.
func (g *cgGen) pieceSite(kind SiteKind, node Node, a *Attr, l *TemplateLiteral, p TLPart) {
	o := g.o
	attr := kind == SiteAttr
	cs := CallSite{Kind: SitePiece, Key: p.Start, Node: node, Attr: a, Code: GoCode{p.Span, p.Text}, Lit: l, Parent: l.Start}
	if attr {
		cs.AttrName, cs.URLAttr = a.Name, IsURLAttr(a.Name)
	}
	method, conv := "TextPiece", ""
	if attr {
		method = "AttrPiece"
	}
	if tw, ok := g.in.Typed[p.Start]; ok {
		switch tw.Kind {
		case WriteSkip, WriteFold:
			if tw.Kind == WriteFold {
				if attr {
					o.html(attrValueEscaper.Replace(tw.Text))
				} else {
					o.html(textEscaper.Replace(tw.Text))
				}
			}
			at := Span{len(o.b), len(o.b)}
			cs.Call, cs.Arg = at, at
			g.sites = append(g.sites, cs)
			return
		case WriteFallback:
		default:
			method, conv = tw.Kind.Method(), tw.Conv
		}
	}
	o.flush()
	o.ind()
	start := len(o.b)
	o.str(NameW + "." + method + "(")
	if conv != "" {
		o.str(conv + "(")
	}
	o.str("\n")
	cs.Arg = g.piece(p)
	o.str(",\n")
	o.ind()
	o.str(")")
	if conv != "" {
		o.str(")")
	}
	cs.Call = Span{start, len(o.b)}
	o.str("\n")
	g.sites = append(g.sites, cs)
}

// propLiteral emits a template literal passed whole to a component prop
// (field key of the component n): TemplateAs, converted to the field's
// type.
func (g *cgGen) propLiteral(n *Component, key string, a *Attr, l *TemplateLiteral) {
	if l.Const() {
		g.o.exact(a.Code.Span) // a constant, assignable as "…" is
		return
	}
	qual := ""
	if !n.Self {
		qual = n.Name + "."
	}
	g.concat(l, qual+"Props{}."+key, false, n, a)
}

// visiblyNotString reports whether a Props field's type, as written, can't
// hold a template literal's string: a predeclared number or bool, or a
// slice, array, map, pointer, func, channel or struct type (PLAN.md §2.10,
// the pass-1 check).
func visiblyNotString(typ string) bool {
	typ = strings.TrimSpace(typ)
	switch typ {
	case "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"uintptr", "float32", "float64", "complex64", "complex128", "byte", "rune":
		return true
	}
	for _, p := range []string{"[", "*", "map[", "func(", "func (", "chan ", "chan<-", "<-chan", "struct{", "struct {"} {
		if strings.HasPrefix(typ, p) {
			return true
		}
	}
	return false
}

// propType returns the type of props' field key as written, "" if unknown.
func propType(props *PropsInfo, key string) string {
	for i := range props.Fields {
		if props.Fields[i].Name == key {
			return props.Fields[i].Type
		}
	}
	return ""
}
