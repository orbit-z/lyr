package compiler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// tyCases are the expression kinds of the typed-write table, ported from
// spike/typecheck/typed with the float32 change (bitSize 32, so named
// float32 types are eligible) and the attribute kinds. attr is "" for a
// content site ({expr}); want is "Method/conv", "skip", or Expr/Attr for a
// fallback.
var tyCases = []struct{ attr, expr, want string }{
	{"", `s`, "String"},
	{"", `post.Title`, "String"},
	{"", `slug`, "String/string"},  // type Slug string, no methods
	{"", `tag`, "Stringer"},        // type Tag string with String()
	{"", `n`, "Int/int64"},         // int
	{"", `i64`, "Int"},             // int64
	{"", `u8`, "Uint/uint64"},      // uint8
	{"", `by`, "Uint/uint64"},      // byte
	{"", `status`, "Stringer"},     // type Status int with String()
	{"", `count`, "Int/int64"},     // type Count int, no methods
	{"", `ok`, "Bool"},             // bool
	{"", `flag`, "Bool/bool"},      // type Flag bool
	{"", `f64`, "Float"},           // float64
	{"", `f32`, "Float32"},         // float32: bitSize 32, prints 0.1
	{"", `price`, "Float/float64"}, // type Price float64: fmt %v == strconv g -1 64
	{"", `ratio`, "Float32/float32"},
	{"", `float32(0.1)`, "Float32"}, // typed constant
	{"", `raw`, "Bytes"},            // []byte
	{"", `rawNamed`, "Expr"},        // type RawBytes []byte: fmt prints [..]
	{"", `orbit.HTML("<b>x</b>")`, "HTML"},
	{"", `h`, "HTML"},
	{"", `orbit.URL("/a?b&c")`, "String/string"},
	{"", `created`, "Stringer"},   // time.Time
	{"", `author`, "Stringer"},    // *Author, pointer-receiver String
	{"", `nilAuthor`, "Stringer"}, // nil *Author: Expr's nil case is for nil interfaces only
	{"", `*author`, "Expr"},       // Author value: String is on *Author, fmt prints {..}
	{"", `author.Name`, "String"}, // field through a pointer
	{"", `bdg`, "Stringer"},       // value-receiver String
	{"", `&bdg`, "Stringer"},      // *badge has it too
	{"", `err`, "Error"},          // nil error
	{"", `err2`, "Error"},         // non-nil error
	{"", `myErr`, "Error"},        // *MyErr: Error and String, error wins
	{"", `code`, "Error"},         // type ErrCode string with Error()
	{"", `stringerIface`, "Expr"}, // static fmt.Stringer: the dynamic value may be an error
	{"", `errStringer`, "Error"},  // interface embedding error and String
	{"", `anyV`, "Expr"},          // any
	{"", `rend`, "Render"},        // orbit.Renderer
	{"", `func(w *orbit.Writer) error { w.Raw("<i>r</i>"); return nil }`, "Render/orbit__rt.Renderer"},
	{"", `"a < b"`, "String"},     // untyped string constant
	{"", `42`, "Int/int64"},       // untyped int constant
	{"", `'x'`, "Int/int64"},      // untyped rune → int32
	{"", `3.5`, "Float"},          // untyped float → float64
	{"", `true`, "Bool"},          // untyped bool
	{"", `Published`, "Stringer"}, // typed constant of Status
	{"", `nil`, "skip"},           // untyped nil
	{"", `sp`, "Expr"},            // *string: fmt prints an address
	{"", `tags`, "Expr"},          // []string: Expr handles it at runtime
	{"", `len(tags)`, "Int/int64"},
	{"", `post.URL()`, "String"}, // method call
	{"", `m["k"]`, "String"},     // map index
	{"", `n + 1`, "Int/int64"},
	{"", `up`, "Expr"},   // uintptr (PLAN.md §3.3 keeps it on Expr)
	{"", `c128`, "Expr"}, // complex128
	{"", `strings.ToUpper(s)`, "String"},
	{"", `fmt.Sprint(n)`, "String"},
	{"", `alias`, "String"},         // type AliasStr = string
	{"", `aliasAny`, "Expr"},        // type AliasAny = any
	{"", `fmtd`, "Expr"},            // type Fmtd int with Format: fmt.Sprint calls it
	{"", `myHTML`, "String/string"}, // type MyHTML orbit.HTML: not HTML to Expr, escaped
	{"", `nilRend`, "Render"},       // nil Renderer writes nothing either way

	{"title", `s`, "attr:String"},
	{"href", `s`, "AttrURL"},
	{"href", `"javascript:alert(1)"`, "AttrURL"},
	{"title", `"a<b\"c&d"`, "attr:String"},
	{"title", `slug`, "attr:String/string"},
	{"href", `jsSlug`, "AttrURL/string"}, // a named string is filtered too
	{"title", `tag`, "Attr"},             // named string with String()
	{"title", `code`, "Attr"},            // named string with Error()
	{"title", `fmts`, "Attr"},            // named string with Format()
	{"title", `h`, "attr:String/string"},
	{"href", `orbit.HTML("javascript:x()")`, "AttrURL/string"},
	{"href", `orbit.URL("javascript:ok()")`, "attr:String/string"}, // trusted: unfiltered
	{"hidden", `ok`, "AttrBool"},
	{"hidden", `!ok`, "AttrBool"},
	{"hidden", `true`, "AttrBool"},
	{"hidden", `flag`, "AttrBool/bool"},
	{"hidden", `onOff`, "AttrBool/bool"}, // named bool with String(): Attr checks the kind
	{"data-x", `nil`, "skip"},
	{"data-n", `n`, "attr:Int/int64"},
	{"data-n", `i64`, "attr:Int"},
	{"data-n", `u8`, "attr:Uint/uint64"},
	{"data-n", `count`, "attr:Int/int64"}, // type Count int, no methods
	{"data-n", `status`, "Attr"},          // type Status int with String()
	{"data-n", `fmtd`, "Attr"},            // type Fmtd int with Format
	{"data-n", `up`, "Attr"},              // uintptr stays on Attr, as in content
	{"data-n", `42`, "attr:Int/int64"},
	{"data-n", `'x'`, "attr:Int/int64"},
	{"data", `n`, "attr:Int/int64"}, // a URL attribute: digits are never filtered
	{"data-f", `f32`, "attr:Float32"},
	{"data-f", `f64`, "attr:Float"},
	{"data-f", `3.5`, "attr:Float"},
	{"data-f", `price`, "attr:Float/float64"},
	{"data-f", `ratio`, "attr:Float32/float32"},
	{"data-f", `c128`, "Attr"},
	{"title", `anyV`, "Attr"},
	{"title", `alias`, "attr:String"},
	{"title", `tags`, "Attr"},
}

const tyCaseDecls = `
type Slug string
type Tag string

func (t Tag) String() string { return "#" + string(t) }

type Status int

const Published Status = 1

func (s Status) String() string { return [...]string{"draft", "published"}[s] }

type Count int
type Flag bool
type OnOff bool

func (o OnOff) String() string { return "on/off" }

type Price float64
type Ratio float32
type AliasStr = string
type AliasAny = any
type RawBytes []byte
type MyHTML orbit.HTML
type Fmtd int

func (Fmtd) Format(f fmt.State, verb rune) { fmt.Fprint(f, "formatted") }

type Fmts string

func (Fmts) Format(f fmt.State, verb rune) { fmt.Fprint(f, "formatted") }

type Author struct{ Name string }

func (a *Author) String() string {
	if a == nil {
		return "anon"
	}
	return "<" + a.Name + ">"
}

type badge struct{ L string }

func (b badge) String() string { return "[" + b.L + "]" }

type MyErr struct{}

func (*MyErr) Error() string  { return "my error <e>" }
func (*MyErr) String() string { return "my stringer" }

type ErrCode string

func (c ErrCode) Error() string { return "code " + string(c) }

type Post struct{ Title string }

func (p Post) URL() string { return "/p/" + p.Title }

var (
	_ = errors.New
	_ = fmt.Sprint
	_ = strings.ToUpper
	_ = time.Now
)
`

const tyCaseVars = `	s := "Tom & Jerry"
	post := Post{Title: "Hi <there>"}
	slug := Slug("a&b")
	jsSlug := Slug("javascript:x()")
	tag := Tag("go")
	n := 1234567
	i64 := int64(-5)
	u8 := uint8(200)
	by := byte('A')
	status := Status(0)
	count := Count(77)
	ok := true
	flag := Flag(false)
	onOff := OnOff(true)
	f64 := 1234567.0
	f32 := float32(0.1)
	price := Price(1234567)
	ratio := Ratio(0.1)
	var alias AliasStr = "al<"
	var aliasAny AliasAny = 3
	raw := []byte("<raw>")
	rawNamed := RawBytes("ab")
	h := orbit.HTML("<hr>")
	myHTML := MyHTML("<em>")
	created := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	author := &Author{Name: "Ann"}
	var nilAuthor *Author
	bdg := badge{L: "x"}
	var err error
	err2 := errors.New("bad <thing>")
	myErr := &MyErr{}
	code := ErrCode("E1")
	var stringerIface fmt.Stringer = myErr
	var errStringer interface {
		error
		String() string
	} = myErr
	var anyV any = Slug("s")
	var rend orbit.Renderer = func(w *orbit.Writer) error { w.Raw("<r/>"); return nil }
	var nilRend orbit.Renderer
	str := "p"
	sp := &str
	tags := []string{"a", "b<"}
	m := map[string]string{"k": "v<"}
	up := uintptr(7)
	c128 := complex(1, 2)
	fmtd := Fmtd(3)
	fmts := Fmts("x")
	_, _, _, _, _, _, _, _, _, _, _, _, _, _, _ = s, post, slug, tag, n, i64, u8, by, status, count, ok, flag, f64, f32, price
	_, _, _, _, _, _, _, _, _, _, _, _, _, _, _ = raw, rawNamed, h, created, author, nilAuthor, bdg, err, err2, myErr, code, stringerIface, errStringer, anyV, rend
	_, _, _, _, _, _, _, _, _, _, _, _, _, _ = sp, tags, m, up, c128, ratio, alias, aliasAny, fmtd, fmts, myHTML, nilRend, jsSlug, onOff
`

// tyCasesComp is the cases as one component at orbit.
func tyCasesComp(orbit string) tyComp {
	var body strings.Builder
	body.WriteString(tyCaseVars)
	for _, c := range tyCases {
		body.WriteString("\torbit__w.Raw(\"\\n\")\n")
		if c.attr == "" {
			fmt.Fprintf(&body, "\torbit__w.Expr(\n%s,\n\t)\n", c.expr)
		} else {
			fmt.Fprintf(&body, "\torbit__w.Attr(%s,\n%s,\n\t)\n", strconv.Quote(c.attr), c.expr)
		}
	}
	return tyComp{
		orbit:       orbit,
		implImports: "\t\"errors\"\n\t\"fmt\"\n\t\"strings\"\n\t\"time\"\n\torbit \"" + RuntimePath + "\"\n",
		implTop:     tyCaseDecls,
		body:        body.String(),
	}
}

// TestClassify checks the write chosen for every kind through the Checker,
// then renders the cases with the real runtime both ways (Expr, and typed
// re-emitted) and compares the bytes.
func TestClassify(t *testing.T) {
	exprU := tyFixture(t, tyCasesComp("src/components/Cases.orbit"))
	typedU := tyFixture(t, tyCasesComp("src/components/TypedCases.orbit"))
	g := newTyGen(exprU, typedU)
	c := NewChecker(&tyTestLoader{})
	c.generate = g.generate
	res := tyCheck(t, c, []Unit{exprU, typedU})
	if len(res.Warnings) > 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	tu := res.Units[1]
	if !tu.Checked {
		t.Fatal("cases unit not checked")
	}
	got := tyGot(typedU, tu)
	if len(got) != len(tyCases) {
		t.Fatalf("%d call sites, want %d", len(got), len(tyCases))
	}
	for i, c := range tyCases {
		if got[i] != c.want {
			t.Errorf("%s %-40s got %-26s want %s", c.attr, c.expr, got[i], c.want)
		}
	}

	m := newGenModule(t)
	m.writeOutput(exprU.Pass1)
	m.writeOutput(tu.Output)
	m.buildAndVet()
	out := m.run(`package main

import (
	"bytes"
	"fmt"

	"github.com/bertrandkhe/orbit"
	cases "example.com/blog/orbit_gen/decl/components/cases"
	typedcases "example.com/blog/orbit_gen/decl/components/typedcases"
	_ "example.com/blog/orbit_gen/impl/components/cases"
	_ "example.com/blog/orbit_gen/impl/components/typedcases"
)

func main() {
	var a, b bytes.Buffer
	if err := cases.Render(&orbit.Ctx{}, orbit.NewWriter(&a), cases.Props{}, orbit.Slots{}); err != nil {
		panic(err)
	}
	if err := typedcases.Render(&orbit.Ctx{}, orbit.NewWriter(&b), typedcases.Props{}, orbit.Slots{}); err != nil {
		panic(err)
	}
	fmt.Print(a.String(), "\x00", b.String())
}
`)
	exprOut, typedOut, _ := strings.Cut(out, "\x00")
	el, tl := strings.Split(exprOut, "\n"), strings.Split(typedOut, "\n")
	if len(el) != len(tyCases)+1 || len(tl) != len(el) {
		t.Fatalf("got %d and %d output lines, want %d:\n%s\n---\n%s", len(el), len(tl), len(tyCases)+1, exprOut, typedOut)
	}
	for i, c := range tyCases {
		e, ty := el[i+1], tl[i+1]
		if c.expr == "sp" { // *string prints its address
			continue
		}
		if e != ty {
			t.Errorf("%s %s: Expr wrote %q, typed (%s) wrote %q", c.attr, c.expr, e, c.want, ty)
		}
	}
	if el[14] != "0.1" { // f32
		t.Errorf("float32(0.1) wrote %q, want 0.1", el[14])
	}
	t.Logf("output:\n%s", exprOut)
}

// TestClassifyNoRuntime: without the runtime package, nothing is typed.
func TestClassifyNoRuntime(t *testing.T) {
	u := tyFixture(t, tyComp{orbit: "src/components/A.orbit", body: "\torbit__w.Expr(\n\"x\",\n\t)\n"})
	l := &tyTestLoader{fail: map[string]bool{RuntimePath: true}}
	res := tyCheck(t, NewChecker(l), []Unit{u})
	if res.Units[0].Checked || res.Units[0].Output != u.Pass1 {
		t.Errorf("unit checked without the runtime")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], RuntimePath) {
		t.Errorf("warnings = %q", res.Warnings)
	}
	_ = context.Background
}
