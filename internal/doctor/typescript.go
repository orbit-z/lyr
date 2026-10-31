package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/format"
	"github.com/orbit-z/lyr/internal/tsengine"
)

// typescriptCheck: TypeScript 7's language server, which lyr lsp runs
// for <script> blocks (PLAN.md §9b). Optional, like Bun: a warning at
// most, and inside a project only when some .lyr file has a script the
// editor's TypeScript features apply to.
func (d *doctor) typescriptCheck() Check {
	const name = "typescript"
	const off = "TypeScript features in <script> blocks are off in the editor"
	env := tsengine.Env{LookPath: d.env.LookPath, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Version: func(ctx context.Context, bin string) ([]byte, error) {
			return d.env.Run(ctx, d.dir(), bin, "--version")
		}}
	e, err := tsengine.Find(d.ctx, env, tsengine.Options{Root: d.root, BunX: true})
	if err == nil {
		if e.Via == "bun x" {
			return ok(name, "%s (fetched into Bun's cache on first use)", e.Describe())
		}
		return ok(name, "%s", e.Describe())
	}
	if d.root != "" && !hasEditorScripts(filepath.Join(d.root, "src")) {
		return ok(name, "not needed (no <script> blocks in .lyr files)")
	}
	var nf *tsengine.NotFound
	if errors.As(err, &nf) && len(nf.TooOld) > 0 {
		o := nf.TooOld[0]
		return warn(name, tsengine.Install, "%s is TypeScript %s, which has no language server (%s or later needed); %s", o.Path, o.Version, tsengine.Min, off)
	}
	return warn(name, tsengine.Install, "not found (%s or later needed); %s", tsengine.Min, off)
}

// hasEditorScripts reports whether a .lyr file under src has a script
// lyr lsp gives TypeScript features: an inline processed script, or a
// JavaScript one (is:inline, no type or a JavaScript type), without src.
func hasEditorScripts(src string) bool {
	found := false
	filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if de.IsDir() || !strings.HasSuffix(p, ".lyr") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), "<script") {
			return nil
		}
		f := compiler.Parse(string(data), compiler.Tolerant)
		if f.Template == nil {
			return nil
		}
		compiler.Walk(f.Template, func(n compiler.Node) bool {
			if s, ok := n.(*compiler.Script); ok {
				lang := format.BlockLang(s)
				if (lang == "ts" || lang == "js") && s.Attr("src") == nil {
					found = true
				}
				return false
			}
			return !found
		})
		if found {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
