# Getting started

This page takes you from an empty machine to a running Orbit site: you install the CLI, create a project, edit it with live reload, build the server, and get it ready to deploy.

## Install Orbit and run `orbit doctor`

Install the CLI with Go, then let `orbit doctor` check your setup:

```sh
go install github.com/orbit-z/orbit/cmd/orbit@latest
orbit doctor
```

`go install` puts the `orbit` binary in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` if `GOBIN` is unset. Make sure that directory is on your `PATH`.

`orbit doctor` prints one line per check: ✓ means it passed, ⚠ is a warning, and ✗ is a problem that blocks you. Every ⚠ and ✗ comes with the command that fixes it:

```
✓ go: go1.27.3
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); orbit check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
```

Outside a project, `orbit doctor` checks your environment only: the `go` command and its toolchain, gopls (for [editor support](editor-setup.md)), and Bun (for TypeScript type checking in `orbit check`). Inside a project it also checks `go.mod`, the pinned CLI version, the generated code, `.gitignore` and `orbit.json`. It exits with code 1 if any check fails; `orbit doctor --json` prints the same results as JSON.

Run `orbit doctor` again whenever something looks wrong. Orbit's own error messages point to it when the cause is in your environment, such as an old gopls or a CLI version mismatch.

### What you need

- **Go 1.27 or later** (required). See [Go version](#go-version) below.
- **gopls v0.24.0 or later** (recommended), for completion and diagnostics in `.orbit` files. See [Editor setup](editor-setup.md).
- **Bun** (optional), only for TypeScript type checking in `orbit check`.
- **Docker** (optional), to build the scaffolded container image.

You don't need Node.js: Orbit bundles your client scripts with esbuild, which runs inside the `orbit` CLI.

## Go version

Orbit needs **Go 1.27 or later**. This is a hard minimum, not a recommendation: Orbit reports errors at their position in your `.orbit` files (`src/pages/index.orbit:17:9`), and that relies on a Go 1.27 compiler feature. Older Go versions print the positions with unresolved paths.

You don't have to upgrade your installed Go by hand. Since Go 1.21, the `go` command can switch toolchains on its own: with the default `GOTOOLCHAIN=auto`, when a module asks for a newer Go than the one installed, `go` downloads that version once (into the module cache) and runs it. This covers `go install …@latest` above and every `go` command inside an Orbit project. Check your setting with:

```sh
go env GOTOOLCHAIN
```

If it prints `local` (some Linux distributions set this), either run `go env -w GOTOOLCHAIN=auto` or install Go 1.27 from [go.dev/dl](https://go.dev/dl/). If your Go is older than 1.21, install Go 1.27 directly.

### Why `go.mod` has a `toolchain` line

Every project created by `orbit new` has two version lines in `go.mod`:

```
go 1.27
toolchain go1.27.3
```

- `go 1.27` is the minimum language version. Generated code may use Go 1.27 features.
- `toolchain go1.27.3` names the exact Go release to use: the one the `orbit` CLI was built with. Without it, `GOTOOLCHAIN=auto` would pick `go1.27.0`, the first release that satisfies `go 1.27`, instead of the current patch release.

To move to a newer Go release later, update the line with `go get toolchain@go1.27.4` (or edit it by hand).

## Projects pin their CLI version

The `orbit` CLI generates Go code that runs against the `github.com/orbit-z/orbit` runtime package, so the two must come from the same version. Each project pins the CLI in `go.mod` with a `tool` directive:

```
tool github.com/orbit-z/orbit/cmd/orbit
```

Inside a project, run the pinned CLI as `go tool orbit`:

```sh
go tool orbit build
```

You can keep typing plain `orbit`, too: inside a project with this directive, the globally installed `orbit` hands over to `go tool orbit` with the same arguments, so you always run the project's version. Scripts and CI should call `go tool orbit` directly, so they don't need a global install. The first `go tool orbit` in a project compiles the CLI, which takes a moment; later runs come from Go's build cache.

To upgrade a project, update the module. The CLI and the runtime move together, because they're the same module:

```sh
go get -tool github.com/orbit-z/orbit/cmd/orbit@latest
```

## Create a project

```sh
orbit new blog --module example.com/blog
cd blog
```

`orbit new` creates the `blog` directory from a built-in starter, generates `orbit_gen/` (Orbit's generated Go code), and runs `go mod tidy` to fetch dependencies.

`--module` sets the Go module path written to `go.mod`. Without it, the module path is the directory name (`orbit new blog` gives `module blog`), which is fine for a site you never import from elsewhere. These docs use the blog created above, with the module path `example.com/blog`, on every page.

When it's done, `orbit new` prints the next steps:

```
Created blog (module example.com/blog).

Next steps:

	cd blog
	orbit dev
```

If generating the code or `go mod tidy` fails, `orbit new` says which step failed and leaves the directory as it is, so you can look at it; delete it before running `orbit new` again.

### Trying Orbit before its first release

Orbit has no published release yet, so `go install …@latest` and the `require github.com/orbit-z/orbit` line that `orbit new` writes can't be downloaded. Build the CLI from a clone of the repository instead, and `orbit new` points the new project at that clone:

```sh
git clone https://github.com/orbit-z/orbit
cd orbit
go install ./cmd/orbit
cd ..
orbit new blog --module example.com/blog
```

A CLI built from a checkout (with `go install ./cmd/orbit`, `go build` or `go run`: any version that isn't a tagged release) looks for the checkout it was built from. When the directory is still there, `orbit new` adds a `replace` directive to `go.mod`:

```
require github.com/orbit-z/orbit v0.0.0

replace github.com/orbit-z/orbit => /home/you/orbit
```

`go tool orbit` then builds the CLI from your clone too, so a `git pull` there updates both the CLI and the runtime of every project that uses it. To use another checkout, or when the CLI can't find its sources (a binary built with `-trimpath`), set `ORBIT_REPLACE`:

```sh
ORBIT_REPLACE=/path/to/orbit orbit new blog
```

The `replace` names a directory on your machine, so the scaffolded `Dockerfile` can't build such a project as it is; see [Deploy](deploy.md#docker). Remove the `replace` line, and set the `require` line to a release, once Orbit is published.

## Start the dev server

```sh
orbit dev
```

Your global `orbit` hands the command to the version pinned in the project's `go.mod` (see [The pinned CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit)), so it always matches your project. Prefer it to `go tool orbit dev`: both run the same code, but the `go` command makes Ctrl-C exit with status 1.

Open <http://localhost:4321>. Use `orbit dev --port 3000` to pick another port. The dev server listens on localhost only; `orbit dev --host` makes it reachable from other devices on your network, such as a phone.

`orbit dev` compiles your `.orbit` files to Go, builds your server from `cmd/server/main.go`, and runs it behind a small proxy that adds live reload. See [Dev server](dev-server.md) for details.

## Edit a page

Open `src/pages/index.orbit`. Like an Astro component, it has a frontmatter block between `---` lines, followed by an HTML template. The frontmatter is Go:

```orbit
---
import (
    "time"

    Base "../layouts/Base.orbit"
)

year := time.Now().Year()
---
<Base title="My Orbit site">
  <h1>Welcome to Orbit</h1>
  <p>Edit <code>src/pages/index.orbit</code> and save: the page reloads.</p>
  <footer>Rendered by Orbit in {year}</footer>
</Base>
```

- The `import` block imports Go packages (`"time"`) and components. A component import is a `.orbit` path with a required alias (`Base "../layouts/Base.orbit"`), and the alias is the tag name in the template.
- Other statements (`year := …`) run every time the page is requested. Orbit renders every page on the server, per request; there's no static build step.
- `{year}` writes a Go expression into the HTML, escaped.

Because the page renders per request, the frontmatter can read the request through `ctx`. Greet visitors by a `?name=` query parameter: add these lines before `year := …`, and change the heading:

```orbit
name := ctx.Request.URL.Query().Get("name")
if name == "" {
    name = "Orbit"
}
```

```orbit
  <h1>Hello, {name}</h1>
```

Save, and open <http://localhost:4321/?name=Gopher>. The browser reloads with the change, usually in about a second.

## Add a component

Create `src/components/Card.orbit`. A component declares its inputs in a `Props` struct, and the template reads them through `props`:

```orbit file=src/components/Card.orbit
---
type Props struct {
    Title string
    Href  string
    Tag   string
}
---
<a class="card" href={props.Href}>
  <h2>
    {props.Title}
    {if props.Tag != ""}
      <span class="tag">{props.Tag}</span>
    {/if}
  </h2>
  <slot />
</a>
<style>
  .card {
    display: block;
    padding: 1rem 1.25rem;
    border: 1px solid #ddd;
    border-radius: 8px;
    color: inherit;
    text-decoration: none;
  }
  .card:hover { border-color: #888; }
  .tag {
    font-size: 0.75rem;
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
    background: #eef;
  }
</style>
<script>
  const cards = document.querySelectorAll<HTMLAnchorElement>("a.card");
  console.log(`${cards.length} card(s) on this page`);
</script>
```

- The `<style>` is scoped: it applies only to this component's elements, so `.card` and `.tag` can't clash with other components.
- `<slot />` is where the children passed to `<Card>` go.
- The `<script>` is TypeScript. Orbit bundles it into a file under `/_orbit/` and links it from the `<head>` of every page that uses `Card`.

Now use it from the page. Import it with an alias, and pass its props as attributes:

```orbit file=src/pages/index.orbit
---
import (
    "time"

    Base "../layouts/Base.orbit"
    Card "../components/Card.orbit"
)

name := ctx.Request.URL.Query().Get("name")
if name == "" {
    name = "Orbit"
}
year := time.Now().Year()
---
<Base title="My Orbit site">
  <h1>Hello, {name}</h1>
  <Card title="Read the docs" href="https://github.com/orbit-z/orbit">
    <p>Components, routing, server rendering and more.</p>
  </Card>
  <footer>Rendered by Orbit in {year}</footer>
</Base>
```

Attribute names match `Props` fields ignoring case, so `title` sets `Title`. Now pass a tag, but misspell the attribute on purpose:

```orbit file=src/pages/index.orbit expect-error at=17
  <Card tga="New" title="Read the docs" href="https://github.com/orbit-z/orbit">
```

Save. The browser shows an error overlay instead of the page, pointing at the attribute and listing the props `Card` accepts:

```text
src/pages/index.orbit:17:9: unknown prop "tga" on <Card> (props: title, href, tag)
```

Fix the typo (`tag="New"`) and save. The overlay goes away and the page reloads, now with the tag. Your Go code is type-checked the same way: a wrong type in an attribute or a typo in `{props.Titel}` is reported at its `.orbit` line and column.

## Build the server

Stop the dev server (Ctrl+C) and build:

```sh
orbit build
```

`orbit build` compiles your pages and your `cmd/server/main.go` into one Go binary, and bundles your CSS and scripts:

```
dist/
  server           your server, compiled; it renders every page per request
  client/
    _orbit/        hashed CSS and JS bundles
    favicon.svg    copied from public/
```

No page is written as an HTML file: the binary renders each one when it's requested. Run the build locally with:

```sh
orbit preview
```

It starts `dist/server`, which listens on <http://localhost:4321> like the dev server, without live reload.

## Deploy

You have three options, all covered in [Deploy](deploy.md):

- Copy `dist/server` and `dist/client/` to your server, side by side, and run `./server`.
- Build a single self-contained binary with `orbit build --embed`, and copy only `dist/server`.
- Build the scaffolded `Dockerfile` into a small image: `docker build -t blog .`, then `docker run -p 8080:8080 blog`.

If you run several instances from different builds at once, as in a rolling deploy, give them a shared island key: create one with `orbit create-key` and set it as `ORBIT_KEY`. See [Deploy](deploy.md#island-key-orbit_key).

## A tour of the starter

`orbit new` creates these files:

```
blog/
  go.mod                   module, Go version, toolchain line, pinned orbit CLI
  go.sum
  cmd/server/main.go       your server: Orbit's middleware plus your own routes
  src/
    pages/
      index.orbit          the home page, /
      404.orbit            the not-found page
    layouts/
      Base.orbit           the HTML shell shared by pages, with a scoped <style>
    env.d.ts               TypeScript declarations for your client scripts
  public/
    favicon.svg            served as-is at /favicon.svg
  tsconfig.json            TypeScript settings for <script> tags
  Dockerfile               builds a single-binary server image
  .dockerignore
  .gitignore               ignores orbit_gen/, .orbit/, dist/, node_modules/
  orbit_gen/               generated Go code (not committed)
```

There's no `orbit.json`: every setting has a default. Add one when you need it; see [Configuration](configuration.md). As the site grows, you add directories such as `src/components/`, `src/locals/` (typed request data) and `internal/` (your own Go packages); see [Project structure](project-structure.md).

### `go.mod`

```
module example.com/blog

go 1.27

toolchain go1.27.3

tool github.com/orbit-z/orbit/cmd/orbit

require github.com/orbit-z/orbit v0.1.0
```

The `require` line names the version of the CLI that ran `orbit new`. `go mod tidy` also adds `require` lines for the CLI's own dependencies (esbuild and a few others), because the `tool` directive makes them part of your module graph. The `orbit` runtime package your server imports uses only the standard library.

### `src/layouts/Base.orbit`

A layout is an ordinary component that renders the page shell around its default slot:

```orbit file=src/layouts/Base.orbit
---
type Props struct {
    Title string
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="icon" href="/favicon.svg">
    <title>{props.Title}</title>
  </head>
  <body>
    <main>
      <slot />
    </main>
  </body>
</html>
<style>
  main {
    max-width: 40rem;
    margin: 2rem auto;
    padding: 0 1rem;
    font-family: system-ui, sans-serif;
  }
</style>
```

You don't link CSS or JS yourself. Orbit adds the page's stylesheet and script tags just before `</head>`. The `<style>` is scoped to the layout's own elements, so it styles this `<main>` but not the elements that pages pass into the slot.

### `src/pages/404.orbit`

```orbit file=src/pages/404.orbit
---
import Base "../layouts/Base.orbit"
---
<Base title="Page not found">
  <h1>Page not found</h1>
  <p><a href="/">Go back home</a></p>
</Base>
```

The server renders it, with status 404, for any URL that no page or route handles. See [Routing](routing.md).

### `cmd/server/main.go`

Orbit is a `net/http` middleware, and this file is where you decide how it fits with your own handlers. It's yours to edit:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/orbit"

	"example.com/blog/orbit_gen/app"
)

func main() {
	site := app.New() // generated from src/: routes, pages and assets

	mux := http.NewServeMux()
	// Add your own routes here. More specific patterns win over "/":
	// mux.HandleFunc("GET /api/hello", hello)

	// Everything else goes to Orbit: pages, /_orbit/* assets and public/ files.
	mux.Handle("/", site.Handler())

	// Wrap mux in your own net/http middleware (logging, auth, locals) here.
	if err := orbit.ListenAndServe(mux); err != nil {
		log.Fatal(err)
	}
}
```

The `app` import is your module path plus `/orbit_gen/app`. `orbit dev`, `orbit preview` and the built binary all run this same `main`, so your routes and middleware behave the same in development and production. `orbit.ListenAndServe` listens on `:4321` by default, or on the port in `PORT` or the address in `ORBIT_ADDR`, and shuts down gracefully on SIGINT and SIGTERM. After a graceful shutdown it returns `nil`, so `log.Fatal` only runs (and exits with status 1) when the server fails, for example because the port is taken; Ctrl-C exits cleanly. See [Server](server.md) for custom routes and precedence, and [Middleware and locals](middleware.md) for middleware.

### TypeScript files

`tsconfig.json` and `src/env.d.ts` exist for TypeScript in `<script>` tags. Bundling doesn't need them, because esbuild only strips types. `orbit check` type-checks your scripts with `bun check`, Bun's built-in type checker, so there's no `package.json` and no `typescript` package to install. Add a `package.json` only if your scripts import npm packages. See [Styles and scripts](styles-and-scripts.md).

### `Dockerfile` and `.dockerignore`

A multi-stage build that compiles a single binary with `orbit build --embed` and copies it into a small distroless image. It works on the unmodified starter. See [Deploy](deploy.md#docker).

### `orbit_gen/`

The Go code Orbit generates from your `.orbit` files. Don't edit it: `orbit dev`, `orbit build` and `orbit generate` rewrite it. It's in `.gitignore`, so after a fresh clone, run `go tool orbit generate` before `go build` or `go vet`. See [Project structure](project-structure.md#fresh-clones-and-ci).

## See also

- [Project structure](project-structure.md): every directory, generated code, fresh clones and CI
- [Components](components.md): frontmatter, props, slots and template syntax
- [Routing](routing.md): file-based and dynamic routes
- [Rendering](rendering.md): the request lifecycle, status codes, redirects and errors
- [Server](server.md): `cmd/server/main.go`, custom routes and precedence
- [Deploy](deploy.md): the server binary, `--embed` and Docker
- [Editor setup](editor-setup.md): VS Code, Neovim and gopls
- [CLI reference](reference/cli.md)
