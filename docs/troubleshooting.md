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
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); orbit check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
✓ orbit_gen: up to date
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

**Cause:** your project has no `tool github.com/bertrandkhe/orbit/cmd/orbit` directive in `go.mod`, and your globally installed `orbit` has a different version than the `github.com/bertrandkhe/orbit` requirement. The generated code must match the runtime it runs against, so the CLI stops and points you to `orbit doctor`.

**Fix:** pin the CLI in the project, at the version `go.mod` already requires (`v0.1.0` here; use the one on your `require` line):

```sh
go get -tool github.com/bertrandkhe/orbit/cmd/orbit@v0.1.0
go tool orbit build
```

With the directive, a global `orbit` hands over to `go tool orbit` on its own, so the versions always match. The first `go tool orbit` compiles the CLI, which takes a moment. The check is skipped when `go.mod` replaces `github.com/bertrandkhe/orbit` with a local directory. See [The pinned CLI](reference/cli.md#the-pinned-cli-orbit-and-go-tool-orbit).

### Go features are disabled in the editor: gopls is too old

**Symptom:** your editor shows an error with the gopls install command and a pointer to `orbit doctor`. In `.orbit` files there's no Go completion, hover, Go diagnostics, definition, references or rename, while tag, prop, slot and directive completion still work.

**Cause:** the gopls that `orbit lsp` found was built with a Go older than your project's `go` directive. Such a gopls would reject newer language features as errors, so Orbit refuses it.

**Fix:** install a current gopls, then restart the language server:

```sh
go install golang.org/x/tools/gopls@latest
```

If the error stays, check which gopls is used: the `orbit.goplsPath` setting (or the `goplsPath` initialization option) comes first, then `gopls` on your `PATH`. A gopls built with a recent enough Go but older than v0.24.0 still runs; `orbit doctor` only warns about it. See [Old gopls versions](editor-setup.md#old-gopls-versions) and [How gopls is found](editor-setup.md#how-gopls-is-found).

### `orbit check` skips TypeScript type checking

**Cause:** Bun is missing or older than 1.4.4. `orbit check` then prints a notice with the command to install or upgrade Bun, still runs the Go checks, and its exit code reflects the Go checks only.

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

## Compile errors in `.orbit` files

`orbit build`, `orbit check` and the `orbit dev` overlay print every error as `src/…:line:col: message`, at its position in your `.orbit` file. Plain `go build` and `go vet` print the same positions as `./src/…`. If an error points into `orbit_gen/` instead, see [The Go extension reports errors in `orbit_gen/`](#the-go-extension-reports-errors-in-orbit_gen).

### `unknown prop "psot" on <Card> (props: post, featured)`

**Cause:** an attribute on a component tag matches no field of its `Props`. Matching ignores case, `-` and `_`, and the list shows the names that would work.

**Fix:** use one of the listed names (`post={p}`), or add the field to the component's `Props`. When `Props` embeds another struct or is an alias (`type Props = other.T`), Orbit can't list the fields, and Go reports the same mistake at the same place as `unknown field Psot in struct literal of type Card.Props`. See [Unknown props](components.md#unknown-props).

### `unknown component <Crad>: no .orbit import with this alias`

**Cause:** a capitalized tag is always a component, and its name must be the alias of an `.orbit` import.

**Fix:** fix the tag's spelling, or import the component in the frontmatter: `Card "../components/Card.orbit"`. See [Template syntax: Components](reference/template-syntax.md#components).

### Go type errors at `.orbit` lines

```text
src/pages/index.orbit:9:11: cannot use "yes" (untyped string constant) as bool value in struct literal
src/components/Card.orbit:11:29: props.Post.Titel undefined (type posts.Post has no field or method Titel, but does have field Title)
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

```text
src/pages/blog/[slug].orbit:9:26: params.slug undefined (type Params has no field or method slug, but does have field Slug)
src/pages/blog/[slug].orbit:9:26: params.Slg undefined (type Params has no field or method Slg)
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

### `only .orbit components can be imported by relative path; import client modules from a <script>`

**Cause:** the frontmatter imports a relative path that doesn't end in `.orbit`, such as `"./util.ts"`.

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

### `src/locals can't import example.com/blog/orbit_gen/app: every page imports src/locals, so this is an import cycle`

**Cause:** every generated page and component imports `src/locals`, so `src/locals`, and anything it imports, can't import `orbit_gen/app`. An indirect import is reported at the import in `src/locals` that leads to it, with the chain of packages.

**Fix:** keep `src/locals` a plain types package. It may import your own packages, such as `internal/auth` for a `User` type, as long as none of them leads to `orbit_gen/app`; move code that needs `app.New()` into `cmd/server`. `orbit doctor` checks this too. See [The import rule](middleware.md#the-import-rule).

### An import cycle between two components' `Props`

**Symptom:** a build error showing the full import chain between two components.

**Cause:** two `Props` types refer to each other's components: `A.Props` has a field of type `B.Props`, and `B.Props` one of type `A.Props`. Components may otherwise import each other freely.

**Fix:** declare the shared data types in a package under `internal/` and use them in both `Props`, as the comment-thread example does with `posts.Comment`. See [Mutual recursion](components.md#mutual-recursion).

### `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`

**Cause:** a page's file or directory name mixes text and a parameter in one segment (`post-[id].orbit`, `[lang]-[version].orbit`). That isn't supported yet.

**Fix:** rename it to a whole-segment parameter (`[id].orbit`) and parse the value in the page, for example with `id, ok := strings.CutPrefix(params.ID, "post-")`. Route errors name the file, without a line and column. See [Dynamic routes](routing.md#dynamic-routes-with-param).

### `rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`

**Cause:** a rest parameter is followed by more segments (`[...path]/edit.orbit`). That isn't supported yet.

**Fix:** use a whole-segment `[path]` there, or restructure the routes so the rest parameter comes last. See [Catch-all routes](routing.md#catch-all-routes-with-rest).

### Two files produce the same route

**Symptom:** a build error that names both files.

**Cause:** for example `src/pages/blog.orbit` next to `src/pages/blog/index.orbit` (both are `/blog`), or `[slug].orbit` next to `[id].orbit` in one directory.

**Fix:** delete or rename one of them. A similar error names two files whose generated Go packages would collide, such as `my-card.orbit` and `my_card.orbit`; rename one. See [File-based routes](routing.md#file-based-routes) and [Package paths](project-structure.md#package-paths).

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

### `set:json only works on <script type="application/json">`

**Cause:** `set:json` was used on another element, or on a `<script>` without `type="application/json"`.

**Fix:** put it on an empty JSON script, and read it with `JSON.parse`:

```orbit
<script type="application/json" id="tags" set:json={props.Tags}></script>
```

A value `encoding/json` can't encode (a channel, a func, a cyclic structure) fails at render time, at the `set:json` position. For data that belongs to one element, use `data-*` attributes instead. See [Template syntax: `set:json`](reference/template-syntax.md#setjson).

### `class:list isn't supported; build the class string in the frontmatter (e.g. strings.Join)`

**Cause:** `class:list` is an Astro directive. Orbit reports it instead of writing it out as a plain attribute.

**Fix:** build the class string in the frontmatter and pass it to `class`:

```orbit
---
import "strings"

classes := []string{"card"}
if props.Featured {
    classes = append(classes, "featured")
}
---
<article class={strings.Join(classes, " ")}>…</article>
```

See [Combining and reserved namespaces](reference/template-syntax.md#combining-and-reserved-namespaces) and [Coming from Astro](coming-from-astro.md#templates).

### `define:vars isn't supported; pass data with data-* attributes or set:json`

**Cause:** `define:vars` is an Astro directive, and `<script>` and `<style>` content can't see Go values.

**Fix:** write per-element values into `data-*` attributes and read `el.dataset` in a custom element, or pass a block of data with `set:json` on a `<script type="application/json">`. See [Passing data to client scripts](components.md#passing-data-to-client-scripts).

### `the frontmatter always returns here, so the template below is never rendered`

**Cause:** the last statement of the frontmatter always returns (`return ctx.Redirect("/", 302)`, or an `if`/`else` that returns on every branch), and the template below it isn't empty.

**Fix:** remove the template, or make the return conditional. See [Returning errors, redirects and 404s](components.md#returning-errors-redirects-and-404s).

### `unexpected </ul>: <li> opened at 12:5 isn't closed`

**Cause:** Orbit doesn't auto-close elements the way browsers do, so `<p>`, `<li>`, `<td>` and `<option>` need their closing tags.

**Fix:** close the element (`<li>…</li>`) or self-close it (`<div />`). Void elements such as `<br>` and `<img>` take no closing tag. See [HTML and custom elements](reference/template-syntax.md#html-and-custom-elements).

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

### A URL answers 404

Check which case you're in:

- **No page matches.** A `[param]` matches exactly one segment, so `src/pages/blog/[slug].orbit` doesn't match `/blog/hello/like`. A `public/` file or `/_orbit/*` asset is looked up before pages.
- **The page returned `orbit.ErrNotFound`**, as `blog/[slug].orbit` does for an unknown slug.
- **The body is empty**: the project has no `src/pages/404.orbit`. `orbit new` scaffolds one.
- **You get your mux's plain-text 404** instead of `404.orbit`: with pages first (`site.Middleware(mux)`), give the mux `site.NotFound()` as its catch-all:

  ```go
  mux.Handle("/", site.NotFound()) // unmatched paths render 404.orbit
  ```

See [The 404 page](routing.md#the-404-page) and [Choosing precedence](server.md#choosing-precedence). For a 404 on a deferred component, see [below](#a-serverdefer-component-only-shows-its-fallback).

### A URL redirects with 308

**Cause:** pages have no trailing slash, so `/blog/hello/` gets a 308 to `/blog/hello`, keeping the query string (`/search/?q=go` → `/search?q=go`). The redirect is sent only when the slash-less path is a page; other paths reach your next handler untouched, and files are never redirected.

**Fix:** nothing is broken. Link to canonical URLs (`href="/blog/hello"`) to skip the extra round trip. Middleware that wraps Orbit runs before the redirect, so it sees both forms; compare paths with that in mind. This behavior isn't configurable. See [Trailing slashes](routing.md#trailing-slashes).

### A non-GET request to a file gets a 404 or 405

**Cause:** `/_orbit/*` assets and `public/` files are served for `GET` and `HEAD` only. Orbit passes any other method to your next handler, so the response is whatever that handler answers: a 404 from `site.Handler()`, or your own mux's answer. The only 405 Orbit sends itself is on the island endpoint `/_orbit/island/…`, for methods other than `GET`, `HEAD` and `POST`, with `Allow: GET, HEAD, POST`.

**Fix:** request files with `GET`. Handle form posts and uploads with a page (pages receive every method, in `ctx.Request.Method`) or with your own handler at a path that isn't a file. See [Which HTTP methods Orbit handles](routing.md#which-http-methods-orbit-handles).

### Visitors get a `500 Internal Server Error` page

**Cause:** a page or component returned an error, or panicked. In production, Orbit answers with a minimal built-in page that never shows the error, and logs the error with its `.orbit` position to stderr:

```text
orbit: GET /blog/hello: src/pages/blog/[slug].orbit:14:5: posts: connection refused
```

**Fix:** find the line in your server's stderr (your platform's logs) and fix the error at that position. `orbit dev` shows the same error in an overlay. Because responses are buffered, a failed render never sends half a page. In a stack trace from a binary built with `-trimpath`, frames from `.orbit` files keep their absolute paths. A custom `src/pages/500.orbit` is planned after the first release. See [Status codes and errors](rendering.md#status-codes-and-errors) and [Stack traces and `-trimpath`](reference/cli.md#stack-traces-and--trimpath).

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
- **Startup errors**: the new server built but exited, for example because a database is unreachable. The overlay shows its stderr.
- **Render errors**: a page returned an error or panicked; your server's own dev overlay shows it with its `file:line`.

**Fix:** fix the error and save. The overlay listens for live-reload events, so the page reloads by itself. The same messages appear in the terminal running `orbit dev`. See [Dev server: Errors](dev-server.md#errors).

### Port 4321 is already in use

**Cause:** another process is listening there. `orbit dev`, `orbit preview` and a built `dist/server` all default to port 4321.

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

**Cause:** when you edit a `.go` package that every component's `Props` depends on (such as `internal/posts`), `go build` recompiles every generated package. On a 200-component project this took about 0.75 s on 16 threads and about 1.3 s on 4 cores, against about 0.24 s for a component edit. Edits to only a `<style>` or `<script>` need no Go rebuild at all.

**Fix:** this is `go build` time, and dev builds already skip DWARF (`-ldflags=-w`) to link faster. Edits to packages fewer components import rebuild less. Also check your server: each Go rebuild restarts it, so a slow startup adds to every save, and a server that runs its own `http.Server` without handling `SIGINT` is only killed after 5 seconds. `orbit.ListenAndServe` handles `SIGINT` for you. See [How long a rebuild takes](dev-server.md#how-long-a-rebuild-takes).

## Editor

### No completion in `.orbit` files

Go through these in order:

1. Run `orbit doctor` and fix the gopls line. If only template completion works (tags, props, slots, directives) and Go completion doesn't, see [Go features are disabled in the editor](#go-features-are-disabled-in-the-editor-gopls-is-too-old).
2. **VS Code**: install the extension with `code --install-extension bertrandkhe.orbit`. It runs the `orbit` on your `PATH`; if it isn't there, set `orbit.path`.
3. **Neovim**: add the `vim.filetype.add({ extension = { orbit = 'orbit' } })` line and `vim.lsp.enable('orbit')` from [Editor setup](editor-setup.md#neovim).
4. Open the project at its root: the language server looks for the directory with `go.mod` and `src/`.

TypeScript inside `<script>` gets syntax highlighting only, with no completion or diagnostics; run `orbit check` to type-check it. See [What completion covers](editor-setup.md#what-completion-covers).

### The Go extension reports errors in `orbit_gen/`

**Cause:** the VS Code Go extension runs its own gopls, which sees the generated files in `orbit_gen/` and reports their errors there. Orbit doesn't suppress them.

**Fix:** they're usually the same errors Orbit already shows at the right place in your `.orbit` file, so fix them there. Errors that appear only in `orbit_gen/` often mean the generated code is stale: run `orbit generate`, or keep `orbit dev` running. Don't hide them with a gopls `directoryFilters` entry such as `"-orbit_gen"`: Orbit passes your editor's `gopls` settings to its own gopls too, and that one needs `orbit_gen/` to give you Go features in `.orbit` files. See [The Go extension's errors in `orbit_gen/`](editor-setup.md#the-go-extensions-errors-in-orbit_gen).

### Rename and references wait after the editor starts

**Cause:** until gopls has loaded every component, references and rename would miss uses, so Orbit holds them; your editor shows a progress indicator meanwhile. Completion, hover and diagnostics work right away. Without `orbit_gen/` on disk, gopls loads components one at a time, which is slower: about 2.5 s for 100 components, against about 0.8 s with `orbit_gen/` present.

**Fix:** click **Run orbit generate for faster startup** when your editor offers it, or run `orbit generate` yourself (or keep `orbit dev` running). If a rename is refused with a message, it would have changed generated code that doesn't correspond to anything in your files. See [The "Run orbit generate" nudge](editor-setup.md#the-run-orbit-generate-nudge) and [Rename](editor-setup.md#rename).

## See also

- [Coming from Astro](coming-from-astro.md): what maps to what, and what isn't supported
- [CLI](reference/cli.md): `orbit doctor`, error output and exit codes
- [Components](components.md): frontmatter, props and the compile errors around them
- [Template syntax](reference/template-syntax.md): every directive and its errors
- [Lazy loading](lazy-loading.md): `server:defer`, the island key and island status codes
- [Dev server](dev-server.md): rebuilds, live reload and the overlay
- [Editor setup](editor-setup.md): gopls, VS Code and Neovim
