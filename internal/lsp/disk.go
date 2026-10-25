package lsp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/project"
)

// The typed check (PLAN.md §9 Startup step 2): the workspace generates
// every file in tolerant mode, with Expr/Attr fallbacks, while orbit
// generate writes typed writes, so a byte comparison with orbit_gen/ never
// matches an impl file. The typed check compiles the current sources the
// way orbit generate does (strict mode, one go list -export and the go/types
// pass, internal/project's pipeline) and, for every generated file whose
// typed output is what's on disk, lets gopls read the disk instead of an
// overlay, mapping it with the typed output's source map. It runs at
// startup and again after orbit_gen/ changes on disk (orbit dev or orbit
// generate rewrote it), so overlays don't outlive the edits that needed
// them. It is only ever used to match the disk: the tolerant code of an
// edited file is never type-checked.

// diskCheckWait is how long startup waits for the typed check before
// sending tolerant overlays for everything; a check that finishes later
// still closes the overlays it can.
const diskCheckWait = 5 * time.Second

// diskCheckDelay debounces the typed checks after orbit_gen/ changes:
// orbit generate rewrites many files at once.
const diskCheckDelay = 300 * time.Millisecond

// diskCheck is one typed check: a strict copy of the project's current
// sources, and the tolerant pass-1 output each file had when it was taken.
type diskCheck struct {
	proj  *project.Project
	pass1 map[string]*compiler.Output // by .orbit path
	// typed are the typed generated files found on disk, by
	// module-relative path (set by run).
	typed map[string]*compiler.GenFile
	files int // generated files compared
	took  time.Duration
}

// newDiskCheck snapshots ws for a typed check (with s.mu held).
func (ws *workspace) newDiskCheck() *diskCheck {
	c := &diskCheck{proj: ws.proj.Clone(compiler.Strict), pass1: make(map[string]*compiler.Output, len(ws.proj.Files))}
	for _, f := range ws.proj.Files {
		c.pass1[f.Path] = f.Pass1
	}
	return c
}

// run compiles the snapshot with typed writes and keeps the generated
// files that equal the disk. It doesn't touch the workspace (no lock).
func (c *diskCheck) run(ctx context.Context) error {
	start := time.Now()
	defer func() {
		c.took = time.Since(start)
		c.proj = nil // garbage now, with the go/types data (freeMemory)
	}()
	p := c.proj
	p.Front()
	checker := compiler.NewChecker(&compiler.GoListLoader{Dir: p.Root})
	if _, err := p.Typecheck(ctx, checker); err != nil {
		return err
	}
	c.typed = map[string]*compiler.GenFile{}
	for _, f := range p.Files {
		if f.Output == nil {
			continue
		}
		for _, out := range []*compiler.GenFile{f.Output.Decl, f.Output.Impl} {
			if out == nil {
				continue
			}
			c.files++
			b, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(out.Path)))
			if err == nil && bytes.Equal(b, out.Content) {
				c.typed[out.Path] = out
			}
		}
	}
	return nil
}

// adopt applies a finished check (with s.mu held): every generated file
// of a file that wasn't regenerated since the snapshot, whose typed output
// is on disk, is read by gopls from disk (its overlay, if any, is closed)
// and mapped through the typed source map. It returns how many files
// changed over.
func (ws *workspace) adopt(c *diskCheck) int {
	n := 0
	for _, f := range ws.proj.Files {
		if f.Pass1 == nil || c.pass1[f.Path] != f.Pass1 {
			continue // new, or regenerated since the snapshot
		}
		for _, out := range []*compiler.GenFile{f.Pass1.Decl, f.Pass1.Impl} {
			t, g := c.typed[out.Path], ws.gen[out.Path]
			if t == nil || g == nil || g.tol != nil {
				continue
			}
			if !g.open && bytes.Equal(g.content, t.Content) {
				continue // the tolerant output is on disk already (a decl file)
			}
			g.tol, g.tolMap = g.content, g.smap
			g.content, g.lines, g.smap = t.Content, newLineIndex(string(t.Content)), t.Map
			ws.rev++
			delete(ws.goplsDiags, g.rel) // they were about the overlay
			if g.open && ws.gopls != nil {
				ws.gopls.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": g.uri}})
			}
			g.open = false
			n++
		}
	}
	return n
}

// startupCheck is the typed check at startup, run while gopls starts. It
// marks the workspace mapped once its result is applied, or after
// diskCheckWait; a later result is still applied, closing the overlays
// sent meanwhile.
func (s *Server) startupCheck(ws *workspace) {
	if ws.genMissing {
		s.markMapped()
		return
	}
	s.mu.Lock()
	c := ws.newDiskCheck()
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		done <- c.run(ctx)
	}()
	late := false
	var err error
	select {
	case err = <-done:
	case <-time.After(diskCheckWait):
		s.markMapped()
		late = true
		err = <-done
	}
	s.mu.Lock()
	n := 0
	if err == nil && s.ws == ws {
		n = ws.adopt(c)
		if late && n > 0 {
			s.publishAll(ws)
		}
	}
	s.mu.Unlock()
	s.markMapped()
	freeMemory()
	switch {
	case err != nil:
		s.logf("typed check: %v; every generated file is an overlay", err)
	case late:
		s.logf("typed check (late): %d/%d generated files on disk, %d overlays closed (%v)", len(c.typed), c.files, n, c.took.Round(time.Millisecond))
	default:
		s.logf("typed check: %d/%d generated files on disk (%v)", len(c.typed), c.files, c.took.Round(time.Millisecond))
	}
}

// useTolerant switches g from the typed content on disk back to its
// tolerant output; the caller sends it to gopls.
func (ws *workspace) useTolerant(g *genFile) {
	if g.tol == nil {
		return
	}
	g.content, g.lines, g.smap = g.tol, newLineIndex(string(g.tol)), g.tolMap
	g.tol, g.tolMap = nil, nil
	ws.rev++
	delete(ws.goplsDiags, g.rel)
}

// hasOverlays reports whether gopls has an overlay of a generated file
// that a typed check could close.
func (ws *workspace) hasOverlays() bool {
	for _, g := range ws.gen {
		if g.open {
			return true
		}
	}
	return false
}

// scheduleDiskCheck runs a typed check soon (debounced), after orbit_gen/
// changed on disk, when gopls has overlays it could close (with s.mu
// held).
func (s *Server) scheduleDiskCheck() {
	ws := s.ws
	if ws == nil || ws.gopls == nil || !ws.hasOverlays() {
		return
	}
	if s.diskTimer != nil {
		s.diskTimer.Stop()
	}
	s.diskTimer = time.AfterFunc(diskCheckDelay, s.runDiskCheck)
}

// runDiskCheck runs one typed check and adopts its result; a check
// requested while one runs runs after it.
func (s *Server) runDiskCheck() {
	s.mu.Lock()
	ws := s.ws
	if ws == nil || ws.gopls == nil || s.shutdown {
		s.mu.Unlock()
		return
	}
	if s.diskRunning {
		s.diskPending = true
		s.mu.Unlock()
		return
	}
	s.diskRunning = true
	c := ws.newDiskCheck()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := c.run(ctx)
	cancel()

	s.mu.Lock()
	s.diskRunning = false
	n := 0
	if err == nil && s.ws == ws && ws.gopls != nil {
		n = ws.adopt(c)
		if n > 0 {
			s.publishAll(ws)
		}
	}
	again := s.diskPending
	s.diskPending = false
	s.mu.Unlock()
	freeMemory()
	s.logf("typed check: %d/%d generated files on disk, %d overlays closed (%v)", len(c.typed), c.files, n, c.took.Round(time.Millisecond))
	if again {
		s.runDiskCheck()
	}
}

// freeMemory returns a finished typed check's memory to the system: its
// project and go/types data are garbage, ~60 MB of RSS at 200 files that
// the language server would otherwise keep until the runtime scavenges it.
func freeMemory() { debug.FreeOSMemory() }
