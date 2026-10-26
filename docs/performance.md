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

- A component call is a plain Go function call, plus one indirect call through the component's generated package.
- **Slots are a small struct**, not a map: a default slot plus a short list of named ones. A call with no children allocates nothing, and neither does slot content that uses no variables from the parent. Slot content that does (`<Card>{p.Title}</Card>`) allocates one small closure per slot.
- The recursion depth guard, which turns runaway recursion into an error instead of a crash, costs one integer increment per component render. It's always on.

### Routing and locals

- **Routes** are matched by walking a tree of path segments built at startup. No regular expressions.
- **Route params allocate nothing**: the router stores matched values in the pooled request context instead of a map, and a page's typed `params` is a plain struct filled from them. (A `map[string]string` cost 2 allocations, 336 bytes, per match.) Only a value with percent-escapes (`caf%C3%A9`) allocates its decoded string.
- **Typed locals** are allocated lazily, the first time a page or component uses them, and once per request at most. Orbit only fetches `locals` in a page or component whose code mentions it, so a page whose components never use `locals` costs nothing for them, even in a project that declares a `Locals` type.

### Allocations per request

For a typical page, rendering allocates nothing in Orbit's code: routing, the request context, the buffer, escaping, typed writes and head injection are all allocation-free. Serving the page adds exactly one allocation (48 bytes): the `Content-Length` header value. It can't be reused, because `net/http` may read the headers after the handler returns. Orbit's tests check both numbers, so a change that adds an allocation fails `go test`.

A "typical page" here means:
- its expressions are plain types, so they get typed writes
- its slot content doesn't use variables from the parent
- it doesn't use locals or `server:defer`

What still allocates:
- **Go's `net/http` server itself**: about 14 allocations (1.4 KB) per request even for an empty handler, and about 19 (2.2 KB) once it writes a 1 KB page (measured on Go 1.27.3). Orbit uses the standard library server, so this floor is the same as for any Go web app.
- **Locals**, when a page or one of its components uses them: one allocation for the struct (made by Orbit on first use, or by `EnsureLocals` in your middleware), plus the request copy that `EnsureLocals` makes.
- **Slot content that uses parent variables**: one small closure per slot.
- **Values without a typed write** (the `Expr` fallback) and your own `String()` or `Error()` methods.
- **Route params with percent-escapes**, and **`server:defer` props** (encoded and encrypted per instance).
- **Your frontmatter code**: database queries and the like. This is usually the biggest part.

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

## Build and dev-loop speed

Orbit is written in Go partly so that builds stay fast: each component is its own Go package, so a rebuild only recompiles what changed. Measured rebuild times are under [Dev loop](#dev-loop-benchdevloop) below.

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

The same 500-card page (a layout, a header with a nav loop, and cards with attributes, escaped text and a slot), rendered into a buffer. `go test -bench . -benchmem -count 8`; spread ±1% or less.

| Engine | Time per page | Memory per page | Allocations per page |
|---|---:|---:|---:|
| Orbit | 59.4 µs | 39 KB | 501 |
| templ v0.3.1070 | 245 µs | 276 KB | 8,985 |
| `html/template` | 1.73 ms | 622 KB | 26,810 |

Orbit's 501 allocations are the 500 slot closures plus the layout's (each slot that uses a loop variable costs one, see [Allocations per request](#allocations-per-request)). Serving the page through `site.Handler()` adds one allocation (`Content-Length`) and almost no time (59.8 µs). A small page with a route param is served in 0.39 µs with 3 allocations: `Content-Length`, a slot closure, and the page's own string concatenation.

### Dev loop (`bench/devloop`)

The time from a saved edit to a new server running, measured on `orbit dev`'s own rebuild pipeline (`bench/devloop -dev`) with a generated project of 200 `.orbit` files (40 pages, 158 components, scoped styles and scripts on a quarter of them, two user Go packages, typed locals). It covers generating, the type check, esbuild when needed, `go build` with the dev flags (see [Dev server](dev-server.md#what-a-change-rebuilds): no DWARF or symbol table, no inlining in the generated packages, no VCS stamping, no `GOGC`) and restarting the server, but not the watcher's 50 ms debounce or the browser's reload. Median of 7 runs on a quiet machine (load average 0.4 before the first run; the 4-core runs started at 3.2–3.4, left by the benchmark's own previous run), Ryzen 7 9700X. The "before" column is the same benchmark before the dev-loop speed-ups listed below, run alternately with the newer code on the same machine (load average 3–7: another build was running).

| Edit | 16 threads | 4 cores | Before (16 threads / 4 cores) |
|---|---:|---:|---:|
| Template text | 176 ms | 177 ms | 226 / 230 ms |
| Frontmatter statement | 168 ms | 171 ms | 222 / 227 ms |
| Add a `Props` field (component imported by 10 others) | 187 ms | 196 ms | 242 / 270 ms |
| Add a page | 218 ms | 230 ms | 260 / 272 ms |
| Only a `<style>` or `<script>` (no Go rebuild) | 23 ms | 26 ms | same |
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

## See also

- [Rendering](rendering.md): the request lifecycle and buffered responses
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Server](server.md): where to add compression and other middleware
- [Dev server](dev-server.md): rebuild times in development
- [Runtime API](reference/runtime-api.md): the `Writer` and its typed writes
