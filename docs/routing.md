# Routing

Every `.orbit` file in `src/pages/` is a page (unless its name starts with `_`), its path in that directory is its URL, and it renders on every request.

```orbit file=src/pages/blog/[slug].orbit
---
import (
	"errors"

	"example.com/blog/internal/posts"
)

post, err := posts.Find(params.Slug)
if errors.Is(err, posts.ErrNotFound) {
	return orbit.ErrNotFound
}
if err != nil {
	return err
}
---
<article>
  <h1>{post.Title}</h1>
</article>
```

A request for `/blog/hello` renders this page with `params.Slug == "hello"`: Orbit generates a typed `params` from the file name (see [Route params](#route-params-params)). An unknown slug answers with your 404 page. The examples on this page leave out the layout to keep them short; see [Components](components.md) for layouts.

<details>
<summary><code>internal/posts/posts.go</code>, the tiny post store used by the examples on this page</summary>

```go file=internal/posts/posts.go
// Package posts is a tiny in-memory post store for the examples.
package posts

import "errors"

// ErrNotFound is returned by Find for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

type Post struct {
	Slug  string
	Title string
	Tags  []string
}

var all = []Post{
	{Slug: "hello", Title: "Hello, Orbit", Tags: []string{"news"}},
	{Slug: "routing", Title: "File-based routing", Tags: []string{"guides"}},
}

// All returns every post.
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
```

</details>

## File-based routes

The file path under `src/pages/` becomes the URL. `index.orbit` is the root of its directory, and URLs have no trailing slash by default (see [Trailing slashes](#trailing-slashes)).

| File | URL |
|---|---|
| `src/pages/index.orbit` | `/` |
| `src/pages/about.orbit` | `/about` |
| `src/pages/blog/index.orbit` | `/blog` |
| `src/pages/blog/[slug].orbit` | `/blog/hello`, `/blog/routing`, … |
| `src/pages/docs/[...path].orbit` | `/docs`, `/docs/install`, `/docs/guides/routing`, … |
| `src/pages/404.orbit` | none: it answers any URL nothing else matches |

Every `.orbit` file under `src/pages/` is a route, except one whose name, or the name of a directory on its path, starts with `_`. Such a file is a component, as in Astro, so you can keep a page's private parts next to it: `src/pages/blog/_parts/Byline.orbit` isn't a route, and `blog/[slug].orbit` imports it with `Byline "./_parts/Byline.orbit"`. Keep pieces used across the site in `src/components/` or `src/layouts/`.

Pages render on every request; Orbit never writes HTML files at build time (see [Rendering](rendering.md)). Pages also don't take props: they read their route params from `params` and the rest of the URL from `ctx`, and declaring `type Props` in a page is a build error.

Route errors are about a file, not a position in it, so they name the file without a line and column. Two files that produce the same route are a build error that names both files. That covers `src/pages/blog.orbit` next to `src/pages/blog/index.orbit`, and `[slug].orbit` next to `[id].orbit` in one directory.

The `/_orbit/` prefix is reserved for Orbit's assets and islands. No page can have a route there: `src/pages/_orbit/` starts with `_`, so the files in it are components.

### Markdown pages

A Markdown file in `src/pages/` is a route too, with the same rules: `src/pages/about.md` is `/about` and `src/pages/blog/index.md` is `/blog`. Its frontmatter can name an `.orbit` layout, which receives the frontmatter as props and the rendered Markdown in its slot:

```md file=src/pages/about.md
---
title: About
---

# About this blog

Written in **Markdown**, rendered to HTML when Orbit generates code.
```

An `.md` and an `.orbit` file with the same route (`about.md` and `about.orbit`) are a build error naming both, like any two pages. A Markdown page can't have route params (`[slug].md` is a build error). See [Markdown](markdown.md).

## Dynamic routes with `[param]`

A path segment in square brackets is a parameter. `src/pages/blog/[slug].orbit` matches `/blog/hello` and `/blog/anything-else`. A `[param]` matches exactly one non-empty path segment, so it never matches `/blog/hello/like`.

Read the value from `params.Slug`, a field of the page's generated `params` (see [Route params](#route-params-params)):

```orbit
<p>You asked for {params.Slug}</p>
```

The value is percent-decoded, as `ServeMux`'s `PathValue` is: `/blog/caf%C3%A9` gives `"café"`.

Any value reaches the page, so the page decides what exists: look it up, and `return orbit.ErrNotFound` when it's missing, as in the first example.

A parameter is always a whole segment: `[slug].orbit` and `[slug]/index.orbit` work. Mixing text and a parameter in one segment (`post-[id].orbit`, `[lang]-[version].orbit`), which Astro allows, isn't supported yet: it's a build error at the file, suggesting a whole-segment parameter. For `src/pages/blog/post-[id].orbit`:

```orbit file=src/pages/blog/post-[id].orbit expect-error
<h1>Post</h1>
```

```text
src/pages/blog/post-[id].orbit: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page
```

Unbalanced brackets and an empty parameter name are build errors too, such as `src/pages/[slug.orbit` and `src/pages/blog/[].orbit`:

```orbit file=src/pages/[slug.orbit expect-error
<h1>Post</h1>
```

```orbit file=src/pages/blog/[].orbit expect-error
<h1>Post</h1>
```

```text
src/pages/[slug.orbit: invalid route pattern "/[slug": segment "[slug" has unbalanced brackets
src/pages/blog/[].orbit: invalid route pattern "/blog/[]": segment "[]" has an empty parameter name
```

## Catch-all routes with `[...rest]`

A rest parameter, `[...name]`, matches **zero or more** segments. `src/pages/docs/[...path].orbit` matches `/docs`, `/docs/install` and `/docs/guides/routing`.

```orbit file=src/pages/docs/[...path].orbit
---
titles := map[string]string{
	"":               "Docs",
	"install":        "Install",
	"guides/routing": "Routing guide",
}
title, ok := titles[params.Path]
if !ok {
	return orbit.ErrNotFound
}
---
<h1>{title}</h1>
<p>Path: "{params.Path}"</p>
```

The value is the matched segments joined with `/`, with no leading or trailing slash, and percent-decoded like a `[param]`:

| URL | `params.Path` |
|---|---|
| `/docs` | `""` |
| `/docs/install` | `"install"` |
| `/docs/guides/routing` | `"guides/routing"` |

A rest parameter must be the last segment of the route. A rest parameter followed by more segments (`[...path]/edit.orbit`), which Astro allows, isn't supported yet either; it's a build error:

```orbit file=src/pages/docs/[...path]/edit.orbit expect-error
<h1>Edit</h1>
```

```text
src/pages/docs/[...path]/edit.orbit: rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead
```

A catch-all page answers **every method** under its prefix. See [Which HTTP methods Orbit handles](#which-http-methods-orbit-handles).

## Route params: `params`

Every page gets **`params`**, a struct that Orbit generates from the page's file path, with one `string` field per route parameter. For `src/pages/shop/[category]/[...rest].orbit`, Orbit generates, in the page's package:

```go
type Params struct {
	Category string // [category]
	Rest     string // [...rest]
}
```

So the page reads `params.Category` and `params.Rest`, as Astro pages read `Astro.params.category`. The compiler checks every use: a typo such as `{params.Categroy}` is a build error at its `.orbit` line, and your editor completes `params.` with the real field names.

A page whose route has no parameters, such as `index.orbit` or `404.orbit`, gets an empty `Params` (`struct{}`), so `params` always exists in a page.

### Field names

Each parameter becomes a field named in PascalCase, with the same rules as prop names (see [Template syntax](reference/template-syntax.md#name-matching)): the name is split into words at `-`, `_` and case changes, each word is capitalized, and common initialisms are written in capitals.

| File name part | Field |
|---|---|
| `[slug]` | `Slug` |
| `[...path]` | `Path` |
| `[id]` | `ID` |
| `[post-id]`, `[post_id]`, `[postId]` | `PostID` |
| `[api-url]` | `APIURL` |

Every field is a `string`. A `[param]` field holds one percent-decoded segment; a `[...rest]` field holds the matched segments joined with `/`, and `""` when the rest matched none (see [Catch-all routes](#catch-all-routes-with-rest)). Convert in the frontmatter when you need another type:

```orbit file=src/pages/posts/[id].orbit
---
import "strconv"

id, err := strconv.Atoi(params.ID)
if err != nil {
	return orbit.ErrNotFound
}
---
<h1>Post #{id}</h1>
```

A parameter name must start with a letter and use only letters, digits, `-` and `_`, so that it makes a Go field name. Two parameters of one route that give the same field name, such as `[post-id]` and `[postId]`, or `[id]` twice, are a build error at the file, and so is a name that isn't one. For `src/pages/[post-id]/[postId].orbit` and `src/pages/[1st].orbit`:

```orbit file=src/pages/[post-id]/[postId].orbit expect-error
<h1>Post</h1>
```

```orbit file=src/pages/[1st].orbit expect-error
<h1>Post</h1>
```

```text
src/pages/[post-id]/[postId].orbit: route params [post-id] and [postId] both become params.PostID; rename one
src/pages/[1st].orbit: route param [1st] can't become a Go field name; use letters, digits, - and _, starting with a letter
```

`Params` is generated for you, so a page can't declare its own `type Params`, and it can't redeclare `params` at the top of its frontmatter (see [Troubleshooting](troubleshooting.md#params-is-generated-from-the-pages-route-rename-this-type)).

### In components: `ctx.Param`

`params` exists only in pages: a component or layout can be used on many routes, so it has no route of its own. A component that needs a route param, such as a pagination bar or a language switcher, reads it by its bracket name with `ctx.Param`, which returns `""` when the current route has no such param:

```orbit file=src/components/LangSwitch.orbit
---
lang := ctx.Param("lang")
if lang == "" {
	lang = "en"
}
---
<nav>
  {if lang == "fr"}
    <a href="/en">English</a>
  {else}
    <a href="/fr">Français</a>
  {/if}
</nav>
```

A page and its components share one `ctx`, so `ctx.Param("slug")` in a component on `blog/[slug].orbit` returns the same value as the page's `params.Slug`. In a deferred (`server:defer`) component, which renders in its own request, `ctx.Param` returns `""` for every name; pass what it needs as props (see [Lazy loading](lazy-loading.md)).

## Route priority

When several routes could match a URL, Orbit compares them **segment by segment, from the left**. At each segment:

1. a static segment (`new`) beats
2. a `[param]`, which beats
3. a `[...rest]`.

If two routes are still tied, the one with more segments wins.

With these files:

```
src/pages/blog/index.orbit
src/pages/blog/new.orbit
src/pages/blog/[slug].orbit
src/pages/blog/[...rest].orbit
src/pages/[...rest].orbit
```

| URL | Matched file | Why |
|---|---|---|
| `/blog` | `blog/index.orbit` | the directory root |
| `/blog/new` | `blog/new.orbit` | static beats `[slug]` |
| `/blog/hello` | `blog/[slug].orbit` | `[slug]` beats `[...rest]` |
| `/blog/2026/10/hello` | `blog/[...rest].orbit` | only a rest parameter matches three segments |
| `/about` | `[...rest].orbit` | nothing else matches |

Files come before pages: a `public/` file or a `/_orbit/*` asset is looked up first, so `public/about` would win over `about.orbit` for `GET /about`.

Matching uses a precompiled segment tree, with no regular expressions, so the number of routes barely affects lookup time.

## The 404 page

`src/pages/404.orbit` is your not-found page. It renders per request, like any page, with status 404, whenever:

- a page returns `orbit.ErrNotFound`, as `blog/[slug].orbit` does for an unknown slug;
- the request reaches `site.NotFound()` because no page, asset or `public/` file matched. `site.Handler()` ends in it, and you can make it your mux's catch-all (see [Server](server.md)).

Because it renders per request, it has the real request in `ctx` and the `locals` your middleware set:

```orbit file=src/pages/404.orbit
<h1>Page not found</h1>
<p>Nothing lives at <code>{ctx.URL.Path}</code>. Try the <a href="/">home page</a>.</p>
```

The 404 page has no URL of its own. Without a `404.orbit`, the response is a 404 with an empty body (`Content-Length: 0`), as in Astro. `orbit new` scaffolds one.

The status is 404 unless the page calls `ctx.SetStatus` itself. If `404.orbit` returns `orbit.ErrNotFound`, the response is an empty 404 too.

Inside a deferred component (`server:defer`), `orbit.ErrNotFound` gives an empty 404 instead, because the browser is fetching a fragment, not a page (see [Lazy loading](lazy-loading.md)).

## Trailing slashes

Every page has exactly one canonical URL. The `trailingSlash` key of `orbit.json` decides whether it ends in `/` (see [Configuration](configuration.md#trailingslash)):

| | `"never"` (default) | `"always"` |
|---|---|---|
| `src/pages/blog/hello.orbit` | `/blog/hello` | `/blog/hello/` |
| `src/pages/blog/index.orbit` | `/blog` | `/blog/` |
| `src/pages/index.orbit` | `/` | `/` |
| `src/pages/rss.xml.orbit` | `/rss.xml` | `/rss.xml` |

A request for the other form of a page URL gets a **308 Permanent Redirect** to the canonical one, for every method:

```
$ curl -i http://localhost:4321/blog/hello/
HTTP/1.1 308 Permanent Redirect
Location: /blog/hello
```

In `"always"`, it's the mirror image: `/blog/hello` gets a 308 to `/blog/hello/`. The redirect has no body, only the `Location` header.

- **Query kept**: `/search/?q=go` goes to `/search?q=go` (`/search?q=go` to `/search/?q=go` in `"always"`), as `net/http`'s `ServeMux` does for its own redirects. A 308 also keeps the method and body, so a form that posts to the other form of the URL still works, at the cost of an extra round trip.
- **Pages only**: the redirect is sent only when the other form of the path is a page URL. Otherwise the request goes to your next handler untouched, so your own `/api/items/` route keeps working when Orbit comes first (`/api/items` in `"always"`). The exception is a page that matches it, such as a root `[...rest].orbit`: then `/api/items` in `"always"` is redirected to `/api/items/`, as `/api/items/` is redirected to `/api/items` by default.
- **Never redirected**: `/`, paths under `/_orbit/` (assets and `server:defer` islands), files from `public/`, and paths starting with `//`. In `"always"`, a path whose last segment has a file extension (`/feed.xml`, `/v1.2`) isn't redirected either.
- **Extension pages**: a page whose last path segment has a file extension, such as `src/pages/rss.xml.orbit` or `src/pages/api/data.json.orbit`, is at `/rss.xml` in both modes, like an Astro endpoint; `/rss.xml/` matches nothing. An extension is a `.` that isn't the segment's first character, followed by letters, digits or `_` only.
- **Dotted values**: in `"always"`, a `[slug]` value with a dot follows the same rule: `/blog/v1.2/` renders the page with `slug` `"v1.2"`, while `/blog/v1.2` matches nothing and isn't redirected.
- **No collapsing**: `/blog//` matches nothing in either mode; it reaches your next handler (a 404).
- **Same everywhere**: `orbit dev` and `dist/server` behave the same way.
- **Write canonical links**: Orbit doesn't rewrite the `href`s in your templates, so write `href="/blog/hello"` (`href="/blog/{slug}/"` in `"always"`) to skip the redirect. Markdown's `Post.URL` and rewritten `.md` links already follow the mode (see [Markdown](markdown.md#links-between-markdown-files)).

Params come from the path without its slash: in `"always"`, `[slug]` at `/blog/hello/` is `"hello"`, `[...path]` at `/docs/a/b/` is `"a/b"`, and at `/docs/` it's `""`. `ctx.URL` is the request URL as received, slash included.

### Redirecting before your middleware

`site.Middleware` (and `site.Handler()`) redirects when the request reaches Orbit, so middleware that wraps Orbit runs first and sees both `/blog/hello/` and `/blog/hello`: an auth gate would answer a signed-out visitor on the other form with its own redirect, and the visitor would land on the wrong form after signing in. Astro redirects before its middleware. To get Astro's order, wrap your whole handler in `site.TrailingSlash`, outermost (`orbit new`'s `cmd/server/main.go` does):

```go
site := app.New()
mux := http.NewServeMux()
mux.Handle("/", site.Handler())

// logging sees the 308s too; auth never sees the other form.
h := logging(site.TrailingSlash(auth(mux)))
```

- **Same rule**: it sends exactly the 308 `site.Middleware` would (same pages, same exclusions, same `Location`), and `site.Middleware` keeps its own redirect, so a request is never redirected twice.
- **Pages only**: like `site.Middleware`'s, it redirects only paths whose other form is a page URL. Your own mux routes (`/api/items`) reach `next` untouched in either form; if you register a route in one form only and want the other redirected, do it in your handler.
- **Free on canonical URLs**: a path already in its canonical form is passed on after a check of its last byte, with no allocation; a non-canonical one costs a file and route lookup (~35 ns, no allocation) unless it's redirected.
- **Under a prefix** (`http.StripPrefix`), put it inside, on the path Orbit sees.

See [Middleware and locals](middleware.md#where-middleware-goes).

## Which HTTP methods Orbit handles

Orbit is a `net/http` middleware, and it only claims requests it can answer. What it claims depends on the target:

| Request target | Methods Orbit handles | Other methods |
|---|---|---|
| Pages | all methods | — |
| `/_orbit/*` assets, `public/` files | `GET`, `HEAD` | passed to the next handler |
| `/_orbit/island/…` (deferred components) | `GET`, `HEAD`, `POST` | 405, with `Allow: GET, HEAD, POST` |
| Anything else | — | passed to the next handler |

A page receives every method and reads it from `ctx.Request.Method`; see [Handling forms and other methods](rendering.md#handling-forms-and-other-methods).

So a `POST /blog/hello/like` handler in your own mux works even when Orbit comes first in the chain: `[slug]` matches one segment, so no page matches `/blog/hello/like`. A `[...rest]` page is different: it claims **every method under its prefix**. With `src/pages/app/[...rest].orbit`, a `POST /app/save` goes to the page, not to your mux. If you put Orbit first (see [Choosing precedence](server.md#choosing-precedence)), keep your handlers outside the prefixes of catch-all pages.

## See also

- [Rendering](rendering.md): the request lifecycle, status codes, redirects and errors
- [Server](server.md): where Orbit sits in your handler chain
- [Middleware and locals](middleware.md): net/http middleware and typed `Locals`
- [Components](components.md): layouts, props, templates and the names a page gets (`params`, `ctx`)
- [Lazy loading](lazy-loading.md): the `/_orbit/island/` endpoint
