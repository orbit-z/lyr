package compiler

// This file defines the template AST. Every node embeds a Span with byte
// offsets into the original .orbit file (frontmatter included), and so do the
// sub-parts that codegen copies into Go (expressions, control headers, tag
// and attribute names) and that the LSP classifies cursors by.
//
// Invariants (checked by FuzzParse): every span is inside the file with
// Start <= End, a node's children are inside it and don't overlap, in source
// order, and every GoCode/RawText's Text is exactly src[Start:End].

// Node is a template AST node.
type Node interface {
	Bounds() Span
	node()
}

// GoCode is a piece of Go source copied verbatim from the template: an
// expression, a control header or a spread operand. Text is src[Start:End],
// with the surrounding whitespace trimmed. An empty header (as the user types
// {if |}) is a zero-length span where the code would start.
type GoCode struct {
	Span
	Text string
}

// RawText is a piece of the file passed through as is: script and style
// content, HTML comment and doctype bodies, template comment bodies.
// Text is src[Start:End].
type RawText struct {
	Span
	Text string
}

// File is the result of parsing a whole .orbit file.
type File struct {
	Src         string
	Mode        Mode
	Frontmatter Frontmatter
	Template    *Template
	// Diagnostics holds every error and warning, sorted by position.
	Diagnostics []Diagnostic
	Lines       *LineIndex
	// syntax holds the errors raised before the structural checks, sorted.
	syntax []Diagnostic
}

// HasErrors reports whether any diagnostic is an error (not a warning).
func (f *File) HasErrors() bool {
	for _, d := range f.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Frontmatter is the Go code between the --- lines at the top of the file.
type Frontmatter struct {
	// Span covers both delimiter lines and the code. It is empty (and Present
	// false) when the file has no frontmatter.
	Span
	Present bool
	// Closed is false when the closing --- line is missing; the frontmatter
	// then runs to the end of the file.
	Closed bool
	// Open and Close are the two delimiter lines, without their line breaks.
	Open, Close Span
	// Code is the Go source between the delimiter lines.
	Code GoCode
}

// Template is the root of the template AST.
type Template struct {
	Span
	Children []Node
}

// Text is literal template text, emitted verbatim. Value is src[Start:End].
type Text struct {
	Span
	Value string
}

// Expr is {expr}: a Go expression whose value is escaped and written. Code is
// empty for {} (or { }), which renders nothing, as an empty JSX expression
// does in Astro; the LSP sees it as a Go region to complete in.
type Expr struct {
	Span
	Code GoCode
}

// Comment is a template comment, {/* … */}. It emits nothing.
type Comment struct {
	Span
	// Body is the text between /* and */.
	Body RawText
}

// If is an {if c}…{else if c}…{else}…{/if} chain.
type If struct {
	Span
	// Branches has the {if} branch first, then each {else if}, then the
	// {else} branch if there is one (always last).
	Branches []*Branch
	// EndTag is the {/if}; empty when it is missing (unclosed or recovered).
	EndTag Span
}

// Branch is one branch of an If: from its header to the next header or the
// {/if}.
type Branch struct {
	Span
	// Header is the whole {if c}, {else if c} or {else}.
	Header Span
	// Else is true for the final {else} branch, which has no condition.
	Else bool
	// Cond is the condition, which may include an init statement
	// ({if n, ok := m[k]; ok}). Empty for {else}.
	Cond     GoCode
	Children []Node
}

// For is a {for clause}…{/for} loop.
type For struct {
	Span
	// Header is the whole {for clause}.
	Header Span
	// Clause is the Go for clause (range, three-clause or condition).
	Clause   GoCode
	Children []Node
	// EndTag is the {/for}; empty when it is missing.
	EndTag Span
}

// Switch is a {switch clause}…{case list}…{default}…{/switch} block,
// compiled to a Go switch statement.
type Switch struct {
	Span
	// Header is the whole {switch clause}.
	Header Span
	// Clause is the Go switch header, copied verbatim: a tag, an init
	// statement and a tag, a type-switch guard, or empty for a tagless
	// switch.
	Clause GoCode
	// Pre is what comes between the header and the first case. Only
	// whitespace text and template comments are allowed there (anything else
	// is reported, and kept for the tolerant AST); it renders nothing.
	Pre   []Node
	Cases []*Case
	// EndTag is the {/switch}; empty when it is missing.
	EndTag Span
}

// Case is one {case list} or {default} of a Switch: from its header to the
// next case or the {/switch}.
type Case struct {
	Span
	// Header is the whole {case list} or {default}.
	Header Span
	// Default is true for {default}, which has no list.
	Default bool
	// List is the Go case list: expressions, or types in a type switch.
	// Empty for {default}.
	List     GoCode
	Children []Node
}

// Tag holds what every tag-shaped node has in common.
type Tag struct {
	// Name is the tag name as written; NameSpan is its position after "<".
	Name     string
	NameSpan Span
	Attrs    []*Attr
	// StartTag is the whole start tag, "<div …>" or "<div …/>".
	StartTag Span
	// EndTag is "</div>". It is empty for a self-closing or void element and
	// for an unclosed one (whose diagnostic is reported).
	EndTag      Span
	SelfClosing bool
}

// Attr returns the first attribute named name, or nil.
func (t *Tag) Attr(name string) *Attr {
	for _, a := range t.Attrs {
		if a.Kind != AttrSpread && a.Name == name {
			return a
		}
	}
	return nil
}

// Element is an HTML or custom element: a tag that is lowercase or contains
// "-". <script> and <style> are Script and Style instead.
type Element struct {
	Span
	Tag
	// Void is true for void elements (br, img, …), which have no children and
	// no end tag.
	Void bool
	// RCDATA is true for <title> and <textarea>: their children are only Text,
	// Expr, Comment, If and For; markup inside them is text.
	RCDATA   bool
	Children []Node
}

// Component is a capitalized tag: a call of an imported .orbit component, or
// of the current one for <Self>.
type Component struct {
	Span
	Tag
	// Self is true for <Self>.
	Self bool
	// Defer is the server:defer attribute, nil without one.
	Defer *Attr
	// Client is the client:* attribute (the first one if several were
	// written, which is an error), nil without one.
	Client *Attr
	// Children are the slot contents.
	Children []Node
}

// Slot is a slot outlet, <slot> or <slot name="x">, with fallback children.
type Slot struct {
	Span
	Tag
	// NameAttr is the name attribute, nil for the default slot.
	NameAttr *Attr
	// Children are the fallback content.
	Children []Node
}

// Fragment is <Fragment>: children without a wrapper element.
type Fragment struct {
	Span
	Tag
	Children []Node
}

// Script is a <script> element. Its content is opaque.
type Script struct {
	Span
	Tag
	Content RawText
	// Inline is true with is:inline.
	Inline bool
	// JSON is true with set:json (a <script type="application/json">).
	JSON bool
	// Processed is true when Orbit compiles and bundles the script: Astro's
	// rule, no attributes at all or only a src with a relative local path
	// ("./x.ts", "../x.ts"). Any other attribute leaves it as is.
	Processed bool
}

// Style is a <style> element. Its content is opaque.
type Style struct {
	Span
	Tag
	Content RawText
	// Inline is true with is:inline: emitted where it is, unscoped and
	// unbundled.
	Inline bool
	// Global is true with is:global: bundled but not scoped.
	Global bool
	// Processed is true unless the style is is:inline.
	Processed bool
}

// Doctype is a <!…> declaration other than a comment, such as
// <!DOCTYPE html>. It is passed through.
type Doctype struct {
	Span
	// Body is the text between "<!" and ">".
	Body RawText
}

// HTMLComment is <!-- … -->, passed through. {…} inside isn't evaluated.
type HTMLComment struct {
	Span
	// Body is the text between "<!--" and "-->".
	Body RawText
}

func (*Template) node()    {}
func (*Text) node()        {}
func (*Expr) node()        {}
func (*Comment) node()     {}
func (*If) node()          {}
func (*Branch) node()      {}
func (*For) node()         {}
func (*Switch) node()      {}
func (*Case) node()        {}
func (*Element) node()     {}
func (*Component) node()   {}
func (*Slot) node()        {}
func (*Fragment) node()    {}
func (*Script) node()      {}
func (*Style) node()       {}
func (*Doctype) node()     {}
func (*HTMLComment) node() {}

// AttrKind is the syntactic form of an attribute.
type AttrKind uint8

const (
	// AttrEmpty is a bare attribute: a.
	AttrEmpty AttrKind = iota
	// AttrQuoted is a="s" or a='s'.
	AttrQuoted
	// AttrUnquoted is a=bare.
	AttrUnquoted
	// AttrExpr is a={expr}.
	AttrExpr
	// AttrShorthand is {a}, short for a={a}.
	AttrShorthand
	// AttrSpread is {...attrs}.
	AttrSpread
)

var attrKindNames = [...]string{"empty", "quoted", "unquoted", "expr", "shorthand", "spread"}

func (k AttrKind) String() string { return attrKindNames[k] }

// Attr is one attribute of a start tag.
type Attr struct {
	// Span covers the whole attribute as written.
	Span
	Kind AttrKind
	// Name is the attribute name as written (for {a}, the identifier a).
	// Empty for a spread.
	Name     string
	NameSpan Span
	// Value is the static value of a quoted or unquoted attribute, without
	// quotes and undecoded (entities are kept as written).
	Value string
	// ValueSpan is the value's position: inside the quotes for a quoted value,
	// the braces included for {expr}. Empty for a bare attribute.
	ValueSpan Span
	// Code is the Go code of an expr, shorthand or spread attribute.
	Code GoCode
	// Directive classifies the name; NotDirective for plain attributes.
	Directive Directive
	// Classes are the items of a class:list={…} value, in source order;
	// nil for other attributes, and when the value doesn't parse
	// (reported).
	Classes []ClassItem
}

// ClassItem is one item of a class:list value (PLAN.md §2.3), parsed as an
// element of a Go composite literal: "a b", expr, or "c": cond.
type ClassItem struct {
	// Span covers the item as written, a keyed item's condition included.
	Span
	// Code is the class expression: the item, or a keyed item's key.
	Code GoCode
	// Cond is a keyed item's condition; Cond.Text is "" for an unkeyed
	// item.
	Cond GoCode
	// Static is set when Code is a Go string literal; Value is its value.
	Static bool
	Value  string
}

// IsStatic reports whether the attribute has a static (quoted or unquoted)
// value.
func (a *Attr) IsStatic() bool { return a.Kind == AttrQuoted || a.Kind == AttrUnquoted }

// IsExpr reports whether the attribute's value is Go code (a={expr} or {a}).
func (a *Attr) IsExpr() bool { return a.Kind == AttrExpr || a.Kind == AttrShorthand }

// Directive is what Orbit makes of an attribute name.
type Directive uint8

const (
	// NotDirective is a plain attribute (or a prop, on a component).
	NotDirective  Directive = iota
	SetHTML                 // set:html
	SetText                 // set:text
	SetJSON                 // set:json
	SlotDirective           // slot="name"
	IsInline                // is:inline
	IsGlobal                // is:global
	ServerDefer             // server:defer
	ClientLoad              // client:load
	ClientIdle              // client:idle
	ClientVisible           // client:visible
	ClientMedia             // client:media
	ClassList               // class:list
	DefineVars              // define:vars (Astro-only, an error)
	// UnknownDirective is an unknown name in a reserved namespace (set:, is:,
	// server:, client:), client:only included. It is always an error.
	UnknownDirective
)

var directiveNames = [...]string{"", "set:html", "set:text", "set:json", "slot", "is:inline", "is:global",
	"server:defer", "client:load", "client:idle", "client:visible", "client:media", "class:list",
	"define:vars", "unknown"}

func (d Directive) String() string { return directiveNames[d] }

// IsClient reports whether d is one of the client:* loading directives.
func (d Directive) IsClient() bool { return d >= ClientLoad && d <= ClientMedia }

// IsLazy reports whether d is a lazy loading directive: client:idle,
// client:visible or client:media (client:load is eager).
func (d Directive) IsLazy() bool { return d >= ClientIdle && d <= ClientMedia }

// IsSet reports whether d is set:html, set:text or set:json.
func (d Directive) IsSet() bool { return d >= SetHTML && d <= SetJSON }

// classifyDirective maps an attribute name to its directive.
func classifyDirective(name string) Directive {
	switch name {
	case "slot":
		return SlotDirective
	case "set:html":
		return SetHTML
	case "set:text":
		return SetText
	case "set:json":
		return SetJSON
	case "is:inline":
		return IsInline
	case "is:global":
		return IsGlobal
	case "server:defer":
		return ServerDefer
	case "client:load":
		return ClientLoad
	case "client:idle":
		return ClientIdle
	case "client:visible":
		return ClientVisible
	case "client:media":
		return ClientMedia
	case "class:list":
		return ClassList
	case "define:vars":
		return DefineVars
	}
	if reservedNamespace(name) != "" {
		return UnknownDirective
	}
	return NotDirective
}

// reservedNamespace returns the reserved namespace of name ("client", …), or
// "" for a plain attribute name.
func reservedNamespace(name string) string {
	for _, ns := range [...]string{"set", "is", "server", "client"} {
		if len(name) > len(ns) && name[len(ns)] == ':' && name[:len(ns)] == ns {
			return ns
		}
	}
	return ""
}

// ChildNodes returns the direct children of n, in source order. For an If
// these are its branches; for a Switch, its Pre nodes, then its cases.
func ChildNodes(n Node) []Node {
	switch n := n.(type) {
	case *Template:
		return n.Children
	case *If:
		out := make([]Node, len(n.Branches))
		for i, b := range n.Branches {
			out[i] = b
		}
		return out
	case *Branch:
		return n.Children
	case *For:
		return n.Children
	case *Switch:
		out := make([]Node, 0, len(n.Pre)+len(n.Cases))
		out = append(out, n.Pre...)
		for _, c := range n.Cases {
			out = append(out, c)
		}
		return out
	case *Case:
		return n.Children
	case *Element:
		return n.Children
	case *Component:
		return n.Children
	case *Slot:
		return n.Children
	case *Fragment:
		return n.Children
	}
	return nil
}

// Walk visits n and its descendants in source order (pre-order). If fn
// returns false, the children of that node are skipped.
func Walk(n Node, fn func(Node) bool) {
	if !fn(n) {
		return
	}
	for _, c := range ChildNodes(n) {
		Walk(c, fn)
	}
}

// TagOf returns the Tag of a tag-shaped node (Element, Component, Slot,
// Fragment, Script, Style), or nil.
func TagOf(n Node) *Tag {
	switch n := n.(type) {
	case *Element:
		return &n.Tag
	case *Component:
		return &n.Tag
	case *Slot:
		return &n.Tag
	case *Fragment:
		return &n.Tag
	case *Script:
		return &n.Tag
	case *Style:
		return &n.Tag
	}
	return nil
}

// voidElements are the HTML elements that take no end tag.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}
