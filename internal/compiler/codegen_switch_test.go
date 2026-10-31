package compiler

import (
	"strings"
	"testing"
)

// TestSwitchTypedWrites: in a type switch, the case variable has the
// case's type (go/types records each clause's implicit object), so the
// typed pass picks a typed write per case; a case listing several types,
// and default, keep the interface type and the fallback.
func TestSwitchTypedWrites(t *testing.T) {
	src := "---\ntype Props struct{ Item any }\n---\n" +
		"{switch v := props.Item.(type)}\n" +
		"\t{case int}<i>{v}</i>\n" +
		"\t{case string}<s>{v}</s>\n" +
		"\t{case bool, float64}<b>{v}</b>\n" +
		"\t{default}<u>{v}</u>\n" +
		"{/switch}\n"
	m := newGenModule(t)
	p := plPipeline(t, m, map[string]string{"src/components/X.lyr": src}, Options{Module: testModule})
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	pf := p.files[0]
	if !pf.typed.Checked {
		t.Fatal("not type-checked")
	}
	out := pf.typed.Output
	var got []string
	for _, cs := range out.CallSites {
		got = append(got, string(out.Impl.Content[cs.Call.Start:cs.Call.End]))
	}
	want := []string{"Int", "String", "Expr", "Expr"}
	if len(got) != len(want) {
		t.Fatalf("call sites %q, want %d", got, len(want))
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], NameW+"."+w+"(") {
			t.Errorf("site %d: %q, want %s", i, got[i], w)
		}
	}
}
