package orbit

import (
	"errors"
	"strconv"
)

// Dynamic tags (PLAN.md §2.8): <Tag …>…</Tag>, where Tag is a string
// variable of the frontmatter (or of an enclosing {for}/{if}/{switch}),
// renders the element it names. Generated code brackets the attributes with
// TagOpen and Attrs, and the children with TagClose:
//
//	orbit__t1 := orbit__w.TagOpen("src/x.orbit:3:2", "Tag", Tag, true)
//	if orbit__t1.Attrs() {
//		… the attributes, then ">" …
//	}
//	… the children …
//	orbit__w.TagClose(orbit__t1)

// Fragment is the dynamic-tag value for "no element": <Wrap> with Wrap ==
// orbit.Fragment writes its children only, without its tags or attributes
// (Astro's Fragment). Compare with ==; it isn't meant to be printed.
const Fragment = "#fragment"

// For generated code. DynTag is a dynamic tag between TagOpen and
// TagClose.
type DynTag struct {
	name string
	kind dynKind
}

type dynKind uint8

const (
	dynNone    dynKind = iota // a Fragment, or an error: nothing written
	dynElement                // "<name" … "</name>"
	dynVoid                   // "<name" … with no end tag
)

// For generated code. Attrs reports whether the tag's attributes and its ">" are to be written:
// false for Fragment, and after an error.
func (t DynTag) Attrs() bool { return t.kind != dynNone }

// For generated code. TagOpen checks the dynamic tag name and writes
// "<name". pos is the .orbit position of the tag and expr the variable as
// written (Tag), for errors; content is set when the template gives the
// tag children (or set:html, set:text).
//
// A name must be an ASCII letter followed by ASCII letters, digits, -, _,
// . and :. Fragment writes nothing. The empty string, an invalid name, the
// raw-text and RCDATA elements (script, style, textarea, title, xmp,
// iframe, noembed, noframes, plaintext) and head in any case, and a void
// element (img, br, …) with content are render errors, and nothing is
// written.
func (w *Writer) TagOpen(pos, expr, name string, content bool) DynTag {
	if w.err != nil || name == Fragment {
		return DynTag{}
	}
	// One pass: check the name and lowercase its first bytes for the
	// switch below (the longest special name, plaintext, has 9).
	var low [10]byte
	ok := name != ""
	for i := 0; ok && i < len(name); i++ {
		c := name[i]
		switch {
		case 'a' <= c && c <= 'z':
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		case i > 0 && ('0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':'):
		default:
			ok = false
		}
		if i < len(low) {
			low[i] = c
		}
	}
	if !ok {
		w.tagError(pos, expr, name, tagInvalid)
		return DynTag{}
	}
	k := dynElement
	if len(name) < len(low) {
		switch string(low[:len(name)]) {
		case "script", "style", "textarea", "title", "head", "xmp", "iframe", "noembed", "noframes", "plaintext":
			w.tagError(pos, expr, name, tagRejected)
			return DynTag{}
		case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr":
			if content {
				w.tagError(pos, expr, name, tagVoidContent)
				return DynTag{}
			}
			k = dynVoid
		}
	}
	w.buf.WriteByte('<')
	w.buf.WriteString(name)
	return DynTag{name, k}
}

// For generated code. TagClose writes "</name>" for a dynamic tag that
// TagOpen opened as a non-void element, and nothing otherwise.
func (w *Writer) TagClose(t DynTag) {
	if w.err != nil || t.kind != dynElement {
		return
	}
	w.buf.WriteString("</")
	w.buf.WriteString(t.name)
	w.buf.WriteByte('>')
}

type tagProblem uint8

const (
	tagInvalid tagProblem = iota
	tagRejected
	tagVoidContent
)

// tagError sets the render error of a dynamic tag. The messages are built
// by concatenation, never with fmt: a name passed to fmt would escape, and
// a name built by concatenation ("h" + strconv.Itoa(level)) would then
// allocate on every TagOpen (TestDynTagAllocs).
//
//go:noinline
func (w *Writer) tagError(pos, expr, name string, p tagProblem) {
	var msg string
	switch {
	case name == "":
		msg = "dynamic tag " + expr + " is empty; use orbit.Fragment for no element"
	case p == tagRejected:
		msg = "<" + name + "> can't be a dynamic tag"
	case p == tagVoidContent:
		msg = "dynamic tag <" + name + "> is a void element and can't have children"
	default:
		msg = "dynamic tag " + expr + " has an invalid element name " + strconv.Quote(name)
	}
	w.setErr(posErr(pos, msg))
}

// posErr is a render error with the .orbit position pos ("src/x.orbit:3:2:
// msg"), or "orbit: msg" without one.
func posErr(pos, msg string) error {
	if pos == "" {
		return errors.New("orbit: " + msg)
	}
	return &posError{pos: pos, err: errors.New(msg)}
}
