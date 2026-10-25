package dev

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Debounce is how long the watcher waits after the last change before it
// hands a batch to the pipeline (PLAN.md §7).
const Debounce = 50 * time.Millisecond

// event is one relevant file-system change, relative to the module root
// and slash-separated.
type event struct {
	rel   string
	isDir bool
	// created is set for a Create event: a path created and removed again
	// within one batch (an editor's or sed -i's temporary file) is left
	// out of it.
	created bool
}

// watcher watches the module root (PLAN.md §7): src/, public/ and every
// .go file in the module, excluding the directories skipDir rejects
// (orbit_gen/, dist/, .orbit/, node_modules/). fsnotify isn't recursive, so
// it adds every directory, and the new ones as they're created.
type watcher struct {
	root string
	fw   *fsnotify.Watcher
	errf func(error) // reports watch errors
}

func newWatcher(root string, errf func(error)) (*watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &watcher{root: root, fw: fw, errf: errf}
	if err := w.addTree(root, nil); err != nil {
		fw.Close()
		return nil, err
	}
	return w, nil
}

func (w *watcher) close() error { return w.fw.Close() }

// rel returns name relative to the root, slash-separated.
func (w *watcher) rel(name string) (string, bool) {
	r, err := filepath.Rel(w.root, name)
	if err != nil || r == ".." || len(r) > 2 && r[:3] == ".."+string(filepath.Separator) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// addTree watches dir and every directory under it that isn't skipped.
// When found is non-nil, it is called with every file and directory found
// below dir (they may have been created before the watch was added).
func (w *watcher) addTree(dir string, found func(event)) error {
	return filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if name == dir {
				return err
			}
			return nil // vanished meanwhile
		}
		rel, ok := w.rel(name)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if rel != "." && skipDir(rel) {
				return filepath.SkipDir
			}
			if err := w.fw.Add(name); err != nil {
				w.errf(err)
			}
		}
		if found != nil && name != dir {
			found(event{rel: rel, isDir: d.IsDir()})
		}
		return nil
	})
}

// run forwards relevant events to out until ctx is done.
func (w *watcher) run(ctx context.Context, out chan<- event) {
	send := func(e event) {
		if Classify(e.rel, e.isDir) == KindIgnore {
			return
		}
		select {
		case out <- e:
		case <-ctx.Done():
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			w.errf(err)
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue
			}
			rel, ok := w.rel(ev.Name)
			if !ok || rel == "." {
				continue
			}
			isDir := false
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					isDir = true
					if skipDir(rel) {
						continue
					}
					if err := w.addTree(ev.Name, send); err != nil {
						w.errf(err)
					}
				}
			}
			send(event{rel: rel, isDir: isDir, created: ev.Has(fsnotify.Create)})
		}
	}
}

// debounce batches events: a batch is ready Debounce after its last event
// (each event restarts the wait) and is sent on out, as the changed paths
// and the set of those that are directories. Events that arrive while out
// isn't ready to receive (the pipeline is busy) join the next batch. busy is
// called on every event, so requests can start waiting for the rebuild
// before the batch is even ready. exists reports whether a path exists
// when the batch is made: a path created during the batch that no longer
// exists is dropped.
func debounce(ctx context.Context, in <-chan event, out chan<- Change, d time.Duration, busy func(), exists func(rel string) bool) {
	var (
		pending []event
		timer   *time.Timer
		timerC  <-chan time.Time
		ready   []event
		outC    chan<- Change
		batch   Change
	)
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case e := <-in:
			if busy != nil {
				busy()
			}
			pending = append(pending, e)
			if timer == nil {
				timer = time.NewTimer(d)
			} else {
				timer.Stop()
				timer.Reset(d)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			ready = append(ready, pending...)
			pending = nil
			var paths []string
			dirs, created := map[string]bool{}, map[string]bool{}
			for _, e := range ready {
				created[e.rel] = created[e.rel] || e.created
			}
			for _, e := range ready {
				if created[e.rel] && !exists(e.rel) {
					continue // a temporary file
				}
				paths = append(paths, e.rel)
				if e.isDir {
					dirs[e.rel] = true
				}
			}
			batch = NewChange(paths, dirs)
			outC = out
		case outC <- batch:
			ready, outC = nil, nil
		}
	}
}
