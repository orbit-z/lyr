package compiler

// Frontmatter analysis: part (A) of milestone 4. This file is the contract;
// the implementation lives in analyze*.go files owned by agent A. See
// DESIGN.md.

// Role says how a .orbit file is used.
type Role uint8

const (
	// RoleComponent is any .orbit file outside src/pages.
	RoleComponent Role = iota
	// RolePage is a file under src/pages: it gets a generated Params, and
	// may not declare Props or Params (PLAN.md §2.2).
	RolePage
)

// FileInfo is what the caller (project discovery, milestone 5; tests before
// that) knows about a file before analysis.
type FileInfo struct {
	// Path is project-relative with "/" separators: "src/components/Card.orbit".
	Path string
	Role Role
	// Route is a page's route pattern, as the router takes it
	// ("/blog/[slug]", "/docs/[...rest]", "/"); empty for components. Only
	// whole-segment params are read from it; malformed routes (mixed
	// segments, a non-final rest) are reported by milestone 5.
	Route string
}

// Placement says which generated package(s) a hoisted declaration or an
// import goes to (PLAN.md §3.1). It is a bit set.
type Placement uint8

const (
	PlaceDecl Placement = 1 << iota // the declaration package
	PlaceImpl                       // the implementation package
	PlaceBoth = PlaceDecl | PlaceImpl
)

// HoistKind is the keyword a hoisted declaration starts with.
type HoistKind uint8

const (
	HoistType HoistKind = iota
	HoistConst
	HoistFunc // a named func or a method; an IIFE stays in the body
)

// Hoisted is one top-level type, const or func declaration of the
// frontmatter, copied verbatim to package level.
type Hoisted struct {
	Kind HoistKind
	// Span is the declaration from its keyword to its last token (a whole
	// group for type (…) and const (…)), without the trailing semicolon or
	// line comment. Codegen copies src[Span] (kind=exact).
	Span Span
	// Doc is the doc comment group right above the declaration, empty if
	// none. Codegen copies src[Doc.Start:Span.End] when Doc is set, so hover
	// shows it.
	Doc Span
	// Names are the declared names (all of a group's), and NameSpans their
	// positions. A method declares no package-level name: Names is empty and
	// Recv is the receiver's base type name.
	Names     []string
	NameSpans []Span
	Recv      string
	// Place is PlaceDecl or PlaceImpl, never both. A declaration goes to decl
	// when Props depends on it, transitively (methods of decl types and
	// what they reference included); a group is placed as a whole.
	Place Placement
}

// Stmt is one top-level statement of the Render body, in source order.
type Stmt struct {
	// Span runs from the statement's first token to its last, without the
	// separating semicolon. Codegen copies src[Span] (kind=exact).
	Span Span
}

// GoImport is one Go import spec of the frontmatter.
type GoImport struct {
	// Name is the explicit import name ("" if none, "_" for a blank
	// import). Dot imports are rejected by analysis.
	Name string
	// Path is the unquoted import path.
	Path string
	// Span is the whole spec (name and quoted path), PathSpan the quoted path.
	Span, PathSpan Span
	// Place is where the import is used, by object resolution over the
	// hoisted code, the body and every template expression (PLAN.md §3.1).
	// An unused or blank import is PlaceImpl, so Go reports an unused one at
	// its .orbit position. A user import of the runtime named orbit is placed
	// like the implicit one, and the implicit import is then not emitted
	// where it already is (see Analysis.ImplicitOrbit).
	Place Placement
}

// OrbitImport is one .orbit component import: Alias "./X.orbit".
type OrbitImport struct {
	Alias     string
	AliasSpan Span
	// Path is the unquoted path as written ("../components/Card.orbit");
	// PathSpan is the quoted path; Span the whole spec.
	Path           string
	Span, PathSpan Span
	// Resolved is the project-relative target, path.Join(path.Dir(file),
	// Path): "src/components/Card.orbit". Codegen looks it up in the
	// ComponentIndex.
	Resolved string
	// Place is where the alias is used: PlaceImpl for template tags and impl
	// Go code, PlaceDecl when decl code uses it (Props: B.Item). Unused is
	// 0: the import is emitted nowhere and analysis reports it (DESIGN.md
	// gap 1).
	Place Placement
}

// PropsInfo describes a component's Props type.
type PropsInfo struct {
	// Declared is true when the frontmatter declares type Props; otherwise
	// codegen emits type Props struct{} in the declaration package.
	Declared bool
	// Span is the Props type spec (empty when not declared).
	Span Span
	// Fields are the named fields of a struct Props, in order. Embedded
	// fields aren't listed.
	Fields []PropField
	// Listable is true when Fields is every field: Props is a struct type
	// literal with no embedded field (or isn't declared). When false
	// (embedded field, alias, defined type over another type), attributes
	// fall back to FieldName and the unknown-prop pre-check is skipped
	// (PLAN.md §2.3; MatchProp).
	Listable bool
}

// PropField is one named field of Props.
type PropField struct {
	Name     string
	NameSpan Span
	// Type is the field's type expression as written ("posts.Post").
	Type string
	// Doc is the field's doc comment text (go/ast CommentGroup.Text), with
	// its line comment appended if both exist; for LSP completion.
	Doc string
}

// ParamField is one field of a page's generated Params, in route order.
type ParamField struct {
	// Field is the Go field name, FieldName(Param).
	Field string
	// Param is the bracket name: "slug" for [slug], "path" for [...path].
	Param string
	Rest  bool
}

// Analysis is the result of analyzing one file's frontmatter against its
// template. All spans are byte offsets into the whole .orbit file.
type Analysis struct {
	Info FileInfo
	// Decls are the hoisted declarations in source order (both packages).
	Decls []Hoisted
	// Body are the remaining top-level statements, in order.
	Body []Stmt
	// Echo are the names declared at the top level of the body (:=, var),
	// each once, in order of first declaration, "_" excluded; codegen emits
	// "_ = name" for each (kind=echo).
	Echo []string
	// Terminating is true when the last body statement is terminating in
	// the Go spec's sense: codegen emits the echo lines before it and no
	// template code after it (PLAN.md §2.2).
	Terminating bool
	// GoImports and OrbitImports are the import specs in source order.
	GoImports    []GoImport
	OrbitImports []OrbitImport
	// ImplicitOrbit is where codegen emits the implicit
	// orbit "github.com/bertrandkhe/orbit" import: the packages whose user
	// code uses the name orbit, minus those a user import of it covers.
	ImplicitOrbit Placement
	// Props describes the component's Props (for a page: not declared,
	// Listable, no fields).
	Props PropsInfo
	// Params are a page's route params; nil for components (and for a page
	// without params, which still gets type Params struct{}).
	Params []ParamField
	// Diagnostics are analysis errors and warnings, sorted by position.
	Diagnostics []Diagnostic
}

// HasErrors reports whether any diagnostic is an error.
func (a *Analysis) HasErrors() bool {
	for _, d := range a.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Analyze analyzes the frontmatter of f (f.Frontmatter.Code, whose Span.Start
// is its offset in the file) and scans f.Template for import usage and the
// rules that need both. It never panics and always returns an Analysis; in
// Tolerant mode (f.Mode) it recovers from Go syntax errors as best it can.
//
// It reports (PLAN.md §2.2, §3.1): func init; a hoisted Render; orbit__ /
// Orbit__ identifiers anywhere in the frontmatter or template code;
// redeclaring an implicit name at the body's top level; a page declaring
// Props or Params; unexported Props fields and fields that normalize to the
// same name; route params that collide or can't become field names; a
// lowercase .orbit alias; framework and other non-.orbit relative imports;
// dot imports; an unexported decl-placed name used by impl code; an alias or
// impl-side name that equals an exported decl name; a terminating last
// statement followed by template content. The exact messages are listed in
// DESIGN.md.
func Analyze(f *File, info FileInfo) *Analysis {
	return analyze(f, info)
}
