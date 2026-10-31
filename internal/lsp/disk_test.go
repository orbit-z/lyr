package lsp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The on-disk half of the integration test (PLAN.md §9 Startup step 2):
// with a fresh lyr_gen/ from lyr generate, gopls reads the unedited,
// error-free files from disk (no overlay), and Go features map through the
// typed output's source maps.

// lyrGenerate runs lyr generate in dir.
func lyrGenerate(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command(buildLyr(t), "generate")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lyr generate: %v\n%s", err, out)
	}
}

// traceGopls returns the goplsPath init option of a gopls that logs its
// traffic, and a function that returns the generated files (module
// relative) gopls was sent a notification for so far (didOpen, didClose).
func traceGopls(t *testing.T, dir string) (map[string]any, func(method string) []string) {
	t.Helper()
	gopls, err := exec.LookPath("gopls")
	if err != nil {
		t.Skip("no gopls")
	}
	tmp := t.TempDir()
	log := filepath.Join(tmp, "gopls.log")
	script := filepath.Join(tmp, "gopls-trace")
	sh := fmt.Sprintf("#!/bin/sh\nexec %q -rpc.trace -logfile %q \"$@\"\n", gopls, log)
	if err := os.WriteFile(script, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(log)
			if i := strings.Index(string(b), "textDocument/references"); i >= 0 {
				t.Logf("gopls log at the first references:\n%s", b[max(i-200, 0):min(i+3000, len(b))])
			}
		}
	})
	sent := func(method string) []string {
		b, _ := os.ReadFile(log)
		re := regexp.MustCompile(`notification 'textDocument/` + method + `'.\s*Params: [^\n]*?"uri":"(file://[^"]+)"`)
		var out []string
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			rel, err := filepath.Rel(dir, uriToPath(m[1]))
			if err == nil && strings.HasPrefix(filepath.ToSlash(rel), "lyr_gen/") {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		slices.Sort(out)
		return out
	}
	return map[string]any{"goplsPath": script}, sent
}

func TestOnDisk(t *testing.T) {
	needGopls(t)
	dir := writeProject(t, map[string]string{
		"src/components/Card.lyr":   cardLyr,
		"src/pages/index.lyr":       indexLyr,
		"src/pages/blog/[slug].lyr": slugLyr,
	})
	lyrGenerate(t, dir)
	// A file added since: it has no lyr_gen/ files, so it's overlay-only.
	if err := os.WriteFile(filepath.Join(dir, "src/pages/bad.lyr"), []byte(badLyr), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, sent := traceGopls(t, dir)
	c := startClient(t, dir, opts)
	index := c.open("src/pages/index.lyr")
	card := c.open("src/components/Card.lyr")
	const indexPath, cardPath = "src/pages/index.lyr", "src/components/Card.lyr"

	// References wait for the readiness barrier.
	res := c.call("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.uri(indexPath)},
		"position":     at(t, index, "suffix", 0, 0),
		"context":      map[string]any{"includeDeclaration": true},
	})
	t.Run("only changed files are overlays", func(t *testing.T) {
		want := []string{"lyr_gen/decl/pages/bad/decl.go", "lyr_gen/impl/pages/bad/render.go"}
		if got := sent("didOpen"); !slices.Equal(got, want) {
			t.Errorf("gopls got didOpen for %v, want only %v", got, want)
		}
	})

	t.Run("references", func(t *testing.T) {
		locs := c.locations(res)
		if len(locs) != 3 {
			t.Errorf("references of suffix: %d locations, want 3: %s", len(locs), res)
		}
		for _, l := range locs {
			if got := rangeText(index, l.Range); got != "suffix" {
				t.Errorf("reference %v covers %q", l.Range, got)
			}
		}
		// A Props field: its use in Card's template and the page's attribute.
		locs = c.locations(c.call("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(cardPath)},
			"position":     at(t, card, "Featured", 0, 1),
			"context":      map[string]any{"includeDeclaration": true},
		}))
		var where []string
		for _, l := range locs {
			text := card
			if c.rel(l.URI) == indexPath {
				text = index
			}
			where = append(where, c.rel(l.URI)+":"+rangeText(text, l.Range))
		}
		slices.Sort(where)
		if want := []string{cardPath + ":Featured", indexPath + ":featured"}; !slices.Equal(where, want) {
			t.Errorf("references of Featured: %v, want %v", where, want)
		}
	})

	t.Run("hover", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			pos  Position
			want string
		}{
			{"frontmatter", at(t, index, "count :=", 0, 1), "count int"},
			{"template, a typed write", at(t, index, "{suffix}", 0, 3), "suffix string"},
			{"template, a typed write", at(t, index, "{count}", 0, 2), "count int"},
		} {
			text, r := c.hover(indexPath, tc.pos)
			if !strings.Contains(text, tc.want) {
				t.Errorf("%s: hover %q, want %q", tc.name, text, tc.want)
			}
			if r == nil {
				t.Errorf("%s: hover without a range", tc.name)
			}
		}
		text, _ := c.hover(cardPath, at(t, card, "props.Post", 0, 7))
		if !strings.Contains(text, "Post string") {
			t.Errorf("hover on props.Post: %q", text)
		}
	})

	t.Run("definition", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/definition", c.textPos(indexPath, at(t, index, "featured", 0, 2))))
		if len(locs) != 1 || c.rel(locs[0].URI) != cardPath || rangeText(card, locs[0].Range) != "Featured" {
			t.Errorf("definition on featured: %v", locs)
		}
		locs = c.locations(c.call("textDocument/definition", c.textPos(indexPath, at(t, index, "{count}", 0, 2))))
		if len(locs) != 1 || c.rel(locs[0].URI) != indexPath || rangeText(index, locs[0].Range) != "count" {
			t.Errorf("definition on {count}: %v", locs)
		}
	})

	t.Run("completion", func(t *testing.T) {
		items := c.complete(cardPath, at(t, card, "{props.Post}", 0, len("{props.")))
		if item(items, "Post") == nil || item(items, "Featured") == nil {
			t.Errorf("completion after props.: %v", labels(items))
		}
	})

	t.Run("rename", func(t *testing.T) {
		edits := c.workspaceEdit(c.call("textDocument/rename", c.renameParams(indexPath, at(t, index, "count :=", 0, 1), "n")), true)
		if len(edits) != 1 || len(edits[indexPath]) != 2 {
			t.Fatalf("rename count: %v", edits)
		}
		for _, e := range edits[indexPath] {
			if got := rangeText(index, e.editRange()); got != "count" {
				t.Errorf("rename edit covers %q", got)
			}
		}
	})

	t.Run("diagnostics", func(t *testing.T) {
		c.waitDiag("src/pages/bad.lyr", func(ds []map[string]any) bool {
			return hasMessage(ds, "undefined: undefinedThing") && hasMessage(ds, `unknown prop "psot"`)
		})
	})

	t.Run("first edit makes an overlay", func(t *testing.T) {
		edited := strings.Replace(index, "<p>{count}</p>", "<p>{count}</p>\n<p>{greeting}</p>", 1)
		c.change(indexPath, edited)
		text, _ := c.hover(indexPath, at(t, edited, "{greeting}", 0, 2))
		if !strings.Contains(text, "greeting string") {
			t.Errorf("hover after the edit: %q", text)
		}
		if got := sent("didOpen"); !slices.Contains(got, "lyr_gen/impl/pages/index/render.go") {
			t.Errorf("no overlay for index.lyr after its edit: %v", got)
		}

		// Saved, then lyr generate (or lyr dev) writes lyr_gen/: the
		// typed check after the watched-file event closes the overlay.
		if err := os.WriteFile(filepath.Join(dir, indexPath), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Remove(filepath.Join(dir, "src/pages/bad.lyr")) // lyr generate writes nothing with errors
		lyrGenerate(t, dir)
		os.WriteFile(filepath.Join(dir, "src/pages/bad.lyr"), []byte(badLyr), 0o644)
		c.conn.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{
			map[string]any{"uri": c.uri("lyr_gen/impl/pages/index/render.go"), "type": 2},
			map[string]any{"uri": c.uri("lyr_gen/decl/pages/index/decl.go"), "type": 2},
		}})
		deadline := time.Now().Add(30 * time.Second)
		for !slices.Contains(sent("didClose"), "lyr_gen/impl/pages/index/render.go") {
			if time.Now().After(deadline) {
				t.Fatalf("the overlay of index.lyr wasn't closed; didClose: %v", sent("didClose"))
			}
			time.Sleep(50 * time.Millisecond)
		}
		text, r := c.hover(indexPath, at(t, edited, "{greeting}", 0, 2))
		if !strings.Contains(text, "greeting string") || r == nil || rangeText(edited, *r) != "greeting" {
			t.Errorf("hover through lyr_gen/ after the check: %q %v", text, r)
		}
	})
}
