package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// anTestInfo returns the FileInfo a testdata/analyze file is analyzed with:
// page_* files are pages (route from anTestRoutes, else "/<name>"), the
// others components under src/components.
func anTestInfo(name string) FileInfo {
	base := strings.TrimSuffix(name, ".orbit")
	if rest, ok := strings.CutPrefix(base, "page_"); ok {
		route, ok := anTestRoutes[rest]
		if !ok {
			route = "/" + rest
		}
		return FileInfo{Path: "src/pages" + route + ".orbit", Role: RolePage, Route: route}
	}
	return FileInfo{Path: "src/components/" + base + ".orbit", Role: RoleComponent}
}

var anTestRoutes = map[string]string{
	"params":          "/blog/[post-id]/[...rest]",
	"params_collide":  "/[post-id]/[postId]",
	"params_badname":  "/[1st]/[é]",
	"params_samename": "/[id]/[id]",
}

func placeString(p Placement) string {
	return [...]string{"none", "decl", "impl", "both"}[p&PlaceBoth]
}

func spanText(src string, s Span) string {
	return sp(s) + " " + q(src[s.Start:s.End])
}

func dumpAnalysis(f *File, a *Analysis) string {
	var b strings.Builder
	src := f.Src
	role := "component"
	if a.Info.Role == RolePage {
		role = "page"
	}
	fmt.Fprintf(&b, "info %s %s route=%q\n", a.Info.Path, role, a.Info.Route)
	for _, i := range a.GoImports {
		fmt.Fprintf(&b, "goimport name=%q path=%q %s path=%s place=%s\n", i.Name, i.Path, spanText(src, i.Span), sp(i.PathSpan), placeString(i.Place))
	}
	for _, o := range a.OrbitImports {
		fmt.Fprintf(&b, "orbitimport %s path=%q resolved=%s %s pathspan=%s place=%s\n",
			spanText(src, o.AliasSpan), o.Path, o.Resolved, sp(o.Span), sp(o.PathSpan), placeString(o.Place))
	}
	fmt.Fprintf(&b, "implicit orbit: %s\n", placeString(a.ImplicitOrbit))
	kinds := [...]string{"type", "const", "func"}
	for _, d := range a.Decls {
		fmt.Fprintf(&b, "decl %s %s place=%s", kinds[d.Kind], sp(d.Span), placeString(d.Place))
		for k, n := range d.Names {
			fmt.Fprintf(&b, " %s%s", n, sp(d.NameSpans[k]))
		}
		if d.Recv != "" {
			fmt.Fprintf(&b, " recv=%s", d.Recv)
		}
		if d.Doc.Len() > 0 {
			fmt.Fprintf(&b, " doc=%s", spanText(src, d.Doc))
		}
		fmt.Fprintf(&b, "\n  %s\n", q(src[d.Span.Start:d.Span.End]))
	}
	for _, s := range a.Body {
		fmt.Fprintf(&b, "stmt %s\n", spanText(src, s.Span))
	}
	fmt.Fprintf(&b, "echo %v\n", a.Echo)
	fmt.Fprintf(&b, "terminating %v\n", a.Terminating)
	p := a.Props
	fmt.Fprintf(&b, "props declared=%v listable=%v span=%s\n", p.Declared, p.Listable, sp(p.Span))
	for _, fl := range p.Fields {
		fmt.Fprintf(&b, "  field %s type=%q doc=%q\n", spanText(src, fl.NameSpan), fl.Type, fl.Doc)
	}
	for _, pf := range a.Params {
		fmt.Fprintf(&b, "param %s [%s] rest=%v\n", pf.Field, pf.Param, pf.Rest)
	}
	return b.String()
}

func dumpAnalysisDiags(f *File, a *Analysis) string {
	var b strings.Builder
	for _, d := range a.Diagnostics {
		b.WriteString(d.Format(a.Info.Path, f.Lines))
		b.WriteByte('\n')
	}
	return b.String()
}

// checkAnalysis verifies the span invariants of an Analysis.
func checkAnalysis(t testing.TB, f *File, a *Analysis) {
	t.Helper()
	n := len(f.Src)
	in := func(what string, s Span) {
		if s.Start < 0 || s.Start > s.End || s.End > n {
			t.Fatalf("%s %v outside the file (len %d)", what, s, n)
		}
	}
	prev := -1
	var regions []Span
	for _, d := range a.Decls {
		in("decl", d.Span)
		in("doc", d.Doc)
		if d.Doc.Len() > 0 && d.Doc.End > d.Span.Start {
			t.Fatalf("doc %v after decl %v", d.Doc, d.Span)
		}
		for _, s := range d.NameSpans {
			in("name", s)
		}
		if d.Place != PlaceDecl && d.Place != PlaceImpl {
			t.Fatalf("decl placed %d", d.Place)
		}
		regions = append(regions, d.Span)
	}
	for _, s := range a.Body {
		in("stmt", s.Span)
		regions = append(regions, s.Span)
	}
	slices.SortFunc(regions, func(x, y Span) int { return x.Start - y.Start })
	for _, r := range regions {
		if r.Start < prev {
			t.Fatalf("overlapping regions at %v", r)
		}
		prev = r.End
	}
	for _, i := range a.GoImports {
		in("import", i.Span)
		in("import path", i.PathSpan)
		if i.Place == 0 {
			t.Fatal("unplaced import")
		}
	}
	for _, o := range a.OrbitImports {
		in("orbit import", o.Span)
		in("alias", o.AliasSpan)
		in("orbit path", o.PathSpan)
	}
	in("props", a.Props.Span)
	for _, fl := range a.Props.Fields {
		in("field", fl.NameSpan)
	}
	for _, d := range a.Diagnostics {
		in("diagnostic", d.Span)
	}
}

func TestAnalyzeGolden(t *testing.T) {
	paths, err := filepath.Glob("testdata/analyze/*.orbit")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(strings.TrimSuffix(name, ".orbit"), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mode := Strict
			if strings.HasPrefix(name, "tolerant_") {
				mode = Tolerant
			}
			f := Parse(string(src), mode)
			a := Analyze(f, anTestInfo(name))
			checkAnalysis(t, f, a)
			base := strings.TrimSuffix(path, ".orbit")
			golden(t, base+".analysis.golden", dumpAnalysis(f, a))
			golden(t, base+".diag.golden", dumpAnalysisDiags(f, a))
		})
	}
}

func anComp(t *testing.T, src string) *Analysis {
	t.Helper()
	f := Parse(src, Strict)
	a := Analyze(f, FileInfo{Path: "src/components/X.orbit"})
	checkAnalysis(t, f, a)
	return a
}

func anMessages(a *Analysis) []string {
	var out []string
	for _, d := range a.Diagnostics {
		out = append(out, d.Message)
	}
	return out
}

func anPlaces(a *Analysis) map[string]string {
	m := map[string]string{}
	for _, d := range a.Decls {
		key := strings.Join(d.Names, ",")
		if d.Recv != "" {
			key = d.Recv + ".method"
		}
		m[key] = placeString(d.Place)
	}
	for _, i := range a.GoImports {
		m["import "+i.Path] = placeString(i.Place)
	}
	for _, o := range a.OrbitImports {
		m["alias "+o.Alias] = placeString(o.Place)
	}
	m["orbit"] = placeString(a.ImplicitOrbit)
	return m
}

func TestAnalyzePlacement(t *testing.T) {
	tests := []struct {
		name, src string
		want      map[string]string
	}{
		{"props closure via methods and consts", `---
import (
	"strings"
	"strconv"
)
type Item struct{ N Kind }
type Kind int
const Max Kind = limit
const limit = 3
func (i Item) Title() string { return strings.ToUpper(name(i.N)) }
func name(k Kind) string { return strconv.Itoa(int(k)) }
func helper() string { return strings.Repeat("x", 2) }
type Props struct{ Item Item }
x := helper()
---
<p>{x}</p>`, map[string]string{
			"Item": "decl", "Kind": "decl", "Max": "impl", "limit": "impl", "Item.method": "decl",
			"name": "decl", "helper": "impl", "Props": "decl",
			"import strings": "both", "import strconv": "decl", "orbit": "none",
		}},
		{"group placed as a whole", `---
type (
	Props struct{ A A }
	A int
	Other string
)
type Lone int
---
`, map[string]string{"Props,A,Other": "decl", "Lone": "impl", "orbit": "none"}},
		{"shadowing local", `---
import "strings"
strings := []string{"a"}
_ = strings
---
{len(strings)}`, map[string]string{"import strings": "impl", "orbit": "none"}},
		{"shadowing local hides import use in template", `---
import "strings"
---
{for _, strings := range []string{"a"}}{strings}{/for}`, map[string]string{"import strings": "impl", "orbit": "none"}},
		{"implicit orbit, decl and impl", `---
type Props struct{ Body orbit.HTML }
---
<div set:html={orbit.HTML("x")}></div>`, map[string]string{"Props": "decl", "orbit": "both"}},
		{"orbit shadowed in a nested block", `---
---
{for _, orbit := range []int{1}}{orbit}{/for}`, map[string]string{"orbit": "none"}},
		{"explicit orbit import merged", `---
import "github.com/bertrandkhe/orbit"
x := orbit.HTML("a")
---
{x}`, map[string]string{"import github.com/bertrandkhe/orbit": "impl", "orbit": "none"}},
		{"explicit orbit import, used in decl too", `---
import orbit "github.com/bertrandkhe/orbit"
type Props struct{ H orbit.HTML }
---
{orbit.HTML("x")}`, map[string]string{"import github.com/bertrandkhe/orbit": "both", "Props": "decl", "orbit": "none"}},
		{"unused and blank imports", `---
import (
	"fmt"
	_ "embed"
)
---
`, map[string]string{"import fmt": "impl", "import embed": "impl", "orbit": "none"}},
		{"orbit alias in decl and template", `---
import B "./B.orbit"
type Props struct{ T B.Tag }
---
<B />`, map[string]string{"alias B": "both", "Props": "decl", "orbit": "none"}},
		{"versioned import name", `---
import (
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)
type Props struct{ R chi.Router }
var y yaml.Node
---
`, map[string]string{"import github.com/go-chi/chi/v5": "decl", "import gopkg.in/yaml.v3": "impl", "Props": "decl", "orbit": "none"}},
		{"cycle", `---
type Props struct{ N *Node }
type Node struct{ Next *Node; P *Pair }
type Pair struct{ N *Node }
---
`, map[string]string{"Props": "decl", "Node": "decl", "Pair": "decl", "orbit": "none"}},
		{"page: everything impl", "---\ntype Item int\n---\n", map[string]string{"Item": "impl", "orbit": "none"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := FileInfo{Path: "src/components/X.orbit"}
			if strings.HasPrefix(tt.name, "page") {
				info = FileInfo{Path: "src/pages/x.orbit", Role: RolePage, Route: "/x"}
			}
			f := Parse(tt.src, Strict)
			a := Analyze(f, info)
			checkAnalysis(t, f, a)
			got := anPlaces(a)
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s placed %s, want %s (all: %v)", k, got[k], v, got)
				}
			}
			if len(a.Diagnostics) > 0 {
				t.Errorf("unexpected diagnostics %v", anMessages(a))
			}
		})
	}
}

// TestAnalyzeHoisting: named funcs and methods are hoisted, IIFEs stay in
// the body; control headers with semicolons stay one statement.
func TestAnalyzeHoisting(t *testing.T) {
	src := `---
func named() int { return 1 }
func (p Props) M() {}
v := func() int { return 2 }()
func() { _ = v }()
func Generic[T any](x T) T { return x }
type Props struct{}
for i := 0; i < 2; i++ { _ = i }
if xs := []int{1}; len(xs) > 0 { _ = xs }
const c = 1; w := c
---
{w}`
	a := anComp(t, src)
	var hoisted, body []string
	for _, d := range a.Decls {
		hoisted = append(hoisted, src[d.Span.Start:d.Span.End])
	}
	for _, s := range a.Body {
		body = append(body, src[s.Span.Start:s.Span.End])
	}
	wantHoisted := []string{
		"func named() int { return 1 }",
		"func (p Props) M() {}",
		"func Generic[T any](x T) T { return x }",
		"type Props struct{}",
		"const c = 1",
	}
	if !slices.Equal(hoisted, wantHoisted) {
		t.Errorf("hoisted\n%q\nwant\n%q", hoisted, wantHoisted)
	}
	want := []string{
		"v := func() int { return 2 }()",
		"func() { _ = v }()",
		"for i := 0; i < 2; i++ { _ = i }",
		"if xs := []int{1}; len(xs) > 0 { _ = xs }",
		"w := c",
	}
	if !slices.Equal(body, want) {
		t.Errorf("body\n%q\nwant\n%q", body, want)
	}
	if len(a.Diagnostics) > 0 {
		t.Errorf("diagnostics %q", anMessages(a))
	}
}

func TestAnalyzeEcho(t *testing.T) {
	tests := []struct {
		body string
		want []string
	}{
		{"a, err := f()\nb, err := g()", []string{"a", "err", "b"}},
		{"var (\n\tx int\n\ty, z = 1, 2\n)", []string{"x", "y", "z"}},
		{"_, n := f()\nvar _ = 1", []string{"n"}},
		{"if v := 1; v > 0 { w := 2; _ = w }", nil},
		{"L: k := 1", []string{"k"}},
		{"a = 1\nb := a", []string{"b"}},
	}
	for _, tt := range tests {
		f := Parse("---\n"+tt.body+"\n---\n", Strict)
		a := Analyze(f, FileInfo{Path: "src/components/X.orbit"})
		if !slices.Equal(a.Echo, tt.want) {
			t.Errorf("%q: echo %q, want %q", tt.body, a.Echo, tt.want)
		}
	}
}

func TestAnalyzeTerminating(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"return nil", true},
		{"x := 1\n_ = x", false},
		{"if a { return nil } else { return err }", true},
		{"if a { return nil } else if b { return nil } else { panic(1) }", true},
		{"if a { return nil } else if b { return nil }", false},
		{"if a { return nil }", false},
		{"for {}", true},
		{"for { break }", false},
		{"for { for { break } }", true},
		{"L: for { for { break L } }", false},
		{"for x {}", false},
		{"for range xs {}", false},
		{"panic(\"x\")", true},
		{"panic := func(int) {}\npanic(1)", false},
		{"{ return nil }", true},
		{"switch x { case 1: return nil; default: return nil }", true},
		{"switch x { case 1: return nil }", false},
		{"switch x { case 1: fallthrough; default: return nil }", true},
		{"switch x { case 1: break; default: return nil }", false},
		{"select { case <-c: return nil }", true},
		{"select { case <-c: break }", false},
		{"return ctx.Redirect(\"/login\", 302)", true},
		{"func() { return }()", false},
	}
	for _, tt := range tests {
		f := Parse("---\n"+tt.body+"\n---\n", Strict)
		a := Analyze(f, FileInfo{Path: "src/components/X.orbit"})
		if a.Terminating != tt.want {
			t.Errorf("%q: terminating %v, want %v", tt.body, a.Terminating, tt.want)
		}
	}
	// Template content after a terminating statement is an error; whitespace
	// and template comments aren't content.
	for src, wantErr := range map[string]bool{
		"---\nreturn nil\n---\n  \n{/* c */}\n": false,
		"---\nreturn nil\n---\n<p>x</p>":        true,
		"---\nreturn nil\n---\n<!-- c -->":      true,
	} {
		a := Analyze(Parse(src, Strict), FileInfo{Path: "src/components/X.orbit"})
		if got := a.HasErrors(); got != wantErr {
			t.Errorf("%q: errors %v, want %v (%v)", src, got, wantErr, anMessages(a))
		}
	}
}

func TestAnalyzeParams(t *testing.T) {
	tests := []struct {
		route string
		want  []ParamField
		diags []string
	}{
		{"/", nil, nil},
		{"/about", nil, nil},
		{"/blog/[slug]", []ParamField{{"Slug", "slug", false}}, nil},
		{"/docs/[...path]", []ParamField{{"Path", "path", true}}, nil},
		{"/[post-id]/[api_url]/[userId]", []ParamField{{"PostID", "post-id", false}, {"APIURL", "api_url", false}, {"UserID", "userId", false}}, nil},
		{"/[id]/[ID]", []ParamField{{"ID", "id", false}}, []string{"route params [id] and [ID] both become params.ID; rename one"}},
		{"/[1st]", nil, []string{"route param [1st] can't become a Go field name; use letters, digits, - and _, starting with a letter"}},
		{"/post-[id]", nil, nil},
	}
	for _, tt := range tests {
		a := Analyze(Parse("", Strict), FileInfo{Path: "src/pages/x.orbit", Role: RolePage, Route: tt.route})
		if !slices.Equal(a.Params, tt.want) {
			t.Errorf("%s: params %v, want %v", tt.route, a.Params, tt.want)
		}
		if got := anMessages(a); !slices.Equal(got, tt.diags) {
			t.Errorf("%s: diags %q, want %q", tt.route, got, tt.diags)
		}
	}
}

// TestAnalyzeShadowing: implicit names may be shadowed in nested blocks and
// are ordinary names in hoisted code; params is ordinary in a component.
func TestAnalyzeShadowing(t *testing.T) {
	ok := []string{
		"---\nif true { props := 1; _ = props }\n---\n",
		"---\nfor _, ctx := range []int{} { _ = ctx }\n---\n",
		"---\nparams := 1\n---\n{params}",
		"---\nfunc f(ctx int) int { return ctx }\n---\n",
		"---\nx := 1\n---\n{for _, props := range []int{x}}{props}{/for}",
	}
	for _, src := range ok {
		if a := anComp(t, src); len(a.Diagnostics) > 0 {
			t.Errorf("%q: %v", src, anMessages(a))
		}
	}
	bad := map[string]string{
		"---\nprops := 1\n---\n":            "props is provided by Orbit and can't be redeclared here",
		"---\nvar ctx int\n---\n":           "ctx is provided by Orbit and can't be redeclared here",
		"---\nx, slots := 1, 2\n---\n":      "slots is provided by Orbit and can't be redeclared here",
		"---\nlocals := 1\n---\n":           "locals is provided by Orbit and can't be redeclared here",
		"---\norbit := 42\n---\n":           "orbit is provided by Orbit and can't be redeclared here",
		"---\nvar (\n\tprops int\n)\n---\n": "props is provided by Orbit and can't be redeclared here",
	}
	for src, want := range bad {
		if got := anMessages(anComp(t, src)); !slices.Equal(got, []string{want}) {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
	a := Analyze(Parse("---\nparams := 1\n---\n", Strict), FileInfo{Path: "src/pages/x.orbit", Role: RolePage, Route: "/x"})
	if got := anMessages(a); !slices.Equal(got, []string{"params is provided by Orbit and can't be redeclared here"}) {
		t.Errorf("page params: %q", got)
	}
}

func TestNewComponentInfo(t *testing.T) {
	src := "---\ntype Props struct{ Title string }\n---\n<slot /><slot name=\"footer\" /><slot name=\"footer\">x</slot><slot />"
	f := Parse(src, Strict)
	a := Analyze(f, FileInfo{Path: "src/components/My-Card.orbit"})
	ci := NewComponentInfo(f, a)
	if ci.Name != "My-Card" || ci.Pkg.Rel != "components/my_card" || ci.ID != ComponentID("src/components/My-Card.orbit") {
		t.Errorf("info %+v", ci)
	}
	if !slices.Equal(ci.Slots, []string{"default", "footer"}) {
		t.Errorf("slots %q", ci.Slots)
	}
	if len(ci.Props.Fields) != 1 || ci.Props.Fields[0].Name != "Title" {
		t.Errorf("props %+v", ci.Props)
	}
}

func FuzzAnalyze(f *testing.F) {
	for _, glob := range []string{"testdata/analyze/*.orbit", "testdata/parse/*.orbit"} {
		paths, _ := filepath.Glob(glob)
		for _, path := range paths {
			src, _ := os.ReadFile(path)
			f.Add(string(src))
		}
	}
	for _, s := range []string{
		"---\nfunc", "---\nfunc (", "---\nfunc (r T", "---\ntype Props struct{", "---\nimport (", "---\nimport \"",
		"---\nif x {\n---\n{x}", "---\n}\n---\n{)}", "---\n/*\n---\n", "---\n`\n---\n", "---\nfor ;; {\n",
		"---\ntype Props[T any] struct{ x T }\n---\n", "---\nimport X \"../../../x.orbit\"\n---\n",
	} {
		f.Add(s)
	}
	infos := []FileInfo{
		{Path: "src/components/X.orbit"},
		{Path: "src/pages/blog/[slug].orbit", Role: RolePage, Route: "/blog/[slug]"},
	}
	f.Fuzz(func(t *testing.T, src string) {
		for _, mode := range []Mode{Strict, Tolerant} {
			file := Parse(src, mode)
			for _, info := range infos {
				a := Analyze(file, info)
				checkAnalysis(t, file, a)
				NewComponentInfo(file, a)
			}
		}
	})
}
