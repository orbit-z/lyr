package compiler

// The component call sites of a file, for the project's static graphs
// (milestone 8): the island table and Route.Islands (server:defer), and the
// per-page lazy entries of client:* call sites (PLAN.md §5).

// ComponentCall is one component call site of a template.
type ComponentCall struct {
	// Target is the called component's project-relative path: the
	// resolved .lyr import, or the file itself for <Self>. "" when the
	// tag matches no import.
	Target string
	// Defer is true for server:defer.
	Defer bool
	// Lazy is the lazy client:* directive (ClientIdle, ClientVisible or
	// ClientMedia), NotDirective otherwise (client:load is eager).
	Lazy Directive
	// In is the index, in the same list, of the innermost lazy call site
	// whose children (slot content) hold this call, or -1. Slot content
	// renders inside the outer component's <lyr-lazy> wrapper, so it is
	// part of that lazy subtree.
	In int
}

// ComponentCalls returns the component call sites of a parsed and
// analyzed file, in source order. A tag with both server:defer and
// client:* (an error) counts as neither.
func ComponentCalls(f *File, a *Analysis) []ComponentCall {
	if f == nil || f.Template == nil || a == nil {
		return nil
	}
	imports := map[string]string{}
	for _, imp := range a.LyrImports {
		if _, dup := imports[imp.Alias]; !dup {
			imports[imp.Alias] = imp.Resolved
		}
	}
	var out []ComponentCall
	var visit func(n Node, in int)
	component := func(c *Component, in int) {
		call := ComponentCall{Target: imports[c.Name], In: in}
		if c.Self {
			call.Target = a.Info.Path
		}
		switch {
		case c.Defer != nil && c.Client != nil:
		case c.Defer != nil:
			call.Defer = true
		case c.Client != nil && c.Client.Directive.IsLazy():
			call.Lazy = c.Client.Directive
		}
		out = append(out, call)
		if call.Lazy != NotDirective {
			in = len(out) - 1
		}
		for _, ch := range c.Children {
			visit(ch, in)
		}
	}
	visit = func(n Node, in int) {
		if c, ok := n.(*Component); ok {
			component(c, in)
			return
		}
		Walk(n, func(m Node) bool {
			if m == n {
				return true
			}
			if c, ok := m.(*Component); ok {
				component(c, in)
				return false
			}
			return true
		})
	}
	visit(f.Template, -1)
	return out
}
