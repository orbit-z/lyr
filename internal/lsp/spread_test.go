package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Spreading props into a component in the editor (PLAN.md §2.11).

const spreadCard = `---
type Props struct {
	Post   string
	IsOpen bool
}
---
<article>{props.Post}</article>
`

const spreadPage = `---
import Card "../components/Card.orbit"

type Teaser struct{ Post string }

teaser := Teaser{Post: "x"}
---
<Card {...teaser} />
`

// TestSpreadLSP: hover, definition and completion in {...x} go to gopls,
// and renaming a Props field or a field of the spread struct succeeds with
// orbit_gen/ on disk, whose typed code assigns the spread's fields (those
// edits are dropped: the next generate re-emits them).
func TestSpreadLSP(t *testing.T) {
	needGopls(t)
	const card, page = "src/components/Card.orbit", "src/pages/index.orbit"
	files := map[string]string{card: spreadCard, page: spreadPage}
	dir := writeProject(t, maps2(files))
	orbitGenerate(t, dir)
	b, err := os.ReadFile(filepath.Join(dir, "orbit_gen/impl/pages/index/render.go"))
	if err != nil || !strings.Contains(string(b), "orbit__p.Post = orbit__s1.Post") {
		t.Fatalf("typed page on disk (%v):\n%s", err, b)
	}
	c := startClient(t, dir, nil)
	c.open(page)
	if text, _ := c.hover(page, at(t, spreadPage, "{...teaser}", 0, 5)); !strings.Contains(text, "Teaser") {
		t.Errorf("hover in {...teaser}: %q, want its type", text)
	}
	locs := c.locations(c.call("textDocument/definition", c.textPos(page, at(t, spreadPage, "{...teaser}", 0, 5))))
	if len(locs) != 1 || rangeText(spreadPage, locs[0].Range) != "teaser" || locs[0].Range.Start.Line != 5 {
		t.Errorf("definition in {...teaser}: %v", locs)
	}
	if items := labels(c.complete(page, at(t, spreadPage, "{...teaser}", 0, 6))); !slices.Contains(items, "teaser") {
		t.Errorf("completion in {...teaser}: %v", items)
	}

	// The callee's Props field: Card's own uses change, the page's typed
	// assignment is dropped.
	c.open(card)
	res := c.call("textDocument/rename", c.renameParams(card, at(t, spreadCard, "Post ", 0, 1), "Article"))
	checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
		card: strings.ReplaceAll(spreadCard, "Post", "Article"),
	})
	// The spread struct's field.
	res = c.call("textDocument/rename", c.renameParams(page, at(t, spreadPage, "Post string", 0, 1), "Body"))
	checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
		page: strings.ReplaceAll(spreadPage, "Post", "Body"),
	})
}

// TestSpreadTypedCheck: a spread's errors come from the typed check while
// typing (PLAN.md §2.11 (Q5) a): a struct with a field that isn't a prop
// is an error at the spread after the debounce, and fixing the struct
// clears it.
func TestSpreadTypedCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const card, page = "src/components/Card.orbit", "src/pages/index.orbit"
	dir := writeProject(t, map[string]string{card: spreadCard, page: spreadPage})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(stubEnv, "record:"+filepath.Join(t.TempDir(), "rec"))
	c := startInProcess(t, dir, map[string]any{"goplsPath": stub})
	c.open(page)
	none := func(ds []map[string]any) bool { return len(ds) == 0 }

	bad := strings.Replace(spreadPage, "struct{ Post string }", "struct{ Post, Rank string }", 1)
	c.change(page, bad)
	ds := c.waitDiag(page, func(ds []map[string]any) bool {
		if len(ds) != 1 {
			return false
		}
		m, _ := ds[0]["message"].(string)
		sev, _ := ds[0]["severity"].(float64)
		return strings.Contains(m, "has fields that aren't props of Card (Rank)") && sev == sevError
	})
	if r := diagRange(t, ds[0]); r.Start != at(t, bad, "{...teaser}", 0, 0) || r.End != at(t, bad, "{...teaser}", 0, len("{...teaser}")) {
		t.Errorf("error at %+v, want the spread", r)
	}
	c.change(page, spreadPage)
	c.waitDiag(page, none)
}
