package lsp

import (
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/markdown"
	"github.com/orbit-z/orbit/internal/project"
)

// Typed diagnostics of Markdown files (PLAN.md §2.4, §9). Converting a
// frontmatter value to its layout's Props field type needs go/types
// (markdown.Retype, the typed pass), which the workspace never runs on its
// tolerant project. The typed check (disk.go) runs it on a strict copy at
// startup and after orbit_gen/ changes; its Markdown diagnostics are kept
// here, each tied to the frontmatter key it's about: it's shown while that
// key's source text and the layout's Props are unchanged, at the key's
// current position, so it follows edits elsewhere in the file and goes
// away when the key is edited (until the next typed check).

// mdTypedDiag is one typed-pass diagnostic of a Markdown file.
type mdTypedDiag struct {
	// key is the frontmatter key it's about and text that key's source
	// (up to the next key); key "" means the whole file, text its source.
	key, text string
	// rel and length place it relative to the key's start (the file's).
	rel, length int
	// layout and props are the layout's path and Props (propsKey) when
	// it was computed.
	layout, props string
	severity      compiler.Severity
	message       string
}

// hasMarkdownLayouts reports whether a compiled Markdown file has a
// layout: only those get typed diagnostics.
func (ws *workspace) hasMarkdownLayouts() bool {
	for _, f := range ws.proj.Files {
		if f.MD != nil && f.MD.Layout != "" {
			return true
		}
	}
	return false
}

// propsKey identifies the shape of a component's Props, as far as the
// index knows it: the field names and types.
func propsKey(info *compiler.ComponentInfo) string {
	if info == nil {
		return ""
	}
	var b strings.Builder
	if info.Props.Declared {
		b.WriteString("d")
	}
	if info.Props.Listable {
		b.WriteString("l")
	}
	for _, f := range info.Props.Fields {
		b.WriteString(";" + f.Name + " " + f.Type)
	}
	return b.String()
}

// fieldText returns the source of the i-th frontmatter key of d: from the
// key to the next key, or to the closing --- line.
func fieldText(d *markdown.Doc, i int) string {
	start := d.Fields[i].KeyPos
	end := len(d.Src)
	if i+1 < len(d.Fields) {
		end = d.Fields[i+1].KeyPos
	} else {
		for off := start; off < len(d.Src); {
			nl := strings.IndexByte(d.Src[off:], '\n')
			if nl < 0 {
				break
			}
			off += nl + 1
			line, _, _ := strings.Cut(d.Src[off:], "\n")
			if strings.TrimRight(line, " \t\r") == "---" {
				end = off
				break
			}
		}
	}
	if start < 0 || start > end || end > len(d.Src) {
		return ""
	}
	return d.Src[start:end]
}

// fieldAt returns the index of the frontmatter key of d whose source holds
// off, or -1.
func fieldAt(d *markdown.Doc, off int) int {
	for i := len(d.Fields) - 1; i >= 0; i-- {
		if d.Fields[i].KeyPos <= off {
			if off <= d.Fields[i].KeyPos+len(fieldText(d, i)) {
				return i
			}
			return -1
		}
	}
	return -1
}

// diagKey identifies a diagnostic for the set difference in
// typedMarkdownDiags.
type diagKey struct {
	file, msg string
	pos       compiler.Position
	span      compiler.Span
}

func keyOf(d project.Diagnostic) diagKey {
	return diagKey{file: d.File, msg: d.Message, pos: d.Pos, span: d.Span}
}

// typedMarkdownDiags returns the diagnostics of p's Markdown files that the
// typed pass added: those of p.Diagnostics (after Typecheck) not in front
// (p.Diagnostics before it).
func typedMarkdownDiags(p *project.Project, front []project.Diagnostic) map[string][]mdTypedDiag {
	seen := map[diagKey]int{}
	for _, d := range front {
		seen[keyOf(d)]++
	}
	byPath := map[string]*project.File{}
	for _, f := range p.Files {
		if f.MD != nil {
			byPath[f.Path] = f
		}
	}
	out := map[string][]mdTypedDiag{}
	for _, d := range p.Diagnostics {
		k := keyOf(d)
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		f := byPath[d.File]
		if f == nil {
			continue
		}
		td := mdTypedDiag{layout: f.MD.Layout, props: propsKey(p.Index[f.MD.Layout]), severity: d.Severity,
			message: d.Message, text: f.Src, rel: -1}
		off, end := d.Span.Start, d.Span.End
		if d.Span == (compiler.Span{}) && d.Pos != (compiler.Position{}) {
			off = f.MD.Lines.Offset(d.Pos)
			end = off
		}
		if d.Span != (compiler.Span{}) || d.Pos != (compiler.Position{}) {
			td.rel, td.length = off, end-off
			if i := fieldAt(f.MD, off); i >= 0 {
				fd := f.MD.Fields[i]
				td.key, td.text, td.rel = fd.Key, fieldText(f.MD, i), off-fd.KeyPos
			}
		}
		out[d.File] = append(out[d.File], td)
	}
	return out
}

// mdTypedDiagnostics returns the typed diagnostics of the Markdown file at
// p that still apply to its current source, skipping any at the start of
// one of Orbit's own (spans).
func (ws *workspace) mdTypedDiagnostics(p string, lines *lineIndex, spans []compiler.Span) []map[string]any {
	f := ws.byPath[p]
	if f == nil || f.MD == nil {
		return nil
	}
	var out []map[string]any
	for _, td := range ws.mdTyped[p] {
		if td.layout != f.MD.Layout || td.props != propsKey(ws.proj.Index[f.MD.Layout]) {
			continue
		}
		base := 0
		if td.key == "" {
			if td.text != f.Src {
				continue
			}
		} else {
			found := false
			for i, fd := range f.MD.Fields {
				if fd.Key == td.key && fieldText(f.MD, i) == td.text {
					base, found = fd.KeyPos, true
					break
				}
			}
			if !found {
				continue
			}
		}
		r := Range{}
		if td.rel >= 0 {
			start := base + td.rel
			dup := false
			for _, sp := range spans {
				dup = dup || sp.Start == start
			}
			if dup {
				continue
			}
			r = lines.rangeOf(start, start+td.length, encUTF16)
		}
		sev := sevError
		if td.severity == compiler.SeverityWarning {
			sev = sevWarning
		}
		out = append(out, map[string]any{"range": r, "severity": sev, "source": "orbit", "message": td.message})
	}
	return out
}
