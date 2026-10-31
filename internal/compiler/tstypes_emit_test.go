package compiler

import (
	gotoken "go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tsDumpFiles concatenates emitted files for a golden file.
func tsDumpFiles(files []TSFile) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString("=== " + f.Path + " ===\n")
		b.Write(f.Content)
	}
	return b.String()
}

func TestEmitTypesEmpty(t *testing.T) {
	files := EmitTypes(nil)
	if len(files) != 2 {
		t.Fatalf("EmitTypes(nil) = %d files, want 2", len(files))
	}
	golden(t, "testdata/tstypes/empty.golden", tsDumpFiles(files))
}

// TestEmitTypes emits the modules of hand-built units whose types come from
// the mapping test packages: a component with data-* keys (a union, unknown
// keys, a non-identifier key, name collisions, a frontmatter namespace), a
// page with only set:json blocks and a generic, and a unit without keys (no
// module).
func TestEmitTypes(t *testing.T) {
	pkg, fset := tsLoadTest(t)
	// example.com/string stands in for types declared in a frontmatter.
	nsOf := func(p *types.Package) tsNS {
		if p.Path() == "example.com/string" {
			return tsNS{Key: "lyr:src/components/Card.lyr", Base: "Card", Path: "src/components/Card"}
		}
		return tsTestNS(p)
	}
	unit := func(path, attr string, specs [][]string) *UnitTypes {
		m := newTSMapper(nsOf)
		u := &UnitTypes{Path: path}
		var keys []TSKey
		for _, s := range specs {
			k := TSKey{Name: s[0]}
			for i, typ := range s[1:] {
				ts := "unknown"
				if typ != "unknown" {
					tv, err := types.Eval(fset, pkg, tsEvalPos(pkg), typ)
					if err != nil {
						t.Fatal(err)
					}
					ts, _ = m.Map(tv.Type)
				}
				k.Types = append(k.Types, ts)
				k.Sites = append(k.Sites, attr+s[0]+" ("+filepath.Base(path)+":"+string(rune('1'+i))+":14)")
			}
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(a, b TSKey) int { return strings.Compare(a.Name, b.Name) })
		if attr == "data-" {
			u.Data = keys
		} else {
			u.JSON = keys
		}
		u.Decls = m.Decls()
		return u
	}
	units := []*UnitTypes{
		unit("src/pages/index.lyr", "#", [][]string{{"config", "map[string]Status"}, {"page", "Page[Post]"}}),
		unit("src/components/LikeButton.lyr", "data-", [][]string{
			{"post", "Post"},
			{"liked", "bool"},
			{"item", "iposts.Post", "lposts.Post"},
			{"raw", "unknown"},
			{"mixed", "int", "any"},
			{"1x", "[]*int"},
			{"card", "str.T", "Names"},
			{"keyword", "number"},
		}),
		unit("src/components/Empty.lyr", "data-", nil),
	}
	files := EmitTypes(units)
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
		if !strings.HasPrefix(string(f.Content), GenHeader+"\n") {
			t.Errorf("%s doesn't start with the generated-code header", f.Path)
		}
	}
	want := []string{
		"lyr_gen/types/client.d.ts",
		"lyr_gen/types/go.d.ts",
		"lyr_gen/types/src/components/LikeButton.lyr.d.ts",
		"lyr_gen/types/src/pages/index.lyr.d.ts",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
	golden(t, "testdata/tstypes/units.golden", tsDumpFiles(files))
	// Deterministic, whatever the order of the units.
	slices.Reverse(units)
	if again := tsDumpFiles(EmitTypes(units)); again != tsDumpFiles(files) {
		t.Error("EmitTypes depends on the order of the units")
	}
}

func TestTSNamespaceNames(t *testing.T) {
	ns := func(path, base string) tsNS { return tsNS{Key: path, Base: base, Path: path} }
	var decls []TSDecl
	for _, s := range []tsNS{
		ns("example.com/internal/posts", "posts"),
		ns("example.com/legacy/posts", "posts"),
		ns("a.com/x/feed", "feed"),
		ns("b.com/x/feed", "feed"),
		ns("c.com/feed", "feed"),
		ns("example.com/yaml/v3", "yaml"),
		ns("example.com/default", "default"),
		ns("example.com/my-pkg/x", "x"),
		{Key: "lyr:src/components/like-button.lyr", Base: "like-button", Path: "src/components/like-button"},
		{Key: "lyr:src/components/Data.lyr", Base: "Data", Path: "src/components/Data"},
		ns("example.com/Data_", "Data_"),
	} {
		decls = append(decls, TSDecl{Ref: s.Key + "\x01T", NS: s, Name: "T"})
	}
	n := newTSNames(decls)
	want := map[string]string{
		"example.com/internal/posts":         "internal_posts",
		"example.com/legacy/posts":           "legacy_posts",
		"a.com/x/feed":                       "a_com_x_feed",
		"b.com/x/feed":                       "b_com_x_feed",
		"c.com/feed":                         "c_com_feed",
		"example.com/yaml/v3":                "yaml",
		"example.com/default":                "default_",
		"example.com/my-pkg/x":               "x",
		"lyr:src/components/like-button.lyr": "like_button",
		"example.com/Data_":                  "Data_",
		"lyr:src/components/Data.lyr":        "Data__2",
	}
	for k, v := range want {
		if n.ns[k] != v {
			t.Errorf("namespace of %s = %q, want %q", k, n.ns[k], v)
		}
	}
}

// TestTSLocalTypes: function-local types with the same name get distinct
// refs and TS names.
func TestTSLocalTypes(t *testing.T) {
	l := &tyTestLoader{pkgs: map[string]string{"example.com/loc": `package loc
type T struct{ A int }
func F() any { type T struct{ B int }; return T{} }
func G() any { type T struct{ C int }; return T{} }
`}}
	imp, err := l.Load(t.Context(), gotoken.NewFileSet(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := imp.Import("example.com/loc")
	if err != nil {
		t.Fatal(err)
	}
	m := newTSMapper(tsTestNS)
	m.Map(p.Scope().Lookup("T").Type())
	for _, fn := range []string{"F", "G"} {
		_, obj := p.Scope().Lookup(fn).(*types.Func).Scope().LookupParent("T", gotoken.NoPos)
		if obj == nil || obj.Parent() == p.Scope() {
			s := p.Scope().Lookup(fn).(*types.Func).Scope()
			obj = s.Child(0).Lookup("T")
		}
		m.Map(obj.Type())
	}
	got := tsDescribe("", nil, m.Decls(), p)
	want := "\nloc.T { A: number }\nloc.T_2 { B: number }\nloc.T_3 { C: number }"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
