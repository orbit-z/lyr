# Performance

Orbit's server rendering is designed to do as little work per request as possible; this page explains that design and how Orbit is benchmarked against Astro.

Orbit has no target number; it measures and publishes. In short (full numbers and setup in [Results](#results)):

- **Render-heavy page** (500 cards from nested components): Orbit serves about 14.6× as many pages as Astro per CPU, and about 57× at all cores against Astro's single Node process. Rendering it allocates nothing.
- **Page with a 10 ms data fetch**: similar throughput, since both servers mostly wait, but Orbit has lower tail latency and uses far less memory.

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

### Conditional values

[`orbit.If(cond, v)`](reference/template-syntax.md#conditional-values-orbitif-and-orbitcond), written as the whole value of an HTML element attribute or of a `{…}` in content, is compiled to an `if` around the typed write of `v`: `aria-current={orbit.If(cur, "page")}` becomes "if `cur`, write the constant ` aria-current="page"`", and `rel={orbit.If(ext, link.Rel)}` writes ` rel="`, the string and `"` when `ext` is true. Both arguments are still evaluated first, as for the call, and the output is the same bytes. Measured on a Ryzen 7 9700X at `GOMAXPROCS=1`, Go 1.27.3, for a dynamic string: about 9 ns and no allocation (0.6 ns when the condition is false), where the plain call through the generic attribute path takes about 35 ns and one allocation (13 ns, none, when false).

Elsewhere (inside a larger expression, in an `orbit.Attrs` value, as a component prop or a [pass-through attribute](components.md#passing-html-attributes-through)) it stays an ordinary call: no allocation for a constant or a false condition, and one for a dynamic value that's written. `orbit.Cond(c, a, b)` needs nothing special: its result has the type of `a` and `b`, so it gets the usual typed write. A `bool` on a [true/false attribute](reference/template-syntax.md#attribute-values-on-html-elements) such as `aria-pressed` is decided at build time from the attribute's name, so it costs nothing either.

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

## Loading data concurrently: `orbit.All`

A page that loads its data in its frontmatter with [`orbit.All`](data-loading.md) waits for its slowest load instead of the sum: three loads that each wait 10 ms take about 10 ms together, and a chain of two 10 ms calls next to a third takes about 20 ms. The helper itself costs a few hundred nanoseconds per call (`BenchmarkAll` in the runtime; 3 trivial loads; Ryzen 7 9700X, Go 1.27.3, medians of 5):

| Call | One core | 16 threads | Allocations |
|---|---|---|---|
| 3 plain calls in a row (no concurrency) | 4.8 ns | 4.7 ns | 0 |
| `orbit.All` with 3 loads | 460 ns | 470 ns | 8: 5 in `All` (its state, the cancellable context, 2 goroutine starts), 3 for the closures and what they capture |
| `orbit.All` with 1 load | inline call | inline call | 0 in `All` |
| 2 loads, one failing (the error path, with the load's position) | 1.5 µs | 1.8 µs | 11 |

The first load runs on the calling goroutine, so 3 loads start 2 goroutines. The failing load's `.orbit` position is looked up only on the error path. A page that doesn't call `All` pays nothing: no codegen, no field in `Ctx` or `Writer`, and the allocation tests are unchanged.

## Slow components in the same response: `server:concurrent`

The recommended way to load a page's data is in the page itself, with the fetches overlapped by [`orbit.All`](#loading-data-concurrently-orbitall), passing the results down as props. Components that fetch their own data, as Astro components often do (code ported from Astro, for example), are where `server:concurrent` helps. When such a slow part must be in the HTML (SEO, above the fold, no layout shift), `server:defer` doesn't fit. [`server:concurrent`](rendering.md#slow-components) renders each marked component in its own goroutine and buffer instead, and the response is assembled in source order once they're done: the page still waits for its slowest part, but slow parts no longer add up. Two components that each wait 10 ms take about 10 ms together, not 20.

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

The cost is at build time. On a generated project with 200 `.orbit` files (each component calling two others, six levels deep at most), with the standard library cached and every generated package new, `orbit build` took 732 ms with direct calls instead of 650 ms with hooks (+13%) on 16 threads and 1,147 ms instead of 1,044 ms (+10%) on 4 cores; after editing the layout, 412 ms instead of 348 ms, since the pages that call the layout recompile. On `examples/blog` the difference is within noise (321 vs. 316 ms on 16 threads, 337 ms both on 4 cores). Writing the second version of the code takes about 4–5 ms. The binary is about 1% smaller. A deep chain of components, where each one calls the next, builds slower, because Go compiles a package only after the packages it imports: real sites are 2–3 levels deep. `orbit dev` is unaffected: it never writes or compiles the build version. (Medians of 5–7 alternating runs on a shared Ryzen 7 9700X.)

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

Measured on 2026-10-26 (Orbit at commit 65cfe93) on an AMD Ryzen 7 9700X (8 cores, 16 threads), 46 GB RAM, Linux 7.2.11, with Go 1.27.3, Node 25.6.1, Astro 7.3.8 and `@astrojs/node` 11.1.7 (standalone, one process). Every run uses 64 keep-alive connections, a 5 s warm-up and 20 s of measurement. The server runs on cores 0–3 (both threads of each) and the load generator on cores 4–7; Orbit at `GOMAXPROCS=1` and "Astro, one CPU" are pinned to one CPU; "Astro" gets the same 8 CPUs as Orbit at all cores. No run had an error, and the machine was otherwise idle. Reproduce with `bench/run.sh` (about 7 minutes).

### Render-heavy page (`/cards`, 500 cards from nested components)

| Server | req/s | p50 | p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 9,749 | 6.6 ms | 13.0 ms | 14 MB | 0.093 ms |
| Orbit, all cores | 39,746 | 1.2 ms | 6.2 ms | 27 MB | 0.19 ms |
| Astro, one CPU | 669 | 92.5 ms | 155 ms | 475 MB | 1.47 ms |
| Astro | 698 | 89.8 ms | 125 ms | 510 MB | 1.55 ms |

On one CPU each, Orbit serves about 14.6× as many pages as Astro. With all cores, about 57× as many as Astro's single Node process. Part of that second number is Go using every core while Node runs JavaScript on one thread: giving Astro 8 CPUs instead of one helps it by only 4% here.

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
| **CPU per page** while profiled (an earlier build of Orbit) | **1.55 ms** | **0.11 ms** |

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
| Orbit, `GOMAXPROCS=1` | 5,844 | 10.9 ms | 11.8 ms | 14 MB | 0.010 ms |
| Orbit, all cores | 5,958 | 10.8 ms | 11.2 ms | 18 MB | 0.023 ms |
| Astro, one CPU | 4,748 | 12.9 ms | 27.4 ms | 381 MB | 0.102 ms |
| Astro | 5,324 | 11.9 ms | 15.7 ms | 296 MB | 0.114 ms |

Every server spends most of each request waiting, and 64 connections waiting 10 ms each cap the rate near 6,400 req/s, so throughput is close. The differences are in the tail latency, the CPU and the memory.

### The fetch moved into `server:defer` (`/fetch-deferred`)

Each iteration requests the page, then its island, as the browser's loader does. The time to first byte (TTFB) is the page's; the island column is the island request's own latency.

| Server | iterations/s | page TTFB p50 | page TTFB p99 | island p50 | island p99 | Memory (RSS) |
|---|---:|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 5,626 | 0.38 ms | 1.28 ms | 11.0 ms | 12.0 ms | 15 MB |
| Orbit, all cores | 5,916 | 0.06 ms | 0.51 ms | 10.8 ms | 11.3 ms | 17 MB |
| Astro, one CPU | 2,285 | 7.29 ms | 39.6 ms | 16.6 ms | 35.8 ms | 288 MB |
| Astro | 2,840 | 5.92 ms | 12.2 ms | 15.6 ms | 22.4 ms | 613 MB |

Deferring the slow component takes Orbit's first byte from about 11 ms to well under 1 ms. Astro's first byte stays at several milliseconds because its single JavaScript thread is nearly saturated by the pages and islands together (0.9 cores busy).

The deferred page on its own, without its island (`fetch-deferred-page` in `bench/run.sh`):

| Server | req/s | TTFB p50 | TTFB p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 139,965 | 0.45 ms | 1.00 ms | 15 MB | 0.005 ms |
| Orbit, all cores | 413,254 | 0.09 ms | 0.61 ms | 17 MB | 0.010 ms |
| Astro, one CPU | 6,616 | 7.22 ms | 38.1 ms | 288 MB | 0.148 ms |
| Astro | 8,880 | 6.43 ms | 13.3 ms | 601 MB | 0.159 ms |

Even without island requests, Astro's TTFB is mostly queueing: one thread at about 8,900 pages per second is full. Orbit at all cores is limited by the load generator here, which used about 6 of its 8 CPUs. Deferring costs the page one seal of its props (about 1 µs, see [Slow data and the first byte](#slow-data-and-the-first-byte-serverdefer)), which is why this page costs Astro more CPU than `/fetch`.

### Orbit vs. other Go templates (`bench/go`)

The same 500-card page (a layout, a header with a nav loop, and cards with attributes, escaped text and a slot), rendered into a buffer. Orbit's row is the code `orbit build` produces (`go test -overlay=.orbit/build/overlay.json -bench . -benchmem`, see `bench/go/README.md`); 6 runs, medians (2026-10-26, commit 65cfe93, quiet machine).

| Engine | Time per page | Memory per page | Allocations per page |
|---|---:|---:|---:|
| Orbit | 49.6 µs | 14 B | 0 |
| Orbit in dev and `go test` (hook calls) | 58.4 µs | 39 KB | 501 |
| templ v0.3.1070 | 257 µs | 275 KB | 8,985 |
| `html/template` | 1.75 ms | 622 KB | 26,814 |

With direct calls, the 500 slot closures that use the loop variable, and the layout's, stay on the stack (see [Direct calls in production builds](#direct-calls-in-production-builds)); through the hooks, each costs one allocation. Serving the page through `site.Handler()` adds one allocation (`Content-Length`) and almost no time (49.3 µs). A small page with a route param is served in 0.34 µs with 1 allocation, `Content-Length` (3 through the hooks: also a slot closure and a string concatenation it captured). On one core (`-cpu 1`) the direct calls save 18% (49.5 vs. 60.3 µs).

Differences under about 3% between two builds can come from code alignment alone (how the linker lays out functions), as measured on this page, so treat them as noise.

### Dev loop (`bench/devloop`)

The time from a saved edit to a new server running, measured on `orbit dev`'s own rebuild pipeline (`bench/devloop -dev`) with a generated project of 200 `.orbit` files (40 pages, 158 components, scoped styles and scripts on a quarter of them, two user Go packages, typed locals). It covers generating, the type check, esbuild when needed, `go build` with the dev flags (see [Dev server](dev-server.md#what-a-change-rebuilds): no DWARF or symbol table, no inlining in the generated packages, no VCS stamping, no `GOGC`) and restarting the server, but not the watcher's 50 ms debounce or the browser's reload. Median of 7 runs on a quiet Ryzen 7 9700X.

| Edit | 16 threads | 4 cores |
|---|---:|---:|
| Template text | 176 ms | 177 ms |
| Frontmatter statement | 168 ms | 171 ms |
| Add a `Props` field (component imported by 10 others) | 187 ms | 196 ms |
| Add a page | 218 ms | 230 ms |
| Only a `<style>` or `<script>` (no Go rebuild; without per-page script copies, see [Code splitting](#code-splitting) for their cost) | 23 ms | 26 ms |
| A Go package every `Props` imports, function body only | 602 ms | 995 ms |
| The same package, new exported name | 609 ms | 997 ms |

How `orbit dev` keeps rebuilds fast (dev only; `orbit build` doesn't do these):

- **No symbol table and no DWARF** (`-ldflags=-s -w`, `-gcflags=all=-dwarf=false`): a faster link on every rebuild, and smaller objects to write. The first `orbit dev` after a Go toolchain change compiles the standard library once with these flags (about 3–4 s), and dev's compiled packages aren't shared with your own `go build` or `go test`.
- **`-buildvcs=false`**: `go build` doesn't run the version-control tool on every build (10–20 ms in a repository).
- **A lighter `orbit.Ctx` type**: the runtime keeps its island sealer, and the `sync.Pool` inside it, out of `Ctx`'s type graph, so a generated package doesn't compile generic `sync/atomic` code of its own: one that only mentions `*orbit.Ctx` has 6 symbols instead of 196. It matters most when every generated package recompiles.
- **No `GOGC` or `GOMEMLIMIT` for `go build`**: the Go compiler tunes its own memory use only when both are unset. Passing `GOGC=100` through to `go build` made the slowest row on 4 cores take 2.1 s instead of 1.09 s. Your server still gets them.
- **Only the edited component and one small package recompile**: the generated `app` package (the route table, which imports every page) doesn't import the component implementations itself; a small `orbit_gen/link` package does. A template edit recompiles the component and `link` (8–12 ms) instead of the component and `app` (21–38 ms).
- **Only the edited file is re-parsed**: `orbit dev` re-reads and re-analyzes just the files that changed (0.6 ms, against about 10 ms to re-read all 200 files), and starts loading Go type information for the type check while it parses.

Most of a template edit is `go build`, and most of that is the link, about 105 ms, a fixed cost for any Go binary of this size. Template, frontmatter and `Props` edits skip esbuild, and a `.go` edit starts `go build` at once, while Orbit regenerates. The slow rows are a Go package that every component imports: `go build` then recompiles all the generated packages, even when only a function body changed, because Go recompiles a package's importers whenever it changes. Keep frequently edited Go code out of packages that every `Props` imports.

#### Tailwind CSS

The same benchmark with `-tailwind`: the project gets a `global.css` with `@import "tailwindcss"` in its layout and 12 utilities in every `.orbit` file, and `orbit dev` runs the Tailwind CSS 4.3.3 standalone CLI. A Tailwind run takes 0.07–0.1 s here; for a template, frontmatter or `.go` edit that brings a new word it starts with the batch, in parallel with regenerating and `go build`, and esbuild runs after it only when the CSS changed. Every edit below brings a new word, so Tailwind runs on each one. Median of 7 runs, the two configurations run back to back on a busy machine, so treat differences of a few ms as noise.

| Edit | 16 threads, without → with Tailwind | 4 cores, without → with Tailwind |
|---|---:|---:|
| Template text | 183 → 188 ms | 183 → 205 ms |
| Frontmatter statement | 173 → 180 ms | 178 → 200 ms |
| A Go package every `Props` imports, function body only | 594 → 642 ms | 1.10 → 1.04 s |
| Only a `<style>` or `<script>` (no Go rebuild) | 18 → 90 ms | 23–27 → 91–92 ms |

On template and frontmatter edits Tailwind is hidden behind `go build`; what remains (5–20 ms) is CPU contention, worse on 4 cores. A style or script edit has no Go rebuild to hide behind, so it waits for the run, then esbuild. A project without Tailwind runs none of this: no lookup, no process, no word set.

`orbit build` on the same project, everything else cached: 0.30 s without Tailwind, 0.40 s with it (5 runs each, three rounds).

#### Code splitting

With [shared chunks and `import()`](styles-and-scripts.md#shared-chunks-and-import), a component's script block is copied into the bundle of each page that uses it, so that blocks run in import order, while imported files become shared chunks. esbuild processes each copy. On the same 200-file project, with every 4th component's script importing a shared `util.ts` (about 800 block copies of 40 blocks):

| | Cost |
|---|---:|
| Only a `<style>` or `<script>` edit (`bench/devloop -dev -kinds css,ts`, median (min) of 15, two rounds) | 26–31 (25–27) ms |
| esbuild in `orbit generate`/`orbit build` (median (min) of 15 separate processes) | 31 (28) ms |
| `orbit generate` (median (min) of 15) | 78 (75) ms |
| JS requests per page (the module and its preloaded chunks) | 2.0 |
| JS per page, raw / gzip (sum of per-file gzip) | 1,976 / 406 B |

A style or script edit stays far inside the ~1 s budget; most of its time is esbuild linking and printing the copies. A scoped style block is scoped once and a Tailwind block runs Tailwind once per build, however many pages copy it. A page with no shared imports gets a single module.

### Editor (`orbit lsp`)

`bench/lsp` starts `orbit lsp` on the same 200-file project as the dev-loop benchmark and drives it as an editor would, with gopls v0.24.0 and a warm Go build cache. It runs in two modes: without `orbit_gen/` on disk, where every generated file reaches gopls in memory, and after `orbit generate`. Startup times are the median of 3 starts, measured from process start. Request times are the median of 20 runs after the readiness barrier, and a completion includes the edit before it. Measured on a quiet machine.

| | No `orbit_gen/` | `orbit_gen/` on disk |
|---|---:|---:|
| Start → first hover or completion | 340 ms | 290 ms |
| Start → gopls's diagnostics on an open page | 342 ms | 234 ms |
| Start → references answered (references and rename wait until every package is loaded) | 342 ms | 274 ms |
| Edit + completion, `{props.` | 2.07 ms | 2.54 ms |
| Edit + completion, a bare identifier | 7.88 ms | 9.18 ms |
| Edit + completion, `<Card ` props | 1.38 ms | 1.22 ms |
| Hover, definition, references of a local | 0.07–0.10 ms | 0.08–0.11 ms |
| Rename of a local | 0.18 ms | 0.18 ms |
| References of a `Props` field (used by other components) | 0.15 ms | 13.2 ms |
| Memory: `orbit lsp` (peak) | 38 MB | 47 MB (109 MB) |
| Memory: gopls, after startup | 182 MB | 180 MB |
| Memory: gopls, after the requests | 282 MB | 300 MB |

Orbit answers `initialize` and publishes its own diagnostics (such as an unknown prop) in about 25 ms. With `orbit_gen/` on disk, gopls reads the generated files that are up to date from disk, and only the files you edit go to it in memory. That's why references of a `Props` field take longer there: gopls keeps only the packages it holds in memory fully loaded, and reloads the others for a cross-component search. `orbit lsp`'s peak on disk is a one-time check at startup that compares `orbit_gen/` with the sources (about 70 ms). Plain gopls on the same generated code uses about 300 MB.

#### TypeScript in `<script>` blocks

On the same project, with TypeScript 7.0.2: every fourth component has a `<script>`, so opening the probe component starts the TypeScript child next to gopls. The Go and template rows above don't change with it running (measured back to back with and without it: first hover, completion and references within the run-to-run noise, `orbit lsp` 45–56 MB either way).

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

## Your own code

### Struct layout

Go doesn't reorder struct fields, so the padding between them costs memory, and Go's allocator rounds each allocation up to a size class: a 136-byte struct is allocated as 144 bytes, while the same fields reordered to 128 bytes fit exactly, 11% less heap per value. Putting the fields that hold pointers first also lets the garbage collector stop scanning a struct earlier. This pays off for types you hold in bulk (large slices, in-memory caches, indexes, maps of sessions). It doesn't for component `Props`, which are passed by value and usually stay on the stack in an `orbit build` binary, or for structs with one instance per request.

The `fieldalignment` analyzer finds these structs. Build it with Go 1.27, or it refuses Go 1.27 code ("package requires newer Go version go1.27"); pin a release, and `go install` adds nothing to your `go.mod`:

```sh
GOTOOLCHAIN=go1.27.3 go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.52.0
orbit generate
go vet -vettool=$(which fieldalignment) ./...
```

Findings in `.orbit` frontmatter types point at the `.orbit` line, such as `src/components/Card.orbit:4:12: Props has size 136 (allocator size class 144) but the optimal size is 128`. `fieldalignment -fix ./...` rewrites your `.go` files; it leaves `.orbit` files and `orbit_gen/` alone, so reorder `.orbit` types by hand. `-fix` drops the comments of the fields it moves, so commit first.

## See also

- [Rendering](rendering.md): the request lifecycle and buffered responses
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Server](server.md): where to add compression and other middleware
- [Dev server](dev-server.md): rebuild times in development
- [Runtime API](reference/runtime-api.md): the `Writer` and its typed writes
