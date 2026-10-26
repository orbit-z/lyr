# Rendering

Every Orbit page renders on the server, on every request, into a buffer that is sent in one write.

```orbit file=src/pages/search.orbit
---
import (
	"strings"

	"example.com/blog/internal/posts"
)

q := strings.TrimSpace(ctx.URL.Query().Get("q"))
all, err := posts.All()
if err != nil {
	return err
}
var results []posts.Post
for _, p := range all {
	if q != "" && strings.Contains(strings.ToLower(p.Title), strings.ToLower(q)) {
		results = append(results, p)
	}
}
---
<form action="/search">
  <input type="search" name="q" value={q}>
  <button>Search</button>
</form>
{if q != "" && len(results) == 0}
  <p>No posts match "{q}".</p>
{/if}
<ul>
  {for _, p := range results}
    <li><a href={"/blog/" + p.Slug}>{p.Title}</a></li>
  {/for}
</ul>
```

The frontmatter runs for each request, so the page can read the query string; nothing needs to be switched on. The examples on this page leave out the layout to keep them short; see [Components](components.md).

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

## One mode: server rendering

Orbit is SSR-only. There's no static-site generation: no prerendering, no `StaticPaths`, no HTML files written at build time. Every page, including `404.orbit`, runs its frontmatter and template per request, in `orbit dev` and in production alike.

The only files Orbit serves as-is are its bundled assets under `/_orbit/` and your `public/` files.

If your site is fully static, Astro is the better tool. Orbit is for sites that render per request and want that to be fast.

## The request lifecycle

For a page request, the path through your server looks like this:

1. **Your middleware** in `cmd/server/main.go` runs first, if it wraps Orbit: logging, cookies, setting typed locals with `orbit.EnsureLocals` (see [Middleware and locals](middleware.md)).
2. **`site.Middleware`** matches the URL. In order, it tries:
   1. the deferred-island endpoint `/_orbit/island/…` (see [Lazy loading](lazy-loading.md));
   2. a static file: a `/_orbit/*` asset or a `public/` file (`GET` and `HEAD` only);
   3. a page route, which may answer a trailing slash with a 308 (see [Routing](routing.md#trailing-slashes)).

   If nothing matches, it calls the next handler untouched (see [Server](server.md)).
3. **The page renders**: it gets a `ctx` and the request's `locals`, runs its frontmatter and template, and writes into a pooled buffer. Components it uses render into the same buffer.
4. **Head injection**: Orbit inserts the page's `<link>` and `<script>` tags before `</head>`.
5. **One write**: the response goes out with its status, headers and `Content-Length`.

If the frontmatter returns early, with a redirect, `orbit.ErrNotFound` or an error, steps 3 to 5 send that response instead (see below).

## The request: `ctx`

Every page and component gets `ctx`, an `*orbit.Ctx`:

| Field | Holds |
|---|---|
| `ctx.Request` | the `*http.Request`, with its method, headers, cookies, form and `Context()` |
| `ctx.URL` | the request URL, including the query string |
| `ctx.Site` | the `site` URL from [`orbit.json`](configuration.md) |

`ctx.Param(name)` returns a route parameter by its bracket name, or `""` when the route has none by that name. Pages rarely need it: a page gets its route parameters as `params`, a struct Orbit generates from the file path (`params.Slug` for `blog/[slug].orbit`; see [Route params](routing.md#route-params-params)). Components use `ctx.Param`.

Next to `ctx`, the frontmatter has `locals`, your typed request-scoped data (see [Middleware and locals](middleware.md#read-locals-in-pages-and-components)).

Pass `ctx.Request.Context()` to database and HTTP calls, so they stop when the visitor goes away.

`ctx` is pooled and reused across requests. Don't keep it, or anything it points to, after the page finishes rendering.

In a deferred component (`server:defer`), `ctx.Request` and `ctx.URL` are the island's own request, not the page's; the page URL is in `ctx.Request.Referer()`. See [Lazy loading](lazy-loading.md).

## Handling forms and other methods

A page receives every HTTP method (see [Routing](routing.md#which-http-methods-orbit-handles)). Check `ctx.Request.Method` and read the form as in any Go handler:

```orbit file=src/pages/subscribe.orbit
---
import (
	"log"
	"net/http"
	"net/mail"
)

var errMsg string
if ctx.Request.Method == http.MethodPost {
	addr, err := mail.ParseAddress(ctx.Request.FormValue("email"))
	if err == nil {
		log.Printf("new subscriber: %s", addr.Address)
		return ctx.Redirect("/subscribe?ok=1", http.StatusSeeOther)
	}
	ctx.SetStatus(http.StatusUnprocessableEntity)
	errMsg = "Please enter a valid email address."
}
ok := ctx.URL.Query().Get("ok") == "1"
---
{if ok}
  <p>Thanks, you're subscribed.</p>
{else}
  <form method="post">
    <input type="email" name="email" required>
    <button>Subscribe</button>
  </form>
  {if errMsg != ""}
    <p class="error">{errMsg}</p>
  {/if}
{/if}
```

This uses the tools from the next sections: a redirect, a status code, and a template that renders on the same request.

## Redirects

Return `ctx.Redirect(to, code)` from the frontmatter, here in `src/pages/blog/[slug].orbit`:

```go
if params.Slug == "hello-orbit" {
    return ctx.Redirect("/blog/hello", http.StatusMovedPermanently)
}
```

`Redirect` doesn't write anything itself: it returns an `*orbit.RedirectError` (with `Location` and `Code`), and returning that error ends the frontmatter and sends the redirect. The template doesn't render. Remember the `return`.

Headers the page set before returning, such as a `Set-Cookie` from a login form, are sent with the redirect. The response is written by `http.Redirect`, so a relative `Location` (`"../hello"`) is resolved against the request path, as in any Go handler.

## Status codes and errors

The status is 200 unless you change it:

- `ctx.SetStatus(code)` sets it, and the template still renders. The form example above uses it for a 422 with an error message.
- `return orbit.ErrNotFound` stops rendering and answers with your `404.orbit` page and status 404 (see [Routing](routing.md#the-404-page)).
- `return err` with any other error stops rendering and answers 500.
- A panic in a page or component is recovered per request and also answers 500.

In production, the visitor gets a minimal built-in page, `500 Internal Server Error`, which never shows the error. The error itself, with its `.orbit` position, is logged to stderr with Go's `log` package:

```text
orbit: GET /blog/hello: src/pages/blog/[slug].orbit:14:5: posts: connection refused
```

The position is the frontmatter `return` that returned the error: here, line 14 is `return fmt.Errorf("posts: %w", err)`. Orbit adds it when it compiles the page, to every `return` of the frontmatter (not to returns inside a `func` you declare there). The original error is still in the chain, so `errors.Is` and `errors.As` work on it. `orbit.ErrNotFound` and `ctx.Redirect(…)` are returned as is, and an error that already has a position, such as one returned by a component's `Render`, isn't prefixed twice.

A panic is logged at the innermost `.orbit` line on the stack, with the file relative to the server's working directory, and without a stack trace:

```text
orbit: GET /blog/hello: src/pages/blog/[slug].orbit:14: panic: runtime error: index out of range [3] with length 3
```

The headers a page set with `ctx.Header()` are dropped from a 500, and from the 404 that `orbit.ErrNotFound` sends; only a redirect keeps them.

In `orbit dev`, you get an error overlay with the `.orbit` file and line instead (see [Dev server](dev-server.md)). A custom `src/pages/500.orbit` error page is planned after the first release.

Because responses are buffered, a failed render never sends half a page: the visitor gets a clean error response.

Inside a deferred component, errors don't replace the page: the island request fails and the fallback stays. See [Lazy loading](lazy-loading.md).

## Response headers

Set headers with `ctx.Header()`, an `http.Header`:

```go
ctx.Header().Set("Cache-Control", "private, max-age=60")
```

Pages get `Content-Type: text/html; charset=utf-8` unless the page sets another one, or middleware around Orbit set one on the response before calling it. Middleware around Orbit can add headers too (see [Middleware and locals](middleware.md)).

Orbit doesn't compress responses or cache rendered pages. Add a gzip middleware and `Cache-Control` headers yourself; caching of SSR responses is not yet supported.

## Responses are always buffered

Orbit renders a page completely into a pooled in-memory buffer, then writes the response once, with a `Content-Length` header. It never streams a page. This is a deliberate trade-off:

- **Speed**: one write per response and no chunked encoding. Buffers come from a pool, so a typical render allocates almost nothing.
- **Middleware**: a middleware that wraps the `http.ResponseWriter` sees the finished HTML, with assets already injected, and can change the status, headers or body (see [Middleware and locals](middleware.md#rewrite-responses)).
- **Clean errors**: nothing goes out until rendering succeeds, so a page that fails halfway never sends half a page with a 200.

The cost is that the first byte leaves only when the whole page is ready: one slow database query delays the entire page. For that, defer the slow part with `server:defer`. The page renders the component's fallback and is sent at once, and the browser fetches the component's HTML in its own request:

```orbit
<Comments server:defer postID={post.Slug}><p slot="fallback">Loading comments…</p></Comments>
```

See [Lazy loading](lazy-loading.md) for the details, and [Performance](performance.md) for the numbers.

For true streaming (server-sent events, large downloads), write a regular handler in your [server](server.md); Orbit doesn't touch it.

## The build output: `dist/server` and `dist/client`

`orbit build` produces your server binary and the files it serves:

```
dist/
  server                your cmd/server, built
  client/
    _orbit/…            hashed CSS and JS bundles
    robots.txt          copied from public/
```

There are no HTML files: the binary renders every page. Run it with `./dist/server`. It finds `dist/client` next to the binary; `ORBIT_STATIC` overrides the location. `orbit build --embed` puts `dist/client` inside the binary, so you can deploy one file. See [Deploy](deploy.md).

## See also

- [Routing](routing.md): dynamic routes, the 404 page and trailing slashes
- [Middleware and locals](middleware.md): code that runs around every page
- [Server](server.md): the `cmd/server` entry point
- [Lazy loading](lazy-loading.md): `server:defer` for slow parts of a page
- [Deploy](deploy.md): `dist/server`, `--embed` and Docker
