# Getting started

This page takes you from an empty machine to a running Lyr site: you install the CLI, create a project, edit it with live reload, build the server, and get it ready to deploy.

## What you need

- **Go 1.27 or later** (required). See [Go version](#go-version) below.
- **gopls v0.24.0 or later** (recommended), for completion and diagnostics in `.lyr` files. See [Editor setup](editor-setup.md).
- **Bun** (optional), only for TypeScript type checking in `lyr check`.
- **Docker** (optional), to build the scaffolded container image.

You don't need Node.js: Lyr bundles your client scripts with esbuild, which runs inside the `lyr` CLI.

## Install Lyr and run `lyr doctor`

Install the CLI with Go, then let `lyr doctor` check your setup:

```sh
go install github.com/orbit-z/lyr/cmd/lyr@latest
lyr doctor
```

`go install` puts the `lyr` binary in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` if `GOBIN` is unset. Make sure that directory is on your `PATH`.

`lyr doctor` checks Go, gopls and Bun, and prints the command that fixes each problem it finds. See [Checking your setup](#checking-your-setup-with-lyr-doctor) for what it reports.

## Create a project

```sh
lyr new blog --module example.com/blog
cd blog
```

`lyr new` creates the `blog` directory from a built-in starter, generates `lyr_gen/` (Lyr's generated Go code), and runs `go mod tidy` to fetch dependencies.

`--module` sets the Go module path written to `go.mod`. Without it, the module path is the directory name (`lyr new blog` gives `module blog`), which is fine for a site you never import from elsewhere. These docs use the blog created above, with the module path `example.com/blog`, on every page.

When it's done, `lyr new` prints the next steps:

```
Created blog (module example.com/blog).

Next steps:

	cd blog
	lyr dev
```

If generating the code or `go mod tidy` fails, `lyr new` says which step failed and leaves the directory as it is, so you can look at it; delete it before running `lyr new` again.

## Start the dev server

```sh
lyr dev
```

Your global `lyr` hands the command to the version pinned in the project's `go.mod` (see [The pinned CLI](reference/cli.md#the-pinned-cli-lyr-and-go-tool-lyr)), so it always matches your project. Prefer it to `go tool lyr dev`: both run the same code, but the `go` command makes Ctrl-C exit with status 1.

Open <http://localhost:4321>. Use `lyr dev --port 3000` to pick another port. The dev server listens on localhost only; `lyr dev --host` makes it reachable from other devices on your network, such as a phone.

`lyr dev` compiles your `.lyr` files to Go, builds your server from `cmd/server/main.go`, and runs it behind a small proxy that adds live reload. See [Dev server](dev-server.md) for details.

## Edit a page

Open `src/pages/index.lyr`. Like an Astro component, it has a frontmatter block between `---` lines, followed by an HTML template. The frontmatter is Go:

```lyr
---
import (
	"time"

	Base "../layouts/Base.lyr"
)

year := time.Now().Year()
---
<Base title="My Lyr site">
  <h1>Welcome to Lyr</h1>
  <p>Edit <code>src/pages/index.lyr</code> and save: the page reloads.</p>
  <footer>Rendered by Lyr in {year}</footer>
</Base>
```

- The `import` block imports Go packages (`"time"`) and components. A component import is a `.lyr` path with a required alias (`Base "../layouts/Base.lyr"`), and the alias is the tag name in the template.
- Other statements (`year := …`) run every time the page is requested. Lyr renders every page on the server, per request; there's no static build step.
- `{year}` writes a Go expression into the HTML, escaped.

Because the page renders per request, the frontmatter can read the request through `ctx`. Greet visitors by a `?name=` query parameter: add these lines before `year := …`, and change the heading:

```lyr
name := ctx.Request.URL.Query().Get("name")
if name == "" {
    name = "Lyr"
}
```

```lyr
  <h1>Hello, {name}</h1>
```

Save, and open <http://localhost:4321/?name=Gopher>. The browser reloads with the change, usually in about a second.

## Add a component

Create `src/components/Card.lyr`. A component declares its inputs in a `Props` struct, and the template reads them through `props`:

```lyr file=src/components/Card.lyr
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
      {" "}<span class="tag">{props.Tag}</span>
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
  .card:hover {
    border-color: #888;
  }
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
- `{" "}` writes a space. Like Astro, Lyr removes whitespace that contains a line break next to a tag, so the title and the tag would touch without it. See [Whitespace](reference/template-syntax.md#whitespace).
- The `<script>` is TypeScript. Lyr bundles it into a file under `/_lyr/` and links it from the `<head>` of every page that uses `Card`.

Now use it from the page. Import it with an alias, and pass its props as attributes:

```lyr file=src/pages/index.lyr
---
import (
	"time"

	Card "../components/Card.lyr"
	Base "../layouts/Base.lyr"
)

name := ctx.Request.URL.Query().Get("name")
if name == "" {
	name = "Lyr"
}
year := time.Now().Year()
---
<Base title="My Lyr site">
  <h1>Hello, {name}</h1>
  <Card title="Read the docs" href="https://github.com/orbit-z/lyr">
    <p>Components, routing, server rendering and more.</p>
  </Card>
  <footer>Rendered by Lyr in {year}</footer>
</Base>
```

Attribute names match `Props` fields ignoring case, so `title` sets `Title`. Now pass a tag, but misspell the attribute on purpose:

```lyr file=src/pages/index.lyr expect-error at=17
  <Card tga="New" title="Read the docs" href="https://github.com/orbit-z/lyr">
```

Save. The browser shows an error overlay instead of the page, pointing at the attribute and listing the props `Card` accepts:

```text
src/pages/index.lyr:17:9: unknown prop "tga" on <Card> (props: title, href, tag; add a lyr.RestAttrs field to pass HTML attributes through)
```

Fix the typo (`tag="New"`) and save. The overlay goes away and the page reloads, now with the tag. Your Go code is type-checked the same way: a wrong type in an attribute or a typo in `{props.Titel}` is reported at its `.lyr` line and column.

## Build the server

Stop the dev server (Ctrl+C) and build:

```sh
lyr build
```

`lyr build` compiles your pages and your `cmd/server/main.go` into one Go binary, and bundles your CSS and scripts:

```
dist/
  server           your server, compiled; it renders every page per request
  client/
    _lyr/        hashed CSS and JS bundles
    favicon.svg    copied from public/
```

No page is written as an HTML file: the binary renders each one when it's requested. Run the build locally with:

```sh
lyr preview
```

It starts `dist/server`, which listens on <http://localhost:4321> like the dev server, without live reload.

## Deploy

You have three options, all covered in [Deploy](deploy.md):

- Copy `dist/server` and `dist/client/` to your server, side by side, and run `./server`.
- Build a single self-contained binary with `lyr build --embed`, and copy only `dist/server`.
- Build the scaffolded `Dockerfile` into a small image: `docker build -t blog .`, then `docker run -p 8080:8080 blog`.

If you run several instances from different builds at once, as in a rolling deploy, give them a shared island key: create one with `lyr create-key` and set it as `LYR_KEY`. See [Deploy](deploy.md#island-key-lyr_key).

## Checking your setup with `lyr doctor`

`lyr doctor` prints one line per check: ✓ means it passed, ⚠ is a warning, and ✗ is a problem that blocks you. Every ⚠ and ✗ comes with the command that fixes it:

```
✓ go: go1.27.3
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); lyr check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
```

Outside a project, `lyr doctor` checks your environment only: the `go` command and its toolchain, gopls (for [editor support](editor-setup.md)), and Bun (for TypeScript type checking in `lyr check`). Inside a project it also checks `go.mod`, the pinned CLI version, the generated code, `.gitignore` and `lyr.json`. It exits with code 1 if any check fails; `lyr doctor --json` prints the same results as JSON.

Run `lyr doctor` again whenever something looks wrong. Lyr's own error messages point to it when the cause is in your environment, such as an old gopls or a CLI version mismatch.

## Go version

Lyr needs **Go 1.27 or later**. This is a hard minimum, not a recommendation: Lyr reports errors at their position in your `.lyr` files (`src/pages/index.lyr:17:9`), and that relies on a Go 1.27 compiler feature. Older Go versions print the positions with unresolved paths.

You don't have to upgrade your installed Go by hand. Since Go 1.21, the `go` command can switch toolchains on its own: with the default `GOTOOLCHAIN=auto`, when a module asks for a newer Go than the one installed, `go` downloads that version once (into the module cache) and runs it. This covers `go install …@latest` above and every `go` command inside a Lyr project. Check your setting with:

```sh
go env GOTOOLCHAIN
```

If it prints `local` (some Linux distributions set this), either run `go env -w GOTOOLCHAIN=auto` or install Go 1.27 from [go.dev/dl](https://go.dev/dl/). If your Go is older than 1.21, install Go 1.27 directly.

### Why `go.mod` has a `toolchain` line

Every project created by `lyr new` has two version lines in `go.mod`:

```
go 1.27
toolchain go1.27.3
```

- `go 1.27` is the minimum language version. Generated code may use Go 1.27 features.
- `toolchain go1.27.3` names the exact Go release to use: the one the `lyr` CLI was built with. Without it, `GOTOOLCHAIN=auto` would pick `go1.27.0`, the first release that satisfies `go 1.27`, instead of the current patch release.

To move to a newer Go release later, update the line with `go get toolchain@go1.27.4` (or edit it by hand).

## Projects pin their CLI version

The `lyr` CLI generates Go code that runs against the `github.com/orbit-z/lyr` runtime package, so the two must come from the same version. Each project pins the CLI in `go.mod` with a `tool` directive:

```
tool github.com/orbit-z/lyr/cmd/lyr
```

Inside a project, run the pinned CLI as `go tool lyr`:

```sh
go tool lyr build
```

You can keep typing plain `lyr`, too: inside a project with this directive, the globally installed `lyr` hands over to `go tool lyr` with the same arguments, so you always run the project's version. Scripts and CI should call `go tool lyr` directly, so they don't need a global install. The first `go tool lyr` in a project compiles the CLI, which takes a moment; later runs come from Go's build cache.

To upgrade a project, update the module. The CLI and the runtime move together, because they're the same module:

```sh
go get -tool github.com/orbit-z/lyr/cmd/lyr@latest
```

## A tour of the starter

`lyr new` creates these files:

```
blog/
  go.mod                   module, Go version, toolchain line, pinned lyr CLI
  go.sum
  cmd/server/main.go       your server: Lyr's middleware plus your own routes
  src/
    pages/
      index.lyr          the home page, /
      404.lyr            the not-found page
    layouts/
      Base.lyr           the HTML shell shared by pages, with a scoped <style>
    env.d.ts               TypeScript declarations for your client scripts
  public/
    favicon.svg            served as-is at /favicon.svg
  tsconfig.json            TypeScript settings for <script> tags
  Dockerfile               builds a single-binary server image
  .dockerignore
  .gitignore               ignores lyr_gen/, .lyr/, dist/, node_modules/
  lyr_gen/               generated Go code (not committed)
```

There's no `lyr.json`: every setting has a default. Add one when you need it; see [Configuration](configuration.md). As the site grows, you add directories such as `src/components/`, `src/locals/` (typed request data) and `internal/` (your own Go packages); see [Project structure](project-structure.md).

### `go.mod`

```
module example.com/blog

go 1.27

toolchain go1.27.3

tool github.com/orbit-z/lyr/cmd/lyr

require github.com/orbit-z/lyr v0.1.0
```

The `require` line names the version of the CLI that ran `lyr new`. `go mod tidy` also adds `require` lines for the CLI's own dependencies (esbuild and a few others), because the `tool` directive makes them part of your module graph. The `lyr` runtime package your server imports uses only the standard library.

### `src/layouts/Base.lyr`

A layout is an ordinary component that renders the page shell around its default slot:

```lyr file=src/layouts/Base.lyr
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

You don't link CSS or JS yourself. Lyr adds the page's stylesheet and script tags just before `</head>`. The `<style>` is scoped to the layout's own elements, so it styles this `<main>` but not the elements that pages pass into the slot.

### `src/pages/404.lyr`

```lyr file=src/pages/404.lyr
---
import Base "../layouts/Base.lyr"
---
<Base title="Page not found">
  <h1>Page not found</h1>
  <p><a href="/">Go back home</a></p>
</Base>
```

The server renders it, with status 404, for any URL that no page or route handles. See [Routing](routing.md).

### `cmd/server/main.go`

Lyr is a `net/http` middleware, and this file is where you decide how it fits with your own handlers. It's yours to edit:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/lyr"

	"example.com/blog/lyr_gen/app"
)

func main() {
	site := app.New() // generated from src/: routes, pages and assets

	mux := http.NewServeMux()
	// Add your own routes here. More specific patterns win over "/":
	// mux.HandleFunc("GET /api/hello", hello)

	// Everything else goes to Lyr: pages, /_lyr/* assets and public/ files.
	mux.Handle("/", site.Handler())

	// Trailing-slash redirects (lyr.json's trailingSlash) run first, as in
	// Astro. Wrap mux in your own net/http middleware (logging, auth, locals)
	// inside it: site.TrailingSlash(logging(mux)).
	if err := lyr.ListenAndServe(site.TrailingSlash(mux)); err != nil {
		log.Fatal(err)
	}
}
```

The `app` import is your module path plus `/lyr_gen/app`. `lyr dev`, `lyr preview` and the built binary all run this same `main`, so your routes and middleware behave the same in development and production. `site.TrailingSlash` sends the 308 for the other form of a page URL (`/about/` to `/about`) before your middleware runs, as Astro does (see [Trailing slashes](routing.md#redirecting-before-your-middleware)). `lyr.ListenAndServe` listens on `:4321` by default, or on the port in `PORT` or the address in `LYR_ADDR`, and shuts down gracefully on SIGINT and SIGTERM. After a graceful shutdown it returns `nil`, so `log.Fatal` only runs (and exits with status 1) when the server fails, for example because the port is taken; Ctrl-C exits cleanly. See [Server](server.md) for custom routes and precedence, and [Middleware and locals](middleware.md) for middleware.

### TypeScript files

`tsconfig.json` and `src/env.d.ts` exist for TypeScript in `<script>` tags. Bundling doesn't need them, because esbuild only strips types. `lyr check` type-checks your scripts with `bun check`, Bun's built-in type checker, so there's no `package.json` and no `typescript` package to install. Add a `package.json` only if your scripts import npm packages. See [Styles and scripts](styles-and-scripts.md).

### `Dockerfile` and `.dockerignore`

A multi-stage build that compiles a single binary with `lyr build --embed` and copies it into a small distroless image. It works on the unmodified starter. See [Deploy](deploy.md#docker).

### `lyr_gen/`

The Go code Lyr generates from your `.lyr` files. Don't edit it: `lyr dev`, `lyr build` and `lyr generate` rewrite it. It's in `.gitignore`, so after a fresh clone, run `go tool lyr generate` before `go build` or `go vet`. See [Project structure](project-structure.md#fresh-clones-and-ci).

## See also

- [Project structure](project-structure.md): every directory, generated code, fresh clones and CI
- [Components](components.md): frontmatter, props, slots and template syntax
- [Routing](routing.md): file-based and dynamic routes
- [Rendering](rendering.md): the request lifecycle, status codes, redirects and errors
- [Server](server.md): `cmd/server/main.go`, custom routes and precedence
- [Deploy](deploy.md): the server binary, `--embed` and Docker
- [Using Tailwind](tailwind.md): add Tailwind CSS with one stylesheet
- [Editor setup](editor-setup.md): VS Code, Neovim and gopls
- [CLI reference](reference/cli.md)
