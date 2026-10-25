# Routing

Every `.orbit` file in `src/pages/` is a page, its path in that directory is its URL, and it renders on every request.

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

The file path under `src/pages/` becomes the URL. `index.orbit` is the root of its directory, and URLs have no trailing slash (see [Trailing slashes](#trailing-slashes)).

| File | URL |
|---|---|
| `src/pages/index.orbit` | `/` |
| `src/pages/about.orbit` | `/about` |
| `src/pages/blog/index.orbit` | `/blog` |
| `src/pages/blog/[slug].orbit` | `/blog/hello`, `/blog/routing`, … |
| `src/pages/docs/[...path].orbit` | `/docs`, `/docs/install`, `/docs/guides/routing`, … |
| `src/pages/404.orbit` | none: it answers any URL nothing else matches |

Every `.orbit` file under `src/pages/` is a route. Keep reusable pieces in `src/components/` or `src/layouts/` and import them.

Pages render on every request; Orbit never writes HTML files at build time (see [Rendering](rendering.md)). Pages also don't take props: they read their route params from `params` and the rest of the URL from `ctx`, and declaring `type Props` in a page is a build error.

Route errors are about a file, not a position in it, so they name the file without a line and column. Two files that produce the same route are a build error that names both files. That covers `src/pages/blog.orbit` next to `src/pages/blog/index.orbit`, and `[slug].orbit` next to `[id].orbit` in one directory.

The `/_orbit/` prefix is reserved for Orbit's assets and islands, so a page under `src/pages/_orbit/` is a build error:

```text
src/pages/_orbit/stats.orbit: route /_orbit/stats is under /_orbit/, which Orbit reserves for its assets and islands; rename the page or its directory
```

## Dynamic routes with `[param]`

A path segment in square brackets is a parameter. `src/pages/blog/[slug].orbit` matches `/blog/hello` and `/blog/anything-else`. A `[param]` matches exactly one non-empty path segment, so it never matches `/blog/hello/like`.

Read the value from `params.Slug`, a field of the page's generated `params` (see [Route params](#route-params-params)):

```orbit
<p>You asked for {params.Slug}</p>
```

The value is percent-decoded, as `ServeMux`'s `PathValue` is: `/blog/caf%C3%A9` gives `"café"`.

Any value reaches the page, so the page decides what exists: look it up, and `return orbit.ErrNotFound` when it's missing, as in the first example.

A parameter is always a whole segment: `[slug].orbit` and `[slug]/index.orbit` work. Mixing text and a parameter in one segment (`post-[id].orbit`, `[lang]-[version].orbit`), which Astro allows, isn't supported yet: it's a build error at the file, suggesting a whole-segment parameter:

```text
src/pages/blog/post-[id].orbit: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page
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

A parameter name must start with a letter and use only letters, digits, `-` and `_`, so that it makes a Go field name. Two parameters of one route that give the same field name, such as `[post-id]` and `[postId]`, or `[id]` twice, are a build error at the file:

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

The 404 page has no URL of its own. Without a `404.orbit`, the response is a 404 with an empty body, as in Astro. `orbit new` scaffolds one.

Inside a deferred component (`server:defer`), `orbit.ErrNotFound` gives an empty 404 instead, because the browser is fetching a fragment, not a page (see [Lazy loading](lazy-loading.md)).

## Trailing slashes

Every page has one canonical URL, and it has no trailing slash: `/blog/hello`, and `/blog` for `blog/index.orbit`. `/` stays `/`.

A request with a trailing slash gets a **308 Permanent Redirect** to the canonical URL:

```
$ curl -i http://localhost:4321/blog/hello/
HTTP/1.1 308 Permanent Redirect
Location: /blog/hello
```

- **Query kept**: `/search/?q=go` goes to `/search?q=go`, as `net/http`'s `ServeMux` does for its own redirects. A 308 also keeps the method and body, so a form that posts to `/subscribe/` still works, at the cost of an extra round trip.
- **Pages only**: the redirect is sent only when the path without its slash matches a page. Otherwise the request goes to your next handler untouched, so your own `/api/items/` route keeps working when Orbit comes first.
- **Files never**: paths under `/_orbit/` and files from `public/` are never redirected.
- **Same everywhere**: `orbit dev` and `dist/server` behave the same way.
- **Write canonical links**: use `href="/blog/hello"` in your templates to skip the redirect.

Middleware that wraps Orbit runs before the redirect, so it can see `/blog/hello/` as well as `/blog/hello`. See [Middleware and locals](middleware.md).

This behavior is fixed. Astro's `trailingSlash` modes are not yet supported.

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
