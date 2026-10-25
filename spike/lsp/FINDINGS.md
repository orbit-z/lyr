# Spike B: the LSP proxy against real gopls (milestone 0)

This spike tests how real gopls behaves when Orbit's language server proxies to it with in-memory overlays of the generated Go (PLAN.md §9). It drives gopls directly, acting as the client `orbit lsp` would be. The coordinator reran the `minimal` session independently and got the same results.

## Setup
- **gopls**: v0.24.0 (2026-10-14), built with go1.27.3 into `spike/lsp/bin/gopls`. The old `~/go/bin/gopls` (v0.18.1, built with go1.23.4) was left untouched.
- **Project**: `spike/lsp/project` (`example.com/blog`, `go 1.27`, `toolchain go1.27.3`). It has `internal/posts` and reference-only `.orbit` sources, and **no `orbit_gen/` on disk**.
- **Stub runtime**: `spike/lsp/orbitstub`, `replace`d. `Ctx` holds `*http.Request`, so `net/http` is type-checked.
- **Codegen**: `harness/gen.go` emits the §3.1/§3.2 layout with inline `/*line*/` directives and resets. Sanity check with `go run ./harness -dump DIR`: then `go build` in `DIR/project` reports `./src/pages/index.orbit:6:10: undefined: hoem`.
- **Harness**: `harness/jsonrpc.go`, a stdio JSON-RPC client using `json.RawMessage`. Run it as `go run ./harness [-only main|utf16|old|timing|hoverdoc|scale|refs|refs2|refs3|minimal|shape] [-delay 50ms] [-verbose]`. Evidence goes to `results/*.md` and `results/*.log`.

All positions are 0-based LSP positions.

## Results

| # | Check | Result |
|---|---|---|
| 1 | Overlay-only packages, cross-package visibility | **PASS**. Caveat: overlays load lazily and serially (1b). |
| 2 | Completion (`props.`, `Card.Props{\|}`), response shape | **PASS** |
| 3 | Live `Props` change seen by another overlay package | **PASS** |
| 4 | Positions under line directives | gopls **ignores directives** and reports generated URIs with raw positions. Directive comments leak into docs (4c). |
| 5 | `positionEncodings: ["utf-8"]` | **FAIL**: always UTF-16 |
| 6 | Server→client requests | **PASS**: `null` answers are enough; an error answer is fatal |
| 7 | Rename | **PASS**, with caveats: always `documentChanges`; edits land in glue code |
| 8 | Timing | **PASS** |
| – | Old gopls v0.18.1 | False errors on Go ≥1.24 features |

### 1. Overlay-only packages: PASS
- Four overlays (two decl, two impl) type-check with 0 diagnostics, and nothing has to be on disk except the module.
- Cross-package overlays resolve: the page impl imports the Card decl overlay.
- **1b. Caveat**:
  - The initial workspace load doesn't see overlay-only directories. gopls loads each overlay with its own serial `go list file=…` (about 25–35 ms each).
  - Forward lookups work on demand.
  - **Reverse lookups (references, rename) silently miss importers** until everything has loaded (0–50 of 50 runs incomplete in `results/refs.md`). On-disk `orbit_gen` was always complete.
  - "All diagnostics published, plus 2 s of quiet" isn't a reliable readiness signal.
  - **A barrier that works**: one `textDocument/documentHighlight` per overlay, sent in parallel. 2.4–2.5 s for 102 overlays, after which references were complete 3/3. The same barrier takes 0.8 s with `orbit_gen` on disk.
- **1c. Side effects**:
  - A spurious `go.mod` warning: `"<module> is not used in this module"` (source `go mod tidy`) for modules imported only by overlays.
  - `window/showMessage` "Warning: editing render.go, a generated file." on the first `didChange` of each overlay.

### 2. Completion: PASS (1–3 ms)
- `{props.|}` in the impl gives `Featured, Post, Post.Slug, Post.Title`.
- `props.Fe|` gives `Featured`, with a replace range.
- A synthetic `_ = Card.Props{|}` in a scratch func gives 58 items: `Post` and `Featured` first (kind 5, sortText `00000`/`00001`), then deep completions and every in-scope identifier.
- Fields that are already set are excluded.
- A directive comment glued to the cursor doesn't disturb the parser.
- **Response shape**:
  - `isIncomplete: true`, and always `textEdit`, never `data`.
  - With `insertReplaceSupport`, the edit has `insert`/`replace` and a `labelDetails` block.
  - Inside composite literals the text is a snippet, `Field: ${1:}`.
  - Docs come inline in `documentation`. **`completionItem/resolve` isn't implemented** (-32601).

### 3. Live Props change: PASS
- Adding `Tags []string` to the Card decl overlay clears the page's `unknown field Tags` diagnostic. That takes **1.01 s** at the default `diagnosticsDelay`, and **54 ms** with `diagnosticsDelay: 50ms`.
- Completion sees the new field within 1–2 ms.

### 4. Line directives: ignored by gopls
| Error | `.orbit` | gopls (generated URI, raw position) |
|---|---|---|
| `title := hoem` | index.orbit:6:10 | render.go 22:55 |
| `{titel}` | 9:5 | render.go 27:46 |
| key `Psot` | 11:9 | render.go 33:50 |
| `posts.Postt` in Props | Card.orbit:4:20 | decl.go 10:19, plus a knock-on error in the impl |

- Diagnostics, hover, definition and references all use generated URIs and raw positions. Nothing is ever reported for a `.orbit` URI (gopls uses `PositionFor(pos, false)`).
- **4c. Doc leak**: inline `/*line*/` comments become a declaration's doc or line comment. Hover then shows `line decl.go:13:24` or `line ../../../../src/components/Card.orbit:3:1 Props are …`.
  - **Fix**: frame hoisted declarations with **own-line `//line`** directives, which `go/ast` strips from docs. `go build` still reports `./src/components/Card.orbit:5:20` correctly. Inline spans keep `/*line*/`.

### 5. Position encoding: FAIL
- gopls v0.24.0 never returns `positionEncoding`, whatever the client offers.
- Proof that positions are UTF-16: after `"Héllo ✓"`, hover's `character` is 25 (UTF-8 would be 28).

### 6. Server→client requests: PASS
| Request | Minimal answer |
|---|---|
| `workspace/configuration` (section `gopls`) | `[{}]` or `null` |
| `client/registerCapability` ×2: `didChangeWatchedFiles` (`**/*.{mod,work}`, `**/*.{go,mod,sum,work}`) and `didChangeConfiguration` | `null` |
| `window/workDoneProgress/create` | `null` |

- **An error answer is fatal**: gopls reports "no views" and every request fails.
- `workspace/applyEdit` and `showMessageRequest` never appeared.

### 7. Rename: PASS, with caveats
- `prepareRename` on a local works.
- Renaming `title` → `heading` gives 3 edits: the frontmatter span (mapped), the **glue `_ = title` line (unmapped)**, and the `{title}` span (mapped).
- Renaming a `Props` field gives 3 `TextDocumentEdit`s: the decl, the Card impl, and the page's field key. All carry overlay URIs and versions.
- gopls **always returns `documentChanges`**, even when the client says it doesn't support them.
- **Race**: a rename right after startup returned only 2 of the 3 files (see 1b).
- `prepareRename` also succeeds on glue identifiers such as `orbit__w`, so Orbit must gate it.

### 8. Timing
| Scenario | Time |
|---|---|
| 4 overlays, process start → first completion | 312 ms cold, 82–102 ms warm |
| 102 overlays, process start → first completion | 386 ms cold, 132 ms warm |
| 102 overlays, readiness barrier | 2.4–2.5 s overlay-only, 0.8 s on disk |
| `didChange` + completion | 1.6–3 ms |
| decl change → importer's completion sees it | 9–11 ms |
| decl change → importer's diagnostics | 1.0 s default, 54 ms with `diagnosticsDelay` 50ms |
| RSS, 50 components | 260 MB warm, 650 MB cold |

### Old gopls v0.18.1
- It works on plain code.
- It falsely rejects `new(expr)` (Go 1.26): `"Héllo ✓" is not a type`.
- It can be detected through `serverInfo.version` (`"GoVersion":"go1.23.4"`).

## Proposed PLAN.md changes
- **§9 position encoding**: gopls always speaks UTF-16. Convert at the gopls edge using the overlays' line index. Negotiate UTF-16 with the client by default (choice A). Re-check gopls's `positionEncoding` at startup in case a future version returns one.
- **§9 transport**: `data`, `labelDetails`, `documentation` and unknown fields pass through. **All ranges are rewritten**: `textEdit.range`, `insert`/`replace`, `additionalTextEdits`. There's no `data` and no resolve.
- **§9 startup**: add the per-overlay readiness barrier. Hold or answer "still loading" for references, rename and prepareRename until it completes. Completion, hover and diagnostics don't wait.
- **§9 acting as gopls's client**:
  - Never answer with an error; answer `workspace/configuration` with the client's `gopls` section, or `{}`.
  - Forward watchers and `didChangeConfiguration`.
  - Swallow the "editing a generated file" message.
  - Drop the overlay-only "not used in this module" go.mod warning.
  - Set `diagnosticsDelay: 50ms` unless the user overrides it.
- **§9 diagnostics**: gopls ignores directives everywhere. The mapper only translates `orbit_gen/**` locations; the directives exist for `go build`/`go vet`.
- **§9 rename**:
  - Convert `documentChanges` to `changes` for clients that lack support.
  - Handle the `_ = name` echo lines (choice C).
  - Gate `prepareRename` on Orbit's own Go-region routing.
  - Wait for the barrier.
- **§9 `Props{}` completion**: keep only kind 5 (Field) items with no `.` in the label. Use their inline docs, and replace the snippet with attribute insert text (`post={$1}`).
- **§9 startup version check**: warn when gopls was built with an older Go than the project's (choice E).
- **§3.3**: hoisted declarations use own-line `//line`; inline spans keep `/*line*/`.
- **§11 LSP tests**: no `line …` text in hover; references and rename complete after the barrier at ≥50 components in overlay-only mode.

## Choices for the user
- **A. Client position encoding**: (1) always UTF-16 *(recommended)*; (2) offer UTF-8 where the client supports it.
- **B. Startup when `orbit_gen` is missing**:
  - (1) overlay-only plus the readiness barrier *(recommended)*;
  - (2) (1) plus a nudge or action to run `orbit generate`;
  - (3) the LSP writes `orbit_gen` when it's missing (reverses a §9 rule).
- **C. Echo lines in rename**:
  - (1) a source-map kind `echo`, with edits there dropped *(recommended)*;
  - (2) map echo lines back to the declaration and deduplicate;
  - (3) a different unused-variable suppression.
- **D. Doc leak**: (1) own-line `//line` for hoisted declarations *(recommended)*; (2) strip the text from hover.
- **E. Old gopls**: warn *(suggested)* or refuse.
