package compiler

import "testing"

func TestScriptsAndStyles(t *testing.T) {
	src := "---\n---\n<div>\n{if true}<script>let a: number = 1</script>{/if}\n<script is:inline>x()</script>\n<script src=\"./b.ts\"></script>\n<script type=\"module\">y()</script>\n<style>.a{color:red}</style>\n<style is:global>body{margin:0}</style>\n<style is:inline>p{}</style>\n</div>\n"
	f := Parse(src, Strict)
	if f.HasErrors() {
		t.Fatalf("parse: %v", f.Diagnostics)
	}
	sc := Scripts(f)
	if len(sc) != 2 || sc[0].Content.Text != "let a: number = 1" || sc[1].Src != "./b.ts" || sc[1].Index != 1 {
		t.Fatalf("Scripts = %+v", sc)
	}
	if got := src[sc[0].Content.Start:sc[0].Content.End]; got != sc[0].Content.Text {
		t.Errorf("script span %q", got)
	}
	st := Styles(f)
	if len(st) != 2 || st[0].Global || !st[1].Global || st[1].Content.Text != "body{margin:0}" {
		t.Fatalf("Styles = %+v", st)
	}
	if !HasScopedStyle(f) {
		t.Error("HasScopedStyle = false")
	}
	if ScopeAttr("src/components/Card.orbit") != "data-o-"+ScopeHash("src/components/Card.orbit") {
		t.Error("ScopeAttr")
	}
}
