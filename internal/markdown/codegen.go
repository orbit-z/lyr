package markdown

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// The generated packages of a Markdown file (PLAN.md §2.4): decl holds
// Props, Params (pages), Frontmatter, Headings, URL (pages) and the uniform
// Render wrapper; impl holds the HTML constant, the layout's props built
// once at package initialization, and the render function.

// LayoutAlias is the layout's decl package's import name in impl.
const LayoutAlias = "Layout"

// GenInput is what code generation needs besides the Doc.
type GenInput struct {
	Info    compiler.FileInfo
	Pkg     compiler.GenPackage
	Options compiler.Options
	// Index is the project's component index, for the layout.
	Index compiler.ComponentIndex
}

// Info returns the component index entry of a Markdown file: a component
// that takes no props and has no slots.
func Info(in GenInput) *compiler.ComponentInfo {
	return &compiler.ComponentInfo{
		Path: in.Info.Path, Role: in.Info.Role, Pkg: in.Pkg,
		Name: compiler.TrimSourceExt(in.Info.Path), ID: compiler.ComponentID(in.Info.Path),
		Props: compiler.PropsInfo{Listable: true},
	}
}

// assign is one statement of the layout props function: p.<Field> = code,
// with a //line directive to the frontmatter key at pos (pos < 0: none).
type assign struct {
	pos  int
	code string
}

// Generate returns the pass-1 output of d: the layout props guessed from
// the YAML values, untyped (the fallback of PLAN.md §2.4), and the
// diagnostics that need only the component index: a missing layout, an
// unknown frontmatter key, a headings key when the layout fills Headings.
func Generate(d *Doc, in GenInput) *compiler.Output {
	out := &compiler.Output{}
	layout := in.layout(d, &out.Diagnostics)
	var assigns []assign
	var imports []string
	if layout != nil {
		headings := headingsField(&layout.Props)
		usesTime := false
		for _, f := range d.Fields {
			if headings && compiler.NormalizeName(f.Key) == "headings" {
				out.Diagnostics = append(out.Diagnostics, headingsConflict(f, layout.Name))
				continue
			}
			name, err := compiler.MatchProp(&layout.Props, f.Key)
			if err != nil {
				out.Diagnostics = append(out.Diagnostics, compiler.Diagnostic{Severity: compiler.SeverityError,
					Span:    compiler.Span{Start: f.KeyPos, End: f.KeyPos + len(f.Key)},
					Message: fmt.Sprintf("unknown frontmatter key %q for layout %s (%s)", f.Key, layout.Name, compiler.PropList(layout.Name, &layout.Props))})
				continue
			}
			usesTime = usesTime || usesTimeOf(f.Value)
			assigns = append(assigns, assign{pos: f.KeyPos, code: "p." + name + " = " + goLit(f.Value, false)})
		}
		if headings {
			assigns = append(assigns, assign{pos: -1, code: "p.Headings = Headings"})
		}
		if usesTime {
			imports = append(imports, `"time"`)
		}
	}
	out.Decl = genDecl(d, in)
	out.Impl = genImpl(d, in, layout, assigns, imports)
	return out
}

func usesTimeOf(v *Value) bool { return usesTime(v) }

// layout returns the layout's index entry, reporting a missing one.
func (in GenInput) layout(d *Doc, ds *[]compiler.Diagnostic) *compiler.ComponentInfo {
	if d.Layout == "" {
		return nil
	}
	ci := in.Index[d.Layout]
	if ci == nil {
		*ds = append(*ds, compiler.Diagnostic{Severity: compiler.SeverityError,
			Span:    compiler.Span{Start: d.LayoutPos, End: d.LayoutPos + len(d.LayoutRaw)},
			Message: fmt.Sprintf("layout %s: no such file", d.LayoutRaw)})
	}
	return ci
}

// headingsField reports whether Props has a Headings field of a type
// written as []<pkg>.Heading: pass 1's guess at []orbit.Heading.
func headingsField(p *compiler.PropsInfo) bool {
	for _, f := range p.Fields {
		if f.Name == "Headings" {
			t := strings.ReplaceAll(f.Type, " ", "")
			return strings.HasPrefix(t, "[]") && (t == "[]Heading" || strings.HasSuffix(t, ".Heading"))
		}
	}
	return false
}

func headingsConflict(f *Field, layout string) compiler.Diagnostic {
	return compiler.Diagnostic{Severity: compiler.SeverityError,
		Span:    compiler.Span{Start: f.KeyPos, End: f.KeyPos + len(f.Key)},
		Message: fmt.Sprintf("frontmatter key %q can't be set: Orbit fills %s.Props.Headings with the page's headings; rename the key", f.Key, layout)}
}

// genDecl generates the declaration package.
func genDecl(d *Doc, in GenInput) *compiler.GenFile {
	path := in.Pkg.DeclFile()
	page := in.Info.Role == compiler.RolePage
	var b strings.Builder
	b.WriteString(compiler.GenHeader + "\n\n")
	b.WriteString("package " + in.Pkg.Name + "\n\nimport (\n")
	b.WriteString("\t" + compiler.NameRuntime + " " + strconv.Quote(compiler.RuntimePath) + "\n")
	needTime := false
	for _, f := range d.Fields {
		needTime = needTime || usesTime(f.Value)
	}
	if needTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString(")\n\n")
	b.WriteString("// Props is empty: a Markdown file takes no props.\ntype Props struct{}\n\n")
	if page {
		b.WriteString("type Params struct{}\n\n")
	}
	fmt.Fprintf(&b, "// Frontmatter holds the frontmatter values of %s. Read-only.\n", in.Info.Path)
	b.WriteString("var Frontmatter = struct {\n")
	for _, f := range d.Fields {
		b.WriteString("\t" + f.Name + " " + goType(f.Value) + "\n")
	}
	b.WriteString("}{\n")
	for _, f := range d.Fields {
		b.WriteString("\t" + f.Name + ": " + goLit(f.Value, false) + ",\n")
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "// Headings are the headings of %s. Read-only.\n", in.Info.Path)
	if len(d.Headings) == 0 {
		b.WriteString("var Headings []" + compiler.NameRuntime + ".Heading\n\n")
	} else {
		b.WriteString("var Headings = []" + compiler.NameRuntime + ".Heading{\n")
		for _, h := range d.Headings {
			fmt.Fprintf(&b, "\t{Depth: %d, Slug: %s, Text: %s},\n", h.Depth, strconv.Quote(h.Slug), strconv.Quote(h.Text))
		}
		b.WriteString("}\n\n")
	}
	if page && in.Info.Route != "" && strings.HasPrefix(in.Info.Route, "/") {
		b.WriteString("// URL is the page's URL path.\n")
		fmt.Fprintf(&b, "const URL = %s\n\n", strconv.Quote(in.Info.Route))
	}
	sig := "*" + compiler.NameRuntime + ".Ctx, *" + compiler.NameRuntime + ".Writer, Props, " + compiler.NameRuntime + ".Slots"
	if page {
		sig += ", Params"
	}
	fmt.Fprintf(&b, "var %s func(%s) error\n\n", compiler.NameHook, sig)
	fmt.Fprintf(&b, "func Render(%s *%s.Ctx, %s *%s.Writer, %s Props, %s %s.Slots) error {\n",
		compiler.NameCtx, compiler.NameRuntime, compiler.NameW, compiler.NameRuntime, compiler.NameProps, compiler.NameSlots, compiler.NameRuntime)
	fmt.Fprintf(&b, "\tif %s == nil {\n\t\tpanic(%s)\n\t}\n", compiler.NameHook,
		strconv.Quote("orbit: "+in.Info.Path+" not linked; import "+in.Options.Module+"/"+compiler.GenDir+"/app"))
	args := compiler.NameCtx + ", " + compiler.NameW + ", " + compiler.NameProps + ", " + compiler.NameSlots
	if page {
		args += ", Params{}"
	}
	fmt.Fprintf(&b, "\treturn %s(%s)\n}\n", compiler.NameHook, args)
	return &compiler.GenFile{Path: path, ImportPath: in.Pkg.DeclImportPath(in.Options.Module), Package: in.Pkg.Name,
		Content: []byte(b.String()), Map: &compiler.SourceMap{Orbit: in.Info.Path, Gen: path}}
}

// genImpl generates the implementation package. imports are extra import
// specs, as written in the import block.
func genImpl(d *Doc, in GenInput, layout *compiler.ComponentInfo, assigns []assign, imports []string) *compiler.GenFile {
	path := in.Pkg.ImplFile()
	page := in.Info.Role == compiler.RolePage
	dir := compiler.DirectiveFile(path, in.Info.Path)
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	line(compiler.GenHeader)
	line("")
	line("package " + in.Pkg.ImplName())
	line("")
	line("import (")
	line("\t" + compiler.NameRuntime + " " + strconv.Quote(compiler.RuntimePath))
	line("\t. " + strconv.Quote(in.Pkg.DeclImportPath(in.Options.Module)))
	if layout != nil {
		line("\t" + LayoutAlias + " " + strconv.Quote(layout.Pkg.DeclImportPath(in.Options.Module)))
	}
	imports = slices.Clone(imports)
	slices.Sort(imports)
	for _, im := range slices.Compact(imports) {
		line("\t" + im)
	}
	line(")")
	line("")
	if layout != nil {
		line("// orbit__lprops are the layout's props, built once from the frontmatter.")
		line("var orbit__lprops = func() (p " + LayoutAlias + ".Props) {")
		for i, a := range assigns {
			if a.pos >= 0 {
				pos := d.Lines.Position(a.pos)
				fmt.Fprintf(&b, "//line %s:%d:%d\n", dir, pos.Line, pos.Col)
			}
			line(a.code)
			if a.pos >= 0 && (i == len(assigns)-1 || assigns[i+1].pos < 0) {
				n := strings.Count(b.String(), "\n") + 2
				fmt.Fprintf(&b, "//line %s:%d:1\n", compiler.ImplFileName, n)
			}
		}
		line("\treturn p")
		line("}()")
		line("")
	}
	line("const orbit__html = " + strconv.Quote(d.HTML))
	line("")
	if layout != nil {
		line("func orbit__content(" + compiler.NameW + " *" + compiler.NameRuntime + ".Writer) error {")
		line("\t" + compiler.NameW + ".Raw(orbit__html)")
		line("\treturn " + compiler.NameW + ".Err()")
		line("}")
		line("")
	}
	line("var _ = func() int { " + compiler.NameHook + " = " + compiler.NameRenderImpl + "; return 0 }()")
	line("")
	params := ""
	if page {
		params = ", _ Params"
	}
	line("func " + compiler.NameRenderImpl + "(" + compiler.NameCtx + " *" + compiler.NameRuntime + ".Ctx, " + compiler.NameW + " *" +
		compiler.NameRuntime + ".Writer, _ Props, _ " + compiler.NameRuntime + ".Slots" + params + ") error {")
	line("\tif " + compiler.NameErr + " := " + compiler.NameW + ".Enter(" + strconv.Quote(in.Info.Path) + "); " + compiler.NameErr + " != nil {")
	line("\t\treturn " + compiler.NameErr)
	line("\t}")
	line("\tdefer " + compiler.NameW + ".Leave()")
	if layout != nil {
		line("\treturn " + LayoutAlias + ".Render(" + compiler.NameCtx + ", " + compiler.NameW + ", orbit__lprops, " +
			compiler.NameRuntime + ".Slots{Default: orbit__content})")
	} else {
		line("\t" + compiler.NameW + ".Raw(orbit__html)")
		line("\treturn " + compiler.NameW + ".Err()")
	}
	line("}")
	return &compiler.GenFile{Path: path, ImportPath: in.Pkg.ImplImportPath(in.Options.Module), Package: in.Pkg.ImplName(),
		Content: []byte(b.String()), Map: &compiler.SourceMap{Orbit: in.Info.Path, Gen: path}}
}
