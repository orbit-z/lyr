// Package synth generates the synthetic Orbit project shared by the
// benchmarks in bench/devloop and bench/lsp (see bench/devloop/README.md,
// "The project").
package synth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Shape is the size of the synthetic project.
type Shape struct {
	Files      int // .orbit files in total
	Pages      int
	Layouts    int // always 2: Base, and Docs (which wraps Base)
	Components int // Files - Pages - Layouts, Badge included
}

func NewShape(files int) Shape {
	s := Shape{Files: files, Pages: files / 5, Layouts: 2}
	s.Components = files - s.Pages - s.Layouts
	return s
}

// chain is the number of layered components C0..C(chain-1); Badge is the
// last component.
func (s Shape) Chain() int { return s.Components - 1 }

// target is the component edited by the body/frontmatter/CSS/TS edits:
// index.orbit renders it (through target-1), so the body edit is visible
// at "/". It has a scoped style and a script (target%4 == 0).
func (s Shape) Target() int { return (s.Chain() / 3) &^ 3 }

// badgeImporters are the components that import Badge (the Props edit).
func (s Shape) BadgeImporters() []int {
	var out []int
	for i := 0; i < s.Chain(); i += 16 {
		out = append(out, i)
	}
	return out
}

func CompPath(i int) string { return fmt.Sprintf("src/components/C%d.orbit", i) }

// Paths of the files the devloop edits touch.
const (
	BadgePath = "src/components/Badge.orbit"
	DataPath  = "internal/data/data.go"
)

// ProjectFiles returns the synthetic project:
//   - internal/data: the types every Props uses (data.Item), so an
//     exported change there recompiles every generated package;
//   - internal/format: helpers used by component frontmatter only;
//   - src/locals: typed locals, set by cmd/server's middleware and read by
//     templates;
//   - cmd/server: the user's server entry (mux + locals middleware);
//   - components C0..C(n-1): component i renders i+1 and i+2 while its
//     depth prop lasts, with default and named slots; every fourth one has
//     a scoped <style> and a TypeScript <script> importing
//     src/scripts/util.ts; every 16th also renders Badge;
//   - Badge: imported by ~n/16 components (10 at 200 files);
//   - layouts Base and Docs (Docs wraps Base);
//   - pages: index, plain, [slug] and [...rest] pages, each rendering one
//     component three levels deep inside a layout.
func ProjectFiles(s Shape, salt string) map[string]string {
	files := map[string]string{
		DataPath: DataGo(salt, ""),
		"internal/format/format.go": `package format

import "strconv"

// Money formats a price.
func Money(p float64) string { return "$" + strconv.FormatFloat(p, 'f', 2, 64) }
`,
		"src/locals/locals.go": `package locals

type Locals struct {
	RequestID string
	Theme     string
}
`,
		"src/scripts/util.ts": "export const twice = (n: number): number => n * 2;\n",
		"cmd/server/main.go": `package main

import (
	"log"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/bertrandkhe/orbit"

	"example.com/devloop/orbit_gen/app"
	"example.com/devloop/src/locals"
)

var lastID atomic.Int64

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.RequestID = "req-" + strconv.FormatInt(lastID.Add(1), 10)
		l.Theme = "light"
		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("pong")) })
	mux.Handle("/", app.New().Handler())
	if err := orbit.ListenAndServe(requestID(mux)); err != nil {
		log.Fatal(err)
	}
}
`,
		BadgePath: BadgeOrbit(""),
		"src/layouts/Base.orbit": `---
type Props struct{ Title string }
---
<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>{props.Title}</title></head>
<body class={locals.Theme}><slot /></body></html>
`,
		"src/layouts/Docs.orbit": `---
import Base "./Base.orbit"

type Props struct {
	Title   string
	Section string
}
---
<Base title={props.Title}>
  <nav><a href="/">Home</a> / {props.Section}</nav>
  <main><slot /></main>
</Base>
`,
	}
	n := s.Chain()
	badge := map[int]bool{}
	for _, i := range s.BadgeImporters() {
		badge[i] = true
	}
	for i := range n {
		files[CompPath(i)] = ComponentOrbit(i, n, badge[i], "", "", "", "")
	}
	for i := range s.Pages {
		name, src := PageOrbit(s, i)
		files[name] = src
	}
	return files
}

// DataGo is internal/data. api adds exported declarations (the API edit);
// salt is in a //go:noinline body (the body edit: no export data change).
func DataGo(salt, api string) string {
	return `package data

import "time"

type Item struct {
	ID    int
	Name  string
	Price float64
	Tags  []string
	When  time.Time
	Draft bool
}

type Status int

func (s Status) String() string { return [...]string{"draft", "live"}[s] }

func Items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: i, Name: Name(i), Price: 1.5, Tags: []string{"a", "b"}}
	}
	return out
}

// Name is not inlinable, so editing its body leaves the export data alone.
//
//go:noinline
func Name(i int) string {
	if i < 0 {
		return "` + salt + `"
	}
	return "item"
}
` + api
}

func BadgeOrbit(extra string) string {
	return `---
import "example.com/devloop/internal/data"

type Props struct {
	Label string
	Item  data.Item
` + extra + `}
---
<span class="badge" data-id={props.Item.ID}>{props.Label}: {props.Item.Name}</span>
`
}

// ComponentOrbit is component i of n. The edit parameters are inserted
// verbatim: tmpl into the template body, front into the frontmatter body,
// css into the scoped style, ts into the script.
func ComponentOrbit(i, n int, badge bool, tmpl, front, css, ts string) string {
	var imports, calls strings.Builder
	for _, j := range []int{i + 1, i + 2} {
		if j < n {
			fmt.Fprintf(&imports, "\tC%d \"./C%d.orbit\"\n", j, j)
			fmt.Fprintf(&calls, "  {if props.Depth > 0}<C%d title={props.Title} count={props.Count + 1} items={props.Items} depth={props.Depth - 1}><span>{props.Count}</span><p slot=\"footer\">f</p></C%d>{/if}\n", j, j)
		}
	}
	if badge {
		imports.WriteString("\tBadge \"./Badge.orbit\"\n")
		calls.WriteString("  {for _, it := range props.Items}<Badge label={props.Title} item={it} />{/for}\n")
	}
	src := fmt.Sprintf(`---
import (
	"fmt"
	"strings"

	"example.com/devloop/internal/data"
	"example.com/devloop/internal/format"
%s)

type Props struct {
	Title  string
	Count  int
	Items  []data.Item
	Active bool
	Depth  int
}

func label(it data.Item) string { return fmt.Sprintf("%%s-%%d", it.Name, it.ID) }

upper := strings.ToUpper(props.Title)%s
status := data.Status(props.Count %% 2)
---
<section class="c%d" data-count={props.Count} data-active={props.Active} data-theme={locals.Theme}>
  <h2 title={upper}>{upper} ({status})%s</h2>
  <ul>
    {for _, it := range props.Items}
      <li id={label(it)} class={strings.Join(it.Tags, " ")}>{it.Name}: {format.Money(it.Price)} {it.ID} {it.Draft}</li>
    {/for}
  </ul>
  <slot />
%s  <footer><slot name="footer">none</slot></footer>
</section>
`, imports.String(), front, i, tmpl, calls.String())
	if i%4 == 0 {
		src += fmt.Sprintf(`
<style>
  .c%d h2 { color: red; }
  .c%d li:hover { color: blue; }%s
</style>

<script>
  import { twice } from "../scripts/util";
  const n: number = twice(%d);%s
  document.querySelectorAll(".c%d").forEach((el) => el.setAttribute("data-n", String(n)));
</script>
`, i, i, css, i, ts, i)
	}
	return src
}

// PageOrbit returns page i: page 0 is the index (rendering the edit
// target), then [slug], [...rest] and plain pages; odd pages use the Docs
// layout.
func PageOrbit(s Shape, i int) (name, src string) {
	n := s.Chain()
	c := (i * 4) % n
	if i == 0 {
		c = max(s.Target()-1, 0)
	}
	name = fmt.Sprintf("src/pages/p%d.orbit", i)
	title := `"Page"`
	switch {
	case i == 0:
		name = "src/pages/index.orbit"
	case i%4 == 1:
		name = fmt.Sprintf("src/pages/s%d/[slug].orbit", i)
		title = "params.Slug"
	case i%8 == 2:
		name = fmt.Sprintf("src/pages/r%d/[...rest].orbit", i)
		title = "params.Rest"
	}
	up := "../"
	if strings.Contains(name, "[") {
		up = "../../"
	}
	layout, open, close := "Base", "<Base title={"+title+"}>", "</Base>"
	if i%2 == 1 {
		layout, open, close = "Docs", "<Docs title={"+title+"} section=\"docs\">", "</Docs>"
	}
	src = fmt.Sprintf(`---
import (
	"example.com/devloop/internal/data"
	%s "%slayouts/%s.orbit"
	C%d "%scomponents/C%d.orbit"
)

items := data.Items(3)
---
%s
  <h1>{%s}</h1>
  <C%d title="x" count={1} items={items} active depth={2}>body</C%d>
%s
`, layout, up, layout, c, up, c, open, title, c, c, close)
	return name, src
}

// NewPage is the page added by the new-page edit.
func NewPage(k int) (name, src string) {
	return fmt.Sprintf("src/pages/added%d.orbit", k), fmt.Sprintf(`---
import (
	"example.com/devloop/internal/data"
	Base "../layouts/Base.orbit"
	C3 "../components/C3.orbit"
)

items := data.Items(2)
---
<Base title="Added %d">
  <h1>Added page %d</h1>
  <C3 title="added" count={%d} items={items} depth={1}>new</C3>
</Base>
`, k, k, k)
}

// WriteProject writes files and a go.mod requiring the runtime at orbitRoot.
func WriteProject(dir, orbitRoot string, files map[string]string) error {
	gomod := "module example.com/devloop\n\ngo 1.27\n\ntoolchain go1.27.3\n\n" +
		"require github.com/bertrandkhe/orbit v0.0.0\n\nreplace github.com/bertrandkhe/orbit => " + filepath.ToSlash(orbitRoot) + "\n"
	if err := WriteFile(dir, "go.mod", gomod); err != nil {
		return err
	}
	for name, content := range files {
		if err := WriteFile(dir, name, content); err != nil {
			return err
		}
	}
	return nil
}

func WriteFile(dir, rel, content string) error {
	name := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, []byte(content), 0o644)
}
