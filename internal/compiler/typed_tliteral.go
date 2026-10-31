package compiler

// The typed pass of template literals (PLAN.md §2.10): each piece of a
// whole-value literal (SitePiece) gets the write {…} would get in text,
// written as a piece; each concatenation (SiteConcat) gets its pieces' TB
// methods, or Go's + when they're all strings, and a prop's literal its
// conversion to the field's type. Constant pieces are folded into the
// static text. A piece is inert when its static type can't carry markup:
// that decides the lyr.HTML/URL/CSS conversions and the style and on*
// warnings.

import (
	"go/ast"
	"go/constant"
	gotoken "go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/orbit-z/lyr/internal/jsfloat"
)

// tyQual names packages by their name in type strings ("lyr.URL"), and
// leaves the component's own implementation package out.
func tyQual(p *types.Package) string {
	if strings.HasSuffix(p.Name(), "_impl") {
		return ""
	}
	return p.Name()
}

// article prefixes s with "a" or "an".
func article(s string) string {
	if s != "" && strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// classifyPiece picks the write of a template literal's piece: as {…} in
// text, except that lyr.HTML is escaped like a string (a literal is
// text), a constant without String/Error/Format methods is folded, and in
// an attribute (attr) the text writes are attribute pieces. bad is set for
// a Renderer piece, which is an error.
func (e *tyEnv) classifyPiece(tv types.TypeAndValue, attr bool) (w TypedWrite, bad bool) {
	w = e.classify(tv)
	switch w.Kind {
	case WriteHTML:
		w = TypedWrite{Kind: WriteString, Conv: "string"}
	case WriteRender:
		return TypedWrite{}, true
	}
	if text, ok := foldPiece(tv.Value, w); ok {
		return TypedWrite{Kind: WriteFold, Text: text}, false
	}
	if attr {
		switch w.Kind {
		case WriteString:
			w.Kind = WriteAttrString // AttrValue: attribute-escaped
		case WriteBytes, WriteError, WriteStringer:
			w = TypedWrite{} // AttrPiece
		}
	}
	return w, false
}

// foldPiece returns the text a constant piece v prints with write w, for
// the types whose text is their value (w is then a string, number or bool
// write: classify gives types with methods other writes).
func foldPiece(v constant.Value, w TypedWrite) (string, bool) {
	if v == nil {
		return "", false
	}
	switch w.Kind {
	case WriteString:
		if v.Kind() == constant.String {
			return constant.StringVal(v), true
		}
	case WriteInt, WriteUint:
		if v.Kind() == constant.Int {
			return v.ExactString(), true
		}
	case WriteBool:
		if v.Kind() == constant.Bool {
			return strconv.FormatBool(constant.BoolVal(v)), true
		}
	case WriteFloat:
		if v.Kind() == constant.Int || v.Kind() == constant.Float {
			f, _ := constant.Float64Val(constant.ToFloat(v))
			return jsfloat.Format(f, 64), true
		}
	case WriteFloat32:
		if v.Kind() == constant.Int || v.Kind() == constant.Float {
			f, _ := constant.Float32Val(constant.ToFloat(v))
			return jsfloat.Format(float64(f), 32), true
		}
	}
	return "", false
}

// inert reports whether a piece of static type tv can't carry markup or a
// URL scheme (PLAN.md §2.10 (Q4)): a constant (folded, so checked as static
// text) or a number or bool, either without a String, Error or Format
// method on T or *T, untyped nil, or a value of the marker type itself
// (marker may be nil).
func (e *tyEnv) inert(tv types.TypeAndValue, marker types.Type) bool {
	t, nilv, ok := tyStatic(tv)
	switch {
	case !ok:
		return false
	case nilv:
		return true
	case marker != nil && identical(t, types.Unalias(marker)):
		return true
	case hasPrintMethod(t):
		return false
	case tv.Value != nil:
		return true
	}
	b, isB := t.Underlying().(*types.Basic)
	return isB && b.Info()&(types.IsInteger|types.IsFloat|types.IsBoolean) != 0
}

// hasPrintMethod reports whether T or *T has a String, Error or Format
// method, which the printing rules would call.
func hasPrintMethod(t types.Type) bool {
	for _, m := range [...]string{"String", "Error", "Format"} {
		if obj, _, _ := types.LookupFieldOrMethod(t, true, nil, m); obj != nil {
			if _, isFunc := obj.(*types.Func); isFunc {
				return true
			}
		}
	}
	return false
}

// pieceDiag is the error for a Renderer piece.
func pieceDiag(p TLPart, tv types.TypeAndValue) Diagnostic {
	return Diagnostic{SeverityError, p.Span, "can't put " + p.Text + " (" + types.TypeString(tv.Type, tyQual) +
		") in a template literal: it renders HTML, and a template literal is text"}
}

// concatWrite classifies a SiteConcat's call: the write of each piece and,
// for TemplateAs, the conversion to the witness's type, with the errors of
// a field that can't hold a string or of a piece that isn't inert for a
// trusted type. tvs are the pieces' types, in order.
func (e *tyEnv) concatWrite(call *ast.CallExpr, site *CallSite, info *types.Info) (w TypedWrite, diags []Diagnostic, tvs []types.TypeAndValue) {
	l := site.Lit
	off := 0
	if site.As {
		off = 1
	}
	if l == nil || len(call.Args) != off+len(l.Parts) {
		return TypedWrite{}, nil, nil
	}
	w = TypedWrite{Kind: WriteConcat, Pieces: make([]TypedWrite, len(l.Parts))}
	for i, p := range l.Parts {
		if !p.Piece {
			continue
		}
		tv, ok := info.Types[call.Args[off+i]]
		tvs = append(tvs, tv)
		if !ok {
			return TypedWrite{}, nil, nil
		}
		pw, bad := e.classifyPiece(tv, false)
		if bad {
			diags = append(diags, pieceDiag(p, tv))
		}
		w.Pieces[i] = pw
	}
	if !site.As {
		return w, diags, tvs
	}
	ftv, ok := info.Types[call.Args[0]]
	if !ok || ftv.Type == nil {
		return TypedWrite{}, diags, tvs
	}
	field := types.Unalias(ftv.Type)
	marker := identical(field, e.html) || identical(field, e.url) || identical(field, e.css)
	switch {
	case types.Identical(field, types.Typ[types.String]):
	case marker:
		k := 0
		for i, p := range l.Parts {
			if !p.Piece {
				continue
			}
			tv := tvs[k]
			k++
			if e.inert(tv, field) {
				continue
			}
			name := types.TypeString(field, tyQual)
			pt := "a"
			if t, _, ok := tyStatic(tv); ok {
				pt = article(types.TypeString(t, tyQual))
			}
			msg := pt + " piece can't be part of " + article(name) + " prop: make the prop a string (filtered where it's written) or convert the piece with " + name + "(…)"
			if site.Attr != nil && site.Attr.Directive == SetHTML {
				msg = pt + " piece can't be part of set:html: write the literal as {…} content, or convert the piece with lyr.HTML(…)"
			}
			diags = append(diags, Diagnostic{SeverityError, l.Parts[i].Span, msg})
			break
		}
		w.Conv = "as"
	case isStringKind(field):
		w.Conv = "as"
	case types.IsInterface(field) && types.AssignableTo(types.Typ[types.String], field):
	default:
		tag := "Self"
		if n, ok := site.Node.(*Component); ok && !n.Self {
			tag = n.Name
		}
		at := l.Span
		if site.Attr != nil {
			at = site.Attr.Span
		}
		diags = append(diags, Diagnostic{SeverityError, at,
			"a template literal is a string; prop " + site.AttrName + " of <" + tag + "> is " + types.TypeString(field, tyQual)})
		return TypedWrite{}, diags, tvs
	}
	return w, diags, tvs
}

// tySpanMap maps the Go spans of a call site's sub-expressions in the
// pass-1 file back to .lyr spans: by arithmetic when the site's code is
// copied byte for byte, else through the source map, with the
// concatenations (whose Go code starts and ends in glue) mapped to their
// literals.
type tySpanMap struct {
	tf           *gotoken.File
	smap         *SourceMap
	starts, ends map[int]int
}

func newTySpanMap(tf *gotoken.File, smap *SourceMap, sites []CallSite) *tySpanMap {
	m := &tySpanMap{tf: tf, smap: smap, starts: map[int]int{}, ends: map[int]int{}}
	for i := range sites {
		if s := &sites[i]; s.Kind == SiteConcat {
			m.starts[s.Call.Start], m.ends[s.Call.End] = s.Code.Start, s.Code.End
		}
	}
	return m
}

func (m *tySpanMap) span(site *CallSite, n ast.Node) Span {
	a, b := m.tf.Offset(n.Pos()), m.tf.Offset(n.End())
	if m.smap == nil || strings.IndexByte(site.Code.Text, '`') < 0 {
		return Span{a - site.Arg.Start + site.Code.Start, b - site.Arg.Start + site.Code.Start}
	}
	s, ok := m.starts[a]
	if !ok {
		s, _ = m.smap.LyrOffset(a)
	}
	e, ok := m.ends[b]
	if !ok {
		e, _ = m.smap.LyrOffset(b)
	}
	return Span{s, max(s, e)}
}

// isStringKind reports whether t's underlying type is string.
func isStringKind(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// litTrusted reports whether a whole-value literal on a style or on*
// attribute gets no warning: every piece inert (lyr.CSS counts on
// style), tvs being the pieces' types.
func (e *tyEnv) litTrusted(name string, tvs []types.TypeAndValue) bool {
	var marker types.Type
	if strings.EqualFold(name, "style") {
		marker = e.css
	}
	for _, tv := range tvs {
		if !e.inert(tv, marker) {
			return false
		}
	}
	return true
}
