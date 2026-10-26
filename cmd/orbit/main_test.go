package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(&env{stdout: &out, stderr: &errb, dir: dir, ctx: context.Background()}, args)
	return code, out.String(), errb.String()
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stdout string // substring
		stderr string // substring
	}{
		{nil, 2, "", "Usage:"},
		{[]string{"help"}, 0, "orbit <command>", ""},
		{[]string{"help", "generate"}, 0, "usage: orbit generate", ""},
		{[]string{"help", "nope"}, 2, "", "orbit help nope: unknown command"},
		{[]string{"nope"}, 2, "", "orbit nope: unknown command"},
		{[]string{"dev"}, 1, "", "no go.mod"},
		{[]string{"dev", "--bogus"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"dev", "--port", "0"}, 2, "", "orbit dev: invalid port 0"},
		{[]string{"dev", "--port=x"}, 2, "", "invalid value"},
		{[]string{"dev", "extra"}, 2, "", `orbit dev: unexpected argument "extra"`},
		{[]string{"dev", "-h"}, 0, "", "usage: orbit dev [--port 4321] [--host]"},
		{[]string{"build", "extra"}, 2, "", `orbit build: unexpected argument "extra"`},
		{[]string{"build"}, 1, "", "no go.mod"},
		{[]string{"preview", "--port", "3000"}, 2, "", "flag provided but not defined: -port"},
		{[]string{"preview"}, 1, "", "no go.mod"},
		{[]string{"generate", "--bogus"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"generate", "extra"}, 2, "", `orbit generate: unexpected argument "extra"`},
		{[]string{"generate", "-h"}, 0, "", "usage: orbit generate"},
		{[]string{"check"}, 1, "", "no go.mod"},
		{[]string{"check", "extra"}, 2, "", `orbit check: unexpected argument "extra"`},
		{[]string{"check", "--bogus"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"check", "-h"}, 0, "", "usage: orbit check"},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCLI(t, t.TempDir(), tt.args...)
		if code != tt.code || !strings.Contains(stdout, tt.stdout) || !strings.Contains(stderr, tt.stderr) {
			t.Errorf("orbit %v: exit %d\nstdout: %s\nstderr: %s\nwant exit %d, stdout ~ %q, stderr ~ %q",
				tt.args, code, stdout, stderr, tt.code, tt.stdout, tt.stderr)
		}
	}
}

func TestGenerateNoModule(t *testing.T) {
	code, _, stderr := runCLI(t, t.TempDir(), "generate")
	if code != 1 || !strings.Contains(stderr, "no go.mod") {
		t.Errorf("exit %d, stderr %s", code, stderr)
	}
}

func TestBuildPreviewErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/site\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, dir, "preview")
	if code != 1 || !strings.Contains(stderr, "orbit preview: no dist/server in "+dir+"; run orbit build first") {
		t.Errorf("preview: exit %d, stderr %s", code, stderr)
	}
}

func TestGenerateCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	write := func(rel, content string) {
		name := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/site\n\ngo 1.27\n\nrequire github.com/orbit-z/orbit v0.0.0\n\nreplace github.com/orbit-z/orbit => "+filepath.ToSlash(root)+"\n")
	write("src/pages/index.orbit", "<p>{1 + 1}</p>\n")

	// From a subdirectory: the root is found by walking up to go.mod.
	code, stdout, stderr := runCLI(t, filepath.Join(dir, "src", "pages"), "generate")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "orbit_gen", "app", "app.go")); err != nil {
		t.Fatal(err)
	}

	write("src/pages/post-[id].orbit", "<p>x</p>\n")
	write("src/pages/about.orbit", "<p>{x}<p>\n")
	code, _, stderr = runCLI(t, dir, "generate")
	if code != 1 || !strings.Contains(stderr, `src/pages/post-[id].orbit: route segment "post-[id]" mixes text and a parameter`) ||
		!strings.Contains(stderr, "src/pages/about.orbit:1:") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
}
