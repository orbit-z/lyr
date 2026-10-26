package lsp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

func TestMapCompletion(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.orbit": mapCard, "src/pages/index.orbit": mapPage})
	const card = "src/components/Card.orbit"
	_, impl := ws.genOf(card)
	m := mapper{ws: ws, enc: encUTF16}
	gen := string(impl.content)
	at := func(sub string, delta int) Position {
		i := strings.Index(gen, sub)
		if i < 0 {
			t.Fatalf("%q not in the generated file", sub)
		}
		return impl.lines.position(i+delta, encUTF16)
	}
	cursor := at("props./", len("props."))
	empty := Range{Start: cursor, End: cursor}
	glue := Range{Start: at("orbit__w.Enter", 0), End: at("orbit__w.Enter", 5)}
	rtLine := `orbit__rt "github.com/orbit-z/orbit"`
	afterRT := at(rtLine, len(rtLine))
	importEdit := map[string]any{"range": Range{Start: afterRT, End: afterRT}, "newText": "\n\t\"strings\""}
	genImport := map[string]any{"range": Range{Start: afterRT, End: afterRT}, "newText": "\n\t\"example.com/site/orbit_gen/decl/components/card\""}
	res := map[string]any{
		"isIncomplete": true,
		"itemDefaults": map[string]any{"editRange": map[string]any{"insert": empty, "replace": empty}},
		"items": []any{
			map[string]any{"label": "Title", "kind": 5, "labelDetails": map[string]any{"detail": " string"},
				"textEdit": map[string]any{"insert": empty, "replace": empty, "newText": "Title"}},
			map[string]any{"label": "Glue", "textEdit": map[string]any{"range": glue, "newText": "x"}},
			map[string]any{"label": "orbit__w", "textEdit": map[string]any{"range": empty, "newText": "orbit__w"}},
			map[string]any{"label": "strings.ToUpper", "textEdit": map[string]any{"range": empty, "newText": "strings.ToUpper"},
				"additionalTextEdits": []any{importEdit}},
			map[string]any{"label": "card.Props", "textEdit": map[string]any{"range": empty, "newText": "card.Props"},
				"additionalTextEdits": []any{genImport}},
			map[string]any{"label": "Dropped", "textEdit": map[string]any{"range": empty, "newText": "x"},
				"additionalTextEdits": []any{map[string]any{"range": glue, "newText": "y"}}},
		},
	}
	raw, _ := json.Marshal(res)
	out := m.mapCompletion(impl, raw)
	var list struct {
		IsIncomplete bool `json:"isIncomplete"`
		ItemDefaults struct {
			EditRange textEdit `json:"editRange"`
		} `json:"itemDefaults"`
		Items []struct {
			completionItem
			LabelDetails map[string]any `json:"labelDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatal(err)
	}
	want := posAt(mapCard, strings.Index(mapCard, "props.}")+len("props."))
	wantR := Range{Start: want, End: want}
	if !list.IsIncomplete || list.ItemDefaults.EditRange.Insert == nil || *list.ItemDefaults.EditRange.Insert != wantR {
		t.Errorf("list fields: %s", out)
	}
	var got []string
	for _, it := range list.Items {
		got = append(got, it.Label)
	}
	if !slices.Equal(got, []string{"Title", "strings.ToUpper", "Dropped"}) {
		t.Fatalf("items %v, want Title, strings.ToUpper, Dropped (glue edit, glue name and generated import dropped)", got)
	}
	title := list.Items[0]
	if *title.TextEdit.Insert != wantR || *title.TextEdit.Replace != wantR || title.LabelDetails["detail"] != " string" {
		t.Errorf("InsertReplaceEdit item: %+v", title)
	}
	up := list.Items[1]
	if len(up.AdditionalTextEdits) != 1 {
		t.Fatalf("auto-import edits: %+v", up.AdditionalTextEdits)
	}
	if got := applyEdits(mapCard, up.AdditionalTextEdits); !strings.HasPrefix(got, "---\nimport \"strings\"\ntype Props struct") {
		t.Errorf("auto-import gives:\n%s", got)
	}
	if d := list.Items[2]; len(d.AdditionalTextEdits) != 0 {
		t.Errorf("an unmapped additional edit was kept: %+v", d.AdditionalTextEdits)
	}
}

func TestImportInsertion(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"no frontmatter", "<p>x</p>\n", "---\nimport \"strings\"\n---\n<p>x</p>\n"},
		{"no import", "---\nx := 1\n---\n", "---\nimport \"strings\"\nx := 1\n---\n"},
		{"single", "---\nimport \"fmt\"\n\nx := 1\n---\n", "---\nimport \"fmt\"\nimport \"strings\"\n\nx := 1\n---\n"},
		{"group", "---\nimport (\n\t\"fmt\"\n\tCard \"./Card.orbit\"\n)\nx := 1\n---\n",
			"---\nimport (\n\t\"fmt\"\n\tCard \"./Card.orbit\"\n\t\"strings\"\n)\nx := 1\n---\n"},
		{"group on one line", "---\nimport (\"fmt\")\n---\n", "---\nimport (\"fmt\"\n\t\"strings\"\n)\n---\n"},
		{"group then single", "---\nimport (\n\t\"fmt\"\n)\nimport Card \"./Card.orbit\"\n---\n",
			"---\nimport (\n\t\"fmt\"\n)\nimport Card \"./Card.orbit\"\nimport \"strings\"\n---\n"},
		{"CRLF", "---\r\nx := 1\r\n---\r\n", "---\r\nimport \"strings\"\nx := 1\r\n---\r\n"},
	} {
		f := compiler.Parse(tc.src, compiler.Tolerant)
		off, text := importInsertion(tc.src, f.Frontmatter, []string{`"strings"`})
		if got := tc.src[:off] + text + tc.src[off:]; got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestAttrNames(t *testing.T) {
	for field, want := range map[string]string{"IsOpen": "isOpen", "ID": "id", "Title": "title", "UserID": "userID", "HTTPServer": "httpServer"} {
		if got := compiler.LowerCamel(field); got != want {
			t.Errorf("LowerCamel(%s) = %s, want %s", field, got, want)
		}
	}
	const card = `---
type Props struct {
	Title  string
	// IsOpen opens it.
	IsOpen bool
	ID     int
}
---
<p>{props.Title}</p>
`
	const page = `---
import Card "../components/Card.orbit"
---
<Card title="x"  />
`
	ws := memWorkspace(t, map[string]string{"src/components/Card.orbit": card, "src/pages/index.orbit": page})
	f, lines := ws.file("src/pages/index.orbit")
	off := strings.Index(page, `"x" `) + len(`"x" `)
	c := cursor{ws: ws, f: f, lines: lines, path: f.Path, off: off, reg: classify(f, off)}
	if c.reg.kind != regionAttr || c.reg.comp == nil {
		t.Fatalf("classify: %+v", c.reg)
	}
	info := ws.target(f, c.reg.comp)
	names := func(items []any) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.(map[string]any)["label"].(string))
		}
		return out
	}
	// The native fallback: title is set; isOpen and id are offered.
	items := ws.componentItems(c, info, nil, false, true)
	if got := names(items); !slices.Equal(got[:2], []string{"isOpen", "id"}) || slices.Contains(got, "title") {
		t.Errorf("native props: %v", got)
	}
	isOpen := items[0].(map[string]any)
	if te := isOpen["textEdit"].(map[string]any); te["newText"] != "isOpen={$1}" || isOpen["insertTextFormat"] != formatSnippet {
		t.Errorf("snippet insert: %v", isOpen)
	}
	if doc := isOpen["documentation"].(map[string]any)["value"]; doc != "IsOpen opens it." {
		t.Errorf("doc: %v", doc)
	}
	if te := ws.componentItems(c, info, nil, false, false)[0].(map[string]any)["textEdit"].(map[string]any); te["newText"] != "isOpen=" {
		t.Errorf("plain insert: %v", te)
	}
	// From gopls: fields keep gopls's order, sortText and details; the
	// doc is scrubbed of directive text and falls back to the index's.
	fields := []obj{
		decodeObj(jsonRaw(`{"label":"ID","kind":5,"detail":"int","sortText":"00000","documentation":{"kind":"markdown","value":"line ../../x.orbit:3:1\n"}}`)),
		decodeObj(jsonRaw(`{"label":"Title","kind":5,"detail":"string","sortText":"00001"}`)),
		decodeObj(jsonRaw(`{"label":"IsOpen","kind":5,"detail":"bool","sortText":"00002","documentation":"Is it open?"}`)),
	}
	items = ws.componentItems(c, info, fields, true, true)
	if got := names(items); !slices.Equal(got[:2], []string{"id", "isOpen"}) {
		t.Errorf("gopls props: %v", got)
	}
	id, open := items[0].(map[string]any), items[1].(map[string]any)
	if id["sortText"] != "000000" || id["detail"] != "int" || id["documentation"] != nil {
		t.Errorf("id item: %v", id)
	}
	if open["documentation"].(map[string]any)["value"] != "Is it open?" {
		t.Errorf("isOpen doc: %v", open["documentation"])
	}
	// Directives: on a component, not on an element.
	for _, d := range []string{"slot", "server:defer", "client:load", "client:idle", "client:visible", "client:media"} {
		if !slices.Contains(names(items), d) {
			t.Errorf("component directives: %v, missing %s", names(items), d)
		}
	}
}

func TestClassifyCompletion(t *testing.T) {
	const src = `---
import Card "../components/Card.orbit"
---
<div><
<Card server:defer><p slot="x"></p><Ca
</Card>
<Card><div><p slot=""></p></div></Card>
<script type="application/json" ></script>
<div>{}</div>
`
	ws := memWorkspace(t, map[string]string{"src/components/Card.orbit": mapCard, "src/pages/index.orbit": src})
	f := ws.byPath["src/pages/index.orbit"]
	for _, tc := range []struct {
		at        string
		delta     int
		want      regionKind
		hasParent bool
	}{
		{"<div><\n", len("<div><"), regionTagName, false},
		{"<Ca\n", len("<Ca"), regionTag, false},
		{"<Card server", len("<Card "), regionAttr, false},
		{"server:defer>", 3, regionAttr, false},
		{`slot="x"`, len(`slot="`), regionSlot, true},
		{`slot=""`, len(`slot="`), regionSlot, false}, // inside a <div>: no parent component
		{`json" >`, len(`json" `), regionAttr, false},
		{`json" >`, len(`json" >`), regionNone, false}, // after the ">"
		{"{}", 1, regionGo, false},
		{`type="`, len(`type="`), regionNone, false}, // a plain attribute value
	} {
		off := strings.Index(src, tc.at) + tc.delta
		reg := classify(f, off)
		if reg.kind != tc.want || (reg.parent != nil) != tc.hasParent {
			t.Errorf("classify %q+%d = kind %d parent %v, want %d parent %v", tc.at, tc.delta, reg.kind, reg.parent != nil, tc.want, tc.hasParent)
		}
	}
}
