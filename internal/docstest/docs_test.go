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
//     one, replacing a block for the same PATH), which must fail with the
//     documented error. The documented error is the next untagged fenced
//     block: its lines that start with "PATH:" must all appear in the
//     output, or, when none does, its first line. So several expect-error
//     blocks can share one error block.
//   - expect-warning is the same, except that the project must build: the
//     documented lines are warnings.
//   - An expect-error or expect-warning block with at=N is a fragment: its
//     lines replace as many lines of the page's block for the same PATH,
//     from line N, so a page can show just the line that changes. With
//     at=N-M, they replace lines N to M (to add or remove lines).
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
	"strconv"
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
var pageStubs = map[string]map[string]string{
	// The import cycle example: B.orbit imports A.orbit, whose
	// expect-error version imports B.orbit back.
	// The name clash examples import Item.orbit.
	"components.md": {
		"src/components/A.orbit":    "---\ntype Props struct {\n    Name string\n}\n---\n",
		"src/components/Item.orbit": "---\ntype Props struct {\n    Name string\n}\n---\n<li>{props.Name}</li>\n",
	},
	// The indirect import example: src/locals imports internal/auth, which
	// imports orbit_gen/app.
	"middleware.md": {"internal/auth/auth.go": "package auth\n\nimport _ \"example.com/blog/orbit_gen/app\"\n\ntype User struct{ Name string }\n"},
	// The Go type errors example: a page passes a string to Card's bool
	// prop, and a Card has a typo in a posts.Post field.
	"troubleshooting.md": {
		"internal/posts/posts.go":   stubPosts,
		"src/components/Card.orbit": stubCard,
	},
	// The error output examples pass wrong props to a Card.
	"reference/cli.md": {
		"internal/posts/posts.go":   stubPosts,
		"src/components/Card.orbit": stubCard,
	},
}

// stubPosts and stubCard are the posts package and Card component of the
// components page, cut down.
const (
	stubPosts = "package posts\n\ntype Post struct {\n\tSlug  string\n\tTitle string\n}\n\nfunc Find(slug string) (Post, error) { return Post{Slug: slug}, nil }\n"
	stubCard  = "---\nimport \"example.com/blog/internal/posts\"\n\ntype Props struct {\n    Post     posts.Post\n    Featured bool\n}\n---\n<h2>{props.Post.Title}</h2>\n"
)

// pageSetup are go commands a page's docs tell you to run before go test,
// by page.
var pageSetup = map[string][][]string{
	// Create the golden file of the Snapshot example.
	"testing.md": {{"test", "./internal/views_test", "-update"}},
}

// skipPages are pages not built, with the Orbit bug that stops them.
var skipPages = map[string]string{}

// skipExamples are expect-error and expect-warning blocks not built, by
// page and PATH ("page PATH"), with the Orbit bug that makes their output
// differ from the documented one.
var skipExamples = map[string]string{
}

// block is a tagged code block.
type block struct {
	lang, file string
	line       int      // 1-based line of the opening fence
	expectKind string   // "error", "warning", or "" for a block that builds clean
	expect     []string // with expectKind: lines that must appear in the output
	at, atEnd  int      // with expectKind: the lines it replaces (see parseAt), or 0
	content    string
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
		for _, kind := range []string{"error", "warning"} {
			if _, ok := attrs["expect-"+kind]; ok {
				b.expectKind = kind
			}
		}
		if b.expectKind != "" {
			b.expect = expected(fences[i+1:], file)
			if b.expect == nil {
				t.Errorf("line %d: expect-%s block isn't followed by the %[2]s", f.line, b.expectKind)
				continue
			}
			if at, ok := attrs["at"]; ok {
				if b.at, b.atEnd, ok = parseAt(at); !ok {
					t.Errorf("line %d: invalid at=%s", f.line, at)
					continue
				}
			}
		}
		out = append(out, b)
	}
	return out
}

// expected returns the documented output of an expect-error or
// expect-warning block for file: the lines of the next untagged fence that
// start with "file:", or its first line when none does. It returns nil
// without such a fence.
func expected(fences []fence, file string) []string {
	for _, f := range fences {
		if _, attrs := parseInfo(f.info); attrs["file"] != "" {
			continue
		}
		var out []string
		for line := range strings.Lines(f.content) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, file+":") {
				out = append(out, line)
			}
		}
		if len(out) == 0 {
			first, _, _ := strings.Cut(strings.TrimSpace(f.content), "\n")
			out = []string{first}
		}
		return out
	}
	return nil
}

// parseAt parses an at= value: "N" (from line N, as many lines as the
// fragment has; end is 0) or "N-M" (lines N to M).
func parseAt(s string) (from, end int, ok bool) {
	a, b, isRange := strings.Cut(s, "-")
	from, err := strconv.Atoi(a)
	if err != nil || from < 1 {
		return 0, 0, false
	}
	if isRange {
		end, err = strconv.Atoi(b)
		if err != nil || end < from {
			return 0, 0, false
		}
	}
	return from, end, true
}

// splice returns base with its lines from to end (1-based, inclusive; 0
// for as many lines as frag has) replaced by the lines of frag, or false
// when they run past the end of base.
func splice(base, frag string, from, end int) (string, bool) {
	lines := strings.SplitAfter(strings.TrimSuffix(base, "\n"), "\n")
	repl := strings.SplitAfter(strings.TrimSuffix(frag, "\n"), "\n")
	if end == 0 {
		end = from + len(repl) - 1
	}
	if end > len(lines) {
		return "", false
	}
	for i := range lines {
		if !strings.HasSuffix(lines[i], "\n") {
			lines[i] += "\n"
		}
	}
	for i := range repl {
		if !strings.HasSuffix(repl[i], "\n") {
			repl[i] += "\n"
		}
	}
	return strings.Join(slices.Replace(lines, from-1, end, repl...), ""), true
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
				if b.expectKind != "" {
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
				if b.at > 0 {
					i := slices.IndexFunc(good, func(g block) bool { return g.file == b.file })
					if i < 0 {
						t.Errorf("docs/%s:%d: at= is set, but no other block is tagged file=%s", rel, b.line, b.file)
						continue
					}
					var ok bool
					if b.content, ok = splice(good[i].content, b.content, b.at, b.atEnd); !ok {
						t.Errorf("docs/%s:%d: at= runs past the end of %s (docs/%s:%d)", rel, b.line, b.file, rel, good[i].line)
						continue
					}
				}
				files := slices.DeleteFunc(slices.Clone(good), func(g block) bool { return g.file == b.file })
				// Each error example is a project of its own; build them
				// in parallel.
				t.Run(fmt.Sprintf("line%d", b.line), func(t *testing.T) {
					if bug, ok := skipExamples[rel+" "+b.file]; ok {
						t.Skip("TODO(orbit bug): " + bug)
					}
					t.Parallel()
					ok, out := buildProject(t, starter, pageStubs[rel], nil, append(files, b), &bunNotice)
					switch {
					case b.expectKind == "warning" && !ok:
						t.Errorf("docs/%s:%d: %s doesn't build, but the docs show it building with warnings:\n%s", rel, b.line, b.file, out)
						return
					case b.expectKind == "warning":
					case ok && bunSkipped.Load():
						t.Logf("docs/%s:%d: %s builds; its error may be a TypeScript one, which needs Bun", rel, b.line, b.file)
						return
					case ok:
						t.Errorf("docs/%s:%d: %s builds, but the docs show it failing with %q", rel, b.line, b.file, b.expect)
						return
					}
					for _, want := range b.expect {
						if !strings.Contains(out, want) {
							t.Errorf("docs/%s:%d: %s output doesn't have the documented %q:\n%s", rel, b.line, b.file, want, out)
						}
					}
				})
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
		return false, "orbit: " + err.Error() // as orbit check prints it
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

func TestSplice(t *testing.T) {
	base := "a\nb\nc\nd\n"
	tests := []struct {
		at, frag string
		want     string
		ok       bool
	}{
		{"2", "B\n", "a\nB\nc\nd\n", true},
		{"2", "B\nC", "a\nB\nC\nd\n", true},
		{"2-3", "X\n", "a\nX\nd\n", true},
		{"1-1", "X\nY\n", "X\nY\nb\nc\nd\n", true},
		{"4", "D\nE\n", "", false},
		{"3-5", "X\n", "", false},
	}
	for _, tt := range tests {
		from, end, ok := parseAt(tt.at)
		if !ok {
			t.Fatalf("parseAt(%q) failed", tt.at)
		}
		got, ok := splice(base, tt.frag, from, end)
		if got != tt.want || ok != tt.ok {
			t.Errorf("splice at=%s %q = %q, %v; want %q, %v", tt.at, tt.frag, got, ok, tt.want, tt.ok)
		}
	}
	for _, bad := range []string{"", "0", "x", "3-2", "2-"} {
		if _, _, ok := parseAt(bad); ok {
			t.Errorf("parseAt(%q) succeeded", bad)
		}
	}
}

func TestExpected(t *testing.T) {
	src := "```orbit file=a.orbit expect-error\nx\n```\n\n```orbit file=b.orbit expect-error\ny\n```\n\n" +
		"```text\na.orbit:1:1: one\nb.orbit:2:1: two\n```\n"
	fences := parseFences(src)
	if got := expected(fences[1:], "a.orbit"); !slices.Equal(got, []string{"a.orbit:1:1: one"}) {
		t.Errorf("a.orbit: got %q", got)
	}
	if got := expected(fences[2:], "b.orbit"); !slices.Equal(got, []string{"b.orbit:2:1: two"}) {
		t.Errorf("b.orbit: got %q", got)
	}
	if got := expected(fences[2:], "c.orbit"); !slices.Equal(got, []string{"a.orbit:1:1: one"}) {
		t.Errorf("c.orbit: got %q, want the first line", got)
	}
	if got := expected(nil, "a.orbit"); got != nil {
		t.Errorf("no fence: got %q", got)
	}
}
