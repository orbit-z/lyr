package assets

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/tailwind"
	"github.com/orbit-z/lyr/internal/tailwind/tailwindtest"
)

// twFiles is §5d's example: a Tailwind stylesheet imported from the
// layout, which imports a Fontsource package; a component with utilities
// and a scoped @apply through @reference.
var twFiles = map[string]string{
	".gitignore": "lyr_gen/\n.lyr/\ndist/\nnode_modules/\n",
	"src/styles/global.css": "@import \"tailwindcss\";\n@import \"@fontsource-variable/figtree\";\n\n" +
		"@theme {\n  --color-ember: #d1703f;\n  --font-ui: \"Figtree Variable\", system-ui, sans-serif;\n}\n\n" +
		"@utility card-row {\n  @apply flex items-baseline gap-2;\n}\n\n" +
		"@custom-variant hocus (&:hover, &:focus);\n",
	"node_modules/@fontsource-variable/figtree/package.json":                          `{"name":"@fontsource-variable/figtree","version":"5.2.10","main":"index.css","exports":{".":{"sass":"./index.scss","default":"./index.css"},"./*":"./*"}}`,
	"node_modules/@fontsource-variable/figtree/index.css":                             "/* figtree */\n@font-face {\n  font-family: 'Figtree Variable';\n  font-display: swap;\n  src: url(./files/figtree-latin-wght-normal.woff2) format('woff2-variations');\n}\n",
	"node_modules/@fontsource-variable/figtree/files/figtree-latin-wght-normal.woff2": "wOF2fake",
	"src/layouts/Base.lyr": "---\ntype Props struct{ Title string }\n---\n<html><head><title>{props.Title}</title></head>\n<body class=\"bg-zinc-900 font-ui\"><slot /></body></html>\n\n" +
		"<style is:global>\n  @import \"../styles/global.css\";\n</style>\n",
	"src/components/Card.lyr": "---\ntype Props struct {\n\tTitle  string\n\tActive bool\n}\nsize := \"p-8\"\n---\n" +
		"<article class={\"card-row rounded-lg \" + size + map[bool]string{true: \" ring-2 ring-ember\"}[props.Active]}>\n  <h2 class=\"title hocus:underline\">{props.Title}</h2>\n</article>\n\n" +
		"<style>\n  @reference \"../styles/global.css\";\n  .title { @apply text-lg font-bold hover:text-ember; }\n</style>\n",
	"src/pages/index.lyr": "<p class=\"italic\">home</p>\n",
	"internal/ui/c.go":    "package ui\n\nconst Cls = \"tracking-wide\"\n",
	"lyr_gen/x.go":        "package x\n\nconst y = \"line-through\"\n",
}

func twInput(t *testing.T, dir string) *Input {
	return input(t, dir, map[string][]string{
		"src/pages/index.lyr": {"src/layouts/Base.lyr", "src/components/Card.lyr", "src/pages/index.lyr"},
	})
}

func pageCSS(t *testing.T, res *Result, page string) string {
	t.Helper()
	for _, a := range res.Pages[page] {
		if a.Kind == "css" {
			return string(res.File(a.URL).Contents)
		}
	}
	t.Fatalf("%s has no stylesheet: %+v", page, res.Pages[page])
	return ""
}

// TestTailwindReal runs the real CLI over §5d's example.
func TestTailwindReal(t *testing.T) {
	tailwindtest.Real(t)
	dir := project(t, twFiles)
	res := bundle(t, Options{Root: dir}, twInput(t, dir))
	noErrors(t, res)
	css := pageCSS(t, res, "src/pages/index.lyr")
	scope := regexp.QuoteMeta("[data-l-")
	for _, want := range []string{
		`\.card-row\{`, `\.rounded-lg\{`, `\.p-8\{`, `\.ring-2\{`, `\.ring-ember\{`, `\.bg-zinc-900\{`, `\.font-ui\{`,
		`\.italic\{`, `\.tracking-wide\{`, `\.hocus\\:underline:(hover|focus)`,
		`\.title` + scope + `[^\]]*\]\{[^}]*font-size:var\(--text-lg`, // scoped @apply
		`\.title` + scope + `[^\]]*\]:hover\{color:var\(--color-ember`,
		`--color-ember: #d1703f`,
		`/\*! tailwindcss v4`,
		`url\("/_lyr/figtree-latin-wght-normal-[A-Z0-9]{8}\.woff2"\)`,
	} {
		if !regexp.MustCompile(want).MatchString(css) {
			t.Errorf("CSS has no %s:\n%s", want, css)
		}
	}
	for _, unwanted := range []string{".line-through", "@apply", "@theme", "@reference", "@import"} {
		if strings.Contains(css, unwanted) {
			t.Errorf("CSS has %s", unwanted)
		}
	}
	font := false
	for _, f := range res.Files {
		if strings.HasPrefix(f.Name, "figtree-latin-wght-normal-") && string(f.Contents) == "wOF2fake" {
			font = true
		}
	}
	if !font {
		t.Errorf("the font isn't an output file: %v", res.Files)
	}

	// Deterministic, and the same in a copy of the project elsewhere.
	dir2 := project(t, twFiles)
	res2 := bundle(t, Options{Root: dir2}, twInput(t, dir2))
	noErrors(t, res2)
	if css2 := pageCSS(t, res2, "src/pages/index.lyr"); css2 != css {
		t.Errorf("not deterministic:\n%s\n---\n%s", css, css2)
	}
}

// TestTailwindRealErrors: an unknown @apply class is reported at the
// @apply, in the .lyr file.
func TestTailwindRealErrors(t *testing.T) {
	tailwindtest.Real(t)
	files := map[string]string{}
	for k, v := range twFiles {
		files[k] = v
	}
	files["src/components/Card.lyr"] = strings.Replace(twFiles["src/components/Card.lyr"], "font-bold", "font-boldest", 1)
	dir := project(t, files)
	res := bundle(t, Options{Root: dir}, twInput(t, dir))
	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics: %+v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.File != "src/components/Card.lyr" || d.Line != 14 || d.Col != 12 || !strings.HasPrefix(d.Message, "Cannot apply unknown utility class `font-boldest`") {
		t.Errorf("diagnostic: %+v", d)
	}
}

// TestTailwindFake checks the plumbing with the fake CLI: the arguments,
// working directory and input of each kind, the source(…) of blocks, the
// order in the stylesheet, and the cache.
func TestTailwindFake(t *testing.T) {
	log := tailwindtest.Fake(t, "")
	files := map[string]string{
		"src/styles/global.css": "@import \"tailwindcss\";\n@import \"./fonts.css\";\n.g { color: red }\n",
		"src/styles/fonts.css":  "@font-face { font-family: F; src: url(./f/x.woff2); }\n",
		"src/styles/f/x.woff2":  "font",
		"src/styles/plain.css":  ".plain { color: blue }\n",
		"src/layouts/Base.lyr": "<html><head></head><body><slot /></body></html>\n" +
			"<style is:global>\n  @import \"../styles/global.css\";\n  @import \"../styles/plain.css\";\n</style>\n",
		"src/components/Card.lyr": "<h2 class=\"title\">x</h2>\n<style>\n  @reference \"../styles/global.css\";\n  .title { @apply font-bold; }\n</style>\n" +
			"<style>\n  .other { color: green }\n</style>\n",
		"src/pages/index.lyr": "<p>home</p>\n",
	}
	dir := project(t, files)
	in := twInput(t, dir)
	tw := tailwind.NewProcessor(dir)
	b := New(Options{Root: dir, Tailwind: tw})
	defer b.Close()
	res, err := b.Bundle(in)
	if err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	css := pageCSS(t, res, "src/pages/index.lyr")
	for _, want := range []string{".tw-base", ".g", ".plain", "--applied: font-bold", ".other", `url("/_lyr/x-`} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS has no %s:\n%s", want, css)
		}
	}
	// Import-graph order: the layout's stylesheet, then the card's blocks.
	if i, j, k := strings.Index(css, ".tw-base"), strings.Index(css, ".plain"), strings.Index(css, "--applied"); !(i < j && j < k) {
		t.Errorf("order: %d %d %d\n%s", i, j, k, css)
	}
	// Scoped first, then Tailwind.
	if !regexp.MustCompile(`\.title\[data-l-[^\]]+\]`).MatchString(css) {
		t.Errorf("block not scoped:\n%s", css)
	}
	lines := tailwindtest.Log(t, log)
	if len(lines) != 2 {
		t.Fatalf("runs: %q", lines)
	}
	wantRuns := map[string]bool{
		"cwd=" + filepath.Join(dir, "src/styles") + " args=--optimize -i - -o -":     true,
		"cwd=" + filepath.Join(dir, "src/components") + " args=--optimize -i - -o -": true,
	}
	for _, l := range lines {
		if !wantRuns[l] {
			t.Errorf("run %q", l)
		}
	}

	// A rebuild with nothing changed runs nothing; the cache is kept.
	res, err = b.Bundle(in)
	if err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if n := len(tailwindtest.Log(t, log)); n != 2 {
		t.Errorf("rebuild ran Tailwind: %d runs", n)
	}
	// Sources changed: only the root reruns.
	tw.Invalidate(false, true)
	if res, err = b.Bundle(in); err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if lines := tailwindtest.Log(t, log); len(lines) != 3 || !strings.Contains(lines[2], "src/styles") {
		t.Errorf("runs after a source change: %q", lines)
	}
	// CSS changed: every input reruns.
	tw.Invalidate(true, false)
	if res, err = b.Bundle(in); err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if n := len(tailwindtest.Log(t, log)); n != 5 {
		t.Errorf("runs after a CSS change: %d, want 5", n)
	}
}

// TestTailwindFakeErrors: Tailwind's messages cleaned up and placed.
func TestTailwindFakeErrors(t *testing.T) {
	tailwindtest.Fake(t, "")
	tests := []struct {
		name, global, card string
		want               Diagnostic
	}{
		{"unknown class at its @apply", "@import \"tailwindcss\";\n", "<p>x</p>\n<style>\n  .a { color: red }\n  /* FAIL_TW */ .b { @apply nope; }\n</style>\n",
			Diagnostic{File: "src/components/Card.lyr", Line: 4, Col: 22, Message: "Cannot apply unknown utility class `nope`."}},
		{"Tailwind's own position", "@import \"tailwindcss\";\n/* FAIL_TW_POS */\n.a { color: red\n", "<p>x</p>\n",
			Diagnostic{File: "src/styles/global.css", Line: 2, Col: 3, Message: "Missing closing } at .a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := project(t, map[string]string{
				"src/styles/global.css":   tt.global,
				"src/layouts/Base.lyr":    "<html><head></head><body><slot /></body></html>\n<style is:global>\n  @import \"../styles/global.css\";\n</style>\n",
				"src/components/Card.lyr": tt.card,
				"src/pages/index.lyr":     "<p>home</p>\n",
			})
			res := bundle(t, Options{Root: dir}, twInput(t, dir))
			if len(res.Diagnostics) != 1 || res.Diagnostics[0] != tt.want {
				t.Errorf("diagnostics: %+v, want %+v", res.Diagnostics, tt.want)
			}
		})
	}
}

// TestTailwindBlockOnce: a Tailwind <style> block on three pages has a
// copy per page (code splitting, PLAN.md §5e) but is inlined and processed
// once per build, and its error is reported once.
func TestTailwindBlockOnce(t *testing.T) {
	log := tailwindtest.Fake(t, "")
	for _, tt := range []struct {
		name, card string
		errs       int
	}{
		{"ok", "<h2 class=\"title\">x</h2>\n<style>\n  @reference \"../styles/global.css\";\n  .title { @apply font-bold; }\n</style>\n", 0},
		{"error", "<p>x</p>\n<style>\n  @reference \"../styles/global.css\";\n  /* FAIL_TW */ .b { @apply nope; }\n</style>\n", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := project(t, map[string]string{
				"src/styles/global.css":   "@import \"tailwindcss\";\n",
				"src/components/Card.lyr": tt.card,
				"src/pages/a.lyr":         "<p>a</p>\n<script>console.log(1)</script>\n",
				"src/pages/b.lyr":         "<p>b</p>\n<script>console.log(2)</script>\n",
				"src/pages/c.lyr":         "<p>c</p>\n",
			})
			pages := map[string][]string{}
			for _, p := range []string{"a", "b", "c"} {
				pages["src/pages/"+p+".lyr"] = []string{"src/components/Card.lyr", "src/pages/" + p + ".lyr"}
			}
			before := len(tailwindtest.Log(t, log))
			b := New(Options{Root: dir}).(*esbuildBundler)
			defer b.Close()
			res, err := b.Bundle(input(t, dir, pages))
			if err != nil {
				t.Fatal(err)
			}
			if n := b.twLoads.Load(); n != 1 {
				t.Errorf("block loaded %d times, want 1", n)
			}
			if n := len(tailwindtest.Log(t, log)) - before; n != 1 {
				t.Errorf("%d Tailwind runs, want 1", n)
			}
			if len(res.Diagnostics) != tt.errs {
				t.Fatalf("diagnostics: %+v", res.Diagnostics)
			}
			if tt.errs > 0 {
				if d := res.Diagnostics[0]; d.File != "src/components/Card.lyr" || d.Line != 4 {
					t.Errorf("diagnostic: %+v", d)
				}
				return
			}
			for page := range pages {
				if css := pageCSS(t, res, page); !strings.Contains(css, "--applied: font-bold") {
					t.Errorf("%s CSS:\n%s", page, css)
				}
			}
		})
	}
}

// TestTailwindNotFound: without a CLI, an error at the marker, only for
// projects with a Tailwind input.
func TestTailwindNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := project(t, basicFiles)
	noErrors(t, bundle(t, Options{Root: dir}, basicInput(t, dir)))

	dir = project(t, map[string]string{
		"src/styles/global.css": "/* site */\n@import \"tailwindcss\";\n",
		"src/layouts/Base.lyr":  "<html><head></head><body><slot /></body></html>\n<style is:global>\n  @import \"../styles/global.css\";\n</style>\n",
		"src/pages/index.lyr":   "<p>home</p>\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{"src/pages/index.lyr": {"src/layouts/Base.lyr", "src/pages/index.lyr"}}))
	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics: %+v", res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.File != "src/styles/global.css" || d.Line != 2 || d.Col != 1 ||
		!strings.HasPrefix(d.Message, `@import "tailwindcss" needs the Tailwind CSS CLI v4, which wasn't found; install it: `) || !strings.Contains(d.Message, "bun add -d @tailwindcss/cli@"+tailwind.Pinned) {
		t.Errorf("diagnostic: %+v", d)
	}
}
