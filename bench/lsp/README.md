# lsp: orbit lsp startup, latency and memory

This program measures `orbit lsp` (PLAN.md §9) the way an editor sees it.
It builds `orbit` from this checkout, generates `bench/devloop`'s
synthetic project (200 `.orbit` files by default, see
[its README](../devloop/README.md#the-project)), and drives `orbit lsp`
over stdio as a client would. It measures:

- how long startup takes, up to the readiness barrier;
- the latency of completion, hover, definition and references (and
  rename, once the server offers it);
- the memory of `orbit lsp` and of its gopls child.

It is a separate module (`github.com/bertrandkhe/orbit/bench/lsp`), so it
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
| `-trace prefix` | run gopls with `-rpc.trace`, one log per session (`prefix.<mode>.<n>.log`), to see the traffic between Orbit and gopls |

The header prints the load average at the start and at the end. If it's
well above 1, other work was competing and the numbers are noisy.

## Modes

Both modes start with a warm Go build cache: the setup runs `orbit
generate` and `go build ./cmd/server` on the project first. gopls's own
cache (`~/.cache/gopls`) is left as it is. The project content is the same
on every run (a fixed salt), so after the first run that cache is warm too.

| Mode | Project on disk |
|---|---|
| `overlay` | no `orbit_gen/`: every generated file reaches gopls as an overlay, the case of a fresh clone |
| `disk` | `orbit_gen/` from `orbit generate`. Orbit sends overlays only for the generated files whose in-memory content differs from disk |

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
| barrier progress ended | Orbit's `$/progress` "Loading N generated files" ends |
| references answered | the `references` request held for the barrier is answered |
| server log rows | from `orbit lsp`'s own log: spawning gopls up to all overlays sent, and the barrier on its own |

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

### Memory rows

`VmRSS` of `orbit lsp` and of its gopls child (found through
`/proc/<pid>/task/*/children`), right after the barrier and again after
the request runs, plus each one's peak (`VmHWM`).
