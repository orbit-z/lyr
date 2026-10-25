package orbit

import "bytes"

// HTML escaping, used for both text content and quoted attribute values.
//
// It follows html/template, which uses the same table (htmlEscaper) for text
// and for quoted attribute values:
//
//	&     → &amp;   starts a character reference
//	<, >  → &lt;, &gt;   start or end a tag
//	"     → &#34;   ends a double-quoted attribute value
//	'     → &#39;   ends a single-quoted attribute value
//	NUL   → U+FFFD  the HTML parser drops or replaces NUL; html/template
//	                replaces it so output is the same in every context
//
// Everything else, including non-ASCII and invalid UTF-8, is copied as-is.
//
// Unlike html/template, '+' isn't escaped. html/template does it to defeat
// UTF-7 charset sniffing, which can't happen with the charset=utf-8 that
// Orbit sends by default. Escaping it would also make fmt.Sprint output (the Expr
// fallback) differ from the typed writes: a named float would print
// 1e&#43;06 where Float prints 1e+06.
//
// The fast path scans for the first byte that needs escaping and, if there
// is none, writes the original string with no allocation and no copy beyond
// the buffer write.

// escTable maps each byte to its replacement, or "" if it's written as-is.
var escTable = [256]string{
	0:    "\uFFFD",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// needsEsc is escTable as a compact lookup for the scan loop.
var needsEsc = func() (t [256]bool) {
	for i, r := range escTable {
		t[i] = r != ""
	}
	return t
}()

// firstEsc returns the index of the first byte of s that needs escaping, or
// -1 if there is none.
func firstEsc[T ~string | ~[]byte](s T) int {
	for i := 0; i < len(s); i++ {
		if needsEsc[s[i]] {
			return i
		}
	}
	return -1
}

// escapeString writes s to buf, HTML-escaped.
func escapeString(buf *bytes.Buffer, s string) {
	i := firstEsc(s)
	if i < 0 {
		buf.WriteString(s)
		return
	}
	escapeSlow(buf, s, i)
}

// escapeBytes writes b to buf, HTML-escaped.
func escapeBytes(buf *bytes.Buffer, b []byte) {
	i := firstEsc(b)
	if i < 0 {
		buf.Write(b)
		return
	}
	escapeSlow(buf, b, i)
}

// escapeSlow escapes s into buf, given i, the index of the first byte that
// needs escaping.
func escapeSlow[T ~string | ~[]byte](buf *bytes.Buffer, s T, i int) {
	buf.Grow(len(s) + len(s)/8 + 8)
	b := buf.AvailableBuffer()
	last := 0
	for ; i < len(s); i++ {
		r := escTable[s[i]]
		if r == "" {
			continue
		}
		b = append(b, s[last:i]...)
		b = append(b, r...)
		last = i + 1
	}
	b = append(b, s[last:]...)
	buf.Write(b)
}
