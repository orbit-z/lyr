package compiler

import (
	"context"
	gotoken "go/token"
	"go/types"
	"strings"
	"testing"
)

// tsTestPkgs are the packages of the mapping tests: p uses every mapping
// row, the posts packages collide by name, and a package named string
// collides with a TS keyword.
var tsTestPkgs = map[string]string{
	"example.com/internal/posts": `package posts
type Post struct{ ID int }
`,
	"example.com/legacy/posts": `package posts
type Post struct{ Title string }
`,
	"example.com/string": `package string
type T struct{ X int }
`,
	"example.com/p": `package p

import (
	"encoding/json"
	"net/netip"
	"time"
	"unsafe"

	orbit "github.com/orbit-z/orbit"
	iposts "example.com/internal/posts"
	lposts "example.com/legacy/posts"
	str "example.com/string"
)

var (
	_ json.Number
	_ netip.Addr
	_ time.Time
	_ unsafe.Pointer
	_ orbit.HTML
)

type Status string
type IDs []int
type Blob []byte
type Ptr *int

type Post struct {
	ID     int64   ` + "`json:\"id\"`" + `
	Tags   []string ` + "`json:\"tags\"`" + `
	Author *Author ` + "`json:\"author,omitempty\"`" + `
	draft  bool
}
type Author struct {
	Name string ` + "`json:\"name\"`" + `
	URL  string ` + "`json:\"url,omitempty\"`" + `
}

type JM struct{}

func (JM) MarshalJSON() ([]byte, error) { return nil, nil }

type PJM struct{ X int }

func (*PJM) MarshalJSON() ([]byte, error) { return nil, nil }

type TM int

func (TM) MarshalText() ([]byte, error) { return nil, nil }

type PTM struct{ X int }

func (*PTM) MarshalText() ([]byte, error) { return nil, nil }

type BothM struct{}

func (BothM) MarshalText() ([]byte, error)  { return nil, nil }
func (*BothM) MarshalJSON() ([]byte, error) { return nil, nil }

type MByte byte

func (*MByte) MarshalText() ([]byte, error) { return nil, nil }

type FnM func()

func (FnM) MarshalJSON() ([]byte, error) { return nil, nil }

type Q struct {
	I  int         ` + "`json:\"i,string\"`" + `
	P  *int        ` + "`json:\"p,string\"`" + `
	B  bool        ` + "`json:\",string\"`" + `
	S  string      ` + "`json:\"s,string\"`" + `
	T  time.Time   ` + "`json:\"t,string\"`" + `
	X  []int       ` + "`json:\"x,string\"`" + `
	N  TM          ` + "`json:\"n,string\"`" + `
	JN json.Number ` + "`json:\"jn,string\"`" + `
	PS *Status     ` + "`json:\"ps,string,omitempty\"`" + `
}

type ZeroSlice []int

func (ZeroSlice) IsZero() bool { return false }

type O struct {
	S    string         ` + "`json:\"s,omitempty\"`" + `
	P    *int           ` + "`json:\"p,omitempty\"`" + `
	L    []int          ` + "`json:\"l,omitempty\"`" + `
	M    map[string]int ` + "`json:\"m,omitempty\"`" + `
	St   Author         ` + "`json:\"st,omitempty\"`" + `
	PSt  *Author        ` + "`json:\"pst,omitempty\"`" + `
	A0   [0]int         ` + "`json:\"a0,omitempty\"`" + `
	A2   [2]int         ` + "`json:\"a2,omitempty\"`" + `
	NL   IDs            ` + "`json:\"nl,omitempty\"`" + `
	ZS   Author         ` + "`json:\"zs,omitzero\"`" + `
	ZP   *int           ` + "`json:\"zp,omitzero\"`" + `
	ZL   []int          ` + "`json:\"zl,omitzero\"`" + `
	ZT   time.Time      ` + "`json:\"zt,omitzero\"`" + `
	ZI   ZeroSlice      ` + "`json:\"zi,omitzero\"`" + `
	Dash int            ` + "`json:\"-,\"`" + `
	Skip int            ` + "`json:\"-\"`" + `
	Fn   func()         ` + "`json:\"-\"`" + `
	Bad  int            ` + "`json:\"a'b\"`" + `
	Hy   int            ` + "`json:\"my-field\"`" + `
	Opt  int            ` + "`json:\",omitempty\"`" + `
	fn   func()
}

type Base struct {
	ID   int ` + "`json:\"id\"`" + `
	Name string
}
type Other struct {
	Name  string
	Extra int
}
type Tagged struct {
	Name string ` + "`json:\"Name\"`" + `
}
type Deep struct{ Base }
type status int
type e7 struct{ Hidden int }

type E1 struct {
	Base
	Other
}
type E2 struct {
	Base
	Tagged
}
type E3 struct {
	Name string
	Base
}
type E4 struct {
	*Base
	X int
}
type E5 struct {
	Base ` + "`json:\"base\"`" + `
}
type E6 struct {
	Status
	*IDs
}
type E7 struct {
	e7
	status
}
type E8 struct {
	Deep
	Other
}
type E9 struct {
	time.Time
}

type Page[T any] struct {
	Items []T      ` + "`json:\"items\"`" + `
	Next  *Page[T] ` + "`json:\"next\"`" + `
}
type Pair[K comparable, V any] struct {
	K K
	V V
}
type List[T any] []T
type G[string any] struct {
	X string ` + "`json:\"x,omitempty\"`" + `
	Y string ` + "`json:\"y,string\"`" + `
}

type Node struct {
	Kids   []Node ` + "`json:\"kids\"`" + `
	Parent *Node  ` + "`json:\"parent\"`" + `
}
type A struct{ B *B }
type B struct{ A *A }

type Bad struct {
	Ok     int
	Notify func()
}
type OptS struct {
	A orbit.Opt[string]
	B orbit.Opt[int]    ` + "`json:\"b,omitzero\"`" + `
	C orbit.Opt[*int]   ` + "`json:\"c,omitzero\"`" + `
	D orbit.Opt[Status] ` + "`json:\"d,omitempty\"`" + `
}
type OptBad struct{ F orbit.Opt[func()] }
type BadDeep struct{ Posts []Bad }
type BadMap struct{ M map[[2]int]string }
type BadC struct{ C complex128 }
type Rec struct {
	R *Rec
	F chan int
}
type FnOK struct {
	F FnM
	K map[netip.Addr]int
	T map[TM]int
}

type number struct{ X int }
type Record struct{ X int }
type Names struct {
	I iposts.Post
	L lposts.Post
	S str.T
	N number
	R Record
}
`,
}

// tsLoadTest type-checks the test packages and returns p.
func tsLoadTest(t testing.TB) (*types.Package, *gotoken.FileSet) {
	t.Helper()
	fset := gotoken.NewFileSet()
	l := &tyTestLoader{pkgs: tsTestPkgs}
	imp, err := l.Load(context.Background(), fset, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := imp.Import("example.com/p")
	if err != nil {
		t.Fatal(err)
	}
	return p, fset
}

func tsTestNS(p *types.Package) tsNS { return tsNS{Key: p.Path(), Base: p.Name(), Path: p.Path()} }

// tsDescribe renders a mapping: the TS text (refs qualified), then one line
// per declaration, then the problem.
func tsDescribe(ts string, prob *jsonProblem, decls []TSDecl, pkg *types.Package) string {
	n := newTSNames(decls)
	lines := []string{n.render(ts, "", nil)}
	for _, t := range n.order {
		d := t.d
		head := t.ns + "." + t.name
		if len(d.TypeParams) > 0 {
			ps := make([]string, len(d.TypeParams))
			for i, p := range d.TypeParams {
				ps[i] = tsIdent(p)
			}
			head += "<" + strings.Join(ps, ", ") + ">"
		}
		if !d.Interface {
			lines = append(lines, head+" = "+n.render(d.Alias, d.NS.Key, nil))
			continue
		}
		fs := make([]TSField, len(d.Fields))
		for i, f := range d.Fields {
			f.Type = n.render(f.Type, d.NS.Key, nil)
			fs[i] = f
		}
		lines = append(lines, head+" "+tsInline(fs))
	}
	if prob != nil {
		lines = append(lines, "prob "+prob.path+": "+types.TypeString(prob.typ, types.RelativeTo(pkg)))
	}
	return strings.Join(lines, "\n")
}

func TestTSMap(t *testing.T) {
	pkg, fset := tsLoadTest(t)
	tests := []struct {
		typ  string
		want string
	}{
		// Basic kinds.
		{"bool", "boolean"},
		{"int8", "number"},
		{"uint64", "number"},
		{"uintptr", "number"},
		{"float32", "number"},
		{"string", "string"},
		{"orbit.HTML", "string"},
		{"orbit.URL", "string"},
		{"*orbit.HTML", "string | null"},
		// Bytes.
		{"[]byte", "string | null"},
		{"[4]byte", "number[]"},
		{"[]MByte", "unknown[] | null"},
		{"Blob", "p.Blob\np.Blob = string | null"},
		// Slices, arrays, maps, pointers.
		{"[]string", "string[] | null"},
		{"[][]int", "(number[] | null)[] | null"},
		{"[3]int", "number[]"},
		{"[]*int", "(number | null)[] | null"},
		{"map[string]int", "Record<string, number> | null"},
		{"map[int]*bool", "Record<string, boolean | null> | null"},
		{"*int", "number | null"},
		{"**int", "number | null"},
		{"*[]int", "number[] | null"},
		{"Ptr", "p.Ptr\np.Ptr = number | null"},
		{"*Ptr", "p.Ptr | null\np.Ptr = number | null"},
		// Interfaces.
		{"any", "unknown"},
		{"error", "unknown"},
		{"*any", "unknown"},
		{"[]any", "unknown[] | null"},
		// Standard library types.
		{"time.Time", "string"},
		{"*time.Time", "string | null"},
		{"time.Duration", "time.Duration\ntime.Duration = number"},
		{"json.Number", "number"},
		{"json.RawMessage", "unknown"},
		{"netip.Addr", "string"},
		{"*netip.Addr", "string | null"},
		// Marshalers.
		{"JM", "unknown"},
		{"*JM", "unknown"},
		{"PJM", "unknown"},
		{"*PJM", "unknown"},
		{"TM", "string"},
		{"*TM", "string | null"},
		{"PTM", "unknown"},
		{"*PTM", "string | null"},
		{"BothM", "unknown"},
		{"FnM", "unknown"},
		// Structs.
		{"struct{ A int; b int }", "{ A: number }"},
		{"struct{}", "{}"},
		{"struct{ time.Time }", "unknown"},
		{"Status", "p.Status\np.Status = string"},
		{"IDs", "p.IDs\np.IDs = number[] | null"},
		{"Post", "p.Post\np.Author { name: string; url?: string }\np.Post { id: number; tags: string[] | null; author?: Author }"},
		{"Q", "p.Q\np.Q { i: string; p: string | null; B: string; s: string; t: string; x: number[] | null; n: string; jn: string; ps?: string }"},
		{"O", "p.O\np.Author { name: string; url?: string }\np.IDs = number[] | null\np.O { s?: string; p?: number; l?: number[]; m?: Record<string, number>; st: Author; pst?: Author; a0?: number[]; a2: number[]; nl?: NonNullable<IDs>; zs?: Author; zp?: number; zl?: number[]; zt?: string; zi?: ZeroSlice; \"-\": number; a: number; \"my-field\": number; Opt?: number }\np.ZeroSlice = number[] | null"},
		// Embedded structs.
		{"E1", "p.E1\np.E1 { id: number; Extra: number }"},
		{"E2", "p.E2\np.E2 { id: number; Name: string }"},
		{"E3", "p.E3\np.E3 { Name: string; id: number }"},
		{"E4", "p.E4\np.E4 { id?: number; Name?: string; X: number }"},
		{"E5", "p.E5\np.Base { id: number; Name: string }\np.E5 { base: Base }"},
		{"E6", "p.E6\np.E6 { Status: Status; IDs: IDs | null }\np.IDs = number[] | null\np.Status = string"},
		{"E7", "p.E7\np.E7 { Hidden: number }"},
		{"E8", "p.E8\np.E8 { id: number; Name: string; Extra: number }"},
		{"E9", "unknown"},
		// Generics.
		{"Page[Post]", "p.Page<p.Post>\np.Author { name: string; url?: string }\np.Page<T> { items: T[] | null; next: Page<T> | null }\np.Post { id: number; tags: string[] | null; author?: Author }"},
		{"Pair[string, *Status]", "p.Pair<string, p.Status | null>\np.Pair<K, V> { K: K; V: V }\np.Status = string"},
		{"List[int]", "p.List<number>\np.List<T> = T[] | null"},
		{"G[int]", "p.G<number>\np.G<string_> { x?: string_; y: unknown }"},
		// Recursive types.
		{"Node", "p.Node\np.Node { kids: Node[] | null; parent: Node | null }"},
		{"A", "p.A\np.A { B: B | null }\np.B { A: A | null }"},
		// Names.
		{"Names", "p.Names\ninternal_posts.Post { ID: number }\nlegacy_posts.Post { Title: string }\np.Names { I: internal_posts.Post; L: legacy_posts.Post; S: string_.T; N: number_; R: Record_ }\np.Record_ { X: number }\np.number_ { X: number }\nstring_.T { X: number }"},
		// Generate-time problems.
		{"Bad", "p.Bad\np.Bad { Ok: number; Notify: unknown }\nprob Bad.Notify: func()"},
		{"BadDeep", "p.BadDeep\np.Bad { Ok: number; Notify: unknown }\np.BadDeep { Posts: Bad[] | null }\nprob BadDeep.Posts[].Notify: func()"},
		{"[]Bad", "p.Bad[] | null\np.Bad { Ok: number; Notify: unknown }\nprob [].Notify: func()"},
		{"BadMap", "p.BadMap\np.BadMap { M: Record<string, string> | null }\nprob BadMap.M: map[[2]int]string"},
		{"BadC", "p.BadC\np.BadC { C: unknown }\nprob BadC.C: complex128"},
		{"Rec", "p.Rec\np.Rec { R: Rec | null; F: unknown }\nprob Rec.F: chan int"},
		{"chan int", "unknown\nprob : chan int"},
		{"unsafe.Pointer", "unknown\nprob : unsafe.Pointer"},
		{"map[PTM]int", "Record<string, number> | null\nprob : map[PTM]int"},
		{"Page[func()]", "p.Page<unknown>\np.Page<T> { items: T[] | null; next: Page<T> | null }\nprob Page.Items[]: func()"},
		{"map[Status]Bad", "Record<string, p.Bad> | null\np.Bad { Ok: number; Notify: unknown }\nprob [].Notify: func()"},
		// orbit.Opt (PLAN.md §2.6 (Q5)).
		{"orbit.Opt[string]", "string | null"},
		{"orbit.Opt[*int]", "number | null"},
		{"[]orbit.Opt[bool]", "(boolean | null)[] | null"},
		{"OptS", "p.OptS\np.OptS { A: string | null; b?: number; c?: number | null; d: Status | null }\np.Status = string"},
		{"OptBad", "p.OptBad\np.OptBad { F: unknown }\nprob OptBad.F: func()"},
		{"FnOK", "p.FnOK\np.FnOK { F: unknown; K: Record<string, number> | null; T: Record<string, number> | null }"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			tv, err := types.Eval(fset, pkg, tsEvalPos(pkg), tt.typ)
			if err != nil || !tv.IsType() {
				t.Fatalf("Eval(%s): %v", tt.typ, err)
			}
			m := newTSMapper(tsTestNS)
			ts, prob := m.Map(tv.Type)
			got := tsDescribe(ts, prob, m.Decls(), pkg)
			if got != tt.want {
				t.Errorf("Map(%s):\n got: %s\nwant: %s", tt.typ, got, tt.want)
			}
		})
	}
}

// TestTSMapUntypedNil: orbit.JSON(nil) is null.
func TestTSMapUntypedNil(t *testing.T) {
	ts, prob := newTSMapper(tsTestNS).Map(types.Typ[types.UntypedNil])
	if ts != "null" || prob != nil {
		t.Errorf("Map(untyped nil) = %q, %v", ts, prob)
	}
}

// TestTSMapCache: one mapper reused across Map calls gives the same
// results, problems included (the problem walk caches per named type).
func TestTSMapCache(t *testing.T) {
	pkg, fset := tsLoadTest(t)
	m := newTSMapper(tsTestNS)
	for range 2 {
		for _, typ := range []string{"Rec", "A", "Bad", "BadDeep", "Page[Post]"} {
			tv, _ := types.Eval(fset, pkg, tsEvalPos(pkg), typ)
			_, prob := m.Map(tv.Type)
			if (prob != nil) != (typ == "Rec" || typ == "Bad" || typ == "BadDeep") {
				t.Errorf("Map(%s): prob %v", typ, prob)
			}
		}
	}
	// B reached first through A (on the walk stack) must still report
	// nothing, and Rec's problem stays found through the cycle.
	tv, _ := types.Eval(fset, pkg, tsEvalPos(pkg), "*Rec")
	if _, prob := m.Map(tv.Type); prob == nil || prob.path != ".F" {
		t.Errorf("Map(*Rec): prob %+v", prob)
	}
}

func BenchmarkTSMap(b *testing.B) {
	pkg, fset := tsLoadTest(b)
	tv, _ := types.Eval(fset, pkg, tsEvalPos(pkg), "Page[Post]")
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m := newTSMapper(tsTestNS)
			m.Map(tv.Type)
		}
	})
	b.Run("warm", func(b *testing.B) {
		b.ReportAllocs()
		m := newTSMapper(tsTestNS)
		m.Map(tv.Type)
		for b.Loop() {
			m.Map(tv.Type)
		}
	})
}

// tsEvalPos is a position in p's file, where its imports are in scope.
func tsEvalPos(p *types.Package) gotoken.Pos { return p.Scope().Child(0).Pos() + 1 }
