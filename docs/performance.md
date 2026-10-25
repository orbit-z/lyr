# Performance

Orbit's server rendering is designed to do as little work per request as possible; this page explains that design and how Orbit is benchmarked against Astro.

Orbit has no target number. Instead, it measures and publishes. The results will appear on this page once the benchmarks run (see [Results](#results)).

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

When Orbit compiles a template, it type-checks each `{…}` expression with `go/types` and emits a write for its exact type: a `string` goes through an escaping string write, an `int` is formatted with `strconv` straight into the buffer, an `orbit.HTML` value is copied raw, and so on. There's no conversion to `any`, no runtime type switch and no reflection. This happens in `orbit dev` too, so dev renders the same way as production.

Only values whose static type is an interface (such as `any` or `fmt.Stringer`), and a few unusual types, go through the generic path. That path is still a type switch over the common types; reflection is only its last resort (`fmt.Sprint`). Both paths produce identical bytes. The full table is in the [Runtime API](reference/runtime-api.md#typed-writes).

### Escaping allocates nothing when there's nothing to escape

The escaper first scans a string for characters that need escaping. Most strings have none, and are copied into the buffer as they are, with no intermediate string and no allocation.

### One pooled buffer per request, no streaming

Every response renders into a `bytes.Buffer` taken from a `sync.Pool`, through a writer that writes straight into it, with no `bufio` layer in between. When the page is done, Orbit inserts its `<link>` and `<script>` tags at the position it recorded for `</head>` (no scan of the output) and sends the page in one write with a `Content-Length`. SSR responses are always buffered; Orbit never streams.

Buffering also means a middleware that wraps the `http.ResponseWriter` sees the complete page, with its assets already inserted, so it can rewrite the status, headers or body. See [Middleware and locals](middleware.md).

The request context (`*orbit.Ctx`) is pooled along with its writer and buffer, so don't keep a `Ctx` after the request ends.

### Components are cheap to call

- A component call is a plain Go function call, plus one indirect call through the component's generated package.
- **Slots are a small struct**, not a map: a default slot plus a short list of named ones. A call with no children allocates nothing; a call with children allocates one closure per slot.
- The recursion depth guard, which turns runaway recursion into an error instead of a crash, costs one integer increment per component render. It's always on.

### Routing and locals

- **Routes** are matched by walking a tree of path segments built at startup. No regular expressions.
- **Route params allocate nothing**: the router stores matched values in the pooled request context instead of a map, and a page's typed `params` is a plain struct filled from them. (A `map[string]string` cost 2 allocations, 336 bytes, per match.) Only a value with percent-escapes (`caf%C3%A9`) allocates its decoded string.
- **Typed locals** are allocated lazily, the first time a page or component uses them, and once per request at most.

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

- **On the page**, per deferred instance: one `json.Marshal` of its props and one encryption (HMAC-SHA256 plus AES-GCM, about 1 µs for 1 KB of props), plus a small inline loader script of a few hundred bytes, once per page.
- **Pages without islands pay nothing**: no loader, no extra work.
- **The island request** goes through your middleware and renders like any other request, so it costs about what rendering that component inside the page did.

Lazy component scripts (`client:idle`, `client:visible`, `client:media`) cost nothing at render time: their wrapper elements are part of the precomputed static HTML.

## Build and dev-loop speed

Orbit is written in Go partly so that builds stay fast: each component is its own Go package, so a rebuild only recompiles what changed. Typical rebuild times in `orbit dev` are on the [Dev server](dev-server.md#how-long-a-rebuild-takes) page.

## Benchmark methodology

### Orbit vs. Astro

Two dedicated sites in the Orbit repository mirror each other page for page:

- `bench/orbit-site`, the Orbit version;
- `bench/astro-site`, the same site in Astro, served by `@astrojs/node` in standalone mode, as **a single Node process** (no cluster, no Bun), with `output: 'server'`.

They're kept separate from the example blog so the comparison is easy to audit. Every benchmark page renders on each request in both sites: `src/pages/cards.orbit`, `src/pages/fetch.orbit` and `src/pages/fetch-deferred.orbit`, with `.astro` mirrors.

**Scenarios**, driven by an HTTP load tester such as `oha`:

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

The runtime's writer and escaper have their own Go micro-benchmarks, which report allocations. They're development tools for Orbit itself, run when the runtime changes. There are no automatic performance gates in CI.

## Results

> **Placeholder: results are published after milestone 11.**
>
> This section will hold the measured requests per second, p50/p99 latency, TTFB and RSS for the four scenarios (Orbit at `GOMAXPROCS=1` and at all cores, against Astro), plus the Go benchmarks against templ and `html/template`. Until then, Orbit makes no claims about how much faster it is.

## See also

- [Rendering](rendering.md): the request lifecycle and buffered responses
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Server](server.md): where to add compression and other middleware
- [Dev server](dev-server.md): rebuild times in development
- [Runtime API](reference/runtime-api.md): the `Writer` and its typed writes
