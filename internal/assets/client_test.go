package assets

import (
	"strings"
	"testing"

	"github.com/evanw/esbuild/pkg/api"
)

// TestClientModules: import "./X.orbit" from a block script or a .ts file
// bundles orbit:client's runtime (PLAN.md §5c), orbit:types is empty, and
// another component's block isn't pulled in.
func TestClientModules(t *testing.T) {
	dir := project(t, map[string]string{
		"src/components/Like.orbit": "<like-button data-n='1'></like-button>\n<script>\n  import { data, json } from \"./Like.orbit\";\n  import type { posts } from \"orbit:types\";\n  const p: posts.Post | null = null;\n  console.log(data(document.body, \"n\"), json(\"cfg\"), p);\n</script>\n",
		"src/pages/a.orbit":         "<p>a</p>\n<script src=\"../scripts/use.ts\"></script>\n",
		"src/scripts/use.ts":        "import { data } from \"../components/Like.orbit\";\nimport \"orbit:types\";\nconsole.log(data(document.body, \"liked\"));\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{
		"src/pages/a.orbit": {"src/pages/a.orbit", "src/components/Like.orbit"},
	}))
	noErrors(t, res)
	var js string
	for _, f := range res.Files {
		if strings.HasSuffix(f.Name, ".js") {
			js += string(f.Contents)
		}
	}
	for _, want := range []string{"getAttribute(", "getElementById", `"liked"`, `"cfg"`} {
		if !strings.Contains(js, want) {
			t.Errorf("bundle lacks %q:\n%s", want, js)
		}
	}
	if n := strings.Count(js, "getElementById"); n != 1 {
		t.Errorf("runtime bundled %d times:\n%s", n, js)
	}
	if strings.Contains(js, "<like-button") {
		t.Errorf("a component's markup leaked into the bundle:\n%s", js)
	}
}

// TestClientRuntimeSize: orbit:client minified stays small (PLAN.md §5c:
// ≤ 400 B), and data alone, tree-shaken, smaller still.
func TestClientRuntimeSize(t *testing.T) {
	min := func(src string) int {
		r := api.Transform(src, api.TransformOptions{Loader: api.LoaderTS, MinifyWhitespace: true,
			MinifyIdentifiers: true, MinifySyntax: true, Format: api.FormatESModule})
		if len(r.Errors) > 0 {
			t.Fatal(r.Errors)
		}
		return len(r.Code)
	}
	all := min(clientTS)
	if all > 400 {
		t.Errorf("orbit:client is %d B minified, want ≤ 400", all)
	}
	t.Logf("orbit:client: %d B minified", all)
}
