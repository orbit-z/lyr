# Project structure

An Orbit project is an ordinary Go module with a `src/` directory for your `.orbit` files; this page explains each directory, what Orbit generates, and how to set up fresh clones and CI.

## Layout

Here's the example blog used throughout these docs (module `example.com/blog`):

```
blog/
  go.mod                  module example.com/blog; pins the orbit CLI with a tool directive
  go.sum
  orbit.json              optional config
  cmd/
    server/
      main.go             your server: Orbit's middleware plus your own handlers
  src/
    pages/                file-based routes
      index.orbit         /
      about.md            /about: a Markdown page
      404.orbit           the not-found page
      blog/
        [slug].orbit      /blog/<slug>
    content/              optional Markdown files that pages import
      intro.md
    components/
      Card.orbit
    layouts/
      Base.orbit
    locals/               optional Go package: your typed Locals struct
      locals.go
    scripts/              optional .ts modules imported from <script> tags
  internal/
    posts/                your own Go code
  public/                 static files, served as-is at the site root
  orbit_gen/              generated Go packages (gitignored)
  .orbit/                 cache (gitignored)
  dist/                   build output (gitignored)
```

## `src/pages/`

Every `.orbit` file under `src/pages/` is a page, and its path is its URL: `src/pages/about.orbit` is served at `/about`, and `index.orbit` is its directory's root (`src/pages/blog/index.orbit` is `/blog`). URLs have no trailing slash; a request for `/about/` is redirected to `/about`. Brackets make a dynamic segment (`[slug].orbit`, `[...rest].orbit`), and `404.orbit` renders unknown URLs. A file whose name, or the name of a directory on its path, starts with `_` isn't a page but a component, as in Astro: `src/pages/blog/_parts/Byline.orbit` has no URL. A Markdown file (`.md`, `.markdown`, …) under `src/pages/` is a page too, with the same rules (see [Markdown pages](markdown.md#markdown-pages)). Every page is rendered on the server for each request. [Routing](routing.md) covers the rules.

## `src/components/` and `src/layouts/`

Every `.orbit` file under `src/` that isn't a page is a component: the files outside `src/pages/`, and the `_` files inside it. `components/` and `layouts/` are conventions, not special directories: a layout is just a component that renders the `<html>` shell around a `<slot />`. You can organize components in any directory under `src/`.

A page or component imports another with a relative path and an alias:

```
import Card "../components/Card.orbit"
```

See [Components](components.md).

Orbit finds every `.orbit` and Markdown file under `src/`. It skips files and directories whose names start with `.`, and follows symlinked directories, except a link that leads back to a directory it's already in. A project without a `src/` directory is an error, so running `orbit generate` in the wrong module doesn't leave an `orbit_gen/` there:

```text
orbit: /home/me/blog has no src/ directory; run orbit in an Orbit project
```

## `src/content/`

A Markdown file outside `src/pages/` isn't a route: it's compiled only when an `.orbit` file imports it (`Intro "../content/intro.md"`), so a stray `README.md` under `src/` never breaks a build. `content/` is a convention, not a special directory: Markdown files can live anywhere under `src/`. See [Importing Markdown into a component](markdown.md#importing-markdown-into-a-component).

## `src/locals/`

An optional Go package (`package locals`, import path `example.com/blog/src/locals`) that declares your typed request-scoped data, such as the current user or a request ID:

```go file=src/locals/locals.go
package locals

// Locals holds request-scoped data. Middleware in cmd/server sets it;
// every page and component reads it as `locals`.
type Locals struct {
	RequestID string
	Theme     string
}
```

Orbit looks for `type Locals struct` in this package. When it finds one, every page and component gets a `locals` variable of type `*locals.Locals`, fully type-checked and completed by your editor. Your net/http middleware fills it in with `orbit.EnsureLocals[locals.Locals](r)`, and your own handlers read it with `orbit.LocalsFromRequest[locals.Locals](r)`. Without the package, `locals` is an empty `*orbit.NoLocals`.

Keep `src/locals` a plain types package. It can import your own packages (such as an `internal/auth` for a `User` type), but neither it nor anything it imports may import `orbit_gen/app`: that would be an import cycle, because every generated page imports `src/locals`. Orbit reports it clearly, and so does `orbit doctor`. See [Middleware and locals](middleware.md).

## Client scripts

`<script>` tags are TypeScript, and they can import `.ts`, `.tsx` and `.js` files from anywhere in the project; `src/scripts/` is a good home for them. Bare imports (`import confetti from "canvas-confetti"`) resolve from `node_modules/` if you have one. `tsconfig.json` and `src/env.d.ts` configure TypeScript; a `package.json` is needed only for npm packages. See [Styles and scripts](styles-and-scripts.md).

## `cmd/server/`

Your server's `main` package. It creates the site with `app.New()` from `orbit_gen/app`, places Orbit's middleware in your handler chain next to your own routes and middleware, and listens. `orbit new` scaffolds it, and it's yours to edit. `orbit dev` runs it during development, and `orbit build` compiles it to `dist/server`.

If you delete `cmd/server/`, Orbit generates a default entry that serves only your pages. To use another directory, set `server` in [`orbit.json`](configuration.md#server). See [Server](server.md).

## `internal/` and other Go code

Everything else is a normal Go module. Put your data access, content loaders and helpers in packages such as `internal/posts`, and import them from frontmatter:

```
import "example.com/blog/internal/posts"
```

## `public/`

Files in `public/` are served unchanged at the root of the site: `public/robots.txt` is served at `/robots.txt`. They're not processed or hashed. `orbit build` copies them into `dist/client/`, and `orbit dev` serves them straight from `public/`. A file under `public/_orbit/` is a build error (each one is listed), because `/_orbit/` is where Orbit serves its bundles. Dotfiles are copied; a symbolic link to a file is copied as that file, and a symbolic link to a directory is a build error.

## `orbit_gen/`: generated Go code

Orbit compiles every `.orbit` file to Go packages under `orbit_gen/`. Each generated file starts with `// Code generated by orbit. DO NOT EDIT.`, so don't edit them: `orbit generate`, `orbit build` and `orbit dev` rewrite them.

A file whose content hasn't changed isn't rewritten, so its modification time stays the same and Go's build cache stays warm; a changed file is written to a temporary file and renamed into place. Generated files that are no longer needed (for a deleted `.orbit` file) are removed, then the directories they leave empty. Only files that start with the generated-code line are removed, so a file of your own under `orbit_gen/` is kept, and in `orbit_gen/static/` Orbit only writes its `doc.go` and never removes anything.

```
orbit_gen/
  decl/        one declaration package per .orbit file: its Props type and Render function
               (and, for a page, its generated Params type)
  impl/        the rendering code for each .orbit file; never import these
  app/         app.New(): the route table and the wiring of every page
  link/        imports every implementation package, so each component is linked
               to its code (app imports it); never import it yourself
  manifest/    the CSS and JS URLs of each page
  cmd/server/  the default server, generated only when you have no cmd/server
  static/      an embedded copy of dist/client, only for orbit build --embed
```

Your own Go code imports two kinds of generated packages:

- `orbit_gen/app`, for `app.New()` in `cmd/server/main.go`.
- A component's **declaration** package, to render it from a handler or test it:

  ```
  import card "example.com/blog/orbit_gen/decl/components/card"
  ```

  Code that renders components must also link the app (`cmd/server` does, through `app.New()`; tests add `import _ "example.com/blog/orbit_gen/app"`). Otherwise `Render` panics with `orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app`. See [Testing](testing.md).

### Package paths

A file's generated packages mirror its path under `src/`, with `src/` dropped:

| Source file | Declaration package | Package name |
|---|---|---|
| `src/components/Card.orbit` | `orbit_gen/decl/components/card` | `card` |
| `src/components/my-card.orbit` | `orbit_gen/decl/components/my_card` | `my_card` |
| `src/layouts/Base.orbit` | `orbit_gen/decl/layouts/base` | `base` |
| `src/pages/index.orbit` | `orbit_gen/decl/pages/index` | `index` |
| `src/pages/blog/[slug].orbit` | `orbit_gen/decl/pages/blog/slug_param` | `slug_param` |
| `src/pages/docs/[...rest].orbit` | `orbit_gen/decl/pages/docs/rest_rest` | `rest_rest` |
| `src/pages/404.orbit` | `orbit_gen/decl/pages/p_404` | `p_404` |

The rules:

- Each path segment is lowercased, and `-`, `.` and any other character that isn't valid in a Go identifier becomes `_`.
- `[name]` becomes `name_param`, and `[...name]` becomes `name_rest`.
- A segment that is a Go keyword (`default`, `type`, `func`, …), `main`, `init`, `internal`, `testdata` or `vendor`, or that starts with a digit or `_`, gets a `p_` prefix: `404` becomes `p_404`, and `default.orbit` becomes `p_default`.
- Two files that map to the same package are an error, reported at the later path (in sorted order) and naming the other one. Rename one:

  ```text
  src/components/my_card.orbit: generates the same Go package as src/components/my-card.orbit (orbit_gen/decl/components/my_card); rename one of them
  ```

The implementation package has the same path under `orbit_gen/impl/`, with an `_impl` suffix on its name (`card_impl`). Go error messages never show these names: Orbit rewrites them to the alias you used in your `.orbit` file (`Card.Props`).

## `.orbit/`: cache

Files that Orbit reuses between runs: the server binaries that `orbit dev` builds and runs (`.orbit/devserver/`), the assets it bundles (`.orbit/dev/`), and the scripts `orbit check` extracts for TypeScript (`.orbit/check/`). It's gitignored and safe to delete at any time.

## `dist/`: build output

`orbit build` writes the server binary and the files it serves:

```
dist/
  server           your cmd/server, compiled, with every page in it
  client/
    _orbit/        hashed CSS and JS bundles
    robots.txt     from public/
```

There are no HTML files: the binary renders every page per request. At startup, it serves static files from the `client/` directory next to it. With `orbit build --embed`, those files are also embedded in the binary, so `dist/server` runs on its own. See [Deploy](deploy.md).

## `.gitignore`

`orbit new` writes a `.gitignore` that covers the generated and built directories:

```text file=.gitignore
orbit_gen/
.orbit/
dist/
node_modules/
```

`orbit doctor` warns if `orbit_gen/`, `.orbit/` or `dist/` isn't ignored.

## Fresh clones and CI

Because `orbit_gen/` isn't committed, a fresh clone doesn't have it, and `go build`, `go vet` and `go mod tidy` fail until you generate it: `cmd/server/main.go` imports `orbit_gen/app`. Run `orbit generate` first:

```sh
go tool orbit generate     # creates orbit_gen/
go vet ./...
go test ./...
go tool orbit build
```

Use the same steps in CI. `go tool orbit` runs the CLI version pinned in `go.mod`, so CI needs only Go, not a global `orbit` install. You can also run `go tool orbit check` to type-check your `.orbit` files and, with Bun installed, your TypeScript.

To check that your `.orbit` files are formatted, add `go tool orbit fmt -l` (see [`orbit fmt`](reference/cli.md#orbit-fmt)). It lists the files that aren't formatted and exits with code 1 if there are any, so the step fails and names them. It needs no generated code, so it can run before `orbit generate`. Install Bun in CI too if you want it to check `<script>` and `<style>` formatting; without Bun, it skips them.

Two Go tools see `orbit_gen/` as ordinary Go code:

- **`gofmt -l .`** lists the generated files, because Orbit never gofmts them (formatting would move the code away from its line directives, which point into your `.orbit` files). Check only your own files: `gofmt -l $(git ls-files '*.go')`, or `test -z "$(gofmt -l $(git ls-files '*.go'))"` to fail the CI step when any needs formatting.
- **`go test ./...`** prints `? … [no test files]` for every generated package. That's harmless; to keep the output short, leave them out: `go test $(go list ./... | grep -v /orbit_gen/)`.

## Committing `orbit_gen/` (optional)

If you'd rather have `go build ./...` work right after a clone (for tools that don't know about Orbit, or for `go install` of your server), commit `orbit_gen/`: replace the `orbit_gen/` line in `.gitignore` with these two lines:

```gitignore
orbit_gen/static/client/
orbit_gen/static/static.go
```

They're the copy of your built files that only `orbit build --embed` writes, so keep them ignored. Commit `orbit_gen/static/doc.go`: it keeps the package present for `go mod tidy` and `go vet`.

Generation is deterministic, so CI can check that the committed code is up to date:

```sh
go tool orbit generate && git diff --exit-code orbit_gen
```

Generated code uses relative file names in its line directives, so the committed files are the same on every machine and OS. Nothing random is written to `orbit_gen/` either: the island key and build ID that each `orbit build` creates are passed to the Go linker instead (see [Deploy](deploy.md#island-key-orbit_key)).

A server built with plain `go build` works, but it has no build key: it creates a random island key per process and warns about it on startup. Use `orbit build` for anything you deploy.

## See also

- [Getting started](getting-started.md)
- [Routing](routing.md): how file names become URLs
- [Server](server.md): `cmd/server/main.go`
- [Middleware and locals](middleware.md): middleware and the `Locals` struct
- [Configuration](configuration.md): `orbit.json`
- [Deploy](deploy.md): what to ship from `dist/`
