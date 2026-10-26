# Orbit

**Goal: Astro's developer experience, with much faster SSR.** Every design choice is weighed against both halves:
- does it feel like Astro?
- does it keep the SSR hot path fast? (Few allocations, no reflection, precomputed static HTML, benchmarked.)
- does it keep **compile and dev-loop time short**? Go was chosen over Rust for fast compiles. A save in `orbit dev` should show up in the browser in about 1 s, and `orbit build` should stay quick. Any feature that adds noticeable build or regenerate time (type-checking passes, heavy codegen, big dependencies) must be measured and discussed with the user first.

Orbit is an Astro-like web framework with Go on the server. You write `.orbit` single-file components (Go frontmatter plus an HTML template), and the `orbit` CLI compiles them to Go. **SSR only**: every page renders per request. There's no static-site generation; for static sites, Astro is the better choice. Client scripts are TypeScript, bundled with esbuild's Go API. Orbit plugs into a server as a `net/http` middleware. `orbit lsp` provides editor support by proxying to gopls.

- **Design and spec**: `PLAN.md`, the source of truth for architecture and milestones.
- **User docs**: `docs/`. These are written first (milestone 1, right after a throwaway spike, milestone 0) and act as the user-facing spec. When behavior changes, update the docs in the same change.

## How to work in this repo: coordinator model

The main session is the **coordinator**. It plans, delegates, reviews and integrates. It does **not** write large amounts of code itself.

### Delegate (with the Agent tool)
Delegate anything non-trivial:
- implementing a feature or milestone step
- writing a docs page
- writing a test suite
- multi-file refactors
- broad codebase searches
- debugging that needs investigation

**Effort**: run delegated agents at **medium** effort (`effort: "medium"`), per the user, unless they say otherwise for a specific task.

Pick the agent type:
- `general-purpose`: implementation, docs writing, debugging.
- `Explore`: read-only searches across many files.
- `Plan`: design questions and reviews of a proposed approach.

Every delegation prompt must be self-contained, because subagents don't see this conversation. It includes:
1. **Goal and why**: the milestone and the relevant `PLAN.md` sections, by section number.
2. **Scope**: the exact files/packages to create or touch, and what's explicitly out of scope.
3. **Interfaces to honor**: signatures and types from `PLAN.md` §4/§5b, plus any existing code they must integrate with.
4. **Acceptance criteria**: which tests must exist and pass (`go test ./...`, `go vet ./...`), and any golden files.
5. **Report back**: a short summary of what changed, any deviations from the plan and why, and open questions.

When independent tasks can run in parallel, launch them in **one message**. If parallel agents edit code that might overlap, use `isolation: "worktree"`. Never give two concurrent agents the same files.

### Do it yourself (small tasks)
- Edits to one file, roughly 50 lines or fewer
- Config tweaks, renames, fixing a failing assertion
- Reading a known file, a single grep
- Running tests, builds and `go vet`
- Reviewing agent output and integrating it
- Updating `PLAN.md` or this file

### Asking the user
- **Ask, don't assume**: whenever something is unclear or there are several reasonable options (design, API shape, naming, scope, trade-offs), ask the user with AskUserQuestion. Put the recommended option first, with short trade-offs. Don't silently pick, and don't let subagents silently pick either: tell them to report the choices they faced instead of settling them.
- **Triage small details; don't ask about every one.** Decide minor details (exact error wording, edge-case HTTP behavior, defaults with an obvious answer) by these rules, in order:
  1. Follow Astro's behavior if it has one.
  2. Otherwise follow Go standard-library conventions (`net/http`, `encoding/json`, `go vet`-style errors).
  3. Otherwise pick the simplest option.

  Log each one in `docs/DECISIONS.md` (decision, rule applied, alternatives) so the user can skim and override. Ask the user only about choices that shape the public API or visible behavior, or that trade off the project goals.
- **Batch questions** (up to 4 per prompt) at natural checkpoints: after a review, before a milestone, or when a subagent reports open choices.
- **Propose additions**: at each checkpoint, suggest concrete additions or changes worth considering (features, simplifications, risks), and ask whether the user wants to add anything.

### After every delegated task
- **Verify, don't trust**: read the diff, then run `go build ./... && go vet ./... && go test ./...`, plus the milestone's checks from `PLAN.md` §13.
- If it deviates from the plan, either fix it (small) or send it back to the same agent with concrete feedback (SendMessage).
- Keep `PLAN.md` accurate when a design decision changes. Ask the user before changing anything they explicitly decided (listed below).
- Give the user short progress updates per milestone.

## User decisions (don't change without asking)
- **Files**: custom `.orbit` format: Go frontmatter between `---` lines, then the template. Components are imported with `Alias "./X.orbit"`.
- **Rendering**: **SSR only**. No `Prerender`, no `StaticPaths`, no static output. The only static files are `/_orbit/*` assets and `public/`. SSR caching comes after the MVP.
- **Client JS**: vanilla `<script>` only, TypeScript by default. **No framework islands, ever** (importing `.tsx`/`.svelte`/… is an error).
- **Lazy loading (MVP)**:
  - `server:defer` deferred server rendering (Astro server islands): props encrypted, a fallback slot, served through the user's middleware chain.
  - `client:load|idle|visible|media` for lazy component scripts. A directive covers the **whole subtree** (eager uses elsewhere win). The loader is tested with `bun test` + happy-dom.
  - **Island props** are encrypted with stdlib synthetic-nonce AES-256-GCM: the nonce is an HMAC-SHA256 of the input, and both keys come from `ORBIT_KEY` via HKDF. Deterministic, tamper-proof, 3–4.5× faster than pure-Go GCM-SIV, no vendored code, FIPS-approved primitives.
  - **Island URLs are cache-ready in the MVP**: a deterministic nonce (same props give the same URL), the build ID in the URL, and GET up to ~8 KB of props. Default `Cache-Control: private, no-store`.
  - **Cacheable islands come after the MVP**: ETag/304, public-caching guidance, the cache benchmark. HTTP caching only, with headers set by the component and docs-only safety.
- **Locals helper**: `r, l := orbit.EnsureLocals[L](r)` in middleware.
- **Dev server**: no HMR; full-page live reload.
- **HTTP**: stdlib `net/http` only, no web framework. Orbit is a middleware (`site.Middleware(next)`), and the user's `cmd/server/main.go` decides where it sits in the chain and its precedence.
- **Locals**: typed, via a user-declared `type Locals struct` (location per PLAN.md §5b).
- **Components**: recursive and self components (`<Self>`) and mutual recursion are supported.
- **Generated packages**: one decl + one impl package per component (PLAN §3.1), reconfirmed by the user after the speed work (not one shared package, not per directory): small rebuilds and verbatim user code outweigh the ~40 ms of per-package build overhead at 200 files.
- **Editor support**: an LSP is required (gopls proxy plus native template completion), with a VS Code extension.
- **Order**: milestone 0 is a throwaway spike that proves the risky mechanisms (DONE). Docs come next (milestone 1), and the user reviews them before implementation starts.
- **Compile speed**: fast compiles and a ~1 s dev loop are a core goal (the reason for Go over Rust).
- **Performance**:
  - **Pay at build time, not at runtime** (user principle, 2026-10-26): moving cost from each request to `orbit build` is good as long as the build impact is small and `orbit dev` gets no slower.
  - SSR responses are **always buffered**, using pooled buffers; no streaming (reconfirmed after M12: the user deploys behind a CDN and doesn't want streaming).
  - Codegen uses **go/types** to emit typed writes (`w.String`/`w.Int`/…) and falls back to `w.Expr(any)`.
  - **Benchmarks**:
    - Head-to-head vs Astro (`@astrojs/node` standalone, single process) on the same site.
    - Scenarios: a render-heavy page and a page with a simulated 10 ms data fetch. Orbit runs at `GOMAXPROCS=1` and at all cores.
    - Metrics: req/s, p50/p99 latency, RSS.
    - Also compared with templ and html/template (measure only; parity with templ isn't a gate).
  - **Slots**: a value struct `orbit.Slots{Default, Named []NamedSlot}`, not a map. No allocation without children. Slot content that captures parent variables costs 1 alloc per slot today. **Revised by the user (2026-10-26)**: remove it with **direct calls in `orbit build` only** (impl → impl calls, so escape analysis keeps slot closures on the stack; `orbit dev` keeps the hook calls so a save stays fast; components in an import cycle keep the hook). PLAN §3.5 (implemented; every recommended answer, except that the hook flavor stays byte-identical to before and only the build flavor exports `Orbit__RenderImpl`, because exporting it in dev slowed `orbit dev` by ~16 ms (the dev-speed rule), and `.md` callees keep the hook): build flavor via `go build -overlay` from `.orbit/build/` (`orbit_gen/` keeps the hook flavor), `<Self>` a direct self-call, `go test -overlay` to measure it, no depth cap; measured −10% render and 0 allocs on the 500-card page, +8–17% on a full `orbit build` of a typical 200-file project. Measured: 500 capturing slots, hook 13.0 µs / 500 allocs / 24 KB vs direct 7.6 µs / 0.
  - **Zero allocations in Orbit's own render path** for a typical page, asserted by `AllocsPerRun` tests (serving adds exactly 1: the `Content-Length` value). `net/http`'s own ~14–19 allocs/request is the floor.
    - No fixed target number: we measure and publish the results.
  - SSR caching is not in the MVP.
  - **Milestone 9 checkpoint**: URL attribute values are written as pieces through a public `Writer.URL(s)`; `locals` is declared only in components that reference it (truly lazy); the dev-loop worst case (a Go package every `Props` imports, ~1.5 s on 4 cores) is accepted and documented, with M10 mitigations.
- **MVP additions**:
  - `orbit doctor` (environment and project checks).
  - The `orbittest` component test helpers.
  - A Dockerfile scaffolded by `orbit new` (distroless, `--embed`).
  - LSP rename; single-binary deploy (`orbit build --embed`); TypeScript type checking in `orbit check` via **`bun check`** (Bun ≥ 1.4.4's built-in checker; no `typescript` package needed).
- **Docs-pass decisions**:
  - `orbit new <dir> [--module path]`.
  - Mixed route segments (`post-[id]`) and non-trailing rest params are an error in the MVP.
  - Production 500: a built-in page plus a stderr log with the `.orbit` position (frontmatter `return x` is wrapped at codegen with `orbit.ErrorAt`); `500.orbit` comes after the MVP.
  - The minor ones are logged in `docs/DECISIONS.md`.
- **Typed route params**:
  - Codegen generates `type Params struct{…}` per page from its route, and pages get `params` (e.g. `params.Slug`).
  - Components use `ctx.Param("slug")`. There's no `ctx.Params` map.
  - Router matches allocate nothing.
- **Whitespace**: `orbit.json` `compressHTML`: `"jsx"` (**default since 2026-10-26, implemented**, user decision reversing the earlier `"html"` default: Astro v7's default, so ported Astro sites render the same; `{" "}` keeps a space across lines), `"html"` (HTML-aware lossless; `true` is accepted as Astro's spelling of it) or `false`. Applied at codegen time.
- **Unused component import**: an Orbit error.
- **From the `nyth` assessment** (user decisions 2026-10-26; specs first): an **error → response hook** (the app maps its own errors to a redirect/403/503 instead of the built-in 500), **pass-through attributes** (a component collects undeclared HTML attributes in an `orbit.Attrs` prop and spreads them), a **slot-to-string helper**, and **esbuild code splitting moved up** (next, for lazy `import()` chunks). Also an **opt-in concurrent-render directive** for slow components (user decision: spec now; automatic concurrency rejected by measurement: ~170 ns + 2 allocs per sibling, 10× a small component; Astro 7 buffers siblings after an async child).
- **Tailwind** (user decision 2026-10-26, from the `nyth` port): built-in support through Tailwind's **standalone CLI** as an external, optional tool (no Node), detected from `@import "tailwindcss"`; spec PLAN §5d (accepted): local CLI then `bun x`; `<style>` blocks with `@apply`/`@reference` processed; Orbit rewrites `url()`s of CSS imported by a Tailwind stylesheet (user decision, like `@tailwindcss/vite`); one-shot runs per change; VS Code sets `tailwindCSS.includeLanguages`; no Tailwind in `orbit new`.
- **`class:list`** (implemented 2026-10-26, from the `nyth` port): `class:list={"a", expr, "c": cond}`, Go composite-literal elements, static parts joined at build time, zero allocations, merged with `class` (PLAN §2.3).
- **Bundler**: esbuild via its Go API (in-process, no runtime requirement), behind one internal interface. Bun stays optional (`bun check`, later `bun fmt`) and isn't used for bundling.
- **Formatter (`orbit fmt`)**: comes after the MVP. Go parts are formatted by Orbit; script/style blocks go through `bun fmt --stdin-filepath`. Spec in PLAN §6.1 (fmt checkpoint): never changes rendered output; gofmt tabs in the frontmatter; `go fmt`-style CLI (in place over `src/`, `-l`/`-d` exit 1 when unformatted); first release = frontmatter + expressions/control headers + script/style; Bun formatting on when Bun ≥ 1.4.4 is found, no switch; `.md` never touched.
- **Other settled choices**:
  - `orbit_gen/` is gitignored.
  - URL sanitizing blocklists `javascript:`/`vbscript:`.
  - The LSP looks for gopls on PATH first; pinning is optional.
  - Benchmarks use separate `bench/orbit-site` and `bench/astro-site`.
- **Spike decisions** (milestone 0, see `spike/*/FINDINGS.md`):
  - **Error messages**: Orbit pre-checks unknown props with a friendly message. Type names in errors are cleaned up both ways: a distinct impl package name, plus the CLI rewriting names to the user's alias.
  - **Generated code**:
    - Users may shadow `ctx` and friends; internals are named `orbit__*`.
    - A frontmatter `func init` is rejected.
    - Relative line directives; own-line `//line` around hoisted declarations.
  - **Typed writes**: on in dev too (hot, incremental). Engine: go/types + `go list -export` (no go/packages, no type cache). float32 prints as `0.1`. Dev builds use `-ldflags=-s -w`, `-buildvcs=false` and `-gcflags=all=-dwarf=false` (user decision after the `go build` investigation, replacing `-ldflags=-w`; PLAN §12 "Dev-loop speed work").
  - **LSP**:
    - UTF-16 positions to the editor.
    - In-memory start plus a readiness barrier. The "orbit generate" nudge was removed at the M12 checkpoint (user decision: startup is ~50 ms slower without `orbit_gen/`).
    - Rename drops `echo` edits.
    - An **old gopls is refused** for Go features.
  - `orbit new` always writes the `toolchain` line.
- **Milestone 11 checkpoint**: `orbit doctor` reports a missing gopls as ✗ (kept); `orbit new`'s next steps and `getting-started.md` use the global `orbit dev` (it execs the pinned CLI, so Ctrl-C exits 0; `go tool orbit dev` exits 1); docs error examples become `expect-error` blocks; M12 runs in 3 stages (transport/proxy, completion, rename) plus an LSP latency benchmark.
- **Milestone 12 checkpoint**: the "Run orbit generate" nudge is removed; M13 (VS Code extension) packages a `.vsix` **and** sets up publishing (a script for the Marketplace and Open VSX that the user runs with their own tokens; nothing is published by Claude); additions: commands (Restart Language Server, Run orbit generate, Run orbit doctor) and grammar snapshot tests (`vscode-tmgrammar-test`); `editors/vscode` uses **Bun** as its package manager.
- **Dogfood checkpoint** (docs site, after M13): scoped CSS uses **attribute selectors** like Astro's default (`a[data-o-xxxx]`, +1 specificity) instead of `:where()`; Markdown code blocks are highlighted with **Chroma at generate time** (via goldmark-highlighting, CLI dependency; zero runtime cost); `orbit.json` gets a **`"watch"`** option (globs whose changes reload `orbit dev`).
- **Markdown** (implemented; spec in PLAN §2.4 and `docs/markdown.md`; checkpoint: `--` becomes an em dash like Astro, the ~5.7 ms CLI startup cost of Chroma is accepted, LSP rename of a layout field set by `.md` frontmatter stays refused; then added: an `orbit` Chroma lexer, `.md` diagnostics in the LSP, import-path completion for `.orbit` and `.md` (full relative paths, user decision); typed frontmatter errors refresh while typing (debounced, user decision); the VS Code extension activates on `workspaceContains:**/*.orbit`): YAML frontmatter via `go.yaml.in/yaml/v3` (CLI dependency); keys set the layout's typed `Props` (unknown key or wrong type = error); imports expose `<Post />`, `Post.Frontmatter`, `Post.Headings`, `Post.URL`; relative `.md` links rewritten to page URLs; Chroma classes + generated `light-dark()` CSS, with Orbit's own renderer over Chroma (no goldmark-highlighting).
- **URLs** have no trailing slash by default (`/blog/hello`; `/blog/hello/` gets a 308 to `/blog/hello`). Accepted next (after direct calls): an `orbit.json` `trailingSlash` option, `"never"` (default) or `"always"` (the mirror image), no `"ignore"` (PLAN §12).
- **Middleware**: no `OnRequest`. There's one middleware model: plain net/http middleware plus typed locals (`WithLocals`/`LocalsFromRequest`). Orbit ships no auth.
- **Server→client data**:
  - MVP: `data-*` attributes plus `set:json` JSON blocks.
  - After the MVP: a **typed bridge**, spec in PLAN §5c (accepted, every recommended answer): `orbit.JSON(v)` on any attribute (typed on `data-*`), several per element; scripts `import { data } from "./X.orbit"` for per-component typed `data(el, key)` and `json(id)` for `set:json`; single-quoted values; nil slices/maps typed `| null`; 64-bit ints are `number` (docs note); types in `orbit_gen/types/`; no `bun check` in `orbit dev`. TS editor support in `<script>` is a separate item, next after the bridge: PLAN §9b (implemented): TypeScript 7 native (`tsc --lsp`, no Node) as a second LSP child, found locally then via `bun x`; in-memory virtual documents under `.orbit/lsp/` with `orbit check`'s tsconfig; full feature set; fresh in-memory `.d.ts` while typing; `is:inline` scripts included as JavaScript (user decision); all scripts opened on the first references/rename.
- **Go**: latest stable, currently **1.27.3** (`go 1.27`, `toolchain go1.27.3`).
- **Name**: Orbit. Repo **https://github.com/orbit-z/orbit**, module **`github.com/orbit-z/orbit`** (renamed from `github.com/bertrandkhe/orbit` after M13, user decision). VS Code extension publisher **`orbit-z`** (ID `orbit-z.orbit`).
- **License**: MIT (`LICENSE` at the repo root; milestone 12 checkpoint).
- **After the MVP** (M13 checkpoint): dogfood on a real site first; then `orbit fmt`, the typed server→client bridge (spec first, user decision), cacheable islands. Also on the list (added after fmt, not ordered): `{switch}` blocks (implemented: keyword `switch`, not `match`; Go switch semantics, PLAN §2.3), a custom `500.orbit`, esbuild code splitting. Markdown: `.md` pages and `.md` imports, compiled at generate time with goldmark (a CLI dependency; runtime stays stdlib-only), designed after the dogfood report.

## Conventions
- **Go**: 1.27. Code must be `gofmt`-clean and pass `go vet`. Use only the standard library plus the dependencies listed in `PLAN.md` §10; ask before adding a new one. Pin versions at least two weeks old.
- **Generated code** (`orbit_gen/` in user projects): starts with `// Code generated by orbit. DO NOT EDIT.`. It's never gofmt'ed, because that would break source maps.
- **Tests**:
  - Table-driven where it fits.
  - Compiler golden files live in `internal/compiler/testdata/`; regenerate them with `go test ./internal/compiler -update`.
  - The parser has a fuzz test (`FuzzParse`).
  - Integration tests build `examples/blog`.
- **Errors**: always include `file:line:col` pointing into the `.orbit` source.
- **Commits**: only when the user asks. If on the default branch, branch first.

## Commands
```sh
go build ./... && go vet ./... && go test ./...      # full check
go test ./internal/compiler -update                  # refresh golden files
go test -fuzz=FuzzParse -fuzztime=30s ./internal/compiler
cd examples/blog && go run ../../cmd/orbit build     # build the example site
cd examples/blog && go run ../../cmd/orbit dev       # dev server on :4321
```

## Layout (target)
```
orbit.go ctx.go writer.go router.go ...   runtime, package orbit (repo root)
cmd/orbit/                                CLI
internal/{config,compiler,project,assets,build,dev,lsp,scaffold}/
editors/vscode/                           VS Code extension
examples/blog/                            example site (replace => ../..)
docs/                                     user documentation (spec)
```
