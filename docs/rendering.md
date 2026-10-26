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
   3. a page route, which may answer the other form of a page URL (`/blog/hello/` by default) with a 308 (see [Routing](routing.md#trailing-slashes)).

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
- `return err` with any other error stops rendering and answers 500, unless your [error handler](#answering-errors-yourself-witherrorhandler) answers it.
- A panic in a page or component is recovered per request and also answers 500 (or goes to the error handler).

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

In `orbit dev`, you get an error overlay with the `.orbit` file and line instead (see [Dev server](dev-server.md)). A custom `src/pages/500.orbit` error page is planned for a later release.

Because responses are buffered, a failed render never sends half a page: the visitor gets a clean error response.

Inside a deferred component, errors don't replace the page: the island request fails and the fallback stays. See [Lazy loading](lazy-loading.md).

### Answering errors yourself: `WithErrorHandler`

Your data layer often fails in ways that deserve a real answer rather than a 500: a reader who must sign in first, a backend that's down for a minute. Give `app.New` an error handler, and Orbit hands it every error that would otherwise become the 500:

```go file=internal/access/access.go
// Package access holds the errors the error handler example maps to
// responses.
package access

import "errors"

// ErrUnavailable means the backing service is down.
var ErrUnavailable = errors.New("library: unavailable")

// Refused means the reader must sign in first.
type Refused struct{ Reason string }

func (e *Refused) Error() string { return "access refused: " + e.Reason }
```

```orbit file=src/pages/members.orbit
---
import "example.com/blog/internal/access"

if _, err := ctx.Request.Cookie("session"); err != nil {
	return &access.Refused{Reason: "no session"}
}
---
<h1>Members</h1>
```

```go file=cmd/server/main.go
package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/orbit-z/orbit"

	"example.com/blog/internal/access"
	"example.com/blog/orbit_gen/app"
)

func main() {
	site := app.New(orbit.WithErrorHandler(answerError))
	if err := orbit.ListenAndServe(site.Handler()); err != nil {
		log.Fatal(err)
	}
}

// answerError maps the app's errors to responses. It returns false for any
// other error, which then gets Orbit's built-in 500 and its log line.
func answerError(w http.ResponseWriter, r *http.Request, err error) bool {
	var refused *access.Refused
	switch {
	case errors.As(err, &refused):
		http.Redirect(w, r, "/signin?next="+url.QueryEscape(pageURL(r)), http.StatusSeeOther)
	case errors.Is(err, access.ErrUnavailable):
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "The library is unavailable right now. Try again in a minute.", http.StatusServiceUnavailable)
	default:
		return false
	}
	return true
}

// pageURL is the page the reader was on: for a deferred island's request,
// its Referer.
func pageURL(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/_orbit/island/") {
		return r.Referer()
	}
	return r.URL.RequestURI()
}
```

The rules:

- **What reaches it**: every error that would become the 500, from a page or any component it renders, at any depth (the `404.orbit` page and [deferred islands](lazy-loading.md#status-codes-and-errors) included), and recovered panics. `orbit.ErrNotFound` and `ctx.Redirect(…)` never reach it: they keep their 404 and redirect, however the handler is written. The island endpoint's own 400 and 413 (a bad token, props that don't decode, a body over 1 MiB) aren't render errors and don't reach it either.
- **`true` or `false`**: return `true` when the handler wrote the response, `false` to let Orbit answer with the built-in 500 and its log line.
- **Your error is still there**: the error carries the `.orbit` position of the `return` that returned it (its text starts with `src/pages/members.orbit:5:2: `), but `errors.Is` and `errors.As` see through it, through any number of components and through `errors.Join`. A panic reaches the handler as an error whose text names the `.orbit` line (`src/pages/x.orbit:7: panic: …`); when the panic value is an `error`, `errors.As` finds it too.
- **Nothing has been written yet**: responses are buffered, so the handler can send any status, headers and body. The page's partial output and the headers it set with `ctx.Header()` are dropped, as for the 500. Headers your middleware set on the response before calling Orbit stay.
- **`r`** is the request Orbit was serving: the page request, or the island request, whose page is in `r.Referer()`. Locals your middleware attached are there (`orbit.LocalsFromRequest`).
- **Logging is yours**: Orbit logs only the errors it answers itself. Log in the handler when you want a line, as `answerError` does for the 503.
- **The same in `orbit dev`**: the handler runs in development too; only the errors it declines show the dev error page.
- **Not recovered**: a panic in the handler itself goes to `net/http`, which logs it and drops the connection.
- **One handler**: with several `WithErrorHandler` options, the last one wins.

In your own handlers, `site.ServeError(w, r, err)` answers an error exactly as a page's would be: a redirect, `404.orbit`, your error handler, then the 500. See [Rendering components from handlers](server.md#rendering-components-from-handlers).

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

## Slow components

A buffered page waits for its slowest part, and by default Orbit renders components in order, one after the other: two components that each wait 10 ms for their own data make the page take 20 ms. There are three ways to deal with slow parts:

- **Load the data in the page** (recommended): the page's frontmatter runs its fetches at once with [`orbit.All`](data-loading.md) and passes the results down as props. Components stay fast and simple to test, and the page owns its queries. Data that several components need can come from a per-request [loader in `locals`](data-loading.md#share-data-without-props-drilling-a-loader-in-locals). See [Loading data](data-loading.md).
- **`server:concurrent`** is for components that fetch their own data, such as code ported from Astro: it overlaps them in the same response. Each marked component renders in its own goroutine, starting when the page reaches it, while the page goes on; Orbit waits for all of them and assembles the HTML in source order. The page still waits for its slowest part, but slow parts no longer add up.
- **`server:defer`** takes a part off the first byte: the page is sent with a fallback, and the part arrives in a second request. See [Lazy loading](lazy-loading.md#which-one-serverdefer-or-serverconcurrent) for which to pick.

A component that fetches its own data:

```orbit file=src/components/Weather.orbit
---
import "time"

type Props struct {
	City string
}

time.Sleep(10 * time.Millisecond) // stands in for a weather API call
forecast := "sunny"
---
<p>{props.City}: {forecast}</p>
```

```orbit file=src/components/LatestPosts.orbit
---
import "time"

type Props struct {
	Limit int
}

time.Sleep(10 * time.Millisecond) // stands in for a database query
titles := []string{"Hello, world", "A second post"}[:min(props.Limit, 2)]
---
<ul>
  {for _, t := range titles}
    <li>{t}</li>
  {/for}
</ul>
```

Mark both on the page, and it renders in about 10 ms instead of 20, with the same HTML:

```orbit file=src/pages/index.orbit
---
import (
	LatestPosts "../components/LatestPosts.orbit"
	Weather "../components/Weather.orbit"
)
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Home</title>
  </head>
  <body>
    <Weather server:concurrent city="Paris" />
    <LatestPosts server:concurrent limit={5} />
    <footer>…</footer>
  </body>
</html>
```

The rules:

- **Mark every slow component.** Only marked components leave the page's goroutine: in `<A server:concurrent /><B />`, A and B overlap, but in `<A /><B server:concurrent />`, B starts only after A is done.
- **Don't mark fast ones.** A marked component costs about half a microsecond and one small allocation; that's nothing against a 10 ms fetch and a lot against a 200 ns card.
- **A marked component's subtree** (its children, its slot content) renders in its goroutine too, and can contain marked components of its own. Marked components in different parents (one in the header, one in the body) overlap as well.
- **Errors** come out in source order, exactly as the in-order render would report them: the first failing component in the HTML wins, whatever failed first in time. Redirects and `orbit.ErrNotFound` work as usual, and so does [`WithErrorHandler`](#answering-errors-yourself-witherrorhandler). Orbit doesn't cancel the other components when one fails, so a component after the failing one may already have run its fetch; the response waits for every marked component that started.
- **Status and headers** set with `ctx.SetStatus` and `ctx.Header()` in a marked component are applied when the page is assembled, in source order, after the page's own: a marked component's status replaces the page's, and each header it sets replaces the page's values for that key.
- **A limit per render**: at most 16 marked components run at once, counted across nesting. Past that, a marked component renders in order on the goroutine that reaches it, with the same output. Change it with `orbit.WithConcurrencyLimit(n)` in `app.New(…)` (0 renders every marked component in order). A marked component in a `{for}` loop is allowed, but it's usually a sign that one query in the page would do better than one per item.
- **Your code must be safe to run concurrently.** See [Fetching data in components](components.md#fetching-data-in-components) for the data-race rules.

## Caching fragments

`orbit.RenderHTML(ctx, slots.Default)` renders a slot, or any renderer, to an `orbit.HTML` string instead of into the page (see [Rendering a slot to a string](components.md#rendering-a-slot-to-a-string)). That's the building block of a fragment cache: a component that renders its children once, keeps the HTML for a while, and writes the cached copy on later requests. Orbit has no cache of its own; the pattern below is a few dozen lines of your code.

The cache, with a time to live, a size cap, and single-flight, so that concurrent misses for one key render once:

```go file=internal/htmlcache/htmlcache.go
// Package htmlcache is a small in-memory cache of rendered HTML fragments.
package htmlcache

import (
	"errors"
	"sync"
	"time"

	"github.com/orbit-z/orbit"
)

// Fragments is the process's fragment cache.
var Fragments = New(10_000)

// Cache maps keys to rendered HTML. It's safe for concurrent use.
type Cache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*entry
}

type entry struct {
	done    chan struct{} // closed once html and err are set
	html    orbit.HTML
	err     error
	expires time.Time
}

var errPanicked = errors.New("htmlcache: render panicked")

// New returns a cache that holds up to max entries.
func New(max int) *Cache {
	return &Cache{max: max, entries: make(map[string]*entry)}
}

// Get returns the HTML cached for key, or calls render and caches its
// result for ttl. Concurrent calls for a missing key wait for one render.
// Errors aren't cached.
func (c *Cache) Get(key string, ttl time.Duration, render func() (orbit.HTML, error)) (orbit.HTML, error) {
	for {
		c.mu.Lock()
		e, ok := c.entries[key]
		if !ok {
			if len(c.entries) >= c.max {
				clear(c.entries) // a crude cap: start over
			}
			e = &entry{done: make(chan struct{})}
			c.entries[key] = e
			c.mu.Unlock()
			c.fill(key, e, ttl, render)
			return e.html, e.err
		}
		c.mu.Unlock()
		<-e.done
		if e.err != nil || time.Now().Before(e.expires) {
			return e.html, e.err
		}
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key) // expired: the next turn renders it again
		}
		c.mu.Unlock()
	}
}

// fill renders e. A failed or panicking render is removed, so the next
// request tries again, and its waiters are always released.
func (c *Cache) fill(key string, e *entry, ttl time.Duration, render func() (orbit.HTML, error)) {
	defer func() {
		c.mu.Lock()
		if e.err != nil && c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		close(e.done)
	}()
	e.err = errPanicked // replaced unless render panics
	e.html, e.err = render()
	e.expires = time.Now().Add(ttl)
}
```

The component; its `key` must hold everything its children's HTML depends on:

```orbit file=src/components/Cached.orbit
---
import (
	"time"

	"example.com/blog/internal/htmlcache"
)

type Props struct {
	Key string        // everything the children's HTML depends on
	TTL time.Duration // how long a copy is served
}

html, err := htmlcache.Fragments.Get(props.Key, props.TTL, func() (orbit.HTML, error) {
	return orbit.RenderHTML(ctx, slots.Default)
})
if err != nil {
	return err
}
---
<Fragment set:html={html} />
```

And a page using it:

```orbit file=src/pages/archive.orbit
---
import (
	"time"

	Cached "../components/Cached.orbit"
	"example.com/blog/internal/posts"
)

all, err := posts.All()
if err != nil {
	return err
}
---
<h1>Archive</h1>
<Cached key="archive:v1" ttl={time.Hour}>
  <ul>
    {for _, p := range all}
      <li><a href={"/blog/" + p.Slug}>{p.Title}</a></li>
    {/for}
  </ul>
</Cached>
```

On a hit, the children don't render at all (in Astro, the slot is built on a hit too). The rules that keep this safe:

- **Never cache per-user content.** The key must hold everything the HTML depends on (a data version, the locale), and the cached children must not read cookies, the session or per-user `locals`, or one visitor's page ends up in another's. Orbit can't check this for you. If you want a guard, set a flag on your `locals` around the render (`locals.Shared = true`, reset with `defer`) and make your per-user accessors refuse while it's set.
- **Per-user parts go outside** the cached component, or inside it with `server:defer`: a deferred placeholder is the same for every visitor, and the browser loads its content per visitor, through your middleware (see [Lazy loading](lazy-loading.md#serverdefer-inside-cached-fragments)).
- **Side effects run only on a miss**: `ctx.SetStatus`, `ctx.Header()` and `locals` writes inside the cached children happen when they render, not when a cached copy is served.
- **Assets need nothing**: the children's components are imported by the file that renders `<Cached>`, so their CSS and scripts are already in the page's bundles, and `client:*` markers keep working in a cached copy.
- **No `</head>` inside**: the page's `<link>` and `<script>` tags go at the page's own `</head>`, never into a fragment, so a `</head>` inside `orbit.RenderHTML` is a render error.
- **The render shares the request's `ctx`** only while it runs; the cache keeps only the HTML.
- **Measure first.** A hit costs a map lookup and a copy into the page (about 20 ns for 2 KB) and a miss one allocation on top of the render, but writing static markup in place is cheaper still. Caching pays off when the children need slow data or heavy work.

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
- [Template syntax](reference/template-syntax.md#serverconcurrent): `server:concurrent` and its compile errors
- [Runtime API](reference/runtime-api.md): `WithErrorHandler`, `ServeError` and `RenderHTML`
- [Deploy](deploy.md): `dist/server`, `--embed` and Docker
