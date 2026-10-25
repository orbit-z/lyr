package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sessionRefs3: open overlays in the worst order (page first), then force
// gopls to load each overlay's package with a per-file request that needs
// type information (documentHighlight on the package clause), all in parallel.
// Measure the barrier and re-check references completeness.
func sessionRefs3(gopls string) *report {
	r := &report{}
	r.p("# Session refs3: readiness barrier via one documentHighlight per overlay (%d components)", scaleN)
	for run := 0; run < 3; run++ {
		s, _, err := newSession(gopls, fmt.Sprintf("refs3-%d.log", run), []string{"utf-8"}, true, nil)
		if err != nil {
			r.p("init: %v", err)
			continue
		}
		g := filepath.Join(projectDir, "orbit_gen")
		bd, bi := genBigPage(scaleN)
		s.open("bigi", filepath.Join(g, "impl/pages/big/render.go"), bi)
		s.open("bigd", filepath.Join(g, "decl/pages/big/decl.go"), bd)
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			s.open(pkg+"i", filepath.Join(g, "impl/components", pkg, "render.go"), genCompImpl(fmt.Sprintf("C%d", i), pkg, cardImplOpts{}))
		}
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			s.open(pkg+"d", filepath.Join(g, "decl/components", pkg, "decl.go"), genCompDecl(fmt.Sprintf("C%d", i), pkg, cardProps))
		}
		t0 := time.Now()
		var wg sync.WaitGroup
		for name, d := range s.docs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := posOf(d.text, "package ", 0, len("package "), "utf-16")
				if _, _, err := s.c.call("textDocument/documentHighlight", s.tdp(name, p)); err != nil {
					r.p("highlight %s: %v", name, err)
				}
			}()
		}
		wg.Wait()
		barrier := time.Since(t0)
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
		r.p("- run %d: barrier (102 parallel documentHighlight) took %v; then references incomplete %d/%d %v", run, barrier.Round(time.Millisecond), len(missed), scaleN, missed)
		s.c.shutdown()
	}
	// Same barrier with orbit_gen written to disk (in a temp copy), no overlays.
	tmp, _ := os.MkdirTemp("", "orbitdisk")
	defer os.RemoveAll(tmp)
	os.CopyFS(tmp, os.DirFS(filepath.Dir(projectDir)))
	os.RemoveAll(filepath.Join(tmp, "bin"))
	root := filepath.Join(tmp, "project")
	g := filepath.Join(root, "orbit_gen")
	files := map[string]string{}
	for i := 0; i < scaleN; i++ {
		pkg := fmt.Sprintf("c%d", i)
		files[filepath.Join(g, "decl/components", pkg, "decl.go")] = genCompDecl(fmt.Sprintf("C%d", i), pkg, cardProps)
		files[filepath.Join(g, "impl/components", pkg, "render.go")] = genCompImpl(fmt.Sprintf("C%d", i), pkg, cardImplOpts{})
	}
	bd, bi := genBigPage(scaleN)
	files[filepath.Join(g, "decl/pages/big/decl.go")] = bd
	files[filepath.Join(g, "impl/pages/big/render.go")] = bi
	for p, t := range files {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(t), 0o644)
	}
	saved := projectDir
	projectDir = root
	t0 := time.Now()
	s, _, err := newSession(gopls, "refs3-disk.log", []string{"utf-8"}, true, nil)
	projectDir = saved
	if err != nil {
		r.p("init: %v", err)
		return r
	}
	var wg sync.WaitGroup
	for p, t := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.c.call("textDocument/documentHighlight", map[string]any{"textDocument": map[string]any{"uri": uriOf(p)}, "position": posOf(t, "package ", 0, len("package "), "utf-16")})
		}()
	}
	wg.Wait()
	r.p("- orbit_gen on disk, no overlays: process start → same barrier done %v", time.Since(t0).Round(time.Millisecond))
	s.c.shutdown()
	return r
}
