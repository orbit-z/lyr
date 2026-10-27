package compiler

import (
	"bytes"
	"testing"

	"github.com/orbit-z/orbit"
)

// TestEscapersMatchRuntime: the build-time escapers write every byte as
// the runtime does, for text (Writer.String) and for a double-quoted
// attribute value (Writer.AttrString, between the quotes).
func TestEscapersMatchRuntime(t *testing.T) {
	var all []byte
	for c := range 256 {
		all = append(all, byte(c))
	}
	for _, s := range []string{string(all), `a&b<c>d"e'f` + "\x00", "plain"} {
		var buf bytes.Buffer
		w := orbit.NewWriter(&buf)
		w.String(s)
		if got := textEscaper.Replace(s); got != buf.String() {
			t.Errorf("textEscaper(%q) = %q, runtime %q", s, got, buf.String())
		}
		buf.Reset()
		w.AttrString("a", s)
		want := buf.String()
		want = want[len(` a="`) : len(want)-1]
		if got := attrValueEscaper.Replace(s); got != want {
			t.Errorf("attrValueEscaper(%q) = %q, runtime %q", s, got, want)
		}
	}
	// Astro's spelling (html-escaper; render/util.js toAttributeString),
	// with < and > also escaped in attribute values.
	if got := textEscaper.Replace(`&<>"'`); got != "&amp;&lt;&gt;&quot;&#39;" {
		t.Errorf("text: %s", got)
	}
	if got := attrValueEscaper.Replace(`&<>"'`); got != "&amp;&lt;&gt;&quot;'" {
		t.Errorf("attribute: %s", got)
	}
}
