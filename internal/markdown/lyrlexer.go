package markdown

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// The lyr lexer highlights ```lyr blocks (PLAN.md §2.4). It isn't in
// Chroma's global registry: lexerFor knows it by name, and it's built on
// first use, so it costs nothing at CLI startup.
//
//   - The frontmatter, between a first line "---" and the next "---" line
//     (as internal/compiler splits it), is Go.
//   - The template is HTML, with its own rules rather than Chroma's HTML
//     lexer (which can't be taught {…}): tags, attributes, comments,
//     entities. Component tags (<Card>, <Self>, <Fragment>, capitalized
//     like in the compiler) are NameClass, other tags NameTag; the fragment
//     shorthand <>…</> is tag punctuation.
//   - {…} in content, as an attribute value and in a start tag (spreads) is
//     matched up to its closing brace by scanBrace (a state machine, as the
//     compiler's scanBraced), then split by braceTokens as the compiler's
//     lexBrace does: {/* comment */}, {if …}, {else if …}, {else}, {/if},
//     {for …}, {/for}, {switch …}, {case …}, {default}, {/switch},
//     {...spread}, or a Go expression.
//   - In a template expression every backtick string is a template literal
//     (PLAN.md §2.10): its text is LiteralStringBacktick (escapes
//     LiteralStringEscape), "${" and "}" LiteralStringInterpol, and each
//     piece is lexed as a template expression again, so nested literals are
//     exact. The frontmatter keeps Go's raw strings.
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
var lyrLexer = sync.OnceValue(func() chroma.Lexer {
	return chroma.Coalesce(chroma.MustNewLexer(&chroma.Config{
		Name:      "Lyr",
		Aliases:   []string{"lyr"},
		Filenames: []string{"*.lyr"},
		DotAll:    true,
	}, lyrRules))
})

func lyrRules() chroma.Rules {
	goLexer := lexers.Get("go")
	ts := lexers.Get("typescript")
	css := twCSS{lexers.Get("css")}
	json := lexers.Get("json")
	// brace is the emitter of a rule matching "{": it scans the rest of
	// the expression itself and moves the lexer past it (regular
	// expressions can't follow nested template literals).
	brace := chroma.EmitterFunc(func(_ []string, l *chroma.LexerState) chroma.Iterator {
		start := l.Pos - 1
		end := scanBrace(l.Text, start)
		if end < 0 {
			return chroma.Literator(chroma.Token{Type: chroma.Error, Value: "{"})
		}
		l.Pos = end
		return chroma.Literator(braceTokens(goLexer, string(l.Text[start:end]))...)
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
			{Pattern: `\{`, Type: brace}, // an error token without its }
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
			{Pattern: `</?>`, Type: chroma.Punctuation}, // the fragment shorthand <>…</>
			{Pattern: `&(?:[A-Za-z][A-Za-z0-9]*|#[0-9]+|#[xX][0-9A-Fa-f]+);`, Type: chroma.NameEntity},
			{Pattern: `[<&}]`, Type: chroma.Text}, // "a < b", a lone "&" or "}"
		},
		"tag": {
			{Pattern: `\s+`, Type: chroma.Text},
			{Pattern: `/?>`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
			{Pattern: `\{`, Type: brace}, // {...spread}
			{Pattern: `([^\s"'<>/=\{\}` + "`" + `]+)(\s*)(=)(\s*)`, Type: chroma.ByGroups(chroma.NameAttribute, chroma.Text, chroma.Operator, chroma.Text), Mutator: chroma.Push("attr")},
			{Pattern: `[^\s"'<>/=\{\}` + "`" + `]+`, Type: chroma.NameAttribute},
			{Pattern: `.`, Type: chroma.Text},
		},
		"attr": {
			{Pattern: `\{`, Type: brace, Mutator: chroma.Pop(1)},
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
	goCode := func(code string) { out = exprTokens(out, goLexer, code) }
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

// scanBrace returns the end (exclusive) of the {…} starting at t[i], as
// internal/compiler's scanBraced finds it, or -1: braces inside Go strings
// ("…" and '…' with backslash escapes, ending at a newline like in Go),
// comments (// up to a newline, /* … */) and template literals (`…` with
// ${…} pieces, nested) don't count.
func scanBrace(t []rune, i int) int {
	depth := 0
	for i < len(t) {
		switch t[i] {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
			if depth == 0 {
				return i
			}
		case '"', '\'':
			i = scanQuoted(t, i)
		case '`':
			i = scanLiteral(t, i)
		case '/':
			i = scanComment(t, i, true)
		default:
			i++
		}
		if i < 0 {
			return -1
		}
	}
	return -1
}

// scanQuoted returns the end of the Go string or rune at t[i], or -1.
func scanQuoted(t []rune, i int) int {
	q := t[i]
	for i++; i < len(t); i++ {
		switch t[i] {
		case q:
			return i + 1
		case '\n':
			return -1
		case '\\':
			if i+1 >= len(t) || t[i+1] == '\n' {
				return -1
			}
			i++
		}
	}
	return -1
}

// scanComment returns the end of the comment at t[i] (i+1 for a lone
// "/"). An unterminated comment ends at -1 when strict, else at len(t).
func scanComment(t []rune, i int, strict bool) int {
	if i+1 >= len(t) || (t[i+1] != '/' && t[i+1] != '*') {
		return i + 1
	}
	line := t[i+1] == '/'
	for j := i + 2; j < len(t); j++ {
		if line && t[j] == '\n' {
			return j // the newline isn't part of the comment
		}
		if !line && t[j] == '/' && t[j-1] == '*' && j > i+2 {
			return j + 1
		}
	}
	if strict {
		return -1
	}
	return len(t)
}

// scanLiteral returns the end of the template literal at t[i], or -1.
func scanLiteral(t []rune, i int) int {
	for i++; i < len(t); {
		switch {
		case t[i] == '`':
			return i + 1
		case t[i] == '\\':
			i += 2
		case t[i] == '$' && i+1 < len(t) && t[i+1] == '{':
			if i = scanBrace(t, i+1); i < 0 {
				return -1
			}
		default:
			i++
		}
	}
	return -1
}

// exprTokens appends the tokens of the Go code of a template expression:
// Go, with its backtick strings as template literals.
func exprTokens(out []chroma.Token, goLexer chroma.Lexer, code string) []chroma.Token {
	if !strings.Contains(code, "`") {
		return goTokens(out, goLexer, code)
	}
	t := []rune(code)
	last := 0
	for i := 0; i < len(t); {
		switch t[i] {
		case '"', '\'':
			if i = scanQuoted(t, i); i < 0 {
				i = len(t)
			}
		case '/':
			i = scanComment(t, i, false)
		case '`':
			out = goTokens(out, goLexer, string(t[last:i]))
			out, i = literalTokens(out, goLexer, t, i)
			last = i
		default:
			i++
		}
	}
	return goTokens(out, goLexer, string(t[last:]))
}

// literalTokens appends the tokens of the template literal at t[i] and
// returns its end.
func literalTokens(out []chroma.Token, goLexer chroma.Lexer, t []rune, i int) ([]chroma.Token, int) {
	tok := func(tt chroma.TokenType, v []rune) {
		if len(v) > 0 {
			out = append(out, chroma.Token{Type: tt, Value: string(v)})
		}
	}
	text := i // start of the pending static text, the opening backtick included
	for i++; i < len(t); {
		switch {
		case t[i] == '`':
			tok(chroma.LiteralStringBacktick, t[text:i+1])
			return out, i + 1
		case t[i] == '\\':
			tok(chroma.LiteralStringBacktick, t[text:i])
			e := min(i+2, len(t))
			tok(chroma.LiteralStringEscape, t[i:e])
			i, text = e, e
		case t[i] == '$' && i+1 < len(t) && t[i+1] == '{':
			tok(chroma.LiteralStringBacktick, t[text:i])
			tok(chroma.LiteralStringInterpol, t[i:i+2])
			end := scanBrace(t, i+1)
			if end < 0 { // not closed: the rest is the piece
				return exprTokens(out, goLexer, string(t[i+2:])), len(t)
			}
			out = exprTokens(out, goLexer, string(t[i+2:end-1]))
			tok(chroma.LiteralStringInterpol, t[end-1:end])
			i, text = end, end
		default:
			i++
		}
	}
	tok(chroma.LiteralStringBacktick, t[text:])
	return out, len(t)
}
