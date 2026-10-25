package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// Position mapping between .orbit files and the generated Go files gopls
// sees (PLAN.md §9 Positions). gopls ignores line directives, so every
// location it reports in orbit_gen/** is translated through that file's
// source map; locations in the user's own files pass through.

type jsonRaw = json.RawMessage

// obj is a JSON object decoded one level deep: the generic way to patch a
// few known fields and pass the rest through untouched.
type obj map[string]jsonRaw

func decodeObj(raw jsonRaw) obj {
	var o obj
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	return o
}

func (o obj) str(key string) string {
	var s string
	json.Unmarshal(o[key], &s)
	return s
}

func (o obj) set(key string, v any) {
	if b, err := json.Marshal(v); err == nil {
		o[key] = b
	}
}

func (o obj) rng(key string) (Range, bool) {
	raw, ok := o[key]
	if !ok {
		return Range{}, false
	}
	var r Range
	return r, json.Unmarshal(raw, &r) == nil
}

func (o obj) marshal() jsonRaw {
	b, _ := json.Marshal(o)
	return b
}

// isNull reports whether raw is absent or JSON null.
func isNull(raw jsonRaw) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// toGo maps a byte offset of the .orbit file at p to its generated code:
// the segment that contains off (end inclusive, so a cursor right after
// "props." maps) in the impl or decl file, preferring one that contains it
// strictly over one that ends at it. exact segments map by arithmetic, span
// segments clamp, echo segments never match.
func (ws *workspace) toGo(p string, off int) (*genFile, int, bool) {
	decl, impl := ws.genOf(p)
	var best *genFile
	var seg compiler.Segment
	rank := 0
	for _, g := range []*genFile{impl, decl} {
		if g == nil || g.smap == nil {
			continue
		}
		for _, s := range g.smap.Segments {
			if s.Kind == compiler.SegEcho || off < s.OrbitStart || off > s.OrbitEnd {
				continue
			}
			r := 1
			if off < s.OrbitEnd {
				r = 2
			}
			if r > rank {
				best, seg, rank = g, s, r
			}
		}
	}
	if best == nil {
		return nil, 0, false
	}
	return best, seg.GoStart + min(off-seg.OrbitStart, seg.GoEnd-seg.GoStart), true
}

// fromGo maps a byte offset of a generated file back to its .orbit file.
func (g *genFile) fromGo(goOff int) (int, bool) {
	if g.smap == nil {
		return 0, false
	}
	return g.smap.OrbitOffset(goOff)
}

// mapper translates gopls's locations for one response, with the gopls
// edge's position encoding.
type mapper struct {
	ws  *workspace
	enc encoding // gopls's position encoding
}

// genRange converts a gopls range in g to byte offsets.
func (m mapper) genRange(g *genFile, r Range) (int, int) {
	return g.lines.offset(r.Start, m.enc), g.lines.offset(r.End, m.enc)
}

// location maps a gopls location (uri, range). For a generated file both
// ends go through its source map: an unmapped start drops the location
// (ok false), an unmapped end collapses the range to its start. Other
// files pass through (converted to UTF-16 if gopls speaks another
// encoding). gen is the generated file the location was in, nil for other
// files.
func (m mapper) location(uri string, r Range) (outURI string, out Range, gen *genFile, ok bool) {
	abs := uriToPath(uri)
	if abs == "" {
		return uri, r, nil, true
	}
	rel := m.ws.rel(abs)
	if !isGenPath(rel) {
		if m.enc != encUTF16 {
			r = m.reencode(abs, r)
		}
		return uri, r, nil, true
	}
	g := m.ws.gen[rel]
	if g == nil {
		return "", Range{}, nil, false // a stale generated file of no .orbit file
	}
	start, end := m.genRange(g, r)
	ostart, ok1 := g.fromGo(start)
	if !ok1 {
		return "", Range{}, g, false
	}
	oend, ok2 := g.fromGo(end)
	if !ok2 || oend < ostart {
		oend = ostart
	}
	lines := m.ws.lines[g.orbit]
	if lines == nil {
		return "", Range{}, g, false
	}
	return m.ws.orbitURI(g.orbit), lines.rangeOf(ostart, oend, encUTF16), g, true
}

// reencode converts a range in a user file from gopls's encoding to UTF-16.
func (m mapper) reencode(abs string, r Range) Range {
	b, err := os.ReadFile(abs)
	if err != nil {
		return r
	}
	x := newLineIndex(string(b))
	return x.rangeOf(x.offset(r.Start, m.enc), x.offset(r.End, m.enc), encUTF16)
}

// mapLocationObj maps a Location object ({uri, range}) in place.
func (m mapper) mapLocationObj(o obj) bool {
	r, ok := o.rng("range")
	if !ok {
		return false
	}
	uri, r, _, ok := m.location(o.str("uri"), r)
	if !ok {
		return false
	}
	o.set("uri", uri)
	o.set("range", r)
	return true
}

// mapLocations maps a Location, Location[] or LocationLink[] result,
// dropping what doesn't map and duplicates; fallback, when non-nil, gets a
// chance at locations in generated files that don't map (definition on
// params). It returns null when nothing is left.
func (m mapper) mapLocations(raw jsonRaw, fallback func(g *genFile, r Range) (string, Range, bool)) jsonRaw {
	if isNull(raw) {
		return jsonRaw("null")
	}
	var list []jsonRaw
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		if json.Unmarshal(raw, &list) != nil {
			return jsonRaw("null")
		}
	} else {
		list = []jsonRaw{raw}
	}
	var out []obj
	seen := map[string]bool{}
	for _, item := range list {
		o := decodeObj(item)
		if o == nil {
			continue
		}
		if _, link := o["targetUri"]; link {
			if !m.mapLink(o, fallback) {
				continue
			}
		} else {
			r, ok := o.rng("range")
			if !ok {
				continue
			}
			uri, nr, g, ok := m.location(o.str("uri"), r)
			if !ok && g != nil && fallback != nil {
				uri, nr, ok = fallback(g, r)
			}
			if !ok {
				continue
			}
			o.set("uri", uri)
			o.set("range", nr)
		}
		key := string(o.marshal())
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	if len(out) == 0 {
		return jsonRaw("null")
	}
	b, _ := json.Marshal(out)
	return b
}

// mapLink maps a LocationLink in place.
func (m mapper) mapLink(o obj, fallback func(g *genFile, r Range) (string, Range, bool)) bool {
	tr, ok1 := o.rng("targetRange")
	ts, ok2 := o.rng("targetSelectionRange")
	if !ok1 || !ok2 {
		return false
	}
	target := o.str("targetUri")
	uri, ntr, g, ok := m.location(target, tr)
	if !ok && g != nil && fallback != nil {
		uri, ntr, ok = fallback(g, tr)
	}
	if !ok {
		return false
	}
	_, nts, _, ok := m.location(target, ts)
	if !ok {
		nts = ntr
	}
	o.set("targetUri", uri)
	o.set("targetRange", ntr)
	o.set("targetSelectionRange", nts)
	// originSelectionRange is in the requesting document: the caller's
	// .orbit file, which gopls saw as generated code. Without a reliable
	// mapping, drop it (the editor then highlights the word itself).
	delete(o, "originSelectionRange")
	return true
}

// mapTextEdits maps the edits of one generated file into its .orbit file,
// dropping edits whose range doesn't map (echo lines, glue). It returns the
// mapped edits and how many were dropped.
func (m mapper) mapTextEdits(g *genFile, edits []jsonRaw) (out []jsonRaw, dropped int) {
	lines := m.ws.lines[g.orbit]
	for _, e := range edits {
		o := decodeObj(e)
		if o == nil {
			dropped++
			continue
		}
		ok := true
		for _, key := range []string{"range", "insert", "replace"} {
			r, has := o.rng(key)
			if !has {
				continue
			}
			start, end := m.genRange(g, r)
			ostart, ok1 := g.fromGo(start)
			oend, ok2 := g.fromGo(end)
			if !ok1 || !ok2 || oend < ostart || lines == nil {
				ok = false
				break
			}
			o.set(key, lines.rangeOf(ostart, oend, encUTF16))
		}
		if !ok {
			dropped++
			continue
		}
		out = append(out, o.marshal())
	}
	return out, dropped
}

// mapWorkspaceEdit maps a WorkspaceEdit from gopls: edits in generated
// files move to their .orbit files (unmappable ones dropped), others pass
// through. With changes set, documentChanges' text edits are converted to
// the changes form (for clients without documentChanges support). It
// returns the edit and the number of dropped edits.
func (m mapper) mapWorkspaceEdit(raw jsonRaw, toChanges bool) (jsonRaw, int) {
	o := decodeObj(raw)
	if o == nil {
		return raw, 0
	}
	dropped := 0
	changes := map[string][]jsonRaw{}
	var order []string
	add := func(uri string, edits []jsonRaw) {
		if _, ok := changes[uri]; !ok {
			order = append(order, uri)
		}
		changes[uri] = append(changes[uri], edits...)
	}
	mapFile := func(uri string, edits []jsonRaw) (string, []jsonRaw) {
		g := m.ws.genAt(uriToPath(uri))
		if g == nil {
			if rel := m.ws.rel(uriToPath(uri)); isGenPath(rel) {
				dropped += len(edits)
				return "", nil
			}
			return uri, edits
		}
		out, d := m.mapTextEdits(g, edits)
		dropped += d
		return m.ws.orbitURI(g.orbit), out
	}
	if raw, ok := o["changes"]; ok {
		var ch map[string][]jsonRaw
		json.Unmarshal(raw, &ch)
		for _, uri := range sortedKeys(ch) {
			if u, edits := mapFile(uri, ch[uri]); u != "" && len(edits) > 0 {
				add(u, edits)
			}
		}
		o.set("changes", changes)
	}
	if raw, ok := o["documentChanges"]; ok {
		var dcs []jsonRaw
		json.Unmarshal(raw, &dcs)
		var out []jsonRaw
		for _, dc := range dcs {
			d := decodeObj(dc)
			if d == nil {
				continue
			}
			if _, isEdit := d["edits"]; !isEdit {
				out = append(out, dc) // create/rename/delete: pass through
				continue
			}
			td := decodeObj(d["textDocument"])
			var edits []jsonRaw
			json.Unmarshal(d["edits"], &edits)
			u, mapped := mapFile(td.str("uri"), edits)
			if u == "" || len(mapped) == 0 {
				continue
			}
			if toChanges {
				add(u, mapped)
				continue
			}
			if u != td.str("uri") {
				td.set("uri", u)
				td["version"] = jsonRaw("null")
				if doc := m.ws.docs[m.ws.orbitPath(u)]; doc != nil {
					td.set("version", doc.version)
				}
			}
			d["textDocument"] = td.marshal()
			d.set("edits", mapped)
			out = append(out, d.marshal())
		}
		if toChanges {
			delete(o, "documentChanges")
			o.set("changes", changes)
		} else {
			o.set("documentChanges", out)
		}
	}
	return o.marshal(), dropped
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// identAt returns the Go identifier around offset off of src.
func identAt(src []byte, off int) string {
	isID := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	if off < 0 || off > len(src) {
		return ""
	}
	start, end := off, off
	for start > 0 && isID(src[start-1]) {
		start--
	}
	for end < len(src) && isID(src[end]) {
		end++
	}
	return string(src[start:end])
}

// paramsFallback maps definition results that land on generated code with
// no .orbit source (PLAN.md §9 Definition): the generated Params type of a
// page's decl.go, and the params (or props) binding in its render, go to
// the page's own .orbit file (Params comes from the file name), or to the
// component's Props declaration.
func (m mapper) paramsFallback(g *genFile, r Range) (string, Range, bool) {
	f := m.ws.byPath[g.orbit]
	if f == nil {
		return "", Range{}, false
	}
	off := g.lines.offset(r.Start, m.enc)
	id := identAt(g.content, off)
	top := Range{}
	if g.decl {
		if f.Info.Role != compiler.RolePage {
			return "", Range{}, false
		}
		start := bytes.Index(g.content, []byte("\ntype Params struct"))
		if start < 0 {
			return "", Range{}, false
		}
		end := bytes.Index(g.content[start:], []byte("\n}\n"))
		if end < 0 || off < start || off > start+end+2 {
			return "", Range{}, false
		}
		return m.ws.orbitURI(g.orbit), top, true
	}
	switch id {
	case "params":
		if f.Info.Role == compiler.RolePage {
			return m.ws.orbitURI(g.orbit), top, true
		}
	case "props":
		if p := f.Analysis.Props; p.Declared {
			if lines := m.ws.lines[g.orbit]; lines != nil {
				return m.ws.orbitURI(g.orbit), lines.rangeOf(p.Span.Start, p.Span.Start, encUTF16), true
			}
		}
	}
	return "", Range{}, false
}

// absOrbit is the absolute path of a project path.
func (ws *workspace) absOrbit(p string) string { return filepath.Join(ws.root, filepath.FromSlash(p)) }
