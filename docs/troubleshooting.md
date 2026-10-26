# Troubleshooting

This page lists the errors and odd behaviors you're most likely to run into, grouped by area, each with its cause and its fix. Search it for the message you see. Messages are shown without their `src/…:line:col:` prefix, and the names in them (`Card`, `psot`, `Comments`) come from the example blog used throughout these docs (module `example.com/blog`).

## Run `orbit doctor` first

Many problems come from the environment rather than your code, and one command checks it:

```sh
orbit doctor
```

It prints one line per check: ✓ passed, ⚠ warning, ✗ a problem that blocks you. Every ⚠ and ✗ comes with the command that fixes it:

```
✓ go: go1.27.3 (go.mod: go 1.27, toolchain go1.27.3)
✓ cli: orbit v0.1.0 hands commands to the pinned go tool orbit (v0.1.0)
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); orbit check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
✓ orbit_gen: up to date
✓ gitignore: covers orbit_gen/, .orbit/ and dist/
✓ config: no orbit.json (defaults); server ./cmd/server
```

| Check | What it verifies |
|---|---|
| Go | The `go` command and the active toolchain; that `go.mod` has `go 1.27` or later and a `toolchain` line |
| CLI version | The global `orbit` against the version pinned by your project's `tool` directive |
| gopls | Its version, and that it was built with a Go at least as new as your `go` directive |
| Bun | Bun 1.4.4 or later, for the TypeScript half of `orbit check` (⚠ only) |
| Generated code | That `orbit_gen/` exists and matches your sources |
| `.gitignore` | That it covers `orbit_gen/`, `.orbit/` and `dist/` |
| Config | That `orbit.json` is valid, the server entry exists, and `src/locals` doesn't import `orbit_gen/app` |

Outside a project, only the environment checks run (Go, gopls, Bun). The exit code is 1 if any check reports ✗. For scripts and editors, `orbit doctor --json` prints the same results as one object with a `checks` array:

```json
{
  "checks": [
    {"name": "gopls", "status": "error", "message": "gopls v0.18.1 was built with go1.23.4, older than go 1.27 in go.mod", "fix": "go install golang.org/x/tools/gopls@latest"}
  ]
}
```

`status` is `"ok"`, `"warning"` or `"error"`, and `fix` is empty for a passing check. See [CLI: `orbit doctor`](reference/cli.md#orbit-doctor).

## Setup and toolchain

### Error positions show unresolved `../../../../src/…` paths

**Cause:** your code was compiled with a Go older than 1.27. Orbit's generated code points back at your `.orbit` files with relative file names, and only the Go 1.27 compiler resolves them. With an older Go, the `go` command may also refuse to build the module, because `go.mod` asks for `go 1.27`.

**Fix:** let `go` switch toolchains. Check the setting, and turn switching back on if it prints `local`:

```sh
go env GOTOOLCHAIN
go env -w GOTOOLCHAIN=auto
```

Or install Go 1.27 from [go.dev/dl](https://go.dev/dl/). A Go older than 1.21 can't switch toolchains at all, so install Go 1.27 directly. See [Go version](getting-started.md#go-version).

### The first `go` command in a project downloads Go

**Cause:** this is expected. `go.mod` names the exact release with `toolchain go1.27.3`, and with `GOTOOLCHAIN=auto` the `go` command downloads it once, into the module cache, then reuses it.

**Fix:** nothing to fix. If your network or policy blocks the download, install Go 1.27 from [go.dev/dl](https://go.dev/dl/). If `go.mod` has no `toolchain` line, `go` picks `go1.27.0` instead of the current patch release; `orbit doctor` checks for the line, and you can add or update it:

```sh
go get toolchain@go1.27.3
```

See [Why `go.mod` has a `toolchain` line](getting-started.md#why-gomod-has-a-toolchain-line).

### The global `orbit` stops with a version-mismatch error

```
orbit: version mismatch: this orbit is v0.2.0, but go.mod requires github.com/orbit-z/orbit v0.1.0; pin the CLI with go get -tool github.com/orbit-z/orbit/cmd/orbit@v0.1.0, then run go tool orbit (see orbit doctor)
```

**Cause:** your project has no `tool github.com/orbit-z/orbit/cmd/orbit` directive in `go.mod`, and your globally installed `orbit` has a different version than the `github.com/orbit-z/orbit` requirement. The generated code must match the runtime it runs against, so the CLI stops and points you to `orbit doctor`.

**Fix:** pin the CLI in the project, at the version `go.mod` already requires (`v0.1.0` here; use the one on your `require` line):

```sh
go get -tool github.com/orbit-z/orbit/cmd/orbit@v0.1.0
go tool orbit build
```

With the directive, a global `orbit` hands over to `go tool orbit` on its own, so the versions always match. The first `go tool orbit` compiles the CLI, which takes a moment. The check is skipped when `go.mod` replaces `github.com/orbit-z/orbit` with a local directory. See [The pinned CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit).

### Go features are disabled in the editor: gopls is too old

**Symptom:** your editor shows an error with the gopls install command and a pointer to `orbit doctor`. In `.orbit` files there's no Go completion, hover, Go diagnostics, definition, references or rename, while tag, prop, slot and directive completion still work.

**Cause:** the gopls that `orbit lsp` found was built with a Go older than your project's `go` directive. Such a gopls would reject newer language features as errors, so Orbit refuses it.

**Fix:** install a current gopls, then restart the language server:

```sh
go install golang.org/x/tools/gopls@latest
```

If the error stays, check which gopls is used: the `orbit.goplsPath` setting (or the `goplsPath` initialization option) comes first, then `gopls` on your `PATH`. A gopls built with a recent enough Go but older than v0.24.0 still runs; `orbit doctor` only warns about it. See [Old gopls versions](editor-setup.md#old-gopls-versions) and [How gopls is found](editor-setup.md#how-gopls-is-found).

### `orbit check` skips TypeScript type checking

**Cause:** Bun is missing or older than 1.4.4. `orbit check` then prints a notice with the command to install or upgrade Bun, still runs the Go checks, and its exit code reflects the Go checks only:

```
orbit check: skipping TypeScript type checking: bun not found (1.4.4 or later needed); install it with: curl -fsSL https://bun.sh/install | bash
orbit check: skipping TypeScript type checking: bun 1.3.2 is older than 1.4.4; upgrade it with: bun upgrade
```

**Fix:** install or upgrade Bun. No `package.json` or `typescript` package is needed.

```sh
curl -fsSL https://bun.sh/install | bash   # install
bun upgrade                                # upgrade an older Bun
```

Nothing else in Orbit needs Bun, so `orbit doctor` reports it as a warning only. See [Type checking with `orbit check`](styles-and-scripts.md#type-checking-with-orbit-check).

### `go build`, `go vet`, `go test` or `go mod tidy` fail in a fresh clone

**Cause:** `orbit_gen/` is gitignored, so a fresh clone doesn't have it, and `cmd/server/main.go` imports `example.com/blog/orbit_gen/app`.

**Fix:** generate it first, locally and in CI:

```sh
go tool orbit generate     # creates orbit_gen/
go vet ./...
go test ./...
go tool orbit build
```

The same applies after you edit an `.orbit` file: `go test` runs against the old generated code until you regenerate (a running `orbit dev` keeps it up to date). `orbit doctor` warns when `orbit_gen/` is missing or stale. To make plain `go build ./...` work right after a clone, you can commit `orbit_gen/` instead. See [Fresh clones and CI](project-structure.md#fresh-clones-and-ci).

### `orbit.json: compressHTML must be "jsx", "html" (or true) or false, not "none"`

**Cause:** `orbit.json` has a value Orbit doesn't accept. Other messages of this kind: `orbit.json: server must not be empty; remove it to use ./cmd/server`, `orbit.json: server "/srv/cmd/server" must be a path relative to the project root, such as "./cmd/server"`, `orbit.json: site "blog.example.com" isn't an absolute URL; include the scheme and host, such as "https://example.com"`, `orbit.json: trailingSlash must be "never" or "always", not "ignore"; Orbit has no "ignore" mode: each page has one URL`, and JSON syntax errors with a position, such as `orbit.json:3:1: invalid character '}' looking for beginning of object key string` (a trailing comma).

**Fix:** correct the value the message names, or remove the key to get its default (`null` counts as unset too). See [Configuration](configuration.md#all-keys).

### `has no src/ directory; run orbit in an Orbit project`

**Symptom:** `orbit: /home/me/blog has no src/ directory; run orbit in an Orbit project`.

**Cause:** the nearest `go.mod` above the current directory belongs to a module without a `src/` directory, for example because you ran `orbit` from a parent module or a fresh directory. Orbit stops instead of writing an `orbit_gen/` there.

**Fix:** `cd` into your Orbit project (the directory with its `go.mod` and `src/`), or create one with `orbit new`. See [Project structure](project-structure.md#srccomponents-and-srclayouts).

## Compile errors in `.orbit` files

`orbit build`, `orbit check` and the `orbit dev` overlay print every error as `src/…:line:col: message`, at its position in your `.orbit` file. Plain `go build` and `go vet` print the same positions as `./src/…`. If an error points into `orbit_gen/` instead, see [The Go extension reports errors in `orbit_gen/`](#the-go-extension-reports-errors-in-orbit_gen).

### `unknown prop "psot" on <Card> (props: post, featured)`

**Cause:** an attribute on a component tag matches no field of its `Props`. Matching ignores case, `-` and `_`, and the list shows the names that would work.

**Fix:** use one of the listed names (`post={p}`), or add the field to the component's `Props`. When `Props` embeds another struct or is an alias (`type Props = other.T`), Orbit can't list the fields, and Go reports the same mistake at the same place as `unknown field Psot in struct literal of type Card.Props`. See [Unknown props](components.md#unknown-props).

### `unknown component <Crad>: no .orbit import with this alias`

**Cause:** a capitalized tag is always a component, and its name must be the alias of an `.orbit` import.

**Fix:** fix the tag's spelling, or import the component in the frontmatter: `Card "../components/Card.orbit"`. See [Template syntax: Components](reference/template-syntax.md#components).

### Go type errors at `.orbit` lines

A page that passes a string to a `bool` prop, and a `Card` with a typo in a field name:

```orbit file=src/pages/index.orbit expect-error
---
import Card "../components/Card.orbit"
---
<Card featured="yes" />
```

```orbit file=src/components/Card.orbit expect-error
---
import "example.com/blog/internal/posts"

type Props struct {
	Post     posts.Post
	Featured bool
}
---
<h2>{props.Post.Titel}</h2>
```

```text
src/pages/index.orbit:4:16: cannot use "yes" (untyped string constant) as bool value in struct literal
src/components/Card.orbit:9:17: props.Post.Titel undefined (type posts.Post has no field or method Titel)
```

**Cause:** these are the Go compiler's own errors. Orbit's generated code carries line directives, so they land on your `.orbit` line and column, and generated type names are rewritten to your aliases (`Card.Props`).

**Fix:** fix them as in any Go code. Common ones:

- A quoted attribute value is a Go string literal, so it only fits a `string` field: write `count={3}` or `featured={false}`.
- A call that returns a value and an error (`posts.All()`) can't be used in `{…}`: call it in the frontmatter and handle the error there.
- An unused import is an error, as in Go. Unused variables in the frontmatter are fine.

See [Error output](reference/cli.md#error-output).

### `cannot use body (variable of type string) as orbit.HTML value in argument to orbit__w.HTML`

**Cause:** `set:html` only accepts an `orbit.HTML` value (or a string constant you wrote in the template), so raw output is always an explicit choice. `orbit__w.HTML` is the generated write behind `set:html`.

**Fix:** convert content you trust, such as your own Markdown renderer's output: `<div set:html={orbit.HTML(body)}></div>`. For text from users, use `{body}` or `set:text={body}`, which escape it. See [Raw HTML with `orbit.HTML`](components.md#raw-html-with-orbithtml).

### `pages don't receive props; move this to a component or read params`

**Cause:** a file under `src/pages/` declares `type Props`. Pages never receive props, so it could never be set.

**Fix:** read the route params from `params` (`params.Slug`) and the rest of the request from `ctx` (`ctx.Request.URL.Query()`), or move the markup that needs props into a component under `src/components/`. See [Pages](components.md#pages).

### `Params is generated from the page's route; rename this type`

**Cause:** a file under `src/pages/` declares `type Params`. Orbit generates `Params` for every page, from its file path, so the name is taken.

**Fix:** rename your type (for example `type Filters struct`). Read route params from the generated `params`. Components may declare a `Params` type. See [Route params](routing.md#route-params-params).

### `params.slug undefined (type Params has no field or method slug, but does have field Slug)`

```orbit file=src/pages/blog/[slug].orbit expect-error
---
---
<h1>{params.slug}</h1>
<p>{params.Slg}</p>
```

```text
src/pages/blog/[slug].orbit:3:13: params.slug undefined (type Params has no field or method slug, but does have field Slug)
src/pages/blog/[slug].orbit:4:12: params.Slg undefined (type Params has no field or method Slg)
```

**Cause:** the field doesn't exist in the page's generated `Params`. Its fields are the route params, in PascalCase (`[slug]` is `Slug`, `[post-id]` is `PostID`, `[id]` is `ID`), and only the params of *this* page's path are there. The same error, with `params` itself `undefined`, appears in a component: only pages get `params`.

**Fix:** use the field name your editor completes after `params.`, or check the file name. In a component, use `ctx.Param("slug")`. See [Field names](routing.md#field-names).

### `route params [post-id] and [postId] both become params.PostID; rename one`

**Cause:** two parameters in one page's path give the same `params` field name, after the PascalCase conversion (`[post-id]` and `[postId]`), or the same name is used twice (`[id]/[id].orbit`).

**Fix:** rename one of the directories or files so the names differ, such as `[post-id]/[comment-id].orbit`. Route errors name the file, without a line and column. See [Field names](routing.md#field-names).

### `route param [1st] can't become a Go field name; use letters, digits, - and _, starting with a letter`

**Cause:** a parameter name, such as `[1st]` or `[é]`, can't be turned into an exported Go field of `params`.

**Fix:** rename it, for example `[first].orbit`. See [Field names](routing.md#field-names).

### `component alias card must start with an uppercase letter (rename it to Card)`

**Cause:** a lowercase tag such as `<card>` is always an HTML element, so a component alias must be capitalized.

**Fix:** `Card "../components/Card.orbit"`, and use it as `<Card>`. See [Importing components](components.md#importing-components).

### `framework components aren't supported; use a .orbit component with a <script>`

**Cause:** the frontmatter imports a `.tsx`, `.jsx`, `.svelte` or `.vue` file. Orbit has no framework components, by design.

**Fix:** write an `.orbit` component with a vanilla `<script>`. A `<script>` can still import `.ts` and `.tsx` modules. See [No framework components](components.md#no-framework-components).

### `only .orbit and .md files can be imported by relative path; import client modules from a <script>`

**Cause:** the frontmatter imports a relative path that doesn't end in `.orbit` or a Markdown extension (`.md`, `.markdown`, …), such as `"./util.ts"`.

**Fix:** import client modules from inside a `<script>` (`import { greet } from "../scripts/greet";`). Go packages are imported by their import paths, as usual (`"example.com/blog/internal/posts"`). See [No framework components](components.md#no-framework-components).

### `func init isn't allowed in a component; use a package under internal/ for initialization`

**Cause:** the frontmatter declares a hoisted `func init()`. Rendering a component during package initialization isn't supported.

**Fix:** move the initialization (caches, clients, configuration) into a package under `internal/`, and import that package. See [No `func init`](components.md#no-func-init).

### `Props field post must be exported, since a parent sets it (rename it to Post)`

**Cause:** a parent sets `Props` fields from another Go package, so every field must be exported.

**Fix:** rename the field as suggested. Attributes still match it in any case: `post={p}` sets `Post`. See [Fields must be exported](components.md#fields-must-be-exported).

### `item is used by Props, so it lives in the component's declaration package and must be exported (rename it to Item)`

**Cause:** `Props` and everything it depends on live in the component's declaration package, and the rest of the component (body, template, helpers) only sees that package's exported names.

**Fix:** rename it to `Item`. Parents then refer to it through the alias, as `List.Item`. A lowercase name that only `Props` uses is fine. See [Types used by Props](components.md#types-used-by-props).

### `Item clashes with Item in the component's declaration package (Props uses it); rename one`

**Cause:** a component import alias, a named Go import or a hoisted declaration reuses a name that `Props` pulled into the component's declaration package, for example `Item "./Item.orbit"` in a file whose `Props` uses a `type Item`. The rest of the component already sees that name, so it's taken.

**Fix:** rename one of them, for example the alias: `ItemRow "./Item.orbit"`. Shadowing the name inside the render body (`Item := 42`) is allowed. See [Types used by Props](components.md#types-used-by-props).

### `Props clashes with the generated Props of the component's declaration package; rename it`

**Cause:** a component import alias, a named Go import or a hoisted declaration is named `Props` or `Render` (or, in a page, `Params`), which Orbit generates in the component's declaration package. For example `Props "./Other.orbit"`.

**Fix:** rename it, for example `Other "./Other.orbit"`. See [Types used by Props](components.md#types-used-by-props).

### `Render is generated for every component; rename this declaration`

**Cause:** the frontmatter declares a hoisted `Render` (a func, type or const). Orbit generates `Render` for every component.

**Fix:** pick another name, such as `renderRow`. See [Redeclaring and shadowing](components.md#redeclaring-and-shadowing).

### `orbit__x is reserved: names starting with orbit__ or Orbit__ are used by generated code`

**Cause:** a name in the frontmatter or the template starts with `orbit__` or `Orbit__`, the prefixes of Orbit's generated code.

**Fix:** rename it. See [Redeclaring and shadowing](components.md#redeclaring-and-shadowing).

### `Props fields IsOpen and Is_Open both match the attribute name isopen; rename one`

**Cause:** attributes match `Props` fields ignoring case, `-` and `_`, so two fields that differ only in those would take the same attribute.

**Fix:** rename or remove one of the fields. See [Props](components.md#props).

### `Props can't have type parameters`

**Cause:** `type Props` is generic (`type Props[T any] struct{…}`). A parent's tag can't pass type arguments.

**Fix:** use concrete field types, or an interface. See [Props](components.md#props).

### `a component import needs an alias: Card "./Card.orbit"`

**Cause:** an `.orbit` import has no alias (`"./Card.orbit"`) or the blank alias `_`. The alias is the component's tag name, so it's required.

**Fix:** add the suggested alias, `Card "./Card.orbit"`, and use the component as `<Card>`. See [Importing components](components.md#importing-components).

### `component Card is imported but not used; remove the import`

**Cause:** an `.orbit` import whose alias is used neither as a tag (`<Card>`) nor in Go code (`Card.Props`), like an unused Go import.

**Fix:** remove the import, or use the component. See [Importing components](components.md#importing-components).

### `dot imports aren't supported in a component; import the package by name`

**Cause:** a dot import (`. "fmt"` or `. "./Card.orbit"`) in the frontmatter. Components import packages by name only.

**Fix:** import the package by name and qualify its names (`fmt.Sprint`), or give a component its alias. See [Importing components](components.md#importing-components).

### `component path ../../X.orbit is outside src/`

**Cause:** an `.orbit` import's relative path leads out of `src/`. Components are compiled from `src/` only.

**Fix:** move the component under `src/` (usually `src/components/`), and fix the path. See [Importing components](components.md#importing-components).

### `no such component: src/components/Crad.orbit`

**Cause:** an `.orbit` import points at a file that doesn't exist. The error is at the import path, which Orbit resolves relative to the importing file and prints from the project root.

**Fix:** fix the path or the file name. See [Importing components](components.md#importing-components).

### `orbit: can't read export data for example.com/blog/internal/posts: …; using Expr fallbacks`

**Cause:** a warning, not an error. Orbit reads the compiled type information of your imports (with `go list -export`) to pick typed writes; when it can't (here for one package, or `orbit: can't load export data: …` for all of them), it uses the generic `Expr` writes instead. The rendered HTML is the same, only slightly slower to produce.

**Fix:** usually a problem in that package or in `go.mod`: run `go build ./...` to see it. See [Expressions are written by type](performance.md#expressions-are-written-by-type-without-reflection).

### `props is provided by Orbit and can't be redeclared here`

**Cause:** the top level of the render body declares one of the names Orbit provides, `props`, `ctx`, `slots`, `locals` or `orbit` (or, in a page, `params`), with `:=` or `var`.

**Fix:** pick another name (`post := props.Post`). Shadowing these names inside a nested block, such as `{for _, props := range variants}`, is allowed. Names starting with `orbit__` or `Orbit__`, and a hoisted declaration named `Render`, are reserved too. See [Redeclaring and shadowing](components.md#redeclaring-and-shadowing).

### `src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle`

**Cause:** every generated page and component imports `src/locals`, so `src/locals`, and anything it imports, can't import `orbit_gen/app`. An indirect import is reported at the import in `src/locals` that leads to it, with the chain of packages.

**Fix:** keep `src/locals` a plain types package. It may import your own packages, such as `internal/auth` for a `User` type, as long as none of them leads to `orbit_gen/app`; move code that needs `app.New()` into `cmd/server`. `orbit doctor` checks this too. See [The import rule](middleware.md#the-import-rule).

### `src/locals must be package locals, not package middleware` or `Locals must be a struct type: type Locals struct { … }`

**Cause:** the Go files in `src/locals` declare another package name, or `Locals` is a defined type that isn't a struct, such as `type Locals map[string]any`.

**Fix:** use `package locals` and `type Locals struct { … }`. An alias to a struct declared elsewhere (`type Locals = auth.Session`) works too. See [Declare the struct](middleware.md#declare-the-struct).

### `import cycle not allowed between component Props: src/components/A.orbit → src/components/B.orbit → src/components/A.orbit; declare the shared types in a package under internal/`

**Symptom:** a build error at the import alias in the first file of the chain, such as `src/components/A.orbit:2:8`, showing the shortest import chain between the components.

**Cause:** two `Props` types refer to each other's components: `A.Props` has a field of type `B.Props`, and `B.Props` one of type `A.Props`. Components may otherwise import each other freely.

**Fix:** declare the shared data types in a package under `internal/` and use them in both `Props`, as the comment-thread example does with `posts.Comment`. See [Mutual recursion](components.md#mutual-recursion).

### `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`

**Cause:** a page's file or directory name mixes text and a parameter in one segment (`post-[id].orbit`, `[lang]-[version].orbit`). That isn't supported yet.

**Fix:** rename it to a whole-segment parameter (`[id].orbit`) and parse the value in the page, for example with `id, ok := strings.CutPrefix(params.ID, "post-")`. Route errors name the file, without a line and column. See [Dynamic routes](routing.md#dynamic-routes-with-param).

### `rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`

**Cause:** a rest parameter is followed by more segments (`[...path]/edit.orbit`). That isn't supported yet.

**Fix:** use a whole-segment `[path]` there, or restructure the routes so the rest parameter comes last. See [Catch-all routes](routing.md#catch-all-routes-with-rest).

### `invalid route pattern "/[slug": segment "[slug" has unbalanced brackets`

**Cause:** a page's file or directory name has a `[` without its `]`, or the other way round. The same error ends `has an empty parameter name` for `[].orbit`.

**Fix:** close the bracket and name the parameter (`[slug].orbit`), or remove the brackets. See [Dynamic routes](routing.md#dynamic-routes-with-param).

### Two files produce the same route

**Symptom:** a build error that names both files.

**Cause:** for example `src/pages/blog.orbit` next to `src/pages/blog/index.orbit` (both are `/blog`), `[slug].orbit` next to `[id].orbit` in one directory, or a Markdown page with the same route as an `.orbit` page (`about.md` and `about.orbit`): `src/pages/about.orbit: route /about is already defined by src/pages/about.md; remove or rename one of them`.

**Fix:** delete or rename one of them. See [File-based routes](routing.md#file-based-routes).

### `generates the same Go package as src/components/my-card.orbit (orbit_gen/decl/components/my_card); rename one of them`

**Cause:** two `.orbit` files map to the same generated Go package, because package paths turn `-`, `.` and other characters into `_` and lowercase every segment: `my-card.orbit` and `my_card.orbit`, or `Card.orbit` and `card.orbit`. The error is on the later path, in sorted order: `src/components/my_card.orbit: generates the same Go package as …`.

**Fix:** rename one of them. See [Package paths](project-structure.md#package-paths).

### `<Comments server:defer>: Props.OnLoad has type func(), which can't be sent to a deferred component; props must be JSON-serializable`

**Cause:** a deferred component's props travel through the browser as JSON, so Orbit checks its whole `Props` type, including fields the call site doesn't set. Functions (so `orbit.Renderer` and `orbit.Slots`), channels, `unsafe.Pointer`, complex numbers, interface types (`any`, `error`) and maps with unsupported key types are rejected.

**Fix:** remove the field, tag it `json:"-"` (it then arrives as its zero value), or pass a small value such as an ID and load the data in the component. See [Props must be JSON-serializable](lazy-loading.md#props-must-be-json-serializable).

### `only slot="fallback" content is allowed inside a server:defer component`

**Cause:** a deferred component can only take a fallback; other slots can't be passed to it yet.

**Fix:** mark the child with `slot="fallback"`, or move it out of the tag:

```orbit
<Comments server:defer postID={post.Slug}>
  <p slot="fallback">Loading comments…</p>
</Comments>
```

See [The fallback slot](lazy-loading.md#the-fallback-slot).

### `server:defer only works on .orbit components` or `server:defer takes no value`

**Cause:** `server:defer` is a bare attribute, and only on a component tag (`<Self>` included), not on an HTML element, `<slot>` or `<Fragment>`.

**Fix:** move it to the component tag, and drop any value (`server:defer`, not `server:defer="x"`). See [Template syntax: `server:defer`](reference/template-syntax.md#serverdefer).

### `server:defer and client:* can't be combined; put client:* on a component inside the deferred one`

**Cause:** one tag has both directives.

**Fix:** keep `server:defer` on the tag, and put the `client:*` directive on a component that the deferred component renders. See [Combining the two](lazy-loading.md#combining-the-two).

### `client:*` directive errors

Each message names the directive you wrote:

| Message | Fix |
|---|---|
| `client:visible only works on .orbit components; move the <script> into a component` | Move the element and its `<script>` into a component, and put the directive on its tag. |
| `client:visible takes a static string value, not an expression` | Write the value as a string: `client:visible="200px"`. |
| `client:idle takes no value` | `client:load` and `client:idle` are bare: drop the value. |
| `client:media needs a media query, e.g. client:media="(max-width: 600px)"` | Add the media query. |
| `only one client:* directive is allowed per component` | Keep one directive on the tag. |
| `client:only is for framework components, which Orbit doesn't support; use client:load, client:idle, client:visible or client:media` | Pick one of the four strategies. |
| `unknown directive "client:hover" (client directives: client:load, client:idle, client:visible, client:media)` | Fix the spelling. `server:` and `client:` are reserved namespaces. |

See [Template syntax: `client:*`](reference/template-syntax.md#client).

### `orbit.JSON: Post.Notify has type func(), which encoding/json can't encode`

**Cause:** the value passed to `orbit.JSON` (or to `set:json`) has a type that `encoding/json` can never encode: a func, a channel, a complex number, an `unsafe.Pointer`, or a map whose keys aren't strings, integers or `TextMarshaler`s, anywhere in the type. Here a field of `Post` is a func:

```orbit file=src/components/NewsItem.orbit expect-error
---
type Post struct {
	Title  string `json:"title"`
	Notify func()
}

type Props struct {
	Title string
}
---
<news-item data-post={orbit.JSON(Post{Title: props.Title})}></news-item>
```

```text
src/components/NewsItem.orbit:11:34: orbit.JSON: Post.Notify has type func(), which encoding/json can't encode
```

For `set:json`, the message reads `set:json: the value has type func(), which encoding/json can't encode`. Orbit reports it at generate time, in `orbit build`, `orbit dev`, `orbit generate` and `orbit check`, because the encoding would fail on every render.

**Fix:** tag the field `json:"-"` (or unexport it), or pass a struct with only the fields the script needs. Interfaces (`any`) are accepted: what they hold is checked at render time. See [Client modules: Unsupported types](reference/client-modules.md#unsupported-types).

### `set:json only works on <script type="application/json">`

**Cause:** `set:json` was used on another element, or on a `<script>` without `type="application/json"`.

**Fix:** put it on an empty JSON script, and read it with `json(id)` from the component's module (`import { json } from "./PostMeta.orbit"`):

```orbit
<script type="application/json" id="tags" set:json={props.Tags}></script>
```

A value `encoding/json` can't encode (a NaN float, a cyclic structure) fails at render time, at the `set:json` position. For data that belongs to one element, use `orbit.JSON` in a `data-*` attribute instead. See [Template syntax: `set:json`](reference/template-syntax.md#setjson).

### `class:list` errors

| Message | Fix |
|---|---|
| `class:list items must be a string or []string, not int` | Convert the item to a string (`strconv.Itoa(n)`), or make it a condition: `"wide": n > 2`. |
| `class={…} next to class:list must be a string or []string, not int` | The `class` value is merged into the list, so it follows the same rule. |
| `class:list sets the class prop, which <Card> doesn't have (props: title)` | Add a `Class string` field to the component's `Props`, and write `class={props.Class}` (or a `class:list` with `props.Class`) where it belongs in the component's template. |
| `class:list takes a list in braces, e.g. class:list={"card", "active": active}` | Write the list in braces. For one static string, use `class="…"`. |
| `class:list items can't be nested lists; list the classes directly, or use a []string` | Flatten Astro's nested array or object: `{"a", {"b": on}}` becomes `{"a", "b": on}`. |
| `a class:list condition must be a bool expression, e.g. class:list={"card", "active": active}` | Write `"class": cond`, with a `bool` on the right. |
| `syntax error in class:list: …` | Write the items as Go composite-literal elements: separated by commas, conditions as `"class": cond`. |
| `only one class:list is allowed per tag` | Merge them into one list. |
| `class:list only works on HTML elements and components` | Put the classes on an element: `<slot>`, `<Fragment>`, `<script>` and `<style>` take no `class:list`. |

A condition that isn't a `bool` is Go's own error at the condition: `non-boolean condition in if statement` on an HTML element, `cannot use n (variable of type int) as bool value in class:list condition` on a component. See [Template syntax: `class:list`](reference/template-syntax.md#classlist).

### `define:vars isn't supported; pass data with orbit.JSON on a data-* attribute, or set:json`

**Cause:** `define:vars` is an Astro directive, and `<script>` and `<style>` content can't see Go values.

**Fix:** write the values into `data-*` attributes, with `orbit.JSON` for structured data (`data-post={orbit.JSON(props.Post)}`, read with `data(this, "post")` from `import { data } from "./Card.orbit"`), or pass a block of data with `set:json` on a `<script type="application/json">`. See [Passing data to client scripts](components.md#passing-data-to-client-scripts).

### `the frontmatter always returns here, so the template below is never rendered`

**Cause:** the last statement of the frontmatter always returns (`return ctx.Redirect("/", 302)`, or an `if`/`else` that returns on every branch), and the template below it isn't empty.

**Fix:** remove the template, or make the return conditional. See [Returning errors, redirects and 404s](components.md#returning-errors-redirects-and-404s).

### `a // comment can't end Go code in the template; use /* */`

**Cause:** an expression or control header in the template ends with a line comment, such as `{count // items}`. A `//` comment runs to the end of the line, so it would swallow the code Orbit generates after the expression.

**Fix:** use a block comment (`{count /* items */}`), or put the note in a template comment, `{/* … */}`.

### `unknown prop "1x" on <Card>: not a Go field name`

**Cause:** an attribute on a component can't become a Go field name (it starts with a digit, or holds characters other than letters, digits, `-` and `_`). Orbit reports this when it can't list the component's `Props` fields, for example when `Props` embeds another struct.

**Fix:** rename the attribute to match a `Props` field (`<Card first={…}>` for a field `First`). See [Props](components.md#props).

### `unexpected </ul>: <li> opened at 12:5 isn't closed`

**Cause:** Orbit doesn't auto-close elements the way browsers do, so `<p>`, `<li>`, `<td>` and `<option>` need their closing tags.

**Fix:** close the element (`<li>…</li>`) or self-close it (`<div />`). Void elements such as `<br>` and `<img>` take no closing tag. See [HTML and custom elements](reference/template-syntax.md#html-and-custom-elements).

### `content before the first {case} of a {switch} is never rendered; move it into a {case} or {default}`

**Cause:** text, an element or an expression sits between `{switch …}` and its first `{case}`. A Go switch runs only the body of the case that matches, so this content could never render. Whitespace and template comments are fine there.

**Fix:** move it into a `{case}` or `{default}`, or before the `{switch}` if it should always render. See [`{switch}`](reference/template-syntax.md#switch).

### `{switch}` block errors

- `unexpected {case}: no {switch} is open` (or `{default}`): the case isn't directly inside a `{switch}`. An element or block opened in the previous case must be closed first.
- `multiple {default} cases in {switch} (first at 9:2)`: a switch takes at most one `{default}`.
- `fallthrough isn't supported in a {switch}; list the values in one {case a, b}`: a template has no statement position for `fallthrough`; list the values that share a body in one case.
- `{case} needs a value or a condition, e.g. {case "draft"}; use {default} for the rest`: the case list is empty.
- `unexpected {/case}: a {case} ends at the next {case}, {default} or {/switch}`: cases have no closing tag.
- `duplicate case "a" (constant of type string) in expression switch`: Go's error, at the `.orbit` position of the second `"a"`.

## Markdown files

Errors in Markdown files point at their `.md` line and column: a frontmatter key or value, the `layout` value, or a link's `[`. See [Markdown](markdown.md).

### `unknown frontmatter key "tilte" for layout Post (props: title, date, tags, headings)`

**Cause:** a frontmatter key matches no field of the layout's `Props` (keys match fields ignoring case, `-` and `_`).

**Fix:** fix the key's spelling, or add the field to the layout's `Props`. See [Layouts](markdown.md#layouts).

### `frontmatter draft: "yes" isn't a bool (Post.Props.Draft is bool)`

**Cause:** a frontmatter value doesn't fit the type of the layout's field: a quoted `"yes"` for a `bool`, a list for a `string`, a quoted date for a `time.Time`. A number too large for the field is `frontmatter order: 300 overflows uint8 (Post.Props.Order is uint8)`, and a field that frontmatter can't set at all (`any`, a pointer, an interface, a func) is `frontmatter on-click: Post.Props.OnClick is func(), which frontmatter can't set`.

**Fix:** write the value as the field's type expects (`draft: true`, `date: 2026-10-26` without quotes), or change the field's type. See the table in [Layouts](markdown.md#layouts).

### `frontmatter key "subtitle" has no value; delete the line or give it a value`

**Cause:** a frontmatter key with nothing after the colon (or `null`).

**Fix:** delete the line, or give the key a value.

### `invalid YAML in the frontmatter: mapping values are not allowed in this context`

**Cause:** the frontmatter isn't valid YAML: here, a value with an unquoted `: ` in it, such as `title: Go: a tour`. YAML anchors, aliases and tags are errors too (`YAML aliases (*x) aren't supported in frontmatter; repeat the value`).

**Fix:** quote the value (`title: "Go: a tour"`). See [Frontmatter](markdown.md#frontmatter).

### `layout ../layouts/Post.orbit: no such file`

**Cause:** the `layout` path, relative to the Markdown file, doesn't name an `.orbit` file under `src/`. A path that doesn't end in `.orbit` is `layout ./Post.md must be an .orbit file`, and one outside `src/` is `layout ../../../X.orbit is outside src/`.

**Fix:** fix the path, counting `../` from the Markdown file's directory.

### `frontmatter key "headings" can't be set: Orbit fills Post.Props.Headings with the page's headings; rename the key`

**Cause:** the layout has a `Headings []orbit.Heading` field, which Orbit fills with the page's headings, and the frontmatter sets it too.

**Fix:** rename the key (and the field it's meant for).

### `link ./routing.md: no such file`

**Cause:** a relative link to a Markdown file that doesn't exist. A link to a Markdown file that isn't a page is `link ../content/intro.md: src/content/intro.md isn't a page, so it has no URL; link to a page, or import it into one`.

**Fix:** fix the link, or link to the URL of the page that renders the file. See [Links between Markdown files](markdown.md#links-between-markdown-files).

### `warning: link ./routing-guide.md#setup: src/pages/blog/routing-guide.md has no heading #setup`

**Cause:** the link's `#fragment` is no heading ID of the target. It's a warning, since the ID may come from the target's layout or raw HTML.

**Fix:** use the heading's ID (lowercase, punctuation removed, spaces turned into `-`), or ignore the warning if the ID exists. See [Heading IDs](markdown.md#heading-ids).

### `a Markdown page can't have route params; rename it, or write a [slug].orbit page`

**Cause:** a Markdown file in `src/pages/` has brackets in its path, such as `blog/[slug].md`. A Markdown page has no code to read params with.

**Fix:** rename the file, or write an `.orbit` page that loads the content for each slug.

### `unknown prop "x" on <Post> (Post takes no props)`

**Cause:** an attribute on an imported Markdown file's tag. `<Post />` takes no attributes; its layout gets the frontmatter.

**Fix:** remove the attribute. To pass values to the layout, put them in the Markdown file's frontmatter. See [Importing Markdown](markdown.md#importing-markdown-into-a-component).

## Runtime

### `orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app`

**Cause:** Go code called a component's `Render` from its declaration package (`orbit_gen/decl/…`) in a program that doesn't import `orbit_gen/app`, which links every component to its code. This usually happens in a test. Rendering a component during package initialization fails the same way.

**Fix:** add a blank import, once per test package:

```go
import _ "example.com/blog/orbit_gen/app"
```

`orbittest` reports this as a test failure with the same hint. See [Where test files go](testing.md#where-test-files-go).

### `orbit: render depth exceeded 1000: src/components/Comment.orbit > src/components/CommentThread.orbit > src/components/Comment.orbit > …`

**Cause:** a recursive component (`<Self>`, or two components that render each other) never stops. Orbit stops it at 1000 nested components instead of letting the Go stack overflow, in development and production alike. It's a render error: a 500, or the overlay in `orbit dev`.

**Fix:** guard the recursive call with an `{if}` (render `<Self>` only for nodes that have children), and make sure your data ends. The message shows the component stack. See [The depth limit](components.md#the-depth-limit).

### `orbit: </head> can't be inside orbit.RenderHTML; keep the document head outside the captured content`

**Cause:** content rendered with `orbit.RenderHTML` (a fragment cache component, say) reached a `</head>`, typically because a layout is inside the cached children. The page's `<link>` and `<script>` tags are written at the page's own `</head>`, and a cached copy must never hold one page's tags. It's a render error, returned by `orbit.RenderHTML`.

**Fix:** keep the layout outside: render `<Base>` in the page and put the cached component inside its body. See [Caching fragments](rendering.md#caching-fragments).

### A URL answers 404

Check which case you're in:

- **No page matches.** A `[param]` matches exactly one segment, so `src/pages/blog/[slug].orbit` doesn't match `/blog/hello/like`. A `public/` file or `/_orbit/*` asset is looked up before pages.
- **The file isn't a page.** A file or directory under `src/pages/` whose name starts with `_` (`_parts/Byline.orbit`) is a component, so it has no URL.
- **The page returned `orbit.ErrNotFound`**, as `blog/[slug].orbit` does for an unknown slug.
- **The body is empty**: the project has no `src/pages/404.orbit`. `orbit new` scaffolds one.
- **You get your mux's plain-text 404** instead of `404.orbit`: with pages first (`site.Middleware(mux)`), give the mux `site.NotFound()` as its catch-all:

  ```go
  mux.Handle("/", site.NotFound()) // unmatched paths render 404.orbit
  ```

See [The 404 page](routing.md#the-404-page) and [Choosing precedence](server.md#choosing-precedence). For a 404 on a deferred component, see [below](#a-serverdefer-component-only-shows-its-fallback).

### A URL redirects with 308

**Cause:** each page has one canonical URL, and the other form gets a 308. By default pages have no trailing slash, so `/blog/hello/` gets a 308 to `/blog/hello`, keeping the query string (`/search/?q=go` → `/search?q=go`); with `"trailingSlash": "always"` in `orbit.json`, `/blog/hello` gets a 308 to `/blog/hello/`. The redirect is sent only when the other form of the path is a page; other paths reach your next handler untouched, and files are never redirected.

**Fix:** nothing is broken. Link to canonical URLs (`href="/blog/hello"`, or `href="/blog/hello/"` in `"always"`) to skip the extra round trip. Middleware that wraps Orbit runs before the redirect, so it sees both forms; compare paths with that in mind. To change which form is canonical, set [`trailingSlash`](configuration.md#trailingslash). See [Trailing slashes](routing.md#trailing-slashes).

### A file added to `dist/client/` isn't served

**Cause:** in production, the server lists its static files once, at startup, so it can check every request against them without touching the disk. A file copied into `dist/client/` (or the `ORBIT_STATIC` directory) while it runs isn't in that list. A directory is never served either, not even its `index.html`: a file is served only at its exact path.

**Fix:** restart the server after changing its static files, or rebuild with `orbit build`. Link to the file's full path (`/docs/index.html`, not `/docs/`). `orbit dev` looks files up on every request, so this doesn't happen there. See [How static files are served](deploy.md#how-static-files-are-served).

### A non-GET request to a file gets a 404 or 405

**Cause:** `/_orbit/*` assets and `public/` files are served for `GET` and `HEAD` only. Orbit passes any other method to your next handler, so the response is whatever that handler answers: a 404 from `site.Handler()`, or your own mux's answer. The only 405 Orbit sends itself is on the island endpoint `/_orbit/island/…`, for methods other than `GET`, `HEAD` and `POST`, with `Allow: GET, HEAD, POST`.

**Fix:** request files with `GET`. Handle form posts and uploads with a page (pages receive every method, in `ctx.Request.Method`) or with your own handler at a path that isn't a file. See [Which HTTP methods Orbit handles](routing.md#which-http-methods-orbit-handles).

### `orbit.JSON: json: unsupported value: NaN`

**Cause:** a value passed to `orbit.JSON` couldn't be encoded at render time, for a reason only the value shows: a NaN or infinite float, a pointer cycle, a func held in an `any` field, or an error from a `MarshalJSON` method. It's a render error at the attribute value's position (a 500 in production, logged with that position; an overlay in `orbit dev`):

```text
src/components/LikeButton.orbit:9:24: orbit.JSON: json: unsupported value: NaN
```

For `set:json`, the position is the `set:json` attribute's: `src/pages/index.orbit:16:47: set:json: json: unsupported value: NaN`. When the value was written through a spread or a variable that Orbit couldn't type, the message has no position: `orbit: orbit.JSON: json: unsupported value: NaN`.

**Fix:** check the value in the frontmatter (`math.IsNaN`), break the cycle, or encode a struct with only what the script needs. See [Client modules](reference/client-modules.md#unsupported-types).

### `orbit: <like-button> has no data-post attribute`

**Cause:** a script called `data(el, "post")` on an element without a `data-post` attribute. The component module's types say which keys the component may write, not which element carries them. Common reasons: the script reads a different element than the one the template puts the attribute on (`this.querySelector("button")` instead of `this`), or the element comes from another component. `json(id)` throws `orbit: no element with id "…"` the same way when the page has no element with that `id`.

**Fix:** call `data` on the element with the attribute; in a custom element, that's usually `this`. See [Client modules: `data(el, key)`](reference/client-modules.md#datael-key).

### Visitors get a `500 Internal Server Error` page

**Cause:** a page or component returned an error, or panicked. In production, Orbit answers with a minimal built-in page that never shows the error, and logs the error with its `.orbit` position to stderr:

```text
orbit: GET /blog/hello: src/pages/blog/[slug].orbit:14:5: posts: connection refused
```

**Fix:** find the line in your server's stderr (your platform's logs) and fix the error at that position: the frontmatter `return` that returned it, or the line that panicked. `orbit dev` shows the same error in an overlay. Because responses are buffered, a failed render never sends half a page. In a stack trace from a binary built with `-trimpath`, frames from `.orbit` files keep their absolute paths. For errors your app expects (a reader who must sign in, a backend that's down), answer them yourself with [`orbit.WithErrorHandler`](rendering.md#answering-errors-yourself-witherrorhandler); a handler that returns `false` leaves the error to this 500. A custom `src/pages/500.orbit` is planned after the first release. See [Status codes and errors](rendering.md#status-codes-and-errors) and [Stack traces and `-trimpath`](reference/cli.md#stack-traces-and--trimpath).

### A `server:defer` component only shows its fallback

The placeholder keeps its fallback whenever the island request fails. In `orbit dev`, the loader logs the status and the response body to the **browser console**; in production it stays silent, so look at the request to `/_orbit/island/…` in your browser's network panel. By status:

- **400 after a deploy, or with several builds behind one load balancer**: each `orbit build` embeds a new island key, so an instance can't open tokens sealed by another build. Create one key and set it as `ORBIT_KEY` on every instance of every build:

  ```sh
  go tool orbit create-key
  ```

- **400 otherwise**: the `props` token is missing, malformed, was changed, was sealed with another key, or doesn't decode into the component's `Props`. A server built with plain `go build` or run with `go run` has no embedded key: it uses a random key per process (and warns on stderr), so pages rendered before a restart, or by another process, can't load their islands. Build with `orbit build`, or set `ORBIT_KEY`. Pages that were open when you rotated the key keep their fallback until reloaded.
- **404**: the component returned `orbit.ErrNotFound`, or its ID isn't one used with `server:defer` in the build answering the request.
- **3xx**: the component returned `ctx.Redirect(…)`; the loader doesn't follow redirects.
- **500**: the component returned an error or panicked. In dev, the body holds the error and its `.orbit` position.
- **A rejection from your CSRF middleware**: props over about 8 KB are sent with `POST`. Exempt `/_orbit/island/`, or use Go's `http.CrossOriginProtection`.
- **No island request at all**: JavaScript is off, a strict Content Security Policy blocks the inline loader (allow it with `'unsafe-inline'` or the script's hash), or the placeholder came in an `orbit.RenderComponent` fragment on a page that has no deferred component of its own, and so no loader.

See [Status codes and errors](lazy-loading.md#status-codes-and-errors), [The key: `ORBIT_KEY` and rolling deploys](lazy-loading.md#the-key-orbit_key-and-rolling-deploys) and [The loader and Content Security Policy](lazy-loading.md#the-loader-and-content-security-policy).

### The server stops at startup with an error about `ORBIT_KEY`

**Cause:** `ORBIT_KEY` is set, but it isn't the standard base64 encoding of exactly 32 bytes.

**Fix:** create a key with `go tool orbit create-key` and set the printed value exactly, quoted in your shell (`ORBIT_KEY='…' ./dist/server`) since it can contain `/` and `+`. Or unset it to use the key embedded by `orbit build`. See [Island key](deploy.md#island-key-orbit_key).

### Locals set in middleware don't reach the page

**Symptom:** the page sees an empty `Locals` (pages never see `nil`), or a later middleware doesn't see what an earlier one set.

**Cause:** usually one of these:

- the middleware didn't pass the request returned by `EnsureLocals` on to `next`, so the value never travels with it;
- the middleware sits after Orbit (`site.Middleware(mw(mux))`), so it never runs for pages;
- a middleware attached a fresh value with `WithLocals`, which only handlers further down see; the middleware that ran earlier keeps its own pointer.

**Fix:** in every middleware, use the same line and pass the returned `r` on, and wrap the middleware around Orbit:

```go
r, l := orbit.EnsureLocals[locals.Locals](r)
l.Theme = "dark"
next.ServeHTTP(w, r)
```

In your own handlers, `orbit.LocalsFromRequest` returns `nil` when no middleware attached any, so check for it. See [Set locals from middleware](middleware.md#set-locals-from-middleware) and [Where middleware goes](middleware.md#where-middleware-goes).

### A link renders as `href="about:invalid#orbit"`

**Cause:** the value of a URL attribute (`href`, `src`, `action`, …) has a `javascript:` or `vbscript:` scheme, so Orbit replaced it. The filter applies to expression values and spread attributes, not to static values you write in the template.

**Fix:** if the value comes from users, this is the protection working. If you trust it, give it the type `orbit.URL`, which skips the filter:

```orbit
---
bookmarklet := orbit.URL("javascript:void(document.body.classList.toggle('dark'))")
---
<a href={bookmarklet}>Toggle dark mode</a>
```

See [URL attributes](components.md#url-attributes).

## Dev server

### The browser doesn't reload after a save

**Cause:** usually one of these:

- **A compile error**: the page shows the error overlay instead (see the next entry), and the terminal prints the error.
- **The page has no `</body>`**: the proxy adds its reload script just before `</body>`, so a page without one (check your layout) gets no live reload.
- **The response came back compressed**: the proxy asks your server for uncompressed responses, and passes a compressed one through without the reload script, printing a one-time warning. Make your gzip middleware honor the request's `Accept-Encoding`.
- **You're not on the dev proxy**: open <http://localhost:4321> (or your `--port`). A server you run yourself, for example under Delve, gets no live reload.
- **The file isn't watched**: Orbit watches `src/`, `public/` and every `.go` file in your module, except `orbit_gen/`, `dist/` and `.orbit/`.

If the connection drops while your server restarts, the reload script reconnects with backoff and reloads once the server is back. See [Live reload](dev-server.md#live-reload) and [What a change rebuilds](dev-server.md#what-a-change-rebuilds).

### The browser shows an error overlay

**Cause:** the overlay replaces the page for three kinds of errors:

- **Compile errors** (a Go type error, an unknown prop, a template syntax error): the old server keeps running, and the overlay shows the message, its `file:line:col` and a code frame. Requests that aren't for HTML pages still reach the old server.
- **Startup errors**: the new server built but exited, for example because a database is unreachable, or it didn't accept connections within 10 seconds (`your server didn't accept connections on ORBIT_ADDR=127.0.0.1:… within 10s; does it listen on orbit.Addr()?`). The overlay shows its stderr. A server that exits later on its own (`your server exited: exit status 1; save a file to restart it`) is shown the same way.
- **Render errors**: a page returned an error or panicked; your server's own dev overlay shows it with its `file:line`.

**Fix:** fix the error and save. The overlay listens for live-reload events, so the page reloads by itself. For a server that didn't accept connections, make it listen on `orbit.Addr()`, which reads the `ORBIT_ADDR` that `orbit dev` sets (see [Running under `orbit dev`](server.md#running-under-orbit-dev)). The same messages appear in the terminal running `orbit dev`. See [Dev server: Errors](dev-server.md#errors).

### Port 4321 is already in use

**Cause:** another process is listening there. `orbit dev`, `orbit preview` and a built `dist/server` all default to port 4321. `orbit dev` then moves to the next free port (`orbit dev: port 4321 is in use, using 4322 instead`), but `orbit preview` and `dist/server` stop with `address already in use`.

**Fix:** pick another port for one of them:

```sh
orbit dev --port 3000
PORT=3000 go tool orbit preview
```

Under `orbit dev`, your own server must listen on `orbit.Addr()` (as `orbit.ListenAndServe` does), which `orbit dev` points at a random localhost port; a server that listens on a fixed port can't be reached by the dev proxy. See [Running under `orbit dev`](server.md#running-under-orbit-dev).

### Other devices can't reach the dev server

**Cause:** the dev server listens on localhost only, as Astro's does.

**Fix:** listen on all interfaces, then open your machine's address from the other device:

```sh
orbit dev --host
```

Your own server keeps running on `127.0.0.1` behind the proxy either way. See [Start it](dev-server.md#start-it).

### Rebuilds are slow after editing a widely imported package

**Cause:** when you edit a `.go` package that every component's `Props` depends on (such as `internal/posts`), `go build` recompiles every generated package. Go recompiles a package's importers whenever it changes, even when only a function body did. On a 200-component project this took about 0.6 s on 16 threads and about 1.05 s on 4 cores, against about 0.2 s for a component edit. Edits to only a `<style>` or `<script>` need no Go rebuild at all.

**Fix:** this is `go build` time. Dev builds already skip DWARF and the symbol table (`-ldflags=-s -w`, `-gcflags=all=-dwarf=false`) to compile and link faster, and on a `.go`-only edit `orbit dev` starts `go build` right away, while it regenerates. Keep frequently edited Go code out of packages that every `Props` imports. Edits to packages fewer components import rebuild less. Also check your server: each Go rebuild restarts it, so a slow startup adds to every save, and a server that runs its own `http.Server` without handling `SIGINT` is only killed after 5 seconds. `orbit.ListenAndServe` handles `SIGINT` for you. See [How long a rebuild takes](dev-server.md#how-long-a-rebuild-takes).

## Editor

### No completion in `.orbit` files

Go through these in order:

1. Run `orbit doctor` and fix the gopls line. If only template completion works (tags, props, slots, directives) and Go completion doesn't, see [Go features are disabled in the editor](#go-features-are-disabled-in-the-editor-gopls-is-too-old).
2. **VS Code**: install the extension with `code --install-extension orbit-z.orbit`. It runs the `orbit` on your `PATH`; if it isn't there, set `orbit.path`.
3. **Neovim**: add the `vim.filetype.add({ extension = { orbit = 'orbit' } })` line and `vim.lsp.enable('orbit')` from [Editor setup](editor-setup.md#neovim).
4. Open the project at its root: the language server looks for the directory with `go.mod` and `src/`.

For TypeScript features inside `<script>` (completion, diagnostics, …), see [No TypeScript completion in `<script>`](#no-typescript-completion-in-script) below.

### No TypeScript completion in `<script>`

**Cause:** the TypeScript features of `<script>` blocks come from TypeScript 7's language server, which `orbit lsp` starts when you open an `.orbit` file with a script. Without it, scripts get highlighting only, and the language server says so once: "TypeScript features in `<script>` blocks need TypeScript 7.0.2 or later". The usual reasons:

- No TypeScript 7 is installed, and Bun isn't either (with Bun, Orbit runs `bun x --package typescript@7.0.2` itself).
- The `tsc` found is TypeScript 5 or 6, which has no language server (`tsc --version` prints `Version 5.9.3` or `6.0.3`). Your editor's own TypeScript is usually one of those: it serves `.ts` files, not `.orbit` scripts.
- The cursor is in a script Orbit doesn't treat as code: a JSON block (`type="application/json"`, `set:json`, `application/ld+json`) or a script with a `src` attribute (its `.ts` file has the features instead).

**Fix:** run `orbit doctor` and look at the `typescript` line. Install TypeScript 7 (`bun add -g typescript@7.0.2` or `npm i -g typescript@7.0.2`), or point the `typescriptPath` option at one (`orbit.typescriptPath` in VS Code), then restart the language server (**Orbit: Restart Language Server**). `is:inline` scripts get JavaScript features, without the data types or imports: they run as written. See [TypeScript in `<script>` blocks](editor-setup.md#typescript-in-script-blocks).

### `Cannot find module './LikeButton.orbit' or its corresponding type declarations`

**Cause:** TypeScript (`orbit check`, as `error TS2307`, or your editor) can't find the generated types of `import { data } from "./LikeButton.orbit"`. There are three reasons:

- `orbit_gen/types/` hasn't been written yet, as in a fresh clone. In a `<script>` block in the editor, the language server serves the types from memory instead, a moment after it starts (it type-checks the project first); in a `.ts` file, the editor's own TypeScript needs them on disk.
- Your `tsconfig.json` lacks the `rootDirs` entry that points TypeScript at `orbit_gen/types/` (only for `.ts` files in the editor: `orbit check` and the TypeScript in `<script>` blocks add it themselves). `orbit doctor` reports it.
- The component has no typed data: no `data-*` attribute set with a direct `orbit.JSON(…)` call, and no `set:json` block with a static `id`. Such a component has no module. This is also what you see after renaming the file: the import must use the new name.

**Fix:** in a `<script>` block, wait a second, or check the component for Go errors (a component whose frontmatter doesn't compile has untyped data); otherwise run `orbit generate` (or keep `orbit dev` running); add `"rootDirs": [".", "orbit_gen/types"]` to `compilerOptions` and `"orbit_gen/types/*.d.ts"` to `include` in `tsconfig.json`; or check that the template writes the data with `orbit.JSON`. See [Client modules](reference/client-modules.md#components-without-data).

### The Go extension reports errors in `orbit_gen/`

**Cause:** the VS Code Go extension runs its own gopls, which sees the generated files in `orbit_gen/` and reports their errors there. Orbit doesn't suppress them.

**Fix:** they're usually the same errors Orbit already shows at the right place in your `.orbit` file, so fix them there. Errors that appear only in `orbit_gen/` often mean the generated code is stale: run `orbit generate`, or keep `orbit dev` running. Don't hide them with a gopls `directoryFilters` entry such as `"-orbit_gen"`: Orbit passes your editor's `gopls` settings to its own gopls too, and that one needs `orbit_gen/` to give you Go features in `.orbit` files. See [The Go extension's errors in `orbit_gen/`](editor-setup.md#the-go-extensions-errors-in-orbit_gen).

### Rename and references wait after the editor starts

**Cause:** until gopls has loaded every component, references and rename would miss uses, so Orbit holds them; your editor shows a progress indicator meanwhile. Without `orbit_gen/` on disk, gopls loads every component from memory first, which is a little slower: about 0.4 s for 200 components, against about 0.3 s with an up-to-date `orbit_gen/`.

**Fix:** nothing to fix: it lasts well under a second. Running `orbit generate` (or keeping `orbit dev` running) makes it a little shorter. If a rename is refused with a message, it would have changed generated code that doesn't correspond to anything in your files. See [Startup and `orbit_gen/`](editor-setup.md#startup-and-orbit_gen) and [Rename](editor-setup.md#rename).

## See also

- [Coming from Astro](coming-from-astro.md): what maps to what, and what isn't supported
- [CLI](reference/cli.md): `orbit doctor`, error output and exit codes
- [Components](components.md): frontmatter, props and the compile errors around them
- [Template syntax](reference/template-syntax.md): every directive and its errors
- [Lazy loading](lazy-loading.md): `server:defer`, the island key and island status codes
- [Dev server](dev-server.md): rebuilds, live reload and the overlay
- [Editor setup](editor-setup.md): gopls, VS Code and Neovim
