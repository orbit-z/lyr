package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// diags formats d's diagnostics as "L:C: message" lines.
func diags(d *Doc) string {
	var out []string
	for _, x := range d.Diagnostics {
		p := d.Lines.Position(x.Start)
		out = append(out, fmt.Sprintf("%d:%d: %s", p.Line, p.Col, x.Message))
	}
	return strings.Join(out, "\n")
}

func TestFrontmatter(t *testing.T) {
	src := `---
title: Hello, Orbit
date: 2026-10-26
at: 2026-10-26T09:30:00+02:00
draft: false
order: 3
ratio: 2.5
quoted: "3"
tags: [news, go]
none: []
author:
  name: Ada
  post-id: 7
authors:
  - name: Ada
  - name: Grace
layout: ../layouts/Post.orbit
---
Body.
`
	d := Parse("src/pages/blog/hello.md", src, Options{}, nil)
	if s := diags(d); s != "" {
		t.Fatalf("diagnostics:\n%s", s)
	}
	want := []string{
		`Title string = "Hello, Orbit"`,
		`Date time.Time = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)`,
		`At time.Time = time.Date(2026, 10, 26, 9, 30, 0, 0, time.FixedZone("", 7200))`,
		`Draft bool = false`,
		`Order int = 3`,
		`Ratio float64 = 2.5`,
		`Quoted string = "3"`,
		`Tags []string = []string{"news", "go"}`,
		`None []string = []string{}`,
		`Author struct{Name string; PostID int} = struct{Name string; PostID int}{Name: "Ada", PostID: 7}`,
		`Authors []struct{Name string} = []struct{Name string}{{Name: "Ada"}, {Name: "Grace"}}`,
	}
	var got []string
	for _, f := range d.Fields {
		got = append(got, f.Name+" "+goType(f.Value)+" = "+goLit(f.Value, false))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fields:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if d.Layout != "src/pages/layouts/Post.orbit" || d.LayoutRaw != "../layouts/Post.orbit" {
		t.Errorf("layout %q (%q)", d.Layout, d.LayoutRaw)
	}
	if p := d.Lines.Position(d.LayoutPos); p.Line != 17 || p.Col != 9 {
		t.Errorf("layout at %v, want 17:9", p)
	}
	if p := d.Lines.Position(d.Fields[0].KeyPos); p.Line != 2 || p.Col != 1 {
		t.Errorf("title key at %v, want 2:1", p)
	}
	if d.HTML != "<p>Body.</p>\n" {
		t.Errorf("HTML %q", d.HTML)
	}
}

func TestFrontmatterErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"bad YAML", "---\ntitle: a\n  b: c\n---\n", "3:1: invalid YAML in the frontmatter: mapping values are not allowed in this context"},
		{"no value", "---\ntitle: Hi\nsubtitle:\n---\n", `3:10: frontmatter key "subtitle" has no value; delete the line or give it a value`},
		{"null", "---\nsubtitle: null\n---\n", `2:11: frontmatter key "subtitle" has no value; delete the line or give it a value`},
		{"anchor", "---\na: &x 1\nb: *x\n---\n", "2:4: YAML anchors (&x) aren't supported in frontmatter\n3:4: YAML aliases (*x) aren't supported in frontmatter; repeat the value"},
		{"tag", "---\na: !!str 1\n---\n", "2:4: YAML tags (!!str) aren't supported in frontmatter"},
		{"key name", "---\n1st: x\n---\n", `2:1: frontmatter key "1st" can't become a Go field name; use letters, digits, - and _, starting with a letter`},
		{"same field", "---\npost-id: 1\npostId: 2\n---\n", `3:1: frontmatter keys "post-id" and "postId" both become PostID; rename one`},
		{"repeated", "---\na: 1\na: 2\n---\n", `3:1: frontmatter key "a" is repeated; keep one`},
		{"mixed list", "---\ntags: [a, 1]\n---\n", "2:7: frontmatter tags: a list's items must all have the same type (string and int)"},
		{"not a map", "---\n- a\n---\n", "2:1: the frontmatter must be YAML keys and values (key: value)"},
		{"unclosed", "---\ntitle: x\n", "1:1: the frontmatter has no closing --- line"},
		{"layout not orbit", "---\nlayout: ./Post.md\n---\n", "2:9: layout ./Post.md must be an .orbit file"},
		{"layout outside", "---\nlayout: ../../../X.orbit\n---\n", "2:9: layout ../../../X.orbit is outside src/"},
		{"layout absolute", "---\nlayout: /src/X.orbit\n---\n", "2:9: layout /src/X.orbit must be a path relative to this file, starting with ./ or ../"},
		{"nested", "---\nauthor:\n  name:\n---\n", `3:8: frontmatter key "author.name" has no value; delete the line or give it a value`},
		{"multibyte", "---\ntitré: x\n---\n", `2:1: frontmatter key "titré" can't become a Go field name; use letters, digits, - and _, starting with a letter`},
		{"column after multibyte", "---\na: [é, !!str x]\n---\n", "2:9: YAML tags (!!str) aren't supported in frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Parse("src/pages/x.md", tt.src, Options{}, nil)
			if got := diags(d); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestNoFrontmatter(t *testing.T) {
	for _, src := range []string{"", "# Hi\n", "---\n---\n# Hi\n", "---\n# only a comment\n---\n"} {
		d := Parse("src/pages/x.md", src, Options{}, nil)
		if len(d.Diagnostics) > 0 || len(d.Fields) > 0 {
			t.Errorf("%q: %s %v", src, diags(d), d.Fields)
		}
	}
	// A thematic break that isn't on the first line is Markdown.
	d := Parse("src/pages/x.md", "Hi\n\n---\n\nThere\n", Options{}, nil)
	if d.HTML != "<p>Hi</p>\n<hr>\n<p>There</p>\n" {
		t.Errorf("HTML %q", d.HTML)
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name string
		o    Options
		src  string
		want []string
	}{
		{"heading ids", Options{}, "# Why Markdown?\n## Why Markdown?\n## `go build` & you\n### Don't panic\n## Ünïcode Ok\n## <em>Raw</em> and *em*\n",
			[]string{`<h1 id="why-markdown">Why Markdown?</h1>`, `<h2 id="why-markdown-1">`, `<h2 id="go-build--you">`,
				`<h3 id="dont-panic">Don&rsquo;t panic</h3>`, `<h2 id="ünïcode-ok">`, `<h2 id="raw-and-em">`}},
		{"gfm", Options{}, "| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n\n~~old~~ www.example.com\n\nNote[^1].\n\n[^1]: A footnote.\n",
			[]string{"<table>", `<input checked="" disabled="" type="checkbox"> done`, "<del>old</del>",
				`<a href="http://www.example.com">www.example.com</a>`, `<sup id="fnref:1">`, "A footnote."}},
		{"no gfm", Options{NoGFM: true}, "~~old~~\n", []string{"<p>~~old~~</p>"}},
		{"smartypants", Options{}, "\"Hi\" -- it's... << x >> `don't`\n",
			[]string{"<p>&ldquo;Hi&rdquo; &mdash; it&rsquo;s&hellip; &lt;&lt; x &gt;&gt; <code>don't</code></p>"}},
		// As Astro (retext-smartypants, dashes: true): exactly two hyphens
		// make an em dash, other runs stay; code is untouched.
		{"dashes", Options{}, "a--b a -- b a---b a --- b a----b a-----b x - y\n\n--start and end--\n\n`a--b` `---`\n\n```\na--b\n```\n\n    c -- d\n",
			[]string{"<p>a&mdash;b a &mdash; b a---b a --- b a----b a-----b x - y</p>", "<p>&mdash;start and end&mdash;</p>",
				"<p><code>a--b</code> <code>---</code></p>", "<code>a--b\n</code>", "<code>c -- d\n</code>"}},
		{"no dashes without smartypants", Options{NoSmartypants: true}, "a--b a---b\n", []string{"<p>a--b a---b</p>"}},
		{"no smartypants", Options{NoSmartypants: true}, "\"Hi\" -- it's...\n", []string{"<p>&quot;Hi&quot; -- it's...</p>"}},
		{"raw html", Options{}, "<details>\n<summary>Why?</summary>\n\nBecause.\n</details>\n\n<script>alert(1)</script>\n",
			[]string{"<details>\n<summary>Why?</summary>\n<p>Because.</p>\n</details>", "<script>alert(1)</script>"}},
		{"links as written", Options{}, "[a](javascript:alert(1)) [b](/about) [c](https://x.dev/y.md) ![i](./cat.md)\n",
			[]string{`<a href="javascript:alert(1)">a</a>`, `<a href="/about">b</a>`, `<a href="https://x.dev/y.md">c</a>`, `<img src="./cat.md" alt="i">`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Parse("src/pages/x.md", tt.src, tt.o, nil)
			if len(d.Diagnostics) > 0 {
				t.Fatalf("diagnostics:\n%s", diags(d))
			}
			for _, w := range tt.want {
				if !strings.Contains(d.HTML, w) {
					t.Errorf("no %q in:\n%s", w, d.HTML)
				}
			}
		})
	}
	d := Parse("src/pages/x.md", "# A *b* `c`\n## Don't\n", Options{}, nil)
	if fmt.Sprint(d.Headings) != "[{1 a-b-c A b c} {2 dont Don’t}]" {
		t.Errorf("headings %v", d.Headings)
	}
}

func TestLinks(t *testing.T) {
	files := map[string]string{
		"src/pages/blog/routing-guide.md": "/blog/routing-guide",
		"src/pages/index.md":              "/",
		"src/pages/docs/install.md":       "/docs/install",
		"src/content/intro.md":            "",
	}
	resolve := func(p string) (string, bool) { r, ok := files[p]; return r, ok }
	src := "[routing](./routing-guide.md) [home](../index.md) [install](../docs/install.md#requirements)\n\n" +
		"[q](routing-guide.md?x=1#a-b) [missing](./routing.md) [intro](../../content/intro.md) [self](#top)\n"
	d := Parse("src/pages/blog/hello.md", src, Options{}, resolve)
	for _, w := range []string{`<a href="/blog/routing-guide">routing</a>`, `<a href="/">home</a>`,
		`<a href="/docs/install#requirements">install</a>`, `<a href="/blog/routing-guide?x=1#a-b">q</a>`,
		`<a href="./routing.md">missing</a>`, `<a href="#top">self</a>`} {
		if !strings.Contains(d.HTML, w) {
			t.Errorf("no %q in:\n%s", w, d.HTML)
		}
	}
	want := "3:31: link ./routing.md: no such file\n" +
		"3:55: link ../../content/intro.md: src/content/intro.md isn't a page, so it has no URL; link to a page, or import it into one"
	if got := diags(d); got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	var links []string
	for _, l := range d.Links {
		links = append(links, fmt.Sprintf("%s %s#%s", d.Lines.Position(l.Pos), l.Target, l.Fragment))
	}
	if got := strings.Join(links, ", "); got != "1:1 src/pages/blog/routing-guide.md#, 1:31 src/pages/index.md#, 1:51 src/pages/docs/install.md#requirements, 3:1 src/pages/blog/routing-guide.md#a-b" {
		t.Errorf("links: %s", got)
	}
}

func TestCanonicalURL(t *testing.T) {
	for _, tt := range []struct {
		route         string
		never, always string
	}{
		{"/", "/", "/"},
		{"/blog/hello", "/blog/hello", "/blog/hello/"},
		{"/blog", "/blog", "/blog/"},
		{"/rss.xml", "/rss.xml", "/rss.xml"},
		{"/docs/v1.2", "/docs/v1.2", "/docs/v1.2"},
		{"/.well-known", "/.well-known", "/.well-known/"},
	} {
		if got := CanonicalURL(tt.route, false); got != tt.never {
			t.Errorf("CanonicalURL(%q, false) = %q, want %q", tt.route, got, tt.never)
		}
		if got := CanonicalURL(tt.route, true); got != tt.always {
			t.Errorf("CanonicalURL(%q, true) = %q, want %q", tt.route, got, tt.always)
		}
	}
}

func TestHighlight(t *testing.T) {
	src := "```go title=\"main.go\"\nfunc main() { fmt.Println(\"<hi>\") }\n```\n\n```orbit\n<p>{x}</p>\n```\n\n```mermaid\ngraph TD; A-->B\n```\n\n```\nplain & simple\n```\n\n    indented <code>\n"
	d := Parse("src/pages/x.md", src, Options{}, nil)
	for _, w := range []string{
		`<pre class="chroma" tabindex="0" data-language="go"><code><span class="kd">func</span> <span class="nf">main</span><span class="p">()</span>`,
		`<span class="s">"&lt;hi&gt;"</span>`,
		`<pre class="chroma" tabindex="0" data-language="orbit"><code><span class="p">&lt;</span><span class="nt">p</span><span class="p">&gt;{</span><span class="nx">x</span>`,
		`<pre class="chroma" tabindex="0" data-language="mermaid"><code>graph TD; A--&gt;B` + "\n</code></pre>",
		`<pre class="chroma" tabindex="0" data-language="plaintext"><code>plain &amp; simple` + "\n</code></pre>",
		`<pre class="chroma" tabindex="0" data-language="plaintext"><code>indented &lt;code&gt;` + "\n</code></pre>",
	} {
		if !strings.Contains(d.HTML, w) {
			t.Errorf("no %q in:\n%s", w, d.HTML)
		}
	}
	if !d.Highlighted {
		t.Error("Highlighted not set")
	}
	if d := Parse("src/pages/x.md", "```mermaid\nx\n```\n", Options{}, nil); d.Highlighted {
		t.Error("Highlighted set for an unknown language")
	}
	d = Parse("src/pages/x.md", "```go\nfunc main() {}\n```\n", Options{NoHighlight: true}, nil)
	if d.HTML != "<pre><code class=\"language-go\">func main() {}\n</code></pre>\n" || d.Highlighted {
		t.Errorf("syntaxHighlight false: %q", d.HTML)
	}
}

func TestHighlightCSS(t *testing.T) {
	css := HighlightCSS(Options{})
	for _, w := range []string{
		"Chroma themes github (light) and github-dark (dark)",
		".chroma { color: light-dark(CanvasText, #e6edf3); background-color: light-dark(#f7f7f7, #0d1117); }",
		".chroma .k { color: light-dark(",
		"font-weight: bold",
	} {
		if !strings.Contains(css, w) {
			t.Errorf("no %q in:\n%s", w, css)
		}
	}
	if len(css) > 12<<10 {
		t.Errorf("%d bytes of CSS", len(css))
	}
	one := HighlightCSS(Options{Light: "monokai", SingleTheme: true})
	if strings.Contains(one, "light-dark") || !strings.Contains(one, "Chroma themes monokai,") {
		t.Errorf("one theme:\n%s", one)
	}
	if HighlightCSS(Options{NoCSS: true}) != "" || HighlightCSS(Options{NoHighlight: true}) != "" {
		t.Error("CSS with css: false or syntaxHighlight: false")
	}
	if err := CheckTheme("GitHub"); err != nil {
		t.Error(err)
	}
	if err := CheckTheme("githbu"); err == nil || !strings.Contains(err.Error(), `unknown theme "githbu"; the themes are `) || !strings.Contains(err.Error(), "github-dark") {
		t.Errorf("CheckTheme: %v", err)
	}
}

func TestLexerCache(t *testing.T) {
	if lexerFor("mermaid") != nil || lexerFor("Go") == nil {
		t.Fatal("lexers")
	}
	if _, ok := lexerCache.Load("mermaid"); !ok {
		t.Error("unknown language not cached")
	}
}
