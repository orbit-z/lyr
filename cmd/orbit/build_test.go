package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// TestPrintBundles is the orbit build --verbose golden (PLAN.md §5e): a
// preload row below the module, a lazy row listed once per page, the
// import() section, and totals that count each file once.
func TestPrintBundles(t *testing.T) {
	file := func(name string, n int) assets.File {
		return assets.File{Name: name, Contents: []byte(strings.Repeat("x", n))}
	}
	page := func(p string) *project.File {
		return &project.File{Path: p, Info: compiler.FileInfo{Role: compiler.RolePage}}
	}
	p := &project.Project{
		Files: []*project.File{page("src/pages/about.orbit"), page("src/pages/index.orbit"), page("src/pages/plain.orbit"),
			{Path: "src/components/Comments.orbit", Info: compiler.FileInfo{Role: compiler.RoleComponent}}},
		Assets: &assets.Result{
			Pages: map[string][]assets.Asset{
				"src/pages/index.orbit": {
					{Kind: "css", URL: "/_orbit/index-AAAA.css"},
					{Kind: "js", URL: "/_orbit/index-BBBB.js"},
					{Kind: "lazy", URL: "/_orbit/Comments-CCCC.js", ID: "Comments-1"},
					{Kind: "lazy", URL: "/_orbit/Comments-CCCC.js", ID: "Outer-2"},
					{Kind: "modulepreload", URL: "/_orbit/chunk-DDDD.js"},
				},
				"src/pages/about.orbit": {
					{Kind: "js", URL: "/_orbit/about-EEEE.js"},
					{Kind: "modulepreload", URL: "/_orbit/chunk-DDDD.js"},
				},
			},
			Files: []assets.File{
				file("Comments-CCCC.js", 300),
				file("about-EEEE.js", 420),
				file("chunk-DDDD.js", 310),
				file("chunk-DDDD.js.map", 9999),
				file("font-FFFF.woff2", 5000),
				file("index-AAAA.css", 1300),
				file("index-BBBB.js", 420),
				file("quickSearch-GGGG.js", 48100),
			},
		},
	}
	var b bytes.Buffer
	printBundles(&b, p)
	const want = `src/pages/about.orbit
  /_orbit/about-EEEE.js           0.42 kB  gzip    0.03 kB
    /_orbit/chunk-DDDD.js         0.31 kB  gzip    0.03 kB  (preload)
src/pages/index.orbit
  /_orbit/index-AAAA.css          1.30 kB  gzip    0.03 kB
  /_orbit/index-BBBB.js           0.42 kB  gzip    0.03 kB
    /_orbit/chunk-DDDD.js         0.31 kB  gzip    0.03 kB  (preload)
  /_orbit/Comments-CCCC.js        0.30 kB  gzip    0.03 kB  (lazy)
src/pages/plain.orbit: no CSS or JS
loaded with import()
  /_orbit/quickSearch-GGGG.js    48.10 kB  gzip    0.08 kB
total CSS                         1.30 kB  gzip    0.03 kB
total JS                         49.55 kB  gzip    0.18 kB
`
	if got := b.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
