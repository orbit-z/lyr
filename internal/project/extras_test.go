package project

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// TestExtrasGolden compares the generated app.go, manifest.go and default
// server with testdata/extras/*.golden (go test ./internal/project -update
// rewrites them). The extras are gofmt-clean.
func TestExtrasGolden(t *testing.T) {
	for _, tt := range []struct {
		name  string
		json  string
		extra map[string]string
	}{
		{"site", `{"site": "https://blog.example.com"}`, nil},
		{"nosite", `{}`, nil},
		{"always", `{"trailingSlash": "always"}`, nil},
		{"sitealways", `{"site": "https://blog.example.com", "trailingSlash": "always"}`, nil},
		// Locals without a RouteInfo field: app.go as without src/locals.
		{"locals", `{}`, map[string]string{"src/locals/locals.go": "package locals\n\ntype Locals struct{ User string }\n"}},
		// A RouteInfo field: app.New() passes WithLocalsRoute (PLAN.md §4g).
		{"route", `{"site": "https://blog.example.com"}`, map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/lyr\"\n\ntype Locals struct {\n\tUser    string\n\tMatched lyr.RouteInfo\n}\n",
		}},
		// A Cache field: app.New() passes WithLocalsCache (PLAN.md §4h).
		{"cache", `{}`, map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/lyr\"\n\ntype Locals struct {\n\tRoute lyr.RouteInfo\n\tCache lyr.Cache\n}\n",
		}},
		// routeRules: WithRouteRules with Go literals (PLAN.md §4h).
		{"rules", `{"routeRules": {"/blog/[...path]": {"maxAge": 300, "swr": 3600, "tags": ["blog", "posts"]}, "/about": {"tags": ["static"]}, "/": {"maxAge": 60}}}`, nil},
		{"tagrules", `{"routeRules": {"/about": {"tags": ["static"]}}}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range siteFiles {
				files[k] = v
			}
			for k, v := range tt.extra {
				files[k] = v
			}
			files["lyr.json"] = tt.json
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
			for _, path := range []string{AppFile, AppEmbedFile, LinkFile, ManifestFile, ServerFile} {
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
				if tt.name != "site" && path != AppFile {
					continue
				}
				name := tt.name
				if name == "locals" {
					name = "nosite" // byte-identical
				}
				golden := filepath.Join("testdata", "extras", name+"."+strings.ReplaceAll(strings.TrimPrefix(path, "lyr_gen/"), "/", "_")+".golden")
				if *update && name == tt.name {
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
