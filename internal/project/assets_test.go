package project

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

var graphFiles = map[string]string{
	// Base imports Nav; Card and Badge import each other (mutual
	// recursion); about has no assets.
	"src/layouts/Base.lyr":   "---\nimport Nav \"../components/Nav.lyr\"\n---\n<html><head></head><body><Nav /><slot /></body></html>\n<style is:global>body{margin:0}</style>\n",
	"src/components/Nav.lyr": "<nav>nav</nav>\n<style>nav{color:red}</style>\n",
	"src/components/Card.lyr": "---\nimport Badge \"./Badge.lyr\"\n---\n<div class=\"card\"><Badge /></div>\n<style>.card{color:blue}</style>\n" +
		"<script>\n  console.log(\"card\");\n</script>\n",
	"src/components/Badge.lyr": "---\nimport Card \"./Card.lyr\"\n---\n{if false}<Card />{/if}<b>badge</b>\n<style>b{color:green}</style>\n",
	"src/pages/index.lyr":      "---\nimport (\n\tBase \"../layouts/Base.lyr\"\n\tCard \"../components/Card.lyr\"\n)\n---\n<Base><Card /></Base>\n<style>main{color:gray}</style>\n",
	"src/pages/about.lyr":      "<p>about</p>\n",
}

func TestAssetGraph(t *testing.T) {
	p := newTestProject(t, graphFiles)
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Front() {
		t.Fatal(joinLines(diagStrings(proj.Diagnostics)))
	}
	var index *File
	for _, f := range proj.Files {
		if f.Path == "src/pages/index.lyr" {
			index = f
		}
	}
	got := proj.AssetGraph(index)
	want := []string{"src/components/Nav.lyr", "src/layouts/Base.lyr", "src/components/Badge.lyr", "src/components/Card.lyr", "src/pages/index.lyr"}
	if !slices.Equal(got, want) {
		t.Errorf("graph\n got %v\nwant %v", got, want)
	}
}

func TestGenerateManifest(t *testing.T) {
	needGo(t)
	p := newTestProject(t, graphFiles)
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	m := p.read(ManifestFile)
	link := regexp.MustCompile(`"src/pages/index.lyr": \{\n\t\t\{Kind: "css", URL: "/_lyr/index-[A-Z0-9]{8}\.css"\},\n\t\t\{Kind: "js", URL: "/_lyr/index-[A-Z0-9]{8}\.js"\},\n\t\},`)
	if !link.MatchString(m) {
		t.Errorf("manifest:\n%s", m)
	}
	if strings.Contains(m, "about") {
		t.Errorf("a page without assets is listed:\n%s", m)
	}
	if res.Timings.Assets == 0 {
		t.Errorf("no asset timing")
	}
	// The order follows the graph.
	css := res.Project.Assets.File(res.Project.Assets.Pages["src/pages/index.lyr"][0].URL)
	order := []string{"nav[data-l-", "body{", "b[data-l-", ".card[data-l-", "main[data-l-"}
	last := -1
	for _, s := range order {
		i := strings.Index(string(css.Contents), s)
		if i <= last {
			t.Errorf("CSS order: %q at %d:\n%s", s, i, css.Contents)
		}
		last = i
	}
	// The project compiles with the filled manifest.
	if out, err := p.goCmd("build", "./..."); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}

func TestGenerateAssetError(t *testing.T) {
	needGo(t)
	files := map[string]string{
		"src/pages/index.lyr": "<p>x</p>\n<script>\n  import x from \"./nope\";\n  console.log(x);\n</script>\n",
	}
	p := newTestProject(t, files)
	res := p.generate()
	got := joinLines(diagStrings(res.Diagnostics))
	if want := `src/pages/index.lyr:3:17: Could not resolve "./nope"`; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if res.Write != nil {
		t.Errorf("a failed bundle wrote lyr_gen/")
	}
}

// TestScopedBeatsGlobal is the docs-site dogfood case: a layout's global
// `a` rule and a scoped `a` rule in a component the layout renders. The
// component's CSS comes first in the bundle, so the scoped rule must win
// on specificity: a[data-l-…] is (0,1,1), the global a is (0,0,1).
func TestScopedBeatsGlobal(t *testing.T) {
	needGo(t)
	files := map[string]string{
		"src/layouts/Base.lyr":    "---\nimport Link \"../components/Link.lyr\"\n---\n<html><head></head><body><Link /><slot /></body></html>\n<style is:global>a{color:red}</style>\n",
		"src/components/Link.lyr": "<a href=\"/\">home</a>\n<style>a{color:navy}</style>\n",
		"src/pages/index.lyr":     "---\nimport Base \"../layouts/Base.lyr\"\n---\n<Base><p>x</p></Base>\n",
	}
	p := newTestProject(t, files)
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	css := string(res.Project.Assets.File(res.Project.Assets.Pages["src/pages/index.lyr"][0].URL).Contents)
	scoped := "a[" + compiler.ScopeAttr("src/components/Link.lyr") + "]{color:navy}"
	s, g := strings.Index(css, scoped), strings.Index(css, "a{color:red}")
	if s < 0 || g < 0 || s > g {
		t.Errorf("want the scoped %q (specificity 0,1,1) before the global a{color:red} (0,0,1):\n%s", scoped, css)
	}
}
