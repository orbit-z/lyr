package doctor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/tailwind"
)

// tailwindCheck: the Tailwind CSS v4 CLI (PLAN.md §5d). An error inside a
// project with a Tailwind input, since lyr build fails without it; a
// warning when .gitignore lets Tailwind scan generated code and build
// output (slower runs); otherwise informational.
func (d *doctor) tailwindCheck() Check {
	const name = "tailwind"
	env := tailwind.Env{LookPath: d.env.LookPath, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Help: func(ctx context.Context, bin string, args []string) ([]byte, error) {
			return d.env.Run(ctx, d.dir(), bin, append(append([]string(nil), args...), "--help")...)
		}}
	cli, err := tailwind.Find(d.ctx, env, d.root)
	input := ""
	if d.root != "" {
		input = tailwindInput(d.root)
	}
	if err != nil {
		if input != "" {
			return fail(name, tailwind.Install(runtime.GOOS, runtime.GOARCH), "%v; needed by %s", err, input)
		}
		return ok(name, "not installed (needed only by projects that use Tailwind)")
	}
	found := cli.Describe()
	if cli.Via == "bun x" {
		found += " (fetched into Bun's cache on first use)"
	}
	if input == "" {
		return ok(name, "%s", found)
	}
	data, _ := os.ReadFile(filepath.Join(d.root, ".gitignore"))
	ignored := ignoredNames(string(data))
	var missing []string
	for _, n := range []string{"lyr_gen", ".lyr", "dist"} {
		if !ignored[n] {
			missing = append(missing, n+"/")
		}
	}
	if len(missing) > 0 {
		return warn(name, "printf '"+strings.Join(missing, `\n`)+`\n' >> .gitignore`,
			"%s; .gitignore doesn't cover %s, so Tailwind scans them (slower runs)", found, strings.Join(missing, ", "))
	}
	return ok(name, "%s, used by %s", found, input)
}

// tailwindInput returns the first Tailwind input under the project's src/
// (a .css file or a <style> block, by project-relative path), or "".
func tailwindInput(root string) string {
	found := ""
	filepath.WalkDir(filepath.Join(root, "src"), func(p string, de fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipAll
		}
		if de.IsDir() {
			return nil
		}
		isCSS, isLyr := strings.HasSuffix(p, ".css"), strings.HasSuffix(p, ".lyr")
		if !isCSS && !isLyr {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), "@") {
			return nil
		}
		hit := false
		if isCSS {
			hit = tailwind.Detect(string(data)).Input
		} else if strings.Contains(string(data), "<style") {
			for _, st := range compiler.Styles(compiler.Parse(string(data), compiler.Tolerant)) {
				if tailwind.Detect(st.Content.Text).Input {
					hit = true
					break
				}
			}
		}
		if hit {
			rel, _ := filepath.Rel(root, p)
			found = filepath.ToSlash(rel)
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
