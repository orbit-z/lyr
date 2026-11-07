package compiler

import (
	"slices"
	"strings"
	"testing"
)

// anPage analyzes src as the page src/pages/x.lyr.
func anPage(t *testing.T, src string) *Analysis {
	t.Helper()
	f := Parse(src, Strict)
	a := Analyze(f, FileInfo{Path: "src/pages/x.lyr", Role: RolePage, Route: "/x"})
	checkAnalysis(t, f, a)
	return a
}

// TestAnalyzePartial: the forms of the partial flag a page accepts, and
// Analysis.Partial (PLAN.md §4k).
func TestAnalyzePartial(t *testing.T) {
	tests := []struct {
		src     string
		partial bool
	}{
		{"---\n---\n<p/>", false},
		{"---\nconst partial = true\n---\n<p/>", true},
		{"---\nconst partial = false\n---\n<p/>", false},
		{"---\nconst partial bool = true\n---\n<p/>", true},
		{"---\nconst (\n\tx = 1\n\tpartial = true\n)\n---\n<p>{x}</p>", true},
		{"---\nconst x, partial = 1, true\n---\n<p>{x}</p>", true},
		{"---\nconst partial = true\nif partial {\n\tpartial := 2\n\t_ = partial\n}\n---\n<p/>", true},
		{"---\nconst Partial = 1\n---\n<p>{Partial}</p>", false},
		{"---\nfunc f() { var partial = 1; _ = partial }\n---\n<p/>", false},
	}
	for _, tt := range tests {
		a := anPage(t, tt.src)
		if len(a.Diagnostics) > 0 {
			t.Errorf("%q: %q", tt.src, anMessages(a))
		}
		if a.Partial != tt.partial {
			t.Errorf("%q: Partial = %v, want %v", tt.src, a.Partial, tt.partial)
		}
	}
}

// TestAnalyzePartialErrors: each row of §4k's error table, with the text at
// the reported position.
func TestAnalyzePartialErrors(t *testing.T) {
	const (
		value     = msgPartialValue
		constant  = msgPartialConst
		reserved  = msgPartialReserved
		component = msgPartialComponent
	)
	tests := []struct {
		page bool
		src  string
		msg  string
		at   string // the source text at the diagnostic's span
	}{
		{true, `const partial = "yes"`, value, `"yes"`},
		{true, `const partial = 1`, value, `1`},
		{true, `const partial = isPartial`, value, `isPartial`},
		{true, `const partial = !false`, value, `!false`},
		{true, `const partial = (true)`, value, `(true)`},
		{true, `const partial = iota`, value, `iota`},
		{true, "const (\n\tx = true\n\tpartial\n)", value, `partial`},
		{true, `const partial int = 1`, value, `int`},
		{true, `const partial string = "true"`, value, `string`},
		{true, `var partial = true`, constant, `partial`},
		{true, `partial := true`, constant, `partial`},
		{true, `x, partial := 1, true`, constant, `partial`},
		{true, `func partial() {}`, reserved, `partial`},
		{true, `type partial int`, reserved, `partial`},
		{false, `const partial = true`, component, `partial`},
		{false, `const partial = false`, component, `partial`},
	}
	for _, tt := range tests {
		src := "---\n" + tt.src + "\n---\n<p/>"
		f := Parse(src, Strict)
		info := FileInfo{Path: "src/components/X.lyr", Role: RoleComponent}
		if tt.page {
			info = FileInfo{Path: "src/pages/x.lyr", Role: RolePage, Route: "/x"}
		}
		a := Analyze(f, info)
		var got []Diagnostic
		for _, d := range a.Diagnostics {
			if strings.Contains(d.Message, "partial") {
				got = append(got, d)
			}
		}
		if len(got) != 1 || got[0].Message != tt.msg {
			t.Errorf("%q: %q, want [%q]", tt.src, anMessages(a), tt.msg)
			continue
		}
		if at := src[got[0].Span.Start:got[0].Span.End]; at != tt.at {
			t.Errorf("%q: at %q, want %q", tt.src, at, tt.at)
		}
		if a.Partial {
			t.Errorf("%q: Partial set", tt.src)
		}
	}
	// In a component, only the const looks like the flag.
	for _, src := range []string{
		"---\nvar partial = true\n---\n<p>{partial}</p>",
		"---\npartial := true\n---\n<p>{partial}</p>",
		"---\nfunc partial() bool { return true }\n---\n<p>{partial()}</p>",
		"---\ntype partial int\n---\n<p>{partial(1)}</p>",
	} {
		if a := anComp(t, src); len(a.Diagnostics) > 0 {
			t.Errorf("component %q: %q", src, anMessages(a))
		}
	}
}

// TestAnalyzePartialStyle: a partial page's processed <style> is dropped
// with a warning at the tag (PLAN.md §4k (Q4)); is:inline and non-partial
// pages get none.
func TestAnalyzePartialStyle(t *testing.T) {
	src := "---\nconst partial = true\n---\n<p/>\n<style>p{}</style>\n<style is:global>a{}</style>\n<style is:inline>b{}</style>"
	a := anPage(t, src)
	var at []string
	for _, d := range a.Diagnostics {
		if d.Severity != SeverityWarning || d.Message != MsgPartialStyle {
			t.Errorf("unexpected diagnostic %+v", d)
			continue
		}
		at = append(at, src[d.Span.Start:d.Span.End])
	}
	if want := []string{"<style>", "<style is:global>"}; !slices.Equal(at, want) {
		t.Errorf("warnings at %q, want %q", at, want)
	}
	if a.HasErrors() {
		t.Error("a warning only")
	}
	for _, src := range []string{
		"---\nconst partial = false\n---\n<style>p{}</style>",
		"---\n---\n<style>p{}</style>",
	} {
		if a := anPage(t, src); len(a.Diagnostics) > 0 {
			t.Errorf("%q: %q", src, anMessages(a))
		}
	}
}

// aliasResolver resolves "@/x" to "src/x".
type aliasResolver struct{}

func (aliasResolver) ResolveImport(spec string) (string, bool) {
	if rest, ok := strings.CutPrefix(spec, "@/"); ok {
		return "src/" + rest, true
	}
	return "", false
}

// TestAnalyzeBlankImports: _ "./X.lyr" (PLAN.md §4k) is accepted for .lyr
// and Markdown paths of every form, isn't unused, clashes with nothing and
// is placed nowhere; no alias stays an error.
func TestAnalyzeBlankImports(t *testing.T) {
	src := "---\nimport (\n\t_ \"./A.lyr\"\n\t_ \"@/components/B.lyr\"\n\t_ \"/src/components/C.lyr\"\n\t_ \"../content/d.md\"\n\t_ \"./E.lyr\"\n\tE \"./E.lyr\"\n\t_ \"./F.lyr\"\n\t_ \"./F.lyr\"\n)\n---\n<E />"
	for _, info := range []FileInfo{
		{Path: "src/components/X.lyr", Role: RoleComponent, Aliases: aliasResolver{}},
		{Path: "src/components/X.lyr", Role: RolePage, Route: "/x", Aliases: aliasResolver{}},
	} {
		f := Parse(src, Strict)
		a := Analyze(f, info)
		checkAnalysis(t, f, a)
		if len(a.Diagnostics) > 0 {
			t.Errorf("%v: %q", info.Role, anMessages(a))
		}
		var got []string
		for _, im := range a.LyrImports {
			if im.Blank() {
				if im.Place != 0 {
					t.Errorf("blank %s placed %v", im.Path, im.Place)
				}
				got = append(got, im.Resolved)
			}
		}
		want := []string{"src/components/A.lyr", "src/components/B.lyr", "src/components/C.lyr", "src/content/d.md",
			"src/components/E.lyr", "src/components/F.lyr", "src/components/F.lyr"}
		if !slices.Equal(got, want) {
			t.Errorf("blank imports %q, want %q", got, want)
		}
	}
	for src, want := range map[string]string{
		"---\nimport \"./A.lyr\"\n---\n":          `a component import needs an alias: A "./A.lyr"`,
		"---\nimport _ \"../../../A.lyr\"\n---\n": "component path ../../../A.lyr is outside src/",
		"---\nimport _ \"./A.tsx\"\n---\n":        "framework components aren't supported; use a .lyr component with a <script>",
		"---\nimport _ \"@/A.lyr\"\n---\n":        NoAliasError("@/A.lyr"),
	} {
		if got := anMessages(anComp(t, src)); !slices.Equal(got, []string{want}) {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}
