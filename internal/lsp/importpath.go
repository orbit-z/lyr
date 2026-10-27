package lsp

import (
	"fmt"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// Import path completion (PLAN.md §9 Request routing): in a frontmatter
// import spec whose path starts with "." (relative: an .orbit or Markdown
// import, PLAN.md §2.2, §2.4), Orbit offers the project's .orbit files and
// every Markdown file under src/, as paths relative to the file; in one
// whose path starts with "/", as root-relative paths ("/src/…"); in one
// whose path starts with a tsconfig.json paths alias ("@/" for "@/*"), or
// with the start of one, it offers them as alias paths. Accepting one in a
// spec with no alias also inserts the alias, derived from the file name as
// for tag completion's auto-import.

// kindFile is LSP's CompletionItemKind File.
const kindFile = 17

// importPath is the relative import path being typed at the cursor.
type importPath struct {
	// quote is the offset of the opening quote; end is the end of the
	// text a completion replaces: the closing quote, or the cursor when
	// the string isn't closed on its line.
	quote, end int
	closed     bool
	// alias is set when the spec has an import name.
	alias bool
	// typed is the path typed so far; viaAlias is set when it's (the start
	// of) a tsconfig.json paths alias rather than a relative path.
	typed    string
	viaAlias bool
	// root is set when it's a root-relative path ("/src/…").
	root bool
}

// importBlock matches the opening of an import block.
var importBlock = regexp.MustCompile(`\bimport\s*\(`)

// importPathAt returns the relative import path around offset off of f's
// frontmatter: in an `import "./…"` or `import X "./…"` line, or in an
// `X "./…"` or `"./…"` line of an import ( … ) block. It works on the text
// of the line, so it also works while the frontmatter doesn't parse. A path
// that starts with one of the alias prefixes, or with the start of one, is
// an alias import path.
func importPathAt(f *project.File, off int, prefixes []string) (importPath, bool) {
	if f.AST == nil || !f.AST.Frontmatter.Present || !f.AST.Frontmatter.Code.Contains(off) || off > len(f.Src) {
		return importPath{}, false
	}
	code := f.AST.Frontmatter.Code
	src := f.Src
	ls := max(strings.LastIndexByte(src[:off], '\n')+1, code.Start)
	before := src[ls:off]
	if strings.Count(before, `"`) != 1 || strings.ContainsAny(before, "`") {
		return importPath{}, false
	}
	q := strings.IndexByte(before, '"')
	typed := before[q+1:]
	root := strings.HasPrefix(typed, "/")
	viaAlias := !root && !strings.HasPrefix(typed, ".") && typed != "" && slices.ContainsFunc(prefixes, func(p string) bool {
		return p != "" && (strings.HasPrefix(typed, p) || strings.HasPrefix(p, typed))
	})
	if !strings.HasPrefix(typed, ".") && !viaAlias && !root {
		return importPath{}, false
	}
	head := strings.Fields(before[:q])
	if len(head) > 0 && head[0] == "import" {
		head = head[1:]
	} else {
		opens := importBlock.FindAllStringIndex(src[code.Start:ls], -1)
		if len(opens) == 0 || strings.Contains(src[code.Start+opens[len(opens)-1][1]:ls], ")") {
			return importPath{}, false
		}
	}
	if len(head) > 1 || len(head) == 1 && !token.IsIdentifier(head[0]) {
		return importPath{}, false
	}
	ip := importPath{quote: ls + q, end: off, alias: len(head) == 1, typed: typed, viaAlias: viaAlias, root: root}
	rest, _, _ := strings.Cut(src[off:], "\n")
	if i := strings.IndexByte(rest, '"'); i >= 0 {
		ip.end, ip.closed = off+i, true
	}
	return ip, true
}

// importPathItems are the import path completions at the cursor, and
// whether the cursor is in a relative import path (the completion is then
// Orbit's alone).
func (ws *workspace) importPathItems(c cursor, trigger string) ([]any, bool) {
	switch trigger {
	case "", ".", "/", `"`:
	default:
		return nil, false
	}
	ip, ok := importPathAt(c.f, c.off, ws.proj.AliasPrefixes())
	if !ok {
		return nil, false
	}
	taken := map[string]bool{}
	imported := map[string]bool{c.f.Path: true}
	if a := c.f.Analysis; a != nil {
		for _, im := range a.OrbitImports {
			if im.PathSpan.Contains(c.off) {
				continue // the spec being edited
			}
			taken[im.Alias] = true
			imported[im.Resolved] = true
		}
		for _, im := range a.GoImports {
			name := im.Name
			if name == "" {
				name = path.Base(im.Path)
			}
			taken[name] = true
		}
	}
	var targets []string
	for _, f := range ws.proj.Files {
		if !f.IsMD() {
			targets = append(targets, f.Path)
		}
	}
	targets = append(targets, ws.proj.MarkdownPaths()...)
	slices.Sort(targets)
	targets = slices.Compact(targets)
	closing := ""
	if !ip.closed {
		closing = `"`
	}
	span := compiler.Span{Start: ip.quote + 1, End: ip.end}
	items := []any{}
	for _, t := range targets {
		if imported[t] {
			continue
		}
		rel := relImport(c.f.Path, t)
		if ip.root {
			rel = "/" + t
		} else if ip.viaAlias {
			if rel = aliasSpelling(ws.proj.AliasSpellings(t), ip.typed); rel == "" {
				continue
			}
		}
		it := map[string]any{"label": rel, "kind": kindFile, "filterText": rel,
			"sortText": fmt.Sprintf("%02d%s", strings.Count(rel, "../"), rel),
			"textEdit": c.textEdit(span, rel+closing)}
		alias := uniqueAlias(compiler.FieldName(compiler.TrimSourceExt(t)), taken)
		doc := "Markdown file `" + t + "`"
		if !compiler.IsMarkdown(t) {
			doc = componentDoc(ws.proj.Index[t])
			if doc == "" {
				doc = "Component `" + t + "`"
			}
		}
		it["documentation"] = map[string]any{"kind": "markdown", "value": doc}
		if !ip.alias && alias != "" {
			it["detail"] = alias + " " + `"` + rel + `"`
			it["additionalTextEdits"] = []any{c.textEdit(compiler.Span{Start: ip.quote, End: ip.quote}, alias+" ")}
		} else {
			it["detail"] = t
		}
		items = append(items, it)
	}
	return items, true
}

// aliasSpelling returns the shortest of a file's alias import paths
// (shortest first) that agrees with the path typed so far, or "".
func aliasSpelling(spellings []string, typed string) string {
	for _, s := range spellings {
		if strings.HasPrefix(s, typed) || strings.HasPrefix(typed, s) {
			return s
		}
	}
	// Typed past the alias's prefix ("@/comp" for "@/components/…"): the
	// editor filters on the rest.
	if i := strings.IndexByte(typed, '/'); i >= 0 {
		for _, s := range spellings {
			if strings.HasPrefix(s, typed[:i+1]) {
				return s
			}
		}
	}
	return ""
}
