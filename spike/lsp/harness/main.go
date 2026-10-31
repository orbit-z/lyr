// Command harness drives gopls over stdio to answer the LSP questions of the
// Lyr milestone-0 spike (PLAN.md §9). See ../FINDINGS.md.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	dumpLineMode bool
	outSuffix    string
	spikeDir     string
	projectDir   string
)

func main() {
	dump := flag.String("dump", "", "write a copy of the project with lyr_gen to `dir` (for go build/vet sanity checks) and exit")
	gopls := flag.String("gopls", "", "gopls binary (default: spike bin/gopls)")
	dumpLine := flag.Bool("dumpline", false, "with -dump: emit Card's Props (with a doc comment and a deliberate posts.Postt error) using own-line //line directives")
	delay := flag.String("delay", "", "gopls diagnosticsDelay setting to send (e.g. 50ms)")
	out := flag.String("out", "", "suffix for result files (default none)")
	verbose := flag.Bool("verbose", false, "answer workspace/configuration with verboseOutput=true")
	only := flag.String("only", "", "run only this session: main, utf16, old, timing, hoverdoc, scale, refs, refs2, refs3, minimal, shape")
	flag.Parse()

	wd, _ := os.Getwd()
	spikeDir = wd
	if _, err := os.Stat(filepath.Join(wd, "project", "go.mod")); err != nil {
		log.Fatal("run from spike/lsp")
	}
	projectDir = filepath.Join(spikeDir, "project")
	if *gopls == "" {
		*gopls = filepath.Join(spikeDir, "bin", "gopls")
	}
	dumpLineMode = *dumpLine
	if *delay != "" {
		goplsSettings["diagnosticsDelay"] = *delay
	}
	outSuffix = *out
	if *verbose {
		goplsSettings["verboseOutput"] = true
	}
	if *dump != "" {
		if err := dumpProject(*dump); err != nil {
			log.Fatal(err)
		}
		return
	}
	if _, err := os.Stat(filepath.Join(projectDir, "lyr_gen")); err == nil {
		log.Fatal("project/lyr_gen exists on disk; the overlay-only checks need it absent")
	}
	sessions := []struct {
		name string
		fn   func(gopls string) *report
	}{
		{"main", sessionMain},
		{"utf16", sessionUTF16},
		{"old", sessionOld},
		{"timing", sessionTiming},
		{"hoverdoc", sessionHoverDoc},
		{"scale", sessionScale},
		{"refs", sessionRefs},
		{"refs2", sessionRefs2},
		{"refs3", sessionRefs3},
		{"minimal", sessionMinimal},
		{"shape", sessionShape},
	}
	for _, s := range sessions {
		if *only != "" && *only != s.name {
			continue
		}
		fmt.Println("== session", s.name)
		r := s.fn(*gopls)
		path := filepath.Join(spikeDir, "results", s.name+outSuffix+".md")
		if err := r.save(path); err != nil {
			log.Fatal(err)
		}
		fmt.Println("   wrote", path)
	}
}

type genSet struct {
	cardDecl, cardImpl, pageDecl, pageImpl string
}

func defaultGen() genSet {
	return genSet{
		cardDecl: genCardDecl(cardProps),
		cardImpl: genCardImpl(cardImplOpts{}),
		pageDecl: genPageDecl(),
		pageImpl: genPageImpl(pageImplOpts{scratch: scratchProps}),
	}
}

const scratchProps = `_ = "Héllo ✓"; _ = Card.Props{}`

func genPaths(root string) (cardDecl, cardImpl, pageDecl, pageImpl string) {
	g := filepath.Join(root, "lyr_gen")
	return filepath.Join(g, "decl/components/card/decl.go"),
		filepath.Join(g, "impl/components/card/render.go"),
		filepath.Join(g, "decl/pages/index/decl.go"),
		filepath.Join(g, "impl/pages/index/render.go")
}

func dumpProject(dir string) error {
	if err := os.CopyFS(dir, os.DirFS(spikeDir)); err != nil {
		return err
	}
	root := filepath.Join(dir, "project")
	os.RemoveAll(filepath.Join(dir, "bin"))
	gs := defaultGen()
	if dumpLineMode {
		gs.cardDecl = genCardDeclMode(strings.Replace(cardPropsDoc, "posts.Post ", "posts.Postt", 1), "line")
	}
	a, b, c, d := genPaths(root)
	for p, s := range map[string]string{a: gs.cardDecl, b: gs.cardImpl, c: gs.pageDecl, d: gs.pageImpl} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			return err
		}
	}
	return nil
}
