package compiler

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokKind is the kind of a lexer token.
type tokKind uint8

const (
	tokEOF tokKind = iota
	tokText
	tokExpr
	tokComment
	tokIf
	tokElseIf
	tokElse
	tokEndIf
	tokFor
	tokEndFor
	tokSwitch
	tokCase
	tokDefault
	tokEndSwitch
	tokStartTag
	tokEndTag
	tokDoctype
	tokHTMLComment
	tokRaw // <script>/<style> content
	tokBad // a malformed construct, already reported; the parser drops it
)

// token is one lexical unit of the template. Start tags come out whole, with
// their attributes parsed, because attribute syntax depends on context the
// parser doesn't need.
type token struct {
	kind tokKind
	span Span
	code GoCode  // tokExpr and the control headers
	body RawText // tokComment, tokDoctype, tokHTMLComment, tokRaw
	// Tags.
	name        string
	nameSpan    Span
	attrs       []*Attr
	selfClosing bool
}

// lexer splits a template into tokens. It is driven by the parser, which
// switches it into raw-text mode after <script>/<style> and RCDATA mode
// inside <title>/<textarea>.
type lexer struct {
	src   string
	pos   int
	diags *[]Diagnostic
	// rawTag, when set, makes the next token the raw content up to </rawTag.
	rawTag string
	// rcdata, when set, makes "<" start nothing but </rcdata.
	rcdata string
}

func (l *lexer) errorf(s Span, format string, args ...any) {
	*l.diags = append(*l.diags, Diagnostic{SeverityError, s, fmt.Sprintf(format, args...)})
}

// next returns the next token, tokEOF at the end.
func (l *lexer) next() token {
	if l.rawTag != "" {
		return l.lexRaw()
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, span: Span{len(l.src), len(l.src)}}
	}
	switch c := l.src[l.pos]; {
	case c == '{':
		return l.lexBrace()
	case c == '<' && l.startsTag(l.pos):
		return l.lexMarkup()
	}
	return l.lexText()
}

// startsTag reports whether the '<' at i starts markup: a letter, '/' or '!'
// follows it. In RCDATA only the matching end tag does.
func (l *lexer) startsTag(i int) bool {
	if l.rcdata != "" {
		return l.isEndTagOf(i, l.rcdata)
	}
	if i+1 >= len(l.src) {
		return false
	}
	c := l.src[i+1]
	return isASCIILetter(c) || c == '/' || c == '!'
}

// isEndTagOf reports whether src[i:] starts with </name (case-sensitive,
// like every tag name in Orbit) followed by a space, '/', '>' or the end of the file.
func (l *lexer) isEndTagOf(i int, name string) bool {
	s := l.src[i:]
	if len(s) < 2+len(name) || s[0] != '<' || s[1] != '/' || s[2:2+len(name)] != name {
		return false
	}
	if len(s) == 2+len(name) {
		return true
	}
	c := s[2+len(name)]
	return isSpace(c) || c == '/' || c == '>'
}

func (l *lexer) lexText() token {
	start := l.pos
	i := start + 1
	for i < len(l.src) {
		k := strings.IndexAny(l.src[i:], "{<")
		if k < 0 {
			i = len(l.src)
			break
		}
		i += k
		if l.src[i] == '{' || l.startsTag(i) {
			break
		}
		i++
	}
	l.pos = i
	return token{kind: tokText, span: Span{start, i}}
}

// lexRaw returns the content of a <script> or <style>, up to its end tag.
func (l *lexer) lexRaw() token {
	start := l.pos
	end := len(l.src)
	for i := start; i < len(l.src); {
		k := strings.IndexByte(l.src[i:], '<')
		if k < 0 {
			break
		}
		i += k
		if l.isEndTagOf(i, l.rawTag) {
			end = i
			break
		}
		i++
	}
	l.rawTag = ""
	l.pos = end
	s := Span{start, end}
	return token{kind: tokRaw, span: s, body: RawText{s, l.src[start:end]}}
}

// braceEnd finds the end of the {…} construct at start. It returns the
// offset of the inner end (the closing '}' or, for an unterminated construct,
// the end of the line), the token end, and whether a '}' closed it.
func (l *lexer) braceEnd(start int) (innerEnd, end int, closed bool) {
	if j := scanBraced(l.src, start); j > start && j < len(l.src) {
		return j, j + 1, true
	}
	l.errorf(Span{start, start + 1}, "{ has no matching }")
	eol := strings.IndexByte(l.src[start:], '\n')
	if eol < 0 {
		return len(l.src), len(l.src), false
	}
	return start + eol, start + eol, false
}

// lexBrace lexes {…} in content: an expression, a template comment or a
// control block tag.
func (l *lexer) lexBrace() token {
	start := l.pos
	innerEnd, end, _ := l.braceEnd(start)
	l.pos = end
	tok := token{span: Span{start, end}}
	inner := start + 1
	lead := inner + leadingSpace(l.src[inner:innerEnd])
	t := l.src[lead:innerEnd]

	// {/* comment */}
	if strings.HasPrefix(t, "/*") {
		if k := strings.Index(t[2:], "*/"); k >= 0 && strings.TrimSpace(t[2+k+2:]) == "" {
			b := Span{lead + 2, lead + 2 + k}
			tok.kind = tokComment
			tok.body = RawText{b, l.src[b.Start:b.End]}
			return tok
		}
	}
	switch identPrefix(t) {
	case "if":
		tok.kind = tokIf
		tok.code = l.trimCode(lead+2, innerEnd)
		if tok.code.Text == "" {
			l.errorf(tok.span, "{if} needs a condition")
		}
		return tok
	case "for":
		tok.kind = tokFor
		tok.code = l.trimCode(lead+3, innerEnd)
		if tok.code.Text == "" {
			l.errorf(tok.span, "{for} needs a clause, e.g. {for _, x := range list}")
		}
		return tok
	case "else":
		rest := lead + 4
		rest += leadingSpace(l.src[rest:innerEnd])
		if rest == innerEnd {
			tok.kind = tokElse
			tok.code = GoCode{Span{rest, rest}, ""}
			return tok
		}
		if identPrefix(l.src[rest:innerEnd]) == "if" {
			tok.kind = tokElseIf
			tok.code = l.trimCode(rest+2, innerEnd)
			if tok.code.Text == "" {
				l.errorf(tok.span, "{else if} needs a condition")
			}
			return tok
		}
		l.errorf(tok.span, "unexpected text after {else}; write {else} or {else if condition}")
		tok.kind = tokElse
		tok.code = GoCode{Span{rest, rest}, ""}
		return tok
	case "switch":
		tok.kind = tokSwitch
		tok.code = l.trimCode(lead+6, innerEnd)
		return tok
	case "case":
		tok.kind = tokCase
		tok.code = l.trimCode(lead+4, innerEnd)
		if tok.code.Text == "" {
			l.errorf(tok.span, `{case} needs a value or a condition, e.g. {case "draft"}; use {default} for the rest`)
		}
		return tok
	case "default":
		rest := lead + 7
		rest += leadingSpace(l.src[rest:innerEnd])
		if rest != innerEnd {
			l.errorf(tok.span, "unexpected text after {default}; write {default} or {case list}")
		}
		tok.kind = tokDefault
		tok.code = GoCode{Span{rest, rest}, ""}
		return tok
	case "fallthrough":
		l.errorf(tok.span, "fallthrough isn't supported in a {switch}; list the values in one {case a, b}")
		tok.kind = tokBad
		return tok
	}
	if strings.HasPrefix(t, "/") {
		switch end := strings.TrimSpace(t[1:]); end {
		case "if":
			tok.kind = tokEndIf
		case "for":
			tok.kind = tokEndFor
		case "switch":
			tok.kind = tokEndSwitch
		case "case", "default":
			l.errorf(tok.span, "unexpected {/%s}: a {%s} ends at the next {case}, {default} or {/switch}", end, end)
			tok.kind = tokBad
		default:
			l.errorf(tok.span, "unknown block end {%s}; blocks end with {/if}, {/for} or {/switch}", strings.TrimSpace(t))
			tok.kind = tokBad
		}
		return tok
	}
	if strings.HasPrefix(t, "...") {
		l.errorf(tok.span, "a spread {...} only works inside a start tag")
		tok.kind = tokBad
		return tok
	}
	tok.kind = tokExpr
	tok.code = l.trimCode(inner, innerEnd)
	return tok
}

// trimCode returns src[a:b] without its surrounding whitespace, as GoCode.
func (l *lexer) trimCode(a, b int) GoCode {
	if a > b {
		a = b
	}
	s := l.src[a:b]
	start := a + leadingSpace(s)
	end := start + len(strings.TrimRight(l.src[start:b], " \t\r\n\f"))
	return GoCode{Span{start, end}, l.src[start:end]}
}

// lexMarkup lexes what starts at a '<' that starts a tag.
func (l *lexer) lexMarkup() token {
	start := l.pos
	s := l.src[start:]
	switch {
	case strings.HasPrefix(s, "<!--"):
		k := strings.Index(s[4:], "-->")
		end, bodyEnd := len(l.src), len(l.src)
		if k < 0 {
			l.errorf(Span{start, start + 4}, "HTML comment isn't closed; add -->")
		} else {
			bodyEnd = start + 4 + k
			end = bodyEnd + 3
		}
		l.pos = end
		b := Span{start + 4, bodyEnd}
		return token{kind: tokHTMLComment, span: Span{start, end}, body: RawText{b, l.src[b.Start:b.End]}}
	case s[1] == '!':
		k := strings.IndexByte(s, '>')
		end, bodyEnd := len(l.src), len(l.src)
		if k < 0 {
			l.errorf(Span{start, start + 2}, "<! declaration isn't closed; expected >")
		} else {
			bodyEnd = start + k
			end = bodyEnd + 1
		}
		l.pos = end
		b := Span{start + 2, bodyEnd}
		return token{kind: tokDoctype, span: Span{start, end}, body: RawText{b, l.src[b.Start:b.End]}}
	case s[1] == '/':
		return l.lexEndTag()
	}
	return l.lexStartTag()
}

func (l *lexer) lexEndTag() token {
	start := l.pos
	i := start + 2
	if i >= len(l.src) || !isASCIILetter(l.src[i]) {
		l.errorf(Span{start, start + 2}, "expected a tag name after </")
		l.pos = start + 2
		return token{kind: tokText, span: Span{start, start + 2}}
	}
	nameEnd := i + tagNameLen(l.src[i:])
	tok := token{kind: tokEndTag, name: l.src[i:nameEnd], nameSpan: Span{i, nameEnd}}
	j := nameEnd + leadingSpace(l.src[nameEnd:])
	switch {
	case j < len(l.src) && l.src[j] == '>':
		j++
	default:
		k := strings.IndexAny(l.src[j:], "<>")
		if k >= 0 && l.src[j+k] == '>' {
			l.errorf(Span{nameEnd, j + k}, "end tag </%s> can't have attributes", tok.name)
			j += k + 1
		} else {
			l.errorf(Span{start, nameEnd}, "end tag </%s isn't closed; expected >", tok.name)
		}
	}
	l.pos = j
	tok.span = Span{start, j}
	return tok
}

func (l *lexer) lexStartTag() token {
	start := l.pos
	i := start + 1
	nameEnd := i + tagNameLen(l.src[i:])
	tok := token{kind: tokStartTag, name: l.src[i:nameEnd], nameSpan: Span{i, nameEnd}}
	l.pos = nameEnd
	for {
		l.pos += leadingSpace(l.src[l.pos:])
		if l.pos >= len(l.src) {
			l.errorf(Span{start, nameEnd}, "start tag <%s isn't closed; expected >", tok.name)
			break
		}
		c := l.src[l.pos]
		if c == '>' {
			l.pos++
			break
		}
		if c == '/' {
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
				tok.selfClosing = true
				l.pos += 2
				break
			}
			l.errorf(Span{l.pos, l.pos + 1}, "unexpected / in <%s> start tag", tok.name)
			l.pos++
			continue
		}
		if c == '<' {
			l.errorf(Span{start, nameEnd}, "start tag <%s isn't closed; expected >", tok.name)
			break
		}
		if c == '{' {
			if a := l.lexBraceAttr(); a != nil {
				tok.attrs = append(tok.attrs, a)
			}
			continue
		}
		if c == '"' || c == '\'' || c == '=' || c == '}' {
			l.errorf(Span{l.pos, l.pos + 1}, "unexpected %c in <%s> start tag", c, tok.name)
			l.pos++
			continue
		}
		tok.attrs = append(tok.attrs, l.lexAttr())
	}
	tok.span = Span{start, l.pos}
	return tok
}

// lexAttr lexes a named attribute: a, a="s", a='s', a=bare or a={expr}.
func (l *lexer) lexAttr() *Attr {
	start := l.pos
	n := attrNameLen(l.src[start:])
	a := &Attr{Kind: AttrEmpty, Name: l.src[start : start+n], NameSpan: Span{start, start + n}}
	a.Directive = classifyDirective(a.Name)
	l.pos = start + n
	a.Span = a.NameSpan
	j := l.pos + leadingSpace(l.src[l.pos:])
	if j >= len(l.src) || l.src[j] != '=' {
		return a
	}
	j++
	j += leadingSpace(l.src[j:])
	if j >= len(l.src) || l.src[j] == '>' || strings.HasPrefix(l.src[j:], "/>") || l.src[j] == '<' {
		l.errorf(a.NameSpan, "attribute %s has = but no value", a.Name)
		l.pos = j
		a.End = j
		return a
	}
	switch c := l.src[j]; c {
	case '"', '\'':
		a.Kind = AttrQuoted
		vs := j + 1
		ve, end := vs, 0
		if k := strings.IndexByte(l.src[vs:], c); k >= 0 {
			ve, end = vs+k, vs+k+1
		} else {
			l.errorf(Span{j, j + 1}, "attribute value isn't closed; add %c", c)
			ve = len(l.src)
			if k := strings.IndexByte(l.src[vs:], '\n'); k >= 0 {
				ve = vs + k
			}
			end = ve
		}
		a.Value, a.ValueSpan = l.src[vs:ve], Span{vs, ve}
		l.pos = end
	case '{':
		a.Kind = AttrExpr
		innerEnd, end, _ := l.braceEnd(j)
		a.ValueSpan = Span{j, end}
		a.Code = l.trimCode(j+1, innerEnd)
		l.pos = end
	default:
		a.Kind = AttrUnquoted
		k := j
		for k < len(l.src) && !isSpace(l.src[k]) && l.src[k] != '>' {
			k++
		}
		a.Value, a.ValueSpan = l.src[j:k], Span{j, k}
		l.pos = k
	}
	a.End = l.pos
	return a
}

// lexBraceAttr lexes {a} or {...attrs} inside a start tag. It returns nil for
// a malformed one (reported).
func (l *lexer) lexBraceAttr() *Attr {
	start := l.pos
	innerEnd, end, closed := l.braceEnd(start)
	l.pos = end
	whole := Span{start, end}
	inner := start + 1
	lead := inner + leadingSpace(l.src[inner:innerEnd])
	t := l.src[lead:innerEnd]
	if strings.HasPrefix(t, "...") {
		a := &Attr{Span: whole, Kind: AttrSpread, ValueSpan: whole, Code: l.trimCode(lead+3, innerEnd)}
		if a.Code.Text == "" {
			l.errorf(whole, "a spread needs a value, e.g. {...attrs}")
		}
		return a
	}
	code := l.trimCode(inner, innerEnd)
	switch w := identPrefix(t); {
	case !closed && !isIdent(code.Text):
		// Already reported as unterminated.
	case blockKeywords[w] || strings.HasPrefix(t, "/") && blockKeywords[identPrefix(strings.TrimSpace(t[1:]))]:
		l.errorf(whole, "blocks can't appear inside a start tag; set the attribute's value instead (false and nil omit it)")
	case isIdent(code.Text):
		return &Attr{Span: whole, Kind: AttrShorthand, Name: code.Text, NameSpan: code.Span,
			ValueSpan: whole, Code: code, Directive: classifyDirective(code.Text)}
	default:
		l.errorf(whole, "{…} in a start tag must be a name, as in {title}, or a spread, as in {...attrs}")
	}
	return nil
}

// blockKeywords are the words that start a control block tag.
var blockKeywords = map[string]bool{
	"if": true, "else": true, "for": true, "switch": true, "case": true, "default": true, "fallthrough": true,
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isASCIILetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func leadingSpace(s string) int {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

// tagNameLen returns the length of the tag name at the start of s.
func tagNameLen(s string) int {
	i := 0
	for i < len(s) && !isSpace(s[i]) && !strings.ContainsRune("/><{}\"'=", rune(s[i])) {
		i++
	}
	return i
}

// attrNameLen returns the length of the attribute name at the start of s
// (at least 1: the caller has checked the first byte can start one).
func attrNameLen(s string) int {
	i := 0
	for i < len(s) && !isSpace(s[i]) && !strings.ContainsRune("/><{}\"'=", rune(s[i])) {
		i++
	}
	return max(i, 1)
}

// identPrefix returns the Go identifier at the start of s, or "".
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

// isIdent reports whether s is a Go identifier.
func isIdent(s string) bool {
	return s != "" && identPrefix(s) == s
}
