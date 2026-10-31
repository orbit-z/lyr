package compiler

import (
	goscanner "go/scanner"
	gotoken "go/token"
	"strconv"
	"strings"
)

// cgOut is one generated file being written. It tracks its own line and
// column so line-directive resets name the true position of the next byte
// (PLAN.md §3.3), and records the source-map segments.
type cgOut struct {
	b      []byte
	line   int // 1-based line of the next byte
	lastNL int // index of the last '\n' in b, -1 if none
	depth  int // indentation of glue lines, in tabs

	base    string // bare basename used by resets: "decl.go" or "render.go"
	dirFile string // the .lyr file as directives name it
	src     string
	lines   *LineIndex
	segs    []Segment

	// raw is the pending static HTML, merged into one Raw call.
	raw strings.Builder

	// lit, when set, emits template Go code (at or after litFrom, the
	// template's start) that holds template literals.
	lit     func(Span) (Span, bool)
	litFrom int
}

func newCGOut(genPath, lyrPath, src string, lines *LineIndex) *cgOut {
	base := genPath
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return &cgOut{line: 1, lastNL: -1, base: base, dirFile: DirectiveFile(genPath, lyrPath), src: src, lines: lines}
}

// str writes s verbatim.
func (o *cgOut) str(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			o.line++
			o.lastNL = len(o.b) + i
		}
	}
	o.b = append(o.b, s...)
}

// col is the 1-based byte column of the next byte.
func (o *cgOut) col() int { return len(o.b) - o.lastNL }

// ind writes the indentation of a glue line.
func (o *cgOut) ind() {
	for range o.depth {
		o.b = append(o.b, '\t')
	}
}

// ln writes an indented glue line.
func (o *cgOut) ln(s string) {
	o.ind()
	o.str(s)
	o.str("\n")
}

// reset writes the inline reset /*line <base>:L:C*/, where L:C is the true
// position of the byte after it.
func (o *cgOut) reset() {
	prefix := "/*line " + o.base + ":" + strconv.Itoa(o.line) + ":"
	c0 := o.col()
	for d := 1; ; d++ {
		c := c0 + len(prefix) + d + len("*/")
		if s := strconv.Itoa(c); len(s) == d {
			o.str(prefix + s + "*/")
			return
		}
	}
}

func (o *cgOut) pos(off int) string {
	p := o.lines.Position(off)
	return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col)
}

// inline writes text framed by an inline directive pointing at s.Start and a
// reset, and records a segment of the given kind for it. It returns the Go
// span of text.
func (o *cgOut) inline(s Span, text string, kind SegmentKind) Span {
	o.str("/*line " + o.dirFile + ":" + o.pos(s.Start) + "*/")
	g := Span{len(o.b), len(o.b) + len(text)}
	o.str(text)
	o.segs = append(o.segs, Segment{Kind: kind, LyrStart: s.Start, LyrEnd: s.End, GoStart: g.Start, GoEnd: g.End})
	if endsInLineComment(text) {
		o.str("\n") // {x // c} would swallow the reset and the glue after it
	}
	o.reset()
	return g
}

// endsInLineComment reports whether Go code ends inside a // comment.
func endsInLineComment(code string) bool {
	if !strings.Contains(code, "//") {
		return false
	}
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(code))
	var s goscanner.Scanner
	s.Init(file, []byte(code), func(gotoken.Position, string) {}, goscanner.ScanComments)
	last := ""
	for {
		_, tok, lit := s.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok == gotoken.SEMICOLON && lit == "\n" {
			continue
		}
		last = ""
		if tok == gotoken.COMMENT {
			last = lit
		}
	}
	return strings.HasPrefix(last, "//")
}

// exact copies src[s] inline (kind=exact). Template Go code holding
// template literals (PLAN.md §2.10) goes through the lit hook, which
// rewrites them; the span returned then covers the rewritten code.
func (o *cgOut) exact(s Span) Span {
	if o.lit != nil && s.Start >= o.litFrom && strings.IndexByte(o.src[s.Start:s.End], '`') >= 0 {
		if g, ok := o.lit(s); ok {
			return g
		}
	}
	return o.inline(s, o.src[s.Start:s.End], SegExact)
}

// ownLine starts an own-line directive frame at the beginning of a line:
// //line <file>:L:C for lyr offset off. The framed code follows from
// column 1 of the next line; close it with endOwnLine.
func (o *cgOut) ownLine(off int) {
	o.str("//line " + o.dirFile + ":" + o.pos(off) + "\n")
}

// endOwnLine ends the current line and writes the own-line reset naming the
// true next line.
func (o *cgOut) endOwnLine() {
	o.str("\n")
	o.str("//line " + o.base + ":" + strconv.Itoa(o.line+1) + ":1\n")
}

// copySeg copies src[s] at the current position with an exact segment (the
// caller provides the directive).
func (o *cgOut) copySeg(s Span) {
	g := len(o.b)
	o.str(o.src[s.Start:s.End])
	o.segs = append(o.segs, Segment{Kind: SegExact, LyrStart: s.Start, LyrEnd: s.End, GoStart: g, GoEnd: len(o.b)})
}

// echo writes "_ = name" with an echo segment over name.
func (o *cgOut) echo(name string) {
	o.ind()
	o.str("_ = ")
	g := len(o.b)
	o.str(name)
	o.segs = append(o.segs, Segment{Kind: SegEcho, LyrStart: -1, LyrEnd: -1, GoStart: g, GoEnd: len(o.b)})
	o.str("\n")
}

// html queues static HTML for the next Raw call.
func (o *cgOut) html(s string) { o.raw.WriteString(s) }

// flush emits the pending static HTML as one lyr__w.Raw call.
func (o *cgOut) flush() {
	if o.raw.Len() == 0 {
		return
	}
	o.ln(NameW + ".Raw(" + strconv.Quote(o.raw.String()) + ")")
	o.raw.Reset()
}
