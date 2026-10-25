package compiler

import "strings"

// SplitFrontmatter splits src into its frontmatter and its template. The
// frontmatter is optional: it is present when the file's first line is "---"
// (trailing spaces, tabs and a \r allowed), and runs to the next such line.
// The template is everything after the closing line's line break, returned as
// a span of src, so template offsets stay offsets into the whole file.
//
// A missing closing line is an error at the opening one; the frontmatter then
// runs to the end of the file and the template is empty.
func SplitFrontmatter(src string) (fm Frontmatter, template Span, diags []Diagnostic) {
	first, next := line(src, 0)
	if !isDelimiter(src[first.Start:first.End]) {
		return Frontmatter{}, Span{0, len(src)}, nil
	}
	fm = Frontmatter{Present: true, Open: first}
	codeStart := next
	for pos := next; pos < len(src); {
		l, after := line(src, pos)
		if isDelimiter(src[l.Start:l.End]) {
			fm.Closed = true
			fm.Close = l
			fm.Code = GoCode{Span{codeStart, l.Start}, src[codeStart:l.Start]}
			fm.Span = Span{0, l.End}
			return fm, Span{after, len(src)}, nil
		}
		pos = after
	}
	codeStart = min(codeStart, len(src))
	fm.Code = GoCode{Span{codeStart, len(src)}, src[codeStart:]}
	fm.Close = Span{len(src), len(src)}
	fm.Span = Span{0, len(src)}
	diags = append(diags, Diagnostic{SeverityError, first,
		"frontmatter isn't closed; add a --- line after it"})
	return fm, Span{len(src), len(src)}, diags
}

// line returns the span of the line starting at pos, without its line break,
// and the offset of the next line.
func line(src string, pos int) (Span, int) {
	i := strings.IndexByte(src[pos:], '\n')
	if i < 0 {
		return Span{pos, len(src)}, len(src)
	}
	return Span{pos, pos + i}, pos + i + 1
}

func isDelimiter(l string) bool {
	return strings.TrimRight(l, " \t\r") == "---"
}
