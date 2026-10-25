package orbit

// Milestone 8 contract stubs (PLAN.md §3.4, §4, §5). The runtime agent
// replaces this file with the real implementation; the compiler agent
// generates calls against these signatures.

import "strings"

// NewIsland is one entry of the island table that the generated
// orbit_gen/app passes to NewSite: a component used with server:defer
// somewhere in the project. id is its component ID (compiler.ComponentID),
// file its .orbit path.
func NewIsland[P any](id, file string, render func(*Ctx, *Writer, P, Slots) error) Island {
	return Island{}
}

// Defer is generated at each server:defer call site: it encodes props as
// JSON, seals them with the site's island key and writes the placeholder
//
//	<orbit-island src="/_orbit/island/<buildID>/<id>" props="<token>">fallback</orbit-island>
//
// fallback may be nil.
//
// Stub: writes the placeholder with build ID 0000000000000000 and the
// token "stub", without encoding props.
func Defer(c *Ctx, w *Writer, id string, props any, fallback Renderer) error {
	w.Raw(`<orbit-island src="/_orbit/island/0000000000000000/`)
	w.Raw(strings.ReplaceAll(id, `"`, ""))
	w.Raw(`" props="stub">`)
	if fallback != nil {
		if err := fallback(w); err != nil {
			return err
		}
	}
	w.Raw("</orbit-island>")
	return w.Err()
}
