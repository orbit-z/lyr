package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/pin"
	"github.com/bertrandkhe/orbit/internal/project"
)

const healthyGoMod = `module example.com/blog

go 1.27

toolchain go1.27.3

tool github.com/bertrandkhe/orbit/cmd/orbit

require github.com/bertrandkhe/orbit v0.1.0
`

// fakes is a fake world: commands on PATH, their outputs and binaries'
// build info.
type fakes struct {
	path   map[string]string // command → its path; missing: not found
	out    map[string]string // "name args…" → stdout
	errs   map[string]error  // "name args…" → error
	bi     map[string]*debug.BuildInfo
	self   pin.Self
	gen    *project.WriteResult
	diags  []project.Diagnostic
	genErr error
}

func healthy() *fakes {
	return &fakes{
		path: map[string]string{"go": "/usr/bin/go", "gopls": "/bin/gopls", "bun": "/bin/bun"},
		out: map[string]string{
			"go env GOVERSION GOTOOLCHAIN": "go1.27.3\nauto\n",
			"go list -f {{with .Module}}{{.Version}}{{with .Replace}} => {{.Path}}{{end}}{{end}} github.com/bertrandkhe/orbit/cmd/orbit": "v0.1.0\n",
			"/bin/bun --version": "1.4.4\n",
		},
		errs: map[string]error{},
		bi: map[string]*debug.BuildInfo{
			"/bin/gopls": {GoVersion: "go1.27.3", Main: debug.Module{Path: "golang.org/x/tools/gopls", Version: "v0.24.0"}},
		},
		self: pin.Self{Version: "v0.1.0"},
		gen:  &project.WriteResult{},
	}
}

func (f *fakes) env(dir string) Env {
	return Env{
		Dir: dir,
		LookPath: func(file string) (string, error) {
			if p, ok := f.path[file]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			if err := f.errs[key]; err != nil {
				return nil, err
			}
			out, ok := f.out[key]
			if !ok {
				return nil, errors.New("unexpected command: " + key)
			}
			return []byte(out), nil
		},
		BuildInfo: func(path string) (*debug.BuildInfo, error) {
			if bi, ok := f.bi[path]; ok {
				return bi, nil
			}
			return nil, errors.New("not a Go binary")
		},
		Self: f.self,
		Generate: func(context.Context, string) (*project.WriteResult, []project.Diagnostic, error) {
			return f.gen, f.diags, f.genErr
		},
	}
}

// newProject writes a healthy project and returns its root.
func newProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                    healthyGoMod,
		".gitignore":                "orbit_gen/\n.orbit/\ndist/\nnode_modules/\n",
		"src/pages/index.orbit":     "<h1>Hi</h1>\n",
		"cmd/server/main.go":        "package main\n\nfunc main() {}\n",
		"orbit_gen/app/app.go":      compiler.GenHeader + "\n\npackage app\n",
		"src/locals/locals.go":      "package locals\n\ntype Locals struct{ RequestID string }\n",
		"internal/auth/auth.go":     "package auth\n",
		"src/components/Card.orbit": "<p>card</p>\n",
	}
	for rel, content := range files {
		write(t, dir, rel, content)
	}
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var projectChecks = []string{"go", "cli", "gopls", "bun", "orbit_gen", "gitignore", "config"}

func TestHealthy(t *testing.T) {
	dir := newProject(t)
	r := Run(context.Background(), healthy().env(filepath.Join(dir, "src", "pages")))
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
		if c.Status != OK || c.Fix != "" {
			t.Errorf("%s: %s %q (fix %q)", c.Name, c.Status, c.Message, c.Fix)
		}
	}
	if strings.Join(names, " ") != strings.Join(projectChecks, " ") {
		t.Errorf("checks %v, want %v", names, projectChecks)
	}
	if r.Failed() {
		t.Error("Failed")
	}
	var b bytes.Buffer
	r.WriteText(&b)
	want := "✓ go: go1.27.3 (go.mod: go 1.27, toolchain go1.27.3)\n" +
		"✓ cli: orbit v0.1.0 hands commands to the pinned go tool orbit (v0.1.0)\n" +
		"✓ gopls: v0.24.0, built with go1.27.3\n" +
		"✓ bun: 1.4.4\n" +
		"✓ orbit_gen: up to date\n" +
		"✓ gitignore: covers orbit_gen/, .orbit/ and dist/\n" +
		"✓ config: no orbit.json (defaults); server ./cmd/server\n"
	if b.String() != want {
		t.Errorf("text:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestChecks(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, dir string, f *fakes)
		check  string
		status Status
		msg    string // substring
		fix    string
	}{
		{"old gopls first on PATH", func(t *testing.T, _ string, f *fakes) {
			f.bi["/bin/gopls"] = &debug.BuildInfo{GoVersion: "go1.23.4", Main: debug.Module{Version: "v0.18.1"}}
		}, "gopls", Error, "gopls v0.18.1 was built with go1.23.4, older than go 1.27 in go.mod", "go install golang.org/x/tools/gopls@latest"},
		{"gopls older than recommended", func(t *testing.T, _ string, f *fakes) {
			f.bi["/bin/gopls"] = &debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "v0.23.1"}}
		}, "gopls", Warning, "gopls v0.23.1 is older than the recommended v0.24.0", "go install golang.org/x/tools/gopls@latest"},
		{"gopls missing", func(t *testing.T, _ string, f *fakes) { delete(f.path, "gopls") },
			"gopls", Error, "not found on PATH", "go install golang.org/x/tools/gopls@latest"},
		{"bun missing", func(t *testing.T, _ string, f *fakes) { delete(f.path, "bun") },
			"bun", Warning, "not found (1.4.4 or later needed); orbit check will skip TypeScript type checking", "curl -fsSL https://bun.sh/install | bash"},
		{"bun old", func(t *testing.T, _ string, f *fakes) { f.out["/bin/bun --version"] = "1.3.2" },
			"bun", Warning, "1.3.2 is older than 1.4.4", "bun upgrade"},
		{"go missing", func(t *testing.T, _ string, f *fakes) { delete(f.path, "go") },
			"go", Error, "go command not found", installGo},
		{"go.mod go too old", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, "go.mod", strings.Replace(healthyGoMod, "go 1.27", "go 1.26", 1))
		}, "go", Error, "go.mod has go 1.26; Orbit needs go 1.27 or later", "go mod edit -go=1.27"},
		{"no toolchain line", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, "go.mod", strings.Replace(healthyGoMod, "toolchain go1.27.3\n", "", 1))
		}, "go", Warning, "no toolchain line, so go picks go1.27.0", "go get toolchain@go1.27.3"},
		{"active toolchain too old", func(t *testing.T, _ string, f *fakes) {
			f.out["go env GOVERSION GOTOOLCHAIN"] = "go1.26.2\nlocal\n"
		}, "go", Error, "the active toolchain go1.26.2 is older than go 1.27 in go.mod", "go env -w GOTOOLCHAIN=auto"},
		{"pinned CLI doesn't load", func(t *testing.T, _ string, f *fakes) {
			f.errs["go list -f {{with .Module}}{{.Version}}{{with .Replace}} => {{.Path}}{{end}}{{end}} github.com/bertrandkhe/orbit/cmd/orbit"] = errors.New("missing go.sum entry")
		}, "cli", Error, "go tool orbit can't load the pinned CLI: missing go.sum entry", "go mod tidy"},
		{"running pinned", func(t *testing.T, _ string, f *fakes) { f.self = pin.Self{Pinned: true, Version: "v0.1.0"} },
			"cli", OK, "running the project's pinned CLI (go tool orbit, v0.1.0)", ""},
		{"version mismatch", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, "go.mod", strings.Replace(healthyGoMod, "tool github.com/bertrandkhe/orbit/cmd/orbit\n", "", 1))
			f.self = pin.Self{Version: "v0.2.0"}
		}, "cli", Error, "this orbit is v0.2.0, but go.mod requires github.com/bertrandkhe/orbit v0.1.0 (no tool directive)", "go get -tool github.com/bertrandkhe/orbit/cmd/orbit@v0.1.0"},
		{"mismatch skipped with a local replace", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, "go.mod", strings.Replace(healthyGoMod, "tool github.com/bertrandkhe/orbit/cmd/orbit\n", "", 1)+"replace github.com/bertrandkhe/orbit => ../orbit\n")
			f.self = pin.Self{Version: "(devel)"}
		}, "cli", OK, "go.mod replaces github.com/bertrandkhe/orbit with ../orbit", ""},
		{"orbit_gen deleted", func(t *testing.T, dir string, f *fakes) { os.RemoveAll(filepath.Join(dir, "orbit_gen")) },
			"orbit_gen", Warning, "orbit_gen/ is missing", "orbit generate"},
		{"orbit_gen stale", func(t *testing.T, _ string, f *fakes) {
			f.gen = &project.WriteResult{Written: []string{"orbit_gen/impl/pages/index/render.go"}, Removed: []string{"orbit_gen/impl/x/render.go"}}
		}, "orbit_gen", Warning, "out of date: 2 files differ from what orbit generate writes (orbit_gen/impl/pages/index/render.go)", "orbit generate"},
		{"only the manifest differs", func(t *testing.T, _ string, f *fakes) {
			f.gen = &project.WriteResult{Written: []string{project.ManifestFile}}
		}, "orbit_gen", OK, "up to date", ""},
		{"sources have errors", func(t *testing.T, _ string, f *fakes) {
			f.diags = []project.Diagnostic{{File: "src/pages/index.orbit", Pos: compiler.Position{Line: 2, Col: 3}, Severity: compiler.SeverityError, Message: "unknown prop"}}
		}, "orbit_gen", Warning, "orbit generate fails with src/pages/index.orbit:2:3: unknown prop", "orbit generate"},
		{"bad orbit.json", func(t *testing.T, dir string, f *fakes) { write(t, dir, "orbit.json", `{"compressHTML": "none"}`) },
			"config", Error, `orbit.json: compressHTML must be "html" (or true), "jsx" or false, not "none"`, "fix orbit.json"},
		{"orbit.json syntax", func(t *testing.T, dir string, f *fakes) { write(t, dir, "orbit.json", "{\n  \"site\": 1,\n}") },
			"config", Error, "orbit.json:", "fix orbit.json"},
		{"server entry missing", func(t *testing.T, dir string, f *fakes) { write(t, dir, "orbit.json", `{"server": "./cmd/web"}`) },
			"config", Error, `orbit.json: server "./cmd/web" doesn't exist`, "create ./cmd/web, or remove server from orbit.json to use ./cmd/server"},
		{"generated server", func(t *testing.T, dir string, f *fakes) { os.RemoveAll(filepath.Join(dir, "cmd")) },
			"config", OK, "server: the generated default (no ./cmd/server)", ""},
		{"locals imports app", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, "src/locals/locals.go", "package locals\n\nimport _ \"example.com/blog/internal/auth\"\n\ntype Locals struct{}\n")
			write(t, dir, "internal/auth/auth.go", "package auth\n\nimport _ \"example.com/blog/orbit_gen/app\"\n")
		}, "config", Error, "src/locals/locals.go:3:10: src/locals can't import example.com/blog/orbit_gen/app", "move the code that needs orbit_gen/app from src/locals into cmd/server"},
		{"gitignore entries missing", func(t *testing.T, dir string, f *fakes) { write(t, dir, ".gitignore", "orbit_gen\nnode_modules/\n") },
			"gitignore", Warning, ".gitignore doesn't cover .orbit/, dist/", `printf '.orbit/\ndist/\n' >> .gitignore`},
		{"gitignore negated", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, ".gitignore", "/orbit_gen/\n.orbit/\ndist/**\n!orbit_gen\n")
		},
			"gitignore", Warning, ".gitignore doesn't cover orbit_gen/", `printf 'orbit_gen/\n' >> .gitignore`},
		{"no gitignore", func(t *testing.T, dir string, f *fakes) { os.Remove(filepath.Join(dir, ".gitignore")) },
			"gitignore", Warning, "no .gitignore, so orbit_gen/, .orbit/, dist/ aren't ignored", `printf 'orbit_gen/\n.orbit/\ndist/\n' >> .gitignore`},
		{"committed orbit_gen", func(t *testing.T, dir string, f *fakes) {
			write(t, dir, ".gitignore", "orbit_gen/static/\n.orbit/\ndist/\n")
		},
			"gitignore", OK, "covers orbit_gen/static/, .orbit/ and dist/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newProject(t)
			f := healthy()
			tt.setup(t, dir, f)
			r := Run(context.Background(), f.env(dir))
			if len(r.Checks) != len(projectChecks) {
				t.Fatalf("%d checks: %+v", len(r.Checks), r.Checks)
			}
			var got *Check
			for i := range r.Checks {
				c := &r.Checks[i]
				if c.Name == tt.check {
					got = c
				} else if c.Status != OK {
					t.Errorf("other check %s: %s %q", c.Name, c.Status, c.Message)
				}
			}
			if got.Status != tt.status || !strings.Contains(got.Message, tt.msg) || got.Fix != tt.fix {
				t.Errorf("%s: %s %q fix %q\nwant %s ~%q fix %q", tt.check, got.Status, got.Message, got.Fix, tt.status, tt.msg, tt.fix)
			}
			if r.Failed() != (tt.status == Error) {
				t.Errorf("Failed() = %v", r.Failed())
			}
		})
	}
}

func TestOutsideProject(t *testing.T) {
	f := healthy()
	f.out["go env GOVERSION GOTOOLCHAIN"] = "go1.23.4\nauto\n"
	f.bi["/bin/gopls"] = &debug.BuildInfo{GoVersion: "go1.26.0", Main: debug.Module{Version: "v0.21.0"}}
	r := Run(context.Background(), f.env(t.TempDir()))
	want := []Check{
		{Name: "go", Status: OK, Message: "go1.23.4 (switches to the Go 1.27 toolchain in projects, GOTOOLCHAIN=auto)"},
		{Name: "gopls", Status: Error, Message: "gopls v0.21.0 was built with go1.26.0, older than go 1.27, the oldest Go Orbit supports", Fix: installGopls},
		{Name: "bun", Status: OK, Message: "1.4.4"},
	}
	if len(r.Checks) != len(want) {
		t.Fatalf("checks: %+v", r.Checks)
	}
	for i, c := range r.Checks {
		if c != want[i] {
			t.Errorf("check %d: %+v\nwant %+v", i, c, want[i])
		}
	}

	// A module without src/ isn't a project either.
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n\ngo 1.27\n")
	if r := Run(context.Background(), healthy().env(dir)); len(r.Checks) != 3 {
		t.Errorf("module without src/: %+v", r.Checks)
	}

	f = healthy()
	f.out["go env GOVERSION GOTOOLCHAIN"] = "go1.23.4\nlocal\n"
	if c := Run(context.Background(), f.env(t.TempDir())).Checks[0]; c.Status != Warning || c.Fix != "go env -w GOTOOLCHAIN=auto" {
		t.Errorf("GOTOOLCHAIN=local: %+v", c)
	}
}
