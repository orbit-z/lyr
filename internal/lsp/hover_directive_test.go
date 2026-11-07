package lsp

import (
	"strings"
	"testing"
)

const hoverCard = `---
type Props struct{}
---
<p>card</p>
`

const hoverPage = `---
import Card "../components/Card.lyr"
html := lyr.HTML("<b>x</b>")
---
<Card client:visible />
<Card server:defer client:visible />
<div set:html={html} class="box"></div>
`

func TestDirectiveHover(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.lyr": hoverCard, "src/pages/index.lyr": hoverPage})
	const path = "src/pages/index.lyr"
	f := ws.byPath[path]
	lines := ws.lines[path]
	second := strings.Index(hoverPage, "<Card server")
	for _, tc := range []struct {
		name string
		off  int
		want string // substring of the hover; "" for no native hover
		kind regionKind
	}{
		{"client:visible", strings.Index(hoverPage, "client:visible") + 3, "scrolls into view", regionAttr},
		{"client:visible on server:defer", second + strings.Index(hoverPage[second:], "client:visible") + 3, "also delays the island fetch", regionAttr},
		{"server:defer", strings.Index(hoverPage, "server:defer") + 1, "server island", regionAttr},
		{"set:html", strings.Index(hoverPage, "set:html") + 2, "raw HTML", regionAttr},
		{"plain attribute", strings.Index(hoverPage, "class=") + 2, "", regionAttr},
		{"directive value", strings.Index(hoverPage, "{html}") + 2, "", regionGo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := classify(f, tc.off)
			if reg.kind != tc.kind {
				t.Fatalf("region %d, want %d", reg.kind, tc.kind)
			}
			if reg.kind != regionAttr {
				return // a Go region goes to gopls, as before
			}
			res := ws.nativeHover(f, reg, lines)
			if tc.want == "" {
				if res != nil {
					t.Fatalf("hover = %v, want none", res)
				}
				return
			}
			h, ok := res.(map[string]any)
			if !ok {
				t.Fatalf("hover = %v", res)
			}
			value := h["contents"].(map[string]any)["value"].(string)
			if !strings.Contains(value, tc.want) || !strings.Contains(value, reg.attr.Name) {
				t.Errorf("hover %q, want %q", value, tc.want)
			}
			if got := rangeText(hoverPage, h["range"].(Range)); got != reg.attr.Name {
				t.Errorf("range covers %q, want %q", got, reg.attr.Name)
			}
		})
	}
}
