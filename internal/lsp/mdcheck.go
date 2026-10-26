package lsp

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// The Markdown typed check (PLAN.md §9): the typed diagnostics of Markdown
// frontmatter (mdtyped.go) refresh while the user types. mdCheckDelay
// after the last edit of an open Markdown file, of the layout an .orbit
// buffer declares for a compiled Markdown file, or of a Markdown file or a
// Go file on disk, a background goroutine compiles a strict copy of the
// current buffers and runs the typed pass over the Markdown files that
// have a layout and the decl packages they import, nothing else
// (project.MarkdownChecker). Its compiler.Checker is kept between runs, so
// go list -export runs once, and again after a .go, go.mod, orbit.json or
// orbit_gen/ change. Checks run one at a time; a result whose snapshot predates the
// latest such change is dropped (the next check, already scheduled,
// replaces it). It never blocks requests or the readiness barrier, and it
// doesn't touch gopls or the generated files.

// mdCheckDelay debounces the Markdown typed check after edits.
var mdCheckDelay = 300 * time.Millisecond

// markdownCheck is one Markdown typed check: a strict copy of the
// project's current sources, taken at ws.mdEdit edit.
type markdownCheck struct {
	proj    *project.Project
	edit    uint64
	mdTyped map[string][]mdTypedDiag
	checked int  // files type-checked
	loaded  bool // go list -export ran
	took    time.Duration
}

// run compiles the snapshot and type-checks its Markdown files with mc.
// It doesn't touch the workspace (no lock).
func (c *markdownCheck) run(ctx context.Context, mc *project.MarkdownChecker) error {
	start := time.Now()
	defer func() {
		c.took = time.Since(start)
		c.proj = nil
	}()
	p := c.proj
	p.Front()
	front := slices.Clone(p.Diagnostics)
	n, loaded, err := mc.Typecheck(ctx, p)
	c.checked, c.loaded = n, loaded
	if err != nil {
		return err
	}
	c.mdTyped = typedMarkdownDiags(p, front)
	return nil
}

// markdownChanged records a change that the typed diagnostics of Markdown
// files depend on, and schedules a Markdown typed check when a compiled
// Markdown file has a layout (with s.mu held).
func (s *Server) markdownChanged(ws *workspace) {
	ws.mdEdit++
	if !s.started || s.shutdown || !ws.hasMarkdownLayouts() {
		return
	}
	if s.mdTimer != nil {
		s.mdTimer.Stop()
	}
	s.mdTimer = time.AfterFunc(mdCheckDelay, s.runMarkdownCheck)
}

// runMarkdownCheck runs one Markdown typed check and adopts its result
// when it's still current; one requested while a check runs runs after
// it.
func (s *Server) runMarkdownCheck() {
	s.mu.Lock()
	ws := s.ws
	if ws == nil || s.shutdown {
		s.mu.Unlock()
		return
	}
	if s.mdRunning {
		s.mdPending = true
		s.mu.Unlock()
		return
	}
	s.mdRunning = true
	if s.mdChecker == nil || s.mdCheckerWS != ws {
		s.mdChecker, s.mdCheckerWS = project.NewMarkdownChecker(&compiler.GoListLoader{Dir: ws.root}), ws
	} else if s.mdInvalid {
		s.mdChecker.Invalidate()
	}
	s.mdInvalid = false
	mc := s.mdChecker
	c := &markdownCheck{proj: ws.proj.Clone(compiler.Strict), edit: ws.mdEdit}
	s.mu.Unlock()

	hooks := mdHooks.Load()
	if hooks != nil {
		hooks.run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := c.run(ctx, mc)
	cancel()

	s.mu.Lock()
	s.mdRunning = false
	current := err == nil && s.ws == ws && c.edit == ws.mdEdit
	if current {
		had := len(ws.mdTyped) > 0
		ws.mdTyped = c.mdTyped
		if had || len(c.mdTyped) > 0 {
			s.publishAll(ws)
		}
	}
	again := s.mdPending
	s.mdPending = false
	s.mu.Unlock()
	switch {
	case err != nil:
		s.logf("markdown typed check: %v", err)
	default:
		s.logf("markdown typed check: %d files, go list %v, current %v (%v)", c.checked, c.loaded, current, c.took.Round(time.Millisecond))
	}
	if hooks != nil {
		hooks.done(current)
	}
	if again {
		s.runMarkdownCheck()
	}
}

// mdHooks are the test hooks of an in-process server: run is called when
// a Markdown typed check has taken its snapshot, before it runs, and done
// after it, with whether its result was adopted.
var mdHooks atomic.Pointer[mdTestHooks]

type mdTestHooks struct {
	run  func()
	done func(adopted bool)
}

// isMarkdownLayout reports whether the .orbit file at p is the layout of
// a compiled Markdown file.
func (ws *workspace) isMarkdownLayout(p string) bool {
	for _, f := range ws.proj.Files {
		if f.MD != nil && f.MD.Layout == p {
			return true
		}
	}
	return false
}

// invalidatesExports reports whether a change of the module-relative
// path rel can change the export data of the packages the generated code
// imports: a Go file outside orbit_gen/, go.mod, go.sum, go.work or
// orbit.json.
func invalidatesExports(rel string) bool {
	switch path.Base(rel) {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if rel == "orbit.json" {
		return true
	}
	return strings.HasSuffix(rel, ".go") && !isGenPath(rel) && !strings.HasPrefix(rel, "../")
}
