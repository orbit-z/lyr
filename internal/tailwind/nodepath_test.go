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

// nodeModulesLayout writes an installed @tailwindcss/cli with its
// tailwindcss dependency under nm (the package manager's node_modules)
// and a bin symlink to it at bin, and returns bin.
func nodeModulesLayout(t *testing.T, nm, bin string) string {
	t.Helper()
	for rel, s := range map[string]string{
		"@tailwindcss/cli/dist/index.mjs": "#!/usr/bin/env node\n",
		"@tailwindcss/cli/package.json":   "{}",
		"tailwindcss/package.json":        "{}",
	} {
		p := filepath.Join(nm, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Dir(bin), 0o755)
	if err := os.Symlink(filepath.Join(nm, "@tailwindcss/cli/dist/index.mjs"), bin); err != nil {
		t.Skip("no symlinks:", err)
	}
	return bin
}

func TestPackageNodeModules(t *testing.T) {
	d := t.TempDir()
	// npm and Bun: hoisted into the project's node_modules.
	npm := filepath.Join(d, "npm/node_modules")
	if got := packageNodeModules(nodeModulesLayout(t, npm, filepath.Join(npm, ".bin/tailwindcss"))); got != npm {
		t.Errorf("npm: %q, want %q", got, npm)
	}
	// pnpm: the CLI's dependencies sit next to it under .pnpm.
	pnpm := filepath.Join(d, "pnpm/node_modules/.pnpm/@tailwindcss+cli@4.3.3/node_modules")
	if got := packageNodeModules(nodeModulesLayout(t, pnpm, filepath.Join(d, "pnpm/node_modules/.bin/tailwindcss"))); got != pnpm {
		t.Errorf("pnpm: %q, want %q", got, pnpm)
	}
	// No tailwindcss package next to the CLI, or no node_modules at all.
	bare := filepath.Join(d, "bare/node_modules/.bin/tailwindcss")
	os.MkdirAll(filepath.Dir(bare), 0o755)
	os.WriteFile(bare, []byte("\x7fELF"), 0o755)
	if got := packageNodeModules(bare); got != "" {
		t.Errorf("no package: %q", got)
	}
	if got := packageNodeModules(filepath.Join(d, "tailwindcss")); got != "" {
		t.Errorf("standalone: %q", got)
	}
}

// TestFindNodePath: the CLI found through bun x or node_modules gets the
// node_modules its tailwindcss dependency is in.
func TestFindNodePath(t *testing.T) {
	d := t.TempDir()
	bunx := filepath.Join(d, "cache/.bunx-1000/@tailwindcss/cli@"+Pinned+"/node_modules")
	bin := nodeModulesLayout(t, bunx, filepath.Join(bunx, ".bin/tailwindcss"))
	env := fakeEnv(map[string]string{"bun": "/b"}, nil)
	var probe []string
	env.Output = func(ctx context.Context, b string, args []string) ([]byte, error) {
		probe = append([]string{b}, args...)
		return []byte(bin + "\n"), nil
	}
	c, err := Find(t.Context(), env, t.TempDir())
	if err != nil || c.Via != "bun x" || c.NodePath != bunx {
		t.Errorf("bun x: %+v, %v; want NodePath %s", c, err, bunx)
	}
	if got := strings.Join(probe, " "); !strings.HasPrefix(got, "/b x --package @tailwindcss/cli@"+Pinned+" bun -e ") {
		t.Errorf("probe: %q", got)
	}
	// A failing probe leaves NodePath empty.
	env.Output = func(context.Context, string, []string) ([]byte, error) { return nil, errors.New("offline") }
	if c, err := Find(t.Context(), env, t.TempDir()); err != nil || c.NodePath != "" || c.Via != "bun x" {
		t.Errorf("failed probe: %+v, %v", c, err)
	}

	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	local := nodeModulesLayout(t, nm, filepath.Join(nm, ".bin/tailwindcss"))
	c, err = Find(t.Context(), fakeEnv(map[string]string{"node": "/n"}, map[string]string{local: "≈ tailwindcss v4.3.3"}), root)
	if err != nil || c.Via != "node_modules" || c.NodePath != nm {
		t.Errorf("node_modules: %+v, %v", c, err)
	}
}

// TestProcessorNodePath: a run gets NODE_PATH, after the user's own.
func TestProcessorNodePath(t *testing.T) {
	dir := t.TempDir()
	find := fakeCLI(t, `cat >/dev/null; printf '%s' "$NODE_PATH"`)
	t.Setenv("NODE_PATH", "/user")
	p := NewProcessorWith(dir, func(ctx context.Context) (CLI, error) {
		c, err := find(ctx)
		c.NodePath = "/bunx/node_modules"
		return c, err
	})
	in := Input{Inlined: Inline(Source{Text: "@import \"tailwindcss\";", Dir: dir, File: dir + "/g.css"}, InlineOptions{}), Dir: dir, Root: true}
	out, err := p.Process(t.Context(), in)
	if want := "/user" + string(os.PathListSeparator) + "/bunx/node_modules"; err != nil || out != want {
		t.Errorf("NODE_PATH = %q, %v; want %q", out, err, want)
	}
}

// TestBunxReal runs the real CLI through bun x on a stylesheet in a
// project with no node_modules (found porting nyth: "Can't resolve
// 'tailwindcss'"). It skips without Bun; the first run downloads the CLI
// into Bun's cache.
func TestBunxReal(t *testing.T) {
	if os.Getenv("LYR_TEST_BUNX") == "" {
		t.Skip("set LYR_TEST_BUNX=1 to run the bun x Tailwind test (it may download @tailwindcss/cli)")
	}
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("NOTICE: bun not found; skipping the bun x Tailwind test")
	}
	root := t.TempDir()
	styles := filepath.Join(root, "src", "styles")
	os.MkdirAll(styles, 0o755)
	os.WriteFile(filepath.Join(root, "src", "x.lyr"), []byte(`<p class="p-4 text-red-500">hi</p>`), 0o644)
	env := DefaultEnv()
	look := env.LookPath
	env.LookPath = func(name string) (string, error) {
		if name == "tailwindcss" {
			return "", exec.ErrNotFound
		}
		return look(name)
	}
	cli, err := Find(t.Context(), env, root)
	if err != nil || cli.Via != "bun x" {
		t.Fatalf("Find: %+v, %v", cli, err)
	}
	p := NewProcessorWith(root, func(context.Context) (CLI, error) { return cli, nil })
	src := Source{Text: "@import \"tailwindcss\" source(\"../..\");\n", Dir: styles, File: filepath.Join(styles, "global.css")}
	out, err := p.Process(t.Context(), Input{Inlined: Inline(src, InlineOptions{}), Dir: styles, Root: true})
	if err != nil {
		t.Fatalf("bun x tailwindcss: %v (NodePath %q)", err, cli.NodePath)
	}
	for _, want := range []string{".p-4", ".text-red-500", "tailwindcss v4"} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %s:\n%.500s", want, out)
		}
	}
}
