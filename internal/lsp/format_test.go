package lsp

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/orbit-z/lyr/internal/check"
	"github.com/orbit-z/lyr/internal/format"
)

const fmtPage = "---\nimport \"strings\"\n\ntitle:=strings.ToUpper( \"é—ü\" )\n---\n<h1 title={ title+\"😀\" }>é😀 { title }</h1>\n<p>{len(title)+1}</p>\n<script>\nconst a=1\n</script>\n"

func applyTextEdits(text string, edits []TextEdit) string {
	x := newLineIndex(text)
	type span struct {
		s, e int
		t    string
	}
	var spans []span
	for _, e := range edits {
		spans = append(spans, span{x.offset(e.Range.Start, encUTF16), x.offset(e.Range.End, encUTF16), e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].s > spans[j].s })
	for _, s := range spans {
		text = text[:s.s] + s.t + text[s.e:]
	}
	return text
}

func TestFormatting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	dir := writeProject(t, map[string]string{
		"src/pages/index.lyr": fmtPage,
		"src/pages/bad.lyr":   "<div>{ x }\n",
		"src/pages/about.md":  "# About\n",
	})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, dir, map[string]any{"goplsPath": stub}, stubEnv+"=record:"+t.TempDir()+"/rec")
	var caps struct {
		Capabilities struct {
			DocumentFormattingProvider bool `json:"documentFormattingProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(c.initResult, &caps); err != nil || !caps.Capabilities.DocumentFormattingProvider {
		t.Fatalf("documentFormattingProvider not advertised: %s", c.initResult)
	}
	format1 := func(rel string) jsonRaw {
		return c.call("textDocument/formatting", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(rel)},
			"options":      map[string]any{"tabSize": 8, "insertSpaces": true},
		})
	}

	// The edits give the CLI's output, with UTF-16 ranges over non-ASCII
	// text; an unsaved buffer is what's formatted.
	text := c.open("src/pages/index.lyr")
	text += "<p>{ 1+2 }</p>\n"
	c.change("src/pages/index.lyr", text)
	var edits []TextEdit
	if err := json.Unmarshal(format1("src/pages/index.lyr"), &edits); err != nil {
		t.Fatal(err)
	}
	bun, _ := check.FindBun(context.Background(), "", "")
	want, res := format.Source(text, format.Options{Path: "src/pages/index.lyr", Bun: bun, BunDir: dir})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	if got := applyTextEdits(text, edits); got != want {
		t.Errorf("formatting edits give\n%s\nwant\n%s", got, want)
	}
	if len(edits) < 3 {
		t.Errorf("want one edit per changed region, got %d: %+v", len(edits), edits)
	}

	// A syntax error: no edits.
	c.open("src/pages/bad.lyr")
	if res := format1("src/pages/bad.lyr"); string(res) != "[]" {
		t.Errorf("syntax error: got %s, want []", res)
	}
	// Markdown: null.
	c.open("src/pages/about.md")
	if res := format1("src/pages/about.md"); string(res) != "null" {
		t.Errorf("Markdown: got %s, want null", res)
	}
}
