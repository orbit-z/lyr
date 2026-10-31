package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/compiler"
)

var lazyFiles = map[string]string{
	"src/components/Counter.lyr":  "<button>0</button>\n<script>console.log(\"counter\")</script>\n",
	"src/components/Icon.lyr":     "<i>i</i>\n<script>console.log(\"icon\")</script>\n<style>i{color:red}</style>\n",
	"src/components/Card.lyr":     "---\nimport Icon \"./Icon.lyr\"\n---\n<div><Icon /></div>\n<script>console.log(\"card\")</script>\n",
	"src/components/Tip.lyr":      "<span>tip</span>\n<script>console.log(\"tip\")</script>\n",
	"src/components/Panel.lyr":    "---\nimport (\n\tCard \"./Card.lyr\"\n\tTip \"./Tip.lyr\"\n)\n---\n<section><slot /><Card /><Tip client:idle /></section>\n",
	"src/components/Comments.lyr": "---\nimport Counter \"./Counter.lyr\"\n---\n<div><Counter client:visible /></div>\n",
	"src/pages/both.lyr":          "---\nimport Counter \"../components/Counter.lyr\"\n---\n<Counter /><Counter client:visible />\n",
	"src/pages/only.lyr":          "---\nimport Counter \"../components/Counter.lyr\"\n---\n<Counter client:visible=\"200px\" />\n",
	"src/pages/subtree.lyr":       "---\nimport Panel \"../components/Panel.lyr\"\n---\n<Panel client:visible />\n",
	"src/pages/slot.lyr":          "---\nimport (\n\tCounter \"../components/Counter.lyr\"\n\tPanel \"../components/Panel.lyr\"\n)\n---\n<Panel client:media=\"(min-width: 1px)\"><Counter /></Panel>\n",
	"src/pages/mixed.lyr":         "---\nimport (\n\tCard \"../components/Card.lyr\"\n\tPanel \"../components/Panel.lyr\"\n)\n---\n<Card /><Panel client:idle />\n",
	"src/pages/deferred.lyr":      "---\nimport Comments \"../components/Comments.lyr\"\n---\n<Comments server:defer />\n",
	"src/pages/load.lyr":          "---\nimport Counter \"../components/Counter.lyr\"\n---\n<Counter client:load />\n",
}

func lazyString(ls []assets.Lazy) string {
	var parts []string
	for _, l := range ls {
		var names []string
		for _, m := range l.Modules {
			names = append(names, strings.TrimSuffix(filepath.Base(m), ".lyr"))
		}
		parts = append(parts, strings.SplitN(l.ID, "-", 2)[0]+"["+strings.Join(names, " ")+"]")
	}
	return strings.Join(parts, " ")
}

// TestLazyLists checks the eager-reachability rule and the per-marker lists
// (PLAN.md §5 "Lazy entries").
func TestLazyLists(t *testing.T) {
	p := newTestProject(t, lazyFiles)
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Front() {
		t.Fatal(joinLines(diagStrings(proj.Diagnostics)))
	}
	want := map[string]struct {
		lazy    string
		islands bool
	}{
		"both":     {"", false},                          // eager wins
		"only":     {"Counter[Counter]", false},          // lazy only
		"subtree":  {"Panel[Icon Card] Tip[Tip]", false}, // whole subtree; the nested directive has its own list
		"slot":     {"Panel[Counter Icon Card] Tip[Tip]", false},
		"mixed":    {"Tip[Tip]", false}, // Card and Icon are eager, so Panel lists nothing
		"deferred": {"Counter[Counter]", true},
		"load":     {"", false},
	}
	for _, f := range proj.Files {
		if f.Info.Role != compiler.RolePage {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f.Path), ".lyr")
		w, ok := want[name]
		if !ok {
			t.Fatalf("unexpected page %s", f.Path)
		}
		if got := lazyString(proj.LazyLists(f)); got != w.lazy {
			t.Errorf("%s: lazy %q, want %q", name, got, w.lazy)
		}
		if got := proj.HasIslands(f); got != w.islands {
			t.Errorf("%s: islands %v, want %v", name, got, w.islands)
		}
	}
	var islands []string
	for _, f := range proj.Islands() {
		islands = append(islands, f.Path)
	}
	if got := strings.Join(islands, " "); got != "src/components/Comments.lyr" {
		t.Errorf("islands %s", got)
	}
}

// TestGenerateLazy runs the real bundler: eager wins (PLAN.md §13), lazy
// entries are separate bundles listed as lazy assets in both manifests,
// and styles stay in the page stylesheet.
func TestGenerateLazy(t *testing.T) {
	needGo(t)
	p := newTestProject(t, lazyFiles)
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	a := res.Project.Assets
	kinds := func(page string) string {
		var s []string
		for _, as := range a.Pages["src/pages/"+page+".lyr"] {
			k := as.Kind
			if as.ID != "" {
				k += ":" + strings.SplitN(as.ID, "-", 2)[0]
			}
			if as.Kind == "lazy" {
				k += "=" + regexp.MustCompile(`-[A-Z0-9]{8}\.js$`).ReplaceAllString(strings.TrimPrefix(as.URL, "/_lyr/"), "")
			}
			s = append(s, k)
		}
		return strings.Join(s, " ")
	}
	body := func(page, kind string) string {
		for _, as := range a.Pages["src/pages/"+page+".lyr"] {
			if as.Kind == kind {
				return string(a.File(as.URL).Contents)
			}
		}
		return ""
	}
	for page, want := range map[string]string{
		"both":     "js",
		"only":     "lazy:Counter=Counter",
		"subtree":  "css lazy:Panel=Icon lazy:Panel=Card lazy:Tip=Tip",
		"mixed":    "css js lazy:Tip=Tip",
		"deferred": "lazy:Counter=Counter",
		"load":     "js",
	} {
		if got := kinds(page); got != want {
			t.Errorf("%s: assets %q, want %q", page, got, want)
		}
	}
	if js := body("both", "js"); !strings.Contains(js, `"counter"`) {
		t.Errorf("both: Counter's script isn't in the page bundle:\n%s", js)
	}
	if css := body("subtree", "css"); !strings.Contains(css, "i[data-l-") {
		t.Errorf("subtree: Icon's style isn't in the page stylesheet:\n%s", css)
	}
	if js := body("mixed", "js"); !strings.Contains(js, `"card"`) || !strings.Contains(js, `"icon"`) || strings.Contains(js, `"tip"`) {
		t.Errorf("mixed: page bundle:\n%s", js)
	}
	if js := body("only", "lazy"); !strings.Contains(js, `"counter"`) {
		t.Errorf("only: lazy entry:\n%s", js)
	}
	// One entry per component, shared by pages.
	if body("only", "lazy") == "" || a.Pages["src/pages/only.lyr"][0].URL != a.Pages["src/pages/deferred.lyr"][0].URL {
		t.Errorf("Counter's lazy entry differs between pages")
	}

	m := p.read(ManifestFile)
	id := compiler.ComponentID("src/components/Counter.lyr")
	line := regexp.MustCompile(fmt.Sprintf(`\{Kind: "lazy", URL: "/_lyr/Counter-[A-Z0-9]{8}\.js", ID: %q\},`, id))
	if !line.MatchString(m) {
		t.Errorf("manifest has no lazy asset for Counter:\n%s", m)
	}

	dev := t.TempDir()
	if err := assets.WriteDev(dev, a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dev, assets.DevManifest))
	if err != nil {
		t.Fatal(err)
	}
	var dm struct {
		Pages map[string][]struct{ Kind, URL, ID string }
	}
	if err := json.Unmarshal(data, &dm); err != nil {
		t.Fatal(err)
	}
	if as := dm.Pages["src/pages/only.lyr"]; len(as) != 1 || as[0].Kind != "lazy" || as[0].ID != id {
		t.Errorf("dev manifest: %s", data)
	}
	if out, err := p.goCmd("build", "./..."); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}

// TestGenerateDeferPropsError: a deferred component whose Props can't be
// sent as JSON fails generate at the server:defer attribute, and nothing
// is written.
func TestGenerateDeferPropsError(t *testing.T) {
	needGo(t)
	p := newTestProject(t, map[string]string{
		"src/components/Comments.lyr": "---\ntype Props struct {\n\tPostID int\n\tOnLoad func()\n}\n---\n<p>{props.PostID}</p>\n",
		"src/pages/index.lyr":         "---\nimport Comments \"../components/Comments.lyr\"\n---\n<main>\n  <Comments server:defer postID={1} />\n</main>\n",
	})
	res := p.generate()
	want := "src/pages/index.lyr:5:13: <Comments server:defer>: Props.OnLoad has type func(), which can't be sent to a deferred component; props must be JSON-serializable"
	if got := joinLines(diagStrings(res.Diagnostics)); got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if res.Write != nil {
		t.Errorf("wrote %v", res.Write)
	}
}
