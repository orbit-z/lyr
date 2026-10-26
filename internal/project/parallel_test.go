package project

import (
	"bytes"
	"context"
	"maps"
	"runtime"
	"testing"
)

// TestGenerateDeterministic: the parallel stages (reading sources, parse
// and pass 1, the typed pass and pass 2, writing) give the same files as
// the sequential path that GOMAXPROCS=1 takes, run after run.
func TestGenerateDeterministic(t *testing.T) {
	needGo(t)
	files := maps.Clone(siteFiles)
	maps.Copy(files, mdFiles)
	delete(files, "src/pages/404.md")                                   // siteFiles has a 404.orbit
	files["src/pages/index.orbit"] = siteFiles["src/pages/index.orbit"] // both have one
	p := newTestProject(t, files)
	plan := func(procs int) []GenFile {
		t.Helper()
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
		g := &Generator{Root: p.Dir, Env: []string{"GOFLAGS=-mod=mod", "GOWORK=off"}}
		res, out, err := g.Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if out == nil {
			t.Fatal(joinLines(diagStrings(res.Diagnostics)))
		}
		return out
	}
	want := plan(1)
	for range 3 {
		got := plan(8)
		if len(got) != len(want) {
			t.Fatalf("%d files with GOMAXPROCS=8, %d with 1", len(got), len(want))
		}
		for i := range got {
			if got[i].Path != want[i].Path || !bytes.Equal(got[i].Content, want[i].Content) {
				t.Fatalf("GOMAXPROCS=8: %s differs from the sequential %s", got[i].Path, want[i].Path)
			}
		}
	}
}
