// Package docstest builds the code examples of docs/ (PLAN.md §11
// milestone 11, §13), so the docs can't go stale.
//
// Rules:
//   - Only fenced code blocks whose info string has file=PATH are built
//     (```orbit file=src/pages/index.orbit). Untagged blocks are fragments
//     and are ignored. A PATH starting with ~ or / is outside the project
//     (editor config) and is skipped.
//   - Each docs page is its own project: the orbit new starter (module
//     example.com/blog, Orbit replaced by this checkout), then the page's
//     hidden stubs (pageStubs), then the page's tagged blocks at their
//     paths. One page tagging the same PATH twice is an error.
//   - The project must pass orbit check (generate, go vet of orbit_gen/
//     and, when Bun is available, bun check; without Bun the TypeScript half
//     is skipped with a notice), then go vet ./..., go build ./... and
//     go test ./... (the testing page's examples are tests), after the
//     page's setup commands (pageSetup).
//   - A block whose info string also has expect-error shows failing code:
//     it's built in a project of its own (the page's other blocks plus this
//     one, replacing a block for the same PATH), which must fail, and the
//     first line of the next fenced block (the documented error, with its
//     file:line:col) must appear in the output.
package docstest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bertrandkhe/orbit/internal/check"
	"github.com/bertrandkhe/orbit/internal/scaffold"
)

// goEnv runs the go command in a temp project: no workspace, and go.sum
// filled in as needed.
var goEnv = []string{"GOWORK=off", "GOFLAGS=-mod=mod"}

// bunSkipped is set when orbit check skipped the TypeScript half.
var bunSkipped atomic.Bool

// pageStubs are supporting files a page's examples use but don't show, by
// page (relative to docs/). An empty value removes a starter file.
var pageStubs = map[string]map[string]string{}

// pageSetup are go commands a page's docs tell you to run before go test,
// by page.
var pageSetup = map[string][][]string{
	// Create the golden file of the Snapshot example.
	"testing.md": {{"test", "./internal/views_test", "-update"}},
}

// skipPages are pages not built, with the Orbit bug that stops them.
var skipPages = map[string]string{}

// block is a tagged code block.
type block struct {
	lang, file  string
	line        int // 1-based line of the opening fence
	expectError string
	content     string
}

// fence is a fenced code block of a page.
type fence struct {
	info    string
	line    int
	content string
}

// parseFences returns the fenced code blocks of a Markdown file: ``` or
// longer, possibly indented (in a list), closed by a fence at least as long
// with no info string.
func parseFences(src string) []fence {
	var out []fence
	var cur *fence
	var indent, ticks int
	var body strings.Builder
	sc := bufio.NewScanner(strings.NewReader(src))
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " ")
		ind := len(line) - len(trimmed)
		k := len(trimmed) - len(strings.TrimLeft(trimmed, "`"))
		if cur == nil {
			if k >= 3 && !strings.Contains(trimmed[k:], "`") {
				cur = &fence{info: strings.TrimSpace(trimmed[k:]), line: n}
				indent, ticks = ind, k
				body.Reset()
			}
			continue
		}
		if k >= ticks && strings.TrimSpace(trimmed[k:]) == "" {
			cur.content = body.String()
			out = append(out, *cur)
			cur, indent = nil, 0
			continue
		}
		// Strip the fence's indentation from content lines.
		strip := min(indent, len(line)-len(strings.TrimLeft(line, " ")))
		body.WriteString(line[strip:])
		body.WriteByte('\n')
	}
	return out
}

// parseInfo splits an info string into its language and key=value
// attributes (values may be double-quoted); a bare word is a key with an
// empty value.
func parseInfo(info string) (lang string, attrs map[string]string) {
	attrs = map[string]string{}
	f := strings.Fields(info)
	if len(f) == 0 {
		return "", attrs
	}
	lang = f[0]
	for _, a := range f[1:] {
		k, v, _ := strings.Cut(a, "=")
		attrs[k] = strings.Trim(v, `"`)
	}
	return lang, attrs
}

// pageBlocks returns the tagged blocks of a page.
func pageBlocks(t *testing.T, src string) []block {
	fences := parseFences(src)
	var out []block
	for i, f := range fences {
		lang, attrs := parseInfo(f.info)
		file, ok := attrs["file"]
		if !ok {
			continue
		}
		if file == "" {
			t.Errorf("line %d: empty file=", f.line)
			continue
		}
		b := block{lang: lang, file: file, line: f.line, content: f.content}
		if _, ok := attrs["expect-error"]; ok {
			if i+1 >= len(fences) {
				t.Errorf("line %d: expect-error block isn't followed by the error", f.line)
				continue
			}
			b.expectError, _, _ = strings.Cut(strings.TrimSpace(fences[i+1].content), "\n")
		}
		out = append(out, b)
	}
	return out
}

func TestDocs(t *testing.T) {
	if testing.Short() {
		t.Skip("builds projects with the go command")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(repoRoot, "docs")
	pages, err := filepath.Glob(filepath.Join(docs, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob(filepath.Join(docs, "*", "*.md"))
	pages = append(pages, more...)
	starter, err := scaffold.Files(scaffold.Options{
		Module:    "example.com/blog",
		Toolchain: scaffold.Toolchain(),
		Version:   "v0.0.0",
		Replace:   repoRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var bunNotice sync.Once
	t.Cleanup(func() { t.Logf("built the docs examples in %v", time.Since(start).Round(time.Millisecond)) })
	built := 0
	for _, page := range pages {
		rel, _ := filepath.Rel(docs, page)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		blocks := pageBlocks(t, string(src))
		if len(blocks) == 0 {
			continue
		}
		built++
		t.Run(rel, func(t *testing.T) {
			if bug, ok := skipPages[rel]; ok {
				t.Skip("TODO(orbit bug): " + bug)
			}
			t.Parallel()
			var good []block
			var bad []block
			seen := map[string]int{}
			for _, b := range blocks {
				if strings.HasPrefix(b.file, "~") || strings.HasPrefix(b.file, "/") {
					continue
				}
				if b.expectError != "" {
					bad = append(bad, b)
					continue
				}
				if l, dup := seen[b.file]; dup {
					t.Errorf("%s:%d: file=%s is also tagged at line %d", rel, b.line, b.file, l)
				}
				seen[b.file] = b.line
				good = append(good, b)
			}
			// A table of where each file comes from, for failures.
			var origin strings.Builder
			for _, b := range good {
				fmt.Fprintf(&origin, "\n  %s ← docs/%s:%d", b.file, rel, b.line)
			}

			ok, out := buildProject(t, starter, pageStubs[rel], pageSetup[rel], good, &bunNotice)
			if !ok {
				t.Errorf("examples don't build:%s\n%s", origin.String(), out)
			} else if out != "" {
				t.Logf("warnings:\n%s", out)
			}
			for _, b := range bad {
				files := slices.DeleteFunc(slices.Clone(good), func(g block) bool { return g.file == b.file })
				ok, out := buildProject(t, starter, pageStubs[rel], nil, append(files, b), &bunNotice)
				switch {
				case ok && bunSkipped.Load():
					t.Logf("docs/%s:%d: %s builds; its error may be a TypeScript one, which needs Bun", rel, b.line, b.file)
				case ok:
					t.Errorf("docs/%s:%d: %s builds, but the docs show it failing with %q", rel, b.line, b.file, b.expectError)
				case !strings.Contains(out, b.expectError):
					t.Errorf("docs/%s:%d: %s fails, but not with the documented %q:\n%s", rel, b.line, b.file, b.expectError, out)
				}
			}
		})
	}
	if built == 0 {
		t.Fatal("no tagged code blocks found in docs/")
	}
}

// buildProject writes a project and builds it. It returns whether every
// step passed, and the output of the steps.
func buildProject(t *testing.T, starter map[string][]byte, stubs map[string]string, setup [][]string, blocks []block, bunNotice *sync.Once) (bool, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{}
	for k, v := range starter {
		files[k] = v
	}
	for k, v := range stubs {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = []byte(v)
	}
	for _, b := range blocks {
		files[b.file] = []byte(b.content)
	}
	for rel, data := range files {
		name := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	var out bytes.Buffer
	res, err := check.Run(ctx, check.Options{Root: dir, Env: goEnv})
	if err != nil {
		return false, "orbit check: " + err.Error()
	}
	for _, d := range res.Generate.Diagnostics {
		fmt.Fprintln(&out, d)
	}
	for _, w := range res.Generate.Warnings {
		fmt.Fprintln(&out, w)
	}
	out.Write(res.GoOutput)
	out.Write(res.TSOutput)
	if res.Notice != "" {
		bunSkipped.Store(true)
		bunNotice.Do(func() { t.Log(res.Notice) })
	}
	if res.Failed() {
		return false, out.String()
	}
	steps := append([][]string{{"vet", "./..."}, {"build", "./..."}}, setup...)
	for _, args := range append(steps, []string{"test", "./..."}) {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), goEnv...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(&out, "go %s: %v\n%s", strings.Join(args, " "), err, b)
			return false, out.String()
		}
	}
	return true, out.String()
}
