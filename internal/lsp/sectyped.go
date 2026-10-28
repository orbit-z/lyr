package lsp

import (
	"slices"

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
//
// The errors of spreads on component tags (PLAN.md §2.11 (Q5) a) travel the
// same way: only the typed pass knows a spread's type, so a file with one
// is a candidate file, the spreads and prop attributes of its tags are
// candidate attributes, and the typed pass's diagnostics at one of them
// (not already among pass 1's) are kept with their severity. So are the
// set:html errors (PLAN.md §2.6, user decision 2026-10-28), at the value
// of a set:html={expr}.

// secTypedDiag is one security warning (or spread error) of the typed
// check.
type secTypedDiag struct {
	// text is the attribute's source and ord its index among the file's
	// candidate attributes with that text.
	text     string
	ord      int
	message  string
	severity compiler.Severity
}

// securityCandidates returns the attributes of f's pass-1 call sites that
// can get a security warning, then the spreads and prop attributes of its
// component tags with spreads, in order, each with its source text and
// ordinal among the attributes of the same text.
func securityCandidates(f *project.File) (attrs []*compiler.Attr, texts []string, ords []int) {
	if f == nil || f.MD != nil || f.Pass1 == nil {
		return nil, nil, nil
	}
	seen := map[string]int{}
	add := func(a *compiler.Attr) {
		if a == nil || a.Start < 0 || a.End > len(f.Src) || a.Start > a.End {
			return
		}
		text := f.Src[a.Start:a.End]
		attrs, texts, ords = append(attrs, a), append(texts, text), append(ords, seen[text])
		seen[text]++
	}
	for i := range f.Pass1.CallSites {
		cs := &f.Pass1.CallSites[i]
		if cs.Kind != compiler.SiteAttr && cs.Kind != compiler.SiteRest && cs.Kind != compiler.SiteLit || cs.Attr == nil || compiler.SecurityWarning(cs.AttrName) == "" {
			continue
		}
		add(cs.Attr)
	}
	// The spreads on component tags and the prop attributes next to them
	// (PLAN.md §2.11): the typed pass's spread errors are at one of them.
	var tags []*compiler.SpreadTag
	for i := range f.Pass1.CallSites {
		if t := f.Pass1.CallSites[i].Spread; t != nil && !slices.Contains(tags, t) {
			tags = append(tags, t)
			for _, it := range t.Items {
				add(it.Attr)
			}
		}
	}
	// The set:html={expr} values (PLAN.md §2.6): the typed pass's error
	// for a value that isn't an orbit.HTML is at the value, so the
	// candidate is the value (an Attr of its span).
	for i := range f.Pass1.CallSites {
		if cs := &f.Pass1.CallSites[i]; cs.Kind == compiler.SiteHTML {
			add(&compiler.Attr{Span: cs.Code.Span})
		}
	}
	return attrs, texts, ords
}

// typedSecurityDiags returns the security warnings and spread errors of
// p's .orbit files (after Typecheck): the typed pass's diagnostics at a
// candidate attribute that aren't in front (p.Diagnostics before it).
func typedSecurityDiags(p *project.Project, front []project.Diagnostic) map[string][]secTypedDiag {
	type found struct {
		severity compiler.Severity
		message  string
	}
	seen := map[diagKey]int{}
	for _, d := range front {
		seen[keyOf(d)]++
	}
	at := map[string]map[compiler.Span][]found{} // path → attribute span → diagnostics
	for _, d := range p.Diagnostics {
		if k := keyOf(d); seen[k] > 0 {
			seen[k]--
			continue
		}
		if at[d.File] == nil {
			at[d.File] = map[compiler.Span][]found{}
		}
		at[d.File][d.Span] = append(at[d.File][d.Span], found{d.Severity, d.Message})
	}
	out := map[string][]secTypedDiag{}
	for _, f := range p.Files {
		w := at[f.Path]
		if w == nil {
			continue
		}
		attrs, texts, ords := securityCandidates(f)
		for i, a := range attrs {
			for _, d := range w[a.Span] {
				out[f.Path] = append(out[f.Path], secTypedDiag{text: texts[i], ord: ords[i], message: d.message, severity: d.severity})
			}
		}
	}
	return out
}

// hasSecuritySites reports whether an .orbit file has a style or on*
// attribute with an expression value, or a spread on a component tag: only
// then can the typed check find security warnings (or spread errors).
func (ws *workspace) hasSecuritySites() bool {
	for _, f := range ws.proj.Files {
		if hasSecurity(f) {
			return true
		}
	}
	return false
}

// hasSecurity reports whether the .orbit file f has a security warning
// candidate or a spread on a component tag (in its tolerant output): edits
// of such a file (or of one that had one) rerun the typed check.
func hasSecurity(f *project.File) bool {
	return project.HasSecurity(f) || project.HasSpread(f) || project.HasSetHTML(f)
}

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
				sev := sevWarning
				if sd.severity == compiler.SeverityError {
					sev = sevError
				}
				out = append(out, map[string]any{"range": lines.rangeOf(a.Start, a.End, encUTF16), "severity": sev,
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
