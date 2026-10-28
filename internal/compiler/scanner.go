// Package compiler turns .orbit files into Go code.
package compiler

import "strings"

// scanBraced finds the end of a {…} construct in a template. src[start] must
// be the opening '{'. It returns the index of the matching '}', or -1 if the
// construct is unterminated (or if src[start] isn't '{').
//
// The scanner is Go-aware: braces inside Go strings ("…" with escapes), raw
// strings, template literals (`a ${x} b`, which may span lines and nest,
// PLAN.md §2.10), runes ('{', or a quote escaped with
// a backslash) and comments (// up to the newline, /* … */) don't count, so
// {fmt.Sprintf("{%d}", n)} and
// {posts.Filter(func(p Post) bool { return p.Draft })} end at the right '}'.
// Nested braces (composite, map and func literals) are counted, and the '}'
// that brings the depth back to 0 ends the construct.
//
// Only braces decide the end; parentheses and brackets are not tracked. In a
// valid Go expression every '}' outside strings and comments matches a '{'
// inside the expression, so this finds the same end as a full bracket stack.
// On broken input like {f(x} it ends at the first unmatched '}' instead of
// running on to some later ')', which keeps errors local.
//
// Unterminated cases return -1: a string or rune with a newline (escaped or
// not) before its closing quote, as in Go; a raw string or block comment
// without its closing delimiter; a line comment that reaches the end of src
// (the '}' must follow on a later line); and missing closing braces.
//
// It's a small hand-written lexer rather than go/scanner: Go's lexical
// grammar for this question needs only the five cases above, and go/scanner
// would need a token.File and a scanner.Scanner per call (allocations) and
// does work we don't need (identifiers, numbers, semicolon insertion). The
// fuzz test checks it against go/scanner.
func scanBraced(src string, start int) int {
	if start < 0 || start >= len(src) || src[start] != '{' {
		return -1
	}
	depth := 0
	for i := start; i < len(src); i++ {
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
			// A template literal (PLAN.md §2.10): its pieces may hold
			// braces, strings and nested literals.
			j := scanTemplateLiteral(src, i)
			if j < 0 {
				return -1
			}
			i = j // the closing '`'
		case '/':
			if i+1 >= len(src) {
				return -1
			}
			switch src[i+1] {
			case '/':
				j := strings.IndexByte(src[i+2:], '\n')
				if j < 0 {
					return -1
				}
				i += 2 + j // the '\n'
			case '*':
				j := strings.Index(src[i+2:], "*/")
				if j < 0 {
					return -1
				}
				i += 2 + j + 1 // the closing '/'
			}
		}
	}
	return -1
}

// bracedLiteralFails reports whether the {…} construct at start is
// unterminated because of a template literal (or one of its pieces), whose
// own error is reported instead (checkTemplateLiterals). That needs the
// literal to open on the line of the "{": the construct is cut at the end
// of that line, and a literal after it is never seen. Only called after
// scanBraced failed.
func bracedLiteralFails(src string, start int) bool {
	for i := start; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '\'':
			if i = skipQuoted(src, i, c); i < 0 {
				return false
			}
		case '`':
			if strings.IndexByte(src[start:i], '\n') >= 0 {
				return false
			}
			if i = scanTemplateLiteral(src, i); i < 0 {
				return true
			}
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				if j := strings.IndexByte(src[i+2:], '\n'); j >= 0 {
					i += 2 + j
				} else {
					return false
				}
			} else if i+1 < len(src) && src[i+1] == '*' {
				if j := strings.Index(src[i+2:], "*/"); j >= 0 {
					i += 2 + j + 1
				} else {
					return false
				}
			}
		}
	}
	return false
}

// skipQuoted returns the index of the quote q that closes the string or rune
// literal opening at src[i], or -1 if a newline or the end of src comes first.
func skipQuoted(src string, i int, q byte) int {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++ // skip the escaped byte; \" and \' don't close
			if j < len(src) && src[j] == '\n' {
				return -1
			}
		case q:
			return j
		case '\n':
			return -1
		}
	}
	return -1
}

// scanBracedTolerant is scanBraced for tolerant (LSP) mode, PLAN.md §3.3: an
// unterminated construct ends at the end of the line holding its '{'. It
// returns the index of the matching '}' when there is one; otherwise the
// index of the first '\n' after start, or len(src) on the last line. The
// construct's body is src[start+1:end] in every case, and the caller tells
// the cases apart with end < len(src) && src[end] == '}' (it then resumes
// after the '}', and otherwise at end, keeping the newline as text). It
// returns -1 only if src[start] isn't '{'.
func scanBracedTolerant(src string, start int) int {
	if end := scanBraced(src, start); end >= 0 {
		return end
	}
	if start < 0 || start >= len(src) || src[start] != '{' {
		return -1
	}
	if j := strings.IndexByte(src[start:], '\n'); j >= 0 {
		return start + j
	}
	return len(src)
}
