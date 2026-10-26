# bench/go: Orbit vs. templ vs. html/template

Go benchmarks that render the same page with three engines (PLAN.md §4b,
milestone 9). This is its own module, so templ is a dependency here and
never of Orbit itself.

## The page

A layout (doctype, `<head>` with a title, a footer), a small header with a
nav loop, and a list of **500 cards** from `data.Items`. Each card has an int
attribute (`data-id`), a bool attribute (`data-featured`), a URL attribute
(`href`), a title, escaped text containing `<`, `&` and `>`, an int, and a
default slot whose content uses the loop variable (`<em>#{it.ID}</em>`).

- **Orbit**: `src/` (`layouts/Layout.orbit`, `components/{Header,Card}.orbit`,
  `pages/cards.orbit`), compiled by the real pipeline into `orbit_gen/`
  (gitignored, like in any Orbit project). `pages/post/[slug].orbit` is a
  small page with a route param, used by the serve benchmark only.
- **templ**: `templpage/page.templ`, with the generated `page_templ.go`
  committed. templ is pinned to v0.3.1070.
- **html/template**: `htmltpl/htmltpl.go`. It has no slots, so the card's
  slot content is written inline in the card template.

`TestEquivalent` checks that the three outputs are identical once
whitespace is normalized (and templ's `<!doctype html>` uppercased), so the
benchmarks compare the same work.

## Running

```sh
cd bench/go
go generate ./...        # orbit generate, then templ generate (needs network once)
go test ./...            # the equivalence test
go tool orbit build      # the production flavor in .orbit/build/ (PLAN.md §3.5)
go test -overlay=.orbit/build/overlay.json ./...
go test -overlay=.orbit/build/overlay.json -run '^$' -bench . -benchmem -count 8 > new.txt
```

The published numbers (`docs/performance.md`) are the last command: the
code `orbit build` compiles, where components are called directly, so the
slot closures stay on the stack. Without `-overlay`, `go test` measures
`orbit_gen/` as `orbit dev` runs it, through the components' hooks (one
allocation per capturing slot); that's the "in dev" row.

Compare runs with benchstat, without adding it to a go.mod:

```sh
go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e old.txt new.txt
```

Rerun `go generate` after you change `src/`, `templpage/page.templ` or
Orbit's compiler. If you only changed `src/`, `go tool orbit generate` is
enough. Rerun `go tool orbit build` too before measuring with `-overlay`.

## What's measured

- `BenchmarkRender/engine=…`: each engine renders the page into a reused
  `bytes.Buffer`. Orbit renders through its generated page function with a
  reused `*orbit.Writer`, templ with `Component.Render`, html/template with
  `ExecuteTemplate`.
- `BenchmarkServe/path=…`: Orbit only, through `Site.Middleware` with a
  reusable fake `ResponseWriter`: routing, the pooled context, writer and
  buffer, rendering, headers and the body write, without `net/http`'s own
  per-request cost. `/cards` is the 500-card page, `/post/hello-world` a
  small page with a route param.

Each reports ns/op, B/op and allocs/op. With `-overlay`, Orbit renders the
cards page with no allocations, and the serve path adds only the
`Content-Length` value. Without it (the hook flavor), each slot closure that
captures the loop variable costs one allocation (one per card, plus the
layout's slot).

## Caveats

These are single-machine micro-benchmarks: one process, warm caches, no
network, no real `net/http` server. Absolute numbers depend on the CPU and
on whatever else runs on the machine; compare A/B runs made back to back on
the same machine, with `-count` of 6 or more. The page is synthetic, so it
says how fast the template engines are, not how fast a site is: real pages
spend most of their time in data fetching. For whole-server numbers see the
Astro head-to-head (`bench/orbit-site`, `bench/astro-site`).
