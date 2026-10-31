package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/config"
)

// The gopls side (PLAN.md §9 Startup steps 4–6 and Acting as gopls's
// client): finding and starting gopls, the old-gopls refusal, the overlays
// and the readiness barrier, and answering gopls's requests to its client
// (never with an error: gopls treats one as fatal).

// installGopls is the command that installs a current gopls.
const installGopls = "go install golang.org/x/tools/gopls@latest"

// goplsTool is the tool directive that pins gopls in go.mod.
const goplsTool = "golang.org/x/tools/gopls"

// minGo is the oldest Go Lyr supports, for a go.mod without a go
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
		s.showMessage(msgError, "Lyr: "+err.Error()+", so Go features in .lyr files are off (template features keep working). Install it with: "+installGopls)
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
		s.showMessage(msgError, fmt.Sprintf("Lyr: can't start %s: %v. Go features in .lyr files are off.", bin, err))
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
	// With many overlay-only files, gopls starts on an empty placeholder
	// folder and gets the project's folder once the overlays are open.
	placeholder := ""
	if ws.overlayOnlyCount() >= reloadMin {
		if dir, err := os.MkdirTemp("", "lyr-lsp-"); err == nil {
			placeholder = dir
			s.placeholder = pathToURI(dir)
			defer os.RemoveAll(dir)
		} else {
			s.logf("gopls placeholder folder: %v", err)
		}
	}
	folder := ws.root
	if placeholder != "" {
		folder = placeholder
	}
	params := s.goplsInitParams(ws, folder)
	s.mu.Unlock()

	// go tool gopls builds gopls on first use, which can take a minute.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	raw, err := proc.conn.Call(ctx, "initialize", params)
	cancel()
	if err != nil {
		s.logf("gopls initialize: %v", err)
		s.stopGopls()
		s.showMessage(msgError, fmt.Sprintf("Lyr: gopls failed to start (%v). Go features in .lyr files are off.", err))
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

	// Overlays go only where the typed check found nothing on disk.
	<-s.mapped
	s.mu.Lock()
	s.goplsEnc = parseEncoding(res.Capabilities.PositionEncoding)
	s.goEnabled = true
	ws.openOverlays(proc.conn)
	targets := s.barrierTargets(ws)
	overlays := 0
	for _, g := range ws.gen {
		if g.open {
			overlays++
		}
	}
	// A workspace load misses the packages that exist only as overlays
	// sent after it started, and gopls then loads each one lazily with its
	// own go list (~25 ms each at 200 files, serially). With many of them,
	// the project's folder is added only now, so that its one workspace
	// load sees them all. gopls ran on the placeholder folder meanwhile:
	// with the project's folder there, each didOpen costs gopls time
	// proportional to the overlays already open (a lazy load of the file's
	// package, a snapshot clone), and its initial load (~0.2 s) would only
	// delay the folder change. Without a placeholder (overlay-only files
	// found only by the typed check), re-adding the folder reloads it.
	reload := placeholder != "" || len(targets) >= reloadMin
	if reload {
		root := map[string]any{"uri": pathToURI(ws.root), "name": filepath.Base(ws.root)}
		old := root
		if placeholder != "" {
			old = map[string]any{"uri": pathToURI(placeholder), "name": filepath.Base(placeholder)}
		}
		proc.conn.Notify("workspace/didChangeWorkspaceFolders", map[string]any{
			"event": map[string]any{"removed": []any{old}, "added": []any{root}}})
	}
	s.mu.Unlock()
	s.markReady()
	reloaded := ""
	switch {
	case placeholder != "":
		reloaded = "; project folder added after them"
	case reload:
		reloaded = "; workspace reloaded"
	}
	s.logf("gopls ready in %v (%d generated files, %d overlays, %d of them overlay-only%s)", time.Since(start).Round(time.Millisecond),
		len(ws.gen), overlays, len(targets), reloaded)
	t := time.Now()
	loaded := s.barrier(proc.conn, ws, targets)
	s.logf("readiness barrier: %v (%d packages loaded one by one)", time.Since(t).Round(time.Millisecond), len(loaded))
	// gopls diagnoses an overlay-only file when it's opened, before the
	// lazy load of its package, and publishes nothing then; it doesn't
	// diagnose it again once loaded. Re-sending those overlays (same
	// content, new version) makes it publish the real diagnostics. A
	// workspace load diagnoses what it loads.
	s.mu.Lock()
	if s.goEnabled && ws.gopls == proc.conn {
		for _, t := range loaded {
			if g := ws.gen[t.rel]; g != nil && g.open {
				ws.pushOverlay(g)
			}
		}
	}
	s.mu.Unlock()
}

// reloadMin is the number of overlay-only generated files from which
// startup reloads gopls's workspace rather than letting gopls load each
// one lazily: about 16 lazy loads cost one workspace load.
const reloadMin = 16

// barrierParallel bounds the barrier's lazy loads in flight: gopls runs
// them one at a time anyway, and requests for the open documents
// shouldn't queue behind hundreds of them.
const barrierParallel = 4

// goplsInitParams builds gopls's initialize params from the editor's
// capabilities, with UTF-16 positions, configuration, progress and
// versioned diagnostics, which the proxy handles itself.
func (s *Server) goplsInitParams(ws *workspace, folder string) map[string]any {
	caps := cloneJSON(s.rawCaps)
	setCap(caps, []string{"general", "positionEncodings"}, []string{"utf-16"})
	setCap(caps, []string{"workspace", "configuration"}, true)
	setCap(caps, []string{"window", "workDoneProgress"}, true)
	setCap(caps, []string{"textDocument", "publishDiagnostics", "versionSupport"}, true)
	uri := pathToURI(folder)
	return map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "lyr lsp"},
		"rootUri":          uri,
		"workspaceFolders": []any{map[string]any{"uri": uri, "name": filepath.Base(folder)}},
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
	want, where := minGo, "go 1.27, the oldest Go Lyr supports"
	if ws.goVersion != "" && version.IsValid("go"+ws.goVersion) {
		want, where = "go"+ws.goVersion, "go "+ws.goVersion+" in go.mod"
	}
	if !version.IsValid(goV) || version.Compare(goV, want) >= 0 {
		return ""
	}
	if goplsV == "" {
		goplsV = bin
	}
	return fmt.Sprintf("Lyr: gopls %s was built with %s, older than %s, so it can't type-check this project. "+
		"Go features in .lyr files are off (template features keep working). Install a current gopls with: %s "+
		"(lyr doctor checks it), then restart the language server.", goplsV, goV, where, installGopls)
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
		s.showMessage(msgError, "Lyr: gopls exited unexpectedly; Go features in .lyr files are off until the language server restarts.")
	}
}

// barrierTarget is one overlay-only generated file to load: its path, URI,
// package path and the position of its package name.
type barrierTarget struct {
	rel, uri, pkg string
	pos           Position
}

// barrierTargets returns the generated files gopls has as overlays only
// (no file on disk), the open documents' first (with s.mu held). The
// others are in gopls's initial workspace load.
func (s *Server) barrierTargets(ws *workspace) []barrierTarget {
	var first, rest []barrierTarget
	for _, rel := range slices.Sorted(maps.Keys(ws.gen)) {
		g := ws.gen[rel]
		if !g.open {
			continue
		}
		if _, err := os.Stat(g.abs); err == nil {
			continue
		}
		off := bytes.Index(g.content, []byte("\npackage "))
		if off < 0 {
			continue
		}
		t := barrierTarget{rel: rel, uri: g.uri, pkg: ws.module + "/" + path.Dir(rel),
			pos: g.lines.position(off+len("\npackage "), s.goplsEnc)}
		if ws.docs[g.lyr] != nil {
			first = append(first, t)
		} else {
			rest = append(rest, t)
		}
	}
	return append(first, rest...)
}

// barrier is the readiness barrier (PLAN.md §9 step 6). gopls loads an
// overlay-only package lazily, with its own go list, and reverse lookups
// miss importers until every one is loaded. The barrier asks gopls which
// packages under lyr_gen/ it knows (gopls.packages, which waits for the
// workspace load), then loads each missing target with a
// documentHighlight, the open documents' first, a few at a time. It
// returns the targets it loaded that way. References and rename wait for
// it; progress shows in the editor.
//
// The barrier never type-checks the packages gopls already knows: gopls
// keeps an open file's type-checked package with the imports of the
// request that checked it, so one request per overlay, each in its own
// type-checking batch, cost ~7 MB per overlay (3 GB at 200 files) when
// gopls's file cache was warm.
func (s *Server) barrier(c *Conn, ws *workspace, targets []barrierTarget) []barrierTarget {
	defer s.markLoaded()
	if len(targets) == 0 {
		return nil
	}
	token := s.progressBegin("Lyr", fmt.Sprintf("Loading %d generated files", len(targets)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	missing := targets
	if known, ok := s.goplsPackages(ctx, c, ws); ok {
		missing = slices.DeleteFunc(slices.Clone(targets), func(t barrierTarget) bool { return known[t.pkg] })
	}
	var done atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, barrierParallel)
	step := max(len(missing)/10, 1)
	for _, t := range missing {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			c.Call(ctx, "textDocument/documentHighlight", map[string]any{
				"textDocument": map[string]any{"uri": t.uri}, "position": t.pos})
			if n := done.Add(1); token != "" && n%int64(step) == 0 {
				s.progress(token, map[string]any{"kind": "report", "percentage": int(n * 100 / int64(len(missing))),
					"message": fmt.Sprintf("%d/%d generated files", n, len(missing))})
			}
		})
	}
	wg.Wait()
	s.markLoaded()
	if token != "" {
		s.progress(token, map[string]any{"kind": "end", "message": "Components loaded"})
	}
	return missing
}

// internalToken is the work-done token of Lyr's own gopls commands: their
// progress isn't forwarded to the editor.
const internalToken = "lyr-internal"

// goplsPackages returns the package paths under lyr_gen/ that gopls has
// loaded, after its workspace load; ok is false if gopls can't tell (a
// gopls without the gopls.packages command).
func (s *Server) goplsPackages(ctx context.Context, c *Conn, ws *workspace) (map[string]bool, bool) {
	raw, err := c.Call(ctx, "workspace/executeCommand", map[string]any{
		"command":       "gopls.packages",
		"arguments":     []any{map[string]any{"Files": []string{pathToURI(filepath.Join(ws.root, compiler.GenDir))}, "Recursive": true}},
		"workDoneToken": internalToken,
	})
	if err != nil {
		s.logf("gopls.packages: %v", err)
		return nil, false
	}
	var res struct {
		Packages []struct{ Path string }
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, false
	}
	known := make(map[string]bool, len(res.Packages))
	for _, p := range res.Packages {
		known[p.Path] = true
	}
	return known, true
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
	token := fmt.Sprintf("lyr-%d", progressTokens.Add(1))
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
		if ok && decodeObj(m.Params).str("token") != internalToken {
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
// didChange: the user edits the .lyr file, not render.go.
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
			if scope := it.str("scopeUri"); scope != "" && ws != nil && (scope == s.placeholder || isGenPath(ws.rel(uriToPath(scope)))) {
				it.set("scopeUri", pathToURI(ws.root)) // the editor knows neither generated files nor the placeholder
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
			// Lyr's gopls sees only the project's module, so it needs no
			// zero-config views for open files, whose selection costs each
			// didOpen time proportional to the files already open (it walks
			// each one's directories): ~1 s over 400 overlays.
			if _, set := o["zeroConfig"]; !set {
				o.set("zeroConfig", false)
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
