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
		// .orbit files: the pipeline decides between esbuild and Go.
		{"src/components/Card.orbit", false, KindOrbit},
		{"src/pages/_partials/Note.orbit", false, KindOrbit},
		{"src/pages/blog", true, KindOrbit},
		{"src/pages/blog", false, KindOrbit}, // removed: maybe a directory
		{"other/Card.orbit", false, KindIgnore},
		// Files the bundles read.
		{"src/scripts/util.ts", false, KindAsset},
		{"src/styles/base.css", false, KindAsset},
		{"src/img/logo.png", false, KindAsset},
		{"tsconfig.json", false, KindAsset},
		{"package.json", false, KindAsset},
		// Go: user packages, src/locals, go.mod.
		{"internal/posts/posts.go", false, KindGo},
		{"src/locals/locals.go", false, KindGo},
		{"cmd/server/main.go", false, KindGo},
		{"main.go", false, KindGo},
		{"go.mod", false, KindGo},
		{"go.sum", false, KindGo},
		{"internal/posts/posts_test.go", false, KindIgnore},
		{"orbit.json", false, KindConfig},
		{"src/orbit.json", false, KindAsset},
		// Excluded directories.
		{"orbit_gen/app/app.go", false, KindIgnore},
		{"orbit_gen", true, KindIgnore},
		{"dist/server", false, KindIgnore},
		{".orbit/dev/manifest.json", false, KindIgnore},
		{"node_modules/x/index.ts", false, KindIgnore},
		{"src/node_modules/x/index.ts", false, KindIgnore},
		{".git/HEAD", false, KindIgnore},
		{"internal/testdata/x.go", false, KindIgnore},
		{"_tools/gen.go", false, KindIgnore},
		{"internal/newpkg", true, KindIgnore},
		// Editors' temporary files, other files.
		{"src/components/.Card.orbit.swp", false, KindIgnore},
		{"src/components/Card.orbit~", false, KindIgnore},
		{"src/components/4913", false, KindIgnore},
		{"src/components/#Card.orbit#", false, KindIgnore},
		{"README.md", false, KindIgnore},
		{"Dockerfile", false, KindIgnore},
	}
	for _, tt := range tests {
		if got := Classify(tt.rel, tt.isDir); got != tt.want {
			t.Errorf("Classify(%q, dir=%v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
		}
	}
}

func TestChange(t *testing.T) {
	c := NewChange([]string{"internal/a/a.go", "README.md", "go.mod", "internal/a/a.go"}, nil)
	if want := []string{"go.mod", "internal/a/a.go"}; !slices.Equal(c.Paths, want) {
		t.Errorf("Paths = %v, want %v", c.Paths, want)
	}
	if !c.Only(KindGo) || c.Has(KindOrbit) || c.Empty() {
		t.Errorf("Go-only change: Only(Go)=%v Has(Orbit)=%v Empty=%v", c.Only(KindGo), c.Has(KindOrbit), c.Empty())
	}
	c = NewChange([]string{"src/a.ts", "public/x.txt"}, nil)
	if !c.Only(KindAsset, KindPublic) || c.Only(KindAsset) {
		t.Errorf("asset+public change: %+v", c)
	}
	if c := NewChange([]string{"README.md"}, nil); !c.Empty() || c.Only(KindGo) {
		t.Errorf("ignored-only change: %+v", c)
	}
}
