package lyr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevManifest(t *testing.T) {
	static := t.TempDir()
	page := func(c *Ctx, w *Writer) error {
		w.Raw("<!DOCTYPE html><html><head><title>x</title>")
		w.HeadEnd()
		w.Raw("</head><body>hi</body></html>")
		return nil
	}
	routes := []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: page, Assets: []Asset{{Kind: "css", URL: "/_lyr/compiled.css"}}},
		{Pattern: "/other", File: "src/pages/other.lyr", Render: page},
	}
	manifest := filepath.Join(static, "manifest.json")
	t.Setenv("LYR_DEV", "1")
	t.Setenv("LYR_STATIC", static)
	t.Setenv("LYR_PUBLIC", "")
	s := NewSite(routes, nil)
	get := func(path string) string {
		t.Helper()
		rec := do(s.Handler(), "GET", path)
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}

	// No manifest yet: the compiled assets.
	if body := get("/"); !strings.Contains(body, `href="/_lyr/compiled.css"`) {
		t.Errorf("without a manifest: %s", body)
	}

	writeFile(t, manifest, `{"pages": {"src/pages/index.lyr": [{"kind": "css", "url": "/_lyr/index-AAAA.css"}, {"kind": "js", "url": "/_lyr/index-AAAA.js"}]}}`)
	want := `<title>x</title><link rel="stylesheet" href="/_lyr/index-AAAA.css"><script type="module" src="/_lyr/index-AAAA.js"></script></head>`
	if body := get("/"); !strings.Contains(body, want) {
		t.Errorf("from the manifest:\n%s\nwant %s", body, want)
	}
	if body := get("/other"); strings.Contains(body, "<link") || strings.Contains(body, "<script") {
		t.Errorf("page not in the manifest got assets: %s", body)
	}

	// A rewrite (same size, renamed into place as lyr dev does) is picked up.
	tmp := filepath.Join(static, ".tmp")
	writeFile(t, tmp, `{"pages": {"src/pages/index.lyr": [{"kind": "css", "url": "/_lyr/index-BBBB.css"}, {"kind": "js", "url": "/_lyr/index-BBBB.js"}]}}`)
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(tmp, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, manifest); err != nil {
		t.Fatal(err)
	}
	if body := get("/"); !strings.Contains(body, "index-BBBB.css") || strings.Contains(body, "AAAA") {
		t.Errorf("after a rewrite: %s", body)
	}

	// A broken manifest keeps the last good one.
	logs := captureLog(t)
	writeFile(t, manifest, `{"pages":`)
	if body := get("/"); !strings.Contains(body, "index-BBBB.css") {
		t.Errorf("after a broken write: %s", body)
	}
	if !strings.Contains(logs.String(), "dev manifest") {
		t.Errorf("no log line for a broken manifest: %q", logs)
	}

	// Production ignores the file.
	t.Setenv("LYR_DEV", "")
	prod := NewSite(routes, nil)
	if prod.devAssets != nil {
		t.Errorf("production site reads the dev manifest")
	}
}
