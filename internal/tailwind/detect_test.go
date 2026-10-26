package tailwind

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		name, src   string
		input, root bool
	}{
		{"import", `@import "tailwindcss";`, true, true},
		{"single quotes", `@import 'tailwindcss';`, true, true},
		{"url token", `@import url(tailwindcss);`, true, true},
		{"url string", `@import url("tailwindcss");`, true, true},
		{"subpath layer", `@import "tailwindcss/theme.css" layer(theme);`, true, true},
		{"source", `@import "tailwindcss" source("../src");`, true, true},
		{"prefix", `@import "tailwindcss" prefix(tw);`, true, true},
		{"tailwind directive", "@tailwind utilities;", true, true},
		{"upper case", `@IMPORT "tailwindcss";`, true, true},
		{"apply", ".a { @apply flex; }", true, false},
		{"reference", `@reference "../styles/global.css"; .a { @apply p-2 }`, true, false},
		{"theme", "@theme { --color-x: red; }", true, false},
		{"utility", "@utility tab-4 { tab-size: 4; }", true, false},
		{"variant", ".a { @variant dark { color: red } }", true, false},
		{"custom-variant", "@custom-variant dark (&:where(.dark, .dark *));", true, false},
		{"source rule", `@source "../node_modules/x";`, true, false},
		{"plugin", `@plugin "@tailwindcss/typography";`, true, false},
		{"config", `@config "./tailwind.config.js";`, true, false},
		{"plain", ".a { color: red } @media (min-width: 1px) { .b { color: blue } }", false, false},
		{"other import", `@import "./reset.css";`, false, false},
		{"tailwindcss-like package", `@import "tailwindcss-animate";`, false, false},
		{"in comment", `/* @import "tailwindcss"; @apply flex; */ .a{}`, false, false},
		{"in string", `.a::after { content: "@apply flex; @import 'tailwindcss'" }`, false, false},
		{"name in a value", `.tailwind { font-family: tailwind }`, false, false},
		{"layer", "@layer base { .a { color: red } }", false, false},
		{"empty", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.src)
			if got.Input != tt.input || got.Root != tt.root {
				t.Errorf("Detect(%q) = %+v, want input %v root %v", tt.src, got, tt.input, tt.root)
			}
		})
	}
}

func TestDetectPos(t *testing.T) {
	src := "/* x */\n.a { color: red }\n@theme { --c: red; }\n@import \"tailwindcss\";\n"
	got := Detect(src)
	if want := 26; got.Pos != want || !got.Root {
		t.Errorf("Detect = %+v, want Pos %d and Root", got, want)
	}
	if n := MarkerName(src, got.Pos); n != "@theme" {
		t.Errorf("MarkerName = %q", n)
	}
	if n := MarkerName(`@import 'tailwindcss' source(none);`, 0); n != `@import "tailwindcss"` {
		t.Errorf("MarkerName = %q", n)
	}
}

func BenchmarkDetectPlain(b *testing.B) {
	src := ""
	for range 200 {
		src += ".card { color: red; padding: 1rem } @media (min-width: 40rem) { .card { padding: 2rem } }\n"
	}
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		Detect(src)
	}
}
