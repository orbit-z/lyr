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
| `orbit create-key` prints a base64 island key | Astro (`astro create-key`) | Document `openssl rand -base64 32` instead | [CLI](reference/cli.md#orbit-create-key) |
| A committed `orbit_gen/` still ignores `orbit_gen/static/` (the `--embed` copy of the built files) | simplest | Commit `orbit_gen/static/` too | [Project structure](project-structure.md#committing-orbit_gen-optional) |

## Configuration

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Unknown `orbit.json` keys are ignored | Go stdlib (`encoding/json`) | An error, or a warning naming the key | [Configuration](configuration.md#example) |
| An unset `site` gives a nil `ctx.Site` (like `Astro.site` being `undefined`) | Astro | A zero `url.URL` | [Configuration](configuration.md#site), [Runtime API](reference/runtime-api.md#request-context-ctx) |
| A `site` that isn't an absolute URL (no scheme or host) is an error when `orbit.json` loads | simplest | Accept it and resolve it later, or warn | [Configuration](configuration.md#site) |

## Components and templates

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Declaring `type Props` in a page is a build error (pages never receive props) | simplest | Ignore it, or warn | [Components](components.md#pages) |
| Astro's `class:list` and `define:vars` are compile errors with a hint (other colon attributes like `xlink:href` stay plain HTML) | simplest | Write them out as plain attributes | [Template syntax](reference/template-syntax.md#combining-and-reserved-namespaces) |
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
| A `set:` directive on an element with children is an error, and `set:` on a component is an error | Astro | Let the directive win over children; allow `set:` on components | [Template syntax](reference/template-syntax.md#directives) |
| A slice in an attribute value is formatted like content (elements concatenated, no separator) | simplest | Join with spaces, as for class lists | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements) |
| URL attributes filtered at runtime: `html/template`'s list plus `xlink:href` | Go stdlib | The HTML spec's full list | [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements), [Runtime API](reference/runtime-api.md#attribute-values) |
| Spread attributes are written sorted by name; duplicates of attributes on the same element aren't merged | simplest | Map order; merge, with the spread or the explicit attribute winning | [Template syntax](reference/template-syntax.md#spread-attributes) |
| The PascalCase fallback for props uses golint's common-initialisms table | Go stdlib (Go tooling convention) | A short custom list | [Template syntax](reference/template-syntax.md#name-matching) |
| `client:load` or `client:idle` with a value is an error (`client:idle takes no value`) | simplest | Ignore the value | [Template syntax](reference/template-syntax.md#client) |
| `client:*` values must be static strings | simplest (the marker is folded into precomputed static HTML) | Allow `{expr}` values | [Template syntax](reference/template-syntax.md#client) |
| Wording of the errors PLAN didn't specify: `unknown component <Crad>: no .orbit import with this alias`, `unexpected </ul>: <li> opened at 12:5 isn't closed`, `server:defer takes no value`, `client:visible takes a static string value, not an expression`, `client:media needs a media query, e.g. …`, `only one client:* directive is allowed per component` | simplest (`go vet`-style: lowercase, no trailing period) | Other wording | [Template syntax](reference/template-syntax.md#directives) |
| `rendering.md` is called that, not `rendering-modes.md`, since there's only one mode | simplest | Astro's page name | [Rendering](rendering.md) |

## Routing

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Mixed segments (`post-[id].orbit`) and non-trailing rest params (`[...path]/edit.orbit`) are a build error in the MVP, suggesting a whole-segment `[param]` | user | Support them like Astro | [Routing](routing.md#dynamic-routes-with-param) |
| A rest param must be the last segment | simplest | (see the row above) | [Routing](routing.md#catch-all-routes-with-rest) |
| Route errors name the file, without a line and column (they're about the file, not a position in it) | simplest | Report them at `:1:1` | [Routing](routing.md#file-based-routes) |
| Two files with the same route are a build error naming both | simplest | Pick one by a priority rule | [Routing](routing.md#file-based-routes) |
| Params are percent-decoded; a rest param joins its segments with `/`, and is `""` when it matches none | Go stdlib (`PathValue`) | Raw, undecoded values | [Routing](routing.md#dynamic-routes-with-param) |
| Without a `404.orbit`, a not-found response is a 404 with an empty body | simplest | A built-in 404 page | [Routing](routing.md#the-404-page) |
| The trailing-slash 308 is sent only when the slash-less path is a page; otherwise the request reaches `next` untouched | simplest (never steals your routes) | Redirect every trailing-slash path | [Routing](routing.md#trailing-slashes) |
| The island endpoint answers other methods with 405 and `Allow: GET, HEAD, POST` | Astro (405), Go stdlib (`Allow`) | Fall through to `next` | [Routing](routing.md#which-http-methods-orbit-handles) |

## Rendering and the server

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Production 500: a minimal built-in page; the error and its `.orbit` position are logged to stderr. A custom `src/pages/500.orbit` comes after the MVP | user | A `500.orbit` page now | [Rendering](rendering.md#status-codes-and-errors) |
| The 500 log line reads `orbit: <METHOD> <path>: <file:line:col>: <error>` | simplest | Structured (`log/slog`) output | [Rendering](rendering.md#status-codes-and-errors) |
| `orbit.ListenAndServe` returns nil after a graceful shutdown on SIGINT/SIGTERM, so `log.Fatal(orbit.ListenAndServe(h))` doesn't exit 1 on Ctrl-C | coordinator (simplest) | Return `http.ErrServerClosed`, like `net/http` | [Server](server.md#listening-listenandserve-and-addr), [Runtime API](reference/runtime-api.md#serving) |
| `ListenAndServe` timeouts: `ReadHeaderTimeout` 10 s, `IdleTimeout` 120 s, no `ReadTimeout`/`WriteTimeout`, 5 s shutdown grace period | coordinator (simplest; no cut-off uploads, and the same 5 s `orbit dev` waits) | Read/Write timeouts; a longer grace period | [Server](server.md#listening-listenandserve-and-addr), [Runtime API](reference/runtime-api.md#serving) |
| `RenderComponent` is buffered: on success 200, `text/html; charset=utf-8` and `Content-Length`; on error nothing is written; locals come from `r` | simplest (follows buffered pages) | Stream the fragment; write a 500 itself | [Server](server.md#rendering-components-from-handlers) |
| `ORBIT_STATIC` wins over `WithStaticDir`, which wins over `--embed` files, then `client/` next to the binary | simplest (so `orbit dev` keeps working) | Code options win over the environment | [Deploy](deploy.md#setting-the-static-directory-or-the-key-in-code), [Runtime API](reference/runtime-api.md#options) |
| The server finds `client/` next to the binary (its own directory, not the working directory) | simplest | Relative to the working directory | [Deploy](deploy.md#server-binary-distserver-and-distclient) |

## Locals

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| `Locals` lives in `src/locals` (package `locals`) | simplest | `src/middleware/locals.go`; a path in `orbit.json` | [Middleware and locals](middleware.md#declare-the-struct) |
| `LocalsFromRequest` returns nil when nothing is attached | Go stdlib (context accessors return nil) | A detached zero value | [Middleware and locals](middleware.md#set-locals-from-middleware) |
| `WithLocals` on a request that already has locals shadows them downstream only, like `context.WithValue` | Go stdlib | Replace the value in place for everyone | [Middleware and locals](middleware.md#set-locals-from-middleware) |
| The `src/locals` import-cycle error is reported at the import, `file:line:col`, with the chain of packages for an indirect import | Go stdlib (`go vet`-style positions) | Go's own `import cycle not allowed` | [Middleware and locals](middleware.md#the-import-rule) |
| The `Props` import-cycle error is reported with the full import chain | Go stdlib | Go's own error | [Components](components.md#mutual-recursion) |

## Lazy loading

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
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
| A `::pseudo-element` keeps the scope before it; with `:global(…)`, the scope goes on the nearest compound outside it | Astro | Scope the last compound regardless | [Styles and scripts](styles-and-scripts.md#how-scoping-works), [`:global`](styles-and-scripts.md#escaping-the-scope-global) |
| Page stylesheet order: a component's imports first, in import order, then its own styles | simplest (module evaluation order) | Source order of `<style>` tags | [Styles and scripts](styles-and-scripts.md#global-styles-isglobal) |
| `is:inline` is stripped from the emitted tag | Astro | Keep it | [Styles and scripts](styles-and-scripts.md#which-scripts-orbit-processes) |
| CSS imported from a script joins the page stylesheet | coordinator (esbuild default, and Astro's behavior) | A build error suggesting a `<link>` or `public/` | [Styles and scripts](styles-and-scripts.md#importing-css-from-a-script) |
| The scaffolded `tsconfig.json` follows Astro's `strict` preset, with an empty `src/env.d.ts` | Astro | A minimal `tsconfig.json` | [Styles and scripts](styles-and-scripts.md#tsconfigjson) |

## Testing

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Under `go test` (`testing.Testing()`), `orbittest.Render` and `app.New()` (so `orbittest.Get`) seal `server:defer` placeholders with a fixed test island key and build ID `0000000000000000`, unless `ORBIT_KEY`/`WithKey` is set, so snapshots are stable | coordinator (simplest) | The normal key resolution; an `orbittest.WithKey` option | [Testing](testing.md#testing-serverdefer-components) |
| `orbittest.Render` uses `GET /` by default and does no head injection | simplest (like `RenderComponent`) | Inject the page's assets | [Testing](testing.md#a-first-component-test), [Runtime API](reference/runtime-api.md#orbittest) |
| `orbittest.Get` accepts only `WithLocals` and `WithRequest`; any option that doesn't apply fails the test | simplest | Ignore it silently | [Testing](testing.md#options) |
| A missing golden file fails the test, with a hint; `orbittest` owns the `-update` flag | simplest (Go golden-file convention) | Create the file on first run; a differently named flag | [Testing](testing.md#snapshot-tests) |

## Deploy and Docker

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| Dockerfile: `WORKDIR /src`, a `go mod download` layer, the binary at `/server`, `USER nonroot:nonroot` | simplest | A single `COPY . .` stage | [Deploy](deploy.md#docker) |
| `.dockerignore` keeps `node_modules`, since the image has no Node or Bun to install it | simplest | Ignore it and install packages in the image | [Deploy](deploy.md#docker) |

## Dev server

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The dev proxy listens on localhost; `orbit dev --host` listens on all interfaces | coordinator (Astro) | All interfaces by default | [Dev server](dev-server.md#start-it), [CLI](reference/cli.md#orbit-dev) |

## Editor support

| Decision | Rule | Alternative | Where documented |
|---|---|---|---|
| The VS Code extension is published to the Marketplace and Open VSX as `bertrandkhe.orbit` | coordinator | A `.vsix` on GitHub releases only; Marketplace only | [Editor setup](editor-setup.md#vs-code) |
| gopls is chosen with the `goplsPath` initialization option, set by the VS Code setting `orbit.goplsPath` | simplest | Only `PATH` | [Editor setup](editor-setup.md#how-gopls-is-found) |
| The docs advise against a gopls `directoryFilters` entry for `orbit_gen/`, because Orbit's gopls gets the same settings | simplest | Strip `directoryFilters` from the settings Orbit forwards | [Editor setup](editor-setup.md#the-go-extensions-errors-in-orbit_gen) |
