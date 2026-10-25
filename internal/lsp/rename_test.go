package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The LSP integration test's rename bullets (PLAN.md §13): orbit lsp with
// the gopls on PATH.

const renameCard = `---
// Props are the card's inputs.
type Props struct {
	Post   string
	IsOpen bool
}
---
<article class="card">{props.Post}<slot /></article>
`

const renameIndex = `---
import Card "../components/Card.orbit"

title := "Héllo ✓ 😀"
---
<h1>Héllo ✓ 😀 {title}</h1>
<Card post={title} is-open />
<Card post={title} isOpen>x</Card>
`

const renameAbout = `---
import Card "../components/Card.orbit"

post := "x"
---
<Card {post} isopen />
<Card post="y" IsOpen={true} />
`

const renameSlug = `---
heading := "Post " + params.Slug
---
<h1>{heading}</h1>
`

const renameLink = `---
import "net/url"

type Props struct {
	Link *url.URL
}

u := url.URL{Path: "/x"}
---
<a href={u.String()}>{props.Link}</a>
`

// callErr is call that returns the error too.
func (c *testClient) callErr(method string, params any) (jsonRaw, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.conn.Call(ctx, method, params)
}

func (c *testClient) renameParams(rel string, pos Position, newName string) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(rel)}, "position": pos, "newName": newName}
}

// workspaceEdit decodes a WorkspaceEdit into edits per project-relative
// path, checking its form: documentChanges (with the versions of open
// documents) when docChanges, else changes.
func (c *testClient) workspaceEdit(res jsonRaw, docChanges bool) map[string][]textEdit {
	c.t.Helper()
	defer func() {
		if c.t.Failed() {
			c.t.Logf("WorkspaceEdit: %s", res)
		}
	}()
	var we struct {
		Changes         map[string][]textEdit `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version *int   `json:"version"`
			} `json:"textDocument"`
			Edits []textEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(res, &we); err != nil {
		c.t.Fatalf("WorkspaceEdit %s: %v", res, err)
	}
	if docChanges != (we.Changes == nil) || docChanges != (we.DocumentChanges != nil) {
		c.t.Errorf("WorkspaceEdit form (documentChanges %v): %s", docChanges, res)
	}
	out := map[string][]textEdit{}
	for uri, edits := range we.Changes {
		out[c.rel(uri)] = append(out[c.rel(uri)], edits...)
	}
	for _, dc := range we.DocumentChanges {
		rel := c.rel(dc.TextDocument.URI)
		c.mu.Lock()
		v, open := c.versions[rel]
		c.mu.Unlock()
		switch {
		case open && (dc.TextDocument.Version == nil || *dc.TextDocument.Version != v):
			c.t.Errorf("%s is open at version %d, edit version %v", rel, v, dc.TextDocument.Version)
		case !open && dc.TextDocument.Version != nil:
			c.t.Errorf("%s isn't open, edit version %d, want null", rel, *dc.TextDocument.Version)
		}
		out[rel] = append(out[rel], dc.Edits...)
	}
	return out
}

func (c *testClient) rel(uri string) string {
	r, err := filepath.Rel(c.dir, uriToPath(uri))
	if err != nil {
		return uri
	}
	return filepath.ToSlash(r)
}

// checkRename applies a WorkspaceEdit to files and compares with want
// (the files it must change, with their new text); no other file may
// change.
func checkRename(t *testing.T, edits map[string][]textEdit, files, want map[string]string) {
	t.Helper()
	for rel := range edits {
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected edits of %s: %v", rel, edits[rel])
		}
	}
	for rel, w := range want {
		if got := applyEdits(files[rel], edits[rel]); got != w {
			t.Errorf("%s after the rename:\n%s\nwant:\n%s", rel, got, w)
		}
	}
}

// rpcMessage returns the message of a JSON-RPC error, or "".
func rpcMessage(err error) string {
	if re, ok := err.(*rpcError); ok {
		return re.Message
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestRename(t *testing.T) {
	needGopls(t)
	files := map[string]string{
		"src/components/Card.orbit":   renameCard,
		"src/components/Link.orbit":   renameLink,
		"src/pages/index.orbit":       renameIndex,
		"src/pages/about.orbit":       renameAbout,
		"src/pages/blog/[slug].orbit": renameSlug,
	}
	dir := writeProject(t, maps2(files))
	c := startClient(t, dir, nil)
	if !strings.Contains(string(c.initResult), `"renameProvider":{"prepareProvider":true}`) {
		t.Errorf("initialize: renameProvider with prepareProvider missing: %s", c.initResult)
	}
	index := c.open("src/pages/index.orbit")
	card := c.open("src/components/Card.orbit")
	const indexPath, cardPath = "src/pages/index.orbit", "src/components/Card.orbit"

	t.Run("frontmatter variable", func(t *testing.T) {
		// title is used after non-ASCII text on the same template line.
		for _, pos := range []Position{at(t, index, "title :=", 0, 2), at(t, index, "{title}", 0, 3)} {
			res := c.call("textDocument/prepareRename", c.textPos(indexPath, pos))
			var pr struct {
				Range       Range  `json:"range"`
				Placeholder string `json:"placeholder"`
			}
			json.Unmarshal(res, &pr)
			if pr.Placeholder != "title" || rangeText(index, pr.Range) != "title" {
				t.Errorf("prepareRename at %v: %s", pos, res)
			}
		}
		res := c.call("textDocument/rename", c.renameParams(indexPath, at(t, index, "{title}", 0, 3), "heading"))
		edits := c.workspaceEdit(res, true)
		// The declaration, {title} and both post={title}: the echo line's
		// edit is dropped.
		if n := len(edits[indexPath]); n != 4 {
			t.Errorf("%d edits, want 4: %v", n, edits[indexPath])
		}
		checkRename(t, edits, files, map[string]string{indexPath: strings.ReplaceAll(index, "title", "heading")})
	})

	t.Run("Props field from its declaration", func(t *testing.T) {
		res := c.call("textDocument/rename", c.renameParams(cardPath, at(t, card, "Post ", 0, 1), "Article"))
		checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
			cardPath:                strings.ReplaceAll(card, "Post", "Article"),
			indexPath:               strings.ReplaceAll(index, "post=", "article="),
			"src/pages/about.orbit": strings.NewReplacer("{post}", "article={post}", `post="y"`, `article="y"`).Replace(renameAbout),
		})
	})

	t.Run("Props field from props.Field", func(t *testing.T) {
		res := c.call("textDocument/rename", c.renameParams(cardPath, at(t, card, "props.Post", 0, 7), "Article"))
		if len(c.workspaceEdit(res, true)) != 3 {
			t.Errorf("rename from props.Post: %s", res)
		}
	})

	t.Run("Props field from an attribute", func(t *testing.T) {
		pos := at(t, index, "is-open", 0, 3)
		res := c.call("textDocument/prepareRename", c.textPos(indexPath, pos))
		if !strings.Contains(string(res), `"placeholder":"is-open"`) {
			t.Errorf("prepareRename on is-open: %s", res)
		}
		res = c.call("textDocument/rename", c.renameParams(indexPath, pos, "is-closed"))
		checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
			cardPath:                strings.ReplaceAll(card, "IsOpen", "IsClosed"),
			indexPath:               strings.NewReplacer("is-open", "is-closed", "isOpen", "isClosed").Replace(index),
			"src/pages/about.orbit": strings.NewReplacer("isopen", "isclosed", "IsOpen=", "IsClosed=").Replace(renameAbout),
		})
	})

	t.Run("refused", func(t *testing.T) {
		slug := files["src/pages/blog/[slug].orbit"]
		for _, tc := range []struct {
			name, rel, text, at string
			delta               int
			want                string
		}{
			{"glue props", cardPath, card, "props.Post", 1, "Orbit's generated code declares it"},
			{"params field", "src/pages/blog/[slug].orbit", slug, "params.Slug", 8, "rename the file instead"},
			{"params", "src/pages/blog/[slug].orbit", slug, "params.Slug", 2, "Orbit's generated code declares it"},
			{"Props type", cardPath, card, "Props struct", 1, "can't rename Props"},
		} {
			pos := at(t, tc.text, tc.at, 0, tc.delta)
			for _, method := range []string{"textDocument/prepareRename", "textDocument/rename"} {
				_, err := c.callErr(method, c.renameParams(tc.rel, pos, "Other"))
				if msg := rpcMessage(err); !strings.Contains(msg, tc.want) {
					t.Errorf("%s: %s: error %q, want %q", tc.name, method, msg, tc.want)
				}
			}
		}
		// Template text: nothing to rename.
		if res := c.call("textDocument/prepareRename", c.textPos(indexPath, at(t, index, "<h1>", 0, 2))); !isNull(res) {
			t.Errorf("prepareRename on markup: %s", res)
		}
		// A Props field must stay exported.
		_, err := c.callErr("textDocument/rename", c.renameParams(cardPath, at(t, card, "Post ", 0, 1), "article"))
		if msg := rpcMessage(err); !strings.Contains(msg, "Props field article must be exported") {
			t.Errorf("unexported Props field: %q", msg)
		}
	})

	t.Run("import alias", func(t *testing.T) {
		want := strings.NewReplacer("Card \"", "Tile \"", "<Card", "<Tile", "</Card>", "</Tile>").Replace(index)
		for _, pos := range []Position{at(t, index, "Card \"", 0, 1), at(t, index, "<Card", 1, 2), at(t, index, "</Card>", 0, 3)} {
			res := c.call("textDocument/prepareRename", c.textPos(indexPath, pos))
			if !strings.Contains(string(res), `"placeholder":"Card"`) {
				t.Errorf("prepareRename at %v: %s", pos, res)
			}
			res = c.call("textDocument/rename", c.renameParams(indexPath, pos, "Tile"))
			checkRename(t, c.workspaceEdit(res, true), files, map[string]string{indexPath: want})
		}
		_, err := c.callErr("textDocument/rename", c.renameParams(indexPath, at(t, index, "<Card", 0, 2), "tile"))
		if msg := rpcMessage(err); msg != "component alias tile must start with an uppercase letter (rename it to Tile)" {
			t.Errorf("lowercase alias: %q", msg)
		}
	})

	t.Run("import in both generated files", func(t *testing.T) {
		// net/url is used by Props (declaration file) and the body
		// (implementation file).
		link := c.open("src/components/Link.orbit")
		res := c.call("textDocument/rename", c.renameParams("src/components/Link.orbit", at(t, link, "url.URL{", 0, 1), "neturl"))
		checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
			"src/components/Link.orbit": strings.NewReplacer(`import "net/url"`, `import neturl "net/url"`, "url.URL", "neturl.URL").Replace(link),
		})
	})

	t.Run("nothing written", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, "orbit_gen")); !os.IsNotExist(err) {
			t.Errorf("orbit_gen/ was written: %v", err)
		}
	})
}

// TestRenameAliasGoUse: renaming an alias from its use in Go code renames
// the import, the Go uses and the tags.
func TestRenameAliasGoUse(t *testing.T) {
	needGopls(t)
	const page = `---
import Card "../components/Card.orbit"

p := Card.Props{Post: "x"}
---
<Card {...p} />
<Card post="y"></Card>
`
	files := map[string]string{"src/components/Card.orbit": renameCard, "src/pages/index.orbit": page}
	dir := writeProject(t, maps2(files))
	c := startClient(t, dir, nil)
	c.open("src/pages/index.orbit")
	pos := at(t, page, "Card.Props", 0, 1)
	res := c.call("textDocument/prepareRename", c.textPos("src/pages/index.orbit", pos))
	if !strings.Contains(string(res), `"placeholder":"Card"`) {
		t.Errorf("prepareRename on Card.Props: %s", res)
	}
	res = c.call("textDocument/rename", c.renameParams("src/pages/index.orbit", pos, "Tile"))
	checkRename(t, c.workspaceEdit(res, true), files, map[string]string{
		"src/pages/index.orbit": strings.ReplaceAll(strings.ReplaceAll(page, "Card", "Tile"), "Tile.orbit", "Card.orbit"),
	})
}

// TestRenameChanges: an editor without documentChanges support gets
// changes, and renameProvider without prepareProvider.
func TestRenameChanges(t *testing.T) {
	needGopls(t)
	files := map[string]string{"src/components/Card.orbit": renameCard, "src/pages/index.orbit": renameIndex}
	dir := writeProject(t, maps2(files))
	c := startClient(t, dir, map[string]any{noDocumentChanges: true})
	if !strings.Contains(string(c.initResult), `"renameProvider":true`) {
		t.Errorf("initialize: want renameProvider true: %s", c.initResult)
	}
	card := c.open("src/components/Card.orbit")
	res := c.call("textDocument/rename", c.renameParams("src/components/Card.orbit", at(t, card, "Post ", 0, 1), "Article"))
	checkRename(t, c.workspaceEdit(res, false), files, map[string]string{
		"src/components/Card.orbit": strings.ReplaceAll(card, "Post", "Article"),
		"src/pages/index.orbit":     strings.ReplaceAll(renameIndex, "post=", "article="),
	})
}

// TestRenameOverlayOnly: with no orbit_gen/ on disk and 50 components, a
// rename asked right at startup is complete (it waits for the readiness
// barrier); then the warm latency of prepareRename and rename.
func TestRenameOverlayOnly(t *testing.T) {
	needGopls(t)
	const n = 50
	files := map[string]string{}
	var page strings.Builder
	page.WriteString("---\n")
	for i := range n {
		fmt.Fprintf(&page, "import C%d \"../components/C%d.orbit\"\n", i, i)
		files[fmt.Sprintf("src/components/C%d.orbit", i)] =
			"---\ntype Props struct {\n\tTitle    string\n\tFeatured bool\n}\nlabel := props.Title\n---\n<p class={label}>{props.Featured}</p>\n"
	}
	page.WriteString("title := \"x\"\n---\n")
	for i := range n {
		fmt.Fprintf(&page, "<C%d title={title} featured />\n", i)
	}
	files["src/pages/index.orbit"] = page.String()
	dir := writeProject(t, maps2(files))
	c := startClient(t, dir, nil)

	rename := func(i int, newName string) map[string][]textEdit {
		rel := fmt.Sprintf("src/components/C%d.orbit", i)
		res := c.call("textDocument/rename", c.renameParams(rel, at(t, files[rel], "Featured", 0, 1), newName))
		return c.workspaceEdit(res, true)
	}
	check := func(i int, edits map[string][]textEdit) {
		rel := fmt.Sprintf("src/components/C%d.orbit", i)
		if len(edits[rel]) != 2 || len(edits["src/pages/index.orbit"]) != 1 || len(edits) != 2 {
			t.Errorf("C%d: incomplete rename: %v", i, edits)
			return
		}
		if e := edits["src/pages/index.orbit"][0]; e.NewText != "highlighted" ||
			rangeText(files["src/pages/index.orbit"], e.editRange()) != "featured" {
			t.Errorf("C%d: call-site edit %v", i, e)
		}
	}
	t0 := time.Now()
	check(n-1, rename(n-1, "Highlighted"))
	first := time.Since(t0)

	var prep, ren []time.Duration
	for i := range n - 1 {
		rel := fmt.Sprintf("src/components/C%d.orbit", i)
		pos := at(t, files[rel], "Featured", 0, 1)
		t1 := time.Now()
		res := c.call("textDocument/prepareRename", c.textPos(rel, pos))
		prep = append(prep, time.Since(t1))
		if !strings.Contains(string(res), `"placeholder":"Featured"`) {
			t.Errorf("C%d: prepareRename %s", i, res)
		}
		t1 = time.Now()
		edits := rename(i, "Highlighted")
		ren = append(ren, time.Since(t1))
		check(i, edits)
	}
	t.Logf("%d components, overlay-only: first rename (barrier included) %v; warm prepareRename p50 %v max %v; rename p50 %v max %v",
		n, first.Round(time.Millisecond), p50(prep), slices.Max(prep).Round(time.Microsecond), p50(ren), slices.Max(ren).Round(time.Microsecond))
	if _, err := os.Stat(filepath.Join(dir, "orbit_gen")); !os.IsNotExist(err) {
		t.Errorf("orbit_gen/ was written: %v", err)
	}
}

func p50(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2].Round(time.Microsecond)
}

// maps2 copies a file map (writeProject adds go.mod and go.sum to it).
func maps2(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
