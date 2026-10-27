package tsconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolve(t *testing.T) {
	type want struct {
		spec, resolved string
		ok             bool
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []want
	}{
		{"none", map[string]string{}, []want{{"@/x.orbit", "", false}}},
		{"no paths", map[string]string{"tsconfig.json": `{"compilerOptions": {"strict": true}}`}, []want{{"@/x.orbit", "", false}}},
		{"broken file", map[string]string{"tsconfig.json": `{"compilerOptions": `}, []want{{"@/x.orbit", "", false}}},
		{"no baseUrl: relative to the tsconfig", map[string]string{"tsconfig.json": `{"compilerOptions": {"paths": {"@/*": ["./src/*"]}}}`},
			[]want{{"@/components/Card.orbit", "src/components/Card.orbit", true}, {"@x/Card.orbit", "", false}, {"~/Card.orbit", "", false}}},
		{"baseUrl", map[string]string{"tsconfig.json": `{"compilerOptions": {"baseUrl": "src", "paths": {"@c/*": ["components/*"]}}}`},
			[]want{{"@c/Card.orbit", "src/components/Card.orbit", true},
				// No entry matches: baseUrl-relative, as TypeScript (and Astro) resolve bare paths.
				{"layouts/Base.orbit", "src/layouts/Base.orbit", true}}},
		{"JSONC", map[string]string{"tsconfig.json": "{\n  // comment\n  \"compilerOptions\": {\n    /* block */ \"paths\": {\"@/*\": [\"src/*\",],},\n  },\n}\n"},
			[]want{{"@/a.orbit", "src/a.orbit", true}}},
		{"longest prefix and exact keys", map[string]string{"tsconfig.json": `{"compilerOptions": {"paths": {
			"@/*": ["src/*"], "@/ui/*": ["src/components/ui/*"], "@/ui/Button.orbit": ["src/special/Button.orbit"], "*.orbit": ["src/any/*.orbit"]}}}`},
			[]want{{"@/x.orbit", "src/x.orbit", true}, {"@/ui/Card.orbit", "src/components/ui/Card.orbit", true},
				{"@/ui/Button.orbit", "src/special/Button.orbit", true}, {"Card.orbit", "src/any/Card.orbit", true}}},
		{"several targets: the first existing", map[string]string{
			"tsconfig.json":             `{"compilerOptions": {"paths": {"@/*": ["src/a/*", "src/b/*"]}}}`,
			"src/b/Only.orbit":          "",
			"src/a/Both.orbit":          "",
			"src/b/Both.orbit":          "",
			"src/components/Card.orbit": ""},
			[]want{{"@/Only.orbit", "src/b/Only.orbit", true}, {"@/Both.orbit", "src/a/Both.orbit", true}, {"@/None.orbit", "src/a/None.orbit", true}}},
		{"extends: local file, paths relative to it", map[string]string{
			"tsconfig.json":          `{"extends": "./config/base", "compilerOptions": {"strict": true}}`,
			"config/base.json":       `{"compilerOptions": {"paths": {"@/*": ["../src/*"]}}}`,
			"src/components/X.orbit": ""},
			[]want{{"@/components/X.orbit", "src/components/X.orbit", true}}},
		{"extends: own paths override, base baseUrl", map[string]string{
			"tsconfig.json": `{"extends": ["./a.json", "./b.json"], "compilerOptions": {"paths": {"~/*": ["src/*"]}}}`,
			"a.json":        `{"compilerOptions": {"baseUrl": "."}}`,
			"b.json":        `{"compilerOptions": {"paths": {"@/*": ["src/*"]}}}`},
			[]want{{"~/x.orbit", "src/x.orbit", true}, {"@/x.orbit", "@/x.orbit", true}}},
		{"extends: a package under node_modules", map[string]string{
			"tsconfig.json":                          `{"extends": "astro/tsconfigs/base"}`,
			"node_modules/astro/tsconfigs/base.json": `{"compilerOptions": {"paths": {"@/*": ["../../../src/*"]}}}`},
			[]want{{"@/x.orbit", "src/x.orbit", true}}},
		{"extends: missing package ignored", map[string]string{
			"tsconfig.json": `{"extends": "nope/base", "compilerOptions": {"paths": {"@/*": ["src/*"]}}}`},
			[]want{{"@/x.orbit", "src/x.orbit", true}}},
		{"outside the project", map[string]string{"tsconfig.json": `{"compilerOptions": {"paths": {"@lib/*": ["../lib/*"]}}}`},
			[]want{{"@lib/x.orbit", "../lib/x.orbit", true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, tc.files)
			a, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			exists := func(rel string) bool {
				_, err := os.Stat(filepath.Join(dir, rel))
				return err == nil
			}
			for _, w := range tc.want {
				got, ok := a.Resolve(w.spec, exists)
				if got != w.resolved || ok != w.ok {
					t.Errorf("Resolve(%q) = %q, %v; want %q, %v", w.spec, got, ok, w.resolved, w.ok)
				}
			}
		})
	}
}

func TestFilesAndSpellings(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"tsconfig.json":      `{"extends": "./tsconfig.base.json", "compilerOptions": {"baseUrl": "."}}`,
		"tsconfig.base.json": `{"compilerOptions": {"paths": {"@/*": ["src/*"], "@c/*": ["src/components/*"]}}}`,
	})
	a, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Files, []string{"tsconfig.json", "tsconfig.base.json"}) {
		t.Errorf("Files %v", a.Files)
	}
	got := a.Spellings("src/components/Card.orbit", nil)
	if want := []string{"@c/Card.orbit", "@/components/Card.orbit", "src/components/Card.orbit"}; !slices.Equal(got, want) {
		t.Errorf("Spellings %v, want %v", got, want)
	}
	if got := a.Prefixes(); !slices.Equal(got, []string{"@c/", "@/"}) {
		t.Errorf("Prefixes %v", got)
	}
	if a.MultiTarget() || a.Empty() {
		t.Error("MultiTarget or Empty")
	}
}
