package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// dumpFile renders a File as a readable, stable tree for golden files.
// Spans print as @start-end byte offsets into the whole file.
func dumpFile(f *File) string {
	var b strings.Builder
	if fm := f.Frontmatter; fm.Present {
		fmt.Fprintf(&b, "Frontmatter %s closed=%v open=%s close=%s code=%s %s\n",
			sp(fm.Span), fm.Closed, sp(fm.Open), sp(fm.Close), sp(fm.Code.Span), q(fm.Code.Text))
	}
	dumpNode(&b, f.Template, 0)
	return b.String()
}

func sp(s Span) string { return fmt.Sprintf("@%d-%d", s.Start, s.End) }

func q(s string) string {
	const max = 60
	if len(s) > max {
		s = s[:max] + "…"
	}
	return strconv.Quote(s)
}

func code(c GoCode) string { return sp(c.Span) + " " + q(c.Text) }

func dumpNode(b *strings.Builder, n Node, depth int) {
	ind := strings.Repeat("  ", depth)
	switch n := n.(type) {
	case *Template:
		fmt.Fprintf(b, "%sTemplate %s\n", ind, sp(n.Span))
	case *Text:
		fmt.Fprintf(b, "%sText %s %s\n", ind, sp(n.Span), q(n.Value))
	case *Expr:
		fmt.Fprintf(b, "%sExpr %s code=%s\n", ind, sp(n.Span), code(n.Code))
	case *Comment:
		fmt.Fprintf(b, "%sComment %s body=%s %s\n", ind, sp(n.Span), sp(n.Body.Span), q(n.Body.Text))
	case *Doctype:
		fmt.Fprintf(b, "%sDoctype %s body=%s %s\n", ind, sp(n.Span), sp(n.Body.Span), q(n.Body.Text))
	case *HTMLComment:
		fmt.Fprintf(b, "%sHTMLComment %s body=%s %s\n", ind, sp(n.Span), sp(n.Body.Span), q(n.Body.Text))
	case *If:
		fmt.Fprintf(b, "%sIf %s end=%s\n", ind, sp(n.Span), sp(n.EndTag))
		for i, br := range n.Branches {
			kind := "if"
			switch {
			case br.Else:
				kind = "else"
			case i > 0:
				kind = "else-if"
			}
			fmt.Fprintf(b, "%s  Branch %s %s header=%s cond=%s\n", ind, kind, sp(br.Span), sp(br.Header), code(br.Cond))
			for _, c := range br.Children {
				dumpNode(b, c, depth+2)
			}
		}
		return
	case *For:
		fmt.Fprintf(b, "%sFor %s header=%s clause=%s end=%s\n", ind, sp(n.Span), sp(n.Header), code(n.Clause), sp(n.EndTag))
	case *Element:
		flags := ""
		if n.Void {
			flags += " void"
		}
		if n.RCDATA {
			flags += " rcdata"
		}
		fmt.Fprintf(b, "%sElement %s%s\n", ind, sp(n.Span), flags)
		dumpTag(b, &n.Tag, depth+1)
	case *Component:
		flags := ""
		if n.Self {
			flags += " self"
		}
		if n.Defer != nil {
			flags += " defer=" + sp(n.Defer.Span)
		}
		if n.Client != nil {
			flags += " client=" + n.Client.Directive.String()
		}
		fmt.Fprintf(b, "%sComponent %s%s\n", ind, sp(n.Span), flags)
		dumpTag(b, &n.Tag, depth+1)
	case *Slot:
		name := "default"
		if n.NameAttr != nil {
			name = "name=" + sp(n.NameAttr.Span)
		}
		fmt.Fprintf(b, "%sSlot %s %s\n", ind, sp(n.Span), name)
		dumpTag(b, &n.Tag, depth+1)
	case *Fragment:
		fmt.Fprintf(b, "%sFragment %s\n", ind, sp(n.Span))
		dumpTag(b, &n.Tag, depth+1)
	case *Script:
		fmt.Fprintf(b, "%sScript %s processed=%v inline=%v json=%v content=%s %s\n", ind, sp(n.Span),
			n.Processed, n.Inline, n.JSON, sp(n.Content.Span), q(n.Content.Text))
		dumpTag(b, &n.Tag, depth+1)
	case *Style:
		fmt.Fprintf(b, "%sStyle %s processed=%v inline=%v global=%v content=%s %s\n", ind, sp(n.Span),
			n.Processed, n.Inline, n.Global, sp(n.Content.Span), q(n.Content.Text))
		dumpTag(b, &n.Tag, depth+1)
	default:
		fmt.Fprintf(b, "%s%T\n", ind, n)
	}
	for _, c := range ChildNodes(n) {
		dumpNode(b, c, depth+1)
	}
}

func dumpTag(b *strings.Builder, t *Tag, depth int) {
	ind := strings.Repeat("  ", depth)
	sc := ""
	if t.SelfClosing {
		sc = " selfclosing"
	}
	fmt.Fprintf(b, "%s<%s> name=%s start=%s end=%s%s\n", ind, t.Name, sp(t.NameSpan), sp(t.StartTag), sp(t.EndTag), sc)
	for _, a := range t.Attrs {
		fmt.Fprintf(b, "%s  attr %s %s", ind, a.Kind, sp(a.Span))
		if a.Kind != AttrSpread {
			fmt.Fprintf(b, " name=%s %s", sp(a.NameSpan), q(a.Name))
		}
		if a.Directive != NotDirective {
			fmt.Fprintf(b, " directive=%s", a.Directive)
		}
		switch {
		case a.IsStatic():
			fmt.Fprintf(b, " value=%s %s", sp(a.ValueSpan), q(a.Value))
		case a.Kind != AttrEmpty:
			fmt.Fprintf(b, " value=%s code=%s", sp(a.ValueSpan), code(a.Code))
		}
		b.WriteByte('\n')
	}
}

func dumpDiags(f *File, name string) string {
	var b strings.Builder
	for _, d := range f.Diagnostics {
		b.WriteString(d.Format(name, f.Lines))
		b.WriteByte('\n')
	}
	return b.String()
}

// checkInvariants verifies the AST's position invariants: every span is in
// the file, children are inside their parent and in order, sub-spans are
// inside their node, and every copied text equals its span's source.
func checkInvariants(t testing.TB, f *File) {
	t.Helper()
	src := f.Src
	inFile := func(what string, s Span) {
		if s.Start < 0 || s.Start > s.End || s.End > len(src) {
			t.Fatalf("%s %v outside the file (len %d)", what, s, len(src))
		}
	}
	inside := func(what string, inner, outer Span) {
		inFile(what, inner)
		if inner.Start < outer.Start || inner.End > outer.End {
			t.Fatalf("%s %v not inside %v", what, inner, outer)
		}
	}
	text := func(what string, s Span, got string) {
		inFile(what, s)
		if src[s.Start:s.End] != got {
			t.Fatalf("%s %v: text %q != source %q", what, s, got, src[s.Start:s.End])
		}
	}
	for _, d := range f.Diagnostics {
		inFile("diagnostic "+d.Message, d.Span)
	}
	if fm := f.Frontmatter; fm.Present {
		inside("frontmatter code", fm.Code.Span, fm.Span)
		text("frontmatter code", fm.Code.Span, fm.Code.Text)
		if fm.Span.End > f.Template.Start {
			t.Fatalf("frontmatter %v overlaps template %v", fm.Span, f.Template.Span)
		}
	}
	var walk func(n Node)
	walk = func(n Node) {
		s := n.Bounds()
		inFile(fmt.Sprintf("%T", n), s)
		switch n := n.(type) {
		case *Text:
			text("text", n.Span, n.Value)
		case *Expr:
			inside("expr code", n.Code.Span, s)
			text("expr code", n.Code.Span, n.Code.Text)
		case *Comment:
			inside("comment body", n.Body.Span, s)
			text("comment body", n.Body.Span, n.Body.Text)
		case *Doctype:
			inside("doctype body", n.Body.Span, s)
			text("doctype body", n.Body.Span, n.Body.Text)
		case *HTMLComment:
			inside("comment body", n.Body.Span, s)
			text("comment body", n.Body.Span, n.Body.Text)
		case *If:
			if n.EndTag.Len() > 0 {
				inside("{/if}", n.EndTag, s)
			}
		case *Branch:
			inside("branch header", n.Header, s)
			inside("branch cond", n.Cond.Span, n.Header)
			text("branch cond", n.Cond.Span, n.Cond.Text)
		case *For:
			inside("for header", n.Header, s)
			inside("for clause", n.Clause.Span, n.Header)
			text("for clause", n.Clause.Span, n.Clause.Text)
			if n.EndTag.Len() > 0 {
				inside("{/for}", n.EndTag, s)
			}
		case *Script:
			inside("script content", n.Content.Span, s)
			text("script content", n.Content.Span, n.Content.Text)
		case *Style:
			inside("style content", n.Content.Span, s)
			text("style content", n.Content.Span, n.Content.Text)
		}
		if tag := TagOf(n); tag != nil {
			inside("start tag", tag.StartTag, s)
			inside("tag name", tag.NameSpan, tag.StartTag)
			text("tag name", tag.NameSpan, tag.Name)
			if tag.EndTag.Len() > 0 {
				inside("end tag", tag.EndTag, s)
				if tag.EndTag.Start < tag.StartTag.End {
					t.Fatalf("end tag %v before start tag end %v", tag.EndTag, tag.StartTag)
				}
			}
			prev := tag.NameSpan.End
			for _, a := range tag.Attrs {
				inside("attr", a.Span, tag.StartTag)
				if a.Start < prev {
					t.Fatalf("attr %v overlaps the previous one (ends %d)", a.Span, prev)
				}
				prev = a.End
				if a.Kind != AttrSpread {
					inside("attr name", a.NameSpan, a.Span)
					text("attr name", a.NameSpan, a.Name)
				}
				if a.Kind != AttrEmpty {
					inside("attr value", a.ValueSpan, a.Span)
				}
				if a.IsStatic() {
					text("attr value", a.ValueSpan, a.Value)
				}
				if a.IsExpr() || a.Kind == AttrSpread {
					inside("attr code", a.Code.Span, a.ValueSpan)
					text("attr code", a.Code.Span, a.Code.Text)
				}
			}
		}
		prev := s.Start
		if tag := TagOf(n); tag != nil {
			prev = tag.StartTag.End
		}
		for _, c := range ChildNodes(n) {
			cs := c.Bounds()
			inside(fmt.Sprintf("child %T", c), cs, s)
			if cs.Start < prev {
				t.Fatalf("child %T %v overlaps its previous sibling or start tag (ends %d)", c, cs, prev)
			}
			prev = cs.End
			walk(c)
		}
		if tag := TagOf(n); tag != nil && tag.EndTag.Len() > 0 && prev > tag.EndTag.Start {
			t.Fatalf("children of <%s> end at %d, after the end tag %v", tag.Name, prev, tag.EndTag)
		}
	}
	walk(f.Template)
}
