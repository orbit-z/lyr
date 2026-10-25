package build

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/internal/project"
)

func TestCopyPublic(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "robots.txt", "robots")
	writeFile(t, src, "img/logo.svg", "<svg/>")
	writeFile(t, src, ".well-known/security.txt", "sec")
	if err := os.Mkdir(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(src, "robots.txt"), filepath.Join(src, "link.txt")); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "client")
	problems, err := CopyPublic(src, dst)
	if err != nil || len(problems) > 0 {
		t.Fatalf("CopyPublic: %v, %v", problems, err)
	}
	want := map[string]string{"robots.txt": "robots", "img/logo.svg": "<svg/>", ".well-known/security.txt": "sec"}
	if runtime.GOOS != "windows" {
		want["link.txt"] = "robots"
	}
	for rel, content := range want {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil || string(got) != content {
			t.Errorf("%s: %q, %v; want %q", rel, got, err, content)
		}
	}
	for _, dir := range []string{"_orbit", "empty"} {
		if fi, err := os.Stat(filepath.Join(dst, dir)); err != nil || !fi.IsDir() {
			t.Errorf("no directory %s: %v", dir, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(dst, "link.txt")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("link.txt was copied as a symbolic link")
	}
}

func TestCopyPublicMissing(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "client")
	problems, err := CopyPublic(filepath.Join(t.TempDir(), "public"), dst)
	if err != nil || len(problems) > 0 {
		t.Fatalf("CopyPublic: %v, %v", problems, err)
	}
	if !exists(filepath.Join(dst, "_orbit")) {
		t.Errorf("no _orbit directory")
	}
}

func TestCopyPublicConflicts(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "_orbit/app.js", "x")
	writeFile(t, src, "_orbit/sub/a.css", "x")
	writeFile(t, src, "_orbitx.txt", "fine")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(src, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	problems, err := CopyPublic(src, filepath.Join(t.TempDir(), "client"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"public/_orbit/app.js: the /_orbit/ path is reserved for Orbit's assets; move it out of public/_orbit",
		"public/_orbit/sub/a.css: the /_orbit/ path is reserved for Orbit's assets; move it out of public/_orbit",
	}
	if runtime.GOOS != "windows" {
		want = append(want, "public/linked: a symbolic link to a directory isn't supported; copy the directory or link its files")
	}
	if !slices.Equal(problems, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(problems, "\n"), strings.Join(want, "\n"))
	}

	// An empty public/_orbit directory is ignored.
	src = t.TempDir()
	if err := os.Mkdir(filepath.Join(src, "_orbit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if problems, err := CopyPublic(src, filepath.Join(t.TempDir(), "client")); err != nil || len(problems) > 0 {
		t.Errorf("empty public/_orbit: %v, %v", problems, err)
	}
}

func TestRunPublicConflict(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	writeFile(t, dir, "public/_orbit/x.js", "x")
	writeFile(t, dir, "dist/client/old.txt", "old")
	res, err := Run(t.Context(), Options{Root: dir, Env: goEnv})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() || len(res.Public) != 1 || !strings.HasPrefix(res.Public[0], "public/_orbit/x.js: ") {
		t.Errorf("Public: %q", res.Public)
	}
	// The previous dist/ is untouched, and the staging directory removed.
	if !exists(filepath.Join(dir, "dist/client/old.txt")) {
		t.Errorf("dist/client changed on a failed build")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "dist")); len(entries) != 1 {
		t.Errorf("dist/: %v", entries)
	}
}

func TestCleanOutput(t *testing.T) {
	dir := fixture(t, false)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Front() {
		t.Fatalf("diagnostics: %v", p.Diagnostics)
	}
	in := strings.Join([]string{
		"# example.com/site/orbit_gen/impl/components/card",
		"./src/components/Card.orbit:7:34: props.Titel undefined (type card.Props has no field or method Titel)",
		"# example.com/site/orbit_gen/impl/pages/index",
		`./src/pages/index.orbit:13:11: cannot use 1 (untyped int constant) as string value in struct literal of type "example.com/site/orbit_gen/decl/components/card".Props`,
		"./src/layouts/Base.orbit:2:6: Props redeclared in this block",
		"\t./src/layouts/Base.orbit:2:6[" + dir + "/orbit_gen/decl/layouts/base/decl.go:5:6]: other declaration of Props",
		"# example.com/site/internal/x",
		"internal/x/x.go:3:9: undefined: undefinedZ",
		"./cmd/server/main.go:5:15: undefined: y",
		"",
	}, "\n")
	want := strings.Join([]string{
		"src/components/Card.orbit:7:34: props.Titel undefined (type Props has no field or method Titel)",
		"src/pages/index.orbit:13:11: cannot use 1 (untyped int constant) as string value in struct literal of type Card.Props",
		"src/layouts/Base.orbit:2:6: Props redeclared in this block",
		"\tsrc/layouts/Base.orbit:2:6: other declaration of Props",
		"# example.com/site/internal/x",
		"internal/x/x.go:3:9: undefined: undefinedZ",
		"cmd/server/main.go:5:15: undefined: y",
		"",
	}, "\n")
	if got := string(CleanOutput(p, []byte(in))); got != want {
		t.Errorf("CleanOutput:\n%s\nwant:\n%s", got, want)
	}
}
