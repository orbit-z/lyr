package project

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

// TestExtrasGolden compares the generated app.go, manifest.go and default
// server with testdata/extras/*.golden (go test ./internal/project -update
// rewrites them). The extras are gofmt-clean.
func TestExtrasGolden(t *testing.T) {
	for _, tt := range []struct {
		name string
		json string
	}{
		{"site", `{"site": "https://blog.example.com"}`},
		{"nosite", `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range siteFiles {
				files[k] = v
			}
			files["orbit.json"] = tt.json
			p := newTestProject(t, files)
			proj, err := Load(p.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if proj.Front() {
				t.Fatal(joinLines(diagStrings(proj.Diagnostics)))
			}
			got := map[string][]byte{}
			for _, f := range proj.GenFiles() {
				got[f.Path] = f.Content
			}
			for _, path := range []string{AppFile, AppEmbedFile, ManifestFile, ServerFile} {
				content, ok := got[path]
				if !ok {
					t.Fatalf("%s not generated", path)
				}
				if !bytes.HasPrefix(content, []byte(compiler.GenHeader+"\n")) {
					t.Errorf("%s: no generated-code marker", path)
				}
				if f, err := format.Source(content); err != nil || !bytes.Equal(f, content) {
					t.Errorf("%s isn't gofmt-clean (%v)", path, err)
				}
				if tt.name == "nosite" && path != AppFile {
					continue
				}
				golden := filepath.Join("testdata", "extras", tt.name+"."+strings.ReplaceAll(strings.TrimPrefix(path, "orbit_gen/"), "/", "_")+".golden")
				if *update {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, content, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run with -update)", err)
				}
				if !bytes.Equal(content, want) {
					t.Errorf("%s differs from %s:\n%s", path, golden, content)
				}
			}
		})
	}
}

func TestPageAlias(t *testing.T) {
	seen := map[string]string{}
	for _, rel := range []string{"pages/a_b/c", "pages/a/b_c", "pages/a_b_c", "pages/blog/slug_param", "pages/p_404"} {
		a := pageAlias(compiler.GenPackage{Rel: rel})
		if prev, dup := seen[a]; dup {
			t.Errorf("%s and %s both alias to %s", prev, rel, a)
		}
		seen[a] = rel
	}
	if got := pageAlias(compiler.GenPackage{Rel: "pages/blog/slug_param"}); got != "pages_blog_slug__param" {
		t.Errorf("alias %s", got)
	}
}
