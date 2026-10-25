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
		go debounce(ctx, in, out, Debounce, func() { busy++ }, func(rel string) bool { return !gone[rel] })

		start := time.Now()
		in <- event{rel: "src/components/Card.orbit"}
		time.Sleep(30 * time.Millisecond)
		in <- event{rel: "src/pages/sedAb12", created: true}
		in <- event{rel: "src/pages/index.orbit", created: true}
		time.Sleep(30 * time.Millisecond)
		in <- event{rel: "src/components/Card.orbit"}
		synctest.Wait()
		if busy != 4 {
			t.Errorf("busy called %d times, want 4 (once per event)", busy)
		}

		c := <-out
		if got, want := time.Since(start), 110*time.Millisecond; got != want {
			t.Errorf("batch after %v, want %v (Debounce after the last event)", got, want)
		}
		if want := []string{"src/components/Card.orbit", "src/pages/index.orbit"}; !slices.Equal(c.Paths, want) {
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
// src/, public/ and to .go files arrive; orbit_gen/, dist/ and .orbit/ are
// ignored; a new directory is watched, and the files already in it reported.
func TestWatcher(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src/components", "public", "orbit_gen/app", ".orbit/dev", "dist", "internal/posts"} {
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

	write("orbit_gen/app/app.go")
	write(".orbit/dev/manifest.json")
	write("dist/x")
	write("src/components/Card.orbit")
	if got := next(); got != "src/components/Card.orbit" {
		t.Fatalf("first event %q, want src/components/Card.orbit (the excluded directories have none)", got)
	}
	write("public/robots.txt")
	expect("public/robots.txt")
	write("internal/posts/posts.go")
	expect("internal/posts/posts.go")

	// A new directory, then a file in it.
	write("src/pages/blog/post.orbit")
	expect("src/pages/blog/post.orbit")
	write("src/pages/blog/other.orbit")
	expect("src/pages/blog/other.orbit")
}
