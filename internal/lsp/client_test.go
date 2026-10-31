package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// The editor side of the integration tests: an LSP client that spawns
// lyr lsp over stdio, answers its requests like a minimal editor and
// records what it publishes.

var (
	buildOnce sync.Once
	lyrBin    string
	buildErr  error
	binDir    string
)

func TestMain(m *testing.M) {
	if os.Getenv(stubEnv) != "" {
		runStubGopls()
		return
	}
	if os.Getenv(tsStubEnv) != "" {
		runStubTS()
		return
	}
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// buildLyr builds the lyr CLI once per test run.
func buildLyr(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "lyr-lsp-test")
		if buildErr != nil {
			return
		}
		lyrBin = filepath.Join(binDir, "lyr")
		cmd := exec.Command("go", "build", "-o", lyrBin, "./cmd/lyr")
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/lyr: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return lyrBin
}

func repoRoot(t *testing.T) string {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// needGopls skips the test without gopls on PATH, and under -short.
func needGopls(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the gopls integration test in -short mode")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("NOTICE: gopls not on PATH; skipping the LSP integration test (go install golang.org/x/tools/gopls@latest)")
	}
}

// writeProject creates a module example.com/site requiring the runtime
// through a replace to this repository, with the given files.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	root := repoRoot(t)
	sum, err := os.ReadFile(filepath.Join(root, "examples/blog/go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/site\n\ngo 1.27\n\ntoolchain go1.27.3\n\n" +
		"require github.com/orbit-z/lyr v0.0.0\n\n" +
		"require (\n\tgithub.com/evanw/esbuild v0.28.2 // indirect\n\tgithub.com/fsnotify/fsnotify v1.10.1 // indirect\n" +
		"\tgithub.com/tdewolff/parse/v2 v2.8.16 // indirect\n\tgolang.org/x/sys v0.13.0 // indirect\n)\n\n" +
		"replace github.com/orbit-z/lyr => " + filepath.ToSlash(root) + "\n"
	files["go.sum"] = string(sum)
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// testClient is the editor.
type testClient struct {
	t    *testing.T
	dir  string
	conn *Conn
	cmd  *exec.Cmd
	// done is closed when an in-process server (startInProcess, cmd nil)
	// returned.
	done  chan struct{}
	start time.Time

	mu       sync.Mutex
	cond     *sync.Cond
	diags    map[string][]map[string]any // latest per URI
	diagAt   map[string]time.Time        // first publish per URI
	messages []string                    // showMessage texts, with their type
	requests []string                    // showMessageRequest texts
	// registrations are the registerCapability params received.
	registrations []string
	texts         map[string]string // open documents
	versions      map[string]int

	initResult jsonRaw
}

// noDocumentChanges is the startClient initOpts key for an editor without
// documentChanges and prepareRename support.
const noDocumentChanges = "test:noDocumentChanges"

// startClient spawns lyr lsp in dir and initializes it.
func startClient(t *testing.T, dir string, initOpts map[string]any, env ...string) *testClient {
	t.Helper()
	cmd := exec.Command(buildLyr(t), "lsp")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GOFLAGS=-mod=mod", "GOWORK=off"}, env...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	logf, err := os.Create(filepath.Join(t.TempDir(), "lsp.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = logf
	c := &testClient{t: t, dir: dir, cmd: cmd, diags: map[string][]map[string]any{}, diagAt: map[string]time.Time{},
		texts: map[string]string{}, versions: map[string]int{}}
	c.cond = sync.NewCond(&c.mu)
	c.start = time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c.conn = newConn(stdout, stdin)
	go c.conn.Run(c.handle)
	t.Cleanup(func() {
		c.shutdown()
		logf.Close()
		if t.Failed() || testing.Verbose() {
			b, _ := os.ReadFile(logf.Name())
			t.Logf("lyr lsp log:\n%s", b)
		}
	})
	c.initialize(initOpts)
	return c
}

// startInProcess runs the language server in this test process (so that
// tests can set its test hooks) on dir, and initializes it. The go command
// runs in the module mode startClient sets.
func startInProcess(t *testing.T, dir string, initOpts map[string]any) *testClient {
	t.Helper()
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOWORK", "off")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	logf, err := os.Create(filepath.Join(t.TempDir(), "lsp.log"))
	if err != nil {
		t.Fatal(err)
	}
	c := &testClient{t: t, dir: dir, done: make(chan struct{}), diags: map[string][]map[string]any{}, diagAt: map[string]time.Time{},
		texts: map[string]string{}, versions: map[string]int{}}
	c.cond = sync.NewCond(&c.mu)
	c.start = time.Now()
	go func() {
		defer close(c.done)
		Run(context.Background(), inR, outW, Options{Log: logf})
		outW.Close()
	}()
	c.conn = newConn(outR, inW)
	go c.conn.Run(c.handle)
	t.Cleanup(func() {
		c.shutdown()
		inW.Close()
		<-c.done
		logf.Close()
		if t.Failed() || testing.Verbose() {
			b, _ := os.ReadFile(logf.Name())
			t.Logf("lyr lsp log:\n%s", b)
		}
	})
	c.initialize(initOpts)
	return c
}

// initialize sends initialize and initialized.
func (c *testClient) initialize(initOpts map[string]any) {
	t, dir := c.t, c.dir
	// noDocumentChanges (an initOpts key, not sent) makes an editor
	// without workspace.workspaceEdit.documentChanges support (and
	// without prepareRename support).
	docChanges := true
	if _, ok := initOpts[noDocumentChanges]; ok {
		docChanges = false
		initOpts = nil
	}
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(dir),
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":         true,
				"workspaceEdit":         map[string]any{"documentChanges": docChanges},
				"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true},
			},
			"textDocument": map[string]any{
				"hover":              map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"publishDiagnostics": map[string]any{"relatedInformation": true},
				"rename":             map[string]any{"prepareSupport": docChanges},
			},
			"window": map[string]any{"workDoneProgress": true},
		},
	}
	if initOpts != nil {
		params["initializationOptions"] = initOpts
	}
	res := c.call("initialize", params)
	c.initResult = res
	if !strings.Contains(string(res), `"utf-16"`) {
		t.Fatalf("initialize: no utf-16 position encoding: %s", res)
	}
	c.conn.Notify("initialized", map[string]any{})
}

func (c *testClient) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	c.conn.Call(ctx, "shutdown", nil)
	cancel()
	c.conn.Notify("exit", nil)
	if c.cmd == nil {
		select {
		case <-c.done:
		case <-time.After(10 * time.Second):
		}
		return
	}
	done := make(chan struct{})
	go func() { c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		c.cmd.Process.Kill()
		<-done
	}
	c.conn.Close()
}

// handle answers the server's requests like a minimal editor.
func (c *testClient) handle(m *message) {
	if m.isRequest() {
		var res any
		switch m.Method {
		case "workspace/configuration":
			var p struct {
				Items []any `json:"items"`
			}
			json.Unmarshal(m.Params, &p)
			items := make([]any, len(p.Items))
			for i := range items {
				items[i] = map[string]any{}
			}
			res = items
		case "window/showMessageRequest":
			c.mu.Lock()
			c.requests = append(c.requests, decodeObj(m.Params).str("message"))
			c.cond.Broadcast()
			c.mu.Unlock()
		case "client/registerCapability":
			c.mu.Lock()
			c.registrations = append(c.registrations, string(m.Params))
			c.cond.Broadcast()
			c.mu.Unlock()
		}
		c.conn.Reply(m.ID, res, nil)
		return
	}
	switch m.Method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string           `json:"uri"`
			Diagnostics []map[string]any `json:"diagnostics"`
		}
		json.Unmarshal(m.Params, &p)
		path := uriToPath(p.URI)
		c.mu.Lock()
		c.diags[path] = p.Diagnostics
		if _, ok := c.diagAt[path]; !ok {
			c.diagAt[path] = time.Now()
		}
		c.cond.Broadcast()
		c.mu.Unlock()
	case "window/showMessage":
		o := decodeObj(m.Params)
		var typ int
		json.Unmarshal(o["type"], &typ)
		c.mu.Lock()
		c.messages = append(c.messages, fmt.Sprintf("%d: %s", typ, o.str("message")))
		c.cond.Broadcast()
		c.mu.Unlock()
	}
}

func (c *testClient) call(method string, params any) jsonRaw {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.conn.Call(ctx, method, params)
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	return res
}

// waitFor waits until cond (called with c.mu held) holds.
func (c *testClient) waitFor(what string, timeout time.Duration, cond func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { c.mu.Lock(); c.cond.Broadcast(); c.mu.Unlock() })
	defer timer.Stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for !cond() {
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s", what)
		}
		c.cond.Wait()
	}
}

// waitDiag waits until the diagnostics of rel satisfy pred and returns them.
func (c *testClient) waitDiag(rel string, pred func([]map[string]any) bool) []map[string]any {
	c.t.Helper()
	defer func() {
		if c.t.Failed() {
			c.mu.Lock()
			c.t.Logf("last diagnostics of %s: %v", rel, c.diags[filepath.Join(c.dir, filepath.FromSlash(rel))])
			c.mu.Unlock()
		}
	}()
	path := filepath.Join(c.dir, filepath.FromSlash(rel))
	var out []map[string]any
	c.waitFor("diagnostics of "+rel, 60*time.Second, func() bool {
		ds, ok := c.diags[path]
		if ok && pred(ds) {
			out = ds
			return true
		}
		return false
	})
	return out
}

func (c *testClient) uri(rel string) string {
	return pathToURI(filepath.Join(c.dir, filepath.FromSlash(rel)))
}

// open opens rel with its disk content.
func (c *testClient) open(rel string) string {
	c.t.Helper()
	b, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(rel)))
	if err != nil {
		c.t.Fatal(err)
	}
	c.mu.Lock()
	c.texts[rel] = string(b)
	c.versions[rel] = 1
	c.mu.Unlock()
	c.conn.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": c.uri(rel), "languageId": "lyr", "version": 1, "text": string(b)}})
	return string(b)
}

// change replaces the text of the open document rel, as one incremental
// change over the whole old text.
func (c *testClient) change(rel, text string) {
	c.mu.Lock()
	old := c.texts[rel]
	c.texts[rel] = text
	c.versions[rel]++
	v := c.versions[rel]
	c.mu.Unlock()
	end := posAt(old, len(old))
	c.conn.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": c.uri(rel), "version": v},
		"contentChanges": []any{map[string]any{"range": Range{End: end}, "text": text}},
	})
}

// at returns the position of the nth (0-based) occurrence of needle in
// text, plus delta bytes.
func at(t *testing.T, text, needle string, nth, delta int) Position {
	t.Helper()
	off := -1
	for i, from := 0, 0; i <= nth; i++ {
		j := strings.Index(text[from:], needle)
		if j < 0 {
			t.Fatalf("%q: occurrence %d not found", needle, nth)
		}
		off = from + j
		from = off + 1
	}
	return posAt(text, off+delta)
}

// posAt computes a UTF-16 position independently of lineIndex.
func posAt(text string, off int) Position {
	line, lineStart := 0, 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	n := 0
	for _, r := range text[lineStart:off] {
		n += len(utf16.Encode([]rune{r}))
	}
	return Position{Line: line, Character: n}
}

func (c *testClient) textPos(rel string, pos Position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(rel)}, "position": pos}
}

// hover returns the hover's markdown text and range ("" for null).
func (c *testClient) hover(rel string, pos Position) (string, *Range) {
	c.t.Helper()
	res := c.call("textDocument/hover", c.textPos(rel, pos))
	if isNull(res) {
		return "", nil
	}
	var h struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
		Range *Range `json:"range"`
	}
	if err := json.Unmarshal(res, &h); err != nil {
		c.t.Fatalf("hover result %s: %v", res, err)
	}
	return h.Contents.Value, h.Range
}

// locations decodes a definition or references result.
func (c *testClient) locations(res jsonRaw) []location {
	c.t.Helper()
	if isNull(res) {
		return nil
	}
	var list []location
	if strings.HasPrefix(strings.TrimSpace(string(res)), "{") {
		var l location
		json.Unmarshal(res, &l)
		return []location{l}
	}
	if err := json.Unmarshal(res, &list); err != nil {
		c.t.Fatalf("locations %s: %v", res, err)
	}
	return list
}

// rangeText returns the text a range covers in text.
func rangeText(text string, r Range) string {
	x := newLineIndex(text)
	return text[x.offset(r.Start, encUTF16):x.offset(r.End, encUTF16)]
}
