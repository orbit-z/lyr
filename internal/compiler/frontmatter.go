package compiler

import "strings"

// SplitFrontmatter splits src into its frontmatter and its template. The
// frontmatter is optional: it is present when the file's first line is "---"
// (trailing spaces, tabs and a \r allowed), and runs to the next such line.
// The template is everything after the closing line's line break, returned as
// a span of src, so template offsets stay offsets into the whole file.
//
// A leading UTF-8 byte order mark is skipped, as Go's scanner skips it: it
// belongs to neither part, so the frontmatter is still found and the BOM
// isn't rendered.
//
// A missing closing line is an error at the opening one; the frontmatter then
// runs to the end of the file and the template is empty.
func SplitFrontmatter(src string) (fm Frontmatter, template Span, diags []Diagnostic) {
	start := 0
	if strings.HasPrefix(src, bom) {
		start = len(bom)
	}
	first, next := line(src, start)
	if !isDelimiter(src[first.Start:first.End]) {
		return Frontmatter{}, Span{start, len(src)}, nil
	}
	fm = Frontmatter{Present: true, Open: first}
	codeStart := next
	for pos := next; pos < len(src); {
		l, after := line(src, pos)
		if isDelimiter(src[l.Start:l.End]) {
			fm.Closed = true
			fm.Close = l
			fm.Code = GoCode{Span{codeStart, l.Start}, src[codeStart:l.Start]}
			fm.Span = Span{start, l.End}
			return fm, Span{after, len(src)}, nil
		}
		pos = after
	}
	codeStart = min(codeStart, len(src))
	fm.Code = GoCode{Span{codeStart, len(src)}, src[codeStart:]}
	fm.Close = Span{len(src), len(src)}
	fm.Span = Span{start, len(src)}
	diags = append(diags, Diagnostic{SeverityError, first,
		"frontmatter isn't closed; add a --- line after it"})
	return fm, Span{len(src), len(src)}, diags
}

// bom is the UTF-8 encoding of U+FEFF, the byte order mark.
const bom = "\uFEFF"

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
