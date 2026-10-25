# Spike C: go/types cost for typed writes (PLAN.md §3.3)

**Question.** Is type-checking the generated code at generate time fast enough for `orbit build`, and for the `orbit dev` loop (about 1 s per save)? What's the fastest viable approach?

**Answer.** Yes, with any approach. The type check costs much less than the `go build` + link that follows it. The fastest approach isn't go/packages. It's plain `go/types`, with:
- `importer.ForCompiler(fset, "gc", lookup)` over `go list -export` data for **non-generated** packages only (user code, runtime, std);
- decl and impl packages checked **from source**, in-process and in parallel.

In a long-lived `orbit dev` process, re-checking one edited component takes about **0.3 ms**. The coordinator re-ran `go test ./typed` (pass) and one s200 srcdecl run (median 43 ms) independently.

## Setup
- **Machine**: AMD Ryzen 7 9700X, 8C/16T, 46 GB RAM, Fedora 44.
- **Software**: Go 1.27.3 (auto-switched from 1.23.4; the switch adds under 10 ms), x/tools v0.52.0.
- **Noise**: other agents were building at the same time. Treat ±15% as noise.
- **Synthetic project** (`cmd/gensite`), in the §3.1 layout:
  - **s50**: 50 components, about 1.5k `Expr` calls. **s200**: 200 components, 5.8k `Expr` calls.
  - Each impl imports 3–5 decl packages, cycles included, with about 29 mixed-type expressions, a for loop and slot closures.
  - Every `Props` references `internal/posts`, the worst case for user-package edits.
- **Approaches** (`cmd/tcbench`):
  - **pkgs**: go/packages.
  - **custom**: one `go list -export`, impl packages checked from source.
  - **srcdecl**: like custom, but decl packages are also checked from source.
  - **hot**: a resident process re-checks only the changed package.
  - **none**: `Expr` fallback, no check.
- **Correctness**: after the typed rewrite, both sites render byte-identical output to the `Expr` version, and `go vet` is clean.

## Timings (ms, s50 / s200, 16 threads)

| Scenario | none | pkgs | custom | srcdecl | hot |
|---|---|---|---|---|---|
| Check only, warm, all impl packages | — | 53 / 110 | 38 / 68 | – / 46 | — |
| Check only, warm, one impl package | — | 34 / 34 | 28 / 26 | — | **0.26 / 0.24** |
| `orbit build` (check + rewrite + `go build`), std cached | 377 / 752 | 561 / 1379 | 251 / 705 | – / 1200 (4C) | — |
| `orbit build`, empty GOCACHE | 3262 / 3695 | 3417 / 4205 | 3244 / 3657 | — | — |
| Dev: body edit, total incl. build | 192 / 236 | 287 / 289 | 226 / 267 | 227 / 344* | ≈ none + 0.3 |
| Dev: `Props` edit, total | 204 / 248 | 302 / 311 | 234 / 288 | 234 / 304* | ≈ none + a few ms |
| Dev: user `.go` edit (all packages affected), total | 315 / 752 | 526 / 1233 | 366 / 948 | 351 / 915 | ≈ srcdecl |
| Check step within that user edit | — | 211 / 647 | 91 / 288 | 38 / 69 | — |

\* Measured at load average 5–6. That run's own `none` baseline was 286 ms (body edit) and 830 ms (user edit).

**4 cores** (s200; none / pkgs / custom / srcdecl):
- `orbit build`, std cached: 1308 / 2227 / 1233 / 1200
- dev body edit: 243 / 293 / 268 / 302
- dev user edit: **1303** / 2149 / 1397 / 1362
- hot re-check: 0.29 ms

**Other measurements**:
- In a body-edit rebuild, compiling the changed package takes 35 ms; the rest is app recompile plus link.
- `-ldflags=-w` brings that rebuild from 190 to 148 ms.
- Typed code and `Expr` code compile at the same speed.

## Analysis
1. **The type check is never the bottleneck; `go build` and the link are.** Typed writes add 0.3 ms (hot), about 30 ms (custom) or about 50 ms (pkgs).
2. **go/packages compiles impl packages twice**: once in `go list -export`, again in `go build`. That costs +0.5–0.9 s on a cold 200-component build, and +0.85 s on a user edit on 4 cores. The custom approaches compile only what `go build` reuses afterwards anyway, so their check is close to free.
3. **srcdecl wins on user-package edits.** It checks the 400 generated packages from source in about 15 ms, instead of compiling every stale decl.
4. **The 1 s dev target is at risk from `go build`, not typed writes.** A user `.go` edit that every `Props` depends on rebuilds 400 packages: 0.75 s at 16 threads, **1.3 s at 4 cores**, even with no type check.
5. **A persistent type cache isn't worth it.** The full check is ≤ 70 ms, mostly `go list`.
6. **Pass 1 never needs to touch disk** with custom, srcdecl or hot.

## Mapping (`typed/`, 55 expression kinds; typed vs `Expr` output byte-identical)
`Expr`'s type switch matches **exact dynamic types**: `type Slug string` does *not* hit `case string`. The typed choice must mirror that.

| Static type | Typed write |
|---|---|
| untyped nil | nothing |
| constants | by default type; can be folded into `Raw` |
| `orbit.HTML` | `HTML` |
| `string` / alias | `String` |
| `orbit.URL` | `String(string(x))` |
| `[]byte` | `Bytes` |
| ints / untyped rune | `Int(int64)` |
| uints / byte | `Uint(uint64)` |
| `float64` | `Float` |
| `float32` | `Float(float64)` (mirrors widening) |
| `bool` | `Bool` |
| `orbit.Renderer` / unnamed `func(*Writer) error` | `Render` |
| static interface implementing `error`, or a concrete type implementing `error` | `Error` |
| concrete `fmt.Stringer` (value or pointer receiver, `time.Time`, `type Status int` with `String`) | `Stringer` |
| named type with no such methods, underlying string/int/uint/bool/float64 | the matching typed write (same bytes as `fmt.Sprint`) |
| static `fmt.Stringer` / `any` / other interfaces; named float32, `[]byte` or func; `T` with `String` on `*T`; `*string`; slices; `uintptr`; complex | **`Expr`** |

Implementation notes:
- Call `types.Unalias` first.
- Find calls by the offset recorded in pass 1.
- Pass 2 re-emits the file rather than patching it, so line-directive columns stay correct.

## Recommendation
- **`orbit build`**: srcdecl. Run one `go list -export -e` (with the build's `-tags`/`-mod`/GOFLAGS) for the non-`orbit_gen` imports. Check decl and impl from in-memory pass-1 source in parallel behind a memoized importer, then write the typed files.
- **`orbit dev`**: the same code, kept hot in the dev process.
  - Body edit: re-check one impl package.
  - `Props` edit: re-check that decl package and its importers.
  - User `.go`/`go.mod`/`orbit.json` edit: rerun `go list -export`, then re-check everything.
- **Fallback**: a package with type errors, or unreadable export data, keeps `Expr`/`Attr`. The following `go build` reports the error.

## Choices for the user
1. **Typed writes in dev**: on, hot and incremental (recommended), or off.
2. **Engine**: go/types + `importer.ForCompiler`, decl from source (recommended; 1.5–3× faster, drops x/tools), or go/packages (simpler, slower).
3. **Persistent type cache**: drop it (recommended) or keep it.
4. **float32 formatting**: bitSize 32 (`0.1`), or keep widening (`0.10000000149011612`).
5. **Static `fmt.Stringer` expressions**: keep them on `Expr` (recommended), or check for `error` first inside `w.Stringer`.
6. **Measured side issues**: dev builds with `-ldflags=-w` save about 40 ms. The 1.3 s user-edit rebuild at 200 components on 4 cores threatens the §7 target. A `go 1.27` module without a `toolchain` line downloads go1.27.0, so `orbit new` must always write the `toolchain` line.

## Reproduce
```sh
cd spike/typecheck
go build -o bin/gensite ./cmd/gensite && go build -o bin/tcbench ./cmd/tcbench
go test ./typed
./bench.sh 50 all; ./bench.sh 200 all; CORES=0-3 ./bench.sh 200 all
./bin/tcbench -dir sites/s200 -mode custom-srcdecl -n 5
```
