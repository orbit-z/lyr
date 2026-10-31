package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orbit-z/lyr/internal/tsengine"
)

// tsStubEnv makes the test binary act as a TypeScript 7 stub (TestMain):
// "<mode>:<file>", recording every message it receives (and its client's
// answers to the requests it sends) in <file>, one JSON object per line.
// Modes: "ok" answers hover and pulls diagnostics, and asks for a
// diagnostic refresh after the first pull; "crash" exits on a hover.
const tsStubEnv = "LYR_LSP_TEST_TS_STUB"

func runStubTS() {
	mode, file, _ := strings.Cut(os.Getenv(tsStubEnv), ":")
	for _, a := range os.Args[1:] {
		if a == "--version" {
			fmt.Println("Version 7.0.2")
			return
		}
	}
	var mu sync.Mutex
	rec := func(method string, params jsonRaw) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(stubRecord{method, params})
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		f.Write(append(b, '\n'))
		f.Close()
	}
	c := newConn(os.Stdin, os.Stdout)
	exit := make(chan struct{})
	texts := map[string]string{}
	refreshed := false
	ask := func(method string, params any) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := c.Call(ctx, method, params)
			if err != nil {
				res = jsonRaw(`"error: ` + err.Error() + `"`)
			}
			rec("answer:"+method, res)
		}()
	}
	go c.Run(func(m *message) {
		if m.Method != "" {
			rec(m.Method, m.Params)
		}
		switch m.Method {
		case "initialize":
			c.Reply(m.ID, map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-16", "hoverProvider": true},
				"serverInfo": map[string]any{"name": "typescript-stub", "version": "7.0.2"}}, nil)
		case "initialized":
			ask("workspace/configuration", map[string]any{"items": []any{map[string]any{"section": "js/ts"}, map[string]any{"section": "typescript"}}})
			ask("client/registerCapability", map[string]any{"registrations": []any{
				map[string]any{"id": "w1", "method": "workspace/didChangeWatchedFiles",
					"registerOptions": map[string]any{"watchers": []any{map[string]any{"globPattern": "/tmp/**/*"}}}},
				map[string]any{"id": "c1", "method": "workspace/didChangeConfiguration"},
			}})
		case "textDocument/didOpen", "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			json.Unmarshal(m.Params, &p)
			mu.Lock()
			if m.Method == "textDocument/didOpen" {
				texts[p.TextDocument.URI] = p.TextDocument.Text
			} else if len(p.ContentChanges) > 0 {
				texts[p.TextDocument.URI] = p.ContentChanges[0].Text
			}
			mu.Unlock()
		case "textDocument/hover":
			if mode == "crash" {
				os.Exit(3)
			}
			c.Reply(m.ID, map[string]any{"contents": map[string]any{"kind": "markdown", "value": "stub hover"}}, nil)
		case "textDocument/diagnostic":
			uri := decodeObj(decodeObj(m.Params)["textDocument"]).str("uri")
			mu.Lock()
			text := texts[uri]
			again := !refreshed
			refreshed = true
			mu.Unlock()
			// One diagnostic on the first character of the script.
			off := len(text) - len(strings.TrimLeft(text, " \n\r\t"))
			r := newLineIndex(text).rangeOf(off, off+1, encUTF16)
			c.Reply(m.ID, map[string]any{"kind": "full", "items": []any{map[string]any{"range": r, "severity": 1, "message": "stub diag"}}}, nil)
			if again {
				ask("workspace/diagnostic/refresh", nil)
			}
		case "exit":
			close(exit)
		default:
			if m.isRequest() {
				c.Reply(m.ID, nil, nil)
			}
		}
	})
	select {
	case <-exit:
	case <-c.Done():
	}
	c.Close()
}

// waitRecords polls a stub's recording until pred holds.
func waitRecords(t *testing.T, file, what string, pred func([]stubRecord) bool) []stubRecord {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		recs := readRecords(t, file)
		if pred(recs) {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countRecords(recs []stubRecord, method string) int {
	n := 0
	for _, r := range recs {
		if r.Method == method {
			n++
		}
	}
	return n
}

// useTSStub makes the in-process server start the stub in mode, and
// returns its recording file.
func useTSStub(t *testing.T, mode string) string {
	file := filepath.Join(t.TempDir(), "ts.jsonl")
	t.Setenv(tsStubEnv, mode+":"+file)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := findEngine
	findEngine = func(context.Context, tsengine.Options) (tsengine.Engine, error) {
		return tsengine.Engine{Bin: self, Version: tsengine.Min, Via: "test"}, nil
	}
	t.Cleanup(func() { findEngine = old })
	return file
}

const scriptCard = `---
type Props struct {
	Title string
}
count := 1
---
<h2>{props.Title} {count}</h2>
<script>
  console.log("card");
</script>
`

func TestTSStub(t *testing.T) {
	file := useTSStub(t, "ok")
	dir := writeProject(t, map[string]string{"src/components/Card.lyr": scriptCard, "src/pages/index.lyr": "---\n---\n<p>hi</p>\n"})
	c := startInProcess(t, dir, nil)
	const card = "src/components/Card.lyr"
	text := c.open(card)

	h, _ := c.hover(card, at(t, text, `console.log`, 0, 2))
	if h != "stub hover" {
		t.Errorf("hover in the script: %q, want the child's", h)
	}
	ds := c.waitDiag(card, func(ds []map[string]any) bool { return len(tsDiags(ds)) > 0 })
	if d := tsDiags(ds)[0]; diagRange(t, d).Start != at(t, text, "console", 0, 0) || d["message"] != "stub diag" {
		t.Errorf("diagnostic %v", d)
	}
	// The refresh the child asks for pulls again; the answer is null.
	recs := waitRecords(t, file, "a second pull after the refresh", func(recs []stubRecord) bool {
		return countRecords(recs, "textDocument/diagnostic") >= 2 && countRecords(recs, "answer:workspace/diagnostic/refresh") == 1
	})
	for _, r := range recs {
		switch r.Method {
		case "answer:workspace/diagnostic/refresh", "answer:client/registerCapability":
			if string(r.Params) != "null" {
				t.Errorf("%s: %s, want null", r.Method, r.Params)
			}
		case "answer:workspace/configuration":
			if string(r.Params) != "[{},{}]" {
				t.Errorf("configuration answer %s", r.Params)
			}
		}
		if strings.HasPrefix(string(r.Params), `"error`) {
			t.Errorf("the child got an error answer: %s %s", r.Method, r.Params)
		}
	}
	opened := ""
	for _, r := range recs {
		if r.Method == "textDocument/didOpen" {
			opened += decodeObj(decodeObj(r.Params)["textDocument"]).str("uri") + "\n"
		}
	}
	for _, want := range []string{"/.lyr/lsp/tsconfig.json", "/.lyr/lsp/src/components/Card.lyr.0.ts"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the child didn't open %s:\n%s", want, opened)
		}
	}
	// The editor got the child's configuration registration (prefixed)
	// and Lyr's own watcher, never the child's broad watcher.
	c.waitFor("registrations", 10*time.Second, func() bool {
		all := strings.Join(c.registrations, "\n")
		return strings.Contains(all, `"ts:c1"`) && strings.Contains(all, "lyr-ts-files")
	})
	c.mu.Lock()
	all := strings.Join(c.registrations, "\n")
	c.mu.Unlock()
	if strings.Contains(all, `"ts:w1"`) || strings.Contains(all, "/tmp/**/*") {
		t.Errorf("the child's watcher was forwarded: %s", all)
	}
	// Positions outside scripts don't reach the child.
	before := countRecords(readRecords(t, file), "textDocument/hover")
	c.hover(card, at(t, text, "count :=", 0, 1))
	c.hover(card, at(t, text, "<h2>", 0, 1))
	if n := countRecords(readRecords(t, file), "textDocument/hover"); n != before {
		t.Errorf("%d hovers reached the child for Go and template positions", n-before)
	}
	// Closing the file drops its TypeScript diagnostics.
	c.conn.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": c.uri(card)}})
	c.waitDiag(card, func(ds []map[string]any) bool { return len(tsDiags(ds)) == 0 })
}

func TestTSStubCrash(t *testing.T) {
	file := useTSStub(t, "crash")
	dir := writeProject(t, map[string]string{"src/components/Card.lyr": scriptCard})
	c := startInProcess(t, dir, nil)
	const card = "src/components/Card.lyr"
	text := c.open(card)
	pos := at(t, text, `console.log`, 0, 2)
	for i := range 3 {
		if h, _ := c.hover(card, pos); h != "" {
			t.Errorf("hover %d: %q from a crashing child", i, h)
		}
		if i == 0 {
			// Wait for the exit to be noticed before asking again.
			time.Sleep(200 * time.Millisecond)
		}
	}
	c.waitFor("the second-crash notice", 10*time.Second, func() bool {
		for _, m := range c.messages {
			if strings.Contains(m, "exited again") {
				return true
			}
		}
		return false
	})
	if n := countRecords(readRecords(t, file), "initialize"); n != 2 {
		t.Errorf("%d starts, want 2 (one restart after the first crash, none after the second)", n)
	}
}

func TestTSMissingEngine(t *testing.T) {
	old := findEngine
	findEngine = func(context.Context, tsengine.Options) (tsengine.Engine, error) {
		return tsengine.Engine{}, errors.New("TypeScript 7.0.2 or later not found")
	}
	t.Cleanup(func() { findEngine = old })
	dir := writeProject(t, map[string]string{"src/components/Card.lyr": scriptCard,
		"src/components/Other.lyr": strings.Replace(scriptCard, "card", "other", 1)})
	c := startInProcess(t, dir, nil)
	text := c.open("src/components/Card.lyr")
	c.open("src/components/Other.lyr")
	c.waitFor("the notice", 10*time.Second, func() bool {
		for _, m := range c.messages {
			if strings.Contains(m, tsNoticePrefix) {
				return true
			}
		}
		return false
	})
	// Script requests answer nothing; one notice in all.
	res := c.call("textDocument/completion", c.textPos("src/components/Card.lyr", at(t, text, "console.", 0, len("console."))))
	if labels := completionLabels(t, res); len(labels) != 0 {
		t.Errorf("completion in a script without TypeScript: %v", labels)
	}
	time.Sleep(100 * time.Millisecond)
	c.mu.Lock()
	n := 0
	for _, m := range c.messages {
		if strings.Contains(m, tsNoticePrefix) {
			n++
			if !strings.Contains(m, "bun add -g typescript@") || !strings.HasPrefix(m, "2:") {
				t.Errorf("notice %q", m)
			}
		}
	}
	c.mu.Unlock()
	if n != 1 {
		t.Errorf("%d notices, want 1", n)
	}
	// Go features don't depend on it.
	if _, err := exec.LookPath("gopls"); err != nil {
		return
	}
	if h, _ := c.hover("src/components/Card.lyr", at(t, text, "count :=", 0, 1)); !strings.Contains(h, "count int") {
		t.Errorf("Go hover without TypeScript: %q", h)
	}
}
