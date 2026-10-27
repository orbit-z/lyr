package markdown

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

// fakeImporter type-checks the runtime stand-in and the layout's decl
// package from source; everything else (time) comes from GOROOT sources.
type fakeImporter struct {
	fset *token.FileSet
	std  types.Importer
	srcs map[string]string
	pkgs map[string]*types.Package
}

func (f *fakeImporter) Import(path string) (*types.Package, error) {
	if p := f.pkgs[path]; p != nil {
		return p, nil
	}
	src, ok := f.srcs[path]
	if !ok {
		return f.std.Import(path)
	}
	file, err := parser.ParseFile(f.fset, path+"/decl.go", src, 0)
	if err != nil {
		return nil, err
	}
	conf := types.Config{Importer: f}
	p, err := conf.Check(path, f.fset, []*ast.File{file}, nil)
	if err != nil {
		return nil, err
	}
	f.pkgs[path] = p
	return p, nil
}

const layoutDecl = `package post

import (
	orbit "github.com/orbit-z/orbit"
	"time"
)

type Kind string

type Author struct {
	Name string
	URL  string
}

type Base struct{ Section string }

type Props struct {
	Base
	Title    string
	Date     time.Time
	Tags     []string
	Draft    bool
	Order    uint8
	Weight   float32
	Kind     Kind
	Intro    orbit.HTML
	Author   Author
	Authors  []Author
	Links    map[string]string
	Meta     struct{ Lang string }
	OnClick  func()
	Subtitle orbit.Opt[string]
	Rank     orbit.Opt[Kind]
	Note     orbit.Opt[string]
	Headings []orbit.Heading
}
`

func retype(t *testing.T, src string) (*compiler.Output, []compiler.Diagnostic, *Doc) {
	t.Helper()
	fset := token.NewFileSet()
	imp := &fakeImporter{fset: fset, std: importer.ForCompiler(fset, "source", nil), pkgs: map[string]*types.Package{},
		srcs: map[string]string{
			compiler.RuntimePath:                           "package orbit\n\ntype Heading struct {\n\tDepth int\n\tSlug, Text string\n}\n\ntype HTML string\n\ntype Opt[T any] struct {\n\tv  T\n\tok bool\n}\n\nfunc Some[T any](v T) Opt[T] { return Opt[T]{v, true} }\n",
			"example.com/blog/orbit_gen/decl/layouts/post": layoutDecl,
		}}
	d := Parse("src/pages/blog/hello.md", src, Options{}, nil, nil)
	if len(d.Diagnostics) > 0 {
		t.Fatalf("parse: %s", diags(d))
	}
	layout := &compiler.ComponentInfo{Path: "src/layouts/Post.orbit", Name: "Post",
		Pkg: compiler.GenPackage{Rel: "layouts/post", Name: "post"}, Props: compiler.PropsInfo{Declared: true}}
	in := GenInput{
		Info:    compiler.FileInfo{Path: "src/pages/blog/hello.md", Role: compiler.RolePage, Route: "/blog/hello"},
		Pkg:     compiler.GenPackage{Rel: "pages/blog/hello_md", Name: "hello_md"},
		Options: compiler.Options{Module: "example.com/blog"},
		Index:   compiler.ComponentIndex{"src/layouts/Post.orbit": layout},
	}
	pass1 := Generate(d, in)
	out, ds, ok := Retype(d, in, pass1)(imp)
	if !ok {
		t.Fatal("Retype fell back")
	}
	return out, ds, d
}

func TestRetype(t *testing.T) {
	src := `---
title: 3
date: 2026-10-26
tags: [news, go]
draft: true
order: 200
weight: 1.5
kind: post
intro: <b>Hi</b>
author: {name: Ada, url: "https://ada.dev"}
authors:
  - name: Grace
links: {home: /}
meta: {lang: en}
section: Blog
subtitle: Hello
rank: post
note: null
layout: ../../layouts/Post.orbit
---
## One
`
	out, ds, _ := retype(t, src)
	if len(ds) > 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	impl := string(out.Impl.Content)
	for _, w := range []string{
		"//line ../../../../../src/pages/blog/hello.md:2:1\np.Title = \"3\"\n",
		`p.Date = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)`,
		`p.Tags = []string{"news", "go"}`,
		`p.Draft = true`,
		`p.Order = 200`,
		`p.Weight = 1.5`,
		`p.Kind = "post"`,
		`p.Intro = "<b>Hi</b>"`,
		`p.Author = Layout.Author{Name: "Ada", URL: "https://ada.dev"}`,
		`p.Authors = []Layout.Author{Layout.Author{Name: "Grace"}}`,
		`p.Links = map[string]string{"home": "/"}`,
		`p.Meta = struct{Lang string}{Lang: "en"}`,
		`p.Section = "Blog"`,
		`p.Subtitle = orbit__rt.Some[string]("Hello")`,
		`p.Rank = orbit__rt.Some[Layout.Kind]("post")`,
		"\np.Headings = Headings\n",
		"\t\"time\"\n",
	} {
		if !strings.Contains(impl, w) {
			t.Errorf("no %q in:\n%s", w, impl)
		}
	}
	if strings.Contains(impl, "p.Note") {
		t.Errorf("note: null sets p.Note:\n%s", impl)
	}
	// The reset directive names the line after it.
	lines := strings.Split(impl, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "//line render.go:") && l != fmt.Sprintf("//line render.go:%d:1", i+2) {
			t.Errorf("line %d: %s", i+1, l)
		}
	}
}

func TestRetypeErrors(t *testing.T) {
	tests := []struct{ yaml, want string }{
		{"draft: \"yes\"", `2:8: frontmatter draft: "yes" isn't a bool (Post.Props.Draft is bool)`},
		{"order: 300", "2:8: frontmatter order: 300 overflows uint8 (Post.Props.Order is uint8)"},
		{"order: -1", "2:8: frontmatter order: -1 overflows uint8 (Post.Props.Order is uint8)"},
		{"order: 1.5", "2:8: frontmatter order: 1.5 isn't an integer (Post.Props.Order is uint8)"},
		{"weight: 1e40", "2:9: frontmatter weight: 1e40 overflows float32 (Post.Props.Weight is float32)"},
		{"title: [a]", "2:8: frontmatter title: a list isn't a string (Post.Props.Title is string)"},
		{"tags: news", `2:7: frontmatter tags: "news" isn't a list (Post.Props.Tags is []string)`},
		{"tags: [a, [b]]", "2:7: frontmatter tags: a list's items must all have the same type (string and []string)"},
		{"date: \"2026-10-26\"", `2:7: frontmatter date: "2026-10-26" isn't a date (Post.Props.Date is time.Time)`},
		{"author: {nme: Ada}", `2:10: frontmatter author: unknown key "nme" for Post.Props.Author (fields: name, url)`},
		{"authors: [{name: 3}]", ""},
		{"authors: [{name: [x]}]", "2:18: frontmatter authors[0].name: a list isn't a string (Post.Props.Authors[0].Name is string)"},
		{"title: null", `2:8: frontmatter key "title" has no value; delete the line or give it a value`},
		{"subtitle: [a]", "2:11: frontmatter subtitle: a list isn't a string (Post.Props.Subtitle is orbit.Opt[string])"},
		{"on-click: x", "2:1: frontmatter on-click: Post.Props.OnClick is func(), which frontmatter can't set"},
		{"headings: x", `2:1: frontmatter key "headings" can't be set: Orbit fills Post.Props.Headings with the page's headings; rename the key`},
		{"subtitel: x", `2:1: unknown frontmatter key "subtitel" for layout Post (props: base, title, date, tags, draft, order, weight, kind, intro, author, authors, links, meta, onClick, subtitle, rank, note, headings, section)`},
	}
	for _, tt := range tests {
		t.Run(tt.yaml, func(t *testing.T) {
			src := "---\n" + tt.yaml + "\nlayout: ../../layouts/Post.orbit\n---\n"
			d := Parse("src/pages/blog/hello.md", src, Options{}, nil, nil)
			if len(d.Diagnostics) > 0 {
				if got := diags(d); got != tt.want {
					t.Errorf("parse:\n%s\nwant:\n%s", got, tt.want)
				}
				return
			}
			_, ds, d := retype(t, src)
			var got []string
			for _, x := range ds {
				p := d.Lines.Position(x.Start)
				got = append(got, fmt.Sprintf("%d:%d: %s", p.Line, p.Col, x.Message))
			}
			if s := strings.Join(got, "\n"); s != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", s, tt.want)
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	src := "---\ntitle: Hi\ntilte: typo\nlayout: ../layouts/Post.orbit\n---\n# Hi\n"
	d := Parse("src/pages/hi.md", src, Options{}, nil, nil)
	layout := &compiler.ComponentInfo{Path: "src/layouts/Post.orbit", Name: "Post", Pkg: compiler.GenPackage{Rel: "layouts/post", Name: "post"},
		Props: compiler.PropsInfo{Declared: true, Listable: true, Fields: []compiler.PropField{{Name: "Title", Type: "string"}, {Name: "Headings", Type: "[]orbit.Heading"}}}}
	in := GenInput{
		Info:    compiler.FileInfo{Path: "src/pages/hi.md", Role: compiler.RolePage, Route: "/hi"},
		Pkg:     compiler.GenPackage{Rel: "pages/hi_md", Name: "hi_md"},
		Options: compiler.Options{Module: "example.com/blog"},
		Index:   compiler.ComponentIndex{"src/layouts/Post.orbit": layout},
	}
	out := Generate(d, in)
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Message != `unknown frontmatter key "tilte" for layout Post (props: title, headings)` ||
		d.Lines.Position(out.Diagnostics[0].Start) != (compiler.Position{Line: 3, Col: 1}) {
		t.Errorf("diagnostics: %v", out.Diagnostics)
	}
	decl := string(out.Decl.Content)
	for _, w := range []string{"package hi_md\n", "type Params struct{}", "var Frontmatter = struct {\n\tTitle string\n\tTilte string\n}{",
		`{Depth: 1, Slug: "hi", Text: "Hi"},`, `const URL = "/hi"`, "Params{})"} {
		if !strings.Contains(decl, w) {
			t.Errorf("no %q in decl:\n%s", w, decl)
		}
	}
	impl := string(out.Impl.Content)
	for _, w := range []string{"p.Title = \"Hi\"\n//line render.go:", "p.Headings = Headings", `return Layout.Render(orbit__ctx, orbit__w, orbit__lprops, orbit__rt.Slots{Default: orbit__content})`} {
		if !strings.Contains(impl, w) {
			t.Errorf("no %q in impl:\n%s", w, impl)
		}
	}
	in.Index = nil
	if out := Generate(d, in); len(out.Diagnostics) != 1 || out.Diagnostics[0].Message != "layout ../layouts/Post.orbit: no such file" {
		t.Errorf("missing layout: %v", out.Diagnostics)
	}
}
