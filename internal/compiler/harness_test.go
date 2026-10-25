package compiler

// The golden-compile harness shared by the codegen (B) and typed-pass (C)
// tests: a temporary Go module that requires the runtime through a replace
// directive to this repository, so generated code is built, vetted and run
// for real. Frozen for milestone 4; add helpers in your own test files.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testModule is the module path of generated test projects.
const testModule = "example.com/blog"

// genModule is a temporary user module.
type genModule struct {
	t   testing.TB
	Dir string
}

// newGenModule creates an empty module example.com/blog in a temp dir. It
// skips the test in -short mode and when the go command is missing, since
// every build takes a second or more.
func newGenModule(t testing.TB) *genModule {
	t.Helper()
	if testing.Short() {
		t.Skip("golden compile test: skipped in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m := &genModule{t: t, Dir: t.TempDir()}
	m.write("go.mod", "module "+testModule+"\n\ngo 1.27\n\ntoolchain go1.27.3\n\n"+
		"require "+RuntimePath+" v0.0.0\n\nreplace "+RuntimePath+" => "+filepath.ToSlash(root)+"\n")
	return m
}

// write writes a module-relative file, creating its directories.
func (m *genModule) write(rel, content string) {
	m.t.Helper()
	p := filepath.Join(m.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// writeOutput writes both generated files of a codegen Output.
func (m *genModule) writeOutput(out *Output) {
	m.t.Helper()
	for _, f := range []*GenFile{out.Decl, out.Impl} {
		m.write(f.Path, string(f.Content))
	}
}

// goCmd runs the go command in the module root and returns its combined
// output. Paths in the output are relative to the module root (./src/…).
func (m *genModule) goCmd(args ...string) (string, error) {
	m.t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = m.Dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildAndVet runs go build ./... and go vet ./..., failing the test on any
// output.
func (m *genModule) buildAndVet() {
	m.t.Helper()
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		if out, err := m.goCmd(args...); err != nil || strings.TrimSpace(out) != "" {
			m.t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// buildErrors runs go build ./... and go vet ./..., which must fail, and
// returns both outputs, for the line-directive position tests.
func (m *genModule) buildErrors() (build, vet string) {
	m.t.Helper()
	build, err := m.goCmd("build", "./...")
	if err == nil {
		m.t.Fatal("go build succeeded, want errors")
	}
	vet, err = m.goCmd("vet", "./...")
	if err == nil {
		m.t.Fatal("go vet succeeded, want errors")
	}
	return build, vet
}

// run writes mainSrc as cmd/render/main.go and returns what go run prints.
// mainSrc imports the decl packages it renders and the impl packages (as _)
// that link them; there is no orbit_gen/app before milestone 5.
func (m *genModule) run(mainSrc string) string {
	m.t.Helper()
	m.write("cmd/render/main.go", mainSrc)
	out, err := m.goCmd("run", "./cmd/render")
	if err != nil {
		m.t.Fatalf("go run: %v\n%s", err, out)
	}
	return normalizeIslands(out)
}

// islandPlaceholder matches the parts of a server:defer placeholder that
// change on every go run (a random build ID and key outside go test): the
// runtime's own tests check the tokens.
var islandPlaceholder = regexp.MustCompile(`/_orbit/island/[0-9a-f]{16}/([^"]*)" props="[A-Za-z0-9_-]*"`)

func normalizeIslands(s string) string {
	return islandPlaceholder.ReplaceAllString(s, `/_orbit/island/*/$1" props="*"`)
}

// TestHarness checks the harness itself with a hand-written package in the
// generated layout.
func TestHarness(t *testing.T) {
	m := newGenModule(t)
	m.write("orbit_gen/decl/components/hello/decl.go", GenHeader+`

package hello

import orbit__rt "github.com/bertrandkhe/orbit"

type Props struct{ Name string }

func Render(orbit__ctx *orbit__rt.Ctx, orbit__w *orbit__rt.Writer, orbit__props Props, orbit__slots orbit__rt.Slots) error {
	orbit__w.Raw("<p>hello ")
	orbit__w.String(orbit__props.Name)
	orbit__w.Raw("</p>")
	return orbit__w.Err()
}
`)
	m.buildAndVet()
	got := m.run(`package main

import (
	"bytes"
	"fmt"

	"github.com/bertrandkhe/orbit"
	hello "example.com/blog/orbit_gen/decl/components/hello"
)

func main() {
	var buf bytes.Buffer
	if err := hello.Render(&orbit.Ctx{}, orbit.NewWriter(&buf), hello.Props{Name: "<you>"}, orbit.Slots{}); err != nil {
		panic(err)
	}
	fmt.Print(buf.String())
}
`)
	if want := "<p>hello &lt;you&gt;</p>"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}
