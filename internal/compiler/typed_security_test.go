package compiler

import (
	"strings"
	"testing"
)

// The style and on* security warnings of the typed pass (PLAN.md §2.3,
// §2.7): none for an orbit.CSS style value or a constant, the old warning
// for everything else, every candidate of a unit that isn't type-checked,
// and pass-through attributes warned at the call site.

const secWarn = `---
import "strconv"
import Button "./Button.orbit"
import Styled "./Styled.orbit"

type S orbit.CSS
type A = orbit.CSS

const hidden = "display: none"

func width(n int) orbit.CSS { return orbit.CSS("width:" + strconv.Itoa(n) + "%") }

s := "color: red"
css := orbit.CSS("color: blue")
var alias A = "color: green"
handler := "go()"
style := s
---
<div style={s}>string</div>
<div style={css}>css</div>
<div style={width(3)}>func</div>
<div style={S(s)}>named</div>
<div style={alias}>alias</div>
<div style={"display: none"}>literal</div>
<div style={hidden}>const</div>
<div style={"a" + "b"}>concat</div>
<div style="color: red">static</div>
<div {style}>shorthand</div>
<button onclick={"go()"}>on const</button>
<button onclick={handler} onMouseOver={handler}>on var</button>
<button onclick={css}>on css</button>
<div on={s}>not an event</div>
<Button style={s} onclick={handler}>rest</Button>
<Button style={css} onclick={"go()"}>rest trusted</Button>
<Styled style={css} />
`

const secButton = `---
type Props struct {
	Attrs orbit.RestAttrs
}
---
<button {...props.Attrs}><slot /></button>
`

const secStyled = `---
type Props struct {
	Style orbit.CSS
}
---
<p style={props.Style}>styled</p>
`

// secUnchecked has a type error, so it keeps every fallback and warns for
// every candidate, constants included.
const secUnchecked = `---
var n int = "x"
---
<div style={"display: none"} onclick={"go()"}>{n}</div>
`

func TestSecurityWarnings(t *testing.T) {
	m := newGenModule(t)
	p := plPipeline(t, m, map[string]string{
		"src/components/Warn.orbit":      secWarn,
		"src/components/Button.orbit":    secButton,
		"src/components/Styled.orbit":    secStyled,
		"src/components/Unchecked.orbit": secUnchecked,
	}, Options{Module: testModule, Mode: Strict})
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("front diagnostics (the parser no longer warns):\n%s", strings.Join(d, "\n"))
	}
	var got []string
	for _, pf := range p.files {
		want := pf.path != "src/components/Unchecked.orbit"
		if pf.typed.Checked != want {
			t.Errorf("%s: checked %v, want %v", pf.path, pf.typed.Checked, want)
		}
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
	}
	golden(t, "testdata/typed/warnings.golden", strings.Join(got, "\n")+"\n")
}
