# bench/profile: where the CPU goes on `/cards`

CPU profiles of Lyr and Astro serving the render-heavy `/cards` page (500 `Card`s with nested `Badge`s, see `bench/README.md`) under the same closed-loop load as `bench/run.sh`. The summary is in [`docs/performance.md`](../../docs/performance.md#where-the-time-goes-lyr-vs-astro); this page has the method and the detailed numbers.

## Running

```sh
bench/profile/run.sh             # 5 s warm-up + 20 s profiled per server, about 1 minute
QUICK=1 bench/profile/run.sh     # 1 s + 5 s
SKIP_BUILD=1 bench/profile/run.sh
```

It needs what `bench/run.sh` needs (Go 1.27, Node ≥ 22.12 with npm, `curl`, `taskset`, Linux). Output goes to `bench/results/profile-<date>/` (gitignored):

| File | What it is |
|---|---|
| `summary.md` | every table below, regenerated from the raw files |
| `astro.cpuprofile` | the V8 CPU profile (open it in Chrome DevTools → Performance, or `speedscope`) |
| `astro.cpuprofile.json` | main-thread GC pauses, process CPU and event-loop utilization over the same window |
| `astro-promises.json` | promises and other async resources created by 100 sequential requests |
| `lyr.pprof`, `lyr-server` | the Go CPU profile and the binary it belongs to (`go tool pprof -http=: lyr-server lyr.pprof`) |
| `load-astro.json`, `load-lyr.json` | the load generator's result for each profiled run |

Settings are environment variables: `PAGE` (`cards`), `CONCURRENCY` (`64`), `WARMUP`/`DURATION` (whole seconds, `5s`/`20s`), `SERVERS` (`astro lyr`), `ASTRO_CPUS` (`0-3,8-11`), `LYR_CPUS` (`0`), `LYR_GOMAXPROCS` (`1`), `LOAD_CPUS` (`4-7,12-15`). The ports (4410–4412) differ from `bench/run.sh`'s.

## Method

- **The same configurations as the published rows**: Astro is the "Astro" row (one Node process on the 8 server CPUs); Lyr is the "`GOMAXPROCS=1`" row (one CPU). The load generator runs on its own cores, 64 keep-alive connections, and the profile covers only the measured window, after the warm-up.
- **Astro**: `node --import bench/profile/node-hook.mjs dist/server/entry.mjs`. The hook starts the V8 sampling profiler in-process (`node:inspector`, 200 µs interval) on the first `SIGUSR2` and writes it on the second. Over the same window it records main-thread GC pauses (`perf_hooks` `gc` entries), `process.cpuUsage()` and event-loop utilization. A second, untimed server run counts async resources with `async_hooks` over 100 sequential requests (the hook is too slow for the timed run).
- **Astro attribution**: `dist/server` is a rolldown bundle that keeps `//#region node_modules/…` and `//#region src/…` comments, so `summarize-cpuprofile.mjs` maps each sampled line back to its original file (`astro/dist/runtime/server/render/streaming.js`, `src/components/Card.astro`, …). Self time goes to the sampled frame; a native frame (a V8 builtin or C++ binding such as `writev`) counts under its nearest JavaScript caller. TurboFan inlines small functions into their callers, so function-level splits are approximate; the categories, which group whole files, are more robust.
- **Lyr**: the site's own `cmd/server`, built with `go build -overlay` adding `pprof.go.in`, which serves `net/http/pprof` on a separate listener when `LYR_PPROF_ADDR` is set. The site's handler chain is unchanged, and the overlay leaves the site's files and `dist/` alone. The script fetches a 20 s `/debug/pprof/profile` during the window; `summarize-pprof.mjs` reads `go tool pprof -traces` and puts each sample in the first category whose rule matches anywhere in its stack (GC, then `mallocgc`, then syscalls, then the page render, …).
- **Noise**: the profiler itself costs CPU (Astro ran at about 2.0 ms CPU per page instead of the published 1.56 ms), and other jobs may share the machine. Compare **shares**, not absolute numbers, and don't replace the published req/s with these.

## Results (2026-10-25)

AMD Ryzen 7 9700X, Linux 7.2.11, Go 1.27.3, Node 25.6.1, astro 7.3.8, @astrojs/node 11.1.7. Load average 2.7 (1 min) at the start: other work was running on the machine. A 5 s quick run before it gave shares within about 1 point of these.

### Astro

513 req/s, 2.05 ms CPU per request while profiled. 70,000 samples. Process CPU 1.05 cores; the event loop was 100% busy.

| Category (main-thread self time) | Share |
|---|---:|
| render engine: walking template parts and expressions, type checks, string concatenation (`render/streaming.js`, `render/astro/render.js`, `render/common.js`, `util.js`, …) | 31.6% |
| component instances: the `Astro` global, `AstroComponentInstance`, slot closures, props validation, head-propagation registration | 22.9% |
| escaping and attributes (`escapeHTML`, `addAttribute`, `toAttributeString`, `classifyAttribute`) | 16.4% |
| Node HTTP, web streams, sockets (incl. `writev` 2.7%) | 9.0% |
| UTF-8 encoding of the HTML (`TextEncoder.encode`) | 8.4% |
| the compiled `.astro` code itself (`src/`) | 4.0% |
| GC (main-thread pauses, `(garbage collector)`) | 3.6% |
| Astro request pipeline (routing, middleware, `Response`) | 2.3% |
| V8 `(program)` and other | 1.7% |
| `@astrojs/node` adapter | 0.2% |

Top functions by self time: `renderStreaming` 15.7%, **`createAstro` 14.5%** (one `Object.create` + `Object.assign` + `Object.defineProperty` per component render, 1,501 per page), `TextEncoder.encode` 8.4%, the `renderStreaming` wrapper in `render.js` 7.2% (inlined engine code), `escapeHTML` 6.1%, `toAttributeString` 5.3%, `writevGeneric` 2.8%, `Card.astro`'s template function 2.4%, `isHTMLString` 2.3%, `registerIfPropagating` 2.1%.

Inclusive (synchronous stacks): the render (`renderStreamToAsyncIterable`) 72%, writing the response (`writeResponse`, pulling the web stream into Node's `http`) 12%.

**GC**: 406 collections in 20 s, 606 ms of main-thread pauses = 3.0% of the window (minor GCs 508 ms, incremental marking 86 ms). Process CPU beyond the busy main thread was about 1 s = 4.8% of the process's CPU: V8's helper threads (concurrent marking and sweeping) and libuv. So GC is about 8% of Astro's CPU at most.

**Promises**: 122 promises, 15 `process.nextTick` objects and 1 microtask per request, for 1,501 component renders and about 5,000 interpolated expressions. Astro 7's renderer (`renderStreaming`) runs synchronously while the components do, and only switches to buffering on the first real promise. No promise or microtask machinery appears in the profile (`runMicrotasks` < 0.1%).

**Render vs. overhead**: the render categories (engine, instances, escaping, compiled code) are 75% of Astro's main-thread CPU on `/cards`. For comparison, the published `/fetch` page (a 20-item list after a 10 ms wait) costs Astro 0.114 ms CPU per request, about 7% of `/cards`' 1.56 ms, which bounds Astro's fixed per-request cost.

### Lyr

8,057 req/s, 0.114 ms CPU per request while profiled. 18.35 s of samples in 20 s (0.92 cores).

| Category (first rule matching the stack) | Share |
|---|---:|
| page render: generated code, writer, escaping | 68.4% |
| syscalls (socket write of the 260 KB page, reads, epoll) | 18.0% |
| allocation (`mallocgc`; mostly the 500 slot closures) | 6.5% |
| GC (background marking, sweeping, assists) | 3.4% |
| `net/http` (request parsing, headers, connection) | 1.8% |
| Lyr serving (routing, pooled context, response) | 1.5% |
| scheduler and other runtime | 0.3% |

Inside the render, by innermost frame: buffer writes (`bytes.Buffer`, `memmove` of the merged static HTML and the text) 29.3% of all CPU, escaping (`firstEsc` scan + `escapeSlow`; this page's text is full of `<&>`) 24.0%, other Lyr runtime (`Writer.Raw`, `Enter`/`Leave` depth guard, `URL`, `AttrBool`) 6.6%, the generated component code itself 5.6%, `strconv` 1.6%, runtime bookkeeping 1.3%.

### Side by side, per page

Using the published CPU per page (Astro 1.56 ms, Lyr 0.12 ms) and the shares above:

| | Astro | Lyr | Ratio |
|---|---:|---:|---:|
| render (all template work, incl. escaping) | ≈ 1.17 ms | ≈ 0.08 ms | ≈ 14× |
| escaping and attributes | ≈ 0.26 ms | ≈ 0.03 ms | ≈ 9× |
| encoding + sending (UTF-8, streams, socket) | ≈ 0.27 ms | ≈ 0.02 ms | ≈ 12× |
| GC (main thread / Go's background GC) | ≈ 0.06 ms | ≈ 0.004 ms | |

## templ (`bench/go`)

Not part of `run.sh`; reproduce with:

```sh
cd bench/go
go test -run '^$' -bench 'Render/engine=templ$' -benchtime 3s -memprofile mem.pprof -memprofilerate 1 -o go.test .
go tool pprof -sample_index=alloc_objects -top go.test mem.pprof
go test -run '^$' -bench 'Render/engine=templ$' -benchtime 5s -cpuprofile cpu.pprof -o go.test .
go tool pprof -top -cum go.test cpu.pprof
```

Where templ's 8,985 allocations per page come from (share of allocated objects):

| Source | Share | Why |
|---|---:|---|
| `templ.JoinStringErrs`, `templ.ResolveAttributeValue` and the `fmt.Sprint` they call | 40% | every `{ expr }`, strings included, goes through `fmt.Sprint(v)` (templ v0.3.1070, `runtime.go`): one allocation to box the value into an `any`, one for the resulting string |
| `context.WithValue` | 13% | `{ children... }` passes the children through the `context.Context`: `WithChildren` and `ClearChildren` each make a new context per component call with children |
| `runtime.GeneratedTemplate`, `templ.WithChildren`, the `Card`/children closures | 33% | each component call builds a `templ.Component` value and closure, and its children another |
| `html.EscapeString` (`strings.Replacer`) | 13% | escaping always returns a new string when there's something to escape |

CPU: `fmt.Sprint` 24% of the run inclusive, `html.EscapeString` 18%, `mallocgc` 13%, background GC 8%, `context.WithValue` 4%. Lyr's 501 allocations are the 500 slot closures plus the layout's; ints are formatted with `strconv` straight into the buffer and escaping writes into it without an intermediate string.
