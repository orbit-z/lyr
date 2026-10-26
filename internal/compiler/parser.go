package compiler

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Mode selects how the parser is used.
type Mode uint8

const (
	// Strict is for builds: every structural error is collected (parsing
	// doesn't stop at the first), and a file with errors isn't compiled.
	Strict Mode = iota
	// Tolerant is for the LSP: the parse never fails and always yields an AST
	// for completion and navigation. Diagnostics are still returned.
	Tolerant
)

// Both modes recover the same way, so a strict build reports every error at
// once and the LSP shows the same diagnostics as the build:
//   - an unclosed tag or block closes at the end of the file;
//   - an unterminated { ends at the end of its line;
//   - a closing tag or block that skips open ones closes them where it is;
//   - a stray closer is dropped.

// Parse parses a whole .orbit file: the frontmatter split, then the
// template, then the structural checks that need no frontmatter or types
// (tag nesting, directive placement and values, Fragment attributes, the
// on*/style warnings). It never panics and always returns a File; errors are
// in File.Diagnostics.
func Parse(src string, mode Mode) *File {
	f := &File{Src: src, Mode: mode, Lines: NewLineIndex(src)}
	fm, tmpl, diags := SplitFrontmatter(src)
	f.Frontmatter = fm
	p := &parser{
		src:   src,
		lines: f.Lines,
		diags: diags,
	}
	p.lx = lexer{src: src, pos: tmpl.Start, diags: &p.diags}
	f.Template = p.parseTemplate(tmpl)
	for _, d := range p.diags {
		if d.Severity == SeverityError {
			f.syntax = append(f.syntax, d)
		}
	}
	sortDiagnostics(f.syntax)
	validate(f.Template, p)
	sortDiagnostics(p.diags)
	f.Diagnostics = p.diags
	return f
}

type frameKind uint8

const (
	frameRoot frameKind = iota
	frameTag
	frameIf
	frameFor
)

// frame is an open tag or block on the parser's stack.
type frame struct {
	kind frameKind
	name string // tag name
	open Span   // start tag or block header
	node Node
	kids *[]Node
	// For an If: the {else} header once seen.
	elseAt Span
}

func (f *frame) label() string {
	switch f.kind {
	case frameIf:
		return "{if}"
	case frameFor:
		return "{for}"
	}
	return "<" + f.name + ">"
}

func (f *frame) closer() string {
	switch f.kind {
	case frameIf:
		return "{/if}"
	case frameFor:
		return "{/for}"
	}
	return "</" + f.name + ">"
}

type parser struct {
	src   string
	lines *LineIndex
	lx    lexer
	diags []Diagnostic
	stack []*frame
}

func (p *parser) errorf(s Span, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{SeverityError, s, fmt.Sprintf(format, args...)})
}

func (p *parser) warnf(s Span, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{SeverityWarning, s, fmt.Sprintf(format, args...)})
}

func (p *parser) at(off int) Position { return p.lines.Position(off) }

func (p *parser) top() *frame { return p.stack[len(p.stack)-1] }

func (p *parser) add(n Node) {
	k := p.top().kids
	*k = append(*k, n)
}

func (p *parser) parseTemplate(s Span) *Template {
	t := &Template{Span: s}
	p.stack = []*frame{{kind: frameRoot, node: t, kids: &t.Children}}
	for {
		tok := p.lx.next()
		if tok.kind == tokEOF {
			break
		}
		p.token(tok)
	}
	for len(p.stack) > 1 {
		f := p.top()
		p.errorf(f.open, "%s isn't closed; add %s", f.label(), f.closer())
		p.closeTop(len(p.src), Span{len(p.src), len(p.src)})
	}
	return t
}

func (p *parser) token(tok token) {
	switch tok.kind {
	case tokText:
		p.add(&Text{Span: tok.span, Value: p.src[tok.span.Start:tok.span.End]})
	case tokExpr:
		p.add(&Expr{Span: tok.span, Code: tok.code})
	case tokComment:
		p.add(&Comment{Span: tok.span, Body: tok.body})
	case tokDoctype:
		p.add(&Doctype{Span: tok.span, Body: tok.body})
	case tokHTMLComment:
		p.add(&HTMLComment{Span: tok.span, Body: tok.body})
	case tokIf:
		br := &Branch{Span: tok.span, Header: tok.span, Cond: tok.code}
		n := &If{Span: tok.span, Branches: []*Branch{br}}
		p.add(n)
		p.stack = append(p.stack, &frame{kind: frameIf, open: tok.span, node: n, kids: &br.Children})
	case tokFor:
		n := &For{Span: tok.span, Header: tok.span, Clause: tok.code}
		p.add(n)
		p.stack = append(p.stack, &frame{kind: frameFor, open: tok.span, node: n, kids: &n.Children})
	case tokElseIf, tokElse:
		p.elseBranch(tok)
	case tokEndIf:
		p.closer(tok, frameIf, "", "{/if}")
	case tokEndFor:
		p.closer(tok, frameFor, "", "{/for}")
	case tokStartTag:
		p.startTag(tok)
	case tokEndTag:
		if voidElements[tok.name] {
			p.errorf(tok.span, "unexpected </%s>: <%s> is a void element and takes no end tag", tok.name, tok.name)
			return
		}
		p.closer(tok, frameTag, tok.name, "</"+tok.name+">")
	case tokRaw:
		switch n := p.top().node.(type) {
		case *Script:
			n.Content = tok.body
			n.End = tok.span.End
		case *Style:
			n.Content = tok.body
			n.End = tok.span.End
		}
	}
}

// find returns the stack index of the innermost frame of kind (and name, for
// tags), or -1.
func (p *parser) find(kind frameKind, name string) int {
	for i := len(p.stack) - 1; i > 0; i-- {
		if f := p.stack[i]; f.kind == kind && (kind != frameTag || f.name == name) {
			return i
		}
	}
	return -1
}

// closer handles an end tag, {/if} or {/for}.
func (p *parser) closer(tok token, kind frameKind, name, text string) {
	i := p.find(kind, name)
	if i < 0 {
		what := "<" + name + ">"
		switch kind {
		case frameIf:
			what = "{if}"
		case frameFor:
			what = "{for}"
		}
		p.errorf(tok.span, "unexpected %s: no %s is open", text, what)
		return
	}
	if i != len(p.stack)-1 {
		f := p.top()
		p.errorf(tok.span, "unexpected %s: %s opened at %s isn't closed", text, f.label(), p.at(f.open.Start))
		p.closeTo(i, tok.span.Start)
	}
	p.closeTop(tok.span.End, tok.span)
}

func (p *parser) elseBranch(tok token) {
	text := "{else}"
	if tok.kind == tokElseIf {
		text = "{else if}"
	}
	i := p.find(frameIf, "")
	if i < 0 {
		p.errorf(tok.span, "unexpected %s: no {if} is open", text)
		return
	}
	if i != len(p.stack)-1 {
		f := p.top()
		p.errorf(tok.span, "unexpected %s: %s opened at %s isn't closed", text, f.label(), p.at(f.open.Start))
		p.closeTo(i, tok.span.Start)
	}
	f := p.top()
	n := f.node.(*If)
	if f.elseAt.End > 0 {
		p.errorf(tok.span, "unexpected %s: the {else} at %s must be the last branch", text, p.at(f.elseAt.Start))
	}
	last := n.Branches[len(n.Branches)-1]
	last.End = tok.span.Start
	br := &Branch{Span: tok.span, Header: tok.span, Cond: tok.code, Else: tok.kind == tokElse}
	if br.Else {
		f.elseAt = tok.span
	}
	n.Branches = append(n.Branches, br)
	f.kids = &br.Children
}

// closeTo closes every frame above stack index i at offset at, without end
// tags (the caller has reported the error).
func (p *parser) closeTo(i, at int) {
	for len(p.stack)-1 > i {
		p.closeTop(at, Span{at, at})
	}
}

// closeTop pops the top frame, ending its node at end with endTag.
func (p *parser) closeTop(end int, endTag Span) {
	f := p.top()
	p.stack = p.stack[:len(p.stack)-1]
	switch n := f.node.(type) {
	case *Element:
		n.End, n.EndTag = end, endTag
		if n.RCDATA {
			p.lx.rcdata = ""
		}
	case *Component:
		n.End, n.EndTag = end, endTag
	case *Slot:
		n.End, n.EndTag = end, endTag
	case *Fragment:
		n.End, n.EndTag = end, endTag
	case *Script:
		n.End, n.EndTag = end, endTag
		p.lx.rawTag = ""
	case *Style:
		n.End, n.EndTag = end, endTag
		p.lx.rawTag = ""
	case *If:
		n.End, n.EndTag = end, endTag
		n.Branches[len(n.Branches)-1].End = endTag.Start
	case *For:
		n.End, n.EndTag = end, endTag
	}
}

func (p *parser) startTag(tok token) {
	tag := Tag{Name: tok.name, NameSpan: tok.nameSpan, Attrs: tok.attrs, StartTag: tok.span,
		SelfClosing: tok.selfClosing, EndTag: Span{tok.span.End, tok.span.End}}
	sp := tok.span
	var n Node
	var kids *[]Node
	switch name := tok.name; {
	case name == "script":
		n = &Script{Span: sp, Tag: tag, Content: RawText{Span: Span{sp.End, sp.End}}}
		if !tag.SelfClosing {
			p.lx.rawTag = name
		}
	case name == "style":
		n = &Style{Span: sp, Tag: tag, Content: RawText{Span: Span{sp.End, sp.End}}}
		if !tag.SelfClosing {
			p.lx.rawTag = name
		}
	case name == "slot":
		s := &Slot{Span: sp, Tag: tag}
		n, kids = s, &s.Children
	case name == "Fragment":
		s := &Fragment{Span: sp, Tag: tag}
		n, kids = s, &s.Children
	case isComponentName(name):
		if !isIdent(name) {
			p.errorf(tok.nameSpan, "invalid component name <%s>: a component tag is an import alias such as <Card>", name)
		}
		c := &Component{Span: sp, Tag: tag, Self: name == "Self"}
		n, kids = c, &c.Children
	default:
		e := &Element{Span: sp, Tag: tag, Void: voidElements[name], RCDATA: name == "title" || name == "textarea"}
		n, kids = e, &e.Children
		if e.Void {
			p.add(n)
			return
		}
		if e.RCDATA && !tag.SelfClosing {
			p.lx.rcdata = name
		}
	}
	p.add(n)
	if tag.SelfClosing {
		return
	}
	if kids == nil {
		var none []Node
		kids = &none // script and style have raw content, never children
	}
	p.stack = append(p.stack, &frame{kind: frameTag, name: tok.name, open: tok.span, node: n, kids: kids})
}

// isComponentName reports whether a tag name is a component: capitalized
// and without a "-" (a custom element such as <My-widget> stays HTML).
func isComponentName(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	if !unicode.IsUpper(r) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '-' {
			return false
		}
	}
	return true
}
