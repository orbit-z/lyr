package compiler

import (
	"regexp"
	"strings"
	"testing"
)

// lineDirective matches the //line and /*line*/ directives, whose positions
// differ between two sources of different lengths.
var lineDirective = regexp.MustCompile(`//line [^\n]*|/\*line [^*]*\*/`)

// TestFragmentShorthandCodegen: <>…</> generates the same code as
// <Fragment>…</Fragment>, at the top level, in a loop, in a component's
// default slot and inside a named-slot <Fragment>.
func TestFragmentShorthandCodegen(t *testing.T) {
	const card = "---\ntype Props struct{}\n---\n<div><slot /><slot name=\"footer\" /></div>\n"
	tmpl := "---\nimport Card \"./Card.lyr\"\ntype Props struct{ Items []string }\n---\n" +
		"<F>\n  <h1>Title</h1>\n  {props.Items[0]}\n</F>\n" +
		"<ul>{for _, x := range props.Items}<F><li>{x}</li><li>sep</li></F>{/for}</ul>\n" +
		"<Card>\n  <Fragment slot=\"footer\"><F>a</F></Fragment>\n  <F><b>default</b></F>\n</Card>\n" +
		"<F></F>\n"
	gen := func(open, close string) (string, string) {
		src := strings.NewReplacer("<F>", open, "</F>", close).Replace(tmpl)
		p := plFront(map[string]string{
			"src/components/Card.lyr": card,
			"src/components/X.lyr":    src,
		}, Options{Module: testModule})
		if d := p.diagnostics(); len(d) > 0 {
			t.Fatalf("diagnostics for %s:\n%s", open, strings.Join(d, "\n"))
		}
		out := p.files[1].pass1
		strip := func(b []byte) string { return lineDirective.ReplaceAllString(string(b), "") }
		return strip(out.Decl.Content), strip(out.Impl.Content)
	}
	shortDecl, shortImpl := gen("<>", "</>")
	longDecl, longImpl := gen("<Fragment>", "</Fragment>")
	if shortDecl != longDecl {
		t.Errorf("decl differs:\n<>:\n%s\n<Fragment>:\n%s", shortDecl, longDecl)
	}
	if shortImpl != longImpl {
		t.Errorf("impl differs:\n<>:\n%s\n<Fragment>:\n%s", shortImpl, longImpl)
	}
	if !strings.Contains(shortImpl, "<li>") || !strings.Contains(shortImpl, "sep") {
		t.Errorf("impl is missing the fragment content:\n%s", shortImpl)
	}
}
