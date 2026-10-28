package compiler

import (
	"strings"
	"testing"
)

// optSiteKind names how the typed pass wrote a call site of the opt case.
func optSiteKind(w TypedWrite) string {
	switch {
	case w.If != nil && w.If.Opt:
		return "opt"
	case w.Kind == WriteOptWrapIf:
		return "wrapif"
	case w.If != nil && w.If.Folded:
		return "folded"
	case w.If != nil:
		return "if"
	case w.Opt:
		return "class"
	case w.Kind == WriteOptAsIs:
		return "asis"
	case w.Kind == WriteOptWrap:
		return "wrap"
	case w.Kind == WriteRestOpt:
		return "restopt"
	case w.Kind == WriteRestOptURL:
		return "restopturl"
	case w.Kind == WriteHTML:
		return "html"
	}
	return ""
}

// TestOptTypedGolden pins the typed pass-2 output of the opt case (PLAN.md
// §2.6, "Typed optional values"): Opt values lowered on attributes, in
// content and in class:list, props of type Opt[T] passed as is or
// wrapped, pass-through Opt attributes as RestOpt, and the style trust of
// an Opt[orbit.CSS]. The byte identity with the fallback is
// TestPipelineTypedVsFallback's.
func TestOptTypedGolden(t *testing.T) {
	c := loadCase(t, "testdata/codegen/opt")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	kinds := map[string]string{} // code → optSiteKind (the last site with that code)
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		for _, d := range pf.typed.Diagnostics {
			t.Errorf("%s", d.Format(pf.path, pf.f.Lines)) // no style warning either
		}
		switch pf.path {
		case "src/components/Opt.orbit", "src/pages/index.orbit", "src/pages/allocs.orbit":
			pkg, _ := PackageFor(pf.path)
			golden(t, "testdata/codegen/opt/"+strings.ReplaceAll(pkg.Rel, "/", "_")+".render.go.typed.golden", string(pf.typed.Output.Impl.Content))
		}
		for _, cs := range pf.pass1.CallSites {
			kinds[cs.Code.Text] = optSiteKind(pf.typed.Writes[cs.Key])
		}
	}
	for code, want := range map[string]string{
		// Opt.orbit: attributes and content.
		"o": "opt", "none": "opt", // last: class={none} "u": "opt", "bad": "opt", "i": "opt", "f": "opt", "b": "opt", "bt": "opt",
		"fl": "opt", "h": "opt", "css": "opt", "j": "opt", "sl": "opt", "nest": "opt", "nestNone": "opt", "anyo": "opt",
		"orbit.NonZero(props.S)": "opt", "orbit.Some(props.S)": "opt", "(o) /* c */": "opt", "orbit.NonZero(k)": "opt",
		"orbit.If(k != 1, k)": "if", `orbit.If(props.On, css.Or(""))`: "if",
		"mine": "", // a type defined over an Opt
		// class:list items.
		"cls": "class", "clss": "class",
		// Props of type Opt[T].
		`orbit.If(ext, "noopener")`: "asis", "summary": "wrap", "describedBy": "restopt", "n": "wrap", "ext": "wrap",
		`orbit.CSS("color: red")`: "wrap", `Link.Slug("s")`: "wrap", `orbit.If(ext, "c")`: "wrapif",
		"orbit.NonZero(summary)": "asis", `orbit.If(!ext, "never")`: "asis",
		"orbit.NonZero(it.Rel)": "restopt", "it.Title": "wrap", "it.ID": "wrap",
		// Pass-through attributes.
		`orbit.If(ext, "help")`: "restopt", `orbit.If(!ext, "no")`: "restopt", `orbit.Some("javascript:x")`: "restopturl",
		"orbit.Some(n)": "restopt", "orbit.Some(true)": "restopt", `orbit.If(it.ID == 2, "help")`: "restopt",
		// allocs.orbit.
		`orbit.If(it.Href == current, "page")`: "folded", "orbit.NonZero(it.ID)": "opt", "orbit.NonZero(it.Title)": "opt",
		// set:html of an Opt[orbit.HTML] (user decision, 2026-10-27), and
		// of an orbit.HTML (the plain write).
		"orbit.None[orbit.HTML]()": "opt", `orbit.If(props.On, orbit.HTML("<i>if</i>"))`: "opt",
		"orbit.NonZero(orbit.HTML(it.Rel))": "opt", `h.Or("")`: "html",
	} {
		got, ok := kinds[code]
		if !ok {
			t.Errorf("no call site %s", code)
			continue
		}
		if got != want {
			t.Errorf("%s: %q, want %q", code, got, want)
		}
	}
}

// TestOptAllocs checks the cost table of PLAN.md §2.6 "Typed optional
// values": the opt case's allocs page (lowered Opt and If attributes and
// content, NonZero, props of type Opt[T] wrapped and passed as is, static
// props, pass-through Opt attributes) renders with 0 allocations in the
// build flavor; through the hooks, per loop iteration, the pass-through
// call site's list escapes (PLAN.md §2.5) and so does a slot capturing
// a loop variable (§3.5): neither is an Opt cost.
func TestOptAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/opt")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	var res []*cgResult
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		res = append(res, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: pf.in})
	}
	direct := directCalls(res)
	builds := map[string]*Output{}
	for _, pf := range p.files {
		m.writeOutput(pf.typed.Output)
		if d := direct[pf.path]; d != nil {
			in := *pf.in
			in.Typed, in.Direct = pf.typed.Writes, d
			builds[pf.path] = Generate(&in)
		}
	}
	m.writeOverlay(builds)
	// condAllocTestSrc, with the opt case's components linked (the hook
	// flavor calls them through their hooks).
	m.write("alloc/alloc_test.go", strings.Replace(condAllocTestSrc, `_ "example.com/blog/orbit_gen/impl/pages/allocs"`,
		`_ "example.com/blog/orbit_gen/impl/pages/allocs"
	_ "example.com/blog/orbit_gen/impl/components/link"
	_ "example.com/blog/orbit_gen/impl/components/button"`, 1))
	allocs := func(args ...string) string {
		out, err := m.goCmd(append(append([]string{"test", "-count=1", "-v"}, args...), "./alloc")...)
		if err != nil {
			t.Fatalf("go test %v: %v\n%s", args, err, out)
		}
		_, n, ok := strings.Cut(out, "allocs=")
		if !ok {
			t.Fatalf("no allocs= in\n%s", out)
		}
		n, _, _ = strings.Cut(n, "\n")
		return n
	}
	if got := allocs("-overlay=" + OverlayFile); got != "0" {
		t.Errorf("build flavor: %s allocations per render, want 0", got)
	}
	if got := allocs(); got != "4" {
		t.Errorf("hook flavor: %s allocations per render, want 4 (per loop iteration: the pass-through list and a slot capturing it.Label)", got)
	}
}

// TestOptPropErrors: a value of another type passed to a prop of type
// Opt[T] is an error at the value (PLAN.md §2.6 (Q3)); untyped constants
// take T.
func TestOptPropErrors(t *testing.T) {
	srcs := map[string]string{
		"src/components/Link.orbit": "---\ntype Slug string\n\ntype Props struct {\n\tRel orbit.Opt[string]\n\tID  orbit.Opt[Slug]\n\tN   orbit.Opt[int]\n}\n---\n<a rel={props.Rel} id={props.ID} data-n={props.N}></a>\n",
		"src/pages/index.orbit": "---\nimport Link \"../components/Link.orbit\"\n\nconst c = \"k\"\nn := 3\nvar s string\nvar o orbit.Opt[int]\n---\n" +
			"<Link rel={n} />\n<Link id={c} n={o} />\n<Link id={s} />\n<Link rel={orbit.Some(2)} />\n",
	}
	m := newGenModule(t)
	pl := plPipeline(t, m, srcs, Options{Module: testModule})
	var got []string
	for _, pf := range pl.files {
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
	}
	want := []string{
		"src/pages/index.orbit:9:12: prop rel of <Link> is orbit.Opt[string]; got int",
		"src/pages/index.orbit:11:11: prop id of <Link> is orbit.Opt[link.Slug]; got string",
		"src/pages/index.orbit:12:12: prop rel of <Link> is orbit.Opt[string]; got orbit.Opt[int]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	idx := pl.files[1]
	for _, cs := range idx.pass1.CallSites {
		if cs.Code.Text == "c" && idx.typed.Writes[cs.Key].Kind != WriteOptWrap {
			t.Errorf("id={c}: %+v, want Wrap", idx.typed.Writes[cs.Key])
		}
		if cs.Code.Text == "o" && idx.typed.Writes[cs.Key].Kind != WriteOptAsIs {
			t.Errorf("n={o}: %+v, want as is", idx.typed.Writes[cs.Key])
		}
	}
}
