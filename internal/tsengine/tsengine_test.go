package tsengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv is a machine with the given commands on PATH and native
// binaries whose --version prints versions[path].
type fakeEnv struct {
	path     map[string]string
	versions map[string]string
}

func (f fakeEnv) env() Env {
	return Env{
		LookPath: func(name string) (string, error) {
			if p, ok := f.path[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Version: func(_ context.Context, bin string) ([]byte, error) {
			if v, ok := f.versions[bin]; ok {
				return []byte("Version " + v + "\n"), nil
			}
			return nil, errors.New("exec failed")
		},
		GOOS: "linux", GOARCH: "amd64",
	}
}

func write(t *testing.T, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return name
}

// npmInstall lays out node_modules/typescript (version v) with its shim and,
// when native, the platform package's binary; it returns the package dir,
// the shim and the native binary.
func npmInstall(t *testing.T, nodeModules, v string, native bool) (pkg, shim, bin string) {
	pkg = filepath.Join(nodeModules, "typescript")
	write(t, filepath.Join(pkg, "package.json"), `{"name": "typescript", "version": "`+v+`"}`)
	shim = write(t, filepath.Join(pkg, "bin", "tsc"), "#!/usr/bin/env node\nimport \"../lib/tsc.js\";\n")
	if native {
		bin = write(t, filepath.Join(nodeModules, "@typescript", "typescript-linux-x64", "lib", "tsc"), "\x7fELF")
	}
	return pkg, shim, bin
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	t.Run("setting: native binary", func(t *testing.T) {
		bin := write(t, filepath.Join(t.TempDir(), "tsc"), "\x7fELF")
		e, err := Find(ctx, fakeEnv{versions: map[string]string{bin: "7.0.2"}}.env(), Options{Setting: bin})
		if err != nil || e.Bin != bin || e.Via != "typescriptPath" || e.Version != (Version{7, 0, 2}) {
			t.Fatalf("Find: %+v, %v", e, err)
		}
		if got := strings.Join(e.LSPArgs(), " "); got != "--lsp --stdio" {
			t.Errorf("args %q", got)
		}
	})
	t.Run("setting: package directory", func(t *testing.T) {
		pkg, _, bin := npmInstall(t, filepath.Join(t.TempDir(), "node_modules"), "7.0.2", true)
		e, err := Find(ctx, fakeEnv{}.env(), Options{Setting: pkg})
		if err != nil || e.Bin != bin {
			t.Fatalf("Find: %+v, %v; want %s", e, err, bin)
		}
	})
	t.Run("setting: bad is an error, no fallback", func(t *testing.T) {
		_, err := Find(ctx, fakeEnv{path: map[string]string{"bun": "/usr/bin/bun"}}.env(), Options{Setting: "/nope/tsc", BunX: true})
		if err == nil || !strings.Contains(err.Error(), "typescriptPath /nope/tsc") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("project node_modules before PATH", func(t *testing.T) {
		root := t.TempDir()
		_, _, bin := npmInstall(t, filepath.Join(root, "node_modules"), "7.1.0", true)
		other := write(t, filepath.Join(t.TempDir(), "tsc"), "\x7fELF")
		e, err := Find(ctx, fakeEnv{path: map[string]string{"tsc": other}, versions: map[string]string{other: "7.0.2"}}.env(), Options{Root: root})
		if err != nil || e.Bin != bin || e.Via != "node_modules" || e.Version != (Version{7, 1, 0}) {
			t.Fatalf("Find: %+v, %v; want %s", e, err, bin)
		}
	})
	t.Run("PATH shim resolved to the native binary, symlinks followed", func(t *testing.T) {
		g := filepath.Join(t.TempDir(), "global")
		_, shim, bin := npmInstall(t, filepath.Join(g, "node_modules"), "7.0.2", true)
		link := filepath.Join(t.TempDir(), "tsc")
		if err := os.Symlink(shim, link); err != nil {
			t.Fatal(err)
		}
		e, err := Find(ctx, fakeEnv{path: map[string]string{"tsc": link}}.env(), Options{Root: t.TempDir()})
		if err != nil || e.Bin != bin || e.Via != "PATH" {
			t.Fatalf("Find: %+v, %v; want %s", e, err, bin)
		}
	})
	t.Run("PATH TypeScript 5 skipped for bun x", func(t *testing.T) {
		_, shim, _ := npmInstall(t, filepath.Join(t.TempDir(), "node_modules"), "5.9.3", false)
		env := fakeEnv{path: map[string]string{"tsc": shim, "bun": "/usr/bin/bun"}}.env()
		e, err := Find(ctx, env, Options{BunX: true})
		if err != nil || e.Via != "bun x" || strings.Join(e.LSPArgs(), " ") != "x --package typescript@"+Pinned+" tsc --lsp --stdio" {
			t.Fatalf("Find: %+v, %v", e, err)
		}
		if e.Describe() != "TypeScript "+Pinned+" through bun x" {
			t.Errorf("Describe %q", e.Describe())
		}
		// Without bun: not found, naming the old one.
		_, err = Find(ctx, env, Options{})
		var nf *NotFound
		if !errors.As(err, &nf) || len(nf.TooOld) != 1 || nf.TooOld[0].Version != (Version{5, 9, 3}) {
			t.Fatalf("err %v", err)
		}
		if !strings.Contains(err.Error(), "TypeScript 5.9.3, which has no language server") {
			t.Errorf("message %q", err)
		}
	})
	t.Run("native tsc too old (TypeScript 6)", func(t *testing.T) {
		bin := write(t, filepath.Join(t.TempDir(), "tsc"), "\x7fELF")
		_, err := Find(ctx, fakeEnv{path: map[string]string{"tsc": bin}, versions: map[string]string{bin: "7.0.1-rc"}}.env(), Options{})
		var nf *NotFound
		if !errors.As(err, &nf) || len(nf.TooOld) != 1 {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("package without its platform package", func(t *testing.T) {
		root := t.TempDir()
		npmInstall(t, filepath.Join(root, "node_modules"), "7.0.2", false)
		_, err := Find(ctx, fakeEnv{}.env(), Options{Root: root})
		if err == nil || !strings.Contains(err.Error(), "@typescript/typescript-linux-x64 isn't installed") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		if _, err := Find(ctx, fakeEnv{}.env(), Options{Root: t.TempDir(), BunX: true}); err == nil {
			t.Fatal("found something")
		}
	})
}

func TestPlatformNames(t *testing.T) {
	for _, tc := range [][3]string{{"darwin", "arm64", "darwin-arm64"}, {"windows", "amd64", "win32-x64"}, {"linux", "amd64", "linux-x64"}} {
		if got := nodePlatform(tc[0]) + "-" + nodeArch(tc[1]); got != tc[2] {
			t.Errorf("%s/%s: %s, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}
