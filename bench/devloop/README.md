# devloop: the orbit dev rebuild budget

This program measures the stages `orbit dev` will chain after a save
(PLAN.md §7), before `internal/dev` exists. The goal is to know where the
~1 s save-to-browser budget goes. It builds a synthetic project, then
applies each kind of edit several times. For each one it measures the time
from "file written" to "new server binary ready" and to "new server
accepting connections".

It is a separate module (`github.com/orbit-z/orbit/bench/devloop`,
`replace … => ../..`). That lets it import Orbit's `internal/` packages
without adding anything to the root module.

## Run

```sh
cd bench/devloop
go build -o /tmp/devloop .

/tmp/devloop                                            # all cores (~35 s)
GOMAXPROCS=4 taskset -c 0-3 /tmp/devloop                # 4 cores (~60 s)
QUICK=1 /tmp/devloop                                    # smoke test: 50 files, 2 runs (~10 s)
```

Build the binary first and run it directly, rather than with `go run`, so
`taskset` also applies to the `go build` children. `GOMAXPROCS=4` matters
too: the type checker and `go build -p` size their parallelism from it.

| Flag | Meaning |
|---|---|
| `-files 200` | `.orbit` files: 1/5 pages, 2 layouts, the rest components |
| `-runs 7` | measured runs per edit kind, after one warm-up run that isn't counted |
| `-kinds body,props` | run only these edit kinds |
| `-ldflags -w` | the `go build -ldflags` value (`""` for full DWARF, `"-s -w"`) |
| `-buildflags "-p=8"` | extra `go build` flags, space-separated |
| `-fresh=false` | skip the fresh-checker comparison (it runs off the critical path) |
| `-json out.json` | also write every sample |
| `-keep -dir d` | build the project in `d` and keep it |
| `-dev` | measure `internal/dev`'s pipeline, which is what `orbit dev` runs (below) |
| `DEVLOOP_VERBOSE=1` | log every package `go build` compiled |

Each run uses a fresh random salt in every edit, so no stage can hit the
build cache from an earlier run. The header prints the load average at the
start and at the end. If it's well above 1, other work was competing and
the numbers are noisy.

## `-dev`: the real `orbit dev` pipeline

By default each step runs the prototype stages described below, which
predate `internal/dev`. With `-dev`, each step instead calls
`internal/dev.Pipeline.Run` with the paths the edit wrote, so it measures
what `orbit dev` runs after a debounced save: the change classifier, the
resident checker, esbuild skipped when no style or script changed, and,
on a `.go`-only edit, `go build` started before regenerating (kept when
regenerating writes nothing to `orbit_gen/`). The server swap and the
first request are the same as in the default mode.

In `-dev` mode:

- `go build` is the time after gen+write until the binary is ready. With
  an early build it's only the part that didn't overlap the stages before
  (the `early build` column says `used`, or `rebuilt after` when
  regenerating wrote files and `go build` ran again).
- `-ldflags`, `-buildflags` and `-fresh` don't apply: the pipeline uses
  `orbit dev`'s flags (`-ldflags=-s -w`, merged with any `-ldflags` in
  `GOFLAGS`; `-gcflags=all=-dwarf=false`, no inlining in `orbit_gen/`,
  `-buildvcs=false`; no `GOGC`/`GOMEMLIMIT`),
  and other go flags come from `GOFLAGS`, e.g.
  `GOFLAGS=-gcflags=-l ./devloop -dev`.
- `compiled` and `(link)` aren't measured (no action graph), and `noop` is
  an empty change: `orbit dev` never runs one, so it shows only the front.

## The project

There are 200 `.orbit` files by default. The generator is package `synth`, which `bench/lsp` uses too.

- **Pages**: 40. They include the index, `[slug]` and `[...rest]` routes,
  and pages that use either layout.
- **Layouts**: `Base`, and `Docs`, which wraps `Base`.
- **Components**: 158.
  - Each component renders the next two while its `depth` prop lasts, using
    default and named slots.
  - Every fourth component has a scoped `<style>` and a TypeScript
    `<script>` that imports `src/scripts/util.ts`.
  - Every 16th component renders `Badge`, so 10 components import it.

The Go side:

- **`internal/data`**: used by every `Props` (`[]data.Item`) and by most
  frontmatter.
- **`internal/format`**: used by frontmatter only.
- **`src/locals`**: read by templates.
- **`cmd/server`**: the user's server entry, with a mux and a locals
  middleware.

## What a step does

Each step is what a resident `orbit dev` would do. The column names below
are the ones the table prints.

| Column | What it measures |
|---|---|
| front | `project.Load` and `Front`: discovery, reading, parse, analyze, index, project checks, pass 1, for every file (there is no per-file API yet) |
| esbuild | the resident dev `assets.New(Options{Dev: true})` context: `Bundle`, then `assets.WriteDev` to `.orbit/dev` |
| check | the **resident** `compiler.Checker`: the first step calls `Check`, later steps call `Update` with the units whose pass-1 output changed (after `Invalidate` on a `.go` edit). Files it doesn't re-check keep their previous output. |
| gen+write | `GenFiles` and `WriteGenerated`, which only rewrites files whose content changed. The compiled manifest is left empty, because dev reads `.orbit/dev/manifest.json`. |
| go build | `go build -o <tmp> -ldflags=-w -debug-actiongraph=… ./cmd/server` |
| (link) | the link action's share of `go build`, from the action graph |
| stop old | SIGINT to the previous server, then wait for it to exit |
| start | exec the new binary (`ORBIT_DEV=1`, `ORBIT_STATIC`, `ORBIT_ADDR=127.0.0.1:<free port>`), then poll until it accepts a TCP connection |
| **binary ready** | front + esbuild + check + gen+write + go build |
| **serving** | binary ready + stop old + start |
| (1st GET /) | the first request after the start. The body edit checks that the new text is in it. |
| (check, fresh) | for comparison: a new `Checker` and a full `Check` (`go list -export` plus every unit), which is what `project.Generator.Run` does on every call. It isn't included in the totals. |

The table also has these counters, taken from the last run:

| Counter | What it counts |
|---|---|
| written | `orbit_gen` files rewritten |
| rechecked | units the resident checker re-checked |
| compiled | packages `go build` compiled rather than took from the cache |
| linked | whether `go build` relinked |

Every step also compares the resident checker's output with the fresh
check's. A mismatch aborts the run.

Not measured:

- the watcher's 50 ms debounce;
- the SSE reload event and the browser's reload;
- requests that wait for the swap.

### Edit kinds

| Kind | Edit |
|---|---|
| `noop` | a rebuild with nothing changed: the floor (it still relinks, since the output path is new) |
| `body` | template text in one component, which is rendered at `/` |
| `frontmatter` | a frontmatter statement in the same component |
| `props` | a new field on `Badge`'s `Props` (10 importers) |
| `newpage` | a new page using `Base` and a component |
| `gofile-body` | a `//go:noinline` function body in `internal/data` (export data unchanged) |
| `gofile-api` | a new exported const in `internal/data` (the worst case) |
| `css` / `ts` | `<style>` or `<script>` content: esbuild only, no Go rebuild (PLAN.md §7.1) |
