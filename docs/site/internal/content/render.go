package content

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/orbit-z/orbit"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// RepoURL is where links to files outside docs/ point.
const RepoURL = "https://github.com/orbit-z/orbit/blob/main/"

type navLink struct {
	group, title, desc, slug string
}

// pendingLink is an internal link to check once every page is loaded.
type pendingLink struct {
	from, slug, fragment, dest string
}

type mdRenderer struct {
	docs     *Docs
	md       goldmark.Markdown
	links    []pendingLink
	navLinks []navLink
}

func newRenderer(d *Docs) *mdRenderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(), // the docs are ours, and use <details>
			renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100)),
		),
	)
	return &mdRenderer{docs: d, md: md}
}

func (r *mdRenderer) render(file string, src []byte) (*Page, error) {
	slug := SlugOf(file)
	p := &Page{Slug: slug, URL: "/" + slug, File: file, ids: map[string]bool{}}
	doc := r.md.Parser().Parse(text.NewReader(src))

	var sec *Section
	var buf strings.Builder
	flush := func() {
		if sec != nil {
			sec.Text = strings.Join(strings.Fields(buf.String()), " ")
			sec.lower = strings.ToLower(sec.Heading + " " + sec.Text)
			if sec.Text != "" || sec.Heading != "" {
				p.Sections = append(p.Sections, *sec)
			}
		}
		buf.Reset()
	}
	sec = &Section{}
	isReadme := slug == ""
	group := ""

	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			t := plainText(n, src)
			id := p.uniqueID(slugify(t))
			n.SetAttributeString("id", []byte(id))
			// A self-link, so readers can copy a heading's URL.
			a := ast.NewLink()
			a.Destination = []byte("#" + id)
			a.SetAttributeString("class", []byte("anchor"))
			a.SetAttributeString("aria-hidden", []byte("true"))
			a.AppendChild(a, ast.NewString([]byte("#")))
			n.AppendChild(n, a)
			switch {
			case n.Level == 1 && p.Title == "":
				p.Title = t
			case n.Level == 2 || n.Level == 3:
				p.TOC = append(p.TOC, Heading{Level: n.Level, ID: id, Text: t})
			}
			if n.Level == 2 {
				group = t
			}
			if n.Level > 1 {
				flush()
				sec = &Section{Heading: t, ID: id}
			}
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph:
			if p.Summary == "" && p.Title != "" {
				p.Summary = strings.Join(strings.Fields(plainText(n, src)), " ")
			}
			buf.WriteByte(' ')
		case *ast.Link:
			r.rewriteLink(p, n)
			if isReadme && group != "" {
				if li, ok := n.Parent().Parent().(*ast.ListItem); ok && n.Parent().FirstChild() == n {
					_ = li
					r.addNavLink(group, n, src)
				}
			}
		case *ast.Text:
			buf.Write(n.Value(src))
			if n.SoftLineBreak() || n.HardLineBreak() {
				buf.WriteByte(' ')
			}
		case *ast.String:
			buf.Write(n.Value)
		case *ast.CodeSpan:
			buf.WriteString(plainText(n, src))
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			buf.WriteByte(' ')
			lines := n.Lines()
			for i := range lines.Len() {
				seg := lines.At(i)
				buf.Write(seg.Value(src))
			}
			buf.WriteByte(' ')
		case *ast.ListItem, *extast.TableCell:
			buf.WriteByte(' ')
		}
		if n.Type() == ast.TypeBlock {
			buf.WriteByte(' ')
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	flush()

	var out bytes.Buffer
	if err := r.md.Renderer().Render(&out, src, doc); err != nil {
		return nil, err
	}
	p.HTML = orbit.HTML(out.String())
	if p.Title == "" {
		p.Title = slug
	}
	return p, nil
}

// addNavLink records a README.md list entry "[Title](page.md): description".
func (r *mdRenderer) addNavLink(group string, n *ast.Link, src []byte) {
	dest := string(n.Destination)
	if !strings.HasPrefix(dest, "/") || strings.Contains(dest, "#") {
		return // external, or an anchor into a page
	}
	desc := strings.TrimSpace(plainText(n.Parent(), src))
	desc = strings.TrimPrefix(desc, plainText(n, src))
	desc = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(desc), ":"))
	r.navLinks = append(r.navLinks, navLink{
		group: group,
		title: plainText(n, src),
		desc:  desc,
		slug:  strings.TrimPrefix(dest, "/"),
	})
}

// rewriteLink turns a relative link to a .md file into a site URL, and a
// relative link to anything else in the repository into a GitHub URL.
func (r *mdRenderer) rewriteLink(p *Page, n *ast.Link) {
	dest := string(n.Destination)
	if dest == "" {
		return
	}
	if strings.HasPrefix(dest, "#") {
		r.links = append(r.links, pendingLink{from: p.File, slug: p.Slug, fragment: dest[1:], dest: dest})
		return
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(u.Path, "/") {
		return
	}
	target := path.Join(pageDir(p.File), u.Path)
	if strings.HasPrefix(target, "../") || !strings.HasSuffix(target, ".md") {
		// Outside docs/ (../examples/blog/README.md), or not a page.
		n.Destination = []byte(RepoURL + path.Clean("docs/"+target) + fragmentOf(u))
		return
	}
	slug := SlugOf(target)
	n.Destination = []byte("/" + slug + fragmentOf(u))
	r.links = append(r.links, pendingLink{from: p.File, slug: slug, fragment: u.Fragment, dest: dest})
}

func fragmentOf(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	return "#" + u.Fragment
}

// checkLinks reports links to missing pages and to anchors that no heading has.
func (r *mdRenderer) checkLinks() {
	for _, l := range r.links {
		target := r.docs.bySlug[l.slug]
		switch {
		case target == nil:
			r.docs.Problems = append(r.docs.Problems, fmt.Sprintf("%s: link %s: no such page", l.from, l.dest))
		case l.fragment != "" && !target.ids[l.fragment]:
			r.docs.Problems = append(r.docs.Problems, fmt.Sprintf("%s: link %s: %s has no heading #%s", l.from, l.dest, target.File, l.fragment))
		}
	}
}

// uniqueID returns id, or id-1, id-2, … if it's taken, as GitHub does.
func (p *Page) uniqueID(id string) string {
	u := id
	for i := 1; p.ids[u]; i++ {
		u = id + "-" + strconv.Itoa(i)
	}
	p.ids[u] = true
	return u
}

// slugify makes a GitHub-style heading anchor: lowercase, punctuation
// removed (except - and _), spaces turned into -.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.M, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plainText returns the text of an inline tree, without markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Value(src))
			if c.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.CodeSpan:
			for t := c.FirstChild(); t != nil; t = t.NextSibling() {
				if s, ok := t.(*ast.Text); ok {
					b.Write(s.Value(src))
				} else if s, ok := t.(*ast.String); ok {
					b.Write(s.Value)
				}
			}
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// codeRenderer renders fenced code blocks as a <figure>, captioned with the
// file name of the docs' "lang file=path" info strings.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderFenced)
}

func renderFenced(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	var lang, file string
	var flags []string
	if n.Info != nil {
		fields := strings.Fields(string(n.Info.Segment.Value(src)))
		for i, f := range fields {
			switch {
			case i == 0:
				lang = f
			case strings.HasPrefix(f, "file="):
				file = strings.TrimPrefix(f, "file=")
			case f == "expect-error" || f == "expect-warning":
				flags = append(flags, f)
			}
		}
	}
	w.WriteString(`<figure class="code`)
	if lang != "" {
		w.WriteString(" code-" + html.EscapeString(lang))
	}
	w.WriteString(`">`)
	if file != "" || len(flags) > 0 {
		w.WriteString(`<figcaption>`)
		if file != "" {
			w.WriteString(`<span class="file">` + html.EscapeString(file) + `</span>`)
		}
		for _, f := range flags {
			label := "won't compile"
			if f == "expect-warning" {
				label = "compiles with a warning"
			}
			w.WriteString(`<span class="badge badge-` + f + `">` + label + `</span>`)
		}
		w.WriteString(`</figcaption>`)
	}
	w.WriteString(`<pre><code`)
	if lang != "" {
		w.WriteString(` class="language-` + html.EscapeString(lang) + `"`)
	}
	w.WriteString(`>`)
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		w.WriteString(html.EscapeString(string(seg.Value(src))))
	}
	w.WriteString("</code></pre></figure>\n")
	return ast.WalkSkipChildren, nil
}
