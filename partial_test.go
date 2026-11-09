package lyr

import (
	"github.com/orbit-z/lyr/internal/loader"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Page partials (PLAN.md §4k).

// partialScript is a partial's loader script with config cfg.
func partialScript(cfg string) string {
	return `<script src="` + loader.LoaderURL + `" data-lyr-loader='` + cfg + `'></script>`
}

// wrappersLink is the display:contents stylesheet of a partial with call sites.
const wrappersLink = `<link rel="stylesheet" href="` + loader.WrappersURL + `">`

var partialRunner = partialScript(`{"dev":false,"run":[["List-1a2b3c4d","/_lyr/List-A.js"],["Card-9f8e7d6c","/_lyr/Card-B.js"]]}`)

func fragment(c *Ctx, w *Writer) error {
	w.Raw(`<ul><li>one</li></ul>`)
	return nil
}

// headed writes a doctype and a head: copied as written in a partial.
func headed(c *Ctx, w *Writer) error {
	w.Raw(`<!DOCTYPE html><html><head>`)
	w.HeadEnd()
	w.Raw(`</head><body><p>x</p></body></html>`)
	return nil
}

var runnerAssets = []Asset{
	{Kind: "component", URL: "/_lyr/List-A.js", ID: "List-1a2b3c4d"},
	{Kind: "component", URL: "/_lyr/Card-B.js", ID: "Card-9f8e7d6c"},
}

func TestPartialTail(t *testing.T) {
	wrappers := Asset{Kind: "css", URL: loader.WrappersURL}
	lazyAssets := []Asset{
		wrappers,
		{Kind: "component", URL: "/_lyr/List-A.js", ID: "List-1a2b3c4d"},
		{Kind: "component", URL: "/_lyr/Tip-C.js", ID: "Tip-11112222"},
		{Kind: "lazy", URL: "/_lyr/Tip-C.js", ID: "Tip-11112222"},
	}
	tests := []struct {
		name     string
		dev, isl bool
		assets   []Asset
		want     string
	}{
		{"no scripts", false, false, nil, ""},
		{"runner only", false, false, runnerAssets, partialRunner},
		{"lazy", false, false, lazyAssets,
			wrappersLink + partialScript(`{"dev":false,"lazy":{"Tip-11112222":["/_lyr/Tip-C.js"]},"ids":{"/_lyr/Tip-C.js":"Tip-11112222"},"run":[["List-1a2b3c4d","/_lyr/List-A.js"]]}`)},
		{"islands", true, true, []Asset{wrappers}, wrappersLink + partialScript(`{"dev":true}`)},
		// A client:* marker whose component is eager: the link, no script.
		{"marker only", false, false, []Asset{wrappers}, wrappersLink},
	}
	for _, tt := range tests {
		if got := partialTail(tt.dev, tt.isl, tt.assets); got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
	}
	// IDs and URLs are escaped for a single-quoted attribute.
	got := partialTail(false, false, []Asset{{Kind: "component", URL: "/_lyr/x'</script>&.js", ID: "A'&<"}})
	want := partialScript(`{"dev":false,"run":[["A&#39;\u0026\u003c","/_lyr/x&#39;\u003c/script\u003e\u0026.js"]]}`)
	if got != want {
		t.Errorf("not escaped:\n got %s\nwant %s", got, want)
	}
}

func TestPartialServe(t *testing.T) {
	routes := []Route{
		{Pattern: "/p", File: "src/pages/p.lyr", Render: fragment, Assets: runnerAssets, Partial: true},
		{Pattern: "/plain", File: "src/pages/plain.lyr", Render: fragment, Partial: true},
		{Pattern: "/headed", File: "src/pages/headed.lyr", Render: headed, Assets: runnerAssets, Partial: true, Islands: true},
		// The same render as a page: the tags are injected at the start.
		{Pattern: "/page", File: "src/pages/page.lyr", Render: fragment, Assets: []Asset{{Kind: "js", URL: "/_lyr/page-X.js"}}},
	}
	s := testSite(t, routes)
	h := s.Handler()
	for target, want := range map[string]string{
		"/p":     `<ul><li>one</li></ul>` + partialRunner,
		"/plain": `<ul><li>one</li></ul>`,
		"/headed": `<!DOCTYPE html><html><head></head><body><p>x</p></body></html>` +
			partialScript(`{"dev":false,"run":[["List-1a2b3c4d","/_lyr/List-A.js"],["Card-9f8e7d6c","/_lyr/Card-B.js"]]}`),
		"/page": `<script type="module" src="/_lyr/page-X.js"></script><ul><li>one</li></ul>`,
	} {
		rec := do(h, "GET", target)
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d\n got %s\nwant %s", target, rec.Code, rec.Body, want)
		}
		if got := rec.Header().Get("Content-Length"); got != itoa(len(want)) {
			t.Errorf("%s: Content-Length %s", target, got)
		}
	}
	if rec := do(h, "HEAD", "/p"); rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != itoa(len(`<ul><li>one</li></ul>`+partialRunner)) {
		t.Errorf("HEAD: %q %s", rec.Body, rec.Header().Get("Content-Length"))
	}
}

// TestPartialDevManifest: in dev, the tail comes from the dev manifest, per
// request, like the head.
func TestPartialDevManifest(t *testing.T) {
	static := t.TempDir()
	t.Setenv("LYR_DEV", "1")
	t.Setenv("LYR_STATIC", static)
	t.Setenv("LYR_PUBLIC", "")
	writeFile(t, filepath.Join(static, "manifest.json"), `{"pages": {"src/pages/p.lyr": [`+
		`{"kind": "component", "url": "/_lyr/List-A.js", "id": "List-1a2b3c4d"}, {"kind": "component", "url": "/_lyr/Card-B.js", "id": "Card-9f8e7d6c"}]}}`)
	s := NewSite([]Route{{Pattern: "/p", File: "src/pages/p.lyr", Render: fragment, Partial: true}}, nil)
	if got, want := do(s.Handler(), "GET", "/p").Body.String(), `<ul><li>one</li></ul>`+strings.Replace(partialRunner, `"dev":false`, `"dev":true`, 1); got != want {
		t.Errorf("dev:\n got %s\nwant %s", got, want)
	}
	writeFile(t, filepath.Join(static, "manifest.json"), `{"pages": {"src/pages/p.lyr": []}}`)
	if got, want := do(s.Handler(), "GET", "/p").Body.String(), `<ul><li>one</li></ul>`; got != want {
		t.Errorf("dev, no scripts: %s", got)
	}
}

// TestPartialCache: a partial is cached like a page, runner included.
func TestPartialCache(t *testing.T) {
	var n atomic.Int32
	s, _ := cacheSite(t, []Route{
		{Pattern: "/p", File: "src/pages/p.lyr", Render: counted(&n, setOpts(hour), "frag"), Assets: runnerAssets, Partial: true},
	})
	h := s.Handler()
	r1, r2 := get(h, "/p"), get(h, "/p")
	want := "frag #1" + partialRunner
	if r1.Body.String() != want || r2.Body.String() != want || r2.Header().Get("X-Lyr-Cache") != "HIT" || n.Load() != 1 {
		t.Fatalf("MISS %q, HIT %q %q, %d renders", r1.Body, r2.Header().Get("X-Lyr-Cache"), r2.Body, n.Load())
	}
	r3 := get(h, "/p", "If-None-Match", r1.Header().Get("Etag"))
	if r3.Code != http.StatusNotModified {
		t.Errorf("conditional: %d", r3.Code)
	}
}

// TestPartialNotFoundAndRewrite: ErrNotFound renders 404.lyr with its own
// head; a rewrite serves the target's rule.
func TestPartialNotFoundAndRewrite(t *testing.T) {
	routes := []Route{
		{Pattern: "/p", File: "src/pages/p.lyr", Render: func(c *Ctx, w *Writer) error { return ErrNotFound }, Assets: runnerAssets, Partial: true},
		{Pattern: "/r", File: "src/pages/r.lyr", Render: func(c *Ctx, w *Writer) error { return c.Rewrite("/full") }, Assets: runnerAssets, Partial: true},
		{Pattern: "/full", File: "src/pages/full.lyr", Render: fragment, Assets: []Asset{{Kind: "css", URL: "/_lyr/full-X.css"}}},
		{Pattern: notFoundPattern, File: "src/pages/404.lyr", Render: fragment, Assets: []Asset{{Kind: "css", URL: "/_lyr/404-X.css"}}},
	}
	h := testSite(t, routes).Handler()
	if rec := do(h, "GET", "/p"); rec.Code != 404 || rec.Body.String() != `<link rel="stylesheet" href="/_lyr/404-X.css"><ul><li>one</li></ul>` {
		t.Errorf("404: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/r"); rec.Code != 200 || rec.Body.String() != `<link rel="stylesheet" href="/_lyr/full-X.css"><ul><li>one</li></ul>` {
		t.Errorf("rewrite: %d %s", rec.Code, rec.Body)
	}
}
