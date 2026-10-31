package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fmtUgly = "---\nx:=1\n---\n<p>{ x+1 }</p>\n"
	fmtNice = "---\nx := 1\n---\n<p>{x + 1}</p>\n"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFmt(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runFmtCLI(t *testing.T, dir, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	e := &env{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, dir: dir, ctx: context.Background()}
	code = run(e, append([]string{"fmt"}, args...))
	return code, out.String(), errb.String()
}

func fmtProject(t *testing.T) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                     "module example.com/site\n\ngo 1.27\n",
		"src/pages/index.lyr":        fmtUgly,
		"src/pages/ok.lyr":           fmtNice,
		"src/components/Card.lyr":    "<div>{ a }</div>\n",
		"src/pages/about.md":         "#   About\n",
		"src/node_modules/x/a.lyr":   fmtUgly,
		"src/lyr_gen/a.lyr":          fmtUgly,
		"src/.hidden/a.lyr":          fmtUgly,
		"dist/a.lyr":                 fmtUgly,
		"other/b.lyr":                fmtUgly,
		"src/components/notes.txt":   "x",
		"src/components/sub/Sub.lyr": fmtNice,
	})
	return dir
}

func TestFmtModes(t *testing.T) {
	t.Setenv("PATH", "") // no Bun: scripts and styles are skipped
	dir := fmtProject(t)
	sub := filepath.Join(dir, "src", "components")

	// -l lists project-relative paths, sorted, writes nothing, exits 1.
	code, out, errs := runFmtCLI(t, sub, "", "-l")
	if code != 1 || out != "src/components/Card.lyr\nsrc/pages/index.lyr\n" || errs != "" {
		t.Errorf("-l: code %d, stdout %q, stderr %q", code, out, errs)
	}
	if readFmt(t, dir, "src/pages/index.lyr") != fmtUgly {
		t.Error("-l wrote a file")
	}
	// -d prints a gofmt -d diff.
	code, out, _ = runFmtCLI(t, dir, "", "-d", "src/pages")
	if code != 1 || !strings.HasPrefix(out, "diff src/pages/index.lyr.orig src/pages/index.lyr\n--- src/pages/index.lyr.orig\n+++ src/pages/index.lyr\n@@ -1,4 +1,4 @@\n ---\n-x:=1\n+x := 1\n") {
		t.Errorf("-d: code %d, stdout %q", code, out)
	}
	// A path outside src/ is formatted when named.
	code, out, _ = runFmtCLI(t, dir, "", "-l", "other", "dist/a.lyr")
	if code != 1 || out != "dist/a.lyr\nother/b.lyr\n" {
		t.Errorf("-l other: code %d, stdout %q", code, out)
	}

	// No flag: write in place, silently, keeping the mode.
	if err := os.Chmod(filepath.Join(dir, "src/pages/index.lyr"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runFmtCLI(t, dir, "")
	if code != 0 || out != "" || errs != "" {
		t.Errorf("write: code %d, stdout %q, stderr %q", code, out, errs)
	}
	if got := readFmt(t, dir, "src/pages/index.lyr"); got != fmtNice {
		t.Errorf("index.lyr = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "src/pages/index.lyr")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	// Skipped: dot directories, node_modules, lyr_gen, dist/, Markdown.
	for _, f := range []string{"src/node_modules/x/a.lyr", "src/lyr_gen/a.lyr", "src/.hidden/a.lyr", "dist/a.lyr"} {
		if readFmt(t, dir, f) != fmtUgly {
			t.Errorf("%s was formatted", f)
		}
	}
	if readFmt(t, dir, "src/pages/about.md") != "#   About\n" {
		t.Error("about.md was changed")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "src/pages"))
	if len(entries) != 3 {
		t.Errorf("src/pages has a leftover temp file: %v", entries)
	}
	// Formatted now.
	if code, out, _ := runFmtCLI(t, dir, "", "-l"); code != 0 || out != "" {
		t.Errorf("-l after write: code %d, stdout %q", code, out)
	}
	// -l -w lists and writes, and succeeds.
	writeFiles(t, dir, map[string]string{"src/pages/index.lyr": fmtUgly})
	if code, out, _ := runFmtCLI(t, dir, "", "-l", "-w"); code != 0 || out != "src/pages/index.lyr\n" || readFmt(t, dir, "src/pages/index.lyr") != fmtNice {
		t.Errorf("-l -w: code %d, stdout %q", code, out)
	}
}

func TestFmtErrors(t *testing.T) {
	t.Setenv("PATH", "")
	dir := fmtProject(t)
	writeFiles(t, dir, map[string]string{"src/pages/bad.lyr": "---\nx:=\n---\n<p>{ x }</p>\n"})
	code, _, errs := runFmtCLI(t, dir, "")
	if code != 1 || !strings.Contains(errs, "src/pages/bad.lyr:3:1: expected operand") {
		t.Errorf("syntax error: code %d, stderr %q", code, errs)
	}
	if readFmt(t, dir, "src/pages/bad.lyr") != "---\nx:=\n---\n<p>{ x }</p>\n" {
		t.Error("bad.lyr was changed")
	}
	if readFmt(t, dir, "src/pages/index.lyr") != fmtNice {
		t.Error("the other files weren't formatted")
	}

	tests := []struct {
		dir    string
		args   []string
		code   int
		stderr string
	}{
		{dir, []string{"src/pages/about.md"}, 1, "lyr fmt: src/pages/about.md: lyr fmt formats .lyr files; format Markdown with bun fmt\n"},
		{dir, []string{"go.mod"}, 1, "lyr fmt: go.mod: lyr fmt formats .lyr files\n"},
		{dir, []string{"nope.lyr"}, 1, "no such file"},
		{t.TempDir(), nil, 1, "lyr fmt: no go.mod found; pass the files or directories to format\n"},
		{dir, []string{"--bogus"}, 2, "flag provided but not defined"},
		{dir, []string{"--stdin-filepath=x.lyr", "-w"}, 2, "--stdin-filepath takes no paths and no -w"},
		{dir, []string{"--stdin-filepath=x.lyr", "src"}, 2, "--stdin-filepath takes no paths and no -w"},
	}
	for _, tt := range tests {
		code, _, errs := runFmtCLI(t, tt.dir, "", tt.args...)
		if code != tt.code || !strings.Contains(errs, tt.stderr) {
			t.Errorf("lyr fmt %v: code %d, stderr %q; want %d, %q", tt.args, code, errs, tt.code, tt.stderr)
		}
	}
}

func TestFmtStdin(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir() // outside a project: formatting needs only the file
	code, out, errs := runFmtCLI(t, dir, fmtUgly, "--stdin-filepath=src/pages/x.lyr")
	if code != 0 || out != fmtNice || errs != "" {
		t.Errorf("stdin: code %d, stdout %q, stderr %q", code, out, errs)
	}
	if code, out, _ := runFmtCLI(t, dir, fmtUgly, "-l", "--stdin-filepath=src/pages/x.lyr"); code != 1 || out != "src/pages/x.lyr\n" {
		t.Errorf("stdin -l: code %d, stdout %q", code, out)
	}
	if code, out, _ := runFmtCLI(t, dir, fmtNice, "-l", "--stdin-filepath=x.lyr"); code != 0 || out != "" {
		t.Errorf("stdin -l formatted: code %d, stdout %q", code, out)
	}
	if code, out, _ := runFmtCLI(t, dir, fmtUgly, "-d", "--stdin-filepath=x.lyr"); code != 1 || !strings.Contains(out, "+x := 1\n") {
		t.Errorf("stdin -d: code %d, stdout %q", code, out)
	}
	code, out, errs = runFmtCLI(t, dir, "<p>{ x </p>\n", "--stdin-filepath=x.lyr")
	if code != 1 || out != "" || !strings.Contains(errs, "x.lyr:1:4: { has no matching }") {
		t.Errorf("stdin syntax error: code %d, stdout %q, stderr %q", code, out, errs)
	}
}

func TestFmtBunNotice(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":              "module example.com/site\n",
		"src/pages/index.lyr": "<p>{ 1 }</p>\n<script>\nconst a=1\n</script>\n",
		"src/pages/plain.lyr": "<p>{ 1 }</p>\n",
	})
	// Only files with a script or style need Bun.
	code, _, errs := runFmtCLI(t, dir, "", "src/pages/plain.lyr")
	if code != 0 || errs != "" {
		t.Errorf("plain: code %d, stderr %q", code, errs)
	}
	code, _, errs = runFmtCLI(t, dir, "")
	if code != 0 || strings.Count(errs, "\n") != 1 || !strings.HasPrefix(errs, "lyr fmt: skipping <script> and <style> formatting: bun not found") {
		t.Errorf("notice: code %d, stderr %q", code, errs)
	}
	if got := readFmt(t, dir, "src/pages/index.lyr"); got != "<p>{1}</p>\n<script>\nconst a=1\n</script>\n" {
		t.Errorf("index.lyr = %q", got)
	}
}

func TestFmtSymlinks(t *testing.T) {
	t.Setenv("PATH", "")
	dir := fmtProject(t)
	target := filepath.Join(dir, "other")
	if err := os.Symlink(target, filepath.Join(dir, "src", "linked")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(target, "b.lyr"), filepath.Join(dir, "src", "pages", "link.lyr")); err != nil {
		t.Skip(err)
	}
	code, out, _ := runFmtCLI(t, dir, "", "-l")
	if code != 1 || strings.Contains(out, "linked") || strings.Contains(out, "link.lyr") {
		t.Errorf("symlinks followed: %q", out)
	}
}
