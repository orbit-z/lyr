package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/project"
)

// TestPrintBundles is the lyr build --verbose golden (PLAN.md §5e): a
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
		Files: []*project.File{page("src/pages/about.lyr"), page("src/pages/index.lyr"), page("src/pages/plain.lyr"),
			{Path: "src/components/Comments.lyr", Info: compiler.FileInfo{Role: compiler.RoleComponent}}},
		Assets: &assets.Result{
			Pages: map[string][]assets.Asset{
				"src/pages/index.lyr": {
					{Kind: "css", URL: "/_lyr/index-AAAA.css"},
					{Kind: "js", URL: "/_lyr/index-BBBB.js"},
					{Kind: "lazy", URL: "/_lyr/Comments-CCCC.js", ID: "Comments-1"},
					{Kind: "lazy", URL: "/_lyr/Comments-CCCC.js", ID: "Outer-2"},
					{Kind: "modulepreload", URL: "/_lyr/chunk-DDDD.js"},
				},
				"src/pages/about.lyr": {
					{Kind: "js", URL: "/_lyr/about-EEEE.js"},
					{Kind: "modulepreload", URL: "/_lyr/chunk-DDDD.js"},
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
	const want = `src/pages/about.lyr
  /_lyr/about-EEEE.js           0.42 kB  gzip    0.03 kB
    /_lyr/chunk-DDDD.js         0.31 kB  gzip    0.03 kB  (preload)
src/pages/index.lyr
  /_lyr/index-AAAA.css          1.30 kB  gzip    0.03 kB
  /_lyr/index-BBBB.js           0.42 kB  gzip    0.03 kB
    /_lyr/chunk-DDDD.js         0.31 kB  gzip    0.03 kB  (preload)
  /_lyr/Comments-CCCC.js        0.30 kB  gzip    0.03 kB  (lazy)
src/pages/plain.lyr: no CSS or JS
loaded with import()
  /_lyr/quickSearch-GGGG.js    48.10 kB  gzip    0.08 kB
total CSS                       1.30 kB  gzip    0.03 kB
total JS                       49.55 kB  gzip    0.18 kB
`
	if got := b.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
