package typed

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each case is an expression (with helper declarations in decls) and the
// expected typed write ("Kind" or "Kind/conv").
var cases = []struct{ expr, want string }{
	{`s`, "String"},
	{`post.Title`, "String"},
	{`slug`, "String/string"},        // type Slug string, no methods
	{`tag`, "Stringer"},              // type Tag string with String()
	{`n`, "Int/int64"},               // int
	{`i64`, "Int"},                   // int64
	{`u8`, "Uint/uint64"},            // uint8
	{`by`, "Uint/uint64"},            // byte
	{`status`, "Stringer"},           // type Status int with String()
	{`count`, "Int/int64"},           // type Count int, no methods
	{`ok`, "Bool"},                   // bool
	{`flag`, "Bool/bool"},            // type Flag bool
	{`f64`, "Float"},                 // float64
	{`f32`, "Float/float64"},         // float32
	{`price`, "Float/float64"},       // type Price float64: fmt %v == strconv g -1 64
	{`ratio`, "Expr"},                // type Ratio float32: fmt uses bitSize 32, Expr widens
	{`raw`, "Bytes"},                 // []byte
	{`rawNamed`, "Expr"},             // type RawBytes []byte: fmt prints [..]
	{`lyr.HTML("<b>x</b>")`, "HTML"}, // constant of type HTML
	{`h`, "HTML"},                    // HTML var
	{`lyr.URL("/a?b&c")`, "String/string"},
	{`created`, "Stringer"},   // time.Time
	{`author`, "Stringer"},    // *Author, pointer-receiver String
	{`nilAuthor`, "Stringer"}, // nil *Author: Expr's nil case is for nil interfaces only
	{`*author`, "Expr"},       // Author value: String is on *Author, fmt prints {..}
	{`author.Name`, "String"}, // field through a pointer
	{`bdg`, "Stringer"},       // value-receiver String
	{`&bdg`, "Stringer"},      // *badge has it too
	{`err`, "Error"},          // nil error
	{`err2`, "Error"},         // non-nil error
	{`myErr`, "Error"},        // *MyErr: Error and String, error wins
	{`code`, "Error"},         // type ErrCode string with Error()
	{`stringerIface`, "Expr"}, // static fmt.Stringer: dynamic value may be an error
	{`errStringer`, "Error"},  // interface embedding error and String
	{`anyV`, "Expr"},          // any
	{`rend`, "Render"},        // lyr.Renderer
	{`func(w *lyr.Writer) error { w.Raw("<i>r</i>"); return nil }`, "Render/lyr.Renderer"},
	{`"a < b"`, "String"},            // untyped string constant
	{`42`, "Int/int64"},              // untyped int constant
	{`'x'`, "Int/int64"},             // untyped rune -> int32
	{`3.5`, "Float"},                 // untyped float -> float64
	{`Published`, "Stringer"},        // typed constant of Status
	{`nil`, "skip"},                  // untyped nil
	{`sp`, "Expr"},                   // *string: fmt prints an address
	{`tags`, "Expr"},                 // []string: handled by Expr at runtime
	{`len(tags)`, "Int/int64"},       //
	{`post.URL()`, "String"},         // method call
	{`m["k"]`, "String"},             // map index
	{`n + 1`, "Int/int64"},           //
	{`up`, "Expr"},                   // uintptr
	{`c128`, "Expr"},                 // complex128
	{`strings.ToUpper(s)`, "String"}, //
	{`fmt.Sprint(n)`, "String"},
	{`alias`, "String"},  // type AliasStr = string
	{`aliasAny`, "Expr"}, // type AliasAny = any      //
}

const decls = `package t

import (
	"errors"
	"fmt"
	"strings"
	"time"

	lyr "github.com/bertrandkhe/lyr"
)

type Slug string
type Tag string

func (t Tag) String() string { return "#" + string(t) }

type Status int

const Published Status = 1

func (s Status) String() string { return [...]string{"draft", "published"}[s] }

type Count int
type Flag bool
type Price float64
type Ratio float32
type AliasStr = string
type AliasAny = any
type RawBytes []byte
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

func cases(lyr__w *lyr.Writer) {
	s := "Tom & Jerry"
	post := Post{Title: "Hi <there>"}
	slug := Slug("a&b")
	tag := Tag("go")
	n := 1234567
	i64 := int64(-5)
	u8 := uint8(200)
	by := byte('A')
	status := Status(0)
	count := Count(77)
	ok := true
	flag := Flag(false)
	f64 := 1234567.0
	f32 := float32(0.1)
	price := Price(1234567)
	ratio := Ratio(0.1)
	var alias AliasStr = "al<"
	var aliasAny AliasAny = 3
	raw := []byte("<raw>")
	rawNamed := RawBytes("ab")
	h := lyr.HTML("<hr>")
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
	var rend lyr.Renderer = func(w *lyr.Writer) error { w.Raw("<r/>"); return nil }
	str := "p"
	sp := &str
	tags := []string{"a", "b"}
	m := map[string]string{"k": "v<"}
	up := uintptr(7)
	c128 := complex(1, 2)
	_, _, _, _, _, _, _, _, _, _, _, _, _, _, _ = s, post, slug, tag, n, i64, u8, by, status, count, ok, flag, f64, f32, price
	_, _, _, _, _, _, _, _, _, _, _, _, _, _, _ = raw, rawNamed, h, created, author, nilAuthor, bdg, err, err2, myErr, code, stringerIface, errStringer, anyV, rend
	_, _, _, _, _, _, _, _ = sp, tags, m, up, c128, ratio, alias, aliasAny
`

func source() string {
	var b strings.Builder
	b.WriteString(decls)
	for _, c := range cases {
		fmt.Fprintf(&b, "\tlyr__w.Raw(\"\\n\")\n\tlyr__w.Expr(%s)\n", c.expr)
	}
	b.WriteString("}\n")
	return b.String()
}

func stubDir(t *testing.T) string {
	d, err := filepath.Abs("../lyrstub")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func checkStub(t *testing.T, fset *token.FileSet) *types.Package {
	f, err := parser.ParseFile(fset, filepath.Join(stubDir(t), "lyr.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("github.com/bertrandkhe/lyr", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

type mapImporter struct {
	lyr *types.Package
	def types.Importer
}

func (m mapImporter) Import(path string) (*types.Package, error) {
	if path == lyrPath {
		return m.lyr, nil
	}
	return m.def.Import(path)
}

func TestClassify(t *testing.T) {
	fset := token.NewFileSet()
	lyr := checkStub(t, fset)
	src := source()
	f, err := parser.ParseFile(fset, "t.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: mapImporter{lyr, importer.Default()}}
	if _, err := conf.Check("t", fset, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	env := NewEnv(lyr)
	i := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Expr" {
			return true
		}
		d := env.Classify(info.Types[call.Args[0]])
		got := d.Kind.String()
		if d.Conv != "" {
			got += "/" + d.Conv
		}
		if got != cases[i].want {
			t.Errorf("%-40s got %-22s want %s", cases[i].expr, got, cases[i].want)
		}
		i++
		return true
	})
	if i != len(cases) {
		t.Fatalf("found %d Expr calls, want %d", i, len(cases))
	}

	// End to end: rewrite with typed writes, run both versions, compare bytes.
	stats := Stats{}
	out := Rewrite(fset, []*ast.File{f}, info, env, map[string][]byte{"t.go": []byte(src)}, stats)
	typedSrc := string(out["t.go"])
	exprOut := runCases(t, src)
	typedOut := runCases(t, typedSrc)
	if exprOut != typedOut {
		el, tl := strings.Split(exprOut, "\n"), strings.Split(typedOut, "\n")
		for j := range el {
			if j < len(tl) && el[j] != tl[j] && !strings.HasPrefix(el[j], "0x") { // *string prints its address
				t.Errorf("case %q: Expr wrote %q, typed wrote %q", cases[j-1].expr, el[j], tl[j])
			}
		}
	}
	t.Logf("decisions: %v", stats)
	t.Logf("output:\n%s", exprOut)
}

// runCases builds a tiny program around cases() and returns what it writes.
func runCases(t *testing.T, src string) string {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module t\n\ngo 1.27\n\ntoolchain go1.27.3\n\nrequire github.com/bertrandkhe/lyr v0.0.0\n\nreplace github.com/bertrandkhe/lyr => "+stubDir(t)+"\n")
	write("t.go", strings.Replace(src, "package t", "package main", 1)+`
func main() {
	var buf bytes.Buffer
	cases(lyr.NewWriter(&buf))
	os.Stdout.Write(buf.Bytes())
}
`)
	write("x.go", "package main\n\nimport (\n\t\"bytes\"\n\t\"os\"\n)\n\nvar _ = bytes.NewBuffer\nvar _ = os.Stdout\n")
	// main() needs bytes/os imported in t.go's file; add them there.
	b, _ := os.ReadFile(filepath.Join(dir, "t.go"))
	write("t.go", strings.Replace(string(b), "import (", "import (\n\t\"bytes\"\n\t\"os\"", 1))
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s\n--- src:\n%s", err, out, src)
	}
	return string(out)
}
