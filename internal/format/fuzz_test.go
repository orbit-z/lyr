package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// FuzzFormat: Source never panics; on a file without syntax errors that it
// formats, the output parses, has the same template tree, and formatting
// it again changes nothing.
func FuzzFormat(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.lyr")
	more, _ := filepath.Glob("../compiler/testdata/*/*.lyr")
	for _, name := range append(files, more...) {
		if b, err := os.ReadFile(name); err == nil {
			f.Add(string(b))
		}
	}
	f.Add("---\nx:=1\n---\n<p>{ x+1 }</p>\n")
	f.Fuzz(func(t *testing.T, src string) {
		out, res := Source(src, Options{})
		if len(res.Errors) > 0 {
			if out != src {
				t.Fatalf("a refused file was changed")
			}
			return
		}
		g := compiler.Parse(out, compiler.Strict)
		if errs := g.SyntaxErrors(); len(errs) > 0 {
			t.Fatalf("output doesn't parse: %v\n%q\n=>\n%q", errs, src, out)
		}
		if out == src {
			return
		}
		if treeSig(compiler.Parse(src, compiler.Strict)) != treeSig(g) {
			t.Fatalf("template tree changed:\n%q\n=>\n%q", src, out)
		}
		again, res2 := Source(out, Options{})
		if len(res2.Errors) > 0 || again != out {
			t.Fatalf("not idempotent: %v\n%q\n=>\n%q\n=>\n%q", res2.Errors, src, out, again)
		}
	})
}
