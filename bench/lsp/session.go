package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// session is one orbit lsp process driven as an editor would: it answers
// the server's requests and records when notifications arrive.
type session struct {
	dir   string
	cmd   *exec.Cmd
	c     *client
	start time.Time
	log   *bytes.Buffer // orbit lsp's stderr (its log and gopls's)
	init  map[string]any

	mu        sync.Mutex
	cond      *sync.Cond
	exited    bool
	texts     map[string]string // open documents by path relative to dir
	versions  map[string]int
	diagWait  map[string]diagCond      // diagnostic conditions by name
	diagHit   map[string]time.Duration // when each condition first held
	progTitle map[string]string        // $/progress token → begin title
	progEnd   map[string]time.Duration // title → first end
	lastDiags map[string][]diag        // latest per URI
	reqSeen   map[string]int           // server→client requests by method
}

type diag struct {
	Message string `json:"message"`
	Source  string `json:"source"`
}

// goEnv is the environment for go, orbit and gopls: module edits allowed
// (the project's go.mod has no indirect requires until the first build),
// and no go.work from the caller.
var goEnv = []string{strings.TrimSpace("GOFLAGS=" + os.Getenv("GOFLAGS") + " -mod=mod"), "GOWORK=off"}

func fileURI(p string) string { return (&url.URL{Scheme: "file", Path: filepath.ToSlash(p)}).String() }

// startSession spawns orbit lsp in dir and sends initialize. The returned
// duration is process start → initialize answered.
func startSession(orbitBin, dir string, initOpts map[string]any, env ...string) (*session, time.Duration, error) {
	s := &session{dir: dir, log: &bytes.Buffer{}, texts: map[string]string{}, versions: map[string]int{},
		diagWait: map[string]diagCond{}, diagHit: map[string]time.Duration{},
		progTitle: map[string]string{}, progEnd: map[string]time.Duration{}, lastDiags: map[string][]diag{}, reqSeen: map[string]int{}}
	s.cond = sync.NewCond(&s.mu)
	s.cmd = exec.Command(orbitBin, "lsp")
	s.cmd.Dir = dir
	s.cmd.Env = append(append(os.Environ(), goEnv...), env...)
	s.cmd.Stderr = &lockedWriter{mu: &s.mu, w: s.log}
	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return nil, 0, err
	}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	s.start = time.Now()
	if err := s.cmd.Start(); err != nil {
		return nil, 0, err
	}
	go func() {
		s.cmd.Wait()
		s.mu.Lock()
		s.exited = true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	s.c = newClient(stdout, stdin, s.onRequest, s.onNotify)
	params := map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "orbit-bench-lsp"},
		"rootUri":          fileURI(dir),
		"workspaceFolders": []any{map[string]any{"uri": fileURI(dir), "name": filepath.Base(dir)}},
		"capabilities":     clientCapabilities,
	}
	if initOpts != nil {
		params["initializationOptions"] = initOpts
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := s.c.call(ctx, "initialize", params)
	d := time.Since(s.start)
	if err != nil {
		s.kill()
		return nil, 0, fmt.Errorf("initialize: %w\n%s", err, s.logText())
	}
	if err := json.Unmarshal(res, &s.init); err != nil {
		s.kill()
		return nil, 0, fmt.Errorf("initialize result %s: %w", res, err)
	}
	if !strings.Contains(string(res), `"utf-16"`) {
		s.kill()
		return nil, 0, fmt.Errorf("initialize: no utf-16 position encoding: %s", res)
	}
	s.c.notify("initialized", map[string]any{})
	return s, d, nil
}

// clientCapabilities are what a current VS Code or Neovim client sends,
// trimmed to the parts orbit lsp and gopls look at.
var clientCapabilities = map[string]any{
	"general": map[string]any{"positionEncodings": []string{"utf-16"}},
	"workspace": map[string]any{
		"configuration":          true,
		"workspaceFolders":       true,
		"applyEdit":              true,
		"workspaceEdit":          map[string]any{"documentChanges": true, "resourceOperations": []string{"create", "rename", "delete"}},
		"didChangeWatchedFiles":  map[string]any{"dynamicRegistration": true, "relativePatternSupport": true},
		"didChangeConfiguration": map[string]any{"dynamicRegistration": true},
	},
	"textDocument": map[string]any{
		"synchronization": map[string]any{"dynamicRegistration": true, "didSave": true, "willSave": false},
		"completion": map[string]any{
			"contextSupport": true,
			"completionItem": map[string]any{
				"snippetSupport":          true,
				"insertReplaceSupport":    true,
				"labelDetailsSupport":     true,
				"deprecatedSupport":       true,
				"commitCharactersSupport": true,
				"documentationFormat":     []string{"markdown", "plaintext"},
				"resolveSupport":          map[string]any{"properties": []string{"documentation", "detail", "additionalTextEdits"}},
			},
		},
		"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
		"signatureHelp":      map[string]any{"signatureInformation": map[string]any{"documentationFormat": []string{"markdown", "plaintext"}}},
		"definition":         map[string]any{"linkSupport": false},
		"references":         map[string]any{},
		"documentHighlight":  map[string]any{},
		"rename":             map[string]any{"prepareSupport": true},
		"publishDiagnostics": map[string]any{"relatedInformation": true, "versionSupport": true},
	},
	"window": map[string]any{"workDoneProgress": true, "showMessage": map[string]any{}},
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (s *session) logText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.String()
}

// onRequest answers like a minimal editor: configuration gets an empty
// section per item, everything else null (never an error).
func (s *session) onRequest(method string, params json.RawMessage) any {
	s.mu.Lock()
	s.reqSeen[method]++
	s.mu.Unlock()
	if method == "workspace/configuration" {
		var p struct {
			Items []any `json:"items"`
		}
		json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		for i := range out {
			out[i] = map[string]any{}
		}
		return out
	}
	return nil
}

func (s *session) onNotify(method string, params json.RawMessage) {
	now := time.Since(s.start)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.cond.Broadcast()
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string `json:"uri"`
			Diagnostics []diag `json:"diagnostics"`
		}
		json.Unmarshal(params, &p)
		s.lastDiags[p.URI] = p.Diagnostics
		for name, c := range s.diagWait {
			if _, ok := s.diagHit[name]; !ok && c.uri == p.URI && c.f(p.Diagnostics) {
				s.diagHit[name] = now
			}
		}
	case "$/progress":
		var p struct {
			Token any `json:"token"`
			Value struct {
				Kind  string `json:"kind"`
				Title string `json:"title"`
			} `json:"value"`
		}
		json.Unmarshal(params, &p)
		tok := fmt.Sprint(p.Token)
		switch p.Value.Kind {
		case "begin":
			s.progTitle[tok] = p.Value.Title
		case "end":
			t := s.progTitle[tok]
			if _, ok := s.progEnd[t]; !ok {
				s.progEnd[t] = now
			}
		}
	}
}

func (s *session) uri(rel string) string {
	return fileURI(filepath.Join(s.dir, filepath.FromSlash(rel)))
}

type diagCond struct {
	uri string
	f   func([]diag) bool
}

// expectDiag records, as name, when rel's published diagnostics first
// satisfy f.
func (s *session) expectDiag(name, rel string, f func([]diag) bool) {
	s.mu.Lock()
	s.diagWait[name] = diagCond{s.uri(rel), f}
	s.mu.Unlock()
}

// waitFor waits until cond (called with s.mu held) holds, or the timeout
// passes or the process exits.
func (s *session) waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	t := time.AfterFunc(timeout, func() { s.mu.Lock(); s.cond.Broadcast(); s.mu.Unlock() })
	defer t.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for !cond() {
		if s.exited || time.Now().After(deadline) {
			return false
		}
		s.cond.Wait()
	}
	return true
}

func (s *session) open(rel, text string) {
	s.mu.Lock()
	s.texts[rel] = text
	s.versions[rel] = 1
	s.mu.Unlock()
	s.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": s.uri(rel), "languageId": "orbit", "version": 1, "text": text}})
}

// change replaces rel's text with one incremental change over the whole
// old text, as an editor does after a paste.
func (s *session) change(rel, text string) {
	s.mu.Lock()
	old := s.texts[rel]
	s.texts[rel] = text
	s.versions[rel]++
	v := s.versions[rel]
	s.mu.Unlock()
	s.c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": s.uri(rel), "version": v},
		"contentChanges": []any{map[string]any{
			"range": map[string]any{"start": position{}, "end": posAt(old, len(old))}, "text": text}},
	})
}

func (s *session) textPos(rel string, p position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": s.uri(rel)}, "position": p}
}

// call times one request.
func (s *session) call(method string, params any) (json.RawMessage, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t := time.Now()
	res, err := s.c.call(ctx, method, params)
	return res, time.Since(t), err
}

// hasRename reports whether the server advertises renameProvider.
func (s *session) hasRename() bool {
	caps, _ := s.init["capabilities"].(map[string]any)
	switch v := caps["renameProvider"].(type) {
	case bool:
		return v
	case map[string]any:
		return true
	}
	return false
}

func (s *session) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	s.c.call(ctx, "shutdown", nil)
	cancel()
	s.c.notify("exit", nil)
	if !s.waitFor(10*time.Second, func() bool { return s.exited }) {
		s.kill()
	}
}

func (s *session) kill() {
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.waitFor(10*time.Second, func() bool { return s.exited })
}

// logDuration parses "orbit lsp: <prefix> <duration>" from the server log.
func (s *session) logDuration(prefix string) (time.Duration, bool) {
	for _, line := range strings.Split(s.logText(), "\n") {
		_, rest, ok := strings.Cut(line, "orbit lsp: "+prefix)
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if d, err := time.ParseDuration(f[0]); err == nil {
			return d, true
		}
	}
	return 0, false
}

// mem is a process's memory from /proc/<pid>/status.
type mem struct {
	RSS, HWM int64 // KiB
}

func readMem(pid int) (mem, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return mem{}, err
	}
	var m mem
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		switch k {
		case "VmRSS":
			m.RSS = n
		case "VmHWM":
			m.HWM = n
		}
	}
	return m, nil
}

// children returns pid's child processes: /proc/<pid>/task/*/children,
// or a scan of /proc for the parent PID when that file is missing.
func children(pid int) []int {
	var out []int
	tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	for _, t := range tasks {
		b, err := os.ReadFile(t)
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				out = append(out, n)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", n))
		if err != nil {
			continue
		}
		// pid (comm) state ppid …: comm may contain spaces.
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b[i+1:]))
		if len(f) > 1 {
			if pp, _ := strconv.Atoi(f[1]); pp == pid {
				out = append(out, n)
			}
		}
	}
	return out
}

func comm(pid int) string {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return strings.TrimSpace(string(b))
}

// memory returns orbit lsp's and its gopls and TypeScript (tsc) children's
// memory.
func (s *session) memory() (orbit, gopls, ts mem) {
	pid := s.cmd.Process.Pid
	orbit, _ = readMem(pid)
	for _, c := range children(pid) {
		switch name := comm(c); {
		case strings.HasPrefix(name, "gopls"):
			gopls, _ = readMem(c)
		case name == "tsc" || name == "tsgo":
			ts, _ = readMem(c)
		}
	}
	return orbit, gopls, ts
}
