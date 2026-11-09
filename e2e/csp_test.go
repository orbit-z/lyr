//go:build e2e && unix

package e2e

import (
	"cmp"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCSP builds testdata/site with lyr build and loads its pages in a
// headless browser (Chromium or Firefox, see findBrowser) under script-src 'self'; style-src 'self' (PLAN.md
// §5f): deferred, delayed and lazy components, and page partials that
// htmx swaps in (one whose components the host lacks, one with an island
// and a lazy component, the same partial twice), once per vendored htmx
// version, plus an htmx 4 innerMorph swap that brings a new component. Zero
// violations, every script run exactly once, and the wrappers are
// display:contents. The pages report back over HTTP (public/csp-check.js).
func TestCSP(t *testing.T) {
	browser := findBrowser(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	lyrBin := filepath.Join(tmp, "lyr")
	run(t, root, nil, "go", "build", "-o", lyrBin, "./cmd/lyr")
	site := filepath.Join(tmp, "site")
	if err := os.CopyFS(site, os.DirFS("testdata/site")); err != nil {
		t.Fatal(err)
	}
	// The repository's go and toolchain lines and go.sum, so the fixture
	// builds with the same toolchain and, with a warm module cache,
	// offline.
	rootMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/site\n\n"
	for _, line := range strings.Split(string(rootMod), "\n") {
		if strings.HasPrefix(line, "go ") || strings.HasPrefix(line, "toolchain ") {
			gomod += line + "\n"
		}
	}
	gomod += "\nrequire github.com/orbit-z/lyr v0.0.0\n\nreplace github.com/orbit-z/lyr => " + filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(site, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum, err := os.ReadFile(filepath.Join(root, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(site, "go.sum"), sum, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command(lyrBin, "build")
	build.Dir = site
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		if offline.Match(out) {
			t.Skipf("lyr build needs the network (offline, module cache cold):\n%s", out)
		}
		t.Fatalf("lyr build: %v\n%s", err, out)
	}

	for _, htmx := range []string{"htmx-4.0.1.min.js", "htmx-2.0.11.min.js"} {
		t.Run(htmx, func(t *testing.T) {
			file, err := filepath.Abs(filepath.Join("testdata", htmx))
			if err != nil {
				t.Fatal(err)
			}
			p := newProxy(t, serve(t, site, "E2E_HTMX="+file))
			index := p.load(t, browser, "/")
			check(t, index, map[string]string{
				"tip-runs": "1", "pa-runs": "1", "pc-runs": "1", "pb-tip-runs": "1",
			})
			if index.LazyDisplay != "contents" || index.IslandDisplay != "contents" {
				t.Errorf("/: <lyr-lazy> display %s, <lyr-island> display %s, want contents", index.LazyDisplay, index.IslandDisplay)
			}
			for s, n := range map[string]int{
				`id="deferred-done"`: 1, `id="delayed-done"`: 1, "deferred fallback": 0, "delayed fallback": 0,
				"never fallback":   1,                        // client:media that never matches: still a placeholder
				"<b>partial a</b>": 3, "<b>partial c</b>": 1, // a twice, and c
				`class="pb-island-done"`: 1, "pb fallback": 0,
				`class="pb-late-done"`: 1, "pb late fallback": 0, // a delayed island in a partial
			} {
				if got := strings.Count(index.HTML, s); got != n {
					t.Errorf("/: %d × %s, want %d", got, s, n)
				}
			}
			// A host with no call sites of its own: the partial's wrappers
			// link gives the display:contents rule, and partial b twice.
			bare := p.load(t, browser, "/bare")
			check(t, bare, map[string]string{"pa-runs": "1", "pb-tip-runs": "1"})
			if bare.LazyDisplay != "contents" {
				t.Errorf("/bare: <lyr-lazy> display %s, want contents", bare.LazyDisplay)
			}
			for s, n := range map[string]int{`class="pb-island-done"`: 2, `class="pb-late-done"`: 2, "fallback": 0} {
				if got := strings.Count(bare.HTML, s); got != n {
					t.Errorf("/bare: %d × %s, want %d", got, s, n)
				}
			}
			// htmx 4's innerMorph of partial c over partial a: c's loader
			// script differs from a's (its config lists Pc), so the morph
			// inserts it instead of keeping a's, and it runs: Pc starts.
			if strings.HasPrefix(htmx, "htmx-4") {
				morph := p.load(t, browser, "/morph")
				check(t, morph, map[string]string{"pa-runs": "1", "pc-runs": "1"})
				if !strings.Contains(morph.HTML, "<b>partial c</b>") {
					t.Errorf("/morph: partial c not morphed in")
				}
			}
		})
	}
}

// browser is the browser the test drives.
type browser struct {
	path    string
	firefox bool
}

// findBrowser returns the browser, or skips the test: $LYR_BROWSER (a
// path, or "firefox" or "chromium"; $LYR_CHROME is still read as a path),
// else the first of chromium, chromium-browser, google-chrome and firefox
// on PATH.
func findBrowser(t *testing.T) browser {
	t.Helper()
	if testing.Short() {
		t.Skip("drives a browser")
	}
	names := []string{"chromium", "chromium-browser", "google-chrome", "firefox"}
	if b := cmp.Or(os.Getenv("LYR_BROWSER"), os.Getenv("LYR_CHROME")); b != "" {
		names = []string{b}
		if b == "chromium" {
			names = []string{"chromium", "chromium-browser", "google-chrome"}
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return browser{path: p, firefox: strings.Contains(filepath.Base(p), "firefox")}
		}
	}
	t.Skipf("no browser found (%s): set LYR_BROWSER or install Chromium or Firefox", strings.Join(names, ", "))
	return browser{}
}

// result is what the fixture's public/csp-check.js POSTs.
type result struct {
	Page          string   `json:"page"`
	Violations    []string `json:"violations"`
	LazyDisplay   string   `json:"lazyDisplay"`
	IslandDisplay string   `json:"islandDisplay"`
	HTML          string   `json:"html"`
}

// proxy serves the built site through a reverse proxy, and receives the
// pages' results at POST /__e2e/result (same origin as the pages).
type proxy struct {
	base    string
	results chan result
}

func newProxy(t *testing.T, backend string) *proxy {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	p := &proxy{results: make(chan result, 4)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__e2e/result" {
			rp.ServeHTTP(w, r)
			return
		}
		var res result
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.results <- res
	}))
	t.Cleanup(srv.Close)
	p.base = srv.URL
	return p
}

// load opens path in the headless browser, with a fresh profile (a cold
// cache), and returns the page's result, killing the browser once it
// arrives. It fails the test after 20 s without one, or on a violation.
func (p *proxy) load(t *testing.T, b browser, path string) result {
	t.Helper()
	profile := t.TempDir()
	var args []string
	if b.firefox {
		if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(firefoxPrefs), 0o644); err != nil {
			t.Fatal(err)
		}
		args = []string{"--headless", "--no-remote", "--profile", profile}
	} else {
		args = []string{"--headless", "--no-sandbox", "--disable-gpu", "--no-first-run", "--user-data-dir=" + profile}
	}
	cmd := exec.Command(b.path, append(args, p.base+path)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // kill its child processes too
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	}()
	select {
	case res := <-p.results:
		if res.Page != path {
			t.Fatalf("%s: got the result of %s", path, res.Page)
		}
		if len(res.Violations) > 0 {
			t.Errorf("%s: %d CSP violations:\n%s", path, len(res.Violations), strings.Join(res.Violations, "\n"))
		}
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("%s DOM:\n%s", path, res.HTML)
			}
		})
		return res
	case <-time.After(20 * time.Second):
		t.Fatalf("%s: no result in 20 s (the check script didn't run, or a CSP blocked it); browser output:\n%s", path, out.String())
	}
	return result{}
}

// firefoxPrefs open the URL straight away: no first-run or welcome pages,
// no default-browser check, no telemetry or updates.
const firefoxPrefs = `user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("startup.homepage_welcome_url", "");
user_pref("startup.homepage_welcome_url.additional", "");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("datareporting.healthreport.uploadEnabled", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("toolkit.telemetry.enabled", false);
user_pref("app.update.auto", false);
user_pref("app.update.disabledForTesting", true);
user_pref("browser.sessionstore.resume_from_crash", false);
user_pref("toolkit.startup.max_resumed_crashes", -1);
user_pref("browser.tabs.warnOnClose", false);
user_pref("extensions.update.enabled", false);
`

// run runs name in dir and fails the test on an error.
func run(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// serve starts the built site's dist/server with env, and returns its base
// URL once it listens.
func serve(t *testing.T, site string, env ...string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	cmd := exec.Command(filepath.Join(site, "dist", "server"))
	cmd.Dir = site
	cmd.Env = append(append(os.Environ(), "PORT="+port, "LYR_ADDR=", "LYR_DEV="), env...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server output:\n%s", out.String())
		}
	})
	for deadline := time.Now().Add(20 * time.Second); ; {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second); err == nil {
			c.Close()
			return "http://127.0.0.1:" + port
		}
		if time.Now().After(deadline) {
			t.Fatalf("dist/server doesn't listen:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var htmlTag = regexp.MustCompile(`<html[^>]*>`)

// offline matches a go command failing to reach the module proxy.
var offline = regexp.MustCompile(`dial tcp|lookup .*: no such host|proxy.golang.org|GOPROXY=off`)

// check compares data-* attributes of the result's <html> (the run
// counters) with want.
func check(t *testing.T, res result, want map[string]string) {
	t.Helper()
	tag := htmlTag.FindString(res.HTML)
	for name, w := range want {
		m := regexp.MustCompile(` data-` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(tag)
		got := "(unset)"
		if m != nil {
			got = m[1]
		}
		if got != w {
			t.Errorf("%s: data-%s = %s, want %s", res.Page, name, got, w)
		}
	}
}
