package build

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// EmbedTag is the build tag of an orbit build --embed server: it compiles
// orbit_gen/app/embed.go, which makes app.New() serve orbit_gen/static.
const EmbedTag = "orbit_embed"

// writeStatic is PLAN.md §6 orbit build step 6 for --embed: it replaces
// orbit_gen/static/client with a copy of the staged dist/client (go:embed
// can't reach outside the package directory) and writes static.go. The
// generated doc.go stays, so the package exists without the build tag.
func writeStatic(root, client string) error {
	dir := filepath.Join(root, filepath.FromSlash(project.StaticDir))
	if err := os.RemoveAll(filepath.Join(dir, "client")); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.CopyFS(filepath.Join(dir, "client"), os.DirFS(client)); err != nil {
		return err
	}
	hasFiles := false
	fs.WalkDir(os.DirFS(client), ".", func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			hasFiles = true
			return fs.SkipAll
		}
		return nil
	})
	return os.WriteFile(filepath.Join(dir, "static.go"), staticGo(hasFiles), 0o644)
}

// staticGo is orbit_gen/static/static.go. go:embed rejects a directory with
// no files, so a site without assets or public/ files embeds nothing.
func staticGo(hasFiles bool) []byte {
	var b bytes.Buffer
	b.WriteString(compiler.GenHeader + "\n\n")
	b.WriteString("//go:build " + EmbedTag + "\n\n")
	b.WriteString("// Package static holds dist/client, embedded by orbit build --embed.\n")
	b.WriteString("package static\n\n")
	if !hasFiles {
		b.WriteString("import \"embed\"\n\n")
		b.WriteString("// FS is empty: the site has no assets and no public/ files.\n")
		b.WriteString("var FS embed.FS\n")
		return b.Bytes()
	}
	b.WriteString("import (\n\t\"embed\"\n\t\"io/fs\"\n)\n\n")
	b.WriteString("//go:embed all:client\nvar files embed.FS\n\n")
	b.WriteString("// FS is dist/client: the /_orbit/ assets and the public/ files.\n")
	b.WriteString("var FS, _ = fs.Sub(files, \"client\")\n")
	return b.Bytes()
}

// embedTagsFlag returns the -tags flag for an --embed go build: the tags
// already in goflags (the effective GOFLAGS, from go env, so a go env -w
// setting counts too) plus EmbedTag. A -tags on the command line replaces
// the one in GOFLAGS, so both are passed.
func embedTagsFlag(goflags string) string {
	out := goflags
	tags := ""
	for _, f := range strings.Fields(out) {
		f = strings.TrimPrefix(f, "-")
		if v, ok := strings.CutPrefix(f, "-tags="); ok {
			tags = v
		} else if v, ok := strings.CutPrefix(f, "tags="); ok {
			tags = v
		}
	}
	tags = strings.Trim(strings.ReplaceAll(tags, " ", ","), ",")
	if tags == "" {
		return "-tags=" + EmbedTag
	}
	return "-tags=" + tags + "," + EmbedTag
}
