package format

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/check"
	"github.com/orbit-z/orbit/internal/compiler"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// findBun returns a usable bun, or skips the test with a notice.
func findBun(t testing.TB) string {
	t.Helper()
	bun, notice := check.FindBun(context.Background(), "", "skipping the Bun formatting tests")
	if bun == "" {
		t.Skip(notice)
	}
	return bun
}

// render describes a formatting result for golden files: the output, or
// the errors, then the warnings.
func render(path, src string, opts Options) string {
	out, res := Source(src, opts)
	lines := compiler.NewLineIndex(src)
	var b strings.Builder
	if len(res.Errors) > 0 {
		b.WriteString("-- errors --\n")
		for _, d := range res.Errors {
			b.WriteString(d.Format(path, lines) + "\n")
		}
	} else {
		b.WriteString(out)
	}
	if len(res.Warnings) > 0 {
		b.WriteString("\n-- warnings --\n")
		for _, d := range res.Warnings {
			b.WriteString(d.Format(path, lines) + "\n")
		}
	}
	return b.String()
}

func runGolden(t *testing.T, dir string, opts Options) {
	files, err := filepath.Glob(filepath.Join(dir, "*.orbit"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden inputs in %s: %v", dir, err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(strings.TrimSuffix(name, ".orbit"), func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			o := opts
			o.Path = name
			got := render(name, string(src), o)
			golden := strings.TrimSuffix(file, ".orbit") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/format -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s: got\n%s\nwant\n%s\n%s", name, got, want, Diff(name, string(want), got))
			}
			// Idempotence: formatting the output changes nothing.
			out, res := Source(string(src), o)
			if len(res.Errors) == 0 {
				again, res2 := Source(out, o)
				if again != out || len(res2.Errors) > 0 {
					t.Errorf("%s: not idempotent:\n%s%v", name, Diff(name, out, again), res2.Errors)
				}
			}
		})
	}
}

func TestGolden(t *testing.T) {
	runGolden(t, "testdata", Options{})
}

func TestGoldenBun(t *testing.T) {
	bun := findBun(t)
	dir, err := filepath.Abs("testdata/bun")
	if err != nil {
		t.Fatal(err)
	}
	runGolden(t, "testdata/bun", Options{Bun: bun, BunDir: dir})
}
