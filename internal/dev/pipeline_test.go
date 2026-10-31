package dev

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

// testPipeline is a pipeline on dir whose go build is fake: build is
// called instead, and the stages are recorded.
type testPipeline struct {
	*pipeline
	mu     sync.Mutex
	stages []string
	wrote  chan struct{} // closed at the first "write" after reset
}

func newTestPipeline(t *testing.T, dir string) *testPipeline {
	tp := &testPipeline{pipeline: newPipeline(dir, testEnv, "")}
	t.Cleanup(tp.close)
	tp.trace = func(stage string) {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		tp.stages = append(tp.stages, stage)
		if stage == "write" && tp.wrote != nil {
			close(tp.wrote)
			tp.wrote = nil
		}
	}
	tp.goBuild = func(ctx context.Context, out, entry string) ([]byte, error) { return nil, nil }
	return tp
}

// reset clears the recorded stages.
func (tp *testPipeline) reset() {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.stages = nil
	tp.wrote = make(chan struct{})
}

func (tp *testPipeline) got() []string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return slices.Clone(tp.stages)
}

func (tp *testPipeline) run(t *testing.T, paths ...string) outcome {
	t.Helper()
	out, err := tp.pipeline.run(t.Context(), NewChange(paths, nil), true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Failure != nil {
		t.Fatalf("failure: %s", out.Failure.Text)
	}
	return out
}

// TestPipelineStages checks what each kind of edit runs (PLAN.md §7.1 and
// the milestone 10 speed-ups): a .go-only edit starts go build before
// regenerating and keeps that build when regenerating writes nothing, and
// rebuilds after when it does; esbuild is skipped when no style or script
// changed; a CSS-only edit runs no Go stage.
func TestPipelineStages(t *testing.T) {
	needE2E(t)
	dir := fixture(t)
	tp := newTestPipeline(t, dir)
	if out := tp.run(t); out.Bin == "" || !out.Assets {
		t.Fatalf("first run: %+v", out)
	}

	// .go edit, export data unchanged: the early build overlaps front,
	// check and write (it only returns once "write" has started), and is
	// the build used.
	tp.reset()
	wrote := tp.wrote
	tp.goBuild = func(ctx context.Context, out, entry string) ([]byte, error) {
		select {
		case <-wrote:
			return nil, nil
		case <-time.After(30 * time.Second):
			t.Error("go build didn't run in parallel with regenerating")
			return nil, nil
		}
	}
	edit(t, dir, "internal/msg/msg.go", `"hello"`, `"bonjour"`)
	out := tp.run(t, "internal/msg/msg.go")
	st := tp.got()
	if !out.Timings.EarlyBuild || !out.Timings.EarlyUsed || out.Bin == "" {
		t.Errorf(".go edit: %+v", out.Timings)
	}
	if len(st) == 0 || st[0] != "gobuild-early" || slices.Contains(st, "gobuild") || slices.Contains(st, "esbuild") {
		t.Errorf(".go edit stages %v: want gobuild-early first, no other go build, no esbuild", st)
	}
	if i, j := slices.Index(st, "write"), slices.Index(st, "gobuild-early-done"); i < 0 || j < i {
		t.Errorf(".go edit stages %v: the early build should finish after write started", st)
	}

	// .go edit that changes the generated code (Greeting now returns an
	// int, so Card's typed write changes): go build runs again after
	// writing.
	tp.reset()
	tp.goBuild = func(ctx context.Context, out, entry string) ([]byte, error) { return nil, nil }
	edit(t, dir, "internal/msg/msg.go", `func Greeting() string { return "bonjour" }`, `func Greeting() int { return 42 }`)
	out = tp.run(t, "internal/msg/msg.go")
	st = tp.got()
	if !out.Timings.EarlyBuild || out.Timings.EarlyUsed || out.Timings.Written == 0 {
		t.Errorf(".go API edit: %+v", out.Timings)
	}
	if i, j := slices.Index(st, "write"), slices.Index(st, "gobuild"); i < 0 || j < i {
		t.Errorf(".go API edit stages %v: want go build after write", st)
	}

	// Template edit: Go stages, no esbuild.
	tp.reset()
	edit(t, dir, "src/components/Card.lyr", "card {props.Post}", "Card {props.Post}")
	out = tp.run(t, "src/components/Card.lyr")
	if st := tp.got(); slices.Contains(st, "esbuild") || !slices.Contains(st, "gobuild") || out.Assets || out.Bin == "" {
		t.Errorf("template edit: stages %v, %+v", st, out)
	}

	// CSS edit: esbuild only.
	tp.reset()
	edit(t, dir, "src/layouts/Base.lyr", "color: navy;", "color: teal;")
	out = tp.run(t, "src/layouts/Base.lyr")
	if st := tp.got(); !slices.Equal(st, []string{"front", "esbuild"}) || !out.Assets || out.Bin != "" {
		t.Errorf("CSS edit: stages %v, %+v", st, out)
	}

	// A .ts file: esbuild from the last input, without even loading the
	// project.
	tp.reset()
	writeFile(t, dir, "src/scripts/x.ts", "export const x = 1;\n")
	out = tp.run(t, "src/scripts/x.ts")
	if st := tp.got(); !slices.Equal(st, []string{"esbuild"}) || out.Bin != "" {
		t.Errorf(".ts edit: stages %v, %+v", st, out)
	}

	// public/: nothing.
	tp.reset()
	out = tp.run(t, "public/robots.txt")
	if st := tp.got(); len(st) != 0 || out.Bin != "" || out.Assets {
		t.Errorf("public/ edit: stages %v, %+v", st, out)
	}

	// The dev manifest is there, and the compiled one stays empty.
	if _, err := os.Stat(dir + "/.lyr/dev/manifest.json"); err != nil {
		t.Error(err)
	}
}

func TestDevBuildFlags(t *testing.T) {
	for _, tc := range []struct {
		goflags string
		want    []string
	}{
		{"", []string{devGCFlags[0], devGCFlags[1], devVCSFlag}},
		{"-mod=mod -tags=prod", []string{devGCFlags[0], devGCFlags[1], devVCSFlag}},
		{"-gcflags=all=-N", []string{devVCSFlag}},
		{"-mod=mod --gcflags=-m", []string{devVCSFlag}},
		{"-buildvcs=true", devGCFlags},
		{"--buildvcs", devGCFlags},
		{"-buildvcs=auto -gcflags=-m", nil},
		{"-gcflagsx=1 -buildvcsx", []string{devGCFlags[0], devGCFlags[1], devVCSFlag}},
	} {
		if got := devBuildFlags(tc.goflags); !slices.Equal(got, tc.want) {
			t.Errorf("devBuildFlags(%q) = %q, want %q", tc.goflags, got, tc.want)
		}
	}
}

func TestDevLDFlags(t *testing.T) {
	for _, tc := range []struct{ goflags, want string }{
		{"", "-ldflags=-s -w"},
		{"-mod=mod", "-ldflags=-s -w"},
		{"-ldflags=-X=main.v=1", "-ldflags=-X=main.v=1 -s -w"},
		{`"-ldflags=-X main.v=1" -mod=mod`, "-ldflags=-X main.v=1 -s -w"},
	} {
		pl := newPipeline(t.TempDir(), nil, tc.goflags)
		pl.close()
		if got := pl.ldflags; got != tc.want {
			t.Errorf("ldflags with GOFLAGS=%q = %q, want %q", tc.goflags, got, tc.want)
		}
	}
}

func TestGoBuildEnv(t *testing.T) {
	for _, tc := range []struct{ environ, extra, want []string }{
		{nil, nil, []string{}},
		{
			[]string{"HOME=/h", "GOGC=100", "GOFLAGS=-mod=mod", "GOMEMLIMIT=1GiB", "GOGCX=1"},
			[]string{"LYR_DEV=1", "GOGC=off"},
			[]string{"HOME=/h", "GOFLAGS=-mod=mod", "GOGCX=1", "LYR_DEV=1"},
		},
	} {
		if got := goBuildEnv(tc.environ, tc.extra); !slices.Equal(got, tc.want) {
			t.Errorf("goBuildEnv(%q, %q) = %q, want %q", tc.environ, tc.extra, got, tc.want)
		}
	}
}
