package check

import (
	"strings"
	"testing"
)

// likeCard is a component with typed data (PLAN.md §5c): its <script>
// starts on line 12.
func likeCard(script string) string {
	return "---\ntype Post struct {\n\tSlug string   `json:\"slug\"`\n\tTags []string `json:\"tags\"`\n}\n\ntype Props struct {\n\tPost Post\n}\n---\n" +
		"<like-card data-post={orbit.JSON(props.Post)} data-n={orbit.JSON(3)}></like-card>\n" +
		"<script>\n" + script + "\n</script>\n"
}

// TestCheckBridge: a script's use of its component's data is checked
// against the Go types, and errors map to the .orbit position.
func TestCheckBridge(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>", "console.log(1);"),
		"src/components/Like.orbit": likeCard(`import { data } from "./Like.orbit";
const el = document.body;
const post = data(el, "post");
const n: string = data(el, "n");
console.log(post.slug, post.tags.length, n, data(el, "psot"));`),
		"src/scripts/use.ts": "import { data } from \"../components/Like.orbit\";\nexport const slug: number = data(document.body, \"post\").slug;\n",
	})
	res, out := output(t, dir)
	for _, want := range []string{
		"src/components/Like.orbit:16:7: error TS2322: Type 'number' is not assignable to type 'string'.",
		"src/components/Like.orbit:17:24: error TS18047: 'post.tags' is possibly 'null'.",
		`src/components/Like.orbit:17:54: error TS2345: Argument of type '"psot"' is not assignable to parameter of type '"n" | "post"'.`,
		"src/scripts/use.ts:2:14: error TS2322: Type 'string' is not assignable to type 'number'.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s", want)
		}
	}
	if !res.Failed() || strings.Count(out, "error TS") != 4 || strings.Contains(out, "orbit_gen/") || t.Failed() {
		t.Errorf("failed=%v, output:\n%s", res.Failed(), out)
	}
}

// TestCheckBridgeNoModule: a component without typed data has no module, so
// importing it is TS2307 at the import.
func TestCheckBridgeNoModule(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>", "import { data } from \"./Card.orbit\";\nconsole.log(data);"),
	})
	_, out := output(t, dir)
	if want := "src/components/Card.orbit:8:22: error TS2307: Cannot find module './Card.orbit' or its corresponding type declarations.\n"; out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
