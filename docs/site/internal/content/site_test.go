package content

import (
	"flag"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite src/styles/highlight.css")

// TestHighlightCSS checks that src/styles/highlight.css is the stylesheet
// of the Chroma themes in use; go test ./internal/content -update writes it.
func TestHighlightCSS(t *testing.T) {
	const file = "../../src/styles/highlight.css"
	want := HighlightCSS()
	if *update {
		if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date; run go test ./internal/content -update", file)
	}
}

func TestLaunchContent(t *testing.T) {
	d := load(t)

	// DECISIONS.md is the maintainers' log: not a page, not in the nav or search.
	if d.Page("decisions") != nil {
		t.Errorf("DECISIONS.md is a page")
	}
	for _, g := range d.Nav {
		for _, it := range g.Items {
			if strings.Contains(strings.ToLower(it.URL), "decisions") {
				t.Errorf("nav links %s", it.URL)
			}
		}
	}
	if rs := d.Search("decisions log"); len(rs) > 0 && rs[0].Page.File == "DECISIONS.md" {
		t.Errorf("DECISIONS.md in search")
	}

	// The changelog is a page, and gives the version.
	if p := d.Page("changelog"); p == nil || p.File != "../CHANGELOG.md" {
		t.Fatalf("no changelog page")
	}
	if !strings.HasPrefix(d.Version, "v") {
		t.Errorf("version %q", d.Version)
	}

	for _, p := range d.Pages {
		if n := utf8.RuneCountInString(p.Description); n > 161 || n == 0 {
			t.Errorf("%s: description of %d characters: %q", p.File, n, p.Description)
		}
	}

	// Code blocks are highlighted, orbit blocks with Orbit's lexer.
	html := string(d.Page("components").HTML)
	if !strings.Contains(html, `<pre class="chroma"`) || !strings.Contains(html, `<span class="k">`) {
		t.Errorf("components.md: code not highlighted")
	}

	// The example blog is a link to GitHub in the nav.
	found := false
	for _, g := range d.Nav {
		for _, it := range g.Items {
			if it.External && strings.HasPrefix(it.URL, RepoURL+"examples/blog") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no external nav link to the example blog: %+v", d.Nav)
	}
}

func TestDescription(t *testing.T) {
	long := strings.Repeat("word ", 50)
	for in, want := range map[string]string{
		"Short.": "Short.",
		"First sentence that is long enough to keep on its own here, really it is. " + long: "First sentence that is long enough to keep on its own here, really it is.",
	} {
		if got := description(in); got != want {
			t.Errorf("description(%q) = %q, want %q", in, got, want)
		}
	}
	if got := description(long); !strings.HasSuffix(got, "word…") || utf8.RuneCountInString(got) > 161 {
		t.Errorf("description(long) = %q", got)
	}
}
