# Benchmarks: Orbit vs. Astro

This directory holds the head-to-head SSR benchmark from `PLAN.md` §4b. Results are published in `docs/performance.md`.

| Path | What it is |
|---|---|
| `orbit-site/` | The Orbit site, created with `orbit new` (its `go.mod` replaces the module with `../..`, so it always benchmarks this checkout). |
| `astro-site/` | The same site in Astro: `output: 'server'`, `@astrojs/node` in standalone mode. Versions are pinned exactly in `package.json` and `package-lock.json`. |
| `load/` | The load generator, a separate Go module that uses only the standard library (`run`, `check` and `report` subcommands). |
| `run.sh` | Builds both sites, checks they serve the same content, runs every scenario and writes the report. |
| `go/` | Go-only render benchmarks: Orbit vs. templ vs. `html/template`. They're separate from this harness. |
| `profile/` | CPU profiles of both servers on `/cards` under the same load, summarized by category (`profile/run.sh`, see `profile/README.md`). |
| `lsp/` | `orbit lsp` startup, request latency and memory on the 200-file synthetic project, with and without `orbit_gen/` on disk. A separate Go module, separate from this harness (see `lsp/README.md`). |

## Running

```sh
bench/run.sh            # full run: 5 s warm-up + 20 s measured per run, about 5 minutes
QUICK=1 bench/run.sh    # smoke run: 1 s + 3 s per run, about 1 minute
```

You need Go 1.27, Node ≥ 22.12 with npm, `curl` and `taskset` (util-linux), on Linux, because RSS and CPU time are read from `/proc`. The script writes `bench/results/<date>.md` (tables) and `bench/results/<date>.json` (raw numbers and metadata). These files are gitignored: copy the tables you publish into `docs/performance.md`.

Settings are environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `CONCURRENCY` | `64` | concurrent keep-alive connections (closed loop) |
| `WARMUP`, `DURATION` | `5s`, `20s` | warm-up (not recorded) and measured window, per run |
| `SERVERS` | `orbit-1 orbit-all astro-1 astro` | which servers to run |
| `SCENARIOS` | `cards fetch fetch-deferred fetch-deferred-page fetch-cached` | which pages to run |
| `SERVER_CPUS` | `0-3,8-11` | CPUs for `orbit-all` and Astro |
| `ONE_CPU` | `0` | the single CPU for `orbit-1` and `astro-1` |
| `LOAD_CPUS` | `4-7,12-15` | CPUs for the load generator |
| `SKIP_BUILD` | unset | `1` reuses the existing builds |

The CPU defaults assume Linux's usual SMT numbering on an 8-core/16-thread CPU, where CPU *n* and CPU *n*+8 are the two threads of one core. Check yours with `lscpu -e` and adjust the defaults so the server and the load generator never share a physical core.

Run on a quiet machine: close browsers and builds, and use a fixed CPU governor if you can. A run where another job is busy is noise.

## Scenarios

| Scenario | Page | What it measures |
|---|---|---|
| 1. Render-heavy | `/cards` | A layout and 500 `Card`s. Each card has string, int, bool, URL and slice props, text with `<&>` and quotes that must be escaped, a default slot, two nested `Badge` components, a conditional, and scoped CSS. The page is about 260 KB (Orbit) and 300 KB (Astro). This is the template engine itself. |
| 2. Fetch | `/fetch` | The frontmatter waits 10 ms (`time.Sleep` / `setTimeout`) to simulate a data fetch, then renders a 20-item list. This measures concurrency over I/O waits. |
| 3. Cores | all pages | Each framework runs twice: `orbit-1` (`GOMAXPROCS=1`) and `astro-1` (the same Node process, all its threads) pinned to one CPU, and `orbit-all` and `astro` on every server CPU. |
| 4. Deferred | `/fetch-deferred` | The same 10 ms list moved into a `server:defer` component with a fallback (an Astro server island on the Astro side). Each iteration GETs the page, then the island URL taken from it, as the browser's loader does. It reports page latency and TTFB, plus the island request's latency on its own. |
| 4b. Deferred page alone | `/fetch-deferred` | Only the page, with its island never fetched (`fetch-deferred-page`): the page's TTFB without the island renders competing for the server. |
| 5. Cached | `/fetch-cached` | The `/fetch` page stored in the route cache, fresh for an hour: `ctx.Cache().Set(orbit.CacheOptions{MaxAge: time.Hour})` with `orbit.WithCache(cache.NewMemory(…))` in `orbit-site/cmd/server`, and `Astro.cache.set({ maxAge: 3600 })` with `cache: { provider: memoryCache() }` in `astro.config.mjs`. Before each run the script requests the page once (a MISS, which stores it) and fails unless the next response is a HIT (`X-Orbit-Cache: HIT` / `X-Astro-Cache: HIT`), so the run measures cache hits only. The other pages never call `Set`, so they aren't cached. |

The data (`orbit-site/internal/data/data.go`, `astro-site/src/data.ts`) is generated identically on both sides, once at startup.

## Method

- **Load generator** (`load run`): a closed loop. `-c` workers each send requests back to back over a keep-alive connection (`MaxConnsPerHost = c`, no compression, no HTTP/2), first for a warm-up and then for the measured window. Every response body is read to the end.
  - Latency is measured from request start to the last body byte; TTFB comes from `httptrace`'s `GotFirstResponseByte`.
  - Percentiles are nearest-rank over every sample, with no histogram bucketing.
  - Non-2xx responses and transport errors count as errors and are left out of the latencies.
  - req/s counts successful iterations in the window. In scenario 4 an iteration is a page plus its island.
- **Server metrics**: the generator samples `/proc/<pid>/status` every 100 ms during the window. **RSS steady** is the median VmRSS, **peak** the maximum, and VmHWM is in the JSON. **CPU cores** is the server's utime+stime over the window divided by wall time (1.00 means one core fully busy), and **CPU ms / 1k req** is the same time per 1000 iterations: a cost per request that doesn't depend on how many cores the server had.
- **Loader CPU**: the generator's own CPU use. If it approaches the number of `LOAD_CPUS`, that run was limited by the load generator, not the server, and its req/s is only a lower bound.
- **A fresh server process for each (server, scenario) pair**, so each RSS figure belongs to its scenario.
- **Content check** (`load check`): before measuring, the script fetches every page from both servers. It requires a 200 and compares the **visible text** (tags, scripts, styles and comments stripped, entities decoded, whitespace dropped), which must be identical; for scenario 4 it also compares the island responses. The check also stores `/fetch-cached` on both servers and asserts the next response is a HIT. Byte sizes are reported and differ, because the scope attributes have different names (`data-o-xxxxxxxx` vs `data-astro-cid-xxxxxxxx`). Both sides use the JSX whitespace rules (Astro v7's default, and Orbit's).

## Fairness choices

- **Same markup**: the components and pages mirror each other line for line, with the same elements, classes, scoped styles and data. Both sites link their CSS as files, because Astro's `build.inlineStylesheets` is set to `'never'` to match Orbit. On the Astro side `data-featured={featured || undefined}` emits the attribute only when true, as Orbit's bool attribute does, so the CSS means the same thing on both sides.
- **Single process for Node**: `node ./dist/server/entry.mjs` with `NODE_ENV=production`. There's no cluster mode and no Bun, as `PLAN.md` requires.
- **CPU pinning**: the servers and the load generator are pinned with `taskset` to separate physical cores.
  - `orbit-1` gets one CPU with `GOMAXPROCS=1`.
  - Astro and `orbit-all` get the same 8 CPUs (4 cores). Node runs JavaScript on one thread, but its GC and libuv helper threads can use the other CPUs; the "CPU cores" column shows how much they do (about 1.1).
  - So the `orbit-1` vs. `astro` comparison gives Node slightly *more* CPU than Orbit, never less. `astro-1` pins all of Node's threads to `orbit-1`'s single CPU, for a strict one-CPU comparison.
- **Keep-alive** connections on both servers. Neither compresses: Orbit never does, and `@astrojs/node` doesn't by default.
- **Warm-up** before each measured window, so the V8 JIT and Go's caches and pools are warm.
- **Island key**: Orbit's build embeds a fresh `ORBIT_KEY`, and Astro's build generates its own server-island key. Neither needs configuration for a single-process benchmark.
- **Responses**: Orbit buffers each response and sends it with a `Content-Length`; Astro streams with chunked encoding. That's each framework's normal behavior, and it's what's measured.

## Versions

`astro` and `@astrojs/node` are pinned to releases at least two weeks old when the benchmark was set up (`npm install --before=<date>` resolved the transitive dependencies the same way). To upgrade, edit both versions in `astro-site/package.json`, run `npm install` there and commit the lockfile.
