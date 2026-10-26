package doctor

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/orbit-z/orbit/internal/check"
)

// tsconfigFix is the change that lets the editor's TypeScript see the
// components' client modules (PLAN.md §5c).
const tsconfigFix = `in tsconfig.json, add "rootDirs": [".", "orbit_gen/types"] to compilerOptions and "orbit_gen/types/*.d.ts" to include`

// tsconfigCheck: when the project passes typed data to scripts (orbit.JSON
// or set:json), tsconfig.json overlays orbit_gen/types onto the project
// with rootDirs, so the editor's TypeScript resolves import "./X.orbit" in
// .ts files. orbit check and orbit lsp's TypeScript (for <script> blocks)
// don't need it: they use a generated tsconfig.
func (d *doctor) tsconfigCheck() Check {
	const name = "tsconfig"
	if !usesTypedData(filepath.Join(d.root, "src")) {
		return ok(name, "no typed script data (orbit.JSON, set:json) to check")
	}
	u, err := check.ReadUserTsconfig(d.root)
	if err != nil {
		return warn(name, "", "can't read tsconfig.json: %v", err)
	}
	if u == nil {
		return warn(name, "create tsconfig.json (orbit new writes one) with \"rootDirs\": [\".\", \"orbit_gen/types\"]",
			"no tsconfig.json, so the editor's TypeScript can't type import \"./X.orbit\" in .ts files")
	}
	for _, dir := range u.CompilerOptions.RootDirs {
		if path.Clean(filepath.ToSlash(dir)) == "orbit_gen/types" {
			return ok(name, "rootDirs includes orbit_gen/types")
		}
	}
	return warn(name, tsconfigFix, "tsconfig.json's rootDirs lacks orbit_gen/types, so the editor's TypeScript can't type import \"./X.orbit\" in .ts files")
}

// usesTypedData reports whether a .orbit file under src calls JSON (as
// orbit.JSON, under any import name) or has a set:json.
func usesTypedData(src string) bool {
	found := false
	filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if de.IsDir() || !strings.HasSuffix(p, ".orbit") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil && (strings.Contains(string(data), ".JSON(") || strings.Contains(string(data), "set:json")) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
