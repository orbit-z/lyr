package markdown

import (
	"regexp"

	"github.com/alecthomas/chroma/v2"
)

// twCSS is Chroma's CSS lexer for <style> content in ```lyr blocks,
// taught Tailwind's @apply (PLAN.md §5d), which it otherwise lexes as
// error tokens inside a rule. Each "@apply" is masked as "--app:" (the
// same length, a custom property Chroma knows), the masked text is
// lexed, and the tokens are cut back out of the original text, with the
// "@apply" itself a keyword.
type twCSS struct{ chroma.Lexer }

var applyRe = regexp.MustCompile(`@apply\b`)

func (l twCSS) Tokenise(opts *chroma.TokeniseOptions, text string) (chroma.Iterator, error) {
	locs := applyRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return l.Lexer.Tokenise(opts, text)
	}
	masked := []byte(text)
	for _, loc := range locs {
		copy(masked[loc[0]:loc[1]], "--app:")
	}
	it, err := l.Lexer.Tokenise(opts, string(masked))
	if err != nil {
		return nil, err
	}
	var out []chroma.Token
	pos, k := 0, 0
	emit := func(tt chroma.TokenType, end int) {
		if end <= pos {
			return
		}
		if n := len(out); n > 0 && out[n-1].Type == tt && tt == chroma.Keyword {
			out[n-1].Value += text[pos:end] // "--app" and ":" are one "@apply"
		} else {
			out = append(out, chroma.Token{Type: tt, Value: text[pos:end]})
		}
		pos = end
	}
	for _, t := range it.Tokens() {
		end := pos + len(t.Value)
		for pos < end {
			for k < len(locs) && locs[k][1] <= pos {
				k++
			}
			switch {
			case k < len(locs) && locs[k][0] <= pos: // inside an @apply
				emit(chroma.Keyword, min(end, locs[k][1]))
			case k < len(locs) && locs[k][0] < end:
				emit(t.Type, locs[k][0])
			default:
				emit(t.Type, end)
			}
		}
	}
	return chroma.Literator(out...), nil
}
