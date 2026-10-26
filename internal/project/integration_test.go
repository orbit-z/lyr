package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// siteFiles is a small blog: pages index, about, blog/[slug],
// docs/[...rest] and 404, a layout and a component with props and a slot,
// and a Go package import.
var siteFiles = map[string]string{
	"orbit.json": `{"site": "https://blog.example.com", "compressHTML": "html"}`,
	"internal/posts/posts.go": `package posts

type Post struct {
	Slug, Title string
	Views       int
}

func All() []Post { return []Post{{"hello", "Hello", 3}} }
`,
	"src/layouts/Base.orbit": `---
type Props struct{ Title string }
---
<!DOCTYPE html>
<html><head><title>{props.Title}</title></head>
<body><slot /><footer><slot name="footer">no footer</slot></footer></body></html>
`,
	"src/components/Card.orbit": `---
import "example.com/blog/internal/posts"

type Props struct {
	Post     posts.Post
	Featured bool
}
---
<article data-featured={props.Featured}>
  <a href={"/blog/" + props.Post.Slug}>{props.Post.Title}</a> ({props.Post.Views})
  <slot />
</article>
`,
	"src/pages/index.orbit": `---
import (
	"example.com/blog/internal/posts"
	Base "../layouts/Base.orbit"
	Card "../components/Card.orbit"
)

list := posts.All()
---
<Base title="Home">
  {for _, p := range list}
    <Card post={p} featured><em>hi</em></Card>
  {/for}
  <p slot="footer">footer</p>
</Base>
`,
	"src/pages/about.orbit": "<p>About</p>\n",
	"src/components/Comments.orbit": `---
import "time"

type Props struct {
	PostID int
	Since  time.Time
}
---
<section>{props.PostID}</section>
`,
	"src/components/Post.orbit": `---
import Comments "./Comments.orbit"

type Props struct{ Slug string }
---
<h1>{props.Slug}</h1>
<Comments server:defer postID={1}><p slot="fallback">Loading…</p></Comments>
`,
	"src/pages/blog/[slug].orbit":    "---\nimport Post \"../../components/Post.orbit\"\n---\n<Post slug={params.Slug} />\n",
	"src/pages/docs/[...rest].orbit": "<p>{params.Rest}</p>\n",
	"src/pages/404.orbit":            "<p>Not found</p>\n",
}

// TestGenerateSite generates the site with and without cmd/server and
// builds and vets the result. The runtime is still a contract stub, so the
// server isn't run.
func TestGenerateSite(t *testing.T) {
	needGo(t)
	p := newTestProject(t, siteFiles)
	res := p.generate()
	if len(res.Diagnostics) > 0 || len(res.Warnings) > 0 {
		t.Fatalf("diagnostics:\n%s\nwarnings: %v", joinLines(diagStrings(res.Diagnostics)), res.Warnings)
	}
	for _, f := range res.Project.Files {
		if !f.Typed {
			t.Errorf("%s: not type-checked", f.Path)
		}
	}
	if res.Timings.GoLists != 1 {
		t.Errorf("%d go list runs, want 1 (the one started during Front)", res.Timings.GoLists)
	}
	if !strings.Contains(p.read("orbit_gen/impl/components/card/render.go"), "orbit__w.Int(") {
		t.Errorf("Card has no typed Int write")
	}
	if !p.exists(ServerFile) {
		t.Fatalf("no %s without cmd/server", ServerFile)
	}
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		if out, err := p.goCmd(args...); err != nil || strings.TrimSpace(out) != "" {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// A no-change re-run rewrites nothing.
	if res := p.generate(); len(res.Write.Written) != 0 || len(res.Write.Removed) != 0 {
		t.Errorf("re-run wrote %v, removed %v", res.Write.Written, res.Write.Removed)
	}

	// With a user cmd/server, the default entry is removed.
	p.write("cmd/server/main.go", `package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/orbit"

	"example.com/blog/orbit_gen/app"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/", app.New().Handler())
	if err := orbit.ListenAndServe(mux); err != nil {
		log.Fatal(err)
	}
}
`)
	res = p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	if p.exists(ServerFile) || p.exists("orbit_gen/cmd") {
		t.Errorf("the default server entry is still there with a cmd/server")
	}
	if want := []string{ServerFile}; strings.Join(res.Write.Removed, ",") != strings.Join(want, ",") {
		t.Errorf("removed %v, want %v", res.Write.Removed, want)
	}
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		if out, err := p.goCmd(args...); err != nil || strings.TrimSpace(out) != "" {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// TestGenerateTypeError: a Go type error in an .orbit file isn't Orbit's to
// report; generate succeeds (the file keeps its fallbacks) and go build
// reports it at the .orbit position, cleaned up by CleanGoOutput.
func TestGenerateTypeError(t *testing.T) {
	needGo(t)
	files := map[string]string{}
	for k, v := range siteFiles {
		files[k] = v
	}
	files["src/pages/about.orbit"] = "---\nimport Card \"../components/Card.orbit\"\n\nvar n int = Card.Props{}\n---\n<Card /><p>{n}</p>\n"
	p := newTestProject(t, files)
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	out, err := p.goCmd("build", "./...")
	if err == nil {
		t.Fatal("go build succeeded")
	}
	clean := string(res.Project.CleanGoOutput([]byte(out)))
	want := "src/pages/about.orbit:4:13: cannot use Card.Props{} (value of struct type Card.Props) as int value in variable declaration"
	if !strings.Contains(clean, want) {
		t.Errorf("cleaned output:\n%s\nwant a line %q", clean, want)
	}
	if regexp.MustCompile(`(^|\n)\./src/`).MatchString(clean) {
		t.Errorf("cleaned output still has ./src/:\n%s", clean)
	}
}

// TestGenerateErrorsWriteNothing: with an error, orbit_gen/ isn't touched.
func TestGenerateErrorsWriteNothing(t *testing.T) {
	needGo(t)
	files := map[string]string{}
	for k, v := range siteFiles {
		files[k] = v
	}
	files["src/pages/post-[id].orbit"] = "<p>x</p>\n"
	files["src/pages/index.orbit"] = "---\nimport Card \"../components/Card.orbit\"\n---\n<Card psot={1} />\n"
	p := newTestProject(t, files)
	p.write("orbit_gen/app/app.go", "// Code generated by orbit. DO NOT EDIT.\n\npackage app\n")
	res := p.generate()
	want := []string{
		`src/pages/index.orbit:4:7: unknown prop "psot" on <Card> (props: post, featured; add an orbit.RestAttrs field to pass HTML attributes through)`,
		`src/pages/post-[id].orbit: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`,
	}
	if got := diagStrings(res.Diagnostics); joinLines(got) != joinLines(want) {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
	}
	if res.Write != nil {
		t.Errorf("wrote %v", res.Write)
	}
	if got := p.read("orbit_gen/app/app.go"); got != "// Code generated by orbit. DO NOT EDIT.\n\npackage app\n" {
		t.Errorf("app.go was rewritten")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "orbit_gen", "decl")); err == nil {
		t.Errorf("decl packages were written")
	}
}
