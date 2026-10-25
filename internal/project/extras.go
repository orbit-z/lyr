package project

import (
	"bytes"
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// GenFile is one file to write under orbit_gen/.
type GenFile struct {
	// Path is relative to the module root, "/"-separated.
	Path    string
	Content []byte
}

// Paths of the extra generated files (PLAN.md §3.1).
const (
	AppFile      = compiler.GenDir + "/app/app.go"
	ManifestFile = compiler.GenDir + "/manifest/manifest.go"
	ServerFile   = compiler.GenDir + "/cmd/server/main.go"
)

// GenFiles returns every generated file: each .orbit file's decl.go and
// render.go (its final Output), then orbit_gen/app, orbit_gen/manifest and,
// when the project has no server entry, orbit_gen/cmd/server.
func (p *Project) GenFiles() []GenFile {
	files := make([]GenFile, 0, 2*len(p.Files)+3)
	for _, f := range p.Files {
		files = append(files,
			GenFile{Path: f.Output.Decl.Path, Content: f.Output.Decl.Content},
			GenFile{Path: f.Output.Impl.Path, Content: f.Output.Impl.Content})
	}
	files = append(files, GenFile{Path: AppFile, Content: p.appGo()}, GenFile{Path: ManifestFile, Content: p.manifestGo()})
	if p.GeneratedServer {
		files = append(files, GenFile{Path: ServerFile, Content: p.serverGo()})
	}
	return files
}

// pageAlias is the import name of a page's decl package in app.go: its Rel
// with "_" doubled and "/" turned into "_" (pages/blog/slug_param →
// pages_blog_slug__param). Segments never start with "_" (PackageFor
// prefixes them), so distinct Rels give distinct aliases, and every alias
// starts with "pages_", so none is orbit or manifest.
func pageAlias(pkg compiler.GenPackage) string {
	return strings.ReplaceAll(strings.ReplaceAll(pkg.Rel, "_", "__"), "/", "_")
}

// appGo generates orbit_gen/app/app.go: the route table, the imports that
// link every implementation package, and New (PLAN.md §3.1, §3.2).
func (p *Project) appGo() []byte {
	var b bytes.Buffer
	b.WriteString(compiler.GenHeader + "\n\n")
	fmt.Fprintf(&b, "// Package app wires the pages of %s into an orbit.Site: the\n", p.Module)
	b.WriteString("// route table, the imports that link every component, and New.\n")
	b.WriteString("package app\n\nimport (\n")
	fmt.Fprintf(&b, "\torbit %q\n\n", compiler.RuntimePath)
	fmt.Fprintf(&b, "\t%q\n", p.Module+"/"+compiler.GenDir+"/manifest")
	var pages []*File
	for _, f := range p.Files {
		if f.Info.Role == compiler.RolePage {
			pages = append(pages, f)
		}
	}
	if len(pages) > 0 {
		b.WriteString("\n")
		for _, f := range pages {
			fmt.Fprintf(&b, "\t%s %q\n", pageAlias(f.Pkg), f.Pkg.DeclImportPath(p.Module))
		}
	}
	if len(p.Files) > 0 {
		b.WriteString("\n")
		for _, f := range p.Files {
			fmt.Fprintf(&b, "\t_ %q\n", f.Pkg.ImplImportPath(p.Module))
		}
	}
	b.WriteString(")\n\n")
	b.WriteString("// New returns the site: every page of src/pages, with its assets. Options\n")
	b.WriteString("// passed here come after the ones from orbit.json, so they win.\n")
	b.WriteString("func New(opts ...orbit.Option) *orbit.Site {\n")
	if p.Config.Site != "" {
		fmt.Fprintf(&b, "\topts = append([]orbit.Option{orbit.WithSite(%s)}, opts...)\n", strconv.Quote(p.Config.Site))
	}
	b.WriteString("\treturn orbit.NewSite(routes(), nil, opts...)\n}\n\n")
	b.WriteString("// routes returns a fresh route table, sorted by file.\n")
	b.WriteString("func routes() []orbit.Route {\n\treturn []orbit.Route{\n")
	for _, f := range pages {
		a := pageAlias(f.Pkg)
		b.WriteString("\t\t{\n")
		fmt.Fprintf(&b, "\t\t\tPattern: %s,\n", strconv.Quote(f.Info.Route))
		fmt.Fprintf(&b, "\t\t\tFile: %s,\n", strconv.Quote(f.Path))
		fmt.Fprintf(&b, "\t\t\tRender: func(c *orbit.Ctx, w *orbit.Writer) error {\n\t\t\t\treturn %s.Render(c, w, %s.Props{}, orbit.Slots{})\n\t\t\t},\n", a, a)
		fmt.Fprintf(&b, "\t\t\tAssets: manifest.Pages[%s],\n", strconv.Quote(f.Path))
		b.WriteString("\t\t},\n")
	}
	b.WriteString("\t}\n}\n")
	return gofmt(b.Bytes())
}

// manifestGo generates orbit_gen/manifest/manifest.go. Milestone 6 fills it
// with each page's bundled assets; until then it is empty.
func (p *Project) manifestGo() []byte {
	var b bytes.Buffer
	b.WriteString(compiler.GenHeader + "\n\n")
	b.WriteString("// Package manifest lists the assets of every page.\n")
	b.WriteString("package manifest\n\n")
	fmt.Fprintf(&b, "import orbit %q\n\n", compiler.RuntimePath)
	b.WriteString("// Pages maps a page's .orbit path to its CSS and JS assets.\n")
	b.WriteString("var Pages = map[string][]orbit.Asset{}\n")
	return gofmt(b.Bytes())
}

// serverGo generates the default server entry, orbit_gen/cmd/server.
func (p *Project) serverGo() []byte {
	var b bytes.Buffer
	b.WriteString(compiler.GenHeader + "\n\n")
	b.WriteString("// Command server is the default server entry, generated because the project\n")
	b.WriteString("// has no cmd/server: it serves the pages and nothing else.\n")
	b.WriteString("package main\n\nimport (\n\t\"log\"\n\n")
	fmt.Fprintf(&b, "\torbit %q\n\n", compiler.RuntimePath)
	fmt.Fprintf(&b, "\t%q\n)\n\n", p.Module+"/"+compiler.GenDir+"/app")
	// Not log.Fatal(orbit.ListenAndServe(…)): log.Fatal exits 1 even for
	// the nil of a graceful shutdown.
	b.WriteString("func main() {\n\tif err := orbit.ListenAndServe(app.New().Handler()); err != nil {\n\t\tlog.Fatal(err)\n\t}\n}\n")
	return gofmt(b.Bytes())
}

// gofmt formats generated Go that has no source map (the extras); it is
// already valid, so an error leaves it as is.
func gofmt(src []byte) []byte {
	if out, err := format.Source(src); err == nil {
		return out
	}
	return src
}
