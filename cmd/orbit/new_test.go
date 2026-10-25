package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newEnv runs the go command in a scaffolded project: no workspace.
var newEnv = []string{"GOWORK=off"}

// TestNew runs orbit new against this checkout (ORBIT_REPLACE), then builds
// and serves the starter with no edits (PLAN.md §13).
func TestNew(t *testing.T) {
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
	t.Setenv("ORBIT_REPLACE", root)
	t.Setenv("GOWORK", "off")
	base := t.TempDir()

	code, stdout, stderr := runCLI(t, base, "new", "blog", "--module", "example.com/blog")
	if code != 0 {
		t.Fatalf("orbit new: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "cd blog\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
	dir := filepath.Join(base, "blog")
	gomod := readFile(t, dir, "go.mod")
	for _, s := range []string{
		"module example.com/blog\n", "\ngo 1.27\n", "\ntoolchain go1.", "\ntool github.com/bertrandkhe/orbit/cmd/orbit\n",
		"replace github.com/bertrandkhe/orbit => " + filepath.ToSlash(root) + "\n", "github.com/evanw/esbuild",
	} {
		if !strings.Contains(gomod, s) {
			t.Errorf("go.mod lacks %q:\n%s", s, gomod)
		}
	}
	for _, rel := range []string{"go.sum", "orbit_gen/app/app.go", "Dockerfile", ".dockerignore", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Error(err)
		}
	}

	goCmd(t, dir, "go", "build", "./...")
	goCmd(t, dir, "go", "vet", "./...")
	goCmd(t, dir, "go", "tool", "orbit", "build")
	site := serve(t, dir, filepath.Join(dir, "dist", "server"))

	body := get(t, site+"/", 200)
	for _, s := range []string{"<title>My Orbit site</title>", "<h1>Welcome to Orbit</h1>"} {
		if !strings.Contains(body, s) {
			t.Errorf("GET /: no %q:\n%s", s, body)
		}
	}
	css := regexp.MustCompile(`<link rel="stylesheet" href="(/_orbit/[^"]+\.css)">`).FindStringSubmatch(body)
	if css == nil {
		t.Errorf("GET /: no stylesheet link:\n%s", body)
	} else if sheet := get(t, site+css[1], 200); !strings.Contains(sheet, "main:where([data-o-") || !strings.Contains(sheet, "max-width") {
		t.Errorf("GET %s: not the layout's scoped CSS:\n%s", css[1], sheet)
	}
	if body := get(t, site+"/nope", 404); !strings.Contains(body, "<h1>Page not found</h1>") {
		t.Errorf("GET /nope:\n%s", body)
	}
	get(t, site+"/favicon.svg", 200)

	// Into a non-empty directory: nothing is written.
	code, _, stderr = runCLI(t, base, "new", "blog")
	if code != 1 || !strings.Contains(stderr, "orbit new: "+dir+": directory exists and is not empty") {
		t.Errorf("orbit new into a non-empty dir: exit %d\n%s", code, stderr)
	}

	// Without --module, the module path is the directory name; the dir may
	// be a path, and exist empty.
	other := filepath.Join(base, "sites", "my-site")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCLI(t, base, "new", "sites/my-site")
	if code != 0 {
		t.Fatalf("orbit new sites/my-site: exit %d\n%s", code, stderr)
	}
	if gomod := readFile(t, other, "go.mod"); !strings.HasPrefix(gomod, "module my-site\n") {
		t.Errorf("go.mod:\n%s", gomod)
	}
	if main := readFile(t, other, "cmd/server/main.go"); !strings.Contains(main, `"my-site/orbit_gen/app"`) {
		t.Errorf("main.go:\n%s", main)
	}
}

func TestNewUsage(t *testing.T) {
	tests := []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"new"}, 2, "orbit new: missing directory"},
		{[]string{"new", "--bogus", "x"}, 2, "flag provided but not defined: -bogus"},
		{[]string{"new", "x", "--bogus"}, 2, "flag provided but not defined: -bogus"},
		{[]string{"new", "x", "y"}, 2, `orbit new: unexpected argument "y"`},
		{[]string{"new", "x", "--module"}, 2, "flag needs an argument: -module"},
		{[]string{"new", "my site"}, 1, `orbit new: malformed module path "my site"`},
		{[]string{"new", "x", "--module", "a//b"}, 1, `malformed module path "a//b"`},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		code, _, stderr := runCLI(t, dir, tt.args...)
		if code != tt.code || !strings.Contains(stderr, tt.stderr) {
			t.Errorf("orbit %q: exit %d, stderr:\n%s\nwant exit %d, stderr ~ %q", tt.args, code, stderr, tt.code, tt.stderr)
		}
		if entries, _ := os.ReadDir(dir); len(entries) > 0 {
			t.Errorf("orbit %q wrote files", tt.args)
		}
	}
	if code, _, stderr := runCLI(t, t.TempDir(), "new", "-h"); code != 0 || !strings.Contains(stderr, "usage: orbit new <dir> [--module path]") {
		t.Errorf("orbit new -h: exit %d\n%s", code, stderr)
	}
}

// TestNewDocker builds the image of a fresh project and checks it serves
// the site (PLAN.md §13). Opt-in with ORBIT_TEST_DOCKER=1. The replace of
// this checkout is pointed at a named build context, since the image can't
// see the host's directory.
func TestNewDocker(t *testing.T) {
	if os.Getenv("ORBIT_TEST_DOCKER") != "1" {
		t.Skip("set ORBIT_TEST_DOCKER=1 to build the scaffolded Docker image")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORBIT_REPLACE", root)
	t.Setenv("GOWORK", "off")
	base := t.TempDir()
	if code, _, stderr := runCLI(t, base, "new", "site"); code != 0 {
		t.Fatalf("orbit new: exit %d\n%s", code, stderr)
	}
	dir := filepath.Join(base, "site")

	// The only edits: the replace points at /orbit, which a line added to
	// the Dockerfile fills from a copy of this checkout.
	gomod := strings.Replace(readFile(t, dir, "go.mod"), "=> "+filepath.ToSlash(root), "=> /orbit", 1)
	writeFileT(t, dir, "go.mod", gomod)
	docker := strings.Replace(readFile(t, dir, "Dockerfile"), "WORKDIR /src\n", "WORKDIR /src\nCOPY --from=orbit . /orbit\n", 1)
	writeFileT(t, dir, "Dockerfile", docker)
	src := filepath.Join(base, "orbit-src")
	copySource(t, root, src)

	tag := "orbit-new-test:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	cmd := exec.Command("docker", "build", "--build-context", "orbit="+src, "-t", tag, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })

	out, err := exec.Command("docker", "image", "inspect", "--format", "{{json .Size}}", tag).Output()
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := json.Unmarshal(bytes.TrimSpace(out), &size); err != nil {
		t.Fatal(err)
	}
	t.Logf("image size: %.1f MB", float64(size)/1e6)
	if size >= 30e6 {
		t.Errorf("image is %d bytes, want < 30 MB", size)
	}

	port := freePortT(t)
	name := strings.ReplaceAll(tag, ":", "-")
	run := exec.Command("docker", "run", "--rm", "--name", name, "-p", "127.0.0.1:"+port+":8080", tag)
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run(); run.Wait() })
	site := "http://127.0.0.1:" + port
	waitHTTP(t, site+"/")
	if body := get(t, site+"/", 200); !strings.Contains(body, "<h1>Welcome to Orbit</h1>") {
		t.Errorf("GET /:\n%s", body)
	}
	if body := get(t, site+"/nope", 404); !strings.Contains(body, "Page not found") {
		t.Errorf("GET /nope:\n%s", body)
	}
}

// copySource copies the Orbit module's files (no .git, worktrees, spikes or
// build output) into dst.
func copySource(t *testing.T, root, dst string) {
	t.Helper()
	skip := map[string]bool{".git": true, ".claude": true, "spike": true, "node_modules": true, "dist": true, ".orbit": true, "orbit_gen": true}
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, name)
		if skip[d.Name()] && rel != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFileT(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), newEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func freePortT(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// serve starts bin in dir on a free port and returns its base URL.
func serve(t *testing.T, dir, bin string) string {
	t.Helper()
	port := freePortT(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PORT="+port, "ORBIT_ADDR=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server output:\n%s", out.String())
		}
	})
	base := "http://127.0.0.1:" + port
	waitHTTP(t, base+"/")
	return base
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", url, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func get(t *testing.T, url string, status int) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Errorf("GET %s: status %d, want %d\n%s", url, resp.StatusCode, status, body)
	}
	return string(body)
}
