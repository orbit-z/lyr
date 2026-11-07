package project

import (
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Page partials and blank imports (PLAN.md §4k).

func partialProjectFiles() map[string]string {
	files := maps.Clone(lazyFiles)
	maps.Copy(files, map[string]string{
		// Blank imports are eager roots, and count for the islands loader.
		"src/pages/blank.lyr":       "---\nimport _ \"../components/Panel.lyr\"\n---\n<p>b</p>\n",
		"src/pages/blankisland.lyr": "---\nimport _ \"../components/Wrap.lyr\"\n---\n<p>b</p>\n",
		// A blank import inside a lazy subtree is lazy with it.
		"src/components/Holder.lyr": "---\nimport _ \"./Counter.lyr\"\n---\n<b>h</b>\n",
		"src/pages/blanklazy.lyr":   "---\nimport Holder \"../components/Holder.lyr\"\n---\n<Holder client:idle />\n",
		// A partial: List (eager), Tip (lazy) and a deferred Feed.
		"src/components/List.lyr": "<ul>l</ul>\n<style>ul{color:red}</style>\n<script>console.log(\"list\")</script>\n",
		"src/pages/partials/p.lyr": "---\nimport (\n\tList \"../../components/List.lyr\"\n\tTip \"../../components/Tip.lyr\"\n\tFeed \"../../components/Feed.lyr\"\n)\n\n" +
			"const partial = true\n---\n<List /><Tip client:visible><i>t</i></Tip><Feed server:defer />\n<style>ul{color:blue}</style>\n",
		// The host renders Tip, blank-imports List.
		"src/pages/host.lyr": "---\nimport (\n\t_ \"../components/List.lyr\"\n\tTip \"../components/Tip.lyr\"\n)\n---\n<Tip />\n",
	})
	return files
}

func TestBlankImportGraphs(t *testing.T) {
	p := newTestProject(t, partialProjectFiles())
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Front() {
		t.Fatal(joinLines(diagStrings(proj.Diagnostics)))
	}
	byName := map[string]*File{}
	for _, f := range proj.Files {
		byName[strings.TrimSuffix(filepath.Base(f.Path), ".lyr")] = f
	}
	names := func(paths []string) string {
		var s []string
		for _, p := range paths {
			s = append(s, strings.TrimSuffix(filepath.Base(p), ".lyr"))
		}
		return strings.Join(s, " ")
	}
	for page, want := range map[string]struct {
		graph, lazy      string
		islands, delayed bool
	}{
		"blank":       {"Icon Card Tip Panel blank", "Tip[Tip]", false, false},
		"blankisland": {"Icon Card Feed Wrap blankisland", "Feed[Icon Card]", true, true},
		"blanklazy":   {"Counter Holder blanklazy", "Holder[Counter]", false, false},
		"host":        {"List Tip host", "", false, false},
	} {
		f := byName[page]
		if got := names(proj.AssetGraph(f)); got != want.graph {
			t.Errorf("%s: graph %q, want %q", page, got, want.graph)
		}
		if got := lazyString(proj.LazyLists(f)); got != want.lazy {
			t.Errorf("%s: lazy %q, want %q", page, got, want.lazy)
		}
		if got := proj.HasIslands(f); got != want.islands {
			t.Errorf("%s: islands %v", page, got)
		}
		if got := proj.HasDelayedIslands(f); got != want.delayed {
			t.Errorf("%s: delayed %v", page, got)
		}
	}
	// The partial's own <style>: a warning, at the tag (§4k (Q4)).
	if got := diagStrings(proj.Diagnostics); len(got) != 1 || !strings.Contains(got[0], "src/pages/partials/p.lyr:11:1: warning: "+compiler.MsgPartialStyle) {
		t.Errorf("diagnostics %q", got)
	}
	in := proj.AssetInput()
	for _, pg := range in.Pages {
		if want := pg.File == "src/pages/partials/p.lyr"; pg.Partial != want {
			t.Errorf("%s: Partial %v", pg.File, pg.Partial)
		}
	}
}

// TestGeneratePartial runs the real bundler and the generated app.go.
func TestGeneratePartial(t *testing.T) {
	needGo(t)
	p := newTestProject(t, partialProjectFiles())
	res := p.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	a := res.Project.Assets
	short := func(page string) []string {
		var s []string
		for _, as := range a.Pages[page] {
			s = append(s, as.Kind+":"+strings.SplitN(as.ID, "-", 2)[0]+"="+
				regexp.MustCompile(`-[A-Z0-9]{8}\.js$`).ReplaceAllString(strings.TrimPrefix(as.URL, "/_lyr/"), ""))
		}
		return s
	}
	// List and Tip have scripts: List for the runner, Tip for its lazy
	// URL's ID; Feed's subtree (Card, Icon) is served by the island.
	if got, want := short("src/pages/partials/p.lyr"), []string{
		"component:List=List", "component:Tip=Tip", "component:Icon=Icon", "component:Card=Card", "lazy:Tip=Tip",
	}; !slices.Equal(got, want) {
		t.Errorf("partial assets %q, want %q", got, want)
	}
	// The host's bundle marks List (blank-imported) and Tip.
	var hostJS string
	for _, as := range a.Pages["src/pages/host.lyr"] {
		if as.Kind == "js" {
			hostJS = string(a.File(as.URL).Contents)
		}
	}
	for _, f := range []string{"src/components/List.lyr", "src/components/Tip.lyr"} {
		if !strings.Contains(hostJS, `"`+compiler.ComponentID(f)+`"`) {
			t.Errorf("host bundle doesn't mark %s:\n%s", f, hostJS)
		}
	}
	app := p.read("lyr_gen/app/app.go")
	route := regexp.MustCompile(`(?s)File:\s+"src/pages/partials/p\.lyr",.*?\n\t\t},`).FindString(app)
	for _, want := range []string{"Partial", "Islands"} {
		if !regexp.MustCompile(want + `:\s+true`).MatchString(route) {
			t.Errorf("partial route lacks %s:\n%s", want, route)
		}
	}
	if len(regexp.MustCompile(`Partial:\s+true`).FindAllString(app, -1)) != 1 {
		t.Errorf("Partial set on other routes:\n%s", app)
	}
	if _, err := p.goCmd("build", "./..."); err != nil {
		t.Fatal(err)
	}
}
