package dev

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEnv is the go commands' environment in the tests: the fixture's
// go.mod replaces the runtime with this checkout.
var testEnv = []string{"GOFLAGS=-mod=mod", "GOWORK=off"}

// needE2E skips the test under -short or without the go command.
func needE2E(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs a real server")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
}

// fixture copies testdata/site into a new module that replaces the
// runtime with this checkout, and returns its directory.
func fixture(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/site")); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/devsite\n\ngo 1.27\n\ntoolchain go1.27.3\n\nrequire github.com/orbit-z/orbit v0.0.0\n\nreplace github.com/orbit-z/orbit => " + filepath.ToSlash(repo) + "\n"
	writeFile(t, dir, "go.mod", gomod)
	// The checkout's go.sum covers the runtime's module graph, so go build
	// doesn't write go.sum (which the watcher would see as a Go edit).
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.sum", string(sum))
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// edit replaces old with new in a fixture file.
func edit(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("%s has no %q", rel, old)
	}
	writeFile(t, dir, rel, strings.Replace(string(data), old, new, 1))
}

// session is a running orbit dev.
type session struct {
	t      *testing.T
	dir    string
	url    string
	cancel context.CancelFunc
	done   chan error
	out    *syncBuffer

	mu     sync.Mutex
	stages []string
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func startSession(t *testing.T, dir string) *session {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &session{t: t, dir: dir, cancel: cancel, done: make(chan error, 1), out: &syncBuffer{}}
	ready := make(chan string, 1)
	go func() {
		s.done <- Run(ctx, Options{
			Root:   dir,
			Port:   14321,
			Stdout: s.out,
			Stderr: s.out,
			Env:    testEnv,
			Ready:  func(url string) { ready <- url },
			trace: func(stage string) {
				s.mu.Lock()
				s.stages = append(s.stages, stage)
				s.mu.Unlock()
			},
		})
	}()
	select {
	case s.url = <-ready:
	case err := <-s.done:
		t.Fatalf("orbit dev: %v\n%s", err, s.out)
	case <-time.After(2 * time.Minute):
		t.Fatalf("orbit dev didn't start\n%s", s.out)
	}
	t.Cleanup(func() {
		if t.Failed() || testing.Verbose() {
			t.Logf("orbit dev output:\n%s", s.out)
		}
	})
	return s
}

// stop ends the session as Ctrl-C does.
func (s *session) stop() {
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		s.t.Error("Run didn't return after cancel")
	}
}

// count returns how many times stage ran.
func (s *session) count(stage string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, st := range s.stages {
		if st == stage {
			n++
		}
	}
	return n
}

// mark returns the number of stages so far; since returns the stages
// after a mark.
func (s *session) mark() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stages)
}

func (s *session) since(m int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.stages[m:])
}

var client = &http.Client{Timeout: time.Minute}

func (s *session) get(path string, header ...string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequest("GET", s.url+path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// events subscribes to /__orbit/events and returns a channel of event
// names.
func (s *session) events() <-chan string {
	s.t.Helper()
	resp, err := http.Get(s.url + eventsPath)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				ch <- name
			}
		}
	}()
	return ch
}

func waitReload(t *testing.T, ch <-chan string, what string) {
	t.Helper()
	select {
	case e := <-ch:
		if e != "reload" {
			t.Fatalf("%s: event %q", what, e)
		}
	case <-time.After(time.Minute):
		t.Fatalf("%s: no reload event", what)
	}
}

var (
	cssLink   = regexp.MustCompile(`<link rel="stylesheet" href="(/_orbit/[^"]+\.css)">`)
	islandTag = regexp.MustCompile(`<orbit-island src="([^"]+)" props="([^"]+)">`)
)

// TestDevServer runs orbit dev on the fixture and walks through PLAN.md
// §13's orbit dev checks: live reload after a component edit, CSS-only
// edits without a Go rebuild, a first <style> with a Go rebuild, the
// reload script under a gzip middleware, island fragments and the user's
// /robots.txt left alone, the compile-error overlay and its fix, and the
// SIGINT app swap.
func TestDevServer(t *testing.T) {
	needE2E(t)
	dir := fixture(t)
	writeFile(t, dir, "orbit.json", `{"watch": ["content/**"]}`)
	writeFile(t, dir, "content/post.md", "# post")
	s := startSession(t, dir)
	defer s.stop()

	// The page, through the user's gzip middleware: the proxy removed
	// Accept-Encoding, so it's uncompressed and has the reload script.
	resp, body := s.get("/", "Accept-Encoding", "gzip", "Accept", "text/html")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || !strings.Contains(body, "card one hello") || !strings.Contains(body, reloadTag+"</body>") {
		t.Fatalf("GET /: %d %v\n%s", resp.StatusCode, resp.Header, body)
	}
	// The user's /robots.txt wins over public/robots.txt, as in prod.
	if _, body := s.get("/robots.txt"); body != "custom robots" {
		t.Errorf("robots.txt: %q", body)
	}
	// An island fragment gets no reload script.
	m := islandTag.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no island in\n%s", body)
	}
	resp, frag := s.get(m[1]+"?p="+m[2], "Accept-Encoding", "gzip")
	if resp.StatusCode != 200 || frag != `<section class="slow">island 7</section>` {
		t.Errorf("island: %d %q", resp.StatusCode, frag)
	}
	_, pid1 := s.get("/pid")

	events := s.events()
	builds := s.count("gobuild")

	// A component edit: reload event, new content, the old server got
	// SIGINT (it shut down gracefully and wrote its marker).
	edit(t, dir, "src/components/Card.orbit", "card {props.Post}", "edited card {props.Post}")
	waitReload(t, events, "Card edit")
	if _, body := s.get("/"); !strings.Contains(body, "edited card one") {
		t.Errorf("after the Card edit:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, ".orbit/stopped", pid1)); err != nil {
		t.Errorf("the old server (pid %s) didn't shut down after SIGINT: %v", pid1, err)
	}
	if _, pid2 := s.get("/pid"); pid2 == pid1 {
		t.Errorf("same server after a Go rebuild")
	}
	if n := s.count("gobuild"); n != builds+1 {
		t.Errorf("%d go builds for a template edit, want 1", n-builds)
	}
	builds = s.count("gobuild")

	// A CSS-only edit: no Go rebuild, a new hashed stylesheet URL.
	_, body = s.get("/")
	css1 := cssLink.FindStringSubmatch(body)
	_, pid := s.get("/pid")
	edit(t, dir, "src/layouts/Base.orbit", "color: navy;", "color: teal;")
	waitReload(t, events, "CSS edit")
	_, body = s.get("/")
	css2 := cssLink.FindStringSubmatch(body)
	if css1 == nil || css2 == nil || css1[1] == css2[1] {
		t.Errorf("stylesheet before %v, after %v", css1, css2)
	} else if _, css := s.get(css2[1]); !strings.Contains(css, "teal") {
		t.Errorf("new stylesheet:\n%s", css)
	}
	if n := s.count("gobuild"); n != builds {
		t.Errorf("a CSS-only edit ran go build")
	}
	if _, p := s.get("/pid"); p != pid {
		t.Errorf("a CSS-only edit restarted the server")
	}

	// Card's first <style>: a Go rebuild, and the scope attribute.
	edit(t, dir, "src/components/Card.orbit", "</p>\n", "</p>\n\n<style>\n  .card { color: red; }\n</style>\n")
	waitReload(t, events, "first <style>")
	if _, body := s.get("/"); !regexp.MustCompile(`<p class="card" data-o-[0-9a-f]{8}>`).MatchString(body) {
		t.Errorf("no scope attribute on the card:\n%s", body)
	}
	if n := s.count("gobuild"); n != builds+1 {
		t.Errorf("%d go builds for a first <style>, want 1", n-builds)
	}

	// A type error: the overlay, while the old server keeps answering
	// other requests; the fix reloads.
	edit(t, dir, "src/components/Card.orbit", "{props.Post}", "{props.Psot}")
	waitReload(t, events, "type error")
	resp, body = s.get("/", "Accept", "text/html")
	if resp.StatusCode != 500 || !regexp.MustCompile(`src/components/Card\.orbit:8:\d+</span>: props\.Psot undefined`).MatchString(body) {
		t.Errorf("overlay: %d\n%s", resp.StatusCode, body)
	}
	if _, body := s.get("/robots.txt"); body != "custom robots" {
		t.Errorf("robots.txt during a build error: %q", body)
	}
	edit(t, dir, "src/components/Card.orbit", "{props.Psot}", "{props.Post}")
	waitReload(t, events, "fix")
	if resp, body := s.get("/", "Accept", "text/html"); resp.StatusCode != 200 || !strings.Contains(body, "edited card one") {
		t.Errorf("after the fix: %d\n%s", resp.StatusCode, body)
	}

	// A .go-only edit: go build starts before regenerating, and its binary
	// is used (regenerating writes nothing).
	m0 := s.mark()
	edit(t, dir, "internal/msg/msg.go", `"hello"`, `"bonjour"`)
	waitReload(t, events, ".go edit")
	if _, body := s.get("/"); !strings.Contains(body, "edited card one bonjour") {
		t.Errorf("after the .go edit:\n%s", body)
	}
	if st := s.since(m0); len(st) == 0 || st[0] != "gobuild-early" || slices.Contains(st, "gobuild") {
		t.Errorf("stages of a .go edit: %v, want gobuild-early first and no second go build", st)
	}

	// A server that fails to start: the overlay shows its stderr; the fix
	// starts it.
	edit(t, dir, "cmd/server/main.go", "func main() {\n", "func main() {\n\tlog.Fatal(\"database unreachable\")\n")
	waitReload(t, events, "start failure")
	resp, body = s.get("/", "Accept", "text/html")
	if resp.StatusCode != 500 || !strings.Contains(body, "Your server failed to start") || !strings.Contains(body, "database unreachable") {
		t.Errorf("start failure overlay: %d\n%s", resp.StatusCode, body)
	}
	edit(t, dir, "cmd/server/main.go", "\tlog.Fatal(\"database unreachable\")\n", "")
	waitReload(t, events, "start fix")
	if resp, body := s.get("/", "Accept", "text/html"); resp.StatusCode != 200 || !strings.Contains(body, "edited card one bonjour") {
		t.Errorf("after the start fix: %d\n%s", resp.StatusCode, body)
	}

	// A public/ file: nothing rebuilt, just a reload.
	m0 = s.mark()
	writeFile(t, dir, "public/new.txt", "new file")
	waitReload(t, events, "public/ file")
	if st := s.since(m0); len(st) != 0 {
		t.Errorf("stages for a public/ file: %v", st)
	}
	if _, body := s.get("/new.txt"); body != "new file" {
		t.Errorf("new public file: %q", body)
	}

	// A Markdown page (PLAN.md §2.4): a body edit regenerates only its impl
	// package; a frontmatter edit changes its layout's props.
	if _, body := s.get("/about"); !strings.Contains(body, "<title>About</title>") || !strings.Contains(body, `<h2 id="first-version">First version</h2>`) {
		t.Errorf("GET /about:\n%s", body)
	}
	decl := filepath.Join(dir, "orbit_gen/decl/pages/about_md/decl.go")
	before, err := os.Stat(decl)
	if err != nil {
		t.Fatal(err)
	}
	builds = s.count("gobuild")
	edit(t, dir, "src/pages/about.md", "The about page, in Markdown.", "The about page, *edited*.")
	waitReload(t, events, ".md body edit")
	if _, body := s.get("/about"); !strings.Contains(body, "<p>The about page, <em>edited</em>.</p>") {
		t.Errorf("after the .md body edit:\n%s", body)
	}
	if n := s.count("gobuild"); n != builds+1 {
		t.Errorf("%d go builds for a .md edit, want 1", n-builds)
	}
	if after, err := os.Stat(decl); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a .md body edit rewrote its decl.go (%v)", err)
	}
	edit(t, dir, "src/pages/about.md", "title: About", "title: About us")
	waitReload(t, events, ".md frontmatter edit")
	if _, body := s.get("/about"); !strings.Contains(body, "<title>About us</title>") {
		t.Errorf("after the .md frontmatter edit:\n%s", body)
	}

	// A file matching orbit.json's "watch": the same binary is restarted
	// (no pipeline stage runs), then a reload.
	_, pid = s.get("/pid")
	m0 = s.mark()
	writeFile(t, dir, "content/post.md", "# changed")
	waitReload(t, events, "watched file")
	if st := s.since(m0); len(st) != 0 {
		t.Errorf("stages for a watched file: %v", st)
	}
	if _, p := s.get("/pid"); p == pid {
		t.Errorf("a watched file didn't restart the server")
	}
	if !strings.Contains(s.out.String(), "orbit dev: content/post.md: server restarted in") {
		t.Errorf("no restart message for the watched file")
	}
}
