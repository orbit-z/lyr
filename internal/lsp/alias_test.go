package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAliasImports: tsconfig.json paths aliases in .orbit imports (PLAN.md
// §2.2, §9): import path completion offers alias paths after an alias
// prefix (and relative ones after "."), definition goes to the file, and a
// tsconfig.json change re-resolves the imports.
func TestAliasImports(t *testing.T) {
	const page = "src/pages/index.orbit"
	pageSrc := "---\nimport Card \"@/components/Card.orbit\"\n---\n<Card />\n"
	ws := memWorkspace(t, map[string]string{
		"tsconfig.json":             "{\n  // JSONC\n  \"compilerOptions\": { \"paths\": { \"@/*\": [\"src/*\"], }, },\n}\n",
		"src/components/Card.orbit": "<p>card</p>\n",
		"src/components/Box.orbit":  "<p>box</p>\n",
		"src/content/note.md":       "# Note\n",
		page:                        pageSrc,
	})
	if ds := ws.proj.Diagnostics; len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	f := ws.byPath[page]

	complete := func(src, marker string) []string {
		t.Helper()
		ws.setSource(page, src)
		f = ws.byPath[page]
		off := strings.Index(src, marker) + len(marker)
		items, ok := ws.importPathItems(cursor{ws: ws, f: f, lines: ws.lines[page], path: page, off: off}, "/")
		if !ok {
			t.Fatalf("%q: not an import path", marker)
		}
		var got []string
		for _, it := range items {
			got = append(got, it.(map[string]any)["label"].(string))
		}
		return got
	}
	got := complete("---\nimport Card \"@/components/Card.orbit\"\nimport Box \"@/\"\n---\n<Card /><Box />\n", `Box "@/`)
	if !slices.Contains(got, "@/components/Box.orbit") || !slices.Contains(got, "@/content/note.md") || slices.Contains(got, "@/components/Card.orbit") {
		t.Errorf(`"@/: %v, want Box and note.md, not the imported Card`, got)
	}
	got = complete("---\nimport Box \"../\"\n---\n<Box />\n", `Box "../`)
	if !slices.Contains(got, "../components/Box.orbit") {
		t.Errorf(`"../: %v, want relative paths`, got)
	}

	// Definition on the alias path goes to the file.
	ws.setSource(page, pageSrc)
	f = ws.byPath[page]
	reg := classify(f, strings.Index(pageSrc, "@/")+1)
	loc, ok := ws.nativeDefinition(f, reg)
	if !ok || loc == nil || !strings.HasSuffix(loc.URI, "/src/components/Card.orbit") {
		t.Errorf("definition on the alias path: %+v %v", loc, ok)
	}

	// Without the alias, the import is an error; with it back, it isn't.
	if err := os.WriteFile(filepath.Join(ws.root, "tsconfig.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.reloadAliases()
	if ds := ws.proj.Diagnostics; len(ds) == 0 || !strings.Contains(ds[0].Message, `no tsconfig.json paths alias matches "@/components/Card.orbit"`) {
		t.Errorf("after removing the alias: %v", ds)
	}
	if err := os.WriteFile(filepath.Join(ws.root, "tsconfig.json"), []byte(`{"compilerOptions": {"baseUrl": ".", "paths": {"@/*": ["src/*"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.reloadAliases()
	if ds := ws.proj.Diagnostics; len(ds) != 0 {
		t.Errorf("after restoring the alias: %v", ds)
	}
}
