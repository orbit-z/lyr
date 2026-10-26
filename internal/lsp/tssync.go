package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/orbit-z/orbit/internal/check"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// What the TypeScript child sees (PLAN.md §9b): the in-memory tsconfig,
// the virtual documents of the .orbit files open in the editor (of every
// file after the first references or rename), and the emitted .d.ts files
// that differ from the disk, as overlays (Q5). Diagnostics are pulled,
// debounced, for the documents of open .orbit files. Every function here
// runs with s.mu held.

// tsDiagDelay debounces the diagnostic pulls, like gopls's
// diagnosticsDelay (PLAN.md §9).
var tsDiagDelay = 50 * time.Millisecond

// tsOverlay is an emitted .d.ts file open in the child from memory.
type tsOverlay struct {
	uri     string
	content []byte
	version int
}

// tsOnReady opens everything in the freshly started child.
func (s *Server) tsOnReady(ws *workspace, c *Conn) {
	s.tsRegisterWatcher()
	s.tsSyncTsconfig(ws, c)
	s.tsPushTypes(ws)
	for _, p := range slices.Sorted(maps.Keys(ws.byPath)) {
		if s.tsWanted(ws, p) {
			s.tsSyncFile(ws, p)
		}
	}
	// Fresh types: the startup typed check may not have run (no
	// orbit_gen/), and nothing kept them fresh before the child ran.
	s.dataChanged(ws)
}

// tsWanted reports whether the child should have the documents of p.
func (s *Server) tsWanted(ws *workspace, p string) bool {
	return s.ts.all || ws.docs[p] != nil
}

// tsRunningConn returns the child's connection if it runs.
func (s *Server) tsRunningConn() *Conn {
	if s.ts.status != tsRunning || s.ts.proc == nil {
		return nil
	}
	return s.ts.proc.conn
}

// tsRegisterWatcher registers Orbit's watcher of the files the child
// reads, once (PLAN.md §9b: instead of the child's own globs).
func (s *Server) tsRegisterWatcher() {
	if s.ts.watching || !s.caps.watchDynamic {
		return
	}
	s.ts.watching = true
	go s.client.Call(context.Background(), "client/registerCapability", map[string]any{
		"registrations": []any{map[string]any{
			"id":     "orbit-ts-files",
			"method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{
				"watchers": []any{map[string]any{"globPattern": "**/*.{ts,mts,cts,tsx,js,mjs,cjs,jsx,json}"}},
			},
		}},
	})
}

// tsconfigText is the in-memory tsconfig: the one orbit check writes
// (PLAN.md §9b Q3), which sits two directories down as well.
func tsconfigText(root string) string {
	u, _ := check.ReadUserTsconfig(root)
	return check.Tsconfig(u)
}

// tsSyncTsconfig sends the in-memory tsconfig when it changed.
func (s *Server) tsSyncTsconfig(ws *workspace, c *Conn) {
	if c == nil {
		return
	}
	text := tsconfigText(ws.root)
	if text == s.ts.tsconfig {
		return
	}
	uri := ws.tsconfigURI()
	if s.ts.tsconfig == "" {
		s.ts.tsconfigVersion = 1
		c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "jsonc", "version": 1, "text": text}})
	} else {
		s.ts.tsconfigVersion++
		c.Notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": s.ts.tsconfigVersion},
			"contentChanges": []any{map[string]any{"text": text}}})
	}
	s.ts.tsconfig = text
}

// tsSyncFile rebuilds the virtual documents of the .orbit file at p after
// its source changed, and opens, changes or closes them in the child as
// needed. A parse without a template keeps the previous documents.
func (s *Server) tsSyncFile(ws *workspace, p string) {
	old := ws.vdocs[p]
	f := ws.byPath[p]
	var docs []*vdoc
	if f != nil {
		var ok bool
		if docs, ok = buildVdocs(ws.root, f); !ok {
			if old == nil {
				return
			}
			docs = old
		}
	}
	c := s.tsRunningConn()
	wanted := f != nil && s.tsWanted(ws, p)
	byURI := map[string]*vdoc{}
	for _, d := range old {
		byURI[d.uri] = d
	}
	for _, d := range docs {
		o := byURI[d.uri]
		delete(byURI, d.uri)
		if o != nil && o != d {
			d.open, d.version, d.diags, d.pullSeq = o.open, o.version, o.diags, o.pullSeq
		}
		if c == nil || !wanted {
			continue
		}
		switch {
		case !d.open:
			d.open = true
			d.version++
			c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
				"uri": d.uri, "languageId": d.kind.languageID(), "version": d.version, "text": d.text}})
			s.tsDirty(d)
		case o != nil && o.text != d.text:
			d.version++
			c.Notify("textDocument/didChange", map[string]any{
				"textDocument":   map[string]any{"uri": d.uri, "version": d.version},
				"contentChanges": []any{map[string]any{"text": d.text}}})
			s.tsDirty(d)
		}
	}
	for _, d := range byURI { // gone
		if d.open && c != nil {
			c.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": d.uri}})
		}
	}
	if !wanted {
		for _, d := range docs {
			if d.open && c != nil {
				c.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": d.uri}})
			}
			d.open, d.diags = false, nil
		}
	}
	if f == nil {
		delete(ws.vdocs, p)
		return
	}
	ws.vdocs[p] = docs
}

// tsOpenAll opens the documents of every .orbit file, once per session,
// so references and rename see closed files too (Q7).
func (s *Server) tsOpenAll(ws *workspace) {
	if s.ts.all || s.tsRunningConn() == nil {
		return
	}
	s.ts.all = true
	start := time.Now()
	for _, p := range slices.Sorted(maps.Keys(ws.byPath)) {
		s.tsSyncFile(ws, p)
	}
	s.logf("typescript: opened every script's document (%v)", time.Since(start).Round(time.Microsecond))
}

// tsDirty schedules a diagnostic pull of d if its .orbit file is open
// in the editor.
func (s *Server) tsDirty(d *vdoc) {
	ws := s.ws
	if ws == nil || ws.docs[d.orbit] == nil {
		return
	}
	if s.ts.dirty == nil {
		s.ts.dirty = map[string]bool{}
	}
	s.ts.dirty[d.uri] = true
	if s.ts.diagTimer != nil {
		s.ts.diagTimer.Stop()
	}
	s.ts.diagTimer = time.AfterFunc(tsDiagDelay, s.tsPull)
}

// tsDirtyAll schedules a pull of every open .orbit file's documents (the
// child's diagnostic refresh, a types overlay change).
func (s *Server) tsDirtyAll(ws *workspace) {
	for p := range ws.docs {
		for _, d := range ws.vdocs[p] {
			if d.open {
				s.tsDirty(d)
			}
		}
	}
}

// tsPull pulls the diagnostics of the dirty documents and publishes them
// with the .orbit file's others (source "ts").
func (s *Server) tsPull() {
	s.mu.Lock()
	ws, c := s.ws, s.tsRunningConn()
	dirty := s.ts.dirty
	s.ts.dirty = nil
	type pull struct {
		d            *vdoc
		version, seq int
	}
	var pulls []pull
	if ws != nil && c != nil {
		for uri := range dirty {
			if d := ws.vdocByURI(uri); d != nil && d.open && ws.docs[d.orbit] != nil {
				d.pullSeq++
				pulls = append(pulls, pull{d, d.version, d.pullSeq})
			}
		}
	}
	s.mu.Unlock()
	for _, pl := range pulls {
		go func() {
			raw, _ := s.tsCall(context.Background(), c, "textDocument/diagnostic",
				map[string]any{"textDocument": map[string]any{"uri": pl.d.uri}})
			var rep struct {
				Kind  string `json:"kind"`
				Items []obj  `json:"items"`
			}
			if raw == nil || json.Unmarshal(raw, &rep) != nil || rep.Kind != "full" {
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.ws != ws || ws.vdocByURI(pl.d.uri) != pl.d || pl.d.version != pl.version || pl.d.pullSeq != pl.seq {
				return // changed meanwhile, or a later pull is in flight
			}
			pl.d.diags = ws.tsFilterDiags(pl.d, rep.Items)
			s.publish(ws, pl.d.orbit)
			if h := tsHooks.Load(); h != nil && h.pulled != nil {
				h.pulled(pl.d.orbit)
			}
		}()
	}
}

// tsFilterDiags keeps the diagnostics that start inside d's scripts,
// labeled source "ts" (the unnecessary/deprecated hints stay).
func (ws *workspace) tsFilterDiags(d *vdoc, items []obj) []jsonRaw {
	lines := ws.lines[d.orbit]
	if lines == nil {
		return nil
	}
	out := []jsonRaw{}
	for _, it := range items {
		r, ok := it.rng("range")
		if !ok || !d.contains(lines.offset(r.Start, encUTF16)) {
			continue
		}
		if it.str("source") == "" {
			it.set("source", "ts")
		}
		out = append(out, it.marshal())
	}
	return out
}

// tsDiagnostics returns the TypeScript diagnostics to publish for the
// .orbit file at p: only while it's open in the editor.
func (ws *workspace) tsDiagnostics(p string) []jsonRaw {
	if ws.docs[p] == nil {
		return nil
	}
	var out []jsonRaw
	for _, d := range ws.vdocs[p] {
		if d.open {
			out = append(out, d.diags...)
		}
	}
	return out
}

// tsPushTypes makes the child see the in-memory .d.ts files (Q5): each
// one that differs from the disk is an overlay; one the disk caught up
// with is closed.
func (s *Server) tsPushTypes(ws *workspace) {
	c := s.tsRunningConn()
	if c == nil || ws.tsTypes == nil {
		return
	}
	want := map[string]*compiler.TSFile{}
	for i := range ws.tsTypes {
		f := &ws.tsTypes[i]
		b, err := os.ReadFile(filepath.Join(ws.root, filepath.FromSlash(f.Path)))
		if err == nil && bytes.Equal(b, f.Content) {
			continue
		}
		want[pathToURI(filepath.Join(ws.root, filepath.FromSlash(tsOverlayRel(f.Path))))] = f
	}
	changed := false
	for uri := range ws.tsOverlays {
		if want[uri] == nil {
			c.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
			delete(ws.tsOverlays, uri)
			changed = true
		}
	}
	for _, uri := range slices.Sorted(maps.Keys(want)) {
		f := want[uri]
		o := ws.tsOverlays[uri]
		switch {
		case o == nil:
			o = &tsOverlay{uri: uri, content: f.Content, version: 1}
			ws.tsOverlays[uri] = o
			c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
				"uri": uri, "languageId": "typescript", "version": 1, "text": string(f.Content)}})
		case !bytes.Equal(o.content, f.Content):
			o.content = f.Content
			o.version++
			c.Notify("textDocument/didChange", map[string]any{
				"textDocument":   map[string]any{"uri": uri, "version": o.version},
				"contentChanges": []any{map[string]any{"text": string(f.Content)}}})
		default:
			continue
		}
		changed = true
	}
	if changed {
		s.tsDirtyAll(ws)
	}
}

// adoptTypes takes the data contracts of a typed check: known are the
// .orbit files it compiled (others keep their previous contract), units
// their contracts (nil without data). It re-emits the .d.ts files and
// pushes them to the child.
func (s *Server) adoptTypes(ws *workspace, known map[string]bool, units map[string]*compiler.UnitTypes) {
	for p := range known {
		if u := units[p]; u != nil {
			ws.tsUnits[p] = u
		} else {
			delete(ws.tsUnits, p)
		}
	}
	for p := range ws.tsUnits {
		if ws.byPath[p] == nil {
			delete(ws.tsUnits, p)
		}
	}
	list := make([]*compiler.UnitTypes, 0, len(ws.tsUnits))
	for _, p := range slices.Sorted(maps.Keys(ws.tsUnits)) {
		list = append(list, ws.tsUnits[p])
	}
	ws.tsTypes = compiler.EmitTypes(list)
	s.tsPushTypes(ws)
	if h := tsHooks.Load(); h != nil && h.types != nil {
		h.types()
	}
}

// typesOf collects a compiled project's data contracts for adoptTypes:
// the .orbit files with a pass-1 output, and the contracts of those with
// JSON sites.
func typesOf(p *project.Project) (known map[string]bool, units map[string]*compiler.UnitTypes) {
	known, units = map[string]bool{}, map[string]*compiler.UnitTypes{}
	for _, f := range p.Files {
		if f.MD != nil || f.Pass1 == nil {
			continue
		}
		known[f.Path] = true
		if len(f.Pass1.JSONSites) > 0 && f.Types != nil {
			units[f.Path] = f.Types
		}
	}
	return known, units
}

// hasData reports whether the .orbit file f has orbit.JSON or set:json
// sites (in its tolerant output).
func hasData(f *project.File) bool {
	return f != nil && f.Pass1 != nil && len(f.Pass1.JSONSites) > 0
}

// tsWatched handles the watched-file events the child cares about (with
// s.mu held): a root tsconfig.json change recomputes the in-memory one,
// orbit_gen/types/ changes re-compare the overlays, and every event of a
// TypeScript, JavaScript or JSON file under the root is forwarded.
func (s *Server) tsWatched(ws *workspace, changes []any, rels []string) {
	c := s.tsRunningConn()
	if c == nil {
		return
	}
	var forward []any
	types := false
	for i, rel := range rels {
		if rel == "" || !isTSWatched(rel) {
			continue
		}
		if rel == "tsconfig.json" {
			s.tsSyncTsconfig(ws, c)
		}
		if strings.HasPrefix(rel, typesPrefix) {
			types = true
		}
		forward = append(forward, changes[i])
	}
	if len(forward) > 0 {
		c.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": forward})
	}
	if types {
		s.tsPushTypes(ws)
	}
}

// isTSWatched reports whether a module-relative path is one Orbit's
// TypeScript watcher covers.
func isTSWatched(rel string) bool {
	switch filepath.Ext(rel) {
	case ".ts", ".mts", ".cts", ".tsx", ".js", ".mjs", ".cjs", ".jsx", ".json":
		return true
	}
	return false
}

// tsHooks are test hooks of an in-process server: pulled after a pull's
// diagnostics were published, types after in-memory types were adopted.
var tsHooks atomic.Pointer[tsTestHooks]

type tsTestHooks struct {
	pulled func(orbit string)
	types  func()
}
