# Project structure

A Lyr project is an ordinary Go module with a `src/` directory for your `.lyr` files; this page explains each directory, what Lyr generates, and how to set up fresh clones and CI.

## Layout

Here's the example blog used throughout these docs (module `example.com/blog`):

```
blog/
  go.mod                  module example.com/blog; pins the lyr CLI with a tool directive
  go.sum
  lyr.json              optional config
  cmd/
    server/
      main.go             your server: Lyr's middleware plus your own handlers
  src/
    pages/                file-based routes
      index.lyr         /
      about.md            /about: a Markdown page
      404.lyr           the not-found page
      blog/
        [slug].lyr      /blog/<slug>
    content/              optional Markdown files that pages import
      intro.md
    components/
      Card.lyr
    layouts/
      Base.lyr
    locals/               optional Go package: your typed Locals struct
      locals.go
    scripts/              optional .ts modules imported from <script> tags
  internal/
    posts/                your own Go code
  public/                 static files, served as-is at the site root
  lyr_gen/              generated Go packages and TypeScript types (gitignored)
  .lyr/                 dev assets, check files and lyr build's overlay (gitignored)
  dist/                   build output (gitignored)
```

## `src/pages/`

Every `.lyr` file under `src/pages/` is a page, and its path is its URL: `src/pages/about.lyr` is served at `/about`, and `index.lyr` is its directory's root (`src/pages/blog/index.lyr` is `/blog`). URLs have no trailing slash by default; a request for `/about/` is redirected to `/about` (`lyr.json`'s `trailingSlash` can flip that). Brackets make a dynamic segment (`[slug].lyr`, `[...rest].lyr`), and `404.lyr` renders unknown URLs. A file whose name, or the name of a directory on its path, starts with `_` isn't a page but a component, as in Astro: `src/pages/blog/_parts/Byline.lyr` has no URL. A Markdown file (`.md`, `.markdown`, …) under `src/pages/` is a page too, with the same rules (see [Markdown pages](markdown.md#markdown-pages)). Every page is rendered on the server for each request. [Routing](routing.md) covers the rules.

## `src/components/` and `src/layouts/`

Every `.lyr` file under `src/` that isn't a page is a component: the files outside `src/pages/`, and the `_` files inside it. `components/` and `layouts/` are conventions, not special directories: a layout is just a component that renders the `<html>` shell around a `<slot />`. You can organize components in any directory under `src/`.

A page or component imports another with a relative path and an alias:

```
import Card "../components/Card.lyr"
```

or with an alias from `tsconfig.json`'s `compilerOptions.paths`. The starter `tsconfig.json` maps `@/` to `src/` (`"paths": { "@/*": ["./src/*"] }`), so `Card "@/components/Card.lyr"` works from any directory. Scripts use the same aliases. See [Components](components.md) and [Import aliases](components.md#import-aliases).

Lyr finds every `.lyr` and Markdown file under `src/`. It skips files and directories whose names start with `.`, and follows symlinked directories, except a link that leads back to a directory it's already in. A project without a `src/` directory is an error, so running `lyr generate` in the wrong module doesn't leave a `lyr_gen/` there:

```text
lyr: /home/me/blog has no src/ directory; run lyr in a Lyr project
```

## `src/content/`

A Markdown file outside `src/pages/` isn't a route: it's compiled only when a `.lyr` file imports it (`Intro "../content/intro.md"`), so a stray `README.md` under `src/` never breaks a build. `content/` is a convention, not a special directory: Markdown files can live anywhere under `src/`. See [Importing Markdown into a component](markdown.md#importing-markdown-into-a-component).

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

Lyr looks for `type Locals struct` in this package. When it finds one, every page and component gets a `locals` variable of type `*locals.Locals`, fully type-checked and completed by your editor. Your net/http middleware fills it in with `lyr.EnsureLocals[locals.Locals](r)`, and your own handlers read it with `lyr.LocalsFromRequest[locals.Locals](r)`. Without the package, `locals` is an empty `*lyr.NoLocals`.

Keep `src/locals` a plain types package. It can import your own packages (such as an `internal/auth` for a `User` type), but neither it nor anything it imports may import `lyr_gen/app`: that would be an import cycle, because every generated page imports `src/locals`. Lyr reports it clearly, and so does `lyr doctor`. See [Middleware and locals](middleware.md).

## Client scripts

`<script>` tags are TypeScript, and they can import `.ts`, `.tsx` and `.js` files from anywhere in the project; `src/scripts/` is a good home for them. Bare imports (`import confetti from "canvas-confetti"`) resolve from `node_modules/` if you have one. `tsconfig.json` and `src/env.d.ts` configure TypeScript; a `package.json` is needed only for npm packages. See [Styles and scripts](styles-and-scripts.md).

A script can also import a component's `.lyr` file (`import { data } from "./LikeButton.lyr"`) to read the data that component writes with `lyr.JSON` or `set:json`, typed from Go. The types are generated into `lyr_gen/types/`. The starter `tsconfig.json` points TypeScript at them with two entries:

```json
{
  "compilerOptions": {
    "rootDirs": [".", "lyr_gen/types"]
  },
  "include": ["src/**/*.ts", "lyr_gen/types/*.d.ts"]
}
```

`rootDirs` overlays `lyr_gen/types/src/` onto `src/`, so `./LikeButton.lyr` resolves to the generated `LikeButton.lyr.d.ts`. A project created before these entries existed needs them only for editor support (`lyr check` adds them itself, and `lyr doctor` warns when they're missing). See [Client modules](reference/client-modules.md).

## `cmd/server/`

Your server's `main` package. It creates the site with `app.New()` from `lyr_gen/app`, places Lyr's middleware in your handler chain next to your own routes and middleware, and listens. `lyr new` scaffolds it, and it's yours to edit. `lyr dev` runs it during development, and `lyr build` compiles it to `dist/server`.

If you delete `cmd/server/`, Lyr generates a default entry that serves only your pages. To use another directory, set `server` in [`lyr.json`](configuration.md#server). See [Server](server.md).

## `internal/` and other Go code

Everything else is a normal Go module. Put your data access, content loaders and helpers in packages such as `internal/posts`, and import them from frontmatter:

```
import "example.com/blog/internal/posts"
```

## `public/`

Files in `public/` are served unchanged at the root of the site: `public/robots.txt` is served at `/robots.txt`. They're not processed or hashed. `lyr build` copies them into `dist/client/`, and `lyr dev` serves them straight from `public/`. A file under `public/_lyr/` is a build error (each one is listed), because `/_lyr/` is where Lyr serves its bundles. Dotfiles are copied; a symbolic link to a file is copied as that file, and a symbolic link to a directory is a build error.

## `lyr_gen/`: generated Go code

Lyr compiles every `.lyr` file to Go packages under `lyr_gen/`. Each generated file starts with `// Code generated by lyr. DO NOT EDIT.`, so don't edit them: `lyr generate`, `lyr build` and `lyr dev` rewrite them.

A file whose content hasn't changed isn't rewritten, so its modification time stays the same and Go's build cache stays warm; a changed file is written to a temporary file and renamed into place. Generated files that are no longer needed (for a deleted `.lyr` file) are removed, then the directories they leave empty. Only files that start with the generated-code line are removed, so a file of your own under `lyr_gen/` is kept, and in `lyr_gen/static/` Lyr only writes its `doc.go` and never removes anything.

```
lyr_gen/
  decl/        one declaration package per .lyr file: its Props type and Render function
               (and, for a page, its generated Params type)
  impl/        the rendering code for each .lyr file; never import these
  app/         app.New(): the route table and the wiring of every page
  link/        imports every implementation package, so each component is linked
               to its code (app imports it); never import it yourself
  manifest/    the CSS and JS URLs of each page
  cmd/server/  the default server, generated only when you have no cmd/server
  static/      an embedded copy of dist/client, only for lyr build --embed
  types/       TypeScript declarations for scripts: go.d.ts (lyr:types, your Go types),
               client.d.ts (lyr:client) and src/…/X.lyr.d.ts (each component's data)
```

`lyr_gen/types/` isn't Go, so the `go` command ignores it. It holds a `.d.ts` file only for components that write typed data (a `lyr.JSON` `data-*` attribute, or a `set:json` block with a static `id`). `lyr check` also writes it.

Your own Go code imports two kinds of generated packages:

- `lyr_gen/app`, for `app.New()` in `cmd/server/main.go`.
- A component's **declaration** package, to render it from a handler or test it:

  ```
  import card "example.com/blog/lyr_gen/decl/components/card"
  ```

  Code that renders components must also link the app (`cmd/server` does, through `app.New()`; tests add `import _ "example.com/blog/lyr_gen/app"`). Otherwise `Render` panics with `lyr: src/components/Card.lyr not linked; import example.com/blog/lyr_gen/app`. See [Testing](testing.md).

`lyr_gen/` holds the version of the code that `lyr dev` runs, in which components call each other through their declaration package. `lyr build` doesn't change it: it compiles a version with direct calls from `.lyr/build/`, over `lyr_gen/` with `go build -overlay`. So a committed `lyr_gen/` still matches `lyr generate` after a build, and a plain `go build` from it gives a correct server, but not the production one (see [Direct calls in production builds](performance.md#direct-calls-in-production-builds)).

### Package paths

A file's generated packages mirror its path under `src/`, with `src/` dropped:

| Source file | Declaration package | Package name |
|---|---|---|
| `src/components/Card.lyr` | `lyr_gen/decl/components/card` | `card` |
| `src/components/my-card.lyr` | `lyr_gen/decl/components/my_card` | `my_card` |
| `src/layouts/Base.lyr` | `lyr_gen/decl/layouts/base` | `base` |
| `src/pages/index.lyr` | `lyr_gen/decl/pages/index` | `index` |
| `src/pages/blog/[slug].lyr` | `lyr_gen/decl/pages/blog/slug_param` | `slug_param` |
| `src/pages/docs/[...rest].lyr` | `lyr_gen/decl/pages/docs/rest_rest` | `rest_rest` |
| `src/pages/404.lyr` | `lyr_gen/decl/pages/p_404` | `p_404` |

The rules:

- Each path segment is lowercased, and `-`, `.` and any other character that isn't valid in a Go identifier becomes `_`.
- `[name]` becomes `name_param`, and `[...name]` becomes `name_rest`.
- A segment that is a Go keyword (`default`, `type`, `func`, …), `main`, `init`, `internal`, `testdata` or `vendor`, or that starts with a digit or `_`, gets a `p_` prefix: `404` becomes `p_404`, and `default.lyr` becomes `p_default`.
- Two files that map to the same package are an error, reported at the later path (in sorted order) and naming the other one. Rename one:

  ```text
  src/components/my_card.lyr: generates the same Go package as src/components/my-card.lyr (lyr_gen/decl/components/my_card); rename one of them
  ```

The implementation package has the same path under `lyr_gen/impl/`, with an `_impl` suffix on its name (`card_impl`). Go error messages never show these names: Lyr rewrites them to the alias you used in your `.lyr` file (`Card.Props`).

## `.lyr/`: cache

Files that Lyr reuses between runs: the server binaries that `lyr dev` builds and runs (`.lyr/devserver/`), the assets it bundles (`.lyr/dev/`), the scripts `lyr check` extracts for TypeScript (`.lyr/check/`), and the production version of the generated code that `lyr build` compiles (`.lyr/build/`, with its `overlay.json`, rewritten on every build; see [Direct calls in production builds](performance.md#direct-calls-in-production-builds)). It's gitignored and safe to delete at any time.

## `dist/`: build output

`lyr build` writes the server binary and the files it serves:

```
dist/
  server           your cmd/server, compiled, with every page in it
  client/
    _lyr/        hashed CSS and JS bundles
    robots.txt     from public/
```

There are no HTML files: the binary renders every page per request. At startup, it serves static files from the `client/` directory next to it. With `lyr build --embed`, those files are also embedded in the binary, so `dist/server` runs on its own. See [Deploy](deploy.md).

## `.gitignore`

`lyr new` writes a `.gitignore` that covers the generated and built directories:

```text file=.gitignore
lyr_gen/
.lyr/
dist/
node_modules/
```

`lyr doctor` warns if `lyr_gen/`, `.lyr/` or `dist/` isn't ignored.

## Fresh clones and CI

Because `lyr_gen/` isn't committed, a fresh clone doesn't have it, and `go build`, `go vet` and `go mod tidy` fail until you generate it: `cmd/server/main.go` imports `lyr_gen/app`. Run `lyr generate` first:

```sh
go tool lyr generate     # creates lyr_gen/
go vet ./...
go test ./...
go tool lyr build
```

Use the same steps in CI. `go tool lyr` runs the CLI version pinned in `go.mod`, so CI needs only Go, not a global `lyr` install. You can also run `go tool lyr check` to type-check your `.lyr` files and, with Bun installed, your TypeScript.

To check that your `.lyr` files are formatted, add `go tool lyr fmt -l` (see [`lyr fmt`](reference/cli.md#lyr-fmt)). It lists the files that aren't formatted and exits with code 1 if there are any, so the step fails and names them. It needs no generated code, so it can run before `lyr generate`. Install Bun in CI too if you want it to check `<script>` and `<style>` formatting; without Bun, it skips them.

Two Go tools see `lyr_gen/` as ordinary Go code:

- **`gofmt -l .`** lists the generated files, because Lyr never gofmts them (formatting would move the code away from its line directives, which point into your `.lyr` files). Check only your own files: `gofmt -l $(git ls-files '*.go')`, or `test -z "$(gofmt -l $(git ls-files '*.go'))"` to fail the CI step when any needs formatting.
- **`go test ./...`** prints `? … [no test files]` for every generated package. That's harmless; to keep the output short, leave them out: `go test $(go list ./... | grep -v /lyr_gen/)`.

## Committing `lyr_gen/` (optional)

If you'd rather have `go build ./...` work right after a clone (for tools that don't know about Lyr, or for `go install` of your server), commit `lyr_gen/`: replace the `lyr_gen/` line in `.gitignore` with these two lines:

```gitignore
lyr_gen/static/client/
lyr_gen/static/static.go
```

They're the copy of your built files that only `lyr build --embed` writes, so keep them ignored. Commit `lyr_gen/static/doc.go`: it keeps the package present for `go mod tidy` and `go vet`.

Generation is deterministic, so CI can check that the committed code is up to date:

```sh
go tool lyr generate && git diff --exit-code lyr_gen
```

Generated code uses relative file names in its line directives, so the committed files are the same on every machine and OS. Nothing random is written to `lyr_gen/` either: the island key and build ID that each `lyr build` creates are passed to the Go linker instead (see [Deploy](deploy.md#island-key-lyr_key)).

A server built with plain `go build` works, but it has no build key: it creates a random island key per process and warns about it on startup. Use `lyr build` for anything you deploy.

## See also

- [Getting started](getting-started.md)
- [Routing](routing.md): how file names become URLs
- [Server](server.md): `cmd/server/main.go`
- [Middleware and locals](middleware.md): middleware and the `Locals` struct
- [Configuration](configuration.md): `lyr.json`
- [Deploy](deploy.md): what to ship from `dist/`
