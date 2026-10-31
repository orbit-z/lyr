package dev

import (
	"slices"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		rel   string
		isDir bool
		want  Kind
	}{
		// PLAN.md §7.1: public/ files rebuild nothing.
		{"public/robots.txt", false, KindPublic},
		{"public/img/logo.svg", false, KindPublic},
		{"public/img", true, KindPublic},
		{"public", true, KindPublic},
		// .lyr files: the pipeline decides between esbuild and Go.
		{"src/components/Card.lyr", false, KindLyr},
		{"src/pages/_partials/Note.lyr", false, KindLyr},
		{"src/pages/about.md", false, KindLyr},
		{"src/content/post.markdown", false, KindLyr},
		{"README.md", false, KindIgnore},
		{"src/pages/blog", true, KindLyr},
		{"src/pages/blog", false, KindLyr}, // removed: maybe a directory
		{"other/Card.lyr", false, KindIgnore},
		// Files the bundles read.
		{"src/scripts/util.ts", false, KindAsset},
		{"src/styles/base.css", false, KindAsset},
		{"src/img/logo.png", false, KindAsset},
		{"tsconfig.json", false, KindAsset},
		{"tsconfig.base.json", false, KindAsset}, // a base tsconfig.json may extend
		{"config/tsconfig.json", false, KindIgnore},
		{"package.json", false, KindAsset},
		// Go: user packages, src/locals, go.mod.
		{"internal/posts/posts.go", false, KindGo},
		{"src/locals/locals.go", false, KindGo},
		{"cmd/server/main.go", false, KindGo},
		{"main.go", false, KindGo},
		{"go.mod", false, KindGo},
		{"go.sum", false, KindGo},
		{"internal/posts/posts_test.go", false, KindIgnore},
		{"lyr.json", false, KindConfig},
		{"src/lyr.json", false, KindAsset},
		// Excluded directories.
		{"lyr_gen/app/app.go", false, KindIgnore},
		{"lyr_gen", true, KindIgnore},
		{"dist/server", false, KindIgnore},
		{".lyr/dev/manifest.json", false, KindIgnore},
		{"node_modules/x/index.ts", false, KindIgnore},
		{"src/node_modules/x/index.ts", false, KindIgnore},
		{".git/HEAD", false, KindIgnore},
		{"internal/testdata/x.go", false, KindIgnore},
		{"_tools/gen.go", false, KindIgnore},
		{"internal/newpkg", true, KindIgnore},
		// Editors' temporary files, other files.
		{"src/components/.Card.lyr.swp", false, KindIgnore},
		{"src/components/Card.lyr~", false, KindIgnore},
		{"src/components/4913", false, KindIgnore},
		{"src/components/#Card.lyr#", false, KindIgnore},
		{"README.md", false, KindIgnore},
		{"Dockerfile", false, KindIgnore},
	}
	for _, tt := range tests {
		if got := Classify(tt.rel, tt.isDir); got != tt.want {
			t.Errorf("Classify(%q, dir=%v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
		}
	}
}

// TestClassifyWatch checks the lyr.json "watch" patterns: a matching path
// that's no other kind is KindWatch; other kinds win; Lyr's own output
// and editors' temporary files never are.
func TestClassifyWatch(t *testing.T) {
	ws := watchSet{"../*.md", "content/**", "testdata/*.json", "dist/**", "src/**"}
	tests := []struct {
		rel   string
		isDir bool
		want  Kind
	}{
		{"../README.md", false, KindWatch},
		{"../reference/cli.md", false, KindIgnore},
		{"../x.txt", false, KindIgnore},
		{"content/a/b.md", false, KindWatch},
		{"content/a", true, KindWatch},
		{"content/.b.md.swp", false, KindIgnore},
		{"testdata/x.json", false, KindWatch}, // a directory the watcher skips otherwise
		{"dist/x", false, KindIgnore},
		{"src/a.ts", false, KindAsset},
		{"src/pages/a.lyr", false, KindLyr},
		{"README.md", false, KindIgnore},
		{"go.mod", false, KindGo},
	}
	for _, tt := range tests {
		if got := ws.classify(tt.rel, tt.isDir); got != tt.want {
			t.Errorf("classify(%q, dir=%v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
		}
	}
	c := newChange([]string{"../README.md", "public/x"}, nil, ws.classify)
	if !c.Only(KindWatch, KindPublic) || !c.Has(KindWatch) {
		t.Errorf("watch change: %+v", c)
	}
}

func TestChange(t *testing.T) {
	c := NewChange([]string{"internal/a/a.go", "README.md", "go.mod", "internal/a/a.go"}, nil)
	if want := []string{"go.mod", "internal/a/a.go"}; !slices.Equal(c.Paths, want) {
		t.Errorf("Paths = %v, want %v", c.Paths, want)
	}
	if !c.Only(KindGo) || c.Has(KindLyr) || c.Empty() {
		t.Errorf("Go-only change: Only(Go)=%v Has(Lyr)=%v Empty=%v", c.Only(KindGo), c.Has(KindLyr), c.Empty())
	}
	c = NewChange([]string{"src/a.ts", "public/x.txt"}, nil)
	if !c.Only(KindAsset, KindPublic) || c.Only(KindAsset) {
		t.Errorf("asset+public change: %+v", c)
	}
	if c := NewChange([]string{"README.md"}, nil); !c.Empty() || c.Only(KindGo) {
		t.Errorf("ignored-only change: %+v", c)
	}
	// A tsconfig change also re-resolves import aliases: the front runs.
	if c := NewChange([]string{"tsconfig.base.json", "src/a.ts"}, nil); !c.touchesTsconfig() {
		t.Errorf("tsconfig change: %+v", c)
	}
	if c := NewChange([]string{"src/a.ts", "package.json"}, nil); c.touchesTsconfig() {
		t.Errorf("non-tsconfig change: %+v", c)
	}
}
