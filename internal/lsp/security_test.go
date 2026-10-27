package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSecurityWarnings: the style and on* warnings come from the typed
// check (PLAN.md §2.7 (Q2) a): a string style value warns once the check
// ran, an orbit.CSS one and a constant don't, the warning follows edits
// elsewhere in the file and goes away when the value becomes orbit.CSS.
func TestSecurityWarnings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const src = "---\ns := \"color: red\"\ncss := orbit.CSS(\"color: blue\")\n---\n" +
		"<div style={s}>a</div>\n<div style={css}>b</div>\n<p style={\"display: none\"}>c</p>\n"
	dir := writeProject(t, map[string]string{"src/pages/index.orbit": src})
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, dir, map[string]any{"goplsPath": stub}, stubEnv+"=record:"+filepath.Join(t.TempDir(), "rec"))
	const index = "src/pages/index.orbit"
	text := c.open(index)
	start := func(d map[string]any) Position {
		var r Range
		b, _ := json.Marshal(d["range"])
		json.Unmarshal(b, &r)
		return r.Start
	}
	isWarn := func(ds []map[string]any) bool {
		if len(ds) != 1 {
			return false
		}
		m, _ := ds[0]["message"].(string)
		sev, _ := ds[0]["severity"].(float64)
		return strings.Contains(m, "style={…} is escaped for HTML, not for CSS") && sev == sevWarning
	}
	ds := c.waitDiag(index, isWarn)
	if got, want := start(ds[0]), at(t, text, "style={s}", 0, 0); got != want {
		t.Errorf("warning at %+v, want %+v", got, want)
	}

	// An edit before it: the warning follows its attribute.
	moved := strings.Replace(text, "<div style={s}>", "<h1>title</h1>\n<div style={s}>", 1)
	c.change(index, moved)
	ds = c.waitDiag(index, func(ds []map[string]any) bool {
		return isWarn(ds) && start(ds[0]) == at(t, moved, "style={s}", 0, 0)
	})

	// The value becomes an orbit.CSS: no warning.
	fixed := strings.Replace(moved, "style={s}", "style={orbit.CSS(s)}", 1)
	c.change(index, fixed)
	c.waitDiag(index, func(ds []map[string]any) bool { return len(ds) == 0 })
}

// TestSecurityTypedCheck: the typed check while typing refreshes the
// style and on* warnings (user decision, 2026-10-27), with no orbit_gen/
// change: a newly typed style={s} warns after the debounce, an orbit.CSS
// value or a frontmatter change of s's type clears it, and an edit of a
// file without candidates runs no check. The server runs in process, for
// its hooks.
func TestSecurityTypedCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	const index, other = "src/pages/index.orbit", "src/pages/other.orbit"
	plain := "---\ns := \"color: red\"\n---\n<div>a</div>\n"
	dir := writeProject(t, map[string]string{index: plain, other: "<p>other</p>\n"})
	var runs atomic.Int32
	mdHooks.Store(&mdTestHooks{run: func() { runs.Add(1) }, done: func(bool) {}})
	t.Cleanup(func() { mdHooks.Store(nil) })
	stub, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(stubEnv, "record:"+filepath.Join(t.TempDir(), "rec"))
	c := startInProcess(t, dir, map[string]any{"goplsPath": stub})
	c.open(index)
	c.open(other)
	isWarn := func(ds []map[string]any) bool {
		if len(ds) != 1 {
			return false
		}
		m, _ := ds[0]["message"].(string)
		return strings.Contains(m, "style={…} is escaped for HTML, not for CSS")
	}
	none := func(ds []map[string]any) bool { return len(ds) == 0 }
	// settle waits until the checks of the edits so far are done.
	settle := func() {
		c.call("textDocument/hover", c.textPos(index, Position{}))
		time.Sleep(3 * mdCheckDelay)
	}

	// A new style={s}, s a string: the warning comes from the check.
	warned := strings.Replace(plain, "<div>", "<div style={s}>", 1)
	c.change(index, warned)
	c.waitDiag(index, isWarn)

	// orbit.CSS(s): no warning, and none after the check either.
	c.change(index, strings.Replace(warned, "style={s}", "style={orbit.CSS(s)}", 1))
	c.waitDiag(index, none)
	settle()
	c.waitDiag(index, none)

	// Back to style={s}: the warning is the next check's (the last one
	// found none).
	c.change(index, warned)
	c.waitDiag(index, isWarn)

	// s becomes an orbit.CSS, the attribute's text unchanged: the check
	// clears the warning.
	c.change(index, strings.Replace(warned, `s := "color: red"`, `s := orbit.CSS("color: red")`, 1))
	c.waitDiag(index, none)

	// An edit of a file without candidates runs no check.
	settle()
	n := runs.Load()
	c.change(other, "<p>edited</p>\n")
	settle()
	if got := runs.Load(); got != n {
		t.Errorf("an edit of a file without candidates ran %d typed checks", got-n)
	}
}
