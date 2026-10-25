package compiler

import "strings"

// tagKind is the kind of a tag-shaped node, for the directive rules.
type tagKind uint8

const (
	tkElement tagKind = iota
	tkScript
	tkStyle
	tkComponent
	tkSlot
	tkFragment
)

// validate runs the structural checks that need neither the frontmatter nor
// types, and fills in the directive summaries (Component.Defer/Client,
// Script/Style flags). Component alias resolution, prop names and the Props
// and Params rules come with codegen.
func validate(t *Template, p *parser) {
	Walk(t, func(n Node) bool {
		switch n := n.(type) {
		case *Element:
			p.checkTag(&n.Tag, tkElement, n.Children, nil)
		case *Script:
			p.checkTag(&n.Tag, tkScript, nil, &n.Content)
			n.Inline = n.Attr("is:inline") != nil
			n.JSON = n.Attr("set:json") != nil
			n.Processed = !n.Inline && (len(n.Attrs) == 0 ||
				len(n.Attrs) == 1 && n.Attrs[0].Name == "src" && n.Attrs[0].IsStatic() && isRelativePath(n.Attrs[0].Value))
		case *Style:
			p.checkTag(&n.Tag, tkStyle, nil, &n.Content)
			n.Inline = n.Attr("is:inline") != nil
			n.Global = n.Attr("is:global") != nil
			n.Processed = !n.Inline
		case *Component:
			n.Defer, n.Client = p.checkTag(&n.Tag, tkComponent, n.Children, nil)
			if n.Defer != nil {
				p.checkDeferChildren(n)
			}
		case *Slot:
			p.checkTag(&n.Tag, tkSlot, n.Children, nil)
			n.NameAttr = n.Attr("name")
		case *Fragment:
			p.checkTag(&n.Tag, tkFragment, n.Children, nil)
		}
		return true
	})
}

// isRelativePath reports whether a script src points at a local file by a
// relative path, which Orbit bundles.
func isRelativePath(v string) bool {
	return strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../")
}

// checkTag checks the attributes of one tag. For a component it returns its
// server:defer and (first) client:* attributes.
func (p *parser) checkTag(t *Tag, k tagKind, children []Node, raw *RawText) (deferAttr, client *Attr) {
	var set *Attr
	nClient := 0
	for _, a := range t.Attrs {
		if a.Kind == AttrSpread {
			switch k {
			case tkFragment:
				p.errorf(a.Span, "{...} spread isn't allowed on <Fragment>; it takes only slot, set:html and set:text")
			case tkComponent, tkSlot:
				p.errorf(a.Span, "{...} spread only works on HTML elements")
			}
			continue
		}
		switch d := a.Directive; {
		case d == ClassList:
			p.errorf(a.Span, "class:list isn't supported; build the class string in the frontmatter (e.g. strings.Join)")
		case d == DefineVars:
			p.errorf(a.Span, "define:vars isn't supported; pass data with data-* attributes or set:json")
		case d == UnknownDirective:
			p.unknownDirective(a)
		case d == ServerDefer:
			if k != tkComponent {
				p.errorf(a.Span, "server:defer only works on .orbit components")
				continue
			}
			if a.Kind != AttrEmpty {
				p.errorf(a.Span, "server:defer takes no value")
			}
			if deferAttr == nil {
				deferAttr = a
			}
		case d.IsClient():
			if k != tkComponent {
				p.errorf(a.Span, "%s only works on .orbit components; move the <script> into a component", a.Name)
				continue
			}
			nClient++
			if nClient > 1 {
				p.errorf(a.Span, "only one client:* directive is allowed per component")
				continue
			}
			client = a
			p.checkClientValue(a)
		case d == SetHTML || d == SetText || d == SetJSON:
			switch {
			case d == SetJSON && (k != tkScript || !isJSONScript(t)):
				p.errorf(a.Span, `set:json only works on <script type="application/json">`)
				continue
			case k == tkComponent || k == tkSlot:
				p.errorf(a.Span, "%s only works on HTML elements and <Fragment>", a.Name)
				continue
			case a.Kind == AttrEmpty:
				p.errorf(a.Span, "%s needs a value, e.g. %s={value}", a.Name, a.Name)
			}
			if set != nil {
				p.errorf(a.Span, "only one set: directive is allowed per element")
				continue
			}
			set = a
		case d == SlotDirective:
			switch {
			case a.IsExpr():
				p.errorf(a.Span, `slot takes a static name, e.g. slot="footer"`)
			case a.Kind == AttrEmpty || a.Value == "":
				p.errorf(a.Span, `slot needs a name, e.g. slot="footer"`)
			}
		case d == IsInline:
			if k != tkScript && k != tkStyle {
				p.errorf(a.Span, "is:inline only works on <script> and <style>")
			} else if a.Kind != AttrEmpty {
				p.errorf(a.Span, "is:inline takes no value")
			}
		case d == IsGlobal:
			if k != tkStyle {
				p.errorf(a.Span, "is:global only works on <style>")
			} else if a.Kind != AttrEmpty {
				p.errorf(a.Span, "is:global takes no value")
			}
		default:
			if k == tkFragment {
				p.errorf(a.Span, "attribute %s isn't allowed on <Fragment>; it takes only slot, set:html and set:text", a.Name)
				continue
			}
			if (k == tkElement || k == tkScript || k == tkStyle) && a.IsExpr() {
				p.securityWarning(a)
			}
		}
	}
	if deferAttr != nil && client != nil {
		first := deferAttr
		if client.Start < first.Start {
			first = client
		}
		p.errorf(first.Span, "server:defer and client:* can't be combined; put client:* on a component inside the deferred one")
	}
	if set != nil {
		for _, c := range children {
			if !isBlank(c) {
				p.errorf(childSpan(c), "an element with %s can't have children; the directive sets its content", set.Name)
				break
			}
		}
		if raw != nil && strings.TrimSpace(raw.Text) != "" {
			p.errorf(trimSpan(raw.Span, raw.Text), "an element with %s can't have children; the directive sets its content", set.Name)
		}
	}
	return deferAttr, client
}

func (p *parser) checkClientValue(a *Attr) {
	switch a.Directive {
	case ClientLoad, ClientIdle:
		if a.Kind != AttrEmpty {
			p.errorf(a.Span, "%s takes no value", a.Name)
		}
	case ClientVisible:
		if a.IsExpr() {
			p.errorf(a.Span, "%s takes a static string value, not an expression", a.Name)
		}
	case ClientMedia:
		switch {
		case a.IsExpr():
			p.errorf(a.Span, "%s takes a static string value, not an expression", a.Name)
		case a.Kind == AttrEmpty || strings.TrimSpace(a.Value) == "":
			p.errorf(a.Span, `client:media needs a media query, e.g. client:media="(max-width: 600px)"`)
		}
	}
}

func (p *parser) unknownDirective(a *Attr) {
	if a.Name == "client:only" {
		p.errorf(a.Span, "client:only is for framework components, which Orbit doesn't support; use client:load, client:idle, client:visible or client:media")
		return
	}
	var valid string
	switch ns := reservedNamespace(a.Name); ns {
	case "client":
		valid = "client:load, client:idle, client:visible, client:media"
	case "server":
		valid = "server:defer"
	case "set":
		valid = "set:html, set:text, set:json"
	case "is":
		valid = "is:inline, is:global"
	}
	p.errorf(a.Span, "unknown directive %q (%s directives: %s)", a.Name, reservedNamespace(a.Name), valid)
}

// securityWarning warns about on*={expr} and style={expr} on HTML elements.
func (p *parser) securityWarning(a *Attr) {
	lower := strings.ToLower(a.Name)
	switch {
	case len(lower) > 2 && strings.HasPrefix(lower, "on"):
		p.warnf(a.Span, "%s={…} is escaped for HTML, not for JavaScript; don't put untrusted values here", a.Name)
	case lower == "style":
		p.warnf(a.Span, "%s={…} is escaped for HTML, not for CSS; don't put untrusted values here", a.Name)
	}
}

func isJSONScript(t *Tag) bool {
	a := t.Attr("type")
	return a != nil && a.IsStatic() && strings.EqualFold(strings.TrimSpace(a.Value), "application/json")
}

// isBlank reports whether n renders nothing visible as content: whitespace
// text or a template comment.
func isBlank(n Node) bool {
	switch n := n.(type) {
	case *Text:
		return strings.TrimSpace(n.Value) == ""
	case *Comment:
		return true
	}
	return false
}

// checkDeferChildren enforces that a server:defer component only has
// fallback children.
func (p *parser) checkDeferChildren(c *Component) {
	for _, ch := range c.Children {
		if isBlank(ch) {
			continue
		}
		if t := TagOf(ch); t != nil {
			if s := t.Attr("slot"); s != nil && s.IsStatic() && s.Value == "fallback" {
				continue
			}
		}
		p.errorf(childSpan(ch), `only slot="fallback" content is allowed inside a server:defer component`)
	}
}

// childSpan is where a diagnostic about a child goes: its start tag, or its
// text without the leading and trailing whitespace.
func childSpan(n Node) Span {
	if t := TagOf(n); t != nil {
		return t.StartTag
	}
	if t, ok := n.(*Text); ok {
		return trimSpan(t.Span, t.Value)
	}
	return n.Bounds()
}

// trimSpan narrows s, whose text is text, to the text without surrounding
// whitespace.
func trimSpan(s Span, text string) Span {
	lead := leadingSpace(text)
	return Span{s.Start + lead, s.Start + lead + len(strings.TrimRight(text[lead:], " \t\n\r\f"))}
}
