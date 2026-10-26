// Package lsp is orbit lsp, Orbit's language server (PLAN.md §9). It
// speaks LSP over stdio to the editor, generates every .orbit file in
// memory (tolerant mode, never writing orbit_gen/), sends the generated Go
// that isn't on disk to its own gopls as overlays, proxies Go features to it with positions
// mapped through the source maps, and answers template features natively.
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/orbit-z/orbit/internal/project"
)

// Options configure Run.
type Options struct {
	// Log receives the server's log (the editor shows stderr); nil
	// discards it.
	Log io.Writer
	// Version is reported in serverInfo.
	Version string
}

// Run serves one editor session on in/out until the editor sends exit or
// closes the stream, or ctx ends.
func Run(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	s := &Server{opts: opts, cancels: map[string]context.CancelFunc{}, exited: make(chan struct{}),
		ready: make(chan struct{}), loaded: make(chan struct{}), mapped: make(chan struct{})}
	s.client = newConn(in, out)
	errc := make(chan error, 1)
	go func() { errc <- s.client.Run(s.handle) }()
	var err error
	select {
	case err = <-errc:
	case <-s.exited:
	case <-ctx.Done():
	}
	s.stopGopls()
	s.client.Close()
	return err
}

// Server is one language server session.
type Server struct {
	opts   Options
	client *Conn

	// mu guards everything below and the workspace.
	mu          sync.Mutex
	ws          *workspace
	initialized bool // initialize answered
	shutdown    bool
	caps        clientCaps
	rawCaps     map[string]any
	goplsPath   string // the goplsPath initialization option
	started     bool   // startGopls ran
	cancels     map[string]context.CancelFunc

	// gopls: the process, whether Go features are on, and gopls's
	// position encoding (UTF-16 unless its initialize result says
	// otherwise).
	gopls     *goplsProc
	goEnabled bool
	goplsEnc  encoding
	// placeholder is the URI of the empty folder gopls starts on when
	// there are many overlay-only files (PLAN.md §9 step 6), or "".
	placeholder string
	// ready is closed when gopls is running with every overlay sent, or
	// when it won't run; loaded when the readiness barrier finished.
	ready, loaded         chan struct{}
	readyOnce, loadedOnce sync.Once
	// mapped is closed once the startup typed check (disk.go) has decided
	// which generated files gopls reads from disk, or has timed out:
	// requests map positions through the generated files only after it.
	mapped     chan struct{}
	mappedOnce sync.Once

	// The typed checks after orbit_gen/ changes (disk.go): the debounce
	// timer, and whether one runs and another was asked for meanwhile.
	diskTimer                *time.Timer
	diskRunning, diskPending bool

	exited   chan struct{}
	exitOnce sync.Once
}

// clientCaps are the editor capabilities the server acts on.
type clientCaps struct {
	progress        bool // window.workDoneProgress
	configuration   bool // workspace.configuration
	watchDynamic    bool // workspace.didChangeWatchedFiles.dynamicRegistration
	documentChanges bool // workspace.workspaceEdit.documentChanges
	snippets        bool // textDocument.completion.completionItem.snippetSupport
	prepareRename   bool // textDocument.rename.prepareSupport
}

func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.opts.Log, "orbit lsp: "+format+"\n", args...)
}

func (s *Server) markReady()  { s.readyOnce.Do(func() { close(s.ready) }) }
func (s *Server) markLoaded() { s.loadedOnce.Do(func() { close(s.loaded) }) }
func (s *Server) markMapped() { s.mappedOnce.Do(func() { close(s.mapped) }) }

// waitMapped waits until positions can be mapped through the generated
// files (the startup typed check is done), when the workspace started.
func (s *Server) waitMapped(ctx context.Context) {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-s.mapped:
	case <-ctx.Done():
	}
}

// handle dispatches one message from the editor, on the reading goroutine:
// notifications are handled in order, requests in their own goroutine.
func (s *Server) handle(m *message) {
	if m.isNotification() {
		s.notification(m.Method, m.Params)
		return
	}
	switch m.Method {
	case "initialize":
		res, err := s.initialize(m.Params)
		s.client.Reply(m.ID, res, err)
		return
	case "shutdown":
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		go func() {
			s.stopGopls()
			s.client.Reply(m.ID, nil, nil)
		}()
		return
	}
	s.mu.Lock()
	ok, down := s.initialized, s.shutdown
	s.mu.Unlock()
	if !ok {
		s.client.Reply(m.ID, nil, &rpcError{Code: -32002, Message: "server not initialized"})
		return
	}
	if down {
		s.client.Reply(m.ID, nil, &rpcError{Code: codeInvalidRequest, Message: "server is shutting down"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	key := string(m.ID)
	s.mu.Lock()
	s.cancels[key] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.cancels, key)
			s.mu.Unlock()
			cancel()
		}()
		res, err := s.request(ctx, m.Method, m.Params)
		if err == nil && ctx.Err() != nil {
			err = &rpcError{Code: codeRequestCancelled, Message: "request cancelled"}
		}
		s.client.Reply(m.ID, res, err)
	}()
}

// request answers one editor request.
func (s *Server) request(ctx context.Context, method string, params jsonRaw) (any, *rpcError) {
	// Every request below may map positions through the generated files,
	// which the startup typed check can still switch to orbit_gen/'s typed
	// content.
	s.waitMapped(ctx)
	switch method {
	case "textDocument/hover":
		return s.hover(ctx, params)
	case "textDocument/definition":
		return s.definition(ctx, params)
	case "textDocument/references":
		return s.references(ctx, params)
	case "textDocument/completion":
		return s.completion(ctx, params)
	case "textDocument/signatureHelp":
		return s.signatureHelp(ctx, params)
	case "textDocument/prepareRename":
		return s.prepareRename(ctx, params)
	case "textDocument/rename":
		return s.rename(ctx, params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not supported: " + method}
}

// notification handles one editor notification.
func (s *Server) notification(method string, params jsonRaw) {
	switch method {
	case "initialized":
		s.mu.Lock()
		ws := s.ws
		s.mu.Unlock()
		if ws != nil {
			s.start(ws)
		}
	case "textDocument/didOpen":
		s.didOpen(params)
	case "textDocument/didChange":
		s.didChange(params)
	case "textDocument/didClose":
		s.didClose(params)
	case "workspace/didChangeWatchedFiles":
		s.didChangeWatchedFiles(params)
	case "workspace/didChangeConfiguration":
		if c := s.goplsConn(); c != nil {
			c.Notify(method, params)
		}
	case "window/workDoneProgress/cancel":
		if c := s.goplsConn(); c != nil {
			c.Notify(method, params)
		}
	case "$/cancelRequest":
		var p struct {
			ID jsonRaw `json:"id"`
		}
		json.Unmarshal(params, &p)
		s.mu.Lock()
		cancel := s.cancels[string(p.ID)]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case "exit":
		s.exitOnce.Do(func() { close(s.exited) })
	}
}

// initialize answers the editor's initialize: it finds the project from
// the workspace root and loads it (parse and generate every .orbit file in
// memory). gopls starts on initialized.
func (s *Server) initialize(params jsonRaw) (any, *rpcError) {
	var p struct {
		RootURI          string         `json:"rootUri"`
		RootPath         string         `json:"rootPath"`
		Capabilities     map[string]any `json:"capabilities"`
		WorkspaceFolders []struct {
			URI string `json:"uri"`
		} `json:"workspaceFolders"`
		InitializationOptions struct {
			GoplsPath string `json:"goplsPath"`
		} `json:"initializationOptions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = true
	s.rawCaps = p.Capabilities
	s.goplsPath = p.InitializationOptions.GoplsPath
	s.caps = clientCaps{
		progress:        capBool(p.Capabilities, "window", "workDoneProgress"),
		configuration:   capBool(p.Capabilities, "workspace", "configuration"),
		watchDynamic:    capBool(p.Capabilities, "workspace", "didChangeWatchedFiles", "dynamicRegistration"),
		documentChanges: capBool(p.Capabilities, "workspace", "workspaceEdit", "documentChanges"),
		snippets:        capBool(p.Capabilities, "textDocument", "completion", "completionItem", "snippetSupport"),
		prepareRename:   capBool(p.Capabilities, "textDocument", "rename", "prepareSupport"),
	}
	var dirs []string
	if d := uriToPath(p.RootURI); d != "" {
		dirs = append(dirs, d)
	}
	for _, f := range p.WorkspaceFolders {
		if d := uriToPath(f.URI); d != "" {
			dirs = append(dirs, d)
		}
	}
	if p.RootPath != "" {
		dirs = append(dirs, p.RootPath)
	}
	for _, d := range dirs {
		if root, err := findRoot(d); err == nil {
			s.load(root)
			break
		}
	}
	var renameProvider any = true
	if s.caps.prepareRename {
		renameProvider = map[string]any{"prepareProvider": true}
	}
	return map[string]any{
		"capabilities": map[string]any{
			"renameProvider":   renameProvider,
			"positionEncoding": "utf-16",
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    2, // incremental
				"save":      map[string]any{"includeText": false},
			},
			"hoverProvider":      true,
			"definitionProvider": true,
			"referencesProvider": true,
			// No resolveProvider: gopls's items are complete (docs inline,
			// no data) and so are Orbit's.
			"completionProvider": map[string]any{
				"triggerCharacters": []string{".", "<", " ", ":", `"`, "'", "{"},
			},
			"signatureHelpProvider": map[string]any{"triggerCharacters": []string{"(", ","}},
		},
		"serverInfo": map[string]any{"name": "orbit", "version": s.opts.Version},
	}, nil
}

// load loads the workspace at root (with s.mu held).
func (s *Server) load(root string) {
	ws, err := loadWorkspace(root)
	if err != nil {
		s.logf("loading %s: %v", root, err)
		s.showMessage(msgError, "Orbit: "+err.Error())
		return
	}
	s.ws = ws
	s.logf("project %s: %d .orbit files", ws.root, len(ws.proj.Files))
}

// start runs once the project is known and the editor is initialized:
// Orbit's diagnostics, the .orbit file watcher and gopls.
func (s *Server) start(ws *workspace) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.publishAll(ws)
	watch := s.caps.watchDynamic
	s.mu.Unlock()
	if watch {
		go s.client.Call(context.Background(), "client/registerCapability", map[string]any{
			"registrations": []any{map[string]any{
				"id":     "orbit-files",
				"method": "workspace/didChangeWatchedFiles",
				"registerOptions": map[string]any{
					"watchers": []any{map[string]any{"globPattern": "**/*.orbit"}},
				},
			}},
		})
	}
	go s.startupCheck(ws)
	go s.startGopls(ws)
}

// capBool reads a boolean capability at a path.
func capBool(caps map[string]any, path ...string) bool {
	var cur any = caps
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur = m[k]
	}
	b, _ := cur.(bool)
	return b
}

// LSP message types.
const (
	msgError   = 1
	msgWarning = 2
	msgInfo    = 3
)

func (s *Server) showMessage(typ int, text string) {
	s.client.Notify("window/showMessage", map[string]any{"type": typ, "message": text})
}

// workspaceFor returns the workspace for an .orbit document, loading the
// project around it when the editor's root had none.
func (s *Server) workspaceFor(uri string) *workspace {
	if s.ws != nil {
		return s.ws
	}
	abs := uriToPath(uri)
	if abs == "" || !strings.HasSuffix(abs, ".orbit") {
		return nil
	}
	root, err := findRoot(filepath.Dir(abs))
	if err != nil {
		return nil
	}
	s.load(root)
	if s.ws != nil {
		ws := s.ws
		go s.start(ws) // takes s.mu, held by the caller
	}
	return s.ws
}

func (s *Server) didOpen(params jsonRaw) {
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
			Text    string `json:"text"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.workspaceFor(p.TextDocument.URI)
	if ws == nil {
		return
	}
	path := ws.orbitPath(p.TextDocument.URI)
	if path == "" {
		return
	}
	ws.docs[path] = &document{uri: p.TextDocument.URI, version: p.TextDocument.Version}
	ws.setSource(path, p.TextDocument.Text)
	s.publishAll(ws)
}

func (s *Server) didChange(params jsonRaw) {
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []obj `json:"contentChanges"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	if ws == nil {
		return
	}
	path := ws.orbitPath(p.TextDocument.URI)
	f := ws.byPath[path]
	doc := ws.docs[path]
	if f == nil || doc == nil {
		return
	}
	doc.version = p.TextDocument.Version
	ws.setSource(path, applyChanges(f.Src, p.ContentChanges))
	s.publishAll(ws)
}

// applyChanges applies didChange content changes: incremental ones (with
// a range, in UTF-16) in order, or a full replacement.
func applyChanges(text string, changes []obj) string {
	for _, c := range changes {
		newText := c.str("text")
		r, ok := c.rng("range")
		if !ok {
			text = newText
			continue
		}
		x := newLineIndex(text)
		start, end := x.offset(r.Start, encUTF16), x.offset(r.End, encUTF16)
		if end < start {
			start, end = end, start
		}
		text = text[:start] + newText + text[end:]
	}
	return text
}

func (s *Server) didClose(params jsonRaw) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	if ws == nil {
		return
	}
	path := ws.orbitPath(p.TextDocument.URI)
	if ws.docs[path] == nil {
		return
	}
	delete(ws.docs, path)
	ws.reloadFromDisk(path)
	decl, impl := ws.genOf(path)
	for _, g := range []*genFile{decl, impl} {
		if g != nil {
			ws.recheckDisk(g)
		}
	}
	s.publishAll(ws)
}

// didChangeWatchedFiles handles the editor's file events: .orbit files
// that aren't open are reloaded from disk; everything else is forwarded to
// gopls, and generated files are re-compared with their overlays.
func (s *Server) didChangeWatchedFiles(params jsonRaw) {
	var p struct {
		Changes []struct {
			URI  string `json:"uri"`
			Type int    `json:"type"`
		} `json:"changes"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.ws
	var forward []any
	var gens []*genFile
	orbitChanged := false
	for _, c := range p.Changes {
		if ws == nil {
			forward = append(forward, c)
			continue
		}
		rel := ws.rel(uriToPath(c.URI))
		switch {
		case isOrbitSource(rel):
			if ws.docs[rel] == nil {
				ws.reloadFromDisk(rel)
				orbitChanged = true
			}
			continue
		case isGenPath(rel):
			if g := ws.gen[rel]; g != nil {
				gens = append(gens, g)
			}
		}
		forward = append(forward, c)
	}
	if len(forward) > 0 && ws != nil && ws.gopls != nil {
		ws.gopls.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": forward})
	}
	for _, g := range gens {
		ws.recheckDisk(g)
	}
	if len(gens) > 0 {
		s.scheduleDiskCheck()
	}
	if orbitChanged {
		s.publishAll(ws)
	}
}

// goplsConn returns gopls's connection when Go features are on.
func (s *Server) goplsConn() *Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws == nil || !s.goEnabled {
		return nil
	}
	return s.ws.gopls
}

// textPosition decodes TextDocumentPositionParams.
func textPosition(params jsonRaw) (obj, string, Position, bool) {
	o := decodeObj(params)
	if o == nil {
		return nil, "", Position{}, false
	}
	td := decodeObj(o["textDocument"])
	var pos Position
	if td == nil || json.Unmarshal(o["position"], &pos) != nil {
		return nil, "", Position{}, false
	}
	return o, td.str("uri"), pos, true
}

// cursor is a request's cursor resolved in the workspace.
type cursor struct {
	ws    *workspace
	f     *project.File
	lines *lineIndex
	path  string
	off   int
	reg   region
}

// resolve finds the cursor of a request on an .orbit document (with s.mu
// held).
func (s *Server) resolve(uri string, pos Position) (cursor, bool) {
	ws := s.ws
	if ws == nil {
		return cursor{}, false
	}
	path := ws.orbitPath(uri)
	f, lines := ws.file(path)
	if f == nil || lines == nil {
		return cursor{}, false
	}
	off := lines.offset(pos, encUTF16)
	return cursor{ws: ws, f: f, lines: lines, path: path, off: off, reg: classify(f, off)}, true
}

// toGopls rewrites a TextDocumentPositionParams for gopls: the generated
// file and position for the cursor (with s.mu held).
func (s *Server) toGopls(c cursor, o obj) (*genFile, jsonRaw, bool) {
	g, goOff, ok := c.ws.toGo(c.path, c.off)
	if !ok {
		return nil, nil, false
	}
	fwd := maps1(o)
	fwd.set("textDocument", map[string]any{"uri": g.uri})
	fwd.set("position", g.lines.position(goOff, s.goplsEnc))
	return g, fwd.marshal(), true
}

// callGopls sends a request to gopls once it's ready (and, for
// references, once the readiness barrier finished). It returns null when
// Go features are off, and never passes gopls's errors to the editor (it
// logs them and answers null) except cancellation.
func (s *Server) callGopls(ctx context.Context, method string, params jsonRaw, barrier bool) (jsonRaw, *rpcError) {
	raw, err := s.callGoplsErr(ctx, method, params, barrier)
	if err != nil && err.Code != codeRequestCancelled {
		s.logf("gopls %s: %s", method, err.Message)
		return nil, nil
	}
	return raw, err
}

// callGoplsErr is callGopls passing gopls's own errors through, as
// RequestFailed (rename shows them to the user).
func (s *Server) callGoplsErr(ctx context.Context, method string, params jsonRaw, barrier bool) (jsonRaw, *rpcError) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, &rpcError{Code: codeRequestCancelled, Message: "request cancelled"}
	}
	if barrier {
		select {
		case <-s.loaded:
		case <-ctx.Done():
			return nil, &rpcError{Code: codeRequestCancelled, Message: "request cancelled"}
		}
	}
	c := s.goplsConn()
	if c == nil {
		return nil, nil
	}
	raw, err := c.Call(ctx, method, params)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &rpcError{Code: codeRequestCancelled, Message: "request cancelled"}
		}
		if re, ok := err.(*rpcError); ok {
			return nil, &rpcError{Code: codeRequestFailed, Message: re.Message}
		}
		return nil, &rpcError{Code: codeRequestFailed, Message: err.Error()}
	}
	return raw, nil
}

func (s *Server) mapper() mapper { return mapper{ws: s.ws, enc: s.goplsEnc} }

// hover: native on a component tag; gopls in Go regions and on prop
// attributes (their field keys), with the range mapped back.
func (s *Server) hover(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	switch c.reg.kind {
	case regionTag:
		res := c.ws.nativeHover(c.f, c.reg, c.lines)
		s.mu.Unlock()
		return res, nil
	case regionGo, regionProp:
	default:
		s.mu.Unlock()
		return nil, nil
	}
	g, fwd, ok := s.toGopls(c, o)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := s.callGopls(ctx, "textDocument/hover", fwd, false)
	if err != nil || isNull(raw) {
		return nil, err
	}
	h := decodeObj(raw)
	if h == nil {
		return nil, nil
	}
	if ct := decodeObj(h["contents"]); ct != nil {
		ct.set("value", scrubHover(ct.str("value")))
		h["contents"] = ct.marshal()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := h.rng("range"); ok {
		if _, nr, _, ok := s.mapper().location(g.uri, r); ok && s.ws.genAt(g.abs) == g {
			h.set("range", nr)
		} else {
			delete(h, "range")
		}
	}
	return h.marshal(), nil
}

// definition: native on tags, props and .orbit imports; gopls in Go
// regions, with params mapped to the page file.
func (s *Server) definition(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	if loc, native := c.ws.nativeDefinition(c.f, c.reg); native {
		s.mu.Unlock()
		if loc == nil {
			return nil, nil
		}
		return loc, nil
	}
	if c.reg.kind != regionGo && c.reg.kind != regionProp {
		s.mu.Unlock()
		return nil, nil
	}
	_, fwd, ok := s.toGopls(c, o)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := s.callGopls(ctx, "textDocument/definition", fwd, false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mapper()
	return m.mapLocations(raw, m.paramsFallback), nil
}

// references: gopls, once the readiness barrier finished (before that it
// would miss importers of overlay-only packages), with every location
// mapped; glue and echo lines drop out.
func (s *Server) references(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok || c.reg.kind != regionGo && c.reg.kind != regionProp {
		s.mu.Unlock()
		return nil, nil
	}
	_, fwd, ok := s.toGopls(c, o)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := s.callGopls(ctx, "textDocument/references", fwd, true)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mapper().mapLocations(raw, nil), nil
}

var (
	// directiveLine is a line directive gopls shows as a doc comment: the
	// inline /*line …*/ before a frontmatter statement (§3.3) becomes the
	// doc of the variable it declares.
	directiveLine = regexp.MustCompile(`(?m)^line \S+:\d+(:\d+)?[ \t]*\n?`)
	// genDocLink is gopls's pkg.go.dev link for a generated package.
	genDocLink = regexp.MustCompile(`\[[^\]]*\]\(https://pkg\.go\.dev/[^)]*/orbit_gen/[^)]*\)`)
	// emptySection is a markdown rule left with nothing after it.
	emptySection = regexp.MustCompile(`(\s*\n---\s*)+$`)
)

// scrubHover removes generated-code details from gopls's hover markdown:
// line directive text and links to orbit_gen packages.
func scrubHover(v string) string {
	v = directiveLine.ReplaceAllString(v, "")
	v = genDocLink.ReplaceAllString(v, "")
	for {
		w := emptySection.ReplaceAllString(strings.TrimRight(v, " \t\n"), "")
		if w == v {
			return v
		}
		v = w
	}
}
