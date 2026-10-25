package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLazyLocals builds a project with a real Locals type and serves it for
// real: a page whose components never reference locals declares no locals
// (no LocalsOf, no import of src/locals) and is served with Orbit's 1
// allocation (the Content-Length value), while a page and a component that do reference it share the
// one per-request *Locals, the one middleware attached.
func TestLazyLocals(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"options":                    "locals=" + testModule + "/src/locals\n",
		"src/components/Plain.orbit": "---\ntype Props struct{ N int }\n---\n<p>{props.N}</p>\n",
		"src/pages/plain.orbit":      "---\nimport Plain \"../components/Plain.orbit\"\n---\n<main><Plain n={1} /><Plain n={2} /></main>\n",
		"src/components/Who.orbit":   "<p>{locals.User} {locals.N}</p>\n",
		"src/pages/who.orbit": "---\nimport Who \"../components/Who.orbit\"\n" +
			"locals.N++\n---\n<main><Who /></main>\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := generateCase(t, loadCase(t, dir))
	for _, r := range res {
		if r.a.HasErrors() || len(r.out.Diagnostics) > 0 {
			t.Fatalf("%s: %v %v", r.path, r.a.Diagnostics, r.out.Diagnostics)
		}
		impl := r.out.Impl.Content
		uses := strings.Contains(r.path, "who") || strings.Contains(r.path, "Who")
		if got := bytes.Contains(impl, []byte(".LocalsOf[")); got != uses {
			t.Errorf("%s: declares locals = %v, want %v:\n%s", r.path, got, uses, impl)
		}
		if got := bytes.Contains(impl, []byte(NameLocals+" ")); got != uses {
			t.Errorf("%s: imports src/locals = %v, want %v", r.path, got, uses)
		}
	}

	m := newGenModule(t)
	m.write("src/locals/locals.go", "package locals\n\ntype Locals struct {\n\tUser string\n\tN    int\n}\n")
	for _, r := range res {
		m.writeOutput(r.out)
	}
	m.buildAndVet()
	out := m.run(`package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/blog/src/locals"
	plain "example.com/blog/orbit_gen/decl/pages/plain"
	who "example.com/blog/orbit_gen/decl/pages/who"
	_ "example.com/blog/orbit_gen/impl/components/plain"
	_ "example.com/blog/orbit_gen/impl/components/who"
	_ "example.com/blog/orbit_gen/impl/pages/plain"
	_ "example.com/blog/orbit_gen/impl/pages/who"
	"github.com/bertrandkhe/orbit"
)

type discard struct{ h http.Header }

func (d *discard) Header() http.Header         { return d.h }
func (d *discard) WriteHeader(int)             {}
func (d *discard) Write(b []byte) (int, error) { return len(b), nil }

func main() {
	site := orbit.NewSite([]orbit.Route{
		{Pattern: "/plain", File: "src/pages/plain.orbit", Render: func(c *orbit.Ctx, w *orbit.Writer) error {
			return plain.Render(c, w, plain.Props{}, orbit.Slots{})
		}},
		{Pattern: "/who", File: "src/pages/who.orbit", Render: func(c *orbit.Ctx, w *orbit.Writer) error {
			return who.Render(c, w, who.Props{}, orbit.Slots{})
		}},
	}, nil)
	pages := site.Middleware(http.NotFoundHandler())
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("user") {
			var l *locals.Locals
			r, l = orbit.EnsureLocals[locals.Locals](r)
			l.User = r.URL.Query().Get("user")
		}
		pages.ServeHTTP(w, r)
	})
	for _, target := range []string{"/plain", "/who", "/who?user=ann"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		fmt.Printf("%s %d %s\n", target, rec.Code, bytes.TrimSpace(rec.Body.Bytes()))
	}
	d := &discard{h: http.Header{}}
	for _, target := range []string{"/plain", "/who"} {
		r := httptest.NewRequest("GET", target, nil)
		fmt.Println(target, "allocs", testing.AllocsPerRun(1000, func() {
			clear(d.h)
			pages.ServeHTTP(d, r)
		}))
	}
}
`)
	want := "/plain 200 <main><p>1</p><p>2</p></main>\n" +
		"/who 200 <main><p> 1</p></main>\n" +
		"/who?user=ann 200 <main><p>ann 1</p></main>\n" +
		"/plain allocs 1\n" + // Content-Length only
		"/who allocs 2\n" // and the new(Locals)
	t.Logf("output:\n%s", out)
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}
