# Decisions log

This page lists the small decisions behind the docs, so you can skim them and override any of them. Big design choices (SSR only, no framework islands, buffered responses, typed locals, and so on) are in `PLAN.md` and `CLAUDE.md`, not here.

## How these were decided

Minor details (exact error wording, edge-case HTTP behavior, defaults with an obvious answer) are settled by the **triage rule**, in order:

1. **Astro**: follow Astro's behavior, if it has one.
2. **Go stdlib**: otherwise, follow Go standard-library conventions (`net/http`, `encoding/json`, `flag`, `go vet`-style errors).
3. **Simplest**: otherwise, pick the simplest option.

The **Rule** column says which step applied. Two other values appear:

- **user**: you decided it when asked.
- **coordinator**: the rule didn't settle it on its own (it left several reasonable options), so the coordinator picked one, noted in parentheses with the reasoning.

To override a decision, say which row and the alternative you want; the docs and `PLAN.md` change together.

## Project and CLI

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Before Orbit's first release, `orbit new` adds a `replace` to your local checkout: from `ORBIT_REPLACE=/path/to/orbit`, or found automatically when the CLI was built from a git clone (not a tagged release) | coordinator (simplest for trying Orbit from the repo) | `ORBIT_REPLACE` only | [Getting started](getting-started.md#trying-orbit-before-its-first-release) |
| With a replace, the requirement is `github.com/orbit-z/orbit v0.0.0` | simplest | A zero pseudo-version | — |
| The `toolchain` line is the CLI's `runtime.Version()` when it's a release (`go1.N.P`, rc, beta), else the framework's own `go1.27.3` | simplest | Always the framework's | [CLI](reference/cli.md#orbit-new) |
| If generating or `go mod tidy` fails, `orbit new` names the failed step and leaves the directory | simplest | Delete the directory | [Getting started](getting-started.md) |
| A malformed `--module` path is exit 1, before anything is written | Go stdlib (`go mod init`) | Exit 2 (usage) | [CLI](reference/cli.md#orbit-new) |
| `orbit new` accepts flags before or after `<dir>` | simplest (the docs show `orbit new blog --module …`) | Flags before `<dir>` only | [CLI](reference/cli.md#orbit-new) |
| `orbit new` runs its own `go` commands with `GOWORK=off`, so a `go.work` in a parent directory doesn't break it | simplest | Respect `go.work` | — |
| `orbit new`'s next steps say `cd <dir>`, then `go tool orbit dev` (they said `go tool orbit build && go tool orbit preview` until milestone 10) | simplest (only print commands that work) | Also print `go tool orbit build` | [Getting started](getting-started.md) |
| `orbit new <dir> [--module path]`; the module path defaults to the directory name | user | Always ask for a module path; default to `example.com/<dir>` | [Getting started](getting-started.md#create-a-project), [CLI](reference/cli.md#orbit-new) |
| `orbit new` refuses a `<dir>` that exists and isn't empty | simplest | Scaffold into a non-empty directory, skipping or overwriting files | [CLI](reference/cli.md#orbit-new) |
| The starter has `public/favicon.svg`, and no `Card` component, `orbit.json` or `package.json` | simplest | A richer starter with a sample component and config | [Getting started](getting-started.md#a-tour-of-the-starter) |
| Outside a project, `orbit doctor` runs only the environment checks (Go, gopls, Bun) | simplest | Fail with "not an Orbit project" | [Getting started](getting-started.md#install-orbit-and-run-orbit-doctor) |
| Exit codes for every command: 0 success (warnings included), 1 failure, 2 usage error; `orbit preview` passes on the server's code | Go stdlib (`flag`) | 1 for every failure, usage errors included | [CLI](reference/cli.md#exit-codes) |
| `orbit doctor --json` prints `{"checks":[{"name","status","message","fix"}]}`, with `status` `ok`, `warning` or `error` | simplest | One JSON object per line; a map keyed by check name | [CLI](reference/cli.md#orbit-doctor) |
| `orbit preview` has no flags; it runs `dist/server` with your environment, so `PORT=3000 orbit preview` picks the port | simplest | A `--port` flag that sets `PORT` | [CLI](reference/cli.md#orbit-preview) |
| `orbit build` has no Go build flags of its own (only `--embed`); Go flags come from `GOFLAGS` | Go stdlib | `--tags`, `--ldflags`, … flags passed to `go build` | [CLI](reference/cli.md#orbit-build) |
| `GOOS` and `GOARCH` pass through to `go build` for cross-compiling | Go stdlib | `--os`/`--arch` flags | [Deploy](deploy.md#building-for-another-os-or-architecture) |
| The CLI prints positions as `src/…:L:C`, normalizing Go's `./src/…`; plain `go build`/`go vet` still print `./src/…` | simplest | Keep Go's `./src/…` in CLI output too | [CLI](reference/cli.md#error-output), [Components](components.md#how-it-compiles) |
| Without a `tool` directive, a global CLI whose version differs from the runtime in `go.mod` stops with an error pointing to `orbit doctor`; skipped when `go.mod` has a local `replace` | simplest | A warning only; no check | [CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit) |
| The mismatch check is also skipped when `go.mod` doesn't require `github.com/orbit-z/orbit` (the command then reports what's wrong) | simplest | Stop with "not an Orbit project" | [CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit) |
| A CLI is "global" when its build info's main module is `github.com/orbit-z/orbit` (`go install …@v`, `go build`/`go run` in a clone); a binary built inside another module (`go tool orbit`) is the pinned copy and never hands over; `ORBIT_REEXEC=1` stops the hand-over too | simplest | `ORBIT_REEXEC` alone (a direct `go tool orbit` would hand over to itself once); comparing executable paths | [CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit) |
| The hand-over runs `go tool -n orbit` (builds the pinned CLI, prints its cached path), then execs that binary (replacing the process on Unix; a child with Ctrl-C ignored on Windows), with `ORBIT_REEXEC=1` and the selected toolchain's `bin` first on `PATH` as `go tool` sets it | simplest correct option: signals and exit code are the pinned CLI's own, so Ctrl-C on `orbit dev` exits 0 (`go tool orbit dev` exits 1: its `go` wrapper forwards the terminal's SIGINT, so the tool gets two) | Run `go tool orbit <args>` as a child and forward signals | [CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit) |
| `orbit doctor` is exempt from the hand-over and the mismatch stop, like `orbit new` | simplest (it must run when the pinned CLI doesn't build, and it reports the mismatch) | Hand it over too | [CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit) |
| `orbit doctor` treats the module around the working directory as a project when it has a `src/` directory | simplest (the LSP's rule, PLAN.md §9) | Any `go.mod`; a `go.mod` requiring Orbit | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor`'s freshness check regenerates the project in memory (as `orbit generate`, writing nothing) and compares with `orbit_gen/`, ignoring `orbit_gen/manifest/manifest.go` (asset URLs; `orbit dev` writes it empty); errors in `.orbit` files are a ⚠ naming the first | simplest reliable (the generated headers carry no source hash) | Source hashes in the generated headers (a codegen change); comparing mtimes (unchanged files aren't rewritten, so they're unreliable) | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor`'s `go` check: `go.mod` without `go ≥ 1.27` is ✗ (`go mod edit -go=1.27`); a missing `toolchain` line is ⚠ (`go get toolchain@<active>`); an active toolchain older than the `go` line is ✗ (`go env -w GOTOOLCHAIN=auto`); outside a project, a Go older than 1.27 passes when it can switch toolchains (≥ 1.21, `GOTOOLCHAIN` not `local`) | simplest | ⚠ for every old Go outside a project | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor`'s `cli` check, with the `tool` directive, runs `go list` on the pinned CLI's package (✗ → `go mod tidy`) instead of building it | simplest (fast; a first build of the CLI takes tens of seconds) | `go tool -n orbit` (builds it) | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor`'s `.gitignore` check reads plain patterns only (`dist`, `/dist/`, `dist/**`, a later `!dist` re-includes) and accepts `orbit_gen/static/` for a committed `orbit_gen/`; the fix is a `printf '…' >> .gitignore` of the missing lines | simplest | Implement gitignore globbing; ask `git check-ignore` (needs git and a repository) | [CLI](reference/cli.md#orbit-doctor) |
| A failing `orbit doctor` check whose problem has no runnable fix gets a short instruction in `fix` (`fix orbit.json`, `create ./cmd/web, or remove server from orbit.json`) | simplest | Leave `fix` empty | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor` reads gopls's version and Go version from the binary's build info (`debug/buildinfo`), without running it | Go stdlib | `gopls version -json` | [CLI](reference/cli.md#orbit-doctor) |
| `orbit doctor --json` is indented, with `=>` and `<` unescaped | simplest (readable) | One line; `encoding/json`'s HTML escaping | [CLI](reference/cli.md#orbit-doctor) |
| `orbit create-key` prints a base64 island key | Astro (`astro create-key`) | Document `openssl rand -base64 32` instead | [CLI](reference/cli.md#orbit-create-key) |
| A committed `orbit_gen/` still ignores `orbit_gen/static/client/` and `orbit_gen/static/static.go` (the `--embed` copy of the built files); `orbit generate` always writes `orbit_gen/static/doc.go`, so `go mod tidy` and `go vet`, which load every build tag, find the package `app/embed.go` imports | simplest | Commit them too | [Project structure](project-structure.md#committing-orbit_gen-optional) |
| Generated package paths: every byte outside `[a-z0-9_]` becomes `_`, and a segment starting with `_`, or named `testdata`, `vendor` or `internal`, gets a `p_` prefix (the go command treats those specially) | simplest | Hash-based package names; reject such file names | [Project structure](project-structure.md#orbit_gen-generated-go-code) |
| The CLI strips the `vet: ` prefix `go vet` puts before a type error, so `orbit build` and `orbit check` print it alike | simplest | Keep Go's prefix | [CLI](reference/cli.md#error-output) |
| Two files that generate the same package are an error on the later path (sorted), naming the other and the package: `src/components/my_card.orbit: generates the same Go package as src/components/my-card.orbit (orbit_gen/decl/components/my_card); rename one of them` | simplest | Report it on both files | [Project structure](project-structure.md#package-paths), [Troubleshooting](troubleshooting.md#generates-the-same-go-package-as-srccomponentsmy-cardorbit-orbit_gendeclcomponentsmy_card-rename-one-of-them) |
| Discovery skips files and directories whose names start with `.`, and follows symlinked directories except a link back to a directory being walked (a loop) | simplest | Skip every symlink; follow loops until an error | [Project structure](project-structure.md#srccomponents-and-srclayouts) |
| A project without a `src/` directory is an error (`<root> has no src/ directory; run orbit in an Orbit project`), so a stray `orbit_gen/` isn't written into the wrong module | simplest | Generate an empty app | [Troubleshooting](troubleshooting.md#has-no-src-directory-run-orbit-in-an-orbit-project) |
| Stale removal deletes only files that start with the generated-code line, then the directories they left empty; in `orbit_gen/static/` only `doc.go` is written and nothing is removed; changed files are written atomically (temp file plus rename) and identical ones aren't rewritten | simplest | Delete all of `orbit_gen/` and rewrite it | [Project structure](project-structure.md#orbit_gen-generated-go-code), [CLI](reference/cli.md#orbit-generate) |
| `orbit generate` prints nothing on success; `-h` prints the usage and exits 0 | Go stdlib (`flag`, quiet tools) | Print a summary; exit 2 on `-h` | [CLI](reference/cli.md#orbit-generate) |
| Pass-1 code generation runs on every file, even ones with errors, so all codegen errors are reported in one run; the typed pass is skipped when there are errors | simplest | Stop at the first file with errors | [CLI](reference/cli.md#orbit-generate) |
| The generated `app.New` prepends `orbit.WithSite(site)` to the caller's options when `orbit.json` sets `site` | simplest | (see the `WithSite` row under Rendering and the server) | [CLI](reference/cli.md#orbit-generate), [Runtime API](reference/runtime-api.md#options) |

| `orbit build` prints nothing on success | Go stdlib (`go build`) | An Astro-style summary | [CLI](reference/cli.md#orbit-build) |
| `orbit build --verbose` prints each page's CSS/JS bundles with raw and gzip sizes, and totals | user | Always print a summary, as Astro does | [CLI](reference/cli.md#orbit-build) |
| Production source maps are opt-in with `"sourcemap": true` in `orbit.json`: external `.map` files under `/_orbit/`, not linked from the bundles; `orbit dev` always writes linked maps pointing at `.orbit` lines | user (the key name: coordinator, esbuild's and Vite's name) | Always on; linked in production | [Configuration](configuration.md) |
| A failed `orbit build` leaves `dist/` unchanged (built in a staging directory, then swapped in) | simplest (a new client is never paired with an old server) | Keep the old server but replace `dist/client`, as `go build` would | [CLI](reference/cli.md#orbit-build) |
| Each file under `public/_orbit/` is reported: `public/_orbit/x.js: the /_orbit/ path is reserved for Orbit's assets; move it out of public/_orbit`; an empty `public/_orbit/` is ignored | simplest | One error for the directory | [Project structure](project-structure.md#public) |
| `public/` copy: dotfiles are copied, a link to a file is copied as the file, a link to a directory is an error | simplest (no loops) | Follow directory links | [Project structure](project-structure.md#public) |
| `orbit preview` runs the server in its own process group and forwards SIGINT/SIGTERM/SIGHUP (so Ctrl-C reaches it once and it shuts down gracefully); nothing is forwarded on Windows | simplest correct option | One process group, like `go run` | [CLI](reference/cli.md#orbit-preview) |
| `orbit preview` exits 1 with the reason when the server is killed by a signal | simplest | 128 + signal, like a shell | [CLI](reference/cli.md#orbit-preview) |
| `orbit preview` with no `dist/server`: `orbit preview: no dist/server in <root>; run orbit build first`, exit 1; it works from any project directory and runs the server from the root | simplest | Only from the root | [CLI](reference/cli.md#orbit-preview) |
| Extra positional arguments to `generate`, `build` or `preview` are a usage error (exit 2) | Go stdlib (`flag` conventions) | Ignore them | [CLI](reference/cli.md#exit-codes) |
| `dist/server` keeps its name for `GOOS=windows` (no `.exe`) | Go stdlib (`go build -o` uses the name as given) | Add `.exe` | [Deploy](deploy.md) |
## Configuration

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Unknown `orbit.json` keys are ignored | Go stdlib (`encoding/json`) | An error, or a warning naming the key | [Configuration](configuration.md#example) |
| An unset `site` gives a nil `ctx.Site` (like `Astro.site` being `undefined`) | Astro | A zero `url.URL` | [Configuration](configuration.md#site), [Runtime API](reference/runtime-api.md#request-context-ctx) |
| A `site` that isn't an absolute URL (no scheme or host) is an error when `orbit.json` loads | simplest | Accept it and resolve it later, or warn | [Configuration](configuration.md#site) |
| `compressHTML`: `"jsx"` (default, Astro v7's: JSX rules; `{" "}` forces a space), `"html"` (HTML-aware and lossless, Astro's `true`, which is accepted as a synonym) or `false` (verbatim), applied at codegen time. The default follows Astro 7 (user decision 2026-10-26, reversing the earlier `"html"` default) | user | `"html"` default (lossless, words never join, but ported Astro sites render differently); no compression | [Configuration](configuration.md#compresshtml), [Template syntax](reference/template-syntax.md#whitespace) |
| `"html"` mode drops a whitespace-only text when its neighbors on both sides are block boundaries (block elements' tags, doctypes, scripts and styles, the template's ends) or kept whitespace, looking through every `{if}` branch and `{for}` iteration; components, slots and HTML comments count as inline. Other runs collapse to one space, or one newline if they contain one | simplest (never joins words, never changes rendering) | Treat components as blocks; drop only between two block tags, not looking through `{if}`/`{for}` | [Template syntax](reference/template-syntax.md#whitespace) |
| `"jsx"` mode applies Babel's line rule to each text (lines trimmed except at the text's own ends, empty lines dropped, the rest joined with one space) | Astro (its JSX mode follows Babel) | A custom approximation | [Template syntax](reference/template-syntax.md#whitespace) |
| `compressHTML: true` means `"html"`; any other value is an error: `orbit.json: compressHTML must be "jsx", "html" (or true) or false, not "none"` | user | An error | [Configuration](configuration.md#compresshtml) |
| Whitespace edge cases stay Orbit's own, not byte-identical to Astro 7.3: under `"jsx"`, text directly inside a component or `<Fragment>` follows the JSX rules (Astro leaves it verbatim, a compiler quirk); `"html"` collapses every run and drops whitespace between block tags (Astro's `true` keeps in-text runs and a space between blocks) | user (2026-10-26) | Copy Astro's output in both cases | [Coming from Astro](coming-from-astro.md) |
| A key set to `null` counts as unset | Go stdlib (`encoding/json`) | An error | [Configuration](configuration.md#example) |
| `server` must be non-empty and relative to the project root; `orbit generate` doesn't check that it exists (`orbit doctor` does) | simplest | Check existence on every generate | [Configuration](configuration.md#server) |
| JSON syntax errors carry `orbit.json:L:C` | simplest | Go's byte offset only | [Configuration](configuration.md#example) |
| `orbit.json` keys match case-insensitively (`"Site"` works like `"site"`) | Go stdlib (`encoding/json`) | Exact-case keys only | — |
| `watch` globs: each `/`-separated part is a `path.Match` pattern, and a part that is exactly `**` matches zero or more directories; no `{a,b}` braces | Go stdlib (`path.Match`) plus the `**` of the glob libraries Astro's Vite uses (picomatch), the simplest form of it | `path.Match` only (no recursion); full picomatch syntax | [Configuration](configuration.md#watch) |
| `watch` patterns are relative to the project root and may start with `../` (outside the module); absolute paths and `\` are errors | simplest (the dogfood docs site watches `../*.md`) | Allow absolute paths | [Configuration](configuration.md#watch) |
| Invalid `watch` patterns (empty, absolute, bad `[` class) and a non-array value are errors when `orbit.json` loads: `orbit.json: watch[0] "…": …` | simplest (same as the other keys) | Report them only in `orbit dev` | [Configuration](configuration.md#watch) |
| A pattern matches files only (`content` doesn't match `content/a.md`; write `content/**`) | picomatch (Vite's watcher) | A directory pattern matches everything below it | [Configuration](configuration.md#watch) |

## Components and templates

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The `set:` and `is:` namespaces are reserved like `server:`/`client:`: an unknown directive in them is an error listing the valid ones | simplest | Pass them through as attributes | [Template syntax](reference/template-syntax.md) |
| `is:inline` works only on `<script>`/`<style>`, `is:global` only on `<style>` | Astro | Ignore elsewhere | [Styles and scripts](styles-and-scripts.md) |
| `slot="…"` must be a static, non-empty name | simplest | Allow `{expr}` names | [Components](components.md) |
| `{for}` with an empty clause is an error (no infinite loops by accident); `{}` is allowed and renders nothing | simplest / Astro | Allow `{for}`; reject `{}` | [Template syntax](reference/template-syntax.md) |
| An unquoted attribute value ends at whitespace or `>` (HTML rules), so `a=b/>` doesn't self-close | Go stdlib / HTML | Treat `/>` specially | [Template syntax](reference/template-syntax.md) |
| Tag names are case-sensitive (`<Card>` is a component, `<card>` an HTML element) | Astro | Case-insensitive | [Components](components.md) |
| A `<script src>` counts as local (processed) when it starts with `./` or `../`; `<style>` is processed unless `is:inline` | Astro | — | [Styles and scripts](styles-and-scripts.md) |
| Children of a `server:defer` component inside `{if}`/`{for}` are rejected like other non-fallback children | simplest | Allow conditional fallbacks | [Lazy loading](lazy-loading.md) |
| When `server:defer` and `client:*` are combined, the error is reported at the first of the two | simplest | At both | [Lazy loading](lazy-loading.md) |
| The brace scanner tracks only `{}` (not `()[]`) to find the end; valid Go gives the same result, and `{f(x}` stops at the first `}`, keeping errors local | simplest | Track all brackets | — |
| A newline inside a `"…"` or `'…'` literal, or a `//` comment reaching the end of input, makes the construct unterminated, as in Go | Go spec | — | — |
| `+` isn't HTML-escaped (html/template escapes it against UTF-7 sniffing, impossible with Orbit's `charset=utf-8`); escaped set: `& < > " '` in text and `& < > "` in attribute values (spelled as Astro does since the nyth port; see "Port fixes"), and NUL → U+FFFD | simplest (typed and `Expr` output stay byte-identical, e.g. `1e+21`) | Escape `+` like html/template | [Template syntax](reference/template-syntax.md) |
| Only unnamed slices render element by element; a named slice (`type Tags []string`) uses `fmt.Sprint`, following the exact-type rule | Go stdlib | Expand named slices too | [Template syntax](reference/template-syntax.md) |
| A named bool in `Attr`/`Spread` behaves like `bool` (bare or omitted), matching the `AttrBool` codegen emits | simplest | `fmt.Sprint` it | [Template syntax](reference/template-syntax.md) |
| URL attribute names match case-insensitively; tabs/newlines inside a scheme are ignored (`java\tscript:`), as browsers do | Go stdlib / browsers | Exact match | [Components](components.md) |
| A nil pointer whose `String`/`Error` method panics prints `<nil>`, like fmt | Go stdlib | Propagate the panic | [Template syntax](reference/template-syntax.md) |
| The depth-limit error lists the component stack outermost first, naming the first 10 then ` > …` | simplest | Full stack | [Components](components.md) |
| Head-injected tags keep asset order; whitespace before `<!DOCTYPE>` is allowed; pooled buffers over 1 MiB are dropped, new ones start at 8 KB | simplest | — | [Performance](performance.md) |
| Declaring `type Props` in a page is a build error (pages never receive props) | simplest | Ignore it, or warn | [Components](components.md#pages) |
| The page-`Props` error now ends `…or read params` (it used to point at the removed `ctx.Params`) | simplest | `…or read ctx.Param` | [Components](components.md#pages) |
| Declaring `type Params` in a page is a build error at the declaration: `Params is generated from the page's route; rename this type`. Components may declare a `Params` | simplest (like `Props` in a page) | Let the user's type replace the generated one; a warning | [Troubleshooting](troubleshooting.md#params-is-generated-from-the-pages-route-rename-this-type) |
| `params` can't be redeclared at the top level of a page's body (same message as `props`); in a component it's an ordinary name | simplest (the "provided by Orbit" message would be false in a component) | Reserve `params` in components too | [Components](components.md#redeclaring-and-shadowing) |
| Astro's `define:vars` is a compile error with a hint (other colon attributes like `xlink:href` stay plain HTML; `class:list` was one too, until it was accepted: see [class:list](#classlist)) | simplest | Write it out as a plain attribute | [Template syntax](reference/template-syntax.md#combining-and-reserved-namespaces) |
| A lowercase component alias (`card "./Card.orbit"`) is an error suggesting `Card` | simplest | Accept it and capitalize the tag | [Components](components.md#importing-components) |
| A relative import that isn't `.orbit` (`"./util.ts"`) gets its own error, not the framework-component one | simplest | Reuse the framework-component message | [Components](components.md#no-framework-components) |
| The unknown-prop list spells fields in lower camel case (`isOpen`, `id`) | Astro (props are usually camelCase) | Go field names (`IsOpen`, `ID`) | [Components](components.md#unknown-props) |
| An unexported name used only by `Props` (and other declaration-package code) is allowed; the export error appears only when the body or template uses it | simplest | Always require names `Props` uses to be exported | [Components](components.md#types-used-by-props) |
| `slot="…"` is stripped from the output; several children with the same slot all render there, in order | Astro | Last one wins; an error | [Components](components.md#slots) |
| Whitespace-only children pass no slot, so `slots.Has` is false and fallbacks render | Astro | Pass the whitespace as a default slot | [Components](components.md#checking-for-a-slot), [Template syntax](reference/template-syntax.md#components) |
| The `on*={…}` and `style={…}` warnings can't be silenced | simplest | A comment or flag to silence one | [Components](components.md#event-handler-and-style-attributes) |
| The `set:html` type error keeps Go's own text (`… as orbit.HTML value in argument to orbit__w.HTML`), explained in the docs | simplest | Rewrite it to an Orbit message | [Components](components.md#raw-html-with-orbithtml) |
| `{…}` inside an HTML comment isn't evaluated | Astro | Evaluate expressions in comments | [Template syntax](reference/template-syntax.md#comments) |
| `{if}` and `{else if}` accept an init statement (`{if n, ok := m[k]; ok}`) | Go stdlib (Go's `if` syntax) | Condition only | [Template syntax](reference/template-syntax.md#if) |
| Any element may be self-closed: `<div />` renders `<div></div>` | Astro | Self-closing only for void elements and components | [Template syntax](reference/template-syntax.md#html-and-custom-elements) |
| `<title>` and `<textarea>` content is RCDATA: `{expr}` works, other markup is text | Astro (HTML parsing rules) | Parse markup inside them | [Template syntax](reference/template-syntax.md#html-and-custom-elements) |
| `<Fragment>` takes only `slot`, `set:html` and `set:text`; any other attribute is an error | Astro | `slot` only | [Template syntax](reference/template-syntax.md#fragment) |
| The fragment shorthand `<>…</>` is supported, compiled exactly as `<Fragment>…</Fragment>`; it takes no attributes (`<Fragment slot="x">` for a slot), so `<` followed by `>` now starts a tag (`a <> b` in text must be written `a &lt;> b`); a `<>`/`</>` mismatch is an end-tag error | Astro | Reject `<>` with a hint to write `<Fragment>` (the earlier behavior was an unhelpful `expected a tag name after </`) | [Template syntax](reference/template-syntax.md#fragment) |
| A `set:` directive on an element with children is an error, and `set:` on a component is an error | Astro | Let the directive win over children; allow `set:` on components | [Template syntax](reference/template-syntax.md#directives) |
| A slice in an attribute value is formatted like content (elements concatenated, no separator) | simplest | Join with spaces, as for class lists | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements) |
| URL attributes filtered at runtime: `html/template`'s list plus `xlink:href` | Go stdlib | The HTML spec's full list | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements), [Runtime API](reference/runtime-api.md#attribute-values) |
| Spread attributes are written sorted by name; duplicates of attributes on the same element aren't merged | simplest | Map order; merge, with the spread or the explicit attribute winning | [Template syntax](reference/template-syntax.md#spread-attributes) |
| The PascalCase fallback for props uses golint's common-initialisms table | Go stdlib (Go tooling convention) | A short custom list | [Template syntax](reference/template-syntax.md#name-matching) |
| `client:load` or `client:idle` with a value is an error (`client:idle takes no value`) | simplest | Ignore the value | [Template syntax](reference/template-syntax.md#client) |
| `client:*` values must be static strings | simplest (the marker is folded into precomputed static HTML) | Allow `{expr}` values | [Template syntax](reference/template-syntax.md#client) |
| Wording of the errors PLAN didn't specify: `unknown component <Crad>: no .orbit import with this alias`, `unexpected </ul>: <li> opened at 12:5 isn't closed`, `server:defer takes no value`, `client:visible takes a static string value, not an expression`, `client:media needs a media query, e.g. …`, `only one client:* directive is allowed per component` | simplest (`go vet`-style: lowercase, no trailing period) | Other wording | [Template syntax](reference/template-syntax.md#directives) |
| `rendering.md` is called that, not `rendering-modes.md`, since there's only one mode | simplest | Astro's page name | [Rendering](rendering.md) |
| Wording of the frontmatter and codegen errors PLAN didn't specify: `orbit__x is reserved: names starting with orbit__ or Orbit__ are used by generated code`, `Render is generated for every component; rename this declaration`, `Props field title must be exported, since a parent sets it (rename it to Title)`, `Props fields IsOpen and Is_Open both match the attribute name isopen; rename one`, `dot imports aren't supported in a component; import the package by name`, `Item clashes with Item in the component's declaration package (Props uses it); rename one`, `Props can't have type parameters`, `component path ../../X.orbit is outside src/`, `no such component: src/components/Crad.orbit` | simplest (`go vet`-style) | Other wording | [Troubleshooting](troubleshooting.md#compile-errors-in-orbit-files) |
| An `.orbit` import without an alias, or with `_`, is an error suggesting one from the file name: `a component import needs an alias: Card "./Card.orbit"` | simplest | Derive the alias from the file name | [Components](components.md#importing-components) |
| An alias, named Go import or implementation-side name equal to `Props`, `Render` (or a page's `Params`) is an error: `Props clashes with the generated Props of the component's declaration package; rename it` | simplest (like the `Props`-export clash) | Go's own `redeclared` error at a generated position | [Components](components.md#types-used-by-props) |
| An unused component import is an error at the alias: `component Card is imported but not used; remove the import`; it isn't emitted. A use in Go code (`Card.Props`) counts, `<Self>` doesn't | user | Emit it and let Go report the unused import | [Components](components.md#importing-components) |
| Rename hints (`rename it to Item`, `Card "./Card.orbit"`) uppercase only the first letter (`url` → `Url`) | simplest | Apply the initialism table (`URL`) | [Components](components.md#fields-must-be-exported) |
| Orbit doesn't report Go syntax errors in the frontmatter; Go and gopls do, at the same `.orbit` positions | simplest (no duplicate errors) | Orbit's own syntax errors | — |
| An import without a name is placed by the package name guessed from its path's last element (skipping `/vN`, dropping `.vN`, a `go-` prefix and a `-go` suffix), without loading the package | simplest (keeps generation fast) | Load each package to read its name | — |
| Template comments and whitespace after a terminating last statement are allowed (only other content is the "always returns" error) | simplest | Require an empty template | [Components](components.md#returning-errors-redirects-and-404s) |
| `FieldName` keeps a word's tail as written (`isOpen` → `IsOpen`); `LowerCamel` (the unknown-prop list) follows Go's unexported-name style (`ID` → `id`) | Go stdlib (Go naming conventions) | Lowercase the tail | [Template syntax](reference/template-syntax.md#name-matching) |
| A grouped hoisted declaration (`type (…)`, `const (…)`) goes to one package as a whole; doc comments move with their declaration | simplest | Split a group by what `Props` needs | [Components](components.md#types-used-by-props) |
| Dot imports are rejected, Go and `.orbit` alike | simplest | Allow Go dot imports | [Troubleshooting](troubleshooting.md#dot-imports-arent-supported-in-a-component-import-the-package-by-name) |
| A non-struct, aliased or embedding `Props` isn't listed for the unknown-prop check (Go reports unknown fields); a generic `Props` is an error | simplest | Resolve embedded fields | [Troubleshooting](troubleshooting.md#unknown-prop-psot-on-card-props-post-featured) |
| Static attributes are copied as written; static prop values (`title="a &amp; b"`) aren't entity-decoded | simplest | Decode entities in prop values | [Template syntax](reference/template-syntax.md#attributes) |
| Void elements are written as `<br>`, also when written `<br />` | simplest (HTML serialization) | Keep the slash as written | [Template syntax](reference/template-syntax.md#html-and-custom-elements) |
| `slot="…"` is stripped only on a component's direct children; elsewhere it's a plain HTML attribute | simplest (keeps the attribute for web components) | Strip it everywhere | [Components](components.md#slots) |
| Until milestone 8, `server:defer` and lazy `client:*` (`idle`, `visible`, `media`) are errors at the directive (`server:defer isn't implemented yet`, `client:idle isn't implemented yet`), not silent no-ops; `client:load` compiles like no directive | simplest | Ignore the directive until then | — |
| Until milestone 6, processed `<script>`/`<style>` tags are left out of the generated HTML (they'll be bundled) | simplest | Copy them verbatim meanwhile | — |
| Typed constants aren't folded into the static `Raw` HTML yet | simplest | Fold them now | — |
| Attribute typed writes mirror `Attr`: `string`, `orbit.HTML` and named strings without methods use `AttrString`, or `AttrURL` on URL attributes (since milestone 9, written as pieces with `String` and `URL`); `orbit.URL` uses `AttrString`; `bool` and every named bool, with methods or not, use `AttrBool`; the rest `Attr` | simplest (byte-identical to `Attr`) | `Attr` for every named type | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements) |
| A type with a `Format` method keeps `Expr`/`Attr`, since `fmt` calls `Format` | Go stdlib (`fmt.Formatter`) | Use the typed write of its underlying type | [Runtime API](reference/runtime-api.md#typed-writes) |
| `uintptr` keeps `Expr` | simplest (as PLAN keeps complex types) | `Uint` | [Runtime API](reference/runtime-api.md#typed-writes) |
| Generated glue uses predeclared names (`true`, `error`, `int64`); shadowing them in the frontmatter isn't guarded | simplest (as PLAN §3.2's example) | Qualify or alias every predeclared name | — |
| Declaration packages on an import cycle fall back to `Expr` writes (milestone 5 reports the cycle) | simplest | Fail the type check | — |
| A `//` comment at the end of template Go code (`{x // note}`) is an error: `a // comment can't end Go code in the template; use /* */` | simplest (Go would swallow the generated code after it) | Rewrite it to `/* */` silently | [Troubleshooting](troubleshooting.md) |
| An attribute that can't be a Go field name, on a component whose `Props` fields can't be listed, is an error: `unknown prop "1x" on <Card>: not a Go field name` | simplest | Pass it to Go and let it fail at a generated position | [Troubleshooting](troubleshooting.md) |
| An `.orbit` import used only in Go code (for example `Card.Props{…}` in the frontmatter, no `<Card>` tag) counts as used | simplest (it's a real Go use) | Require a tag use | [Components](components.md) |
| In error-output cleanup, a bare `card.X` is rewritten to the alias only when the component's declaration package declares `X` and the file imports no Go package named `card` | simplest (never rewrites a real Go package's names) | Rewrite every `card.X` | — |
| `compressHTML: "html"` can leave a doubled newline at slot edges; not trimmed at codegen time, because trimming slot edges can join words | simplest (lossless first) | Trim slot edges | — |

## Routing

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| At `/docs`, an exact route `/docs` beats `/docs/[...rest]` (which also matches with an empty rest) | Astro | More segments win | [Routing](routing.md) |
| A file or directory under `src/pages/` whose name starts with `_` isn't a route but a component (`src/pages/blog/_parts/Byline.orbit`); so `src/pages/_orbit/…` is never a route, and only the runtime router still rejects a `/_orbit/` pattern | Astro | Route every file; a build error for `src/pages/_orbit/` | [Routing](routing.md#file-based-routes), [Project structure](project-structure.md#srcpages) |
| Paths with empty segments (`/blog//x`) never match a page, and a trailing-slash redirect never targets `//…` | simplest | Collapse slashes | [Routing](routing.md) |
| `404.orbit` uses a reserved pattern outside the route trie, so `/404` isn't a routable URL | simplest | Route `/404` too | [Routing](routing.md) |
| All route errors are reported together (`errors.Join`); a duplicate reads `<file>: route /x is already defined by <file>; remove or rename one of them` | Go stdlib | Stop at the first | [Routing](routing.md) |
| Mixed segments (`post-[id].orbit`) and non-trailing rest params (`[...path]/edit.orbit`) are a build error in the MVP, suggesting a whole-segment `[param]` | user | Support them like Astro | [Routing](routing.md#dynamic-routes-with-param) |
| A rest param must be the last segment | simplest | (see the row above) | [Routing](routing.md#catch-all-routes-with-rest) |
| Route errors, route-param errors (`[1st]`, colliding fields) and package collisions name the file, without a line and column (they're about the file, not a position in it) | simplest | Report them at `:1:1` | [Routing](routing.md#file-based-routes), [CLI](reference/cli.md#error-output) |
| Unbalanced brackets and empty param names are build errors: `invalid route pattern "/[slug": segment "[slug" has unbalanced brackets`, `… has an empty parameter name` | simplest | Treat the brackets as text | [Routing](routing.md#dynamic-routes-with-param) |
| Two files with the same route are a build error naming both | simplest | Pick one by a priority rule | [Routing](routing.md#file-based-routes) |
| Params are percent-decoded; a rest param joins its segments with `/`, and is `""` when it matches none | Go stdlib (`PathValue`) | Raw, undecoded values | [Routing](routing.md#dynamic-routes-with-param) |
| Typed route params: each page gets a generated `Params` struct (one `string` field per param, PascalCase with the props initialism rules) as `params`; `ctx.Params` and `orbit.Params` are removed; components use `ctx.Param(name)` | user | Keep the `map[string]string` | [Routing](routing.md#route-params-params) |
| `ctx.Param(name)` returns `""` for a name the route doesn't have, with no second result | Go stdlib (`Request.PathValue`) | `(string, bool)` | [Runtime API](reference/runtime-api.md#request-context-ctx) |
| Two route params whose field names collide (`[post-id]` and `[postId]`, or `[id]` twice) are a build error at the file naming both | simplest | Let the last one win | [Routing](routing.md#field-names) |
| A route param name must match `[A-Za-z][A-Za-z0-9_-]*`; anything else (`[1st]`, `[é]`) is a build error at the file | simplest | Mangle it into a field name (`P1st`) | [Routing](routing.md#field-names) |
| A page's `Params` is filled by its generated `Render` wrapper from `ctx.Param` calls, not by the route table, so `orbittest.Render` and real requests build it the same way | simplest (keeps one `Render` signature for pages and components) | Pass `Params` from the route table as an extra `Render` argument | [Testing](testing.md#route-params-withparam) |
| Without a `404.orbit`, a not-found response is a 404 with an empty body and `Content-Length: 0` | simplest | A built-in 404 page | [Routing](routing.md#the-404-page) |
| `404.orbit` renders with 404 unless it calls `ctx.SetStatus`; if it returns `orbit.ErrNotFound` itself, the response is an empty 404 | simplest | Always 404; a 500 | [Routing](routing.md#the-404-page) |
| The trailing-slash 308 has no body, only `Location` | simplest | `http.Redirect`'s short HTML body | [Routing](routing.md#trailing-slashes) |
| The trailing-slash 308 is sent only when the slash-less path is a page; otherwise the request reaches `next` untouched | simplest (never steals your routes) | Redirect every trailing-slash path | [Routing](routing.md#trailing-slashes) |
| The island endpoint answers other methods with 405 and `Allow: GET, HEAD, POST` | Astro (405), Go stdlib (`Allow`) | Fall through to `next` | [Routing](routing.md#which-http-methods-orbit-handles) |

## Rendering and the server

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `RenderComponent` honors the component's `ctx.SetStatus` and `ctx.Header()` (its own `Content-Type` wins); a panic is recovered into an error at the `.orbit` line (`http.ErrAbortHandler` re-panics); `Redirect` and `ErrNotFound` are returned unchanged; HEAD gets no body | simplest (consistent with pages) | Always 200; let net/http recover panics | [Server](server.md#rendering-components-from-handlers) |
| `ctx.Site` is nil inside `RenderComponent` (it has no `*Site`) | simplest | A `(*Site).RenderComponent` method | [Runtime API](reference/runtime-api.md) |
| `--embed` adds `orbit_embed` to the `-tags` already in `GOFLAGS` (a command-line `-tags` would replace them); a site with no static files embeds an empty `embed.FS` | Go stdlib (`go build` flag rules) | Pass `-tags orbit_embed` alone | [Deploy](deploy.md) |
| `ctx.Redirect` with a non-3xx code panics with a clear message (recovered per request as a 500 pointing at the call) | Go stdlib (net/http panics on invalid codes) | Pass any code through | [Rendering](rendering.md) |
| `ctx.Redirect` stores `Location` as given; relative paths are resolved when the response is written, like `http.Redirect` | Go stdlib | Resolve in `Redirect` | [Rendering](rendering.md) |
| Production 500: a minimal built-in page; the error and its `.orbit` position are logged to stderr. A custom `src/pages/500.orbit` comes after the MVP | user | A `500.orbit` page now | [Rendering](rendering.md#status-codes-and-errors) |
| The 500 log line reads `orbit: <METHOD> <path>: <file:line:col>: <error>` | simplest | Structured (`log/slog`) output | [Rendering](rendering.md#status-codes-and-errors) |
| A frontmatter `return x` is wrapped at codegen with its position (`orbit.ErrorAt`), so the 500 log reads `src/pages/blog/[slug].orbit:14:5: …`; `ErrNotFound`, redirects and errors that already carry a position pass through unwrapped | user | Log only the file, or no position | [Rendering](rendering.md#status-codes-and-errors), [Runtime API](reference/runtime-api.md) |
| Bare and multi-value returns aren't wrapped (Go reports them); returns inside function literals and hoisted funcs aren't either | simplest | Wrap the last result | — |
| A wrapped `ErrNotFound` (`fmt.Errorf("x: %w", orbit.ErrNotFound)`) gets a position but is still a 404 through `errors.Is` | simplest (identity check only, no cost on every return) | `errors.Is` inside `ErrorAt` | — |
| The position is the project-relative path and the byte column of the `return` keyword | Go stdlib (byte columns, like `go build`) | Rune or UTF-16 columns | — |
| A type error in a wrapped return reads "in return statement", not "in argument to orbit__rt.ErrorAt" (rewritten in the CLI's error cleanup) | simplest | Show the generated name | — |
| A panic is logged as `orbit: GET /path: src/pages/x.orbit:LINE: panic: <value>`: the innermost `.orbit` frame, its path relative to the working directory, no stack trace | simplest | Log the full stack | [Rendering](rendering.md#status-codes-and-errors) |
| A redirect keeps the headers the page set (such as `Set-Cookie`); a 500 and the `ErrNotFound` 404 drop them | Go stdlib (`http.Redirect` on the same `Header()`) | Keep them on every response; drop them on redirects too | [Rendering](rendering.md#redirects) |
| Orbit sets `Content-Type: text/html; charset=utf-8` only when neither the page nor outer middleware set one | simplest | Always overwrite it | [Rendering](rendering.md#response-headers) |
| `orbit.ListenAndServe` returns nil after a graceful shutdown on SIGINT/SIGTERM; examples and the scaffold call it as `if err := orbit.ListenAndServe(h); err != nil { log.Fatal(err) }`, so Ctrl-C exits 0 (`log.Fatal` exits 1 even for nil, which the milestone 5 e2e test caught) | coordinator (simplest) | Return `http.ErrServerClosed`, like `net/http` | [Server](server.md#listening-listenandserve-and-addr), [Runtime API](reference/runtime-api.md#serving) |
| If requests are still running after the 5 s grace period, `ListenAndServe` closes the connections and returns `orbit: shutdown: context deadline exceeded`, not nil; a second Ctrl-C stops the process at once | Go stdlib (`Server.Shutdown`'s error, `signal.NotifyContext`'s stop) | Return nil; ignore the second signal | [Server](server.md#listening-listenandserve-and-addr), [Runtime API](reference/runtime-api.md#serving) |
| `ListenAndServe` timeouts: `ReadHeaderTimeout` 10 s, `IdleTimeout` 120 s, no `ReadTimeout`/`WriteTimeout`, 5 s shutdown grace period | coordinator (simplest; no cut-off uploads, and the same 5 s `orbit dev` waits) | Read/Write timeouts; a longer grace period | [Server](server.md#listening-listenandserve-and-addr), [Runtime API](reference/runtime-api.md#serving) |
| `orbit.ListenAndServe` logs `orbit: listening on http://localhost:4321` once listening, through the `log` package (stderr, with its timestamp); the address is the bound one, an unspecified host (`:4321`, `0.0.0.0`, `[::]`) shown as `localhost` | Astro (`@astrojs/node` standalone logs `Server listening on http://localhost:4321`); Go stdlib (`log`) for the output | Print nothing; print to stdout | [Server](server.md#listening-listenandserve-and-addr) |
| Under `orbit dev` (`ORBIT_DEV=1`), `ListenAndServe` doesn't log its address: the app's random port isn't the one to open, and the dev banner is the only URL | simplest | Log it anyway | [Server](server.md#listening-listenandserve-and-addr) |
| `RenderComponent` is buffered: on success 200, `text/html; charset=utf-8` and `Content-Length`; on error nothing is written; locals come from `r` | simplest (follows buffered pages) | Stream the fragment; write a 500 itself | [Server](server.md#rendering-components-from-handlers) |
| `ORBIT_STATIC` wins over `WithStaticDir`, which wins over `--embed` files, then `client/` next to the binary | simplest (so `orbit dev` keeps working) | Code options win over the environment | [Deploy](deploy.md#setting-the-static-directory-or-the-key-in-code), [Runtime API](reference/runtime-api.md#options) |
| The server finds `client/` next to the binary (its own directory, not the working directory) | simplest | Relative to the working directory | [Deploy](deploy.md#server-binary-distserver-and-distclient) |
| The binary's directory is found from `os.Executable()` after resolving symlinks, so a symlinked binary still finds its `client/` | Go stdlib (`os.Executable`'s documented advice) | Use the symlink's directory | [Deploy](deploy.md#server-binary-distserver-and-distclient) |
| `public/` files get `Cache-Control: public, max-age=0`, `/_orbit/*` files `public, max-age=31536000, immutable`; both `no-cache` in dev | Astro | No header on `public/` files | [Deploy](deploy.md#how-static-files-are-served), [Runtime API](reference/runtime-api.md#site) |
| Directories are never served, not even their `index.html`; a file is served only at its exact path | simplest (no clash with page routes or the trailing-slash rule) | Serve `index.html` for a directory | [Deploy](deploy.md#how-static-files-are-served) |
| In production, static files are indexed once at startup (files added later need a restart); in dev they're looked up per request | simplest (keeps the per-request file check allocation-free) | Look files up on every request; watch the directory | [Deploy](deploy.md#how-static-files-are-served), [Troubleshooting](troubleshooting.md#a-file-added-to-distclient-isnt-served) |
| In dev, `$ORBIT_PUBLIC` serves every path outside `/_orbit/`, and `$ORBIT_STATIC` only `/_orbit/*` | simplest | Fall back from one to the other | [Dev server](dev-server.md#dev-mode) |
| Paths that aren't clean (`..`, `.`, `//`, a trailing slash) are never files; `os.Root` blocks escapes, symlinks included; dotfiles are served | simplest | Clean the path first; hide dotfiles | [Deploy](deploy.md#how-static-files-are-served) |
| Embedded files (no modification time) get a strong content `ETag`; files from a directory get `Last-Modified` | simplest | An `ETag` for every file | [Deploy](deploy.md#how-static-files-are-served), [Runtime API](reference/runtime-api.md#options) |
| A static directory set explicitly (`ORBIT_STATIC` or `WithStaticDir`) that doesn't exist logs one warning line at startup in production | coordinator (simplest; silence hid misconfigurations) | Stay silent; fail at startup | [Deploy](deploy.md#how-static-files-are-served) |
| Zero allocations in Orbit's own render path for a typical page, and exactly one (48 B, the `Content-Length` value, which `net/http` may read after the handler returns) to serve it, are asserted by allocation-count tests, not just benchmarked | coordinator (you had no preference; matches the router's tested 0-alloc match) | Report allocations in benchmarks only | [Performance](performance.md#allocations-per-request) |
| Slot content that uses parent variables allocates one closure per slot | user | Direct calls for non-cyclic imports (keeps it on the stack; slower dev loop) | [Performance](performance.md#components-are-cheap-to-call) |
| Constant response headers (`Content-Type`) use a shared read-only value instead of `Header().Set` | simplest (saves 1 alloc/request, measured) | `Header().Set` | [Performance](performance.md#allocations-per-request) |
| Locals aren't pooled | coordinator (simplest; a pooled value kept after the request would be silently reused) | A `sync.Pool` per `Locals` type | [Performance](performance.md#allocations-per-request) |
| `orbit.json`'s `site` reaches `ctx.Site` through a new option, `orbit.WithSite(url)`, which the generated `app.New()` passes first, so your own option can override it (for example per environment); a non-absolute value panics | coordinator (simplest; the generated code needs some way to pass it) | An unexported field set only by generated code | [Runtime API](reference/runtime-api.md#options) |

## Locals

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A `src/locals` package without `type Locals` (or with no Go files) is ignored: pages get `orbit.NoLocals` | Astro (an undeclared `App.Locals` is fine) | Warn or fail | [Middleware and locals](middleware.md) |
| `src/locals` must be `package locals` (`src/locals must be package locals, not package X`) | simplest (the one documented convention) | Accept any name | [Troubleshooting](troubleshooting.md) |
| `Locals` must be a struct (`Locals must be a struct type: type Locals struct { … }`); an alias (`type Locals = auth.Session`) is accepted | simplest | Accept any type | [Middleware and locals](middleware.md) |
| The locals cycle check reports any import path from `src/locals` to `orbit_gen/app`, `orbit_gen/impl/…` or `orbit_gen/cmd/…`, at the import, with the chain for indirect imports; other import cycles are left to `go build` | simplest | Check every cycle | [Middleware and locals](middleware.md) |
| `Locals` lives in `src/locals` (package `locals`) | simplest | `src/middleware/locals.go`; a path in `orbit.json` | [Middleware and locals](middleware.md#declare-the-struct) |
| `LocalsFromRequest` returns nil when nothing is attached | Go stdlib (context accessors return nil) | A detached zero value | [Middleware and locals](middleware.md#set-locals-from-middleware) |
| `WithLocals` on a request that already has locals shadows them downstream only, like `context.WithValue` | Go stdlib | Replace the value in place for everyone | [Middleware and locals](middleware.md#set-locals-from-middleware) |
| The `src/locals` import-cycle error is reported at the import, `file:line:col`, with the chain of packages for an indirect import | Go stdlib (`go vet`-style positions) | Go's own `import cycle not allowed` | [Middleware and locals](middleware.md#the-import-rule) |
| The `Props` import-cycle error reads `import cycle not allowed between component Props: A → B → A; declare the shared types in a package under internal/`, once per cycle, with the shortest chain, at the alias of the chain's first import in its first file (by path) | Go stdlib (`import cycle not allowed`) | Go's own error at a generated position | [Components](components.md#mutual-recursion), [Troubleshooting](troubleshooting.md) |

## Lazy loading

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Under `go test` the build ID is always `0000000000000000` (only the key follows `ORBIT_KEY`), and `orbittest.Render` always uses the fixed test key | simplest (stable snapshots) | Follow `ORBIT_KEY` under test | [Testing](testing.md) |
| The random-key warning is printed once, and only when the site has `server:defer` components | simplest (no noise on every `go run`) | Always | [Lazy loading](lazy-loading.md) |
| A failing island in production gets a 500 with an empty body and a log line; in dev the body is the error as `text/plain` | simplest (the loader keeps the fallback) | The built-in 500 page | [Lazy loading](lazy-loading.md) |
| Everything under `/_orbit/island/` belongs to the endpoint: a malformed path or unknown ID is an empty 404, never `next`; the method check (405) comes first | PLAN (the prefix is reserved) | Fall through to `next` | [Lazy loading](lazy-loading.md) |
| A POST token is read from the whitespace-trimmed body (`?p=` is ignored on POST); a body read error other than the size limit is a 400 | simplest | — | [Lazy loading](lazy-loading.md) |
| `X-Robots-Tag: noindex` is set only if the component didn't set one, like `Cache-Control` | simplest | Always overwrite | [Lazy loading](lazy-loading.md) |
| A duplicate island ID panics in `NewSite` naming both files; an invalid embedded key or build ID panics at startup | Go stdlib (constructor convention) | — | — |
| The loader tags go before the page's CSS/JS in `<head>`, so the `display:contents` style comes first | simplest | After | — |
| The loader config's `lazy` map is always present and lists URLs per component ID (`{"Counter-…":["/_orbit/Counter-….js", …]}`), in module-evaluation order without duplicates | coordinator (a lazy call site can need several entries) | One URL per ID | [Lazy loading](lazy-loading.md) |
| Lists from several lazy call sites of the same component on a page are merged (the marker names only the component) | simplest | A per-call-site key on the wrapper | — |
| Slot content passed to a lazy component is part of its lazy subtree | Astro (island children render inside the island) | Count only the component's own imports | [Lazy loading](lazy-loading.md) |
| A lazy entry is built for every component with scripts that is lazy on at least one page (none for components eager everywhere) | simplest (no unused files) | One for every component with a lazy call site | — |
| CSS imported by a lazy entry's scripts is an extra page `css` asset on pages where that component is lazy | simplest (CSS stays with the page) | Merge it into the page CSS bundle | — |
| A `client:visible` value is copied as written into `opts` (only `"` is escaped), like other static attributes | simplest | Full HTML escaping | — |
| The JSON-check error names the field path with dots and `[]`, one error per top-level field, types qualified by package name | simplest | Every problem | [Lazy loading](lazy-loading.md) |
| A page may use `<Self server:defer>`; its island renders the page with empty params | simplest | Reject it | — |
| The island table in `orbit_gen/app` is sorted by file | simplest | By ID | — |
| The island loader's GET limit and test key: the fixed test key is the bytes `"orbit fixed test island key 0000"` | simplest | — | — |
| The islands loader watches the page with a `MutationObserver`, so placeholders and lazy markers in HTML inserted later (htmx partials, island fragments) load on pages that have the loader | coordinator (the docs and PLAN §5b promise it) | Scan only fragments the loader inserts | [Lazy loading](lazy-loading.md) |
| The islands and lazy scripts hand off through `window.orbit__lazy`; either can start first | simplest | A custom DOM event | — |
| A lazy component's entries load when its **first** instance on the page fires; markers nested in a lazy subtree wait until an enclosing lazy ID has fired | simplest (each script loads at most once per page) | Per-instance nesting | [Lazy loading](lazy-loading.md) |
| `client:idle` falls back to `setTimeout(…, 200)` without `requestIdleCallback` | Astro | 0 ms | [Lazy loading](lazy-loading.md) |
| `client:visible` observes the wrapper's element children; a component with only text never loads its script | Astro | Observe a text node's parent | [Lazy loading](lazy-loading.md) |
| The 8000-character GET limit counts `src + "?p=" + encodeURIComponent(token)`; POST sends `content-type: text/plain` | simplest | Count the absolute URL | [Lazy loading](lazy-loading.md) |
| The dev POST warning is printed once per island URL | simplest | Once per element | [Lazy loading](lazy-loading.md) |
| The loader's test dependency is happy-dom 20.14.6 (2026-10-08), the newest release at least two weeks old; a dev dependency of `loader/` only | coordinator (pinning rule) | — | — |
| Under `go test`, the runtime detects tests with `testing.Testing()` and uses the fixed island key and build ID `0000000000000000` (unless `ORBIT_KEY` or `WithKey` is set) | user (measured: +68 KB per production binary, no test flags registered) | A flag set by `orbittest` (no size cost; only tests importing it get the fixed key) | [Testing](testing.md) |
| In dev, the loader warns once per island in the console when its props are too big for a GET and it falls back to POST (POST isn't cacheable) | user (brought forward from post-MVP) | Silent | [Lazy loading](lazy-loading.md) |
| `server:defer` and `client:*` on one tag is an error | Astro (no such combination), then simplest | Apply `client:*` to the fragment's scripts | [Lazy loading](lazy-loading.md#combining-the-two) |
| Component IDs are `<Name>-<hash8>` | Astro (names endpoints after the component) | A hash only; the full path | [Lazy loading](lazy-loading.md#defer-a-slow-component) |
| Deferred props: `json:"-"` fields and unexported fields of nested structs are skipped | Go stdlib (`encoding/json`) | Report them as errors | [Lazy loading](lazy-loading.md#props-must-be-json-serializable) |
| `client:*` call sites are wrapped in an `<orbit-lazy>` element | Astro (`<astro-island>`) | Marker comments; an attribute on the root element | [Lazy loading](lazy-loading.md#the-orbit-lazy-wrapper) |
| An invalid `ORBIT_KEY` panics at startup | Go stdlib (constructors without an error result) | Return an error; fall back to a random key | [Deploy](deploy.md#island-key-orbit_key) |
| The island endpoint accepts any build ID whose token authenticates | simplest (rolling deploys keep working) | Only its own build ID | [Deploy](deploy.md#island-key-orbit_key) |
| A bad token gets a 400 with an empty body | Astro | 403 or 404 | [Lazy loading](lazy-loading.md#status-codes-and-errors) |
| `orbit.ErrNotFound` in an island gives an empty 404, not `404.orbit` | simplest | Render `404.orbit` | [Lazy loading](lazy-loading.md#status-codes-and-errors) |
| Island responses carry `X-Robots-Tag: noindex` | Astro | No header | [Lazy loading](lazy-loading.md#response-headers-and-caching) |
| The loader switches to POST above 8000 characters | simplest (fits nginx's default 8 KB request line) | Astro's 2048 | [Lazy loading](lazy-loading.md#large-props-get-and-post) |
| The loader logs island failures to the console in dev only | simplest | Always, or never | [Lazy loading](lazy-loading.md#status-codes-and-errors) |
| The loader is inline, and is injected only on pages whose static graph needs it | Astro (inline), simplest (static graph) | An external `/_orbit/loader.js`; detect islands at render time | [Lazy loading](lazy-loading.md#the-loader-and-content-security-policy) |

## Styles and scripts

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `orbit generate` (and `orbit check`) also run esbuild, in memory, to fill `orbit_gen/manifest`, so `generate` and `build` write the same `orbit_gen/`; bundling errors fail them too | coordinator (simplest; keeps the committed-`orbit_gen/` CI check working, about 8 ms) | An empty manifest from `generate` | [CLI](reference/cli.md#orbit-generate) |
| Dev bundles aren't minified | Astro (Vite) | Minify in dev too | [Styles and scripts](styles-and-scripts.md) |
| Source map sources are root-relative `/src/…` paths, with the `.orbit` file as their content; CSS columns after an inserted `[data-o-…]` can be off by a few, lines are exact | Astro (Vite) | Paths relative to the map file | [Styles and scripts](styles-and-scripts.md#source-maps) |
| With `"sourcemap": true`, the `.map` files are in `dist/client/_orbit/` and served if requested (not linked); delete them before deploying if your sources are private | user (unlinked maps under `/_orbit/`) | Write them outside `dist/client` | [Deploy](deploy.md) |
| `orbit build --verbose` prints to stdout in Vite's format (kB = 1000 bytes, 2 decimals, gzip at the default level) | Astro (Vite) | stderr, like `go build -v` | [CLI](reference/cli.md#orbit-build) |
| Fonts and images imported from scripts or CSS (woff, woff2, ttf, otf, eot, png, jpg, jpeg, gif, svg, webp, avif, ico, bmp) are copied, hashed, into `/_orbit/` | Astro | esbuild's default "no loader" error | [Styles and scripts](styles-and-scripts.md) |
| In a component, its styles come before the CSS its scripts import | simplest | Script CSS first | [Styles and scripts](styles-and-scripts.md) |
| A page with only styles gets a CSS entry, not an empty JS one; a JS bundle with no code (type-only scripts) is dropped with its `<script>` tag | simplest (PLAN: no empty entries) | Always emit both | [Styles and scripts](styles-and-scripts.md) |
| esbuild errors without a location print as `esbuild: …`; esbuild warnings (including CSS syntax warnings) are printed as Orbit warnings and don't fail the build | simplest | Fail on warnings | — |
| Bundle names come from the page path (`blog-slug-HASH.js`), made unique on collision | simplest | One name per page file hash | — |
| The scope attribute (`data-o-xxxxxxxx`) is written last on each element, after spreads | Astro | First | [Styles and scripts](styles-and-scripts.md) |
| Elements that never get the scope attribute: `head`, `title`, `meta`, `link`, `base`, `noscript`, `font`, `frame`, `frameset`, `noframes`, and `is:inline` scripts and styles (`html` and `body` do get it) | Astro (its compiler's `NeverScopedElements`, checked in its source) | Scope every element | [Styles and scripts](styles-and-scripts.md) |
| The selectors `html`, `body` and `:root` are never scoped | Astro (checked in its source) | Scope them | [Styles and scripts](styles-and-scripts.md) |
| Only the last compound of a selector is scoped (`.a .b` → `.a .b[data-o-…]`); Astro scopes every compound | PLAN (earlier design) | Scope every compound, like Astro | [Styles and scripts](styles-and-scripts.md) |
| Scoped selectors use the attribute selector `[data-o-…]` (+1 attribute specificity), not `:where([data-o-…])`, so scoped rules beat a layout's global element rules whatever the bundle order | user (dogfood checkpoint; Astro's default `scopedStyleStrategy: "attribute"`) | `:where()` (no added specificity); a `scopedStyleStrategy` option | [Styles and scripts](styles-and-scripts.md#how-scoping-works) |
| Within a compound, the scope goes before the first pseudo-class or pseudo-element (`a:hover` → `a[data-o-…]:hover`, `:is(.a):hover` → `[data-o-…]:is(.a):hover`), else at its end; Astro's printer also puts it right after the type, id or first class (`a.b` → `a[…].b`), which matches the same elements | Astro (its compiler's `printCompoundSelector`, checked in its source) for pseudo-classes; simplest for the rest | Copy Astro's exact position | [Styles and scripts](styles-and-scripts.md#how-scoping-works) |
| Rules inside `@media`, `@supports`, `@layer`, `@container`, `@scope`, `@starting-style` and `@document` are scoped; every other at-rule body (`@keyframes`, `@font-face`, `@page`, `@property`, unknown ones) is copied unchanged | simplest (CSS specs: only rule-list bodies hold selectors) | Scope inside unknown at-rules | [Styles and scripts](styles-and-scripts.md) |
| Nested CSS rules are scoped like top-level ones, except that a compound holding `&` isn't scoped again | simplest (consistent with the last-compound rule) | Astro's rule: nested selectors without `&` aren't scoped | [Styles and scripts](styles-and-scripts.md) |
| A compound holding a `:global(…)` counts as global; inside `:is()` it's just unwrapped; a bare `:global` or empty `:global()` is an error | simplest | — | [Styles and scripts](styles-and-scripts.md) |
| CSS syntax errors in a `<style>` (unbalanced brackets, unclosed comments or strings, a selector without `{`, stray combinators) fail the build at the `.orbit` position | simplest | Pass the CSS through to esbuild | [Styles and scripts](styles-and-scripts.md) |
| `orbit check` vets only `orbit_gen/...`, not your own Go packages (run `go vet ./...` for those) | Astro (`astro check` checks only its own files) | Also vet the server entry | [CLI](reference/cli.md#orbit-check) |
| `orbit check` writes `orbit_gen/`, like `orbit generate` | simplest (PLAN: "generate, then go vet") | Check from memory only | [CLI](reference/cli.md#orbit-check) |
| Extracted scripts go to `.orbit/check/<path>.orbit.<n>.ts` with `rootDirs`, so relative imports resolve from the `.orbit` file's directory; each ends with `export {}` so scripts are modules, as esbuild bundles them | simplest | `moduleDetection: "force"` (also changes your own `.ts` files) | [Styles and scripts](styles-and-scripts.md) |
| `bun check` always gets `--no-script`, so a `package.json` `check` script can't replace the type checker | simplest | Let a script named `check` run | — |
| Bun's summary lines are dropped, so a clean `orbit check` prints nothing | Go stdlib (`go vet`) | Pass them through | [CLI](reference/cli.md#orbit-check) |
| TypeScript columns are printed as byte columns (`path:L:C: error TSxxxx: …`), like Go's, also for your own `.ts` files | Go stdlib | UTF-16 columns, as tsc prints them | [CLI](reference/cli.md#orbit-check) |
| Without a root `tsconfig.json`, the generated config uses the scaffold's defaults | simplest | Fail, asking for a tsconfig | [CLI](reference/cli.md#orbit-check) |
| Nothing to type-check (no inline script, no `.ts` under `src/`) skips the TypeScript half silently, with no Bun notice | simplest | Always run it | — |
| The Bun notices read `orbit check: skipping TypeScript type checking: bun not found (1.4.4 or later needed); install it with: curl -fsSL https://bun.sh/install \| bash` and `… bun X is older than 1.4.4; upgrade it with: bun upgrade` | simplest | — | [Troubleshooting](troubleshooting.md) |
| The extracted tree is synced incrementally (changed files only, stale ones removed) | simplest | Rewrite everything each run | — |
| The generated check tsconfig merges your root `tsconfig.json`'s `include` and `exclude` (rewritten for `.orbit/check/`); globs from a config it `extends` aren't seen | simplest | Replace them with `src/**/*.ts` | [CLI](reference/cli.md#orbit-check) |
| Without a root tsconfig, the defaults include `allowImportingTsExtensions`, so `import "./x.ts"` type-checks as it bundles | Astro (its base tsconfig sets it) | Leave it off | [Styles and scripts](styles-and-scripts.md) |
| A `::pseudo-element` keeps the scope before it; with `:global(…)`, the scope goes on the nearest compound outside it | Astro | Scope the last compound regardless | [Styles and scripts](styles-and-scripts.md#how-scoping-works), [`:global`](styles-and-scripts.md#escaping-the-scope-global) |
| Page stylesheet order: a component's imports first, in import order, then its own styles | simplest (module evaluation order) | Source order of `<style>` tags | [Styles and scripts](styles-and-scripts.md#global-styles-isglobal) |
| `is:inline` is stripped from the emitted tag | Astro | Keep it | [Styles and scripts](styles-and-scripts.md#which-scripts-orbit-processes) |
| CSS imported from a script joins the page stylesheet | coordinator (esbuild default, and Astro's behavior) | A build error suggesting a `<link>` or `public/` | [Styles and scripts](styles-and-scripts.md#importing-css-from-a-script) |
| The scaffolded `tsconfig.json` follows Astro's `strict` preset, with an empty `src/env.d.ts` | Astro | A minimal `tsconfig.json` | [Styles and scripts](styles-and-scripts.md#tsconfigjson) |

## Testing

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `orbittest` reaches the runtime through a module-internal bridge (`internal/testhook`), so package `orbit` gains no exported test API | simplest | Exported helpers in package `orbit` | — |
| `WithSlot` with the same name twice: the later one wins | Go stdlib (like `WithParam`) | Fail the test | [Testing](testing.md#slots-withslot) |
| `WithLocals` and `WithRequest` can come in any order (locals are attached to the chosen request) | simplest | Apply options strictly in order | [Testing](testing.md#locals-withlocals) |
| Golden names may contain `/` (`pages/post`), and `-update` creates the directories; empty, absolute, `..`, `.`, empty elements, a trailing `/` and backslashes fail | simplest | Flat names only | [Testing](testing.md#snapshot-tests) |
| A snapshot mismatch shows the first difference with line, column and about 60 bytes from each side, not a line diff (a compressed page is often one line) | simplest | A line diff | [Testing](testing.md#snapshot-tests) |
| `Snapshot` normalizes `/_orbit/<name>-<8 base32 chars>.<ext>` to `/_orbit/<name>-*.<ext>` and `/_orbit/island/<16 hex>/` to `/_orbit/island/*/`, only in what it compares and stores | user (stable snapshots) | Compare raw HTML | [Testing](testing.md#hashed-asset-urls) |
| `Get` with a path that doesn't start with `/` fails the test (instead of `httptest.NewRequest` panicking) | simplest | Panic | [Testing](testing.md#testing-through-http-with-get) |
| `Render` of a page that returns `ErrNotFound` or a redirect fails the test; use `Get` to test those | PLAN (render errors fail) | Return the error to the caller | [Testing](testing.md#a-first-component-test) |
| Under `go test` (`testing.Testing()`), `orbittest.Render` and `app.New()` (so `orbittest.Get`) seal `server:defer` placeholders with a fixed test island key and build ID `0000000000000000`, unless `ORBIT_KEY`/`WithKey` is set, so snapshots are stable | coordinator (simplest) | The normal key resolution; an `orbittest.WithKey` option | [Testing](testing.md#testing-serverdefer-components) |
| `orbittest.Render` uses `GET /` by default and does no head injection | simplest (like `RenderComponent`) | Inject the page's assets | [Testing](testing.md#a-first-component-test), [Runtime API](reference/runtime-api.md#orbittest) |
| `orbittest.Get` accepts only `WithLocals` and `WithRequest`; any option that doesn't apply fails the test | simplest | Ignore it silently | [Testing](testing.md#options) |
| `orbittest.WithParams(orbit.Params)` is replaced by `WithParam(name, value string)`: one call per param, a later call for the same name replaces the value, `Render` only | Go stdlib (`http.Request.SetPathValue(name, value)`) | `WithParams(map[string]string)`; a variadic name/value list | [Testing](testing.md#route-params-withparam) |
| A missing golden file fails the test, with a hint; `orbittest` owns the `-update` flag | simplest (Go golden-file convention) | Create the file on first run; a differently named flag | [Testing](testing.md#snapshot-tests) |

## Deploy and Docker

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Dockerfile: `WORKDIR /src`, a `go mod download` layer, the binary at `/server`, `USER nonroot:nonroot` | simplest | A single `COPY . .` stage | [Deploy](deploy.md#docker) |
| `.dockerignore` keeps `node_modules`, since the image has no Node or Bun to install it | simplest | Ignore it and install packages in the image | [Deploy](deploy.md#docker) |

## Benchmarks

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The head-to-head load generator is a small stdlib Go tool in `bench/load` (closed loop, keep-alive, latency percentiles, RSS sampling), with server and load generator pinned to separate CPUs | coordinator (simplest; no tool to install) | Install `oha` or `wrk` | [Performance](performance.md) |
| CPU pinning splits physical cores (server 0–3,8–11; load generator 4–7,12–15), since CPUs n and n+8 are one core's two threads | simplest correct | 0–7 / 8–15, which shares cores | `bench/README.md` |
| Astro gets the same 8 server CPUs as Orbit at all cores (room for its GC and libuv threads); Orbit at `GOMAXPROCS=1` gets 1 CPU | simplest, conservative for Orbit | Pin Node to 1 CPU too | [Performance](performance.md#results) |
| The deferred scenario's iteration is the page, then its island, on the same connections, as the browser's loader does | simplest | Separate page-only and island-only runs | [Performance](performance.md#results) |
| Latency percentiles are exact (all samples sorted), not an HDR histogram | simplest | HDR histogram | `bench/README.md` |
| Both sites must serve the same visible text before a run (scope attributes and whitespace legitimately differ) | simplest | Byte comparison | `bench/README.md` |
| Astro's stylesheets are linked, not inlined (`build.inlineStylesheets: 'never'`), as Orbit links its CSS | simplest (same work on both sides) | Astro's default inlining | `bench/astro-site/astro.config.mjs` |
| `bench/go` doesn't commit its generated `orbit_gen/`; `go generate ./...` creates it | user decision (`orbit_gen/` is gitignored) | Commit it | `bench/go/README.md` |
| `bench/go` compares the three engines after whitespace normalization and ignoring the doctype's case | simplest | An HTML-parser comparison | `bench/go/README.md` |
| String and number attribute values are written as pieces (`Raw` + `String`/`Int` + `Raw`); `uintptr` stays on `Attr` | coordinator (byte-identical, measured −17% on the cards page) | Keep `Attr`/`AttrString` | [Performance](performance.md#expressions-are-written-by-type-without-reflection) |
| Typed string URL attribute values are written as pieces too (`Raw` + `URL` + `Raw`); `orbit.URL` keeps `String` pieces, unfiltered | user decision (milestone 9) | Keep `AttrURL` | [Runtime API](reference/runtime-api.md#attribute-values) |
| `locals` is declared (`LocalsOf`, the `src/locals` import) only in components whose body or template has an unresolved `locals`; also when that code doesn't parse, and always in tolerant (LSP) mode so gopls can complete it | user decision (lazy locals); safe side and simplest for the details | Declare it everywhere (a context lookup and `new(L)` per request) | [Performance](performance.md#routing-and-locals) |
| The loader config leaves out `"lazy"` on pages without lazy entries | simplest | Always write `"lazy":{}` | — |
| The dev-loop benchmark leaves the compiled dev manifest empty (dev reads `manifest.json`), so a CSS edit needs no Go rebuild | Plan §5 | Compile the dev bundle's hashes | `bench/devloop/README.md` |

## Dev server

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The dev proxy listens on localhost; `orbit dev --host` listens on all interfaces | coordinator (Astro) | All interfaces by default | [Dev server](dev-server.md#start-it), [CLI](reference/cli.md#orbit-dev) |
| A new external import in a component reloads export data (`go list -export`) and re-checks everything, like a `go.mod` edit | simplest | Load only the new package | [Dev server](dev-server.md#what-a-change-rebuilds) |
| When the port is taken, `orbit dev` tries the next nine and prints which one it uses | Astro (Vite's port fallback) | Fail with `address already in use` | [Dev server](dev-server.md#start-it), [CLI](reference/cli.md#orbit-dev) |
| Without `--host`, the proxy listens on `127.0.0.1` and, when available, `[::1]` (both resolve from `localhost`) | simplest | `127.0.0.1` only; whatever `localhost` resolves to first | [CLI](reference/cli.md#orbit-dev) |
| The startup banner prints `Local` and `Network` URLs (`use --host to expose` without `--host`) | Astro | One `listening on` line | [Dev server](dev-server.md#start-it) |
| A change that is only `watch` files (and `public/` files) restarts the running server's binary without regenerating or `go build`; with no server running, it builds one as any change does. Mixed with other changes, the server is restarted even when the rest needed no Go rebuild | user (dogfood checkpoint) for the restart; simplest for the rest | Rebuild as for a `.go` edit | [Dev server](dev-server.md#what-a-change-rebuilds) |
| `orbit dev` watches each `watch` pattern's leading literal directory, recursively only when the pattern can match deeper, skipping the usual skipped directories below it; a missing leading directory is a warning at startup and isn't watched later | simplest | Watch the nearest existing parent; an error | [Configuration](configuration.md#watch) |
| Watched paths that are also other kinds (`src/`, `.go`, `public/`) keep their usual rebuild; `orbit_gen/`, `dist/` and `.orbit/` never match | simplest (no rebuild loop) | `watch` overrides the kind | [Configuration](configuration.md#watch) |
| Editing `watch` in `orbit.json` during `orbit dev` applies the new patterns after the rebuild; watches for removed patterns stay but their events are ignored | simplest | Restart orbit dev to apply | — |
| The proxy keeps the browser's `Host` header and adds no `X-Forwarded-*` headers | simplest (the app sees what it sees in prod, minus the client address) | `X-Forwarded-For`/`-Host`/`-Proto` as `httputil.ProxyRequest.SetXForwarded` | [Dev server](dev-server.md#your-server-runs-in-dev-too) |
| The reload script is `<script src="/__orbit/reload.js" async></script>` before the last `</body>` (case-insensitive) | simplest | An inline script (needs a CSP hash) | [Dev server](dev-server.md#live-reload) |
| HEAD responses and `text/html` without `</body>` pass through unchanged; `text/html` responses are read whole before injecting, other responses stream (`FlushInterval: -1`) | simplest | Inject while streaming | [Dev server](dev-server.md#live-reload) |
| Page requests (GET/HEAD with `Accept: text/html`) get the overlay while a build is broken; other requests reach the old server, or get a `503` with the error text when none is running | simplest | Overlay for every request | [Dev server](dev-server.md#errors) |
| The overlay page answers with status 500 and `Cache-Control: no-store` | Go stdlib (an error page is a 5xx) | 200, as Vite's overlay is injected into a 200 page | [Dev server](dev-server.md#errors) |
| A failed build, a failed start and a server that exits on its own all send a `reload` event, so open pages switch to the overlay at once | Astro (its overlay appears in the open page) | Show the overlay only on the next navigation | [Dev server](dev-server.md#live-reload) |
| A request whose connection to the old server is refused during a swap waits for the new server and is sent again (only when it has no body, or a replayable one) | simplest (requests during a swap wait, PLAN.md §7) | A `502` | — |
| A started server must accept connections within 10 s, else it's reported as a failed start | simplest | Wait forever; 30 s | [Troubleshooting](troubleshooting.md#the-browser-shows-an-error-overlay) |
| A server that exits on its own is shown in the overlay with its stderr until the next change rebuilds and restarts it; it isn't restarted automatically | simplest (no restart loop on a crash at startup) | Restart it at once, with backoff | [Dev server](dev-server.md#errors) |
| Watched beyond PLAN.md §7's list: `orbit.json`, `go.mod`/`go.sum`, `tsconfig.json`, `package.json`. Ignored: `node_modules/`, dot directories, `testdata/`, `_`-prefixed directories outside `src/`, `_test.go` files, editor swap/backup files, and files created and deleted within one debounce (`sed -i`, editors' atomic saves) | Go stdlib (the `go` command ignores `testdata`, `_` and `.` directories) | Watch only `src/`, `public/` and `.go` files | [Dev server](dev-server.md#what-a-change-rebuilds) |
| Requests wait from the first file event, not only once the 50 ms debounce has passed | simplest (a quick refresh after saving never sees the old page) | Wait only during the rebuild itself | [Dev server](dev-server.md#what-a-change-rebuilds) |
| esbuild is skipped when no processed block's content, kind or line:col changed and no page's asset graph changed; a template edit that moves a `<style>` to another line rebuilds the bundles (their source maps point at `.orbit` lines) | simplest | Ignore positions (stale source-map lines) | [Dev server](dev-server.md#what-a-change-rebuilds) |
| A change to `src/` files other than `.orbit` (`.ts`, `.css`, images) rebuilds the bundles from the last input without reloading the project; a path under `src/` without an extension is treated like a `.orbit` change (it may be a removed or renamed directory) | simplest | Reload the project on every change | — |
| Dev server binaries are built to `.orbit/devserver/server-0` and `server-1` alternately, so `go build` never overwrites the running binary | simplest | A new temp file per build | [Project structure](project-structure.md#orbit-cache) |
| The terminal gets one line per change (`orbit dev: <file>: server restarted in 250ms`, `…: CSS and JS rebuilt in 3ms`, `…: reloaded`), plus errors and warnings | simplest | Astro's per-file update lines with timestamps | [Dev server](dev-server.md#errors) |
| `--port` must be 1–65535; anything else is a usage error (exit 2) | Go stdlib (`flag` values) | Accept 0 as "random port" | [CLI](reference/cli.md#orbit-dev) |
| Each restarted dev app gets a new random build ID; island tokens still open, since the per-session `ORBIT_KEY` is shared and the build ID comes from the URL | simplest | A fixed per-session build ID passed with `-X`, so island URLs also stay identical across restarts | [Dev server](dev-server.md) |
| `orbit dev` compiles the generated packages without inlining (`-gcflags=./orbit_gen/...=-l -dwarf=false`) unless `GOFLAGS` has its own `-gcflags` | user (measured −10–14% on the slowest rebuild) | Same compile flags as `orbit build` | [Dev server](dev-server.md) |
| `orbit dev`'s `go build` uses `-ldflags=-s -w` (merged with a `GOFLAGS` `-ldflags`), `-gcflags=all=-dwarf=false` and `-buildvcs=false`, and runs without `GOGC`/`GOMEMLIMIT` (the server keeps them) | user (dev-loop speed-ups) | `-ldflags=-w` only; keep the user's `GOGC` | [Dev server](dev-server.md) |
| The resident type checker's `go list -export` in `orbit dev` also runs with `GOGC`/`GOMEMLIMIT` cleared, like `go build` | simplest (same rule as `go build`; it compiles the edited package on `.go` edits) | keep the user's values for `go list` | [Dev server](dev-server.md) |
| A `-buildvcs` in `GOFLAGS` replaces Orbit's `-buildvcs=false`, as a `-gcflags` there replaces Orbit's `-gcflags` | Go stdlib (a command-line flag overrides `GOFLAGS`, so Orbit stays out of the way when you set one) | Always pass `-buildvcs=false` | [Dev server](dev-server.md) |
| `orbit dev`'s type checker runs `go list -export` with the same `-gcflags` and `-buildvcs` as its `go build`, so both share the compiled packages in the build cache (with different flags a `.go` edit compiled the edited package twice) | simplest | Default flags for `go list` | — |

## Editor support

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The VS Code extension is published to the Marketplace and Open VSX as `orbit-z.orbit` | coordinator | A `.vsix` on GitHub releases only; Marketplace only | [Editor setup](editor-setup.md#vs-code) |
| gopls is chosen with the `goplsPath` initialization option, set by the VS Code setting `orbit.goplsPath` | simplest | Only `PATH` | [Editor setup](editor-setup.md#how-gopls-is-found) |
| The docs advise against a gopls `directoryFilters` entry for `orbit_gen/`, because Orbit's gopls gets the same settings | simplest | Strip `directoryFilters` from the settings Orbit forwards | [Editor setup](editor-setup.md#the-go-extensions-errors-in-orbit_gen) |
| The LSP always generates every file in memory and uses its own source maps; it never reads maps for `orbit_gen/` files, and none are written to disk | coordinator (simplest) | Sidecar map files next to the generated code | — |
| Tolerant (editor) code is never type-checked: every expression uses `Expr`. A separate typed check compiles the current sources the way `orbit generate` does (strict mode, one `go list -export`, the go/types pass), at startup and after `orbit_gen/` changes on disk, only to find the generated files that equal the disk | coordinator (PLAN §9: overlays only where `orbit_gen/` is missing or stale; the check costs ~70 ms at 200 files with a warm build cache) | Type-check the tolerant code too; compare tolerant code with disk (no `render.go` ever matched) | — |
| A generated file whose typed output equals the disk is read by gopls from disk and mapped through the typed output's source map; any other file (edited, with errors, not yet generated, or stale) is sent as an overlay of its tolerant code. A file's first change makes it an overlay | coordinator (gopls must see exactly the code the source map describes) | Overlays for every file whose tolerant code differs from disk | [Editor setup](editor-setup.md#startup-and-orbit_gen) |
| Startup waits up to 5 s for the typed check before sending overlays for everything; a check that finishes later still closes the overlays it can. Requests wait for that decision before mapping positions | simplest (a cold build cache can make `go list -export` slow; mapping must use the content gopls reads) | No limit; never wait | — |
| After a watched-file event for `orbit_gen/`, the typed check runs again 300 ms after the last event (one at a time), if gopls has overlays | simplest (debounce: `orbit generate` rewrites many files at once) | Check on every event; only at startup | — |
| With 16 or more overlay-only generated files (no file on disk), gopls starts on an empty placeholder folder (a temporary directory, removed after the readiness barrier) and gets the project's folder in its place once every overlay is open, so its one workspace load sees them all; with fewer, gopls starts on the project's folder and loads each lazily. If the typed check alone finds that many (no placeholder), the folder is removed and re-added after the overlays | simplest (measured at 200 files: a lazy load ~25 ms each; with the project's folder open, each `didOpen` costs gopls time proportional to the overlays already open, and the folder re-add waited for that: 1.4–3.8 s to the first hover; with the placeholder ~0.33 s) | Always lazy (9.7 s at 200 files); remove and re-add the project's folder (1.4–3.8 s); load the open documents' packages first | — |
| Orbit's gopls gets `zeroConfig: false` unless the editor's `gopls` settings set it | simplest (it sees only the project's module; zero-config view selection walks every open file's directories on each `didOpen`: ~1 s of 1.6 s at 400 overlays, and 0.70 s → 0.62 s to the first hover, 400 → 240 MB, with 200 stale overlays) | Leave it on | [Editor setup](editor-setup.md#how-gopls-is-found) |
| The readiness barrier asks gopls which `orbit_gen/` packages it has loaded (`gopls.packages`) and sends a `documentHighlight` only for the missing overlay-only ones, the open documents' first, 4 at a time; only those are re-sent afterwards for their diagnostics. Its progress isn't forwarded | simplest (one request per overlay kept each overlay's type-checked package with its own copy of the imports in gopls: 3 GB at 200 files) | One `documentHighlight` per generated file, all in parallel | — |
| Diagnostics are published for every `.orbit` file of the project, open or not; gopls's diagnostics for your own `.go` files and `go.mod` aren't forwarded, since the editor's Go support reports them (forwarding showed them twice) | Astro (its language server reports only `.astro` files) | Forward them as they are | [Editor setup](editor-setup.md#what-completion-covers) |
| Orbit's diagnostics have source `orbit`; gopls's keep theirs (`compiler`, …) | Go stdlib (gopls's convention) | One source for all | — |
| Hover on a component tag (`<Card`) is answered natively: the component's file, its props with types and docs, and its slots | simplest (gopls would describe the generated `Render` function) | No hover on tags | — |
| gopls's hover text is cleaned: `line …:L:C` directive text (a frontmatter statement's inline directive shows as the variable's doc) and pkg.go.dev links to `orbit_gen` packages are removed | simplest (no codegen change) | Own-line directives before body statements too | — |
| Requests that arrive before gopls has started wait for it (hover, definition); references also wait for the readiness barrier. gopls's errors are logged and answered as "no result" | simplest | Answer "no result" until gopls is ready | — |
| The text-document sync is incremental (`change: 2`); gopls always gets the whole generated file | simplest | Full sync from the editor | — |
| Definition on `props` goes to the component's `Props` declaration | simplest (same rule as `params`) | No result | — |
| Prop completion asks gopls for the fields of a `Props{}` literal in a small overlay-only scratch package per component (`orbit_gen/orbit__lsp/…`, never written), opened once and never changed, rather than a scratch function added to the page's own overlay | simplest (no edit-and-restore of the user's overlay, no diagnostics churn; gopls sees the current `Props` through the overlays) | A scratch function in the impl overlay, added and removed per request | — |
| An accepted prop inserts `post={…}` for every prop, `bool` ones included; without snippet support, `post=` | simplest (PLAN §9) | A bare `featured` for `bool` props | [Editor setup](editor-setup.md#what-completion-covers) |
| An accepted HTML attribute inserts `name="…"`, or the bare name for boolean attributes (`disabled`); directives insert `set:html={…}`, `slot="…"`, `client:media="…"`, or the bare name (`is:inline`, `server:defer`, `client:load`) | Astro (its HTML completion inserts quoted values and bare boolean attributes) | Always the bare name | — |
| A tag-name completion inserts only the name; the editor closes the tag | Astro | Insert the whole tag | — |
| Tag names: imported components, then the project's other components, then `<Fragment>` and the HTML elements; `<Self>` isn't offered | simplest | Offer `<Self>` in components | [Editor setup](editor-setup.md#what-completion-covers) |
| The import added for an unimported component uses the file's base name as the alias (in Go field-name form, `my-card` → `MyCard`), numbered if taken (`Card2`), and a path relative to the file (`./`, `../`) | simplest | Ask for the alias | — |
| An added import (a component's, or a Go package's from gopls) goes into the frontmatter's last `import (…)` group, else after its last single import, else on a new line at the top of the frontmatter, which is created if the file has none | simplest (gopls's own placement can't be mapped: the generated import block is glue) | gopls's position when it maps | — |
| gopls completions of the generated code's own names (`orbit__w`, `Orbit__Render`, …) and ones that import an `orbit_gen` package are dropped | simplest (user code can't use them) | Pass everything through | — |
| In Go regions, completion answers only when invoked or triggered by `.` (gopls's trigger) or by `{` opening a template expression; the other trigger characters (space, `<`, `:`, quotes) are for the template, so typing a space in the frontmatter opens no list | simplest | Forward every trigger to gopls | — |
| Slot-name completion offers the target component's slot names in order of appearance, `default` included | simplest | Omit `default` | [Editor setup](editor-setup.md#what-completion-covers) |
| The HTML data is a hand-curated subset of the WHATWG element index (current elements, their attributes, the global attributes and a few ARIA ones; no event handlers, which Orbit warns about), with Orbit's own descriptions | simplest (no license notice needed; small) | Derive it from vscode-html-languageservice's data | — |
| When gopls answers a `Props{}` completion with no field but the index lists some (it couldn't load the scratch package), the index's fields are used; when gopls is off or fails, too | simplest | Show nothing | — |
| Completion on an empty frontmatter line returns nothing until a character is typed (no source-map segment covers the empty line); editors ask again as you type, so identifiers complete from the first letter | simplest (MVP) | Map empty frontmatter lines to the render body | — |
| Rename keeps a call site's attribute spelling: the field's own spelling (`IsOpen`) stays the Go name, kebab-case and snake_case stay lowercase with their separator (`is-closed`, `is_closed`), an all-lowercase spelling of a multi-word field stays all lowercase (`isopen` → `isclosed`); anything else, including camelCase and unusual mixes (`ISOPEN`), becomes the lowerCamel spelling completion inserts (`isClosed`). Words split at case changes and before the last capital of a run (`HTTPServer` → `http-server`) | simplest | Keep unusual spellings' case pattern; always lowerCamel | [Editor setup](editor-setup.md#rename) |
| A rename started on a call site's attribute takes an attribute spelling: the field becomes `FieldName(newName)` (`is-closed` → `IsClosed`), and that attribute gets the new name as typed; one that makes no exported Go identifier is refused | simplest (same rule as attribute → field matching, §2.3) | Take a Go field name there | [Editor setup](editor-setup.md#rename) |
| A shorthand `{post}` stays meaningful: renaming the field gives `article={post}`, renaming the variable gives `post={value}` | simplest | Rename both the attribute and the variable | [Editor setup](editor-setup.md#rename) |
| A refused rename or prepareRename is an LSP `RequestFailed` error whose message the editor shows; a position with nothing to rename (markup, an unmapped spot) gets `null` | simplest (the editor tells the user why) | `null` for everything | — |
| prepareRename and rename ask gopls for the identifier's definition first and refuse names the generated code declares (`props`, `ctx`, `slots`, `locals`, `params`, `orbit__…`), a page's `Params` fields (with the "rename the file instead" message) and the `Props` type | simplest (one rule for all glue) | A list of glue names | [Editor setup](editor-setup.md#rename) |
| gopls's own rename errors (a conflict, a standard-library name) are passed through, with generated-file positions rewritten to their `.orbit` position or "generated code" | simplest | A generic "can't rename" | — |
| New names are checked before gopls runs: a valid Go identifier, not `orbit__…` (the compiler's reserved-name error), an exported name for a `Props` field and an uppercase first letter for an alias (the compiler's messages), and an alias not already an import name of the file | simplest (the compiler would report them after the rename) | Let the rename produce a broken file | [Editor setup](editor-setup.md#rename) |
| An alias rename is native for the import and its start and end tags; its Go uses (`Card.Props` in the frontmatter) are renamed by gopls in each generated file that imports it, with the generated code's own uses dropped | simplest | Tags only | [Editor setup](editor-setup.md#rename) |
| A Go import in both generated files (used by `Props` and by the body) is renamed in both: gopls renames a package name per file, so Orbit asks again for the other file | simplest | Rename it in one file and break the other | — |
| Edits of the same `.orbit` range from two generated files are merged; two different edits of one range reject the rename | simplest | — | — |
| Edits for generated files that belong to no `.orbit` file (stale `orbit_gen/` leftovers) and for the LSP's scratch files are skipped | simplest | Refuse the rename | — |
| `documentChanges` carry the editor's version of open `.orbit` documents and `null` for the others and for your `.go` files | simplest (LSP: `null` means "not versioned") | gopls's versions | — |
| A rename whose generated files changed while gopls computed it (you typed meanwhile) is refused with "try again" | simplest | Map against the new files | — |
| `renameProvider` is `{prepareProvider: true}` only for an editor that announces `prepareSupport`, else `true` | simplest (what the LSP spec asks) | Always `prepareProvider` | — |
| VS Code language configuration: block comment `<!-- -->` and no line comment for the template (the frontmatter and `{…}` are embedded Go, so VS Code uses Go's `//` there); brackets `<!-- -->`, `<>`, `{}`, `()`, `[]`; auto-closing `{}`, `[]`, `()`, quotes, backticks, `<!-- -->` and `/* */` | Astro (its language configuration) | `//` as the template's line comment | [Editor setup](editor-setup.md#vs-code) |
| The grammar's frontmatter is `\A---` to the next `^---` line, embedded `source.go`, as Astro's grammar does; a `---` line inside an unclosed Go construct (a raw string) doesn't end it | Astro | A `begin`/`while` rule | — |
| `{…}` in the template matches Go strings, raw strings, runes, comments and nested braces in its own rules before `source.go`, so a `}` inside them doesn't end the expression (the compiler's Go-aware scanner) | simplest | Rely on `source.go` alone (its rules for these differ by version) | — |
| Components get `support.class.component.orbit` (`.self`/`.fragment` for `<Self>`/`<Fragment>`), directives `entity.other.attribute-name.directive.orbit` with namespace/localname parts, control keywords `keyword.control.*.orbit`; HTML parts keep `text.html.basic`'s scope names | Astro (same scope families) | HTML scopes for everything | — |
| `<script>` is TypeScript whatever its other attributes (`is:inline` included); `type="application/json"` and `application/ld+json` on the same line as `<script` make it JSON | simplest | Detect the type on later lines too | — |
| The grammar snapshot tests load only Orbit's grammar (embedded Go/TS/CSS/HTML regions show as their `meta.embedded.*` scope), so they don't depend on VS Code's bundled grammars | simplest | Vendor VS Code's grammars into the tests | — |
| One `orbit lsp` per window, started in the project root (of the active `.orbit` file, else the first workspace folder in a project) so a global `orbit` hands over to the pinned CLI; in a multi-root workspace with several Orbit projects only the first is served | simplest (the server handles one project) | One client per workspace folder | [Editor setup](editor-setup.md#vs-code) |
| The extension starts when a `.orbit` file opens or a command runs (VS Code's implicit activation), not when the workspace merely contains `.orbit` files | simplest | `workspaceContains:**/*.orbit` | — |
| `orbit.path`: a bare name is looked up on `PATH`, `~/` is expanded, a relative path is resolved against the first workspace folder; a missing binary shows an error with the `go install` command and buttons to the install docs and the setting | simplest | Let the client's spawn error speak | [Editor setup](editor-setup.md#vs-code) |
| With the default `orbit.path` and no `orbit` on `PATH`, the extension runs `go tool orbit` (server and commands) when the project's `go.mod` has the `tool …/cmd/orbit` directive | simplest (the same order as the gopls lookup: `PATH`, then `go tool`) | Only `PATH`, with an error | [Editor setup](editor-setup.md#vs-code) |
| Changing `orbit.path` or `orbit.goplsPath` restarts the language server | simplest | Ask to restart | — |
| `Run orbit generate`/`Run orbit doctor` print to an **Orbit** output channel (doctor's is shown); the language server's own log is the separate **Orbit Language Server** channel. Doctor reports success when it exits 0 (⚠ warnings included) | simplest | One channel for both | [Editor setup](editor-setup.md#vs-code) |
| `engines.vscode` is `^1.104.0` (September 2025), with `@types/vscode` 1.104.0 | simplest (at least a year old; vscode-languageclient 10 needs 1.91) | An older floor | — |
| The extension's `bunfig.toml` sets `minimumReleaseAge` to two weeks, so transitive dependencies are at least two weeks old too | simplest (CLAUDE.md's pinning rule) | Pin only direct dependencies | — |

## Docs examples

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Only code blocks tagged `file=PATH` are built; untagged blocks are fragments and are ignored; a `PATH` starting with `~` or `/` (editor config) is skipped | simplest | Build every `go`/`orbit` block, wrapping fragments | `internal/docstest/docs_test.go` |
| Each docs page is one project: the `orbit new` starter (module `example.com/blog`), hidden per-page stubs, then the page's tagged blocks; one page tagging the same `PATH` twice is an error | simplest | Later blocks replace earlier ones; one project for all pages | `internal/docstest/docs_test.go` |
| A project passes when `orbit check` (TypeScript half only with Bun), `go vet ./...`, `go build ./...` and `go test ./...` pass; the testing page first runs `go test ./internal/views_test -update`, as it tells the reader to | simplest (what a reader would run) | Build only, no `go test` | `internal/docstest/docs_test.go` |
| A block whose info string has `expect-error` shows failing code: it's built with the page's other blocks, must fail, and the documented error must appear in the output; without Bun a passing build is only logged | simplest | An `expect-error="message"` attribute (quoting, no position) | `internal/docstest/docs_test.go`, [Styles and scripts](styles-and-scripts.md#type-checking-with-orbit-check) |
| The documented error is the next *untagged* fenced block: every line of it that starts with the block's `PATH:` must appear, or its first line when none does (an error reported in another file). So several `expect-error` blocks can share one error block, and one block can document several errors | simplest | Only the first line of the next block; one `expect-error` block per error block | `internal/docstest/docs_test.go` |
| Every compile-time error example on the docs pages is an `expect-error` block: the smallest complete file that triggers it, then a `text` block with Orbit's exact output. Each one builds in a project of its own, in parallel | user decision (implementation: simplest) | Keep fragments with unchecked messages | `internal/docstest/docs_test.go` |
| `expect-error at=N` is a fragment: its lines replace as many lines of the page's block for the same `PATH`, from line `N` (`at=N-M` replaces lines `N` to `M`), so a tutorial can show only the line it changes (the misspelled prop in Getting started, the deferred-component errors in Lazy loading) | simplest (keeps the page's flow, still checks the position) | Repeat the whole file | `internal/docstest/docs_test.go` |
| `expect-warning` is the same as `expect-error` for warnings: the project must build, and the documented warnings must appear | simplest | Leave warning examples unchecked | `internal/docstest/docs_test.go`, [Components](components.md#event-handler-and-style-attributes) |
| A file an error example needs but the page doesn't show (a component it uses, a package it imports) is a hidden `pageStubs` entry | simplest | Show every file on the page | `internal/docstest/docs_test.go` |
| An error example whose real output is wrong because of an Orbit bug stays in the docs with the correct output and is skipped (`skipExamples`, naming the bug) | simplest (the docs stay the spec) | Document the buggy output; leave the example untagged | `internal/docstest/docs_test.go` |
| The harness prints an `orbit check` failure as `orbit: <error>`, as the CLI does, so a documented `orbit: orbit.json:3:1: …` matches | simplest | Strip the `orbit:` prefix from the docs | `internal/docstest/docs_test.go`, [Configuration](configuration.md) |

## Example site (`examples/blog`)

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `examples/blog/go.mod` requires `v0.0.0` with `replace github.com/orbit-z/orbit => ../..`; the integration test copies the example and rewrites the replace to the repository's absolute path | simplest | An absolute path from `orbit new`; a `go.work` file | `examples/blog/README.md` |
| The integration test lives in `internal/build` (`TestExampleBlog`), next to the other end-to-end tests whose helpers it reuses | simplest | A new `internal/examples` test package with copied helpers | — |
| `LikeButton` is a custom element whose script POSTs to `/blog/{slug}/like` and swaps in the returned fragment (`outerHTML`); no htmx | simplest (no dependency; the docs' recommended custom-element pattern) | htmx `hx-post`, as in the `server.md` snippet | `examples/blog/README.md` |
| The `javascript:` URL filter is shown by an untrusted commenter website in the deferred comments, not a dedicated page | simplest (keeps the example realistic) | A `/security` demo page | `examples/blog/README.md` |
| The JSON-LD `<script>` goes into the layout's `<head>` through a named `head` slot | Astro (named slot in a layout's `<head>`) | Put it in `<body>` | `examples/blog/README.md` |
| The `theme` cookie sets `Theme` to `dark` only for the value `dark`, `light` otherwise | simplest (as in [Middleware and locals](middleware.md)) | Copy any cookie value | [Middleware and locals](middleware.md) |
| The simulated slow comment query is a package variable, `posts.CommentDelay` (200 ms), which `views_test` sets to 0 | simplest | A build tag; an environment variable | — |

## Markdown

Decided with the Markdown spec (`PLAN.md` §2.4); the choices that shape the public API (frontmatter format, how layouts receive it, what an import exposes, raw HTML, highlighting themes, link rewriting) were the user's, recorded in `PLAN.md`, not rows here. The rows after `The Orbit language server…` were decided during the implementation.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `.md`, `.markdown`, `.mdown`, `.mkdn`, `.mkd` and `.mdwn` are all Markdown | Astro | `.md` only | [Markdown](markdown.md#markdown-pages) |
| A `_` prefix keeps a Markdown file out of the routes; `404.md` is the not-found page | Astro | — | [Markdown](markdown.md#markdown-pages) |
| A bracket in a Markdown page's path (`[slug].md`) is a build error at the file | simplest (a Markdown page has no code to read `params`) | Treat it as a literal name | [Markdown](markdown.md#markdown-pages) |
| A Markdown file outside `src/pages/` is compiled only when something imports it | Astro | Compile every `.md` under `src/`, like `.orbit` files | [Markdown](markdown.md#importing-markdown-into-a-component) |
| Generated packages of a `.md` get an `_md` suffix (`pages/blog/hello_md`) | simplest (never collides with a sibling `.orbit`) | The `.orbit` naming, reporting a collision | `PLAN.md` §2.4 |
| A Markdown page without a layout renders only the Markdown's HTML | Astro | Wrap it in a minimal document | [Markdown](markdown.md#markdown-pages) |
| `layout` is relative to the `.md` file, must be an `.orbit` file inside `src/`, and isn't passed to the layout or included in `Frontmatter` | Astro (relative path) / simplest | Pass it through like other keys | [Markdown](markdown.md#frontmatter) |
| Frontmatter keys become field names with the route-param rules; two keys giving one field are an error | simplest (one naming rule) | Keep keys as written, in a map | [Markdown](markdown.md#frontmatter) |
| A frontmatter key with no value is an error | simplest | A zero or `nil` value | [Markdown](markdown.md#frontmatter) |
| YAML anchors, aliases, tags and multiple documents in frontmatter are errors | simplest | Support them | `PLAN.md` §2.4 |
| YAML dates and timestamps are `time.Time` (UTC for a bare date); a `string` field gets the text as written | Astro (YAML dates are `Date` objects) | Always strings | [Markdown](markdown.md#layouts) |
| A list mixing value types is an error in an imported file's `Frontmatter` | simplest (concrete Go types) | `[]any` | [Markdown](markdown.md#postfrontmatter) |
| A layout's `Headings []orbit.Heading` field is filled by Orbit, and `orbit.Heading` has `Depth`, `Slug`, `Text`, for h1–h6 | Astro (`headings` prop, `MarkdownHeading`) | `Level`/`ID` names; h2–h3 only | [Markdown](markdown.md#layouts) |
| Frontmatter props are built once at startup and shared by every request (read-only) | simplest (zero allocations per request) | Build them per request | [Markdown](markdown.md#layouts) |
| The Markdown content is the layout's default slot | Astro | A `Content` prop | [Markdown](markdown.md#layouts) |
| GFM (with footnotes) and smart punctuation are on by default | Astro (`gfm`, `smartypants` default `true`) | Off by default | [Markdown](markdown.md#what-you-can-write) |
| Exactly two hyphens (`--`) make an em dash; `---` and longer runs stay as written (user decision: match Astro) | Astro (retext-smartypants 6.2 `dashes: true`: only a `--` punctuation node becomes `—`; checked with `@astrojs/markdown-remark` 7.3.4) | goldmark's Typographer (`--` en dash, `---` em dash); `"oldschool"` | [Markdown](markdown.md#what-you-can-write) |
| No `#` self-links next to headings and no `{#id}` syntax | Astro | Add anchors (as `docs/site` does) | [Markdown](markdown.md#heading-ids) |
| Markdown link destinations aren't URL-filtered | simplest (static author content, like static template attributes, which aren't filtered either) | Apply the `javascript:` filter | [Markdown](markdown.md#raw-html) |
| A raw `<script>` or `<style>` in Markdown is left as written (not bundled or scoped) | Astro | Process it like a template's | [Markdown](markdown.md#raw-html) |
| Image paths are left as written; images go in `public/` | simplest (no image pipeline yet) | Hash relative images into `/_orbit/` | [Markdown](markdown.md#links-between-markdown-files) |
| `compressHTML` doesn't alter Markdown HTML | simplest (goldmark's output is already compact) | Apply the `"html"` rules | `PLAN.md` §2.4 |
| The code fence's info string after the language is ignored | Astro | Render `title=`/`file=` as a caption | [Markdown](markdown.md#code-blocks) |
| A block in a language Chroma doesn't know is escaped plain text in the same `<pre class="chroma">`, keeping `data-language` | simplest | Guess the language | [Markdown](markdown.md#code-blocks) |
| Highlighted `<pre>` gets `tabindex="0"` and `data-language` | Astro (Shiki output) | — | [Markdown](markdown.md#code-blocks) |
| The highlight CSS comes first in the page stylesheet, so user styles override it | simplest | Last | [Markdown](markdown.md#code-blocks) |
| `<Post />` takes no attributes (unknown-prop error) and ignores children | Astro (`<Content />` takes no props) | Pass attributes to its layout | [Markdown](markdown.md#importing-markdown-into-a-component) |
| `server:defer` and `client:*` on a Markdown component aren't special-cased | simplest | Reject them | `PLAN.md` §2.4 |
| An empty list (`tags: []`) is a `[]string` in `Frontmatter` | simplest | `[]any`, or an error | [Markdown](markdown.md#postfrontmatter) |
| The keys of nested maps follow the field-name rules, also when the layout field is a `map[string]T` | simplest (one parse for `Frontmatter` and the props) | Map keys as written | [Markdown](markdown.md#frontmatter) |
| A YAML number with a fraction can't set an integer field (`1.5` for `uint8`); an integer can set a float field | Go (constant conversion rules) | Truncate | [Markdown](markdown.md#layouts) |
| Integer ranges assume a 64-bit `int` and `uint` | simplest (Orbit targets 64-bit servers) | The target's size | `internal/markdown` |
| A nested map sets a struct's direct exported fields; a top-level key also sets a field promoted from an embedded struct | Go (composite literals can't name promoted fields; `p.X = …` can) | Promoted fields everywhere | [Markdown](markdown.md#layouts) |
| A frontmatter whose closing `---` is missing is an error at the first line | simplest | Treat the file as Markdown with a thematic break | [Markdown](markdown.md#frontmatter) |
| A code block without a language is `data-language="plaintext"`, and indented code blocks render like fenced ones | Astro (Shiki's `plaintext`) | No attribute; goldmark's `<pre><code>` | [Markdown](markdown.md#code-blocks) |
| Whitespace tokens get no `<span class="w">`, and token text escapes only `&`, `<` and `>` | simplest (smaller HTML; no theme colors whitespace) | Chroma's HTML formatter's output | [Markdown](markdown.md#code-blocks) |
| A theme with no text or background color of its own uses `CanvasText` / `Canvas` in the generated CSS | simplest (`light-dark()` needs two colors) | Inherit (impossible inside `light-dark()`) | [Markdown](markdown.md#code-blocks) |
| ```` ```orbit ```` blocks use Orbit's own Chroma lexer, outside Chroma's registry; component tags are `NameClass` (`nc`), HTML tags `NameTag` (`nt`), control words (`if`, `else`, `for`, `/if`, `/for`) `Keyword`, `{/* … */}` `CommentMultiline`, braces `Punctuation` | simplest (the VS Code grammar's scopes, mapped to Chroma's nearest types) | All tags `nt`; register it globally | [Markdown](markdown.md#code-blocks) |
| In an `orbit` block, a `{` without its `}` is an error token, and an ellipsis (`…`) in Go code is text, not an error | simplest (docs write `…` for elided code) | Highlight to the end of the line | `internal/markdown` |
| `<<` and `>>` aren't turned into guillemets | Astro (smartypants has no such rule) | goldmark Typographer's default | [Markdown](markdown.md#what-you-can-write) |
| Link errors and warnings point at the link's `[`; a `?query` and the `#fragment` are kept when a link is rewritten | simplest | The destination's position | [Markdown](markdown.md#links-between-markdown-files) |
| `orbit lsp` regenerates on `.md` changes on disk (its file watcher), so gopls completes `Post.Frontmatter.`; an open `.md` follows its buffer instead, like an `.orbit` file | simplest | — | [Editor setup](editor-setup.md#markdown-files) |

## Markdown in the language server

Decided with the language server's Markdown support (user-approved: `.md` diagnostics and `.md` import paths; `PLAN.md` §9).

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Diagnostics are published for every compiled `.md` file (pages and imported files), open or not, and stay after `didClose` (then about the disk content); `.md` files Orbit doesn't compile, or outside `src/`, get none | same as `.orbit` files (simplest) | Only open files; clear on close | [Editor setup](editor-setup.md#markdown-files) |
| gopls's diagnostics in a `.md` file's generated Go aren't shown | simplest (no user code there; the untyped fallback assignments would report false mismatches, such as a YAML number for a `string` field) | Map them to the key through the `//line` directives | `PLAN.md` §9 |
| A frontmatter type error comes from the typed check (startup, and after `orbit_gen/` changes), shown at its key while the key's text and the layout's `Props` are unchanged; the check now also runs at startup without `orbit_gen/` when a compiled `.md` has a layout | superseded: a debounced Markdown typed check also runs while typing (user decision "Yes, debounced", rows below) | — | [Editor setup](editor-setup.md#markdown-files) |
| The Markdown typed check runs 300 ms after the last change, like the check after `orbit_gen/` changes; a change while one runs doesn't cancel it: its result is dropped (a change counter at the snapshot) and the next one runs after it | simplest (cancelling mid-`go list` or mid-check would leave the resident checker in an unknown state; a reused check takes ~5 ms wall) | Cancel the running check through its context | `PLAN.md` §9 |
| It reruns on: an open `.md` buffer edit; a `.md` change on disk; an `.orbit` buffer edit of a compiled `.md`'s direct layout whose pass-1 decl package changed (a template edit doesn't); a `.go`, `go.mod`, `go.sum`, `go.work` or `orbit.json` event | simplest (the direct layout covers the usual case; a `Props` type declared in another component's file isn't followed) | Every `.orbit` edit; the layout's whole import closure | `PLAN.md` §9 |
| It checks only the `.md` files with a layout and the files whose decl packages they import, and keeps its `compiler.Checker` between runs: `go list -export` reruns only after a `.go`/`go.mod`/`go.sum`/`go.work`/`orbit.json` or `orbit_gen/` event, a load warning, or every 100 runs | simplest that keeps a run near 20 ms CPU at 200 files (a fresh `go list` each run is ~70 ms) | The full typed check each time (~140 ms) | `PLAN.md` §9 |
| Requests other than diagnostics on a `.md` (hover, completion, definition, references, rename) answer `null` | simplest | — | [Editor setup](editor-setup.md#markdown-files) |
| Opening a `.md` doesn't make the server look for a project (an `.orbit` file does, when the editor's root isn't one) | simplest (a `src/` next to a `go.mod` isn't always an Orbit project) | Also load from a `.md` | — |
| The VS Code extension selects Markdown by path (`**/src/**/*.{md,markdown,mdown,mkdn,mkd,mdwn}`, any language id), and its middleware skips every request but diagnostics for non-`.orbit` documents | simplest (covers every extension Orbit accepts, even ones VS Code doesn't call `markdown`) | `language: "markdown"` plus the pattern | `editors/vscode/README.md` |
| Import path completion offers full paths relative to the file (every `.orbit` file and every `.md` under `src/`, minus the file itself and those already imported), sorted nearest first; it starts at `./` or `../`, with `/` as a new trigger character; with no alias, accepting adds one, derived as for tag auto-imports | simplest | Directory-by-directory completion; offering only components | [Editor setup](editor-setup.md#what-completion-covers) |
| Tag completion offers a `.md` component only when Orbit compiles it (it's in the component index); `.md` pages aren't offered, like `.orbit` pages | simplest (consistent with the index) | Every `.md` under `src/` | [Editor setup](editor-setup.md#what-completion-covers) |
| Renaming a layout `Props` field that a frontmatter sets is refused, naming the file, line and key | user decision (Orbit doesn't edit Markdown) | — | [Editor setup](editor-setup.md#rename) |

## Codegen speed

Decided with the dev-loop speed work (`PLAN.md` §12, items 3 and 4). None changes what's generated for a page; `orbit_gen/link` is a new generated package.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| One `orbit_gen/link` package, not sharded | simplest (16 shards made the recompile after a body edit 3 ms instead of 8–12 ms, within the noise of a ~200 ms `go build`) | Shards by path hash or by first path segment | [Project structure](project-structure.md#orbit_gen-generated-go-code) |
| `project.Reload` falls back to a full `Load` and `Front` for `go.mod`, `orbit.json`, or a Go edit that changes which `src/locals` type is used; it always rediscovers `src/`, and a changed path that's a directory stands for every file under it | simplest (those change every file's codegen options; a walk of `src/` is well under a millisecond and catches renamed directories) | Trusting the watcher's paths for added and removed files | `PLAN.md` §12 |
| `orbit generate` starts `go list -export` once the files are parsed, over the runtime, `src/locals`, every frontmatter Go import and `time` (with Markdown files); a guess that misses a package loads again | simplest (the exact set needs pass 1, which comes later; the guess is exact for every project in the repository) | Waiting for pass 1; the import set of the last `orbit_gen/` on disk | `PLAN.md` §3.3 |

## Formatter

With the `orbit fmt` spec and implementation (`PLAN.md` §6.1). The bigger choices (what the first release covers, tabs in the frontmatter, the default mode, `-l`'s exit code, Bun by default, Markdown) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| No `-s` simplifications | Go stdlib (`go fmt` doesn't apply them) | A `-s` flag | `PLAN.md` §6.1 |
| Imports are sorted by path within each blank-line-separated group, as `gofmt` sorts them, so `.orbit` imports (`./`, `../`) come first in their group | Go stdlib (gofmt) | Moving `.orbit` imports into a group of their own | [CLI](reference/cli.md#the-frontmatter) |
| Frontmatter statements are never reordered or moved between hoisted declarations and body statements; each run is formatted on its own, and blank lines between runs are kept (at most one) | simplest (hoisting is semantic; the source order is what you read) | Gathering hoisted declarations at the top | [CLI](reference/cli.md#the-frontmatter) |
| Leading and trailing blank lines in the frontmatter are removed; delimiter lines become exactly `---` | Go stdlib (gofmt trims a file's leading and trailing blank lines) | Keep them | [CLI](reference/cli.md#the-frontmatter) |
| An expression is formatted as a standalone value (`x := <expr>`'s spacing, so `{a+b}` → `{a + b}`), not as the call argument codegen makes of it (where gofmt would keep `a+b`) | simplest (what you read is the expression, not the generated call) | Argument spacing | [CLI](reference/cli.md#expressions-and-control-blocks) |
| Braces hug the code: `{ title }` → `{title}`, `{ }` → `{}` | Astro (Prettier's JSX printer removes the padding) | Keep padding as written | [CLI](reference/cli.md#expressions-and-control-blocks) |
| Block tags are respelled `{if c}`, `{else if c}`, `{else}`, `{/if}`, `{for clause}`, `{/for}` (and `{switch x}`, `{case a, b}`, `{default}`, `{/switch}`) | simplest | Keep the spacing as written | [CLI](reference/cli.md#expressions-and-control-blocks) |
| Attribute forms are kept: no shorthand `{title}` introduced or expanded, quotes and unquoted values as written | simplest (attributes are HTML, which phase 1 never touches) | Prefer the shorthand | [CLI](reference/cli.md#expressions-and-control-blocks) |
| Continuation lines of a multi-line expression or header are indented from the `{` line, one template indent unit per gofmt level; the unit is detected from the template (a tab, or the smallest space indentation), two spaces when nothing is indented | simplest (two spaces: Orbit's starter and Prettier's default) | Always a tab; always the frontmatter's tabs | [CLI](reference/cli.md#expressions-and-control-blocks) |
| Processed scripts are formatted as TypeScript, other scripts with no `type` or a JavaScript `type` as JavaScript, scripts with any other `type` (JSON, JSON-LD, import maps) not at all; every `<style>` as CSS | simplest (formatting JSON-LD would change rendered bytes for no gain) | Also format JSON blocks | [CLI](reference/cli.md#scripts-and-styles) |
| A script or style block keeps its own indentation; content on the tag's line moves to its own lines, one unit in from the tag | Astro (prettier-plugin-astro indents script and style content) for the new lines; simplest (a block's existing indentation is kept so multi-line template literals don't change) | Always re-indent one level | [CLI](reference/cli.md#scripts-and-styles) |
| The print width isn't reduced by a block's indentation | simplest | Pass `--print-width` minus the indentation (it would override the user's Prettier config) | `PLAN.md` §6.1 |
| `bun fmt` gets `--stdin-filepath=<file>.orbit.<n>.<ext>` and runs in the project root, so the user's `.prettierrc`, `bunfig.toml` and `.editorconfig` apply; `--no-script` | simplest (`orbit check`'s file naming; Prettier's config lookup) | Ignore user config (`--no-config`) | [CLI](reference/cli.md#scripts-and-styles) |
| One `bun fmt` process per block, 10 s timeout each, no cache | simplest (about 1.3 ms per call, measured; gofmt has no cache) | Batch through temp files; a content-hash cache in `.orbit/` | `PLAN.md` §6.1 |
| A file with any syntax error (template, frontmatter or `{…}` Go, a script or style Bun rejects, a Bun timeout) is left unchanged, and the other files are still formatted | Go stdlib (gofmt leaves a file it can't parse alone and goes on) | Format the parts that parse | [CLI](reference/cli.md#errors-and-exit-codes) |
| Only syntax errors stop formatting; Orbit's other checks (unknown props, unused imports, directive misuse) don't | Go stdlib (gofmt formats code that doesn't type-check) | Refuse any file `orbit check` rejects | [CLI](reference/cli.md#errors-and-exit-codes) |
| A safety check re-parses each result (same template tree, same Go tokens, same esbuild-minified script and style); on a mismatch the region is left as written, with a warning | simplest (the "never changes rendered output" promise is checked, not assumed) | Trust the formatters | `PLAN.md` §6.1 |
| A CRLF file keeps CRLF in the regions Orbit rewrites; template text keeps its bytes, mixed endings follow the first line break | simplest (gofmt writes LF, but changing template text would change rendered output) | Convert to LF | [CLI](reference/cli.md#orbit-fmt) |
| A BOM is kept by `orbit fmt`, and the template's final newline (or its absence) is never changed; a file ending at the closing `---` with no line break gets one | simplest (both are rendered template text) | gofmt's single trailing newline | [CLI](reference/cli.md#orbit-fmt) |
| Directory walks take `.orbit` regular files, skip `.*` directories, `node_modules` and `orbit_gen`, and don't follow symlinks | Go stdlib (gofmt skips symlinks; `go fmt` skips dot directories) | Respect `.gitignore` | [CLI](reference/cli.md#paths-and-flags) |
| A file named explicitly that isn't `.orbit` (a `.md`, say) is an error naming `bun fmt`; directory walks skip it | simplest | Format any file named explicitly, as gofmt does | [CLI](reference/cli.md#paths-and-flags) |
| No path, outside a project: `orbit fmt: no go.mod found; pass the files or directories to format`, exit 1 | simplest (like `orbit generate` outside a project) | Read stdin, as gofmt does | `PLAN.md` §6.1 |
| Stdin is read with `--stdin-filepath=PATH` | Astro (Prettier's flag, also `bun fmt`'s) | `-` as a path; gofmt's "no arguments reads stdin" | [CLI](reference/cli.md#paths-and-flags) |
| Writes are atomic (temp file in the same directory, then rename, keeping the file mode); unchanged files aren't rewritten | simplest (`orbit dev` never sees a half-written file) | Write in place | [CLI](reference/cli.md#paths-and-flags) |
| `-d` prints `gofmt -d`'s unified diff format | Go stdlib (gofmt) | — | [CLI](reference/cli.md#paths-and-flags) |
| The language server formats the whole document only (no range or on-type formatting), returns one edit per changed region, ignores the editor's tab settings, and returns no edits on a syntax error | Go stdlib (gopls: no range formatting, ignores `FormattingOptions`) | A single whole-file edit; an error response on a syntax error | [Editor setup](editor-setup.md#formatting) |
| Without Bun, the language server says so once per session (`window/showMessage`, Info) | simplest | Say nothing; a message on every format | [Editor setup](editor-setup.md#formatting) |
| The VS Code extension makes itself the default formatter for `[orbit]`; format on save stays off unless you turn it on | simplest (no other formatter knows `.orbit`; VS Code's default for format on save is off) | Ask on first use | [Editor setup](editor-setup.md#formatting) |
| A `//` comment that ends an `{if}`, `{else if}` or `{for}` header is a syntax error (`a // comment can't end a {if} header; use /* … */`); one that ends an `{expr}` is kept, with a line break before the `}` | simplest (`if x // c {` can't compile; an expression can end in a comment) | Move the comment out of the braces | `PLAN.md` §6.1 |
| A frontmatter chunk that ends with a comment on its own line gets a blank line before the next chunk | simplest (otherwise the comment becomes the next statement's doc comment on the next run, and formatting isn't idempotent) | Attach the comment to the next statement | `PLAN.md` §6.1 |
| The safety check also keeps the number of comments in each Go region, and lets a control header lose redundant parentheses | Go stdlib (gofmt drops an empty `//` above a block and `if (x)`'s parentheses) | Trust gofmt | `PLAN.md` §6.1 |
| Without Bun, the notice is printed only when some file has a script or style to format | simplest (no noise for projects without scripts) | Print it on every run | [CLI](reference/cli.md#scripts-and-styles) |
| Directory walks also skip the project's `dist/` | simplest (it's build output) | Only `.*`, `node_modules` and `orbit_gen` | [CLI](reference/cli.md#paths-and-flags) |
| The language server formats only the documents it follows (`.orbit` under the workspace's `src/`); others get `null` | simplest (the same set it gives diagnostics for) | Format any `.orbit` buffer | `PLAN.md` §6.1 |
| A docs ```` ```orbit ```` block whose info string has `unformatted` (a "before" example) is exempt from the fmt-clean test | simplest | A separate fence language | `PLAN.md` §6.1 |
| A leading UTF-8 BOM is skipped by the parser, so the frontmatter after it is found and the BOM isn't rendered | Go stdlib (go/scanner skips a leading BOM) | Treat it as template text | [CLI](reference/cli.md#orbit-fmt) |

## Typed bridge

With the typed server→client bridge (`orbit.JSON`, the `./X.orbit` client modules; `PLAN.md` §5c). The bigger choices (per-component modules imported by relative path, `orbit.JSON` on any attribute, `set:json` typed with `json(id)`, single-quoted values, nil slices and maps typed `| null`, 64-bit integers as `number`, the types in `orbit_gen/types/`, no TypeScript features inside `<script>` at first (added later, see [TypeScript in scripts](#typescript-in-scripts)), no `bun check` in `orbit dev`) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `orbit.JSON` is also allowed in content (written HTML-escaped, like a string) and in spreads (untyped) | simplest (it's what `Expr` and `Spread` do with any value) | A compile error outside attributes | [Runtime API](reference/runtime-api.md#json) |
| Keys are the attributes' DOM dataset names (`data-author-name` → `authorName`) | web standard (`el.dataset`); simplest | The attribute name as written | [Client modules](reference/client-modules.md#datael-key) |
| The starter `tsconfig.json` lists `orbit_gen/types/*.d.ts` in `include` | Astro (≥ 5 adds its generated `.astro/types.d.ts` to `include`) | A triple-slash reference in `src/env.d.ts` | [Styles and scripts](styles-and-scripts.md#tsconfigjson) |
| Generate-time errors only for types that always fail to encode (func, chan, complex, `unsafe.Pointer`, bad map keys); interfaces are accepted | Go stdlib (`json.Marshal` fails on the type) | Reject interfaces too, as `server:defer` props do | [Client modules](reference/client-modules.md#unsupported-types) |
| `set:json` values get the same generate-time check as `orbit.JSON` arguments | Go stdlib (`json.Marshal` fails on the type) | Check `orbit.JSON` only | [Client modules](reference/client-modules.md#unsupported-types) |
| A marshaler declared only on `*T`, at a `T` position, maps to `unknown` | simplest (whether `encoding/json` calls it depends on addressability; `unknown` is never wrong) | Use the marshaler's type | [Client modules](reference/client-modules.md#type-mapping) |
| `[N]T` maps to `T[]`, not a tuple | simplest | A fixed-length tuple type | [Client modules](reference/client-modules.md#type-mapping) |
| `JSONAttr`'s position is the attribute value's `{`; `set:json`'s is the `set:json` attribute | simplest | The `orbit.JSON` call; the element | [Runtime API](reference/runtime-api.md#attribute-values) |
| `JSONValue` implements `json.Marshaler`, and `set:json` unwraps a `JSONValue` (`set:json={orbit.JSON(x)}` writes `x`, not `{}`) | Go stdlib (`encoding/json` sees it as its value) | An opaque struct that encodes as `{}` | [Runtime API](reference/runtime-api.md#json) |
| Encode errors without a position (the `Attr`, `Expr` and `Spread` fallbacks) read `orbit: orbit.JSON: json: …` and `orbit: set:json: …` | Go stdlib (package-prefixed errors); simplest | No prefix | [Troubleshooting](troubleshooting.md#orbitjson-json-unsupported-value-nan) |
| A component without keys has no `.d.ts` module, so importing it is `TS2307 Cannot find module` | simplest (one module per file with at least one key, PLAN §5c) | An empty module for every component | [Client modules](reference/client-modules.md#components-without-data) |
| `orbit doctor`'s `tsconfig` check runs when a `.orbit` file contains `.JSON(` or `set:json` (a text search; a non-runtime `.JSON(` call can make it warn, harmlessly) | simplest | Run the typed pass to find real uses | [CLI](reference/cli.md#orbit-doctor) |
| The doc comments of a component module name each site as `data-post (LikeButton.orbit:9:14)` and `set:json #cfg (X.orbit:L:C)` | simplest | Full paths | [Client modules](reference/client-modules.md#component-modules-xorbit) |

## Switch blocks

With `{switch}` template blocks (`PLAN.md` §2.3). The keyword (`switch`, not `match`) and the rules in §2.3 (content before the first case, `fallthrough`, stray cases, a second `{default}`, an unclosed `{switch}` are errors) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Content before the first case is reported once per `{switch}`, at its first non-whitespace byte: `content before the first {case} of a {switch} is never rendered; move it into a {case} or {default}` | simplest | One error per node | [Template syntax](reference/template-syntax.md#switch) |
| A second `{default}` reads `multiple {default} cases in {switch} (first at L:C)` | Go stdlib (the compiler's `multiple defaults in switch (first at …)`) | `unexpected {default}: …` like a second `{else}` | [Template syntax](reference/template-syntax.md#switch) |
| `{case}`/`{default}` outside a switch read `unexpected {case}: no {switch} is open`; with an element or block still open in the previous case, `unexpected {case}: <div> opened at L:C isn't closed` (and it's closed there) | simplest (the `{else}` messages) | — | [Troubleshooting](troubleshooting.md#switch-block-errors) |
| `{fallthrough}` reads `fallthrough isn't supported in a {switch}; list the values in one {case a, b}` | simplest | Go's `fallthrough statement out of place` | [Template syntax](reference/template-syntax.md#switch) |
| An empty `{case}` reads `{case} needs a value or a condition, e.g. {case "draft"}; use {default} for the rest`; `{default x}` reads `unexpected text after {default}; write {default} or {case list}` | simplest (the `{if}` and `{else}` messages) | Go's syntax error from the generated code | [Template syntax](reference/template-syntax.md#switch) |
| `{/case}` and `{/default}` get their own message (`unexpected {/case}: a {case} ends at the next {case}, {default} or {/switch}`); other unknown block ends now list `{/if}, {/for} or {/switch}` | simplest | The generic unknown-block-end message | [Template syntax](reference/template-syntax.md#switch) |
| `{switch}`, `{case}`, `{default}` and `{fallthrough}` in a start tag get the existing `blocks can't appear inside a start tag` error | simplest | — | [Template syntax](reference/template-syntax.md#nesting-rules) |
| A `{switch}` with no cases is allowed and renders nothing | Go stdlib (`switch x {}` is valid) | An error | — |
| `compressHTML: "html"` looks through every case, and past the switch when it has no `{default}` | simplest (the `{if}` rule) | — | [Template syntax](reference/template-syntax.md#whitespace) |
| `orbit fmt` respells `{switch x}`, `{switch}`, `{case a, b}`, `{default}`, `{/switch}`; the header is formatted like an `{if}` header (so `{switch (x)}` loses its parentheses), and a case list inside its own switch, so a type switch's types format as types | simplest (the `{if}` rules) | Format case lists as plain expression lists | `PLAN.md` §6.1 |
| In the language server's tolerant code, a reported second `{default}` adds no label, so its body joins the clause before it and gopls still sees valid Go | simplest | Emit it, a Go error on top of Orbit's | `internal/compiler/DESIGN.md` |
| The VS Code grammar scopes `switch`, `case` and `default` as `keyword.control.conditional.orbit` (like `if`/`else`), `/switch` like `/if` | simplest | A separate `keyword.control.switch` scope | `editors/vscode/README.md` |

## TypeScript in scripts

With TypeScript features in `<script>` blocks (`PLAN.md` §9b). The bigger choices (TypeScript 7's native language server, the lookup with the `bun x` fallback, in-memory documents under `.orbit/lsp/` with an in-memory copy of `orbit check`'s tsconfig, the full first-release feature set, in-memory types refreshed while typing, `is:inline` scripts as JavaScript, opening every script for references and rename) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `.ts` files under `src/` are left to the editor's own TypeScript | Astro (its language server leaves `.ts` files alone) | Serve them through Orbit's TypeScript child too | [Editor setup](editor-setup.md#ts-files) |
| A virtual document is sent as one full-text `didChange`, only when its text changed | simplest (a 10 KB file costs nothing next to TypeScript's recheck) | Incremental changes | `PLAN.md` §9b |
| Orbit registers one watcher of its own (`**/*.{ts,mts,cts,tsx,js,mjs,cjs,jsx,json}`) and forwards its events to the TypeScript child; the child's own watcher registrations (`<root>/**/*`, `/tmp/**/*`) aren't forwarded, and those events no longer go to gopls | simplest (the child filters events itself) | Forward the child's registrations | `PLAN.md` §9b |
| Completion advertises Orbit's and TypeScript's trigger characters; a trigger that isn't the cursor region's own answers an empty list without asking anyone | simplest | A fixed subset | `PLAN.md` §9b |
| TypeScript diagnostics are published only for `.orbit` files open in the editor (the documents opened for references and rename aren't diagnosed) | Astro | Every file with a script (that's `orbit check`) | [Editor setup](editor-setup.md#ts-files) |
| The TypeScript child starts lazily, on the first `.orbit` file opened with a script (or the first request in one) | simplest that keeps memory flat in sessions without scripts | At startup | [CLI](reference/cli.md#orbit-lsp) |
| Diagnostics are pulled 50 ms after the last change, like gopls's `diagnosticsDelay` | same as gopls (PLAN §9) | Pull at once | `PLAN.md` §9b |
| `orbit doctor`'s `typescript` check is ⚠ at most, never ✗; inside a project without scripts, a missing TypeScript passes ("not needed") | as Bun's check | ✗ when missing | [CLI](reference/cli.md#orbit-doctor) |
| A crashed child is restarted once, on the next request or opened script; after a second crash, TypeScript features stay off with a notice until the language server restarts | simplest | Restart with backoff | `PLAN.md` §9b |
| A request to the child that takes over 10 s answers `null` | simplest (a hung child must not hang the editor's requests) | No timeout | `PLAN.md` §9b |
| `is:inline` scripts, and other unprocessed scripts with no `type` or a JavaScript one (the ones `orbit fmt` formats as `.js`), are JavaScript documents: a file's classic scripts share one document (the page's global scope, a `;` at each script's start), each `type="module"` script gets its own (a module); they get every feature the TypeScript documents get, from TypeScript's inferred project (default compiler options, the DOM), without the tsconfig, data types or imports. Scripts with `src` and JSON scripts get none | Astro (its language server merges a file's inline scripts into one `inline.mjs` JavaScript document, gives each module script its own, and turns on every feature but formatting) | Completion and hover only; one document per inline script | [Editor setup](editor-setup.md#typescript-in-script-blocks) |
| A component's client module served from memory is opened next to its `.orbit` file (`src/…/X.orbit.d.ts`, never written), not under `orbit_gen/types/src/…`; `go.d.ts` and `client.d.ts` stay under `orbit_gen/types/` | simplest that works: TypeScript 7.0.2 doesn't resolve a module into an overlay whose directory doesn't exist on disk (no `orbit_gen/` yet), and the tsconfig's `rootDirs` lists the project root first, so the in-memory module also wins over a stale one on disk | The `orbit_gen/types/` path (fails on a fresh clone); writing the directory | `PLAN.md` §9b |
| The data types are re-emitted while typing only for files with a data contract: a `set:json` block, a direct `orbit.JSON` call, or a `data-*` attribute in a file that calls `JSON(`; plain `data-*` attributes don't re-check the file | simplest (other `data-*` values carry no keys; checking every file with a `data-*` attribute re-checked 38 of 50 files per edit in the benchmark) | Every file with a `data-*` attribute | `PLAN.md` §9b |
| A lookup candidate older than 7.0.2 (a TypeScript 5 or 6 `tsc` on `PATH`, an old `node_modules/typescript`) is skipped for the next one (`bun x`); the notice names it only if nothing is found. A bad `typescriptPath` setting is an error of its own, with no fallback | simplest (like `goplsPath`: an explicit setting isn't second-guessed) | Stop at the first `tsc` found | [Editor setup](editor-setup.md#how-typescript-is-found) |
| The missing-engine notice is a warning (`window/showMessage` type 2), shown once per session | as optional tools elsewhere (Bun's notice) | An error, like a missing gopls | [Troubleshooting](troubleshooting.md#no-typescript-completion-in-script) |
| Code actions: only `quickfix` actions with edits are kept (the request asks for `only: ["quickfix"]` unless the editor narrows it); commands and lazily resolved actions are dropped | simplest (Orbit can't run the child's commands; `source.*` actions are out of scope, §9b Q4) | Proxy `workspace/executeCommand` and `codeAction/resolve` | `PLAN.md` §9b |
| An edit TypeScript places in the blanked area before the script (an import at the top of the file) moves to the first content line of the script, indented like it; other edits outside the script are dropped | simplest | Drop it | `PLAN.md` §9b |
| On a definition that lands in a `.d.ts` side-table line, the declaration's range is used for both the target and selection ranges | simplest (the side table maps lines to declarations) | Keep the selection range in the `.d.ts` | `PLAN.md` §9b |
| The child's `didChangeConfiguration` registration is forwarded with its id prefixed `ts:`, and its work-done tokens likewise, so they can't collide with gopls's | simplest | Forward as is | `PLAN.md` §9b |
| The child's `window/logMessage` goes to Orbit's log only for errors and warnings | simplest (TypeScript 7 logs every snapshot at info level) | Log everything; forward to the editor | — |

## Direct calls

With direct calls in production builds (`PLAN.md` §3.5). That `orbit build` calls components directly and `orbit dev` keeps the hooks, the `-overlay` from `.orbit/build/`, the direct `<Self>`, measuring with `go test -overlay` and no depth cap were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The impl render function is exported as `Orbit__RenderImpl` only in the build flavor; `orbit_gen/` keeps the unexported `orbit__render`, so a direct callee's impl file is overlaid too | simplest that keeps `orbit dev` no slower (user principle): exporting it in `orbit_gen/` put each impl's signature, and so its decl `Props`, in the export data `orbit_gen/link` reads, and link's compile went from 8 to 22 ms at 200 files (+15–30 ms per dev rebuild) | The spec's `Orbit__RenderImpl` in both flavors | `PLAN.md` §3.5 |
| Pages called as components keep the hook (their `Render` wrapper builds `Params`), and so does `<Self>` in a page | simplest | Build `Params` at the call site | `PLAN.md` §3.5 |
| Calls to an imported `.md` file keep the hook, and so does a Markdown file's call to its layout; `.md` impls are never overlaid | simplest: a Markdown render ignores props and slots, so a direct call keeps nothing on the stack | The spec's direct `.orbit` → `.md` calls (a build flavor in `internal/markdown`) | `PLAN.md` §3.5 |
| The route and island tables, `RenderComponent` and `orbittest` keep calling `Render` through the hook | simplest | Direct calls there too | `PLAN.md` §3.5 |
| `.orbit/build/` is removed and rewritten on every build; `overlay.json` is always written, with an empty `Replace` when nothing is overlaid, and `-overlay` is always passed | simplest | Write only changed files; skip the flag when empty | [CLI](reference/cli.md#orbit-build) |
| Overlay paths are relative to the project root (where `go build` runs) | simplest (the same file in every checkout) | Absolute paths | `PLAN.md` §3.5 |
| `orbit check` vets the hook flavor only | simplest | Vet both | `PLAN.md` §3.5 |
| An `-overlay` in `GOFLAGS` stops `orbit build` with `orbit: -overlay in GOFLAGS isn't supported (orbit build passes its own)`, checked before anything is generated | simplest (the CLI's usual `orbit:` prefix) | Merge the user's overlay into Orbit's | [CLI](reference/cli.md#orbit-build) |
| A build-flavor codegen error (a direct call site the import list missed) is an internal error that fails `orbit build` | simplest | Fall back to the hook flavor | — |
| `orbit build` runs `go env GOFLAGS` once, before generating (it ran up to twice, after) | simplest | — | — |

## class:list

With the `class:list` directive (`PLAN.md` §2.3). The syntax (Go composite-literal elements: strings, `[]string`, `"class": cond`), the build-time folding, the merge with `class` and the component `class` prop, and normalizing whitespace were the user's, not rows here. Astro's behavior was read from `astro@7.3.0` (`clsx` 2.1.1 in `addAttribute`, `normalizeProps` for components, `@astrojs/compiler-rs` 0.4.1 for the attribute merge).

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Empty items (`""`, blank strings, empty slices, false conditions) are dropped; duplicates are kept, in list order | Astro (`clsx` drops falsy items and doesn't dedupe) | Dedupe the static classes at build time | [Template syntax](reference/template-syntax.md#classlist) |
| Whitespace inside items is normalized too (runs collapse to one space, ends trimmed), at build time for static items and without allocating at run time for dynamic ones. Astro 7's `clsx` keeps an item's whitespace as written (`" a  b "` stays) | user (§2.3), a deviation from Astro | Write dynamic strings as they are | [Template syntax](reference/template-syntax.md#classlist) |
| A list with no classes writes no `class` attribute (codegen opens it with the first class when it can't tell at build time) | Astro (`addAttribute` returns `""` for an empty list) | `class=""` | [Template syntax](reference/template-syntax.md#classlist) |
| A `class` attribute on the same element goes first in the list, and the merged attribute is written at `class:list`'s position, whichever comes first in the tag | Astro (its compiler moves `class` into the list as the first item) | Write it at `class`'s position | [Template syntax](reference/template-syntax.md#classlist) |
| Several `class` attributes next to a `class:list` are all merged, in source order | simplest | Merge the first, report the others | — |
| A spread's `class` isn't merged: both are written, and browsers use the first | Astro (`spreadAttributes` and `addAttribute` are written separately) | Merge at run time | [Template syntax](reference/template-syntax.md#classlist) |
| On a component, `class` and `class:list` are joined into the `class` prop (`""` when empty, Go's zero value); the target needs a `Class` field, and the pre-check's message is `class:list sets the class prop, which <Card> doesn't have (props: …)` at `class:list` | Astro (`normalizeProps`: `clsx(class, class:list)`, deleted when empty) | Pass a `[]string` | [Template syntax](reference/template-syntax.md#classlist) |
| Joining a component's dynamic items allocates the prop's string once; a single item that's already clean is passed without allocating | simplest (the prop is a Go `string`) | A builder type in `Props` | [Template syntax](reference/template-syntax.md#classlist) |
| A static item is a Go string literal (interpreted or raw), escaped at build time with the runtime's table; a merged static `class="…"` keeps its character references as written, as static attributes do | simplest | Decode character references | [Template syntax](reference/template-syntax.md#classlist) |
| The key of `"c": cond` may be any string expression; a literal key folds at build time | simplest (a keyed item is "the key's classes if cond", one rule) | Literal keys only | [Template syntax](reference/template-syntax.md#classlist) |
| Items are a string kind (named ones included) or `[]string` (a named slice of `string` too); anything else, `any` and `nil` included, is a build error at the item from the typed pass: `class:list items must be a string or []string, not int` (`class={…} next to class:list must be …` for a merged value) | simplest (the PLAN's types) | Format other values like `clsx` | [Troubleshooting](troubleshooting.md#classlist-errors) |
| The untyped fallback code (`ClassAny`, `ClassString`) accepts any value, so a wrong type is reported by `orbit build`, `dev` and `check`, not by the editor; at run time without the typed pass, it's a render error (a panic in a component's prop) | simplest (a generic helper's constraint error lands on the generated call, not the item) | A generic helper with Go's error | `internal/compiler/classlist.go` |
| A condition that isn't a `bool` is Go's error at the condition; in a component's prop, `in argument to orbit.ClassIf` is rewritten to `in class:list condition` | Go stdlib (as the frontmatter `return` rewrite) | An Orbit check | [Troubleshooting](troubleshooting.md#classlist-errors) |
| Nested lists (`{"a", {"b": on}}`) are an error, not flattened | simplest | Flatten them | [Troubleshooting](troubleshooting.md#classlist-errors) |
| `class:list` works on HTML elements and component tags only, once per tag, and only with a `{…}` value | simplest | Accept `class:list="a b"` as `class` | [Troubleshooting](troubleshooting.md#classlist-errors) |
| Syntax errors read `syntax error in class:list: <go/parser's message>` at its position | Go stdlib | — | [Troubleshooting](troubleshooting.md#classlist-errors) |
| `orbit fmt` formats the list as composite-literal elements (gofmt spacing); a multi-line list keeps its lines and trailing comma, a one-line list loses a trailing comma | Go stdlib (gofmt) | Leave lists as written | `PLAN.md` §6.1 |
| The language server offers `class:list` on HTML elements and on components whose `Props` has a `Class` field; an unparsable list (while typing) is still completed by gopls | simplest | Offer it on every component | [Editor setup](editor-setup.md) |
| The VS Code grammar scopes `class:list` like the other directives, its value as Go | simplest | — | `editors/vscode/CHANGELOG.md` |

## Error hook

`orbit.WithErrorHandler` and `site.ServeError` (`PLAN.md` §4c). The shape (a `Site` option returning `bool`), what reaches it (only 500-bound errors, panics included) and `ServeError` were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| With several `WithErrorHandler` options, the last one wins | Go stdlib (functional options, like `WithKey`) | Chain them | [Runtime API](reference/runtime-api.md#witherrorhandler) |
| A recovered panic's error unwraps to the panic value when it's an `error` | simplest (so `errors.As` sees `panic(err)`) | Wrap only errors that Orbit created | [Runtime API](reference/runtime-api.md#errors-and-control-flow) |
| The handler's `err` keeps the `.orbit` position as its text prefix; there's no separate position API | simplest | An `orbit.ErrorPos(err)` accessor | [Rendering](rendering.md#answering-errors-yourself-witherrorhandler) |
| The page's `ctx.Header()` headers are dropped before the handler runs, as for the 500; middleware's headers on `w` stay | Astro (a thrown error discards the page's response) | Pass them to the handler | [Rendering](rendering.md#answering-errors-yourself-witherrorhandler) |
| For an island request, `Cache-Control: private, no-store` and `X-Robots-Tag: noindex` are set on `w` before the handler runs (only when a handler is set), and the handler may replace them | simplest (an error answer isn't cached either) | Leave the headers to the handler | [Lazy loading](lazy-loading.md#status-codes-and-errors) |
| A handled error isn't logged; the handler decides | Go stdlib (`httputil.ReverseProxy.ErrorHandler`) | Always log | [Rendering](rendering.md#answering-errors-yourself-witherrorhandler) |
| A panic in the handler isn't recovered by Orbit (it goes to `net/http`) | Go stdlib | Recover and send the 500 | [Rendering](rendering.md#answering-errors-yourself-witherrorhandler) |
| The handler runs in `orbit dev` too; only errors it declines show the dev error page | simplest (dev and prod behave the same) | Skip it in dev | [Rendering](rendering.md#answering-errors-yourself-witherrorhandler) |
| `site.ServeError` with a `nil` error writes nothing | simplest | Panic, or send a 500 | [Runtime API](reference/runtime-api.md#site) |
| `site.ServeError` treats the request as a page request (a not-found renders `404.orbit`, no island headers), whatever its path | simplest | Detect island paths | [Runtime API](reference/runtime-api.md#site) |

## Slot to string

`orbit.RenderHTML` (`PLAN.md` §4d). The name and signature, the `</head>` render error and having no per-user guard helper (a documented pattern instead) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The render's error is returned, not set as the page's sticky error, and the partial output is dropped | simplest (the caller decides) | Set the page's error | [Runtime API](reference/runtime-api.md#renderers-and-slots) |
| Panics aren't recovered in `RenderHTML`; the request's recovery handles them, and the `ctx`'s writer pointer is restored on the way out | simplest | Recover into an error | [Runtime API](reference/runtime-api.md#renderers-and-slots) |
| A nil renderer (no slot content) returns `""` and no error | simplest | An error | [Components](components.md#rendering-a-slot-to-a-string) |
| The capture's writer starts with a copy of the request writer's component stack; a `ctx` with no writer (or `nil`) starts at depth 0 | simplest | Share one stack | [Runtime API](reference/runtime-api.md#renderers-and-slots) |
| The current writer is an unexported `Ctx` field, set where the runtime pairs a `Ctx` with a `Writer` (pages, islands, `RenderComponent`, `orbittest`) | simplest (no exported API) | A `Writer` argument | `PLAN.md` §4d |
| The docs' cache example caps its size by clearing the map when full | simplest (an example, not a library) | An LRU | [Rendering](rendering.md#caching-fragments) |

## Tailwind

With Tailwind CSS v4 support (`PLAN.md` §5d). The bigger choices (the standalone CLI found locally, then `bun x`, else an error with install lines; Tailwind at-rules in component `<style>` blocks; Orbit inlining the CSS a Tailwind stylesheet imports and rewriting its `url()`s; one-shot runs per batch behind the token gate; the VS Code `configurationDefaults` entry; no Tailwind in `orbit new`) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Tailwind v4 only: a v3 CLI is skipped and named in the error; a v3 JavaScript config still works through v4's `@config` | Astro (its Tailwind integration is `@tailwindcss/vite`; the v3 `@astrojs/tailwind` is deprecated) | Support v3 | [Using Tailwind](tailwind.md#install-the-tailwind-cli) |
| Tailwind's source detection starts at the project root | Astro (`@tailwindcss/vite` scans Vite's root) | The stylesheet's directory | [Using Tailwind](tailwind.md#use-utilities-in-templates) |
| Every input runs as `tailwindcss --optimize -i - -o -`, its text on stdin and the stylesheet's (or `.orbit` file's) directory as the working directory, so its relative `@plugin`, `@config`, `@source` and `@reference` paths resolve as written; an `@import "tailwindcss"` (or `tailwindcss/utilities.css`, or a `@tailwind utilities`) without `source(…)` gets `source("<project root, relative>")` | simplest (after Orbit's inlining, the text Tailwind must see isn't a file on disk; one way for stylesheets and blocks) | `-i <path>` from the project root for stylesheets without imports | `PLAN.md` §5d |
| A block is scoped first, then run through Tailwind | Astro (its compiler scopes before Vite's CSS plugins) | Tailwind first | [Using Tailwind](tailwind.md#apply-in-component-styles) |
| A `.css` file is a Tailwind input when it imports `tailwindcss` or uses any Tailwind at-rule (`@apply`, `@reference`, `@theme`, …), not only when it imports `tailwindcss` | Astro (`@tailwindcss/vite` runs on every stylesheet) | Only files that import `tailwindcss` | [Using Tailwind](tailwind.md#add-a-stylesheet) |
| `--optimize` in dev and in builds | simplest (the same CSS in both; Tailwind's nesting lowered for its own browser targets) | Raw output in dev | `PLAN.md` §5d |
| esbuild minifies, Tailwind doesn't (`--minify` unused) | simplest (one minifier; the same size) | Tailwind's `--minify` | `PLAN.md` §5d |
| No `orbit.json` key and no environment variable for the CLI's path | simplest (`PATH` covers a project-local binary) | A `tailwindPath` key | [Configuration](configuration.md#settings-that-arent-in-orbitjson) |
| `.gitignore` keeps generated code out of the scan; Orbit adds no `@source not` lines; `orbit doctor` warns when `orbit_gen/`, `.orbit/` or `dist/` aren't ignored in a project that uses Tailwind | simplest | Orbit excludes them itself | [Using Tailwind](tailwind.md#use-utilities-in-templates) |
| Tailwind's errors are reported at `file:line:col` with ANSI codes and box characters stripped: Tailwind's own `stdin.css:L:C` mapped back through the inlining to the file it came from, else the first `@apply` that names an unknown class, else the input's start; Tailwind's "Are you using CSS modules…" hint is cut | Go stdlib (`go vet`-style positions) | Tailwind's message as is | [Using Tailwind](tailwind.md#errors) |
| A missing CLI is an error at the input's first Tailwind rule, naming the install commands for the current OS and architecture | Go stdlib (a position for every error) | An error without a position | [Troubleshooting](troubleshooting.md#the-tailwind-css-cli-isnt-found) |
| Tailwind's `/*! tailwindcss v4.3.3 \| MIT License … */` banner stays, where esbuild puts legal comments | simplest (esbuild's default) | Strip it | — |
| Several Tailwind stylesheets are allowed, each run on its own | simplest (Tailwind's behavior) | An error for a second one | [Using Tailwind](tailwind.md#add-a-stylesheet) |
| A `watch` file that brings a new word reruns Tailwind | simplest (how a `@source` directory outside the module is watched) | Restart only | [Configuration](configuration.md#watch) |
| `orbit check` reports Tailwind's errors | simplest (it runs generate, which bundles) | Skip Tailwind in `orbit check` | [CLI](reference/cli.md#orbit-check) |
| Inlining: an `@import` is inlined when esbuild's resolver finds a CSS file for it (relative or npm); its `layer(…)`, `supports(…)` and media conditions become `@media { @supports { @layer { … } } }`; a nested `@charset` is dropped; an import cycle is dropped; imports of `tailwindcss`, remote ones and ones with other conditions (`theme(…)`, `prefix(…)`) are left to Tailwind | simplest (the CSS cascade's meaning of `@import`, as esbuild bundles it) | Inline only relative imports | [Using Tailwind](tailwind.md#fonts-and-other-css-your-stylesheet-imports) |
| A rewritten `url()` is relative to the input's directory (`../../node_modules/…/x.woff2`), which esbuild then resolves and hashes | simplest (deterministic output, no absolute paths) | Absolute paths | `PLAN.md` §5d |
| In inlined files, `@plugin`, `@config` and `@reference` paths are rewritten only when they start with `./` or `../` (a bare name is a package); `@source` paths always (they're always paths) | simplest | Rewrite only `url()` | [Using Tailwind](tailwind.md#fonts-and-other-css-your-stylesheet-imports) |
| At most `GOMAXPROCS` Tailwind processes run at once | simplest | One at a time | `PLAN.md` §5d |
| Each Tailwind run gets its own `TMPDIR` (a temporary directory, removed after) | simplest fix: the standalone CLI extracts its native addon to `$TMPDIR` under a fixed name, and concurrent runs fail with `cannot open shared object file` (seen 1 in 12) | Run one at a time | `PLAN.md` §5d |
| The dev loop's word set is seeded, after the first bundle with a Tailwind input, from the module's files with the watcher's skip rules (no CSS, lock or binary files); a changed directory, or a file over 4 MB, counts as new | simplest | Seed from Tailwind's own file list | `PLAN.md` §5d |
| A failed Tailwind run's error is kept until the next batch, which reruns it | simplest (so one batch doesn't run a failing input twice) | Rerun at once | [Using Tailwind](tailwind.md#in-orbit-dev) |
| The output cache keeps only the inputs the last successful bundle used | simplest (memory stays bounded in long dev sessions) | Keep everything | — |

## Trailing slashes

The `orbit.json` `trailingSlash` option (`PLAN.md` §4e). The modes (`"never"`, `"always"`, no `"ignore"`), the 308 for every method, the exported `orbit.WithTrailingSlash` option passed before the caller's options, and the same behavior in dev and prod were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A wrong value is `orbit.json: trailingSlash must be "never" or "always", not <raw JSON>`, plus `; Orbit has no "ignore" mode: each page has one URL` for `"ignore"`; `null` means unset | simplest (Astro's default is the likeliest wrong value) | No hint; accept `"ignore"` as `"never"` | [Configuration](configuration.md#trailingslash) |
| A path has a file extension when its last segment has a `.` that isn't its first byte, followed by one or more of `[A-Za-z0-9_]` only (a byte loop) | Astro (`hasFileExtension`) | Any dot | [Routing](routing.md#trailing-slashes) |
| An extension route (last segment static with an extension, `rss.xml.orbit`) is slash-less in both modes, and `/rss.xml/` matches nothing | Astro (its endpoint rule) | Astro's page quirk (`/rss.xml/`) | [Routing](routing.md#trailing-slashes) |
| In `"always"`, a dotted param value (`/blog/v1.2`) isn't redirected and reaches `next`; `/blog/v1.2/` renders | Astro | Redirect it | [Routing](routing.md#trailing-slashes) |
| Never redirected: `/`, `/_orbit/…`, `public/` files and `//`-prefixed paths (Orbit's counterparts of Astro's internal paths) | Astro, mapped to Orbit's internal prefix | Astro's `/_`, `/@`, `/.` prefixes | [Routing](routing.md#trailing-slashes) |
| The redirect is sent only when the other form is a page (the existing rule, now in both modes), so Orbit-first mux routes keep working | simplest | Astro's unconditional redirect | [Routing](routing.md#trailing-slashes), [Server](server.md#choosing-precedence) |
| Duplicate trailing slashes (`/x//`) aren't collapsed; they match nothing | simplest | Astro's collapse | [Routing](routing.md#trailing-slashes) |
| `Route.Pattern` stays slash-less in both modes; the mode is site-wide | simplest | Patterns with a trailing slash | `PLAN.md` §4e |
| In `"never"`, `//` still gets a 308 to `/` (unchanged; its `Location` isn't protocol-relative) | simplest (today's behavior) | Never redirect `//` | `PLAN.md` §4e |
| The config field is a bool (`TrailingSlashAlways`) in `internal/config` and `markdown.GenInput`; `markdown.CanonicalURL` mirrors the runtime's extension rule rather than importing the runtime package | simplest | A string or the runtime's `TrailingSlash` type | `PLAN.md` §4e |

### Redirecting before middleware (`site.TrailingSlash`)

`PLAN.md` §4e "Redirecting before middleware". The name `site.TrailingSlash(next)`, covering pages only (no mux argument) and its use in `orbit new`'s `main.go` were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A canonical path passes on a check of its last byte, with no collapsing of `/x//`; a path with a `RawPath` (an escaped `/`) takes the full check | simplest (§4e rule 5) | Collapse duplicate slashes | [Routing](routing.md#redirecting-before-your-middleware) |
| Same exclusions and `Location` as `site.Middleware`'s redirect, from the same helper (`classify`, then `redirectTarget`) | simplest (can't drift) | Its own rule | [Runtime API](reference/runtime-api.md#site) |
| 308 for every method, empty body, no `Cache-Control` | the user's §4e (Q1); Astro sends no `Cache-Control` | 301 for GET (Astro) | [Routing](routing.md#trailing-slashes) |
| `site.Middleware` keeps its redirect, with no "already checked" flag | simplest | Skip it behind `TrailingSlash` | [Routing](routing.md#redirecting-before-your-middleware) |
| Placement: outermost, a logger may wrap it | Astro (redirects before middleware) | Inside the logger | [Middleware](middleware.md#where-middleware-goes) |
| The generated default entry (`orbit_gen/cmd/server`) is unchanged: `site.Handler()` has nothing in front of it | simplest | Wrap it too | — |
| Astro's 400 for doubly encoded paths (`rejectInvalidEncoding`) isn't copied | out of scope (Go's URL parser rejects invalid escapes) | Copy it | — |

## Pass-through attributes

With `orbit.RestAttrs` (`PLAN.md` §2.5). The type as the marker, merging a passed `class` into the element's, no typo guard and spreads on component tags were the user's (§2.5 (Q1)–(Q4)), not rows here. Astro's behavior was checked with `@astrojs/compiler-rs` 0.5.1.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| On a component tag, an attribute written on the tag replaces the spread's, `class` included: `<Input {...props.Attrs} class="w-full" />` gives `w-full`, not the caller's class joined with it | user (2026-10-26; Astro behavior, kept after the implementation) | Join the classes, as on HTML elements | [Components](components.md) |
| The field is recognized by its type as written, `orbit.RestAttrs` (spaces ignored), in the frontmatter's `Props` | simplest (no type information at analysis time) | Resolve the type with go/types | [Components](components.md#passing-html-attributes-through) |
| On an element, a spread is pass-through attributes when its operand is spelled `props.<the RestAttrs field>` (parentheses allowed); any other operand is an `orbit.Attrs` spread (a `RestAttrs` held in another variable is Go's type error) | simplest (decided at build time without types, so the untyped and typed builds write the same bytes and the class merge costs nothing) | Decide from the typed pass | [Troubleshooting](troubleshooting.md#pass-through-attribute-errors) |
| A static value is passed as `name="value"` with the value as written (character references kept, `"` escaped), a bare attribute as `name`; names are kept as written and not checked at build time | simplest (static attributes on an element are copied as written) | Decode and re-escape values | [Template syntax](reference/template-syntax.md#pass-through-attributes) |
| The same name twice on a call site is an error at the second, comparing names in any ASCII case | simplest (HTML attribute names are case-insensitive) | Exact comparison | [Troubleshooting](troubleshooting.md#pass-through-attribute-errors) |
| A passed `class={…}` is a class-list item (a string or `[]string`), like the items of `class:list`, and static classes are decoded, since they're escaped when written | simplest (one rule for every class that's merged) | Format any value as `Attr` does | [Template syntax](reference/template-syntax.md#pass-through-attributes) |
| An element with `{...props.Attrs}` merges all its `class` attributes, as next to a `class:list` (whitespace normalized), and the passed class goes last; without a class of its own, the spread writes `class="…"` after the other attributes | simplest | Write the passed class at the element's first `class` only | [Template syntax](reference/template-syntax.md#spread-attributes) |
| On a component tag, attributes written on the tag win over a spread's for the same name, `class` included (not joined); of two spreads, the first wins | simplest (one priority rule; the browser also keeps the first attribute) | Join the classes, as on the element | [Template syntax](reference/template-syntax.md#pass-through-attributes) |
| A spread merged with only static attributes it doesn't have (a forwarding wrapper's scope attribute) is passed without allocating; the tag's attributes are then written after the spread's | simplest (no allocation; the order of distinct attributes doesn't matter) | Always write the tag's first, allocating | — |
| The caller's scope attribute is passed on every call site of a component with a `RestAttrs` field, even with no other attribute | Astro (it passes `data-astro-cid-*` as a prop) | Only with other attributes | [Components](components.md#passing-html-attributes-through) |
| `attrs={…}` together with a spread is the same error as with collected attributes | simplest | Merge them | [Troubleshooting](troubleshooting.md#pass-through-attribute-errors) |
| A spread on a component without a `RestAttrs` field is a codegen error naming the component, no longer a parse error | simplest (only codegen knows the target's `Props`) | Keep the parse error and add a second check | [Troubleshooting](troubleshooting.md#pass-through-attribute-errors) |
| The unknown-prop hint (`add an orbit.RestAttrs field …`) is left out for imported Markdown files, which have no `Props` to change | simplest | Always add it | [Troubleshooting](troubleshooting.md#unknown-prop-x-on-post-post-takes-no-props) |
| An untyped `nil` value is kept as a dynamic attribute and omitted when written | simplest | Drop it at build time | — |
| Generated code builds each dynamic attribute with a constructor (`RestString(pre, name, s)`, `RestAny(pre, name, v)`, …) instead of a struct literal, so the typed pass sees it as an ordinary call site | simplest (the existing call-site machinery) | A composite literal with a kind field (§2.5's sketch) | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| `Has` and `Get` compare names in any ASCII case; `Get` returns the filtered value of a `javascript:` URL, as it would be written | simplest | Exact names | [Runtime API](reference/runtime-api.md#restattrs) |
| JSON: `class` first, then the attributes in written order; decoding filters URL attributes (static ones too) and checks names | simplest (safe after the round trip) | Keep a trusted marker | [Lazy loading](lazy-loading.md#props-must-be-json-serializable) |
| An `orbit.Attrs` `"class"` entry (from `RestOf` or a spread) is the class when it's a string or `[]string`; other values are formatted with `fmt` | simplest | A render error | [Runtime API](reference/runtime-api.md#restattrs) |
| The language server offers `class:list` on components with a `RestAttrs` field too, and lists the field itself among the props (`attrs`) | simplest | — | [Editor setup](editor-setup.md) |

## Code splitting

With esbuild's code splitting (`PLAN.md` §5e). The bigger choices (one build with `Splitting`; a page's own blocks stay in its bundle and only imported files are shared; `<link rel="modulepreload">` for the module's static chunks; CSS of `import()`ed modules in the page stylesheet; no preloading for lazy and dynamic chunks; deploy skew documented only; no opt-out; no Vite) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Chunks are flat in `/_orbit/`, named by esbuild's `[name]-[hash]` (`chunk-<hash>.js`, `<module>-<hash>.js` for an `import()` target) | Astro (`/_astro/[name].[hash].js` for entries and chunks alike) | A `chunks/` subdirectory | [Styles and scripts](styles-and-scripts.md#shared-chunks-and-import) |
| The new asset kind is `modulepreload` | simplest (the `rel` value) | `chunk`, `preload` | [Runtime API](reference/runtime-api.md) |
| The stylesheet esbuild emits for an `import()` target isn't written (its CSS is in the page stylesheets) | simplest (nothing links it) | Write it unused | [Styles and scripts](styles-and-scripts.md#shared-chunks-and-import) |
| Empty shared chunks are removed, and their `import"…";` statements replaced by spaces where a source map exists, deleted otherwise | simplest that keeps maps valid | Keep the empty chunks | `PLAN.md` §5e |
| An empty `import()` target is kept, even with no code | simplest (an `import()` of a removed file would fail) | Remove it and rewrite the `import()` | — |
| Chunks get `Cache-Control: public, max-age=31536000, immutable` | simplest (the existing `/_orbit/` rule) | A shorter max-age | [Deploy](deploy.md#how-static-files-are-served) |
| A diagnostic of a block copied into several pages is reported once (same file, line, column and message) | simplest | One per page | — |
| `modulepreload` assets list the module's static import closure breadth-first, in import order (esbuild's entry imports every chunk it needs directly, so it's usually flat) | simplest | Depth-first | [Runtime API](reference/runtime-api.md) |
| `orbit build --verbose`'s `loaded with import()` section lists every JS file no page lists: `import()` targets, and chunks that only lazy entries import | simplest (the loader `import()`s lazy entries too) | A separate "lazy only" section | [CLI](reference/cli.md#orbit-build) |

## Concurrent rendering

`server:concurrent` (`PLAN.md` §3.6). The name, the call-site placement, the configurable limit (default 16, inline past it), no cancellation and the header merge were the user's or the coordinator's (§3.6 (Q1)–(Q5)), not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A marked component starts when the render reaches its tag, after its props and slots are evaluated; calls aren't hoisted | Astro (siblings after the first async one start when the parent reaches them) | Hoist marked calls to the start of the parent | [Rendering](rendering.md#slow-components) |
| One join at the end of the render (page, island, `RenderComponent`, `orbittest`), not when the parent component returns | simplest that keeps Astro's overlap across parents | Join per parent | [Rendering](rendering.md#slow-components) |
| The first error in output order wins (a child's own holes before its error, a parent's error after its holes) | Astro (its buffers flush, and so reject, in order) | First error in time | [Rendering](rendering.md#slow-components) |
| `server:defer` with `server:concurrent` is a compile error; `client:*`, `<Self>`, `{for}` and `.md` components are allowed | simplest | Ignore one of the two | [Template syntax](reference/template-syntax.md#serverconcurrent) |
| Inline (in order) under `NewWriter`, past the limit and inside an attribute value (`Attr` formats into a scratch buffer) | simplest, deadlock-free | Block until a slot frees | [Runtime API](reference/runtime-api.md#concurrent-rendering) |
| `CtxOf` is emitted in every project, at the start of slot content that calls a component or uses the implicit `ctx` | simplest (a file's output doesn't depend on others) | Only in projects that use the directive | [Runtime API](reference/runtime-api.md#concurrent-rendering) |
| Status and headers from marked components merge at the join in output order, after the page's own, on failure too (a redirect keeps them) | simplest deterministic | Stop at the failing component | [Rendering](rendering.md#slow-components) |
| A `RenderHTML` capture joins its own marked components before returning, with its own limit count; its marked components' headers merge into the capture's `ctx` | simplest correct (the captured HTML is complete) | Share the request's group (a capture inside a marked component would wait on itself) | [Runtime API](reference/runtime-api.md#renderers-and-slots) |
| `RenderComponent` and `orbittest` use the default limit, 16 | simplest (they have no `Site`) | An option per call | [Runtime API](reference/runtime-api.md#withconcurrencylimit) |
| `WithConcurrencyLimit(0)` renders every marked component inline; a negative value means no limit | Go convention (`errgroup.SetLimit`) | Panic on n < 1 | [Runtime API](reference/runtime-api.md#withconcurrencylimit) |
| The limit counts goroutines running now (a finished one frees its place) | simplest | Count every fork of the render | [Runtime API](reference/runtime-api.md#withconcurrencylimit) |
| The runtime function is `orbit.Concurrent` (§3.6's sketch said `Parallel`), the option `WithConcurrencyLimit` | consistent with the directive's name | `Parallel`, `WithParallelLimit` | [Runtime API](reference/runtime-api.md#concurrent-rendering) |
| A marked component's `</head>` gets the page's tags when it's the first in output order; inside a capture it's the usual capture error | simplest (the in-order result) | — | — |

## Concurrent loads

`orbit.All` (`PLAN.md` §4f). The shape, the name, cancellation and panics re-panicked on the caller were the user's (§4f (Q1), (Q3), (Q5), (Q6)); the first error in time, the load's position, `*Ctx` first and no memoizing helper the coordinator's ((Q2), (Q4), (Q7), (Q8)), not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The first load runs on the calling goroutine; zero loads return `nil`; one load runs inline with the request's own context (no derived one); a nil `*Ctx` uses `context.Background()` | simplest | Every load in a goroutine | [Runtime API](reference/runtime-api.md#loading-data-all) |
| `All` waits for every load before returning, after an error too | Go stdlib (`errgroup.Wait`) | Return at the first error | [Loading data](data-loading.md#load-the-pages-data-with-orbitall) |
| No concurrency limit | simplest (the loads are written out) | A cap like `server:concurrent`'s | [Loading data](data-loading.md#load-the-pages-data-with-orbitall) |
| `ErrNotFound`, `*RedirectError` and already-positioned errors are recorded unwrapped | consistent with `ErrorAt` | Wrap them with the load's position | [Runtime API](reference/runtime-api.md#loading-data-all) |
| A load whose func isn't in a `.orbit` file (Go code, a method value) gets no position; the frontmatter's `return` adds one | simplest (no `.orbit` line to name) | The Go file's line | [Runtime API](reference/runtime-api.md#loading-data-all) |
| A panic in any load is re-panicked even when another load failed first; the first panic in time wins | simplest (a panic stays a panic) | The first failure in time, panic or error | [Loading data](data-loading.md#panics) |
| `http.ErrAbortHandler` from a load is re-panicked as is | Go stdlib (`net/http`) | Wrap it in a position | [Runtime API](reference/runtime-api.md#loading-data-all) |
| A nested `All` derives its context from the request's (its first argument), not from the outer load's `rc`; an outer sibling's failure doesn't cancel it | simplest (the API takes a `*Ctx`) | Thread the outer `rc` through | [Loading data](data-loading.md#loads-that-depend-on-each-other) |
| Safety rules (`SetStatus`, `Header()`, `RenderHTML` inside a load) are documented, not checked | simplest (`-race` reports them) | A runtime guard on every call | [Loading data](data-loading.md#whats-safe-inside-a-load) |
| The runtime file is `load.go` | §4f | `all.go` | — |

## Control block completion

Keyword completion after `{` in template content (`internal/lsp/blocks.go`). Where it's offered, the items, the edit swallowing an auto-closed `}` and the merge with gopls were the coordinator's brief; the rows below are the details settled by rule.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Snippets are single-line: `{if ${1:cond}}$0{/if}`, `{for ${1:_, x := range xs}}$0{/for}`, `{switch ${1:x}}{case ${2:v}}$0{/switch}`, `{else if ${1:cond}}`, `{case ${1:v}}` | simplest | Multi-line bodies | [Editor setup](editor-setup.md#what-completion-covers) |
| Without snippet support, `if`, `for`, `switch`, `else if` and `case` insert the keyword and a space and keep an auto-closed `}` (`{if }`, the cursor before the `}`); `else`, `default` and the closers insert the whole tag (`{else}`, `{/for}`) | simplest (no cursor placement without snippets) | Insert the full block with no placeholder | — |
| `{if}`, `{for}` and `{switch}` are always offered; `else if`/`else` only when the innermost enclosing construct is an `{if}` branch other than `{else}`, and `else` not when the `{if}` already has one; `case`/`default` only directly inside a `{switch}`, `default` not when it has one; inside an element inside the block, none of them (the parser would close the element with an error) | simplest (what parses without an error) | Offer them anywhere inside the block | [Editor setup](editor-setup.md#what-completion-covers) |
| The closer is the innermost open block's, and not offered with an element open inside that block (it would close the element with an error) | simplest (what parses without an error; coordinator) | The innermost open block's closer anywhere inside it | [Editor setup](editor-setup.md#what-completion-covers) |
| Order: `if`, `for`, `switch`, then the branches, then the closer; Orbit's sortTexts start with `0`, and gopls's items get a `1` prefix on theirs | simplest (stable positions) | The context-dependent keywords first | — |
| The server doesn't filter keywords by the typed prefix; the editor does (its fuzzy match over the replaced range) | LSP convention (the client filters a complete list) | Filter by prefix on the server | — |
| Offered when invoked, or triggered by `{` or `/`; an `{` directly followed by a space (`{ i`) gets no keywords | simplest (the brief's "nothing else between `{` and the cursor") | Allow leading spaces | — |
| `<title>` and `<textarea>` content gets the keywords: control blocks are valid in RCDATA | the parser (they lex `{…}` there) | None in RCDATA | — |

## Conditional attributes

`orbit.If`, `orbit.Cond` and booleans on true/false attributes (`PLAN.md` §2.6). The generic `If` with its typed-pass lowering, `Cond`, and writing a `bool` as `"true"`/`"false"` only on attributes whose values are those keywords were the user's (§2.6 (Q1)–(Q3)), not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The true/false attributes are every `aria-*` attribute plus `contenteditable`, `draggable`, `spellcheck` and `writingsuggestions`: the attributes whose keywords include `true` and `false` in the WHATWG HTML attribute index (checked 2026-10-26); `translate` (yes/no), `autocomplete`, `autocorrect` and `autocapitalize` (on/off) aren't | the spec's index (user decision (Q3) asked for the check) | Also on/off and yes/no attributes | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements) |
| The rule depends on the attribute's name only, in any ASCII case, so `aria-pressed={x}` on a custom element writes `"true"`/`"false"` too, while its other attributes keep presence; `aria-` alone isn't one | simplest (`Attr` and `Spread` see no tag name, and the typed and untyped writes must agree) | Presence for every attribute of a custom element | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements) |
| `Writer.AttrBool` keeps writing the bare attribute or nothing on every attribute; `Attr`, `Spread`, `SpreadRest` and the typed writes apply the rule | simplest (an explicit presence write keeps its meaning) | Apply the rule in `AttrBool` too | [Runtime API](reference/runtime-api.md#attribute-values) |
| A bare static attribute (`<div aria-hidden>`, `<Button aria-live />`) is written as is | simplest (static attributes are copied as written) | Rewrite it to `="true"` | — |
| `RestBool` on a true/false attribute stores the text `"true"`/`"false"`, so `Get`, the JSON of a `RestAttrs` and the spread agree; a JSON `true`/`false` decoded for such an attribute writes `="true"`/`="false"` | simplest | Keep a bool kind and check the name at each use | [Runtime API](reference/runtime-api.md#attribute-values) |
| The lowering also applies through parentheses (`{(orbit.If(c, v))}`) and to `set:text={orbit.If(…)}`, which is content | simplest (the same value, the same bytes) | Only the bare call in `{…}` | — |
| Constants folded into the static HTML are strings, integers (runes included) and bools; a float constant is written with its typed write under the `if` | simplest (the folded bytes are exactly the runtime's escaping and integer formatting; float formatting stays in one place) | Fold floats too | — |
| The lowered code is a block, `{ orbit__c, orbit__v := cond, v; if orbit__c { … } }`, rather than `if c, v := cond, v; c { … }` | simplest (robust to a `//` comment at the end of an argument) | The one-line `if` with an init statement | — |
| The first lowered site of a file keeps a reference to the callee (`_ = orbit.If[bool]`, no code), so a file whose only use of `orbit` is a lowered `If` still compiles | simplest | Drop the implicit import when unused | — |
| The type check records identifier uses (to identify the runtime's `If` by its object) only for files where an attribute or content expression contains `If` | measured choice: no cost for other files | Always record them | — |

## Typed optional values

`orbit.Opt[T]` (`PLAN.md` §2.6, "Typed optional values"). `If` returning an `Opt`, the hidden fields and method names, `NonZero`, wrapping a `T` passed to an `Opt` prop, `RestOpt`, none writing nothing in content, the JSON shape and the `String` method were the user's ((Q1)–(Q7)), not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Only `orbit.Opt` itself, after resolving aliases, is an `Opt`: a type defined over it (`type MaybeRel orbit.Opt[string]`) is an ordinary struct, written by the generic path (`fmt.Sprint`); an alias is fine | the exact-type rule of `orbit.URL`/`orbit.CSS` | Match any struct with `Opt`'s shape | [Template syntax](reference/template-syntax.md#optional-values-orbitopt) |
| An `Opt[orbit.CSS]` on `style` is trusted like an `orbit.CSS`, and `orbit.If(c, "constant")` on `style` like the constant | the value written is a `T`, so `T`'s rule applies | Warn on every `Opt` | [Template syntax](reference/template-syntax.md#optional-values-orbitopt) |
| In Markdown frontmatter, only a top-level key may have no value (`key:`, `null`, `~`), and only for a layout prop of type `orbit.Opt[T]`; elsewhere it's still an error, now reported at generate time (nested: when the YAML is read); such a key isn't in `Frontmatter` | simplest (a nested `Opt` field would need the type while parsing) | `null` anywhere an `Opt` is | [Markdown](markdown.md#frontmatter) |
| The runtime's case for an `Opt` in `Attr`, `Expr`, `Spread` and `RestAttrs` comes after the concrete types and before `error` and `fmt.Stringer`, so an `Opt`'s `String` method never decides how it's written | simplest (no cost on the existing paths) | Before every case | [Template syntax](reference/template-syntax.md#how-values-render) |
| A `class:list` item of type `Opt[string]`/`Opt[[]string]` on an element is written with an `if` around `Class`/`Classes`; in a component's `class` prop it goes through `ClassString`, which unwraps it (one boxing allocation there) | simplest (the prop is an expression, with no room for an `if`) | A helper returning `""` for none in the prop | [Template syntax](reference/template-syntax.md#classlist) |
| `set:html` takes an `Opt[orbit.HTML]` (none writes nothing) through a typed site; a `string` stays Go's own error, at the same position with the same text, because the build writes everything but an `Opt` with `orbit__w.HTML` as before | user decision (2026-10-27), replacing "only an `orbit.HTML`, written `set:html={o.Or("")}`" | Keep `Or("")`; an Orbit-worded error for a `string` | [Template syntax](reference/template-syntax.md#optional-values-orbitopt) |
| Before the type check, `set:html={x}` is `orbit__rt.SetHTML(w, pos, x)` (`T ~string \| Opt[HTML]`), so the editor no longer underlines a `string` value while typing (the error comes from `orbit check`/`orbit dev`); served without the type check, a value that isn't an `HTML` or an `Opt[HTML]` is a render error, never written | simplest (a stricter constraint would also reject untyped constants, `set:html={nbsp}`) | Keep `orbit__w.HTML(x)` before the type check (an `Opt[HTML]` underlined as an error in the editor) | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| A basic literal in `set:html` (`set:html={"&nbsp;"}`) keeps `orbit__w.HTML` in both passes | simplest (always valid, so no site) | Make it a site too | — |
| Before the build's type check, an `Opt` prop's value goes through `OptOf`, which converts a value (or an `Opt`'s value) of `T`'s kind, or a number for a number, to `T`, so the unchecked output matches the checked one for untyped constants (`rel={c}`, `rel={orbit.If(c, "x")}` into `Opt[Slug]`) | simplest (the two outputs never differ for a valid program) | Leave such a value none until the type check | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| A basic literal passed to an `Opt` prop (`n={3}`) is wrapped with `Wrap` directly, without waiting for the type check | simplest (an untyped constant then takes `T`) | Treat it as any expression | — |
| `WrapIf` takes the `orbit.If` as written as an argument (`orbit.If[bool]`), so a file whose only use of `orbit` was that call still compiles | the `_ = orbit.If[bool]` reference of the `If` lowering, in an expression | A statement before the component call | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| `nil` passed to an `Opt` prop is Go's type error (`OptOf` can't infer its type), not Orbit's message; not passing the prop is how to leave it unset | Go's rule | A special case for `nil` | — |
| `RestOpt` of an `Opt[orbit.JSONValue]` has no `.orbit` position for an encoding error (its signature has none) | simplest | A `RestOptJSON` with a position | — |
| `AttrPair.Value` of an `Opt` of a type `A` stores unboxed returns the `Opt`'s value, or `nil` for none | simplest (what's stored is the value) | Return the `Opt` | [Runtime API](reference/runtime-api.md#values) |
| `A` keeps an `Opt` unboxed only for `string`, `URL`, `HTML`, `CSS`, `JSONValue`, `bool`, `int`, `int64` and `float64` (the other integer and float types are boxed, 1 allocation) | measured: each `Opt` type named in `A`'s switch instantiates its methods in the runtime package; all 19 of `A`'s kinds grew the runtime's object file by 1.9 MB (2.5 → 4.4 MB), these 9 by 0.6 MB with `-dwarf=false` | Every kind `A` stores | [Runtime API](reference/runtime-api.md#values) |
| A Go error about a value passed to an `Opt` prop (`n={"x"}` for an `Opt[int]`) says `in optional prop value` instead of `in argument to orbit__rt.Wrap` | the `ErrorAt`/`ClassIf` cleanups | Leave the generated name | — |

## Port fixes (nyth)

Fixes found porting the `nyth` Astro site (2026-10-27).

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A Tailwind CLI found through `bun x` or `node_modules/.bin` runs with the `node_modules` directory of its own `tailwindcss` dependency appended to `NODE_PATH` (after the user's), found by asking Bun once where `bun x` put the bin, so `@import "tailwindcss"` resolves without a project `node_modules` | simplest (no Bun-internal path hard-coded; `@tailwindcss/node` honors `NODE_PATH`) | Have Orbit's inliner rewrite `@import "tailwindcss"` to the package's absolute path; run Tailwind from the CLI's directory | [Using Tailwind](tailwind.md) |
| `set:html` and `set:text` work on `<script>` and `<style>`, and make them unprocessed (written in place, not bundled or scoped) with or without `is:inline` | Astro (its compiler emits them in place and hoists nothing) | A compile error without `is:inline` | [Template syntax](reference/template-syntax.md#directives) |
| `set:text` on a `<script>`/`<style>` HTML-escapes its value, like any text | Astro | Write it unescaped; an error | [Template syntax](reference/template-syntax.md#directives) |
| Escaping is spelled like Astro's: text `&<>"'` → `&amp; &lt; &gt; &quot; &#39;` (html-escaper), double-quoted attribute values `&` → `&amp;`, `"` → `&quot;` (Astro's `toAttributeString`), `'` raw | user decision (Astro) | Keep `html/template`'s `&#34;`/`&#39;` | [Runtime API](reference/runtime-api.md#escaping-rules), [Coming from Astro](coming-from-astro.md) |
| Attribute values still escape `<` and `>` (Astro leaves them raw): the one difference left | user decision (safety) | Astro's exact attribute escaping | [Runtime API](reference/runtime-api.md#escaping-rules) |
| NUL still becomes U+FFFD in text and attribute values (html-escaper keeps it) | simplest (unchanged; the HTML parser replaces or drops NUL anyway) | Copy NUL as is, like Astro | — |
| Typed string attributes are written as pieces around a new public `Writer.AttrValue(s)` (the attribute escaper), since `String` is now the text escaper | simplest (mirrors `Writer.URL`; generated code needs a public method) | Generate `AttrString(name, s)` calls (loses the joined static pieces) | [Runtime API](reference/runtime-api.md#attributes) |

## Import aliases

`.orbit` and Markdown imports (and a Markdown `layout`) through `tsconfig.json` `compilerOptions.paths`, like Astro (user decision 2026-10-27, `PLAN.md` §2.2). The rules in the brief (TypeScript's `paths` semantics, a non-relative `.orbit`/`.md` path is an alias, the error wording's shape, inside `src/`) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| With `baseUrl` set, a path that no `paths` key matches resolves against `baseUrl` (`"components/Card.orbit"` with `"baseUrl": "src"`); without `baseUrl`, no fallback | TypeScript (Astro's alias plugin does the same) | `paths` only | [Components](components.md#import-aliases) |
| Exact keys win over `*` keys, then the longest prefix; keys or targets with more than one `*` are ignored | TypeScript | — | [Components](components.md#import-aliases) |
| Of several targets, the first existing file wins; when none exists, the first target is reported (`no such component: …`, as for a relative path) | TypeScript | Report every target | [Components](components.md#import-aliases) |
| `extends` (a string, or an array as in TypeScript 5.0, later entries winning) follows local files (`.json` added when missing) and packages under the project's `node_modules/` (`<pkg>/<path>`, `.json` added, or a directory's `tsconfig.json`), without `package.json` `exports`; one that isn't found, a cycle, or a file that doesn't parse is ignored (bun check and esbuild report broken files) | TypeScript, simplified (simplest: no Node resolution) | Error on an unresolved `extends` | [Components](components.md#import-aliases) |
| `paths` and `baseUrl` are merged across `extends` like TypeScript: the nearest file that sets one wins; `paths` without `baseUrl` are relative to the file that sets `paths` | TypeScript | — | — |
| Error: `no tsconfig.json paths alias matches "~/x.orbit"; use a relative or root-relative path ("/src/…"), or add "~/*": ["./src/*"] to compilerOptions.paths`; the suggested target is `./src/<seg>/*` when the first segment starts with a letter or digit (a directory name, not a sigil); a path without `/` suggests `"./Card.orbit"` | go vet style (simplest) | No suggestion | [Troubleshooting](troubleshooting.md) |
| The suggested and scaffolded target is `"./src/*"`, not `"src/*"` | esbuild warns about a non-relative target without `baseUrl` (TypeScript ≥ 4.1 accepts it) | `"src/*"` with `"baseUrl": "."` | [Project structure](project-structure.md) |
| A bare `"Card.orbit"` is an alias (no longer read as relative) | TypeScript (a bare specifier is never relative) | Keep reading it relative to the file | [Troubleshooting](troubleshooting.md) |
| A `/`-rooted path (`"/src/x.orbit"`) is root-relative: resolved from the project root, with the same checks as a relative path (inside `src/`, must exist, framework files an error), in `.orbit`/`.md` imports and a Markdown `layout`; never an alias | user decision (2026-10-27): supported, like Astro | An alias like any non-relative path (the first version) | [Components](components.md#root-relative-imports) |
| A root-relative path is cleaned before the `src/` check, so `"/src/../../x.orbit"` and `"/"` are outside `src/`; any other root-relative path that doesn't end in `.orbit`/`.md` (`"/src/util.ts"`) gets the relative-path error (`only .orbit and .md files can be imported by relative path…`) | simplest (same rule as `./`; a Go package path never starts with `/`) | A separate message | [Components](components.md#root-relative-imports) |
| A Markdown `layout` that's neither relative nor a matching alias gets the same error, prefixed `layout:` (replacing `must be a path relative to this file`) | simplest (one rule for both) | Keep the relative-only rule for `layout` | [Markdown](markdown.md) |
| An unresolvable import is dropped, so its tags also report `unknown component`, as for an import outside `src/` today | simplest (existing behavior); user decision (2026-10-27): keep both errors | Keep a placeholder import to suppress the second error | — |
| `orbit new` scaffolds `"paths": { "@/*": ["./src/*"] }`; the starter's own imports stay relative | Astro (its starter has no aliases); the brief asked for the path | Use `@/` in the starter files | [Project structure](project-structure.md) |
| `tsconfig.json` is read once per `project.Load`; `orbit dev` reloads the project (a full front) when the root `tsconfig.json` or a file in its `extends` chain changes, and root `tsconfig.*.json` files are now watched (asset kind) so an extended base at the root is seen | simplest (rare edit; keeps per-edit reloads free of I/O) | Re-read on every reload | — |
| Paths with several targets: when files are added or removed, files with alias imports are re-analyzed | TypeScript (resolution depends on what exists) | Only on a full reload | — |
| The LSP watches `**/tsconfig*.json` and re-resolves every file on a change to the chain; import-path completion offers alias paths when the typed path starts with an alias's prefix (or the start of one, such as `"@`), the shortest alias spelling per file; relative completion after `.` is unchanged; after `"/` it offers root-relative paths (`/src/…`) | simplest | — | [Editor setup](editor-setup.md) |
| Tag auto-import inserts the file's shortest alias spelling when an alias covers it (including a bare `baseUrl` path, which TypeScript's auto-import also uses), otherwise the relative path | user decision (2026-10-27): like TypeScript's auto-import in aliased projects | Always relative (the first version) | [Editor setup](editor-setup.md) |
| Two spellings of one file are one component: everything is keyed by the resolved path | the brief | — | [Components](components.md#import-aliases) |

## Trusted CSS (`orbit.CSS`)

`orbit.CSS` and where the `style`/`on*` warnings come from (`PLAN.md` §2.7). The marker type with constants exempt (Q1) and the editor showing the warnings from the language server's typed check (Q2) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Only `orbit.CSS` itself (after `types.Unalias`) is trusted: `type MyCSS orbit.CSS` warns, an alias doesn't | the existing exact-type rule (`orbit.URL`, §3.3) | Any type defined from it | [Template syntax](reference/template-syntax.md#directives) |
| A constant value never warns, on `style` and `on*` alike (`"display: none"`, a `const`, `"a" + "b"`, and a constant conversion such as `MyCSS("x")`) | `set:html`'s precedent (constants are accepted since the author wrote them) | Constants warn on `on*` | [Components](components.md#event-handler-and-style-attributes) |
| A unit that isn't type-checked (type errors, unreadable export data) warns for every candidate, constants included | simplest (never silent) | No warnings for it | [Template syntax](reference/template-syntax.md#directives) |
| A project with parse or analysis errors stops before the typed pass, so it shows no `style`/`on*` warnings next to its errors | simplest (errors come first; the warnings return once they're fixed) | Keep a syntactic warning for that case | — |
| `style={…}` and `on*={…}` passed through an `orbit.RestAttrs` field warn at the call site's attribute, with the same rules; a declared prop (`Style orbit.CSS`, or any other) never warns | they're written as HTML attributes | No warning for components (the earlier gap) | [Components](components.md#event-handler-and-style-attributes) |
| `orbit.CSS` is valid on any attribute and in content, written like a string; in a URL attribute it's still scheme-filtered | simplest; it isn't a trusted URL | A CSS-only attribute list | [Runtime API](reference/runtime-api.md#values) |
| No CSS sanitizer for plain strings | Go stdlib (`template.CSS` is an unfiltered marker; a `cssValueFilter`-style filter would blank real declaration strings) | Filter plain strings at runtime (Q1 b) | [Runtime API](reference/runtime-api.md#values) |
| `orbit.CSS` on `on*` still warns: there's no trusted JavaScript type | simplest (§2.7 out of scope: `template.JS`) | Accept it on `on*` too | [Components](components.md#event-handler-and-style-attributes) |
| The candidate sites are the pass-1 call sites of `style`/`on*` attributes with an expression (`SiteAttr`, `SiteRest`), rather than a separate list recorded by the parser | simplest (the call sites already carry the attribute, and cover pass-through attributes, which the parser can't tell from props) | A parser-recorded list | — |
| The language server keeps each warning with its attribute's source text (and its index among attributes with the same text), showing it at that attribute's current position; a check runs at startup and after `orbit_gen/` changes (now also without gopls overlays, when a file has candidates) | the Markdown typed diagnostics' approach (§9); the "only then" part superseded by the next row | Re-run the typed check while typing | [Components](components.md#event-handler-and-style-attributes) |
| The warnings also refresh while typing: the debounced typed check (300 ms after the last change) type-checks the `.orbit` files with `style`/`on*` candidates and the decl packages they import, after an edit of a file that has or had candidates, or a `.go`/`go.mod`/`go.sum`/`go.work`/`orbit.json` change; a result is adopted only if no such change happened since its snapshot (the startup and `orbit_gen/` checks follow the same rule now); edits of files without candidates run nothing | user decision (2026-10-27) | Only at startup and after `orbit_gen/` changes | [Components](components.md#event-handler-and-style-attributes) |

## Dynamic tags (`<Tag>`)

Dynamic tags and optional wrappers (`PLAN.md` §2.8). A capitalized variable in scope as the tag (Q1 a), `orbit.Fragment` for no element with `""` an error (Q2 a) and the rejected names (Q3 a) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| "In scope" is go/parser's object resolution at the start tag (a body `:=`/`var`, a `{for}`/`{if}`/`{switch}` header) plus the hoisted `const`s; a hoisted type or func, or a variable only declared inside a function literal, isn't a dynamic tag (the unknown-component error) | simplest (the analyzer's existing parse) | Accept any declared name and let Go report the type | [Template syntax](reference/template-syntax.md#dynamic-tags) |
| When the body doesn't parse (tolerant mode, while typing), no capitalized tag is dynamic | simplest | Guess from the frontmatter's tokens | — |
| The alias/variable clash is reported once per name, at the variable's declaration, and the tag stays a component | simplest | One error per tag | [Template syntax](reference/template-syntax.md#dynamic-tags) |
| `TagOpen` takes the tag's `.orbit` position and the variable's name (`TagOpen(pos, "Tag", Tag, content)`), not just `(name, content)` as sketched, so its render errors read `src/x.orbit:3:2: dynamic tag Tag is empty; …` | the existing positioned render errors (`JSONAttr`) | Errors without a position | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| The invalid-name error reads `dynamic tag Tag has an invalid element name "a b"` | go vet style (simplest) | — | [Template syntax](reference/template-syntax.md#dynamic-tags) |
| The rejected and void names are matched in any ASCII case; `Fragment`'s value `"#fragment"` is compared exactly (`"#Fragment"` is an invalid name) | HTML is case-insensitive; the sentinel is a constant | — | [Template syntax](reference/template-syntax.md#dynamic-tags) |
| The parser keeps a capitalized tag's directive checks for both readings: the component's are reported as before, and analysis swaps them for the element's on a dynamic tag (`server:defer only works on .orbit components`, `set:html` allowed) | simplest (parse-only output unchanged) | Defer every directive check to analysis | — |
| A dynamic tag's children are compressed as inline content, and its `set:html`/`set:text` content is written even for `orbit.Fragment` (like `<Fragment set:html>`) | Astro (`Fragment` with `set:html`) | Skip the content with the tags | [Template syntax](reference/template-syntax.md#dynamic-tags) |
| Attribute completion on a dynamic tag offers the global HTML attributes and the element directives (`slot`, `class:list`, `set:html`, `set:text`), whatever the variable's name | the spec (as for an unknown element) | Look the variable's name up as an element | [Editor setup](editor-setup.md) |
| The generated variable is `orbit__t1`, `orbit__t2`, … in emission order | simplest (stable across passes and across `orbit fmt`'s generated-code comparison, which an offset isn't) | The tag name's offset | — |

## Ordered spreads (`orbit.AttrList`)

`orbit.AttrList` (`PLAN.md` §2.9). The type and constructor (Q1 a), the names `AttrList`/`AttrPair`/`A` (Q2) and the duplicate-name error (Q3 a) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `A` also keeps `orbit.CSS` unboxed, and every integer type (with its own type for `Value`), besides the listed kinds | simplest (the same switch) | Box `CSS` | [Runtime API](reference/runtime-api.md#values) |
| `SpreadOf` takes the spread's `.orbit` position (`SpreadOf(w, pos, x)`), so a name error in a spread, `orbit.Attrs` included, reads `src/x.orbit:9:7: invalid attribute name "a b" in spread`; `Spread` and `SpreadList` keep `orbit: …` | the existing positioned render errors | No position | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| A spread's errors are built without `fmt`, so a name doesn't escape | the allocation rule (§4b) | — | — |
| An `AttrList` that marshals to JSON with a duplicate or invalid name is an error; `nil` marshals as `null`, and decoding `null` gives `nil` | `encoding/json` conventions | Drop the duplicate | [Runtime API](reference/runtime-api.md#values) |
| Decoding keeps a JSON `false` as a bool (`aria-x` then writes `"false"`; elsewhere it's omitted), like `RestAttrs` | the existing `RestAttrs` rule | Drop `false` | [Runtime API](reference/runtime-api.md#values) |
| In a `RestAttrs`, a merged `AttrList` is written after the tag's own attributes, in list order, skipping the names they set, as a merged `Attrs` is | the existing merge rule (§2.5) | — | [Components](components.md#passing-html-attributes-through) |
| `isURLAttr` now matches by a lowercase switch instead of looping over the list (same names, `TestIsURLAttr`); spreads and `Attr` got faster (an `orbit.Attrs` of five: 295 → 250 ns) | the spec's "can tighten it" | Keep the loop | [Performance](performance.md#ordered-spreads) |

## The matched route (`orbit.RouteInfo`)

The matched route for middleware and logging (`PLAN.md` §4g). A field in the user's `Locals` (Q1 b), recording pages, `/404` and islands (Q2 a), and not setting `r.Pattern` (Q3 a) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The option is `orbit.WithLocalsRoute[L](func(*L) *RouteInfo)`, passed by `app.New()` before the caller's options, after `WithSite`/`WithTrailingSlash`; it imports `src/locals` under its own name `locals` | simplest (the approved sketch) | A reflect-based lookup | [Runtime API](reference/runtime-api.md#generated-wiring) |
| Without middleware attaching locals, the `Locals` a page allocates through `LocalsOf` is filled too, so `{locals.Route.Pattern}` works; Orbit never allocates one just to fill it (the fill happens inside `LocalsOf`'s own lazy allocation) | Astro (`Astro.routePattern` always works) and the allocation rule | Leave it zero without middleware; allocate a `Locals` per request | [Middleware](middleware.md#logging-the-matched-route) |
| More than one `orbit.RouteInfo` field (`Locals has two orbit.RouteInfo fields (Route, Other); Orbit fills one`), an embedded one, an unexported one or a `*orbit.RouteInfo` is an error at the field, from `orbit generate`'s `src/locals` check | like `orbit.RestAttrs` (§2.5) | Fill the first; ignore pointers | [Middleware](middleware.md#logging-the-matched-route) |
| The field is found by its type only: `orbit.RouteInfo` through the file's import of `github.com/orbit-z/orbit` under any name (a dot import too); a `Locals` alias isn't looked into | simplest (go/parser, no type checking, as for `Locals` itself) | Type-check `src/locals` | [Middleware](middleware.md#declare-the-struct) |
| The 404 page rendered for a page's `orbit.ErrNotFound` keeps the page's route (`RoutePage`), in middleware and in the 404 page's own `locals.Route` | the spec's table (Astro reports the page's pattern) | Switch to `/404` | [Middleware](middleware.md#logging-the-matched-route) |
| `site.ServeError(w, r, orbit.ErrNotFound)` from a custom handler records `RouteNotFound` (`/404`), with or without `404.orbit` | simplest (it answers as `NotFound()` does) | Leave it unchanged | — |
| Island params are `buildID` and `id` from the path, recorded for every answer, a 405 included; `File` is `""` for an unknown ID. `ctx.Param` in the island stays `""` | the spec's table | — | [Middleware](middleware.md#logging-the-matched-route) |
| `orbittest.Get` fills it (it serves through the real handler); `orbittest.Render` and `orbit.RenderComponent` don't (no route) | simplest | Fill a fake route in `Render` | [Runtime API](reference/runtime-api.md#the-matched-route-routeinfo) |
| The values are copied into the struct (an inline `[8]string`, then one slice), so a copy of the struct stays valid and independent | Go value semantics | Point into the struct itself | [Runtime API](reference/runtime-api.md#the-matched-route-routeinfo) |

### Matching before serving (`site.Match`)

`PLAN.md` §4g "Matching before serving". The shape `site.Match(r) RouteInfo` (Q1 a), `RouteNone` for a path no page matches (Q2 a), matching again inside `site.Middleware` (Q3 a) and leaving the app's own routes to the app (Q4 a) were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| No route for the trailing-slash redirect's other form (`RouteNone`), as Orbit's fill records. A static file was `RouteNone` too, until `RouteFile` (user decision 2026-10-28, row below) | Astro (answered before routing middleware) | Report the page the redirect leads to | [Middleware](middleware.md#knowing-the-route-before-orbit) |
| `RouteFile` (user decision 2026-10-28): `Pattern` is the URL path (`r.URL.Path`, `/robots.txt`), `File` is `""` for `public/` files and `/_orbit/` assets alike; no params. `site.Middleware` fills the same value when it serves the file, so `Match` and the fill agree | simplest (no allocation: `public/` + name would allocate per request; Astro has no route for files to copy) | `File` as the project path (`public/robots.txt`), precomputed in the index; leave the fill at `RouteNone` (`Match` would then disagree with it) | [Middleware](middleware.md#knowing-the-route-before-orbit) |
| An island request is `RouteIsland` for any method, with the same values Orbit records (a 405 included) | the existing fill (§4g table) | Only GET/HEAD/POST | [Middleware](middleware.md#knowing-the-route-before-orbit) |
| In production, a file in the static index counts as served without opening it (no allocation); in dev the file is opened, as `site.Middleware` does. A file deleted from a `WithStaticDir` directory after startup is then reported as a file while `site.Middleware` falls back to pages | simplest (the index is what production serves) | Open the file in production too (allocates) | — |
| `Match` reads the request as it is and never writes to it (no `r.Pattern`, no locals): the caller assigns the result | Go stdlib (`ServeMux.Handler(r)` doesn't serve or change `r`) | Fill the attached locals | [Runtime API](reference/runtime-api.md#the-matched-route-routeinfo) |

## Route caching

Route caching (`PLAN.md` §4h). The lookup inside `site.Middleware` after the user's middleware (Q1), stores and header writers driven by Orbit (Q2), `WithCache` in `main.go` with `routeRules` in `orbit.json` (Q3), the `Locals` `Cache` field (Q4), one background revalidation per stale key (Q6), GET islands in the route cache (Q8) and the `orbit/cache` package (Q9) were the user's; automatic ETags and 304s (Q5) and miss coalescing on routes known to cache (Q7) were the coordinator's, with the recommendations. None are rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The key is the host, the escaped path and the normalized query, without the scheme (behind a proxy, Go doesn't see it) | simplest | Astro's `origin` (scheme included), from `X-Forwarded-Proto` | [Caching](caching.md#the-key) |
| `cache.Memory` panics when `Query` sets both `Include` and `Exclude` | Go stdlib (a constructor panics on a contradictory configuration, as `WithSite` does) | Return an error; let `Include` win | [Caching](caching.md#the-memory-provider-cachememory) |
| The `Set-Cookie` and `Vary: Cookie`/`*` refusals are logged once per route and reason, not on every request | simplest (Astro logs every request, which floods logs) | Astro's every-request warning | [Caching](caching.md#whats-stored) |
| The `404.orbit` page answered for `orbit.ErrNotFound`, and unmatched URLs, are never stored | simplest (unmatched URLs are an unbounded key space) | Store a page's 404 like any status | [Caching](caching.md#whats-stored) |
| `InvalidateOptions.Path` drops every query variant and host of the path | Astro (its CDN providers' `astro-path:` tag), so every provider behaves the same | Astro's memory provider: the exact path and normalized query | [Caching](caching.md#invalidation) |
| Caching is off under `orbit dev` (nothing stored, no headers, `Enabled()` false, `Invalidate` nil); `orbittest.Get` through a site built with `WithCache` caches like production | Astro (no caching in `astro dev`; tested with build and preview) | Cache in dev too | [Caching](caching.md#in-orbit-dev-and-in-tests) |
| Only `GET` is looked up and stored; `HEAD` always renders | Astro (`memoryCache` caches GET only) | Serve `HEAD` from a stored `GET` | [Caching](caching.md#whats-looked-up) |
| A stored response's `Vary` headers (minus `Set-Cookie`) key its variants | Astro | Refuse responses with `Vary` | [Caching](caching.md#the-key) |
| The automatic `ETag` is the base64 of a CRC-64 of the body, so every instance gives the same one | simplest (correctness across instances; a per-process `maphash` seed would differ between instances) | `hash/maphash`, as the spec first said; SHA-256 | [Caching](caching.md#conditional-requests-and-etag) |
| Orbit answers conditional requests with 304s only when a provider is configured, so a site without one keeps its current serve path | simplest | 304s on every site | [Caching](caching.md#conditional-requests-and-etag) |
| `RenderComponent` under `site.CacheHandler` sets options on the handler's handle, even without a `Locals` `Cache` field | simplest | Require the field | [Runtime API](reference/runtime-api.md#rendering-from-your-handlers) |
| The "caching is not enabled" warning covers `Set` in pages and islands, not options set only on a `Locals` field in middleware | simplest | Also warn for middleware's `Set` | [Caching](caching.md#without-a-provider) |
| `Set` with a zero `MaxAge` (or any zero field) doesn't reset it; `Disable()` is the way to turn caching off | Astro (an `undefined` field is unset) | Zero resets the field | [Caching](caching.md#setting-options-ctxcache) |
| A request disabled before the lookup stays out of the cache even if the page calls `Set` later (no store, no CDN headers), and the cookie and `Vary` refusals also withhold a header provider's CDN headers | simplest (safety: a CDN would otherwise cache a signed-in visitor's page) | Astro: a later `set({…})` enables the handle again | [Caching](caching.md#safety-what-orbit-checks-and-what-you-check) |
| A provider that is both a `CacheStore` and a `CacheHeaderWriter` is used as a store | simplest | Store and write headers | [Caching](caching.md#providers) |

## Template literals

Template literals (`PLAN.md` §2.10). The syntax, every backtick string in the template being one (Q1), JavaScript's escapes with an unknown one an error (Q2), content literals (Q3), the inert-pieces rule (Q4), `Renderer` pieces as errors and slices concatenating (Q5), `orbit fmt` formatting pieces as Go (Q6) and `{false}` printing `false` were the user's, not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A URL attribute's run-time check runs over the written (attribute-escaped) bytes; the one difference from checking the raw value is a leading NUL, which the escaper turns into U+FFFD, so `"\x00javascript:"` built from pieces isn't filtered (the browser sees no scheme either) | simplest (no scratch buffer, no allocation) | Format the pieces into a scratch buffer and check that | [Template syntax](reference/template-syntax.md#urls) |
| The URL decision uses the text before the first piece, even a constant one: `` `${"/"}x${y}` `` gets the run-time check | simplest (pass 1, without types, decides the same way as the typed pass) | Fold constant pieces into the prefix | — |
| A literal passed to an interface prop (`any`) gives it a `string` | simplest (a literal is a string, as in JavaScript) | An error | [Template syntax](reference/template-syntax.md#values) |
| An attribute whose literal is empty, or whose pieces all print nothing, is written `name=""` | Astro (`title=""` for an empty string) | Omit the attribute | [Template syntax](reference/template-syntax.md#template-literals) |
| A prop whose field type is visibly not a string in the target's `Props` (a predeclared number or `bool`, a slice, array, map, pointer, func, channel or struct type) is an error before the type check, so it's reported even when another type error stops the typed pass; the typed pass doesn't repeat it | simplest | Typed pass only | [Template syntax](reference/template-syntax.md#template-literals) |
| A literal inside `orbit.If(c, …)` is built before the call, even when `c` is false; it isn't lowered into the `if` | Go (both arguments are evaluated, `PLAN.md` §2.6) | Lower the pieces into the branch | [Template syntax](reference/template-syntax.md#where-a-literal-is-lowered-and-what-it-costs) |
| CRLF and a lone CR in a literal's text become LF | JavaScript (a template literal's cooked and raw values normalize line terminators) | Keep them | [Template syntax](reference/template-syntax.md#template-literals) |
| `\0` followed by a digit, a malformed `\x` or `\u` escape and a lone surrogate (`\uD83D` without its low half) are errors | JavaScript (syntax errors in an untagged template literal) | Keep them as written | [Template syntax](reference/template-syntax.md#template-literals) |
| An unterminated literal (or `${`) is reported at its backtick (or `${`) instead of `{ has no matching }` | simplest (the cause, at its position) | Both errors | — |
| Before the type check (and when it can't run), a literal passed to an `orbit.HTML`/`URL`/`CSS` prop or `set:html` checks each piece's dynamic type at render time, by the inert rule, and panics on a non-inert one; a named string constant piece is then refused, which the typed pass accepts | simplest (never writes unchecked markup) | Trust every piece in the fallback | — |
| In pass 1, each piece of `orbit__rt.TemplateString(…)` is parenthesized, so `${a, b}` is Go's syntax error as it is in the typed forms | Go's rule (a piece is one expression) | Accept it | — |
| An `error` or `fmt.Stringer` piece in an attribute is written by the generic `AttrPiece` (boxed as `Writer.Stringer`'s argument is) | simplest (no new attribute-escaping writes) | `AttrStringer`/`AttrError` methods | — |
| `set:html` with a literal is one concatenation converted to `orbit.HTML` (one allocation when a piece is dynamic), not piece writes | simplest | Raw piece writes | [Template syntax](reference/template-syntax.md#trusted-targets-and-the-styleon-warning) |
| A concatenation with a piece that isn't a string is built in a 64-byte stack buffer (`orbit__rt.TB(make([]byte, 0, 64)).S(…).Int(…).String()`), Go's `+` when every piece is a string | Go (escape analysis keeps a non-escaping `make` on the stack) | A package-level `var orbit__tbN [64]byte` per literal, as the spec sketched | — |
| A Go error naming a literal's generated call says `template literal` (`invalid operation: n == template literal (mismatched types int and string)`), at the literal's backtick | the `ErrorAt`/`Wrap` cleanups | Leave the generated name | — |
| `orbit fmt` keeps a piece written over several lines exactly as written (`${f(a,` / `b)}`); the literal's other pieces are still formatted | gofmt (keeps the line breaks you wrote) and simplest (a reformatted multi-line piece would need its own indentation rule inside the literal's text) | Reformat it as a multi-line Go expression | [CLI](reference/cli.md#template-literals) |
| `orbit fmt` leaves a literal whose piece isn't valid Go (`${a+}`) as written, with no error, and still formats the code around it | simplest (`orbit fmt` doesn't type-check or report Go inside the template; `orbit check` and the build do) | Leave the whole expression as written; report an error | [CLI](reference/cli.md#template-literals) |
| In the editor, a literal's text (outside its `${…}` pieces) gets no completion, hover or other Go feature, as a quoted value | simplest (it's a string) | Treat it as Go | [Editor setup](editor-setup.md#what-completion-covers) |

## Float formatting

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Floats print as JavaScript's `Number.prototype.toString` (ECMA-262 Number::toString): shortest digits, decimal notation for `1e-6 <= abs(x) < 1e21` (`1e6` → `1000000`, `9.9e-7` → `9.9e-7`), else `1e-7`/`1e+21`; `-0` → `0`; `NaN`, `Infinity`, `-Infinity`; float32 keeps its shortest float32 digits. Applies to `{x}`, attribute values, pass-through and `orbit.A` attributes, template-literal pieces and the constants folded at build time | user decision (2026-10-28): what Astro renders | Go's shortest `'g'` form, as `fmt.Sprint` (`1e+06`, `1e-07`, `+Inf`) | [Template syntax](reference/template-syntax.md#how-values-render) |
| A named float type (`type Price float64`) without a `Format` method prints as a float too, at run time (`Expr`) as the typed write does; a float inside another value (a struct, a map) keeps `fmt.Sprint`'s form | simplest (typed and `Expr` output stay byte-identical; fmt owns composite values) | Format named floats with fmt (typed and untyped writes would differ) | [Template syntax](reference/template-syntax.md#how-values-render) |
| `set:json` and `orbit.JSON` keep `encoding/json`'s numbers, which already match JavaScript's except that `NaN`/`±Inf` are an error | Go stdlib | — | [Template syntax](reference/template-syntax.md#how-values-render) |

## Props spreads

Spreading props into a component (`PLAN.md` §2.11). Struct spreads with fields matched by name and type, extra fields as errors, source order with always-overwritten values as errors, `T` into `Opt[T]` as `Some` and the checks in the editor's background type check were the user's ((Q1)–(Q5)), not rows here.

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| A spread field sets the `Props` field of exactly the same Go name (`QuestionID`), with no attribute-style folding | Go (a struct conversion matches names exactly; both sides are Go identifiers) | Fold case, `-` and `_` as attributes do | [Components](components.md#spreading-props) |
| The fields of a spread value, and the settable fields of `Props`, are `encoding/json`'s field set: exported fields, promoted through embedded structs, a shallower field shadowing a deeper one and two at one depth dropping each other; tags ignored | Go stdlib (`encoding/json`) | Only direct fields | [Template syntax](reference/template-syntax.md#props-spreads) |
| Unexported fields of the value are skipped | Go stdlib (`encoding/json`) | An error | [Template syntax](reference/template-syntax.md#props-spreads) |
| A nil pointer spreads nothing | Astro (`{...undefined}`, `{...null}` are no-ops) | A render error | [Components](components.md#spreading-props) |
| Slots aren't set by a spread; an `orbit.Renderer` field is an ordinary prop | Astro (slots aren't props) | Spread a `Slots` field into the slots | [Components](components.md#spreading-props) |
| A spread's `Class` sets a `Class` prop like any field, not merged with a `class` on the tag | Astro (a later `class` replaces the spread's) | Merge them | [Components](components.md#spreading-props) |
| A spread on a Markdown import is the unknown-prop error, `unknown prop {...x} on <Post> (Post takes no props)`, from the first pass | simplest (a Markdown import takes no props, whatever the type) | Wait for the type check | [Template syntax](reference/template-syntax.md#props-spreads) |
| A field of the value promoted through an embedded **pointer** is an error (`{...x} can't read Lines: it's promoted through the embedded pointer *Options; pass it as an attribute`), like a `Props` field promoted through one | simplest (reading it would panic on a nil pointer; symmetric with the callee rule) | Read it behind a nil check; skip it | [Template syntax](reference/template-syntax.md#props-spreads) |
| Pass-through attributes written on a tag with a props spread are evaluated after the props (the `RestAttrs` value is built last); props and spreads are in source order | simplest (one rest value, built once) | Evaluate each pass-through attribute at its position | [Template syntax](reference/template-syntax.md#props-spreads) |
| A spread operand is evaluated once into a temporary (`orbit__s1 := x`), also when it has no call; a value of the callee's own `Props` type is one copy (`orbit__p = x`) when it's the first item | simplest (one shape; the compiler keeps both on the stack, 0 allocations measured) | Read an operand without calls in place, as the spec sketched | — |
| Pass 1 checks a spread on a callee with an `orbit.RestAttrs` field inside `RestFromAny`/`RestMergeAny` (no separate `SpreadCheck` before the call), so the operand is evaluated once there too | simplest | `SpreadCheck` before every call plus the `any` variants, as the spec sketched (the operand evaluated twice) | [Runtime API](reference/runtime-api.md#generated-code-hooks) |
| A struct with no exported fields spreads nothing, without an error | simplest (nothing is an extra field) | An error | — |
| Two spreads that both set props list every field they share (`both set Slug, Version of <Cached>`), reported at the second | simplest | Only the first shared field | [Template syntax](reference/template-syntax.md#props-spreads) |
| A tag with a spread in error keeps the first pass's form (the build fails on the error anyway) | simplest | Emit the props of the valid spreads | — |
| In the editor, the spread errors travel with the `style`/`on*` warnings: tied to the text of the spread or attribute, refreshed by the typed check while typing for files with a spread on a component tag | simplest (the existing channel: same debounce, same "a moment after you stop typing" behavior) | A separate channel | [Components](components.md#spreading-props) |
| A field assignment a spread generates (`orbit__p.F = orbit__s1.F`) maps back to the whole `{...x}` (so a Go error there lands on the spread) and is skipped when mapping a cursor (the operand's own code serves hover and completion); a rename drops edits there | the echo lines' rule (PLAN.md §3.3) | Map it to nothing | — |
