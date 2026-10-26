package tailwind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFS is a project for Inline: files by absolute path, and a resolver
// for relative paths and node_modules packages (index.css).
type fakeFS map[string]string

func (fs fakeFS) opts(root string) InlineOptions {
	return InlineOptions{
		Root: root,
		Read: func(p string) (string, error) {
			s, ok := fs[p]
			if !ok {
				return "", os.ErrNotExist
			}
			return s, nil
		},
		Resolve: func(spec, dir string) (string, bool) {
			var p string
			if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
				p = filepath.Join(dir, spec)
			} else {
				for d := dir; ; d = filepath.Dir(d) {
					cand := filepath.Join(d, "node_modules", spec)
					if !strings.HasSuffix(cand, ".css") {
						cand = filepath.Join(cand, "index.css")
					}
					if _, ok := fs[cand]; ok {
						p = cand
						break
					}
					if d == filepath.Dir(d) {
						return "", false
					}
				}
			}
			_, ok := fs[p]
			return p, ok
		},
	}
}

func TestInline(t *testing.T) {
	const root = "/p"
	fs := fakeFS{
		"/p/src/styles/fonts/fonts.css": "@charset \"utf-8\";\n@font-face { font-family: X; src: url(./x.woff2) format(\"woff2\"), url('../img/y.woff?v=1#iefix'); }\n" +
			".bg { background: url(data:image/png;base64,AA), url(https://e.com/a.png), url(/public.png), url(#f), url(\"z.png\"); }\n",
		"/p/src/styles/theme.css": "@import \"./fonts/fonts.css\";\n@theme { --c: red; }\n@source \"../components\";\n@source not \"./legacy\";\n@plugin \"./plug.mjs\";\n@plugin \"daisyui\";\n@reference \"./ref.css\";\n",
		"/p/node_modules/@fontsource-variable/figtree/index.css": "@font-face { font-family: 'Figtree Variable'; src: url(./files/figtree-latin-wght-normal.woff2) format('woff2-variations'); }\n",
		"/p/src/styles/print.css":                               ".p { color: black }\n",
		"/p/src/styles/a.css":                                   "@import \"./b.css\";\n.a{}\n",
		"/p/src/styles/b.css":                                   "@import \"./a.css\";\n.b{}\n",
	}
	src := Source{
		File: "/p/src/styles/global.css", Line: 1, Col: 1, Dir: "/p/src/styles",
		Text: "@import \"tailwindcss\";\n" +
			"@import \"@fontsource-variable/figtree\";\n" +
			"@import \"./theme.css\" layer(components);\n" +
			"@import url(\"./print.css\") supports(display: grid) print and (min-width: 1px);\n" +
			"@import \"./print.css\" layer;\n" +
			"@import \"./a.css\";\n" +
			"@import \"./missing.css\";\n" +
			"@import \"https://fonts.googleapis.com/css?family=X\";\n" +
			"@import \"./theme.css\" theme(reference);\n" +
			".x { background: url(./here.png) }\n",
	}
	got := Inline(src, fs.opts(root))
	want := "@import \"tailwindcss\" source(\"../..\");\n" +
		"@font-face { font-family: 'Figtree Variable'; src: url(\"../../node_modules/@fontsource-variable/figtree/files/figtree-latin-wght-normal.woff2\") format('woff2-variations'); }\n\n" +
		"@layer components{\n" + // @charset dropped
		"@font-face { font-family: X; src: url(\"./fonts/x.woff2\") format(\"woff2\"), url(\"./img/y.woff?v=1#iefix\"); }\n" +
		".bg { background: url(data:image/png;base64,AA), url(https://e.com/a.png), url(/public.png), url(#f), url(\"./fonts/z.png\"); }\n\n" +
		"@theme { --c: red; }\n@source \"../components\";\n@source not \"./legacy\";\n@plugin \"./plug.mjs\";\n@plugin \"daisyui\";\n@reference \"./ref.css\";\n}\n" +
		"@media print and (min-width: 1px){@supports (display: grid){.p { color: black }\n}}\n" +
		"@layer{.p { color: black }\n}\n" +
		"\n.b{}\n\n.a{}\n\n" + // the cycle back to a.css dropped
		"@import \"./missing.css\";\n" +
		"@import \"https://fonts.googleapis.com/css?family=X\";\n" +
		"@import \"./theme.css\" theme(reference);\n" +
		".x { background: url(./here.png) }\n"
	if got.Text != want {
		t.Errorf("Inline:\n%s\nwant:\n%s", got.Text, want)
	}
	if len(got.Files) != 7 {
		t.Errorf("Files = %v", got.Files)
	}

	// Positions map back to the files.
	for _, tc := range []struct {
		find, file string
		line, col  int
	}{
		{"@import \"tailwindcss\"", "/p/src/styles/global.css", 1, 1},
		{"@theme", "/p/src/styles/theme.css", 2, 1},
		{".bg", "/p/src/styles/fonts/fonts.css", 3, 1},
		{".x {", "/p/src/styles/global.css", 10, 1},
		{"@import \"./missing.css\"", "/p/src/styles/global.css", 7, 1},
	} {
		off := strings.Index(got.Text, tc.find)
		file, line, col := got.Position(off)
		if file != tc.file || line != tc.line || col != tc.col {
			t.Errorf("Position(%q) = %s:%d:%d, want %s:%d:%d", tc.find, file, line, col, tc.file, tc.line, tc.col)
		}
	}
}

func TestInlineBlock(t *testing.T) {
	// A <style> block: positions are in the .orbit file, from the block's
	// start; @tailwind utilities gets its source(…).
	fs := fakeFS{"/p/src/styles/x.css": ".x { background: url(./x.png) }\n"}
	src := Source{File: "/p/src/components/Card.orbit", Line: 5, Col: 8, Dir: "/p/src/components",
		Text: "@import \"../styles/x.css\";\n  @tailwind utilities;\n  .t { @apply font-bold; }"}
	got := Inline(src, fs.opts("/p"))
	want := ".x { background: url(\"../styles/x.png\") }\n\n  @tailwind utilities source(\"../..\");\n  .t { @apply font-bold; }"
	if got.Text != want {
		t.Errorf("Inline:\n%q\nwant:\n%q", got.Text, want)
	}
	file, line, col := got.Position(strings.Index(got.Text, "@apply"))
	if file != src.File || line != 7 || col != 8 {
		t.Errorf("Position(@apply) = %s:%d:%d, want line 7 col 8", file, line, col)
	}
	file, line, col = got.Position(0)
	if file != "/p/src/styles/x.css" || line != 1 || col != 1 {
		t.Errorf("Position(0) = %s:%d:%d", file, line, col)
	}

	// No Root: no source(…); an import with source(…) keeps it.
	o := fs.opts("")
	if got := Inline(Source{Text: `@import "tailwindcss";`, Dir: "/p"}, o); got.Text != `@import "tailwindcss";` {
		t.Errorf("no root: %q", got.Text)
	}
	if got := Inline(Source{Text: `@import "tailwindcss" source(none);`, Dir: "/p/src"}, fs.opts("/p")); got.Text != `@import "tailwindcss" source(none);` {
		t.Errorf("source(none): %q", got.Text)
	}
	if got := Inline(Source{Text: `@import "tailwindcss/theme.css" layer(theme);`, Dir: "/p/src"}, fs.opts("/p")); got.Text != `@import "tailwindcss/theme.css" layer(theme);` {
		t.Errorf("theme: %q", got.Text)
	}
}

func TestLineCol(t *testing.T) {
	in := &Inlined{Text: "ab\ncd\nef"}
	for _, tc := range []struct{ line, col, off int }{{1, 1, 0}, {2, 2, 4}, {3, 1, 6}, {9, 1, 8}} {
		if got := in.LineCol(tc.line, tc.col); got != tc.off {
			t.Errorf("LineCol(%d, %d) = %d, want %d", tc.line, tc.col, got, tc.off)
		}
	}
}
