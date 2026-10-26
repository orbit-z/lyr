# Dev server

`orbit dev` runs your site locally, rebuilds what changed when you save a file, and reloads the browser.

## Start it

From your project root:

```sh
orbit dev
```

It prints where it's listening:

```
  Local    http://localhost:4321/
  Network  use --host to expose

orbit dev: server ready in 1.2s
```

Then open <http://localhost:4321>. Use `--port` to pick another port:

```sh
orbit dev --port 3000
```

If the port is taken, `orbit dev` tries the next ones (4322, 4323, …, up to nine more), as Astro does, and says which one it picked.

The dev server listens on localhost only, as Astro's does, so other machines on your network can't reach it. To open the site from another device, such as a phone on the same Wi-Fi, add `--host`, which listens on all interfaces:

```sh
orbit dev --host
```

Your server itself always runs on `127.0.0.1` behind the proxy (below), with or without `--host`.

Inside a project that pins the CLI, plain `orbit dev` runs the project's own version (`go tool orbit dev`); see [The pinned CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit).

Leave it running while you work. Every time you save, Orbit rebuilds as little as it can and the page reloads by itself.

## Dev renders like production

Every page renders on each request in production, and `orbit dev` does exactly the same: same router, same middleware, same buffered responses, same `server:defer` islands. Only two things differ:

- **where assets come from**: CSS and JS bundles are rebuilt into `.orbit/dev/`, and `public/` files are served straight from your project;
- **how errors are shown**: in the browser, with the [error overlay](#errors), instead of a plain 500 page.

So a page that works in `orbit dev` works the same way after `orbit build`.

## Your server runs in dev too

`orbit dev` doesn't replace your server: it builds and runs **your own server entry** (`cmd/server/main.go`, or the default one Orbit generates if you don't have one), so custom routes, auth and logging middleware behave exactly as in production.

A request flows like this:

```
browser
  │
  ▼
orbit dev proxy  (localhost:4321)
  ├── /__orbit/*  → answered by the proxy: live-reload events, reload script, error overlay
  │
  └── everything else
        │
        ▼
      your server  (127.0.0.1:<random port>, ORBIT_DEV=1)
        └── your middleware → site.Middleware(…) → island requests, /_orbit/* assets, public/ files, pages
                                                    (or your own handlers, in the order you chose)
```

The proxy only owns `/__orbit/*` (two underscores). Everything else, including `/_orbit/*` assets, `/_orbit/island/…` requests and `public/` files, goes to your server, so a custom handler for `/robots.txt` works the same in dev as in production.

The proxy forwards requests with the `Host` header the browser sent, and adds no `X-Forwarded-*` headers, so your server sees what it would see without a proxy in front, except that requests come from `127.0.0.1`.

The dev binaries are built into `.orbit/devserver/`.

Orbit starts your server with these environment variables:

| Variable | Value | Effect |
|---|---|---|
| `ORBIT_DEV` | `1` | `app.New()` runs in dev mode (below). |
| `ORBIT_ADDR` | `127.0.0.1:<port>` | The address your server must listen on. |
| `ORBIT_STATIC` | `.orbit/dev` | Where the dev CSS/JS bundles and their `manifest.json` live. |
| `ORBIT_PUBLIC` | `public` | `public/` files are served straight from your project. |
| `ORBIT_KEY` | a key generated when `orbit dev` starts | The key that seals `server:defer` props (below). |

`orbit.ListenAndServe` listens on `orbit.Addr()`, which reads `ORBIT_ADDR`, so the scaffolded `main.go` needs nothing special. If you start your own `http.Server`, use `orbit.Addr()` for its address to stay compatible with `orbit dev`:

```go
srv := &http.Server{Addr: orbit.Addr(), Handler: h}
```

### Dev mode

With `ORBIT_DEV=1`, `app.New()`:

- reads asset URLs from `$ORBIT_STATIC/manifest.json`, re-reading it when it changes, so a CSS or JS edit needs no Go rebuild;
- serves `/_orbit/*` from `ORBIT_STATIC` and every other file path from `ORBIT_PUBLIC` (so `ORBIT_STATIC` serves only `/_orbit/*`), all with `Cache-Control: no-cache`, looking files up on every request so new files appear without a restart;
- shows render errors in an error overlay instead of a plain 500 page;
- answers a failing `server:defer` island with the error text and its `.orbit` position, which the loader logs to the browser console.

Everything else, including routing, the trailing-slash redirect, status codes and your middleware, behaves as in production.

### The dev island key

`server:defer` props are encrypted with an island key (see [Lazy loading](lazy-loading.md)). `orbit dev` generates one key when it starts and passes it to your server as `ORBIT_KEY` every time it restarts it. So a page that's already open in your browser keeps loading its islands after a rebuild. The key lives only as long as the `orbit dev` process.

If you set `ORBIT_KEY` yourself before running `orbit dev`, your key is used instead.

## Live reload

Every change ends with a **full page reload**. There's no hot module replacement: the browser reloads the page, so state in the page (form input, scroll position, script variables) resets. A change that fails to build reloads open pages too, into the [error overlay](#errors).

The proxy adds a small script to every HTML page it forwards, just before `</body>`. It listens on `/__orbit/events` (server-sent events) and calls `location.reload()` when a rebuild finishes. If the connection drops, for example while your server restarts, it reconnects with backoff and reloads once the server is back.

Responses without a `</body>`, such as the HTML fragments of `server:defer` islands, are left alone.

To add the script, the proxy asks your server for uncompressed responses (it removes `Accept-Encoding` from forwarded requests), so a gzip middleware in your chain doesn't get in the way. If a response comes back compressed anyway, it's passed through untouched, without the reload script, and Orbit prints a one-time warning. The proxy reads a whole `text/html` response before passing it on, so an HTML page that your own handler streams arrives all at once in dev. Other responses, server-sent events included, are passed through as they come.

## What a change rebuilds

Orbit watches `src/`, `public/`, `orbit.json`, `go.mod`, `tsconfig.json`, every `.go` file in your module, and the files that match the [`watch`](configuration.md#watch) patterns in `orbit.json` (which can be outside the module). It skips `orbit_gen/`, `dist/`, `node_modules/`, directories starting with `.` (such as `.orbit/` and `.git/`), and, as the `go` command does, `testdata/` and `_test.go` files; editors' swap and backup files are ignored too. Changes are grouped with a 50 ms debounce. Requests that arrive from the first change until the rebuild is done wait for it, so you never get a half-built page.

| You change | Orbit does |
|---|---|
| only the contents of a `<style>` or `<script>`, or a `.ts`, `.css` or other file under `src/` that they import | rebuilds the CSS/JS bundles (incremental esbuild). No Go rebuild; your server keeps running and picks up the new asset URLs. |
| a file in `public/` | nothing to rebuild; it just reloads. |
| a template, frontmatter or `Props` | regenerates the affected Go packages, rebuilds your server and restarts it. The CSS/JS bundles aren't rebuilt unless a `<style>` or `<script>` changed too. |
| a Markdown file under `src/` | regenerates its Go packages, rebuilds your server and restarts it. A body edit that keeps the frontmatter and headings regenerates only its implementation package, like a template edit; a frontmatter or heading edit also rebuilds the components that import it. Links between Markdown pages are checked again. |
| only `.go` files (including `src/locals`) or `go.mod` | starts `go build` at once, while it regenerates; when regenerating changes nothing in `orbit_gen/` (the usual case), that build is the one it runs, otherwise it builds again. Then it restarts your server. |
| a file matching a [`watch`](configuration.md#watch) pattern in `orbit.json` | nothing to rebuild; it restarts your server (the same binary), then reloads. |
| `orbit.json`, or several kinds of files at once | regenerates, rebuilds your server and restarts it. |

Adding or removing a component's scoped `<style>` or a processed `<script>`, or toggling `is:global`, counts as "anything else": it changes the HTML (scope attributes, bundle entries), not just the CSS or JS.

For a Go rebuild, Orbit:

1. regenerates only the `.orbit` files affected by the change, and type-checks only those (the type checker stays loaded between rebuilds);
2. runs `go build`, which recompiles only the packages that changed (each component is its own Go package);
3. stops the old server with `SIGINT`, waits up to 5 seconds for it to shut down gracefully, and kills it if it hasn't;
4. starts the new server on a fresh port and waits until it accepts connections;
5. tells the browser to reload.

The old server is stopped *before* the new one starts, so a server that binds a fixed port of its own or holds a file lock keeps working. Anything your server keeps in memory is lost on each restart. `orbit.ListenAndServe` handles `SIGINT` with a graceful shutdown; if you run your own `http.Server`, handle `SIGINT` too, or your server is killed after 5 seconds.

Dev builds are linked without DWARF debug information (`-ldflags=-w`), which makes linking faster, and Orbit's generated packages are compiled without inlining (`-gcflags=./orbit_gen/...=-l`), which makes the slowest rebuilds 10–14% faster. Pages render a little slower in dev because of it; `orbit build` uses neither flag. A `-gcflags` in your `GOFLAGS` replaces Orbit's. To step through your server with a debugger such as Delve, run it yourself next to `orbit dev`, with the same environment variables, for example `ORBIT_DEV=1 ORBIT_STATIC=.orbit/dev ORBIT_PUBLIC=public dlv debug ./cmd/server`. It then listens on `:4321` (or `ORBIT_ADDR`), so start `orbit dev` on another port; pages still get their dev assets, but no live reload.

### How long a rebuild takes

These numbers come from Orbit's dev-loop benchmark (`bench/devloop`) on a generated project with 200 `.orbit` files, on an AMD Ryzen 7 9700X. They're the time from a saved edit to the new server running, without the watcher's 50 ms debounce or the browser's reload. Your times depend on your machine, your project and how long your server takes to start. The full table is in [Performance](performance.md#dev-loop-benchdevloop).

| Change | 16 threads | 4 cores |
|---|---:|---:|
| a component's frontmatter or template | about 0.22 s | about 0.22 s |
| a `.go` file that every component's `Props` depends on, such as `internal/posts` (even a function body) | about 0.73 s | about 1.3 s |
| only `<style>` or `<script>` contents (no Go rebuild) | about 30 ms | about 40 ms |

The goal is for a save to show up in the browser in about a second.

## Errors

Orbit tells you about errors in the browser, without losing your page.

**Compile errors** (a Go type error in a component, an unknown prop, a template syntax error) keep the **old server running**. The proxy answers page requests with an error overlay instead: the cleaned-up message, its `file:line:col` in your `.orbit` file, and a code frame. For example, after a typo (`{props.Post.Titel}`) in `src/components/Card.orbit`:

```
src/components/Card.orbit:11:29: props.Post.Titel undefined (type posts.Post has no field or method Titel)
```

Requests that aren't for HTML pages (API calls, assets) still reach the old server.

**Startup errors**: if the new server builds but fails to start (for example it exits because a database is unreachable), the overlay shows its stderr. The old server was already stopped, so until you fix it, requests that aren't for HTML pages get a `503`. A server that doesn't accept connections within 10 seconds counts as failed: check that it listens on `orbit.Addr()`.

**A server that exits on its own** while you work (a crash, `log.Fatal`) is reported the same way, with its stderr. Save any watched file to build and start it again.

**Render errors**: if a page returns an error or panics while rendering, your server's own dev error overlay shows the error with its `file:line`. In production, the same error gives a 500 page.

**Island errors**: if a `server:defer` component fails, its placeholder keeps the fallback, as in production, and the loader logs the status and the error text to the browser console. In production the loader stays silent.

The overlay stays connected to the live-reload events. Fix the error, save, and the page reloads by itself.

The same messages appear in the terminal running `orbit dev`, in the format described in the [CLI reference](reference/cli.md#error-output). After each successful change it prints one line, such as `orbit dev: src/components/Card.orbit: server restarted in 250ms` or `orbit dev: src/layouts/Base.orbit: CSS and JS rebuilt in 3ms`. Your server's own output goes to the same terminal.

## See also

- [Server](server.md): your `cmd/server/main.go`, `orbit.Addr()` and handler order
- [Lazy loading](lazy-loading.md): `server:defer` islands and the island key
- [Styles and scripts](styles-and-scripts.md): what's rebuilt for CSS and JS changes
- [Middleware and locals](middleware.md): net/http middleware and typed `Locals`
- [CLI reference](reference/cli.md#orbit-dev): `orbit dev` flags
