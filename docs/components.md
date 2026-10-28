# Components

A component is a `.orbit` file: Go frontmatter that prepares data, followed by an HTML template that renders it.

## A first component

A `Card` that shows one blog post:

```orbit file=src/components/Card.orbit
---
import "example.com/blog/internal/posts"

type Props struct {
	Post     posts.Post
	Featured bool
}

func href(p posts.Post) string { return "/blog/" + p.Slug }

url := href(props.Post)
---
<article class="card" data-featured={props.Featured}>
  <h2><a href={url}>{props.Post.Title}</a></h2>
  <time>{props.Post.Date.Format("2 Jan 2006")}</time>
  <slot />
</article>
<style>
  .card {
    border: 1px solid #ccc;
    padding: 1rem;
  }
</style>
```

A page uses it like an HTML tag:

```orbit file=src/pages/index.orbit
---
import (
	Card "../components/Card.orbit"
	Base "../layouts/Base.orbit"
	"example.com/blog/internal/posts"
)

list, err := posts.All()
if err != nil {
	return err
}
---
<Base title="Home">
  <h1>Latest posts</h1>
  {if len(list) == 0}
    <p>No posts yet.</p>
  {else}
    {for i, p := range list}
      <Card post={p} featured={i == 0}>
        <p>{p.Summary}</p>
      </Card>
    {/for}
  {/if}
  <p slot="footer">Subscribe to the RSS feed.</p>
</Base>
```

The `posts` package is plain Go in your module:

<details>
<summary><code>internal/posts/posts.go</code>, the tiny post store used by the examples on this page</summary>

```go file=internal/posts/posts.go
// Package posts holds the blog's content.
package posts

import (
	"errors"
	"time"
)

// ErrNotFound is returned by Find for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

// Post is one blog post.
type Post struct {
	Slug    string
	Title   string
	Summary string
	Date    time.Time
	Body    string // HTML produced by our own Markdown step, so it's trusted
}

// Comment is a reader comment. Replies nest to any depth.
type Comment struct {
	Author  string
	Text    string
	Replies []Comment
}

var all = []Post{
	{
		Slug:    "hello",
		Title:   "Hello, Orbit",
		Summary: "Why this blog exists.",
		Date:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Body:    "<p>First post.</p>",
	},
}

// All returns every post, newest first.
func All() ([]Post, error) { return all, nil }

// Find returns the post with the given slug, or ErrNotFound.
func Find(slug string) (Post, error) {
	for _, p := range all {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Post{}, ErrNotFound
}

// Comments returns the comment tree of a post.
func Comments(slug string) ([]Comment, error) {
	if slug != "hello" {
		return nil, nil
	}
	return []Comment{
		{Author: "Ada", Text: "Nice!", Replies: []Comment{
			{Author: "Bertrand", Text: "Thanks!", Replies: []Comment{
				{Author: "Ada", Text: "Looking forward to the next one."},
			}},
		}},
	}, nil
}
```

</details>

A `.orbit` file has three parts:

- **Frontmatter**, between the `---` lines: ordinary Go. It declares the component's `Props`, imports other packages and components, and computes values for the template.
- **Template**: HTML with `{expressions}`, control blocks such as `{if}` and `{for}`, and other components as tags.
- **`<style>` and `<script>`**: scoped CSS and client-side TypeScript, extracted and bundled by Orbit. See [Styles and scripts](styles-and-scripts.md).

The frontmatter is optional. A file without it is pure template.

Every page renders on the server for each request; a component renders as part of the page that uses it.

### Coming from Astro

| Astro | Orbit |
|---|---|
| `Astro.props` | `props` (a typed `Props` struct) |
| `interface Props { … }` | `type Props struct { … }` |
| `const { variant, ...rest } = Astro.props`, `<button {...rest}>` | An `Attrs orbit.RestAttrs` field, `<button {...props.Attrs}>`: see [Passing HTML attributes through](#passing-html-attributes-through) |
| `import Card from "./Card.astro"` | `Card "./Card.orbit"` inside `import` |
| `{cond && <p/>}`, `{list.map(…)}` | `{if cond}…{/if}`, `{for _, x := range list}…{/for}` |
| `Astro.slots.has("x")` | `slots.Has("x")` |
| `<Astro.self />` | `<Self />` |
| `Astro.locals` | `locals` (a typed struct) |
| `Astro.redirect("/login")` | `return ctx.Redirect("/login", 302)` |
| `<Comments server:defer />` | The same: see [Lazy loading](lazy-loading.md) |
| `<Counter client:visible />` on a React or Svelte component | `client:visible` on an `.orbit` component, which loads its `<script>` lazily |
| `import Chart from "./Chart.tsx"` | Not supported: no framework components |

## The frontmatter

The frontmatter is Go source. Orbit splits it into two parts:

- **Hoisted declarations**: a top-level statement that starts with `import`, `type` or `const`, or a `func` followed by a name (`func href(…)`) or by a receiver and a name (`func (p Props) Href() …`), is moved to package level.
- **The render body**: everything else runs, in order, every time the component renders. A top-level `var` is a local variable of the render body, not a package variable, and so is a function literal (`f := func() { … }` or `func() { … }()`).

In `Card.orbit` above, the `import`, `type Props` and `func href` are hoisted, and `url := href(props.Post)` is the render body.

For package-level state (caches, clients, configuration), use an ordinary Go package under `internal/` and import it.

### Values computed once

Because the render body runs on every render, a table built there, `var` included, is rebuilt (and allocated) every time the component renders, as code in an Astro frontmatter is. Frontmatter `var` declarations aren't hoisted, so a value you want built once goes in one of these places:

- a **`const`**, for strings and numbers: hoisted, and free at runtime;
- a **hoisted named `func`** with a `switch`, for a small fixed lookup: compiled code, nothing allocated;
- a **package-level `var` in a Go package** of your own, for anything else (maps, slices, parsed templates, regular expressions): built once when the program starts.

```orbit file=src/components/Icon.orbit
---
import "example.com/blog/internal/icons"

type Props struct{ Name string }

const size = "24"

// Hoisted: compiled once, no allocation per render.
func label(name string) string {
	switch name {
	case "home":
		return "Home"
	case "rss":
		return "RSS feed"
	}
	return name
}

// Wrong: rebuilt on every render.
// paths := map[string]string{"home": "M3 12…", "rss": "M4 11…"}
---
<svg width={size} height={size} aria-label={label(props.Name)}><path d={icons.Paths[props.Name]} /></svg>
```

```go file=internal/icons/icons.go
// Package icons holds the SVG paths, built once at startup.
package icons

var Paths = map[string]string{
	"home": "M3 12l9-9 9 9",
	"rss":  "M4 11a9 9 0 0 1 9 9",
}
```

### What's available in the render body

These names are provided in the render body and the template, without declaring or importing anything:

| Name | Type | What it is |
|---|---|---|
| `props` | `Props` | The values the parent passed. |
| `ctx` | `*orbit.Ctx` | The request: `ctx.Request`, `ctx.URL`, `ctx.Site`, plus `ctx.Param`, `ctx.SetStatus`, `ctx.Header`, `ctx.Redirect` and `ctx.Cache()` ([Caching](caching.md)). It isn't a `context.Context`: pass `ctx.Request.Context()` to database and API calls (or, inside an `orbit.All` load, the `rc` it gets; see [Loading data](data-loading.md)). See [Rendering](rendering.md) and [Runtime API](reference/runtime-api.md). |
| `slots` | `orbit.Slots` | The children the parent passed. See [Slots](#slots). |
| `params` | `Params` | **Pages only.** The route params, typed: `params.Slug` for `src/pages/blog/[slug].orbit`. Orbit generates the `Params` struct from the file path. Components read a route param with `ctx.Param("slug")` instead. See [Route params](routing.md#route-params-params). |
| `locals` | `*locals.Locals` | Request-scoped data, typed by your `type Locals struct` in the package `src/locals`. Without one, it's `*orbit.NoLocals`. See [Middleware and locals](middleware.md). |
| `orbit` | package | The runtime package `github.com/orbit-z/orbit`. You can use it without importing it; an explicit import of it under the name `orbit` is merged with the implicit one. |

A page and all its components share one `ctx` and one `locals`, with two exceptions. A component rendered with `server:defer` renders in its own request, with its own `ctx`; see [Lazy loading](lazy-loading.md). A component rendered with `server:concurrent` gets its own `ctx`, a copy of the page's (same request, params and `locals`) whose status and headers start empty and whose status, headers and cache settings are merged into the page's when the page joins it; see [Slow components](rendering.md#slow-components). `ctx` is pooled and reused for later requests, so don't keep it, or start a goroutine that uses it, after the render returns.

Hoisted declarations live at package level, so they **can't see** `props`, `ctx`, `slots`, `locals` or `params`. Pass what a helper needs as arguments, as `href(props.Post)` does.

### Redeclaring and shadowing

You can't redeclare `props`, `ctx`, `slots`, `locals` or `orbit`, or `params` in a page, at the top level of the body (with `:=` or `var`); in a component, `params` is an ordinary name:

```orbit file=src/components/Card.orbit expect-error
---
type Props struct {
	Title string
}

props := Props{Title: "Untitled"}
---
<h2>{props.Title}</h2>
```

```text
src/components/Card.orbit:6:1: props is provided by Orbit and can't be redeclared here
```

Shadowing them in a nested block is allowed, as in Go. Orbit's own generated code never uses these names, so a shadow can't break it:

```orbit
---
import Card "../components/Card.orbit"

type Props struct {
	Variants []Card.Props
}

variants := props.Variants
---
{for _, props := range variants}
  <Card post={props.Post} featured={props.Featured} />
{/for}
```

Names that start with `orbit__` or `Orbit__` are reserved, and a hoisted declaration named `Render` is rejected.

### Unused variables

Unlike plain Go, a variable declared at the top level of the body and never used isn't an error, so you can compute a value and stop using it without editing the frontmatter. Unused **imports** are still an error: Go's own for a Go package, and Orbit's for a component (see [Importing components](#importing-components)).

### No `func init`

A hoisted `func init()` is rejected:

```orbit file=src/components/Card.orbit expect-error
---
func init() {
	println("Card loaded")
}
---
<article class="card"></article>
```

```text
src/components/Card.orbit:2:6: func init isn't allowed in a component; use a package under internal/ for initialization
```

Put initialization in a package under `internal/`. Rendering a component during package initialization isn't supported.

### Returning errors, redirects and 404s

The render body is the inside of a function that returns `error`, so it can return early:

| Return | Effect |
|---|---|
| `return err` | A 500 response. In `orbit dev`, an error overlay with the `.orbit` position. |
| `return orbit.ErrNotFound` | A 404, rendered with `src/pages/404.orbit`. |
| `return ctx.Redirect("/login", 302)` | A redirect to `/login` with status 302. |
| `return nil` | The component stops here and renders nothing more; the rest of the page renders as usual. |

An error returned by a component stops the whole page, exactly as if the page itself had returned it. Status codes, headers and errors are covered in [Rendering](rendering.md).

`return nil` is how a component renders nothing. This one shows a notice only when it has text:

```orbit file=src/components/Notice.orbit
---
type Props struct {
	Text string
}

if props.Text == "" {
	return nil
}
---
<p class="notice">{props.Text}</p>
```

The value returned must be an `error` (or `nil`); a component can't return HTML or a string. To render text, put it in the template.

When the body ends in a statement that always returns, the template must be empty. This page only redirects:

```orbit file=src/pages/latest.orbit
---
import "example.com/blog/internal/posts"

list, err := posts.All()
if err != nil {
	return err
}
if len(list) == 0 {
	return orbit.ErrNotFound
}
return ctx.Redirect("/blog/"+list[0].Slug, 302)
---
```

A statement "always returns" in the Go spec's sense: a `return`, or an `if`/`else` that returns on every branch. If such a statement is last and the template below it has anything other than whitespace, Orbit reports it at the statement (won't compile):

```orbit file=src/pages/old.orbit expect-error
---
return ctx.Redirect("/", 302)
---
<p>You won't see this.</p>
```

```text
src/pages/old.orbit:2:1: the frontmatter always returns here, so the template below is never rendered
```

### Importing components

Import a component with an **alias** and a path **relative to the current file**, in the same `import` block as your Go packages:

```orbit
---
import (
	Card "../components/Card.orbit"
	Base "../layouts/Base.orbit"
	"example.com/blog/internal/posts"
	"strings"
)
---
```

The alias is required: it's the tag name you use in the template (`<Card>`) and the Go package name for the component's exported names (`Card.Props`). It must start with an uppercase letter, because a lowercase tag such as `<card>` is always an HTML element:

```orbit file=src/pages/index.orbit expect-error
---
import card "../components/Card.orbit"
---
<card />
```

```text
src/pages/index.orbit:2:8: component alias card must start with an uppercase letter (rename it to Card)
```

An import without an alias (`"./Card.orbit"`), or with `_`, is an error suggesting one, named after the file; a dot import (`. "./Card.orbit"`) is rejected too:

```orbit file=src/pages/index.orbit expect-error
---
import "../components/Card.orbit"
---
<Card />
```

```text
src/pages/index.orbit:2:8: a component import needs an alias: Card "../components/Card.orbit"
```

A component you import but never use, neither as a tag nor in Go code (`Card.Props`), is an error at the alias, like an unused Go import:

```orbit file=src/pages/index.orbit expect-error
---
import Card "../components/Card.orbit"
---
<h1>Latest posts</h1>
```

```text
src/pages/index.orbit:2:8: component Card is imported but not used; remove the import
```

The path must stay inside `src/` (`component path ../../X.orbit is outside src/`).

#### Root-relative imports

A path that starts with `/` is resolved from the project root, as in Astro and Vite, so the same import works from any file:

```orbit file=src/pages/rooted.orbit
---
import (
	Base "/src/layouts/Base.orbit"
)
---
<Base title="Rooted">
  <h1>Hello</h1>
</Base>
```

The same rules apply as for a relative path: the file must exist and be inside `src/` (`"/lib/X.orbit"` is outside it), and a `.tsx`, `.jsx`, `.svelte` or `.vue` file is an error. A Markdown `layout` and a Markdown import can be root-relative too.

#### Import aliases

Instead of a relative path, you can use an alias from `compilerOptions.paths` in your `tsconfig.json`, as in Astro. `orbit new` sets up `@/` for `src/`:

```json
{
  "compilerOptions": {
    "paths": { "@/*": ["./src/*"] }
  }
}
```

```orbit file=src/pages/aliased.orbit
---
import (
	Base "@/layouts/Base.orbit"
)
---
<Base title="Aliased">
  <h1>Hello</h1>
</Base>
```

Aliases follow TypeScript's rules: a `*` in the key matches the rest of the path, the most specific key (the longest prefix) wins, and when an entry lists several targets, the first one that exists is used. Paths are relative to `baseUrl` when it's set, otherwise to the `tsconfig.json`. A path that no key matches resolves against `baseUrl`, if there is one. `extends` is followed for local files and for packages in `node_modules/`. Your `<script>` imports use the same aliases, because esbuild reads the same file.

A path that doesn't start with `./`, `../` or `/` and ends in `.orbit` or `.md` is an alias, so it must match one, and the file it points to must be inside `src/`:

```orbit file=src/pages/aliased.orbit expect-error
---
import (
	Base "~/layouts/Base.orbit"
)
---
<Base title="Aliased" />
```

```text
src/pages/aliased.orbit:3:7: no tsconfig.json paths alias matches "~/layouts/Base.orbit"; use a relative or root-relative path ("/src/…"), or add "~/*": ["./src/*"] to compilerOptions.paths
```

A component imported under several spellings (`"@/components/Card.orbit"`, `"/src/components/Card.orbit"` and `"../components/Card.orbit"`) is the same component. Go package paths such as `"strings"` never go through aliases, since they don't end in `.orbit` or `.md`.

Components can import each other in cycles; see [Mutual recursion](#mutual-recursion).

#### Importing Markdown

A Markdown file is imported the same way, with an alias, and renders as a component that takes no props (see [Markdown](markdown.md)):

```orbit
---
import (
	Intro "../content/intro.md"
)
---
<section class="prose">
  <Intro />
</section>
<p>{len(Intro.Headings)} sections, by {Intro.Frontmatter.Author}</p>
```

The import also gives the file's frontmatter as a typed struct (`Intro.Frontmatter`), its headings (`Intro.Headings`, for a table of contents) and, for a Markdown page, its URL (`Intro.URL`). The Markdown is converted to HTML when Orbit generates code, so `<Intro />` costs the same as static template HTML.

### No framework components

Orbit has no React, Preact, Svelte, Vue or Solid components, by design. Client-side code is a vanilla `<script>` in an `.orbit` component (see [Styles and scripts](styles-and-scripts.md)). Importing a framework component is an error at the import path (won't compile):

```orbit file=src/pages/index.orbit expect-error
---
import Chart "./Chart.tsx"
---
```

```text
src/pages/index.orbit:2:14: framework components aren't supported; use a .orbit component with a <script>
```

The same goes for `.jsx`, `.svelte` and `.vue` files. Any other relative path that doesn't end in `.orbit` or `.md`, such as `"./util.ts"`, is an error too:

```orbit file=src/pages/index.orbit expect-error
---
import Util "./util.ts"
---
```

```text
src/pages/index.orbit:2:13: only .orbit and .md files can be imported by relative path; import client modules from a <script>
```

Go packages are imported by their import paths, as usual.

### Pages

A page is a component under `src/pages/`. It gets one extra name, `params`: its route params as a generated struct, such as `params.Slug` for `src/pages/blog/[slug].orbit`. The rest of the request is in `ctx`, such as `ctx.Request.URL.Query()`. See [Route params](routing.md#route-params-params).

Pages never receive props, so declaring `type Props` in a page is an error at the declaration (won't compile):

```orbit file=src/pages/blog/[slug].orbit expect-error
---
type Props struct {
	Slug string
}
---
```

```text
src/pages/blog/[slug].orbit:2:6: pages don't receive props; move this to a component or read params
```

For the same reason, a page can't declare `type Params`: Orbit generates it from the route (`Params is generated from the page's route; rename this type`).

## Props

`type Props struct` declares what a component accepts. The parent sets fields with attributes:

```orbit
<Card post={p} featured />
```

- `post={p}` sets `Post` to the Go value `p`. The Go compiler checks its type.
- A bare attribute (`featured`) sets the field to `true`.
- A quoted value (`label="New"`) is a Go string literal, so it only fits a `string` field. Use `count={3}` for numbers.
- `{post}` is shorthand for `post={post}`.

Attribute names match fields ignoring case, `-` and `_`: `featured`, `Featured` and `fea-tured` all set `Featured`; `id` sets `ID` and `is-open` sets `IsOpen`. The full rules are in [Template syntax](reference/template-syntax.md#component-attributes-and-props).

A field you don't set gets Go's zero value; there are no required props. For a default, check the zero value in the frontmatter:

```orbit
---
type Props struct {
	Title string
}

title := props.Title
if title == "" {
	title = "Untitled"
}
---
<h2>{title}</h2>
```

A component without `type Props` gets an empty `Props struct{}` and accepts no attributes.

### Unknown props

An attribute that matches no field is an error at the attribute, in your spelling, listing the attributes that would work:

```orbit file=src/pages/index.orbit expect-error
---
import (
	Card "../components/Card.orbit"
	"example.com/blog/internal/posts"
)

post, _ := posts.Find("hello")
---
<Card psot={post} />
```

```text
src/pages/index.orbit:9:7: unknown prop "psot" on <Card> (props: post, featured; add an orbit.RestAttrs field to pass HTML attributes through)
```

The list spells each field in lower camel case, as Astro props are usually written: a field `IsOpen` is listed as `isOpen`, `ID` as `id`. A component that should accept any HTML attribute (`name`, `aria-label`, `data-*`, …) declares an `orbit.RestAttrs` field instead: see [Passing HTML attributes through](#passing-html-attributes-through).

A value of the wrong type is a regular Go error, also at the `.orbit` position:

```orbit file=src/pages/index.orbit expect-error
---
import (
	Card "../components/Card.orbit"
	"example.com/blog/internal/posts"
)

post, _ := posts.Find("hello")
---
<Card post={post} featured="yes" />
```

```text
src/pages/index.orbit:9:28: cannot use "yes" (untyped string constant) as bool value in struct literal
```

### Fields must be exported

Every `Props` field must be exported (start with an uppercase letter), because the parent's code sets it from another package. An unexported field is an error naming it:

```orbit file=src/components/Card.orbit expect-error
---
import "example.com/blog/internal/posts"

type Props struct {
	post posts.Post
}
---
<h2>{props.post.Title}</h2>
```

```text
src/components/Card.orbit:5:2: Props field post must be exported, since a parent sets it (rename it to Post)
```

### Optional props

Astro's optional prop, `rel?: string`, leaves the attribute out when the caller doesn't pass it. In Orbit, give the field the type **`orbit.Opt[T]`**, a [typed optional value](reference/template-syntax.md#optional-values-orbitopt): its zero value is "none", and none omits the attribute where the component writes it.

```orbit file=src/components/Link.orbit
---
type Props struct {
	Href  string
	Rel   orbit.Opt[string] // unset: no rel attribute
	Title orbit.Opt[string]
}
---
<a href={props.Href} rel={props.Rel} title={props.Title}><slot /></a>
```

```orbit file=src/pages/links.orbit
---
import "strings"

import Link "../components/Link.orbit"

url, summary := "https://go.dev/blog", "News from the Go team"
ext := strings.HasPrefix(url, "http")
---
<Link href={url} rel={orbit.If(ext, "noopener")}>Go blog</Link>
<Link href="/about" rel="author">About</Link>
<Link href="/x" title={summary}>X</Link>
<Link href="/y">Y</Link>
```

renders `<a href="https://go.dev/blog" rel="noopener">Go blog</a><a href="/about" rel="author">About</a><a href="/x" title="News from the Go team">X</a><a href="/y">Y</a>`.

- **What a caller can pass**: a static value (`rel="author"`), a bare attribute (for an `orbit.Opt[bool]`) or an expression of type `T` (`title={summary}`) is set; an expression that's already an `orbit.Opt[T]` (`orbit.If`, `orbit.NonZero`, a variable, another component's prop) is passed as it is, so `rel={orbit.If(ext, "noopener")}` is unset when `ext` is false. Not passing the prop leaves it unset.
- **Any other value** is an error at the value, as for a prop of type `T`:

```orbit file=src/pages/links.orbit expect-error at=10
<Link href="/z" rel={len(url)}>Z</Link>
```

```text
src/pages/links.orbit:10:22: prop rel of <Link> is orbit.Opt[string]; got int
```

- **Defaults**: `props.Rel.Or("nofollow")` is the value or a default, Astro's `rel ?? "nofollow"`. For a plain `string` prop where `""` means unset, the standard library's `cmp.Or(props.Rel, "nofollow")` does the same.
- **In Go code**, read it with `v, ok := props.Rel.Get()` or `props.Rel.IsSome()`, and build one with `orbit.Some("x")`, `orbit.None[string]()` or `orbit.NonZero(s)`. In a `server:defer` component's props, none is sent as JSON `null`.
- Orbit recognizes the field by its type as written in `Props`, `orbit.Opt[…]`. A field whose type is an alias declared in a Go package (`type MaybeRel = orbit.Opt[string]`) takes only `orbit.Opt[string]` values, as Go's assignment rule says.

### Passing HTML attributes through

A component that wraps an HTML element, such as a button or an input, usually lets the caller add any attribute to it: `name`, `form`, `disabled`, `aria-*`, `data-*`. In Astro, that's `const { variant, ...rest } = Astro.props` and `<button {...rest}>`. In Orbit, declare a `Props` field of type **`orbit.RestAttrs`**: it collects every attribute of the call site that matches no other field, and `{...props.Attrs}` writes them on an element.

```orbit file=src/components/Button.orbit
---
type Props struct {
	Variant string
	// Type is a prop, not a passed attribute, because the component sets
	// the attribute itself; "button" when unset.
	Type orbit.Opt[string]
	// Attrs holds every other attribute of the call site.
	Attrs orbit.RestAttrs
}
---
<button {...props.Attrs} type={props.Type.Or("button")} class:list={"btn", "btn-" + props.Variant}><slot /></button>
```

```orbit file=src/pages/settings.orbit
---
import Button "../components/Button.orbit"

shelf := "s-81"
---
<form method="post">
  <Button variant="quiet" name="shelf" data-shelf={shelf} required class="mt-2">Save</Button>
</form>
<style>
  .mt-2 {
    margin-top: 0.5rem;
  }
</style>
```

renders (with the page's scope attribute `data-o-…`):

```html
<form method="post" data-o-…>
  <button name="shelf" data-shelf="s-81" required data-o-… type="button" class="btn btn-quiet mt-2">Save</button>
</form>
```

- **What's collected**: `variant` sets `Variant`, as usual. `name`, `data-shelf` and `required` match no field, so they go into `Attrs`, in the order you wrote them, with their names as written. Static values (`name="shelf"`) cost nothing at render time; expression values (`data-shelf={shelf}`) follow the same [rules as on an element](reference/template-syntax.md#attribute-values-on-html-elements): `true` is bare, `false` and `nil` are omitted, URL attributes are filtered.
- **`class` is merged**: a passed `class` (or `class:list`) joins the element's own `class` or `class:list` instead of being written twice (`class="btn btn-quiet mt-2"`). On an element without a class, the spread writes `class="mt-2"`. If the component declares a `Class` prop, `class` sets that prop instead.
- **The caller's scoped styles apply**: when the calling file has a scoped `<style>`, its scope attribute is passed along too, so the page's `.mt-2` rule matches the button, as in Astro.
- **Declare a prop for any attribute the component sets itself.** If both the call site and the element write `type`, both are written and the browser keeps the first. Declaring `Type` makes `type="submit"` set the prop, and the component decides.
- **Spread it on any number of elements**: `{...props.Attrs}` can appear on several elements. To render an `<a>` when an `href` is passed and a `<button>` otherwise, use one [dynamic tag](#one-element-or-another-dynamic-tags). `props.Attrs.Has("href")` and `props.Attrs.Get("href")` read a passed attribute.
- **Forwarding**: a wrapper passes its own pass-through attributes on with a spread on the inner component, `<Input {...props.Attrs} />`; attributes written on the tag win over the spread's. A spread of an `orbit.RestAttrs`, an `orbit.Attrs` or an `orbit.AttrList` (whose order is kept) works only when the component has an `orbit.RestAttrs` field; a spread of a struct sets props instead ([Spreading props](#spreading-props)).
- **Typos aren't caught**: on a component with an `orbit.RestAttrs` field, a misspelled prop (`varient="quiet"`) becomes an HTML attribute instead of an error, as in Astro. Declared props come first in editor completion.

A `Props` takes at most one `orbit.RestAttrs` field, and it can't have embedded fields (Orbit must be able to tell props from pass-through attributes). From Go code, such as a test, build the field with `orbit.RestOf(orbit.Attrs{"name": "shelf"})`, or `orbit.RestOf(orbit.AttrList{orbit.A("name", "shelf")})` to keep an order. The [template syntax reference](reference/template-syntax.md#pass-through-attributes) has the details and the error messages.

### Spreading props

Astro's `<Card {...card} />` passes an object's keys as props. In Orbit, a spread of a **struct** (or a pointer to one) on a component tag sets props: each field sets the prop of the **same Go name**. Which spread is which is decided by the value's type: a struct sets props, an `orbit.Attrs`, `orbit.AttrList` or `orbit.RestAttrs` passes [HTML attributes through](#passing-html-attributes-through).

A Go helper can build all of a component's props. Here the component's `Props` is the helper's type:

```go file=internal/teaser/teaser.go
// Package teaser builds the props of a post teaser.
package teaser

import (
	"github.com/orbit-z/orbit"

	"example.com/blog/internal/posts"
)

// Props is what a teaser shows.
type Props struct {
	Href, Title, Summary string
	Badge                orbit.Opt[string]
}

// Of returns the teaser of p; the first post gets a badge.
func Of(p posts.Post, first bool) Props {
	t := Props{Href: "/blog/" + p.Slug, Title: p.Title, Summary: p.Summary}
	if first {
		t.Badge = orbit.Some("new")
	}
	return t
}
```

```orbit file=src/components/Teaser.orbit
---
import "example.com/blog/internal/teaser"

type Props = teaser.Props
---
<article>
  <h2><a href={props.Href}>{props.Title}</a> <small>{props.Badge}</small></h2>
  <p>{props.Summary}</p>
</article>
```

```orbit file=src/pages/teasers.orbit
---
import (
	Share "../components/Share.orbit"
	Teaser "../components/Teaser.orbit"
	"example.com/blog/internal/posts"
	"example.com/blog/internal/teaser"
)

list, err := posts.All()
if err != nil {
	return err
}
share := Share.Props{URL: "/blog", Title: "The blog"}
---
{for i, p := range list}
  <Teaser {...teaser.Of(p, i == 0)} />
{/for}
<Share {...share} placement="top" />
<Share {...share} placement="bottom" />
```

The second shape builds part of the callee's own `Props`, reachable through its import alias (`Share.Props`), and sets the rest on the tag:

```orbit file=src/components/Share.orbit
---
type Props struct {
	URL, Title string
	Placement  string
}
---
<nav class={"share share-" + props.Placement}><a href={props.URL}>Share “{props.Title}”</a></nav>
```

- **Order**: attributes and spreads apply in source order, so an attribute **after** a spread overrides it (`placement="top"` above). A Go struct always sets every field, so a value that would always be overwritten is an error instead of dead code: an attribute **before** a spread that sets the same prop, or two spreads that set the same prop. Two spreads of disjoint structs are fine: `<Cached {...ref} {...options} />`.
- **Missing and extra fields**: a prop that nothing sets keeps its zero value, as always. A field of the struct that isn't a prop is an error listing it; spread a struct whose fields are all props, or pass the values one by one.
- **Types**: each field must be assignable to its prop, with one widening: a `T` field sets an `orbit.Opt[T]` prop to `orbit.Some(v)`, as an attribute of type `T` would. An `orbit.Opt[T]` field can't set a `T` prop (use `Or` in the helper).
- **Which fields**: the struct's exported fields, including those promoted through embedded structs, as `encoding/json` sees them. The callee's `Props` can embed structs too; a prop promoted through an embedded **pointer** can't be set by a spread (it could be nil).
- **Pointers**: a nil pointer spreads nothing.
- **Forwarding a whole `Props`**: `<Inner {...props} />` passes a wrapper's props on when the wrapper declares the same fields (or `type Props = Inner.Props`). When the struct has the callee's `orbit.RestAttrs` field, it's merged with the attributes passed through on the tag, which win.
- **Not set by a spread**: slots (they come from children) and the `class` merge: a spread's `Class` field sets a `Class` prop like any field.
- **Cost**: none at render time. The build assigns the fields one by one (one struct copy when the value's type is the callee's `Props`), with no allocation and no reflection.

A struct with a field that isn't a prop:

```orbit file=src/pages/hello.orbit expect-error
---
import (
	Teaser "../components/Teaser.orbit"
	"example.com/blog/internal/posts"
)

p, _ := posts.Find("hello")
---
<Teaser {...p} />
```

```text
src/pages/hello.orbit:9:9: {...p} on <Teaser>: posts.Post has fields that aren't props of Teaser (Slug, Date, Body); spread a struct whose fields are all props, or pass them one by one (props: href, title, summary, badge)
```

The [template syntax reference](reference/template-syntax.md#props-spreads) lists every error. These checks need the types, so in the editor they appear a moment after you stop typing.

### One element or another: dynamic tags

A capitalized variable used as a tag renders the element it names, as Astro's `const Tag = …` does. A link that looks like a button, or a heading whose level is a prop, then needs one copy of its markup:

```orbit file=src/components/Action.orbit
---
type Props struct {
	Variant string
	Type    orbit.Opt[string]
	Attrs   orbit.RestAttrs
}

link := props.Attrs.Has("href")
Tag := orbit.Cond(link, "a", "button")
---
<Tag {...props.Attrs} type={orbit.If(!link, props.Type.Or("button"))} class:list={"btn", "btn-" + props.Variant}>
  <slot />
</Tag>
```

`<Action variant="quiet" href="/new">New</Action>` renders an `<a href="/new" class="btn btn-quiet">`, and `<Action variant="quiet">Save</Action>` a `<button type="button" class="btn btn-quiet">`. A heading works the same way: `Heading := "h" + strconv.Itoa(props.Level)`, then `<Heading class="title">{props.Text}</Heading>`.

**Optional wrappers**: `orbit.Fragment` as the value renders no element, only the children:

```orbit file=src/components/Choice.orbit
---
type Props struct {
	Label   string
	Wrapped bool
}

Wrap := orbit.Cond(props.Wrapped, "div", orbit.Fragment)
---
<Wrap class="choice">
  <label><input type="checkbox"> {props.Label}</label>
</Wrap>
```

With `wrapped`, the `<label>` is inside a `<div class="choice">`; without, it's on its own.

The name is a Go `string`, checked when the page renders: an empty string, `script`, `style` and a few other special elements, and an `img` or other void element with children are render errors. The [template syntax reference](reference/template-syntax.md#dynamic-tags) has the rules. A variable can't hold a component; to pick one, use `{switch}`.

### Types used by Props

`Props` and everything it depends on (types, constants and functions declared in the same frontmatter, and methods on them) live in the component's **declaration package**, which other components import (see [How it compiles](#how-it-compiles)). The rest of the component can only see the *exported* names there. So if `Props` uses a type that the body or template also uses, it must be exported:

```orbit file=src/components/List.orbit
---
type Item struct {
	Name string
}

type Props struct {
	Items []Item
}
---
<ul>
  {for _, it := range props.Items}
    <li>{it.Name}</li>
  {/for}
</ul>
```

A parent builds the value with the alias: `<List items={[]List.Item{{Name: "Go"}}} />`. With a lowercase `item` that the body also uses, Orbit reports:

```orbit file=src/components/List.orbit expect-error
---
type item struct {
	Name string
}

type Props struct {
	Items []item
}

items := append([]item{{Name: "Go"}}, props.Items...)
---
<ul>
  {for _, it := range items}
    <li>{it.Name}</li>
  {/for}
</ul>
```

```text
src/components/List.orbit:2:6: item is used by Props, so it lives in the component's declaration package and must be exported (rename it to Item)
```

A lowercase name that only `Props` (and other declarations in the declaration package) uses is fine; the error appears only when the body, the template or another helper refers to it.

For the same reason, a component import alias, a named Go import or another hoisted name can't reuse a name that `Props` pulled into the declaration package (`Item "./Item.orbit"` next to a `type Item` that `Props` uses). Orbit reports it at that name:

```orbit file=src/components/List.orbit expect-error
---
import (
	Item "./Item.orbit"
)

type Item struct {
	Name string
}

type Props struct {
	Items []Item
}
---
<ul>
  {for _, it := range props.Items}
    <Item name={it.Name} />
  {/for}
</ul>
```

```text
src/components/List.orbit:3:2: Item clashes with Item in the component's declaration package (Props uses it); rename one
```

The names Orbit generates there, `Props` and `Render` (and a page's `Params`), are taken the same way:

```orbit file=src/components/List.orbit expect-error
---
import (
	Props "./Item.orbit"
	"strings"
)

type Item struct {
	Name string
}
---
<p>{strings.ToUpper("items")}</p>
<Props />
```

```text
src/components/List.orbit:3:2: Props clashes with the generated Props of the component's declaration package; rename it
```

Shadowing such a name inside the body (`Item := 42`) is fine.

## Templates

The template is HTML plus a few constructs. The full grammar is in [Template syntax](reference/template-syntax.md).

```orbit
<h1 class="title">{props.Post.Title}</h1>
<a href={url} class={"card " + extra}>Read</a>
<p>{len(list)} posts, {props.Post.Date.Format("2006")}</p>
{/* a comment that renders nothing */}
<!-- an HTML comment, sent to the browser -->
```

- `{expr}` is any Go expression. Its value is escaped and written; how each type prints (numbers, `nil`, errors, `fmt.Stringer`, …) is in [How values render](reference/template-syntax.md#how-values-render).
- An attribute is static (`class="title"`) or an expression (`href={url}`). There's no interpolation inside quotes: `class="card {extra}"` is the literal text `card {extra}`. To build a value, use a [template literal](reference/template-syntax.md#template-literals), as in Astro: `` class={`card ${extra}`} ``, `` href={`/blog/${post.Slug}`} ``, `` aria-label={`Chapter ${n}`} `` (pieces of any type, printed as `{…}` prints them). On an element it costs nothing beyond the writes of its pieces.
- To build a `class` from parts and conditions, use `class:list`, as in Astro: `class:list={"card", extra, "featured": props.Featured}` writes `card`, the classes in `extra`, and `featured` when `props.Featured` is true. Static classes are joined at build time, so it costs no more than writing the attribute by hand. On a component tag, it sets the `class` prop. See [`class:list`](reference/template-syntax.md#classlist).
- `true` writes a bare attribute and `false`, `nil` or an unset [`orbit.Opt`](#optional-props) omits it, so `data-featured={props.Featured}` renders `data-featured` or nothing. On `aria-*` attributes and the few HTML attributes whose values are the words `true` and `false` (`contenteditable`, `draggable`, `spellcheck`, `writingsuggestions`), a `bool` is written as text instead: `aria-pressed={props.Saved}` renders `aria-pressed="false"`, which is what ARIA expects.
- Every element must be closed (`<p>…</p>`, `<li>…</li>`), or self-closed (`<div />`); void elements such as `<br>` and `<img>` take no closing tag. Orbit doesn't auto-close tags.
- A `<` starts a tag only when a letter, `/`, `!` or `>` (the fragment shorthand `<>`) follows it; otherwise it's text, so `{a} < {b}` works. To write a literal `{`, use `{"{"}`.

### Conditional attributes

Go has no `?:`, so Orbit has helpers for values that depend on a condition: `orbit.If(cond, v)` is an `orbit.Opt` set to `v` when `cond` is true and unset otherwise (unset omits the attribute), `orbit.NonZero(x)` is `x` unless it's empty or zero (Astro's `x || undefined`), and `orbit.Cond(cond, a, b)` is `a` or `b`:

```orbit file=src/components/TopBar.orbit
---
type Item struct {
	Section, Href, Label string
}

type Props struct {
	Items   []Item
	Current string
	Saved   bool
}
---
<nav>
  {for _, item := range props.Items}
    <a href={item.Href} aria-current={orbit.If(item.Section == props.Current, "page")}>{item.Label}</a>
  {/for}
  <button aria-pressed={props.Saved} title={orbit.Cond(props.Saved, "Remove from library", "Add to library")}>
    {orbit.Cond(props.Saved, "Saved", "Save")}
  </button>
</nav>
```

With `current="fiction"` and two items, this renders `<a href="/fiction" aria-current="page">Fiction</a><a href="/news">News</a>`, then `<button aria-pressed="false" title="Add to library">Save</button>`.

- Both arguments are evaluated, as for any Go call: `orbit.If(user != nil, user.Name)` panics when `user` is nil. Guard it in the frontmatter, or use `{if}`.
- For a default value, Astro's `x ?? "fallback"`, use the standard library's `cmp.Or(x, "fallback")`, or `o.Or("fallback")` on an `orbit.Opt`.
- `orbit.If`, or any `orbit.Opt` value, as the whole value of an attribute or a `{…}` costs no allocation: Orbit compiles it to an `if`. The details are in [Conditional values](reference/template-syntax.md#conditional-values-orbitif-and-orbitcond).

### Control flow

`{if}`, `{else if}`, `{else}` and `{/if}` form a Go `if` chain. `{for …}` takes any Go `for` clause and ends with `{/for}`:

```orbit file=src/components/Tags.orbit
---
type Props struct {
	Tags []string
}
---
{if len(props.Tags) == 0}
  <p>No tags.</p>
{else if len(props.Tags) == 1}
  <p>Tag: {props.Tags[0]}</p>
{else}
  <ul>
    {for i, tag := range props.Tags}
      <li data-index={i}>{tag}</li>
    {/for}
  </ul>
{/if}

{for i := 0; i < 3; i++}<span>★</span>{/for}
```

Each loop iteration has its own variables (Go 1.22 semantics), so closures inside a loop are safe.

`{switch …}`, `{case …}`, `{default}` and `{/switch}` form a Go `switch`. The header can be a tag, an init statement and a tag, nothing (each case is then a condition), or a type switch:

```orbit file=src/components/Media.orbit
---
type Props struct {
	Items []any
}
---
<ul>
  {for _, item := range props.Items}
    {switch v := item.(type)}
      {case string}
        <li>{v}</li>
      {case int}
        <li>{v} items</li>
      {default}
        <li>other</li>
    {/switch}
  {/for}
</ul>
```

As in Go, the first matching case wins and there's no fall-through; list several values in one case instead (`{case "scheduled", "queued"}`). The details and errors are in [`{switch}`](reference/template-syntax.md#switch).

A call that returns a value and an error can't be used directly in `{…}`. Handle the error in the frontmatter, then use the value.

### Variables in the template

The template has no statements, so you can't write `x := …` between two tags. A variable lives in one of these places:

- **A block header**, as in Go. `{if}` and `{else if}` take an init statement (`{if n, ok := counts[tag]; ok}`), and the variable is visible in that branch and the branches after it. `{for}` declares its loop variables. `{switch}` takes an init statement too (`{switch s := label(p); s}`), and a type switch declares its variable with each case's type.
- **The frontmatter**, for anything computed once per render.
- **A helper func** in the frontmatter, called from the template: `{label(p)}`.
- **A component**, when each loop iteration needs its own variables: the component's frontmatter runs on every render.

```orbit file=src/components/TagRow.orbit
---
type Props struct {
	Tag   string
	Count int
}

plural := "s"
if props.Count == 1 {
	plural = ""
}
---
<li>{props.Tag}: {props.Count} post{plural}</li>
```

```orbit file=src/components/TagCounts.orbit
---
import (
	TagRow "./TagRow.orbit"
	"maps"
	"slices"
)

type Props struct {
	Counts   map[string]int
	Featured string
}
---
{if n, ok := props.Counts[props.Featured]; ok}
  <p>Featured: {props.Featured} ({n})</p>
{/if}
<ul>
  {for _, tag := range slices.Sorted(maps.Keys(props.Counts))}
    <TagRow tag={tag} count={props.Counts[tag]} />
  {/for}
</ul>
```

Splitting a template into components costs almost nothing at runtime. A call is one Go function call, with `Props` passed as a struct value. There's no per-call object or context to set up, unlike Astro, where setting up each component is a large share of the render time (see [Performance](performance.md#where-the-time-goes-orbit-vs-astro)). Slot content that uses a variable of the caller, such as `<Row>{p.Title}</Row>` inside a loop, allocates nothing in an `orbit build` binary either: components are called directly there, so the content stays on the stack. In `orbit dev` and `go test`, which call components through a hook so an edit recompiles only that component, it allocates a small closure per call, and so does content passed into components that render each other in a cycle ([Direct calls in production builds](performance.md#direct-calls-in-production-builds)).

## Slots

A component renders the children its parent passed with `<slot />`. Children are compiled in the parent, so they see the parent's variables and get the parent's scoped styles.

This layout has a default slot and a named `footer` slot with fallback content:

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
    <title>{props.Title} · My blog</title>
  </head>
  <body>
    <header><a href="/">My blog</a></header>
    <main>
      <slot />
    </main>
    <footer>
      <slot name="footer">Made with Orbit.</slot>
    </footer>
  </body>
</html>
```

- `<slot />` renders the default slot: every child without a `slot` attribute.
- `<slot name="footer">…</slot>` renders the children marked `slot="footer"`. If the parent passed none, the slot's own children (the fallback) render instead. `<slot />` accepts a fallback too.
- On a child, `slot="footer"` sends that element to the named slot. The element itself is rendered there, without its `slot` attribute.
- Several children can target the same slot; they all render there, in their order in the parent.

In `index.orbit` above, `<p slot="footer">` replaces the fallback text.

### Checking for a slot

`slots.Has(name)` reports whether the parent passed content for a slot (`"default"` is the default slot). Use it to skip a wrapper element:

```orbit file=src/components/Panel.orbit
---
type Props struct {
	Title string
}
---
<section class="panel">
  <h2>{props.Title}</h2>
  <slot />
  {if slots.Has("actions")}
    <div class="actions"><slot name="actions" /></div>
  {/if}
</section>
```

Whitespace alone isn't content: `<Panel title="x"> </Panel>` passes no default slot, so `slots.Has("default")` is false and `<slot />` renders its fallback.

### `<Fragment>`

`<Fragment>` groups children without adding an element. It's mostly used to send several elements to one named slot:

```orbit file=src/pages/about.orbit
---
import (
	Panel "../components/Panel.orbit"
	Base "../layouts/Base.orbit"
)
---
<Base title="About">
  <Panel title="Contact">
    <p>Write to us any time.</p>
    <Fragment slot="actions">
      <a href="mailto:hello@example.com">Email</a>{" "}
      <a href="/rss.xml">RSS</a>
    </Fragment>
  </Panel>
  <Fragment slot="footer">
    <p>Made with Orbit.</p>
    <p>Hosted anywhere.</p>
  </Fragment>
</Base>
```

The `<a>` links render inside `<div class="actions">`, with no wrapper around them.

### Rendering a slot to a string

`orbit.RenderHTML(ctx, slots.Default)` renders a slot in the frontmatter and returns its HTML as an `orbit.HTML` value (Astro's `await Astro.slots.render("default")`), which the template writes with `set:html`. Use `slots.Get("footer")` for a named slot. This component wraps its children's HTML in a `<details>` only when it's long:

```orbit file=src/components/Collapse.orbit
---
html, err := orbit.RenderHTML(ctx, slots.Default)
if err != nil {
	return err
}
---
{if len(html) > 2000}
  <details><summary>Show more</summary><Fragment set:html={html} /></details>
{else}
  <Fragment set:html={html} />
{/if}
```

- **Same HTML**: the string is byte for byte what the slot would write in place, with the components, scoped styles, `class:list`, `client:*` markers and `server:defer` placeholders in it.
- **Errors are returned**: the error of the slot's content comes back as `err`, its partial output is dropped, and the page isn't affected until you `return err`. A slot that wasn't passed gives `""`.
- **The depth limit counts through it**, so [runaway recursion](#the-depth-limit) through a rendered slot stops with the usual error.
- **No `</head>` inside**: the page's asset tags go at the page's own `</head>`, so rendering a document head inside `orbit.RenderHTML` is a render error: `orbit: </head> can't be inside orbit.RenderHTML; keep the document head outside the captured content`.
- **Cost**: one allocation, the returned string; the buffer is pooled.

`orbit.RenderHTML` takes any `orbit.Renderer`, so it works for renderers written in Go too. Its main use is a fragment cache: see [Caching fragments](rendering.md#caching-fragments).

## Recursion with `<Self>`

Inside a component, `<Self>` renders the component itself, with the same props and slots rules as any other component. It's how you render trees:

```orbit file=src/components/Tree.orbit
---
type Node struct {
	Name     string
	Children []Node
}

type Props struct {
	Node Node
}
---
<li>
  {props.Node.Name}
  {if len(props.Node.Children) > 0}
    <ul>
      {for _, child := range props.Node.Children}
        <Self node={child} />
      {/for}
    </ul>
  {/if}
</li>
```

`Node` is used by `Props`, so it's exported, and a parent builds it as `Tree.Node`:

```orbit file=src/pages/topics.orbit
---
import (
	Tree "../components/Tree.orbit"
	Base "../layouts/Base.orbit"
)

root := Tree.Node{
	Name: "Topics",
	Children: []Tree.Node{
		{Name: "Go", Children: []Tree.Node{{Name: "Generics"}, {Name: "Iterators"}}},
		{Name: "Web"},
	},
}
---
<Base title="Topics">
  <ul class="tree">
    <Tree node={root} />
  </ul>
</Base>
```

Stopping the recursion is up to you, usually with an `{if}` around the recursive call (here, a node without children renders no `<Self>`).

### Mutual recursion

Two components can render each other. A comment thread is a list of comments, and each comment has a thread of replies:

```orbit file=src/components/CommentThread.orbit
---
import (
	Comment "./Comment.orbit"
	"example.com/blog/internal/posts"
)

type Props struct {
	Comments []posts.Comment
}
---
{if len(props.Comments) > 0}
  <ul class="thread">
    {for _, c := range props.Comments}
      <li><Comment comment={c} /></li>
    {/for}
  </ul>
{/if}
```

```orbit file=src/components/Comment.orbit
---
import (
	CommentThread "./CommentThread.orbit"
	"example.com/blog/internal/posts"
)

type Props struct {
	Comment posts.Comment
}
---
<article class="comment">
  <p class="author">{props.Comment.Author}</p>
  <p>{props.Comment.Text}</p>
  <CommentThread comments={props.Comment.Replies} />
</article>
```

The two files import each other, which Go would normally reject as an import cycle; Orbit compiles components so that this works (see [How it compiles](#how-it-compiles)). The recursion ends at a comment with no replies, where `CommentThread` renders nothing. The post page finds its post from the URL and renders the whole tree:

```orbit file=src/pages/blog/[slug].orbit
---
import (
	"errors"

	CommentThread "../../components/CommentThread.orbit"
	Base "../../layouts/Base.orbit"
	"example.com/blog/internal/posts"
)

post, err := posts.Find(params.Slug)
if errors.Is(err, posts.ErrNotFound) {
	return orbit.ErrNotFound
}
if err != nil {
	return err
}
comments, err := posts.Comments(post.Slug)
if err != nil {
	return err
}
---
<Base title={post.Title}>
  <article>
    <h1>{post.Title}</h1>
    <div set:html={orbit.HTML(post.Body)}></div>
  </article>
  <h2>Comments</h2>
  <CommentThread comments={comments} />
</Base>
```

If loading the comments is slow, render them with `server:defer` so they don't hold up the page; see [Lazy loading](#lazy-loading).

One cycle can't be compiled: two `Props` types that refer to each other's components (`A.Props` has a field of type `B.Props` and `B.Props` has one of type `A.Props`). Orbit reports it once per cycle, with the shortest import chain, at the import alias in the first file of the chain (by path). With this `B.orbit`:

```orbit file=src/components/B.orbit
---
import A "./A.orbit"

type Props struct {
	Parent *A.Props
}
---
```

an `A.orbit` that uses `B.Props` in its own `Props` is reported:

```orbit file=src/components/A.orbit expect-error
---
import B "./B.orbit"

type Props struct {
	Child *B.Props
}
---
```

```text
src/components/A.orbit:2:8: import cycle not allowed between component Props: src/components/A.orbit → src/components/B.orbit → src/components/A.orbit; declare the shared types in a package under internal/
```

Passing shared data types from an `internal/` package, as above, avoids it.

### The depth limit

If recursion never stops, Orbit stops it: rendering more than 1000 nested components returns an error that names the component stack, instead of crashing the process with a Go stack overflow. The guard is always on, in development and production:

```text
orbit: render depth exceeded 1000: src/components/Comment.orbit > src/components/CommentThread.orbit > src/components/Comment.orbit > …
```

It's handled like any other render error: a 500 response, or an overlay in `orbit dev`.

## Lazy loading

Two directives on a component tag keep pages fast. They're covered in [Lazy loading](lazy-loading.md):

- **`server:defer`** renders a slow component in its own request after the page is sent. The page shows the `fallback` slot until the component's HTML arrives:

  ```orbit
  <Comments server:defer postID={post.Slug}>
    <p slot="fallback">Loading comments…</p>
  </Comments>
  ```

- **`client:idle`**, **`client:visible`** and **`client:media`** load the `<script>` of a component, and of every component it renders, only when needed. The HTML is still rendered on the server:

  ```orbit
  <ShareButtons title={post.Title} client:visible="200px" />
  ```

Both work only on `.orbit` components, and they can't be combined on one tag.

## Fetching data in components

Prefer loading data in the page and passing it down as props, or reading it from a per-request loader in `locals` when several components need it: see [Loading data](data-loading.md). A component can still fetch its own data in its frontmatter, as in Astro. Orbit renders components in order, so two slow components add up; mark each slow one with `server:concurrent` to render it in its own goroutine, overlapping the others (see [Slow components](rendering.md#slow-components)):

```orbit
<Weather server:concurrent city="Paris" />
<LatestPosts server:concurrent limit={5} />
```

A marked component runs at the same time as the rest of the page, so the usual Go rules about data races apply to request data that's now shared:

- **Props passed by reference** (slices, maps, pointers): don't change them in the page or in a marked component while the other may read them.
- **`locals` fields**: every component of a request shares one `locals` value. A field that a marked component writes (`locals.Count++`) and that other components read or write needs a `sync` type, such as `sync.Mutex` or `atomic.Int64`, in your `Locals`.
- **`ctx` in your own closures**: a frontmatter closure that captures `ctx` and calls `ctx.SetStatus` or `ctx.Header()` must not be called from a marked component's subtree.
- **`ctx.Request.Body`**: read it in the page's frontmatter, not in components.

Package-level variables were already shared between concurrent requests, so nothing changes for them. Run your tests with `go test -race` to catch races: `orbittest.Render` and `orbittest.Get` render marked components concurrently, as in production.

## Passing data to client scripts

A component's `<script>` runs in the browser, so it can't read `props`. Pass it data in the HTML: as typed JSON with `orbit.JSON`, as plain `data-*` attributes, or as a JSON block with `set:json`. All three are escaped for you.

### Typed data with `orbit.JSON`

Wrap a value in `orbit.JSON` to write it into a `data-*` attribute as JSON. Orbit encodes it with `encoding/json`, so the usual `json` struct tags apply:

```go file=internal/api/api.go
// Package api holds the JSON that pages send to the browser.
package api

import "time"

// Post is a post as a like button sees it.
type Post struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags"`
	Published time.Time `json:"published"`
	Likes     int       `json:"likes"`
	Author    *Author   `json:"author,omitempty"`
	draft     bool
}

// Author is a post's author.
type Author struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}
```

The component's script imports **its own `.orbit` file** to read the data back. `data(el, key)` parses the attribute and returns it with the TypeScript type of the Go value. In the editor, completion inside `data(this, "…")` offers the keys the template writes, and go-to-definition on a key or a field jumps to the attribute or the Go field (see [TypeScript in `<script>` blocks](editor-setup.md#typescript-in-script-blocks)):

```orbit file=src/components/LikeButton.orbit
---
import "example.com/blog/internal/api"

type Props struct {
	Post  api.Post
	Liked bool
}
---
<like-button data-post={orbit.JSON(props.Post)} data-liked={orbit.JSON(props.Liked)}>
  <button type="button">♥ <span>{props.Post.Likes}</span></button>
</like-button>
<script>
  import { data } from "./LikeButton.orbit";

  class LikeButton extends HTMLElement {
    connectedCallback() {
      const post = data(this, "post"); // api.Post
      const liked = data(this, "liked"); // boolean
      const count = this.querySelector("span")!;
      this.title = post.tags?.join(", ") ?? "";
      this.querySelector("button")!.addEventListener("click", async () => {
        const res = await fetch(`/blog/${post.slug}/like`, { method: "POST" });
        if (res.ok) count.textContent = String(post.likes + (liked ? 0 : 1));
      });
    }
  }
  customElements.define("like-button", LikeButton);
</script>
```

The value is written single-quoted, so the JSON stays readable in view-source:

```html
<like-button data-post='{"id":42,"slug":"hello","title":"Hello \u003cworld\u003e","tags":["go"],"published":"2026-10-26T12:00:00Z","likes":7}' data-liked='false'>
```

The import never loads the component itself: it bundles a tiny reader (368 bytes minified, once per page bundle). Its types come from a declaration file that `orbit generate` (and `build`, `dev`, `check`) writes to `orbit_gen/types/src/components/LikeButton.orbit.d.ts`:

```ts
import type { api } from "orbit:types";

/** The orbit.JSON data-* attributes written by src/components/LikeButton.orbit, by dataset name. */
export interface Data {
  /** data-liked (LikeButton.orbit:9:49) */
  liked: boolean;
  /** data-post (LikeButton.orbit:9:14) */
  post: api.Post;
}
export declare function data<K extends "liked" | "post">(el: Element, key: K): Data[K];
```

with `api.Post` declared in `orbit_gen/types/go.d.ts` (`tags: string[] | null`, `published: string`, `author?: Author`, and no `draft`, which is unexported). So [`orbit check`](styles-and-scripts.md#type-checking-with-orbit-check) catches a script that reads a field the server doesn't send, misspells a key, or reads a key the template no longer writes, at the `.orbit` position. A nil slice encodes as `null`, so `tags` is typed `string[] | null`, and forgetting that is an error (won't type-check):

```orbit file=src/components/LikeButton.orbit expect-error at=20
      this.title = post.tags.join(", ");
```

```text
src/components/LikeButton.orbit:20:20: error TS18047: 'post.tags' is possibly 'null'.
```

To get a non-null type, tag the field `omitempty` (or `omitzero`): a nil slice is then left out, so the field becomes optional (`tags?: string[]`).

- **Keys** are the attributes' dataset names, as `el.dataset` uses them: `data-author-name` is `"authorName"`. Every `data-*` attribute of an HTML element whose value is a direct `orbit.JSON(…)` call is a key, and an element can have several. The same key written on several elements, or in `{if}` branches, gets the union of their types.
- **`data` throws** if the element doesn't have the attribute: `orbit: <like-button> has no data-post attribute`. The types say what the component may write, not which element carries it, so call it on the element the template puts the attribute on.
- **`.ts` files** can import any component's module by relative path (`import { data } from "../components/LikeButton.orbit"`), and your editor checks them. See [Importing TypeScript files](styles-and-scripts.md#importing-typescript-files).
- **Unsupported types** are a compile error at the `orbit.JSON` argument: a func, a channel, a complex number or a map with non-string keys can never be encoded. Values that only fail at render time (a NaN float, a pointer cycle) are a render error at the attribute's position.
- **64-bit integers** are typed `number`, but JavaScript numbers lose precision past 2⁵³. For IDs that large, add the `,string` tag option (`json:"id,string"`) to send them as strings.

The full Go-to-TypeScript mapping, `set:json` typing with `json(id)` and the untyped `orbit:client` module are in [Client modules](reference/client-modules.md).

**When to keep plain `data-*`**: encoding a value as JSON costs about 130 ns for a two-field struct, and around 500 ns for a typical post, against about 13 ns and no allocation for the same values as plain attributes. Use plain `data-*` attributes (below) for a few scalars and `orbit.JSON` for structured data, and pass large values as a pointer (`orbit.JSON(&post)`), which saves `encoding/json` a copy. See [Performance](performance.md#encoding-json-for-scripts).

### `data-*` attributes and custom elements

For a few scalar values that belong to one element, write them into plain `data-*` attributes and read `el.dataset` in a custom element. The script runs once per page, and `connectedCallback` runs for every instance of the element, including instances that arrive later in a deferred component:

```orbit file=src/components/LikeCount.orbit
---
type Props struct {
	Slug  string
	Likes int
}
---
<like-count data-slug={props.Slug} data-likes={props.Likes}>
  <button type="button">♥ <span>{props.Likes}</span></button>
</like-count>
<script>
  class LikeCount extends HTMLElement {
    connectedCallback() {
      const slug = this.dataset.slug ?? "";
      let likes = Number(this.dataset.likes);
      const count = this.querySelector("span")!;
      this.querySelector("button")!.addEventListener("click", async () => {
        const res = await fetch(`/blog/${slug}/like`, { method: "POST" });
        if (res.ok) count.textContent = String(++likes);
      });
    }
  }
  customElements.define("like-count", LikeCount);
</script>
```

Attribute values are always strings in the browser, so convert numbers yourself (`Number(this.dataset.likes)`). A boolean written as `data-featured={props.Featured}` is a bare attribute or nothing, so test it with `"featured" in this.dataset`.

### JSON blocks with `set:json`

For a larger block of data, such as a list, use `set:json` on a `<script type="application/json">`. Orbit encodes the value with `encoding/json` and escapes it so it can't break out of the script element (`<`, `>`, `&`, U+2028 and U+2029 become `\u003c`, `\u003e`, `\u0026`, `\u2028` and `\u2029`). The script reads it back with `JSON.parse`:

```orbit file=src/components/PostFilter.orbit
---
import "example.com/blog/internal/posts"

type entry struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

list, err := posts.All()
if err != nil {
	return err
}
entries := make([]entry, 0, len(list))
for _, p := range list {
	entries = append(entries, entry{Title: p.Title, URL: "/blog/" + p.Slug})
}
---
<post-filter>
  <input type="search" placeholder="Filter posts">
  <ul></ul>
  <script type="application/json" set:json={entries}></script>
</post-filter>
<script>
  type Entry = { title: string; url: string };

  class PostFilter extends HTMLElement {
    connectedCallback() {
      const data = this.querySelector('script[type="application/json"]')!;
      const entries: Entry[] = JSON.parse(data.textContent ?? "[]");
      const input = this.querySelector("input")!;
      const list = this.querySelector("ul")!;
      const show = () => {
        const q = input.value.toLowerCase();
        list.replaceChildren(
          ...entries
            .filter((e) => e.title.toLowerCase().includes(q))
            .map((e) => {
              const a = document.createElement("a");
              a.href = e.url;
              a.textContent = e.title;
              const li = document.createElement("li");
              li.append(a);
              return li;
            }),
        );
      };
      input.addEventListener("input", show);
      show();
    }
  }
  customElements.define("post-filter", PostFilter);
</script>
```

- The JSON block sits inside the custom element, so each instance finds its own, and the script names the type itself (`Entry`). For per-instance data, `data-entries={orbit.JSON(entries)}` is typed for you instead.
- For page-wide data, give the block a static `id` and read it with `json(id)` from the component's module (`import { json } from "./PostFilter.orbit"`): like `data`, it's typed from the Go value. See [JSON blocks](styles-and-scripts.md#json-blocks-with-setjson).
- `set:json` only works on `<script type="application/json">`, which Orbit never bundles or processes. Anywhere else it's a compile error: `set:json only works on <script type="application/json">`.
- The value can have any type `encoding/json` can encode, and the usual `json` struct tags apply. A type it can never encode (a func, a channel) is a compile error; a value it can't encode (a NaN float, a cycle) is a render error at the `set:json` position.

## Escaping and security

Orbit escapes everything you write with `{…}` and every attribute expression, so values from users and databases can't inject markup.

```orbit
<p>{comment.Text}</p>            {/* "<b>hi</b>" renders as text, not bold */}
<a title={comment.Author}>…</a>   {/* quotes in the value are escaped */}
```

### URL attributes

Escaping doesn't stop a `javascript:` URL, so URL attributes (`href`, `src`, `action`, `formaction` and the others listed in [Template syntax](reference/template-syntax.md#attribute-values-on-html-elements)) are also filtered at runtime. A value whose scheme is `javascript:` or `vbscript:` (ignoring case and leading whitespace or control characters) is replaced with `about:invalid#orbit`:

```orbit
<a href={profile.Website}>Website</a>
```

With `profile.Website` set to `"javascript:alert(1)"`, this renders `<a href="about:invalid#orbit">Website</a>`. The filter also applies to spread attributes (`{...attrs}`). Static values you write in the template yourself are left as written.

When you do need such a URL and you trust it, give it the type `orbit.URL`, which skips the filter:

```orbit
---
bookmarklet := orbit.URL("javascript:void(document.body.classList.toggle('dark'))")
---
<a href={bookmarklet}>Toggle dark mode</a>
```

### Raw HTML with `orbit.HTML`

To insert trusted markup without escaping, convert it to `orbit.HTML`. Either write it with `{…}` or use the `set:html` directive on an element:

```orbit
<div set:html={orbit.HTML(props.Post.Body)}></div>
<div>{orbit.HTML(props.Post.Body)}</div>
```

`set:html` only accepts an `orbit.HTML` value, an [`orbit.Opt[orbit.HTML]`](reference/template-syntax.md#optional-values-orbitopt) (none writes no content, as for an optional prop: `<div set:html={props.Intro}></div>` with `Intro orbit.Opt[orbit.HTML]`), or a string constant written in the template. A plain `string` variable is a compile error at the `.orbit` position, so raw output is always an explicit choice (won't compile):

```orbit file=src/components/PostBody.orbit expect-error
---
type Props struct {
	Body string
}

body := props.Body
---
<div set:html={body}></div>
```

```text
src/components/PostBody.orbit:8:16: set:html needs orbit.HTML or orbit.Opt[orbit.HTML], not string: convert a trusted value with orbit.HTML(…), or use set:text
```

The editor shows it too, a moment after you type.

Only convert content you control (your own Markdown renderer's output, say), never user input. `set:text={expr}` is the escaped counterpart: it sets an element's text content.

### Event handler and style attributes

Escaping makes a value safe inside an HTML attribute, but not inside JavaScript or CSS. So the compiler **warns** (the build still succeeds) on `on*={expr}` attributes such as `onclick={…}` and on `style={expr}`:

```orbit file=src/components/Promo.orbit expect-warning
---
type Props struct {
	Handler string
	Color   string
}
---
<article class="card">
  <button onclick={props.Handler}>Open</button>
  <p style={"color: " + props.Color}>Hello</p>
</article>
```

```text
src/components/Promo.orbit:8:11: warning: onclick={…} is escaped for HTML, not for JavaScript; don't put untrusted values here
src/components/Promo.orbit:9:6: warning: style={…} is escaped for HTML, not for CSS; don't put untrusted values here
```

There's no switch to silence these warnings, but two kinds of values don't warn:

- **Constants**: values you write yourself, whether static (`onclick="toggle()"`, `style="color: red"`) or a Go constant expression (`style={"display: none"}`, a `const`, `"a" + "b"`).
- **`orbit.CSS` values** on `style`: an `orbit.CSS` is CSS declarations (or a CSS value) that your code vouches for. Convert to it where you build the value from data you've validated, typically in a helper, and the trust travels with the value:

```go file=internal/reader/style.go
package reader

import (
	"strconv"

	"github.com/orbit-z/orbit"
)

type Settings struct {
	Size    int
	Leading float64
}

// Style is the reader settings as CSS custom properties. Every value is a
// number, so the declarations are trusted.
func Style(s Settings) orbit.CSS {
	return orbit.CSS("--reader-size:" + strconv.Itoa(s.Size) + "px;--reader-leading:" +
		strconv.FormatFloat(s.Leading, 'f', 2, 64))
}
```

```orbit file=src/layouts/Reader.orbit
---
import "example.com/blog/internal/reader"

type Props struct{ Settings reader.Settings }
---
<div class="reader" style={reader.Style(props.Settings)}><slot /></div>
```

With `Settings{Size: 18, Leading: 1.6}`, this renders `<div class="reader" style="--reader-size:18px;--reader-leading:1.60">`, with no warning. An `orbit.CSS` is written escaped like any string (`"` and `&` become `&quot;` and `&amp;`, which the browser decodes before the CSS parser runs), and Orbit never filters it, so **never put untrusted input into one without validating it**: a user-supplied color could close the declaration and add its own. Only `orbit.CSS` itself counts (`type MyCSS orbit.CSS` warns), and it means nothing to a URL attribute, which still filters `javascript:` URLs. There's no trusted type for `on*` attributes: pass data to a script with `orbit.JSON`, a `data-*` attribute or `set:json`; see [Passing data to client scripts](#passing-data-to-client-scripts).

The same rules apply to `style={…}` and `on*={…}` passed to a component with an [`orbit.RestAttrs`](#passing-html-attributes-through) field, which end up on an HTML element: the warning is at the call site's attribute. A component that declares a `Style orbit.CSS` prop instead takes only trusted values, and its own `style={props.Style}` doesn't warn.

The warnings come from the type check that picks typed writes, so a component whose Go code doesn't type-check (the build then fails anyway) warns on every `style={…}` and `on*={…}` attribute. In the editor, they come from the language server's background type check, which runs at startup, after `orbit dev` or `orbit generate` rewrites `orbit_gen/`, and 300 ms after you stop typing in a file that has such an attribute (or just lost one) or change a Go file. A warning goes away as soon as you edit its attribute, and comes back after that check if the new value still warns.

## How it compiles

Each `.orbit` file becomes two Go packages under `orbit_gen/`. The **declaration package** (`orbit_gen/decl/components/card`) holds `Props`, what it depends on, and a `Render` function (for a page, also the generated `Params`); the **implementation package** holds everything else, including your render body and template. Components import each other's declaration packages only, which is why mutual recursion doesn't create a Go import cycle, why names `Props` uses must be exported, and why errors mention `Card.Props` (Orbit rewrites generated names to your import alias).

Your Go code is copied as is, with line directives, so both Orbit's errors and the Go compiler's point at the `.orbit` file as `file:line:col` (columns count bytes, and a tab counts as one). The `orbit` CLI prints every error as `src/…`; `go build` or `go vet` run directly print the same positions as `./src/…`.

Your own Go code (a test or an HTTP handler) can import a component through its declaration package, as long as the program also imports `example.com/blog/orbit_gen/app`; otherwise `Render` panics with `orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app`. See [Testing](testing.md) and [Server](server.md).

## See also

- [Template syntax](reference/template-syntax.md): the full grammar, every directive, and how each value type renders
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`
- [Routing](routing.md): pages, typed `params`, `ctx.Param` and `404.orbit`
- [Rendering](rendering.md): the request lifecycle, status codes, redirects and errors
- [Middleware and locals](middleware.md): typed `locals`
- [Styles and scripts](styles-and-scripts.md): scoped CSS and TypeScript
- [Client modules](reference/client-modules.md): `data` and `json` for scripts, and the Go-to-TypeScript type mapping
- [Runtime API](reference/runtime-api.md): `orbit.Ctx`, `orbit.Slots`, `orbit.HTML`, `orbit.URL`, `orbit.JSON`
