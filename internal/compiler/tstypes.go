package compiler

// The Go → TypeScript type mapping of the typed server→client bridge
// (PLAN.md §5c "Type mapping Go → TS"): a value's static Go type becomes the
// TypeScript type of the JSON that encoding/json (v1 semantics) writes for
// it. Named types become declarations (TSDecl), emitted by EmitTypes
// (tstypes_emit.go); everything else is inlined.

import (
	"cmp"
	"fmt"
	gotoken "go/token"
	"go/types"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tsNS identifies the TS namespace a Go package's types go in (PLAN §5c
// Names).
type tsNS struct {
	// Key is the identity: the package import path; for a generated
	// package, "orbit:" + the .orbit path (a component's decl and impl
	// packages share it).
	Key string
	// Base is the preferred name: the package name, or the .orbit file's
	// base name without extension ("LikeButton").
	Base string
	// Path is used for disambiguation: the import path, or the .orbit path
	// without extension ("src/components/LikeButton").
	Path string
}

// TSDecl is the TypeScript declaration of one Go named type.
type TSDecl struct {
	// Ref is NS.Key + "\x01" + Name, plus "\x01" + a position for
	// function-local types.
	Ref string
	NS  tsNS
	// Name is the Go name (the emitter applies the keyword suffix).
	Name string
	// GoPath is "example.com/blog/internal/posts.Post", for the doc
	// comment.
	GoPath string
	// TypeParams are the Go type parameter names of a generic type.
	TypeParams []string
	// Fields are a struct's fields, in encoding order: an interface
	// declaration.
	Fields []TSField
	// Alias is the type of any other named type: `type Name = <Alias>`;
	// "" when Interface is set.
	Alias string
	// Interface is true for a struct (Fields, possibly empty).
	Interface bool
	// Source is where the Go type is declared (the language server's
	// side table, PLAN.md §9b); zero when unknown.
	Source TSSource
}

// TSSource is the Go declaration a .d.ts line stands for: in an .orbit
// file (a frontmatter type, or a data-* attribute), as byte offsets of the
// .orbit source the typed pass saw; or in a Go file, as the compiler's
// file name and 1-based line and byte column.
type TSSource struct {
	// File is the project-relative .orbit path when Orbit is set, else the
	// Go file name as recorded in the export data or FileSet (absolute,
	// or module-path-relative under -trimpath); "" when unknown.
	File  string
	Orbit bool
	// Start and End are byte offsets in the .orbit file (Orbit).
	Start, End int
	// Line and Col are 1-based, Col in bytes (export data may record only
	// the line: Col is then 1), and Name is the declared name (a Go
	// file).
	Line, Col int
	Name      string
}

// TSField is one property of a TSDecl interface (or an inline object type).
type TSField struct {
	// Name is the JSON name as encoding/json writes it (the emitter quotes
	// non-identifiers).
	Name     string
	Optional bool
	// Type is TS text with refs.
	Type string
	// Source is the Go field's declaration; zero when unknown.
	Source TSSource
}

// tsType is a mapped type before rendering.
type tsType struct {
	// text is the TS text without the " | null" member, with refs.
	text string
	// null is set when the value can encode as null.
	null bool
	// unknown is set for the TS type unknown (text "unknown").
	unknown bool
	// nullRef is set when text is a ref to a declaration whose own type
	// includes null (a named slice, map or pointer).
	nullRef bool
}

var tsUnknown = tsType{text: "unknown", unknown: true}

func (t tsType) String() string {
	if t.null && !t.unknown {
		return t.text + " | null"
	}
	return t.text
}

// array returns t as an array element: "(A | null)[]".
func (t tsType) array() string {
	if t.null && !t.unknown {
		return "(" + t.text + " | null)[]"
	}
	return t.text + "[]"
}

// tsMethods are the encoding methods of a type T and of *T.
type tsMethods struct {
	valM, valTM, ptrM, ptrTM bool
}

func (m tsMethods) any() bool { return m.valM || m.valTM || m.ptrM || m.ptrTM }

// valueType is the mapping of a T-position value of a non-pointer type
// with these methods, following encoding/json's newTypeEncoder order: a
// Marshaler on *T comes first (called only when the value is addressable,
// so unknown), then one on T, then a TextMarshaler on *T (string only when
// T has it too), then one on T. ok is false without encoding methods.
func (m tsMethods) valueType() (tsType, bool) {
	switch {
	case m.ptrM || m.valM:
		return tsUnknown, true
	case m.valTM:
		return tsType{text: "string"}, true
	case m.ptrTM:
		return tsUnknown, true
	}
	return tsType{}, false
}

// tsProbEntry is a cached problem walk result of a named type.
type tsProbEntry struct{ p *jsonProblem }

// tsMapper maps static Go types to TypeScript with encoding/json v1
// semantics. One per unit; not concurrent.
type tsMapper struct {
	nsOf func(*types.Package) tsNS
	// src locates a declaration (name position and length) for the side
	// table; nil leaves Source zero.
	src     func(pos gotoken.Pos, name string) TSSource
	decls   map[string]*TSDecl
	refs    map[*types.Named]string // origin → Ref
	named   map[*types.Named]tsType // instance → mapped type
	methods map[*types.Named]tsMethods
	probs   map[*types.Named]tsProbEntry
	stack   map[*types.Named]int // problem walk: named types being walked, by depth
}

func newTSMapper(nsOf func(*types.Package) tsNS) *tsMapper {
	return &tsMapper{
		nsOf:    nsOf,
		decls:   map[string]*TSDecl{},
		refs:    map[*types.Named]string{},
		named:   map[*types.Named]tsType{},
		methods: map[*types.Named]tsMethods{},
		probs:   map[*types.Named]tsProbEntry{},
		stack:   map[*types.Named]int{},
	}
}

// Map returns t's TS type text. Named types appear as refs: "\x00" + Ref +
// "\x00", followed by "<args>" for a generic instance. It records a TSDecl
// for every named type reached (transitively). prob is the first part of t
// that encoding/json always fails on, or nil; its path starts with the root
// type's Go name when it's named ("Post.Notify"), and is relative ("[]",
// ".X") otherwise.
func (m *tsMapper) Map(t types.Type) (ts string, prob *jsonProblem) {
	ts = m.typ(t).String()
	if p, _ := m.walk(t); p != nil {
		root := ""
		if n, ok := types.Unalias(t).(*types.Named); ok {
			root = n.Obj().Name()
		}
		prob = &jsonProblem{path: root + p.path, typ: p.typ}
	}
	return ts, prob
}

// Decls returns the declarations recorded so far, sorted by Ref.
func (m *tsMapper) Decls() []TSDecl {
	out := make([]TSDecl, 0, len(m.decls))
	for _, d := range m.decls {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b TSDecl) int { return strings.Compare(a.Ref, b.Ref) })
	return out
}

// tsSpecial returns the inline mapping of the named types the table lists
// by name: orbit.HTML and orbit.URL (named strings), time.Time (its
// MarshalJSON writes an RFC 3339 string) and json.Number (written as a
// number literal).
func tsSpecial(n *types.Named) (tsType, bool) {
	obj := n.Obj()
	pkg := obj.Pkg()
	if pkg == nil {
		return tsType{}, false
	}
	switch pkg.Path() {
	case RuntimePath:
		if obj.Name() == "HTML" || obj.Name() == "URL" {
			return tsType{text: "string"}, true
		}
	case "time":
		if obj.Name() == "Time" {
			return tsType{text: "string"}, true
		}
	case "encoding/json":
		if obj.Name() == "Number" {
			return tsType{text: "number"}, true
		}
	}
	return tsType{}, false
}

// methodsOf returns the encoding methods of t and *t. Only named types and
// structs with embedded fields can have methods; named results are cached.
func (m *tsMapper) methodsOf(t types.Type) tsMethods {
	n, isNamed := t.(*types.Named)
	if isNamed {
		if r, ok := m.methods[n]; ok {
			return r
		}
	} else if !hasEmbedded(t) {
		return tsMethods{}
	}
	var r tsMethods
	if _, ok := t.Underlying().(*types.Interface); !ok {
		jm := jsonMethods
		ptr := types.NewPointer(t)
		r = tsMethods{
			valM:  types.Implements(t, jm.marshaler),
			valTM: types.Implements(t, jm.textMarshaler),
			ptrM:  types.Implements(ptr, jm.marshaler),
			ptrTM: types.Implements(ptr, jm.textMarshaler),
		}
	}
	if isNamed {
		m.methods[n] = r
	}
	return r
}

// hasEmbedded reports whether t is a struct literal type with an embedded
// field (which may promote methods).
func hasEmbedded(t types.Type) bool {
	st, ok := t.(*types.Struct)
	if !ok {
		return false
	}
	for i := range st.NumFields() {
		if st.Field(i).Embedded() {
			return true
		}
	}
	return false
}

// typ maps t.
func (m *tsMapper) typ(t types.Type) tsType {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Named:
		return m.namedType(t)
	case *types.TypeParam:
		return tsType{text: tsIdent(t.Obj().Name())}
	case *types.Basic:
		return tsBasic(t)
	case *types.Pointer:
		elem := types.Unalias(t.Elem())
		if n, ok := elem.(*types.Named); ok {
			if s, ok := tsSpecial(n); ok {
				s.null = true
				return s
			}
		}
		// The methods of *T itself (a pointer-receiver Marshaler is
		// called through a pointer).
		if _, isIface := elem.Underlying().(*types.Interface); !isIface {
			if me := m.methodsOf(elem); me.ptrM {
				return tsUnknown
			} else if me.ptrTM {
				return tsType{text: "string", null: true}
			}
		}
		e := m.typ(elem)
		if e.unknown {
			return e
		}
		e.null, e.nullRef = true, false
		return e
	case *types.Slice:
		if m.byteSlice(t) {
			return tsType{text: "string", null: true}
		}
		return tsType{text: m.typ(t.Elem()).array(), null: true}
	case *types.Array:
		return tsType{text: m.typ(t.Elem()).array()}
	case *types.Map:
		return tsType{text: "Record<string, " + m.typ(t.Elem()).String() + ">", null: true}
	case *types.Struct:
		if r, ok := m.methodsOf(t).valueType(); ok {
			return r
		}
		return tsType{text: tsInline(m.structFields(t))}
	}
	// Interfaces, and func/chan (generate-time problems).
	return tsUnknown
}

// byteSlice reports whether encoding/json writes s as a base64 string: a
// slice of a uint8 kind whose pointer has no encoding method.
func (m *tsMapper) byteSlice(s *types.Slice) bool {
	elem := types.Unalias(s.Elem())
	b, ok := elem.Underlying().(*types.Basic)
	if !ok || b.Kind() != types.Uint8 {
		return false
	}
	me := m.methodsOf(elem)
	return !me.ptrM && !me.ptrTM
}

func tsBasic(b *types.Basic) tsType {
	switch {
	case b.Kind() == types.UntypedNil:
		return tsType{text: "null"}
	case b.Info()&types.IsBoolean != 0:
		return tsType{text: "boolean"}
	case b.Info()&types.IsComplex != 0:
		return tsUnknown
	case b.Info()&(types.IsInteger|types.IsFloat) != 0:
		return tsType{text: "number"}
	case b.Info()&types.IsString != 0:
		return tsType{text: "string"}
	}
	return tsUnknown // unsafe.Pointer, invalid
}

// namedType maps a named type (an instance for a generic type), cached.
func (m *tsMapper) namedType(n *types.Named) tsType {
	if r, ok := m.named[n]; ok {
		return r
	}
	r := m.namedUncached(n)
	m.named[n] = r
	return r
}

func (m *tsMapper) namedUncached(n *types.Named) tsType {
	if s, ok := tsSpecial(n); ok {
		return s
	}
	nullRef := false
	switch u := n.Underlying().(type) {
	case *types.Interface, *types.Signature, *types.Chan:
		return tsUnknown
	case *types.Basic:
		if r := tsBasic(u); r.unknown {
			return r
		}
	case *types.Pointer, *types.Slice, *types.Map:
		nullRef = true
	}
	if r, ok := m.methodsOf(n).valueType(); ok {
		return r
	}
	if n.Obj().Pkg() == nil {
		return tsUnknown
	}
	ref := m.declare(n.Origin())
	text := "\x00" + ref + "\x00"
	if args := n.TypeArgs(); args.Len() > 0 {
		var b strings.Builder
		b.WriteString(text)
		b.WriteByte('<')
		for i := range args.Len() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(m.typ(args.At(i)).String())
		}
		b.WriteByte('>')
		text = b.String()
	}
	return tsType{text: text, nullRef: nullRef}
}

// declare records the declaration of the (origin) named type n and returns
// its Ref.
func (m *tsMapper) declare(n *types.Named) string {
	if r, ok := m.refs[n]; ok {
		return r
	}
	obj := n.Obj()
	pkg := obj.Pkg()
	ns := m.nsOf(pkg)
	ref := ns.Key + "\x01" + obj.Name()
	if s := tsLocalSuffix(obj); s != "" {
		ref += "\x01" + s
	}
	m.refs[n] = ref
	d := &TSDecl{Ref: ref, NS: ns, Name: obj.Name(), GoPath: pkg.Path() + "." + obj.Name(), Source: m.source(obj.Pos(), obj.Name())}
	m.decls[ref] = d
	if tps := n.TypeParams(); tps.Len() > 0 {
		d.TypeParams = make([]string, tps.Len())
		for i := range tps.Len() {
			d.TypeParams[i] = tps.At(i).Obj().Name()
		}
	}
	if _, ok := n.Underlying().(*types.Struct); ok {
		d.Interface = true
		d.Fields = m.structFields(n)
	} else {
		d.Alias = m.typ(n.Underlying()).String()
	}
	return ref
}

// source locates a declaration for the side table.
func (m *tsMapper) source(pos gotoken.Pos, name string) TSSource {
	if m.src == nil || !pos.IsValid() {
		return TSSource{}
	}
	return m.src(pos, name)
}

// tsLocalSuffix returns "" for a package-level type, and a deterministic
// position for a function-local one: the index of its file among the
// package's files and its offset in it, zero-padded so refs sort by
// position ("0000:00001234"), independent of the
// FileSet's bases.
func tsLocalSuffix(obj *types.TypeName) string {
	pkgScope := obj.Pkg().Scope()
	s := obj.Parent()
	if s == pkgScope {
		return ""
	}
	if s == nil {
		return "local"
	}
	for s.Parent() != nil && s.Parent() != pkgScope {
		s = s.Parent()
	}
	for i := range pkgScope.NumChildren() {
		if f := pkgScope.Child(i); f == s {
			return fmt.Sprintf("%04d:%08d", i, obj.Pos()-f.Pos())
		}
	}
	return "local"
}

// tsInline renders an anonymous struct's fields as an object type.
func tsInline(fields []TSField) string {
	if len(fields) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{ ")
	for i, f := range fields {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(tsPropName(f.Name))
		if f.Optional {
			b.WriteByte('?')
		}
		b.WriteString(": ")
		b.WriteString(f.Type)
	}
	b.WriteString(" }")
	return b.String()
}

// structFields maps the fields encoding/json writes for t (a type whose
// underlying type is a struct).
func (m *tsMapper) structFields(t types.Type) []TSField {
	fs := jsonTypeFields(t)
	if len(fs) == 0 {
		return nil
	}
	out := make([]TSField, len(fs))
	for i := range fs {
		out[i] = m.field(&fs[i])
	}
	return out
}

// field maps one struct field with its tag options.
func (m *tsMapper) field(f *jsonField) TSField {
	ft := types.Unalias(f.typ)
	var t tsType
	if q, ok := m.quoted(f, ft); ok {
		t = q
	} else {
		t = m.typ(ft)
	}
	opt := f.viaPtr
	drop := false
	u := ft.Underlying()
	_, isPtr := u.(*types.Pointer)
	_, isSlice := u.(*types.Slice)
	_, isMap := u.(*types.Map)
	if f.omitEmpty && canBeEmpty(ft) {
		opt = true
		drop = isPtr || isSlice || isMap
	}
	if f.omitZero {
		opt = true
		// nil is the zero value, unless an IsZero method decides (it's
		// always called with a nil check for a pointer).
		drop = drop || isPtr || (isSlice || isMap) && !hasIsZero(ft)
	}
	if drop && !t.unknown {
		if t.null {
			t.null = false
		} else if t.nullRef {
			t.text = "NonNullable<" + t.text + ">"
		}
	}
	return TSField{Name: f.name, Optional: opt, Type: t.String(), Source: m.source(f.pos, f.goName)}
}

// quoted returns the type of a field with the ",string" option when the
// option applies: a bool, number or string kind (or an unnamed pointer to
// one) without encoding methods, which encoding/json writes inside a JSON
// string.
func (m *tsMapper) quoted(f *jsonField, ft types.Type) (tsType, bool) {
	if !f.quoted {
		return tsType{}, false
	}
	t, null := ft, false
	if p, ok := t.(*types.Pointer); ok {
		if m.methodsOf(types.Unalias(p.Elem())).any() {
			return tsType{}, false
		}
		t, null = types.Unalias(p.Elem()), true
	}
	if _, ok := t.(*types.TypeParam); ok {
		return tsUnknown, true // depends on the type argument
	}
	if m.methodsOf(t).any() {
		return tsType{}, false
	}
	return tsType{text: "string", null: null}, true
}

// canBeEmpty reports whether omitempty can omit a value of type t
// (encoding/json's isEmptyValue): not for structs, nor arrays of non-zero
// length. A type parameter may be anything.
func canBeEmpty(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return false
	case *types.Array:
		return u.Len() == 0
	}
	return true
}

// hasIsZero reports whether t or *t has an IsZero() bool method, which
// omitzero calls instead of comparing with the zero value.
func hasIsZero(t types.Type) bool {
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(t), false, nil, "IsZero")
	fn, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	sig := fn.Signature()
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return false
	}
	b, ok := sig.Results().At(0).Type().(*types.Basic)
	return ok && b.Kind() == types.Bool
}

// jsonField is a struct field as encoding/json's typeFields finds it.
type jsonField struct {
	name      string // JSON name
	goName    string // Go field name
	tag       bool   // name came from the tag
	index     []int
	typ       types.Type // the field's declared type
	omitEmpty bool
	omitZero  bool
	quoted    bool
	viaPtr    bool        // promoted through an embedded pointer
	pos       gotoken.Pos // the Go field's name
}

// tsTypeCount counts types by identity (a small linear map).
type tsTypeCount []struct {
	t types.Type
	n int
}

func (c tsTypeCount) get(t types.Type) int {
	for _, e := range c {
		if types.Identical(e.t, t) {
			return e.n
		}
	}
	return 0
}

func (c *tsTypeCount) inc(t types.Type) int {
	for i := range *c {
		if types.Identical((*c)[i].t, t) {
			(*c)[i].n++
			return (*c)[i].n
		}
	}
	*c = append(*c, struct {
		t types.Type
		n int
	}{t, 1})
	return 1
}

// jsonTypeFields is encoding/json's typeFields (encode.go) on go/types: the
// fields encoding/json writes for t (whose underlying type is a struct), in
// encoding order, with embedded structs promoted and conflicts resolved the
// same way.
func jsonTypeFields(t types.Type) []jsonField {
	type queued struct {
		t      types.Type
		index  []int
		viaPtr bool
	}
	current := []queued{}
	next := []queued{{t: t}}
	var count, nextCount tsTypeCount
	var visited []types.Type
	var fields []jsonField
	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, nil
		for _, f := range current {
			if slices.ContainsFunc(visited, func(v types.Type) bool { return types.Identical(v, f.t) }) {
				continue
			}
			visited = append(visited, f.t)
			st := f.t.Underlying().(*types.Struct)
			for i := range st.NumFields() {
				sf := st.Field(i)
				sft := types.Unalias(sf.Type())
				if sf.Embedded() {
					et := sft
					if p, ok := et.(*types.Pointer); ok {
						et = types.Unalias(p.Elem())
					}
					if !sf.Exported() && !isStructType(et) {
						continue // unexported embedded non-struct
					}
				} else if !sf.Exported() {
					continue
				}
				tag := reflect.StructTag(st.Tag(i)).Get("json")
				if tag == "-" {
					continue
				}
				name, opts := jsonTagName(tag)
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sft
				_, embPtr := ft.(*types.Pointer)
				if embPtr {
					ft = types.Unalias(ft.(*types.Pointer).Elem())
				}
				quoted := false
				if jsonOption(opts, "string") {
					switch u := ft.Underlying().(type) {
					case *types.Basic:
						quoted = u.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsString) != 0 &&
							u.Info()&types.IsComplex == 0 && u.Kind() != types.UnsafePointer
					case *types.Interface:
						_, quoted = ft.(*types.TypeParam)
					}
				}
				if name != "" || !sf.Embedded() || !isStructType(ft) {
					tagged := name != ""
					if name == "" {
						name = sf.Name()
					}
					fields = append(fields, jsonField{
						name: name, goName: sf.Name(), tag: tagged, index: index, typ: sf.Type(),
						omitEmpty: jsonOption(opts, "omitempty"),
						omitZero:  jsonOption(opts, "omitzero"),
						quoted:    quoted,
						viaPtr:    f.viaPtr,
						pos:       sf.Pos(),
					})
					if count.get(f.t) > 1 {
						// Several instances: add a second, so that the
						// annihilation below sees a duplicate.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}
				if nextCount.inc(ft) == 1 {
					next = append(next, queued{t: ft, index: index, viaPtr: f.viaPtr || embPtr})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b jsonField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})
	// Drop the fields hidden by Go's embedding rules, except that tagged
	// fields are promoted (dominantField).
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fi)
			continue
		}
		if len(fields[i].index) == len(fields[i+1].index) && fields[i].tag == fields[i+1].tag {
			continue
		}
		out = append(out, fi)
	}
	fields = out
	slices.SortFunc(fields, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })
	return fields
}

func isStructType(t types.Type) bool {
	if _, ok := t.(*types.TypeParam); ok {
		return false
	}
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

// jsonOption reports whether the comma-separated tag options contain name.
func jsonOption(opts, name string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == name {
			return true
		}
	}
	return false
}

// jsonTagName splits a json tag into its name ("" for none: the Go name is
// used) and its comma-separated options. The name follows the encoder Go
// 1.27 ships (encoding/json v1 running on the json/v2 engine, v2/fields.go
// parseFieldOptions), which differs from v1's isValidTag on malformed names
// only: a name runs up to a comma, backslash or quote; one followed by
// anything but a comma is cut to its leading identifier, or dropped when it
// doesn't start with a letter or "_".
func jsonTagName(tag string) (name, opts string) {
	_, opts, _ = strings.Cut(tag, ",")
	n := strings.IndexAny(tag, ",\\'\"`")
	if n < 0 {
		return tag, opts
	}
	if tag[n] == ',' {
		return tag[:n], opts
	}
	r, _ := utf8.DecodeRuneInString(tag)
	if r != '_' && !unicode.IsLetter(r) {
		return "", opts
	}
	rest := strings.TrimLeftFunc(tag, func(r rune) bool {
		return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
	})
	return tag[:len(tag)-len(rest)], opts
}

// walk returns the first part of t that encoding/json always fails on,
// with its path relative to t, and the smallest problem-walk stack depth of
// a named type it reached while that type was being walked (MaxInt when
// none): a nil result found through a type still being walked depends on
// that type's other fields, so it isn't cached.
func (m *tsMapper) walk(orig types.Type) (*jsonProblem, int) {
	const noHit = int(^uint(0) >> 1)
	t := types.Unalias(orig)
	n, isNamed := t.(*types.Named)
	if !isNamed {
		if m.methodsOf(t).any() {
			return nil, noHit
		}
		return m.walkUnder(t, orig)
	}
	if e, ok := m.probs[n]; ok {
		return e.p, noHit
	}
	if d, ok := m.stack[n]; ok {
		return nil, d
	}
	if _, ok := tsSpecial(n); ok || types.IsInterface(n) || m.methodsOf(n).any() {
		m.probs[n] = tsProbEntry{}
		return nil, noHit
	}
	d := len(m.stack)
	m.stack[n] = d
	p, hit := m.walkUnder(n.Underlying(), orig)
	delete(m.stack, n)
	if hit >= d {
		hit = noHit
	}
	if p != nil || hit == noHit {
		m.probs[n] = tsProbEntry{p}
	}
	return p, hit
}

func (m *tsMapper) walkUnder(u, orig types.Type) (*jsonProblem, int) {
	const noHit = int(^uint(0) >> 1)
	bad := &jsonProblem{typ: orig}
	sub := func(prefix string, t types.Type) (*jsonProblem, int) {
		p, hit := m.walk(t)
		if p != nil && prefix != "" {
			p = &jsonProblem{path: prefix + p.path, typ: p.typ}
		}
		return p, hit
	}
	switch u := u.Underlying().(type) {
	case *types.Basic:
		if u.Info()&types.IsComplex != 0 || u.Kind() == types.UnsafePointer {
			return bad, noHit
		}
	case *types.Signature, *types.Chan:
		return bad, noHit
	case *types.Pointer:
		return sub("", u.Elem())
	case *types.Slice:
		return sub("[]", u.Elem())
	case *types.Array:
		return sub("[]", u.Elem())
	case *types.Map:
		if !jsonEncodeMapKey(u.Key()) {
			return bad, noHit
		}
		return sub("[]", u.Elem())
	case *types.Struct:
		st := types.Unalias(orig)
		if _, ok := st.(*types.Named); !ok {
			st = u
		}
		minHit := noHit
		for _, f := range jsonTypeFields(st) {
			p, hit := sub("."+f.goName, f.typ)
			minHit = min(minHit, hit)
			if p != nil {
				return p, minHit
			}
		}
		return nil, minHit
	}
	return nil, noHit // interfaces, type parameters
}

// jsonEncodeMapKey reports whether encoding/json can encode a map with key
// type k: a string or integer kind, or a type implementing
// encoding.TextMarshaler.
func jsonEncodeMapKey(k types.Type) bool {
	if b, ok := k.Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 || ok && b.Info()&types.IsString != 0 {
		return true
	}
	return types.Implements(k, jsonMethods.textMarshaler)
}

// tsReserved are names a namespace or a declaration can't take: JavaScript
// and TypeScript reserved and contextual words, the predeclared type names,
// the global types the emitted declarations use, and the names the
// per-unit modules declare.
var tsReserved = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		break case catch class const continue debugger default delete do else enum export extends
		false finally for function if import in instanceof new null return super switch this throw
		true try typeof var void while with implements interface let package private protected
		public static yield abstract accessor any as asserts async await bigint boolean constructor
		declare from get global infer is keyof module namespace never number object of out readonly
		require satisfies set string symbol type undefined unique unknown
		Record NonNullable Element Data JSONBlocks data json`) {
		m[w] = true
	}
	return m
}()

// tsIdent returns name, with a "_" suffix when it's reserved.
func tsIdent(name string) string {
	if tsReserved[name] {
		return name + "_"
	}
	return name
}
