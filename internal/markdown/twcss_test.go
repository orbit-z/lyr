package markdown

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

// TestLyrLexerApply: Tailwind's @apply in a <style> is a keyword, with
// no error tokens, and the tokens cover the text.
func TestLyrLexerApply(t *testing.T) {
	src := "<p class=\"t\">x</p>\n<style>\n  @reference \"../styles/global.css\";\n  .t { color: red; @apply text-lg hover:text-ember; }\n  .u {\n    @apply p-2;\n  }\n</style>\n"
	_, toks := lyrTokens(t, src)
	var b strings.Builder
	applies := 0
	for _, tok := range toks {
		b.WriteString(tok.Value)
		if tok.Type == chroma.Error {
			t.Errorf("error token %q", tok.Value)
		}
		if tok.Value == "@apply" && tok.Type == chroma.Keyword {
			applies++
		}
	}
	if b.String() != src {
		t.Errorf("tokens don't cover the source:\n%q", b.String())
	}
	if applies != 2 {
		t.Errorf("%d @apply keywords, want 2: %v", applies, toks)
	}
}
