# lsp: orbit lsp startup, latency and memory

This program measures `orbit lsp` (PLAN.md §9) the way an editor sees it.
It builds `orbit` from this checkout, generates `bench/devloop`'s
synthetic project (200 `.orbit` files by default, see
[its README](../devloop/README.md#the-project)), and drives `orbit lsp`
over stdio as a client would. It measures:

- how long startup takes, up to the readiness barrier;
- the latency of completion, hover, definition and references (and
  rename, once the server offers it);
- the memory of `orbit lsp` and of its gopls child;
- the TypeScript child that serves `<script>` blocks (PLAN.md §9b): its
  first answer, edit → diagnostics, completion, hover and references
  (the first of which opens every script's document), and its memory.

It is a separate module (`github.com/orbit-z/orbit/bench/lsp`), so it
adds nothing to the root module. The project generator lives in
`bench/devloop/synth`, which both benchmarks import.

## Run

```sh
cd bench/lsp
go build -o /tmp/lsp-bench .

/tmp/lsp-bench                   # 200 files, 3 starts and 20 runs per mode (~1 min)
QUICK=1 /tmp/lsp-bench           # smoke test: 50 files, 1 start, 3 runs (~15 s)
```

You need gopls on `PATH` (v0.24.0 or later: `go install
golang.org/x/tools/gopls@latest`), and Linux, because memory is read from
`/proc`.

| Flag | Meaning |
|---|---|
| `-files 200` | `.orbit` files in the synthetic project |
| `-starts 3` | `orbit lsp` processes started per mode, for the startup timings |
| `-runs 20` | measured runs per request kind, after one warm-up run that isn't counted |
| `-modes overlay,disk` | run only these modes |
| `-json out.json` | also write every sample (durations in nanoseconds) |
| `-keep -dir d` | create the project in `d` and keep it |
| `-v` | print `orbit lsp`'s log (and gopls's stderr) after each session |
| `-typescript path` | the TypeScript 7 engine, passed as the `typescriptPath` option (default: `$ORBIT_BENCH_TYPESCRIPT`, else orbit lsp's own lookup, which may use `bun x`) |
| `-trace prefix` | run gopls with `-rpc.trace`, one log per session (`prefix.<mode>.<n>.log`), to see the traffic between Orbit and gopls |

The header prints the load average at the start and at the end. If it's
well above 1, other work was competing and the numbers are noisy.

## Modes

Both modes start with a warm Go build cache: the setup runs `orbit
generate` and `go build ./cmd/server` on the project first. gopls's own
cache (`~/.cache/gopls`) is left as it is. The project content is the same
on every run (a fixed salt), but gopls's cache keys include the project's
path, and each run uses a new temporary directory (unless `-dir` is given):
the first start of a run sees a cold gopls cache for this project, the
later ones a warm one. The memory rows come from the last start. Before
the fixes described in the notes below, that made gopls's memory depend on
the number of starts (0.5 GB on a cold cache, 2–3 GB on a warm one).

| Mode | Project on disk |
|---|---|
| `overlay` | no `orbit_gen/`: every generated file reaches gopls as an overlay, the case of a fresh clone |
| `disk` | `orbit_gen/` from `orbit generate`. Orbit's startup typed check finds the generated files whose typed output (what `orbit generate` writes) is on disk; gopls reads those from disk, and only the others are overlays: here, the edited `index.orbit`'s two files |

## What a session does

1. Start `orbit lsp` in the project and send `initialize` with the usual
   client capabilities (UTF-16 positions, snippets, insert/replace edits,
   markdown, `workDoneProgress`, configuration, dynamic watcher
   registration, `documentChanges`). It answers the server's requests like
   a minimal editor: an empty section per `workspace/configuration` item,
   `null` for everything else.
2. Open two files: the probe component (`C52` at 200 files, which renders
   `C53` and `C54`) and `index.orbit`, whose opened text has an undefined
   name (`{nope}`) and an unknown prop (`bogus="x"`) that aren't on disk.
3. At once, as an editor does after opening a file, send a hover, a
   completion (after `props.` in `{props.Count}`) and a `references` on
   the component's `Title` field. Orbit holds `references` until the
   readiness barrier is done.
4. In the last start of each mode, run the latency measurements and read
   memory.

### Startup rows

Every time is measured from process start, so the rows are cumulative.

| Row | Event |
|---|---|
| `initialize` answered | Orbit answers at once; gopls starts in the background |
| Orbit's diagnostic | Orbit's own `unknown prop "bogus"` on `index.orbit` is published |
| first hover / first completion | answered by gopls through Orbit |
| gopls's diagnostic | gopls's `undefined: nope` on `index.orbit` is published |
| barrier progress ended | Orbit's `$/progress` "Loading N generated files" ends ("not seen" when there's nothing to load: no overlay-only files, as in `disk` mode) |
| references answered | the `references` request held for the barrier is answered |
| server log rows | from `orbit lsp`'s own log: spawning gopls up to all overlays sent (in `disk` mode this includes waiting for the typed check), and the barrier on its own ("not seen" when it took 0 s) |

### Latency rows

Each request is measured from send to response, after the barrier, one at
a time. A completion row also includes the edit: each run sends a
`didChange` that inserts a new probe line (`<i>salt</i><p>{props.}</p>`,
`<p>{up}</p>`, or `<C53 />`) and then asks for completion at the probe,
timing both together. The warm-up run checks that the expected item is
there (`Title`, `upper`, `title`), and a missing one is printed as a
warning.

| Row | Request |
|---|---|
| Go selector | completion after `{props.` (gopls) |
| bare identifier | completion after `{up` (gopls, unimported-package and deep completion included) |
| component props | completion in `<C53 \|` (Orbit's synthetic `Props{}` completion through gopls) |
| hover | on `{upper}` in the template |
| definition | on `label(it)`, a frontmatter func |
| references, local variable | `upper` |
| references, `Props` field | `Title`, used by the component and by the attributes of the two components that render it |
| prepareRename, rename | on `upper`, only if `initialize` advertises `renameProvider`; otherwise the rows say so |

### TypeScript rows

The probe component has a `<script>` (every fourth component does), so
opening it starts the TypeScript child. With no TypeScript 7 found, the
rows are empty and the Go rows run without the child.

| Row | Request |
|---|---|
| first `<script>` completion (startup) | a completion after `document.` in the script, sent with the startup requests: the child's lazy start plus its first answer |
| `<script>`: `didChange` + completion | each run inserts `document.` on a new line of the script and completes after it |
| `<script>`: `didChange` → diagnostic | each run inserts a call to an undefined name and waits for its TypeScript diagnostic on the `.orbit` file (includes Orbit's 50 ms debounce of the diagnostic pull) |
| `<script>`: hover | on `twice` |
| `<script>`: first references | on `twice`: the first one opens every `.orbit` file's script documents (one sample) |
| `<script>`: references | the same, afterwards |

### Memory rows

`VmRSS` of `orbit lsp`, of its gopls child and of its TypeScript child (`tsc`) (found through
`/proc/<pid>/task/*/children`), right after the barrier and again after
the request runs, plus each one's peak (`VmHWM`).

## Notes

What this benchmark found, and what changed (milestone 12):

- **Every `render.go` was an overlay, even with `orbit_gen/` on disk.**
  The language server generates tolerant code (untyped `Expr`/`Attr`
  writes) while `orbit generate` writes typed writes, so a byte comparison
  never matched. The startup typed check (`internal/lsp/disk.go`, ~70 ms
  at 200 files with a warm build cache) now finds the files that are on
  disk, and gopls reads them there.
- **gopls's memory was a per-overlay cost.** gopls keeps an open file's
  type-checked package together with the imports of the request that
  checked it. The old readiness barrier sent one `documentHighlight` per
  overlay; each one type-checked its package in its own batch and kept its
  own copy of the imported packages (`gcimporter.IImportShallow` was 80% of
  the heap): about 7 MB per overlay, 3 GB at 400 overlays. With a cold
  gopls cache the type checks were most likely slow enough to overlap and share their
  imports, hence the 0.5 GB. The barrier now asks gopls which packages it
  has loaded (`gopls.packages`) and touches only the missing ones.
- **The barrier took ~25 ms per overlay-only file**, one lazy `go list`
  each, serially (9.7 s at 400). With 16 or more of them, Orbit now
  re-adds gopls's workspace folder after sending the overlays, so a single
  workspace load sees them all.
- **That reload still held the first hover for 1.4–3.8 s**, because gopls
  handles notifications in order and the folder change came after 400
  `didOpen`s that were each slower than the last: with the project's
  folder open, gopls's zero-config view selection walks the directories
  of every open file on each `didOpen` (`selectViewDefs` →
  `findRootPattern`, ~70% of the time, all `stat` calls), and each
  `didOpen` also clones the snapshot and starts a lazy load of the file's
  package (writing every overlay to a temporary file for `go list`). A
  CPU profile (`gopls -profile.cpu=…`) showed 1.56 s in `DidOpen`. Being
  syscall-bound, the total varied with the machine and the project's path
  (1.4 s in `/tmp`, 1.85 s in a deeper directory, 3.8 s in one quiet run
  that couldn't be reproduced). gopls now starts on an empty placeholder
  folder, so the `didOpen`s cost almost nothing (~40 ms for 400), and gets
  the project's folder once they're all sent; Orbit also sets
  `zeroConfig: false`, which helps when many overlays are opened with the
  project's folder there (200 stale files: 0.70 → 0.62 s, 400 → 240 MB).
  First hover overlay-only: 1.4–3.8 s → ~0.33 s (one cold start of five
  took 2.0 s), gopls 340 → 180 MB after the barrier.

To see gopls's memory yourself, put a `gopls` script first on `PATH` that
runs `gopls -debug=localhost:6061 "$@"`, then fetch
`http://localhost:6061/debug/pprof/heap` (or `/memory`) during a session.
