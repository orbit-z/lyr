package tailwind

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tdewolff/parse/v2/css"
)

// Inlining (PLAN.md §5d, Q3). Tailwind's CLI inlines the CSS files an input
// @imports but, unlike @tailwindcss/vite, doesn't rewrite their url()s, so
// a Fontsource stylesheet's url(./files/x.woff2) would resolve from the
// importing stylesheet's directory. Orbit therefore inlines those imports
// itself before running Tailwind: each resolvable @import of a CSS file is
// replaced by the file's text, wrapped in @media, @supports and @layer
// blocks for its conditions, and every relative reference in an inlined
// file (url(), and the paths of @source, @plugin, @config, @reference and
// of the @imports left in place) is rewritten to be relative to the
// input's directory, where Tailwind runs and esbuild resolves the output.
// What stays: imports of tailwindcss itself, remote and unresolvable
// imports, and imports with conditions only Tailwind knows (theme(…),
// source(…), prefix(…)), which Tailwind handles. url(data:…), absolute
// URLs, root-relative paths (public/) and #fragments are left alone.

// Resolver resolves an @import specifier from dir to an absolute file
// path (internal/assets uses esbuild's resolver: relative paths,
// node_modules packages, package.json "style" and exports).
type Resolver func(spec, dir string) (string, bool)

// Source is a stylesheet to process.
type Source struct {
	Text string
	// File is the absolute path of the file Text is in: the .css file, or
	// the .orbit file of a <style> block.
	File string
	// Line and Col are the 1-based position of Text's first byte in File
	// (1, 1 for a .css file).
	Line, Col int
	// Dir is the input's directory: relative references resolve from it,
	// and Tailwind runs there.
	Dir string
}

// InlineOptions configure Inline.
type InlineOptions struct {
	Resolve Resolver
	// Read reads an imported file.
	Read func(path string) (string, error)
	// Root, when set, is the project root: an @import of tailwindcss (or
	// tailwindcss/utilities.css) or a @tailwind utilities without
	// source(…) gets source("<Root, relative to Dir>"), since Tailwind
	// detects sources from its working directory.
	Root string
}

// Inlined is a processed stylesheet, with the map from its offsets back to
// the files it came from.
type Inlined struct {
	Text string
	// Files are the inlined files, in order.
	Files []string
	segs  []seg
}

// seg maps Text[out:] to file text at off; verbatim is unset for
// rewritten or inserted text, which maps to off as a whole.
type seg struct {
	out      int
	f        *fileText
	off      int
	verbatim bool
}

type fileText struct {
	path      string
	text      string
	line, col int
}

// Position returns the file, line and column (1-based, bytes) of offset
// off in Text.
func (in *Inlined) Position(off int) (string, int, int) {
	if len(in.segs) == 0 {
		return "", 0, 0
	}
	k, _ := slices.BinarySearchFunc(in.segs, off, func(s seg, off int) int { return s.out - off - 1 })
	// k is the first segment that starts after off.
	if k > 0 {
		k--
	}
	s := in.segs[k]
	src := s.off
	if s.verbatim {
		src += off - s.out
	}
	src = min(max(src, 0), len(s.f.text))
	line, col := s.f.line, s.f.col
	text := s.f.text[:src]
	if nl := strings.LastIndexByte(text, '\n'); nl >= 0 {
		line += strings.Count(text, "\n")
		col = src - nl
	} else {
		col += src
	}
	return s.f.path, line, col
}

// LineCol returns the offset in Text of a 1-based line and column (bytes).
func (in *Inlined) LineCol(line, col int) int {
	off := 0
	for l := 1; l < line; l++ {
		nl := strings.IndexByte(in.Text[off:], '\n')
		if nl < 0 {
			return len(in.Text)
		}
		off += nl + 1
	}
	return min(off+max(col-1, 0), len(in.Text))
}

type inliner struct {
	opt  InlineOptions
	base string
	b    strings.Builder
	out  *Inlined
}

// Inline returns src with the CSS files it @imports inlined and their
// references rewritten (see above).
func Inline(src Source, opt InlineOptions) *Inlined {
	in := &inliner{opt: opt, base: src.Dir, out: &Inlined{}}
	ft := &fileText{path: src.File, text: src.Text, line: max(src.Line, 1), col: max(src.Col, 1)}
	in.file(ft, src.Dir, []string{src.File}, false)
	in.out.Text = in.b.String()
	return in.out
}

func (in *inliner) copy(f *fileText, from, to int) {
	if from >= to {
		return
	}
	in.out.segs = append(in.out.segs, seg{out: in.b.Len(), f: f, off: from, verbatim: true})
	in.b.WriteString(f.text[from:to])
}

func (in *inliner) insert(f *fileText, at int, text string) {
	if text == "" {
		return
	}
	in.out.segs = append(in.out.segs, seg{out: in.b.Len(), f: f, off: at})
	in.b.WriteString(text)
}

// pathRules are the at-rules whose string argument is a path.
var pathRules = map[string]bool{"source": true, "plugin": true, "config": true, "reference": true}

// file writes f's text, inlining its top-level @imports. dir is f's
// directory; stack the files being inlined (for cycles); nested is set
// for an imported file.
func (in *inliner) file(f *fileText, dir string, stack []string, nested bool) {
	src := f.text
	toks := lex(src)
	rewrite := filepath.Clean(dir) != filepath.Clean(in.base)
	last, depth := 0, 0
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.tt {
		case css.LeftBraceToken:
			depth++
		case css.RightBraceToken:
			depth = max(depth-1, 0)
		case css.URLToken:
			if rewrite {
				if v, ok := in.rebase(urlTokenValue(src[t.start:t.end]), dir, false); ok {
					in.copy(f, last, t.start)
					in.insert(f, t.start, "url("+quote(v)+")")
					last = t.end
				}
			}
		case css.FunctionToken:
			if rewrite && strings.EqualFold(src[t.start:t.end], "url(") {
				if j := skipSpace(toks, i+1); j < len(toks) && toks[j].tt == css.StringToken {
					last = in.rewriteString(f, toks[j], dir, last, false)
					i = j
				}
			}
		case css.AtKeywordToken:
			name := strings.ToLower(src[t.start+1 : t.end])
			end := ruleEnd(toks, i+1)
			switch {
			case name == "charset" && nested && depth == 0:
				in.copy(f, last, t.start)
				last = afterSemicolon(toks, end, len(src))
				i = skipTo(toks, end)
			case name == "import" && depth == 0:
				last = in.importRule(f, toks, i, end, dir, stack, last, rewrite)
				i = skipTo(toks, end)
			case name == "tailwind":
				if in.opt.Root != "" && hasWord(src, toks, i+1, end, "utilities") && !hasFunc(src, toks, i+1, end, "source(") {
					at := len(src)
					if end < len(toks) {
						at = toks[end].start
					}
					in.copy(f, last, at)
					in.insert(f, at, in.sourceArg())
					last = at
				}
			case rewrite && pathRules[name]:
				if j := skipSpace(toks, i+1); j < end && name == "source" && toks[j].tt == css.IdentToken && strings.EqualFold(src[toks[j].start:toks[j].end], "not") {
					i = j
				}
				if j := skipSpace(toks, i+1); j < end && toks[j].tt == css.StringToken {
					last = in.rewriteString(f, toks[j], dir, last, name != "source")
					i = j
				}
			}
		}
	}
	in.copy(f, last, len(src))
}

// rewriteString rewrites a string token's path; pathOnly limits it to
// "./" and "../" paths (a bare @plugin or @reference is a package).
func (in *inliner) rewriteString(f *fileText, t tok, dir string, last int, pathOnly bool) int {
	ref := unquote(f.text[t.start:t.end])
	if t.tt == css.URLToken {
		ref = urlTokenValue(f.text[t.start:t.end])
	}
	v, ok := in.rebase(ref, dir, pathOnly)
	if !ok {
		return last
	}
	in.copy(f, last, t.start)
	in.insert(f, t.start, quote(v))
	return t.end
}

var schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// isRemote reports whether a reference isn't a local relative path: a URL
// with a scheme (data:, https:), protocol-relative, root-relative, or a
// #fragment.
func isRemote(ref string) bool {
	return ref == "" || ref[0] == '/' || ref[0] == '#' || ref[0] == '\\' || schemeRE.MatchString(ref)
}

// rebase rewrites a reference relative to dir to one relative to the base
// directory.
func (in *inliner) rebase(ref, dir string, pathOnly bool) (string, bool) {
	if isRemote(ref) || strings.ContainsAny(ref, "\n") {
		return "", false
	}
	if pathOnly && !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
		return "", false
	}
	p, suffix := ref, ""
	if k := strings.IndexAny(ref, "?#"); k >= 0 {
		p, suffix = ref[:k], ref[k:]
	}
	rel, err := filepath.Rel(in.base, filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel + suffix, true
}

// sourceArg is the source(…) added to a Tailwind import.
func (in *inliner) sourceArg() string {
	rel, err := filepath.Rel(in.base, in.opt.Root)
	if err != nil {
		rel = in.opt.Root
	}
	return " source(" + quote(filepath.ToSlash(rel)) + ")"
}

// ruleEnd returns the index of the ';' ending the at-rule prelude that
// starts at token i (outside parentheses), or of the '{' or '}' that ends
// it, or len(toks).
func ruleEnd(toks []tok, i int) int {
	paren := 0
	for ; i < len(toks); i++ {
		switch toks[i].tt {
		case css.LeftParenthesisToken, css.FunctionToken:
			paren++
		case css.RightParenthesisToken:
			paren = max(paren-1, 0)
		case css.SemicolonToken:
			if paren == 0 {
				return i
			}
		case css.LeftBraceToken, css.RightBraceToken:
			return i
		}
	}
	return i
}

// skipTo returns the loop index that continues after a rule prelude
// ending at token end: its ';', or just before its '{' or '}' so the loop
// sees it.
func skipTo(toks []tok, end int) int {
	if end < len(toks) && toks[end].tt != css.SemicolonToken {
		return end - 1
	}
	return end
}

// afterSemicolon returns the offset after the rule ending at token end.
func afterSemicolon(toks []tok, end, n int) int {
	if end < len(toks) {
		if toks[end].tt == css.SemicolonToken {
			return toks[end].end
		}
		return toks[end].start
	}
	return n
}

func hasWord(src string, toks []tok, i, end int, w string) bool {
	for ; i < end && i < len(toks); i++ {
		if toks[i].tt == css.IdentToken && strings.EqualFold(src[toks[i].start:toks[i].end], w) {
			return true
		}
	}
	return false
}

func hasFunc(src string, toks []tok, i, end int, fn string) bool {
	for ; i < end && i < len(toks); i++ {
		if toks[i].tt == css.FunctionToken && strings.EqualFold(src[toks[i].start:toks[i].end], fn) {
			return true
		}
	}
	return false
}

// conditions are an @import's layer, supports and media conditions.
type conditions struct {
	layer    *string // nil: none; "": anonymous
	supports string
	media    string
}

// parseConditions reads the tokens after an @import's URL. ok is unset
// when they have a function Orbit doesn't know (Tailwind's theme(…),
// source(…), prefix(…)): the import is left to Tailwind.
func parseConditions(src string, toks []tok, i, end int) (c conditions, ok bool) {
	var media strings.Builder
	for i < end {
		t := toks[i]
		switch {
		case t.tt == css.IdentToken && strings.EqualFold(src[t.start:t.end], "layer") && c.layer == nil && media.Len() == 0:
			s := ""
			c.layer = &s
			i++
		case t.tt == css.FunctionToken:
			name := strings.ToLower(src[t.start:t.end])
			close := matchParen(toks, i+1, end)
			inner := strings.TrimSpace(src[t.end:toks[min(close, len(toks)-1)].start])
			switch {
			case name == "layer(" && c.layer == nil && media.Len() == 0:
				c.layer = &inner
			case name == "supports(" && c.supports == "" && media.Len() == 0:
				c.supports = inner
			default:
				return c, false
			}
			i = close + 1
		case t.tt == css.CommentToken || t.tt == css.WhitespaceToken && media.Len() == 0:
			i++
		default:
			media.WriteString(src[t.start:t.end])
			i++
		}
	}
	c.media = strings.TrimSpace(media.String())
	return c, true
}

// matchParen returns the index of the ')' closing the parenthesis opened
// just before token i.
func matchParen(toks []tok, i, end int) int {
	depth := 1
	for ; i < end; i++ {
		switch toks[i].tt {
		case css.LeftParenthesisToken, css.FunctionToken:
			depth++
		case css.RightParenthesisToken:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return end
}

// importRule handles the @import at token i, whose prelude ends at token
// end, and returns the new copy position.
func (in *inliner) importRule(f *fileText, toks []tok, i, end int, dir string, stack []string, last int, rewrite bool) int {
	src := f.text
	t := toks[i]
	spec, j, ok := importSpec(src, toks, i+1)
	if !ok {
		return last
	}
	stop := afterSemicolon(toks, end, len(src))
	if isTailwindSpec(spec) {
		if in.opt.Root != "" && (spec == "tailwindcss" || spec == "tailwindcss/utilities.css" || spec == "tailwindcss/utilities") && !hasFunc(src, toks, j, end, "source(") {
			at := len(src)
			if end < len(toks) {
				at = toks[end].start
			}
			in.copy(f, last, at)
			in.insert(f, at, in.sourceArg())
			return at
		}
		return last
	}
	cond, condOK := parseConditions(src, toks, j, end)
	var path string
	resolved := false
	if condOK && !isRemote(spec) && in.opt.Resolve != nil {
		path, resolved = in.opt.Resolve(spec, dir)
	}
	if resolved && slices.Contains(stack, path) {
		// An import cycle: dropped, as browsers and esbuild do.
		in.copy(f, last, t.start)
		return stop
	}
	var text string
	if resolved {
		var err error
		text, err = in.opt.Read(path)
		resolved = err == nil
	}
	if !resolved {
		// Left to Tailwind (it reports what it can't resolve), with its
		// path rebased when it's in an inlined file.
		if rewrite && (toks[j-1].tt == css.StringToken || toks[j-1].tt == css.URLToken) {
			return in.rewriteString(f, toks[j-1], dir, last, true)
		}
		return last
	}
	in.copy(f, last, t.start)
	var open, close strings.Builder
	if cond.media != "" {
		open.WriteString("@media " + cond.media + "{")
		close.WriteString("}")
	}
	if cond.supports != "" {
		open.WriteString("@supports (" + cond.supports + "){")
		close.WriteString("}")
	}
	if cond.layer != nil {
		if *cond.layer == "" {
			open.WriteString("@layer{")
		} else {
			open.WriteString("@layer " + *cond.layer + "{")
		}
		close.WriteString("}")
	}
	in.insert(f, t.start, open.String())
	in.out.Files = append(in.out.Files, path)
	child := &fileText{path: path, text: text, line: 1, col: 1}
	in.file(child, filepath.Dir(path), append(slices.Clip(stack), path), true)
	in.insert(f, t.start, close.String())
	return stop
}
