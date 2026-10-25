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
3. **Cores**: Orbit runs each scenario twice, with `GOMAXPROCS=1` (one core, the fair comparison with a single Node process) and with all cores (how a Go server normally runs).
4. **Deferred** (`fetch-deferred`): the 10 ms-fetch page with the slow part moved into a `server:defer` component (Astro's mirror uses `server:defer` too). This measures the page's time to first byte, and the island request's latency on its own.

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

Measured on 2026-10-25 on an AMD Ryzen 7 9700X (8 cores, 16 threads), 46 GB RAM, Linux 7.2.11, with Go 1.27.3, Node 25.6.1, Astro 7.3.8 and `@astrojs/node` 11.1.7 (standalone, one process). Every run uses 64 keep-alive connections, a 5 s warm-up and 20 s of measurement. The server runs on cores 0–3 (both threads of each) and the load generator on cores 4–7; Orbit at `GOMAXPROCS=1` is pinned to one CPU. Astro gets the same 8 CPUs as Orbit at all cores, so its garbage collector and I/O threads have room. No run had an error. Reproduce with `bench/run.sh` (about 4 minutes).

### Render-heavy page (`/cards`, 500 cards from nested components)

| Server | req/s | p50 | p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 7,915 | 7.3 ms | 31.7 ms | 15 MB | 0.12 ms |
| Orbit, all cores | 31,084 | 0.87 ms | 13.2 ms | 37 MB | 0.23 ms |
| Astro | 680 | 92.9 ms | 127 ms | 361 MB | 1.56 ms |

On one core, Orbit serves about 12× as many pages as Astro's single Node process; with all cores, about 46×. Part of that second number is Go using every core while Node runs JavaScript on one thread.

### Page with a 10 ms data fetch (`/fetch`)

| Server | req/s | p50 | p99 | Memory (RSS) | CPU per page |
|---|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 5,834 | 11.0 ms | 11.8 ms | 15 MB | 0.010 ms |
| Orbit, all cores | 5,949 | 10.8 ms | 11.2 ms | 18 MB | 0.023 ms |
| Astro | 5,305 | 11.9 ms | 16.0 ms | 526 MB | 0.114 ms |

Every server spends most of each request waiting, and 64 connections waiting 10 ms each cap the rate near 6,400 req/s, so throughput is close. The differences are in the tail latency, the CPU and the memory.

### The fetch moved into `server:defer` (`/fetch-deferred`)

Each iteration requests the page, then its island, as the browser's loader does. The time to first byte (TTFB) is the page's; the island column is the island request's own latency.

| Server | iterations/s | page TTFB p50 | page TTFB p99 | island p50 | island p99 | Memory (RSS) |
|---|---:|---:|---:|---:|---:|---:|
| Orbit, `GOMAXPROCS=1` | 5,582 | 0.46 ms | 1.28 ms | 11.0 ms | 12.0 ms | 15 MB |
| Orbit, all cores | 5,915 | 0.06 ms | 0.48 ms | 10.8 ms | 11.3 ms | 17 MB |
| Astro | 2,818 | 6.06 ms | 11.9 ms | 15.8 ms | 22.4 ms | 306 MB |

Deferring the slow component takes Orbit's first byte from about 11 ms to well under 1 ms. Astro's deferred page is slower to start mostly because its single thread is also busy rendering the island requests.

### Orbit vs. other Go templates (`bench/go`)

The same 500-card page (a layout, a header with a nav loop, and cards with attributes, escaped text and a slot), rendered into a buffer. `go test -bench . -benchmem -count 8`; spread ±1% or less.

| Engine | Time per page | Memory per page | Allocations per page |
|---|---:|---:|---:|
| Orbit | 61.6 µs | 39 KB | 501 |
| templ v0.3.1070 | 248 µs | 276 KB | 8,985 |
| `html/template` | 1.74 ms | 622 KB | 26,810 |

Orbit's 501 allocations are the 500 slot closures plus the layout's (each slot that uses a loop variable costs one, see [Allocations per request](#allocations-per-request)). Serving the page through `site.Handler()` adds one allocation (`Content-Length`) and almost no time (61.6 µs). A small page with a route param is served in 0.40 µs with 3 allocations: `Content-Length`, a slot closure, and the page's own string concatenation.

### Dev loop (`bench/devloop`)

The time from a saved edit to a new server running, measured on `orbit dev`'s own rebuild pipeline (`bench/devloop -dev`) with a generated project of 200 `.orbit` files (40 pages, 158 components, scoped styles and scripts on a quarter of them, two user Go packages, typed locals). It covers generating, the type check, esbuild when needed, `go build -ldflags=-w` and restarting the server, but not the watcher's 50 ms debounce or the browser's reload. Median of the runs, on a quiet machine.

| Edit | 16 threads | 4 cores |
|---|---:|---:|
| Template text | 229 ms | 228 ms |
| Frontmatter statement | 226 ms | 261 ms |
| Add a `Props` field (component imported by 10 others) | 248 ms | 275 ms |
| Add a page | 263 ms | 270 ms |
| Only a `<style>` or `<script>` (no Go rebuild) | 30 ms | 38 ms |
| A Go package every `Props` imports, function body only | 793 ms | 1.46 s |
| The same package, new exported name | 842 ms | 1.50 s |

Most of a template edit is `go build`, and most of that is the link, about 135 ms, a fixed cost for any Go binary of this size. Template, frontmatter and `Props` edits skip esbuild, and a `.go` edit starts `go build` at once, while Orbit regenerates. The slow rows are a Go package that every component imports: `go build` then recompiles all the generated packages, even when only a function body changed, because Go recompiles a package's importers whenever it changes. Keep frequently edited Go code out of packages that every `Props` imports.

## See also

- [Rendering](rendering.md): the request lifecycle and buffered responses
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Server](server.md): where to add compression and other middleware
- [Dev server](dev-server.md): rebuild times in development
- [Runtime API](reference/runtime-api.md): the `Writer` and its typed writes
