package project

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDetectLocals(t *testing.T) {
	const self = testModule + "/src/locals"
	tests := []struct {
		name  string
		files map[string]string
		want  string
		route string // the orbit.RouteInfo field
		cache string // the orbit.Cache field
		diags []string
	}{
		{name: "no src/locals", files: map[string]string{"src/pages/index.orbit": ""}},
		{name: "empty src/locals", files: map[string]string{"src/locals/README.md": "x"}},
		{name: "struct", files: map[string]string{
			"src/locals/locals.go": "package locals\n\ntype Locals struct {\n\tRequestID, Theme string\n}\n",
		}, want: self},
		{name: "in a type group, another file", files: map[string]string{
			"src/locals/doc.go":      "// Package locals is the request data.\npackage locals\n",
			"src/locals/types.go":    "package locals\n\ntype (\n\tUser struct{ Name string }\n\tLocals struct{ User *User }\n)\n",
			"src/locals/x_test.go":   "package locals_test\n",
			"src/locals/ignored.go":  "//go:build ignore\n\npackage main\n",
			"src/locals/_scratch.go": "package scratch\n",
		}, want: self},
		{name: "alias", files: map[string]string{
			"src/locals/locals.go":  "package locals\n\nimport \"example.com/blog/internal/auth\"\n\ntype Locals = auth.Session\n",
			"internal/auth/auth.go": "package auth\n\ntype Session struct{ User string }\n",
		}, want: self},
		{name: "no Locals: ignored", files: map[string]string{
			"src/locals/locals.go": "package locals\n\ntype Local struct{}\n",
		}},
		{name: "not a struct", files: map[string]string{
			"src/locals/locals.go": "package locals\n\ntype Locals map[string]any\n",
		}, diags: []string{"src/locals/locals.go:3:6: Locals must be a struct type: type Locals struct { … }"}},
		{name: "wrong package name", files: map[string]string{
			"src/locals/locals.go": "package middleware\n\ntype Locals struct{}\n",
		}, diags: []string{"src/locals/locals.go:1:9: src/locals must be package locals, not package middleware"}},
		{name: "imports app", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport (\n\t\"fmt\"\n\t_ \"example.com/blog/orbit_gen/app\"\n)\n\nvar _ = fmt.Sprint\n\ntype Locals struct{}\n",
		}, diags: []string{"src/locals/locals.go:5:4: src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle"}},
		{name: "imports an impl package", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport _ \"example.com/blog/orbit_gen/impl/components/card\"\n\ntype Locals struct{}\n",
		}, diags: []string{"src/locals/locals.go:3:10: src/locals can't import example.com/blog/orbit_gen/impl/components/card: pages that use locals import src/locals, so this would be an import cycle"}},
		{name: "imports orbit_gen/link", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport _ \"example.com/blog/orbit_gen/link\"\n\ntype Locals struct{}\n",
		}, diags: []string{"src/locals/locals.go:3:10: src/locals can't import example.com/blog/orbit_gen/link: pages that use locals import src/locals, so this would be an import cycle"}},
		{name: "a decl package is fine", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport _ \"example.com/blog/orbit_gen/decl/components/card\"\n\ntype Locals struct{}\n",
		}, want: self},
		{name: "indirect", files: map[string]string{
			"src/locals/locals.go":    "package locals\n\nimport (\n\t\"example.com/blog/internal/auth\"\n\t\"example.com/blog/internal/ok\"\n)\n\ntype Locals struct{ U auth.U; O ok.O }\n",
			"internal/auth/auth.go":   "package auth\n\nimport _ \"example.com/blog/internal/site\"\n\ntype U struct{}\n",
			"internal/site/site.go":   "package site\n\nimport _ \"example.com/blog/orbit_gen/app\"\n",
			"internal/site/a_test.go": "package site\n",
			"internal/ok/ok.go":       "package ok\n\nimport _ \"example.com/blog/internal/auth/sub\"\n\ntype O struct{}\n",
		}, diags: []string{"src/locals/locals.go:4:2: src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle " +
			"(src/locals → example.com/blog/internal/auth → example.com/blog/internal/site → example.com/blog/orbit_gen/app)"}},
		{name: "ordinary cycle left to go build", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport _ \"example.com/blog/internal/a\"\n\ntype Locals struct{}\n",
			"internal/a/a.go":      "package a\n\nimport _ \"example.com/blog/internal/b\"\n",
			"internal/b/b.go":      "package b\n\nimport _ \"example.com/blog/internal/a\"\nimport _ \"example.com/blog/src/locals\"\n",
		}, want: self},
		{name: "RouteInfo field", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct {\n\tUser  string\n\tRoute orbit.RouteInfo\n}\n",
		}, want: self, route: "Route"},
		{name: "RouteInfo field, any name, aliased import", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport o \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ Matched o.RouteInfo }\n",
		}, want: self, route: "Matched"},
		{name: "RouteInfo field, dot import", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport . \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ R RouteInfo }\n",
		}, want: self, route: "R"},
		{name: "another package's RouteInfo", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"example.com/blog/internal/orbit\"\n\ntype Locals struct{ Route orbit.RouteInfo }\n",
			"internal/orbit/o.go":  "package orbit\n\ntype RouteInfo struct{}\n",
		}, want: self},
		{name: "two RouteInfo fields", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct {\n\tRoute orbit.RouteInfo\n\tOther orbit.RouteInfo\n}\n",
		}, diags: []string{"src/locals/locals.go:7:2: Locals has two orbit.RouteInfo fields (Route, Other); Orbit fills one"}},
		{name: "two RouteInfo names in one field", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ A, B orbit.RouteInfo }\n",
		}, diags: []string{"src/locals/locals.go:5:24: Locals has two orbit.RouteInfo fields (A, B); Orbit fills one"}},
		{name: "embedded RouteInfo", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct {\n\torbit.RouteInfo\n}\n",
		}, diags: []string{"src/locals/locals.go:6:2: Locals can't embed orbit.RouteInfo: give the field a name, such as Route orbit.RouteInfo"}},
		{name: "pointer RouteInfo", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ Route *orbit.RouteInfo }\n",
		}, diags: []string{"src/locals/locals.go:5:21: Locals field Route must be an orbit.RouteInfo, not a pointer: Orbit fills it in place"}},
		{name: "Cache and RouteInfo fields", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct {\n\tRoute orbit.RouteInfo\n\tC     orbit.Cache\n}\n",
		}, want: self, route: "Route", cache: "C"},
		{name: "two Cache fields", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ A, B orbit.Cache }\n",
		}, diags: []string{"src/locals/locals.go:5:24: Locals has two orbit.Cache fields (A, B); Orbit uses one"}},
		{name: "pointer and embedded Cache", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct {\n\torbit.Cache\n\tK *orbit.Cache\n\tk orbit.Cache\n}\n",
		}, diags: []string{
			"src/locals/locals.go:6:2: Locals can't embed orbit.Cache: give the field a name, such as Cache orbit.Cache",
			"src/locals/locals.go:7:2: Locals field K must be an orbit.Cache, not a pointer: Orbit uses it in place",
			"src/locals/locals.go:8:2: Locals field k must be exported for Orbit to use it, such as Cache orbit.Cache",
		}},
		{name: "unexported RouteInfo", files: map[string]string{
			"src/locals/locals.go": "package locals\n\nimport \"github.com/orbit-z/orbit\"\n\ntype Locals struct{ route orbit.RouteInfo }\n",
		}, diags: []string{"src/locals/locals.go:5:21: Locals field route must be exported for Orbit to fill it, such as Route orbit.RouteInfo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProject(t, tt.files)
			got, fields, ds := detectLocals(p.Dir, testModule)
			if got != tt.want {
				t.Errorf("import path %q, want %q", got, tt.want)
			}
			if fields.Route != tt.route || fields.Cache != tt.cache {
				t.Errorf("fields %+v, want route %q, cache %q", fields, tt.route, tt.cache)
			}
			if g, w := joinLines(diagStrings(ds)), joinLines(tt.diags); g != w {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", g, w)
			}
		})
	}
}

// TestLoadLocals checks that Load sets Options.Locals and Front reports
// the src/locals problems.
func TestLoadLocals(t *testing.T) {
	p := newTestProject(t, map[string]string{
		"src/pages/index.orbit": "<p>{locals != nil}</p>\n", // only a page using locals imports it
		"src/locals/locals.go":  "package locals\n\ntype Locals struct{}\n",
	})
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Options.Locals != testModule+"/src/locals" {
		t.Errorf("Options.Locals = %q", proj.Options.Locals)
	}
	if proj.Front() {
		t.Fatalf("errors: %v", diagStrings(proj.Diagnostics))
	}
	if !strings.Contains(string(proj.Files[0].Pass1.Impl.Content), `orbit__locals "example.com/blog/src/locals"`) {
		t.Errorf("impl doesn't import src/locals:\n%s", proj.Files[0].Pass1.Impl.Content)
	}

	p.write("src/locals/locals.go", "package locals\n\nimport _ \"example.com/blog/orbit_gen/app\"\n\ntype Locals struct{}\n")
	proj, err = Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !proj.Front() || proj.Options.Locals != "" {
		t.Fatalf("no error; Options.Locals %q, diagnostics %v", proj.Options.Locals, diagStrings(proj.Diagnostics))
	}
	want := "src/locals/locals.go:3:10: src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle"
	if got := diagStrings(proj.Diagnostics); len(got) != 1 || got[0] != want {
		t.Errorf("diagnostics %q, want %q", got, want)
	}
}

// TestDetectLocalsTime logs what detection and the import check cost on a
// module with a chain of 50 packages behind src/locals.
func TestDetectLocalsTime(t *testing.T) {
	files := map[string]string{
		"src/locals/locals.go": "package locals\n\nimport _ \"example.com/blog/internal/p0\"\n\ntype Locals struct{}\n",
	}
	for i := range 50 {
		imp := ""
		if i < 49 {
			imp = "import _ \"example.com/blog/internal/p" + itoa(i+1) + "\"\n"
		}
		files["internal/p"+itoa(i)+"/p.go"] = "package p\n\n" + imp + "import \"fmt\"\n\nvar _ = fmt.Sprint\n"
	}
	p := newTestProject(t, files)
	if _, err := os.Stat(filepath.Join(p.Dir, "src/locals")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, _, ds := detectLocals(p.Dir, testModule)
	d := time.Since(start)
	if got == "" || len(ds) > 0 {
		t.Fatalf("%q %v", got, diagStrings(ds))
	}
	t.Logf("detectLocals over 51 packages: %v", d)
}

func itoa(i int) string { return strconv.Itoa(i) }
