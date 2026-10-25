package compiler

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const tyPostsPath = testModule + "/internal/posts"

const tyPostsSrc = `package posts

type Slug string

type Post struct {
	Title string
	Slug  Slug
}

type Status int

func (s Status) String() string { return "status" }
`

const (
	tyDeclCard = testModule + "/orbit_gen/decl/components/card"
	tyDeclList = testModule + "/orbit_gen/decl/components/list"
)

// tyProject is a multi-package fixture: Card (Props uses internal/posts),
// List (Props uses Card.Props, so its decl imports Card's), the index page
// (its impl imports List and Card) and an unrelated Other.
type tyProject struct {
	card, list, index, other tyComp
}

func newTyProject() *tyProject {
	return &tyProject{
		card: tyComp{
			orbit:       "src/components/Card.orbit",
			declImports: "\t\"" + tyPostsPath + "\"\n",
			declBody:    "\ntype Props struct {\n\tPost posts.Post\n\tN    int\n}\n",
			body: "\torbit__w.Expr(\nprops.Post.Title,\n\t)\n" +
				"\torbit__w.Attr(\"href\",\nprops.Post.Slug,\n\t)\n" +
				"\torbit__w.Expr(\nprops.N,\n\t)\n",
		},
		list: tyComp{
			orbit:       "src/components/List.orbit",
			declImports: "\tCard \"" + tyDeclCard + "\"\n",
			implImports: "\tCard \"" + tyDeclCard + "\"\n",
			declBody:    "\ntype Props struct{ Items []Card.Props }\n",
			body: "\tfor _, it := range props.Items {\n" +
				"\t\tif orbit__err := Card.Render(orbit__ctx, orbit__w, it, orbit__rt.Slots{}); orbit__err != nil {\n\t\t\treturn orbit__err\n\t\t}\n\t}\n" +
				"\torbit__w.Expr(\nlen(props.Items),\n\t)\n",
		},
		index: tyComp{
			orbit:       "src/pages/index.orbit",
			page:        true,
			implImports: "\tCard \"" + tyDeclCard + "\"\n\tList \"" + tyDeclList + "\"\n",
			body: "\tif orbit__err := List.Render(orbit__ctx, orbit__w, List.Props{Items: []Card.Props{{N: 1}}}, orbit__rt.Slots{}); orbit__err != nil {\n\t\treturn orbit__err\n\t}\n" +
				"\torbit__w.Expr(\n\"hi\",\n\t)\n",
		},
		other: tyComp{
			orbit: "src/components/Other.orbit",
			body:  "\torbit__w.Expr(\n1.5,\n\t)\n",
		},
	}
}

func (p *tyProject) units(t testing.TB) []Unit {
	return []Unit{tyFixture(t, p.card), tyFixture(t, p.list), tyFixture(t, p.index), tyFixture(t, p.other)}
}

func tyPaths(res *CheckResult) []string {
	var s []string
	for _, u := range res.Units {
		s = append(s, u.Output.Impl.Path)
	}
	return s
}

func tyLoader() *tyTestLoader {
	return &tyTestLoader{pkgs: map[string]string{tyPostsPath: tyPostsSrc}}
}

func TestCheckerFullAndUpdate(t *testing.T) {
	p := newTyProject()
	units := p.units(t)
	g := newTyGen(units...)
	l := tyLoader()
	c := NewChecker(l)
	c.generate = g.generate
	ctx := context.Background()

	res := tyCheck(t, c, units)
	if len(res.Warnings) > 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if got := tyChecked(res); got != "true true true true " {
		t.Fatalf("checked = %s", got)
	}
	want := [][]string{
		{"String", "AttrURL/string", "Int/int64"},
		{"Int/int64"},
		{"String"},
		{"Float"},
	}
	for i, u := range units {
		if got := tyGot(u, res.Units[i]); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%s: writes %v, want %v", u.Pass1.Impl.Path, got, want[i])
		}
		if res.Units[i].Output == u.Pass1 {
			t.Errorf("%s: not re-emitted", u.Pass1.Impl.Path)
		}
		if !strings.Contains(string(res.Units[i].Output.Impl.Content), "orbit__w."+strings.Split(want[i][0], "/")[0]+"(") {
			t.Errorf("%s: re-emitted output lacks the typed write:\n%s", u.Pass1.Impl.Path, res.Units[i].Output.Impl.Content)
		}
	}
	if d, i := c.state.stats.decl.Load(), c.state.stats.impl.Load(); d != 4 || i != 4 {
		t.Errorf("full check: %d decl and %d impl checks, want 4 and 4", d, i)
	}
	if g.calls != 4 {
		t.Errorf("Generate called %d times, want 4", g.calls)
	}
	if l.loads != 1 || !reflect.DeepEqual(l.paths[0], []string{tyPostsPath, RuntimePath}) {
		t.Errorf("loads = %d %v", l.loads, l.paths)
	}

	update := func(changed []Unit, removed []string) *CheckResult {
		t.Helper()
		c.state.stats.decl.Store(0)
		c.state.stats.impl.Store(0)
		g.add(changed...)
		res, err := c.Update(ctx, changed, removed)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// A body edit re-checks only that impl package.
	p.card.body += "\torbit__w.Expr(\nprops.Post.Slug,\n\t)\n"
	card := tyFixture(t, p.card)
	res = update([]Unit{card}, nil)
	if got := tyPaths(res); !reflect.DeepEqual(got, []string{"orbit_gen/impl/components/card/render.go"}) {
		t.Errorf("body edit re-checked %v", got)
	}
	if d, i := c.state.stats.decl.Load(), c.state.stats.impl.Load(); d != 0 || i != 1 {
		t.Errorf("body edit: %d decl and %d impl checks, want 0 and 1", d, i)
	}
	if got := tyGot(card, res.Units[0]); !reflect.DeepEqual(got, []string{"String", "AttrURL/string", "Int/int64", "String/string"}) {
		t.Errorf("body edit writes %v", got)
	}

	// A Props edit re-checks that decl, the decls importing it (List) and
	// every impl importing either; Other isn't touched.
	p.card.declBody = "\ntype Props struct {\n\tPost  posts.Post\n\tN     int\n\tExtra bool\n}\n"
	p.card.body += "\torbit__w.Attr(\"hidden\",\nprops.Extra,\n\t)\n"
	card = tyFixture(t, p.card)
	res = update([]Unit{card}, nil)
	if got, want := tyPaths(res), []string{
		"orbit_gen/impl/components/card/render.go",
		"orbit_gen/impl/components/list/render.go",
		"orbit_gen/impl/pages/index/render.go",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("Props edit re-checked %v, want %v", got, want)
	}
	if d, i := c.state.stats.decl.Load(), c.state.stats.impl.Load(); d != 2 || i != 3 {
		t.Errorf("Props edit: %d decl and %d impl checks, want 2 and 3", d, i)
	}
	if got := tyChecked(res); got != "true true true " {
		t.Errorf("checked = %s", got)
	}
	if got := tyGot(card, res.Units[0]); got[len(got)-1] != "AttrBool" {
		t.Errorf("Props edit writes %v", got)
	}

	// A type error falls back silently, keeping the pass-1 output.
	bad := p.card
	bad.body += "\tvar _ int = \"x\"\n"
	badU := tyFixture(t, bad)
	res = update([]Unit{badU}, nil)
	if len(res.Units) != 1 || res.Units[0].Checked || res.Units[0].Output != badU.Pass1 || res.Units[0].Writes != nil {
		t.Errorf("type error: got %+v", res.Units)
	}
	if len(res.Warnings) > 0 {
		t.Errorf("type error warned: %v", res.Warnings)
	}

	// Removing a file nobody imports re-checks nothing.
	res = update(nil, []string{"src/components/Other.orbit"})
	if len(res.Units) != 0 {
		t.Errorf("removal re-checked %v", tyPaths(res))
	}
	// Removing Card makes its importers fall back.
	res = update(nil, []string{"src/components/Card.orbit"})
	if got := tyChecked(res); got != "false false " {
		t.Errorf("after removing Card: %v checked %s", tyPaths(res), got)
	}

	// Invalidate reloads the export data and re-checks everything.
	c.Invalidate()
	all := p.units(t)
	g.add(all...)
	res = update(all, nil)
	if l.loads != 2 || len(res.Units) != 4 || tyChecked(res) != "true true true true " {
		t.Errorf("after Invalidate: loads=%d units=%v checked=%s", l.loads, tyPaths(res), tyChecked(res))
	}
	if d, i := c.state.stats.decl.Load(), c.state.stats.impl.Load(); d != 4 || i != 4 {
		t.Errorf("after Invalidate: %d decl and %d impl checks, want 4 and 4", d, i)
	}

	// A new external import reloads too.
	p.other.implImports = "\t\"strconv\"\n"
	p.other.body = "\torbit__w.Expr(\nstrconv.Itoa(1),\n\t)\n"
	other := tyFixture(t, p.other)
	res = update([]Unit{other}, nil)
	if l.loads != 3 || len(res.Units) != 4 || res.Units[0].Output.Impl.Path != "orbit_gen/impl/components/other/render.go" {
		t.Errorf("new import: loads=%d units=%v", l.loads, tyPaths(res))
	}
	if got := tyGot(other, res.Units[0]); !reflect.DeepEqual(got, []string{"String"}) {
		t.Errorf("new import writes %v", got)
	}
}

func TestCheckerUpdateBeforeCheck(t *testing.T) {
	if _, err := NewChecker(tyLoader()).Update(context.Background(), nil, nil); err == nil {
		t.Error("Update before Check succeeded")
	}
}

func TestCheckerFallbacks(t *testing.T) {
	ctx := context.Background()
	t.Run("decl type error", func(t *testing.T) {
		p := newTyProject()
		p.list.declBody = "\ntype Props struct{ Items []Card.Props; X Missing }\n"
		res := tyCheckWith(t, tyLoader(), p.units(t))
		if got := tyChecked(res); got != "true false false true " {
			t.Errorf("checked = %s", got)
		}
		if len(res.Warnings) > 0 {
			t.Errorf("warnings: %v", res.Warnings)
		}
	})
	t.Run("parse error", func(t *testing.T) {
		p := newTyProject()
		units := p.units(t)
		units[3].Pass1.Impl.Content = append([]byte(nil), "package other_impl\nfunc {"...)
		res := tyCheckWith(t, tyLoader(), units)
		if got := tyChecked(res); got != "true true true false " {
			t.Errorf("checked = %s", got)
		}
	})
	t.Run("unreadable export data", func(t *testing.T) {
		l := tyLoader()
		l.fail = map[string]bool{tyPostsPath: true}
		res := tyCheckWith(t, l, newTyProject().units(t))
		if got := tyChecked(res); got != "false false false true " {
			t.Errorf("checked = %s", got)
		}
		want := []string{"orbit: can't read export data for " + tyPostsPath + ": open /cache/xx-d: no such file or directory; using Expr fallbacks"}
		if !reflect.DeepEqual(res.Warnings, want) {
			t.Errorf("warnings = %q, want %q", res.Warnings, want)
		}
		if res.Units[0].Output == nil || res.Units[0].Writes != nil {
			t.Errorf("fallback unit = %+v", res.Units[0])
		}
	})
	t.Run("loader failure", func(t *testing.T) {
		l := tyLoader()
		l.err = errors.New("go list -export: go: updates to go.mod needed\nmore")
		units := newTyProject().units(t)
		res := tyCheckWith(t, l, units)
		if got := tyChecked(res); got != "false false false false " {
			t.Errorf("checked = %s", got)
		}
		want := []string{"orbit: can't load export data: go list -export: go: updates to go.mod needed; using Expr fallbacks"}
		if !reflect.DeepEqual(res.Warnings, want) {
			t.Errorf("warnings = %q, want %q", res.Warnings, want)
		}
		for i, u := range res.Units {
			if u.Output != units[i].Pass1 {
				t.Errorf("unit %d: output isn't pass 1", i)
			}
		}
	})
	t.Run("decl import cycle", func(t *testing.T) {
		a := tyComp{orbit: "src/components/A.orbit", declImports: "\tB \"" + testModule + "/orbit_gen/decl/components/b\"\n",
			declBody: "\ntype Props struct{ B *B.Props }\n", body: "\torbit__w.Expr(\n\"a\",\n\t)\n"}
		b := tyComp{orbit: "src/components/B.orbit", declImports: "\tA \"" + testModule + "/orbit_gen/decl/components/a\"\n",
			declBody: "\ntype Props struct{ A *A.Props }\n", body: "\torbit__w.Expr(\n\"b\",\n\t)\n"}
		o := tyComp{orbit: "src/components/O.orbit", body: "\torbit__w.Expr(\n\"o\",\n\t)\n"}
		res := tyCheckWith(t, tyLoader(), []Unit{tyFixture(t, a), tyFixture(t, b), tyFixture(t, o)})
		if got := tyChecked(res); got != "false false true " {
			t.Errorf("checked = %s", got)
		}
	})
	t.Run("tolerant mode", func(t *testing.T) {
		units := newTyProject().units(t)
		for _, u := range units {
			u.Input.Options.Mode = Tolerant
		}
		res := tyCheckWith(t, tyLoader(), units)
		if got := tyChecked(res); got != "false false false false " {
			t.Errorf("checked = %s", got)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := NewChecker(tyLoader()).Check(cctx, newTyProject().units(t)); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

// tyCheckWith runs a full check with the tyGen re-emitter.
func tyCheckWith(t *testing.T, l ExportLoader, units []Unit) *CheckResult {
	t.Helper()
	c := NewChecker(l)
	c.generate = newTyGen(units...).generate
	return tyCheck(t, c, units)
}
