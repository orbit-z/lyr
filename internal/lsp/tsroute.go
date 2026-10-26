package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Requests in <script> blocks (PLAN.md §9b "Request routing"): a cursor in
// a script's content goes to the TypeScript child, with the document URI
// swapped for the virtual document's and the position unchanged (the
// virtual document has the .orbit file's lines and UTF-16 columns).
// Results come back with virtual URIs swapped back, .d.ts locations
// mapped to their Go declaration or attribute (the side table), and edits
// kept inside the scripts.

// tsMethods are the requests routed to the child in a script.
var tsMethods = map[string]bool{
	"textDocument/completion":        true,
	"textDocument/hover":             true,
	"textDocument/definition":        true,
	"textDocument/typeDefinition":    true,
	"textDocument/references":        true,
	"textDocument/prepareRename":     true,
	"textDocument/rename":            true,
	"textDocument/signatureHelp":     true,
	"textDocument/documentHighlight": true,
	"textDocument/codeAction":        true,
}

// tsTriggers are TypeScript's completion trigger characters.
var tsTriggers = map[string]bool{".": true, `"`: true, "'": true, "`": true, "/": true, "@": true, "<": true, "#": true, " ": true, "*": true}

// tsResolveMarker marks a TypeScript completion item's wrapped data.
const tsResolveMarker = "ts"

// tsRoute answers a request whose cursor is in a script; ok is false for
// any other position (the caller routes it as before).
func (s *Server) tsRoute(ctx context.Context, method string, params jsonRaw) (res any, rerr *rpcError, ok bool) {
	if method == "completionItem/resolve" {
		r, err := s.tsResolve(ctx, params)
		return r, err, true
	}
	if !tsMethods[method] {
		return nil, nil, false
	}
	o := decodeObj(params)
	if o == nil {
		return nil, nil, false
	}
	uri := decodeObj(o["textDocument"]).str("uri")
	var pos Position
	if method == "textDocument/codeAction" {
		r, has := o.rng("range")
		if !has {
			return nil, nil, false
		}
		pos = r.Start
	} else if json.Unmarshal(o["position"], &pos) != nil {
		return nil, nil, false
	}
	at := func() (*workspace, *vdoc) {
		ws := s.ws
		if ws == nil {
			return nil, nil
		}
		p := ws.orbitPath(uri)
		lines := ws.lines[p]
		if p == "" || lines == nil {
			return ws, nil
		}
		return ws, ws.vdocAt(p, lines.offset(pos, encUTF16))
	}
	s.mu.Lock()
	var d *vdoc
	if s.ts.status != tsOff { // off: scripts answer as before (nothing)
		_, d = at()
	}
	s.mu.Unlock()
	if d == nil {
		return nil, nil, false
	}
	empty := func() (any, *rpcError, bool) {
		if method == "textDocument/completion" {
			return completionList(false, nil), nil, true
		}
		return nil, nil, true
	}
	if method == "textDocument/completion" {
		if t := triggerCharacter(o); t != "" && !tsTriggers[t] {
			return empty()
		}
	}
	c := s.tsConn(ctx)
	if c == nil {
		return empty()
	}
	s.mu.Lock()
	ws, d := at() // the documents may have been rebuilt or opened meanwhile
	if d == nil || ws.docs[d.orbit] == nil && !d.open {
		s.mu.Unlock()
		return empty()
	}
	if method == "textDocument/references" || method == "textDocument/rename" {
		s.tsOpenAll(ws)
	}
	if !d.open {
		// An .orbit file the editor didn't open: open its documents.
		s.tsSyncFile(ws, d.orbit)
		if _, d = at(); d == nil || !d.open {
			s.mu.Unlock()
			return empty()
		}
	}
	fwd := maps1(o)
	fwd.set("textDocument", map[string]any{"uri": d.uri})
	if method == "textDocument/codeAction" {
		cx := decodeObj(fwd["context"])
		if cx == nil {
			cx = obj{}
		}
		if _, set := cx["only"]; !set {
			cx.set("only", []string{"quickfix"})
		}
		fwd["context"] = cx.marshal()
	}
	var name string
	if f := ws.byPath[d.orbit]; f != nil && method == "textDocument/rename" {
		name = identAt([]byte(f.Src), ws.lines[d.orbit].offset(pos, encUTF16))
	}
	s.mu.Unlock()

	raw, err := s.tsCall(ctx, c, method, fwd.marshal())
	if err != nil || isNull(raw) {
		if method == "textDocument/completion" && err == nil {
			return completionList(false, nil), nil, true
		}
		return nil, err, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws != ws {
		return nil, nil, true
	}
	m := s.tsMapper(ws)
	switch method {
	case "textDocument/completion":
		return m.completion(raw, d), nil, true
	case "textDocument/definition", "textDocument/typeDefinition":
		return m.locations(raw, false), nil, true
	case "textDocument/references":
		return m.locations(raw, true), nil, true
	case "textDocument/rename":
		edit, refused := m.workspaceEdit(raw, true)
		if refused {
			return nil, renameErr("can't rename %s: Orbit's generated types (orbit_gen/types) declare it, from Go: "+
				"rename the Go field or its json tag in the frontmatter or the .go file instead", name), true
		}
		return edit, nil, true
	case "textDocument/codeAction":
		return m.codeActions(raw), nil, true
	}
	// hover, signatureHelp, documentHighlight, prepareRename: positions
	// are in the requesting document, the same in the .orbit file.
	return raw, nil, true
}

// tsResolve answers completionItem/resolve: an item of the child (its data
// wrapped by completion) is resolved by the child, with its edits mapped;
// any other item resolves to itself.
func (s *Server) tsResolve(ctx context.Context, params jsonRaw) (any, *rpcError) {
	item := decodeObj(params)
	if item == nil {
		return nil, nil
	}
	w := decodeObj(item["data"])
	if w == nil || w.str("orbit") != tsResolveMarker {
		return params, nil
	}
	item["data"] = w["d"]
	if isNull(item["data"]) {
		delete(item, "data")
	}
	uri := w.str("u")
	s.mu.Lock()
	ws := s.ws
	var c *Conn
	if ws != nil {
		c = s.tsRunningConn()
	}
	s.mu.Unlock()
	if c == nil {
		return item.marshal(), nil
	}
	raw, err := s.tsCall(ctx, c, "completionItem/resolve", item.marshal())
	if err != nil || isNull(raw) {
		return item.marshal(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := ws.vdocByURI(uri)
	res := decodeObj(raw)
	if d == nil || res == nil {
		return raw, nil
	}
	return s.tsMapper(ws).item(res, d).marshal(), nil
}

// tsMapper maps the child's results into .orbit files (with s.mu held).
type tsMapper struct {
	s  *Server
	ws *workspace
}

func (s *Server) tsMapper(ws *workspace) tsMapper { return tsMapper{s: s, ws: ws} }

// completion maps a completion result: items' edits stay inside the
// script (an import insertion moves into it), and data is wrapped for
// resolve.
func (m tsMapper) completion(raw jsonRaw, d *vdoc) jsonRaw {
	head, items := completionItems(raw)
	out := make([]jsonRaw, 0, len(items))
	for _, it := range items {
		o := decodeObj(it)
		if o == nil {
			continue
		}
		out = append(out, m.item(o, d).marshal())
	}
	if head == nil {
		head = obj{"isIncomplete": jsonRaw("false")}
	}
	head.set("items", out)
	return head.marshal()
}

// item maps one completion item of d.
func (m tsMapper) item(o obj, d *vdoc) obj {
	if te := decodeObj(o["textEdit"]); te != nil {
		if m.inScript(d, te) {
			o["textEdit"] = te.marshal()
		} else {
			delete(o, "textEdit")
		}
	}
	if raw, ok := o["additionalTextEdits"]; ok {
		var edits []obj
		json.Unmarshal(raw, &edits)
		if kept := m.vdocEdits(d, edits); len(kept) > 0 {
			o.set("additionalTextEdits", kept)
		} else {
			delete(o, "additionalTextEdits")
		}
	}
	if data, ok := o["data"]; ok && !isNull(data) {
		w := obj{"orbit": jsonRaw(`"` + tsResolveMarker + `"`), "d": data}
		w.set("u", d.uri)
		o["data"] = w.marshal()
	}
	return o
}

// inScript reports whether every range of a TextEdit or InsertReplaceEdit
// lies inside one of d's scripts.
func (m tsMapper) inScript(d *vdoc, e obj) bool {
	lines := m.ws.lines[d.orbit]
	if lines == nil {
		return false
	}
	any := false
	for _, key := range []string{"range", "insert", "replace"} {
		r, has := e.rng(key)
		if !has {
			continue
		}
		any = true
		if !d.containsRange(lines.offset(r.Start, encUTF16), lines.offset(r.End, encUTF16)) {
			return false
		}
	}
	return any
}

// containsRange reports whether [start, end] lies inside one script of d.
func (d *vdoc) containsRange(start, end int) bool {
	for _, sp := range d.spans {
		if start >= sp.Start && end <= sp.End {
			return true
		}
	}
	return false
}

// vdocEdits keeps the TextEdits of d that lie inside its scripts; an
// insertion before the first script (TypeScript adding an import at the
// top of the file, in the blanked frontmatter) moves to the first
// content line of the first script. Others are dropped.
func (m tsMapper) vdocEdits(d *vdoc, edits []obj) []obj {
	lines := m.ws.lines[d.orbit]
	if lines == nil {
		return nil
	}
	var out []obj
	for _, e := range edits {
		r, ok := e.rng("range")
		if !ok {
			continue
		}
		start, end := lines.offset(r.Start, encUTF16), lines.offset(r.End, encUTF16)
		switch {
		case d.containsRange(start, end):
			out = append(out, e)
		case start == end && len(d.spans) > 0 && start < d.spans[0].Start:
			off, indent := firstContent(lines.src, d)
			e = maps1(e)
			e.set("newText", relocatedImport(e.str("newText"), indent))
			p := lines.position(off, encUTF16)
			e.set("range", Range{Start: p, End: p})
			out = append(out, e)
		}
	}
	return out
}

// tsLoc is a mapped location.
type tsLoc struct {
	uri       string
	rng       Range
	generated bool // it was in an emitted .d.ts file
}

// location maps one of the child's locations: a virtual document's to its
// .orbit file (same range), an emitted .d.ts file's through the side table
// to its Go declaration or attribute (unchanged without an entry, but
// under orbit_gen/types), others unchanged. ok is false for a location in
// a virtual document of no current file.
func (m tsMapper) location(uri string, r Range) (tsLoc, bool) {
	rel := m.ws.rel(uriToPath(uri))
	if rel == "" {
		return tsLoc{uri: uri, rng: r}, true
	}
	if orbit := orbitOfVdocPath(rel); orbit != "" {
		if m.ws.byPath[orbit] == nil {
			return tsLoc{}, false
		}
		return tsLoc{uri: m.ws.orbitURI(orbit), rng: r}, true
	}
	if isVdocPath(rel) {
		return tsLoc{}, false // the in-memory tsconfig
	}
	tr, ok := typesRel(rel)
	if !ok {
		return tsLoc{uri: uri, rng: r}, true
	}
	if l, ok := m.fromSideTable(tr, r.Start.Line); ok {
		l.generated = true
		return l, true
	}
	return tsLoc{uri: pathToURI(filepath.Join(m.ws.root, filepath.FromSlash(tr))), rng: r, generated: true}, true
}

// fromSideTable maps line of the emitted file at typesPath to its source.
func (m tsMapper) fromSideTable(typesPath string, line int) (tsLoc, bool) {
	for i := range m.ws.tsTypes {
		f := &m.ws.tsTypes[i]
		if f.Path != typesPath {
			continue
		}
		src, ok := f.Sources[line]
		if !ok {
			return tsLoc{}, false
		}
		if src.Orbit {
			lines := m.ws.lines[src.File]
			if lines == nil {
				return tsLoc{}, false
			}
			n := len(lines.src)
			return tsLoc{uri: m.ws.orbitURI(src.File), rng: lines.rangeOf(min(src.Start, n), min(src.End, n), encUTF16)}, true
		}
		abs := src.File
		if !filepath.IsAbs(abs) {
			rest, ok := strings.CutPrefix(filepath.ToSlash(abs), m.ws.module+"/")
			if !ok {
				return tsLoc{}, false
			}
			abs = filepath.Join(m.ws.root, filepath.FromSlash(rest))
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return tsLoc{}, false
		}
		x := newLineIndex(string(b))
		l := src.Line - 1
		if l < 0 || l >= len(x.starts) {
			return tsLoc{}, false
		}
		ls, le := x.starts[l], x.lineEnd(l)
		start, end := ls, ls
		if from := ls + max(src.Col-1, 0); from <= le {
			if i := strings.Index(x.src[from:le], src.Name); i >= 0 && src.Name != "" {
				start, end = from+i, from+i+len(src.Name)
			}
		}
		return tsLoc{uri: pathToURI(abs), rng: x.rangeOf(start, end, encUTF16)}, true
	}
	return tsLoc{}, false
}

// locations maps a Location, Location[] or LocationLink[] result;
// dropGenerated drops the emitted .d.ts locations (references: they're
// generated).
func (m tsMapper) locations(raw jsonRaw, dropGenerated bool) jsonRaw {
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
	for _, it := range list {
		o := decodeObj(it)
		if o == nil {
			continue
		}
		if _, link := o["targetUri"]; link {
			tr, ok1 := o.rng("targetRange")
			ts, ok2 := o.rng("targetSelectionRange")
			if !ok1 || !ok2 {
				continue
			}
			l, ok := m.location(o.str("targetUri"), tr)
			if !ok || l.generated && dropGenerated {
				continue
			}
			sel, _ := m.location(o.str("targetUri"), ts)
			if l.generated {
				sel.rng = l.rng // the side table maps lines: use the declaration
			}
			o.set("targetUri", l.uri)
			o.set("targetRange", l.rng)
			o.set("targetSelectionRange", sel.rng)
		} else {
			r, ok := o.rng("range")
			if !ok {
				continue
			}
			l, ok := m.location(o.str("uri"), r)
			if !ok || l.generated && dropGenerated {
				continue
			}
			o.set("uri", l.uri)
			o.set("range", l.rng)
		}
		key := string(o.marshal())
		if !seen[key] {
			seen[key] = true
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return jsonRaw("null")
	}
	b, _ := json.Marshal(out)
	return b
}

// workspaceEdit maps a WorkspaceEdit of the child: edits of virtual
// documents go to their .orbit files (kept inside the scripts), edits of
// other files pass through. An edit of an emitted .d.ts file is dropped,
// and with rename set, refused is true (a rename that would rename
// generated types). The result uses documentChanges or changes, as the
// editor supports.
func (m tsMapper) workspaceEdit(raw jsonRaw, rename bool) (out jsonRaw, refused bool) {
	o := decodeObj(raw)
	if o == nil {
		return nil, false
	}
	type fileEdits struct {
		uri     string
		version jsonRaw
		edits   []obj
	}
	var files []*fileEdits
	byURI := map[string]*fileEdits{}
	var ops []jsonRaw // create/rename/delete file operations
	add := func(uri string, version jsonRaw, edits []obj) {
		rel := m.ws.rel(uriToPath(uri))
		if _, gen := typesRel(rel); gen && rel != "" {
			if rename {
				refused = true
			}
			return
		}
		if orbit := orbitOfVdocPath(rel); orbit != "" {
			d := m.ws.vdocByURI(uri)
			if d == nil {
				return
			}
			edits = m.vdocEdits(d, edits)
			uri = m.ws.orbitURI(orbit)
			version = jsonRaw("null")
			if doc := m.ws.docs[orbit]; doc != nil {
				version = jsonRaw(fmt.Sprint(doc.version))
			}
		} else if rel != "" && isVdocPath(rel) {
			return
		}
		if len(edits) == 0 {
			return
		}
		fe := byURI[uri]
		if fe == nil {
			fe = &fileEdits{uri: uri, version: version}
			if fe.version == nil {
				fe.version = jsonRaw("null")
			}
			byURI[uri] = fe
			files = append(files, fe)
		}
		fe.edits = append(fe.edits, edits...)
	}
	if raw, ok := o["changes"]; ok {
		var ch map[string][]obj
		json.Unmarshal(raw, &ch)
		for _, uri := range sortedKeys(ch) {
			add(uri, nil, ch[uri])
		}
	}
	if raw, ok := o["documentChanges"]; ok {
		var dcs []obj
		json.Unmarshal(raw, &dcs)
		for _, dc := range dcs {
			if _, isEdit := dc["edits"]; !isEdit {
				ops = append(ops, dc.marshal())
				continue
			}
			td := decodeObj(dc["textDocument"])
			var edits []obj
			json.Unmarshal(dc["edits"], &edits)
			add(td.str("uri"), td["version"], edits)
		}
	}
	res := obj{}
	if m.s.caps.documentChanges {
		var dcs []any
		for _, fe := range files {
			dcs = append(dcs, map[string]any{"textDocument": map[string]any{"uri": fe.uri, "version": fe.version}, "edits": fe.edits})
		}
		for _, op := range ops {
			dcs = append(dcs, op)
		}
		if dcs == nil {
			dcs = []any{}
		}
		res.set("documentChanges", dcs)
	} else {
		ch := map[string][]obj{}
		for _, fe := range files {
			ch[fe.uri] = fe.edits
		}
		res.set("changes", ch)
	}
	return res.marshal(), refused
}

// codeActions keeps the child's quick fixes whose edits map (PLAN.md §9b
// Q4: kind quickfix only, no commands Orbit couldn't run).
func (m tsMapper) codeActions(raw jsonRaw) jsonRaw {
	var list []obj
	if json.Unmarshal(raw, &list) != nil {
		return jsonRaw("null")
	}
	out := []obj{}
	for _, a := range list {
		if kind := a.str("kind"); kind != "" && kind != "quickfix" && !strings.HasPrefix(kind, "quickfix.") {
			continue
		}
		edit, ok := a["edit"]
		if !ok || isNull(edit) {
			continue // a bare Command, or an action resolved later
		}
		mapped, _ := m.workspaceEdit(edit, false)
		if mapped == nil || !hasEdits(mapped) {
			continue
		}
		a["edit"] = mapped
		delete(a, "command")
		delete(a, "data")
		out = append(out, a)
	}
	b, _ := json.Marshal(out)
	return b
}

// hasEdits reports whether a WorkspaceEdit has a text edit.
func hasEdits(raw jsonRaw) bool {
	return bytes.Contains(raw, []byte(`"newText"`))
}
