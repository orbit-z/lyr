package lsp

import (
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// The style and on* security warnings in the editor (PLAN.md §2.7 (Q2) a).
// The typed pass emits them (it knows when a value is an orbit.CSS or a
// constant), and the workspace never runs it on its tolerant project, so
// the typed check (disk.go) keeps the warnings it found here, each tied to
// its attribute's source text (style={s}). One is shown while the file has
// a candidate attribute with that text (the n-th one of that text, for
// repeats), at the attribute's current position: it follows edits elsewhere
// and goes away when the attribute is edited, until the next typed check.
//
// The typed check while typing (mdcheck.go) refreshes them too (user
// decision, 2026-10-27): an edit of an .orbit file that has or had
// candidates, or a Go file change, counts in ws.secEdit and schedules it,
// and it type-checks the files with candidates and the decl packages they
// import (project.CheckScope.Security). A typed check's warnings are
// adopted only if ws.secEdit hasn't moved since its snapshot. Edits of
// other .orbit files run nothing.

// secTypedDiag is one security warning of the typed check.
type secTypedDiag struct {
	// text is the attribute's source and ord its index among the file's
	// candidate attributes with that text.
	text    string
	ord     int
	message string
}

// securityCandidates returns the attributes of f's pass-1 call sites that
// can get a security warning, in order, each with its source text and
// ordinal among the attributes of the same text.
func securityCandidates(f *project.File) (attrs []*compiler.Attr, texts []string, ords []int) {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return nil, nil, nil
	}
	seen := map[string]int{}
	for i := range f.Pass1.CallSites {
		cs := &f.Pass1.CallSites[i]
		if cs.Kind != compiler.SiteAttr && cs.Kind != compiler.SiteRest && cs.Kind != compiler.SiteLit || cs.Attr == nil || compiler.SecurityWarning(cs.AttrName) == "" {
			continue
		}
		a := cs.Attr
		if a.Start < 0 || a.End > len(f.Src) || a.Start > a.End {
			continue
		}
		text := f.Src[a.Start:a.End]
		attrs, texts, ords = append(attrs, a), append(texts, text), append(ords, seen[text])
		seen[text]++
	}
	return attrs, texts, ords
}

// typedSecurityDiags returns the security warnings of p's .orbit files
// (after Typecheck): the typed pass's warnings at a candidate attribute.
func typedSecurityDiags(p *project.Project) map[string][]secTypedDiag {
	warned := map[string]map[compiler.Span]string{} // path → attribute span → message
	for _, d := range p.Diagnostics {
		if d.Severity == compiler.SeverityWarning {
			if warned[d.File] == nil {
				warned[d.File] = map[compiler.Span]string{}
			}
			warned[d.File][d.Span] = d.Message
		}
	}
	out := map[string][]secTypedDiag{}
	for _, f := range p.Files {
		w := warned[f.Path]
		if w == nil {
			continue
		}
		attrs, texts, ords := securityCandidates(f)
		for i, a := range attrs {
			if msg, ok := w[a.Span]; ok {
				out[f.Path] = append(out[f.Path], secTypedDiag{text: texts[i], ord: ords[i], message: msg})
			}
		}
	}
	return out
}

// hasSecuritySites reports whether an .orbit file has a style or on*
// attribute with an expression value: only then can the typed check find
// security warnings.
func (ws *workspace) hasSecuritySites() bool {
	for _, f := range ws.proj.Files {
		if project.HasSecurity(f) {
			return true
		}
	}
	return false
}

// hasSecurity reports whether the .orbit file f has a security warning
// candidate (in its tolerant output): edits of such a file (or of one that
// had one) rerun the typed check.
func hasSecurity(f *project.File) bool { return project.HasSecurity(f) }

// securityChanged records a change that the security warnings may depend
// on (an edit of an .orbit file that has or had candidates, a Go file
// change) and schedules the typed check while typing (with s.mu held).
// Without candidates left, there's nothing to check and no warning to
// show.
func (s *Server) securityChanged(ws *workspace) {
	ws.secEdit++
	if !ws.hasSecuritySites() {
		ws.secTyped = nil
		return
	}
	if !s.started || s.shutdown {
		return
	}
	s.scheduleTypedCheck()
}

// secTypedDiagnostics returns the typed check's security warnings of the
// .orbit file at p that still apply to its current source.
func (ws *workspace) secTypedDiagnostics(p string, lines *lineIndex) []map[string]any {
	stored := ws.secTyped[p]
	if len(stored) == 0 {
		return nil
	}
	attrs, texts, ords := securityCandidates(ws.byPath[p])
	var out []map[string]any
	for _, sd := range stored {
		for i, a := range attrs {
			if texts[i] == sd.text && ords[i] == sd.ord {
				out = append(out, map[string]any{"range": lines.rangeOf(a.Start, a.End, encUTF16), "severity": sevWarning,
					"source": "orbit", "message": sd.message})
				break
			}
		}
	}
	return out
}

// adoptSecurity sets the security warnings from a finished typed check
// (with s.mu held) and reports whether there were or are any.
func (ws *workspace) adoptSecurity(sec map[string][]secTypedDiag) bool {
	if len(ws.secTyped) == 0 && len(sec) == 0 {
		return false
	}
	ws.secTyped = sec
	return true
}
