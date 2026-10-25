package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttrSpelling(t *testing.T) {
	for _, tc := range []struct{ attr, old, new, want string }{
		{"post", "Post", "Article", "article"},
		{"post", "Post", "MainPost", "mainPost"},
		{"Post", "Post", "Article", "Article"},
		{"isOpen", "IsOpen", "IsClosed", "isClosed"},
		{"is-open", "IsOpen", "IsClosed", "is-closed"},
		{"is_open", "IsOpen", "IsClosed", "is_closed"},
		{"isopen", "IsOpen", "IsClosed", "isclosed"},
		{"IsOpen", "IsOpen", "IsClosed", "IsClosed"},
		{"Is_Open", "Is_Open", "Is_Closed", "Is_Closed"},
		{"ISOPEN", "IsOpen", "IsClosed", "isClosed"},
		{"isOPEN", "IsOpen", "IsClosed", "isClosed"},
		{"Is-Open", "IsOpen", "IsClosed", "is-closed"},
		{"user-id", "UserID", "OwnerID", "owner-id"},
		{"userId", "UserID", "OwnerID", "ownerID"},
		{"url", "URL", "Link", "link"},
		{"link", "Link", "ImageURL", "imageURL"},
		{"link", "Link", "HTTPServer", "httpServer"},
		{"the-link", "TheLink", "HTTPServer", "http-server"},
		{"the-link", "TheLink", "Page2Title", "page2-title"},
	} {
		if got := attrSpelling(tc.attr, tc.old, tc.new); got != tc.want {
			t.Errorf("attrSpelling(%q, %q, %q) = %q, want %q", tc.attr, tc.old, tc.new, got, tc.want)
		}
	}
}

const renameUnitCard = `---
type Props struct {
	Post   string
	IsOpen bool
}
label := "Héllo ✓ " + props.Post
---
<p class={label}>{props.IsOpen}</p>
`

const renameUnitPage = `---
import Card "../components/Card.orbit"

post := "x"
---
<h1>Héllo ✓</h1><Card post={post} is-open />
<Card {post} />
`

// goEdit is a gopls TextEdit at the nth occurrence of needle (from delta,
// n bytes long) in g.
func goEdit(t *testing.T, g *genFile, needle string, nth, delta, n int, text string) map[string]any {
	t.Helper()
	src := string(g.content)
	off := -1
	for i, from := 0, 0; i <= nth; i++ {
		j := strings.Index(src[from:], needle)
		if j < 0 {
			t.Fatalf("%s: %q occurrence %d not found in\n%s", g.rel, needle, nth, src)
		}
		off = from + j
		from = off + 1
	}
	off += delta
	return map[string]any{"range": g.lines.rangeOf(off, off+n, encUTF16), "newText": text}
}

func docChange(uri string, edits ...any) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri, "version": 7}, "edits": edits}
}

func TestRenameMapping(t *testing.T) {
	ws := memWorkspace(t, map[string]string{
		"src/components/Card.orbit": renameUnitCard,
		"src/pages/index.orbit":     renameUnitPage,
		"src/pages/[slug].orbit":    renameSlug,
	})
	const card, page = "src/components/Card.orbit", "src/pages/index.orbit"
	cdecl, cimpl := ws.genOf(card)
	_, pimpl := ws.genOf(page)
	sdecl, _ := ws.genOf("src/pages/[slug].orbit")
	m := mapper{ws: ws, enc: encUTF16}
	run := func(edit map[string]any, docChanges bool) (any, *rpcError) {
		raw, _ := json.Marshal(edit)
		set := newEditSet()
		rm := &renameMapper{mapper: m, touched: map[*genFile]bool{}}
		if err := rm.apply(raw, set); err != nil {
			return nil, err
		}
		return set.result(ws, docChanges)
	}
	edits := func(t *testing.T, res any, uri string) []textEdit {
		t.Helper()
		b, _ := json.Marshal(res)
		var we struct {
			Changes map[string][]textEdit `json:"changes"`
		}
		json.Unmarshal(b, &we)
		return we.Changes[uri]
	}

	t.Run("Props field", func(t *testing.T) {
		// The field, a props.Post use, the call sites' field keys, the
		// shorthand's key, the scratch file and a user file.
		user := pathToURI(filepath.Join(ws.root, "posts/posts.go"))
		scratch := pathToURI(filepath.Join(ws.root, scratchDir, "components/card/props.go"))
		userEdit := map[string]any{"range": Range{Start: Position{2, 1}, End: Position{2, 5}}, "newText": "Article"}
		res, err := run(map[string]any{"documentChanges": []any{
			docChange(cdecl.uri, goEdit(t, cdecl, "Post ", 0, 0, 4, "Article")),
			docChange(cimpl.uri, goEdit(t, cimpl, "props.Post", 0, 6, 4, "Article")),
			docChange(pimpl.uri, goEdit(t, pimpl, "Post/*", 0, 0, 4, "Article"), goEdit(t, pimpl, "Post/*", 1, 0, 4, "Article")),
			docChange(scratch, map[string]any{"range": Range{}, "newText": "x"}),
			docChange(user, userEdit),
		}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := applyEdits(renameUnitCard, edits(t, res, ws.orbitURI(card))), strings.ReplaceAll(renameUnitCard, "Post", "Article"); got != want {
			t.Errorf("Card.orbit:\n%s\nwant:\n%s", got, want)
		}
		want := strings.NewReplacer("post={", "article={", "Card {post}", "Card article={post}").Replace(renameUnitPage)
		if got := applyEdits(renameUnitPage, edits(t, res, ws.orbitURI(page))); got != want {
			t.Errorf("index.orbit:\n%s\nwant:\n%s", got, want)
		}
		if u := edits(t, res, user); len(u) != 1 || u[0].NewText != "Article" {
			t.Errorf("user file edits: %v", u)
		}
		if s := edits(t, res, scratch); s != nil {
			t.Errorf("scratch edits: %v", s)
		}
	})

	t.Run("echo dropped", func(t *testing.T) {
		res, err := run(map[string]any{"changes": map[string]any{cimpl.uri: []any{
			goEdit(t, cimpl, "label :=", 0, 0, 5, "text"),
			goEdit(t, cimpl, "_ = label", 0, 4, 5, "text"),
			goEdit(t, cimpl, "*/label/*", 0, 2, 5, "text"),
		}}}, true)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		var we struct {
			DocumentChanges []struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version *int   `json:"version"`
				} `json:"textDocument"`
				Edits []textEdit `json:"edits"`
			} `json:"documentChanges"`
		}
		json.Unmarshal(b, &we)
		if len(we.DocumentChanges) != 1 || we.DocumentChanges[0].TextDocument.URI != ws.orbitURI(card) ||
			we.DocumentChanges[0].TextDocument.Version != nil {
			t.Fatalf("documentChanges: %s", b)
		}
		if got, want := applyEdits(renameUnitCard, we.DocumentChanges[0].Edits), strings.ReplaceAll(renameUnitCard, "label", "text"); got != want {
			t.Errorf("Card.orbit:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("shorthand variable", func(t *testing.T) {
		// Renaming the variable post keeps the prop name: post={value}.
		res, err := run(map[string]any{"changes": map[string]any{pimpl.uri: []any{
			goEdit(t, pimpl, "post :=", 0, 0, 4, "value"),
			goEdit(t, pimpl, "*/post/*", 0, 2, 4, "value"),
			goEdit(t, pimpl, "*/post/*", 1, 2, 4, "value"),
		}}}, false)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.NewReplacer("post :=", "value :=", "Card {post}", "Card post={value}", "{post}", "{value}").Replace(renameUnitPage)
		if got := applyEdits(renameUnitPage, edits(t, res, ws.orbitURI(page))); got != want {
			t.Errorf("index.orbit:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			g    *genFile
			edit map[string]any
			want string
		}{
			{"glue", cimpl, goEdit(t, cimpl, "orbit__w", 0, 0, 8, "w"), "can't rename orbit__w: it would change Orbit's generated code"},
			{"params", sdecl, goEdit(t, sdecl, "Slug", 0, 0, 4, "ID"), "can't rename params.Slug: route params come from the file path (src/pages/[slug].orbit); rename the file instead"},
			{"part of a span", pimpl, goEdit(t, pimpl, "Render/*", 0, 0, 6, "Draw"), "can't rename Render"},
		} {
			// One bad edit rejects the whole rename.
			_, err := run(map[string]any{"documentChanges": []any{
				docChange(cimpl.uri, goEdit(t, cimpl, "label :=", 0, 0, 5, "text")), docChange(tc.g.uri, tc.edit)}}, false)
			if err == nil || !strings.Contains(err.Message, tc.want) || err.Code != codeRequestFailed {
				t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
			}
		}
		// Two different edits of the same .orbit range conflict.
		_, err := run(map[string]any{"changes": map[string]any{cimpl.uri: []any{
			goEdit(t, cimpl, "label :=", 0, 0, 5, "a"), goEdit(t, cimpl, "label :=", 0, 0, 5, "b")}}}, false)
		if err == nil || !strings.Contains(err.Message, "conflicting edits") {
			t.Errorf("conflicting edits: %v", err)
		}
		// The same edit twice (an import in both generated files) is merged.
		res, err := run(map[string]any{"changes": map[string]any{cimpl.uri: []any{
			goEdit(t, cimpl, "label :=", 0, 0, 5, "a"), goEdit(t, cimpl, "label :=", 0, 0, 5, "a")}}}, false)
		if err != nil || len(edits(t, res, ws.orbitURI(card))) != 1 {
			t.Errorf("duplicate edits: %v %v", res, err)
		}
	})
}

func TestAliasAt(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.orbit": renameUnitCard, "src/pages/index.orbit": renameIndex})
	f := ws.byPath["src/pages/index.orbit"]
	for _, needle := range []string{"Card \"", "<Card post", "</Card>"} {
		off := strings.Index(renameIndex, needle) + strings.IndexByte(needle, 'C') + 1
		im, sp, ok := aliasAt(f, off)
		if !ok || im.Alias != "Card" || renameIndex[sp.Start:sp.End] != "Card" {
			t.Errorf("aliasAt %q: %v %v", needle, im, ok)
		}
	}
	if _, _, ok := aliasAt(f, strings.Index(renameIndex, "title :=")); ok {
		t.Error("aliasAt on a variable")
	}
}
