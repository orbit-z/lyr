package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/orbit-z/orbit/internal/tsengine"
)

// The TypeScript child (PLAN.md §9b "Process", "Acting as the TS child's
// client"): TypeScript 7's native language server, started lazily on the
// first .orbit document with a script, restarted once after a crash, and
// isolated from everything else: Go and template features never wait for
// it or depend on it. Like gopls, it never gets an error answer, and its
// broad file-watcher registrations aren't forwarded (Orbit forwards the
// events of a watcher of its own instead).

// tsStatus is the TypeScript child's state.
type tsStatus uint8

const (
	tsIdle     tsStatus = iota // not running; starts on demand
	tsStarting                 // a start attempt runs
	tsRunning
	tsOff // missing, too old, refused or crashed twice: off for the session
)

// tsState is the Server's TypeScript side, guarded by Server.mu.
type tsState struct {
	// path is the typescriptPath initialization option.
	path   string
	status tsStatus
	proc   *tsProc
	// ready is closed when the current start attempt ends.
	ready chan struct{}
	// crashes counts unexpected exits; notified is set once the missing-
	// engine notice was shown.
	crashes  int
	notified bool
	// all is set once every .orbit file's documents were opened, for
	// references and rename (Q7); they stay open.
	all bool
	// watching is set once Orbit's watcher of TypeScript files is
	// registered with the editor.
	watching bool
	// The in-memory tsconfig as the child has it.
	tsconfig        string
	tsconfigVersion int
	// The diagnostics to pull: the debounce timer and the virtual
	// documents (by URI) changed since the last pull.
	diagTimer *time.Timer
	dirty     map[string]bool
	// tokens maps the prefixed work-done tokens forwarded to the editor
	// back to the child's own.
	tokens map[string]jsonRaw
	// engine describes the running engine, for the log.
	engine string
}

// tsProc is the running child.
type tsProc struct {
	cmd     *exec.Cmd
	conn    *Conn
	stopped atomic.Bool
	exited  chan struct{}
}

// tsTimeout bounds a request to the child: a hung child answers null.
var tsTimeout = 10 * time.Second

// tsStartTimeout bounds the start (bun x may download TypeScript first).
var tsStartTimeout = 2 * time.Minute

// tsNoticePrefix starts the missing-engine notice.
const tsNoticePrefix = "Orbit: TypeScript features in <script> blocks need TypeScript " + "7.0.2 or later"

// tsEnsure starts the child if it's idle (with s.mu held).
func (s *Server) tsEnsure(ws *workspace) {
	if s.ts.status != tsIdle || s.shutdown || ws == nil {
		return
	}
	s.ts.status = tsStarting
	ready := make(chan struct{})
	s.ts.ready = ready
	go s.tsStart(ws, ready)
}

// tsActive reports whether the child runs or is starting (with s.mu
// held): the in-memory types are kept fresh only then.
func (s *Server) tsActive() bool { return s.ts.status == tsStarting || s.ts.status == tsRunning }

// tsStart finds and starts the engine, initializes it and opens the
// documents. Failures turn TypeScript features off with a one-time
// notice; nothing else is affected.
func (s *Server) tsStart(ws *workspace, ready chan struct{}) {
	defer close(ready)
	fail := func(msg string, notice bool) {
		s.logf("typescript: %s", msg)
		s.mu.Lock()
		s.ts.status, s.ts.proc = tsOff, nil
		show := notice && !s.ts.notified
		s.ts.notified = s.ts.notified || notice
		s.mu.Unlock()
		if show {
			s.showMessage(msgWarning, msg)
		}
	}
	s.mu.Lock()
	setting := s.ts.path
	s.mu.Unlock()
	start := time.Now()
	eng, err := findEngine(context.Background(), tsengine.Options{Setting: setting, Root: ws.root, BunX: true})
	if err != nil {
		fail(tsNoticePrefix+": "+err.Error()+". Install it with: "+tsengine.Install+"; orbit doctor checks it. "+
			"Go and template features are unaffected.", true)
		return
	}
	cmd := exec.Command(eng.Bin, eng.LSPArgs()...)
	cmd.Dir = ws.root
	cmd.Stderr = s.opts.Log
	stdin, err1 := cmd.StdinPipe()
	stdout, err2 := cmd.StdoutPipe()
	if err := errors.Join(err1, err2); err != nil {
		fail(err.Error(), false)
		return
	}
	if err := cmd.Start(); err != nil {
		fail("Orbit: can't start "+eng.Bin+": "+err.Error()+"; TypeScript features in <script> blocks are off.", true)
		return
	}
	proc := &tsProc{cmd: cmd, conn: newConn(stdout, stdin), exited: make(chan struct{})}
	go proc.conn.Run(func(m *message) { s.fromTS(proc.conn, m) })
	go func() {
		cmd.Wait()
		close(proc.exited)
		if !proc.stopped.Load() {
			s.tsExited(proc)
		}
	}()
	s.mu.Lock()
	s.ts.proc = proc
	params := s.tsInitParams(ws)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), tsStartTimeout)
	raw, err := proc.conn.Call(ctx, "initialize", params)
	cancel()
	if err != nil {
		s.stopTS()
		fail("Orbit: the TypeScript language server ("+eng.Describe()+") failed to start: "+err.Error()+
			"; TypeScript features in <script> blocks are off.", true)
		return
	}
	var res struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	json.Unmarshal(raw, &res)
	if enc := res.Capabilities.PositionEncoding; enc != "" && enc != "utf-16" {
		// Virtual documents rely on UTF-16 positions passing through;
		// TypeScript 7 accepts utf-16 (PLAN.md §9b).
		s.stopTS()
		fail("Orbit: the TypeScript language server answered with position encoding "+enc+", not utf-16; TypeScript features in <script> blocks are off.", true)
		return
	}
	proc.conn.Notify("initialized", map[string]any{})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown || s.ts.proc != proc {
		return
	}
	s.ts.status, s.ts.engine = tsRunning, eng.Describe()
	s.logf("typescript: %s ready in %v", eng.Describe(), time.Since(start).Round(time.Millisecond))
	s.tsOnReady(ws, proc.conn)
}

// findEngine is tsengine.Find on the real machine (a test hook).
var findEngine = func(ctx context.Context, opts tsengine.Options) (tsengine.Engine, error) {
	return tsengine.Find(ctx, tsengine.Default(), opts)
}

// tsInitParams builds the child's initialize params from the editor's
// capabilities (with s.mu held): UTF-16 only, configuration, client-side
// file watching (Orbit forwards its own watcher's events), and pull
// diagnostics, without which TypeScript 7 diagnoses nothing.
func (s *Server) tsInitParams(ws *workspace) map[string]any {
	caps := cloneJSON(s.rawCaps)
	setCap(caps, []string{"general", "positionEncodings"}, []string{"utf-16"})
	setCap(caps, []string{"workspace", "configuration"}, true)
	setCap(caps, []string{"workspace", "didChangeWatchedFiles", "dynamicRegistration"}, true)
	setCap(caps, []string{"workspace", "diagnostics", "refreshSupport"}, true)
	setCap(caps, []string{"textDocument", "diagnostic"}, map[string]any{"dynamicRegistration": false})
	setCap(caps, []string{"window", "workDoneProgress"}, s.caps.progress)
	uri := pathToURI(ws.root)
	return map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "orbit lsp"},
		"rootUri":          uri,
		"workspaceFolders": []any{map[string]any{"uri": uri, "name": filepath.Base(ws.root)}},
		"capabilities":     caps,
	}
}

// tsConn waits for the child (starting it if idle) and returns its
// connection, or nil when TypeScript features are off.
func (s *Server) tsConn(ctx context.Context) *Conn {
	s.mu.Lock()
	s.tsEnsure(s.ws)
	ready := s.ts.ready
	s.mu.Unlock()
	if ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ts.status != tsRunning || s.ts.proc == nil {
		return nil
	}
	return s.ts.proc.conn
}

// tsCall sends a request to the child. Its errors, a timeout or features
// being off give null (logged), never an error to the editor, except
// cancellation.
func (s *Server) tsCall(ctx context.Context, c *Conn, method string, params any) (jsonRaw, *rpcError) {
	if c == nil {
		return nil, nil
	}
	cctx, cancel := context.WithTimeout(ctx, tsTimeout)
	defer cancel()
	raw, err := c.Call(cctx, method, params)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &rpcError{Code: codeRequestCancelled, Message: "request cancelled"}
		}
		s.logf("typescript %s: %v", method, err)
		return nil, nil
	}
	return raw, nil
}

// stopTS shuts the child down.
func (s *Server) stopTS() {
	s.mu.Lock()
	proc := s.ts.proc
	s.ts.proc = nil
	if s.ts.status == tsRunning || s.ts.status == tsStarting {
		s.ts.status = tsIdle
	}
	s.mu.Unlock()
	if proc == nil || !proc.stopped.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	proc.conn.Call(ctx, "shutdown", nil)
	cancel()
	proc.conn.Notify("exit", nil)
	select {
	case <-proc.exited:
	case <-time.After(2 * time.Second):
		proc.cmd.Process.Kill()
		<-proc.exited
	}
	proc.conn.Close()
}

// tsExited handles the child dying on its own: its documents are dropped;
// after the first crash it restarts on the next demand, after the second
// it stays off with a notice.
func (s *Server) tsExited(proc *tsProc) {
	s.mu.Lock()
	if s.ts.proc != proc {
		s.mu.Unlock()
		return
	}
	s.ts.proc = nil
	s.ts.crashes++
	again := s.ts.crashes == 1
	if again {
		s.ts.status = tsIdle
	} else {
		s.ts.status = tsOff
	}
	ws := s.ws
	if ws != nil {
		ws.tsDropAll()
		s.ts.tsconfig = ""
		s.publishAll(ws)
	}
	down := s.shutdown
	s.mu.Unlock()
	switch {
	case down:
	case again:
		s.logf("typescript: the language server exited unexpectedly; it restarts on the next request")
	default:
		s.logf("typescript: the language server exited again; TypeScript features are off")
		s.showMessage(msgWarning, "Orbit: the TypeScript language server exited again, so TypeScript features in <script> blocks are off until the language server restarts.")
	}
}

// tsDropAll forgets what the child had open (it exited).
func (ws *workspace) tsDropAll() {
	for _, docs := range ws.vdocs {
		for _, d := range docs {
			d.open, d.diags = false, nil
		}
	}
	clear(ws.tsOverlays)
}

// fromTS handles a message from the child: requests to its client in
// their own goroutine, notifications in order.
func (s *Server) fromTS(c *Conn, m *message) {
	if m.isRequest() {
		go func() {
			res := s.tsChildRequest(c, m.Method, m.Params)
			if res == nil {
				res = jsonRaw("null")
			}
			c.Reply(m.ID, res, nil) // never an error
		}()
		return
	}
	switch m.Method {
	case "$/progress":
		o := decodeObj(m.Params)
		if o == nil {
			return
		}
		s.mu.Lock()
		ok := s.caps.progress
		s.mu.Unlock()
		if ok {
			o["token"] = tsToken(o["token"])
			s.client.Notify(m.Method, o.marshal())
		}
	case "window/showMessage":
		s.client.Notify(m.Method, m.Params)
	case "window/logMessage":
		o := decodeObj(m.Params)
		var typ int
		json.Unmarshal(o["type"], &typ)
		if typ == msgError || typ == msgWarning {
			s.logf("typescript: %s", o.str("message"))
		}
	}
	// publishDiagnostics: Orbit pulls diagnostics (tssync.go); the child
	// pushes only for its tsconfig.
}

// tsToken prefixes a child's work-done token so it can't collide with
// gopls's in the editor.
func tsToken(raw jsonRaw) jsonRaw {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = strings.TrimSpace(string(raw))
	}
	b, _ := json.Marshal("ts:" + s)
	return b
}

// tsChildRequest answers a request the child sends its client; unknown
// requests get null.
func (s *Server) tsChildRequest(c *Conn, method string, params jsonRaw) jsonRaw {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.mu.Lock()
	caps := s.caps
	s.mu.Unlock()
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []jsonRaw `json:"items"`
		}
		json.Unmarshal(params, &p)
		out := make([]jsonRaw, len(p.Items))
		if caps.configuration && len(p.Items) > 0 {
			if raw, err := s.client.Call(ctx, method, params); err == nil {
				var res []jsonRaw
				if json.Unmarshal(raw, &res) == nil && len(res) == len(out) {
					copy(out, res)
				}
			}
		}
		for i := range out {
			if isNull(out[i]) {
				out[i] = jsonRaw("{}")
			}
		}
		b, _ := json.Marshal(out)
		return b
	case "client/registerCapability", "client/unregisterCapability":
		key := "registrations"
		if method == "client/unregisterCapability" {
			key = "unregisterations" // sic, LSP
		}
		o := decodeObj(params)
		var regs []obj
		json.Unmarshal(o[key], &regs)
		var keep []obj
		for _, r := range regs {
			if r.str("method") == "workspace/didChangeWatchedFiles" {
				continue // broad globs (<root>/**/*, /tmp/**/*); Orbit watches instead
			}
			r.set("id", "ts:"+r.str("id"))
			keep = append(keep, r)
		}
		if len(keep) > 0 {
			o.set(key, keep)
			s.client.Call(ctx, method, o.marshal())
		}
		return nil
	case "window/workDoneProgress/create":
		if caps.progress {
			o := decodeObj(params)
			if o != nil {
				o["token"] = tsToken(o["token"])
				s.client.Call(ctx, method, o.marshal())
			}
		}
		return nil
	case "workspace/diagnostic/refresh":
		s.mu.Lock()
		if ws := s.ws; ws != nil {
			s.tsDirtyAll(ws)
		}
		s.mu.Unlock()
		return nil
	case "window/showMessageRequest":
		raw, err := s.client.Call(ctx, method, params)
		if err != nil {
			return nil
		}
		return raw
	case "workspace/applyEdit":
		var p struct {
			Label string  `json:"label"`
			Edit  jsonRaw `json:"edit"`
		}
		json.Unmarshal(params, &p)
		s.mu.Lock()
		var edit jsonRaw
		if ws := s.ws; ws != nil {
			edit, _ = s.tsMapper(ws).workspaceEdit(p.Edit, false)
		}
		s.mu.Unlock()
		if edit == nil {
			return jsonRaw(`{"applied":false}`)
		}
		raw, err := s.client.Call(ctx, method, map[string]any{"label": p.Label, "edit": edit})
		if err != nil || isNull(raw) {
			return jsonRaw(`{"applied":false}`)
		}
		return raw
	case "workspace/workspaceFolders":
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.ws == nil {
			return nil
		}
		b, _ := json.Marshal([]any{map[string]any{"uri": pathToURI(s.ws.root), "name": filepath.Base(s.ws.root)}})
		return b
	}
	return nil
}
