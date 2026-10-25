package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestBuildLocalsTypo checks that a typo in a locals field fails orbit
// build at its .orbit position, in the user's names.
func TestBuildLocalsTypo(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, true)
	src, err := os.ReadFile(filepath.Join(dir, "src/pages/account.orbit"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/pages/account.orbit", strings.Replace(string(src), "{locals.Theme}", "{locals.Them}", 1))
	code, out := runOrbit(t, dir, "build")
	want := "src/pages/account.orbit:7:21: locals.Them undefined (type *locals.Locals has no field or method Them)"
	if code != 1 || !strings.Contains(out, "\n"+want+"\n") {
		t.Errorf("orbit build: exit %d\n%s\nwant exit 1 with\n%s", code, out, want)
	}
	if strings.Contains(out, "orbit__") {
		t.Errorf("output contains orbit__:\n%s", out)
	}
}

// TestBuildLocalsCycle checks the src/locals import rule (PLAN.md §5b).
func TestBuildLocalsCycle(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, true)
	writeFile(t, dir, "src/locals/locals.go", "package locals\n\nimport _ \"example.com/site/orbit_gen/app\"\n\ntype Locals struct{ RequestID, Theme string }\n")
	code, out := runOrbit(t, dir, "build")
	want := "src/locals/locals.go:3:10: src/locals can't import example.com/site/orbit_gen/app: every page imports src/locals, so this is an import cycle\n"
	if code != 1 || out != want {
		t.Errorf("orbit build: exit %d\n%s\nwant exit 1 with\n%s", code, out, want)
	}
}

// TestBuildEmbed checks orbit build --embed (PLAN.md §13 "Single binary"):
// dist/server alone, copied to an empty directory, serves the pages, the
// /_orbit/ assets and the public/ files; $ORBIT_STATIC still wins.
func TestBuildEmbed(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	begin := time.Now()
	code, out := runOrbit(t, dir, "build", "--embed")
	if code != 0 || out != "" {
		t.Fatalf("orbit build --embed: exit %d\n%s", code, out)
	}
	t.Logf("orbit build --embed: %v", time.Since(begin))
	// go mod tidy loads every build tag, so it needs orbit_gen/static (the
	// package embed.go imports) to be a package with and without the tag.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir, tidy.Env = dir, append(os.Environ(), goEnv...)
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy after --embed: %v\n%s", err, out)
	}
	for _, rel := range []string{"dist/client/robots.txt", "orbit_gen/static/static.go", "orbit_gen/static/client/robots.txt", "orbit_gen/static/doc.go"} {
		if !exists(filepath.Join(dir, rel)) {
			t.Errorf("no %s", rel)
		}
	}
	alone := t.TempDir()
	bin, err := os.ReadFile(filepath.Join(dir, "dist/server"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dist/server: %d bytes", len(bin))
	if err := os.WriteFile(filepath.Join(alone, "server"), bin, 0o755); err != nil {
		t.Fatal(err)
	}

	s := start(t, alone, filepath.Join(alone, "server"))
	resp, home := s.get("/")
	if resp.StatusCode != 200 || !strings.Contains(home, "<title>Home</title>") {
		t.Errorf("GET /: %d\n%s", resp.StatusCode, home)
	}
	for _, m := range []*regexp.Regexp{cssLink, jsScript} {
		url := m.FindStringSubmatch(home)
		if url == nil {
			t.Fatalf("GET /: no asset matching %s:\n%s", m, home)
		}
		resp, body := s.get(url[1])
		if resp.StatusCode != 200 || body == "" || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(resp.Header.Get("Etag"), `"`) {
			t.Errorf("GET %s: %d, headers %v", url[1], resp.StatusCode, resp.Header)
		}
	}
	resp, body := s.get("/robots.txt")
	if resp.StatusCode != 200 || !strings.Contains(body, "User-agent: *") || resp.Header.Get("Etag") == "" {
		t.Errorf("GET /robots.txt: %d, headers %v\n%s", resp.StatusCode, resp.Header, body)
	}
	if code := s.stop(); code != 0 {
		t.Errorf("server: exit %d\n%s", code, s.stderr)
	}

	s = startEnv(t, alone, []string{"ORBIT_STATIC=" + t.TempDir()}, filepath.Join(alone, "server"))
	if resp, _ := s.get("/robots.txt"); resp.StatusCode != 404 {
		t.Errorf("GET /robots.txt with ORBIT_STATIC set: %d, want 404", resp.StatusCode)
	}
	s.stop()

	// A build without --embed doesn't need orbit_gen/static.
	if err := os.RemoveAll(filepath.Join(dir, "orbit_gen/static")); err != nil {
		t.Fatal(err)
	}
	if code, out := runOrbit(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("orbit build: exit %d\n%s", code, out)
	}
	if fi, err := os.Stat(filepath.Join(dir, "dist/server")); err == nil {
		t.Logf("dist/server without --embed: %d bytes", fi.Size())
	}
}
