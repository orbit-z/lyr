package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type doc struct {
	path    string
	uri     string
	text    string
	version int
}

type session struct {
	c    *client
	r    *report
	enc  string
	docs map[string]*doc // by short name
}

func clientCaps(encodings []string, documentChanges bool) map[string]any {
	general := map[string]any{}
	if encodings != nil {
		general["positionEncodings"] = encodings
	}
	caps := map[string]any{
		"general": general,
		"workspace": map[string]any{
			"configuration":          true,
			"workspaceFolders":       true,
			"workspaceEdit":          map[string]any{"documentChanges": documentChanges, "resourceOperations": []string{"create", "rename", "delete"}},
			"didChangeWatchedFiles":  map[string]any{"dynamicRegistration": true, "relativePatternSupport": true},
			"didChangeConfiguration": map[string]any{"dynamicRegistration": true},
			"applyEdit":              true,
		},
		"textDocument": map[string]any{
			"synchronization": map[string]any{"dynamicRegistration": true, "didSave": true},
			"completion": map[string]any{
				"completionItem": map[string]any{
					"snippetSupport":          true,
					"insertReplaceSupport":    true,
					"labelDetailsSupport":     true,
					"documentationFormat":     []string{"markdown", "plaintext"},
					"resolveSupport":          map[string]any{"properties": []string{"documentation", "detail", "additionalTextEdits"}},
					"insertTextModeSupport":   map[string]any{"valueSet": []int{1, 2}},
					"commitCharactersSupport": true,
				},
				"contextSupport": true,
			},
			"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			"definition":         map[string]any{"linkSupport": false},
			"rename":             map[string]any{"prepareSupport": true},
			"publishDiagnostics": map[string]any{"relatedInformation": true, "versionSupport": true, "tagSupport": map[string]any{"valueSet": []int{1, 2}}},
		},
		"window": map[string]any{"workDoneProgress": true, "showMessage": map[string]any{}},
	}
	if capsHook != nil {
		capsHook(caps)
	}
	return caps
}

// capsHook lets a session tweak the client capabilities.
var capsHook func(map[string]any)

func newSession(gopls, logName string, encodings []string, documentChanges bool, env []string) (*session, json.RawMessage, error) {
	c, err := startClientWith(gopls, logName, env)
	if err != nil {
		return nil, nil, err
	}
	s := &session{c: c, r: &report{}, enc: "utf-16", docs: map[string]*doc{}}
	init, _, err := c.call("initialize", map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "orbit-spike"},
		"rootUri":          uriOf(projectDir),
		"workspaceFolders": []any{map[string]any{"uri": uriOf(projectDir), "name": "blog"}},
		"capabilities":     clientCaps(encodings, documentChanges),
	})
	if err != nil {
		return nil, nil, err
	}
	var ir struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	json.Unmarshal(init, &ir)
	if ir.Capabilities.PositionEncoding != "" {
		s.enc = ir.Capabilities.PositionEncoding
	}
	c.notify("initialized", map[string]any{})
	return s, init, nil
}

func startClientWith(gopls, logName string, env []string) (*client, error) {
	return startClient(gopls, projectDir, filepath.Join(spikeDir, "results", logName), env)
}

func (s *session) open(name, path, text string) {
	d := &doc{path: path, uri: uriOf(path), text: text, version: 1}
	s.docs[name] = d
	s.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": d.uri, "languageId": "go", "version": d.version, "text": text}})
}

func (s *session) change(name, text string) {
	d := s.docs[name]
	d.version++
	d.text = text
	s.c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": d.uri, "version": d.version},
		"contentChanges": []any{map[string]any{"text": text}},
	})
}

func (s *session) tdp(name string, p position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": s.docs[name].uri}, "position": p}
}

// waitVersion waits for a diagnostics publication for doc name at its current version.
func (s *session) waitVersion(from int, name string, timeout time.Duration) (diagEvent, bool) {
	d := s.docs[name]
	return s.c.waitDiag(from, d.uri, timeout, func(e diagEvent) bool { return e.version == d.version })
}

// settle waits until no new diagnostics arrived for quiet.
func (s *session) settle(quiet, max time.Duration) {
	deadline := time.Now().Add(max)
	n := s.c.diagMark()
	for time.Now().Before(deadline) {
		time.Sleep(quiet)
		m := s.c.diagMark()
		if m == n {
			return
		}
		n = m
	}
}

func (s *session) dumpDiags(title string) {
	s.r.p("%s:", title)
	ld := s.c.lastDiags()
	keys := make([]string, 0, len(ld))
	for k := range ld {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := ld[k]
		s.r.p("- `%s` v%d: %d diagnostics", rel(k), e.version, e.n)
		if e.n > 0 {
			s.r.js("  params", e.raw, 4000)
		}
	}
}

func rel(uri string) string {
	return strings.TrimPrefix(uri, uriOf(projectDir)+"/")
}

func (s *session) completion(title, name string, p position) ([]complItem, time.Duration) {
	res, d, err := s.c.call("textDocument/completion", s.tdp(name, p))
	s.r.p("### %s", title)
	s.r.p("request: `%s` at %d:%d (0-based, %s), latency %v", rel(s.docs[name].uri), p.Line, p.Character, s.enc, d.Round(time.Millisecond))
	if err != nil {
		s.r.p("ERROR: %v", err)
		return nil, d
	}
	inc, items := parseCompletion(res)
	s.r.p("isIncomplete=%v, %d items: %v", inc, len(items), labels(items, 15))
	return items, d
}

func (s *session) showItems(items []complItem, names ...string) {
	for _, n := range names {
		if it := find(items, n); it != nil {
			s.r.js("item "+n, it.Raw, 2500)
		} else {
			s.r.p("item %s: MISSING", n)
		}
	}
}

func (s *session) req(title, method string, params any, max int) json.RawMessage {
	res, d, err := s.c.call(method, params)
	s.r.p("### %s", title)
	pb, _ := json.Marshal(params)
	s.r.p("request `%s` %s (latency %v)", method, trim(pb, 300), d.Round(time.Millisecond))
	if err != nil {
		s.r.p("ERROR: %v", err)
		return nil
	}
	s.r.js("result", res, max)
	return res
}

func (s *session) serverRequests() {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	s.r.h("Server→client requests and notifications")
	keys := []string{}
	for k := range s.c.srvReqs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.r.p("- request `%s` ×%d, first params: `%s`", k, s.c.srvReqs[k], trim(s.c.srvFirst[k], 600))
	}
	keys = keys[:0]
	for k := range s.c.notifs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.r.p("- notification `%s` ×%d", k, s.c.notifs[k])
	}
}

func (s *session) openAll(gs genSet) {
	a, b, c, d := genPaths(projectDir)
	s.open("cardDecl", a, gs.cardDecl)
	s.open("cardImpl", b, gs.cardImpl)
	s.open("pageDecl", c, gs.pageDecl)
	s.open("pageImpl", d, gs.pageImpl)
}

// ---------------------------------------------------------------------------

func sessionMain(gopls string) *report {
	t0 := time.Now()
	s, init, err := newSession(gopls, "main.log", []string{"utf-8"}, true, nil)
	if err != nil {
		r := &report{}
		r.p("init failed: %v", err)
		return r
	}
	defer s.c.shutdown()
	r := s.r
	r.p("# Session main (gopls %s, overlay-only, client offers positionEncodings [utf-8], documentChanges=true)", gopls)
	r.h("5. Position encoding")
	r.p("negotiated positionEncoding: **%s**", s.enc)
	r.js("initialize result (trimmed)", init, 3000)

	_, statErr := os.Stat(filepath.Join(projectDir, "orbit_gen"))
	r.h("1. Overlay-only packages")
	r.p("orbit_gen on disk: %v (stat err: %v)", statErr == nil, statErr)
	gs := defaultGen()
	m := s.c.diagMark()
	s.openAll(gs)
	for _, n := range []string{"cardDecl", "cardImpl", "pageDecl", "pageImpl"} {
		e, ok := s.waitVersion(m, n, 60*time.Second)
		r.p("- first diagnostics for %s: ok=%v after %v, n=%d", n, ok, e.at.Sub(t0).Round(time.Millisecond), e.n)
	}
	s.settle(1500*time.Millisecond, 20*time.Second)
	s.dumpDiags("diagnostics after open (settled)")

	// Completion: props. in the template expression of the Card impl.
	r.h("2. Completion")
	impl := genCardImpl(cardImplOpts{title: "props."})
	s.change("cardImpl", impl)
	p := posOf(impl, "props./*line", 0, len("props."), s.enc)
	items, _ := s.completion("props. in Card impl template expr (`{props.}`)", "cardImpl", p)
	s.showItems(items, "Post", "Featured")
	if len(items) > 0 {
		s.r.js("first item", items[0].Raw, 1500)
	}

	impl = genCardImpl(cardImplOpts{title: "props.Fe"})
	s.change("cardImpl", impl)
	p = posOf(impl, "props.Fe/*line", 0, len("props.Fe"), s.enc)
	items, _ = s.completion("partial `props.Fe` in Card impl (replace range)", "cardImpl", p)
	s.showItems(items, "Featured")

	impl = genCardImpl(cardImplOpts{body: "url := href(props.)"})
	s.change("cardImpl", impl)
	p = posOf(impl, "href(props.", 0, len("href(props."), s.enc)
	items, _ = s.completion("`href(props.)` in Card impl frontmatter body", "cardImpl", p)
	s.showItems(items, "Post")
	s.change("cardImpl", gs.cardImpl)

	pi := s.docs["pageImpl"].text
	p = posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), s.enc)
	items, _ = s.completion("synthetic `Card.Props{|}` in page impl scratch func (cross-package overlay)", "pageImpl", p)
	s.showItems(items, "Post", "Featured")

	piFe := genPageImpl(pageImplOpts{scratch: `_ = "Héllo ✓"; _ = Card.Props{Post: posts.Post{}, Fe}`})
	s.change("pageImpl", piFe)
	p = posOf(piFe, "Fe}", 0, 2, s.enc)
	items, _ = s.completion("synthetic `Card.Props{Post: …, Fe|}`", "pageImpl", p)
	s.showItems(items, "Featured")
	s.change("pageImpl", gs.pageImpl)

	// 3. Live Props change.
	r.h("3. Live Props change (nothing on disk)")
	m = s.c.diagMark()
	piTags := genPageImpl(pageImplOpts{scratch: scratchProps, extraKey: `Tags: []string{"a"},`})
	s.change("pageImpl", piTags)
	e, ok := s.waitVersion(m, "pageImpl", 30*time.Second)
	r.p("page impl with `Tags:` key before Card has it: ok=%v n=%d", ok, e.n)
	r.js("diagnostics", e.raw, 2000)

	m = s.c.diagMark()
	tc := time.Now()
	s.change("cardDecl", genCardDecl(cardPropsTags))
	e, ok = s.c.waitDiag(m, s.docs["pageImpl"].uri, 30*time.Second, func(e diagEvent) bool { return e.n == 0 })
	r.p("after didChange of Card decl (adds `Tags []string`): page impl diagnostics cleared=%v after %v (n=%d, version %d)", ok, e.at.Sub(tc).Round(time.Millisecond), e.n, e.version)
	tc = time.Now()
	pi = s.docs["pageImpl"].text
	p = posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), s.enc)
	items, d := s.completion("`Card.Props{|}` right after the decl change", "pageImpl", p)
	r.p("Tags present: %v (latency %v)", find(items, "Tags") != nil, d.Round(time.Millisecond))
	impl = genCardImpl(cardImplOpts{title: "props."})
	s.change("cardImpl", impl)
	tc = time.Now()
	p = posOf(impl, "props./*line", 0, len("props."), s.enc)
	items, d = s.completion("`props.` in Card impl after decl change (didChange of impl immediately before)", "cardImpl", p)
	r.p("Tags present: %v (latency incl. didChange %v)", find(items, "Tags") != nil, time.Since(tc).Round(time.Millisecond))
	s.change("cardImpl", gs.cardImpl)
	s.change("pageImpl", gs.pageImpl)
	s.settle(1500*time.Millisecond, 20*time.Second)

	// Latency series: didChange then completion, 10 times.
	r.h("8b. Completion latency after didChange (warm, 10 runs)")
	var lat []time.Duration
	for i := 0; i < 10; i++ {
		txt := genPageImpl(pageImplOpts{scratch: fmt.Sprintf(`_ = "Héllo ✓"; _ = %d; _ = Card.Props{}`, i)})
		t := time.Now()
		s.change("pageImpl", txt)
		p := posOf(txt, "_ = Card.Props{}", 0, len("_ = Card.Props{"), s.enc)
		s.c.call("textDocument/completion", s.tdp("pageImpl", p))
		lat = append(lat, time.Since(t))
	}
	r.p("page impl didChange+completion: %v", lat)
	lat = lat[:0]
	for i := 0; i < 10; i++ {
		txt := genCardDecl(strings.Replace(cardPropsTags, "Tags", fmt.Sprintf("Tags%d", i), 1))
		t := time.Now()
		s.change("cardDecl", txt)
		pi := s.docs["pageImpl"].text
		p := posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), s.enc)
		res, _, _ := s.c.call("textDocument/completion", s.tdp("pageImpl", p))
		lat = append(lat, time.Since(t))
		if i == 9 {
			_, items := parseCompletion(res)
			r.p("last run sees Tags9: %v", find(items, "Tags9") != nil)
		}
	}
	r.p("Card decl didChange + completion in page impl (cross-package): %v", lat)
	s.change("cardDecl", gs.cardDecl)
	s.settle(1500*time.Millisecond, 20*time.Second)

	// 4. Diagnostics under line directives.
	r.h("4. Diagnostics under line directives")
	bad := genPageImpl(pageImplOpts{scratch: scratchProps, titleBody: `title := hoem`, h1Expr: "titel", postKey: "Psot"})
	m = s.c.diagMark()
	s.change("pageImpl", bad)
	s.waitVersion(m, "pageImpl", 30*time.Second)
	s.settle(1500*time.Millisecond, 20*time.Second)
	r.p("page impl errors: `title := hoem` (index.orbit:6:10), `{titel}` (9:5), key `Psot` (11:9)")
	r.p("expected generated-file positions (0-based utf-8): hoem=%v titel=%v Psot=%v",
		posOf(bad, "hoem", 0, 0, s.enc), posOf(bad, "titel", 0, 0, s.enc), posOf(bad, "Psot", 0, 0, s.enc))
	s.dumpDiags("all latest diagnostics")
	s.change("pageImpl", gs.pageImpl)

	badDecl := genCardDecl(strings.Replace(cardProps, "posts.Post\n", "posts.Postt\n", 1))
	m = s.c.diagMark()
	s.change("cardDecl", badDecl)
	s.waitVersion(m, "cardDecl", 30*time.Second)
	s.settle(1500*time.Millisecond, 20*time.Second)
	r.p("Card decl error: `posts.Postt` (Card.orbit:4:20); expected gen pos %v", posOf(badDecl, "Postt", 0, 0, s.enc))
	s.dumpDiags("all latest diagnostics")
	s.change("cardDecl", gs.cardDecl)
	s.settle(1500*time.Millisecond, 20*time.Second)
	s.dumpDiags("after revert")

	r.h("4b. Hover / definition / references into generated code")
	pi = s.docs["pageImpl"].text
	s.req("hover on `Featured` key in page impl", "textDocument/hover", s.tdp("pageImpl", posOf(pi, "Featured/*line", 0, 1, s.enc)), 1500)
	s.req("hover on `Props` after non-ASCII in scratch (utf-8 range check)", "textDocument/hover", s.tdp("pageImpl", posOf(pi, "Props{}", 0, 1, s.enc)), 1500)
	r.p("expected Props range start (utf-8 bytes) %v vs utf-16 %v", posOf(pi, "Props{}", 0, 0, "utf-8"), posOf(pi, "Props{}", 0, 0, "utf-16"))
	s.req("definition of `Featured` key (→ Card decl overlay)", "textDocument/definition", s.tdp("pageImpl", posOf(pi, "Featured/*line", 0, 1, s.enc)), 1500)
	s.req("definition of `title` use in `{title}` (→ same file, frontmatter span)", "textDocument/definition", s.tdp("pageImpl", posOf(pi, "title/*line", 0, 1, s.enc)), 1500)
	s.req("definition of `posts` package ident in `[]posts.Post` (→ on-disk user pkg)", "textDocument/definition", s.tdp("pageImpl", posOf(pi, "posts.Post{{", 0, 7, s.enc)), 1500)
	ci := s.docs["cardImpl"].text
	s.req("definition of `href` call in Card impl body (→ hoisted func in same overlay)", "textDocument/definition", s.tdp("cardImpl", posOf(ci, "href(props", 0, 1, s.enc)), 1500)
	cd := s.docs["cardDecl"].text
	s.req("references of `Featured` field from Card decl", "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": s.docs["cardDecl"].uri}, "position": posOf(cd, "Featured bool", 0, 1, s.enc),
		"context": map[string]any{"includeDeclaration": true}}, 3000)

	r.h("7. Rename")
	pi = s.docs["pageImpl"].text
	p = posOf(pi, `title := "`, 0, 1, s.enc)
	s.req("prepareRename on frontmatter local `title`", "textDocument/prepareRename", s.tdp("pageImpl", p), 1500)
	pr := s.tdp("pageImpl", p)
	pr["newName"] = "heading"
	s.req("rename `title` → `heading`", "textDocument/rename", pr, 4000)
	p = posOf(cd, "Featured bool", 0, 1, s.enc)
	s.req("prepareRename on Props field `Featured` (Card decl)", "textDocument/prepareRename", s.tdp("cardDecl", p), 1500)
	pr = s.tdp("cardDecl", p)
	pr["newName"] = "Highlighted"
	s.req("rename `Featured` → `Highlighted` from Card decl", "textDocument/rename", pr, 6000)
	p = posOf(pi, "Featured/*line", 0, 1, s.enc)
	pr = s.tdp("pageImpl", p)
	pr["newName"] = "Highlighted"
	s.req("rename `Featured` → `Highlighted` from the key in page impl", "textDocument/rename", pr, 6000)
	s.req("prepareRename on generated glue `orbit__w` (page impl)", "textDocument/prepareRename", s.tdp("pageImpl", posOf(pi, "orbit__w.Raw", 0, 1, s.enc)), 800)

	s.serverRequests()
	return r
}

func sessionUTF16(gopls string) *report {
	s, init, err := newSession(gopls, "utf16.log", nil, false, nil)
	if err != nil {
		r := &report{}
		r.p("init failed: %v", err)
		return r
	}
	defer s.c.shutdown()
	r := s.r
	r.p("# Session utf16 (no positionEncodings offered, documentChanges=false)")
	var ir struct {
		Capabilities struct {
			PositionEncoding *string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	json.Unmarshal(init, &ir)
	r.p("positionEncoding in initialize result: %v (nil = field absent → utf-16)", ir.Capabilities.PositionEncoding)
	gs := defaultGen()
	m := s.c.diagMark()
	s.openAll(gs)
	s.waitVersion(m, "pageImpl", 60*time.Second)
	pi := s.docs["pageImpl"].text
	s.req("hover on `Props` after non-ASCII (utf-16 range)", "textDocument/hover", s.tdp("pageImpl", posOf(pi, "Props{}", 0, 1, "utf-16")), 800)
	r.p("expected Props start utf-16 %v / utf-8 %v", posOf(pi, "Props{}", 0, 0, "utf-16"), posOf(pi, "Props{}", 0, 0, "utf-8"))
	cd := s.docs["cardDecl"].text
	pr := s.tdp("cardDecl", posOf(cd, "Featured bool", 0, 1, "utf-16"))
	pr["newName"] = "Highlighted"
	s.req("rename `Featured` → `Highlighted` (documentChanges=false), issued right after the page impl's first diagnostics", "textDocument/rename", pr, 4000)
	s.settle(1500*time.Millisecond, 20*time.Second)
	s.dumpDiags("diagnostics after settle")
	s.req("same rename after diagnostics settled", "textDocument/rename", pr, 4000)

	// Also: client offering [utf-16, utf-8] order preference.
	s2, init2, err := newSession(gopls, "utf16b.log", []string{"utf-16", "utf-8"}, false, nil)
	if err == nil {
		json.Unmarshal(init2, &ir)
		r.p("client offers [utf-16, utf-8] → positionEncoding %v", deref(ir.Capabilities.PositionEncoding))
		s2.c.shutdown()
	}
	s3, init3, err := newSession(gopls, "utf16c.log", []string{"utf-32", "utf-8"}, false, nil)
	if err == nil {
		json.Unmarshal(init3, &ir)
		r.p("client offers [utf-32, utf-8] → positionEncoding %v", deref(ir.Capabilities.PositionEncoding))
		s3.c.shutdown()
	}
	s.serverRequests()
	return r
}

func deref(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return *p
}

func sessionOld(gopls string) *report {
	home, _ := os.UserHomeDir()
	old := filepath.Join(home, "go", "bin", "gopls")
	r := &report{}
	r.p("# Session old (%s)", old)
	s, init, err := newSession(old, "old.log", []string{"utf-8"}, true, nil)
	if err != nil {
		r.p("init failed: %v", err)
		return r
	}
	defer s.c.shutdown()
	s.r = r
	var ir struct {
		ServerInfo json.RawMessage `json:"serverInfo"`
	}
	json.Unmarshal(init, &ir)
	r.p("serverInfo: %s, encoding %s", ir.ServerInfo, s.enc)
	gs := defaultGen()
	m := s.c.diagMark()
	s.openAll(gs)
	s.waitVersion(m, "pageImpl", 60*time.Second)
	s.settle(2*time.Second, 20*time.Second)
	s.dumpDiags("diagnostics")
	pi := s.docs["pageImpl"].text
	items, _ := s.completion("Card.Props{|}", "pageImpl", posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), s.enc))
	s.showItems(items, "Post")
	s.serverRequests()

	// Go 1.26 language feature (new(expr)): does the old gopls choke?
	for _, bin := range []string{old, gopls} {
		r.h("Go 1.26 feature `new(expr)` in frontmatter, gopls %s", bin)
		s2, _, err := newSession(bin, "old-newexpr.log", []string{"utf-8"}, true, nil)
		if err != nil {
			r.p("init failed: %v", err)
			continue
		}
		s2.r = r
		gs := defaultGen()
		gs.pageImpl = genPageImpl(pageImplOpts{scratch: scratchProps, titleBody: `title := *new("Héllo ✓")`})
		m := s2.c.diagMark()
		s2.openAll(gs)
		s2.waitVersion(m, "pageImpl", 60*time.Second)
		s2.settle(1500*time.Millisecond, 20*time.Second)
		e := s2.c.lastDiags()[s2.docs["pageImpl"].uri]
		r.p("page impl diagnostics: %d", e.n)
		if e.n > 0 {
			r.js("params", e.raw, 2000)
		}
		s2.c.shutdown()
	}
	return r
}

func sessionTiming(gopls string) *report {
	r := &report{}
	r.p("# Session timing")
	cache, _ := os.MkdirTemp("", "goplscache")
	defer os.RemoveAll(cache)
	for i := 0; i < 4; i++ {
		var env []string
		label := "warm gopls file cache"
		if i == 0 {
			env = append(os.Environ(), "GOPLSCACHE="+cache)
			label = "cold: empty GOPLSCACHE"
		} else {
			env = os.Environ()
		}
		t0 := time.Now()
		c, err := startClientWith(gopls, fmt.Sprintf("timing%d.log", i), env)
		if err != nil {
			r.p("start: %v", err)
			continue
		}
		s := &session{c: c, r: &report{}, enc: "utf-8", docs: map[string]*doc{}}
		_, dInit, err := c.call("initialize", map[string]any{
			"processId": os.Getpid(), "rootUri": uriOf(projectDir),
			"workspaceFolders": []any{map[string]any{"uri": uriOf(projectDir), "name": "blog"}},
			"capabilities":     clientCaps([]string{"utf-8"}, true)})
		if err != nil {
			r.p("init: %v", err)
			continue
		}
		c.notify("initialized", map[string]any{})
		s.openAll(defaultGen())
		pi := s.docs["pageImpl"].text
		res, dc, err := c.call("textDocument/completion", s.tdp("pageImpl", posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), "utf-16")))
		total := time.Since(t0)
		_, items := parseCompletion(res)
		r.p("- run %d (%s): initialize %v, first completion request %v, process start→completion %v, items=%d (Post present: %v) err=%v",
			i, label, dInit.Round(time.Millisecond), dc.Round(time.Millisecond), total.Round(time.Millisecond), len(items), find(items, "Post") != nil, err)
		c.shutdown()
	}
	return r
}

func sessionHoverDoc(gopls string) *report {
	s, _, err := newSession(gopls, "hoverdoc.log", []string{"utf-8"}, true, nil)
	r := &report{}
	if err != nil {
		r.p("init failed: %v", err)
		return r
	}
	defer s.c.shutdown()
	s.r = r
	r.p("# Session hoverdoc: do directive comments leak into hover/completion docs?")
	gs := defaultGen()
	gs.pageImpl = genPageImpl(pageImplOpts{scratch: `_ = Card.Props{}; _ = Card.`})
	m := s.c.diagMark()
	s.openAll(gs)
	s.waitVersion(m, "pageImpl", 60*time.Second)
	for _, v := range []struct{ name, props, mode string }{
		{"inline /*line*/, no user doc", cardProps, "block"},
		{"inline /*line*/, user doc comment", cardPropsDoc, "block"},
		{"own-line //line, user doc comment", cardPropsDoc, "line"},
	} {
		r.h("variant: %s", v.name)
		txt := genCardDeclMode(v.props, v.mode)
		r.p("decl.go:\n```go\n%s```", txt)
		m := s.c.diagMark()
		s.change("cardDecl", txt)
		s.c.waitDiag(m, s.docs["pageImpl"].uri, 10*time.Second, nil)
		s.settle(800*time.Millisecond, 10*time.Second)
		pi := s.docs["pageImpl"].text
		s.req("hover on `Card.Props` in page impl", "textDocument/hover", s.tdp("pageImpl", posOf(pi, "Props{}", 0, 1, "utf-16")), 1500)
		s.req("hover on `Post` field in page impl key", "textDocument/hover", s.tdp("pageImpl", posOf(pi, "Post/*line", 0, 1, "utf-16")), 1500)
		items, _ := s.completion("completion `Card.|`", "pageImpl", posOf(pi, "_ = Card.\n", 0, len("_ = Card."), "utf-16"))
		if it := find(items, "Props"); it != nil {
			r.js("item Props", it.Raw, 1500)
			s.req("completionItem/resolve Props", "completionItem/resolve", it.Raw, 1500)
		}
		r.p("diagnostics for decl: %d, page impl: %d", s.c.lastDiags()[s.docs["cardDecl"].uri].n, s.c.lastDiags()[s.docs["pageImpl"].uri].n)
	}
	ci := s.docs["cardImpl"].text
	s.req("hover on `href` call in Card impl", "textDocument/hover", s.tdp("cardImpl", posOf(ci, "href(props", 0, 1, "utf-16")), 1500)
	return r
}
