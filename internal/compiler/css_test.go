package compiler

import (
	"strings"
	"testing"
)

const testScope = "data-l-12345678"

func TestScopeCSS(t *testing.T) {
	const w = "[" + testScope + "]"
	tests := []struct {
		name, in, want string
	}{
		{"type", "button { font-weight: bold; }", "button" + w + " { font-weight: bold; }"},
		{"class", ".a{color:red}", ".a" + w + "{color:red}"},
		{"compound", "a.b#c[d]:hover{}", "a.b#c[d]" + w + ":hover{}"},
		{"universal", "* { margin: 0 }", "*" + w + " { margin: 0 }"},
		{"attribute", "[data-x=\"a b\"] {}", "[data-x=\"a b\"]" + w + " {}"},
		{"attribute spaces", "[ data-x = y ] {}", "[ data-x = y ]" + w + " {}"},
		{"descendant", ".theme-dark .card {}", ".theme-dark .card" + w + " {}"},
		{"combinators", "a > b + c ~ d {}", "a > b + c ~ d" + w + " {}"},
		{"tight combinators", "a>b+c~d{}", "a>b+c~d" + w + "{}"},
		{"column", "col || td {}", "col || td" + w + " {}"},
		{"list", "h1, h2,\n  .x > p {}", "h1" + w + ", h2" + w + ",\n  .x > p" + w + " {}"},
		{"is args untouched", ":is(.a, .b) p:not(.c, .d) {}", ":is(.a, .b) p" + w + ":not(.c, .d) {}"},
		{"where args", "a:where(.x){}", "a" + w + ":where(.x){}"},
		// The scope goes before the first pseudo-class, as in Astro.
		{"hover", "a:hover{}", "a" + w + ":hover{}"},
		{"only pseudo-classes", ":is(.a, .b):hover {}", w + ":is(.a, .b):hover {}"},
		{"pseudo-class then element", "button:focus::before{}", "button" + w + ":focus::before{}"},
		{"pseudo chain", ".a>:not(:first-child)::after{}", ".a>" + w + ":not(:first-child)::after{}"},
		{"nth", "li:nth-child(2n + 1) {}", "li" + w + ":nth-child(2n + 1) {}"},
		{"pseudo-element", "p::first-line {}", "p" + w + "::first-line {}"},
		{"pseudo-element after class", "p.x:hover::before:hover {}", "p.x" + w + ":hover::before:hover {}"},
		{"legacy pseudo-element", "a:before, a:AFTER, p:first-letter {}", "a" + w + ":before, a" + w + ":AFTER, p" + w + ":first-letter {}"},
		{"bare pseudo-element", "::selection {}", w + "::selection {}"},
		{"pseudo-element function", "x::part(foo) {}", "x" + w + "::part(foo) {}"},
		{"global last", ".prose :global(p) { line-height: 1.6 }", ".prose" + w + " p { line-height: 1.6 }"},
		{"global compound", ".prose :global(a:hover) {}", ".prose" + w + " a:hover {}"},
		{"global first", ":global(.dark) .card {}", ".dark .card" + w + " {}"},
		{"global only", ":global(body) {}", "body {}"},
		{"global complex", ":global(.a .b > c) {}", ".a .b > c {}"},
		{"global middle", ".a :global(.b) :global(.c) {}", ".a" + w + " .b .c {}"},
		{"global mixed compound", ".a :global(.b).c {}", ".a" + w + " .b.c {}"},
		{"global with pseudo-element", ".a :global(p)::before {}", ".a" + w + " p::before {}"},
		{"global nested", ":is(:global(.a)) b {}", ":is(.a) b" + w + " {}"},
		{"global case", ".a :GLOBAL(p) {}", ".a" + w + " p {}"},
		{"global in list", ":global(a), b {}", "a, b" + w + " {}"},
		{"html", "html { color: red }", "html { color: red }"},
		{"html class", "html.dark .x {}", "html.dark .x" + w + " {}"},
		{"body", "body { margin: 0 }", "body { margin: 0 }"},
		{"body descendant", "body .x {}", "body .x" + w + " {}"},
		{"body class", "BODY.dark > main {}", "BODY.dark > main" + w + " {}"},
		{"body-ish", "bodyx {}", "bodyx" + w + " {}"},
		{"root", ":root { --c: red; }", ":root { --c: red; }"},
		{"root then", ":root .x {}", ":root .x" + w + " {}"},
		{"html-ish", "htmlx {}", "htmlx" + w + " {}"},
		{"media", "@media (min-width: 40rem) { .a { x: y } .b, .c {} }",
			"@media (min-width: 40rem) { .a" + w + " { x: y } .b" + w + ", .c" + w + " {} }"},
		{"nested at-rules", "@supports (display: grid) { @media print { a {} } }",
			"@supports (display: grid) { @media print { a" + w + " {} } }"},
		{"layer", "@layer base { a {} } @layer x, y;", "@layer base { a" + w + " {} } @layer x, y;"},
		{"container", "@container card (min-width: 1px) { a {} }", "@container card (min-width: 1px) { a" + w + " {} }"},
		{"scope", "@scope (.card) to (.content) { img {} }", "@scope (.card) to (.content) { img" + w + " {} }"},
		{"starting-style", "@starting-style { a { opacity: 0 } }", "@starting-style { a" + w + " { opacity: 0 } }"},
		{"vendor group", "@-moz-document url-prefix() { a {} }", "@-moz-document url-prefix() { a" + w + " {} }"},
		{"keyframes", "@keyframes spin { from { a: b } 50% { c: d } to {} } a {}",
			"@keyframes spin { from { a: b } 50% { c: d } to {} } a" + w + " {}"},
		{"vendor keyframes", "@-webkit-keyframes s { from {} }", "@-webkit-keyframes s { from {} }"},
		{"font-face", "@font-face { font-family: x; src: url(a.woff2) }", "@font-face { font-family: x; src: url(a.woff2) }"},
		{"page", "@page :first { margin: 1in; @top-left { content: 'x' } }", "@page :first { margin: 1in; @top-left { content: 'x' } }"},
		{"property", "@property --x { syntax: '<length>'; inherits: false }", "@property --x { syntax: '<length>'; inherits: false }"},
		{"unknown at-rule", "@foo bar { a {} }", "@foo bar { a {} }"},
		{"statements", "@charset \"utf-8\";\n@import url(x.css) screen;\na {}", "@charset \"utf-8\";\n@import url(x.css) screen;\na" + w + " {}"},
		{"nesting", ".a { color: red; .b { x: y } &:hover { } & .c {} > .d {} }",
			".a" + w + " { color: red; .b" + w + " { x: y } &:hover { } & .c" + w + " {} > .d" + w + " {} }"},
		{"nesting ident", ".a { p:hover { } }", ".a" + w + " { p" + w + ":hover { } }"},
		{"nesting trailing amp", ".a { .b & {} }", ".a" + w + " { .b & {} }"},
		{"nesting global", ".a { :global(.b) {} }", ".a" + w + " { .b {} }"},
		{"nesting media", ".a { @media (x) { color: red; .b {} } }", ".a" + w + " { @media (x) { color: red; .b" + w + " {} } }"},
		{"custom property braces", ".a { --x: { b: c }; .d {} }", ".a" + w + " { --x: { b: c }; .d" + w + " {} }"},
		{"comments kept", "/* c */ a /* d */ b/**/ { /* e */ }", "/* c */ a /* d */ b" + w + "/**/ { /* e */ }"},
		{"comment in compound", "a/**/.b {}", "a/**/.b" + w + " {}"},
		{"strings", "a[title=\"}{,\"]::after { content: \"{\" }", "a[title=\"}{,\"]" + w + "::after { content: \"{\" }"},
		{"escape", ".a\\:b {}", ".a\\:b" + w + " {}"},
		{"namespace", "svg|circle {}", "svg|circle" + w + " {}"},
		{"cdo", "<!-- a {} -->", "<!-- a" + w + " {} -->"},
		{"empty", "", ""},
		{"blank", "  \n ", "  \n "},
		{"empty rule", "a{}", "a" + w + "{}"},
		{"semicolons in decls", "a{;;color:red;;}", "a" + w + "{;;color:red;;}"},
		{"uppercase media", "@MEDIA screen { a {} }", "@MEDIA screen { a" + w + " {} }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := ScopeCSS(tt.in, testScope)
			if len(diags) > 0 {
				t.Errorf("unexpected diagnostics: %v", diags)
			}
			if got != tt.want {
				t.Errorf("ScopeCSS(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestScopeCSSErrors(t *testing.T) {
	tests := []struct {
		name, in string
		msg      string // substring of the first diagnostic
		at       string // the first diagnostic's span starts at the first occurrence of at
	}{
		{"stray brace", "a {} }", "unexpected '}'", "}\x00"},
		{"unclosed block", "a { color: red", "unclosed '{'", "{"},
		{"unclosed nested", ".a { .b { }", "unclosed '{'", "{"},
		{"unclosed media", "@media x { a {} ", "unclosed '{'", "{"},
		{"unclosed keyframes", "@keyframes x { from {} ", "unclosed '{'", "{"},
		{"unclosed paren", "a:is(.b {}", "unclosed '('", "is("},
		{"unclosed bracket", "a[x {}", "unclosed '['", "["},
		{"stray paren", "a) {}", "unexpected ')'", ")"},
		{"no block", "a, b;", "expected '{' after selector", "a"},
		{"no block eof", "a", "expected '{' after selector", "a"},
		{"missing selector", " {}", "missing selector", "{"},
		{"empty in list", "a,,b {}", "empty selector in list", ",b"},
		{"trailing comma", "a, {}", "empty selector in list", " {"},
		{"leading combinator", "> a {}", "can't start with '>'", ">"},
		{"trailing combinator", "a > {}", "can't end with '>'", ">"},
		{"double combinator", "a > + b {}", "unexpected combinator '+'", "+"},
		{"only combinator", ".a { > {} }", "missing selector after '>'", ">"},
		{"empty global", ".a :global() {}", "empty :global()", ":global"},
		{"bare global", ".a :global p {}", ":global needs a selector", ":global"},
		{"unclosed comment", "a {} /* x", "unclosed comment", "/*"},
		{"unclosed string", "a { content: \"x\n}", "unclosed string", "\"x"},
		{"bad url", "a { b: url(x y) }", "invalid url()", "url("},
		{"top semicolon", "a {};", "unexpected ';'", ";"},
		{"declaration at top", "color: red;", "expected '{' after selector", "color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := ScopeCSS(tt.in, testScope)
			if len(diags) == 0 {
				t.Fatalf("ScopeCSS(%q): no diagnostics", tt.in)
			}
			d := diags[0]
			if !strings.Contains(d.Message, tt.msg) {
				t.Errorf("message %q, want %q (all: %v)", d.Message, tt.msg, diags)
			}
			at := strings.Index(tt.in+"\x00", tt.at)
			if d.Start != at || d.End < d.Start || d.End > len(tt.in) {
				t.Errorf("span %d-%d, want start %d", d.Start, d.End, at)
			}
		})
	}
}

// TestScopeCSSAttr checks the scope is the given attribute.
func TestScopeCSSAttr(t *testing.T) {
	attr := ScopeAttr("src/components/Card.lyr")
	got, _ := ScopeCSS("a{}", attr)
	if want := "a[" + attr + "]{}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func FuzzScopeCSS(f *testing.F) {
	for _, s := range []string{
		"a{}", ".prose :global(p) { x: y }", "@media (x) { a, b > c::before {} }",
		".a { &:hover {} .b & {} > c {} --x: {}; }", "@keyframes k { from {} }",
		":global(", "a:is(", "/*", "\"", "url(", "@font-face{", "}}{{", "a,,{}",
		"[x=\"}\"]{}", "::", ":global()", "<!-- a{} -->", "a\\", "@page :first { @top-left {} }",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, diags := ScopeCSS(in, testScope)
		for _, d := range diags {
			if d.Start < 0 || d.End < d.Start || d.End > len(in) {
				t.Fatalf("diagnostic span %d-%d out of range (len %d): %s", d.Start, d.End, len(in), d.Message)
			}
		}
		// Removing the inserted scopes and restoring the :global( wrappers
		// can't be checked in general; check the output only adds scopes
		// and removes text, never invents other text.
		stripped := strings.ReplaceAll(out, "["+testScope+"]", "")
		if len(stripped) > len(in) {
			t.Fatalf("output grew beyond the scopes:\n in %q\nout %q", in, out)
		}
		if !strings.Contains(strings.ToLower(in), "global") && !strings.Contains(in, testScope) && stripped != in {
			t.Fatalf("output differs beyond the scopes:\n in %q\nout %q", in, out)
		}
	})
}

// benchCSS is a ~5 KB component stylesheet.
func benchCSS() string {
	var b strings.Builder
	for i := 0; b.Len() < 5000; i++ {
		b.WriteString(`/* card */
.card { display: grid; gap: 1rem; padding: 1rem 1.5rem; border: 1px solid #ddd; }
.card > h2, .card .title:hover { font: 600 1.25rem/1.2 system-ui, sans-serif; }
.prose :global(p) { margin: 0 0 1em; }
a[href^="http"]::after { content: "↗"; }
@media (min-width: 40rem) { .grid { grid-template-columns: repeat(3, 1fr); } li:nth-child(2n + 1) { color: red; } }
.btn { color: var(--c); &:hover { color: blue; } .icon { width: 1em; } }
@keyframes fade { from { opacity: 0 } to { opacity: 1 } }
`)
	}
	return b.String()
}

func BenchmarkScopeCSS(b *testing.B) {
	src := benchCSS()
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		ScopeCSS(src, testScope)
	}
}
