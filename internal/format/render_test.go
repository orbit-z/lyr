package format

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// TestRenderedOutput checks that formatting never changes what a page
// renders: the corpus projects are "unformatted" (spaces in the
// frontmatter, padded braces), generated as orbit generate would before and
// after formatting, under every compressHTML mode, and the generated Go must
// be the same token for token (comments, which hold the line directives, and
// the .orbit positions passed to orbit.ErrorAt excepted). The generated code
// holds every static byte a page writes and the code that writes the rest,
// so equal code renders equal HTML for every request. Scripts and styles
// aren't formatted here (no Bun): their content is checked by the safety
// net, through esbuild.
func TestRenderedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode: runs orbit generate on copies of the corpus")
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOWORK", "off")
	for _, proj := range []string{"examples/blog", "docs/site"} {
		t.Run(strings.ReplaceAll(proj, "/", "_"), func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{`"html"`, `"jsx"`, `false`} {
				ugly := copyProject(t, proj, mode, deformat)
				nice := copyProject(t, proj, mode, func(src string) string {
					out, res := Source(deformat(src), Options{})
					if len(res.Errors) > 0 {
						t.Fatalf("%v", res.Errors)
					}
					if out == deformat(src) && strings.Contains(src, "---") {
						t.Logf("formatting changed nothing in a file")
					}
					return out
				})
				a, b := plan(t, ugly), plan(t, nice)
				if len(a) != len(b) {
					t.Fatalf("compressHTML %s: %d generated files before formatting, %d after", mode, len(a), len(b))
				}
				for path, ca := range a {
					cb, ok := b[path]
					if !ok {
						t.Errorf("compressHTML %s: %s not generated after formatting", mode, path)
						continue
					}
					if ta, tb := genTokens(ca), genTokens(cb); ta != tb {
						t.Errorf("compressHTML %s: %s changed:\n%s", mode, path, Diff(path, ta, tb))
					}
				}
			}
		})
	}
}

// deformat undoes formatting the way people write: four spaces for tabs in
// the frontmatter, padding inside every {…} expression.
func deformat(src string) string {
	f := compiler.Parse(src, compiler.Strict)
	var edits []Edit
	if fm := f.Frontmatter; fm.Present {
		code := src[fm.Code.Start:fm.Code.End]
		var lines []string
		for _, l := range strings.SplitAfter(code, "\n") {
			n := len(l) - len(strings.TrimLeft(l, "\t"))
			lines = append(lines, strings.Repeat("    ", n)+l[n:])
		}
		edits = append(edits, Edit{Span: fm.Code.Span, New: strings.Join(lines, "")})
	}
	compiler.Walk(f.Template, func(n compiler.Node) bool {
		if e, ok := n.(*compiler.Expr); ok && e.Code.Text != "" {
			edits = append(edits, Edit{Span: e.Span, New: "{ " + e.Code.Text + " }"})
		}
		return true
	})
	return Apply(src, edits)
}

// copyProject copies a repo project to a temp dir, rewriting its .orbit
// files with fix and setting compressHTML.
func copyProject(t *testing.T, proj, compress string, fix func(string) string) string {
	t.Helper()
	src := filepath.Join(repoRoot, proj)
	dst := t.TempDir()
	root, _ := filepath.Abs(repoRoot)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			switch d.Name() {
			case "orbit_gen", "dist", ".orbit", "node_modules":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := string(b)
		switch {
		case strings.HasSuffix(p, ".orbit"):
			s = fix(s)
		case rel == "go.mod":
			s = strings.ReplaceAll(s, "=> ../..", "=> "+filepath.ToSlash(root))
		}
		return os.WriteFile(filepath.Join(dst, rel), []byte(s), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(dst, "orbit.json")); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
	}
	cfg["compressHTML"] = json.RawMessage(compress)
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dst, "orbit.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// plan generates the project in memory and returns the Go files by path.
func plan(t *testing.T, dir string) map[string]string {
	t.Helper()
	g := &project.Generator{Root: dir}
	res, files, err := g.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if files == nil {
		var msgs []string
		for _, d := range res.Diagnostics {
			msgs = append(msgs, fmt.Sprint(d))
		}
		t.Fatalf("generate %s failed:\n%s", dir, strings.Join(msgs, "\n"))
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".go") {
			out[f.Path] = string(f.Content)
		}
	}
	return out
}

var orbitPos = regexp.MustCompile(`^"src/[^"]*\.orbit:\d+:\d+"$`)

// genTokens lists the tokens of generated Go code, one per line, comments
// excluded and .orbit position strings blanked.
func genTokens(src string) string {
	var b strings.Builder
	for _, t := range goTokens(src) {
		lit := t.lit
		if orbitPos.MatchString(lit) {
			lit = `"src/…:L:C"`
		}
		fmt.Fprintf(&b, "%s %s\n", t.tok, lit)
	}
	return b.String()
}
