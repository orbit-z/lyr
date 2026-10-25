# Components

A component is a `.orbit` file: Go frontmatter that prepares data, followed by an HTML template that renders it.

## A first component

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
  .card { border: 1px solid #ccc; padding: 1rem }
</style>
```

A page uses it like an HTML tag:

```orbit file=src/pages/index.orbit
---
import (
    "example.com/blog/internal/posts"
    Base "../layouts/Base.orbit"
    Card "../components/Card.orbit"
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

The file has three parts:

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

### What's available in the render body

These names are provided in the render body and the template, without declaring or importing anything:

| Name | Type | What it is |
|---|---|---|
| `props` | `Props` | The values the parent passed. |
| `ctx` | `*orbit.Ctx` | The request: `ctx.Request`, `ctx.URL`, `ctx.Site`, plus `ctx.Param`, `ctx.SetStatus`, `ctx.Header` and `ctx.Redirect`. See [Rendering](rendering.md) and [Runtime API](reference/runtime-api.md). |
| `slots` | `orbit.Slots` | The children the parent passed. See [Slots](#slots). |
| `params` | `Params` | **Pages only.** The route params, typed: `params.Slug` for `src/pages/blog/[slug].orbit`. Orbit generates the `Params` struct from the file path. Components read a route param with `ctx.Param("slug")` instead. See [Route params](routing.md#route-params-params). |
| `locals` | `*locals.Locals` | Request-scoped data, typed by your `type Locals struct` in the package `src/locals`. Without one, it's `*orbit.NoLocals`. See [Middleware and locals](middleware.md). |
| `orbit` | package | The runtime package `github.com/bertrandkhe/orbit`. You can use it without importing it; an explicit import of it under the name `orbit` is merged with the implicit one. |

A page and all its components share one `ctx` and one `locals`. (A component rendered with `server:defer` is the exception: it renders in its own request, with its own `ctx`; see [Lazy loading](lazy-loading.md).) `ctx` is pooled and reused for later requests, so don't keep it, or start a goroutine that uses it, after the render returns.

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

An error returned by a component stops the whole page, exactly as if the page itself had returned it. Status codes, headers and errors are covered in [Rendering](rendering.md).

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
    "strings"
    "example.com/blog/internal/posts"
    Base "../layouts/Base.orbit"
    Card "../components/Card.orbit"
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

Components can import each other in cycles; see [Mutual recursion](#mutual-recursion).

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

The same goes for `.jsx`, `.svelte` and `.vue` files. Any other relative path that doesn't end in `.orbit`, such as `"./util.ts"`, is an error too:

```orbit file=src/pages/index.orbit expect-error
---
import Util "./util.ts"
---
```

```text
src/pages/index.orbit:2:13: only .orbit components can be imported by relative path; import client modules from a <script>
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
    "example.com/blog/internal/posts"
    Card "../components/Card.orbit"
)

post, _ := posts.Find("hello")
---
<Card psot={post} />
```

```text
src/pages/index.orbit:9:7: unknown prop "psot" on <Card> (props: post, featured)
```

The list spells each field in lower camel case, as Astro props are usually written: a field `IsOpen` is listed as `isOpen`, `ID` as `id`.

A value of the wrong type is a regular Go error, also at the `.orbit` position:

```orbit file=src/pages/index.orbit expect-error
---
import (
    "example.com/blog/internal/posts"
    Card "../components/Card.orbit"
)

post, _ := posts.Find("hello")
---
<Card post={post} featured={"yes"} />
```

```text
src/pages/index.orbit:9:29: cannot use "yes" (untyped string constant) as bool value in struct literal
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
src/components/Card.orbit:5:5: Props field post must be exported, since a parent sets it (rename it to Post)
```

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
src/components/List.orbit:3:5: Item clashes with Item in the component's declaration package (Props uses it); rename one
```

The names Orbit generates there, `Props` and `Render` (and a page's `Params`), are taken the same way:

```orbit file=src/components/List.orbit expect-error
---
import (
    "strings"
    Props "./Item.orbit"
)

type Item struct {
    Name string
}
---
<p>{strings.ToUpper("items")}</p>
<Props />
```

```text
src/components/List.orbit:4:5: Props clashes with the generated Props of the component's declaration package; rename it
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
- An attribute is static (`class="title"`) or an expression (`href={url}`). There's no interpolation inside quotes: `class="card {extra}"` is the literal text `card {extra}`.
- `true` writes a bare attribute and `false` or `nil` omits it, so `data-featured={props.Featured}` renders `data-featured` or nothing.
- Every element must be closed (`<p>…</p>`, `<li>…</li>`), or self-closed (`<div />`); void elements such as `<br>` and `<img>` take no closing tag. Orbit doesn't auto-close tags.
- A `<` starts a tag only when a letter, `/` or `!` follows it; otherwise it's text, so `{a} < {b}` works. To write a literal `{`, use `{"{"}`.

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

Each loop iteration has its own variables (Go 1.22 semantics), so closures inside a loop are safe. `{switch}` isn't supported yet; use `{if}`/`{else if}`.

A call that returns a value and an error can't be used directly in `{…}`. Handle the error in the frontmatter, then use the value.

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
    Base "../layouts/Base.orbit"
    Panel "../components/Panel.orbit"
)
---
<Base title="About">
  <Panel title="Contact">
    <p>Write to us any time.</p>
    <Fragment slot="actions">
      <a href="mailto:hello@example.com">Email</a>
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
    Base "../layouts/Base.orbit"
    Tree "../components/Tree.orbit"
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
    "example.com/blog/internal/posts"
    Comment "./Comment.orbit"
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
    "example.com/blog/internal/posts"
    CommentThread "./CommentThread.orbit"
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

    "example.com/blog/internal/posts"
    Base "../../layouts/Base.orbit"
    CommentThread "../../components/CommentThread.orbit"
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

## Passing data to client scripts

A component's `<script>` runs in the browser, so it can't read `props`. Pass it data in the HTML. There are two patterns, and both are escaped for you.

### `data-*` attributes and custom elements

For data that belongs to one element, write it into `data-*` attributes and read `el.dataset` in a custom element. The script runs once per page, and `connectedCallback` runs for every instance of the element, including instances that arrive later in a deferred component:

```orbit file=src/components/LikeButton.orbit
---
type Props struct {
    Slug  string
    Likes int
}
---
<like-button data-slug={props.Slug} data-likes={props.Likes}>
  <button type="button">♥ <span>{props.Likes}</span></button>
</like-button>
<script>
  class LikeButton extends HTMLElement {
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
  customElements.define("like-button", LikeButton);
</script>
```

Attribute values are always strings in the browser, so convert numbers yourself (`Number(this.dataset.likes)`). A boolean written as `data-featured={props.Featured}` is a bare attribute or nothing, so test it with `"featured" in this.dataset`.

### JSON blocks with `set:json`

For structured data, use `set:json` on a `<script type="application/json">`. Orbit encodes the value with `encoding/json` and escapes it so it can't break out of the script element (`<`, `>`, `&`, U+2028 and U+2029 become `\u003c`, `\u003e`, `\u0026`, `\u2028` and `\u2029`). The script reads it back with `JSON.parse`:

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
        list.replaceChildren(...entries
          .filter((e) => e.title.toLowerCase().includes(q))
          .map((e) => {
            const a = document.createElement("a");
            a.href = e.url;
            a.textContent = e.title;
            const li = document.createElement("li");
            li.append(a);
            return li;
          }));
      };
      input.addEventListener("input", show);
      show();
    }
  }
  customElements.define("post-filter", PostFilter);
</script>
```

- The JSON block sits inside the custom element, so each instance finds its own. For page-wide data, give the block an `id` and read it with `document.getElementById("…").textContent`.
- `set:json` only works on `<script type="application/json">`, which Orbit never bundles or processes. Anywhere else it's a compile error: `set:json only works on <script type="application/json">`.
- The value can have any type `encoding/json` can encode, and the usual `json` struct tags apply. An encoding error is a render error at the `.orbit` position.

A typed bridge that generates TypeScript types from your Go types is planned after the first release.

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

`set:html` only accepts an `orbit.HTML` value (or a string constant written in the template). A plain `string` variable is a compile error at the `.orbit` position, so raw output is always an explicit choice (won't compile):

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
src/components/PostBody.orbit:8:16: cannot use body (variable of type string) as orbit.HTML value in argument to orbit__w.HTML
```

The message is the Go compiler's: `orbit__w.HTML` is the generated write for `set:html`.

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

There's no switch to silence these warnings. Static values you write yourself (`onclick="toggle()"`, `style="color: red"`) don't warn. To pass data to a script, use a `data-*` attribute or `set:json`; see [Passing data to client scripts](#passing-data-to-client-scripts).

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
- [Runtime API](reference/runtime-api.md): `orbit.Ctx`, `orbit.Slots`, `orbit.HTML`, `orbit.URL`
