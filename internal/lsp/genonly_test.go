package lsp

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGenOnlyNamesMatchRuntime checks genOnlyNames against the runtime
// package's doc comments: exactly the exported names (functions, types,
// methods) whose doc starts with "For generated code.".
func TestGenOnlyNamesMatchRuntime(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, n), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name == "orbit" {
			files = append(files, f)
		}
	}
	pkg, err := doc.NewFromFiles(fset, files, "github.com/orbit-z/orbit")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	add := func(name, d string) {
		if strings.HasPrefix(d, genOnlyDoc) {
			want[name] = true
		}
	}
	for _, f := range pkg.Funcs {
		add(f.Name, f.Doc)
	}
	for _, ty := range pkg.Types {
		add(ty.Name, ty.Doc)
		for _, f := range ty.Funcs {
			add(f.Name, f.Doc)
		}
		for _, m := range ty.Methods {
			add(m.Name, m.Doc)
		}
	}
	if len(want) == 0 {
		t.Fatal("no runtime name starts its doc with " + genOnlyDoc)
	}
	for _, n := range slices.Sorted(maps.Keys(want)) {
		if !genOnlyNames[n] {
			t.Errorf("%s: doc starts with %q but it's missing from genOnlyNames", n, genOnlyDoc)
		}
	}
	for _, n := range slices.Sorted(maps.Keys(genOnlyNames)) {
		if !want[n] {
			t.Errorf("%s: in genOnlyNames but no runtime doc comment of that name starts with %q", n, genOnlyDoc)
		}
	}
}

func TestIsGenOnlyItem(t *testing.T) {
	for _, tc := range []struct {
		item string
		want bool
	}{
		{`{"label":"RestString","documentation":"For generated code. RestString is …"}`, true},
		{`{"label":"ErrorAt","documentation":{"kind":"markdown","value":"For generated code. ErrorAt wraps …"}}`, true},
		{`{"label":"String","documentation":"String returns the user's name."}`, false},
		{`{"label":"Route"}`, false}, // no documentation: kept
		{`{"label":"If","documentation":"For generated code. Not in the list."}`, false},
	} {
		if got := isGenOnlyItem(decodeObj(jsonRaw(tc.item))); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.item, got, tc.want)
		}
	}
}
