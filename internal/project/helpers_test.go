package project

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testModule = "example.com/blog"

// testProject is a temporary user project.
type testProject struct {
	t   testing.TB
	Dir string
}

// newTestProject creates a module example.com/blog that requires the runtime
// through a replace to this repository, with the given files.
func newTestProject(t testing.TB, files map[string]string) *testProject {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	p := &testProject{t: t, Dir: t.TempDir()}
	p.write("go.mod", "module "+testModule+"\n\ngo 1.27\n\ntoolchain go1.27.3\n\n"+
		"require "+compiler.RuntimePath+" v0.0.0\n\nreplace "+compiler.RuntimePath+" => "+filepath.ToSlash(root)+"\n")
	for name, content := range files {
		p.write(name, content)
	}
	return p
}

func (p *testProject) write(rel, content string) {
	p.t.Helper()
	name := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *testProject) read(rel string) string {
	p.t.Helper()
	b, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(b)
}

func (p *testProject) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	return err == nil
}

// generate runs the full pipeline.
func (p *testProject) generate() *Result {
	p.t.Helper()
	g := &Generator{Root: p.Dir, Env: []string{"GOFLAGS=-mod=mod", "GOWORK=off"}}
	res, err := g.Run(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	return res
}

// goCmd runs the go command in the project root.
func (p *testProject) goCmd(args ...string) (string, error) {
	p.t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = p.Dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func needGo(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a Go module: skipped in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
}

func diagStrings(ds []Diagnostic) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}

func joinLines(s []string) string { return strings.Join(s, "\n") }
