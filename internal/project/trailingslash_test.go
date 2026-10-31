package project

import (
	"strings"
	"testing"
)

// TestTrailingSlashAlwaysProject generates the Markdown project with
// "trailingSlash": "always" and an extension page, then serves it through
// the generated app.New() (PLAN.md §4e): canonical URLs end with "/", the
// other form gets a 308, and Markdown URLs and links follow the mode.
func TestTrailingSlashAlwaysProject(t *testing.T) {
	needGo(t)
	files := map[string]string{}
	for k, v := range mdFiles {
		files[k] = v
	}
	files["lyr.json"] = `{"trailingSlash": "always"}`
	files["src/pages/rss.xml.lyr"] = "---\nctx.Header().Set(\"Content-Type\", \"application/rss+xml\")\n---\n<rss></rss>\n"
	files["internal/apptest/app_test.go"] = `package apptest

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/blog/lyr_gen/app"
)

func get(t *testing.T, method, path string) (int, string, string) {
	rec := httptest.NewRecorder()
	app.New().Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, rec.Header().Get("Location"), string(b)
}

func TestPages(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		code         int
		location     string
		want         []string
	}{
		{"GET", "/blog/hello/", 200, "", []string{"<title>Hello, Lyr</title>",
			"<a href=\"/blog/routing-guide/#setup\">routing guide</a>"}},
		{"GET", "/", 200, "", []string{"<a href=\"/blog/hello/\">Hello, Lyr</a> (1)"}},
		{"GET", "/rss.xml", 200, "", []string{"<rss></rss>"}},
		{"GET", "/blog/hello", 308, "/blog/hello/", nil},
		{"HEAD", "/blog/hello", 308, "/blog/hello/", nil},
		{"POST", "/blog/hello?x=1", 308, "/blog/hello/?x=1", nil},
		{"GET", "/rss.xml/", 404, "", []string{"<h1 id=\"not-here\">Not here</h1>"}},
		{"GET", "/nope/", 404, "", []string{"<h1 id=\"not-here\">Not here</h1>"}},
	} {
		code, loc, body := get(t, tt.method, tt.path)
		if code != tt.code || loc != tt.location {
			t.Errorf("%s %s: %d %q, want %d %q", tt.method, tt.path, code, loc, tt.code, tt.location)
		}
		if tt.code == 308 && body != "" {
			t.Errorf("%s %s: body %q, want none", tt.method, tt.path, body)
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s %s: no %q in:\n%s", tt.method, tt.path, w, body)
			}
		}
	}
}
`
	p := newTestProject(t, files)
	res := p.generate()
	want := "src/pages/blog/hello.md:17:10: warning: link ./routing-guide.md#setup: src/pages/blog/routing-guide.md has no heading #setup"
	if got := joinLines(diagStrings(res.Diagnostics)); got != want {
		t.Fatalf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if decl := p.read("lyr_gen/decl/pages/blog/hello_md/decl.go"); !strings.Contains(decl, `const URL = "/blog/hello/"`) {
		t.Errorf("hello.md decl:\n%s", decl)
	}
	if app := p.read("lyr_gen/app/app.go"); !strings.Contains(app, "defaults := []lyr.Option{lyr.WithTrailingSlash(lyr.TrailingSlashAlways)}") {
		t.Errorf("app.go:\n%s", app)
	}
	if out, err := p.goCmd("test", "./internal/apptest"); err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
}
