package lyr

import (
	"bytes"
	"encoding/json"
)

// Head injection of the client loader (PLAN.md §5 "The loader"), on pages
// that need it: the islands part when Route.Islands is set, the lazy part
// when the page has "lazy" assets. The scripts (loaderIslandsJS, or
// loaderIslandsWhenJS when Route.IslandsWhen is set, and loaderLazyJS) are
// built from loader/*.ts into loader_gen.go. The contract with the loader:
//
//	<style>lyr-island,lyr-lazy{display:contents}</style>
//	<script type="application/json" id="lyr-loader">{"dev":false,"lazy":{…}}</script>
//	<script type="module">…loaderIslandsJS…</script>   (when Route.Islands)
//	<script type="module">…loaderIslandsWhenJS…</script> (instead, when Route.IslandsWhen)
//	<script type="module">…loaderLazyJS…</script>      (when the page has lazy assets)
//
// A page partial gets none of it in a head: what it needs comes at the end
// of its output instead (partialTail, PLAN.md §4k).

//go:generate go run ./loader/internal/gen

const loaderStyle = `<style>lyr-island,lyr-lazy{display:contents}</style>`

// loaderConfig is the JSON config the loader reads from
// <script id="lyr-loader"> (and, in a page partial's tail, from
// <script data-lyr-loader>).
type loaderConfig struct {
	Dev  bool    `json:"dev"`
	Lazy lazyMap `json:"lazy,omitempty"`
	// IDs maps each lazy URL of a partial to its component's ID, so the
	// lazy loader skips a component the document already ran (PLAN.md
	// §4k). Never set in a page's head.
	IDs map[string]string `json:"ids,omitempty"`
}

// lazyMap maps a component ID to the entry URLs its <lyr-lazy> markers
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

// loaderHead returns the loader tags for a page with the given islands
// flags and assets, or "" if the page needs no loader. when picks the
// islands loader that reads delayed islands' strategies (loaderIslandsWhenJS).
func loaderHead(dev, islands, when bool, assets []Asset) string {
	lazy := lazyMap{}
	for _, a := range assets {
		if a.Kind == "lazy" {
			lazy.addLazy(a)
		}
	}
	if !islands && len(lazy) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString(loaderStyle)
	b.WriteString(`<script type="application/json" id="lyr-loader">`)
	writeLoader(&b, dev, islands, when, lazy, nil)
	return b.String()
}

// writeLoader writes the loader config, from its opening tag's end, and
// the loader scripts: the islands script (the strategy-reading variant
// with when) if islands, the lazy script if lazy has entries.
func writeLoader(b *bytes.Buffer, dev, islands, when bool, lazy lazyMap, ids map[string]string) {
	data, err := json.Marshal(loaderConfig{Dev: dev, Lazy: lazy, IDs: ids})
	if err != nil {
		panic("lyr: loader config: " + err.Error()) // maps of strings always encode
	}
	// Escaped for a script context, like set:json: no </script> or <!--.
	json.HTMLEscape(b, data)
	b.WriteString(`</script>`)
	switch {
	case islands && when:
		b.WriteString(`<script type="module">`)
		b.WriteString(loaderIslandsWhenJS)
		b.WriteString(`</script>`)
	case islands:
		b.WriteString(`<script type="module">`)
		b.WriteString(loaderIslandsJS)
		b.WriteString(`</script>`)
	}
	if len(lazy) > 0 {
		b.WriteString(`<script type="module">`)
		b.WriteString(loaderLazyJS)
		b.WriteString(`</script>`)
	}
}

// partialTail returns what a page partial's output ends with (PLAN.md
// §4k), or "" when it needs nothing: when its subtree has server:defer or
// client:* call sites, the loader pieces it needs, as in a page's head but
// with a data-lyr-loader config (the loaders are idempotent, so a host
// that has them keeps its own); then, when its subtree has scripts, the
// runner, which imports each component entry whose ID the document hasn't
// run yet:
//
//	<script type="module">let s=globalThis.lyr__ran??=new Set;for(let[i,u]of[["ID","URL"],…])s.has(i)||(s.add(i),import(u))</script>
//
// The "component" assets whose URL is also a "lazy" asset's are loaded by
// the lazy loader, not the runner; they give it each lazy URL's component
// ID. In production it runs once per route, in NewSite.
func partialTail(dev, islands, when bool, assets []Asset) string {
	lazy := lazyMap{}
	for _, a := range assets {
		if a.Kind == "lazy" {
			lazy.addLazy(a)
		}
	}
	var ids map[string]string
	var run [][2]string
	for _, a := range assets {
		if a.Kind != "component" {
			continue
		}
		if lazyURL(lazy, a.URL) {
			if ids == nil {
				ids = map[string]string{}
			}
			ids[a.URL] = a.ID
		} else {
			run = append(run, [2]string{a.ID, a.URL})
		}
	}
	var b bytes.Buffer
	if islands || len(lazy) > 0 {
		b.WriteString(loaderStyle)
		b.WriteString(`<script type="application/json" data-lyr-loader>`)
		writeLoader(&b, dev, islands, when, lazy, ids)
	}
	if len(run) > 0 {
		data, err := json.Marshal(run)
		if err != nil {
			panic("lyr: partial runner: " + err.Error()) // string pairs always encode
		}
		b.WriteString(`<script type="module">let s=globalThis.lyr__ran??=new Set;for(let[i,u]of`)
		json.HTMLEscape(&b, data)
		b.WriteString(`)s.has(i)||(s.add(i),import(u))</script>`)
	}
	return b.String()
}

// lazyURL reports whether url is one of m's entries.
func lazyURL(m lazyMap, url string) bool {
	for _, us := range m {
		for _, u := range us {
			if u == url {
				return true
			}
		}
	}
	return false
}
