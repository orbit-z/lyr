package markdown

import (
	"fmt"
	"go/types"
	"math"

	"strconv"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// The typed pass of a Markdown file (PLAN.md §2.4 "Frontmatter → layout
// props"): each frontmatter value is converted to the type of the layout's
// Props field it sets, read with go/types from the layout's decl package.

// Retype returns the compiler.Unit Retype function of d, whose pass-1
// output is pass1.
func Retype(d *Doc, in GenInput, pass1 *compiler.Output) func(types.Importer) (*compiler.Output, []compiler.Diagnostic, bool) {
	return func(imp types.Importer) (*compiler.Output, []compiler.Diagnostic, bool) {
		if d.Layout == "" {
			return pass1, nil, true
		}
		layout := in.Index[d.Layout]
		if layout == nil {
			return nil, nil, false
		}
		lpkg, err := imp.Import(layout.Pkg.DeclImportPath(in.Options.Module))
		if err != nil || lpkg == nil {
			return nil, nil, false
		}
		obj, _ := lpkg.Scope().Lookup("Props").(*types.TypeName)
		if obj == nil {
			return nil, nil, false
		}
		st, ok := obj.Type().Underlying().(*types.Struct)
		if !ok {
			return nil, nil, false
		}
		c := &converter{layoutPkg: lpkg, layout: layout.Name, aliases: map[*types.Package]string{}}
		fields := topFields(st)
		hfield := fieldByName(fields, "Headings")
		headings := hfield != nil && isHeadings(hfield.Type())
		var assigns []assign
		for _, f := range d.Fields {
			if headings && compiler.NormalizeName(f.Key) == "headings" {
				c.diags = append(c.diags, headingsConflict(f, layout.Name))
				continue
			}
			field := matchField(fields, f.Key)
			if field == nil {
				c.errorf(f.KeyPos, "unknown frontmatter key %q for layout %s (%s)", f.Key, layout.Name, fieldList("props", layout.Name, fields))
				continue
			}
			code, ok := c.convert(f.Value, field.Type(), f.KeyPos, f.Key, layout.Name+".Props."+field.Name())
			if ok {
				assigns = append(assigns, assign{pos: f.KeyPos, code: "p." + field.Name() + " = " + code})
			}
		}
		if headings {
			assigns = append(assigns, assign{pos: -1, code: "p.Headings = Headings"})
		}
		if len(c.diags) > 0 {
			return pass1, c.diags, true
		}
		impl := genImpl(d, in, layout, assigns, c.imports)
		return &compiler.Output{Decl: pass1.Decl, Impl: impl, Diagnostics: pass1.Diagnostics}, nil, true
	}
}

// converter converts the values of one file.
type converter struct {
	layoutPkg *types.Package
	layout    string // the layout's name in messages ("Post")
	aliases   map[*types.Package]string
	imports   []string
	diags     []compiler.Diagnostic
}

func (c *converter) errorf(off int, format string, args ...any) {
	c.diags = append(c.diags, compiler.Diagnostic{Severity: compiler.SeverityError,
		Span: compiler.Span{Start: off, End: off}, Message: fmt.Sprintf(format, args...)})
}

// qual names packages in generated code, adding their imports.
func (c *converter) qual(p *types.Package) string {
	switch {
	case p == c.layoutPkg:
		return LayoutAlias
	case p.Path() == compiler.RuntimePath:
		return compiler.NameRuntime
	}
	if a, ok := c.aliases[p]; ok {
		return a
	}
	a := "orbit__p" + strconv.Itoa(len(c.aliases))
	if p.Path() == "time" {
		a = "time"
		c.imports = append(c.imports, `"time"`)
	} else {
		c.imports = append(c.imports, a+" "+strconv.Quote(p.Path()))
	}
	c.aliases[p] = a
	return a
}

// msgQual names packages in messages: the layout's by its name.
func (c *converter) msgQual(p *types.Package) string {
	if p == c.layoutPkg {
		return c.layout
	}
	return p.Name()
}

func (c *converter) typeString(t types.Type) string { return types.TypeString(t, c.qual) }
func (c *converter) msgType(t types.Type) string    { return types.TypeString(t, c.msgQual) }

// convert returns the Go expression of v as a t. keyPos and key are the
// frontmatter key (for errors about the field type), label the field as a
// message names it ("Post.Props.Tags").
func (c *converter) convert(v *Value, t types.Type, keyPos int, key, label string) (string, bool) {
	mismatch := func(want string) (string, bool) {
		c.errorf(v.Pos, "frontmatter %s: %s isn't %s (%s is %s)", key, describe(v), want, label, c.msgType(t))
		return "", false
	}
	unsupported := func() (string, bool) {
		c.errorf(keyPos, "frontmatter %s: %s is %s, which frontmatter can't set", key, label, c.msgType(t))
		return "", false
	}
	if isNamed(t, "time", "Time") {
		if v.Kind != KindTime {
			return mismatch("a date")
		}
		return timeLit(v.Time, c.qual(t.(*types.Named).Obj().Pkg())), true
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		info := u.Info()
		switch {
		case info&types.IsString != 0:
			if v.Kind == KindList || v.Kind == KindMap {
				return mismatch("a string")
			}
			return strconv.Quote(v.Raw), true
		case info&types.IsBoolean != 0:
			if v.Kind != KindBool {
				return mismatch("a bool")
			}
			return strconv.FormatBool(v.Bool), true
		case info&types.IsInteger != 0:
			if v.Kind != KindInt {
				return mismatch("an integer")
			}
			if !intFits(v.Int, u.Kind()) {
				c.errorf(v.Pos, "frontmatter %s: %s overflows %s (%s is %s)", key, v.Raw, u.Name(), label, c.msgType(t))
				return "", false
			}
			return strconv.FormatInt(v.Int, 10), true
		case info&types.IsFloat != 0:
			switch v.Kind {
			case KindInt:
				return strconv.FormatInt(v.Int, 10), true
			case KindFloat:
				if u.Kind() == types.Float32 && math.Abs(v.Float) > math.MaxFloat32 {
					c.errorf(v.Pos, "frontmatter %s: %s overflows float32 (%s is %s)", key, v.Raw, label, c.msgType(t))
					return "", false
				}
				return floatLit(v.Float), true
			}
			return mismatch("a number")
		}
		return unsupported()
	case *types.Slice:
		if v.Kind != KindList {
			return mismatch("a list")
		}
		var b strings.Builder
		b.WriteString(c.typeString(t) + "{")
		ok := true
		for i, it := range v.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			idx := "[" + strconv.Itoa(i) + "]"
			s, iok := c.convert(it, u.Elem(), keyPos, key+idx, label+idx)
			ok = ok && iok
			b.WriteString(s)
		}
		b.WriteString("}")
		return b.String(), ok
	case *types.Map:
		if kb, isBasic := u.Key().Underlying().(*types.Basic); !isBasic || kb.Info()&types.IsString == 0 {
			return unsupported()
		}
		if v.Kind != KindMap {
			return mismatch("a map")
		}
		var b strings.Builder
		b.WriteString(c.typeString(t) + "{")
		ok := true
		for i, f := range v.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			s, fok := c.convert(f.Value, u.Elem(), f.KeyPos, key+"."+f.Key, label+"["+strconv.Quote(f.Key)+"]")
			ok = ok && fok
			b.WriteString(strconv.Quote(f.Key) + ": " + s)
		}
		b.WriteString("}")
		return b.String(), ok
	case *types.Struct:
		if v.Kind != KindMap {
			return mismatch("a map")
		}
		fields := directFields(u)
		var b strings.Builder
		b.WriteString(c.typeString(t) + "{")
		ok := true
		n := 0
		for _, f := range v.Fields {
			field := matchField(fields, f.Key)
			if field == nil {
				c.errorf(f.KeyPos, "frontmatter %s: unknown key %q for %s (%s)", key, f.Key, label, fieldList("fields", label, fields))
				ok = false
				continue
			}
			s, fok := c.convert(f.Value, field.Type(), f.KeyPos, key+"."+f.Key, label+"."+field.Name())
			ok = ok && fok
			if n > 0 {
				b.WriteString(", ")
			}
			n++
			b.WriteString(field.Name() + ": " + s)
		}
		b.WriteString("}")
		return b.String(), ok
	}
	return unsupported()
}

func intFits(n int64, k types.BasicKind) bool {
	switch k {
	case types.Int8:
		return n >= math.MinInt8 && n <= math.MaxInt8
	case types.Int16:
		return n >= math.MinInt16 && n <= math.MaxInt16
	case types.Int32:
		return n >= math.MinInt32 && n <= math.MaxInt32
	case types.Uint8:
		return n >= 0 && n <= math.MaxUint8
	case types.Uint16:
		return n >= 0 && n <= math.MaxUint16
	case types.Uint32:
		return n >= 0 && n <= math.MaxUint32
	case types.Uint, types.Uint64, types.Uintptr:
		return n >= 0
	}
	return true // int, int64 (YAML ints are read as int64)
}

func isNamed(t types.Type, pkg, name string) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}

// isHeadings reports whether t is []orbit.Heading.
func isHeadings(t types.Type) bool {
	s, ok := t.(*types.Slice)
	return ok && isNamed(s.Elem(), compiler.RuntimePath, "Heading")
}

// directFields are a struct's exported fields (embedded ones by their type
// name), which a composite literal can set.
func directFields(st *types.Struct) []*types.Var {
	var out []*types.Var
	for f := range st.Fields() {
		if f.Exported() {
			out = append(out, f)
		}
	}
	return out
}

// topFields are the fields a p.X = assignment can set: the direct ones,
// then the promoted fields of embedded (non-pointer) structs, shallower
// first.
func topFields(st *types.Struct) []*types.Var {
	out := directFields(st)
	seen := map[string]bool{}
	for _, f := range out {
		seen[f.Name()] = true
	}
	queue := []*types.Struct{}
	for f := range st.Fields() {
		if f.Embedded() {
			if s, ok := f.Type().Underlying().(*types.Struct); ok {
				queue = append(queue, s)
			}
		}
	}
	for depth := 0; len(queue) > 0 && depth < 8; depth++ {
		var next []*types.Struct
		for _, s := range queue {
			for f := range s.Fields() {
				if f.Exported() && !seen[f.Name()] {
					seen[f.Name()] = true
					out = append(out, f)
				}
				if f.Embedded() {
					if es, ok := f.Type().Underlying().(*types.Struct); ok {
						next = append(next, es)
					}
				}
			}
		}
		queue = next
	}
	return out
}

func fieldByName(fields []*types.Var, name string) *types.Var {
	for _, f := range fields {
		if f.Name() == name {
			return f
		}
	}
	return nil
}

// matchField matches a key to a field as attributes are matched to props
// (ignoring case, - and _).
func matchField(fields []*types.Var, key string) *types.Var {
	k := compiler.NormalizeName(key)
	for _, f := range fields {
		if compiler.NormalizeName(f.Name()) == k {
			return f
		}
	}
	return nil
}

// fieldList lists fields as the unknown-prop error does.
func fieldList(word, tag string, fields []*types.Var) string {
	if len(fields) == 0 {
		return tag + " takes no props"
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = compiler.LowerCamel(f.Name())
	}
	return word + ": " + strings.Join(names, ", ")
}
