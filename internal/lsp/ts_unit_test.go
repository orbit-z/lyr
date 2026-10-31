package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/orbit-z/lyr/internal/check"
	"github.com/orbit-z/lyr/internal/compiler"
)

// utf16Col is the UTF-16 column of byte offset off in its line.
func utf16Col(s string, off int) (line, col int) {
	ls := strings.LastIndexByte(s[:off], '\n') + 1
	return strings.Count(s[:off], "\n"), len(utf16.Encode([]rune(s[ls:off])))
}

func TestBlankText(t *testing.T) {
	src := "---\nx := \"é✓😀\"\r\n---\n<p>😀</p><script>let a = \"é😀\";\r\nlet b = 1;</script>\n<script>let c = 2;</script>\n"
	first := strings.Index(src, "<script>") + len("<script>")
	second := strings.LastIndex(src, "<script>") + len("<script>")
	spans := []compiler.Span{
		{Start: first, End: strings.Index(src, "</script>")},
		{Start: second, End: strings.LastIndex(src, "</script>")},
	}
	t.Run("one block keeps lines and UTF-16 columns", func(t *testing.T) {
		got := blankText(src, spans[:1], vdocTS)
		if !strings.HasSuffix(got, moduleTail) {
			t.Fatalf("no module tail: %q", got)
		}
		body := strings.TrimSuffix(got, moduleTail)
		if strings.Count(body, "\n") != strings.Count(src, "\n") || strings.Count(body, "\r") != strings.Count(src, "\r") {
			t.Fatalf("line breaks changed: %q", body)
		}
		// Every character of the block is at the same line and UTF-16
		// column in both texts.
		for _, needle := range []string{"let a", "é😀", "let b"} {
			i, j := strings.Index(src, needle), strings.Index(body, needle)
			if j < 0 {
				t.Fatalf("%q blanked: %q", needle, body)
			}
			l1, c1 := utf16Col(src, i)
			l2, c2 := utf16Col(body, j)
			if l1 != l2 || c1 != c2 {
				t.Errorf("%q at %d:%d, want %d:%d", needle, l2, c2, l1, c1)
			}
		}
		if strings.Contains(body, "let c") || strings.Contains(body, "x :=") || strings.Contains(body, "<p>") {
			t.Errorf("text outside the block kept: %q", body)
		}
		// An astral character outside is two spaces (two UTF-16 units).
		x := newLineIndex(body)
		if p := x.position(strings.Index(body, "let a"), encUTF16); p != posAt(src, first) {
			t.Errorf("block start at %v, want %v", p, posAt(src, first))
		}
	})
	t.Run("merged classic scripts are separated", func(t *testing.T) {
		got := blankText(src, spans, vdocJS)
		if strings.HasSuffix(got, moduleTail) {
			t.Error("a classic document got the module tail")
		}
		if !strings.Contains(got, ";let a") || !strings.Contains(got, ";let c") {
			t.Errorf("no ; before each script: %q", got)
		}
		if !strings.Contains(got, "let c = 2;") || !strings.Contains(got, "let a") {
			t.Errorf("a script was blanked: %q", got)
		}
	})
}

func TestBuildVdocs(t *testing.T) {
	ws := memWorkspace(t, map[string]string{
		"src/pages/index.lyr": `---
---
<script>const a: number = 1;</script>
<script src="./x.ts"></script>
<script>const b = 2;</script>
<script is:inline>var c = 3;</script>
<script>/* nothing */</script>
<script type="module">import "x";</script>
<script type="text/javascript">var d = 4;</script>
<script type="application/ld+json">{}</script>
<script type="application/json" set:json={1}></script>
<script is:inline src="https://example.com/x.js"></script>
`,
		"src/pages/open.lyr": "---\n---\n<p>x</p>\n<script>\nlet unclosed = 1;\n",
	})
	docs := ws.vdocsOf("src/pages/index.lyr")
	var got []string
	for _, d := range docs {
		got = append(got, strings.TrimPrefix(uriToPath(d.uri), ws.root+"/")+" "+d.kind.languageID())
	}
	want := []string{
		".lyr/lsp/src/pages/index.lyr.0.ts typescript",
		".lyr/lsp/src/pages/index.lyr.2.ts typescript", // 1 is the src script, like lyr check's names
		".lyr/lsp/src/pages/index.lyr.inline.js javascript",
		".lyr/lsp/src/pages/index.lyr.3.ts typescript",
		".lyr/lsp/src/pages/index.lyr.module.0.js javascript",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("documents:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var inline *vdoc
	for _, d := range docs {
		if d.kind == vdocJS {
			inline = d
		}
	}
	if len(inline.spans) != 2 || !strings.Contains(inline.text, "var c = 3;") || !strings.Contains(inline.text, "var d = 4;") {
		t.Errorf("classic scripts: %d spans, %q", len(inline.spans), inline.text)
	}
	if strings.Contains(inline.text, "const a") {
		t.Error("the classic document holds a processed script")
	}
	// The unclosed script (tolerant parse) still gets a document.
	if d := ws.vdocsOf("src/pages/open.lyr"); len(d) != 1 || !strings.Contains(d[0].text, "let unclosed = 1;") {
		t.Errorf("unclosed script: %v", d)
	}
	// Positions route by content span, end inclusive.
	src := ws.byPath["src/pages/index.lyr"].Src
	if d := ws.vdocAt("src/pages/index.lyr", strings.Index(src, "const b")); d == nil || d != docs[1] {
		t.Errorf("vdocAt const b: %v", d)
	}
	if d := ws.vdocAt("src/pages/index.lyr", strings.Index(src, "<script>")); d != nil {
		t.Errorf("vdocAt on the tag: %v", d.uri)
	}
	// URIs map back.
	for _, d := range docs {
		if ws.vdocByURI(d.uri) != d || lyrOfVdocPath(ws.rel(uriToPath(d.uri))) != "src/pages/index.lyr" {
			t.Errorf("%s doesn't map back", d.uri)
		}
	}
}

func TestTsconfigText(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/pages/index.lyr": "<p/>", "tsconfig.json": `{"include": ["src/**/*.ts"]}`})
	u, err := check.ReadUserTsconfig(ws.root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tsconfigText(ws.root), check.Tsconfig(u); got != want {
		t.Errorf("in-memory tsconfig:\n%s\nwant lyr check's:\n%s", got, want)
	}
	if ws.tsconfigURI() != pathToURI(filepath.Join(ws.root, ".lyr", "lsp", "tsconfig.json")) {
		t.Errorf("tsconfig URI %s", ws.tsconfigURI())
	}
}

func TestTypesPaths(t *testing.T) {
	for _, tc := range []struct{ rel, want string }{
		{"lyr_gen/types/go.d.ts", "lyr_gen/types/go.d.ts"},
		{"lyr_gen/types/src/components/Like.lyr.d.ts", "lyr_gen/types/src/components/Like.lyr.d.ts"},
		{"src/components/Like.lyr.d.ts", "lyr_gen/types/src/components/Like.lyr.d.ts"},
		{"src/scripts/util.ts", ""},
	} {
		got, _ := typesRel(tc.rel)
		if got != tc.want {
			t.Errorf("typesRel(%s) = %q, want %q", tc.rel, got, tc.want)
		}
	}
	if got := tsOverlayRel("lyr_gen/types/src/components/Like.lyr.d.ts"); got != "src/components/Like.lyr.d.ts" {
		t.Errorf("unit overlay at %s", got)
	}
	if got := tsOverlayRel("lyr_gen/types/go.d.ts"); got != "lyr_gen/types/go.d.ts" {
		t.Errorf("go.d.ts overlay at %s", got)
	}
}

// tsUnitServer is a server on a workspace, for the mapper.
func tsUnitServer(t *testing.T, files map[string]string, documentChanges bool) (*Server, *workspace) {
	ws := memWorkspace(t, files)
	s := &Server{ws: ws, caps: clientCaps{documentChanges: documentChanges}}
	return s, ws
}

const likeLyr = `---
type like struct {
	Likes int ` + "`json:\"likes\"`" + `
}
---
<like-button data-like={lyr.JSON(like{1})}></like-button>
<script>
  import { data } from "./Like.lyr";
  const n = data(document.body, "like").likes;
</script>
`

func TestTSMapping(t *testing.T) {
	s, ws := tsUnitServer(t, map[string]string{"src/components/Like.lyr": likeLyr}, true)
	const p = "src/components/Like.lyr"
	src := ws.byPath[p].Src
	lines := ws.lines[p]
	d := ws.vdocsOf(p)[0]
	m := s.tsMapper(ws)
	rangeAt := func(needle string) Range {
		i := strings.Index(src, needle)
		return lines.rangeOf(i, i+len(needle), encUTF16)
	}
	goFile := filepath.Join(ws.root, "internal", "posts", "post.go")
	os.MkdirAll(filepath.Dir(goFile), 0o755)
	os.WriteFile(goFile, []byte("package posts\n\ntype Post struct {\n\tTitle string\n}\n"), 0o644)
	ws.tsTypes = []compiler.TSFile{
		{Path: "lyr_gen/types/go.d.ts", Sources: map[int]compiler.TSSource{
			6: {File: p, Lyr: true, Start: strings.Index(src, "Likes"), End: strings.Index(src, "Likes") + 5},
			9: {File: goFile, Line: 4, Col: 1, Name: "Title"},
		}},
		{Path: "lyr_gen/types/src/components/Like.lyr.d.ts", Sources: map[int]compiler.TSSource{
			8: {File: p, Lyr: true, Start: strings.Index(src, "data-like"), End: strings.Index(src, "data-like") + 9},
		}},
	}
	loc := func(rel string, line int) string {
		return `{"uri":"` + pathToURI(filepath.Join(ws.root, rel)) + `","range":{"start":{"line":` + itoa(line) + `,"character":6},"end":{"line":` + itoa(line) + `,"character":11}}}`
	}
	t.Run("definition locations", func(t *testing.T) {
		vr := rangeAt("data(")
		vloc, _ := json.Marshal(map[string]any{"uri": d.uri, "range": vr})
		raw := jsonRaw("[" + string(vloc) + "," + loc("lyr_gen/types/go.d.ts", 6) + "," + loc("lyr_gen/types/go.d.ts", 9) + "," +
			loc("src/components/Like.lyr.d.ts", 8) + "," + loc("src/scripts/util.ts", 3) + "," + loc("lyr_gen/types/client.d.ts", 2) + "]")
		var got []location
		json.Unmarshal(m.locations(raw, false), &got)
		if len(got) != 6 {
			t.Fatalf("%d locations: %v", len(got), got)
		}
		lyrURI := ws.lyrURI(p)
		if got[0].URI != lyrURI || got[0].Range != vr {
			t.Errorf("virtual document location → %v", got[0])
		}
		if got[1].URI != lyrURI || got[1].Range != rangeAt("Likes") {
			t.Errorf("go.d.ts member → %v, want the Likes field", got[1])
		}
		if got[2].URI != pathToURI(goFile) || got[2].Range.Start != (Position{Line: 3, Character: 1}) || got[2].Range.End.Character != 6 {
			t.Errorf("go.d.ts member of a .go file → %v", got[2])
		}
		if got[3].URI != lyrURI || got[3].Range != rangeAt("data-like") {
			t.Errorf("unit key → %v, want the data-like attribute", got[3])
		}
		if !strings.HasSuffix(got[4].URI, "src/scripts/util.ts") || !strings.HasSuffix(got[5].URI, "lyr_gen/types/client.d.ts") {
			t.Errorf("other files changed: %v %v", got[4], got[5])
		}
		// References drop the generated ones.
		json.Unmarshal(m.locations(raw, true), &got)
		if len(got) != 2 {
			t.Errorf("references: %v, want the script and util.ts", got)
		}
	})
	t.Run("edits stay in the script, imports move into it", func(t *testing.T) {
		in := rangeAt("const n")
		out := rangeAt("type like")
		top := Range{} // TypeScript's insertion at the top of the file
		edits := []obj{
			{"range": mustJSON(in), "newText": jsonRaw(`"let n"`)},
			{"range": mustJSON(out), "newText": jsonRaw(`"x"`)},
			{"range": mustJSON(top), "newText": jsonRaw(`"import { plural } from \"../scripts/util\";\n"`)},
		}
		got := m.vdocEdits(d, edits)
		if len(got) != 2 {
			t.Fatalf("edits %v", got)
		}
		r, _ := got[1].rng("range")
		want := lines.position(strings.Index(src, "import { data }"), encUTF16)
		if r.Start != want || r.End != want {
			t.Errorf("import moved to %v, want %v", r, want)
		}
		if got[1].str("newText") != "import { plural } from \"../scripts/util\";\n  " {
			t.Errorf("import text %q", got[1].str("newText"))
		}
	})
	t.Run("rename touching generated types is refused", func(t *testing.T) {
		we := `{"changes":{"` + d.uri + `":[{"range":` + string(mustJSON(rangeAt(".likes"))) + `,"newText":"count"}],"` +
			pathToURI(filepath.Join(ws.root, "lyr_gen/types/go.d.ts")) + `":[{"range":{"start":{"line":6,"character":6},"end":{"line":6,"character":11}},"newText":"count"}]}}`
		if _, refused := m.workspaceEdit(jsonRaw(we), true); !refused {
			t.Error("not refused")
		}
		out, refused := m.workspaceEdit(jsonRaw(we), false)
		if refused || !strings.Contains(string(out), ws.lyrURI(p)) || strings.Contains(string(out), "lyr_gen") || strings.Contains(string(out), ".lyr/lsp") {
			t.Errorf("mapped edit %s", out)
		}
		s.caps.documentChanges = false
		out, _ = m.workspaceEdit(jsonRaw(we), false)
		if !strings.HasPrefix(string(out), `{"changes":{"`+ws.lyrURI(p)) {
			t.Errorf("changes form: %s", out)
		}
		s.caps.documentChanges = true
	})
	t.Run("completion data is wrapped and resolves", func(t *testing.T) {
		raw := jsonRaw(`{"isIncomplete":false,"items":[{"label":"like","textEdit":{"range":` + string(mustJSON(rangeAt("like\""))) + `,"newText":"like"},"data":{"name":"like"}},{"label":"x","textEdit":{"range":` + string(mustJSON(rangeAt("type like"))) + `,"newText":"x"}}]}`)
		var list struct {
			Items []obj `json:"items"`
		}
		json.Unmarshal(m.completion(raw, d), &list)
		if len(list.Items) != 2 {
			t.Fatalf("items %v", list.Items)
		}
		w := decodeObj(list.Items[0]["data"])
		if w.str("lyr") != "ts" || w.str("u") != d.uri || string(w["d"]) != `{"name":"like"}` {
			t.Errorf("data %s", list.Items[0]["data"])
		}
		if _, ok := list.Items[1]["textEdit"]; ok {
			t.Errorf("an edit outside the script was kept: %v", list.Items[1])
		}
		// Items without the marker (gopls's, Lyr's) resolve to themselves.
		item := jsonRaw(`{"label":"x","data":{"other":1}}`)
		res, _ := s.tsResolve(context.Background(), item)
		if string(res.(jsonRaw)) != string(item) {
			t.Errorf("resolve of a non-TypeScript item: %s", res)
		}
	})
	t.Run("off-region triggers answer empty", func(t *testing.T) {
		pos := lines.position(strings.Index(src, "const n"), encUTF16)
		params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": ws.lyrURI(p)}, "position": pos,
			"context": map[string]any{"triggerKind": 2, "triggerCharacter": ":"}})
		res, err, ok := s.tsRoute(context.Background(), "textDocument/completion", params)
		if !ok || err != nil {
			t.Fatalf("not routed: %v %v", ok, err)
		}
		if b, _ := json.Marshal(res); string(b) != `{"isIncomplete":false,"items":[]}` {
			t.Errorf("completion on ':' in a script: %s", b)
		}
		if s.ts.status != tsIdle {
			t.Error("the child was started for an off-region trigger")
		}
		// Outside a script: not routed.
		params, _ = json.Marshal(map[string]any{"textDocument": map[string]any{"uri": ws.lyrURI(p)},
			"position": lines.position(strings.Index(src, "Likes"), encUTF16)})
		if _, _, ok := s.tsRoute(context.Background(), "textDocument/hover", params); ok {
			t.Error("a frontmatter position was routed to TypeScript")
		}
	})
	t.Run("diagnostics outside the script are dropped", func(t *testing.T) {
		got := ws.tsFilterDiags(d, []obj{
			{"range": mustJSON(rangeAt(".likes")), "message": jsonRaw(`"in"`)},
			{"range": mustJSON(rangeAt("Likes")), "message": jsonRaw(`"out"`)},
		})
		if len(got) != 1 || !strings.Contains(string(got[0]), `"source":"ts"`) {
			t.Errorf("diagnostics %s", got)
		}
	})
}

func mustJSON(v any) jsonRaw {
	b, _ := json.Marshal(v)
	return b
}
