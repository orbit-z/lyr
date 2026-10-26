package lsp

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// The completion part of the LSP integration test (PLAN.md §13).

const boxOrbit = `---
type Props struct {
	Title string
}
---
<section><header><slot name="header" /></header><slot /></section>
`

const completeOrbit = `---
import Card "../components/Card.orbit"
import Box "../components/Box.orbit"

suffix := "!"
greeting := "Héllo ✓ 😀" + suffix
count := len(greeting)
---
<Card post={greeting} />
<div class="x"></div>
<Card server:defer><p slot=""></p></Card>
<Box title="t"><p slot=""></p></Box>
<p>{count}</p>
`

const localsGo = `package locals

// Locals are the request's locals.
type Locals struct {
	// User is the signed-in user.
	User  string
	Admin bool
}
`

// completionItem is the part of a CompletionItem the tests look at.
type completionItem struct {
	Label               string     `json:"label"`
	Kind                int        `json:"kind"`
	Detail              string     `json:"detail"`
	SortText            string     `json:"sortText"`
	TextEdit            *textEdit  `json:"textEdit"`
	AdditionalTextEdits []textEdit `json:"additionalTextEdits"`
	InsertTextFormat    int        `json:"insertTextFormat"`
	Documentation       any        `json:"documentation"`
}

type textEdit struct {
	Range   *Range `json:"range"`
	Insert  *Range `json:"insert"`
	Replace *Range `json:"replace"`
	NewText string `json:"newText"`
}

// editRange is the range a TextEdit or InsertReplaceEdit replaces.
func (e *textEdit) editRange() Range {
	if e.Range != nil {
		return *e.Range
	}
	if e.Replace != nil {
		return *e.Replace
	}
	return Range{}
}

func (c *testClient) complete(rel string, pos Position) []completionItem {
	c.t.Helper()
	res := c.call("textDocument/completion", c.textPos(rel, pos))
	if isNull(res) {
		return nil
	}
	var list struct {
		Items []completionItem `json:"items"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(res)), "[") {
		json.Unmarshal(res, &list.Items)
	} else if err := json.Unmarshal(res, &list); err != nil {
		c.t.Fatalf("completion result %s: %v", res, err)
	}
	return list.Items
}

func labels(items []completionItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func item(items []completionItem, label string) *completionItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

// applyEdits applies TextEdits to text.
func applyEdits(text string, edits []textEdit) string {
	x := newLineIndex(text)
	type e struct {
		start, end int
		text       string
	}
	var es []e
	for _, ed := range edits {
		r := ed.editRange()
		es = append(es, e{x.offset(r.Start, encUTF16), x.offset(r.End, encUTF16), ed.NewText})
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].start > es[j].start })
	for _, ed := range es {
		text = text[:ed.start] + ed.text + text[ed.end:]
	}
	return text
}

// accept applies a completion item (its edit and additional edits).
func accept(text string, it *completionItem) string {
	edits := slices.Clone(it.AdditionalTextEdits)
	if it.TextEdit != nil {
		edits = append(edits, *it.TextEdit)
	}
	return applyEdits(text, edits)
}

func TestCompletion(t *testing.T) {
	needGopls(t)
	dir := writeProject(t, map[string]string{
		"src/components/Card.orbit":   cardOrbit,
		"src/components/Box.orbit":    boxOrbit,
		"src/pages/complete.orbit":    completeOrbit,
		"src/pages/index.orbit":       indexOrbit,
		"src/pages/blog/[slug].orbit": slugOrbit,
		"src/locals/locals.go":        localsGo,
		"src/pages/blog/post.md":      "---\ntitle: Hi\ntags: [a, b]\n---\n# Hi\n",
		"src/pages/md.orbit":          "---\nimport Post \"./blog/post.md\"\n---\n<p>{Post.Frontmatter.Title}</p>\n<Post />\n",
	})
	c := startClient(t, dir, nil)
	const page = "src/pages/complete.orbit"
	text := c.open(page)

	t.Run("props.", func(t *testing.T) {
		card := c.open("src/components/Card.orbit")
		src := strings.Replace(card, "{props.Post}", "{props.}", 1)
		c.change("src/components/Card.orbit", src)
		pos := at(t, src, "{props.}", 0, len("{props."))
		items := c.complete("src/components/Card.orbit", pos)
		for _, want := range []string{"Post", "Featured"} {
			it := item(items, want)
			if it == nil {
				t.Fatalf("completion after {props.: %v, want %s", labels(items), want)
			}
			if it.TextEdit == nil || it.TextEdit.editRange() != (Range{Start: pos, End: pos}) {
				t.Errorf("%s: edit %+v, want an insertion at %v", want, it.TextEdit, pos)
			}
		}
		c.change("src/components/Card.orbit", card)
		for _, it := range items {
			if isGlueName(it.Label) {
				t.Errorf("generated name offered: %s", it.Label)
			}
		}
	})

	// {switch} headers and case lists are Go regions completed by gopls.
	t.Run("switch header and case list", func(t *testing.T) {
		card := c.open("src/components/Card.orbit")
		for _, tc := range []struct{ tmpl, marker string }{
			{"{switch props.}{case \"a\"}a{/switch}", "{switch props."},
			{"{switch props.Post}{case props.}a{/switch}", "{case props."},
			{"{switch}{case props.}a{default}b{/switch}", "{case props."},
		} {
			src := strings.Replace(card, "{props.Post}", tc.tmpl, 1)
			c.change("src/components/Card.orbit", src)
			items := c.complete("src/components/Card.orbit", at(t, src, tc.marker, 0, len(tc.marker)))
			for _, want := range []string{"Post", "Featured"} {
				if item(items, want) == nil {
					t.Errorf("%s: completion %v, want %s", tc.tmpl, labels(items), want)
				}
			}
		}
		c.change("src/components/Card.orbit", card)
	})

	// class:list items are Go regions: an item, a condition, and a list
	// that doesn't parse while typing.
	t.Run("class:list items", func(t *testing.T) {
		card := c.open("src/components/Card.orbit")
		for _, tc := range []struct{ tmpl, marker string }{
			{`<p class:list={"a", props.}>x</p>`, "props."},
			{`<p class:list={"a": props.}>x</p>`, "props."},
			{`<p class:list={"a", props.Post, "b": props.}>x</p>`, `"b": props.`},
		} {
			src := strings.Replace(card, "{props.Post}", tc.tmpl, 1)
			c.change("src/components/Card.orbit", src)
			items := c.complete("src/components/Card.orbit", at(t, src, tc.marker, 0, len(tc.marker)))
			for _, want := range []string{"Post", "Featured"} {
				if item(items, want) == nil {
					t.Errorf("%s: completion %v, want %s", tc.tmpl, labels(items), want)
				}
			}
		}
		c.change("src/components/Card.orbit", card)
	})

	t.Run("component attributes", func(t *testing.T) {
		src := strings.Replace(text, "<Card post={greeting} />", "<Card \n", 1)
		c.change(page, src)
		items := c.complete(page, at(t, src, "<Card \n", 0, len("<Card ")))
		got := labels(items)
		for _, want := range []string{"post", "featured", "server:defer", "server:concurrent", "client:load", "client:idle", "client:visible", "client:media", "slot"} {
			if !slices.Contains(got, want) {
				t.Errorf("<Card : %v, missing %s", got, want)
			}
		}
		if it := item(items, "post"); it == nil || it.Detail != "string" || !strings.Contains(docText(it.Documentation), "post's title") {
			t.Errorf("post item: %+v", it)
		}
		// A prop already set isn't offered again.
		src = strings.Replace(text, "<Card post={greeting} />", "<Card post={greeting}  />", 1)
		c.change(page, src)
		items = c.complete(page, at(t, src, "<Card post={greeting}  />", 0, len("<Card post={greeting} ")))
		if item(items, "post") != nil || item(items, "featured") == nil {
			t.Errorf("<Card post={…} : %v, want featured and not post", labels(items))
		}
	})

	t.Run("HTML attributes", func(t *testing.T) {
		src := strings.Replace(text, `<div class="x">`, `<div class="x" >`, 1)
		c.change(page, src)
		items := c.complete(page, at(t, src, `<div class="x" >`, 0, len(`<div class="x" `)))
		got := labels(items)
		for _, want := range []string{"id", "hidden", "set:html", "set:text", "slot", "class:list"} {
			if !slices.Contains(got, want) {
				t.Errorf("<div : %v, missing %s", got, want)
			}
		}
		for _, bad := range []string{"class", "server:defer", "server:concurrent", "client:load", "client:idle", "client:visible", "client:media"} {
			if slices.Contains(got, bad) {
				t.Errorf("<div : offers %s", bad)
			}
		}
	})

	t.Run("slot names", func(t *testing.T) {
		c.change(page, text)
		items := c.complete(page, at(t, text, `slot=""`, 0, len(`slot="`)))
		if got := labels(items); !slices.Equal(got, []string{"fallback"}) {
			t.Errorf(`slot=" in a server:defer component: %v, want only fallback`, got)
		}
		items = c.complete(page, at(t, text, `slot=""`, 1, len(`slot="`)))
		if got := labels(items); !slices.Equal(got, []string{"header", "default"}) {
			t.Errorf(`slot=" in Box: %v, want header, default`, got)
		}
	})

	t.Run("params.", func(t *testing.T) {
		slug := c.open("src/pages/blog/[slug].orbit")
		src := strings.Replace(slug, "params.Slug", "params.", 1)
		c.change("src/pages/blog/[slug].orbit", src)
		items := c.complete("src/pages/blog/[slug].orbit", at(t, src, "params.", 0, len("params.")))
		if item(items, "Slug") == nil {
			t.Errorf("params.: %v, want Slug", labels(items))
		}
	})

	// A Markdown import (PLAN.md §2.4, §9): gopls completes its
	// frontmatter through the generated decl package.
	t.Run("Post.Frontmatter.", func(t *testing.T) {
		const md = "src/pages/md.orbit"
		src := c.open(md)
		edited := strings.Replace(src, "Post.Frontmatter.Title", "Post.Frontmatter.", 1)
		c.change(md, edited)
		items := c.complete(md, at(t, edited, "Post.Frontmatter.", 0, len("Post.Frontmatter.")))
		for _, want := range []string{"Title", "Tags"} {
			if item(items, want) == nil {
				t.Errorf("Post.Frontmatter.: %v, want %s", labels(items), want)
			}
		}
		edited = strings.Replace(src, "Post.Frontmatter.Title", "Post.", 1)
		c.change(md, edited)
		items = c.complete(md, at(t, edited, "Post.}", 0, len("Post.")))
		for _, want := range []string{"Frontmatter", "Headings", "URL"} {
			if item(items, want) == nil {
				t.Errorf("Post.: %v, want %s", labels(items), want)
			}
		}
		c.change(md, src)
	})

	t.Run("locals.", func(t *testing.T) {
		src := strings.Replace(text, "count := len(greeting)", "count := len(greeting)\nu := locals.", 1)
		c.change(page, src)
		items := c.complete(page, at(t, src, "locals.", 0, len("locals.")))
		if item(items, "User") == nil || item(items, "Admin") == nil {
			t.Errorf("locals.: %v, want User and Admin", labels(items))
		}
	})

	t.Run("tag names", func(t *testing.T) {
		index := c.open("src/pages/index.orbit")
		src := strings.Replace(index, "<p>{count}</p>", "<\n<p>{count}</p>", 1)
		c.change("src/pages/index.orbit", src)
		items := c.complete("src/pages/index.orbit", at(t, src, "<\n<p>", 0, 1))
		// The editor's order: imported components, other components, HTML.
		sorted := labels(slices.SortedStableFunc(slices.Values(items), func(a, b completionItem) int {
			return strings.Compare(a.SortText, b.SortText)
		}))
		iCard, iBox, iDiv := slices.Index(sorted, "Card"), slices.Index(sorted, "Box"), slices.Index(sorted, "div")
		if iCard < 0 || iBox < 0 || iDiv < 0 || !(iCard < iBox && iBox < iDiv) {
			t.Fatalf("after <: %v, want Card, then Box, then div", sorted)
		}
		box := item(items, "Box")
		if len(box.AdditionalTextEdits) != 1 {
			t.Fatalf("Box: additional edits %+v, want the import", box.AdditionalTextEdits)
		}
		after := accept(src, box)
		if !strings.Contains(after, "import Card \"../components/Card.orbit\"\nimport Box \"../components/Box.orbit\"\n") ||
			!strings.Contains(after, "<Box\n<p>") {
			t.Errorf("accepting Box gives:\n%s", after)
		}
		if item(items, "Card").AdditionalTextEdits != nil {
			t.Errorf("the imported Card adds an import")
		}
	})

	t.Run("auto-import", func(t *testing.T) {
		index := c.texts["src/pages/index.orbit"]
		src := strings.Replace(index, "count := len(greeting)", "count := len(greeting)\nup := strings.ToU", 1)
		src = strings.Replace(src, "<p>{count}</p>", "<p>{count}{up}</p>", 1)
		c.change("src/pages/index.orbit", src)
		items := c.complete("src/pages/index.orbit", at(t, src, "strings.ToU", 0, len("strings.ToU")))
		it := item(items, "ToUpper")
		if it == nil {
			t.Fatalf("strings.ToU: %v, want ToUpper", labels(items))
		}
		after := accept(src, it)
		want := "---\nimport Card \"../components/Card.orbit\"\nimport \"strings\"\n"
		if !strings.HasPrefix(after, want) || !strings.Contains(after, "up := strings.ToUpper") {
			t.Errorf("accepting ToUpper gives:\n%s\nwant it to start with:\n%s", after, want)
		}
	})

	t.Run("after non-ASCII text", func(t *testing.T) {
		index := c.texts["src/pages/index.orbit"]
		src := strings.Replace(index, `"Héllo ✓ 😀" + suffix`, `"Héllo ✓ 😀" + suf`, 1)
		c.change("src/pages/index.orbit", src)
		items := c.complete("src/pages/index.orbit", at(t, src, "+ suf", 0, len("+ suf")))
		it := item(items, "suffix")
		if it == nil || it.TextEdit == nil {
			t.Fatalf("after non-ASCII: %v, want suffix", labels(items))
		}
		if got := rangeText(src, it.TextEdit.editRange()); got != "suf" {
			t.Errorf("suffix edit covers %q, want suf (range %+v)", got, it.TextEdit.editRange())
		}
	})

	t.Run("signature help", func(t *testing.T) {
		c.change(page, text)
		res := c.call("textDocument/signatureHelp", c.textPos(page, at(t, text, "len(greeting)", 0, len("len("))))
		if !strings.Contains(string(res), "len(") {
			t.Errorf("signature help in len(: %s", res)
		}
	})

	t.Run("unsaved Props change", func(t *testing.T) {
		card := strings.Replace(cardOrbit, "Featured bool", "Featured bool\n\t// Tags label the post.\n\tTags     []string", 1)
		c.change("src/components/Card.orbit", card)
		src := strings.Replace(text, "<Card post={greeting} />", "<Card  />", 1)
		c.change(page, src)
		items := c.complete(page, at(t, src, "<Card  />", 0, len("<Card ")))
		it := item(items, "tags")
		if it == nil || it.Detail != "[]string" || !strings.Contains(docText(it.Documentation), "label the post") {
			t.Errorf("<Card  after adding Tags: %v (tags: %+v)", labels(items), it)
		}
	})

	t.Run("latency", func(t *testing.T) {
		c.change(page, text)
		measure := func(name string, edit func(i int) (string, Position), want string) {
			var ds, again []time.Duration
			for i := range 30 {
				src, pos := edit(i)
				t0 := time.Now()
				c.change(page, src)
				items := c.complete(page, pos)
				ds = append(ds, time.Since(t0))
				if item(items, want) == nil {
					t.Fatalf("%s: %v, want %s", name, labels(items), want)
				}
				t0 = time.Now()
				c.complete(page, pos)
				again = append(again, time.Since(t0))
			}
			stats := func(ds []time.Duration) string {
				ds = ds[5:] // warm
				slices.Sort(ds)
				r := func(d time.Duration) time.Duration { return d.Round(10 * time.Microsecond) }
				return fmt.Sprintf("median %v, p90 %v, max %v (%d runs)", r(ds[len(ds)/2]), r(ds[len(ds)*9/10]), r(ds[len(ds)-1]), len(ds))
			}
			t.Logf("%s, warm: didChange + completion %s; completion alone %s", name, stats(ds), stats(again))
		}
		measure("Go selector {locals.}", func(i int) (string, Position) {
			src := strings.Replace(text, "<p>{count}</p>", "<p>{count}</p>\n"+strings.Repeat(" ", i)+"<p>{locals.}</p>", 1)
			return src, at(t, src, "{locals.}", 0, len("{locals."))
		}, "User")
		measure("Go identifier {greet}", func(i int) (string, Position) {
			src := strings.Replace(text, "<p>{count}</p>", "<p>{count}</p>\n"+strings.Repeat(" ", i)+"<p>{greet}</p>", 1)
			return src, at(t, src, "{greet}", 0, len("{greet"))
		}, "greeting")
		measure("<Card ", func(i int) (string, Position) {
			src := strings.Replace(text, "<Card post={greeting} />", "<Card "+strings.Repeat(" ", i)+" />", 1)
			return src, at(t, src, "<Card ", 0, len("<Card ")+i)
		}, "featured")
	})
}

// docText returns the text of a documentation string or MarkupContent.
func docText(doc any) string {
	switch d := doc.(type) {
	case string:
		return d
	case map[string]any:
		s, _ := d["value"].(string)
		return s
	}
	return ""
}
