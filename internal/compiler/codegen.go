package compiler

// Code generation: part (B) of milestone 4. This file is the contract; the
// implementation lives in codegen*.go and sourcemap*.go files owned by agent
// B. See DESIGN.md.

// Options configure code generation for a whole project.
type Options struct {
	// Module is the user's module path ("example.com/blog").
	Module string
	// Mode is Strict for builds and Tolerant for the LSP. Both emit the same
	// shape (every expression on its own line); Tolerant additionally emits
	// every Go region of the template even where the construct around it is
	// broken (an unknown component's attribute expressions become
	// "_ = (expr)" lines), so gopls can complete in it.
	Mode Mode
	// Dev is set by orbit dev. Reserved: it changes nothing in milestone 4.
	Dev bool
	// Locals is the import path of the user's src/locals package, or "" to
	// use orbit__rt.NoLocals (detection is milestone 7).
	Locals string
	// CompressHTML is orbit.json's "compressHTML": how template whitespace
	// is compressed at codegen time (DESIGN.md, settled gap 2). The zero
	// value is the default, "jsx" (Astro v7's default).
	CompressHTML Compression
}

// Compression is a compressHTML mode.
type Compression uint8

const (
	// CompressJSX ("jsx", the default, Astro v7's) applies JSX's whitespace
	// rules: whitespace with a newline next to a tag boundary is removed,
	// within a line it's kept.
	CompressJSX Compression = iota
	// CompressHTML ("html", or true) collapses whitespace runs to one
	// space, or one newline if the run has one, and drops whitespace-only
	// text between block-level tags; inline content keeps a space.
	CompressHTML
	// CompressNone (false) copies template text verbatim.
	CompressNone
)

// Input is one file to generate.
type Input struct {
	File     *File
	Analysis *Analysis
	// Index holds the other components; a component's own entry isn't
	// needed (<Self> uses Analysis).
	Index   ComponentIndex
	Options Options
	// Typed holds the typed writes chosen by the go/types pass, keyed by
	// CallSite.Key. Nil in pass 1 and in Tolerant mode: every call site
	// then uses the Expr/Attr fallback. A site missing from a non-nil map
	// also falls back.
	Typed TypedWrites
	// Direct selects the build flavor of Impl (PLAN.md §3.5) when non-nil:
	// the render function is exported as Orbit__RenderImpl (so direct
	// callers can call it), the call sites of the components in
	// Direct.Callees (and <Self> with Direct.Self) call the callee's
	// Orbit__RenderImpl instead of its declaration package's hook, and
	// directives name the .orbit file as seen from the file's overlay
	// location under BuildDir. The file keeps the hook flavor's lines. An
	// empty DirectCalls is the build flavor of a direct callee that calls
	// nothing directly itself. Nil for the hook flavor (orbit generate,
	// orbit dev, the LSP). Ignored in Tolerant mode.
	Direct *DirectCalls
}

// DirectCalls are the direct call sites of one file's build flavor, chosen
// by the project (PLAN.md §3.5, Cycles): the callees outside the file's
// import cycle.
type DirectCalls struct {
	// Callees holds the project-relative paths of the components to call
	// directly. A page callee always keeps the hook (its Render wrapper
	// builds Params), and so do server:defer call sites.
	Callees map[string]bool
	// Self makes <Self> a direct self-call (never in a page).
	Self bool
}

// Output is the generated code of one .orbit file.
type Output struct {
	Decl, Impl *GenFile
	// CallSites are the fallback-capable writes, in the order they appear
	// in Impl. Generating the same Input with a different Typed gives the
	// same sites with the same keys (only their Go spans move).
	CallSites []CallSite
	// DeferSites are the server:defer call sites, in the order they
	// appear in Impl, for the JSON-serializable Props check of the typed
	// pass (PLAN.md §3.4).
	DeferSites []DeferSite
	// JSONSites are the data-* attribute expressions and the set:json
	// blocks with a static id, in order, for the types of the component's
	// client module (PLAN.md §5c).
	JSONSites []JSONSite
	// Diagnostics are codegen errors and warnings (unknown component or
	// prop, missing component file, not-yet-implemented directives),
	// sorted by position. Generate always returns both files, even with
	// errors; in Strict mode the caller must not write them then.
	Diagnostics []Diagnostic
}

// GenFile is one generated Go file.
type GenFile struct {
	// Path is relative to the module root: GenPackage.DeclFile or ImplFile.
	Path string
	// ImportPath and Package are its package's import path and name.
	ImportPath string
	Package    string
	// Content starts with GenHeader and is never gofmt'ed.
	Content []byte
	Map     *SourceMap
}

// SiteKind distinguishes content writes from attribute writes.
type SiteKind uint8

const (
	// SiteContent is {expr}, or the value of set:text: orbit__w.Expr(x).
	SiteContent SiteKind = iota
	// SiteAttr is a={expr} or {a} on an HTML element: orbit__w.Attr("a", x).
	SiteAttr
	// SiteClass is a dynamic class:list item on an HTML element (or the
	// value of a class={expr} merged into it): orbit__w.ClassAny(x).
	SiteClass
	// SiteClassProp is a dynamic class:list item in a component's class
	// prop: orbit__rt.ClassString(x).
	SiteClassProp
	// SiteRest is a dynamic pass-through attribute a={expr} or {a} on a
	// component with an orbit.RestAttrs field (PLAN.md §2.5):
	// orbit__rt.RestAny(pre, "a", x).
	SiteRest
	// SiteTag is the name of a dynamic tag (PLAN.md §2.8):
	// orbit__w.TagOpen(pos, "Tag", Tag, content). It has no typed write.
	SiteTag
	// SiteOptProp is an expression passed to a component prop of type
	// orbit.Opt[T] (PLAN.md §2.6 (Q3)):
	// orbit__rt.OptOf(Alias.Props{}.Field, x). The typed pass checks x and
	// passes it as is (an Opt[T]), wraps it (a T), or reports an error.
	SiteOptProp
	// SitePiece is a piece of a template literal that is the whole value
	// of an HTML element attribute (AttrName set) or of a content
	// expression or set:text (PLAN.md §2.10): orbit__w.AttrPiece(x) or
	// orbit__w.TextPiece(x), each piece its own write.
	SitePiece
	// SiteConcat is a template literal anywhere else, a string expression:
	// orbit__rt.TemplateString(s0, p0, s1, …, sN), or, as the whole value
	// of a component prop or set:html (As),
	// orbit__rt.TemplateAs(witness, s0, p0, …), which the typed pass turns
	// into a concatenation. Key is the offset of its closing backtick.
	SiteConcat
	// SiteLit is the whole-value template literal of an HTML element
	// attribute, whose pieces are SitePiece sites: no write of its own,
	// recorded for the style and on* warnings (Call and Arg are empty).
	SiteLit
)

// CallSite records one write emitted with the Expr/Attr fallback, for the
// go/types pass (C). set:html (always HTML), set:json (always JSON), spreads
// and component props are not call sites, except class:list items.
type CallSite struct {
	Kind SiteKind
	// Key identifies the site across passes: Code.Start, the offset of the
	// expression in the .orbit file.
	Key int
	// Node is the AST node the expression comes from: the *Expr for {expr},
	// the tag's node (*Element, *Fragment) for set:text and attributes.
	Node Node
	// Attr is the attribute (a={expr}, {a} or set:text), nil for {expr}.
	Attr *Attr
	// Code is the expression as written.
	Code GoCode
	// AttrName is the attribute name for SiteAttr, and URLAttr is
	// IsURLAttr(AttrName), selecting URL pieces over String pieces.
	AttrName string
	URLAttr  bool
	// Call is the whole write call in Impl.Content (from "orbit__w." to its
	// closing paren); Arg is the expression's bytes in it, exactly
	// Code.Text, between its line directive and its reset, unless Code
	// holds template literals, which are rewritten (PLAN.md §2.10).
	Call, Arg Span
	// Lit is the template literal of a SitePiece, SiteConcat or SiteLit
	// site; Parent is a SitePiece's SiteLit key (Lit.Start); As is set on
	// a SiteConcat emitted as TemplateAs.
	Lit    *TemplateLiteral
	Parent int
	As     bool
}

// DeferSite is one server:defer call site: orbit__rt.Defer(…, <Alias>.Props{…}, …).
type DeferSite struct {
	// Attr is the server:defer attribute, where Props errors are reported.
	Attr Span
	// Tag is the component's tag name ("Comments", or "Self").
	Tag string
	// Lit is the type of the Props composite literal in Impl.Content
	// ("Comments.Props" or "Props"): the literal starts there.
	Lit Span
}

// WriteKind is the write chosen for a call site (PLAN.md §3.3 table).
type WriteKind uint8

const (
	WriteFallback WriteKind = iota // keep Expr / Attr
	WriteSkip                      // untyped nil: emit nothing
	WriteString
	WriteInt
	WriteUint
	WriteFloat
	WriteFloat32
	WriteBool
	WriteHTML
	WriteBytes
	WriteError
	WriteStringer
	WriteRender
	WriteAttrString
	WriteAttrURL
	WriteAttrBool
	// Numbers in an attribute (Attr's output for them; digits never need
	// escaping or URL filtering), written without boxing in an interface.
	WriteAttrInt
	WriteAttrUint
	WriteAttrFloat
	WriteAttrFloat32
	// WriteAttrJSON is an orbit.JSONValue attribute (PLAN.md §5c): pieces
	// ` name='` and `'` around JSONAttr(pos, value), pos being the .orbit
	// position of the value.
	WriteAttrJSON
	// WriteClass and WriteClasses are a class:list item of a string kind
	// (Conv "string" unless it's string) or of type []string (Conv
	// "[]string" for a named one): orbit__w.Class/Classes on an element;
	// string(x) and orbit__rt.JoinClasses(x...) in a component's prop.
	WriteClass
	WriteClasses
	// The pass-through attribute writes (SiteRest, PLAN.md §2.5): the
	// orbit__rt constructor of the attribute's typed value, with Conv to
	// its parameter type. WriteRestStringURL is a string in a URL
	// attribute (filtered), WriteRestURL an orbit.URL (trusted).
	WriteRestString
	WriteRestStringURL
	WriteRestURL
	WriteRestBool
	WriteRestInt
	WriteRestUint
	WriteRestFloat
	WriteRestFloat32
	WriteRestJSON
	// WriteAttrBoolText is a bool on a true/false attribute (aria-*,
	// draggable, …, IsTrueFalseAttr; PLAN.md §2.6 (Q3)): pieces ` name="`
	// and `"` around Bool, as Attr writes it.
	WriteAttrBoolText
	// WriteRestOpt and WriteRestOptURL are an Opt-typed pass-through
	// attribute (PLAN.md §2.6 (Q7)): orbit__rt.RestOpt(pre, "a", x), or
	// RestOptURL in a URL attribute.
	WriteRestOpt
	WriteRestOptURL
	// The writes of a SiteOptProp (PLAN.md §2.6 (Q3)): WriteOptAsIs is an
	// Opt[T] passed as is, WriteOptWrap a T wrapped with
	// orbit__rt.Wrap(Alias.Props{}.Field, x), and WriteOptWrapIf an
	// orbit.If call whose value is an untyped constant,
	// orbit__rt.WrapIf(Alias.Props{}.Field, <If>[bool], c, v)
	// (TypedWrite.If holds its spans).
	WriteOptAsIs
	WriteOptWrap
	WriteOptWrapIf
	// WriteFold is a constant piece of a template literal, folded into the
	// static text (TypedWrite.Text, unescaped) (PLAN.md §2.10).
	WriteFold
	// WriteConcat is a SiteConcat: TypedWrite.Pieces holds the write of
	// each part (static parts zero), and Conv is "as" when the result is
	// converted to the witness's string type with orbit__rt.StringAs.
	WriteConcat
)

// writeMethods is the Writer method each kind calls. WriteAttrString,
// WriteAttrURL and the number attributes are attribute pieces (see
// attrPieces), so they call a value-only method (AttrValue, URL for
// WriteAttrURL).
var writeMethods = [...]string{"Expr", "", "String", "Int", "Uint", "Float", "Float32", "Bool",
	"HTML", "Bytes", "Error", "Stringer", "Render", "AttrValue", "URL", "AttrBool",
	"Int", "Uint", "Float", "Float32", "JSONAttr", "Class", "Classes",
	"RestString", "RestStringURL", "RestURL", "RestBool", "RestInt", "RestUint", "RestFloat", "RestFloat32", "RestJSON",
	"Bool", "RestOpt", "RestOptURL", "", "Wrap", "WrapIf", "", ""}

// attrPieces reports whether k writes its attribute as pieces: the static
// ` name="` and `"` join the neighbouring Raw constants, and the call writes
// only the value, with no attribute name. That's byte-identical to
// AttrString or AttrURL (and to Attr for a number) and saves the name and
// quote writes.
func (k WriteKind) attrPieces() bool {
	return k == WriteAttrString || k == WriteAttrURL || k >= WriteAttrInt && k <= WriteAttrJSON || k == WriteAttrBoolText
}

// Method is the Writer method the write calls ("Expr" for a content
// fallback; the caller uses "Attr" for an attribute fallback), "" for
// WriteSkip.
func (k WriteKind) Method() string { return writeMethods[k] }

// TypedWrite is the typed write for one call site.
type TypedWrite struct {
	Kind WriteKind
	// Conv is a conversion to wrap the expression in, "" for none: a Go
	// type such as "int64", "string", "bool", or "orbit__rt.Renderer".
	// Codegen emits Method(Conv(<expr>,)) with the same line layout.
	Conv string
	// If is set when the site's expression is a call of the runtime's
	// orbit.If, or a value of type orbit.Opt[T] (If.Opt), lowered (PLAN.md
	// §2.6): Kind and Conv are then the write of its value, and may be
	// WriteFallback (Attr or Expr of it).
	If *IfWrite
	// Opt is set on a class:list item of type orbit.Opt[T] on an element:
	// Kind and Conv write its value when it's set.
	Opt bool
	// Text is a WriteFold piece's constant text, unescaped.
	Text string
	// Pieces are a WriteConcat's part writes, one per part of its literal.
	Pieces []TypedWrite
}

// IfWrite is a lowered orbit.If(cond, v) call site. Codegen emits
//
//	{
//		orbit__c, orbit__v := cond, v
//		if orbit__c {
//			<the write of orbit__v>
//		}
//	}
//
// which evaluates both arguments in order, as the call does, and writes the
// same bytes as Attr or Expr of its result. With Folded, v is a constant
// and the body is Raw(Text) (Text "" writes nothing).
//
// With Opt, the site's expression is a value of type orbit.Opt[T] (not an
// If call; Val is its span, the others are empty), and the block starts
//
//	orbit__o :=
//		x
//	orbit__v, orbit__c := orbit__o.Get()
type IfWrite struct {
	Opt bool
	// Fun, Cond and Val are the .orbit spans of the callee (orbit.If, with
	// its type arguments if written), the condition and the value.
	Fun, Cond, Val Span
	// Inst is true when Fun includes explicit type arguments.
	Inst bool
	// Const is set when the If's value is a constant (a style or on*
	// attribute is then trusted, as for the constant).
	Const  bool
	Folded bool
	Text   string
}

// TypedWrites maps CallSite.Key to the chosen write.
type TypedWrites map[int]TypedWrite

// Generate generates the declaration and implementation files of one .orbit
// file, with their source maps and call sites. It never panics. See
// DESIGN.md for the exact shape (PLAN.md §3.2) and directive rules (§3.3).
func Generate(in *Input) *Output {
	return cgGenerate(in)
}
