// Package htmltpl renders the bench page with html/template: the same
// layout, header and cards as the Orbit and templ versions.
package htmltpl

import (
	"html/template"
	"io"

	"github.com/orbit-z/orbit/bench/go/data"
)

const src = `
{{define "layout"}}<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}}</title>
  </head>
  <body>
    {{template "content" .}}
    <footer>Rendered by Orbit</footer>
  </body>
</html>
{{end}}

{{define "header"}}<header class="site-header">
  <h1>{{.Title}}</h1>
  <nav>
    {{range .Nav}}<a href="{{.Href}}">{{.Text}}</a>{{end}}
  </nav>
</header>{{end}}

{{define "card"}}<article class="card" data-id="{{.ID}}"{{if .Featured}} data-featured{{end}}>
  <h2><a href="{{.Href}}">{{.Title}}</a></h2>
  <p class="body">{{.Body}}</p>
  <span class="count">{{.Count}}</span>
  <div class="extra"><em>#{{.ID}}</em></div>
</article>{{end}}

{{define "content"}}{{template "header" .}}
<main>
  <ul class="cards">
    {{range .Items}}<li>{{template "card" .}}</li>{{end}}
  </ul>
</main>{{end}}
`

var tmpl = template.Must(template.New("page").Parse(src))

// Page is the template's data.
type Page struct {
	Title string
	Nav   []data.Link
	Items []data.Item
}

// Render writes the cards page to w.
func Render(w io.Writer, p *Page) error {
	return tmpl.ExecuteTemplate(w, "layout", p)
}
