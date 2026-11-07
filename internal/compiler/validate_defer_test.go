package compiler

import (
	"strings"
	"testing"
)

// TestDelayedIslandDirectives: server:defer with client:* (a delayed
// island, PLAN.md §3.4 "Delayed island fetch"): every client:* rule still
// applies, server:concurrent is still an error, and client:visible needs a
// fallback element to observe when it's certain there is none.
func TestDelayedIslandDirectives(t *testing.T) {
	const noElement = `client:visible on a server:defer component needs a fallback element to observe, e.g. <div slot="fallback"></div>`
	for _, tc := range []struct {
		src, want string // want: "" for no diagnostic, else "line:col: message"
	}{
		{`<C server:defer client:load />`, ""},
		{`<C server:defer client:idle />`, ""},
		{`<C server:defer client:media="(min-width: 900px)" />`, ""},
		{`<C client:idle server:defer />`, ""},
		{`<C server:defer client:visible><p slot="fallback">Loading…</p></C>`, ""},
		{`<C server:defer client:visible="200px"><div slot="fallback"></div></C>`, ""}, // an empty element
		{`<C server:defer client:visible><Spinner slot="fallback" /></C>`, ""},         // a component: not checked
		{`<C server:defer client:visible><slot name="f" slot="fallback" /></C>`, ""},
		{`<C server:defer client:visible><Fragment slot="fallback">{if ok}<p>x</p>{/if}</Fragment></C>`, ""},
		{`<C server:defer client:visible><Fragment slot="fallback">{x}</Fragment></C>`, ""},
		{`<C server:defer client:visible><Fragment slot="fallback">a<b>b</b></Fragment></C>`, ""},
		{`<C server:defer client:visible><Fragment slot="fallback" set:html={h} /></C>`, ""},
		{`<C server:defer client:visible><Fragment slot="fallback"><Fragment><i></i></Fragment></Fragment></C>`, ""},
		{`<C server:defer client:visible />`, "1:17: " + noElement},
		{`<C server:defer client:visible="200px"></C>`, "1:17: " + noElement},
		{`<C client:visible server:defer />`, "1:4: " + noElement},
		{`<C server:defer client:visible><Fragment slot="fallback">Loading…</Fragment></C>`, "1:17: " + noElement},
		{`<C server:defer client:visible><Fragment slot="fallback"> <!-- c --> {/* c */} </Fragment></C>`, "1:17: " + noElement},
		{`<C server:defer client:visible><Fragment slot="fallback" set:text={s} /></C>`, "1:17: " + noElement},
		{`<Self server:defer client:visible="10px" />`, "1:20: " + noElement},
		{`<C server:defer client:media />`, `1:17: client:media needs a media query, e.g. client:media="(max-width: 600px)"`},
		{`<C server:defer client:visible={m}><i slot="fallback"></i></C>`, "1:17: client:visible takes a static string value, not an expression"},
		{`<C server:defer client:idle client:visible />`, "1:29: only one client:* directive is allowed per component"},
		{`<C server:defer client:only />`, "1:17: client:only is for framework components, which Lyr doesn't support; use client:load, client:idle, client:visible or client:media"},
		{`<C server:defer client:idle server:concurrent />`, "1:4: server:defer and server:concurrent can't be combined; a deferred component already renders in its own request"},
		{`<C server:defer client:idle><p>x</p></C>`, `1:29: only slot="fallback" content is allowed inside a server:defer component`},
	} {
		f := Parse(tc.src, Strict)
		got := strings.TrimSuffix(dumpDiags(f, "x"), "\n")
		want := ""
		if tc.want != "" {
			want = "x:" + tc.want
		}
		if got != want {
			t.Errorf("%s:\n got %q\nwant %q", tc.src, got, want)
		}
	}
}
