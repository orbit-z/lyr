package orbit

// Milestone 8 contract stub for the client loader (PLAN.md §5 "The
// loader"). The loader agent replaces this file with a go:generate'd
// loader_gen.go holding the minified builds of loader/ (TypeScript); the
// runtime agent injects them.
//
// Injection contract (head injection, only on pages that need it):
//
//	<style>orbit-island,orbit-lazy{display:contents}</style>
//	<script type="application/json" id="orbit-loader">{"dev":false,"lazy":{"Counter-1c2d3e4f":"/_orbit/Counter-HASH.js"}}</script>
//	<script type="module">…loaderIslandsJS…</script>   (when Route.Islands)
//	<script type="module">…loaderLazyJS…</script>      (when the page has lazy assets)
//
// The JSON config is always written when either script is: "dev" is true
// under ORBIT_DEV=1 (console errors and the POST-fallback warning are
// dev-only); "lazy" maps component IDs to entry URLs (from the page's
// Kind "lazy" assets) and is omitted or empty for islands-only pages. The
// JSON is escaped for a script context like set:json.

// loaderIslandsJS fetches every <orbit-island> and swaps in its fragment.
const loaderIslandsJS = "/* orbit islands loader (stub) */"

// loaderLazyJS observes <orbit-lazy> markers and imports their entries.
const loaderLazyJS = "/* orbit lazy loader (stub) */"
