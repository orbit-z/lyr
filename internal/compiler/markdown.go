package compiler

import (
	"path"
	"slices"
	"strings"
)

// Markdown files (PLAN.md §2.4) are compiled by internal/markdown; the
// compiler only needs to know their names: imports of them, their
// generated packages and component IDs.

// MarkdownExts are the extensions of Markdown files, as in Astro.
var MarkdownExts = []string{".md", ".markdown", ".mdown", ".mkdn", ".mkd", ".mdwn"}

// IsMarkdown reports whether the path has a Markdown extension.
func IsMarkdown(p string) bool {
	return slices.Contains(MarkdownExts, path.Ext(p))
}

// TrimSourceExt returns the base name of a component file without its
// extension: .orbit or a Markdown one.
func TrimSourceExt(p string) string {
	base := path.Base(p)
	if IsMarkdown(base) {
		return strings.TrimSuffix(base, path.Ext(base))
	}
	return strings.TrimSuffix(base, ".orbit")
}

// PropList is the parenthesized part of the unknown-prop error for a
// component tag: "props: title, date" or "Card takes no props".
func PropList(tag string, p *PropsInfo) string { return propList(tag, p) }
