package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// sessionRefs measures how often references/rename issued from a decl
// file miss the dot-importing impl package, under three setups.
func sessionRefs(gopls string) *report {
	r := &report{}
	r.p("# Session refs: references of cN.Props.Featured from the decl file, %d components", scaleN)
	named := func(s, pkg string) string {
		s = strings.Replace(s, "\t. \"example.com/blog/lyr_gen/decl/components/"+pkg+"\"", "\tlyr__self \"example.com/blog/lyr_gen/decl/components/"+pkg+"\"", 1)
		s = strings.Replace(s, "props Props", "props lyr__self.Props", 1)
		s = strings.Replace(s, "Lyr__Render = lyr__render", "lyr__self.Lyr__Render = lyr__render", 1)
		return s
	}
	vi := 0
	for _, v := range []struct {
		name   string
		onDisk bool
		named  bool
		strip  bool
	}{
		{"overlays, dot-import (PLAN layout)", false, false, false},
		{"overlays, dot-import (PLAN layout), repeat", false, false, false},
		{"overlays, named import lyr__self", false, true, false},
		{"overlays, dot-import, all /*line*/ directives stripped", false, false, true},
		{"overlays, dot-import, all /*line*/ directives stripped, repeat", false, false, true},
		{"files on disk (no overlays), dot-import", true, false, false},
		{"files on disk AND identical overlays open, dot-import", true, false, false},
	} {
		vi++
		root := projectDir
		files := map[string]string{}
		g := filepath.Join(root, "lyr_gen")
		if v.onDisk {
			tmp, _ := os.MkdirTemp("", "lyrrefs")
			defer os.RemoveAll(tmp)
			os.CopyFS(tmp, os.DirFS(filepath.Dir(projectDir)))
			os.RemoveAll(filepath.Join(tmp, "bin"))
			root = filepath.Join(tmp, "project")
			g = filepath.Join(root, "lyr_gen")
		}
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			impl := genCompImpl(fmt.Sprintf("C%d", i), pkg, cardImplOpts{})
			if v.named {
				impl = named(impl, pkg)
			}
			files[filepath.Join(g, "decl/components", pkg, "decl.go")] = genCompDecl(fmt.Sprintf("C%d", i), pkg, cardProps)
			files[filepath.Join(g, "impl/components", pkg, "render.go")] = impl
		}
		bd, bi := genBigPage(scaleN)
		files[filepath.Join(g, "decl/pages/big/decl.go")] = bd
		files[filepath.Join(g, "impl/pages/big/render.go")] = bi
		if v.strip {
			for p, t := range files {
				files[p] = lineDirective.ReplaceAllString(t, "")
			}
		}
		if v.onDisk {
			for p, s := range files {
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte(s), 0o644)
			}
		}
		saved := projectDir
		projectDir = root
		s, _, err := newSession(gopls, fmt.Sprintf("refs-%d.log", vi), []string{"utf-8"}, true, nil)
		projectDir = saved
		if err != nil {
			r.p("init: %v", err)
			continue
		}
		openOverlays := !v.onDisk || strings.Contains(v.name, "AND")
		if openOverlays {
			m := s.c.diagMark()
			for p, txt := range files {
				s.open(p, p, txt)
			}
			for p := range files {
				s.waitVersion(m, p, 60*time.Second)
			}
		} else {
			time.Sleep(3 * time.Second)
		}
		s.settle(2*time.Second, 30*time.Second)
		miss, missImpl, other := 0, 0, 0
		var missed []string
		for i := 0; i < scaleN; i++ {
			pkg := fmt.Sprintf("c%d", i)
			dp := filepath.Join(g, "decl/components", pkg, "decl.go")
			res, _, err := s.c.call("textDocument/references", map[string]any{"textDocument": map[string]any{"uri": uriOf(dp)},
				"position": posOf(files[dp], "Featured bool", 0, 1, "utf-16"), "context": map[string]any{"includeDeclaration": true}})
			n := strings.Count(string(res), `"uri"`)
			if err != nil || n != 3 {
				miss++
				if !strings.Contains(string(res), "impl/components/"+pkg+"/") {
					missImpl++
				} else {
					other++
				}
				missed = append(missed, fmt.Sprintf("%s:%d", pkg, n))
			}
		}
		r.h("%s", v.name)
		r.p("- incomplete results: %d/%d (missing own impl: %d, other: %d) %v", miss, scaleN, missImpl, other, missed)
		s.c.shutdown()
	}
	return r
}

var lineDirective = regexp.MustCompile(`/\*line [^*]*\*/`)
