package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The LSP integration test (PLAN.md §13, the stage-1 bullets): orbit lsp
// spawned over stdio with the gopls on PATH, on a small project.

const cardOrbit = `---
// Props are the card's inputs.
type Props struct {
	// Post is the post's title.
	Post     string
	Featured bool
}
---
<article class="card">{props.Post}<slot /></article>
`

const indexOrbit = `---
import Card "../components/Card.orbit"

suffix := "!"
greeting := "Héllo ✓ 😀" + suffix
count := len(greeting)
---
<h1>Héllo ✓ 😀 {suffix}</h1>
<Card post={greeting} featured />
<p>{count}</p>
`

const slugOrbit = `---
heading := "Post " + params.Slug
---
<h1>{heading}</h1>
`

const badOrbit = `---
import Card "../components/Card.orbit"

x := undefinedThing
---
<Card psot="x" />
<p>{x}</p>
`

func TestIntegration(t *testing.T) {
	needGopls(t)
	dir := writeProject(t, map[string]string{
		"src/components/Card.orbit":   cardOrbit,
		"src/pages/index.orbit":       indexOrbit,
		"src/pages/blog/[slug].orbit": slugOrbit,
		"src/pages/bad.orbit":         badOrbit,
	})
	c := startClient(t, dir, nil)
	index := c.open("src/pages/index.orbit")

	t.Run("hover frontmatter variable", func(t *testing.T) {
		text, _ := c.hover("src/pages/index.orbit", at(t, index, "count :=", 0, 1))
		if !strings.Contains(text, "count int") {
			t.Errorf("hover on count: %q, want the type int", text)
		}
	})

	t.Run("hover after non-ASCII text", func(t *testing.T) {
		// Frontmatter: suffix after "Héllo ✓ 😀" on the same line.
		for _, tc := range []struct {
			name string
			pos  Position
		}{
			{"frontmatter", at(t, index, "suffix", 1, 2)},
			{"template", at(t, index, "{suffix}", 0, 3)},
		} {
			text, r := c.hover("src/pages/index.orbit", tc.pos)
			if !strings.Contains(text, "suffix string") {
				t.Errorf("%s: hover %q, want suffix string", tc.name, text)
			}
			if r == nil || rangeText(index, *r) != "suffix" {
				t.Errorf("%s: hover range %v covers %q, want suffix", tc.name, r, rangeTextOr(index, r))
			}
		}
		// References give UTF-16 ranges that cover exactly the name.
		res := c.call("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": c.uri("src/pages/index.orbit")},
			"position":     at(t, index, "suffix", 0, 0),
			"context":      map[string]any{"includeDeclaration": true},
		})
		locs := c.locations(res)
		if len(locs) != 3 {
			t.Errorf("references of suffix: %d locations, want 3 (declaration and two uses, no echo line): %s", len(locs), res)
		}
		for _, l := range locs {
			if got := rangeText(index, l.Range); got != "suffix" {
				t.Errorf("reference %v covers %q", l.Range, got)
			}
		}
	})

	t.Run("hover never shows line directives", func(t *testing.T) {
		for _, pos := range []Position{
			at(t, index, "post={", 0, 1),      // the prop's field key
			at(t, index, "greeting :=", 0, 1), // a frontmatter variable
			at(t, index, "Card \"", 0, 1),     // the .orbit import alias
		} {
			text, _ := c.hover("src/pages/index.orbit", pos)
			if strings.Contains(text, "line ") || strings.Contains(text, "orbit_gen") {
				t.Errorf("hover at %v shows generated details: %q", pos, text)
			}
		}
		card := c.open("src/components/Card.orbit")
		text, _ := c.hover("src/components/Card.orbit", at(t, card, "props.Post", 0, 7))
		if !strings.Contains(text, "Post string") || !strings.Contains(text, "post's title") || strings.Contains(text, "line ") {
			t.Errorf("hover on props.Post: %q", text)
		}
	})

	t.Run("misspelled prop diagnostic", func(t *testing.T) {
		bad, _ := os.ReadFile(filepath.Join(dir, "src/pages/bad.orbit"))
		ds := c.waitDiag("src/pages/bad.orbit", func(ds []map[string]any) bool { return len(ds) >= 2 })
		var gotProp, gotGo bool
		for _, d := range ds {
			r := diagRange(t, d)
			msg := d["message"].(string)
			switch {
			case strings.Contains(msg, `unknown prop "psot"`):
				gotProp = true
				if got := rangeText(string(bad), r); got != "psot" {
					t.Errorf("unknown prop diagnostic covers %q, want psot", got)
				}
			case strings.Contains(msg, "undefined: undefinedThing"):
				gotGo = true
				if got := rangeText(string(bad), r); got != "undefinedThing" {
					t.Errorf("gopls diagnostic covers %q, want undefinedThing", got)
				}
			}
			if strings.HasPrefix(msg, internalPrefix) {
				t.Errorf("unmapped diagnostic: %v", d)
			}
		}
		if !gotProp || !gotGo {
			t.Errorf("diagnostics of bad.orbit: %v, want the unknown prop and gopls's undefined", ds)
		}
	})

	t.Run("definition on a component tag", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/definition", c.textPos("src/pages/index.orbit", at(t, index, "<Card", 0, 2))))
		if len(locs) != 1 || uriToPath(locs[0].URI) != filepath.Join(dir, "src/components/Card.orbit") {
			t.Errorf("definition on <Card: %v", locs)
		}
		// On an attribute: the Props field.
		locs = c.locations(c.call("textDocument/definition", c.textPos("src/pages/index.orbit", at(t, index, "featured", 0, 2))))
		if len(locs) != 1 || uriToPath(locs[0].URI) != filepath.Join(dir, "src/components/Card.orbit") ||
			rangeText(cardOrbit, locs[0].Range) != "Featured" {
			t.Errorf("definition on featured: %v", locs)
		}
	})

	t.Run("definition on params", func(t *testing.T) {
		slug := c.open("src/pages/blog/[slug].orbit")
		page := filepath.Join(dir, "src/pages/blog/[slug].orbit")
		for _, pos := range []Position{at(t, slug, "params.Slug", 0, 8), at(t, slug, "params.Slug", 0, 2)} {
			locs := c.locations(c.call("textDocument/definition", c.textPos("src/pages/blog/[slug].orbit", pos)))
			if len(locs) != 1 || uriToPath(locs[0].URI) != page {
				t.Errorf("definition at %v: %v, want %s", pos, locs, page)
			}
		}
	})

	t.Run("unsaved Props change", func(t *testing.T) {
		c.change("src/pages/index.orbit", strings.Replace(index, "featured />", "featured tags={nil} />", 1))
		c.waitDiag("src/pages/index.orbit", func(ds []map[string]any) bool { return hasMessage(ds, `unknown prop "tags"`) })
		card := strings.Replace(cardOrbit, "Featured bool", "Featured bool\n\tTags     []string", 1)
		c.change("src/components/Card.orbit", card)
		c.waitDiag("src/pages/index.orbit", func(ds []map[string]any) bool { return len(ds) == 0 })
		// gopls agrees: no error in the page's generated code after the
		// diagnostics delay.
		time.Sleep(500 * time.Millisecond)
		if ds := c.waitDiag("src/pages/index.orbit", func([]map[string]any) bool { return true }); len(ds) != 0 {
			t.Errorf("index.orbit diagnostics after adding Tags: %v", ds)
		}
		if _, err := os.Stat(filepath.Join(dir, "orbit_gen")); !os.IsNotExist(err) {
			t.Errorf("orbit_gen/ was written: %v", err)
		}
	})

	t.Run("startup messages", func(t *testing.T) {
		c.waitFor("the orbit generate nudge", 30*time.Second, func() bool { return len(c.requests) > 0 })
		c.mu.Lock()
		defer c.mu.Unlock()
		if !strings.Contains(c.requests[0], "Run orbit generate for faster startup") {
			t.Errorf("showMessageRequest: %q", c.requests[0])
		}
		for _, m := range c.messages {
			if strings.Contains(m, "generated file") || strings.HasPrefix(m, "1:") {
				t.Errorf("unexpected message: %s", m)
			}
		}
	})
}

func rangeTextOr(text string, r *Range) string {
	if r == nil {
		return ""
	}
	return rangeText(text, *r)
}

func diagRange(t *testing.T, d map[string]any) Range {
	b, _ := json.Marshal(d["range"])
	var r Range
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func hasMessage(ds []map[string]any, sub string) bool {
	for _, d := range ds {
		if m, _ := d["message"].(string); strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// TestOldGopls: a gopls built with an older Go than the project's go
// directive is refused: Go features are off, the install command is shown,
// and native features keep working.
func TestOldGopls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	dir := writeProject(t, map[string]string{
		"src/components/Card.orbit": cardOrbit,
		"src/pages/index.orbit":     indexOrbit,
		"src/pages/bad.orbit":       badOrbit,
	})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, dir, map[string]any{"goplsPath": stub}, stubEnv+"=old")
	c.waitFor("the refusal message", 30*time.Second, func() bool {
		for _, m := range c.messages {
			if strings.HasPrefix(m, "1:") && strings.Contains(m, "go install golang.org/x/tools/gopls@latest") &&
				strings.Contains(m, "go1.23.4") {
				return true
			}
		}
		return false
	})
	index := c.open("src/pages/index.orbit")
	if text, _ := c.hover("src/pages/index.orbit", at(t, index, "count :=", 0, 1)); text != "" {
		t.Errorf("hover with a refused gopls: %q, want none", text)
	}
	locs := c.locations(c.call("textDocument/definition", c.textPos("src/pages/index.orbit", at(t, index, "<Card", 0, 2))))
	if len(locs) != 1 || uriToPath(locs[0].URI) != filepath.Join(dir, "src/components/Card.orbit") {
		t.Errorf("native definition on <Card: %v", locs)
	}
	c.waitDiag("src/pages/bad.orbit", func(ds []map[string]any) bool { return hasMessage(ds, `unknown prop "psot"`) })
}

// TestReferencesOverlayOnly: with no orbit_gen/ on disk and 50 components,
// references asked right at startup are complete: the readiness barrier
// holds them until gopls has loaded every overlay. It also measures startup.
func TestReferencesOverlayOnly(t *testing.T) { testReferences50(t, false) }

// TestReferencesOnDisk is TestReferencesOverlayOnly with orbit_gen/ on
// disk, except for the last five components (as if added since orbit
// generate ran): those are overlay-only, loaded one by one by the barrier.
func TestReferencesOnDisk(t *testing.T) { testReferences50(t, true) }

func testReferences50(t *testing.T, onDisk bool) {
	needGopls(t)
	const n = 50
	files := map[string]string{}
	var page strings.Builder
	page.WriteString("---\n")
	for i := range n {
		fmt.Fprintf(&page, "import C%d \"../components/C%d.orbit\"\n", i, i)
		files[fmt.Sprintf("src/components/C%d.orbit", i)] = fmt.Sprintf(
			"---\ntype Props struct {\n\tTitle    string\n\tFeatured bool\n}\nlabel := props.Title\n---\n<p class={label}>{props.Featured}</p>\n")
	}
	page.WriteString("title := \"x\"\n---\n")
	for i := range n {
		fmt.Fprintf(&page, "<C%d title={title} featured />\n", i)
	}
	files["src/pages/index.orbit"] = page.String()
	dir := writeProject(t, files)
	mode := "overlay-only"
	if onDisk {
		mode = "on disk"
		removeGenerated(t, dir, n-5, n)
	}

	c := startClient(t, dir, nil)
	start := c.start
	idx := c.open("src/pages/index.orbit")
	// First hover: how soon Go features answer.
	text, _ := c.hover("src/pages/index.orbit", at(t, idx, "title :=", 0, 1))
	firstHover := time.Since(start)
	if !strings.Contains(text, "title string") {
		t.Errorf("hover on title: %q", text)
	}
	var missed []string
	t0 := time.Now()
	for i := range n {
		rel := fmt.Sprintf("src/components/C%d.orbit", i)
		src := files[rel]
		res := c.call("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(rel)},
			"position":     at(t, src, "Featured", 0, 1),
			"context":      map[string]any{"includeDeclaration": true},
		})
		// The field, its use in C<i>'s template, and the page's attribute.
		if locs := c.locations(res); len(locs) != 3 {
			missed = append(missed, fmt.Sprintf("C%d:%d", i, len(locs)))
		}
	}
	refs := time.Since(t0)
	if len(missed) > 0 {
		t.Errorf("incomplete references: %v", missed)
	}
	c.mu.Lock()
	firstDiag := time.Duration(0)
	for _, at := range c.diagAt {
		if d := at.Sub(start); firstDiag == 0 || d < firstDiag {
			firstDiag = d
		}
	}
	c.mu.Unlock()
	t.Logf("%d components, %s: start → first diagnostics %v, → first hover %v; 50 references (barrier included) %v; RSS %s",
		n, mode, firstDiag.Round(time.Millisecond), firstHover.Round(time.Millisecond), refs.Round(time.Millisecond), rss(c))
	if _, err := os.Stat(filepath.Join(dir, "orbit_gen")); !onDisk && !os.IsNotExist(err) {
		t.Errorf("orbit_gen/ was written: %v", err)
	}
}

// removeGenerated runs orbit generate in dir, then removes the generated
// packages of components C<from> to C<to-1>.
func removeGenerated(t *testing.T, dir string, from, to int) {
	t.Helper()
	orbitGenerate(t, dir)
	for i := from; i < to; i++ {
		for _, kind := range []string{"decl", "impl"} {
			pkg := filepath.Join(dir, "orbit_gen", kind, "components", fmt.Sprintf("c%d", i))
			if _, err := os.Stat(pkg); err != nil {
				t.Fatalf("generated package: %v", err)
			}
			if err := os.RemoveAll(pkg); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// rss reports the resident memory of orbit lsp and its gopls (Linux).
func rss(c *testClient) string {
	self := procRSS(c.cmd.Process.Pid)
	var children []string
	tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", c.cmd.Process.Pid))
	for _, task := range tasks {
		b, _ := os.ReadFile(task)
		children = append(children, strings.Fields(string(b))...)
	}
	var gopls int
	for _, f := range children {
		var pid int
		fmt.Sscan(f, &pid)
		gopls += procRSS(pid)
	}
	if self == 0 {
		return "unknown"
	}
	return fmt.Sprintf("orbit lsp %d MB, gopls %d MB", self/1024, gopls/1024)
}

func procRSS(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			var kb int
			fmt.Sscan(strings.TrimSpace(v), &kb)
			return kb
		}
	}
	return 0
}
