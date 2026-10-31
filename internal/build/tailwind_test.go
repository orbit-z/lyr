package build

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/tailwind/tailwindtest"
)

// tailwindSite is PLAN.md §5d's example as a project: a Tailwind
// stylesheet with @theme, @utility and @custom-variant, imported from the
// layout, that imports a Fontsource font; a component with utilities in
// attributes and Go strings and a scoped @apply through @reference.
var tailwindSite = map[string]string{
	".gitignore": "lyr_gen/\n.lyr/\ndist/\nnode_modules/\n",
	"src/styles/global.css": "@import \"tailwindcss\";\n@import \"@fontsource-variable/figtree\";\n\n" +
		"@theme {\n  --color-ember: #d1703f;\n  --font-ui: \"Figtree Variable\", system-ui, sans-serif;\n}\n\n" +
		"@utility card-row {\n  @apply flex items-baseline gap-2 border-b border-zinc-700 py-4;\n}\n\n" +
		"@custom-variant hocus (&:hover, &:focus-visible);\n",
	"node_modules/@fontsource-variable/figtree/package.json":                          `{"name":"@fontsource-variable/figtree","version":"5.2.10","main":"index.css","exports":{".":{"sass":"./index.scss","default":"./index.css"},"./*":"./*"}}`,
	"node_modules/@fontsource-variable/figtree/index.css":                             "/* figtree-latin-wght-normal */\n@font-face {\n  font-family: 'Figtree Variable';\n  font-style: normal;\n  font-display: swap;\n  font-weight: 300 900;\n  src: url(./files/figtree-latin-wght-normal.woff2) format('woff2-variations');\n}\n",
	"node_modules/@fontsource-variable/figtree/files/figtree-latin-wght-normal.woff2": "wOF2 test font",
	"src/layouts/Base.lyr": "---\ntype Props struct{ Title string }\n---\n<html lang=\"en\">\n  <head><title>{props.Title}</title></head>\n  <body class=\"bg-zinc-900 font-ui text-zinc-100\"><slot /></body>\n</html>\n\n" +
		"<style is:global>\n  @import \"../styles/global.css\";\n</style>\n",
	"src/components/Card.lyr": "---\ntype Props struct {\n\tTitle  string\n\tActive bool\n}\nsize := \"p-8\"\nif !props.Active {\n\tsize = \"p-2\"\n}\n---\n" +
		"<article class=\"card-row\" class:list={\"rounded-lg\", size, \"ring-2 ring-ember\": props.Active}>\n  <h2 class=\"title hocus:underline\">{props.Title}</h2>\n</article>\n\n" +
		"<style>\n  @reference \"../styles/global.css\";\n  .title { @apply text-lg font-bold hover:text-ember; }\n</style>\n",
	"src/pages/index.lyr": "---\nimport (\n\tBase \"../layouts/Base.lyr\"\n\tCard \"../components/Card.lyr\"\n)\n---\n<Base title=\"Home\">\n  <Card title=\"One\" active={true} />\n</Base>\n",
}

func tailwindProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/tw\n\ngo 1.27\n\ntoolchain go1.27.3\n\nrequire github.com/orbit-z/lyr v0.0.0\n\nreplace github.com/orbit-z/lyr => "+filepath.ToSlash(repoRoot)+"\n")
	sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.sum", string(sum))
	for rel, content := range tailwindSite {
		writeFile(t, dir, rel, content)
	}
	return dir
}

var stylesheetLink = regexp.MustCompile(`<link rel="stylesheet" href="(/_lyr/[^"]+\.css)">`)

// TestTailwindBuild builds §5d's example with lyr build and the real
// Tailwind CLI, and checks what the server serves: the utilities found in
// the templates and Go strings, the scoped @apply rules, and the font
// imported through the Tailwind stylesheet, hashed under /_lyr/.
func TestTailwindBuild(t *testing.T) {
	needE2E(t)
	tailwindtest.Real(t)
	dir := tailwindProject(t)
	if code, out := runLyr(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("lyr build: exit %d\n%s", code, out)
	}
	s := start(t, dir, filepath.Join(dir, "dist/server"))
	defer s.stop()
	_, page := s.get("/")
	m := stylesheetLink.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no stylesheet:\n%s", page)
	}
	resp, css := s.get(m[1])
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("GET %s: %s %s", m[1], resp.Status, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		`\.card-row\{`, `\.rounded-lg\{`, `\.p-8\{`, `\.p-2\{`, `\.ring-2\{`, `\.ring-ember\{`, `\.bg-zinc-900\{`, `\.font-ui\{`, `\.text-zinc-100\{`,
		`\.hocus\\:underline:(hover|focus-visible)`,
		`\.title\[data-l-[0-9a-f]+\]\{[^}]*font-weight:var\(--font-weight-bold`,
		`\.title\[data-l-[0-9a-f]+\]:hover\{color:var\(--color-ember`,
		`/\*! tailwindcss v4`,
	} {
		if !regexp.MustCompile(want).MatchString(css) {
			t.Errorf("CSS has no %s:\n%s", want, css)
		}
	}
	if strings.Contains(css, "@apply") || strings.Contains(css, "@theme") {
		t.Errorf("Tailwind's at-rules left in the CSS")
	}
	fm := regexp.MustCompile(`url\("?(/_lyr/figtree-latin-wght-normal-[A-Z0-9]{8}\.woff2)"?\)`).FindStringSubmatch(css)
	if fm == nil {
		t.Fatalf("the font URL isn't hashed under /_lyr/:\n%s", css)
	}
	if resp, body := s.get(fm[1]); resp.StatusCode != 200 || body != "wOF2 test font" {
		t.Errorf("GET %s: %s %q", fm[1], resp.Status, body)
	}

	// Deterministic: a second build gives the same files.
	before := readTree(t, filepath.Join(dir, "dist/client"))
	if code, out := runLyr(t, dir, "build"); code != 0 {
		t.Fatalf("second lyr build: exit %d\n%s", code, out)
	}
	if after := readTree(t, filepath.Join(dir, "dist/client")); !equalTrees(before, after) {
		t.Errorf("lyr build isn't deterministic with Tailwind")
	}

	// lyr check reports an unknown @apply class at its position.
	writeFile(t, dir, "src/components/Card.lyr", strings.Replace(tailwindSite["src/components/Card.lyr"], "font-bold", "font-boldest", 1))
	code, out := runLyr(t, dir, "check")
	if code == 0 || !strings.Contains(out, "src/components/Card.lyr:17:12: Cannot apply unknown utility class `font-boldest`") {
		t.Errorf("lyr check: exit %d\n%s", code, out)
	}
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
