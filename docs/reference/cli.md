# CLI

This page describes every `orbit` command, its flags and exit codes, how the CLI matches your project's version, and how it reports errors.

## Overview

```sh
orbit new blog         # create a project
orbit dev              # dev server with live reload, on localhost:4321
orbit build            # build into dist/
orbit preview          # run the built server locally
orbit check            # type-check Go and TypeScript without building
orbit fmt              # format .orbit files
orbit generate         # (re)generate orbit_gen/
orbit doctor           # check your environment and project
orbit create-key       # print a new island key for ORBIT_KEY
orbit lsp              # language server, started by your editor
```

| Command | Flags | Does |
|---|---|---|
| [`orbit generate`](#orbit-generate) | | Generates the Go code in `orbit_gen/` |
| [`orbit build`](#orbit-build) | `--embed`, `--verbose` | Builds the server and its assets into `dist/` |
| [`orbit dev`](#orbit-dev) | `--port 4321`, `--host` | Runs the dev server |
| [`orbit preview`](#orbit-preview) | | Runs the built server |
| [`orbit new <dir>`](#orbit-new) | `--module path` | Creates a project |
| [`orbit check`](#orbit-check) | | Type-checks Go and TypeScript |
| [`orbit fmt [paths]`](#orbit-fmt) | `-l`, `-d`, `-w`, `--stdin-filepath` | Formats `.orbit` files |
| [`orbit doctor`](#orbit-doctor) | `--json` | Checks the environment and project |
| [`orbit create-key`](#orbit-create-key) | | Prints a new island key |
| [`orbit lsp`](#orbit-lsp) | | Language server over stdio |

Flags use Go's standard syntax: `--port 3000`, `--port=3000` and `-port 3000` are the same.

## Exit codes

Every command uses the same exit codes, following Go's `flag` package:

| Code | Meaning |
|---|---|
| `0` | Success. Warnings (such as `orbit doctor`'s ⚠ or a skipped TypeScript check) don't change it. |
| `1` | The command failed: a compile error, a failed check, a ✗ in `orbit doctor`. |
| `2` | Usage error: an unknown command or flag, or a missing argument. The usage is printed to stderr. |

`orbit preview` is the exception: it exits with your server's own exit code.

## The pinned CLI: `orbit` and `go tool orbit`

Each project pins its CLI version in `go.mod`:

```
tool github.com/orbit-z/orbit/cmd/orbit
```

You can run that pinned version directly with `go tool orbit <command>`. You can also keep using a globally installed `orbit`: when it runs inside a project whose `go.mod` has this directive, it hands the command over to the pinned version, with the same arguments, as `go tool orbit <args>` would. The generated code therefore always matches the `github.com/orbit-z/orbit` runtime version in your `go.mod`. The CLI sets `ORBIT_REEXEC=1` for the pinned process, so it never loops.

The hand-over runs `go tool -n orbit`, which builds the pinned CLI (or finds it in Go's build cache) and prints its path, and then runs that binary in place of the global one (on Unix it replaces the process), with the selected Go toolchain first on `PATH`, as `go tool` does. So the exit code, signals and output are the pinned CLI's own: Ctrl-C on `orbit dev` exits with code 0. (Run directly, `go tool orbit dev` exits with code 1 on Ctrl-C: the `go` command forwards the terminal's `SIGINT` to the tool, which then gets it twice.) If the pinned CLI doesn't build, you see the `go` command's error and its exit code.

The global CLI tells itself apart from a pinned copy by its build info: a global install (`go install github.com/orbit-z/orbit/cmd/orbit@…`, or `go build` in a clone) has Orbit as its main module, while `go tool orbit` is built inside your module, with Orbit as a dependency, and never hands over.

`orbit new` and `orbit doctor` are the exceptions: they always run the version you invoked. `orbit new` has no project yet, and `orbit doctor` must work when the pinned CLI or the versions are what's broken (it reports both).

In scripts, CI and Dockerfiles, call `go tool orbit` directly: it needs no global install.

In a project without the `tool` directive, the global CLI checks that its version matches the `github.com/orbit-z/orbit` version in `go.mod`. If they differ, it stops with exit code 1 and a version-mismatch error that points to `orbit doctor`:

```
orbit: version mismatch: this orbit is v0.2.0, but go.mod requires github.com/orbit-z/orbit v0.1.0; pin the CLI with go get -tool github.com/orbit-z/orbit/cmd/orbit@v0.1.0, then run go tool orbit (see orbit doctor)
```

The check is skipped when `go.mod` replaces `github.com/orbit-z/orbit` with a local directory, and when `go.mod` doesn't require it at all.

## `orbit generate`

```sh
go tool orbit generate
```

Parses every `.orbit` file under `src/` and writes the generated Go packages into `orbit_gen/`:

- `orbit_gen/decl/…` and `orbit_gen/impl/…`: two packages per `.orbit` file;
- `orbit_gen/app`: the route table, the island table and `app.New()`, which passes `orbit.WithSite(site)` before your own options when `orbit.json` sets [`site`](../configuration.md#site);
- `orbit_gen/manifest`: the page assets;
- `orbit_gen/cmd/server`: a default server, only if your project has no server entry;
- `orbit_gen/types/`: TypeScript declarations for the data components pass to scripts with `orbit.JSON` and `set:json` (`go.d.ts`, `client.d.ts`, and one `src/…/X.orbit.d.ts` per component that has typed data), written by the typed pass. See [Client modules](client-modules.md).

Every generated file starts with `// Code generated by orbit. DO NOT EDIT.`. When a `.orbit` file is deleted or renamed, `generate` removes the generated directories it no longer needs, but only directories whose files carry that header, so it never deletes code you wrote. The output is deterministic: the same sources always produce the same files, and no key or other random value is written there. Files whose content hasn't changed aren't rewritten, so Go's build cache stays warm. `orbit_gen/static/`, which only `orbit build --embed` writes, is left alone.

`orbit_gen/manifest` lists each page's hashed CSS and JS URLs, so `generate` also runs esbuild on your scripts and styles, in memory, without writing the bundles; that's how `generate` and `build` write the same `orbit_gen/`. A script or style that doesn't compile (a syntax error, an import that can't be resolved) is a `generate` error too.

`generate` runs in the project around the current directory (the nearest directory with a `go.mod`) and prints nothing when it succeeds. `orbit generate -h` prints its usage and exits 0. Errors and warnings go to stderr, all of them at once, sorted by file; with any error, nothing in `orbit_gen/` is written or removed, and the exit code is 1. Every file is generated even when another one has errors, so you see all the code-generation errors in one run. The typed pass (which picks typed writes with `go/types`, writes `orbit_gen/types/` and reports `orbit.JSON` and `set:json` values of a type `encoding/json` can't encode) runs only when there are no errors.

`orbit_gen/` is gitignored by default, so run `go tool orbit generate` first in a fresh clone or in CI, before `go build`, `go vet`, `go test` or `go mod tidy`. See [Project structure](../project-structure.md).

## `orbit build`

```sh
go tool orbit build
go tool orbit build --embed
go tool orbit build --verbose
```

Builds your server and its assets into `dist/`:

1. Reads `orbit.json` and the module path from `go.mod`.
2. Parses every `.orbit` file and reports all errors at once, not just the first. Checks the import graph for cycles.
3. Scopes the CSS and bundles each page's scripts and styles with esbuild into `dist/client/_orbit/`, plus one lazy entry per component loaded with `client:*`. Stylesheets that use Tailwind go through the Tailwind CSS CLI first, every one from scratch, in parallel (see [Using Tailwind](../tailwind.md)); `orbit generate` and `orbit check` run them too, since they bundle.
4. Generates `orbit_gen/`, with typed writes and the script types in `orbit_gen/types/`, exactly as `orbit generate` does.
5. Copies `public/` into `dist/client/`. A `public/` file whose path is under `_orbit/` is an error.
6. Writes the production version of the generated code that calls components directly to `.orbit/build/`, with `.orbit/build/overlay.json` (see [Direct calls in production builds](../performance.md#direct-calls-in-production-builds)).
7. Builds your server entry (`cmd/server`, or the generated default) into `dist/server`, with `go build -overlay=.orbit/build/overlay.json`.

`orbit build` prints nothing when it succeeds (unless `--verbose`). If it fails, `dist/` is left as it was: the new client files and server are built in a staging directory and only replace the old ones once everything has succeeded, so a new `dist/client` is never paired with an old `dist/server`.

No page is rendered at build time: `dist/server` renders every page on request. At startup, it finds `dist/client` next to its binary, unless `ORBIT_STATIC` or `orbit.WithStaticDir` says otherwise.

| Flag | Default | Meaning |
|---|---|---|
| `--embed` | `false` (or `"embed"` in `orbit.json`) | Embeds `dist/client` into `dist/server`, for a single-binary deploy. |
| `--verbose` | `false` | Lists each page's CSS and JS files with their sizes. |

With `--verbose`, a successful build prints each page and its files to stdout, with their size and gzipped size (in kB of 1000 bytes, gzip at its default level, as Vite and Astro report them): its stylesheet, its module, the [shared chunks](../styles-and-scripts.md#shared-chunks-and-import) the module imports (indented, `(preload)`: the page links them with `<link rel="modulepreload">`) and its [lazy entries](../lazy-loading.md) (`(lazy)`). Then `loaded with import()` lists the chunks no page preloads: the targets of dynamic `import()`, and chunks that only lazy entries import. The totals count every CSS and JS file once, however many pages list it. This is `examples/blog`:

```text
src/pages/404.orbit
  /_orbit/404-X3S6MRMN.css             0.81 kB  gzip    0.44 kB
src/pages/about.md
  /_orbit/about-T5WON2JX.css           3.52 kB  gzip    0.95 kB
src/pages/account.orbit
  /_orbit/account-WUPF6IZ7.css         0.86 kB  gzip    0.46 kB
  /_orbit/account-ZVPT2ZNZ.js          0.29 kB  gzip    0.25 kB
src/pages/blog/[slug].orbit
  /_orbit/blog-slug-A7YHTO2V.css       1.64 kB  gzip    0.67 kB
  /_orbit/blog-slug-D5CBDCRB.js        0.58 kB  gzip    0.43 kB
    /_orbit/chunk-ANEWWFSU.js          0.16 kB  gzip    0.14 kB  (preload)
  /_orbit/ShareButtons-QRWP56BT.js     0.49 kB  gzip    0.33 kB  (lazy)
src/pages/index.orbit
  /_orbit/index-DFCY47X5.css           1.30 kB  gzip    0.57 kB
src/pages/search.orbit
  /_orbit/search-DDKLSPIY.css          1.36 kB  gzip    0.59 kB
  /_orbit/search-FS5ZIDKX.js           0.21 kB  gzip    0.19 kB
loaded with import()
  /_orbit/suggestions-GF7I45PT.js      0.24 kB  gzip    0.19 kB
total CSS                              9.48 kB  gzip    3.68 kB
total JS                               1.97 kB  gzip    1.54 kB
```

A page with neither prints `src/pages/x.orbit: no CSS or JS`.

To get source maps for the bundles, set [`"sourcemap": true`](../configuration.md#sourcemap) in `orbit.json`.

With `--embed`, Orbit copies the static files into `orbit_gen/static/` and builds the server with the `orbit_embed` build tag, so `app.New()` serves them from inside the binary. You can copy `dist/server` alone to a machine and run it. `dist/client` is still written, for uploading to a CDN. If `ORBIT_STATIC` is set when the server starts, it overrides the embedded files.

**Island key and build ID.** Each `orbit build` generates a new random island key and a new random build ID, and links them into the binary with `-ldflags=-X` (on two internal variables of the `orbit` package). The build ID appears in every `server:defer` island URL, so each build gets fresh URLs. Because the key changes with every build, set a stable [`ORBIT_KEY`](#orbit-create-key) for rolling deploys or several builds behind one load balancer; it overrides the embedded key. An `-ldflags` in your `GOFLAGS` (such as `GOFLAGS=-ldflags=-s`) is kept: Orbit appends its two `-X` flags to it. If you build your server with plain `go build` instead, the key and build ID are random per process.

`orbit build` runs the `go` command, so it honors your `GOFLAGS` and other Go environment variables (for example `GOFLAGS=-mod=vendor`, or `GOFLAGS=-tags=prod` for your own build tags; with `--embed`, Orbit adds `orbit_embed` to them). It has no Go build flags of its own besides `--embed`. It passes `-overlay` itself, so an `-overlay` in `GOFLAGS` is an error (`orbit: -overlay in GOFLAGS isn't supported (orbit build passes its own)`).

**`.orbit/build/`** is removed and rewritten on every build, and `orbit_gen/` stays exactly what `orbit generate` writes, so `orbit doctor`, your editor and a committed `orbit_gen/` see no difference after a build. A plain `go build ./cmd/server` gives a correct server too, but with dev's component calls (more allocations); `orbit build` gives the production one. To run tests against the production version, use `go test -overlay=.orbit/build/overlay.json ./...` after `orbit build` (see [Testing](../testing.md#allocation-counts-and-production-builds)).

## `orbit dev`

```sh
orbit dev
orbit dev --port 3000
orbit dev --host
```

Runs the development server: it builds and runs your server entry, watches your files, rebuilds what changed and reloads the browser. Stop it with Ctrl-C. See [Dev server](../dev-server.md).

| Flag | Default | Meaning |
|---|---|---|
| `--port` | `4321` | The port the dev server listens on. |
| `--host` | off | Listen on all network interfaces instead of localhost only, so other devices on your network can reach the dev server. |

Without `--host`, the dev server listens on localhost only (`127.0.0.1`, and `[::1]` when available), as Astro's does. Your own server always runs on `127.0.0.1` at a random port behind it, either way. When the port is taken, the next nine are tried in turn, as Astro does.

Ctrl-C stops your server (with `SIGINT`, as on every restart) and exits with code 0. `orbit dev` exits with code 1 only when it can't start at all (no `go.mod`, no free port, the file watcher fails), and 2 for a usage error.

Compile errors don't stop `orbit dev`: it prints them, shows them in the browser overlay, and keeps the last working version of your server running until you fix them.

## `orbit preview`

```sh
go tool orbit build
go tool orbit preview
```

Runs `dist/server`, the output of the last `orbit build`, to check it locally before you deploy. It runs the binary as is, with your environment, so it listens where `dist/server` would: `ORBIT_ADDR`, else `:$PORT`, else `:4321`. A server that uses `orbit.ListenAndServe` logs its URL once it's listening (`orbit: listening on http://localhost:4321`). To use another port, set `PORT`:

```sh
PORT=3000 go tool orbit preview
```

It has no flags, and works from any directory of the project (it runs the server from the project root). If there's no `dist/server`, it stops with `orbit preview: no dist/server in <root>; run orbit build first` and exit code 1.

Ctrl-C (or `SIGTERM`) is passed on to the server, which shuts down gracefully; `orbit preview` then exits with the server's exit code. If the server is killed by a signal, `orbit preview` prints the reason and exits 1.

## `orbit new`

```sh
orbit new blog --module example.com/blog
orbit new my-site
```

Creates a project in the directory `<dir>` from the starter template built into the CLI.

| Flag | Default | Meaning |
|---|---|---|
| `--module` | the directory name (`blog`) | The Go module path written to `go.mod`, such as `example.com/blog`. |

`<dir>` must not exist yet, or must be empty. The starter includes:

- `go.mod`, with:
  - `module` set to `--module` or the directory name;
  - `go 1.27`;
  - a `toolchain` line naming the Go version the CLI was built with (for example `toolchain go1.27.3`), so `GOTOOLCHAIN=auto` uses that exact release;
  - `tool github.com/orbit-z/orbit/cmd/orbit`, at the version of the CLI you ran;
- `cmd/server/main.go`, your server, which composes Orbit with your own handlers (see [Server](../server.md));
- a base layout with a scoped `<style>`, `src/pages/index.orbit`, `src/pages/404.orbit` and `public/favicon.svg`;
- `tsconfig.json` and `src/env.d.ts`, for TypeScript in your scripts (see [Styles and scripts](../styles-and-scripts.md#tsconfigjson));
- a `Dockerfile` and `.dockerignore`: a multi-stage build that runs `go tool orbit build --embed` and copies only `dist/server` into a distroless image, with `ENV PORT=8080` (see [Deploy](../deploy.md));
- a `.gitignore` covering `orbit_gen/`, `.orbit/`, `dist/` and `node_modules/`.

It then runs `orbit generate`, then `go mod tidy`, in that order: the scaffolded `cmd/server` imports `orbit_gen/app`, so `go mod tidy` would fail before the code is generated. `orbit build` works on the starter with no edits. Finally it prints the next steps (`cd <dir>`, then `orbit dev`, the global CLI that hands over to the pinned one) to stdout; `go mod tidy`'s progress goes to stderr.

Flags may come before or after `<dir>`. A missing `<dir>`, a second argument or an unknown flag is a usage error (exit code 2). A module path with characters Go doesn't allow (`orbit new "my site"`) or a non-empty `<dir>` fails with exit code 1 before anything is written. If generating the code or `go mod tidy` fails, `orbit new` exits with code 1, names the failed step, and leaves `<dir>` as it is for you to inspect.

A CLI that isn't a tagged release (built with `go install ./cmd/orbit`, `go build` or `go run` in a clone) writes `require github.com/orbit-z/orbit v0.0.0` plus a `replace` directive pointing at the clone it was built from, when that directory is still there. `ORBIT_REPLACE=/path/to/orbit` picks the clone explicitly (it must hold Orbit's `go.mod`). This is for working on Orbit itself; the `replace` names a directory on your machine, so the scaffolded `Dockerfile` can't build such a project as it is (see [Projects that need files outside their directory](../deploy.md#projects-that-need-files-outside-their-directory)).

`orbit new` always runs the CLI version you invoked; it never re-executes a pinned version.

## `orbit check`

```sh
go tool orbit check
```

Type-checks your project without building it.

**Go**: generates `orbit_gen/`, as `orbit generate` does (which runs Orbit's own checks, such as unknown props), then runs `go vet ./orbit_gen/...`. Errors point at your `.orbit` files and are cleaned up like build errors (see [Error output](#error-output)). Your own Go packages, such as `cmd/server`, aren't vetted: run `go vet ./...` for those. If Orbit's own checks find an error, nothing is written and `go vet` doesn't run.

**TypeScript**: the Go half also writes `orbit_gen/types/`, the types of the data components pass to scripts (see [Client modules](client-modules.md)). Then it extracts every processed inline `<script>` into `.orbit/check/`, mirroring `src/` (the second script of `src/components/Card.orbit` becomes `.orbit/check/src/components/Card.orbit.1.ts`), so a relative import such as `../scripts/util` resolves from the `.orbit` file's directory. A generated `.orbit/check/tsconfig.json` extends your `tsconfig.json` and includes the extracted files plus your `include` globs (`src/**/*.ts` if you set none); your `exclude` still applies. The generated tsconfig also adds `orbit_gen/types` to `rootDirs` and `orbit_gen/types/*.d.ts` to `include`, so `import { data } from "./X.orbit"` is typed whether or not your own `tsconfig.json` has those entries. Without a `tsconfig.json`, it uses the options `orbit new` scaffolds, which include `allowImportingTsExtensions` (as in Astro's base tsconfig), so `import { f } from "../scripts/util.ts"` checks as it bundles. A `<script src="…">` isn't extracted; its file is checked through that include. Then Orbit runs `bun check --no-pretty -p .orbit/check`, the TypeScript type checker built into Bun. Each error in a script is mapped back to its line and column in the `.orbit` file; errors in your `.ts` files keep their own path. Both are printed as `file:line:col: error TSxxxx: message` (`src/components/LikeButton.orbit:20:20: error TS18047: 'post.tags' is possibly 'null'.`). An error inside a generated `orbit_gen/types/` file, which would be a bug in Orbit, is printed with that path and prefixed `internal: `. Unchanged scripts aren't rewritten, and stale files in `.orbit/check/` are removed. This half needs **Bun 1.4.4 or later**, and no `typescript` package. If Bun is missing or older, the TypeScript half is skipped with a notice naming the command to install or upgrade Bun, and the Go half still runs. A project with no processed script and no `.ts` file under `src/` skips it silently. See [Styles and scripts](../styles-and-scripts.md#type-checking-with-orbit-check).

Tailwind's errors (an `@apply` of an unknown class, a missing CLI) are Go-half errors: the generate step bundles the styles, so `orbit check` needs the Tailwind CLI when the project uses [Tailwind](../tailwind.md).

Errors are printed to stderr, Go first; a clean project prints nothing. Exit code: 1 if any error was found (warnings and the skip notice don't count), 0 otherwise, 2 for a usage error.

## `orbit fmt`

```sh
go tool orbit fmt                         # format every .orbit file under src/, in place
go tool orbit fmt src/components          # only the files under these paths
go tool orbit fmt -l                      # list the files that aren't formatted; change nothing
go tool orbit fmt -d src/pages/index.orbit   # show the changes as a diff; change nothing
go tool orbit fmt --stdin-filepath=src/pages/index.orbit < in.orbit   # for editors and scripts
```

Formats your `.orbit` files the way `gofmt` formats Go: there's one style, with no options to argue about. `orbit fmt` formats the parts of a file that are code, and never anything that's rendered. So a formatted page renders **exactly the same HTML**, in every [`compressHTML`](../configuration.md#compresshtml) mode.

| It formats | How |
|---|---|
| The frontmatter | With `gofmt`'s rules (`go/format`): tabs, spacing, one statement per line, sorted imports. Comments are kept. |
| `{expr}` expressions, in content and in attribute values (`a={expr}`, `{...attrs}`) | The Go code inside the braces, with `gofmt`'s spacing; the braces hug the code. |
| Control blocks: `{if}`, `{else if}`, `{else}`, `{/if}`, `{for}`, `{/for}`, `{switch}`, `{case}`, `{default}`, `{/switch}` | The condition, clause or case list, with `gofmt`'s spacing, and the block tag spelled one way. |
| `<script>` and `<style>` content | With `bun fmt`, Bun's Prettier-compatible formatter, when Bun 1.4.4 or later is installed (see [Scripts and styles](#scripts-and-styles)). |

It **never** changes:

- the HTML: text, tags, attributes and their order, quotes, and the whitespace between them (indentation included);
- `<pre>` and `<textarea>` content, HTML comments and `{/* … */}` template comments;
- the content of a `<script>` whose `type` isn't JavaScript (JSON, JSON-LD, import maps);
- the order of your frontmatter statements: it doesn't move code between the declarations Orbit hoists and the statements of the render function;
- line endings: a file with CRLF line endings keeps them;
- the end of the template: it doesn't add or remove a final newline (that would be rendered);
- a byte order mark at the start of the file (Orbit skips it, as Go does, so the frontmatter after it is found and the mark isn't rendered).

### Paths and flags

With no paths, `orbit fmt` formats every `.orbit` file under the `src/` directory of the project around the current directory (the nearest directory with a `go.mod`). With paths, it formats those files, and every `.orbit` file under those directories, skipping directories whose name starts with `.`, `node_modules`, `orbit_gen` and the project's `dist/`; symbolic links met in a directory aren't followed. A path can be outside a project, or outside `src/`: formatting needs only the file itself.

| Flag | Meaning |
|---|---|
| (none) | Writes the formatted files in place. Files that are already formatted aren't rewritten. Prints nothing. |
| `-l` | Lists the files whose formatting differs, one per line, and writes nothing. |
| `-d` | Prints a unified diff of each file whose formatting differs (`gofmt -d`'s format), and writes nothing. |
| `-w` | With `-l` or `-d`: also writes the files. On its own, the same as no flag. |
| `--stdin-filepath=PATH` | Reads one file from stdin and prints the formatted file to stdout. `PATH` is used for error positions and for Bun's configuration lookup; the file doesn't have to exist. With `-l` it prints `PATH` if the input isn't formatted, with `-d` the diff. It takes no paths, and `-w` is a usage error with it. |

Only `.orbit` files are formatted. A Markdown file passed by name is an error (`orbit fmt: src/pages/about.md: orbit fmt formats .orbit files; format Markdown with bun fmt`), and Markdown files in a directory are skipped: format them with `bun fmt` or Prettier, as in an Astro project.

Inside a project that pins the CLI, `orbit fmt` runs the pinned version, like every other command, so everyone on the project formats the same way.

### Errors and exit codes

`orbit fmt` only formats a file it can parse. A file with a syntax error (an unclosed tag or block, a `{` without its `}`, frontmatter that isn't valid Go, a script Bun can't parse) is left unchanged, and the error is printed as `file:line:col: message`; the other files are still formatted:

```text
src/components/Card.orbit:7:20: expected ';', found err
src/components/Card.orbit:31:9: <script>: Variable declaration expected.
```

Other errors, such as an unknown prop or an unused import, don't stop it: `gofmt` formats Go that doesn't type-check, too. `orbit check` reports those.

| Code | Meaning |
|---|---|
| `0` | Success. With `-l` or `-d`, every file is already formatted. |
| `1` | A file couldn't be formatted (a syntax error, a Bun error, a file that can't be read or written), or, with `-l` or `-d` and no `-w`, some file isn't formatted. |
| `2` | Usage error. |

So in CI, `go tool orbit fmt -l` fails the step when a file needs formatting, and names it.

### The frontmatter

The frontmatter is formatted with `gofmt`'s rules. The declarations that Orbit hoists (`import`, `type`, `const` and named `func`s) are formatted as file-level Go, and the statements of the render function as a function body, at the left margin. Imports are sorted by path within each group, as `gofmt` sorts them, so `.orbit` imports (their paths start with `.`) come first in their group; separate them with a blank line to keep them apart. Before:

```orbit unformatted
---
import (
    "strings"
    "example.com/blog/internal/posts"
    Card "../components/Card.orbit"
)
type Props struct { Title string;   Count int }

list,err:=posts.All()
if err != nil { return err }
title := strings.ToUpper( props.Title )   // shown in the header
---
```

After:

```orbit
---
import (
	Card "../components/Card.orbit"
	"example.com/blog/internal/posts"
	"strings"
)

type Props struct {
	Title string
	Count int
}

list, err := posts.All()
if err != nil {
	return err
}
title := strings.ToUpper(props.Title) // shown in the header
---
```

A run of hoisted declarations gets `gofmt`'s blank lines (one after the import block). Elsewhere, blank lines are kept as you wrote them, at most one in a row, and leading and trailing blank lines in the frontmatter are removed. Indentation in the frontmatter is always tabs, as in Go files.

### Expressions and control blocks

The Go code in `{…}` gets `gofmt`'s spacing, as if it were written `x := <expr>`. The braces hug it, and block tags are spelled `{if c}`, `{else if c}`, `{else}`, `{/if}`, `{for clause}`, `{/for}`, `{switch x}`, `{case a, b}`, `{default}` and `{/switch}`. Attribute forms are kept: `title={title}` isn't shortened to `{title}`, nor the other way round. Before:

```orbit unformatted
---
import "strconv"

items := []string{"a", "b"}
n := len(items)
---
{ if n==0 }
  <p>Nothing yet</p>
{ else if n==1 }
  <p class={ "one"+"-item" }>One item</p>
{else}
  <ul>
    {for i:=0;i<n;i++}
      <li data-index={ i+1 }>{ items[i]+" ("+strconv.Itoa(i)+")" }</li>
    {/for}
  </ul>
{ /if }
```

After:

```orbit
---
import "strconv"

items := []string{"a", "b"}
n := len(items)
---
{if n == 0}
  <p>Nothing yet</p>
{else if n == 1}
  <p class={"one" + "-item"}>One item</p>
{else}
  <ul>
    {for i := 0; i < n; i++}
      <li data-index={i + 1}>{items[i] + " (" + strconv.Itoa(i) + ")"}</li>
    {/for}
  </ul>
{/if}
```

The HTML lines themselves, and their indentation, are untouched. An expression that spans several lines keeps its line breaks, as `gofmt` keeps them; its continuation lines are indented from the line where the `{` is, one level per `gofmt` indentation level, using the template's own indentation (a tab, or the smallest space indentation the template uses; two spaces if nothing is indented):

```orbit
  <Card post={posts.Post{
    Title: "Hello",
    Tags:  []string{"go", "orbit"},
  }} />
```

### Scripts and styles

`<script>` and `<style>` content is formatted with **`bun fmt`**, the Prettier-compatible formatter built into Bun 1.4.4 and later. Orbit runs it once per block (`bun fmt --stdin-filepath=src/components/Card.orbit.0.ts`), from the project root, so your Prettier or Bun formatter configuration (`.prettierrc`, `bunfig.toml`'s `[fmt]`, `.editorconfig`) applies, as it would to a `.ts` or `.css` file next to the component.

| Block | Formatted as |
|---|---|
| A [processed](../styles-and-scripts.md#which-scripts-orbit-processes) `<script>` | TypeScript |
| Any other `<script>` with no `type`, or a JavaScript `type` (`module`, `text/javascript`) | JavaScript |
| A `<script>` with another `type` (`application/json`, `application/ld+json`, `importmap`, …) | Not formatted |
| Every `<style>` | CSS |

The block keeps its indentation: content indented two spaces inside the tag stays indented two spaces. Content written on the same line as the tag moves to its own lines, one level in from the tag. Before:

```orbit unformatted
<button id="like">Like</button>
<script>
  const btn=document.querySelector<HTMLButtonElement>("#like")!
  btn.addEventListener("click",async ()=>{ const r=await fetch("/api/like",{method:"POST"}); btn.textContent=String((await r.json()).count) })
</script>
<style> button{padding:.5rem 1rem;border-radius:4px} </style>
```

After:

```orbit
<button id="like">Like</button>
<script>
  const btn = document.querySelector<HTMLButtonElement>("#like")!;
  btn.addEventListener("click", async () => {
    const r = await fetch("/api/like", { method: "POST" });
    btn.textContent = String((await r.json()).count);
  });
</script>
<style>
  button {
    padding: 0.5rem 1rem;
    border-radius: 4px;
  }
</style>
```

**Without Bun** (or with a Bun older than 1.4.4), `orbit fmt` formats everything else and leaves scripts and styles as they are, with one notice on stderr naming the command to install or upgrade Bun (only when some file has a script or style to format). The notice doesn't change the exit code. So if your team formats scripts, install Bun in CI too: without it, `orbit fmt -l` can't tell that a script isn't formatted. `orbit doctor` reports a missing Bun.

### In your editor

The language server formats `.orbit` files too, with the same rules, so format-on-save works in VS Code and Neovim. See [Editor setup: Formatting](../editor-setup.md#formatting).

## `orbit doctor`

```sh
orbit doctor
orbit doctor --json
```

Checks your environment and project. Each check prints ✓ (passed), ⚠ (warning) or ✗ (problem), and every ⚠ and ✗ comes with the command that fixes it.

| Check (`name`) | What it verifies |
|---|---|
| Go (`go`) | The `go` command and the active toolchain (`go env GOVERSION`, run in the project, so it's the toolchain `go.mod` selects); that `go.mod` has `go 1.27` or later (✗) and a `toolchain` line (⚠, fixed by `go get toolchain@go1.27.3`). Outside a project, a Go older than 1.27 passes when it can switch toolchains (Go 1.21 or later, `GOTOOLCHAIN` not `local`). |
| CLI version (`cli`) | With the `tool` directive: that the `go` command can load the pinned CLI (✗ otherwise, fixed by `go mod tidy`), so the hand-over works. Without it: the global `orbit` version against the `github.com/orbit-z/orbit` requirement (✗ on a mismatch, unless a local `replace`). |
| gopls (`gopls`) | The gopls on your `PATH`: its version, and that it was built with a Go at least as new as your `go` directive (✗ otherwise, the same rule `orbit lsp` uses to refuse a gopls, fixed by `go install golang.org/x/tools/gopls@latest`). ✗ if there's none. ⚠ if it's older than the recommended v0.24.0. |
| Bun (`bun`) | That Bun 1.4.4 or later is installed, for `orbit check`'s TypeScript half. ⚠ only, with the install or upgrade command (`bun upgrade`). |
| TypeScript (`typescript`) | The TypeScript 7 (7.0.2 or later) that `orbit lsp` would start for `<script>` blocks, found as the language server finds it (the project's `node_modules/typescript`, `tsc` on your `PATH`, then `bun x` with Bun; see [How TypeScript is found](../editor-setup.md#how-typescript-is-found)): `tsc 7.0.2 (/path)`, or `TypeScript 7.0.2 through bun x`. ⚠ only, never ✗ (it's optional): when none is found, or the `tsc` found is older (TypeScript 5 or 6 has no language server), with the install command. Inside a project with no script in any `.orbit` file, a missing TypeScript passes ("not needed"). |
| Tailwind CSS (`tailwind`) | The Tailwind CSS v4 CLI, found as the build finds it (the project's `node_modules/.bin/tailwindcss`, `tailwindcss` on your `PATH`, then `bun x` with Bun; see [Using Tailwind](../tailwind.md#install-the-tailwind-cli)): `tailwindcss 4.3.3 (/path)`, or `Tailwind CSS 4.3.3 through bun x`. ✗ inside a project with a Tailwind input (a `.css` file or a `<style>` under `src/` that uses Tailwind) when no v4 CLI is found, since `orbit build` fails without it, with the install commands. ⚠ in such a project when `.gitignore` doesn't cover `orbit_gen/`, `.orbit/` and `dist/` (Tailwind would scan them). Otherwise ✓, with "not installed (needed only by projects that use Tailwind)" when there's none. |
| Generated code (`orbit_gen`) | That `orbit_gen/` exists and matches your sources (⚠, fixed by `orbit generate`). The generated files carry no source hash, so `orbit doctor` regenerates the project in memory, as `orbit generate` would, and compares; it writes nothing. `orbit_gen/manifest/manifest.go` isn't compared: it only lists asset URLs, `orbit dev` writes it empty and `orbit build` rewrites it. If your `.orbit` files have errors, it's a ⚠ naming the first one. |
| `.gitignore` (`gitignore`) | That it covers `orbit_gen/` (or `orbit_gen/static/`, when you [commit `orbit_gen/`](../project-structure.md#committing-orbit_gen-optional)), `.orbit/` and `dist/` (⚠, with a `printf … >> .gitignore` fix). Plain patterns only (`dist`, `/dist/`, `dist/**`); a later `!dist` re-includes. |
| Config (`config`) | That `orbit.json` is valid, the server entry exists, and `src/locals` doesn't import `orbit_gen/app` (✗ for each). |
| `tsconfig.json` (`tsconfig`) | When a `.orbit` file passes typed data to scripts (it contains `.JSON(` or `set:json`): that `tsconfig.json`'s `rootDirs` includes `orbit_gen/types`, so your editor's TypeScript can type `import "./X.orbit"` in `.ts` files (⚠ otherwise, with the fix: add `"rootDirs": [".", "orbit_gen/types"]` to `compilerOptions` and `"orbit_gen/types/*.d.ts"` to `include`). ⚠ when there's no `tsconfig.json`. `orbit check` and the TypeScript features in `<script>` blocks don't need it. |

Outside a project (no `go.mod` with a `src/` directory around the working directory), only `go`, `gopls`, `bun`, `typescript` and `tailwind` run.

| Flag | Meaning |
|---|---|
| `--json` | Prints the results as JSON instead of text, for scripts and editors. |

With `--json`, the output is one object with a `checks` array, one entry per check, in the order of the table above:

```json
{
  "checks": [
    {"name": "gopls", "status": "error", "message": "gopls v0.18.1 was built with go1.23.4, older than go 1.27 in go.mod", "fix": "go install golang.org/x/tools/gopls@latest"}
  ]
}
```

`status` is `"ok"`, `"warning"` or `"error"`; `fix` is empty for a passing check.

Exit code: 1 if any check reports ✗, 0 otherwise (warnings don't fail).

The language server's "old gopls" error and the CLI's version-mismatch errors both point you to `orbit doctor`.

## `orbit create-key`

```sh
orbit create-key
```

Prints a new random 32-byte island key, in standard base64, for the `ORBIT_KEY` environment variable. The key encrypts the props of `server:defer` components (see [Lazy loading](../lazy-loading.md)).

You need it when instances of **different builds** must open each other's island URLs: during a rolling deploy, or with several builds behind one load balancer. Instances of the same `orbit build` already share the key embedded in it. Set the same `ORBIT_KEY` on every instance, and keep it secret, like any credential. See [Deploy](../deploy.md).

## `orbit lsp`

```sh
orbit lsp
```

Runs the Orbit language server over stdin/stdout. Your editor starts it; you don't run it by hand. It proxies Go features to gopls, proxies TypeScript features in `<script>` blocks to TypeScript 7's language server (`tsc --lsp --stdio`, started the first time you open an `.orbit` file with a script, when one is found), and adds template features of its own. It never writes files: the generated Go code, the scripts' TypeScript documents, their `tsconfig.json` and the data types all stay in memory. It works without `orbit_gen/` too.

It accepts two initialization options: `goplsPath` to choose the gopls binary, and `typescriptPath` to choose the TypeScript 7 one (a native `tsc` binary or a `typescript` package directory; see [How TypeScript is found](../editor-setup.md#how-typescript-is-found)). A `--stdio` flag, which many editors pass, is accepted and ignored: stdio is the only transport. See [Editor setup](../editor-setup.md).

## Error output

Errors are printed one per line as `file:line:col: message`, with the position in your `.orbit` file. For this page, with a misspelled prop:

```orbit file=src/pages/index.orbit expect-error
---
import (
	Card "../components/Card.orbit"
	"example.com/blog/internal/posts"
)

p, _ := posts.Find("hello")
---
<Card psot={p} />
```

`orbit check` prints:

```text
src/pages/index.orbit:9:7: unknown prop "psot" on <Card> (props: post, featured; add an orbit.RestAttrs field to pass HTML attributes through)
```

Errors in Markdown files point at the `.md` file the same way, at a frontmatter key or value, the `layout` value or a link: `src/pages/blog/hello.md:4:8: frontmatter draft: "yes" isn't a bool (Post.Props.Draft is bool)`. A link to a missing heading is a warning (`src/pages/blog/hello.md:21:10: warning: link ./routing-guide.md#setup: src/pages/blog/routing-guide.md has no heading #setup`), which doesn't fail the build. See [Troubleshooting: Markdown files](../troubleshooting.md#markdown-files).

Errors about a whole file, such as an invalid route, a route param that can't become a field name, or two files that generate the same package, have no line and column: `src/pages/[1st].orbit: route param [1st] can't become a Go field name; …`.

This holds for Orbit's own checks, esbuild and TypeScript errors, and Go compiler errors alike. `orbit build`, `orbit check` and the `orbit dev` overlay all print Go errors in your terms:

- **Positions are in `.orbit` files.** The generated code carries line directives, so `go build` and `go vet` report errors at the right line and column of the `.orbit` file, never in `orbit_gen/`.
- **Generated positions are removed.** Go prints related positions (such as "other declaration of …") with a generated location in brackets, like `./src/components/A.orbit:7:6[/abs/…/decl.go:10:53]`. Orbit drops the bracketed part.
- **Type names use your aliases.** A type from a component's generated package, like `card.Props` or `"example.com/blog/orbit_gen/decl/components/card".Props`, is shown as `Card.Props`, using the alias the reporting file gives that component (plain `Props` in the component's own file).
- **Paths are relative to the project root**, with `/` separators: Go's `./src/…` becomes `src/…`, the same form as Orbit's own errors.

For example, a type error in an attribute value:

```orbit file=src/pages/index.orbit expect-error
---
import (
	Card "../components/Card.orbit"
	"example.com/blog/internal/posts"
)

p, _ := posts.Find("hello")
---
<Card post={p.Title} />
```

```text
src/pages/index.orbit:9:13: cannot use p.Title (variable of type string) as posts.Post value in struct literal
```

## Stack traces and `-trimpath`

A panic in a production binary prints a stack trace whose frames point at your `.orbit` lines. If you build with `-trimpath` (for example `GOFLAGS=-trimpath`), note that **it doesn't trim the `.orbit` paths**: frames for code from `.orbit` files show absolute paths (`/home/me/blog/src/components/Card.orbit:25`), while frames in generated files are trimmed. This is a limitation of how Go applies `-trimpath` to line directives.

## Environment variables

| Variable | Read by | Meaning |
|---|---|---|
| `ORBIT_ADDR` | your server (`orbit.Addr()`) | Address to listen on, e.g. `:8080`. |
| `PORT` | your server (`orbit.Addr()`) | Port to listen on, used when `ORBIT_ADDR` isn't set. With neither set, the server listens on `:4321`. |
| `ORBIT_KEY` | your server (`app.New()`) | The island key, as printed by `orbit create-key`: standard base64 of 32 bytes. Overrides the key embedded by `orbit build`; an invalid value stops the server at startup. |
| `ORBIT_STATIC` | your server (`app.New()`) | Directory of the built static files; overrides `dist/client` and embedded files. |
| `ORBIT_DEV` | your server (`app.New()`) | Set to `1` by `orbit dev`: dev asset manifest, no-cache asset headers, error overlay. |
| `ORBIT_PUBLIC` | your server (`app.New()`) | Set by `orbit dev` to serve `public/` straight from your project. |
| `ORBIT_REEXEC` | the CLI | Set to `1` when the global CLI hands a command over to the project's pinned CLI (`go tool orbit`), to prevent loops; with it set, the CLI runs every command itself. |
| `GOFLAGS`, `GOTOOLCHAIN`, `CGO_ENABLED`, … | the `go` command | Honored by every command that runs `go`. |

`ORBIT_DEV` and `ORBIT_PUBLIC` are for `orbit dev`; don't set them in production. `orbit dev` also sets `ORBIT_KEY` for your server, unless you've set it yourself.

## See also

- [Getting started](../getting-started.md): installing the CLI and a first project
- [Dev server](../dev-server.md): `orbit dev` in detail
- [Deploy](../deploy.md): `dist/`, `--embed`, the Dockerfile and `ORBIT_KEY`
- [Lazy loading](../lazy-loading.md): `server:defer` and the island key
- [Configuration](../configuration.md): `orbit.json`
- [Editor setup](../editor-setup.md): `orbit lsp`
