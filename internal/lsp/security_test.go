package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
