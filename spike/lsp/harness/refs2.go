package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// sessionRefs2 probes whether the overlay-only reference misses are stable
// over time and whether re-sending overlays repairs them.
func sessionRefs2(gopls string) *report {
	r := &report{}
	r.p("# Session refs2: stability of overlay-only reference misses (%d components)", scaleN)
	for _, order := range []string{"interleaved (decl, impl per component, page last)", "all decls first, then impls, then page", "page first, then impls, then decls"} {
		s, _, err := newSession(gopls, "refs2.log", []string{"utf-8"}, true, nil)
		if err != nil {
			r.p("init: %v", err)
			continue
		}
		g := filepath.Join(projectDir, "orbit_gen")
		type f struct{ name, path, text string }
		var decls, impls, page []f
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			decls = append(decls, f{pkg + "d", filepath.Join(g, "decl/components", pkg, "decl.go"), genCompDecl(fmt.Sprintf("C%d", i), pkg, cardProps)})
			impls = append(impls, f{pkg + "i", filepath.Join(g, "impl/components", pkg, "render.go"), genCompImpl(fmt.Sprintf("C%d", i), pkg, cardImplOpts{})})
		}
		bd, bi := genBigPage(scaleN)
		page = []f{{"bigd", filepath.Join(g, "decl/pages/big/decl.go"), bd}, {"bigi", filepath.Join(g, "impl/pages/big/render.go"), bi}}
		var all []f
		switch {
		case strings.HasPrefix(order, "interleaved"):
			for i := range decls {
				all = append(all, decls[i], impls[i])
			}
			all = append(all, page...)
		case strings.HasPrefix(order, "all decls"):
			all = append(append(append(all, decls...), impls...), page...)
		default:
			all = append(append(append(all, page...), impls...), decls...)
		}
		m := s.c.diagMark()
		for _, x := range all {
			s.open(x.name, x.path, x.text)
		}
		for _, x := range all {
			s.waitVersion(m, x.name, 60*time.Second)
		}
		s.settle(2*time.Second, 30*time.Second)
		round := func(label string) {
			var missed []string
			for i := 0; i < scaleN; i++ {
				pkg := fmt.Sprintf("c%d", i)
				d := s.docs[pkg+"d"]
				res, _, _ := s.c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": d.uri},
					"position": posOf(d.text, "Featured bool", 0, 1, "utf-16"), "context": map[string]any{"includeDeclaration": true}})
				if n := strings.Count(string(res), `"uri"`); n != 3 {
					missed = append(missed, fmt.Sprintf("%s:%d", pkg, n))
				}
			}
			r.p("  - %s: %d/%d incomplete %v", label, len(missed), scaleN, missed)
		}
		r.h("open order: %s", order)
		round("round 1 (after all diagnostics + 2s quiet)")
		time.Sleep(5 * time.Second)
		round("round 2 (5s later)")
		m = s.c.diagMark()
		for _, x := range decls {
			s.change(x.name, x.text+"\n")
		}
		for _, x := range decls {
			s.waitVersion(m, x.name, 60*time.Second)
		}
		s.settle(2*time.Second, 30*time.Second)
		round("round 3 (after a whitespace didChange of every decl)")
		m = s.c.diagMark()
		for _, x := range impls {
			s.change(x.name, x.text+"\n")
		}
		for _, x := range impls {
			s.waitVersion(m, x.name, 60*time.Second)
		}
		s.settle(2*time.Second, 30*time.Second)
		round("round 4 (after a whitespace didChange of every impl)")
		s.c.shutdown()
	}
	return r
}
