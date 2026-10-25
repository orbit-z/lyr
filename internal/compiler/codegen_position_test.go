package compiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLineDirectivePositions is PLAN.md §3.3's position test: a project
// with deliberate errors in the frontmatter body, a hoisted helper, a
// continuation line of a multi-line declaration, an {expr}, a control
// header, and an unknown prop with the pre-check skipped (an embedded
// Props). go build and go vet must both report each at its .orbit L:C.
func TestLineDirectivePositions(t *testing.T) {
	const dir = "testdata/positions"
	c := loadCase(t, dir)
	res := generateCase(t, c)
	for _, r := range res {
		if len(r.out.Diagnostics) > 0 {
			t.Fatalf("%s: unexpected codegen diagnostics %v", r.path, r.out.Diagnostics)
		}
	}
	want := []struct {
		file, marker string // the error is at the start of marker's first occurrence
		msg          string
	}{
		{"src/components/E1.orbit", `"x"`, "cannot use"},
		{"src/components/E2.orbit", `"n"`, "cannot use"},
		{"src/components/E3.orbit", "intt", "undefined: intt"},
		{"src/components/E4.orbit", "titel", "undefined: titel"},
		{"src/components/E5.orbit", "Dpth", "Dpth undefined"},
		{"src/pages/index.orbit", "nmae", "unknown field Nmae"},
	}
	m := newGenModule(t)
	for _, r := range res {
		m.writeOutput(r.out)
	}
	build, vet := m.buildErrors()
	t.Logf("go build:\n%s\ngo vet:\n%s", build, vet)
	for _, w := range want {
		src, err := os.ReadFile(filepath.Join(dir, w.file))
		if err != nil {
			t.Fatal(err)
		}
		off := strings.Index(string(src), w.marker)
		pos := NewLineIndex(string(src)).Position(off)
		loc := "./" + w.file + ":" + pos.String() + ": "
		for name, out := range map[string]string{"build": build, "vet": vet} {
			line := findLine(out, loc)
			if line == "" {
				t.Errorf("go %s: no error at %s%s\n%s", name, loc, w.msg, out)
			} else if !strings.Contains(line, w.msg) {
				t.Errorf("go %s: %s, want %q", name, line, w.msg)
			}
		}
	}
	// No error may point into a generated file.
	gen := regexp.MustCompile(`(?m)^[^\s]*orbit_gen/[^\s]*\.go:\d+`)
	for name, out := range map[string]string{"build": build, "vet": vet} {
		if l := gen.FindString(out); l != "" {
			t.Errorf("go %s reports a generated position: %s\n%s", name, l, out)
		}
	}
}

func findLine(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, prefix); i >= 0 {
			return l[i:]
		}
	}
	return ""
}
