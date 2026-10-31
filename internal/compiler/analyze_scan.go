package compiler

// Frontmatter tokenizing and the depth-0 statement split (PLAN.md §2.2).

import (
	goscanner "go/scanner"
	gotoken "go/token"
	"strings"
	"unicode/utf8"
)

// anTok is one go/scanner token, with offsets into the whole .lyr file.
type anTok struct {
	tok      gotoken.Token
	lit      string
	pos, end int
}

// anScan tokenizes text, which starts at offset base of the .lyr file.
// Comments are kept; scan errors are ignored (Go reports them).
func anScan(text string, base int) []anTok {
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(text))
	var s goscanner.Scanner
	s.Init(file, []byte(text), func(gotoken.Position, string) {}, goscanner.ScanComments)
	var toks []anTok
	for {
		p, tok, lit := s.Scan()
		off := file.Offset(p)
		end := anTokEnd(text, off, tok, lit)
		toks = append(toks, anTok{tok, lit, base + off, base + end})
		if tok == gotoken.EOF {
			return toks
		}
	}
}

// anTokEnd returns the end offset of the token at off in text. The
// scanner's literals drop carriage returns from raw strings and comments, so
// those are measured on the source.
func anTokEnd(text string, off int, tok gotoken.Token, lit string) int {
	end := off
	switch {
	case tok == gotoken.EOF:
	case tok == gotoken.SEMICOLON:
		if lit == ";" {
			end = off + 1
		}
	case tok == gotoken.COMMENT:
		if strings.HasPrefix(text[off:], "//") {
			end = len(text)
			if i := strings.IndexByte(text[off:], '\n'); i >= 0 {
				end = off + i
			}
			end = off + len(strings.TrimRight(text[off:end], "\r"))
		} else if i := strings.Index(text[min(off+2, len(text)):], "*/"); i >= 0 {
			end = off + 2 + i + 2
		} else {
			end = len(text)
		}
	case tok == gotoken.STRING && off < len(text) && text[off] == '`':
		end = len(text)
		if i := strings.IndexByte(text[off+1:], '`'); i >= 0 {
			end = off + 1 + i + 1
		}
	case tok == gotoken.ILLEGAL:
		// lit is the offending rune, U+FFFD for an invalid byte.
		_, n := utf8.DecodeRuneInString(text[min(off, len(text)):])
		end = off + max(n, 1)
	case tok.IsLiteral():
		end = off + len(lit)
	default:
		end = off + len(tok.String())
	}
	return max(off, min(end, len(text)))
}

// anStmtKind classifies a depth-0 statement of the frontmatter.
type anStmtKind uint8

const (
	anBody anStmtKind = iota
	anImport
	anType
	anConst
	anFunc
)

// anRange is one depth-0 statement: token indexes of its first and last
// non-comment tokens.
type anRange struct {
	kind        anStmtKind
	first, last int
	span        Span
}

// anSplit splits the tokens into depth-0 statements. A statement ends at a
// depth-0 semicolon, except an explicit one inside an if/for/switch/select
// header (before the block's brace); this is exact for hoisted declarations,
// and body statements are re-split by go/parser when the body parses.
func anSplit(toks []anTok) []anRange {
	var out []anRange
	depth, first, last := 0, -1, -1
	inHeader := false
	flush := func() {
		if first >= 0 {
			r := anRange{first: first, last: last, span: Span{toks[first].pos, toks[last].end}}
			r.kind = anClassify(toks, first, last)
			out = append(out, r)
		}
		first, last, inHeader = -1, -1, false
	}
	for i, t := range toks {
		switch t.tok {
		case gotoken.COMMENT:
			continue
		case gotoken.EOF:
			flush()
			return out
		case gotoken.SEMICOLON:
			if depth == 0 && (t.lit != ";" || !inHeader) {
				flush()
				continue
			}
			if first < 0 {
				continue
			}
		case gotoken.LPAREN, gotoken.LBRACK:
			depth++
		case gotoken.LBRACE:
			if depth == 0 {
				inHeader = false
			}
			depth++
		case gotoken.RPAREN, gotoken.RBRACK, gotoken.RBRACE:
			if depth > 0 {
				depth--
			}
		case gotoken.IF, gotoken.FOR, gotoken.SWITCH, gotoken.SELECT:
			if depth == 0 {
				inHeader = true
			}
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	flush()
	return out
}

// anClassify decides whether a statement is hoisted: import, type, const, a
// named func, or a method (func, a receiver list, a name and "("). An IIFE
// stays in the body.
func anClassify(toks []anTok, first, last int) anStmtKind {
	next := func(i int) int {
		for i++; i <= last; i++ {
			if toks[i].tok != gotoken.COMMENT {
				return i
			}
		}
		return -1
	}
	switch toks[first].tok {
	case gotoken.IMPORT:
		return anImport
	case gotoken.TYPE:
		return anType
	case gotoken.CONST:
		return anConst
	case gotoken.FUNC:
		i := next(first)
		if i < 0 {
			return anBody
		}
		if toks[i].tok == gotoken.IDENT {
			return anFunc
		}
		if toks[i].tok != gotoken.LPAREN {
			return anBody
		}
		depth := 0
		for ; i >= 0; i = next(i) {
			switch toks[i].tok {
			case gotoken.LPAREN, gotoken.LBRACK, gotoken.LBRACE:
				depth++
			case gotoken.RPAREN, gotoken.RBRACK, gotoken.RBRACE:
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if i < 0 {
			return anBody
		}
		if n := next(i); n >= 0 && toks[n].tok == gotoken.IDENT {
			if p := next(n); p >= 0 && toks[p].tok == gotoken.LPAREN {
				return anFunc
			}
		}
	}
	return anBody
}

// anDoc returns the doc comment group right above the statement starting at
// token first, following go/parser: comments on the previous token's line
// are its line comment, groups are split by blank lines, and the last group
// is the doc only if it ends on the line before the statement.
func anDoc(toks []anTok, first int, lines *LineIndex) Span {
	line := func(off int) int { return lines.Position(off).Line }
	start := first
	for start > 0 {
		t := toks[start-1]
		if t.tok == gotoken.COMMENT || t.tok == gotoken.SEMICOLON && t.lit != ";" {
			start--
			continue
		}
		break
	}
	var comments []anTok
	for i := start; i < first; i++ {
		if toks[i].tok == gotoken.COMMENT {
			comments = append(comments, toks[i])
		}
	}
	if len(comments) == 0 {
		return Span{}
	}
	i := 0
	if start > 0 {
		// The line comment of the previous token, then comments on the same
		// line as it ended.
		endline := line(toks[start-1].end)
		for i < len(comments) && line(comments[i].pos) <= endline {
			endline = line(comments[i].end)
			i++
		}
	}
	var group Span
	have := false
	endline := 0
	for ; i < len(comments); i++ {
		c := comments[i]
		if !have || line(c.pos) > endline+1 {
			group, have = Span{c.pos, c.end}, true
		} else {
			group.End = c.end
		}
		endline = line(c.end)
	}
	if !have || endline+1 != line(toks[first].pos) {
		return Span{}
	}
	return group
}
