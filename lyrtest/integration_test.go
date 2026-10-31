package lyrtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration generates testdata/project with the lyr CLI and runs its
// own tests: Render of a component and of a page, Get through app.New(), and
// Snapshot written with -update and then compared. A test package without
// the lyr_gen/app import must fail with the not-linked hint.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and tests a project")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	lyr := filepath.Join(tmp, "lyr")
	goCmd(t, repo, "build", "-o", lyr, "./cmd/lyr")

	dir := filepath.Join(tmp, "project")
	if err := os.CopyFS(dir, os.DirFS("testdata/project")); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/site\n\ngo 1.27\n\ntoolchain go1.27.3\n\n" +
		"require github.com/orbit-z/lyr v0.0.0\n\n" +
		"replace github.com/orbit-z/lyr => " + filepath.ToSlash(repo) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	// The runtime's requirements (esbuild, tdewolff, x/sys) need go.sum
	// entries; the repository's own go.sum has them all.
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(lyr, "generate")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lyr generate: %v\n%s", err, out)
	}

	// A missing golden file fails, with the hint.
	out, err := goTest(dir, "./internal/views_test")
	if err == nil || !strings.Contains(out, "no golden file testdata/card.golden; run go test with -update") {
		t.Fatalf("go test without goldens: err %v\n%s", err, out)
	}
	if out, err := goTest(dir, "./internal/views_test", "-update"); err != nil {
		t.Fatalf("go test -update: %v\n%s", err, out)
	}
	for _, rel := range []string{"card.golden", "pages/post.golden", "get/post.golden"} {
		if _, err := os.Stat(filepath.Join(dir, "internal/views_test/testdata", rel)); err != nil {
			t.Errorf("golden file: %v", err)
		}
	}
	get, err := os.ReadFile(filepath.Join(dir, "internal/views_test/testdata/get/post.golden"))
	if err != nil {
		t.Fatal(err)
	}
	// Get injects the head tags; Snapshot stored the hashed name normalized.
	for _, want := range []string{"<title>Post hello</title>", `<link rel="stylesheet" href="/_lyr/blog-slug-*.css">`} {
		if !strings.Contains(string(get), want) {
			t.Errorf("get/post.golden lacks %q:\n%s", want, get)
		}
	}
	if out, err := goTest(dir, "./..."); err != nil {
		t.Fatalf("go test ./...: %v\n%s", err, out)
	}

	out, err = goTest(dir, "-tags", "unlinked", "./internal/unlinked_test")
	want := "lyrtest: src/components/Card.lyr not linked; import example.com/site/lyr_gen/app in this test package"
	if err == nil || !strings.Contains(out, want) {
		t.Errorf("unlinked test: err %v, want output with %q:\n%s", err, want, out)
	}
}

var goEnv = []string{"GOFLAGS=-mod=mod", "GOWORK=off"}

func goCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
}

func goTest(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", append([]string{"test", "-count=1"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
