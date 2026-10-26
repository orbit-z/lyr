package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/tailwind/tailwindtest"
)

// twFixture is the dev fixture with a Tailwind root imported from the
// layout and a block input in Card.
func twFixture(t *testing.T) string {
	dir := fixture(t)
	writeFile(t, dir, ".gitignore", "orbit_gen/\n.orbit/\ndist/\n")
	writeFile(t, dir, "src/styles/global.css", "@import \"tailwindcss\";\n.site { color: red }\n")
	edit(t, dir, "src/layouts/Base.orbit", "<style>", "<style is:global>\n  @import \"../styles/global.css\";\n</style>\n<style>")
	edit(t, dir, "src/components/Card.orbit", `<p class="card">`, `<p class="card tw-one">`)
	writeFile(t, dir, "src/components/Card.orbit", readFile(t, dir, "src/components/Card.orbit")+
		"\n<style>\n  @reference \"../styles/global.css\";\n  .card { @apply font-bold; }\n  .x { @apply italic; }\n</style>\n")
	return dir
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// devCSS returns the index page's dev stylesheet.
func devCSS(t *testing.T, dir string) string {
	t.Helper()
	var m struct {
		Pages map[string][]struct{ Kind, URL string }
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, ".orbit/dev/manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Pages["src/pages/index.orbit"] {
		if a.Kind == "css" {
			return readFile(t, dir, ".orbit/dev"+a.URL)
		}
	}
	t.Fatalf("no stylesheet: %+v", m)
	return ""
}

// TestPipelineTailwind checks the dev loop's Tailwind reruns (PLAN.md §5d,
// Q4) with the fake CLI: a template edit with a new class reruns the root
// in parallel and rebuilds the bundles; one without a new token runs
// nothing; a .css edit reruns every input; a block edit only that block;
// a failed run keeps the last CSS and reports the error.
func TestPipelineTailwind(t *testing.T) {
	needE2E(t)
	dir := twFixture(t)
	log := tailwindtest.Fake(t, filepath.Join(dir, "src"))
	tp := newTestPipeline(t, dir)
	runs := func() int { return len(tailwindtest.Log(t, log)) }

	if out := tp.run(t); out.Bin == "" || !out.Assets {
		t.Fatalf("first run: %+v", out)
	}
	if n := runs(); n != 2 {
		t.Fatalf("first run: %d Tailwind runs, want 2 (the root and Card's block)", n)
	}
	css := devCSS(t, dir)
	for _, want := range []string{".tw-base", ".tw-one", ".site", "--applied: font-bold"} {
		if !strings.Contains(css, want) {
			t.Errorf("first CSS has no %s:\n%s", want, css)
		}
	}

	// A new class in a template: the root reruns, in parallel with the Go
	// stages, then esbuild.
	tp.reset()
	edit(t, dir, "src/components/Card.orbit", "tw-one", "tw-one tw-two")
	out := tp.run(t, "src/components/Card.orbit")
	st := tp.got()
	if !out.Assets || out.Bin == "" || !slices.Contains(st, "tailwind") || !slices.Contains(st, "esbuild-tailwind") || slices.Contains(st, "esbuild") {
		t.Errorf("new class: stages %v, %+v", st, out)
	}
	if st[0] != "tailwind" {
		t.Errorf("new class: Tailwind didn't start first: %v", st)
	}
	if n := runs(); n != 3 {
		t.Errorf("new class: %d runs, want 3", n)
	}
	if css := devCSS(t, dir); !strings.Contains(css, ".tw-two") {
		t.Errorf("new class not in the CSS:\n%s", css)
	}

	// No new token: nothing reruns.
	tp.reset()
	edit(t, dir, "src/components/Card.orbit", "tw-one tw-two", "tw-one  tw-two")
	out = tp.run(t, "src/components/Card.orbit")
	if st := tp.got(); slices.Contains(st, "tailwind") || slices.Contains(st, "esbuild-tailwind") || runs() != 3 {
		t.Errorf("no new token: stages %v, %d runs", st, runs())
	}

	// A new token that doesn't change the CSS: Tailwind reruns, esbuild
	// doesn't.
	tp.reset()
	edit(t, dir, "src/components/Card.orbit", "card {props.Post}", "Card {props.Post}")
	out = tp.run(t, "src/components/Card.orbit")
	if st := tp.got(); !slices.Contains(st, "tailwind") || slices.Contains(st, "esbuild-tailwind") || out.Assets || runs() != 4 {
		t.Errorf("same CSS: stages %v, %+v, %d runs", st, out, runs())
	}

	// A .css edit: every input reruns, in esbuild.
	tp.reset()
	edit(t, dir, "src/styles/global.css", "color: red", "color: blue")
	out = tp.run(t, "src/styles/global.css")
	if st := tp.got(); !slices.Equal(st, []string{"esbuild"}) || runs() != 6 || !out.Assets {
		t.Errorf(".css edit: stages %v, %d runs, %+v", st, runs(), out)
	}

	// A block edit that brings no new token: only the block reruns.
	tp.reset()
	edit(t, dir, "src/components/Card.orbit", "  .x { @apply italic; }\n", "")
	out = tp.run(t, "src/components/Card.orbit")
	if runs() != 7 || !out.Assets || strings.Contains(devCSS(t, dir), "--applied: italic") {
		t.Errorf("block edit: %d runs, %+v", runs(), out)
	}
	if lines := tailwindtest.Log(t, log); !strings.Contains(lines[6], filepath.Join(dir, "src/components")) {
		t.Errorf("block edit ran %q", lines[6])
	}

	// A failed run: the overlay, and the last CSS stays.
	before := devCSS(t, dir)
	tp.reset()
	edit(t, dir, "src/components/Card.orbit", "@apply font-bold;", "@apply font-bold; /* FAIL_TW */")
	o, err := tp.pipeline.run(t.Context(), NewChange([]string{"src/components/Card.orbit"}, nil), true)
	if err != nil {
		t.Fatal(err)
	}
	if o.Failure == nil || !strings.Contains(o.Failure.Text, "src/components/Card.orbit:") || !strings.Contains(o.Failure.Text, "Cannot apply unknown utility class `nope`") {
		t.Errorf("failed run: %+v", o.Failure)
	}
	if css := devCSS(t, dir); css != before {
		t.Errorf("the CSS changed after a failed run")
	}
	edit(t, dir, "src/components/Card.orbit", " /* FAIL_TW */", "")
	if out := tp.run(t, "src/components/Card.orbit"); !out.Assets {
		t.Errorf("after the fix: %+v", out)
	}
}

// TestDevTailwind runs orbit dev with the real CLI: adding a class to a
// template reloads the page with a stylesheet that has it, with one go
// build.
func TestDevTailwind(t *testing.T) {
	needE2E(t)
	tailwindtest.Real(t)
	dir := twFixture(t)
	edit(t, dir, "src/components/Card.orbit", "tw-one", "italic")
	s := startSession(t, dir)
	defer s.stop()
	css := func() string {
		t.Helper()
		_, body := s.get("/")
		m := cssLink.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no stylesheet:\n%s", body)
		}
		_, css := s.get(m[1])
		return css
	}
	if c := css(); !strings.Contains(c, ".italic") || strings.Contains(c, ".underline") || !strings.Contains(c, "font-weight: var(--font-weight-bold") {
		t.Fatalf("first CSS:\n%s", c)
	}
	events := s.events()
	builds := s.count("gobuild")
	edit(t, dir, "src/components/Card.orbit", "italic", "italic underline")
	waitReload(t, events, "new class")
	if c := css(); !strings.Contains(c, ".underline") {
		t.Errorf("no .underline after the edit:\n%s", c)
	}
	if n := s.count("gobuild"); n != builds+1 {
		t.Errorf("%d go builds, want 1", n-builds)
	}
	if s.count("tailwind") == 0 {
		t.Errorf("Tailwind didn't rerun early")
	}
}

// TestPipelineNoTailwind: a project without Tailwind never runs the CLI
// or the gate.
func TestPipelineNoTailwind(t *testing.T) {
	needE2E(t)
	dir := fixture(t)
	log := tailwindtest.Fake(t, "")
	tp := newTestPipeline(t, dir)
	tp.run(t)
	edit(t, dir, "src/components/Card.orbit", "card {props.Post}", "Card tw-x {props.Post}")
	tp.run(t, "src/components/Card.orbit")
	if tp.tw.Active() || tp.gate != nil || len(tailwindtest.Log(t, log)) != 0 {
		t.Errorf("Tailwind ran for a project without it")
	}
}
