package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Markdown files in the language server (PLAN.md §2.4 "LSP", §9): Lyr's
// diagnostics for open and compiled .md files, at UTF-16 positions that
// follow edits, the typed check's frontmatter type errors, null answers to
// requests on them, and .md paths in import completion. None of it needs
// gopls: the test runs on the stub.

const mdLayout = `---
type Props struct {
	Title string
	Draft bool
}
---
<html><head></head><body><h1>{props.Title}</h1><slot /></body></html>
`

const mdHello = "---\nlayout: ../../layouts/Post.lyr\ntitle: Héllo ✓ 😀\ntilte: typo\ndraft: \"yes\"\n---\n# Hello\n\nSee [the other](./other.md), ✓ 😀 [missing](./missing.md).\n"

const mdList = "---\nimport Hello \"./blog/hello.md\"\n---\n<Hello />\n"

func TestMarkdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	dir := writeProject(t, map[string]string{
		"src/layouts/Post.lyr":    mdLayout,
		"src/pages/blog/hello.md": mdHello,
		"src/pages/blog/other.md": "# Other\n",
		"src/pages/list.lyr":      mdList,
		"src/content/README.md":   "[x](./nope.md)\n",
		"src/content/notes.md":    "Notes.\n",
		"README.md":               "[x](./nope.md)\n",
		"src/components/Box.lyr":  "<div><slot /></div>\n",
	})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, dir, map[string]any{"goplsPath": stub}, stubEnv+"=record:"+filepath.Join(t.TempDir(), "rec"))
	const hello = "src/pages/blog/hello.md"
	// Neither part of the project: outside src/, and not compiled.
	c.open("README.md")
	c.open("src/content/README.md")
	text := c.open(hello)

	// diag returns the diagnostic whose message contains sub, or nil.
	diag := func(ds []map[string]any, sub string) map[string]any {
		for _, d := range ds {
			if m, _ := d["message"].(string); strings.Contains(m, sub) {
				return d
			}
		}
		return nil
	}
	rangeOf := func(d map[string]any) Range {
		var r Range
		b, _ := json.Marshal(d["range"])
		json.Unmarshal(b, &r)
		return r
	}
	// check waits for the three diagnostics of text and checks where they
	// start: the unknown key, the typed check's bool error, the link.
	check := func(text string) {
		t.Helper()
		ds := c.waitDiag(hello, func(ds []map[string]any) bool {
			return len(ds) == 3 && diag(ds, `unknown frontmatter key "tilte"`) != nil && diag(ds, "isn't a bool") != nil &&
				diag(ds, "link ./missing.md") != nil && rangeOf(diag(ds, "link ./missing.md")).Start == at(t, text, "[missing]", 0, 0)
		})
		for _, w := range []struct {
			sub string
			at  Position
		}{
			{`unknown frontmatter key "tilte"`, at(t, text, "tilte:", 0, 0)},
			{"isn't a bool", at(t, text, `"yes"`, 0, 0)},
			{"link ./missing.md", at(t, text, "[missing]", 0, 0)},
		} {
			if got := rangeOf(diag(ds, w.sub)).Start; got != w.at {
				t.Errorf("%s at %+v, want %+v", w.sub, got, w.at)
			}
		}
		if d := diag(ds, `"tilte"`); rangeOf(d).End != at(t, text, "tilte:", 0, len("tilte")) {
			t.Errorf("unknown key range %+v", rangeOf(d))
		}
	}
	t.Run("diagnostics", func(t *testing.T) { check(text) })

	t.Run("edits move them", func(t *testing.T) {
		// Two body lines before the link, and a frontmatter comment before
		// the keys (the typed diagnostic follows its key).
		edited := strings.Replace(text, "# Hello\n", "# Hello ✓\n\nA new paragraph.\n", 1)
		edited = strings.Replace(edited, "tilte:", "# a comment\ntilte:", 1)
		c.change(hello, edited)
		check(edited)

		// Fixed: no diagnostics left.
		fixed := strings.Replace(edited, "tilte: typo\n", "", 1)
		fixed = strings.Replace(fixed, `draft: "yes"`, "draft: true", 1)
		fixed = strings.Replace(fixed, "./missing.md", "./other.md", 1)
		c.change(hello, fixed)
		c.waitDiag(hello, func(ds []map[string]any) bool { return len(ds) == 0 })

		// A fragment that the target has no heading for is a warning.
		warn := strings.Replace(fixed, "[the other](./other.md)", "[the other](./other.md#nope)", 1)
		c.change(hello, warn)
		ds := c.waitDiag(hello, func(ds []map[string]any) bool { return len(ds) == 1 })
		if sev, _ := ds[0]["severity"].(float64); sev != sevWarning || rangeOf(ds[0]).Start != at(t, warn, "[the other]", 0, 0) {
			t.Errorf("fragment warning: %v", ds[0])
		}
		// A new type error shows up once the Markdown typed check reruns
		// after the edit, with no lyr_gen/ event (there's no lyr_gen/);
		// fixing it clears it.
		bad := strings.Replace(warn, "draft: true", `draft: "no"`, 1)
		c.change(hello, bad)
		ds = c.waitDiag(hello, func(ds []map[string]any) bool { return len(ds) == 2 && diag(ds, "isn't a bool") != nil })
		if got := rangeOf(diag(ds, "isn't a bool")).Start; got != at(t, bad, `"no"`, 0, 0) {
			t.Errorf("typed error at %+v", got)
		}
		c.change(hello, warn)
		c.waitDiag(hello, func(ds []map[string]any) bool { return len(ds) == 1 && diag(ds, "has no heading") != nil })
	})

	t.Run("requests answer null", func(t *testing.T) {
		pos := at(t, text, "title", 0, 2)
		for _, m := range []string{"textDocument/hover", "textDocument/completion", "textDocument/definition",
			"textDocument/references", "textDocument/signatureHelp", "textDocument/prepareRename"} {
			res, err := c.callErr(m, c.textPos(hello, pos))
			if err != nil || !isNull(res) {
				t.Errorf("%s on a .md: %s, %v", m, res, err)
			}
		}
		params := c.renameParams(hello, pos, "x")
		if res, err := c.callErr("textDocument/rename", params); err != nil || !isNull(res) {
			t.Errorf("rename on a .md: %s, %v", res, err)
		}
	})

	t.Run("close", func(t *testing.T) {
		// The diagnostics are the disk content's again, the typed one
		// after the Markdown typed check.
		c.conn.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": c.uri(hello)}})
		c.waitDiag(hello, func(ds []map[string]any) bool { return len(ds) == 3 })
	})

	t.Run("import paths", func(t *testing.T) {
		const list = "src/pages/list.lyr"
		src := c.open(list)
		edited := strings.Replace(src, `"./blog/hello.md"`, `"./"`, 1)
		c.change(list, edited)
		items := c.complete(list, at(t, edited, `"./"`, 0, len(`"./`)))
		got := labels(items)
		for _, want := range []string{"./blog/hello.md", "./blog/other.md", "../layouts/Post.lyr", "../components/Box.lyr",
			"../content/notes.md", "../content/README.md"} {
			if !slices.Contains(got, want) {
				t.Errorf(`import "./: %v, missing %s`, got, want)
			}
		}
		if slices.Contains(got, "./list.lyr") {
			t.Errorf("the file itself is offered: %v", got)
		}
		it := item(items, "./blog/other.md")
		if after := accept(edited, it); !strings.Contains(after, `import Hello "./blog/other.md"`+"\n") || len(it.AdditionalTextEdits) != 0 {
			t.Errorf("accepting ./blog/other.md gives:\n%s", after)
		}
		// No alias: accepting one adds it.
		edited = strings.Replace(src, `import Hello "./blog/hello.md"`, `import "../c`, 1)
		c.change(list, edited)
		items = c.complete(list, at(t, edited, `"../c`, 0, len(`"../c`)))
		it = item(items, "../content/notes.md")
		if it == nil {
			t.Fatalf(`import "../c: %v`, labels(items))
		}
		if after := accept(edited, it); !strings.HasPrefix(after, "---\nimport Notes \"../content/notes.md\"\n---\n") {
			t.Errorf("accepting ../content/notes.md gives:\n%s", after)
		}
		// In an import block, after another import.
		edited = strings.Replace(src, `import Hello "./blog/hello.md"`, "import (\n\t\"strings\"\n\tHello \"./blog/hello.md\"\n\t\"./b\"\n)", 1)
		c.change(list, edited)
		items = c.complete(list, at(t, edited, `"./b"`, 0, len(`"./b`)))
		if item(items, "./blog/hello.md") != nil || item(items, "./blog/other.md") == nil {
			t.Errorf(`"./b in a block: %v, want other.md and not the imported hello.md`, labels(items))
		}
		c.change(list, src)
	})

	// Files outside the project never get diagnostics, even with broken
	// links.
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rel := range []string{"README.md", "src/content/README.md"} {
		if ds, ok := c.diags[filepath.Join(dir, rel)]; ok {
			t.Errorf("%s: diagnostics %v", rel, ds)
		}
	}
}

// TestMarkdownRename: renaming a layout Props field that a Markdown
// frontmatter sets is refused (user decision), naming the key.
func TestMarkdownRename(t *testing.T) {
	needGopls(t)
	hello := strings.Replace(strings.Replace(mdHello, "tilte: typo\n", "", 1), `"yes"`, "true", 1)
	dir := writeProject(t, map[string]string{
		"src/layouts/Post.lyr":    mdLayout,
		"src/pages/blog/hello.md": hello,
		"src/pages/blog/other.md": "# Other\n",
	})
	c := startClient(t, dir, nil)
	layout := c.open("src/layouts/Post.lyr")
	_, err := c.callErr("textDocument/rename", c.renameParams("src/layouts/Post.lyr", at(t, layout, "Title string", 0, 1), "Heading"))
	want := "can't rename Title: the frontmatter of src/pages/blog/hello.md:3 (title) sets it"
	if msg := rpcMessage(err); !strings.Contains(msg, want) {
		t.Errorf("rename of Title: %q, want %q", msg, want)
	}
}

// TestMarkdownTypedCheck: the Markdown typed check while typing (PLAN.md
// §9) adopts only the result of the latest snapshot, and reruns after an
// edit of the layout's Props. The server runs in process, for its hooks.
func TestMarkdownTypedCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	hello := strings.Replace(strings.Replace(mdHello, "tilte: typo\n", "", 1), `"yes"`, "true", 1)
	hello = strings.Replace(hello, "./missing.md", "./other.md", 1)
	dir := writeProject(t, map[string]string{
		"src/layouts/Post.lyr":    mdLayout,
		"src/pages/blog/hello.md": hello,
		"src/pages/blog/other.md": "# Other\n",
	})
	started, release := make(chan struct{}), make(chan struct{})
	results := make(chan bool, 16)
	var runs atomic.Int32
	mdHooks.Store(&mdTestHooks{
		run: func() {
			if runs.Add(1) == 1 {
				started <- struct{}{}
				<-release
			}
		},
		done: func(adopted bool) { results <- adopted },
	})
	t.Cleanup(func() { mdHooks.Store(nil) })
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(stubEnv, "record:"+filepath.Join(t.TempDir(), "rec"))
	c := startInProcess(t, dir, map[string]any{"goplsPath": stub})
	const md = "src/pages/blog/hello.md"
	text := c.open(md)
	c.call("textDocument/hover", c.textPos(md, Position{})) // open, and no diagnostics to publish
	typed := func(ds []map[string]any, value string) bool {
		for _, d := range ds {
			if m, _ := d["message"].(string); strings.Contains(m, "frontmatter draft: "+value+" isn't a") {
				return true
			}
		}
		return false
	}

	// Two edits: the first one's check, still running when the second
	// comes, is dropped; the second one's is published.
	first := strings.Replace(text, "draft: true", `draft: "no"`, 1)
	c.change(md, first)
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("no Markdown typed check after an edit")
	}
	second := strings.Replace(text, "draft: true", `draft: "maybe"`, 1)
	c.change(md, second)
	c.call("textDocument/hover", c.textPos(md, Position{})) // the edit is in
	close(release)
	if adopted := <-results; adopted {
		t.Error("the check of a superseded edit was adopted")
	}
	ds := c.waitDiag(md, func(ds []map[string]any) bool { return typed(ds, `"maybe"`) })
	if typed(ds, `"no"`) || len(ds) != 1 {
		t.Errorf("after two edits: %v", ds)
	}
	if adopted := <-results; !adopted {
		t.Error("the latest check wasn't adopted")
	}

	// Fixed: the error goes away at once (the key's text changed) and the
	// next check finds none.
	c.change(md, text)
	c.waitDiag(md, func(ds []map[string]any) bool { return len(ds) == 0 })
	if adopted := <-results; !adopted {
		t.Error("the check after the fix wasn't adopted")
	}

	// An edit of the layout's Props reruns it: draft: true isn't an int.
	const layout = "src/layouts/Post.lyr"
	src := c.open(layout)
	c.change(layout, strings.Replace(src, "Draft bool", "Draft int", 1))
	c.waitDiag(md, func(ds []map[string]any) bool { return typed(ds, "true") })
	<-results
	// A template edit of the layout doesn't.
	n := runs.Load()
	c.change(layout, strings.Replace(strings.Replace(src, "Draft bool", "Draft int", 1), "<h1>", "<h1 class=\"t\">", 1))
	c.call("textDocument/hover", c.textPos(md, Position{}))
	time.Sleep(2 * mdCheckDelay)
	if got := runs.Load(); got != n {
		t.Errorf("a template edit of the layout ran %d Markdown typed checks", got-n)
	}
}
