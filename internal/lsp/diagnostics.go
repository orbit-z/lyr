package lsp

import (
	"encoding/json"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// Diagnostics (PLAN.md §9): Orbit's own (parse, analysis, codegen, project
// checks) merged with gopls's diagnostics of the file's two generated
// files, mapped through their source maps, and published per .orbit file.

// LSP diagnostic severities.
const (
	sevError   = 1
	sevWarning = 2
	sevInfo    = 3
)

// internalPrefix marks a gopls diagnostic outside every mapped region:
// it's reported at the top of the file, and should only come from a
// codegen bug.
const internalPrefix = "internal: "

// orbitDiagnostics converts Orbit's diagnostics of the file at p.
func (ws *workspace) orbitDiagnostics(p string, lines *lineIndex) (out []map[string]any, spans []compiler.Span) {
	f := ws.byPath[p]
	for _, d := range ws.proj.Diagnostics {
		if d.File != p {
			continue
		}
		r := Range{}
		switch {
		case d.Span != (compiler.Span{}) || d.Pos != (compiler.Position{}):
			span := d.Span
			if span == (compiler.Span{}) && f != nil {
				off := f.AST.Lines.Offset(d.Pos)
				span = compiler.Span{Start: off, End: off}
			}
			r = lines.rangeOf(span.Start, span.End, encUTF16)
			spans = append(spans, span)
		}
		sev := sevError
		if d.Severity == compiler.SeverityWarning {
			sev = sevWarning
		}
		out = append(out, map[string]any{"range": r, "severity": sev, "source": "orbit", "message": d.Message})
	}
	return out, spans
}

// goplsDiagnostics maps gopls's diagnostics of g into its .orbit file. A
// diagnostic whose start doesn't map goes to the top of the file with the
// "internal:" prefix (an information or hint one is dropped instead); a
// gopls "unknown field" on an attribute Orbit already reports as an
// unknown prop is a duplicate and dropped.
func (s *Server) goplsDiagnostics(ws *workspace, g *genFile, orbitSpans []compiler.Span) []jsonRaw {
	gd, ok := ws.goplsDiags[g.rel]
	if !ok || g.open && gd.version != 0 && gd.version != g.version {
		return nil // stale: gopls will publish again for the current version
	}
	m := mapper{ws: ws, enc: s.goplsEnc}
	lines := ws.lines[g.orbit]
	var out []jsonRaw
	for _, d := range gd.items {
		d := maps1(d)
		r, ok := d.rng("range")
		if !ok {
			continue
		}
		var sev int
		json.Unmarshal(d["severity"], &sev)
		start, end := m.genRange(g, r)
		os, ok := g.fromGo(start)
		if !ok {
			if sev >= sevInfo {
				continue
			}
			d.set("range", Range{})
			d.set("message", internalPrefix+d.str("message"))
		} else {
			oe, ok := g.fromGo(end)
			if !ok || oe < os {
				oe = os
			}
			if strings.Contains(d.str("message"), "unknown field") && inSpans(orbitSpans, os) {
				continue
			}
			d.set("range", lines.rangeOf(os, oe, encUTF16))
		}
		if raw, ok := d["relatedInformation"]; ok {
			var rel []obj
			json.Unmarshal(raw, &rel)
			kept := rel[:0]
			for _, ri := range rel {
				loc := decodeObj(ri["location"])
				if loc != nil && m.mapLocationObj(loc) {
					ri["location"] = loc.marshal()
					kept = append(kept, ri)
				}
			}
			if len(kept) == 0 {
				delete(d, "relatedInformation")
			} else {
				d.set("relatedInformation", kept)
			}
		}
		out = append(out, d.marshal())
	}
	return out
}

// maps1 copies a decoded diagnostic, so patching doesn't touch the stored
// one.
func maps1(d map[string]jsonRaw) obj {
	o := make(obj, len(d))
	for k, v := range d {
		o[k] = v
	}
	return o
}

func inSpans(spans []compiler.Span, off int) bool {
	for _, sp := range spans {
		if sp.Contains(off) {
			return true
		}
	}
	return false
}

// publish sends the merged diagnostics of the .orbit file at p, if they
// changed since the last publish.
func (s *Server) publish(ws *workspace, p string) {
	lines := ws.lines[p]
	var items []jsonRaw
	if lines != nil {
		own, spans := ws.orbitDiagnostics(p, lines)
		for _, d := range own {
			b, _ := json.Marshal(d)
			items = append(items, b)
		}
		if s.goEnabled {
			decl, impl := ws.genOf(p)
			for _, g := range []*genFile{decl, impl} {
				if g != nil {
					items = append(items, s.goplsDiagnostics(ws, g, spans)...)
				}
			}
		}
	}
	if items == nil {
		items = []jsonRaw{}
	}
	b, _ := json.Marshal(items)
	key := string(b)
	if prev, ok := ws.published[p]; ok && prev == key || !ok && len(items) == 0 {
		return
	}
	if lines == nil {
		delete(ws.published, p)
	} else {
		ws.published[p] = key
	}
	s.client.Notify("textDocument/publishDiagnostics", map[string]any{"uri": ws.orbitURI(p), "diagnostics": jsonRaw(b)})
}

// publishAll republishes every file whose diagnostics may have changed:
// every project file, and the files that disappeared.
func (s *Server) publishAll(ws *workspace) {
	for _, f := range ws.proj.Files {
		s.publish(ws, f.Path)
	}
	for p := range ws.published {
		if ws.byPath[p] == nil {
			s.publish(ws, p)
		}
	}
}

// onGoplsDiagnostics handles gopls's publishDiagnostics: diagnostics of a
// generated file are stored and republished on its .orbit file; others are
// dropped.
func (s *Server) onGoplsDiagnostics(params jsonRaw) {
	o := decodeObj(params)
	if o == nil {
		return
	}
	uri := o.str("uri")
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	if ws == nil || !s.goEnabled {
		return
	}
	abs := uriToPath(uri)
	rel := ws.rel(abs)
	if isGenPath(rel) {
		g := ws.gen[rel]
		if g == nil {
			return // a stale generated file of no .orbit file
		}
		var items []map[string]jsonRaw
		json.Unmarshal(o["diagnostics"], &items)
		var version int
		json.Unmarshal(o["version"], &version)
		ws.goplsDiags[rel] = goplsDiags{version: version, items: items}
		s.publish(ws, g.orbit)
		return
	}
	// Others (the user's .go files, go.mod) are left to the editor's Go
	// support, as Astro's language server reports only .astro files.
}
