package compiler

// Support for the formatter (internal/format, PLAN.md §6.1): the syntax
// errors of a parse, and the frontmatter split into chunks of hoisted
// declarations and body statements, with the same scanner as Analyze.

import gotoken "go/token"

// SyntaxErrors returns the errors raised by the frontmatter split, the lexer
// and the parser, without the structural checks that follow them (directive
// placement, Fragment attributes, …). These are the errors that stop
// orbit fmt; the others are semantic, like a Go type error.
func (f *File) SyntaxErrors() []Diagnostic { return f.syntax }

// FrontmatterChunk is a run of consecutive depth-0 frontmatter statements of
// the same kind, hoisted declarations or body statements, with the comments
// that belong to them (PLAN.md §6.1).
type FrontmatterChunk struct {
	// Hoisted is true for import, type, const and named func declarations.
	Hoisted bool
	// Span is the chunk's text: from its first comment or token to the end
	// of its last one, as offsets into the code passed in.
	Span Span
	// TrailingComment is true when the chunk ends with a comment on a line
	// after its last statement's, which a formatter keeps apart from the
	// next chunk with a blank line, so it stays in this chunk.
	TrailingComment bool
}

// FrontmatterChunks splits frontmatter code into chunks. A comment inside a
// statement or on the line where a statement ends belongs to it; a comment
// group right above a statement (go/parser's doc rule) belongs to that
// statement; any other comment belongs to the statement before it, or to the
// first statement if there is none. Code with only comments is one hoisted
// chunk; empty code has none.
func FrontmatterChunks(code string) []FrontmatterChunk {
	toks := anScan(code, 0)
	stmts := anSplit(toks)
	lines := NewLineIndex(code)
	if len(stmts) == 0 {
		var sp Span
		have := false
		for _, t := range toks {
			if t.tok == gotoken.COMMENT {
				if !have {
					sp.Start, have = t.pos, true
				}
				sp.End = t.end
			}
		}
		if !have {
			return nil
		}
		return []FrontmatterChunk{{Hoisted: true, Span: sp}}
	}
	// Each statement's extent, grown by the comments it owns.
	ext := make([]Span, len(stmts))
	for i, s := range stmts {
		ext[i] = s.span
	}
	docs := make([]Span, len(stmts))
	for i, s := range stmts {
		docs[i] = anDoc(toks, s.first, lines)
	}
	si := 0 // the first statement that starts after the current token
	for ti, t := range toks {
		for si < len(stmts) && stmts[si].first <= ti {
			si++
		}
		if t.tok != gotoken.COMMENT {
			continue
		}
		owner := si - 1
		switch {
		case si > 0 && ti <= stmts[si-1].last:
			// Inside the previous statement.
		case si < len(stmts) && docs[si].End > docs[si].Start && t.pos >= docs[si].Start && t.end <= docs[si].End:
			owner = si
		case owner < 0:
			owner = 0
		}
		ext[owner].Start = min(ext[owner].Start, t.pos)
		ext[owner].End = max(ext[owner].End, t.end)
	}
	var out []FrontmatterChunk
	for i, s := range stmts {
		hoisted := s.kind != anBody
		trailing := ext[i].End > s.span.End && lines.Position(ext[i].End).Line > lines.Position(s.span.End).Line
		if len(out) > 0 && out[len(out)-1].Hoisted == hoisted {
			out[len(out)-1].Span.End = max(out[len(out)-1].Span.End, ext[i].End)
			out[len(out)-1].TrailingComment = trailing
			continue
		}
		out = append(out, FrontmatterChunk{Hoisted: hoisted, Span: ext[i], TrailingComment: trailing})
	}
	return out
}
