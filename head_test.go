package lyr

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadTags(t *testing.T) {
	tests := []struct {
		name   string
		assets []Asset
		want   string
	}{
		{"css and js", []Asset{{Kind: "css", URL: "/_lyr/a.css"}, {Kind: "js", URL: "/_lyr/a.js"}},
			`<link rel="stylesheet" href="/_lyr/a.css"><script type="module" src="/_lyr/a.js"></script>`},
		{"modulepreload after the script, in asset order", []Asset{
			{Kind: "css", URL: "/_lyr/a.css"},
			{Kind: "js", URL: "/_lyr/a.js"},
			{Kind: "lazy", URL: "/_lyr/L.js", ID: "L-1"},
			{Kind: "modulepreload", URL: "/_lyr/chunk-B.js"},
			{Kind: "modulepreload", URL: "/_lyr/chunk-A.js"},
		}, `<link rel="stylesheet" href="/_lyr/a.css"><script type="module" src="/_lyr/a.js"></script>` +
			`<link rel="modulepreload" href="/_lyr/chunk-B.js"><link rel="modulepreload" href="/_lyr/chunk-A.js">`},
		{"escaped URL", []Asset{{Kind: "modulepreload", URL: `/_lyr/a"&<.js`}},
			`<link rel="modulepreload" href="/_lyr/a&quot;&amp;&lt;.js">`},
		{"unknown kind ignored", []Asset{{Kind: "prefetch", URL: "/x.js"}}, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headTags("", tt.assets); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestHeadInjectionModulePreload(t *testing.T) {
	routes := []Route{{
		Pattern: "/", File: "src/pages/index.lyr",
		Render: func(c *Ctx, w *Writer) error {
			w.Raw("<html><head><title>x</title>")
			w.HeadEnd()
			w.Raw("</head><body></body></html>")
			return nil
		},
		Assets: []Asset{{Kind: "js", URL: "/_lyr/a.js"}, {Kind: "modulepreload", URL: "/_lyr/chunk-A.js"}},
	}}
	rec := do(testSite(t, routes).Handler(), "GET", "/")
	want := `<html><head><title>x</title><script type="module" src="/_lyr/a.js"></script><link rel="modulepreload" href="/_lyr/chunk-A.js"></head><body></body></html>`
	if rec.Body.String() != want {
		t.Fatalf("body = %q", rec.Body)
	}
}

func TestDevManifestModulePreload(t *testing.T) {
	static := t.TempDir()
	page := func(c *Ctx, w *Writer) error {
		w.Raw("<head>")
		w.HeadEnd()
		w.Raw("</head>")
		return nil
	}
	t.Setenv("LYR_DEV", "1")
	t.Setenv("LYR_STATIC", static)
	t.Setenv("LYR_PUBLIC", "")
	writeFile(t, filepath.Join(static, "manifest.json"), `{"pages": {"src/pages/index.lyr": [{"kind": "js", "url": "/_lyr/index-A.js"}, {"kind": "modulepreload", "url": "/_lyr/chunk-B.js"}]}}`)
	s := NewSite([]Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}}, nil)
	rec := do(s.Handler(), "GET", "/")
	want := `<script type="module" src="/_lyr/index-A.js"></script><link rel="modulepreload" href="/_lyr/chunk-B.js"></head>`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body = %s\nwant %s", rec.Body, want)
	}
}
