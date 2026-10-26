package markdown

import (
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Dashes as Astro's smartypants (retext-smartypants, dashes: true) does
// them: a run of exactly two hyphens becomes an em dash; any other run
// (one, three or more) stays as written. goldmark's typographer, whose
// dash substitutions are turned off, would make "--" an en dash and "---"
// an em dash. Code spans and code blocks are never parsed inline, so they
// keep their hyphens.
type dashes struct{}

func (dashes) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(dashParser{}, 9999)))
}

type dashParser struct{}

func (dashParser) Trigger() []byte { return []byte{'-'} }

func (dashParser) Parse(_ gast.Node, block text.Reader, _ parser.Context) gast.Node {
	line, seg := block.PeekLine()
	n := 0
	for n < len(line) && line[n] == '-' {
		n++
	}
	if n == 0 {
		return nil
	}
	block.Advance(n)
	if n == 2 {
		s := gast.NewString([]byte("&mdash;"))
		s.SetCode(true)
		return s
	}
	// The whole run as text, so its tail isn't parsed again.
	return gast.NewTextSegment(text.NewSegment(seg.Start, seg.Start+n))
}
