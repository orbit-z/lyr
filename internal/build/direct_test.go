package build

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Tests of the build flavor (PLAN.md §3.5): lyr build overlays impl
// files with direct calls on lyr_gen/, which stays as lyr generate
// writes it.

// flavorTest is a go test, written into a project, that GETs each route
// (from FLAVOR_ROUTES, comma-separated) through lyrtest.Get and writes
// the responses to FLAVOR_OUT.
const flavorTest = `package flavor_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/lyrtest"

	"MODULE/lyr_gen/app"
)

func TestDump(t *testing.T) {
	h := app.New().Handler()
	var b strings.Builder
	for _, path := range strings.Split(os.Getenv("FLAVOR_ROUTES"), ",") {
		rec := lyrtest.Get(t, h, path)
		fmt.Fprintf(&b, "== %s %d\n%s\n", path, rec.Code, rec.Body.String())
	}
	if err := os.WriteFile(os.Getenv("FLAVOR_OUT"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
`

// islandToken matches what differs between two test binaries: the island
// build ID and token (a random key without lyr build).
var islandToken = regexp.MustCompile(`/_lyr/island/[0-9a-f]{16}/([^"]*)" props="[A-Za-z0-9_-]*"`)

// checkFlavors runs flavorTest in dir (module module) with and without
// -overlay=.lyr/build/overlay.json, after a lyr build, and checks that
// every route's response is byte-identical between the flavors.
func checkFlavors(t *testing.T, dir, module string, routes []string) {
	t.Helper()
	writeFile(t, dir, "internal/flavor_test/flavor_test.go", strings.ReplaceAll(flavorTest, "MODULE", module))
	defer os.RemoveAll(filepath.Join(dir, "internal/flavor_test"))
	dump := func(args ...string) string {
		out := filepath.Join(t.TempDir(), "out")
		cmd := exec.Command("go", append(append([]string{"test", "-count=1"}, args...), "./internal/flavor_test")...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), append(goEnv, "FLAVOR_ROUTES="+strings.Join(routes, ","), "FLAVOR_OUT="+out)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test %v: %v\n%s", args, err, b)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return islandToken.ReplaceAllString(string(b), `/_lyr/island/*/$1" props="*"`)
	}
	hook, direct := dump(), dump("-overlay=.lyr/build/overlay.json")
	if hook != direct {
		t.Errorf("the build flavor serves different responses:\n--- hook\n%s\n--- build flavor\n%s", hook, direct)
	}
	if strings.Count(hook, " 200\n") < 2 {
		t.Errorf("too few pages rendered:\n%s", hook)
	}
}

// readTree reads every file under dir, by relative path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// checkBuildFlavor checks the disk after a lyr build in dir: lyr_gen/
// is what lyr generate writes (gen, read after it), .lyr/build holds
// the overlay with want among its files, and a second lyr build writes
// the same .lyr/build.
func checkBuildFlavor(t *testing.T, dir string, gen map[string]string, want string) {
	t.Helper()
	if got := readTree(t, filepath.Join(dir, "lyr_gen")); !reflect.DeepEqual(got, gen) {
		t.Error("lyr build left lyr_gen/ different from lyr generate's output")
	}
	first := readTree(t, filepath.Join(dir, ".lyr/build"))
	if _, ok := first["overlay.json"]; !ok {
		t.Fatalf("no .lyr/build/overlay.json; files: %v", first)
	}
	if _, ok := first[want]; !ok || !strings.Contains(first["overlay.json"], `".lyr/build/`+want+`"`) {
		t.Errorf("%s isn't overlaid:\n%s", want, first["overlay.json"])
	}
	if code, out := runLyr(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("second lyr build: exit %d\n%s", code, out)
	}
	if again := readTree(t, filepath.Join(dir, ".lyr/build")); !reflect.DeepEqual(first, again) {
		t.Error("two lyr build runs wrote different .lyr/build trees")
	}
}

// TestBuildOverlayInGOFLAGS checks that lyr build refuses an -overlay in
// GOFLAGS, which its own -overlay would silently replace.
func TestBuildOverlayInGOFLAGS(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	code, out := runLyrEnv(t, dir, []string{"GOFLAGS=-mod=mod -overlay=x.json"}, "build")
	if want := "lyr: -overlay in GOFLAGS isn't supported (lyr build passes its own)\n"; code != 1 || out != want {
		t.Errorf("lyr build: exit %d\n%s\nwant exit 1\n%s", code, out, want)
	}
	if exists(filepath.Join(dir, "lyr_gen")) || exists(filepath.Join(dir, "dist")) {
		t.Error("lyr build wrote files before refusing")
	}
}

// TestBuildFlavorTagsEmbed checks that the overlay combines with --embed
// and with -tags from GOFLAGS: a file excluded by a tag in GOFLAGS is
// compiled out of the build-flavor binary too.
func TestBuildFlavorTagsEmbed(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	writeFile(t, dir, "internal/tagged/on.go", "//go:build fixturetag\n\npackage tagged\n\nconst Word = \"tagged\"\n")
	writeFile(t, dir, "internal/tagged/off.go", "//go:build !fixturetag\n\npackage tagged\n\nconst Word = \"untagged\"\n")
	writeFile(t, dir, "src/pages/word.lyr", "---\nimport \"example.com/site/internal/tagged\"\nimport Base \"../layouts/Base.lyr\"\n---\n<Base title=\"word\"><p>{tagged.Word}</p></Base>\n")
	code, out := runLyrEnv(t, dir, []string{"GOFLAGS=-mod=mod -tags=fixturetag"}, "build", "--embed")
	if code != 0 || out != "" {
		t.Fatalf("lyr build --embed: exit %d\n%s", code, out)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".lyr/build/overlay.json")); err != nil || !strings.Contains(string(b), "pages/word/render.go") {
		t.Errorf("word.lyr isn't overlaid (%v):\n%s", err, b)
	}
	alone := t.TempDir()
	bin, err := os.ReadFile(filepath.Join(dir, "dist/server"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alone, "server"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	s := start(t, alone, filepath.Join(alone, "server"))
	if resp, body := s.get("/word"); resp.StatusCode != 200 || !strings.Contains(body, "<p>tagged</p>") {
		t.Errorf("GET /word: %d\n%s", resp.StatusCode, body)
	}
	if resp, _ := s.get("/robots.txt"); resp.StatusCode != 200 {
		t.Errorf("embed: GET /robots.txt: %d", resp.StatusCode)
	}
	if code := s.stop(); code != 0 {
		t.Errorf("server: exit %d\n%s", code, s.stderr)
	}
}

// runLyrEnv is runLyr with env appended after goEnv.
func runLyrEnv(t *testing.T, dir string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(lyrCLI(t), args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), goEnv...), env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("lyr %v: %v", args, err)
	}
	return cmd.ProcessState.ExitCode(), string(out)
}
