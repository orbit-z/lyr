package markdown

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var update = flag.Bool("update", false, "rewrite golden files")

// orbitTokens tokenizes src with the orbit lexer, one "class value" line
// per token (whitespace-only text skipped).
func orbitTokens(t testing.TB, src string) (string, []chroma.Token) {
	t.Helper()
	it, err := lexerFor("orbit").Tokenise(nil, src)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var toks []chroma.Token
	var got strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		toks = append(toks, tok)
		got.WriteString(tok.Value)
		if strings.TrimSpace(tok.Value) == "" {
			continue
		}
		cls := tokenClass(tok.Type)
		if cls == "" {
			cls = "-"
		}
		fmt.Fprintf(&b, "%-4s %q\n", cls, tok.Value)
	}
	if got.String() != src {
		t.Errorf("tokens don't add up to the source:\n%s", got.String())
	}
	return b.String(), toks
}

func TestOrbitLexer(t *testing.T) {
	src, err := os.ReadFile("testdata/sample.orbit")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := orbitTokens(t, string(src))
	golden := "testdata/sample.orbit.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("tokens differ from %s (go test -update to refresh):\n%s", golden, got)
	}
	// The key parts, so a golden refresh can't hide a regression.
	for _, w := range []string{
		`p    "---"`,    // frontmatter fences
		`kn   "import"`, // Go in the frontmatter
		`s    "\"../components/Card.orbit\""`,
		`kd   "type"`,
		`c1   "// A comment with a } brace."`,
		`cp   "<!doctype html>"`,
		`c    "<!-- an HTML comment -->"`,
		`cm   "{/* a template comment */}"`,
		`nt   "h1"`,
		`na   "class"`,
		`s    "\"literal {x}\""`,         // braces in a quoted attribute are text
		`s    "\"}%d{\""`,                // a Go string with braces in an expression
		`sc   "'}'"`,                     // a rune
		"sb   \"`raw }`\"",               // a template literal without pieces
		"s    \"`a raw ${string}`\"",     // the frontmatter keeps Go's raw strings
		"sb   \"`/posts/\"", `si   "${"`, // a literal with pieces
		`si   "}"`,
		"sb   \"`x \"",   // a nested literal
		`se   "\\$"`,     // an escape
		`cm   "/* } */"`, // a Go comment
		`k    "if"`, `k    "else"`, `k    "for"`, `k    "/for"`, `k    "/if"`,
		`nc   "Card"`, `nc   "Self"`, // component tags
		`o    "..."`, // a spread
		`na   "client:visible"`,
		`kr   "const"`, // TypeScript
		`kt   "number"`,
		`nt   "script"`,
		`nt   "style"`,
		`nt   "h1"`,
		`-    "a < b"`,
	} {
		if !strings.Contains(got, w+"\n") {
			t.Errorf("no %s in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "err ") {
		t.Errorf("error tokens in:\n%s", got)
	}
}

func TestOrbitLexerEdges(t *testing.T) {
	for _, tt := range []struct{ src, want string }{
		// No frontmatter: all template.
		{"<p>{x}</p>\n", "p    \"<\"\nnt   \"p\"\np    \">{\"\nnx   \"x\"\np    \"}</\"\nnt   \"p\"\np    \">\"\n"},
		// An unterminated frontmatter is Go to the end.
		{"---\nx := 1\n", "p    \"---\"\nnx   \"x\"\no    \":=\"\nmi   \"1\"\n"},
		// "---" that isn't on the first line is text.
		{"\n---\n", "-    \"\\n---\\n\"\n"},
		// A { without its } is an error, and the rest is still template.
		{"<p>{x</p>\n", "p    \"<\"\nnt   \"p\"\np    \">\"\nerr  \"{\"\n-    \"x\"\np    \"</\"\nnt   \"p\"\np    \">\"\n"},
		// A // comment needs a newline before the }.
		{"{x // y}\n", "err  \"{\"\n-    \"x // y}\\n\"\n"},
		{"{x // y\n}", "p    \"{\"\nnx   \"x\"\nc1   \"// y\"\np    \"}\"\n"},
		// The fragment shorthand <>…</> is tag punctuation.
		{"<><b>x</b></>", "p    \"<><\"\nnt   \"b\"\np    \">\"\n-    \"x\"\np    \"</\"\nnt   \"b\"\np    \"></>\"\n"},
		// {switch} blocks: keywords, with the header and case lists as Go.
		{"{switch x}{case 1, 2}{default}{/switch}", "p    \"{\"\nk    \"switch\"\nnx   \"x\"\np    \"}{\"\nk    \"case\"\nmi   \"1\"\np    \",\"\nmi   \"2\"\np    \"}{\"\nk    \"default\"\np    \"}{\"\nk    \"/switch\"\np    \"}\"\n"},
	} {
		got, _ := orbitTokens(t, tt.src)
		if got != tt.want {
			t.Errorf("%q:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}

// TestOrbitLexerTemplateLiterals: in a template expression a backtick
// string is a template literal (PLAN.md §2.10), its pieces template
// expressions again, so nesting is exact.
func TestOrbitLexerTemplateLiterals(t *testing.T) {
	bt := func(s string) string { return strings.ReplaceAll(s, "'", "`") } // ' stands for a backtick
	for _, tt := range []struct{ src, want string }{
		// Three levels; braces in a literal's text don't count.
		{bt("{'a${f('b${'c}'}')}'}"), bt(`p    "{"
sb   "'a"
si   "${"
nf   "f"
p    "("
sb   "'b"
si   "${"
sb   "'c}'"
si   "}"
sb   "'"
p    ")"
si   "}"
sb   "'"
p    "}"
`)},
		// A backtick in a piece's Go string doesn't end the literal.
		{bt(`{'${"'"}'}`), bt(`p    "{"
sb   "'"
si   "${"
s    "\"'\""
si   "}"
sb   "'"
p    "}"
`)},
		// $ without { is text; \${ is an escape, not a piece.
		{bt(`{'$x \${y}'}`), bt(`p    "{"
sb   "'$x "
se   "\\$"
sb   "{y}'"
p    "}"
`)},
		// An unterminated literal makes the { an error.
		{bt("<p>{'a</p>\n"), bt(`p    "<"
nt   "p"
p    ">"
err  "{"
-    "'a"
p    "</"
nt   "p"
p    ">"
`)},
		// A control header, a multi-line literal as an attribute value.
		{bt("{if s == 'a-${b}'}{/if}<a x={'1\n${n}'}/>"), bt(`p    "{"
k    "if"
nx   "s"
o    "=="
sb   "'a-"
si   "${"
nx   "b"
si   "}"
sb   "'"
p    "}{"
k    "/if"
p    "}<"
nt   "a"
na   "x"
o    "="
p    "{"
sb   "'1\n"
si   "${"
nx   "n"
si   "}"
sb   "'"
p    "}/>"
`)},
	} {
		got, _ := orbitTokens(t, tt.src)
		if got != tt.want {
			t.Errorf("%q:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}

// orbitBlocks returns the ```orbit blocks of the Markdown files under dir.
func orbitBlocks(t testing.TB, dir string) (blocks []string, where []string) {
	t.Helper()
	md := goldmark.New()
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		root := md.Parser().Parse(text.NewReader(src))
		return ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			f, ok := n.(*ast.FencedCodeBlock)
			if !entering || !ok || f.Info == nil || string(f.Language(src)) != "orbit" {
				return ast.WalkContinue, nil
			}
			var b strings.Builder
			for i := range f.Lines().Len() {
				seg := f.Lines().At(i)
				b.Write(seg.Value(src))
			}
			blocks = append(blocks, b.String())
			line := 1 + strings.Count(string(src[:f.Lines().At(0).Start]), "\n")
			where = append(where, fmt.Sprintf("%s:%d", p, line))
			return ast.WalkContinue, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return blocks, where
}

// TestOrbitLexerDocs highlights every ```orbit block of the docs and
// reports error tokens.
func TestOrbitLexerDocs(t *testing.T) {
	blocks, where := orbitBlocks(t, "../../docs")
	if len(blocks) < 50 {
		t.Fatalf("only %d orbit blocks in docs/", len(blocks))
	}
	for i, src := range blocks {
		_, toks := orbitTokens(t, src)
		for _, tok := range toks {
			if tok.Type == chroma.Error {
				t.Errorf("%s: error token %q in:\n%s", where[i], tok.Value, src)
			}
		}
	}
}

// BenchmarkOrbitDocs highlights every ```orbit block of the docs, as the
// code-block renderer does.
func BenchmarkOrbitDocs(b *testing.B) {
	blocks, _ := orbitBlocks(b, "../../docs")
	start := time.Now()
	var sb strings.Builder
	for _, src := range blocks {
		highlight(&sb, "orbit", src)
	}
	b.Logf("%d blocks, %d bytes of HTML; first pass (cold lexer) %v", len(blocks), sb.Len(), time.Since(start))
	for b.Loop() {
		sb.Reset()
		for _, src := range blocks {
			highlight(&sb, "orbit", src)
		}
	}
}
