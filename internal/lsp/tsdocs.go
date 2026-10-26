package lsp

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/format"
	"github.com/orbit-z/orbit/internal/project"
)

// The TypeScript virtual documents (PLAN.md §9b "Virtual documents"): each
// processed inline <script> of an .orbit file is an in-memory .ts document
// under .orbit/lsp/, the .orbit source with everything outside the block's
// content blanked to spaces, so its lines and UTF-16 columns are the
// .orbit file's and positions pass through both ways. The file's other
// scripts with a JavaScript type (is:inline, no type or a JavaScript MIME
// type, the scripts orbit fmt formats as .js) are JavaScript documents in
// the same way: the classic ones of a file share one document (they share
// the page's global scope, like Astro's inline.mjs), each module one of its
// own. Nothing is written to disk.

// lspDir holds the virtual documents and the in-memory tsconfig, relative
// to the project root.
const lspDir = ".orbit/lsp"

// moduleTail ends a TypeScript or module document, as in orbit check's
// extracted scripts, so each is a module.
const moduleTail = "\nexport {};\n"

// vdocKind is a virtual document's language.
type vdocKind uint8

const (
	vdocTS       vdocKind = iota // a processed script
	vdocJS                       // classic scripts (is:inline, …), merged
	vdocJSModule                 // a type="module" script
)

// vdoc is one virtual document. Guarded by Server.mu.
type vdoc struct {
	uri   string
	orbit string // the .orbit file's project path
	kind  vdocKind
	// spans are the script contents the document keeps, in order.
	spans []compiler.Span
	text  string
	// open is set while the TypeScript child has it, at version.
	open    bool
	version int
	// diags are its latest pulled diagnostics (already filtered to its
	// spans); pullSeq numbers the pulls, so only the latest one's answer
	// is kept.
	diags   []jsonRaw
	pullSeq int
}

func (k vdocKind) languageID() string {
	if k == vdocTS {
		return "typescript"
	}
	return "javascript"
}

// contains reports whether off is inside one of d's scripts (end
// inclusive, so a cursor at the end of the content routes here).
func (d *vdoc) contains(off int) bool {
	for _, sp := range d.spans {
		if off >= sp.Start && off <= sp.End {
			return true
		}
	}
	return false
}

// buildVdocs returns the virtual documents of f, or ok false when its
// parse gave no template (the previous documents stay).
func buildVdocs(root string, f *project.File) (docs []*vdoc, ok bool) {
	if f.AST == nil || f.AST.Template == nil {
		return nil, false
	}
	base := filepath.Join(root, filepath.FromSlash(lspDir), filepath.FromSlash(f.Path))
	var classic *vdoc
	processed, modules := 0, 0
	compiler.Walk(f.AST.Template, func(n compiler.Node) bool {
		s, isScript := n.(*compiler.Script)
		if !isScript {
			return true
		}
		content := s.Content.Span
		switch {
		case s.Processed:
			idx := processed
			processed++
			if s.Attr("src") != nil {
				return false // its file is a .ts file, left to the editor's TypeScript
			}
			docs = append(docs, &vdoc{uri: pathToURI(base + "." + strconv.Itoa(idx) + ".ts"), orbit: f.Path, kind: vdocTS,
				spans: []compiler.Span{content}})
		case format.BlockLang(s) == "js" && s.Attr("src") == nil:
			if t := s.Attr("type"); t != nil && t.IsStatic() && strings.EqualFold(strings.TrimSpace(t.Value), "module") {
				docs = append(docs, &vdoc{uri: pathToURI(base + ".module." + strconv.Itoa(modules) + ".js"), orbit: f.Path,
					kind: vdocJSModule, spans: []compiler.Span{content}})
				modules++
				return false
			}
			if classic == nil {
				classic = &vdoc{uri: pathToURI(base + ".inline.js"), orbit: f.Path, kind: vdocJS}
				docs = append(docs, classic)
			}
			classic.spans = append(classic.spans, content)
		}
		return false
	})
	for _, d := range docs {
		d.text = blankText(f.Src, d.spans, d.kind)
	}
	return docs, true
}

// blankText returns src with every character outside spans replaced by
// spaces, one per UTF-16 unit, and line breaks kept, so lines and UTF-16
// columns stay the same. A TypeScript or module document gets moduleTail;
// in a merged classic document each script's tag end (the byte before its
// content) becomes ";", so a script without a final semicolon doesn't run
// into the next one.
func blankText(src string, spans []compiler.Span, kind vdocKind) string {
	var b strings.Builder
	b.Grow(len(src) + len(moduleTail))
	k := 0 // the next span
	for i := 0; i < len(src); {
		for k < len(spans) && i >= spans[k].End {
			k++
		}
		if k < len(spans) && i >= spans[k].Start {
			end := min(spans[k].End, len(src))
			b.WriteString(src[i:end])
			i = end
			continue
		}
		c := src[i]
		switch {
		case c == '\n' || c == '\r':
			b.WriteByte(c)
			i++
		case c < utf8.RuneSelf:
			if kind == vdocJS && k < len(spans) && i == spans[k].Start-1 {
				b.WriteByte(';')
			} else {
				b.WriteByte(' ')
			}
			i++
		default:
			r, size := utf8.DecodeRuneInString(src[i:])
			b.WriteByte(' ')
			if r >= 0x10000 {
				b.WriteByte(' ') // a surrogate pair: two UTF-16 units
			}
			i += size
		}
	}
	if kind != vdocJS {
		b.WriteString(moduleTail)
	}
	return b.String()
}

// isVdocPath reports whether a module-relative path is under .orbit/lsp/.
func isVdocPath(rel string) bool { return strings.HasPrefix(rel, lspDir+"/") }

// tsconfigURI is the in-memory tsconfig's URI.
func (ws *workspace) tsconfigURI() string {
	return pathToURI(filepath.Join(ws.root, filepath.FromSlash(lspDir), "tsconfig.json"))
}

// typesRel returns the orbit_gen/types path a module-relative path stands
// for when it's an emitted .d.ts file as the TypeScript child sees it: in
// orbit_gen/types/, or, for a component's client module served from
// memory, next to the .orbit file (src/…/X.orbit.d.ts, tsOverlayURI).
func typesRel(rel string) (string, bool) {
	if strings.HasPrefix(rel, typesPrefix) && strings.HasSuffix(rel, ".d.ts") {
		return rel, true
	}
	if strings.HasPrefix(rel, project.SrcDir+"/") && strings.HasSuffix(rel, ".orbit.d.ts") {
		return typesPrefix + rel, true
	}
	return "", false
}

// typesPrefix is where the emitted .d.ts files live (PLAN.md §5c).
const typesPrefix = compiler.GenDir + "/types/"

// tsOverlayRel is where an emitted file is opened when the TypeScript
// child gets it from memory: a component's client module next to its
// .orbit file, which the tsconfig's rootDirs resolves first (TypeScript
// 7.0.2 doesn't resolve a module into an overlay whose directory doesn't
// exist on disk, as orbit_gen/types/src/… doesn't before the first orbit
// generate); go.d.ts and client.d.ts in orbit_gen/types/, which
// declare ambient modules and need no resolution.
func tsOverlayRel(typesPath string) string {
	if rest, ok := strings.CutPrefix(typesPath, typesPrefix); ok && strings.HasPrefix(rest, project.SrcDir+"/") {
		return rest
	}
	return typesPath
}

// vdocsOf returns the current virtual documents of the .orbit file at p,
// building them if needed.
func (ws *workspace) vdocsOf(p string) []*vdoc {
	if docs, ok := ws.vdocs[p]; ok {
		return docs
	}
	f := ws.byPath[p]
	if f == nil {
		return nil
	}
	docs, _ := buildVdocs(ws.root, f)
	ws.vdocs[p] = docs
	return docs
}

// vdocAt returns the virtual document whose script contains off in the
// .orbit file at p.
func (ws *workspace) vdocAt(p string, off int) *vdoc {
	for _, d := range ws.vdocsOf(p) {
		if d.contains(off) {
			return d
		}
	}
	return nil
}

// vdocByURI finds an open or built virtual document by URI.
func (ws *workspace) vdocByURI(uri string) *vdoc {
	abs := uriToPath(uri)
	orbit := orbitOfVdocPath(ws.rel(abs))
	if orbit == "" {
		return nil
	}
	for _, d := range ws.vdocs[orbit] {
		if d.uri == uri || uriToPath(d.uri) == abs {
			return d
		}
	}
	return nil
}

// hasScripts reports whether the .orbit file at p has a virtual document.
func (ws *workspace) hasScripts(p string) bool { return len(ws.vdocsOf(p)) > 0 }

// firstContentLine returns where an import moved out of the blanked area
// goes in d: the start of the first non-blank content of its first
// script, and that line's indentation.
func firstContent(src string, d *vdoc) (off int, indent string) {
	sp := d.spans[0]
	end := min(sp.End, len(src))
	i := sp.Start
	for i < end && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
		i++
	}
	if i == end {
		return sp.Start, ""
	}
	ls := strings.LastIndexByte(src[:i], '\n') + 1
	ls = max(ls, sp.Start)
	return i, src[ls:i]
}

// relocatedImport is TypeScript's import insertion at the top of the file
// (inside the blanked frontmatter) moved to the first content line, each
// inserted line indented like it.
func relocatedImport(text, indent string) string {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n"+indent) + "\n" + indent
}

// orbitOfVdocPath returns the .orbit path of a virtual document's
// module-relative path ("" if it isn't one).
func orbitOfVdocPath(rel string) string {
	if !isVdocPath(rel) {
		return ""
	}
	rest := strings.TrimPrefix(rel, lspDir+"/")
	i := strings.Index(rest, ".orbit.")
	if i < 0 || path.Ext(rest) == ".json" {
		return ""
	}
	return rest[:i+len(".orbit")]
}
