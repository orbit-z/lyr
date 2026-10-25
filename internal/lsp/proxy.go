package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bertrandkhe/orbit/internal/config"
)

// The gopls side (PLAN.md §9 Startup steps 4–6 and Acting as gopls's
// client): finding and starting gopls, the old-gopls refusal, the overlays
// and the readiness barrier, and answering gopls's requests to its client
// (never with an error: gopls treats one as fatal).

// installGopls is the command that installs a current gopls.
const installGopls = "go install golang.org/x/tools/gopls@latest"

// goplsTool is the tool directive that pins gopls in go.mod.
const goplsTool = "golang.org/x/tools/gopls"

// minGo is the oldest Go Orbit supports, for a go.mod without a go
// directive.
const minGo = "go1.27"

// goplsProc is the running gopls.
type goplsProc struct {
	cmd     *exec.Cmd
	conn    *Conn
	stopped atomic.Bool // we shut it down: exiting isn't an error
	exited  chan struct{}
}

// lookupGopls finds gopls (PLAN.md §9 Startup step 4): the goplsPath
// initialization option, gopls on PATH, then go tool gopls when go.mod
// pins it.
func (s *Server) lookupGopls(ws *workspace) (string, []string, error) {
	s.mu.Lock()
	opt := s.goplsPath
	s.mu.Unlock()
	if opt != "" {
		p, err := exec.LookPath(opt)
		if err != nil {
			return "", nil, fmt.Errorf("goplsPath %s: %v", opt, err)
		}
		return p, nil, nil
	}
	if p, err := exec.LookPath("gopls"); err == nil {
		return p, nil, nil
	}
	if mod, err := config.ReadGoMod(ws.root); err == nil && mod.HasTool(goplsTool) {
		if goCmd, err := exec.LookPath("go"); err == nil {
			return goCmd, []string{"tool", "gopls"}, nil
		}
	}
	return "", nil, errors.New("gopls not found on PATH")
}

// startGopls starts gopls for ws, initializes it, sends the overlays and
// runs the readiness barrier. Any failure disables Go features and tells
// the user; native features keep working.
func (s *Server) startGopls(ws *workspace) {
	defer s.markReady()
	defer s.markLoaded()
	start := time.Now()
	bin, args, err := s.lookupGopls(ws)
	if err != nil {
		s.logf("%v", err)
		s.showMessage(msgError, "Orbit: "+err.Error()+", so Go features in .orbit files are off (template features keep working). Install it with: "+installGopls)
		return
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = ws.root
	cmd.Stderr = s.opts.Log
	stdin, err1 := cmd.StdinPipe()
	stdout, err2 := cmd.StdoutPipe()
	if err := errors.Join(err1, err2); err != nil {
		s.logf("gopls: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		s.logf("starting gopls: %v", err)
		s.showMessage(msgError, fmt.Sprintf("Orbit: can't start %s: %v. Go features in .orbit files are off.", bin, err))
		return
	}
	proc := &goplsProc{cmd: cmd, conn: newConn(stdout, stdin), exited: make(chan struct{})}
	go proc.conn.Run(func(m *message) { s.fromGopls(proc.conn, m) })
	go func() {
		cmd.Wait()
		close(proc.exited)
		if !proc.stopped.Load() {
			s.goplsExited()
		}
	}()
	s.mu.Lock()
	s.gopls = proc
	params := s.goplsInitParams(ws)
	s.mu.Unlock()

	// go tool gopls builds gopls on first use, which can take a minute.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	raw, err := proc.conn.Call(ctx, "initialize", params)
	cancel()
	if err != nil {
		s.logf("gopls initialize: %v", err)
		s.stopGopls()
		s.showMessage(msgError, fmt.Sprintf("Orbit: gopls failed to start (%v). Go features in .orbit files are off.", err))
		return
	}
	var res struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
		ServerInfo struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(raw, &res)
	if msg := s.refuseGopls(ws, bin, res.ServerInfo.Version); msg != "" {
		s.logf("%s", msg)
		s.stopGopls()
		s.showMessage(msgError, msg)
		return
	}
	proc.conn.Notify("initialized", map[string]any{})

	s.mu.Lock()
	s.goplsEnc = parseEncoding(res.Capabilities.PositionEncoding)
	s.goEnabled = true
	ws.openOverlays(proc.conn)
	targets := s.barrierTargets(ws)
	s.mu.Unlock()
	s.markReady()
	s.logf("gopls ready in %v (%d generated files)", time.Since(start).Round(time.Millisecond), len(targets))

	if ws.genMissing {
		go s.nudge(ws)
	}
	t := time.Now()
	s.barrier(proc.conn, targets)
	s.logf("readiness barrier: %v", time.Since(t).Round(time.Millisecond))
	// gopls diagnoses an overlay-only file when it's opened, before the
	// lazy load of its package, and publishes nothing then; it doesn't
	// diagnose it again once loaded. Re-sending the overlays (same
	// content, new version) makes it publish the real diagnostics.
	s.mu.Lock()
	if s.goEnabled && ws.gopls == proc.conn {
		for _, g := range ws.gen {
			if g.open {
				ws.pushOverlay(g)
			}
		}
	}
	s.mu.Unlock()
}

// goplsInitParams builds gopls's initialize params from the editor's
// capabilities, with UTF-16 positions, configuration, progress and
// versioned diagnostics, which the proxy handles itself.
func (s *Server) goplsInitParams(ws *workspace) map[string]any {
	caps := cloneJSON(s.rawCaps)
	setCap(caps, []string{"general", "positionEncodings"}, []string{"utf-16"})
	setCap(caps, []string{"workspace", "configuration"}, true)
	setCap(caps, []string{"window", "workDoneProgress"}, true)
	setCap(caps, []string{"textDocument", "publishDiagnostics", "versionSupport"}, true)
	uri := pathToURI(ws.root)
	return map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "orbit lsp"},
		"rootUri":          uri,
		"workspaceFolders": []any{map[string]any{"uri": uri, "name": filepath.Base(ws.root)}},
		"capabilities":     caps,
	}
}

func cloneJSON(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	b, _ := json.Marshal(m)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func setCap(caps map[string]any, path []string, v any) {
	cur := caps
	for _, k := range path[:len(path)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = v
}

var goVersionRE = regexp.MustCompile(`go1\.\d+(\.\d+)?`)

// goplsVersions reads gopls's serverInfo.version: JSON build info with the
// Go version gopls was built with and its own version.
func goplsVersions(info string) (goVersion, gopls string) {
	var bi struct {
		GoVersion string
		Version   string
		Main      struct{ Version string }
	}
	if json.Unmarshal([]byte(info), &bi) == nil {
		goVersion = bi.GoVersion
		gopls = bi.Version
		if gopls == "" {
			gopls = bi.Main.Version
		}
	}
	if goVersion == "" {
		goVersion = goVersionRE.FindString(info)
	}
	goVersion, _, _ = strings.Cut(goVersion, " ")
	return goVersion, gopls
}

// refuseGopls applies PLAN.md §9 step 5: a gopls built with a Go older
// than the project's go directive can't type-check it. It returns the
// message to show, or "".
func (s *Server) refuseGopls(ws *workspace, bin, info string) string {
	goV, goplsV := goplsVersions(info)
	want, where := minGo, "go 1.27, the oldest Go Orbit supports"
	if ws.goVersion != "" && version.IsValid("go"+ws.goVersion) {
		want, where = "go"+ws.goVersion, "go "+ws.goVersion+" in go.mod"
	}
	if !version.IsValid(goV) || version.Compare(goV, want) >= 0 {
		return ""
	}
	if goplsV == "" {
		goplsV = bin
	}
	return fmt.Sprintf("Orbit: gopls %s was built with %s, older than %s, so it can't type-check this project. "+
		"Go features in .orbit files are off (template features keep working). Install a current gopls with: %s "+
		"(orbit doctor checks it), then restart the language server.", goplsV, goV, where, installGopls)
}

// stopGopls shuts gopls down (shutdown, exit, then kill after a grace
// period) and turns Go features off.
func (s *Server) stopGopls() {
	s.mu.Lock()
	proc := s.gopls
	s.gopls = nil
	s.goEnabled = false
	if s.ws != nil {
		s.ws.gopls = nil
	}
	s.mu.Unlock()
	if proc == nil || !proc.stopped.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	proc.conn.Call(ctx, "shutdown", nil)
	cancel()
	proc.conn.Notify("exit", nil)
	select {
	case <-proc.exited:
	case <-time.After(3 * time.Second):
		proc.cmd.Process.Kill()
		<-proc.exited
	}
	proc.conn.Close()
}

// goplsExited handles gopls dying on its own.
func (s *Server) goplsExited() {
	s.mu.Lock()
	s.gopls = nil
	s.goEnabled = false
	ws := s.ws
	if ws != nil {
		ws.gopls = nil
		for _, g := range ws.gen {
			g.open = false
		}
		clear(ws.goplsDiags)
		s.publishAll(ws)
	}
	down := s.shutdown
	s.mu.Unlock()
	s.markReady()
	s.markLoaded()
	if !down {
		s.showMessage(msgError, "Orbit: gopls exited unexpectedly; Go features in .orbit files are off until the language server restarts.")
	}
}

// barrierTarget is one generated file to load: its URI and the position of
// its package name.
type barrierTarget struct {
	uri string
	pos Position
}

func (s *Server) barrierTargets(ws *workspace) []barrierTarget {
	var out []barrierTarget
	for _, g := range ws.gen {
		off := bytes.Index(g.content, []byte("\npackage "))
		if off < 0 {
			continue
		}
		out = append(out, barrierTarget{uri: g.uri, pos: g.lines.position(off+len("\npackage "), s.goplsEnc)})
	}
	return out
}

// barrier is the readiness barrier (PLAN.md §9 step 6): one
// documentHighlight per generated file, in parallel. gopls loads
// overlay-only packages lazily, one go list each, and reverse lookups miss
// importers until all are loaded; when every highlight has answered, they
// are. References wait for it; progress shows in the editor.
func (s *Server) barrier(c *Conn, targets []barrierTarget) {
	token := s.progressBegin("Orbit", fmt.Sprintf("Loading %d generated files", len(targets)))
	var done atomic.Int64
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	step := max(len(targets)/10, 1)
	for _, t := range targets {
		wg.Go(func() {
			c.Call(ctx, "textDocument/documentHighlight", map[string]any{
				"textDocument": map[string]any{"uri": t.uri}, "position": t.pos})
			if n := done.Add(1); token != "" && n%int64(step) == 0 {
				s.progress(token, map[string]any{"kind": "report", "percentage": int(n * 100 / int64(len(targets))),
					"message": fmt.Sprintf("%d/%d generated files", n, len(targets))})
			}
		})
	}
	wg.Wait()
	s.markLoaded()
	if token != "" {
		s.progress(token, map[string]any{"kind": "end", "message": "Components loaded"})
	}
}

var progressTokens atomic.Int64

// progressBegin creates a work-done progress in the editor, or returns ""
// when it doesn't support them.
func (s *Server) progressBegin(title, msg string) string {
	s.mu.Lock()
	ok := s.caps.progress
	s.mu.Unlock()
	if !ok {
		return ""
	}
	token := fmt.Sprintf("orbit-%d", progressTokens.Add(1))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Call(ctx, "window/workDoneProgress/create", map[string]any{"token": token}); err != nil {
		return ""
	}
	s.progress(token, map[string]any{"kind": "begin", "title": title, "message": msg, "percentage": 0})
	return token
}

func (s *Server) progress(token string, value map[string]any) {
	s.client.Notify("$/progress", map[string]any{"token": token, "value": value})
}

// nudge is the one-time "Run orbit generate" message (PLAN.md §9 step 2),
// shown when orbit_gen/ was missing at startup. Its action runs orbit
// generate as a separate process; the server itself writes nothing.
func (s *Server) nudge(ws *workspace) {
	const action = "Run orbit generate"
	raw, err := s.client.Call(context.Background(), "window/showMessageRequest", map[string]any{
		"type":    msgInfo,
		"message": "Run orbit generate for faster startup",
		"actions": []any{map[string]any{"title": action}},
	})
	if err != nil || isNull(raw) || decodeObj(raw).str("title") != action {
		return
	}
	cmd := exec.Command(s.orbitExecutable(), "generate")
	cmd.Dir = ws.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i] + " …"
		}
		if msg == "" {
			msg = err.Error()
		}
		s.showMessage(msgError, "Orbit: orbit generate failed: "+msg)
		return
	}
	s.showMessage(msgInfo, "Orbit: orbit generate wrote orbit_gen/.")
}

// fromGopls handles a message from gopls: requests to its client in their
// own goroutine, notifications in order.
func (s *Server) fromGopls(c *Conn, m *message) {
	if m.isRequest() {
		go func() {
			res := s.goplsRequest(m.Method, m.Params)
			if res == nil {
				res = jsonRaw("null")
			}
			c.Reply(m.ID, res, nil) // never an error (PLAN.md §9)
		}()
		return
	}
	switch m.Method {
	case "textDocument/publishDiagnostics":
		s.onGoplsDiagnostics(m.Params)
	case "$/progress":
		s.mu.Lock()
		ok := s.caps.progress
		s.mu.Unlock()
		if ok {
			s.client.Notify(m.Method, m.Params)
		}
	case "window/showMessage":
		if !generatedFileWarning(m.Params) {
			s.client.Notify(m.Method, m.Params)
		}
	case "window/logMessage":
		s.client.Notify(m.Method, m.Params)
	}
}

// generatedFileWarning reports whether a showMessage is gopls's "Warning:
// editing render.go, a generated file.", sent on each overlay's first
// didChange: the user edits the .orbit file, not render.go.
func generatedFileWarning(params jsonRaw) bool {
	msg := decodeObj(params).str("message")
	return strings.Contains(msg, "a generated file") && strings.Contains(msg, "editing")
}

// goplsRequest answers a request gopls sends its client. The result is
// never an error; unknown requests get null.
func (s *Server) goplsRequest(method string, params jsonRaw) jsonRaw {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.mu.Lock()
	caps := s.caps
	s.mu.Unlock()
	forward := func() jsonRaw {
		raw, err := s.client.Call(ctx, method, params)
		if err != nil {
			return nil
		}
		return raw
	}
	switch method {
	case "workspace/configuration":
		return s.configuration(ctx, params, caps.configuration)
	case "client/registerCapability", "client/unregisterCapability":
		forward()
		return nil
	case "window/workDoneProgress/create":
		if caps.progress {
			forward()
		}
		return nil
	case "window/showMessageRequest":
		if generatedFileWarning(params) {
			return nil
		}
		return forward()
	case "workspace/applyEdit":
		var p struct {
			Label string  `json:"label"`
			Edit  jsonRaw `json:"edit"`
		}
		json.Unmarshal(params, &p)
		s.mu.Lock()
		edit := p.Edit
		if s.ws != nil {
			edit, _ = s.mapper().mapWorkspaceEdit(p.Edit, !caps.documentChanges)
		}
		s.mu.Unlock()
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

// configuration answers workspace/configuration: the editor's answer for
// each item (or null), with diagnosticsDelay "50ms" added to the gopls
// section unless the user set it.
func (s *Server) configuration(ctx context.Context, params jsonRaw, ask bool) jsonRaw {
	var p struct {
		Items []obj `json:"items"`
	}
	json.Unmarshal(params, &p)
	answers := make([]jsonRaw, len(p.Items))
	if ask && len(p.Items) > 0 {
		s.mu.Lock()
		ws := s.ws
		items := make([]obj, len(p.Items))
		for i, it := range p.Items {
			it = maps1(it)
			if scope := it.str("scopeUri"); scope != "" && ws != nil && isGenPath(ws.rel(uriToPath(scope))) {
				it.set("scopeUri", pathToURI(ws.root)) // the editor doesn't know generated files
			}
			items[i] = it
		}
		s.mu.Unlock()
		if raw, err := s.client.Call(ctx, "workspace/configuration", map[string]any{"items": items}); err == nil {
			var res []jsonRaw
			if json.Unmarshal(raw, &res) == nil && len(res) == len(answers) {
				copy(answers, res)
			}
		}
	}
	out := make([]jsonRaw, len(p.Items))
	for i, it := range p.Items {
		v := answers[i]
		if it.str("section") == "gopls" {
			o := decodeObj(v)
			if o == nil {
				o = obj{}
			}
			if _, set := o["diagnosticsDelay"]; !set {
				o.set("diagnosticsDelay", "50ms")
			}
			v = o.marshal()
		}
		if v == nil {
			v = jsonRaw("null")
		}
		out[i] = v
	}
	b, _ := json.Marshal(out)
	return b
}
