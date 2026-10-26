package compiler

// Milestone 6 contract (PLAN.md §5): the processed <script> and <style>
// blocks of a file, for the bundler (internal/assets) and orbit check, and
// the scoped-CSS rewrite (css.go).

// ScriptBlock is a processed <script> of a file: Orbit bundles it.
type ScriptBlock struct {
	// Index is its position among the file's processed scripts, from 0.
	Index int
	// Content is the TypeScript between the tags. With Src set it is
	// whatever is written between the tags (usually nothing).
	Content RawText
	// Src is the relative src ("./x.ts"), or "" for an inline script.
	Src string
}

// StyleBlock is a processed <style> of a file: Orbit bundles it into the
// page stylesheet.
type StyleBlock struct {
	// Index is its position among the file's processed styles, from 0.
	Index   int
	Content RawText
	// Global is true with is:global: bundled but not scoped.
	Global bool
}

// Scripts returns the file's processed scripts in source order. is:inline
// scripts, set:json scripts and scripts with other attributes aren't
// processed (Script.Processed) and aren't listed.
func Scripts(f *File) []ScriptBlock {
	var out []ScriptBlock
	walkTemplate(f, func(n Node) {
		if s, ok := n.(*Script); ok && s.Processed {
			b := ScriptBlock{Index: len(out), Content: s.Content}
			if a := s.Attr("src"); a != nil {
				b.Src = a.Value
			}
			out = append(out, b)
		}
	})
	return out
}

// Styles returns the file's processed styles in source order (is:inline
// styles aren't listed).
func Styles(f *File) []StyleBlock {
	var out []StyleBlock
	walkTemplate(f, func(n Node) {
		if s, ok := n.(*Style); ok && s.Processed {
			out = append(out, StyleBlock{Index: len(out), Content: s.Content, Global: s.Global})
		}
	})
	return out
}

// HasScopedStyle reports whether the file has a processed style without
// is:global. Only then does every HTML element of its template get the
// scope attribute (PLAN.md §5).
func HasScopedStyle(f *File) bool {
	for _, s := range Styles(f) {
		if !s.Global {
			return true
		}
	}
	return false
}

// ScopeAttr returns the scope attribute name of a file, "data-o-" plus its
// ScopeHash, e.g. data-o-3fa2c1d0. CSS selectors use [data-o-…].
func ScopeAttr(orbitPath string) string { return "data-o-" + ScopeHash(orbitPath) }

// ScopeCSS rewrites css so its selectors only match elements carrying
// attr (ScopeAttr): [attr] is added to the last compound of
// each selector (PLAN.md §5). Diagnostics have spans relative to css; the
// caller shifts them to the style's Content.Start.
//
// Comments and formatting are kept; only the scope is inserted and the
// :global(…) wrappers removed. The rules are in docs/styles-and-scripts.md.
// On errors the output is still the best-effort rewrite.
func ScopeCSS(css, attr string) (string, []Diagnostic) {
	return scopeCSS(css, attr)
}

func walkTemplate(f *File, fn func(Node)) {
	if f == nil || f.Template == nil {
		return
	}
	Walk(f.Template, func(n Node) bool {
		fn(n)
		return true
	})
}
