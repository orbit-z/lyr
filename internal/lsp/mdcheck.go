package lsp

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/project"
)

// The Markdown typed check (PLAN.md §9): the typed diagnostics of Markdown
// frontmatter (mdtyped.go) refresh while the user types. mdCheckDelay
// after the last edit of an open Markdown file, of the layout a .lyr
// buffer declares for a compiled Markdown file, or of a Markdown file or a
// Go file on disk, a background goroutine compiles a strict copy of the
// current buffers and runs the typed pass over the Markdown files that
// have a layout and the decl packages they import, nothing else
// (project.MarkdownChecker). Its compiler.Checker is kept between runs, so
// go list -export runs once, and again after a .go, go.mod, lyr.json or
// lyr_gen/ change. Checks run one at a time; a result whose snapshot predates the
// latest such change is dropped (the next check, already scheduled,
// replaces it). It never blocks requests or the readiness barrier, and it
// doesn't touch gopls or the generated files.
//
// While the TypeScript child runs, the same check also keeps the emitted
// .d.ts files fresh (PLAN.md §9b Q5): it type-checks the .lyr files with
// lyr.JSON or set:json sites too, after an edit of one that has or had
// sites or a Go file change, and its contracts (dataChanged, ws.tsEdit)
// are re-emitted in memory and sent to the child.
//
// It also refreshes the style and on* security warnings (sectyped.go):
// while a .lyr file has a candidate, it type-checks the files with
// candidates too, after an edit of one that has or had candidates or a Go
// file change (securityChanged, ws.secEdit).

// mdCheckDelay debounces the Markdown typed check after edits.
var mdCheckDelay = 300 * time.Millisecond

// markdownCheck is one Markdown typed check: a strict copy of the
// project's current sources, taken at ws.mdEdit edit.
type markdownCheck struct {
	proj *project.Project
	edit uint64
	// md, data and sec are the scope: Markdown files with a layout,
	// .lyr files with JSON sites (at tsEdit), .lyr files with
	// security warning candidates (at secEdit).
	md, data, sec bool
	tsEdit        uint64
	secEdit       uint64
	mdTyped       map[string][]mdTypedDiag
	// secTyped are the security warnings (sec; typedSecurityDiags).
	secTyped map[string][]secTypedDiag
	// known and units are the data contracts (data; typesOf).
	known   map[string]bool
	units   map[string]*compiler.UnitTypes
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
	n, loaded, err := mc.TypecheckScope(ctx, p, project.CheckScope{Markdown: c.md, Data: c.data, Security: c.sec})
	c.checked, c.loaded = n, loaded
	if err != nil {
		return err
	}
	if c.md {
		c.mdTyped = typedMarkdownDiags(p, front)
	}
	if c.data {
		c.known, c.units = typesOf(p, true)
	}
	if c.sec {
		c.secTyped = typedSecurityDiags(p, front)
	}
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
	s.scheduleTypedCheck()
}

// dataChanged records a change that the data contracts may depend on (an
// edit of a .lyr file that has or had JSON sites, a Go file change),
// and schedules the typed check while the TypeScript child runs (with
// s.mu held).
func (s *Server) dataChanged(ws *workspace) {
	ws.tsEdit++
	if !s.started || s.shutdown || !s.tsActive() {
		return
	}
	s.scheduleTypedCheck()
}

// scheduleTypedCheck (re)starts the debounce of the typed check while
// typing.
func (s *Server) scheduleTypedCheck() {
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
	c := &markdownCheck{edit: ws.mdEdit, tsEdit: ws.tsEdit, secEdit: ws.secEdit,
		md: ws.hasMarkdownLayouts(), data: s.tsActive() && ws.anyData(), sec: ws.hasSecuritySites()}
	if !c.md && !c.data && !c.sec {
		s.mdRunning = false
		s.mu.Unlock()
		return
	}
	c.proj = ws.proj.Clone(compiler.Strict)
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
	current := err == nil && s.ws == ws && c.md && c.edit == ws.mdEdit
	publish := false
	if current {
		had := len(ws.mdTyped) > 0
		ws.mdTyped = c.mdTyped
		publish = had || len(c.mdTyped) > 0
	}
	secCurrent := err == nil && s.ws == ws && c.sec && c.secEdit == ws.secEdit
	if secCurrent && ws.adoptSecurity(c.secTyped) {
		publish = true
	}
	if publish {
		s.publishAll(ws)
	}
	if err == nil && s.ws == ws && c.data && c.tsEdit == ws.tsEdit {
		s.adoptTypes(ws, c.known, c.units)
	}
	again := s.mdPending
	s.mdPending = false
	s.mu.Unlock()
	switch {
	case err != nil:
		s.logf("markdown typed check: %v", err)
	default:
		what := "markdown typed check"
		if c.data || c.sec {
			what = fmt.Sprintf("typed check (markdown %v, data %v, security %v)", c.md, c.data, c.sec)
		}
		s.logf("%s: %d files, go list %v, current %v (%v)", what, c.checked, c.loaded, current || secCurrent, c.took.Round(time.Millisecond))
	}
	if hooks != nil {
		hooks.done(current || secCurrent)
	}
	if again {
		s.runMarkdownCheck()
	}
}

// mdHooks are the test hooks of an in-process server: run is called when
// a typed check while typing has taken its snapshot, before it runs, and
// done after it, with whether its Markdown or security result was adopted.
var mdHooks atomic.Pointer[mdTestHooks]

type mdTestHooks struct {
	run  func()
	done func(adopted bool)
}

// isMarkdownLayout reports whether the .lyr file at p is the layout of
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
// imports: a Go file outside lyr_gen/, go.mod, go.sum, go.work or
// lyr.json.
func invalidatesExports(rel string) bool {
	switch path.Base(rel) {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if rel == "lyr.json" {
		return true
	}
	return strings.HasSuffix(rel, ".go") && !isGenPath(rel) && !strings.HasPrefix(rel, "../")
}
