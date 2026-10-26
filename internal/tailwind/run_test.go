package tailwind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv is a lookup environment: commands on PATH, --help outputs by
// binary.
func fakeEnv(path map[string]string, help map[string]string) Env {
	return Env{
		LookPath: func(name string) (string, error) {
			if p, ok := path[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		Help: func(ctx context.Context, bin string, args []string) ([]byte, error) {
			key := strings.Join(append([]string{bin}, args...), " ")
			if out, ok := help[key]; ok {
				return []byte(out), nil
			}
			return nil, errors.New("exit status 1")
		},
		GOOS: "linux", GOARCH: "amd64",
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "node_modules", ".bin")
	os.MkdirAll(bin, 0o755)
	local := filepath.Join(bin, "tailwindcss")

	const v4 = "≈ tailwindcss v4.3.3\n\nUsage:"
	tests := []struct {
		name       string
		script     string // node_modules/.bin/tailwindcss, "" for none
		path, help map[string]string
		want       string // Describe, or the error
	}{
		{"PATH", "", map[string]string{"tailwindcss": "/usr/bin/tailwindcss"}, map[string]string{"/usr/bin/tailwindcss": v4},
			"tailwindcss 4.3.3 (/usr/bin/tailwindcss)"},
		{"node_modules with node, before PATH", "#!/usr/bin/env node\n", map[string]string{"node": "/n", "tailwindcss": "/usr/bin/tailwindcss"},
			map[string]string{local: "≈ tailwindcss v4.2.0", "/usr/bin/tailwindcss": v4}, "tailwindcss 4.2.0 (" + local + ")"},
		{"node_modules with bun", "#!/usr/bin/env node\n", map[string]string{"bun": "/b"}, map[string]string{"/b " + local: v4},
			"tailwindcss 4.3.3 (" + local + ")"},
		{"node_modules without node or bun", "#!/usr/bin/env node\n", map[string]string{"tailwindcss": "/t"}, map[string]string{"/t": v4},
			"tailwindcss 4.3.3 (/t)"},
		{"native binary in node_modules", "\x7fELF", nil, map[string]string{local: v4}, "tailwindcss 4.3.3 (" + local + ")"},
		{"v3 refused, bun x", "", map[string]string{"tailwindcss": "/t", "bun": "/b"}, map[string]string{"/t": "tailwindcss v3.4.17\n"},
			"Tailwind CSS 4.3.3 through bun x"},
		{"v3 refused, nothing else", "", map[string]string{"tailwindcss": "/t"}, map[string]string{"/t": "tailwindcss v3.4.17\n"},
			"the Tailwind CSS CLI (v4) was not found; /t is Tailwind CSS 3.4.17, and Orbit needs v4"},
		{"not found", "", nil, nil, "the Tailwind CSS CLI (v4) was not found"},
		{"broken", "", map[string]string{"tailwindcss": "/t"}, nil, "the Tailwind CSS CLI (v4) was not found; /t --help: exit status 1 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(local)
			if tt.script != "" {
				os.WriteFile(local, []byte(tt.script), 0o755)
			}
			c, err := Find(t.Context(), fakeEnv(tt.path, tt.help), root)
			got := ""
			if err != nil {
				got = err.Error()
			} else {
				got = c.Describe()
			}
			if got != tt.want {
				t.Errorf("Find = %q, want %q", got, tt.want)
			}
		})
	}

	// The bun x command line.
	c, err := Find(t.Context(), fakeEnv(map[string]string{"bun": "/b"}, nil), t.TempDir())
	if err != nil || c.Bin != "/b" || strings.Join(c.Args, " ") != "x --package @tailwindcss/cli@"+Pinned+" tailwindcss" {
		t.Errorf("bun x: %+v, %v", c, err)
	}
}

func TestInstall(t *testing.T) {
	got := Install("linux", "amd64")
	for _, want := range []string{"releases/download/v" + Pinned + "/tailwindcss-linux-x64", "chmod +x", "brew install tailwindcss", "bun add -d @tailwindcss/cli@" + Pinned} {
		if !strings.Contains(got, want) {
			t.Errorf("Install = %q, has no %q", got, want)
		}
	}
	if got := Install("windows", "amd64"); !strings.Contains(got, "tailwindcss-windows-x64.exe") || strings.Contains(got, "brew") {
		t.Errorf("Install(windows) = %q", got)
	}
	if a := ReleaseAsset("darwin", "arm64"); a != "tailwindcss-macos-arm64" {
		t.Errorf("ReleaseAsset = %q", a)
	}
}

func TestCleanMessage(t *testing.T) {
	stderr := "≈ tailwindcss v4.3.3\n\n\x1b[31mError:\x1b[39m\n\x1b[2m┌\x1b[22m\n\x1b[2m│\x1b[22m CssSyntaxError: stdin.css:2:1: Missing closing } at .a\n\x1b[2m└\x1b[22m\n"
	if got := CleanMessage(stderr); got != "stdin.css:2:1: Missing closing } at .a" {
		t.Errorf("CleanMessage = %q", got)
	}
}

func TestCLIError(t *testing.T) {
	in := Inline(Source{File: "/p/x.orbit", Line: 10, Col: 3, Dir: "/p",
		Text: ".a { color: red }\n  .b { @apply p-2 nope; }\n"}, InlineOptions{})
	tests := []struct {
		stderr, want string
	}{
		{"│ Error: Cannot apply unknown utility class `nope`. Are you using CSS modules or similar and missing `@reference`? https://x\n",
			"/p/x.orbit:11:8: Cannot apply unknown utility class `nope`."},
		{"│ CssSyntaxError: stdin.css:2:3: Missing closing } at .a\n", "/p/x.orbit:11:3: Missing closing } at .a"},
		{"│ Error: Can't resolve 'daisyui' in '/p'\n", "/p/x.orbit:10:3: Can't resolve 'daisyui' in '/p'"},
		{"", "/p/x.orbit:10:3: tailwindcss failed"},
	}
	for _, tt := range tests {
		if got := in.cliError(tt.stderr).Error(); got != tt.want {
			t.Errorf("cliError(%q) = %q, want %q", tt.stderr, got, tt.want)
		}
	}
}

// fakeCLI writes a shell-script CLI that echoes its input after a
// counter, and returns a lookup for it.
func fakeCLI(t *testing.T, script string) func(context.Context) (CLI, error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	p := filepath.Join(t.TempDir(), "tailwindcss")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (CLI, error) { return CLI{Bin: p, Path: p, Via: "PATH"}, nil }
}

func TestProcessor(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	find := fakeCLI(t, `echo x >> `+count+`; cat; echo "/* $(wc -l < `+count+`) */"`)
	p := NewProcessorWith(dir, find)
	root := Input{Inlined: Inline(Source{Text: "@import \"tailwindcss\";", Dir: dir, File: dir + "/g.css"}, InlineOptions{}), Dir: dir, Root: true}
	block := Input{Inlined: Inline(Source{Text: ".a { @apply x; }", Dir: dir, File: dir + "/c.orbit"}, InlineOptions{}), Dir: dir}
	process := func(in Input) string {
		t.Helper()
		out, err := p.Process(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if p.Active() {
		t.Error("active before any input")
	}
	process(root)
	process(block)
	if out := process(root); out != "@import \"tailwindcss\";/* 1 */\n" || p.Runs() != 2 || !p.Active() {
		t.Errorf("cached output %q, %d runs", out, p.Runs())
	}

	// Sources changed: Rerun starts the root again; its output changed.
	p.Invalidate(false, true)
	pd := p.Rerun(t.Context())
	if !pd.Wait() || p.Runs() != 3 {
		t.Errorf("rerun: %d runs", p.Runs())
	}
	if out := process(root); !strings.Contains(out, "/* 3 */") || p.Runs() != 3 {
		t.Errorf("after rerun: %q, %d runs", out, p.Runs())
	}
	process(block)
	if p.Runs() != 3 {
		t.Errorf("block reran after a source change")
	}
	// CSS changed: both rerun on use.
	p.Invalidate(true, false)
	process(root)
	process(block)
	if p.Runs() != 5 {
		t.Errorf("after a CSS change: %d runs, want 5", p.Runs())
	}
	// Sweep forgets what the last bundle didn't use.
	p.Begin()
	process(root)
	p.Sweep()
	if len(p.entries) != 1 {
		t.Errorf("Sweep kept %d entries", len(p.entries))
	}

	// An unchanged output.
	q := NewProcessorWith(dir, fakeCLI(t, "cat"))
	if _, err := q.Process(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	q.Invalidate(false, true)
	if q.Rerun(t.Context()).Wait() {
		t.Error("same output reported as changed")
	}

	// A failing run: the error is cached until DropErrors.
	f := NewProcessorWith(dir, fakeCLI(t, "cat >/dev/null; echo '│ Error: boom' >&2; exit 1"))
	_, err := f.Process(t.Context(), block)
	var te *Error
	if !errors.As(err, &te) || te.Msg != "boom" || te.File != dir+"/c.orbit" || te.Line != 1 {
		t.Fatalf("error: %v", err)
	}
	f.Process(t.Context(), block)
	if f.Runs() != 1 {
		t.Errorf("error not cached: %d runs", f.Runs())
	}
	f.DropErrors()
	f.Process(t.Context(), block)
	if f.Runs() != 2 {
		t.Errorf("DropErrors: %d runs", f.Runs())
	}

	// No CLI: the error at the marker.
	n := NewProcessorWith(dir, func(context.Context) (CLI, error) { return CLI{}, &NotFound{} })
	in := Input{Inlined: Inline(Source{Text: "/* x */\n@import \"tailwindcss\";", Dir: dir, File: dir + "/g.css"}, InlineOptions{}), Dir: dir, Root: true, Marker: 8, MarkerName: `@import "tailwindcss"`}
	_, err = n.Process(t.Context(), in)
	if !errors.As(err, &te) || te.Line != 2 || te.Col != 1 || !strings.HasPrefix(te.Msg, `@import "tailwindcss" needs the Tailwind CSS CLI v4, which wasn't found; install it: `) {
		t.Errorf("not found: %v", err)
	}
}

func TestGate(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("src/a.orbit", `<p class="flex p-4">hi</p>`)
	write("orbit_gen/x.go", `"skipped-token"`)
	g := NewGate(dir, func(rel string) bool { return rel == "orbit_gen" })
	if g.Changed([]string{"src/a.orbit"}) {
		t.Error("unchanged file seen as new")
	}
	write("src/a.orbit", `<p class="flex p-4">hi</p>  <p`)
	if g.Changed([]string{"src/a.orbit"}) {
		t.Error("repeated tokens seen as new")
	}
	write("src/b.go", `x := "skipped-token"`)
	if !g.Changed([]string{"src/b.go"}) {
		t.Error("tokens of a skipped directory were seeded")
	}
	write("src/a.orbit", `<p class="flex p-8">hi</p>`)
	if !g.Changed([]string{"src/a.orbit"}) {
		t.Error("new class not seen")
	}
	if g.Changed([]string{"src/a.orbit", "src/gone.orbit", "src/x.css"}) {
		t.Error("second look, a removed file or a .css file seen as new")
	}
	os.MkdirAll(filepath.Join(dir, "src/new"), 0o755)
	if !g.Changed([]string{"src/new"}) {
		t.Error("a directory isn't new")
	}
}
