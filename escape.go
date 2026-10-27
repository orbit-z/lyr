package orbit

import "bytes"

// HTML escaping, spelled as Astro spells it (astro/dist/runtime/server:
// escape.js's escapeHTML, the html-escaper package, for text, and
// render/util.js's toAttributeString for attribute values).
//
// Text content (textTable):
//
//	&     → &amp;    starts a character reference
//	<, >  → &lt;, &gt;   start or end a tag
//	"     → &quot;
//	'     → &#39;
//
// Double-quoted attribute values (attrTable): & → &amp; and " → &quot;, as
// in Astro, plus < → &lt; and > → &gt;, which Astro leaves raw (an Orbit
// safety choice: a value can't look like markup to a tool that doesn't
// parse attributes). ' is left as is: it can't end a double-quoted value.
// orbit.JSON's single-quoted values have their own escaper (json.go).
//
// In both, NUL → U+FFFD, as html/template does: the HTML parser drops or
// replaces NUL, so the output is the same in every context.
//
// Everything else, including non-ASCII and invalid UTF-8, is copied as is.
// '+' isn't escaped (html/template does it to defeat UTF-7 charset
// sniffing, which can't happen with the charset=utf-8 Orbit sends by
// default; it would also make fmt.Sprint output, the Expr fallback, differ
// from the typed writes: a named float would print 1e&#43;06 where Float
// prints 1e+06).
//
// The compiler pre-escapes constants at build time with the same tables
// (internal/compiler/escape.go; a test keeps the copies identical).
//
// The fast path scans for the first byte that needs escaping and, if there
// is none, writes the original string with no allocation and no copy beyond
// the buffer write.

// escTable maps each byte to its replacement, "" if it's written as is.
type escTable struct {
	repl [256]string
	// needs is repl as a compact lookup for the scan loop.
	needs [256]bool
}

func newEscTable(m map[byte]string) *escTable {
	t := &escTable{}
	for c, r := range m {
		t.repl[c] = r
		t.needs[c] = true
	}
	return t
}

var (
	textTable = newEscTable(map[byte]string{0: "�", '"': "&quot;", '&': "&amp;", '\'': "&#39;", '<': "&lt;", '>': "&gt;"})
	attrTable = newEscTable(map[byte]string{0: "�", '"': "&quot;", '&': "&amp;", '<': "&lt;", '>': "&gt;"})
)

// firstEsc returns the index of the first byte of s that t escapes, or -1
// if there is none.
func firstEsc[T ~string | ~[]byte](t *escTable, s T) int {
	for i := 0; i < len(s); i++ {
		if t.needs[s[i]] {
			return i
		}
	}
	return -1
}

// escapeString writes s to buf, escaped for text content.
func escapeString(buf *bytes.Buffer, s string) { escapeStringWith(textTable, buf, s) }

// escapeBytes writes b to buf, escaped for text content.
func escapeBytes(buf *bytes.Buffer, b []byte) { escapeBytesWith(textTable, buf, b) }

// escapeAttrString writes s to buf, escaped for a double-quoted attribute
// value.
func escapeAttrString(buf *bytes.Buffer, s string) { escapeStringWith(attrTable, buf, s) }

// escapeAttrBytes writes b to buf, escaped for a double-quoted attribute
// value.
func escapeAttrBytes(buf *bytes.Buffer, b []byte) { escapeBytesWith(attrTable, buf, b) }

func escapeStringWith(t *escTable, buf *bytes.Buffer, s string) {
	i := firstEsc(t, s)
	if i < 0 {
		buf.WriteString(s)
		return
	}
	escapeSlow(t, buf, s, i)
}

func escapeBytesWith(t *escTable, buf *bytes.Buffer, b []byte) {
	i := firstEsc(t, b)
	if i < 0 {
		buf.Write(b)
		return
	}
	escapeSlow(t, buf, b, i)
}

// escapeSlow escapes s into buf with t, given i, the index of the first
// byte that needs escaping.
func escapeSlow[T ~string | ~[]byte](t *escTable, buf *bytes.Buffer, s T, i int) {
	buf.Grow(len(s) + len(s)/8 + 8)
	b := buf.AvailableBuffer()
	last := 0
	for ; i < len(s); i++ {
		// Test the 1-byte needs first: loading the 16-byte repl entry for
		// every byte is measurably slower.
		c := s[i]
		if !t.needs[c] {
			continue
		}
		b = append(b, s[last:i]...)
		b = append(b, t.repl[c]...)
		last = i + 1
	}
	b = append(b, s[last:]...)
	buf.Write(b)
}
