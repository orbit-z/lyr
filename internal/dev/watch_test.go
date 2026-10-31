package dev

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// TestDebounce checks the 50 ms trailing debounce with a fake clock: a
// burst becomes one batch, sent Debounce after its last event; events that
// arrive while the pipeline is busy join the next batch; a temporary file
// (created and gone) is dropped.
func TestDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		in := make(chan event)
		out := make(chan Change)
		busy := 0
		gone := map[string]bool{"src/pages/sedAb12": true}
		go debounce(ctx, in, out, Debounce, func() { busy++ }, func(rel string) bool { return !gone[rel] }, Classify)

		start := time.Now()
		in <- event{rel: "src/components/Card.lyr"}
		time.Sleep(30 * time.Millisecond)
		in <- event{rel: "src/pages/sedAb12", created: true}
		in <- event{rel: "src/pages/index.lyr", created: true}
		time.Sleep(30 * time.Millisecond)
		in <- event{rel: "src/components/Card.lyr"}
		synctest.Wait()
		if busy != 4 {
			t.Errorf("busy called %d times, want 4 (once per event)", busy)
		}

		c := <-out
		if got, want := time.Since(start), 110*time.Millisecond; got != want {
			t.Errorf("batch after %v, want %v (Debounce after the last event)", got, want)
		}
		if want := []string{"src/components/Card.lyr", "src/pages/index.lyr"}; !slices.Equal(c.Paths, want) {
			t.Errorf("batch %v, want %v", c.Paths, want)
		}

		// The pipeline is busy: two bursts pile up into one batch.
		in <- event{rel: "src/a.ts"}
		time.Sleep(100 * time.Millisecond)
		in <- event{rel: "go.mod"}
		time.Sleep(100 * time.Millisecond)
		c = <-out
		if want := []string{"go.mod", "src/a.ts"}; !slices.Equal(c.Paths, want) {
			t.Errorf("batch %v, want %v", c.Paths, want)
		}

		// Nothing more.
		synctest.Wait()
		select {
		case c := <-out:
			t.Errorf("unexpected batch %v", c.Paths)
		default:
		}
	})
}

// TestWatcher runs the fsnotify watcher on a temporary tree: changes under
// src/, public/ and to .go files arrive; lyr_gen/, dist/ and .lyr/ are
// ignored; a new directory is watched, and the files already in it reported.
func TestWatcher(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src/components", "public", "lyr_gen/app", ".lyr/dev", "dist", "internal/posts"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newWatcher(root, func(err error) { t.Error(err) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := make(chan event, 100)
	go w.run(ctx, events)

	write := func(rel string) {
		t.Helper()
		name := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// next returns the next event's path, or "" after a while.
	next := func() string {
		select {
		case e := <-events:
			return e.rel
		case <-time.After(2 * time.Second):
			return ""
		}
	}
	expect := func(want string) {
		t.Helper()
		for {
			got := next()
			if got == want {
				return
			}
			if got == "" {
				t.Fatalf("no event for %s", want)
			}
		}
	}

	write("lyr_gen/app/app.go")
	write(".lyr/dev/manifest.json")
	write("dist/x")
	write("src/components/Card.lyr")
	if got := next(); got != "src/components/Card.lyr" {
		t.Fatalf("first event %q, want src/components/Card.lyr (the excluded directories have none)", got)
	}
	write("public/robots.txt")
	expect("public/robots.txt")
	write("internal/posts/posts.go")
	expect("internal/posts/posts.go")

	// A new directory, then a file in it.
	write("src/pages/blog/post.lyr")
	expect("src/pages/blog/post.lyr")
	write("src/pages/blog/other.lyr")
	expect("src/pages/blog/other.lyr")
}

// TestWatcherPatterns checks the lyr.json "watch" patterns: their
// directories are watched, outside the module too; a missing directory is
// a warning; files that no pattern matches have no events.
func TestWatcherPatterns(t *testing.T) {
	top := t.TempDir()
	root := filepath.Join(top, "site")
	for _, d := range []string{"site/src", "site/content/posts", "reference", "other"} {
		if err := os.MkdirAll(filepath.Join(top, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newWatcher(root, func(err error) { t.Error(err) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	var warnings []string
	w.setWatch([]string{"../*.md", "../reference/*.md", "content/**", "missing/*.md"}, func(s string) { warnings = append(warnings, s) })
	if want := []string{`lyr.json: watch "missing/*.md": directory missing doesn't exist; not watched`}; !slices.Equal(warnings, want) {
		t.Errorf("warnings %q, want %q", warnings, want)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := make(chan event, 100)
	go w.run(ctx, events)

	write := func(rel string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(top, filepath.FromSlash(rel)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	next := func() string {
		select {
		case e := <-events:
			return e.rel
		case <-time.After(2 * time.Second):
			return ""
		}
	}
	write("other.txt")      // in a watched directory, matches no pattern
	write("other/x.md")     // not watched
	write("site/README.md") // in the module, matches no pattern
	// expect wants the next new path to be want (a write can have several
	// events: Create, then Write).
	last := ""
	expect := func(want string) {
		t.Helper()
		for {
			got := next()
			if got == want {
				last = got
				return
			}
			if got == "" || got != last {
				t.Fatalf("event %q, want %q", got, want)
			}
		}
	}
	write("guide.md")
	expect("../guide.md")
	write("reference/cli.md")
	expect("../reference/cli.md")
	write("site/content/posts/a.md")
	expect("content/posts/a.md")
}
