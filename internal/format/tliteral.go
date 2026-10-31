package format

// Template literals (PLAN.md §2.10, (Q6) a): in a template expression a
// backtick string is a JavaScript template literal, which go/format can't
// parse. A region holding literals is formatted with each literal swapped
// for a Go raw string of the same shape (spaces, its line breaks kept), and
// the literals are put back afterwards. A literal's static text is kept byte
// for byte; each piece is formatted as a Go expression, recursively for
// nested literals. A piece written over several lines is kept as written
// (the literal's line breaks are kept), and a literal with a piece that
// doesn't format is kept whole.

import (
	goformat "go/format"
	goscanner "go/scanner"
	gotoken "go/token"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/orbit-z/lyr/internal/compiler"
)

// tlRegion is a Go region's code with its template literals swapped for
// placeholders.
type tlRegion struct {
	texts   []string // each literal, formatted
	phs     []string // the placeholders in fmtCode
	fmtCode string   // placeholders shaped like the formatted literals (rune widths, for gofmt's alignment)
	errCode string   // placeholders shaped like the literals as written (byte offsets, for error positions)
}

// newTLRegion returns nil when code holds no template literal, and ok false
// when a literal has a lexical error (the parser reports it; the region is
// left as written).
func newTLRegion(code string) (t *tlRegion, ok bool) {
	if strings.IndexByte(code, '`') < 0 {
		return nil, true
	}
	lits, ok := compiler.FindTemplateLiterals(code)
	if !ok {
		return nil, false
	}
	if len(lits) == 0 {
		return nil, true
	}
	t = &tlRegion{}
	var fb, eb strings.Builder
	prev := 0
	for _, l := range lits {
		if !l.Closed {
			return nil, false
		}
		orig := code[l.Start:l.End]
		text := fmtLiteral(code, l)
		ph := tlPlaceholder(text, true)
		t.texts = append(t.texts, text)
		t.phs = append(t.phs, ph)
		fb.WriteString(code[prev:l.Start])
		fb.WriteString(ph)
		eb.WriteString(code[prev:l.Start])
		eb.WriteString(tlPlaceholder(orig, false))
		prev = l.End
	}
	fb.WriteString(code[prev:])
	eb.WriteString(code[prev:])
	t.fmtCode, t.errCode = fb.String(), eb.String()
	return t, true
}

// tlPlaceholder returns a raw string shaped like the literal text: a space
// per rune (or per byte), line breaks kept.
func tlPlaceholder(text string, byRune bool) string {
	inner := text[1 : len(text)-1]
	var b strings.Builder
	b.WriteByte('`')
	for i := 0; i < len(inner); {
		n := 1
		if byRune {
			_, n = utf8.DecodeRuneInString(inner[i:])
		}
		if inner[i] == '\n' {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
		i += n
	}
	b.WriteByte('`')
	return b.String()
}

// restore puts the formatted literals back into s, formatted Go code made
// from fmtCode; ok is false when the placeholders aren't all found, in order.
func (t *tlRegion) restore(s string) (string, bool) {
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(s))
	var sc goscanner.Scanner
	sc.Init(file, []byte(s), nil, goscanner.ScanComments)
	var b strings.Builder
	prev, k := 0, 0
	for {
		pos, tok, lit := sc.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok != gotoken.STRING || lit[0] != '`' {
			continue
		}
		if k >= len(t.phs) || lit != t.phs[k] {
			return "", false
		}
		off := file.Offset(pos)
		b.WriteString(s[prev:off])
		b.WriteString(t.texts[k])
		prev = off + len(lit)
		k++
	}
	if k != len(t.phs) {
		return "", false
	}
	b.WriteString(s[prev:])
	return b.String(), true
}

// fmtLiteral returns literal l of code, closed, with its pieces formatted.
func fmtLiteral(code string, l *compiler.TemplateLiteral) string {
	orig := code[l.Start:l.End]
	var b strings.Builder
	b.WriteByte('`')
	for k, p := range l.Parts {
		if !p.Piece {
			b.WriteString(code[p.Start:p.End])
			continue
		}
		// "${ … }", from the previous static part's end to the next one's start.
		raw := code[l.Parts[k-1].End:l.Parts[k+1].Start]
		if strings.IndexByte(raw, '\n') >= 0 {
			b.WriteString(raw)
			continue
		}
		f, ok := fmtPiece(code[p.Start:p.End])
		if !ok {
			return orig
		}
		b.WriteString("${")
		b.WriteString(f)
		b.WriteString("}")
	}
	b.WriteByte('`')
	return b.String()
}

// fmtPiece formats a one-line piece as a Go expression, or reports false
// when it doesn't format on one line with the same code.
func fmtPiece(code string) (string, bool) {
	t, ok := newTLRegion(code)
	if !ok {
		return "", false
	}
	ph := code
	if t != nil {
		ph = t.fmtCode
	}
	const prefix = "package p\n\nvar _ = "
	out, err := goformat.Source([]byte(prefix + ph + "\n"))
	if err != nil {
		return "", false
	}
	s, ok := strings.CutPrefix(string(out), prefix)
	if !ok {
		return "", false
	}
	s = strings.TrimRight(s, "\n")
	if strings.IndexByte(s, '\n') >= 0 {
		return "", false
	}
	if t != nil {
		if s, ok = t.restore(s); !ok {
			return "", false
		}
	}
	if !sameGo(tlCanon(code), tlCanon(s), false) {
		return "", false
	}
	return s, true
}

// tlCanon returns Go code standing for code's template literals, for the
// safety check: each literal is a call of its static parts, quoted as
// written, and its pieces.
func tlCanon(code string) string {
	if strings.IndexByte(code, '`') < 0 {
		return code
	}
	lits, _ := compiler.FindTemplateLiterals(code)
	var b strings.Builder
	prev := 0
	for _, l := range lits {
		b.WriteString(code[prev:l.Start])
		b.WriteString("_tl(")
		for k, p := range l.Parts {
			if k > 0 {
				b.WriteString(", ")
			}
			if p.Piece {
				b.WriteString("(" + tlCanon(code[p.Start:p.End]) + ")")
			} else {
				b.WriteString(strconv.Quote(code[p.Start:p.End]))
			}
		}
		b.WriteString(")")
		prev = l.End
	}
	b.WriteString(code[prev:])
	return b.String()
}
