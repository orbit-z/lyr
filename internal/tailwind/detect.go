// Package tailwind runs Tailwind CSS v4's CLI over the stylesheets that use
// it (PLAN.md §5d): detection of Tailwind inputs, the CLI lookup, the
// inlining of the CSS files a Tailwind input @imports (with their url()s
// rewritten, as @tailwindcss/vite does), the cached runs, error cleanup
// and the dev loop's token gate. internal/assets calls it from esbuild's
// load step; nothing here runs for a project without Tailwind.
package tailwind

import (
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// Info is what Detect found in a stylesheet.
type Info struct {
	// Input is set when the stylesheet is a Tailwind input: it @imports
	// tailwindcss (or tailwindcss/…), has a @tailwind directive, or uses
	// a Tailwind at-rule (@apply, @reference, @theme, …).
	Input bool
	// Root is set when it imports tailwindcss or has a @tailwind
	// directive: its output depends on the classes in the project's
	// sources.
	Root bool
	// Pos is the byte offset of the first marker.
	Pos int
}

// atRules are Tailwind's at-rules other than @import and @tailwind.
var atRules = map[string]bool{
	"apply": true, "reference": true, "theme": true, "utility": true, "variant": true,
	"custom-variant": true, "source": true, "plugin": true, "config": true,
}

// mayUse is a quick pre-check: false when css can't have a marker.
func mayUse(src string) bool {
	if !strings.Contains(src, "@") {
		return false
	}
	if strings.Contains(src, "tailwind") {
		return true
	}
	for i := 0; i < len(src); i++ {
		if src[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(src) && (src[j] == '-' || src[j] >= 'a' && src[j] <= 'z') {
			j++
		}
		if atRules[src[i+1:j]] {
			return true
		}
	}
	return false
}

// Detect reports whether src is a Tailwind input. Comments and strings
// don't count: it runs CSS's tokenizer, only when a quick text search
// finds a candidate.
func Detect(src string) Info {
	if !mayUse(src) {
		return Info{}
	}
	toks := lex(src)
	var info Info
	mark := func(pos int, root bool) {
		if !info.Input {
			info.Input, info.Pos = true, pos
		}
		info.Root = info.Root || root
	}
	for i, t := range toks {
		if t.tt != css.AtKeywordToken {
			continue
		}
		name := strings.ToLower(src[t.start+1 : t.end])
		switch {
		case name == "tailwind":
			mark(t.start, true)
		case name == "import":
			if spec, _, ok := importSpec(src, toks, i+1); ok && isTailwindSpec(spec) {
				mark(t.start, true)
			}
		case atRules[name]:
			mark(t.start, false)
		}
		if info.Root {
			break
		}
	}
	return info
}

// MarkerName names the marker at offset pos of src for messages:
// `@import "tailwindcss"` for an import, else the at-rule ("@apply").
func MarkerName(src string, pos int) string {
	if pos < 0 || pos >= len(src) || src[pos] != '@' {
		return "Tailwind CSS"
	}
	j := pos + 1
	for j < len(src) && (src[j] == '-' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z') {
		j++
	}
	name := src[pos:j]
	if strings.EqualFold(name, "@import") {
		rest := src[j:]
		if spec, _, ok := importSpec(rest, lex(rest), 0); ok {
			return "@import " + quote(spec)
		}
	}
	return name
}

// isTailwindSpec reports whether an @import specifier is Tailwind's own
// CSS: tailwindcss or tailwindcss/….
func isTailwindSpec(spec string) bool {
	return spec == "tailwindcss" || strings.HasPrefix(spec, "tailwindcss/")
}

// tok is a CSS token: src[start:end].
type tok struct {
	tt         css.TokenType
	start, end int
}

// lex tokenizes src; the tokens cover it byte for byte.
func lex(src string) []tok {
	toks := make([]tok, 0, len(src)/4+1)
	l := css.NewLexer(parse.NewInputString(src))
	pos := 0
	for {
		tt, data := l.Next()
		if tt == css.ErrorToken {
			break
		}
		toks = append(toks, tok{tt, pos, pos + len(data)})
		pos += len(data)
	}
	return toks
}

// skipSpace returns the index of the first token from i that isn't
// whitespace or a comment.
func skipSpace(toks []tok, i int) int {
	for i < len(toks) && (toks[i].tt == css.WhitespaceToken || toks[i].tt == css.CommentToken) {
		i++
	}
	return i
}

// importSpec reads the URL of an @import whose prelude starts at token i:
// a string, url(…) or url("…"). It returns the specifier and the index of
// the token after it.
func importSpec(src string, toks []tok, i int) (string, int, bool) {
	i = skipSpace(toks, i)
	if i >= len(toks) {
		return "", i, false
	}
	t := toks[i]
	switch t.tt {
	case css.StringToken:
		return unquote(src[t.start:t.end]), i + 1, true
	case css.URLToken:
		return urlTokenValue(src[t.start:t.end]), i + 1, true
	case css.FunctionToken:
		if !strings.EqualFold(src[t.start:t.end], "url(") {
			return "", i, false
		}
		j := skipSpace(toks, i+1)
		if j >= len(toks) || toks[j].tt != css.StringToken {
			return "", i, false
		}
		s := unquote(src[toks[j].start:toks[j].end])
		k := skipSpace(toks, j+1)
		if k >= len(toks) || toks[k].tt != css.RightParenthesisToken {
			return "", i, false
		}
		return s, k + 1, true
	}
	return "", i, false
}

// unquote returns a CSS string token's value (escapes of quotes and
// backslashes resolved; other escapes kept as written).
func unquote(s string) string {
	if len(s) < 2 {
		return ""
	}
	q := s[0]
	s = s[1:]
	if s[len(s)-1] == q {
		s = s[:len(s)-1]
	}
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\'' || s[i+1] == '\\') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// urlTokenValue returns the value of an unquoted url(…) token.
func urlTokenValue(s string) string {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return ""
	}
	v := strings.TrimSpace(s[open+1 : len(s)-1])
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		return unquote(v) // the lexer's url("…")
	}
	return v
}

// quote returns s as a CSS double-quoted string.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\a `)
	return `"` + r.Replace(s) + `"`
}
