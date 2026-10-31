package lyrtest

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// update is the -update flag: Snapshot rewrites golden files instead of
// comparing. lyrtest owns it; a test package that imports lyrtest reads
// it with flag.Lookup("update") rather than defining its own.
var update = flag.Bool("update", false, "rewrite the lyrtest golden files in testdata/")

// Hashed names that change whenever a file's content (or a deploy) changes,
// and their stable replacements.
var (
	// esbuild's "[name]-[hash]" (internal/assets): 8 characters of
	// uppercase base32. The name keeps any directories, and the extension
	// may be double (.js.map).
	assetHashRe = regexp.MustCompile(`(/_lyr/(?:[^/\s"'<>()?#]+/)*[^/\s"'<>()?#]+)-[A-Z2-7]{8}((?:\.[A-Za-z0-9]+)+)`)
	// The build ID in an island URL: /_lyr/island/<16 hex>/ (PLAN.md §4).
	islandBuildRe = regexp.MustCompile(`/_lyr/island/[0-9a-f]{16}/`)
)

// normalize replaces the parts of html that change between builds with
// stable placeholders: hashed asset names /_lyr/<name>-<HASH>.<ext> become
// /_lyr/<name>-*.<ext>, and the build ID in island URLs,
// /_lyr/island/<build ID>/, becomes /_lyr/island/*/. Snapshot applies it
// before comparing and before writing a golden file.
func normalize(html string) string {
	if !strings.Contains(html, "/_lyr/") {
		return html
	}
	html = islandBuildRe.ReplaceAllString(html, "/_lyr/island/*/")
	return assetHashRe.ReplaceAllString(html, "$1-*$2")
}

// Snapshot compares html with the golden file testdata/<name>.golden in the
// test's package directory, and fails the test, showing the first
// difference, if they differ. A missing golden file fails too, with a hint
// to run with -update. With go test -update, Snapshot writes the file
// instead (creating testdata/ and any directories in name).
//
// Hashed asset URLs and island build IDs are normalized first (see the
// testing docs), so a CSS or JS edit or a new build doesn't break page
// snapshots. name is a slash-separated relative path ("card",
// "pages/home"), without ".." elements.
func Snapshot(t testing.TB, name, html string) {
	t.Helper()
	file, ok := goldenFile(name)
	if !ok {
		t.Fatalf("lyrtest.Snapshot: invalid name %q: use a relative slash-separated path such as \"card\" or \"pages/home\", without \"..\"", name)
	}
	got := normalize(html)
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("lyrtest.Snapshot: %v", err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatalf("lyrtest.Snapshot: %v", err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lyrtest.Snapshot: no golden file %s; run go test with -update to create it", filepath.ToSlash(file))
	}
	if err != nil {
		t.Fatalf("lyrtest.Snapshot: %v", err)
	}
	if string(want) != got {
		t.Errorf("lyrtest.Snapshot: output differs from %s (run go test with -update to accept it):\n%s",
			filepath.ToSlash(file), difference(string(want), got))
	}
}

// goldenFile returns the golden file path for name, or false if name isn't a
// clean local slash path.
func goldenFile(name string) (string, bool) {
	if name == "" || strings.Contains(name, `\`) || strings.HasSuffix(name, "/") {
		return "", false
	}
	for _, elem := range strings.Split(name, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return "", false
		}
	}
	rel := filepath.FromSlash(name) + ".golden"
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.Join("testdata", rel), true
}

// difference describes where got first differs from want: its line and
// column, and both versions around it. It works for pages compressed onto
// one line as well as for multi-line HTML.
func difference(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	line := 1 + strings.Count(got[:i], "\n")
	col := i - strings.LastIndexByte(got[:i], '\n')
	const context = 60
	start := max(0, i-context)
	excerpt := func(s string) string {
		if start >= len(s) {
			return "(end of file)"
		}
		end := min(len(s), i+context)
		e := strconv.Quote(s[start:end])
		if start > 0 {
			e = "…" + e
		}
		if end < len(s) {
			e += "…"
		}
		return e
	}
	return "first difference at line " + strconv.Itoa(line) + ", column " + strconv.Itoa(col) + ":\n" +
		"  want: " + excerpt(want) + "\n" +
		"  got:  " + excerpt(got) + "\n" +
		"  (want " + strconv.Itoa(len(want)) + " bytes, got " + strconv.Itoa(len(got)) + ")"
}
