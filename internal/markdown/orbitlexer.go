package markdown

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// The orbit lexer highlights ```orbit blocks (PLAN.md §2.4). It isn't in
// Chroma's global registry: lexerFor knows it by name, and it's built on
// first use, so it costs nothing at CLI startup.
//
//   - The frontmatter, between a first line "---" and the next "---" line
//     (as internal/compiler splits it), is Go.
//   - The template is HTML, with its own rules rather than Chroma's HTML
//     lexer (which can't be taught {…}): tags, attributes, comments,
//     entities. Component tags (<Card>, <Self>, <Fragment>, capitalized
//     like in the compiler) are NameClass, other tags NameTag.
//   - {…} in content, as an attribute value and in a start tag (spreads) is
//     matched up to its closing brace by braceRe, then split by braceTokens
//     as the compiler's lexBrace does: {/* comment */}, {if …}, {else if …},
//     {else}, {/if}, {for …}, {/for}, {switch …}, {case …}, {default},
//     {/switch}, {...spread}, or a Go expression.
//   - <script> content is TypeScript (JSON for type="application/json" and
//     "application/ld+json"), <style> content CSS (with Tailwind's @apply,
//     twcss.go), as in the compiler.
//
// Limits: a { without its } is one error token (the compiler reports it
// too), and the rest is lexed as template; a "<" followed by a letter
// always starts a tag, also inside <title> and <textarea> (RCDATA in the
// compiler); each {…} and the frontmatter are tokenized by Chroma's Go
// lexer on their own, so an expression is highlighted without its
// frontmatter context; a "</script>" inside a script's string ends the
// script, as in HTML.
var orbitLexer = sync.OnceValue(func() chroma.Lexer {
	return chroma.Coalesce(chroma.MustNewLexer(&chroma.Config{
		Name:      "Orbit",
		Aliases:   []string{"orbit"},
		Filenames: []string{"*.orbit"},
		DotAll:    true,
	}, orbitRules))
})

// braceRe matches a template's {…} the way internal/compiler's scanBraced
// finds its end: braces inside Go strings ("…" and '…' with backslash
// escapes, ending at a newline like in Go), raw strings (`…`) and comments
// (// up to the newline, /* … */) don't count, and nested braces are
// counted with a .NET balancing group (regexp2). Each step is atomic, so a
// construct without its closing brace fails in linear time.
var braceRe = `\{(?:(?>[^{}"'` + "`" + `/]+|"(?:[^"\\\n]|\\[^\n])*"|'(?:[^'\\\n]|\\[^\n])*'|` +
	"`[^`]*`" + `|//[^\n]*\n|/\*(?:[^*]|\*(?!/))*\*/|/(?![/*])|(?<o>\{)|(?<-o>\})))*(?(o)(?!))\}`

func orbitRules() chroma.Rules {
	goLexer := lexers.Get("go")
	ts := lexers.Get("typescript")
	css := twCSS{lexers.Get("css")}
	json := lexers.Get("json")
	brace := chroma.EmitterFunc(func(groups []string, _ *chroma.LexerState) chroma.Iterator {
		return chroma.Literator(braceTokens(goLexer, groups[0])...)
	})
	goCode := chroma.EmitterFunc(func(groups []string, _ *chroma.LexerState) chroma.Iterator {
		return chroma.Literator(goTokens(nil, goLexer, groups[0])...)
	})
	// The end tag of a raw-text element, as in Chroma's HTML lexer.
	endTag := func(name string) chroma.Rule {
		return chroma.Rule{Pattern: `(<)(\s*)(/)(\s*)(` + name + `)(\s*)(>)`,
			Type:    chroma.ByGroups(chroma.Punctuation, chroma.Text, chroma.Punctuation, chroma.Text, chroma.NameTag, chroma.Text, chroma.Punctuation),
			Mutator: chroma.Pop(1)}
	}
	content := func(name string, l chroma.Lexer) []chroma.Rule {
		return []chroma.Rule{
			endTag(name),
			{Pattern: `.+?(?=<\s*/\s*` + name + `\s*>)`, Type: chroma.UsingLexer(l)},
			{Pattern: `.+`, Type: chroma.UsingLexer(l)}, // no end tag
		}
	}
	return chroma.Rules{
		"root": {
			{Pattern: `\A(---)([ \t\r]*\n)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Text), Mutator: chroma.Push("frontmatter")},
			chroma.Default(chroma.Push("template")),
		},
		"frontmatter": {
			{Pattern: `^(---)([ \t\r]*)$`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Text), Mutator: chroma.Push("template")},
			{Pattern: `.+?(?=^---[ \t\r]*$)`, Type: goCode},
			{Pattern: `.+`, Type: goCode}, // no closing ---
		},
		"template": {
			{Pattern: `[^<&{}]+`, Type: chroma.Text},
			{Pattern: braceRe, Type: brace},
			{Pattern: `\{`, Type: chroma.Error}, // no matching }
			{Pattern: `<!--.*?(?:-->|\z)`, Type: chroma.Comment},
			{Pattern: `(?i)<!doctype[^>]*>?`, Type: chroma.CommentPreproc},
			{Pattern: `(?i)(<)(\s*)(script)(?=[^>]*\btype\s*=\s*["']?application/(?:ld\+)?json\b)`,
				Type: chroma.ByGroups(chroma.Punctuation, chroma.Text, chroma.NameTag), Mutator: chroma.Push("json-content", "tag")},
			{Pattern: `(?i)(<)(\s*)(script)(?=[\s/>])`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Text, chroma.NameTag), Mutator: chroma.Push("script-content", "tag")},
			{Pattern: `(?i)(<)(\s*)(style)(?=[\s/>])`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Text, chroma.NameTag), Mutator: chroma.Push("style-content", "tag")},
			{Pattern: `(<)([A-Z][\w.]*)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.NameClass), Mutator: chroma.Push("tag")},
			{Pattern: `(<)([A-Za-z][\w:.-]*)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.NameTag), Mutator: chroma.Push("tag")},
			{Pattern: `(<)(/)(\s*)([A-Z][\w.]*)(\s*)(>)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Punctuation, chroma.Text, chroma.NameClass, chroma.Text, chroma.Punctuation)},
			{Pattern: `(<)(/)(\s*)([A-Za-z][\w:.-]*)(\s*)(>)`, Type: chroma.ByGroups(chroma.Punctuation, chroma.Punctuation, chroma.Text, chroma.NameTag, chroma.Text, chroma.Punctuation)},
			{Pattern: `&(?:[A-Za-z][A-Za-z0-9]*|#[0-9]+|#[xX][0-9A-Fa-f]+);`, Type: chroma.NameEntity},
			{Pattern: `[<&}]`, Type: chroma.Text}, // "a < b", a lone "&" or "}"
		},
		"tag": {
			{Pattern: `\s+`, Type: chroma.Text},
			{Pattern: `/?>`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
			{Pattern: braceRe, Type: brace}, // {...spread}
			{Pattern: `([^\s"'<>/=\{\}` + "`" + `]+)(\s*)(=)(\s*)`, Type: chroma.ByGroups(chroma.NameAttribute, chroma.Text, chroma.Operator, chroma.Text), Mutator: chroma.Push("attr")},
			{Pattern: `[^\s"'<>/=\{\}` + "`" + `]+`, Type: chroma.NameAttribute},
			{Pattern: `\{`, Type: chroma.Error},
			{Pattern: `.`, Type: chroma.Text},
		},
		"attr": {
			{Pattern: braceRe, Type: brace, Mutator: chroma.Pop(1)},
			{Pattern: `".*?"`, Type: chroma.LiteralString, Mutator: chroma.Pop(1)},
			{Pattern: `'.*?'`, Type: chroma.LiteralString, Mutator: chroma.Pop(1)},
			{Pattern: `[^\s>{]+`, Type: chroma.LiteralString, Mutator: chroma.Pop(1)},
			chroma.Default(chroma.Pop(1)),
		},
		"script-content": content("script", ts),
		"json-content":   content("script", json),
		"style-content":  content("style", css),
	}
}

// braceTokens splits a template's {…} as internal/compiler's lexBrace
// does.
func braceTokens(goLexer chroma.Lexer, s string) []chroma.Token {
	inner := s[1 : len(s)-1]
	t := strings.TrimLeft(inner, " \t\r\n\f")
	lead := inner[:len(inner)-len(t)]
	tok := func(tt chroma.TokenType, v string) chroma.Token { return chroma.Token{Type: tt, Value: v} }
	out := []chroma.Token{tok(chroma.Punctuation, "{")}
	if lead != "" {
		out = append(out, tok(chroma.TextWhitespace, lead))
	}
	goCode := func(code string) { out = goTokens(out, goLexer, code) }
	// {/* comment */}
	if strings.HasPrefix(t, "/*") {
		if k := strings.Index(t[2:], "*/"); k >= 0 && strings.TrimSpace(t[2+k+2:]) == "" {
			out[0] = tok(chroma.CommentMultiline, s)
			return out[:1]
		}
	}
	kw := func(word string, rest string) []chroma.Token {
		out = append(out, tok(chroma.Keyword, word))
		goCode(rest)
		return append(out, tok(chroma.Punctuation, "}"))
	}
	switch identPrefix(t) {
	case "if", "for", "switch", "case", "default":
		w := identPrefix(t)
		return kw(w, t[len(w):])
	case "else":
		rest := t[4:]
		r := strings.TrimLeft(rest, " \t\r\n\f")
		if identPrefix(r) == "if" {
			out = append(out, tok(chroma.Keyword, "else"), tok(chroma.TextWhitespace, rest[:len(rest)-len(r)]))
			return kw("if", r[2:])
		}
		return kw("else", rest)
	}
	if strings.HasPrefix(t, "/") {
		if w := strings.TrimSpace(t[1:]); w == "if" || w == "for" || w == "switch" {
			end := strings.TrimRight(t, " \t\r\n\f")
			out = append(out, tok(chroma.Keyword, end))
			if end != t {
				out = append(out, tok(chroma.TextWhitespace, t[len(end):]))
			}
			return append(out, tok(chroma.Punctuation, "}"))
		}
	}
	if strings.HasPrefix(t, "...") {
		out = append(out, tok(chroma.Operator, "..."))
		t = t[3:]
	}
	goCode(t)
	return append(out, tok(chroma.Punctuation, "}"))
}

// goTokens appends the Go tokens of code to out. An ellipsis (…), which
// docs write for elided code, is text rather than an error.
func goTokens(out []chroma.Token, goLexer chroma.Lexer, code string) []chroma.Token {
	if code == "" {
		return out
	}
	it, err := goLexer.Tokenise(&chroma.TokeniseOptions{State: "root", Nested: true}, code)
	if err != nil {
		return append(out, chroma.Token{Type: chroma.Error, Value: code})
	}
	for x := it(); x != chroma.EOF; x = it() {
		if x.Type == chroma.Error && strings.Trim(x.Value, "…") == "" {
			x.Type = chroma.Text
		}
		out = append(out, x)
	}
	return out
}

// identPrefix returns the identifier s starts with, as internal/compiler's.
func identPrefix(s string) string {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			break
		}
		i += size
	}
	return s[:i]
}
