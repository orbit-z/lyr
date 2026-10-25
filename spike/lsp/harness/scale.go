package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const scaleN = 50

func genBigPage(n int) (decl, impl string) {
	decl = header + "package big\n\nimport (\n\torbit \"github.com/bertrandkhe/orbit\"\n)\n\ntype Props struct{}\n\n" + renderHook("src/pages/big.orbit")
	var b strings.Builder
	b.WriteString(header + "package big\n\nimport (\n\torbit \"github.com/bertrandkhe/orbit\"\n\t. \"example.com/blog/orbit_gen/decl/pages/big\"\n\t\"example.com/blog/internal/posts\"\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tC%d \"example.com/blog/orbit_gen/decl/components/c%d\"\n", i, i)
	}
	b.WriteString(")\n\nfunc init() { Orbit__Render = orbit__render }\n\nfunc orbit__render(ctx *orbit.Ctx, orbit__w *orbit.Writer, props Props, slots orbit.Slots) error {\n\tp := posts.Post{}\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tif orbit__err := C%d.Render(ctx, orbit__w, C%d.Props{Post: p, Featured: true}, orbit.Slots{}); orbit__err != nil {\n\t\treturn orbit__err\n\t}\n", i, i)
	}
	b.WriteString("\treturn orbit__w.Err()\n}\n\nfunc orbit__complete() {\n\t_ = C7.Props{}\n}\n")
	return decl, b.String()
}

func rssKB(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "?"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "VmRSS:"))
		}
	}
	return "?"
}

func sessionScale(gopls string) *report {
	r := &report{}
	r.p("# Session scale: %d components + a page using all of them, all overlay-only (%d overlays)", scaleN, 2*scaleN+2)
	cache, _ := os.MkdirTemp("", "goplscache")
	defer os.RemoveAll(cache)
	for run := 0; run < 2; run++ {
		env := os.Environ()
		label := "warm GOPLSCACHE"
		if run == 0 {
			env = append(env, "GOPLSCACHE="+cache)
			label = "cold: empty GOPLSCACHE"
		}
		t0 := time.Now()
		s, _, err := newSession(gopls, fmt.Sprintf("scale%d.log", run), []string{"utf-8"}, true, env)
		if err != nil {
			r.p("init: %v", err)
			continue
		}
		s.r = &report{}
		m := s.c.diagMark()
		g := filepath.Join(projectDir, "orbit_gen")
		names := []string{}
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			s.open(pkg+"d", filepath.Join(g, "decl/components", pkg, "decl.go"), genCompDecl(fmt.Sprintf("C%d", i), pkg, cardProps))
			s.open(pkg+"i", filepath.Join(g, "impl/components", pkg, "render.go"), genCompImpl(fmt.Sprintf("C%d", i), pkg, cardImplOpts{}))
			names = append(names, pkg+"d", pkg+"i")
		}
		bd, bi := genBigPage(scaleN)
		s.open("bigd", filepath.Join(g, "decl/pages/big/decl.go"), bd)
		s.open("bigi", filepath.Join(g, "impl/pages/big/render.go"), bi)
		names = append(names, "bigd", "bigi")
		tOpen := time.Now()
		// first completion immediately
		p := posOf(bi, "_ = C7.Props{", 0, len("_ = C7.Props{"), "utf-16")
		res, dc, _ := s.c.call("textDocument/completion", s.tdp("bigi", p))
		_, items := parseCompletion(res)
		tFirst := time.Since(t0)
		// wait for diagnostics on every overlay
		var lastAt time.Time
		missing := 0
		for _, n := range names {
			e, ok := s.waitVersion(m, n, 120*time.Second)
			if !ok {
				missing++
				continue
			}
			if e.at.After(lastAt) {
				lastAt = e.at
			}
		}
		errs := 0
		for _, e := range s.c.lastDiags() {
			if !strings.HasSuffix(e.uri, "go.mod") {
				errs += e.n
			}
		}
		r.h("run %d (%s)", run, label)
		r.p("- open→all overlays have diagnostics: %v (missing %d, total diagnostics %d)", lastAt.Sub(tOpen).Round(time.Millisecond), missing, errs)
		r.p("- first completion `C7.Props{|}` sent right after the didOpens: latency %v, process start→result %v, items %d, Post present %v", dc.Round(time.Millisecond), tFirst.Round(time.Millisecond), len(items), find(items, "Post") != nil)
		// references right after diagnostics
		cd := s.docs["c0d"].text
		res, dr, _ := s.c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": s.docs["c0d"].uri},
			"position": posOf(cd, "Featured bool", 0, 1, "utf-16"), "context": map[string]any{"includeDeclaration": true}})
		r.p("- references of C0.Props.Featured: %d locations (expect 3: decl, c0 impl, big page) in %v", strings.Count(string(res), `"uri"`), dr.Round(time.Millisecond))
		s.settle(2*time.Second, 30*time.Second)
		for _, c := range []string{"c0", "c1", "c25", "c49"} {
			cd := s.docs[c+"d"].text
			res, _, _ := s.c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": s.docs[c+"d"].uri},
				"position": posOf(cd, "Featured bool", 0, 1, "utf-16"), "context": map[string]any{"includeDeclaration": true}})
			r.p("  - after settle: references of %s.Props.Featured: %d locations: %s", c, strings.Count(string(res), `"uri"`), shortURIs(string(res)))
			ci := s.docs[c+"i"].text
			res, _, _ = s.c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": s.docs[c+"i"].uri},
				"position": posOf(ci, "props.Featured", 0, 7, "utf-16"), "context": map[string]any{"includeDeclaration": true}})
			r.p("  - same, queried from the impl's `props.Featured`: %d locations: %s", strings.Count(string(res), `"uri"`), shortURIs(string(res)))
		}
		// cross-package edit latency
		var lat []time.Duration
		for i := 0; i < 5; i++ {
			t := time.Now()
			s.change("c7d", genCompDecl("C7", "c7", strings.Replace(cardPropsTags, "Tags", fmt.Sprintf("Tags%d", i), 1)))
			res, _, _ := s.c.call("textDocument/completion", s.tdp("bigi", p))
			lat = append(lat, time.Since(t))
			_, items := parseCompletion(res)
			if find(items, fmt.Sprintf("Tags%d", i)) == nil {
				r.p("  run %d: new field missing!", i)
			}
		}
		r.p("- C7 decl didChange → completion in big page sees new field: %v", lat)
		r.p("- gopls RSS: %s", rssKB(s.c.cmd.Process.Pid))
		s.c.shutdown()
	}
	return r
}

func shortURIs(s string) string {
	var out []string
	for _, part := range strings.Split(s, `"uri":"`)[1:] {
		u := part[:strings.Index(part, `"`)]
		out = append(out, strings.TrimPrefix(u, uriOf(projectDir)+"/orbit_gen/"))
	}
	return strings.Join(out, ", ")
}
