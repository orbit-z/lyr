package lsp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/orbit/internal/tsengine"
)

// The TypeScript integration tests (PLAN.md §9b Tests) drive orbit lsp on
// a copy of examples/blog with TypeScript 7: ORBIT_TEST_TYPESCRIPT names
// the engine (a native tsc or a typescript package directory), else one
// is looked up as the server would, without bun x (no network in tests).

// needTypeScript returns the engine to pass as typescriptPath, or skips.
func needTypeScript(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the TypeScript integration test in -short mode")
	}
	if p := os.Getenv("ORBIT_TEST_TYPESCRIPT"); p != "" {
		return p
	}
	e, err := tsengine.Find(context.Background(), tsengine.Default(), tsengine.Options{})
	if err != nil {
		t.Skipf("NOTICE: TypeScript %s not found (%v); skipping the TypeScript integration test (set ORBIT_TEST_TYPESCRIPT to a native tsc)", tsengine.Min, err)
	}
	return e.Bin
}

// copyBlog copies examples/blog (without orbit_gen/ and .orbit/) into a
// temporary directory, with the runtime replaced by this repository, and
// applies the edits.
func copyBlog(t *testing.T, files map[string]string) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", "blog")
	dir := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if rel == "orbit_gen" || rel == ".orbit" || rel == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if rel == "go.mod" {
			b = []byte(strings.Replace(string(b), "=> ../..", "=> "+filepath.ToSlash(repoRoot(t)), 1))
		}
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// completionLabels returns the labels of a completion result.
func completionLabels(t *testing.T, res jsonRaw) []string {
	t.Helper()
	var list struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		var items []struct {
			Label string `json:"label"`
		}
		json.Unmarshal(res, &items)
		list.Items = items
	}
	out := make([]string, len(list.Items))
	for i, it := range list.Items {
		out[i] = it.Label
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// tsDiags returns the diagnostics of source ts.
func tsDiags(ds []map[string]any) []map[string]any {
	var out []map[string]any
	for _, d := range ds {
		if d["source"] == "ts" {
			out = append(out, d)
		}
	}
	return out
}

const inlineOrbit = `---
---
<p id="x">hi</p>
<script is:inline>
  const greeting = document.getElementById("x");
  greeting.
</script>
`

// sharedOrbit uses plural too, and stays closed in the editor.
const sharedOrbit = `---
---
<p>shared</p>
<script>
  import { plural } from "../scripts/util";
  console.log(plural(2, "post"));
</script>
`

func TestTypeScript(t *testing.T) {
	needGopls(t)
	tsc := needTypeScript(t)
	// The project's tsconfig.json without rootDirs (an unmigrated
	// project): the in-memory tsconfig still resolves ./LikeButton.orbit
	// (Q3). No orbit_gen/ on disk: the types come from memory (Q5).
	dir := copyBlog(t, map[string]string{
		"src/pages/inline.orbit":      inlineOrbit,
		"src/components/Shared.orbit": sharedOrbit,
	})
	tsconfig := filepath.Join(dir, "tsconfig.json")
	b, _ := os.ReadFile(tsconfig)
	stripped := strings.Replace(string(b), `,
    "rootDirs": [".", "orbit_gen/types"]`, "", 1)
	if stripped == string(b) {
		t.Fatalf("examples/blog's tsconfig.json changed: no rootDirs line to strip:\n%s", b)
	}
	os.WriteFile(tsconfig, []byte(stripped), 0o644)

	c := startClient(t, dir, map[string]any{"typescriptPath": tsc})
	const like = "src/components/LikeButton.orbit"
	text := c.open(like)
	call := `data(this, "like")`

	t.Run("completion of data keys", func(t *testing.T) {
		pos := at(t, text, call, 0, len(`data(this, "`))
		var labels []string
		// The types come from the typed check in memory: retry until
		// they're there.
		deadline := time.Now().Add(30 * time.Second)
		for {
			res := c.call("textDocument/completion", map[string]any{
				"textDocument": map[string]any{"uri": c.uri(like)}, "position": pos,
				"context": map[string]any{"triggerKind": 2, "triggerCharacter": `"`}})
			labels = completionLabels(t, res)
			if contains(labels, "like") || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !contains(labels, "like") {
			t.Fatalf("completion in data(this, \"…\"): %v, want like", labels)
		}
	})

	t.Run("hover", func(t *testing.T) {
		h, r := c.hover(like, at(t, text, "like.likes", 0, 6))
		if !strings.Contains(h, "likes: number") {
			t.Errorf("hover on like.likes: %q, want likes: number", h)
		}
		if r == nil || rangeText(text, *r) != "likes" {
			t.Errorf("hover range %v", r)
		}
	})

	t.Run("diagnostic at the .orbit position", func(t *testing.T) {
		// None before (the types resolve).
		c.waitDiag(like, func(ds []map[string]any) bool { return len(tsDiags(ds)) == 0 })
		bad := strings.Replace(text, call, `data(this, "lik")`, 1)
		c.change(like, bad)
		ds := c.waitDiag(like, func(ds []map[string]any) bool { return len(tsDiags(ds)) > 0 })
		d := tsDiags(ds)[0]
		r := diagRange(t, d)
		if want := at(t, bad, `"lik"`, 0, 0); r.Start != want {
			t.Errorf("diagnostic at %v, want %v: %v", r.Start, want, d)
		}
		if !strings.Contains(d["message"].(string), `"lik"`) {
			t.Errorf("message %q", d["message"])
		}
		c.change(like, text)
		c.waitDiag(like, func(ds []map[string]any) bool { return len(tsDiags(ds)) == 0 })
	})

	t.Run("definition into util.ts", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/definition", c.textPos(like, at(t, text, "plural(like", 0, 1))))
		if len(locs) != 1 || !strings.HasSuffix(locs[0].URI, "src/scripts/util.ts") {
			t.Fatalf("definition of plural: %v", locs)
		}
		src, err := os.ReadFile(strings.TrimPrefix(locs[0].URI, "file://"))
		if err != nil {
			t.Fatal(err)
		}
		if got := rangeText(string(src), locs[0].Range); got != "plural" {
			t.Errorf("definition of plural is %q, want the declaration's name: %v", got, locs)
		}
	})

	t.Run("definition of data on the orbit.JSON attribute", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/definition", c.textPos(like, at(t, text, call, 0, 1))))
		if len(locs) != 1 || locs[0].URI != c.uri(like) || rangeText(text, locs[0].Range) != "data-like" {
			t.Errorf("definition of data: %v, want the data-like attribute", locs)
		}
	})

	t.Run("definition of a member on the Go field", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/definition", c.textPos(like, at(t, text, "like.likes", 0, 6))))
		if len(locs) != 1 || locs[0].URI != c.uri(like) || rangeText(text, locs[0].Range) != "Likes" ||
			locs[0].Range.Start.Line != at(t, text, "Likes int    `json", 0, 0).Line {
			t.Errorf("definition of likes: %v, want the Likes field of type like", locs)
		}
	})

	t.Run("references across files, one closed", func(t *testing.T) {
		res := c.call("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(like)}, "position": at(t, text, "plural(like", 0, 1),
			"context": map[string]any{"includeDeclaration": true}})
		var files []string
		for _, l := range c.locations(res) {
			files = append(files, strings.TrimPrefix(uriToPath(l.URI), dir+"/"))
		}
		for _, want := range []string{"src/scripts/util.ts", like, "src/components/Shared.orbit"} {
			if !contains(files, want) {
				t.Errorf("references of plural: %v, want %s", files, want)
			}
		}
		for _, f := range files {
			if strings.Contains(f, ".orbit/lsp") {
				t.Errorf("a virtual document leaked: %s", f)
			}
		}
	})

	t.Run("rename a script local", func(t *testing.T) {
		res := c.call("textDocument/rename", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(like)}, "position": at(t, text, "const button", 0, 7), "newName": "btn"})
		var we struct {
			DocumentChanges []struct {
				TextDocument struct{ URI string } `json:"textDocument"`
				Edits        []struct {
					Range   Range  `json:"range"`
					NewText string `json:"newText"`
				} `json:"edits"`
			} `json:"documentChanges"`
		}
		json.Unmarshal(res, &we)
		if len(we.DocumentChanges) != 1 || we.DocumentChanges[0].TextDocument.URI != c.uri(like) {
			t.Fatalf("rename of button: %s", res)
		}
		for _, e := range we.DocumentChanges[0].Edits {
			if rangeText(text, e.Range) != "button" || e.NewText != "btn" {
				t.Errorf("edit %v → %q covers %q", e.Range, e.NewText, rangeText(text, e.Range))
			}
		}
		if n := len(we.DocumentChanges[0].Edits); n != strings.Count(text, "button.")+1 {
			t.Errorf("%d edits", n)
		}
	})

	t.Run("rename of generated types refused", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := c.conn.Call(ctx, "textDocument/rename", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(like)}, "position": at(t, text, "like.likes", 0, 6), "newName": "count"})
		if err == nil || !strings.Contains(err.Error(), "orbit_gen/types") {
			t.Errorf("rename of likes: %v, want a refusal", err)
		}
	})

	t.Run("unsaved Go change updates the script", func(t *testing.T) {
		// The likes key becomes count in Go: like.likes turns red.
		changed := strings.Replace(text, "`json:\"likes\"`", "`json:\"count\"`", 1)
		if changed == text {
			t.Fatal("no likes tag in LikeButton.orbit")
		}
		c.change(like, changed)
		ds := c.waitDiag(like, func(ds []map[string]any) bool {
			for _, d := range tsDiags(ds) {
				if strings.Contains(d["message"].(string), "'likes'") {
					return true
				}
			}
			return false
		})
		d := tsDiags(ds)[0]
		if r := diagRange(t, d); r.Start != at(t, changed, "like.likes", 0, 5) {
			t.Errorf("diagnostic at %v, want on likes: %v", r.Start, d)
		}
		c.change(like, text)
		c.waitDiag(like, func(ds []map[string]any) bool { return len(tsDiags(ds)) == 0 })
	})

	t.Run("is:inline script as JavaScript", func(t *testing.T) {
		const inline = "src/pages/inline.orbit"
		itext := c.open(inline)
		res := c.call("textDocument/completion", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(inline)}, "position": at(t, itext, "greeting.\n", 0, len("greeting.")),
			"context": map[string]any{"triggerKind": 2, "triggerCharacter": "."}})
		if labels := completionLabels(t, res); !contains(labels, "appendChild") {
			t.Errorf("completion after greeting.: %d items, want appendChild", len(labels))
		}
		h, _ := c.hover(inline, at(t, itext, "greeting =", 0, 2))
		if !strings.Contains(h, "HTMLElement | null") {
			t.Errorf("hover on greeting: %q", h)
		}
		// The dangling "greeting." is a syntax error.
		c.waitDiag(inline, func(ds []map[string]any) bool { return len(tsDiags(ds)) > 0 })
	})

	t.Run("nothing written", func(t *testing.T) {
		for _, p := range []string{".orbit", "orbit_gen", "src/components/LikeButton.orbit.d.ts"} {
			if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
				t.Errorf("%s was written", p)
			}
		}
	})
}
