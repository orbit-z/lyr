package compiler

// Template literals (PLAN.md §2.10): in a template expression, every
// backtick string is a JavaScript template literal, `a ${x} b`, whose
// pieces are Go expressions. This file is the lexical part: scanning (for
// scanBraced), parsing into parts with JavaScript's escapes cooked, finding
// the literals of a Go region, and the same-length placeholder text that
// lets go/parser read a region holding literals.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TemplateLiteral is one template literal in a template expression.
type TemplateLiteral struct {
	// Span covers the literal, both backticks included (to the end of the
	// region scanned when it's unterminated).
	Span
	Closed bool
	// Parts alternate: static text, piece, static text, …, static text. A
	// literal with n pieces has 2n+1 parts; static parts may be empty.
	Parts []TLPart
}

// TLPart is a static run of a template literal or one of its pieces.
type TLPart struct {
	// Span is a static part's raw text (between the delimiters), or a
	// piece's Go code with surrounding whitespace trimmed (an empty span
	// after "${" for an empty piece).
	Span
	Piece bool
	// Text is a static part's cooked text (escapes processed, line breaks
	// normalized to \n), or a piece's code as written.
	Text string
	// Lits are the template literals inside a piece, outermost only.
	Lits []*TemplateLiteral
}

// Pieces returns the number of pieces.
func (l *TemplateLiteral) Pieces() int { return len(l.Parts) / 2 }

// Const reports whether the literal has no piece; its value is then the
// cooked text of its only part.
func (l *TemplateLiteral) Const() bool { return len(l.Parts) <= 1 }

// Cooked returns the literal's text when it has no piece.
func (l *TemplateLiteral) Cooked() string {
	if len(l.Parts) == 0 {
		return ""
	}
	return l.Parts[0].Text
}

// scanTemplateLiteral returns the index of the backtick closing the
// template literal that opens at src[i], or -1 when it's unterminated (or a
// piece is). It doesn't allocate; parseTemplateLiteral reports the errors.
func scanTemplateLiteral(src string, i int) int {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '`':
			return j
		case '$':
			if j+1 < len(src) && src[j+1] == '{' {
				end := scanTLPiece(src, j+2)
				if end < 0 {
					return -1
				}
				j = end
			}
		}
	}
	return -1
}

// scanTLPiece returns the index of the '}' that closes the piece whose code
// starts at src[j] (right after "${"), or -1: Go-aware like scanBraced,
// with nested template literals.
func scanTLPiece(src string, j int) int {
	depth := 1
	for i := j; i < len(src); i++ {
		switch c := src[i]; c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		case '"', '\'':
			i = skipQuoted(src, i, c)
			if i < 0 {
				return -1
			}
		case '`':
			i = scanTemplateLiteral(src, i)
			if i < 0 {
				return -1
			}
		case '/':
			if i+1 >= len(src) {
				return -1
			}
			switch src[i+1] {
			case '/':
				k := strings.IndexByte(src[i+2:], '\n')
				if k < 0 {
					return -1
				}
				i += 2 + k
			case '*':
				k := strings.Index(src[i+2:], "*/")
				if k < 0 {
					return -1
				}
				i += 2 + k + 1
			}
		}
	}
	return -1
}

// findLiterals parses the template literals in the Go code src[sp], the
// outermost ones in order (nested ones are in their pieces), appending
// their lexical errors to diags. A literal unterminated in sp ends at
// sp.End.
func findLiterals(src string, sp Span, diags *[]Diagnostic) []*TemplateLiteral {
	if sp.Start < 0 || sp.End > len(src) || sp.Start >= sp.End || strings.IndexByte(src[sp.Start:sp.End], '`') < 0 {
		return nil
	}
	lits, _ := tlScanGo(src, sp.Start, sp.End, false, diags)
	return lits
}

// FindTemplateLiterals returns the outermost template literals of code, Go
// code from a template expression, with spans relative to code; ok is false
// when one of them has a lexical error (lyr fmt leaves such code alone).
func FindTemplateLiterals(code string) (lits []*TemplateLiteral, ok bool) {
	var diags []Diagnostic
	lits = findLiterals(code, Span{0, len(code)}, &diags)
	return lits, len(diags) == 0
}

// tlScanGo scans Go code in src[i:limit] for template literals. With piece
// set, it stops at the '}' that closes the piece (returning its index);
// otherwise it scans to limit. end is -1 when a piece isn't closed.
func tlScanGo(src string, i, limit int, piece bool, diags *[]Diagnostic) (lits []*TemplateLiteral, end int) {
	depth := 1
	for ; i < limit; i++ {
		switch c := src[i]; c {
		case '{':
			depth++
		case '}':
			depth--
			if piece && depth == 0 {
				return lits, i
			}
		case '"', '\'':
			k := skipQuoted(src[:limit], i, c)
			if k < 0 {
				return lits, -1
			}
			i = k
		case '`':
			l := parseTemplateLiteral(src, i, limit, diags)
			lits = append(lits, l)
			i = l.End - 1
		case '/':
			if i+1 >= limit {
				break
			}
			switch src[i+1] {
			case '/':
				k := strings.IndexByte(src[i+2:limit], '\n')
				if k < 0 {
					return lits, -1
				}
				i += 2 + k
			case '*':
				k := strings.Index(src[i+2:limit], "*/")
				if k < 0 {
					return lits, -1
				}
				i += 2 + k + 1
			}
		}
	}
	return lits, -1
}

// parseTemplateLiteral parses the literal opening at src[i] (a backtick),
// within src[:limit].
func parseTemplateLiteral(src string, i, limit int, diags *[]Diagnostic) *TemplateLiteral {
	l := &TemplateLiteral{Span: Span{i, limit}}
	textStart := i + 1
	j := textStart
	addText := func(end int) {
		l.Parts = append(l.Parts, TLPart{Span: Span{textStart, end}, Text: tlCook(src, textStart, end, diags)})
	}
	for j < limit {
		switch src[j] {
		case '\\':
			j += 2
			continue
		case '`':
			addText(j)
			l.End, l.Closed = j+1, true
			return l
		case '$':
			if j+1 < limit && src[j+1] == '{' {
				addText(j)
				open := j
				lits, end := tlScanGo(src, j+2, limit, true, diags)
				codeEnd := end
				if end < 0 {
					*diags = append(*diags, Diagnostic{SeverityError, Span{open, open + 2}, "unterminated ${ in template literal"})
					codeEnd = limit
				}
				cs, ce := j+2, codeEnd
				for cs < ce && isGoSpace(src[cs]) {
					cs++
				}
				for ce > cs && isGoSpace(src[ce-1]) {
					ce--
				}
				if n := len(lits); n > 0 && lits[n-1].End > ce {
					ce = lits[n-1].End // an unterminated nested literal keeps its text
				}
				if cs == ce && end >= 0 {
					*diags = append(*diags, Diagnostic{SeverityError, Span{open, end + 1}, "empty ${} in template literal"})
				}
				l.Parts = append(l.Parts, TLPart{Span: Span{cs, ce}, Piece: true, Text: src[cs:ce], Lits: lits})
				if end < 0 {
					textStart = limit
					addText(limit)
					return l
				}
				j = end + 1
				textStart = j
				continue
			}
		}
		j++
	}
	j = min(j, limit)
	addText(j)
	l.End = limit
	*diags = append(*diags, Diagnostic{SeverityError, Span{i, i + 1}, "unterminated template literal"})
	return l
}

func isGoSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// tlCook returns the cooked value of a static run src[start:end] of a
// template literal: JavaScript's escapes processed, CRLF and CR as LF. An
// unknown or malformed escape is reported (PLAN.md §2.10 (Q2)) and kept as
// written.
func tlCook(src string, start, end int, diags *[]Diagnostic) string {
	raw := src[start:end]
	if !strings.ContainsAny(raw, "\\\r") {
		return raw
	}
	var b strings.Builder
	errf := func(at, n int, format string, args ...any) {
		*diags = append(*diags, Diagnostic{SeverityError, Span{start + at, start + min(at+n, len(raw))}, fmt.Sprintf(format, args...)})
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\r' {
			b.WriteByte('\n')
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
			continue
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(raw) {
			errf(i, 1, "unterminated escape in template literal")
			b.WriteByte(c)
			continue
		}
		e := raw[i+1]
		n := 2 // the escape's length
		switch e {
		case '\\', '`', '$', '\'', '"':
			b.WriteByte(e)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '\n':
		case '\r':
			if i+2 < len(raw) && raw[i+2] == '\n' {
				n = 3
			}
		case '0':
			if i+2 < len(raw) && '0' <= raw[i+2] && raw[i+2] <= '9' {
				errf(i, 3, `octal escape \0%c in template literal; write \x or \u escapes`, raw[i+2])
				n = 3
				break
			}
			b.WriteByte(0)
		case 'x':
			if v, ok := tlHex(raw, i+2, 2); ok {
				b.WriteRune(rune(v))
				n = 4
			} else {
				errf(i, 2, `invalid \x escape in template literal: want two hex digits, \xHH`)
			}
		case 'u':
			r, k, ok := tlUnicode(raw, i)
			if !ok {
				errf(i, 2, `invalid \u escape in template literal: want \uHHHH or \u{H…}`)
				break
			}
			b.WriteRune(r)
			n = k
		default:
			r, size := utf8.DecodeRuneInString(raw[i+1:])
			n = 1 + size
			hint := "\\\\" + string(r)
			errf(i, n, "unknown escape \\%c in template literal; write %s for a backslash, or move a Go raw string to the frontmatter", r, hint)
		}
		i += n - 1
	}
	return b.String()
}

// tlHex parses n hex digits at s[i:].
func tlHex(s string, i, n int) (uint64, bool) {
	if i+n > len(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(s[i:i+n], 16, 32)
	return v, err == nil && !strings.ContainsAny(s[i:i+n], "+-_")
}

// tlUnicode parses the \u escape at s[i] (the backslash): \uHHHH (a
// surrogate pair as two escapes) or \u{H…}. It returns the rune and the
// escape's length.
func tlUnicode(s string, i int) (rune, int, bool) {
	if i+2 < len(s) && s[i+2] == '{' {
		end := strings.IndexByte(s[i+3:], '}')
		if end <= 0 || end > 8 {
			return 0, 0, false
		}
		v, ok := tlHex(s, i+3, end)
		if !ok || v > utf8.MaxRune || 0xD800 <= v && v < 0xE000 {
			return 0, 0, false
		}
		return rune(v), 3 + end + 1, true
	}
	v, ok := tlHex(s, i+2, 4)
	if !ok {
		return 0, 0, false
	}
	if 0xD800 <= v && v < 0xDC00 { // a high surrogate: needs its low half
		if i+12 <= len(s) && s[i+6] == '\\' && s[i+7] == 'u' {
			if lo, ok := tlHex(s, i+8, 4); ok && 0xDC00 <= lo && lo < 0xE000 {
				return rune(0x10000 + (v-0xD800)<<10 + (lo - 0xDC00)), 12, true
			}
		}
		return 0, 0, false
	}
	if 0xDC00 <= v && v < 0xE000 {
		return 0, 0, false
	}
	return rune(v), 6, true
}

// tlPlaceholder returns src[sp] with each template literal replaced by text
// of the same length that go/parser reads as an expression with the same
// pieces at the same offsets: `a ${x} b ${y}` becomes "_(   x,     y )"
// (the blank identifier as a callee, so nothing resolves to it), and a
// literal without pieces a raw string of spaces. Offsets in the result are
// offsets in src[sp], so the analyzer's maps stay exact.
func tlPlaceholder(src string, sp Span) string {
	text := src[sp.Start:sp.End]
	if strings.IndexByte(text, '`') < 0 {
		return text
	}
	var diags []Diagnostic
	lits := findLiterals(src, sp, &diags)
	if len(lits) == 0 {
		return text
	}
	b := []byte(text)
	for _, l := range lits {
		tlBlank(b, sp.Start, l)
	}
	return string(b)
}

// tlPlaceholderText is tlPlaceholder over a whole piece of Go code.
func tlPlaceholderText(code string) string { return tlPlaceholder(code, Span{0, len(code)}) }

// tlBlank writes l's placeholder into b, which holds src from offset base.
func tlBlank(b []byte, base int, l *TemplateLiteral) {
	blank := func(from, to int) {
		for k := max(from, base); k < to && k-base < len(b); k++ {
			b[k-base] = ' '
		}
	}
	if l.Const() {
		blank(l.Start+1, l.End)
		if l.Closed {
			b[l.End-1-base] = '`'
		}
		return
	}
	prev := l.Start
	for k, p := range l.Parts {
		if !p.Piece {
			continue
		}
		blank(prev, p.Start)
		if k == 1 {
			b[prev-base], b[prev+1-base] = '_', '('
		} else {
			b[prev-base] = ','
		}
		for _, n := range p.Lits {
			tlBlank(b, base, n)
		}
		prev = p.End
	}
	blank(prev, l.End)
	switch {
	case l.Closed:
		b[l.End-1-base] = ')' // the call ends where the literal does
	case prev < l.End:
		b[prev-base] = ')'
	}
}

// wholeLiteral returns the template literal that is the whole of code (only
// whitespace around it, which GoCode already trims), or nil.
func wholeLiteral(src string, code GoCode) *TemplateLiteral {
	if code.Text == "" || code.Text[0] != '`' {
		return nil
	}
	var diags []Diagnostic
	lits := findLiterals(src, code.Span, &diags)
	if len(lits) == 1 && lits[0].Span == code.Span && lits[0].Closed {
		return lits[0]
	}
	return nil
}

// checkTemplateLiterals reports the lexical errors of the template literals
// in every Go region of the template.
func checkTemplateLiterals(t *Template, p *parser) {
	if t == nil {
		return
	}
	Walk(t, func(n Node) bool {
		for _, c := range cgTemplateCodes(n) {
			findLiterals(p.src, c.Span, &p.diags)
		}
		return true
	})
}
