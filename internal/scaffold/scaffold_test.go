package scaffold

import (
	"errors"
	"go/format"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var opts = Options{Module: "example.com/blog", Toolchain: "go1.27.3", Version: "v0.1.0"}

func TestFiles(t *testing.T) {
	files, err := Files(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".dockerignore",
		".gitignore",
		"Dockerfile",
		"cmd/server/main.go",
		"go.mod",
		"public/favicon.svg",
		"src/env.d.ts",
		"src/layouts/Base.orbit",
		"src/pages/404.orbit",
		"src/pages/index.orbit",
		"tsconfig.json",
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("files:\n%q\nwant:\n%q", got, want)
	}

	gomod := "module example.com/blog\n\ngo 1.27\n\ntoolchain go1.27.3\n\ntool github.com/orbit-z/orbit/cmd/orbit\n\nrequire github.com/orbit-z/orbit v0.1.0\n"
	if got := string(files["go.mod"]); got != gomod {
		t.Errorf("go.mod:\n%s\nwant:\n%s", got, gomod)
	}
	main := files["cmd/server/main.go"]
	if !strings.Contains(string(main), "\t\"example.com/blog/orbit_gen/app\"\n") ||
		!strings.Contains(string(main), "if err := orbit.ListenAndServe(mux); err != nil {\n\t\tlog.Fatal(err)\n\t}") {
		t.Errorf("main.go:\n%s", main)
	}
	if f, err := format.Source(main); err != nil || string(f) != string(main) {
		t.Errorf("main.go isn't gofmt-clean (%v)", err)
	}
	docker := string(files["Dockerfile"])
	for _, s := range []string{
		"FROM golang:1.27.3 AS build\nWORKDIR /src\n",
		"COPY go.mod go.sum ./\nRUN go mod download\n",
		"RUN CGO_ENABLED=0 go tool orbit build --embed\n",
		"FROM gcr.io/distroless/static-debian12:nonroot\nCOPY --from=build /src/dist/server /server\nENV PORT=8080\nEXPOSE 8080\nUSER nonroot:nonroot\nENTRYPOINT [\"/server\"]\n",
	} {
		if !strings.Contains(docker, s) {
			t.Errorf("Dockerfile lacks %q:\n%s", s, docker)
		}
	}
	if got := string(files[".dockerignore"]); got != ".git\n.orbit\ndist\norbit_gen\n" {
		t.Errorf(".dockerignore: %q", got)
	}
	if got := string(files[".gitignore"]); got != "orbit_gen/\n.orbit/\ndist/\nnode_modules/\n" {
		t.Errorf(".gitignore: %q", got)
	}
	if !strings.Contains(string(files["src/layouts/Base.orbit"]), "<style>") {
		t.Error("Base.orbit has no <style>")
	}
	for _, s := range []string{`"allowImportingTsExtensions": true`, `"moduleResolution": "Bundler"`, `"include": ["src/**/*.ts", "orbit_gen/types/*.d.ts"]`, `"rootDirs": [".", "orbit_gen/types"]`, `"paths": { "@/*": ["./src/*"] }`} {
		if !strings.Contains(string(files["tsconfig.json"]), s) {
			t.Errorf("tsconfig.json lacks %s", s)
		}
	}
}

func TestFilesReplace(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/me/orbit":  "replace github.com/orbit-z/orbit => /home/me/orbit\n",
		"/home/me/my src": "replace github.com/orbit-z/orbit => \"/home/me/my src\"\n",
	} {
		o := opts
		o.Version, o.Replace = "v0.0.0", dir
		files, err := Files(o)
		if err != nil {
			t.Fatal(err)
		}
		got := string(files["go.mod"])
		if !strings.HasSuffix(got, "require github.com/orbit-z/orbit v0.0.0\n\n"+want) {
			t.Errorf("go.mod:\n%s\nwant suffix %q", got, want)
		}
	}
}

func TestFallbackToolchain(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\ntoolchain "+FallbackToolchain+"\n") {
		t.Errorf("FallbackToolchain %s differs from the toolchain line of the framework's go.mod", FallbackToolchain)
	}
	for v, want := range map[string]string{
		"go1.27.3":                        "go1.27.3",
		"go1.28.0":                        "go1.28.0",
		"go1.28rc1":                       "go1.28rc1",
		"devel go1.28-0123abcd Mon Jan 1": FallbackToolchain,
		"go1.27.3 X:nocoverageredesign":   FallbackToolchain,
		"go1.27":                          FallbackToolchain,
	} {
		if got := toolchain(v); got != want {
			t.Errorf("toolchain(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	checkout := t.TempDir() // a git checkout of Orbit
	write(t, checkout, "go.mod", "module github.com/orbit-z/orbit\n\ngo 1.27\n")
	write(t, checkout, ".git", "gitdir: elsewhere\n")
	cache := t.TempDir() // a module cache copy: no .git
	write(t, cache, "go.mod", "module github.com/orbit-z/orbit\n\ngo 1.27\n")
	other := t.TempDir()
	write(t, other, "go.mod", "module example.com/other\n")

	const pseudo = "v0.0.0-20261025162608-abae5f8263d5"
	tests := []struct {
		version, src, env string
		want              Source
		err               string
	}{
		{version: "v0.1.0", src: checkout, want: Source{Version: "v0.1.0"}},
		{version: "v0.2.0-rc.1", want: Source{Version: "v0.2.0-rc.1"}},
		{version: "v0.1.0", env: checkout, want: Source{Version: "v0.0.0", Replace: checkout}},
		{version: "v0.1.0", env: other, err: "not a checkout of github.com/orbit-z/orbit"},
		{version: "(devel)", src: checkout, want: Source{Version: "v0.0.0", Replace: checkout}},
		{version: "", src: checkout, want: Source{Version: "v0.0.0", Replace: checkout}},
		{version: pseudo, src: checkout, want: Source{Version: "v0.0.0", Replace: checkout}},
		{version: pseudo + "+dirty", src: checkout, want: Source{Version: "v0.0.0", Replace: checkout}},
		{version: pseudo, src: cache, want: Source{Version: pseudo}},
		{version: pseudo + "+dirty", src: cache, err: "development build (version " + pseudo + "+dirty)"},
		{version: "(devel)", src: "", err: "set ORBIT_REPLACE=/path/to/orbit"},
		{version: "(devel)", src: other, err: "set ORBIT_REPLACE"},
	}
	for _, tt := range tests {
		got, err := resolve(tt.version, tt.src, tt.env)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("resolve(%q, %q, %q): error %v, want %q", tt.version, tt.src, tt.env, err, tt.err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("resolve(%q, %q, %q) = %+v, %v; want %+v", tt.version, tt.src, tt.env, got, err, tt.want)
		}
	}
}

func TestCheckModulePath(t *testing.T) {
	for _, p := range []string{"blog", "example.com/blog", "github.com/me/my-site", "a/b_c/d~e/v2"} {
		if err := CheckModulePath(p); err != nil {
			t.Errorf("CheckModulePath(%q): %v", p, err)
		}
	}
	for _, p := range []string{"", "my site", "a//b", "/abs", "a/", ".hidden", "-x", "a/../b", "é"} {
		if err := CheckModulePath(p); err == nil {
			t.Errorf("CheckModulePath(%q): no error", p)
		}
	}
}

func TestCreate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new", "blog")
	if err := Create(dir, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "src", "pages", "index.orbit"))
	if err != nil || !strings.Contains(string(data), `Base "../layouts/Base.orbit"`) {
		t.Errorf("index.orbit: %v\n%s", err, data)
	}
	if err := Create(dir, opts); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("Create into a non-empty dir: %v", err)
	}

	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Create(empty, opts); err != nil {
		t.Errorf("Create into an empty dir: %v", err)
	}
	file := filepath.Join(base, "file")
	write(t, base, "file", "x")
	if err := CheckDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("CheckDir(file): %v", err)
	}
	o := opts
	o.Module = "my site"
	if err := Create(filepath.Join(base, "bad"), o); err == nil || !strings.Contains(err.Error(), "malformed module path") {
		t.Errorf("Create with a bad module path: %v", err)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
