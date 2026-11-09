package lyr

import (
	"bytes"
	"encoding/json"

	"github.com/orbit-z/lyr/internal/loader"
)

// Injection of the client loader (PLAN.md §5 "The loader", §5f), on pages
// that need it: those whose static graph has a server:defer call site
// (Route.Islands) or "lazy" assets. The loader is one classic script,
// loader.LoaderURL, built from loader/*.ts into internal/loader, which
// lyr build and lyr dev write under /_lyr/loader/. Lyr writes no inline
// executable script and no inline style, so a site runs under
// script-src 'self'; style-src 'self'. A page's head gets:
//
//	<script type="application/json" id="lyr-loader">{"dev":false,"lazy":{…}}</script>
//	<script defer src="/_lyr/loader/loader-HASH.js"></script>
//
// The config is a data block, which a Content Security Policy doesn't
// block. The display:contents rule for <lyr-island> and <lyr-lazy> is the
// first rule of the page's stylesheet (internal/assets). A page partial
// gets none of it in a head: what it needs comes at the end of its output
// instead (partialTail, PLAN.md §4k).

//go:generate go run ./loader/internal/gen

// loaderConfig is the JSON config the loader reads from
// <script id="lyr-loader"> (and, in a page partial's tail, from the
// data-lyr-loader attribute of its loader script).
type loaderConfig struct {
	Dev  bool    `json:"dev"`
	Lazy lazyMap `json:"lazy,omitempty"`
	// IDs maps each lazy URL of a partial to its component's ID, so the
	// lazy loader skips a component the document already ran (PLAN.md
	// §4k). Never set in a page's head.
	IDs map[string]string `json:"ids,omitempty"`
	// Run lists a partial's component entries, [ID, URL], that its loader
	// script imports unless the document ran the ID. Never set in a
	// page's head.
	Run [][2]string `json:"run,omitempty"`
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
// flag and assets, or "" if the page needs no loader.
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
	var b bytes.Buffer
	b.WriteString(`<script type="application/json" id="lyr-loader">`)
	// Escaped for a script context, like set:json: no </script> or <!--.
	json.HTMLEscape(&b, marshalLoader(loaderConfig{Dev: dev, Lazy: lazy}))
	b.WriteString(`</script><script defer src="` + loader.LoaderURL + `"></script>`)
	return b.String()
}

// marshalLoader encodes c.
func marshalLoader(c loaderConfig) []byte {
	data, err := json.Marshal(c)
	if err != nil {
		panic("lyr: loader config: " + err.Error()) // maps and pairs of strings always encode
	}
	return data
}

// partialTail returns what a page partial's output ends with (PLAN.md
// §4k), or "" when it needs nothing. When its subtree has server:defer or
// lazy client:* call sites (a "css" asset, the wrappers stylesheet, which
// is a partial's only CSS), a link to it; then, when it has islands, lazy
// entries or scripts, the loader script with the partial's config in its
// data-lyr-loader attribute:
//
//	<link rel="stylesheet" href="/_lyr/loader/wrappers-HASH.css">
//	<script src="/_lyr/loader/loader-HASH.js" data-lyr-loader='{"dev":false,"lazy":{…},"ids":{…},"run":[["ID","URL"],…]}'></script>
//
// The script is classic, so the browser runs it on every insertion. It
// imports each "run" entry whose ID the document hasn't run yet, and
// installs the islands and lazy loaders unless the host has them (then it
// merges its config into the host's). The "component" assets whose URL is
// also a "lazy" asset's are loaded by the lazy loader, not "run"; "ids"
// gives it each lazy URL's component ID. In production it runs once per
// route, in NewSite.
func partialTail(dev, islands bool, assets []Asset) string {
	c := loaderConfig{Dev: dev, Lazy: lazyMap{}}
	for _, a := range assets {
		if a.Kind == "lazy" {
			c.Lazy.addLazy(a)
		}
	}
	for _, a := range assets {
		if a.Kind != "component" {
			continue
		}
		if lazyURL(c.Lazy, a.URL) {
			if c.IDs == nil {
				c.IDs = map[string]string{}
			}
			c.IDs[a.URL] = a.ID
		} else {
			c.Run = append(c.Run, [2]string{a.ID, a.URL})
		}
	}
	var b bytes.Buffer
	for _, a := range assets {
		if a.Kind == "css" {
			b.WriteString(`<link rel="stylesheet" href="` + attrEscape(a.URL) + `">`)
		}
	}
	if !islands && len(c.Lazy) == 0 && len(c.Run) == 0 {
		return b.String()
	}
	b.WriteString(`<script src="` + loader.LoaderURL + `" data-lyr-loader='`)
	escapeJSONAttr(&b, marshalLoader(c))
	b.WriteString(`'></script>`)
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
