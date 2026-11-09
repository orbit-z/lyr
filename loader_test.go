package lyr

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/loader"
)

// TestCheckLoader: NewSite logs a loader file that pages use and the
// static files lack (a dist/ from another lyr version), and nothing when
// the files are there or no page needs them.
func TestCheckLoader(t *testing.T) {
	t.Setenv("LYR_DEV", "")
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_KEY", "")
	islandPage := Route{Pattern: "/i", File: "src/pages/i.lyr", Render: headPage("i"), Islands: true}
	partial := Route{Pattern: "/p", File: "src/pages/p.lyr", Render: fragment, Islands: true, Partial: true,
		Assets: []Asset{{Kind: "css", URL: loader.WrappersURL}}}
	plain := Route{Pattern: "/x", File: "src/pages/x.lyr", Render: headPage("x")}
	full := t.TempDir()
	for _, f := range loader.Files {
		writeFile(t, filepath.Join(full, "_lyr", filepath.FromSlash(f.Path)), f.Contents)
	}
	for _, tt := range []struct {
		name   string
		routes []Route
		dir    string
		want   []string
	}{
		{"missing", []Route{islandPage, partial}, t.TempDir(), []string{loader.LoaderURL, loader.WrappersURL}},
		{"page only", []Route{islandPage}, t.TempDir(), []string{loader.LoaderURL}},
		{"present", []Route{islandPage, partial}, full, nil},
		{"unused", []Route{plain}, t.TempDir(), nil},
	} {
		var logged bytes.Buffer
		log.SetOutput(&logged)
		NewSite(tt.routes, nil, WithStaticDir(tt.dir))
		log.SetOutput(os.Stderr)
		if n := strings.Count(logged.String(), "\n"); n != len(tt.want) {
			t.Errorf("%s: log = %q, want %d lines", tt.name, logged.String(), len(tt.want))
		}
		for _, url := range tt.want {
			if !strings.Contains(logged.String(), url+", which pages use") {
				t.Errorf("%s: log = %q, want %s", tt.name, logged.String(), url)
			}
		}
	}
}
