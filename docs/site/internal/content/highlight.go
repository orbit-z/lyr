package content

import (
	"html"
	"strings"
	"sync"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/internal/markdown"
)

// Code blocks are highlighted by Lyr's own Markdown package, the one
// `lyr generate` uses for .md pages: Chroma's token classes, with Lyr's
// lexer for lyr blocks. The site may import it: Go's internal rule allows
// any package under github.com/orbit-z/lyr, and this module's path is
// github.com/orbit-z/lyr/docs/site.

// highlight returns code as highlighted HTML; ok is false for a language
// Chroma doesn't know, or none.
func highlight(lang, code string) (string, bool) {
	switch strings.ToLower(lang) {
	case "", "text", "txt", "plain", "plaintext":
		return "", false
	}
	return markdown.Highlight(lang, code)
}

// homeExample is the component shown on the home page.
const homeExample = `---
import "example.com/blog/internal/posts"

type Props struct {
	Post posts.Post
}

url := "/blog/" + props.Post.Slug
---
<article class="card">
  <h2><a href={url}>{props.Post.Title}</a></h2>
  {if props.Post.Draft}
    <span class="badge">Draft</span>
  {/if}
  <slot />
</article>

<style>
  .card { padding: 1rem; border-radius: 8px; }
</style>
`

// HomeExample is the home page's .lyr example, highlighted.
var HomeExample = sync.OnceValue(func() lyr.HTML {
	if h, ok := highlight("lyr", homeExample); ok {
		return lyr.HTML(h)
	}
	return lyr.HTML(html.EscapeString(homeExample))
})

// HighlightCSS is the stylesheet of highlighted code: Chroma's github theme
// in light mode and github-dark in dark mode, through light-dark(), so it
// follows the page's color-scheme (and the theme toggle). It's written to
// src/styles/highlight.css; TestHighlightCSS checks the file is current.
func HighlightCSS() string { return markdown.HighlightCSS(markdown.Options{}) }
