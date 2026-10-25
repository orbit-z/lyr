package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/project"
)

// Rename (PLAN.md §9 Request routing, rename): Go identifiers go to gopls
// once the readiness barrier finished, and its WorkspaceEdit is mapped edit
// by edit through the source maps of every generated file it touches.
// Edits on echo lines are dropped; a field key (a span segment) becomes the
// call site's attribute in its own spelling; any other edit on generated
// code rejects the whole rename. Import aliases are renamed natively (the
// alias and its tags), with gopls renaming their Go uses file by file.
// prepareRename is gated by Orbit's routing and by where the identifier is
// declared (gopls would also rename glue such as orbit__w or props).

// codeRequestFailed is LSP's RequestFailed: the editor shows the message.
const codeRequestFailed = -32803

func renameErr(format string, args ...any) *rpcError {
	return &rpcError{Code: codeRequestFailed, Message: fmt.Sprintf(format, args...)}
}

func errChanged() *rpcError {
	return renameErr("the files changed during the rename; try again")
}

// renameAt is where a rename starts in gopls's view.
type renameAt struct {
	path  string // the .orbit file
	g     *genFile
	goOff int
	fwd   jsonRaw // TextDocumentPositionParams for gopls
	// attr is the component attribute the rename starts on, nil in Go
	// code: its new name is an attribute spelling.
	attr *compiler.Attr
	rev  int
}

// renameStart resolves the cursor of prepareRename or rename in a Go
// region (an exact segment: the user's own code) or on a prop attribute
// (its field-key span), with s.mu held.
func (s *Server) renameStart(c cursor) (renameAt, bool) {
	var kind compiler.SegmentKind
	switch c.reg.kind {
	case regionGo:
		kind = compiler.SegExact
	case regionProp:
		kind = compiler.SegSpan
	default:
		return renameAt{}, false
	}
	g, goOff, ok := c.ws.toGoKind(c.path, c.off, kind)
	if !ok {
		return renameAt{}, false
	}
	fwd, _ := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": g.uri},
		"position":     g.lines.position(goOff, s.goplsEnc),
	})
	at := renameAt{path: c.path, g: g, goOff: goOff, fwd: fwd, rev: c.ws.rev}
	if c.reg.kind == regionProp {
		at.attr = c.reg.attr
	}
	return at, true
}

// toGoKind is toGo restricted to segments of one kind.
func (ws *workspace) toGoKind(p string, off int, kind compiler.SegmentKind) (*genFile, int, bool) {
	decl, impl := ws.genOf(p)
	var best *genFile
	var seg compiler.Segment
	rank := 0
	for _, g := range []*genFile{impl, decl} {
		if g == nil || g.smap == nil {
			continue
		}
		for _, s := range g.smap.Segments {
			if s.Kind != kind || off < s.OrbitStart || off > s.OrbitEnd {
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

// renameTarget is what the identifier at a renameAt declares, as far as
// rename cares.
type renameTarget struct {
	prop bool // a Props field
	// aliasPath and alias are set when it's an .orbit import alias (used
	// in Go code, as in Card.Props).
	aliasPath, alias string
}

// renameTarget asks gopls where the identifier is declared, and refuses
// identifiers that Orbit's generated code declares (glue such as props or
// orbit__w, a page's Params fields) and the Props type itself.
func (s *Server) renameTarget(ctx context.Context, at renameAt) (renameTarget, *rpcError) {
	raw, err := s.callGoplsErr(ctx, "textDocument/definition", at.fwd, true)
	if err != nil {
		if err.Code == codeRequestCancelled {
			return renameTarget{}, err
		}
		return renameTarget{}, nil // gopls's rename decides
	}
	uri, r, ok := firstLocation(raw)
	if !ok {
		return renameTarget{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	name := identAt(at.g.content, at.goOff)
	rel := ws.rel(uriToPath(uri))
	if !isGenPath(rel) {
		return renameTarget{}, nil // the user's .go files, or gopls refuses (standard library)
	}
	g := ws.gen[rel]
	if g == nil {
		return renameTarget{}, renameErr("can't rename %s: Orbit's generated code declares it", name)
	}
	off := g.lines.offset(r.Start, s.goplsEnc)
	f := ws.byPath[g.orbit]
	o, ok := g.fromGo(off)
	if !ok || f == nil || f.Analysis == nil {
		if f != nil && inParams(g, f, off) {
			return renameTarget{}, paramsErr(name, g.orbit)
		}
		return renameTarget{}, renameErr("can't rename %s: Orbit's generated code declares it", name)
	}
	an := f.Analysis
	if an.Props.Declared && o == an.Props.Span.Start {
		return renameTarget{}, renameErr("can't rename Props: Orbit finds a component's props by this name")
	}
	for _, pf := range an.Props.Fields {
		if pf.NameSpan.Contains(o) {
			return renameTarget{prop: true}, nil
		}
	}
	for _, im := range an.OrbitImports {
		if im.AliasSpan.Contains(o) {
			return renameTarget{aliasPath: g.orbit, alias: im.Alias}, nil
		}
	}
	return renameTarget{}, nil
}

// reservedErr refuses a new name the generated code reserves (the
// compiler's error).
func reservedErr(name string) *rpcError {
	if compiler.IsReservedIdent(name) {
		return renameErr("%s is reserved: names starting with orbit__ or Orbit__ are used by generated code", name)
	}
	return nil
}

func paramsErr(name, page string) *rpcError {
	return renameErr("can't rename params.%s: route params come from the file path (%s); rename the file instead", name, page)
}

// firstLocation returns the first location of a definition result
// (Location, Location[] or LocationLink[]).
func firstLocation(raw jsonRaw) (string, Range, bool) {
	if isNull(raw) {
		return "", Range{}, false
	}
	var list []obj
	if json.Unmarshal(raw, &list) != nil {
		o := decodeObj(raw)
		if o == nil {
			return "", Range{}, false
		}
		list = []obj{o}
	}
	for _, o := range list {
		if r, ok := o.rng("targetSelectionRange"); ok {
			return o.str("targetUri"), r, true
		}
		if r, ok := o.rng("range"); ok {
			return o.str("uri"), r, true
		}
	}
	return "", Range{}, false
}

// inParams reports whether off of g is inside a page's generated Params
// type (its declaration file).
func inParams(g *genFile, f *project.File, off int) bool {
	if !g.decl || f.Info.Role != compiler.RolePage {
		return false
	}
	start := bytes.Index(g.content, []byte("\ntype Params struct"))
	if start < 0 {
		return false
	}
	end := bytes.Index(g.content[start:], []byte("\n}\n"))
	return end >= 0 && off >= start && off <= start+end+2
}

// prepareRename answers textDocument/prepareRename: the range and current
// name of what's under the cursor, or an error saying why it can't be
// renamed. Go identifiers wait for the readiness barrier.
func (s *Server) prepareRename(ctx context.Context, params jsonRaw) (any, *rpcError) {
	_, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	if im, sp, ok := aliasAt(c.f, c.off); ok {
		res := map[string]any{"range": c.lines.rangeOf(sp.Start, sp.End, encUTF16), "placeholder": im.Alias}
		s.mu.Unlock()
		return res, nil
	}
	at, ok := s.renameStart(c)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	t, err := s.renameTarget(ctx, at)
	if err != nil {
		return nil, err
	}
	raw, err := s.callGoplsErr(ctx, "textDocument/prepareRename", at.fwd, true)
	if err != nil {
		return nil, s.scrubError(err)
	}
	if isNull(raw) {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	f, lines := ws.file(at.path)
	if f == nil || ws.rev != at.rev {
		return nil, errChanged()
	}
	if at.attr != nil {
		return map[string]any{"range": lines.rangeOf(at.attr.NameSpan.Start, at.attr.NameSpan.End, encUTF16),
			"placeholder": at.attr.Name}, nil
	}
	// gopls answers {range, placeholder}, a bare Range or
	// {defaultBehavior}; the identifier is in an exact segment, so its
	// .orbit range is the same text.
	o := decodeObj(raw)
	var r Range
	if rr, ok := o.rng("range"); ok {
		r = rr
	} else if json.Unmarshal(raw, &r) != nil || o["start"] == nil {
		start, end := identSpan(at.g.content, at.goOff)
		r = at.g.lines.rangeOf(start, end, s.goplsEnc)
	}
	gs, ge := s.mapper().genRange(at.g, r)
	seg, ok := renameSegment(at.g.smap, gs, ge)
	if !ok || seg.Kind != compiler.SegExact {
		return nil, renameErr("can't rename %s here", identAt(at.g.content, at.goOff))
	}
	os, oe := seg.OrbitStart+gs-seg.GoStart, seg.OrbitStart+ge-seg.GoStart
	name := f.Src[os:oe]
	if t.alias != "" {
		name = t.alias
	}
	return map[string]any{"range": lines.rangeOf(os, oe, encUTF16), "placeholder": name}, nil
}

// rename answers textDocument/rename.
func (s *Server) rename(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	newName := o.str("newName")
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	if im, _, ok := aliasAt(c.f, c.off); ok {
		path, alias := c.path, im.Alias
		s.mu.Unlock()
		return s.renameAlias(ctx, path, alias, newName)
	}
	at, ok := s.renameStart(c)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	t, err := s.renameTarget(ctx, at)
	if err != nil {
		return nil, err
	}
	if t.alias != "" {
		return s.renameAlias(ctx, t.aliasPath, t.alias, newName)
	}
	goName := newName
	var override map[attrKey]string
	if at.attr != nil {
		// Renamed from an attribute: the new name is an attribute
		// spelling, kept as typed there.
		goName = compiler.FieldName(newName)
		if !token.IsIdentifier(goName) || !token.IsExported(goName) {
			return nil, renameErr("%s can't be a prop name: it doesn't make a Go field name", newName)
		}
		override = map[attrKey]string{{at.path, at.attr.NameSpan.Start}: newName}
	} else if !token.IsIdentifier(newName) {
		return nil, renameErr("%s isn't a valid Go identifier", newName)
	}
	if err := reservedErr(goName); err != nil {
		return nil, err
	}
	if t.prop || at.attr != nil {
		if msg := compiler.UnexportedPropError(goName); msg != "" {
			return nil, renameErr("%s", msg)
		}
	}
	fwd := decodeObj(at.fwd)
	fwd.set("newName", goName)
	raw, err := s.callGoplsErr(ctx, "textDocument/rename", fwd.marshal(), true)
	if err != nil {
		return nil, s.scrubError(err)
	}
	if isNull(raw) {
		return nil, nil
	}
	s.mu.Lock()
	if s.ws.rev != at.rev {
		s.mu.Unlock()
		return nil, errChanged()
	}
	set := newEditSet()
	rm := &renameMapper{mapper: s.mapper(), override: override, touched: map[*genFile]bool{}}
	if err := rm.apply(raw, set); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	follow := rm.importFollowUps(set, goName, s.goplsEnc)
	s.mu.Unlock()
	// An import of the frontmatter can be in both generated files; gopls
	// renamed it in one (a package name is file-scoped): rename it in the
	// other too.
	var raws []jsonRaw
	for _, p := range follow {
		raw, err := s.callGoplsErr(ctx, "textDocument/rename", p, true)
		if err != nil {
			return nil, s.scrubError(err)
		}
		raws = append(raws, raw)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws.rev != at.rev {
		return nil, errChanged()
	}
	for _, raw := range raws {
		if err := rm.apply(raw, set); err != nil {
			return nil, err
		}
	}
	return set.result(s.ws, s.caps.documentChanges)
}

// importFollowUps returns the gopls rename params for the generated files
// gopls didn't edit that hold an import whose name this rename changed in
// an .orbit file (with s.mu held).
func (rm *renameMapper) importFollowUps(set *editSet, newName string, enc encoding) []jsonRaw {
	var out []jsonRaw
	for _, uri := range set.order {
		fe := set.files[uri]
		f := rm.ws.byPath[fe.orbit]
		if fe.orbit == "" || f == nil || f.Analysis == nil {
			continue
		}
		for _, im := range f.Analysis.GoImports {
			var at = -1
			for _, e := range fe.spans {
				if e.start >= im.Span.Start && e.end <= im.Span.End {
					at = e.start
				}
			}
			if at < 0 {
				continue
			}
			decl, impl := rm.ws.genOf(fe.orbit)
			for _, g := range []*genFile{decl, impl} {
				if g == nil || rm.touched[g] {
					continue
				}
				goOff, ok := goOffsetKind(g, at, compiler.SegExact)
				if !ok {
					continue
				}
				// On the import's name, or on its path when it has none.
				if im.Name == "" {
					goOff, _ = goOffsetKind(g, im.PathSpan.Start+1, compiler.SegExact)
				}
				b, _ := json.Marshal(map[string]any{
					"textDocument": map[string]any{"uri": g.uri},
					"position":     g.lines.position(goOff, enc),
					"newName":      newName,
				})
				out = append(out, b)
				rm.touched[g] = true
			}
		}
	}
	return out
}

// goOffsetKind maps an .orbit offset into g through a segment of kind.
func goOffsetKind(g *genFile, off int, kind compiler.SegmentKind) (int, bool) {
	if g.smap == nil {
		return 0, false
	}
	for _, s := range g.smap.Segments {
		if s.Kind == kind && off >= s.OrbitStart && off <= s.OrbitEnd {
			return s.GoStart + min(off-s.OrbitStart, s.GoEnd-s.GoStart), true
		}
	}
	return 0, false
}

// renameAlias renames the .orbit import alias of the file at p: the alias,
// every start and end tag that uses it, and its uses in Go code (by gopls,
// in each generated file that imports it).
func (s *Server) renameAlias(ctx context.Context, p, alias, newName string) (any, *rpcError) {
	if !token.IsIdentifier(newName) {
		return nil, renameErr("%s isn't a valid Go identifier", newName)
	}
	if msg := compiler.AliasError(newName); msg != "" {
		return nil, renameErr("%s", msg)
	}
	if err := reservedErr(newName); err != nil {
		return nil, err
	}
	s.mu.Lock()
	ws := s.ws
	f := ws.byPath[p]
	if f == nil || f.Analysis == nil {
		s.mu.Unlock()
		return nil, nil
	}
	im := findAlias(f, alias)
	if im == nil {
		s.mu.Unlock()
		return nil, errChanged()
	}
	if newName != alias {
		if taken := importNamed(f, newName); taken != "" {
			s.mu.Unlock()
			return nil, renameErr("can't rename %s to %s: %s already imports %s", alias, newName, p, taken)
		}
	}
	spans := []compiler.Span{im.AliasSpan}
	for _, c := range components(f) {
		if !c.Self && c.Name == alias {
			spans = append(spans, tagNameSpans(f.Src, c)...)
		}
	}
	type call struct {
		g      *genFile
		params jsonRaw
	}
	var calls []call
	decl, impl := ws.genOf(p)
	for _, g := range []*genFile{decl, impl} {
		if g == nil || g.smap == nil {
			continue
		}
		for _, sg := range g.smap.Segments {
			if sg.Kind == compiler.SegExact && sg.OrbitStart == im.AliasSpan.Start && sg.OrbitEnd == im.AliasSpan.End {
				b, _ := json.Marshal(map[string]any{
					"textDocument": map[string]any{"uri": g.uri},
					"position":     g.lines.position(sg.GoStart, s.goplsEnc),
					"newName":      newName,
				})
				calls = append(calls, call{g, b})
				break
			}
		}
	}
	rev := ws.rev
	s.mu.Unlock()

	raws := make([]jsonRaw, len(calls))
	if newName != alias {
		for i, c := range calls {
			raw, err := s.callGoplsErr(ctx, "textDocument/rename", c.params, false)
			if err != nil {
				return nil, s.scrubError(err)
			}
			raws[i] = raw
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws.rev != rev {
		return nil, errChanged()
	}
	set := newEditSet()
	for _, sp := range spans {
		set.add(s.ws, p, sp.Start, sp.End, newName)
	}
	for i, raw := range raws {
		if isNull(raw) {
			continue
		}
		// The generated code's own uses of the alias (Card.Render,
		// Card.Props in glue) follow from the tags: drop them.
		rm := &renameMapper{mapper: s.mapper(), lenient: calls[i].g, touched: map[*genFile]bool{}}
		if err := rm.apply(raw, set); err != nil {
			return nil, err
		}
	}
	return set.result(s.ws, s.caps.documentChanges)
}

// findAlias returns f's .orbit import with the alias, or nil.
func findAlias(f *project.File, alias string) *compiler.OrbitImport {
	for i := range f.Analysis.OrbitImports {
		if im := &f.Analysis.OrbitImports[i]; im.Alias == alias {
			return im
		}
	}
	return nil
}

// importNamed returns the path of f's import already named name, or "".
func importNamed(f *project.File, name string) string {
	if im := findAlias(f, name); im != nil {
		return im.Path
	}
	for _, im := range f.Analysis.GoImports {
		n := im.Name
		if n == "" {
			n = importBase(im.Path)
		}
		if n == name {
			return im.Path
		}
	}
	return ""
}

// importBase is the last element of an import path (the usual package
// name).
func importBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// aliasAt returns the .orbit import whose alias is at off of f: on the
// alias in the frontmatter, or on the name of a start or end tag that uses
// it; sp is that occurrence.
func aliasAt(f *project.File, off int) (im *compiler.OrbitImport, sp compiler.Span, ok bool) {
	if f.AST == nil || f.Analysis == nil {
		return nil, compiler.Span{}, false
	}
	for i := range f.Analysis.OrbitImports {
		if im := &f.Analysis.OrbitImports[i]; im.AliasSpan.Contains(off) {
			return im, im.AliasSpan, true
		}
	}
	for _, c := range components(f) {
		if c.Self {
			continue
		}
		for _, sp := range tagNameSpans(f.Src, c) {
			if sp.Contains(off) {
				if im := findAlias(f, c.Name); im != nil {
					return im, sp, true
				}
				return nil, compiler.Span{}, false
			}
		}
	}
	return nil, compiler.Span{}, false
}

// components returns f's component tags in source order.
func components(f *project.File) []*compiler.Component {
	var out []*compiler.Component
	if f.AST == nil {
		return nil
	}
	compiler.Walk(f.AST.Template, func(n compiler.Node) bool {
		if c, ok := n.(*compiler.Component); ok {
			out = append(out, c)
		}
		return true
	})
	return out
}

// tagNameSpans returns the name in c's start tag and, if it has one, in
// its end tag.
func tagNameSpans(src string, c *compiler.Component) []compiler.Span {
	out := []compiler.Span{c.NameSpan}
	if et := c.EndTag; et.End > et.Start {
		start := et.Start + len("</")
		if end := start + len(c.Name); end <= len(src) && src[start:end] == c.Name {
			out = append(out, compiler.Span{Start: start, End: end})
		}
	}
	return out
}

// attrKey identifies an attribute by file and name offset.
type attrKey struct {
	path  string
	start int
}

// renameMapper maps gopls's WorkspaceEdits of one rename into an editSet.
type renameMapper struct {
	mapper
	// override is the new spelling of the attribute renamed from.
	override map[attrKey]string
	// lenient is a generated file whose unmapped edits are dropped rather
	// than rejected (an alias rename: glue uses follow the tags).
	lenient *genFile
	// touched are the generated files gopls edited.
	touched map[*genFile]bool
}

// apply maps a WorkspaceEdit (changes or documentChanges) into set.
func (rm *renameMapper) apply(raw jsonRaw, set *editSet) *rpcError {
	o := decodeObj(raw)
	if o == nil {
		return nil
	}
	if ch, ok := o["changes"]; ok {
		var m map[string][]jsonRaw
		json.Unmarshal(ch, &m)
		for _, uri := range sortedKeys(m) {
			if err := rm.file(uri, m[uri], set); err != nil {
				return err
			}
		}
	}
	if dc, ok := o["documentChanges"]; ok {
		var list []jsonRaw
		json.Unmarshal(dc, &list)
		for _, item := range list {
			d := decodeObj(item)
			if d == nil {
				continue
			}
			if _, isEdit := d["edits"]; !isEdit {
				// A resource operation (create, rename, delete file).
				for _, k := range []string{"uri", "oldUri", "newUri"} {
					if u := d.str(k); u != "" && isGenPath(rm.ws.rel(uriToPath(u))) {
						return renameErr("can't rename: it would rename Orbit's generated files")
					}
				}
				set.ops = append(set.ops, item)
				continue
			}
			var edits []jsonRaw
			json.Unmarshal(d["edits"], &edits)
			if err := rm.file(decodeObj(d["textDocument"]).str("uri"), edits, set); err != nil {
				return err
			}
		}
	}
	return nil
}

// file maps the edits of one document.
func (rm *renameMapper) file(uri string, edits []jsonRaw, set *editSet) *rpcError {
	ws := rm.ws
	rel := ws.rel(uriToPath(uri))
	if isScratchPath(rel) {
		return nil // the LSP's own scratch files
	}
	g := ws.gen[rel]
	if g == nil {
		if isGenPath(rel) {
			return nil // a stale generated file of no .orbit file
		}
		fe := set.file(uri, "")
		for _, e := range edits {
			if rm.enc != encUTF16 {
				if eo := decodeObj(e); eo != nil {
					if r, ok := eo.rng("range"); ok {
						eo.set("range", rm.reencode(uriToPath(uri), r))
						e = eo.marshal()
					}
				}
			}
			fe.raw = append(fe.raw, e)
		}
		return nil
	}
	rm.touched[g] = true
	for _, e := range edits {
		eo := decodeObj(e)
		r, ok := eo.rng("range")
		if !ok {
			continue
		}
		if err := rm.edit(g, r, eo.str("newText"), set); err != nil {
			return err
		}
	}
	return nil
}

// edit maps one edit of generated file g.
func (rm *renameMapper) edit(g *genFile, r Range, text string, set *editSet) *rpcError {
	ws := rm.ws
	f := ws.byPath[g.orbit]
	if f == nil {
		return errChanged()
	}
	gs, ge := rm.genRange(g, r)
	seg, ok := renameSegment(g.smap, gs, ge)
	switch {
	case ok && seg.Kind == compiler.SegEcho:
		return nil // _ = name (PLAN.md §2.2)
	case ok && seg.Kind == compiler.SegExact:
		os, oe := seg.OrbitStart+gs-seg.GoStart, seg.OrbitStart+ge-seg.GoStart
		if a := shorthandAt(f, os, oe); a != nil {
			// {post} names the attribute too: keep the name, post={article}.
			code := f.Src[a.Code.Start:os] + text + f.Src[oe:a.Code.End]
			set.add(ws, g.orbit, a.Start, a.End, a.Name+"={"+code+"}")
			return nil
		}
		set.add(ws, g.orbit, os, oe, text)
		return nil
	case ok && seg.Kind == compiler.SegSpan && gs == seg.GoStart && ge == seg.GoEnd:
		if a := propAttrAt(f, seg.OrbitStart, seg.OrbitEnd); a != nil {
			name, ok := rm.override[attrKey{g.orbit, a.NameSpan.Start}]
			if !ok {
				name = attrSpelling(a.Name, string(g.content[gs:ge]), text)
			}
			if a.Kind == compiler.AttrShorthand {
				// {post} with Post renamed: article={post}.
				set.add(ws, g.orbit, a.Start, a.End, name+"={"+f.Src[a.Code.Start:a.Code.End]+"}")
			} else {
				set.add(ws, g.orbit, a.NameSpan.Start, a.NameSpan.End, name)
			}
			return nil
		}
	}
	if g == rm.lenient {
		return nil
	}
	name := string(g.content[gs:ge])
	if name == "" {
		name = identAt(g.content, gs)
	}
	if inParams(g, f, gs) {
		return paramsErr(name, g.orbit)
	}
	p := g.lines.position(gs, encUTF8)
	return renameErr("can't rename %s: it would change Orbit's generated code where it has no place in your .orbit files (%s:%d:%d)",
		name, g.rel, p.Line+1, p.Character+1)
}

// renameSegment returns the segment that holds the Go range [gs, ge]:
// exact before span before echo when an empty range sits on a boundary.
func renameSegment(m *compiler.SourceMap, gs, ge int) (compiler.Segment, bool) {
	if m == nil {
		return compiler.Segment{}, false
	}
	var best compiler.Segment
	rank := 0
	for _, s := range m.Segments {
		if s.GoStart > gs || ge > s.GoEnd {
			continue
		}
		r := map[compiler.SegmentKind]int{compiler.SegExact: 3, compiler.SegSpan: 2, compiler.SegEcho: 1}[s.Kind]
		if r > rank {
			best, rank = s, r
		}
	}
	return best, rank > 0
}

// shorthandAt returns the {name} shorthand attribute whose code holds the
// .orbit range [start, end], or nil.
func shorthandAt(f *project.File, start, end int) *compiler.Attr {
	return findAttr(f, func(_ compiler.Node, a *compiler.Attr) bool {
		return a.Kind == compiler.AttrShorthand && a.Code.Start <= start && end <= a.Code.End
	})
}

// propAttrAt returns the prop attribute of a component tag whose name is
// exactly [start, end], or nil.
func propAttrAt(f *project.File, start, end int) *compiler.Attr {
	return findAttr(f, func(n compiler.Node, a *compiler.Attr) bool {
		_, comp := n.(*compiler.Component)
		return comp && a.Directive == compiler.NotDirective && a.Kind != compiler.AttrSpread &&
			a.NameSpan.Start == start && a.NameSpan.End == end
	})
}

func findAttr(f *project.File, match func(compiler.Node, *compiler.Attr) bool) *compiler.Attr {
	if f.AST == nil {
		return nil
	}
	var out *compiler.Attr
	compiler.Walk(f.AST.Template, func(n compiler.Node) bool {
		if out != nil {
			return false
		}
		if t := compiler.TagOf(n); t != nil {
			for _, a := range t.Attrs {
				if match(n, a) {
					out = a
					return false
				}
			}
		}
		return true
	})
	return out
}

// attrSpelling returns how a call site that spelled the Props field
// oldField as attr spells the renamed field newField, keeping its style:
//
//	the field's own spelling (Post)    → newField as is
//	kebab-case (is-open)               → kebab-case, lowercase (is-closed)
//	snake_case (is_open)               → snake_case, lowercase (is_closed)
//	all lowercase, flattened (isopen)  → all lowercase (isclosed)
//	anything else (isOpen, post, ISOPEN) → lowerCamel (isClosed), the
//	  spelling completion inserts
func attrSpelling(attr, oldField, newField string) string {
	switch {
	case attr == oldField:
		return newField
	case strings.Contains(attr, "-"):
		return joinWords(newField, "-")
	case strings.Contains(attr, "_"):
		return joinWords(newField, "_")
	case attr == strings.ToLower(attr) && attr != compiler.LowerCamel(oldField):
		return strings.ToLower(newField)
	}
	return compiler.LowerCamel(newField)
}

// joinWords lowercases the words of a Go name and joins them with sep:
// IsClosed → is-closed, UserID → user-id, HTTPServer → http-server.
func joinWords(name, sep string) string {
	var words []string
	rs := []rune(name)
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		switch {
		case cur == '_':
			words = append(words, string(rs[start:i]))
			start = i + 1
			continue
		case unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev)),
			unicode.IsUpper(cur) && unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1]):
			if i > start {
				words = append(words, string(rs[start:i]))
			}
			start = i
		}
	}
	if start < len(rs) {
		words = append(words, string(rs[start:]))
	}
	words = slices.DeleteFunc(words, func(w string) bool { return w == "" })
	for i, w := range words {
		words[i] = strings.ToLower(w)
	}
	return strings.Join(words, sep)
}

// identSpan returns the Go identifier around off of src.
func identSpan(src []byte, off int) (int, int) {
	isID := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	start, end := off, off
	for start > 0 {
		r, n := utf8.DecodeLastRune(src[:start])
		if !isID(r) {
			break
		}
		start -= n
	}
	for end < len(src) {
		r, n := utf8.DecodeRune(src[end:])
		if !isID(r) {
			break
		}
		end += n
	}
	return start, end
}

// editSet collects a rename's edits per document.
type editSet struct {
	order []string
	files map[string]*fileEdits
	ops   []jsonRaw // resource operations, passed through
}

// fileEdits are the edits of one document: byte spans of an .orbit file,
// or another file's edits as gopls sent them.
type fileEdits struct {
	orbit string
	spans []spanEdit
	raw   []jsonRaw
}

type spanEdit struct {
	start, end int
	text       string
}

func newEditSet() *editSet { return &editSet{files: map[string]*fileEdits{}} }

func (e *editSet) file(uri, orbit string) *fileEdits {
	fe := e.files[uri]
	if fe == nil {
		fe = &fileEdits{orbit: orbit}
		e.files[uri] = fe
		e.order = append(e.order, uri)
	}
	return fe
}

// add adds an edit of the .orbit file at p.
func (e *editSet) add(ws *workspace, p string, start, end int, text string) {
	fe := e.file(ws.orbitURI(p), p)
	fe.spans = append(fe.spans, spanEdit{start, end, text})
}

// result returns the WorkspaceEdit: documentChanges, with the version of
// open .orbit documents (null otherwise), or changes for editors without
// documentChanges support. Duplicate edits (an import copied into both
// generated files) are merged; overlapping ones reject the rename.
func (e *editSet) result(ws *workspace, documentChanges bool) (any, *rpcError) {
	changes := map[string][]jsonRaw{}
	var dcs []any
	for _, uri := range e.order {
		fe := e.files[uri]
		edits := fe.raw
		version := jsonRaw("null")
		if fe.orbit != "" {
			lines := ws.lines[fe.orbit]
			if lines == nil {
				return nil, errChanged()
			}
			spans := slices.Clone(fe.spans)
			slices.SortStableFunc(spans, func(a, b spanEdit) int {
				if a.start != b.start {
					return a.start - b.start
				}
				return a.end - b.end
			})
			spans = slices.Compact(spans)
			for i := 1; i < len(spans); i++ {
				if spans[i].start < spans[i-1].end || spans[i].start == spans[i-1].start {
					return nil, renameErr("can't rename: conflicting edits in %s", fe.orbit)
				}
			}
			edits = nil
			for _, sp := range spans {
				b, _ := json.Marshal(map[string]any{"range": lines.rangeOf(sp.start, sp.end, encUTF16), "newText": sp.text})
				edits = append(edits, b)
			}
			if d := ws.docs[fe.orbit]; d != nil {
				version = jsonRaw(strconv.Itoa(d.version))
			}
		}
		if len(edits) == 0 {
			continue
		}
		if documentChanges {
			dcs = append(dcs, map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "edits": edits})
		} else {
			changes[uri] = edits
		}
	}
	if !documentChanges {
		if len(e.ops) > 0 {
			return nil, renameErr("can't rename: it would also rename files, which your editor doesn't support")
		}
		return map[string]any{"changes": changes}, nil
	}
	for _, op := range e.ops {
		dcs = append(dcs, op)
	}
	if dcs == nil {
		dcs = []any{}
	}
	return map[string]any{"documentChanges": dcs}, nil
}

// genPos is a generated file position in a gopls message.
var genPos = regexp.MustCompile(`(\S*orbit_gen/\S+?\.go):(\d+):(\d+)`)

// scrubError rewrites generated-file positions in a gopls error to the
// .orbit position they come from (or "generated code").
func (s *Server) scrubError(err *rpcError) *rpcError {
	if err == nil || err.Code == codeRequestCancelled {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	if ws == nil {
		return err
	}
	msg := genPos.ReplaceAllStringFunc(err.Message, func(m string) string {
		sub := genPos.FindStringSubmatch(m)
		abs := strings.TrimPrefix(sub[1], "file://")
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(ws.root, abs)
		}
		g := ws.genAt(abs)
		line, _ := strconv.Atoi(sub[2])
		col, _ := strconv.Atoi(sub[3])
		if g == nil || line < 1 || line > len(g.lines.starts) {
			return "generated code"
		}
		o, ok := g.fromGo(min(g.lines.starts[line-1]+col-1, len(g.content)))
		lines := ws.lines[g.orbit]
		if !ok || lines == nil {
			return "generated code"
		}
		p := lines.position(o, encUTF8)
		return fmt.Sprintf("%s:%d:%d", g.orbit, p.Line+1, p.Character+1)
	})
	return &rpcError{Code: err.Code, Message: msg}
}
