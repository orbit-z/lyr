# Performance

Orbit's server rendering is designed to do as little work per request as possible; this page explains that design and how Orbit is benchmarked against Astro.

Orbit has no target number. Instead, it measures and publishes: see [Results](#results).

## Why Orbit's SSR is fast

Your `.orbit` files compile to plain Go functions, and the Go compiler optimizes them like any other Go code. On top of that, Orbit's code generator and runtime avoid the usual per-request costs of a template engine.

### Static HTML is precomputed

Most of a page is static markup. At compile time, Orbit merges every run of adjacent static text, static attributes and scope attributes into **one string constant**, with static attribute values already escaped. Rendering that part of the page is a single copy into the response buffer.

For example, in

```
<article class="card">
  <a href={url}>{props.Post.Title}</a>
```

everything except `url` and `props.Post.Title` is written by a couple of constant copies.

### Expressions are written by type, without reflection

When Orbit compiles a template, it type-checks each `{…}` expression with `go/types` and emits a write for its exact type: a `string` goes through an escaping string write, an `int` is formatted with `strconv` straight into the buffer, an `orbit.HTML` value is copied raw, and so on. Attribute values are handled the same way: `data-id={n}` is written as the static ` data-id="` (merged into the surrounding constant), the number, and the closing quote. A URL attribute (`href={url}`) is written the same way, with the value going through a write that also does the `javascript:` check. There's no conversion to `any`, no runtime type switch and no reflection. This happens in `orbit dev` too, so dev renders the same way as production.

Only values whose static type is an interface (such as `any` or `fmt.Stringer`), and a few unusual types, go through the generic path. That path is still a type switch over the common types; reflection is only its last resort (`fmt.Sprint`). Both paths produce identical bytes. The full table is in the [Runtime API](reference/runtime-api.md#typed-writes).

### Escaping allocates nothing when there's nothing to escape

The escaper first scans a string for characters that need escaping. Most strings have none, and are copied into the buffer as they are, with no intermediate string and no allocation.

### One pooled buffer per request, no streaming

Every response renders into a `bytes.Buffer` taken from a `sync.Pool`, through a writer that writes straight into it, with no `bufio` layer in between. A page's `<link>` and `<script>` tags are built once per route at startup and written when the render reaches `</head>`, so the output is never scanned or moved. When the page is done, Orbit sends it in one write with a `Content-Length`. SSR responses are always buffered; Orbit never streams.

Buffering also means a middleware that wraps the `http.ResponseWriter` sees the complete page, with its assets already inserted, so it can rewrite the status, headers or body. See [Middleware and locals](middleware.md).

The request context (`*orbit.Ctx`) is pooled along with its writer and buffer, so don't keep a `Ctx` after the request ends.

### Components are cheap to call

- A component call is a plain Go function call. In a binary from `orbit build`, the caller calls the component's code directly; in `orbit dev`, `go test` and a plain `go build`, the call goes through one indirect call in the component's generated package (see [Direct calls in production builds](#direct-calls-in-production-builds)).
- **Slots are a small struct**, not a map: a default slot plus a short list of named ones. A call with no children allocates nothing, and neither does slot content that uses no variables from the parent. Slot content that does (`<Card>{p.Title}</Card>` in a loop) allocates nothing either in an `orbit build` binary: with the direct call, the Go compiler sees that the component doesn't keep the slot, so the closure stays on the stack. It allocates one small closure per slot only in `orbit dev`, `go test` and plain `go build` builds, and in production when it's passed into mutually recursive components or kept by your own Go code.
- **Pass-through attributes** (an `orbit.RestAttrs` field, [Components](components.md#passing-html-attributes-through)) are escaped and joined into strings at build time, and dynamic values keep their type, so a call site that passes only static attributes allocates nothing, and one with expression values allocates nothing in an `orbit build` binary. In `orbit dev`, `go test` and plain `go build`, a call site with expression values allocates once (the list of its dynamic attributes, about 80 bytes each), for the same reason slot closures do. Forwarding them to an inner component (`<Input {...props.Attrs} />`) is free; merging a forwarded `RestAttrs` with other attributes on the same tag allocates once.
- The recursion depth guard, which turns runaway recursion into an error instead of a crash, costs one integer increment per component render. It's always on.

### Routing and locals

- **Routes** are matched by walking a tree of path segments built at startup. No regular expressions.
- **Route params allocate nothing**: the router stores matched values in the pooled request context instead of a map, and a page's typed `params` is a plain struct filled from them. (A `map[string]string` cost 2 allocations, 336 bytes, per match.) Only a value with percent-escapes (`caf%C3%A9`) allocates its decoded string.
- **Typed locals** are allocated lazily, the first time a page or component uses them, and once per request at most. Orbit only fetches `locals` in a page or component whose code mentions it, so a page whose components never use `locals` costs nothing for them, even in a project that declares a `Locals` type.

### Allocations per request

For a typical page, rendering allocates nothing in Orbit's code: routing, the request context, the buffer, escaping, typed writes and head injection are all allocation-free. Serving the page adds exactly one allocation (48 bytes): the `Content-Length` header value. It can't be reused, because `net/http` may read the headers after the handler returns. Orbit's tests check both numbers, so a change that adds an allocation fails `go test`.

A "typical page" here means:
- its expressions are plain types, so they get typed writes
- it's built with `orbit build` (in `orbit dev` and `go test`, slot content that uses parent variables allocates, see below)
- it doesn't use locals or `server:defer`

What still allocates:
- **Go's `net/http` server itself**: about 14 allocations (1.4 KB) per request even for an empty handler, and about 19 (2.2 KB) once it writes a 1 KB page (measured on Go 1.27.3). Orbit uses the standard library server, so this floor is the same as for any Go web app.
- **Locals**, when a page or one of its components uses them: one allocation for the struct (made by Orbit on first use, or by `EnsureLocals` in your middleware), plus the request copy that `EnsureLocals` makes.
- **Slot content that uses parent variables**, in dev, `go test` and plain `go build` builds: one small closure per slot. Likewise, a component call site passing [pass-through attributes](components.md#passing-html-attributes-through) with expression values: one allocation per call site. In an `orbit build` binary only when the content is passed into components that call each other in a cycle (`A` renders `B`, which renders `A`; `<Self>` is fine), or when your Go code keeps a slot's renderer beyond the call.
- **Values without a typed write** (the `Expr` fallback) and your own `String()` or `Error()` methods.
- **Route params with percent-escapes**, and **`server:defer` props** (encoded and encrypted per instance).
- **`server:concurrent` components**: one small allocation each (the goroutine's arguments; see [below](#slow-components-in-the-same-response-serverconcurrent)).
- **`orbit.JSON` and `set:json` values**: `encoding/json`'s own allocations (below). Orbit's code around them adds none.
- **Your frontmatter code**: database queries and the like. This is usually the biggest part.

### Encoding JSON for scripts

[`orbit.JSON`](components.md#typed-data-with-orbitjson) and `set:json` values are encoded with `encoding/json`, through one `json.Encoder` per writer that writes straight into the pooled buffer (no intermediate `[]byte`). Measured on a Ryzen 7 9700X at `GOMAXPROCS=1`, Go 1.27.3:

| Value | Passed by value | Passed as a pointer |
|---|---|---|
| a typical post (7 fields with a `time.Time`, a slice and a pointer; about 210 bytes of JSON) | ~510 ns, 1 alloc | ~365 ns, 0 allocs |
| a 2-field struct | ~130 ns, 1 alloc | ~95 ns, 0 allocs |
| the same two values as plain typed `data-*` attributes | ~13 ns, 0 allocs | |

The allocation for a value is `encoding/json`'s own: Go 1.27's `encoding/json` runs on the json/v2 engine, which copies a value it can't address before encoding it. Pass a pointer (`orbit.JSON(&post)`) to avoid it. So use plain `data-*` attributes for a few scalars, `orbit.JSON` for structured data, and a pointer when the value is large.

JSON attribute values are single-quoted, so the quotes inside need no escaping: the 209-byte post stays 209 bytes in the HTML, where a double-quoted value with `&quot;` would take 389.

### What Orbit leaves to you

**Compression**: Orbit never gzips responses. Add the compression middleware of your choice around Orbit in `cmd/server/main.go`. See [Server](server.md).

Caching rendered SSR responses isn't built in yet.

## Slow data and the first byte: `server:defer`

Because a response is buffered, its first byte leaves the server only when the whole page has rendered. A page is as slow as its slowest part: if one component waits 300 ms on a database query or a third-party API, so does the whole page.

[`server:defer`](lazy-loading.md) is Orbit's answer. Put it on the slow component, and the page renders its `fallback` content instead and is sent at once. The browser then fetches the component's HTML in a separate request, which renders in parallel with the rest of the page loading:

```orbit file=src/components/Recommendations.orbit
---
import "time"

time.Sleep(300 * time.Millisecond) // stands in for a slow query
titles := []string{"Hello, world", "A second post"}
---
<ul>
  {for _, t := range titles}
    <li>{t}</li>
  {/for}
</ul>
```

```orbit file=src/pages/blog/index.orbit
---
import Recommendations "../../components/Recommendations.orbit"
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Blog</title>
  </head>
  <body>
    <h1>Blog</h1>
    <Recommendations server:defer>
      <p slot="fallback">Loading recommendations…</p>
    </Recommendations>
  </body>
</html>
```

What it costs:

- **On the page**, per deferred instance: one `json.Marshal` of its props and one encryption (HMAC-SHA256 plus AES-GCM, about 1 µs for 1 KB of props), plus a small inline loader script, once per page (about 1.1 KB, 0.7 KB gzipped).
- **Pages without islands pay nothing**: no loader, no extra work.
- **The island request** goes through your middleware and renders like any other request, so it costs about what rendering that component inside the page did.

Lazy component scripts (`client:idle`, `client:visible`, `client:media`) cost nothing at render time: their wrapper elements are part of the precomputed static HTML. Pages that have them get the lazy loader script inline (about 1.3 KB, 0.8 KB gzipped).

## Slow components in the same response: `server:concurrent`

The recommended way to load a page's data is in the page itself, with the fetches overlapped there (goroutines and a `sync.WaitGroup`, or an `errgroup`), passing the results down as props. Components that fetch their own data, as Astro components often do (code ported from Astro, for example), are where `server:concurrent` helps. When such a slow part must be in the HTML (SEO, above the fold, no layout shift), `server:defer` doesn't fit. [`server:concurrent`](rendering.md#slow-components) renders each marked component in its own goroutine and buffer instead, and the response is assembled in source order once they're done: the page still waits for its slowest part, but slow parts no longer add up. Two components that each wait 10 ms take about 10 ms together, not 20.

It's opt-in because it isn't free, and Go can't know in advance which components will block. Doing it for every component would cost each one a goroutine, a buffer and a copy, about 10 times the render of a small component.

Measured on full requests through `Site.Handler` with a reused `ResponseWriter` (`BenchmarkConcurrent` in the runtime; small cards; Ryzen 7 9700X, Go 1.27.3, medians of 6, one core unless noted):

| Page | In order | `server:concurrent` |
|---|---|---|
| 3 small cards | 0.17 µs, 1 alloc | 1.0 µs, 4 allocs (288 B) |
| 20 small cards | 0.45 µs, 1 alloc | 4.2 µs, 17 allocs (16 forked, 4 inline past the limit) |
| 500 small cards, limit 16 | 8.1 µs | 19.6 µs, 17 allocs |
| 500 small cards, no limit | 8.2 µs | 123 µs, 501 allocs (40 KB) |
| ~34 KB page with 2 marked cards | 3.9 µs | 5.0 µs (the assembly copy plus 2 goroutines) |

So a marked component costs about 0.3 µs and one small allocation (the goroutine's arguments, ~80 bytes), nothing against the 10 ms it hides; a marked loop stays near the in-order cost thanks to the limit. The 16-thread runs are within 10% of these, except the 500-card loop (37 µs: goroutines on other cores wake up and steal work).

- **Mark every slow component, and only those.** An unmarked slow component still blocks what follows it; a marked fast one costs more than it renders.
- **At most 16 per render run at once** (`orbit.WithConcurrencyLimit`), counted across nesting; past that, a marked component renders in order. A marked component in a `{for}` loop hits the limit quickly and usually means one query in the page would beat one per item.
- **Unmarked pages pay nothing**: no goroutine, no extra allocation, no atomic operation; the allocation tests (0 to render a typical page, 1 to serve it) are unchanged.

## Build and dev-loop speed

Orbit is written in Go partly so that builds stay fast: each component is its own Go package, so a rebuild only recompiles what changed. Measured rebuild times are under [Dev loop](#dev-loop-benchdevloop) below.

### Direct calls in production builds

`orbit dev` calls every component through a hook, an indirect call through the component's generated package, so that editing a component recompiles only that component's package and never the pages and layouts that use it. Go's compiler can't see through an indirect call, so slot content that uses a parent variable has to be allocated on the heap.

`orbit build` calls components directly instead: it writes a second version of the generated code that calls each component's implementation, to `.orbit/build/`, and compiles it with `go build -overlay`, leaving `orbit_gen/` as `orbit generate` writes it. Components that render each other in a cycle keep the hook between them (a direct call would be a Go import cycle). The HTML is byte-for-byte the same; only allocations and time differ. On the [500-card page](#orbit-vs-other-go-templates-benchgo) that's 0 allocations instead of 501, and about 13% less time.

The cost is at build time. On a generated project with 200 `.orbit` files (each component calling two others, six levels deep at most), with the standard library cached and every generated package new, `orbit build` took 732 ms instead of 650 ms (+13%) on 16 threads and 1,147 ms instead of 1,044 ms (+10%) on 4 cores; after editing the layout, 412 ms instead of 348 ms, since the pages that call the layout recompile. On `examples/blog` the difference is within noise (321 vs. 316 ms on 16 threads, 337 ms both on 4 cores). Writing the second version of the code takes about 4–5 ms. The binary is about 1% smaller. A deep chain of components, where each one calls the next, builds slower, because Go compiles a package only after the packages it imports: real sites are 2–3 levels deep. `orbit dev` is unaffected: it never writes or compiles the build version. (Medians of 5–7 alternating runs, Ryzen 7 9700X; the machine was shared, load average 3.7–7.5.)

`go test` and a plain `go build` use the hook version in `orbit_gen/`, so their allocation counts are dev's. To measure what production runs, run `orbit build`, then `go test -overlay=.orbit/build/overlay.json ./...` (see [Testing](testing.md#allocation-counts-and-production-builds)).

## Benchmark methodology

### Orbit vs. Astro

Two dedicated sites in the Orbit repository mirror each other page for page:

- `bench/orbit-site`, the Orbit version;
- `bench/astro-site`, the same site in Astro, served by `@astrojs/node` in standalone mode, as **a single Node process** (no cluster, no Bun), with `output: 'server'`.

They're kept separate from the example blog so the comparison is easy to audit. Every benchmark page renders on each request in both sites: `src/pages/cards.orbit`, `src/pages/fetch.orbit` and `src/pages/fetch-deferred.orbit`, with `.astro` mirrors.

**Scenarios**, driven by `bench/load`, a small load generator written in Go (closed loop over keep-alive connections, exact percentiles, RSS and CPU sampled from `/proc`). `bench/run.sh` builds both sites, checks they serve the same visible text, and runs every scenario with a fresh server process. The server and the load generator are pinned to separate physical cores:

1. **Render-heavy** (`cards`): a list of 500 cards built from nested components. This measures the template engine itself.
2. **Realistic** (`fetch`): a page whose frontmatter waits on a simulated 10 ms data fetch. This measures how well each server handles many requests waiting on I/O at once.
3. **Cores**: Orbit runs each scenario twice, with `GOMAXPROCS=1` (one core, the fair comparison with a single Node process) and with all cores (how a Go server normally runs). Astro also runs twice: on the same 8 CPUs as Orbit at all cores, so its garbage collector and I/O threads have room, and pinned to one CPU, like Orbit at `GOMAXPROCS=1`.
4. **Deferred** (`fetch-deferred`): the 10 ms-fetch page with the slow part moved into a `server:defer` component (Astro's mirror uses `server:defer` too). This measures the page's time to first byte, and the island request's latency on its own. A second run requests only the deferred page, as fast as the server allows, to show what the page costs without its island.

**Metrics**:

- requests per second;
- latency at the 50th and 99th percentiles (p50, p99);
- time to first byte (TTFB) at p50 and p99, for scenario 4, next to the non-deferred page;
- memory use of the server process (RSS).

The published results list the machine, the operating system, the Go, Node and Astro versions and the load-tester settings, with the commands to reproduce them.

### Orbit vs. other Go templates

A separate set of Go benchmarks (`bench/go`) renders an equivalent page with Orbit, with [templ](https://templ.guide) and with the standard library's `html/template`. These are measurements only: matching any of them isn't a goal or a gate.

### Micro-benchmarks

The runtime's writer and escaper have their own Go micro-benchmarks, which report allocations. They're development tools for Orbit itself, run when the runtime changes. There are no automatic timing gates in CI. Allocation counts on the hot path are the exception: they're checked by ordinary tests (see [Allocations per request](#allocations-per-request)).

## Results

Measured on 2026-10-25 on an AMD Ryzen 7 9700X (8 cores, 16 threads), 46 GB RAM, Linux 7.2.11, with Go 1.27.3, Node 25.6.1, Astro 7.3.8 and `@astrojs/node` 11.1.7 (standalone, one process). Every run uses 64 keep-alive connections, a 5 s warm-up and 20 s of measurement. The server runs on cores 0–3 (both threads of each) and the load generator on cores 4–7; Orbit at `GOMAXPROCS=1` and "Astro, one CPU" are pinned to one CPU; "Astro" gets the same 8 CPUs as Orbit at all cores. No run had an error. Reproduce with `bench/run.sh` (about 7 minutes).

### Render-heavy page (`/cards`, 500 cards from nested components)

| Server | req/s | p50 | p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 8,518 | 7.4 ms | 20.8 ms | 15 MB | 0.11 ms |
| Orbit, all cores | 33,012 | 0.78 ms | 12.7 ms | 37 MB | 0.22 ms |
| Astro, one CPU | 679 | 92.9 ms | 149 ms | 445 MB | 1.45 ms |
| Astro | 696 | 90.2 ms | 124 ms | 505 MB | 1.55 ms |

On one CPU each, Orbit serves about 12.5× as many pages as Astro. With all cores, about 47× as many as Astro's single Node process. Part of that second number is Go using every core while Node runs JavaScript on one thread: giving Astro 8 CPUs instead of one helps it by only 2% here.

### Where the time goes (Orbit vs. Astro)

To see where that gap comes from, both servers were CPU-profiled while serving `/cards` under the same load: Astro with V8's sampling profiler, Orbit (at `GOMAXPROCS=1`) with Go's `pprof`. Profiling slows both servers and the machine wasn't idle, so only the shares below are meaningful, not absolute times.

| Share of CPU on `/cards` | Astro | Orbit |
|---|---:|---:|
| Setting up each component (Astro's `Astro` global, component instance, slot closures) | 23% | — |
| Walking the template and building the output | 36% | 43% |
| Escaping text and building attributes | 16% | 24% |
| Allocation and garbage collection | 4% | 11% |
| Converting the HTML to bytes and sending it (UTF-8 encoding, streams, socket writes) | 17% | 18% |
| Request handling (HTTP parsing, routing, middleware) | 3% | 3% |
| **CPU per page** (from the table above) | **1.55 ms** | **0.11 ms** |

In Orbit a component is a plain function call, so its cost is in the other rows. Astro's GC row counts only the main thread's pauses; V8's helper threads used about 5% more CPU, mostly for concurrent GC.

What the profiles show:
- **Both spend most of their time rendering**, roughly 70–75% of the CPU. Astro's rendering costs roughly 14× Orbit's per page. Fixed per-request costs are small for both: the `/fetch` page below costs each under 10% of `/cards`' CPU.
- **Astro does at request time what Orbit does at compile time.** Each component render creates objects (an `Astro` global, a component instance, a template result holding the static parts and the expression values), and a generic loop walks them, checking each value's type and concatenating JavaScript strings. Every interpolated string goes through a regular-expression escaper, and every dynamic attribute is classified by name and built at runtime. Orbit's generated code writes merged static HTML and typed values straight into one buffer.
- **It isn't promises.** Astro 7 renders synchronously while components are synchronous: a `/cards` request creates about 120 promises for 1,501 component renders, and promise handling doesn't show up in the profile.
- **Sending costs about the same share for both**, but Astro first encodes the whole page from a JavaScript string to UTF-8 and pulls it through a web stream into Node's HTTP server.

The shares and counts are measured. The per-row grouping of functions is ours, and V8 inlines small functions into their callers, so Astro's rows are approximate to a few points. The details (top functions, GC counts) are in `bench/profile/README.md`; reproduce with `bench/profile/run.sh` (about a minute). That page also shows where templ's 9k allocations per page in the [Go templates benchmark](#orbit-vs-other-go-templates-benchgo) come from: mostly from passing every expression, strings included, through `fmt.Sprint`, and from a new `context.Context` per component with children.

### Page with a 10 ms data fetch (`/fetch`)

| Server | req/s | p50 | p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 5,842 | 10.9 ms | 11.8 ms | 15 MB | 0.010 ms |
| Orbit, all cores | 5,947 | 10.8 ms | 11.2 ms | 18 MB | 0.023 ms |
| Astro, one CPU | 4,737 | 13.0 ms | 27.2 ms | 304 MB | 0.102 ms |
| Astro | 5,333 | 11.9 ms | 15.6 ms | 294 MB | 0.112 ms |

Every server spends most of each request waiting, and 64 connections waiting 10 ms each cap the rate near 6,400 req/s, so throughput is close. The differences are in the tail latency, the CPU and the memory.

### The fetch moved into `server:defer` (`/fetch-deferred`)

Each iteration requests the page, then its island, as the browser's loader does. The time to first byte (TTFB) is the page's; the island column is the island request's own latency.

| Server | iterations/s | page TTFB p50 | page TTFB p99 | island p50 | island p99 | Memory (RSS) |
|---|---:|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 5,584 | 0.46 ms | 1.29 ms | 11.0 ms | 12.0 ms | 15 MB |
| Orbit, all cores | 5,920 | 0.06 ms | 0.49 ms | 10.8 ms | 11.3 ms | 18 MB |
| Astro, one CPU | 2,284 | 7.23 ms | 39.5 ms | 16.6 ms | 43.9 ms | 292 MB |
| Astro | 2,833 | 6.11 ms | 12.6 ms | 15.7 ms | 21.4 ms | 316 MB |

Deferring the slow component takes Orbit's first byte from about 11 ms to well under 1 ms. Astro's first byte stays at several milliseconds because its single JavaScript thread is nearly saturated by the pages and islands together (0.9 cores busy).

The deferred page on its own, without its island (`fetch-deferred-page` in `bench/run.sh`):

| Server | req/s | TTFB p50 | TTFB p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 141,910 | 0.45 ms | 0.99 ms | 15 MB | 0.005 ms |
| Orbit, all cores | 416,746 | 0.09 ms | 0.58 ms | 17 MB | 0.010 ms |
| Astro, one CPU | 6,541 | 7.50 ms | 37.2 ms | 295 MB | 0.150 ms |
| Astro | 8,681 | 6.65 ms | 13.0 ms | 299 MB | 0.158 ms |

Even without island requests, Astro's TTFB is mostly queueing: one thread at about 8,700 pages per second is full. Orbit at all cores is limited by the load generator here, which used about 6 of its 8 CPUs. Deferring costs the page one seal of its props (about 1 µs, see [Slow data and the first byte](#slow-data-and-the-first-byte-serverdefer)), which is why this page costs Astro more CPU than `/fetch`.

### Orbit vs. other Go templates (`bench/go`)

The same 500-card page (a layout, a header with a nav loop, and cards with attributes, escaped text and a slot), rendered into a buffer. Orbit's row is the code `orbit build` produces (`go test -overlay=.orbit/build/overlay.json -bench . -benchmem`, see `bench/go/README.md`); 6 runs, medians, spread ±1.5% or less (2026-10-26, load average 1.5–1.9).

| Engine | Time per page | Memory per page | Allocations per page |
|---|---:|---:|---:|
| Orbit | 50.2 µs | 13 B | 0 |
| Orbit in dev and `go test` (hook calls) | 57.8 µs | 39 KB | 501 |
| templ v0.3.1070 | 245 µs | 276 KB | 8,985 |
| `html/template` | 1.73 ms | 622 KB | 26,810 |

With direct calls, the 500 slot closures that use the loop variable, and the layout's, stay on the stack (see [Direct calls in production builds](#direct-calls-in-production-builds)); through the hooks, each costs one allocation. Serving the page through `site.Handler()` adds one allocation (`Content-Length`) and almost no time (50.9 µs). A small page with a route param is served in 0.34 µs with 1 allocation, `Content-Length` (3 through the hooks: also a slot closure and a string concatenation it captured). On one core (`-cpu 1`) the direct calls save 18% (49.5 vs. 60.3 µs).

### Dev loop (`bench/devloop`)

The time from a saved edit to a new server running, measured on `orbit dev`'s own rebuild pipeline (`bench/devloop -dev`) with a generated project of 200 `.orbit` files (40 pages, 158 components, scoped styles and scripts on a quarter of them, two user Go packages, typed locals). It covers generating, the type check, esbuild when needed, `go build` with the dev flags (see [Dev server](dev-server.md#what-a-change-rebuilds): no DWARF or symbol table, no inlining in the generated packages, no VCS stamping, no `GOGC`) and restarting the server, but not the watcher's 50 ms debounce or the browser's reload. Median of 7 runs on a quiet machine (load average 0.4 before the first run; the 4-core runs started at 3.2–3.4, left by the benchmark's own previous run), Ryzen 7 9700X. The "before" column is the same benchmark before the dev-loop speed-ups listed below, run alternately with the newer code on the same machine (load average 3–7: another build was running).

| Edit | 16 threads | 4 cores | Before (16 threads / 4 cores) |
|---|---:|---:|---:|
| Template text | 176 ms | 177 ms | 226 / 230 ms |
| Frontmatter statement | 168 ms | 171 ms | 222 / 227 ms |
| Add a `Props` field (component imported by 10 others) | 187 ms | 196 ms | 242 / 270 ms |
| Add a page | 218 ms | 230 ms | 260 / 272 ms |
| Only a `<style>` or `<script>` (no Go rebuild; before code splitting, see [below](#code-splitting)) | 23 ms | 26 ms | same |
| A Go package every `Props` imports, function body only | 602 ms | 995 ms | 740 ms / 1.28 s |
| The same package, new exported name | 609 ms | 997 ms | 770 ms / 1.30 s |

The speed-ups, all dev only (`orbit build` is unchanged):

- **No symbol table and no DWARF** (`-ldflags=-s -w`, `-gcflags=all=-dwarf=false`): a faster link on every rebuild, and smaller objects to write. The first `orbit dev` after a Go toolchain change compiles the standard library once with these flags (about 3–4 s), and dev's compiled packages aren't shared with your own `go build` or `go test`.
- **`-buildvcs=false`**: `go build` no longer runs the version-control tool on every build (10–20 ms in a repository).
- **A lighter `orbit.Ctx` type**: the runtime keeps its island sealer, and the `sync.Pool` inside it, out of `Ctx`'s type graph, so each generated package no longer compiles generic `sync/atomic` code of its own. A package that only mentions `*orbit.Ctx` went from 196 symbols to 6. It matters most when every generated package recompiles.
- **No `GOGC` or `GOMEMLIMIT` for `go build`**: the Go compiler tunes its own memory use only when both are unset. With `GOGC=100` set in the shell, the slowest row on 4 cores went from 2.1 s to 1.09 s. Your server still gets them.
- **Only the edited component and one small package recompile**: the generated `app` package (the route table, which imports every page) no longer imports the component implementations itself; a small `orbit_gen/link` package does. A template edit recompiles the component and `link` (8–12 ms) instead of the component and `app` (21–38 ms).
- **Only the edited file is re-parsed**: `orbit dev` re-reads and re-analyzes just the files that changed (0.6 ms instead of about 10 ms at 200 files), and starts loading Go type information for the type check while it parses.

Most of a template edit is `go build`, and most of that is the link, about 105 ms (130 ms before), a fixed cost for any Go binary of this size. Template, frontmatter and `Props` edits skip esbuild, and a `.go` edit starts `go build` at once, while Orbit regenerates. The slow rows are a Go package that every component imports: `go build` then recompiles all the generated packages, even when only a function body changed, because Go recompiles a package's importers whenever it changes. Keep frequently edited Go code out of packages that every `Props` imports.

#### Tailwind CSS

The same benchmark with `-tailwind` (PLAN.md §5d): the project gets a `global.css` with `@import "tailwindcss"` in its layout and 12 utilities in every `.orbit` file, and `orbit dev` runs the Tailwind CSS 4.3.3 standalone CLI. A Tailwind run takes 0.07–0.1 s here; for a template, frontmatter or `.go` edit that brings a new word it starts with the batch, in parallel with regenerating and `go build`, and esbuild runs after it only when the CSS changed. Every edit below brings a new word, so Tailwind runs on each one. Median of 7 runs, the two configurations run back to back; the machine was shared with other builds (load average 13–21 during these runs), so treat the differences of a few ms as noise.

| Edit | 16 threads, without → with Tailwind | 4 cores, without → with Tailwind |
|---|---:|---:|
| Template text | 183 → 188 ms | 183 → 205 ms |
| Frontmatter statement | 173 → 180 ms | 178 → 200 ms |
| A Go package every `Props` imports, function body only | 594 → 642 ms | 1.10 → 1.04 s |
| Only a `<style>` or `<script>` (no Go rebuild) | 18 → 90 ms | 23–27 → 91–92 ms |

On template and frontmatter edits Tailwind is hidden behind `go build`; what remains (5–20 ms) is CPU contention, worse on 4 cores. A style or script edit has no Go rebuild to hide behind, so it waits for the run, then esbuild. A project without Tailwind runs none of this: no lookup, no process, no word set (measured the same as before, 173 and 17 ms).

`orbit build` on the same project, everything else cached: 0.30 s without Tailwind, 0.40 s with it (5 runs each, three rounds, load average 8.5). A project without Tailwind builds in the same time as before (0.30–0.31 s).

#### Code splitting

Since [shared chunks and `import()`](styles-and-scripts.md#shared-chunks-and-import) (PLAN.md §5e), a component's script block is copied into the bundle of each page that uses it, so that blocks run in import order, while imported files become shared chunks. esbuild processes each copy: on the same 200-file project (every 4th component has a script importing a shared `util.ts`; about 800 block copies instead of 40 blocks), measured against the code before, alternated:

| | Before | With code splitting |
|---|---:|---:|
| Only a `<style>` or `<script>` (`bench/devloop -dev -kinds css,ts`, median (min) of 15, two rounds, load average 9–10) | 16 (15) ms | 26–31 (25–27) ms |
| esbuild in `orbit generate`/`orbit build` (median (min) of 15 separate processes, load average 1.4) | 15 (14) ms | 31 (28) ms |
| `orbit generate` (median (min) of 15, load average 11) | 67 (64) ms | 78 (75) ms |
| JS requests per page (the module and its preloaded chunks) | 1 | 2.0 |
| JS per page, raw / gzip (sum of per-file gzip) | 1,914 / 320 B | 1,976 / 406 B |

A style or script edit stays far inside the ~1 s budget, but it is about 11–14 ms slower than before, most of it esbuild linking and printing the copies. A scoped style block is scoped once and a Tailwind block runs Tailwind once per build, however many pages copy it. A page with no shared imports gets the same single module as before.

### Editor (`orbit lsp`)

`bench/lsp` starts `orbit lsp` on the same 200-file project as the dev-loop benchmark and drives it as an editor would, with gopls v0.24.0 and a warm Go build cache. It runs in two modes: without `orbit_gen/` on disk, where every generated file reaches gopls in memory, and after `orbit generate`. Startup times are the median of 3 starts, measured from process start. Request times are the median of 20 runs after the readiness barrier, and a completion includes the edit before it. Measured on a quiet machine (load average 1.5 at the start, 1.4 at the end). The last column is from the milestone 0 spike, which drove gopls directly with hand-written generated code, at a smaller size.

| | No `orbit_gen/` | `orbit_gen/` on disk | Spike |
|---|---:|---:|---:|
| Start → first hover or completion | 340 ms | 290 ms | 386 ms cold, 132 ms warm (102 files) |
| Start → gopls's diagnostics on an open page | 342 ms | 234 ms | – |
| Start → references answered (references and rename wait until every package is loaded) | 342 ms | 274 ms | 2.4–2.5 s in memory, 0.8 s on disk (102 files) |
| Edit + completion, `{props.` | 2.07 ms | 2.54 ms | 1.6–3 ms |
| Edit + completion, a bare identifier | 7.88 ms | 9.18 ms | – |
| Edit + completion, `<Card ` props | 1.38 ms | 1.22 ms | – |
| Hover, definition, references of a local | 0.07–0.10 ms | 0.08–0.11 ms | – |
| Rename of a local | 0.18 ms | 0.18 ms | – |
| References of a `Props` field (used by other components) | 0.15 ms | 13.2 ms | – |
| Memory: `orbit lsp` (peak) | 38 MB | 47 MB (109 MB) | – |
| Memory: gopls, after startup | 182 MB | 180 MB | 260 MB warm / 650 MB cold (50 components) |
| Memory: gopls, after the requests | 282 MB | 300 MB | – |

Orbit answers `initialize` and publishes its own diagnostics (such as an unknown prop) in about 25 ms. With `orbit_gen/` on disk, gopls reads the generated files that are up to date from disk, and only the files you edit go to it in memory. That's why references of a `Props` field take longer there: gopls keeps only the packages it holds in memory fully loaded, and reloads the others for a cross-component search. `orbit lsp`'s peak on disk is a one-time check at startup that compares `orbit_gen/` with the sources (about 70 ms). Plain gopls on the same generated code uses about 300 MB.

#### TypeScript in `<script>` blocks

On the same project, with TypeScript 7.0.2: every fourth component has a `<script>`, so opening the probe component starts the TypeScript child next to gopls. The Go and template rows above don't change with it running (measured back to back with and without it, at a load average of 2–4: first hover, completion and references within the run-to-run noise, `orbit lsp` 45–56 MB either way).

| | No `orbit_gen/` | `orbit_gen/` on disk |
|---|---:|---:|
| Start → first completion in a `<script>` (the child starts lazily on the `didOpen`) | 55 ms | 85 ms |
| Edit + completion in a script (`document.`) | 2.77 ms | 2.82 ms |
| Edit → TypeScript diagnostic published (includes the 50 ms debounce) | 52.8 ms | 52.6 ms |
| Hover in a script | 0.12 ms | 0.13 ms |
| First references in a script (opens every script's document, 40 here) | 35 ms | 37 ms |
| References in a script, after that | 1.13 ms | 1.02 ms |
| Memory: the TypeScript child | 78 MB | 78 MB |
| Memory: the TypeScript child, every script open | 108 MB | 109 MB |

With `orbit_gen/` on disk, the first script completion waits a little longer because the startup typed check runs at the same time. A Go edit that changes a script's data types reaches the script's completion and diagnostics after the 300 ms debounce of the typed check plus a few milliseconds.

## See also

- [Rendering](rendering.md): the request lifecycle and buffered responses
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Server](server.md): where to add compression and other middleware
- [Dev server](dev-server.md): rebuild times in development
- [Runtime API](reference/runtime-api.md): the `Writer` and its typed writes
