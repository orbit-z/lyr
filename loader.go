package orbit

import (
	"bytes"
	"encoding/json"
)

// Head injection of the client loader (PLAN.md §5 "The loader"), on pages
// that need it: the islands part when Route.Islands is set, the lazy part
// when the page has "lazy" assets. The scripts (loaderIslandsJS,
// loaderLazyJS) are built from loader/*.ts into loader_gen.go. The contract
// with the loader:
//
//	<style>orbit-island,orbit-lazy{display:contents}</style>
//	<script type="application/json" id="orbit-loader">{"dev":false,"lazy":{…}}</script>
//	<script type="module">…loaderIslandsJS…</script>   (when Route.Islands)
//	<script type="module">…loaderLazyJS…</script>      (when the page has lazy assets)

//go:generate go run ./loader/internal/gen

const loaderStyle = `<style>orbit-island,orbit-lazy{display:contents}</style>`

// loaderConfig is the JSON config the loader reads from
// <script id="orbit-loader">.
type loaderConfig struct {
	Dev  bool    `json:"dev"`
	Lazy lazyMap `json:"lazy,omitempty"`
}

// lazyMap maps a component ID to the entry URLs its <orbit-lazy> markers
// import, in module-evaluation order: the tagged component's entry plus
// every lazy-on-this-page component in its subtree (one "lazy" asset each).
type lazyMap map[string][]string

// addLazy records one "lazy" asset in m, keeping order and skipping a URL
// already listed for that ID.
func (m lazyMap) addLazy(a Asset) {
	for _, u := range m[a.ID] {
		if u == a.URL {
			return
		}
	}
	m[a.ID] = append(m[a.ID], a.URL)
}

// loaderHead returns the loader tags for a page with the given islands flag
// and assets, or "" if the page needs no loader.
func loaderHead(dev, islands bool, assets []Asset) string {
	lazy := lazyMap{}
	for _, a := range assets {
		if a.Kind == "lazy" {
			lazy.addLazy(a)
		}
	}
	if !islands && len(lazy) == 0 {
		return ""
	}
	data, err := json.Marshal(loaderConfig{Dev: dev, Lazy: lazy})
	if err != nil {
		panic("orbit: loader config: " + err.Error()) // a map of string lists always encodes
	}
	var b bytes.Buffer
	b.WriteString(loaderStyle)
	b.WriteString(`<script type="application/json" id="orbit-loader">`)
	// Escaped for a script context, like set:json: no </script> or <!--.
	json.HTMLEscape(&b, data)
	b.WriteString(`</script>`)
	if islands {
		b.WriteString(`<script type="module">`)
		b.WriteString(loaderIslandsJS)
		b.WriteString(`</script>`)
	}
	if len(lazy) > 0 {
		b.WriteString(`<script type="module">`)
		b.WriteString(loaderLazyJS)
		b.WriteString(`</script>`)
	}
	return b.String()
}
