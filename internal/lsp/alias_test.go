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

// TestRootImports: root-relative import paths ("/src/…", PLAN.md §2.2):
// completion after `"/` offers them, and definition goes to the file.
func TestRootImports(t *testing.T) {
	const page = "src/pages/index.orbit"
	pageSrc := "---\nimport Card \"/src/components/Card.orbit\"\n---\n<Card />\n"
	ws := memWorkspace(t, map[string]string{
		"src/components/Card.orbit": "<p>card</p>\n",
		"src/components/Box.orbit":  "<p>box</p>\n",
		"src/content/note.md":       "# Note\n",
		page:                        pageSrc,
	})
	if ds := ws.proj.Diagnostics; len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	src := "---\nimport Card \"/src/components/Card.orbit\"\nimport Box \"/\"\n---\n<Card /><Box />\n"
	ws.setSource(page, src)
	f := ws.byPath[page]
	off := strings.Index(src, `Box "/`) + len(`Box "/`)
	items, ok := ws.importPathItems(cursor{ws: ws, f: f, lines: ws.lines[page], path: page, off: off}, "/")
	if !ok {
		t.Fatal(`"/: not an import path`)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.(map[string]any)["label"].(string))
	}
	if !slices.Contains(got, "/src/components/Box.orbit") || !slices.Contains(got, "/src/content/note.md") || slices.Contains(got, "/src/components/Card.orbit") {
		t.Errorf(`"/: %v, want Box and note.md, not the imported Card`, got)
	}

	ws.setSource(page, pageSrc)
	f = ws.byPath[page]
	loc, ok := ws.nativeDefinition(f, classify(f, strings.Index(pageSrc, "/src/")+1))
	if !ok || loc == nil || !strings.HasSuffix(loc.URI, "/src/components/Card.orbit") {
		t.Errorf("definition on the root-relative path: %+v %v", loc, ok)
	}
}

// TestTagAutoImportAlias: accepting an unimported component's tag inserts
// its shortest tsconfig.json alias spelling when an alias covers it, as
// TypeScript's auto-import does, and a relative path otherwise.
func TestTagAutoImportAlias(t *testing.T) {
	const page = "src/pages/index.orbit"
	for _, tc := range []struct {
		name, tsconfig, want string
	}{
		{"alias", `{"compilerOptions": {"paths": {"@/*": ["./src/*"], "@ui/*": ["./src/components/*"]}}}`, `import Card "@ui/Card.orbit"`},
		{"none", `{}`, `import Card "../components/Card.orbit"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "---\n---\n<Ca\n"
			ws := memWorkspace(t, map[string]string{
				"tsconfig.json":             tc.tsconfig,
				"src/components/Card.orbit": "<p>card</p>\n",
				page:                        src,
			})
			f := ws.byPath[page]
			off := strings.Index(src, "<Ca") + 3
			c := cursor{ws: ws, f: f, lines: ws.lines[page], path: page, off: off, reg: classify(f, off)}
			var detail, edit string
			for _, it := range ws.tagItems(c) {
				m := it.(map[string]any)
				if m["label"] == "Card" {
					detail, _ = m["detail"].(string)
					if e, ok := m["additionalTextEdits"].([]jsonRaw); ok && len(e) == 1 {
						edit = string(e[0])
					}
				}
			}
			if detail != tc.want || !strings.Contains(edit, strings.ReplaceAll(tc.want, `"`, `\"`)) {
				t.Errorf("Card: detail %q, edit %s; want %s", detail, edit, tc.want)
			}
		})
	}
}
