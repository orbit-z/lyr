package project

import (
	"strings"
	"testing"
)

// TestAliasImports: .orbit and Markdown imports, and a Markdown layout,
// through tsconfig.json paths aliases (PLAN.md §2.2, §2.4).
func TestAliasImports(t *testing.T) {
	p := newTestProject(t, map[string]string{
		"tsconfig.json": "{\n  // JSONC, with a local base\n  \"extends\": \"./tsconfig.base.json\",\n}\n",
		"tsconfig.base.json": `{"compilerOptions": {"paths": {"@/*": ["src/*"], "@lib/*": ["lib/*"],
			"@ui/*": ["src/missing/*", "src/components/*"]}}}`,
		"src/components/Card.orbit": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
		"src/components/Tag.tsx":    "",
		"src/layouts/Post.orbit":    mdLayout,
		"src/content/intro.md":      "Intro.\n",
		"src/pages/index.orbit": "---\nimport (\n\tCard \"@/components/Card.orbit\"\n\tSame \"../components/Card.orbit\"\n\tUI \"@ui/Card.orbit\"\n" +
			"\tIntro \"@/content/intro.md\"\n)\n---\n<Card Title=\"a\" /><Same Title=\"b\" /><UI Title=\"c\" /><Intro />\n",
		"src/pages/post.md": "---\nlayout: \"@/layouts/Post.orbit\"\ntitle: Hi\n---\n# Hi\n",
		"src/pages/errors.orbit": "---\nimport (\n\tA \"~/components/Card.orbit\"\n\tB \"@lib/X.orbit\"\n\tC \"@/components/Tag.tsx\"\n" +
			"\tD \"@/components/Nope.orbit\"\n\tE \"Card.orbit\"\n\tF \"@/components/Card.orbit\"\n\t\"strings\"\n)\n---\n" +
			"<A /><B /><C /><D /><E />{strings.ToUpper(\"x\")}\n",
		"src/pages/bad.md": "---\nlayout: \"~/layouts/Post.orbit\"\n---\n# Bad\n",
	})
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	proj.Front()
	var got []string
	for _, d := range diagStrings(proj.Diagnostics) {
		if !strings.Contains(d, "unknown component") {
			got = append(got, d)
		}
	}
	want := []string{
		`src/pages/bad.md:2:9: layout: no tsconfig.json paths alias matches "~/layouts/Post.orbit"; use a relative path or add "~/*": ["./src/*"] to compilerOptions.paths`,
		`src/pages/errors.orbit:3:4: no tsconfig.json paths alias matches "~/components/Card.orbit"; use a relative path or add "~/*": ["./src/*"] to compilerOptions.paths`,
		`src/pages/errors.orbit:4:4: component path @lib/X.orbit is outside src/`,
		`src/pages/errors.orbit:5:4: framework components aren't supported; use a .orbit component with a <script>`,
		`src/pages/errors.orbit:6:4: no such component: src/components/Nope.orbit`,
		`src/pages/errors.orbit:7:4: no tsconfig.json paths alias matches "Card.orbit"; use a relative path, such as "./Card.orbit"`,
		`src/pages/errors.orbit:8:2: component F is imported but not used; remove the import`,
	}
	if joinLines(got) != joinLines(want) {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
	}
	idx := proj.byPath()["src/pages/index.orbit"]
	for i, wantPath := range []string{"src/components/Card.orbit", "src/components/Card.orbit", "src/components/Card.orbit", "src/content/intro.md"} {
		if im := idx.Analysis.OrbitImports[i]; im.Resolved != wantPath {
			t.Errorf("import %s resolved to %s, want %s", im.Path, im.Resolved, wantPath)
		}
	}
	if md := proj.byPath()["src/pages/post.md"]; md == nil || md.MD.Layout != "src/layouts/Post.orbit" {
		t.Errorf("post.md layout: %+v", md)
	}

	// A tsconfig change re-resolves: Reload loads the project again.
	p.write("tsconfig.base.json", `{"compilerOptions": {"paths": {"@/*": ["src/*"], "~/*": ["src/*"]}}}`)
	proj, _, err = Reload(proj, p.Dir, []string{"tsconfig.base.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diagStrings(proj.Diagnostics) {
		if strings.Contains(d, `"~/`) {
			t.Errorf("after the tsconfig edit: %s", d)
		}
	}
}

// TestAliasBuild generates and builds a project whose page imports the
// same component under a relative path and an alias.
func TestAliasBuild(t *testing.T) {
	needGo(t)
	p := newTestProject(t, map[string]string{
		"tsconfig.json":             `{"compilerOptions": {"baseUrl": ".", "paths": {"@/*": ["src/*"]}}}`,
		"src/components/Card.orbit": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
		"src/layouts/Post.orbit":    mdLayout,
		"src/pages/index.orbit": "---\nimport (\n\tCard \"@/components/Card.orbit\"\n\tSame \"../components/Card.orbit\"\n)\n---\n" +
			"<Card Title=\"a\" /><Same Title=\"b\" />\n",
		"src/pages/post.md": "---\nlayout: '@/layouts/Post.orbit'\ntitle: Hi\n---\n# Hi\n",
	})
	if res := p.generate(); HasErrors(res.Diagnostics) {
		t.Fatalf("diagnostics: %v", diagStrings(res.Diagnostics))
	}
	if out, err := p.goCmd("build", "./..."); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}
