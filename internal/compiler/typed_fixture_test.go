package compiler

// Hand-written pass-1 files in the generated layout (PLAN.md §3.2), for the
// typed-pass and cleanup tests before codegen (B) lands.

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// tyResetMark in fixture source becomes a /*line <base>:L:C*/ reset naming
// the true position of the next byte.
const tyResetMark = "@R@"

// tyLineMark at the start of a line becomes an own-line //line <base>:N:1
// reset, N being the true next line.
const tyLineMark = "@L@"

// tyResolveResets replaces every tyResetMark and tyLineMark in src (a file
// named base).
func tyResolveResets(src, base string) string {
	var b strings.Builder
	line, col := 1, 1
	for i := 0; i < len(src); {
		if strings.HasPrefix(src[i:], tyLineMark) {
			fmt.Fprintf(&b, "//line %s:%d:1", base, line+1)
			i += len(tyLineMark)
			continue
		}
		if strings.HasPrefix(src[i:], tyResetMark) {
			c := col
			var com string
			for {
				com = fmt.Sprintf("/*line %s:%d:%d*/", base, line, c)
				if col+len(com) == c {
					break
				}
				c = col + len(com)
			}
			b.WriteString(com)
			col += len(com)
			i += len(tyResetMark)
			continue
		}
		b.WriteByte(src[i])
		if src[i] == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
		i++
	}
	return b.String()
}

// tyComp describes one hand-written component.
type tyComp struct {
	orbit string // "src/components/Card.orbit"
	// declImports and implImports are extra import specs, one per line.
	declImports, implImports string
	// declBody is decl code (Props and its types); empty gives
	// type Props struct{}.
	declBody string
	// implTop is hoisted impl code; body is the render body.
	implTop, body string
	page          bool
}

// tyFixture builds the pass-1 Unit of c: both files and the call sites,
// found as the orbit__w.Expr and orbit__w.Attr calls of the impl file.
func tyFixture(t testing.TB, c tyComp) Unit {
	t.Helper()
	pkg, err := PackageFor(c.orbit)
	if err != nil {
		t.Fatal(err)
	}
	declPath := pkg.DeclImportPath(testModule)
	props := c.declBody
	if !strings.Contains(props, "type Props") {
		props += "\ntype Props struct{}\n"
	}
	hook, wrapper := "", "Orbit__Render(orbit__ctx, orbit__w, orbit__props, orbit__slots)"
	implParams := ""
	if c.page {
		props += "\ntype Params struct{}\n"
		hook, wrapper = ", Params", "Orbit__Render(orbit__ctx, orbit__w, orbit__props, orbit__slots, Params{})"
		implParams = ", orbit__params Params"
	}
	decl := GenHeader + "\n\npackage " + pkg.Name + "\n\nimport (\n\torbit__rt \"" + RuntimePath + "\"\n" + c.declImports + ")\n" +
		props + "\nvar Orbit__Render func(*orbit__rt.Ctx, *orbit__rt.Writer, Props, orbit__rt.Slots" + hook + ") error\n\n" +
		"func Render(orbit__ctx *orbit__rt.Ctx, orbit__w *orbit__rt.Writer, orbit__props Props, orbit__slots orbit__rt.Slots) error {\n" +
		"\tif Orbit__Render == nil {\n\t\tpanic(\"orbit: " + c.orbit + " not linked; import " + testModule + "/orbit_gen/app\")\n\t}\n" +
		"\treturn " + wrapper + "\n}\n"
	names := "ctx, props, slots"
	if c.page {
		names += ", params"
	}
	bind := names + " := orbit__ctx, orbit__props, orbit__slots"
	if c.page {
		bind += ", orbit__params"
	}
	impl := GenHeader + "\n\npackage " + pkg.ImplName() + "\n\nimport (\n\torbit__rt \"" + RuntimePath + "\"\n\t. \"" + declPath + "\"\n" + c.implImports + ")\n" +
		c.implTop + "\nvar _ = func() int { Orbit__Render = orbit__render; return 0 }()\n\n" +
		"func orbit__render(orbit__ctx *orbit__rt.Ctx, orbit__w *orbit__rt.Writer, orbit__props Props, orbit__slots orbit__rt.Slots" + implParams + ") error {\n" +
		"\tif orbit__err := orbit__w.Enter(\"" + c.orbit + "\"); orbit__err != nil {\n\t\treturn orbit__err\n\t}\n\tdefer orbit__w.Leave()\n" +
		"\t" + bind + "\n\tlocals := orbit__rt.LocalsOf[orbit__rt.NoLocals](orbit__ctx)\n\t_, _, _, _ = " + names + ", locals\n" +
		c.body + "\treturn orbit__w.Err()\n}\n"
	if c.page {
		impl = strings.Replace(impl, "_, _, _, _ =", "_, _, _, _, _ =", 1)
	}
	decl = tyResolveResets(decl, DeclFileName)
	impl = tyResolveResets(impl, ImplFileName)
	out := &Output{
		Decl: &GenFile{Path: pkg.DeclFile(), ImportPath: declPath, Package: pkg.Name, Content: []byte(decl)},
		Impl: &GenFile{Path: pkg.ImplFile(), ImportPath: pkg.ImplImportPath(testModule), Package: pkg.ImplName(), Content: []byte(impl)},
	}
	out.CallSites = tySites(t, out.Impl)
	role := RoleComponent
	if c.page {
		role = RolePage
	}
	in := &Input{
		Analysis: &Analysis{Info: FileInfo{Path: c.orbit, Role: role}},
		Options:  Options{Module: testModule},
	}
	return Unit{Input: in, Pass1: out}
}

// tySites lists the call sites of a hand-written impl file: each
// orbit__w.Expr(x) and orbit__w.Attr("name", x), keyed by the argument's Go
// offset (a stand-in for the .orbit offset).
func tySites(t testing.TB, f *GenFile) []CallSite {
	fset := gotoken.NewFileSet()
	af, err := goparser.ParseFile(fset, f.Path, f.Content, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: %v\n%s", f.Path, err, f.Content)
	}
	tf := fset.File(af.Pos())
	var sites []CallSite
	ast.Inspect(af, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != NameW || len(call.Args) == 0 {
			return true
		}
		arg := call.Args[len(call.Args)-1]
		s := CallSite{
			Call: Span{tf.Offset(call.Pos()), tf.Offset(call.End())},
			Arg:  Span{tf.Offset(arg.Pos()), tf.Offset(arg.End())},
		}
		s.Key = s.Arg.Start
		s.Code = GoCode{Span: s.Arg, Text: string(f.Content[s.Arg.Start:s.Arg.End])}
		switch sel.Sel.Name {
		case "Expr":
			s.Kind = SiteContent
		case "Attr":
			s.Kind = SiteAttr
			name, _ := strconv.Unquote(call.Args[0].(*ast.BasicLit).Value)
			s.AttrName, s.URLAttr = name, IsURLAttr(name)
		default:
			return true
		}
		sites = append(sites, s)
		return true
	})
	return sites
}

// tyReemit stands in for pass 2 of Generate in these tests: it patches each
// typed call site of pass1 (B re-emits instead).
func tyReemit(pass1 *Output, typed TypedWrites) *Output {
	src := pass1.Impl.Content
	var b []byte
	last := 0
	for _, s := range pass1.CallSites {
		w, ok := typed[s.Key]
		if !ok {
			continue
		}
		b = append(b, src[last:s.Call.Start]...)
		arg := string(src[s.Arg.Start:s.Arg.End])
		if w.Conv != "" {
			arg = w.Conv + "(" + arg + ")"
		}
		switch {
		case w.Kind == WriteSkip:
		case w.Kind.attrPieces():
			b = append(b, NameW+".Raw("+strconv.Quote(" "+s.AttrName+`="`)+"); "+
				NameW+"."+w.Kind.Method()+"("+arg+"); "+NameW+`.Raw("\"")`...)
		case s.Kind == SiteAttr:
			b = append(b, NameW+"."+w.Kind.Method()+"("+strconv.Quote(s.AttrName)+", "+arg+")"...)
		default:
			b = append(b, NameW+"."+w.Kind.Method()+"("+arg+")"...)
		}
		last = s.Call.End
	}
	b = append(b, src[last:]...)
	impl := *pass1.Impl
	impl.Content = b
	out := *pass1
	out.Impl = &impl
	return &out
}

// tyGen is a Checker.generate stub that re-emits with tyReemit from the
// pass-1 outputs it was given, counting calls.
type tyGen struct {
	mu    sync.Mutex
	pass1 map[string]*Output
	calls int
}

func newTyGen(units ...Unit) *tyGen {
	g := &tyGen{pass1: map[string]*Output{}}
	g.add(units...)
	return g
}

func (g *tyGen) add(units ...Unit) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range units {
		g.pass1[u.Input.Analysis.Info.Path] = u.Pass1
	}
}

func (g *tyGen) generate(in *Input) *Output {
	g.mu.Lock()
	g.calls++
	p := g.pass1[in.Analysis.Info.Path]
	g.mu.Unlock()
	return tyReemit(p, in.Typed)
}

// tyDir is the inline directive to L:C of orbit from orbit's impl file.
func tyDir(orbit string, l, c int) string {
	pkg, _ := PackageFor(orbit)
	return fmt.Sprintf("/*line %s:%d:%d*/", DirectiveFile(pkg.ImplFile(), orbit), l, c)
}

// tyLine is the own-line directive form of tyDir, with its newline.
func tyLine(orbit string, l, c int) string {
	pkg, _ := PackageFor(orbit)
	return fmt.Sprintf("//line %s:%d:%d\n", DirectiveFile(pkg.ImplFile(), orbit), l, c)
}
