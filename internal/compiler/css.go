package compiler

// Scoped CSS (PLAN.md §5): the rewrite behind ScopeCSS. The stylesheet is
// tokenized with tdewolff/parse/v2/css's lexer, whose tokens cover the input
// byte for byte, and walked by a small structural parser (rule lists,
// declaration lists with CSS nesting, at-rules). The output is the input
// with edits applied: ":where([attr])" inserted into each selector and the
// ":global(" … ")" wrappers removed. Everything else, comments and
// formatting included, is copied as written.

import (
	"slices"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// cssGroupRules are the at-rules whose block holds rules (or, nested in a
// style rule, declarations and rules): their selectors are scoped. Every
// other at-rule block (@keyframes, @font-face, @page, @property,
// @counter-style, @font-feature-values, @view-transition, unknown ones) is
// copied unchanged.
var cssGroupRules = map[string]bool{
	"media": true, "supports": true, "layer": true, "container": true,
	"scope": true, "starting-style": true, "document": true,
}

// cssLegacyPseudoElements are the pseudo-elements CSS 2 wrote with one colon.
var cssLegacyPseudoElements = map[string]bool{
	"before": true, "after": true, "first-line": true, "first-letter": true,
}

type cssTok struct {
	tt         css.TokenType
	start, end int
}

// cssEdit replaces src[start:end] with text (an insertion when start ==
// end, a deletion when text is "").
type cssEdit struct {
	start, end int
	text       string
}

type cssScoper struct {
	src   string
	toks  []cssTok
	i     int
	where string // ":where([data-o-…])"
	edits []cssEdit
	diags []Diagnostic
	stack []int // prelude's open-bracket stack, reused
}

func scopeCSS(src, attr string) (string, []Diagnostic) {
	s := &cssScoper{src: src, where: ":where([" + attr + "])"}
	if !s.lex() {
		return src, s.diags
	}
	s.block(false, false, true)
	return s.apply(), s.diags
}

func (s *cssScoper) errorf(start, end int, msg string) {
	s.diags = append(s.diags, Diagnostic{Severity: SeverityError, Span: Span{start, end}, Message: msg})
}

func (s *cssScoper) text(t cssTok) string { return s.src[t.start:t.end] }

// lex tokenizes src and reports the token-level errors (unclosed comments
// and strings, bad url()).
func (s *cssScoper) lex() bool {
	s.toks = make([]cssTok, 0, len(s.src)/4+1)
	l := css.NewLexer(parse.NewInputString(s.src))
	pos := 0
	for {
		tt, data := l.Next()
		if tt == css.ErrorToken {
			break
		}
		t := cssTok{tt, pos, pos + len(data)}
		pos = t.end
		s.toks = append(s.toks, t)
		switch tt {
		case css.CommentToken:
			if len(data) < 4 || !strings.HasSuffix(string(data), "*/") {
				s.errorf(t.start, t.start+2, "unclosed comment")
			}
		case css.BadStringToken:
			s.errorf(t.start, t.end, "unclosed string")
		case css.BadURLToken:
			s.errorf(t.start, t.end, "invalid url()")
		}
	}
	if pos != len(s.src) { // the lexer always covers its input; stay safe
		s.errorf(pos, len(s.src), "can't tokenize CSS here")
		return false
	}
	return true
}

func (s *cssScoper) is(k int, tt css.TokenType) bool {
	return k < len(s.toks) && s.toks[k].tt == tt
}

func (s *cssScoper) isDelim(k int, c byte) bool {
	return s.is(k, css.DelimToken) && s.src[s.toks[k].start] == c
}

// block parses the items of a block, or of the stylesheet when top is set,
// up to and including its closing '}'. decls says it's a declaration list
// (a style rule's body), nested that its selectors are nested ones. It
// reports whether the closing '}' was found.
func (s *cssScoper) block(decls, nested, top bool) bool {
	for s.i < len(s.toks) {
		t := s.toks[s.i]
		switch t.tt {
		case css.WhitespaceToken, css.CommentToken, css.CDOToken, css.CDCToken:
			s.i++
		case css.SemicolonToken:
			if !decls {
				s.errorf(t.start, t.end, "unexpected ';'")
			}
			s.i++
		case css.RightBraceToken:
			s.i++
			if top {
				s.errorf(t.start, t.end, "unexpected '}'")
				continue
			}
			return true
		case css.AtKeywordToken:
			s.atRule(decls, nested)
		case css.CustomPropertyNameToken:
			if decls {
				s.customProperty()
				continue
			}
			s.qualifiedRule(nested, false)
		default:
			s.qualifiedRule(nested, decls)
		}
	}
	return false
}

// prelude scans from s.i to the first '{', ';' or '}' outside parentheses
// and brackets, and returns its index (len(toks) at EOF). It reports
// unbalanced parentheses and brackets.
func (s *cssScoper) prelude() int {
	stack := s.stack[:0] // indexes of the open ( [ function tokens
	defer func() { s.stack = stack[:0] }()
	for k := s.i; k < len(s.toks); k++ {
		t := s.toks[k]
		switch t.tt {
		case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken:
			stack = append(stack, k)
		case css.RightParenthesisToken, css.RightBracketToken:
			want := css.RightParenthesisToken
			if len(stack) > 0 && s.toks[stack[len(stack)-1]].tt == css.LeftBracketToken {
				want = css.RightBracketToken
			}
			if len(stack) == 0 || t.tt != want {
				s.errorf(t.start, t.end, "unexpected '"+s.text(t)+"'")
				continue
			}
			stack = stack[:len(stack)-1]
		case css.LeftBraceToken, css.SemicolonToken, css.RightBraceToken:
			if len(stack) == 0 {
				return k
			}
		}
	}
	for _, k := range stack {
		t := s.toks[k]
		c := "("
		if t.tt == css.LeftBracketToken {
			c = "["
		}
		s.errorf(t.start, t.end, "unclosed '"+c+"'")
	}
	return len(s.toks)
}

// qualifiedRule parses a style rule, or in a declaration list (decls) a
// declaration or a nested style rule, whichever it turns out to be.
func (s *cssScoper) qualifiedRule(nested, decls bool) {
	start := s.i
	end := s.prelude()
	if s.is(end, css.LeftBraceToken) {
		s.selectorList(start, end, nested || decls)
		s.i = end + 1
		if !s.block(true, true, false) {
			s.errorf(s.toks[end].start, s.toks[end].end, "unclosed '{'")
		}
		return
	}
	if decls { // a declaration
		s.i = end
		if s.is(end, css.SemicolonToken) {
			s.i++
		}
		return
	}
	at := len(s.src)
	if end < len(s.toks) {
		at = s.toks[end].start
	}
	s.errorf(s.toks[start].start, at, "expected '{' after selector")
	s.i = end
	if s.is(end, css.SemicolonToken) {
		s.i++
	}
}

// customProperty skips a --name: value declaration; its value may hold any
// balanced tokens, braces included.
func (s *cssScoper) customProperty() {
	depth := 0
	for ; s.i < len(s.toks); s.i++ {
		switch s.toks[s.i].tt {
		case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken, css.LeftBraceToken:
			depth++
		case css.RightParenthesisToken, css.RightBracketToken:
			if depth > 0 {
				depth--
			}
		case css.RightBraceToken:
			if depth == 0 {
				return
			}
			depth--
		case css.SemicolonToken:
			if depth == 0 {
				s.i++
				return
			}
		}
	}
}

func (s *cssScoper) atRule(decls, nested bool) {
	t := s.toks[s.i]
	name := strings.ToLower(s.text(t)[1:])
	if strings.HasPrefix(name, "-") { // vendor prefix: -webkit-keyframes
		if k := strings.IndexByte(name[1:], '-'); k >= 0 {
			name = name[k+2:]
		}
	}
	s.i++
	end := s.prelude()
	s.i = end
	switch {
	case s.is(end, css.SemicolonToken):
		s.i++
	case s.is(end, css.LeftBraceToken):
		s.i++
		var ok bool
		if cssGroupRules[name] {
			ok = s.block(decls, nested, false)
		} else {
			ok = s.skipBlock()
		}
		if !ok {
			s.errorf(s.toks[end].start, s.toks[end].end, "unclosed '{'")
		}
	}
}

// skipBlock skips a block copied as written, up to its closing '}'.
func (s *cssScoper) skipBlock() bool {
	depth := 0
	for ; s.i < len(s.toks); s.i++ {
		switch s.toks[s.i].tt {
		case css.LeftBraceToken:
			depth++
		case css.RightBraceToken:
			if depth == 0 {
				s.i++
				return true
			}
			depth--
		}
	}
	return false
}

func (s *cssScoper) significant(k int) bool {
	tt := s.toks[k].tt
	return tt != css.WhitespaceToken && tt != css.CommentToken
}

// selectorList scopes the selector list toks[a:b]; toks[b] is its '{'.
func (s *cssScoper) selectorList(a, b int, nested bool) {
	for a < b && !s.significant(a) {
		a++
	}
	for b > a && !s.significant(b-1) {
		b--
	}
	if a == b {
		s.errorf(s.toks[b].start, s.toks[b].end, "missing selector before '{'")
		return
	}
	depth, from := 0, a
	for k := a; k <= b; k++ {
		if k < b {
			switch s.toks[k].tt {
			case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken:
				depth++
			case css.RightParenthesisToken, css.RightBracketToken:
				depth--
			}
			if depth != 0 || s.toks[k].tt != css.CommaToken {
				continue
			}
		}
		s.complexSelector(from, k, nested)
		from = k + 1
	}
}

type cssCompound struct {
	first, last int
	// skip: inside :global(…), or html / body / :root, which are never scoped
	// (as in Astro); the scope moves to the nearest compound before it.
	skip bool
	// nest: holds '&', the parent selector, which is scoped already.
	nest bool
	// pseudo is the token of the first pseudo-element's colon, or -1.
	pseudo int
}

// complexSelector scopes the complex selector toks[a:b], b being the
// index of its ',' or the list's end.
func (s *cssScoper) complexSelector(a, b int, nested bool) {
	end := b
	for a < b && !s.significant(a) {
		a++
	}
	for b > a && !s.significant(b-1) {
		b--
	}
	if a == b {
		at := len(s.src)
		if end < len(s.toks) {
			at = s.toks[end].start
		}
		s.errorf(at, at+1, "empty selector in list")
		return
	}
	var buf [8]cssCompound
	cs := buf[:0]
	cur := -1
	lastComb := -1 // token of a combinator not yet followed by a compound
	depth := 0
	for k := a; k < b; k++ {
		t := s.toks[k]
		if t.tt == css.ColonToken {
			s.globalWrapper(k, b)
		}
		if depth > 0 {
			cs[cur].last = k
			switch t.tt {
			case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken:
				depth++
			case css.RightParenthesisToken, css.RightBracketToken:
				depth--
			}
			continue
		}
		switch {
		case t.tt == css.CommentToken:
			continue
		case t.tt == css.WhitespaceToken:
			cur = -1
			continue
		case s.isDelim(k, '>') || s.isDelim(k, '+') || s.isDelim(k, '~') || t.tt == css.ColumnToken:
			if lastComb >= 0 {
				s.errorf(t.start, t.end, "unexpected combinator '"+s.text(t)+"'")
			} else if len(cs) == 0 && !nested {
				s.errorf(t.start, t.end, "a selector can't start with '"+s.text(t)+"' outside a nested rule")
			}
			cur, lastComb = -1, k
			continue
		}
		lastComb = -1
		if cur < 0 {
			cs = append(cs, cssCompound{first: k, pseudo: -1})
			cur = len(cs) - 1
		}
		c := &cs[cur]
		c.last = k
		switch t.tt {
		case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken:
			depth++
		case css.IdentToken:
			if k == c.first && (strings.EqualFold(s.text(t), "html") || strings.EqualFold(s.text(t), "body")) {
				c.skip = true
			}
		case css.DelimToken:
			if s.src[t.start] == '&' {
				c.nest = true
			}
		case css.ColonToken:
			switch {
			case s.is(k+1, css.ColonToken):
				if c.pseudo < 0 {
					c.pseudo = k
				}
				k++
				c.last = k
			case s.is(k+1, css.FunctionToken) && strings.EqualFold(s.text(s.toks[k+1]), "global("):
				c.skip = true
			case s.is(k+1, css.IdentToken):
				name := strings.ToLower(s.text(s.toks[k+1]))
				if cssLegacyPseudoElements[name] && c.pseudo < 0 {
					c.pseudo = k
				} else if name == "root" {
					c.skip = true
				}
			}
		}
	}
	if lastComb >= 0 {
		t := s.toks[lastComb]
		if len(cs) == 0 {
			s.errorf(t.start, t.end, "missing selector after '"+s.text(t)+"'")
		} else {
			s.errorf(t.start, t.end, "a selector can't end with '"+s.text(t)+"'")
		}
	}
	for x := len(cs) - 1; x >= 0; x-- {
		c := cs[x]
		if c.skip {
			continue
		}
		if c.nest {
			return // the parent is scoped
		}
		at := s.toks[c.last].end
		if c.pseudo >= 0 {
			at = s.toks[c.pseudo].start
		}
		s.edits = append(s.edits, cssEdit{at, at, s.where})
		return
	}
}

// globalWrapper handles a ':' at toks[k]: for :global(sel), it removes
// ":global(" and its ")", leaving sel; a bare :global is an error.
func (s *cssScoper) globalWrapper(k, b int) {
	if s.is(k+1, css.IdentToken) && strings.EqualFold(s.text(s.toks[k+1]), "global") &&
		(k == 0 || s.toks[k-1].tt != css.ColonToken) {
		s.errorf(s.toks[k].start, s.toks[k+1].end, ":global needs a selector in parentheses: :global(…)")
		return
	}
	if k+1 >= b || s.toks[k+1].tt != css.FunctionToken || !strings.EqualFold(s.text(s.toks[k+1]), "global(") {
		return
	}
	depth, empty := 0, true
	for j := k + 2; j < b; j++ {
		switch s.toks[j].tt {
		case css.LeftParenthesisToken, css.FunctionToken, css.LeftBracketToken:
			depth++
		case css.RightParenthesisToken, css.RightBracketToken:
			if depth == 0 {
				if empty {
					s.errorf(s.toks[k].start, s.toks[j].end, "empty :global()")
				}
				s.edits = append(s.edits,
					cssEdit{s.toks[k].start, s.toks[k+1].end, ""},
					cssEdit{s.toks[j].start, s.toks[j].end, ""})
				return
			}
			depth--
		}
		if s.significant(j) {
			empty = false
		}
	}
	s.errorf(s.toks[k].start, s.toks[k+1].end, "unclosed :global(")
}

// apply returns src with the edits applied.
func (s *cssScoper) apply() string {
	if len(s.edits) == 0 {
		return s.src
	}
	slices.SortStableFunc(s.edits, func(x, y cssEdit) int {
		if x.start != y.start {
			return x.start - y.start
		}
		return (x.end - x.start) - (y.end - y.start) // insertions first
	})
	var b strings.Builder
	b.Grow(len(s.src) + len(s.edits)*len(s.where))
	pos := 0
	for _, e := range s.edits {
		if e.start < pos {
			continue
		}
		b.WriteString(s.src[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.WriteString(s.src[pos:])
	return b.String()
}
