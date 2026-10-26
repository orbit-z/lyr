package project

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

var graphFiles = map[string]string{
	// Base imports Nav; Card and Badge import each other (mutual
	// recursion); about has no assets.
	"src/layouts/Base.orbit":   "---\nimport Nav \"../components/Nav.orbit\"\n---\n<html><head></head><body><Nav /><slot /></body></html>\n<style is:global>body{margin:0}</style>\n",
	"src/components/Nav.orbit": "<nav>nav</nav>\n<style>nav{color:red}</style>\n",
	"src/components/Card.orbit": "---\nimport Badge \"./Badge.orbit\"\n---\n<div class=\"card\"><Badge /></div>\n<style>.card{color:blue}</style>\n" +
		"<script>\n  console.log(\"card\");\n</script>\n",
	"src/components/Badge.orbit": "---\nimport Card \"./Card.orbit\"\n---\n{if false}<Card />{/if}<b>badge</b>\n<style>b{color:green}</style>\n",
	"src/pages/index.orbit":      "---\nimport (\n\tBase \"../layouts/Base.orbit\"\n\tCard \"../components/Card.orbit\"\n)\n---\n<Base><Card /></Base>\n<style>main{color:gray}</style>\n",
	"src/pages/about.orbit":      "<p>about</p>\n",
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
		if f.Path == "src/pages/index.orbit" {
			index = f
		}
	}
	got := proj.AssetGraph(index)
	want := []string{"src/components/Nav.orbit", "src/layouts/Base.orbit", "src/components/Badge.orbit", "src/components/Card.orbit", "src/pages/index.orbit"}
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
	link := regexp.MustCompile(`"src/pages/index.orbit": \{\n\t\t\{Kind: "css", URL: "/_orbit/index-[A-Z0-9]{8}\.css"\},\n\t\t\{Kind: "js", URL: "/_orbit/index-[A-Z0-9]{8}\.js"\},\n\t\},`)
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
	css := res.Project.Assets.File(res.Project.Assets.Pages["src/pages/index.orbit"][0].URL)
	order := []string{"nav[data-o-", "body{", "b[data-o-", ".card[data-o-", "main[data-o-"}
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
		"src/pages/index.orbit": "<p>x</p>\n<script>\n  import x from \"./nope\";\n  console.log(x);\n</script>\n",
	}
	p := newTestProject(t, files)
	res := p.generate()
	got := joinLines(diagStrings(res.Diagnostics))
	if want := `src/pages/index.orbit:3:17: Could not resolve "./nope"`; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if res.Write != nil {
		t.Errorf("a failed bundle wrote orbit_gen/")
	}
}

// TestScopedBeatsGlobal is the docs-site dogfood case: a layout's global
// `a` rule and a scoped `a` rule in a component the layout renders. The
// component's CSS comes first in the bundle, so the scoped rule must win
// on specificity: a[data-o-…] is (0,1,1), the global a is (0,0,1).
func TestScopedBeatsGlobal(t *testing.T) {
	needGo(t)
	files := map[string]string{
		"src/layouts/Base.orbit":    "---\nimport Link \"../components/Link.orbit\"\n---\n<html><head></head><body><Link /><slot /></body></html>\n<style is:global>a{color:red}</style>\n",
		"src/components/Link.orbit": "<a href=\"/\">home</a>\n<style>a{color:navy}</style>\n",
		"src/pages/index.orbit":     "---\nimport Base \"../layouts/Base.orbit\"\n---\n<Base><p>x</p></Base>\n",
	}
	p := newTestProject(t, files)
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	css := string(res.Project.Assets.File(res.Project.Assets.Pages["src/pages/index.orbit"][0].URL).Contents)
	scoped := "a[" + compiler.ScopeAttr("src/components/Link.orbit") + "]{color:navy}"
	s, g := strings.Index(css, scoped), strings.Index(css, "a{color:red}")
	if s < 0 || g < 0 || s > g {
		t.Errorf("want the scoped %q (specificity 0,1,1) before the global a{color:red} (0,0,1):\n%s", scoped, css)
	}
}
