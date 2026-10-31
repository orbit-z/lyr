package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/markdown"
	"github.com/orbit-z/lyr/internal/project"
)

// blockCompletion returns the control block items at the "|" of src (which
// is removed), the text without it, and whether the cursor is a block site.
func blockCompletion(t *testing.T, src string, snippets bool) ([]completionItem, string, bool) {
	t.Helper()
	off := strings.Index(src, "|")
	if off < 0 {
		t.Fatalf("no cursor in %q", src)
	}
	text := src[:off] + src[off+1:]
	f := &project.File{Src: text, AST: compiler.Parse(text, compiler.Tolerant)}
	site, ok := blockSiteAt(f, off)
	if !ok {
		return nil, text, false
	}
	c := cursor{f: f, lines: newLineIndex(text), off: off}
	raw, _ := json.Marshal(c.blockItems(site, snippets))
	var items []completionItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	return items, text, true
}

func TestBlockCompletionLabels(t *testing.T) {
	const fm = "---\nxs := []int{1}\n---\n"
	top := []string{"if", "for", "switch"}
	for _, tc := range []struct {
		name, src string
		want      []string // nil: not a block site
	}{
		{"after {", fm + "<p>{|</p>\n", top},
		{"auto-closed", fm + "<p>{|}</p>\n", top},
		{"prefix", fm + "<p>{i|}</p>\n", top},
		{"top level", "{fo|}\n", top},
		{"retyping {if}", "<p>{if|}</p>\n", top},
		{"in a slot", "---\nimport Box \"./Box.lyr\"\n---\n<Box>{|}</Box>\n", top},
		{"in RCDATA", "<title>{|}</title>\n", top},
		{"in an if", "{if true}<b>x</b>{|}{/if}\n", []string{"if", "for", "switch", "else if", "else", "/if"}},
		{"in an else if", "{if true}a{else if false}{|}{/if}\n", []string{"if", "for", "switch", "else if", "else", "/if"}},
		{"if with an else after", "{if true}{el|}a{else}b{/if}\n", []string{"if", "for", "switch", "else if", "/if"}},
		{"in an else", "{if true}a{else}{|}{/if}\n", []string{"if", "for", "switch", "/if"}},
		{"retyping {else}", "{if true}a{else|}b{/if}\n", []string{"if", "for", "switch", "else if", "else", "/if"}},
		{"if, inside an element", "{if true}<p>{|}</p>{/if}\n", []string{"if", "for", "switch"}},
		{"closer, inside an element", "{if true}<p>{/|}</p>{/if}\n", []string{}},
		{"in a for", "{for range 3}{|}{/for}\n", []string{"if", "for", "switch", "/for"}},
		{"in a switch", "{switch 1}{case 1}{|}{/switch}\n", []string{"if", "for", "switch", "case", "default", "/switch"}},
		{"switch before cases", "{switch 1}{|}{/switch}\n", []string{"if", "for", "switch", "case", "default", "/switch"}},
		{"switch with a default", "{switch 1}{case 1}{|}{default}b{/switch}\n", []string{"if", "for", "switch", "case", "/switch"}},
		{"closer in a for", "{for range 3}{if true}a{/if}{/|}\n", []string{"/for"}},
		{"closer, auto-closed", "{for range 3}{/|}{/for}\n", []string{"/for"}},
		{"closer prefix", "{for range 3}{if true}{/i|}{/for}\n", []string{"/if"}},
		{"retyping a closer", "{for range 3}a{/for|}\n", []string{"/for"}},
		{"closer outside blocks", "<p>{/|}</p>\n", []string{}},
		{"unclosed for", "{for range 3}<p>a</p>{/|\n", []string{"/for"}},
		{"unclosed for, element open", "{for range 3}<p>{/|\n", []string{}},
		{"attribute value", "<p class={|}>x</p>\n", nil},
		{"attribute value prefix", "<p class={i|}>x</p>\n", nil},
		{"spread", "<p {|}>x</p>\n", nil},
		{"script", "<script>if (x) {|}</script>\n", nil},
		{"style", "<style>p {|}</style>\n", nil},
		{"frontmatter", "---\nfunc f() {|}\n---\n<p></p>\n", nil},
		{"html comment", "<!-- {|} -->\n", nil},
		{"after a dot", "<p>{props.|}</p>\n", nil},
		{"after a dot prefix", "<p>{props.T|}</p>\n", nil},
		{"after a space", "<p>{ i|}</p>\n", nil},
		{"brace in Go code", "<p>{f(func() {|})}</p>\n", nil},
		{"in an if header", "{if f(func() {|})}a{/if}\n", nil},
		{"text", "<p>i|</p>\n", nil},
	} {
		items, _, ok := blockCompletion(t, tc.src, true)
		if tc.want == nil {
			if ok {
				t.Errorf("%s: a block site, items %v", tc.name, labels(items))
			}
			continue
		}
		if !ok {
			t.Errorf("%s: not a block site", tc.name)
			continue
		}
		if got := labels(items); !slices.Equal(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("%s: items %v, want %v", tc.name, got, tc.want)
		}
		for i := 1; i < len(items); i++ {
			if items[i-1].SortText >= items[i].SortText {
				t.Errorf("%s: sortText %q before %q", tc.name, items[i-1].SortText, items[i].SortText)
			}
		}
	}
	f := &project.File{Src: "{", AST: compiler.Parse("{", compiler.Tolerant), MD: &markdown.Doc{}}
	if _, ok := blockSiteAt(f, 1); ok {
		t.Error("a Markdown document is a block site")
	}
}

func TestBlockCompletionEdits(t *testing.T) {
	for _, tc := range []struct {
		name, src, label string
		snippets         bool
		want             string
	}{
		{"auto-closed brace", "<p>{i|}</p>", "if", true, "<p>{if ${1:cond}}$0{/if}</p>"},
		{"no closing brace", "<p>{i|</p>", "if", true, "<p>{if ${1:cond}}$0{/if}</p>"},
		{"for", "{|}", "for", true, "{for ${1:_, x := range xs}}$0{/for}"},
		{"switch", "{sw|}", "switch", true, "{switch ${1:x}}{case ${2:v}}$0{/switch}"},
		{"else", "{if true}a{el|}{/if}", "else", true, "{if true}a{else}{/if}"},
		{"else if", "{if true}a{el|}{/if}", "else if", true, "{if true}a{else if ${1:cond}}{/if}"},
		{"case", "{switch 1}{|}{/switch}", "case", true, "{switch 1}{case ${1:v}}{/switch}"},
		{"closer", "{for range 3}a{/|}", "/for", true, "{for range 3}a{/for}"},
		{"plain if", "<p>{i|}</p>", "if", false, "<p>{if }</p>"},
		{"plain else", "{if true}a{|}{/if}", "else", false, "{if true}a{else}{/if}"},
		{"plain closer", "{if true}a{/|}", "/if", false, "{if true}a{/if}"},
		{"UTF-16", "<p>Héllo ✓ 😀 {i|}</p>", "if", true, "<p>Héllo ✓ 😀 {if ${1:cond}}$0{/if}</p>"},
		{"UTF-16 closer", "{for range 3}😀{/|}", "/for", true, "{for range 3}😀{/for}"},
	} {
		items, text, ok := blockCompletion(t, tc.src, tc.snippets)
		if !ok {
			t.Errorf("%s: not a block site", tc.name)
			continue
		}
		it := item(items, tc.label)
		if it == nil || it.TextEdit == nil {
			t.Errorf("%s: no %s item in %v", tc.name, tc.label, labels(items))
			continue
		}
		wantFormat := formatPlain
		if tc.snippets {
			wantFormat = formatSnippet
		}
		if it.InsertTextFormat != wantFormat || it.Kind != kindKeyword {
			t.Errorf("%s: format %d kind %d, want format %d kind %d", tc.name, it.InsertTextFormat, it.Kind, wantFormat, kindKeyword)
		}
		if got := accept(text, it); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestMergeBlockItems(t *testing.T) {
	kw := []any{map[string]any{"label": "if", "sortText": "010"}}
	gopls := jsonRaw(`{"isIncomplete":true,"items":[{"label":"items","sortText":"00000"},{"label":"i"}]}`)
	var list struct {
		IsIncomplete bool             `json:"isIncomplete"`
		Items        []completionItem `json:"items"`
	}
	b, _ := json.Marshal(mergeBlockItems(kw, gopls))
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if !list.IsIncomplete || !slices.Equal(labels(list.Items), []string{"if", "items", "i"}) {
		t.Fatalf("merged: %s", b)
	}
	slices.SortFunc(list.Items, func(a, b completionItem) int { return strings.Compare(a.SortText, b.SortText) })
	if list.Items[0].Label != "if" || list.Items[1].SortText != "100000" || list.Items[2].SortText != "1i" {
		t.Errorf("sortTexts: %s", b)
	}
}

// TestBlockCompletionServer runs control block completion through the
// server with a stub gopls (which answers null): the keywords alone in a
// Go region, none in an attribute value.
func TestBlockCompletionServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const page = "---\nxs := []int{1}\n---\n<ul class={}>{for _, x := range xs}<li>{x}</li>{}{/for}</ul>\n"
	dir := writeProject(t, map[string]string{"src/pages/index.lyr": page})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, dir, map[string]any{"goplsPath": stub}, stubEnv+"=record:"+filepath.Join(t.TempDir(), "rec"))
	const rel = "src/pages/index.lyr"
	text := c.open(rel)
	items := c.complete(rel, at(t, text, "{}{/for}", 0, 1))
	if got := labels(items); !slices.Equal(got, []string{"if", "for", "switch", "/for"}) {
		t.Errorf("{} in a for: %v", got)
	}
	if it := item(items, "/for"); it == nil || it.InsertTextFormat != formatPlain || accept(text, it) != strings.Replace(text, "{}{/for}", "{/for}{/for}", 1) {
		t.Errorf("/for without snippet support: %+v", it)
	}
	if items := c.complete(rel, at(t, text, "class={}", 0, len("class={"))); item(items, "if") != nil {
		t.Errorf("keywords in an attribute value: %v", labels(items))
	}
}
