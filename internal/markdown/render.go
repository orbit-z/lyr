package markdown

import (
	"bytes"
	"html"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Heading is one heading of a document (lyr.Heading in generated code).
type Heading struct {
	Depth int
	Slug  string
	Text  string
}

// Link is a relative link to a Markdown page, rewritten to its URL; the
// project checks its fragment once every file is parsed.
type Link struct {
	// Pos is the offset of the link's "[" in the file; Dest is the
	// destination as written, Target the project-relative .md path, and
	// Fragment its #fragment ("" for none).
	Pos      int
	Dest     string
	Target   string
	Fragment string
}

// Resolver tells the parser about the project's Markdown files: for a
// project-relative path, whether the file exists, and its page's canonical
// URL ("" if it isn't a page; see CanonicalURL).
type Resolver func(target string) (url string, exists bool)

// CanonicalURL returns the canonical URL of a page route (PLAN.md §4e): the
// route itself, or with always (lyr.json "trailingSlash": "always") the
// route plus "/", except "/" and an extension route, whose last segment has
// a file extension (/rss.xml).
func CanonicalURL(route string, always bool) string {
	if !always || route == "/" || hasFileExtension(route) {
		return route
	}
	return route + "/"
}

// hasFileExtension reports whether the last segment of path has a '.' that
// isn't its first byte, followed only by one or more of [A-Za-z0-9_]: the
// runtime router's rule (Astro's hasFileExtension).
func hasFileExtension(path string) bool {
	for i := len(path) - 1; i >= 0; i-- {
		c := path[i]
		switch {
		case c == '.':
			return i < len(path)-1 && i > 0 && path[i-1] != '/'
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '_':
		default:
			return false
		}
	}
	return false
}

// engines caches one goldmark instance per option set; they are safe for
// concurrent use.
var engines sync.Map // engineKey → goldmark.Markdown

type engineKey struct{ gfm, smarty, highlight bool }

func engine(o Options) goldmark.Markdown {
	k := engineKey{!o.NoGFM, !o.NoSmartypants, !o.NoHighlight}
	if md, ok := engines.Load(k); ok {
		return md.(goldmark.Markdown)
	}
	var exts []goldmark.Extender
	if k.gfm {
		exts = append(exts, extension.GFM, extension.Footnote)
	}
	if k.smarty {
		// Astro's smartypants doesn't turn << and >> into guillemets, and
		// does dashes its own way (dashes.go).
		exts = append(exts, extension.NewTypographer(extension.WithTypographicSubstitutions(
			map[extension.TypographicPunctuation][]byte{extension.LeftAngleQuote: nil, extension.RightAngleQuote: nil,
				extension.EnDash: nil, extension.EmDash: nil})), dashes{})
	}
	ropts := []renderer.Option{gmhtml.WithUnsafe()}
	if k.highlight {
		ropts = append(ropts, renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100)))
	}
	md := goldmark.New(goldmark.WithExtensions(exts...), goldmark.WithRendererOptions(ropts...))
	engines.Store(k, md)
	return md
}

// body converts the Markdown body of d (d.Src with the frontmatter blanked
// out, so offsets are file offsets) to HTML, assigning heading IDs and
// rewriting links.
func (d *Doc) body(src []byte, o Options, resolve Resolver) {
	md := engine(o)
	root := md.Parser().Parse(text.NewReader(src))
	ids := map[string]bool{}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			t := plainText(n, src)
			id := uniqueID(ids, slugify(t))
			n.SetAttributeString("id", []byte(id))
			d.Headings = append(d.Headings, Heading{Depth: n.Level, Slug: id, Text: t})
		case *ast.Link:
			d.link(n, resolve)
		case *ast.FencedCodeBlock:
			if !o.NoHighlight && n.Info != nil && lexerFor(string(n.Language(src))) != nil {
				d.Highlighted = true
			}
		}
		return ast.WalkContinue, nil
	})
	var b bytes.Buffer
	if err := md.Renderer().Render(&b, src, root); err != nil {
		d.errorf(0, "can't render the Markdown: %v", err)
	}
	d.HTML = b.String()
}

// link rewrites a relative link to a Markdown page to its URL (PLAN.md
// §2.4), reporting a missing target or a non-page.
func (d *Doc) link(n *ast.Link, resolve Resolver) {
	dest := string(n.Destination)
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.Path == "" ||
		strings.HasPrefix(u.Path, "/") || !compiler.IsMarkdown(u.Path) {
		return
	}
	pos := n.Pos()
	if pos < 0 {
		pos = 0
	}
	target := path.Join(path.Dir(d.Path), u.Path)
	route, exists := "", false
	if resolve != nil {
		route, exists = resolve(target)
	}
	switch {
	case !exists:
		d.errorf(pos, "link %s: no such file", dest)
		return
	case route == "":
		d.errorf(pos, "link %s: %s isn't a page, so it has no URL; link to a page, or import it into one", dest, target)
		return
	}
	out := route
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	n.Destination = []byte(out)
	d.Links = append(d.Links, Link{Pos: pos, Dest: dest, Target: target, Fragment: u.Fragment})
}

// uniqueID returns id, or id-1, id-2, … if it's taken, as github-slugger
// does, and marks it taken.
func uniqueID(ids map[string]bool, id string) string {
	u := id
	for i := 1; ids[u]; i++ {
		u = id + "-" + strconv.Itoa(i)
	}
	ids[u] = true
	return u
}

// slugify makes a github-slugger heading ID: lowercase, punctuation and
// symbols removed (except - and _), spaces turned into -.
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

// plainText returns the text of an inline tree, without markup: raw HTML
// is dropped, entities and backslash escapes are resolved.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			v := c.Value(src)
			if !c.IsRaw() {
				v = util.UnescapePunctuations(v)
			}
			b.WriteString(html.UnescapeString(string(v)))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			if c.IsCode() {
				b.WriteString(html.UnescapeString(string(c.Value)))
			} else {
				b.Write(c.Value)
			}
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// codeRenderer renders code blocks (PLAN.md §2.4): <pre class="chroma"
// tabindex="0" data-language="go"><code> with Chroma's token classes for a
// language Chroma knows, and escaped text for any other or none.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderCode)
	reg.Register(ast.KindCodeBlock, renderCode)
}

func renderCode(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := node.(*ast.FencedCodeBlock); ok && f.Info != nil {
		lang = string(f.Language(src))
	}
	var code strings.Builder
	lines := node.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		code.Write(seg.Value(src))
	}
	var b strings.Builder
	b.WriteString(`<pre class="chroma" tabindex="0" data-language="`)
	if lang == "" {
		b.WriteString("plaintext")
	} else {
		b.WriteString(html.EscapeString(lang))
	}
	b.WriteString(`"><code>`)
	if lang == "" || !highlight(&b, lang, code.String()) {
		codeEscaper.WriteString(&b, code.String())
	}
	b.WriteString("</code></pre>\n")
	_, _ = w.WriteString(b.String())
	return ast.WalkSkipChildren, nil
}
