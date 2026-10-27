# Middleware and locals

Orbit has one middleware model, plain `net/http` middleware in `cmd/server/main.go`, and it hands request data to your pages through a typed `Locals` struct.

```go file=src/locals/locals.go
// Package locals declares the request-scoped data that middleware hands to pages.
package locals

import "github.com/orbit-z/orbit"

type Locals struct {
	RequestID string
	Theme     string          // "light" or "dark"
	Route     orbit.RouteInfo // filled by Orbit: see Logging the matched route
}
```

```go file=cmd/server/main.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/orbit-z/orbit"

	"example.com/blog/orbit_gen/app"
	"example.com/blog/src/locals"
)

func main() {
	site := app.New()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/prefs", prefs)
	mux.Handle("/", site.Handler())

	// logging runs first, then requestInfo, then the mux and Orbit.
	if err := orbit.ListenAndServe(logging(requestInfo(mux))); err != nil {
		log.Fatal(err)
	}
}

// requestInfo gives each request an ID and reads the theme cookie.
func requestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.RequestID = rand.Text()
		l.Theme = "light"
		if c, err := r.Cookie("theme"); err == nil && c.Value == "dark" {
			l.Theme = "dark"
		}
		w.Header().Set("X-Request-ID", l.RequestID)
		next.ServeHTTP(w, r)
	})
}

// logging logs each request with its status, duration and request ID.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s id=%s", r.Method, r.URL.Path, rec.status, time.Since(start), l.RequestID)
	})
}

// statusRecorder remembers the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// prefs is a regular handler that reads the same typed locals.
func prefs(w http.ResponseWriter, r *http.Request) {
	l := orbit.LocalsFromRequest[locals.Locals](r)
	if l == nil {
		http.Error(w, "locals not set: wrap this handler with requestInfo", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"theme": l.Theme}); err != nil {
		log.Printf("prefs: %v", err)
	}
}
```

```orbit file=src/pages/account.orbit
---
import Layout "../layouts/Base.orbit"
---
<Layout title="Your account">
  <h1>Your account</h1>
  <p>Theme: {locals.Theme}</p>
  <p>Request ID: <code>{locals.RequestID}</code></p>
</Layout>
```

`curl -b theme=dark localhost:4321/account` shows `dark` and a fresh request ID, and the log line for that request carries the same ID. Pages read `locals.Theme` fully type-checked: a typo such as `locals.Them` fails `orbit build` at that line of the `.orbit` file, and your editor completes `locals.`.

## Writing middleware

A middleware is a `func(http.Handler) http.Handler`, the standard Go shape. It can work in three places:

- **Before `next.ServeHTTP`**: read the request, set response headers (`requestInfo` sets `X-Request-ID`), and fill in locals.
- **Instead of `next.ServeHTTP`**: write a response itself and stop. See [Short-circuit responses](#short-circuit-responses).
- **After `next.ServeHTTP`**: look at what was written, through a wrapping `http.ResponseWriter` (`logging` records the status). See [Rewrite responses](#rewrite-responses).

There's no Orbit-specific middleware type. Anything from the Go ecosystem (CORS, gzip, rate limits, sessions) plugs in the same way.

## Where middleware goes

Where you put a middleware relative to Orbit decides what it sees:

- **Outside Orbit**, wrapping `site.Middleware` or the mux that leads to `site.Handler()`, as above: it sees every request, pages, island requests, assets, `public/` files and your own handlers alike. Middleware that sets locals for pages must go here, so it runs before Orbit.
- **After Orbit**, inside it (`site.Middleware(cors(mux))`): it only sees requests Orbit didn't handle. Locals it sets never reach a page.

Middleware listed first runs first: in `logging(requestInfo(mux))`, `logging` sees the request before `requestInfo`, and sees the response after it. See [Server](server.md#choosing-precedence) for where the mux and Orbit go.

Outer middleware also runs before Orbit's trailing-slash redirect, so it can see `/account/` as well as `/account` (whichever isn't the canonical URL is redirected afterwards, per `orbit.json`'s `trailingSlash`). Compare paths with that in mind (see [Routing](routing.md#trailing-slashes)).

## The request flow

With the `main.go` above, every request takes this path:

```text
Request → logging → requestInfo → http.ServeMux
                                   ├─ GET /api/prefs  → prefs
                                   └─ / (anything else)
                                        → site.Handler()
                                           ├─ /_orbit/island/…    → deferred component
                                           ├─ /_orbit/*, public/  → file
                                           ├─ page route          → cached copy or page
                                           └─ no match            → 404.orbit
```

Inside `site.Handler()`, Orbit picks up the `*locals.Locals` that `EnsureLocals` attached to the request, and the page, its components and `404.orbit` all render with it. Responses come back out through `requestInfo` and `logging`.

With [route caching](caching.md), a page request may be answered from the cache instead of rendering. That happens inside `site.Handler()` too, so `logging` and `requestInfo` run for cached responses as well.

**Island requests** take the same path. A component rendered with `server:defer` is fetched by the browser in its own request to `/_orbit/island/…`, after the page has loaded. That request goes through `logging` and `requestInfo` like any other, so the deferred component sees the visitor's cookies and its own `locals`, with a new request ID. Nothing from the page's request carries over. See [Lazy loading](lazy-loading.md).

## Typed locals

Locals carry request-scoped data, such as a request ID or the visitor's theme, from your middleware to your pages and handlers. They're a struct you declare, so every use is type-checked.

### Declare the struct

Put `type Locals struct` in the Go package `src/locals` (package `locals`), as in `src/locals/locals.go` above. Orbit finds it by name. Without one, `locals` in pages is an `*orbit.NoLocals`, an empty struct.

`Locals` may also be an alias of a struct declared elsewhere (`type Locals = auth.Session`); Orbit then doesn't see a `RouteInfo` or `Cache` field in it ([Logging the matched route](#logging-the-matched-route), [Keeping requests out of the cache](#keeping-requests-out-of-the-cache)). A `src/locals` whose package isn't named `locals`, or whose `Locals` is a defined type that isn't a struct (`type Locals map[string]any`), is an error at that line.

### Set locals from middleware

Every middleware that sets fields starts with the same line:

```go
r, l := orbit.EnsureLocals[locals.Locals](r)
```

`EnsureLocals` returns the `*locals.Locals` already attached to the request, with `r` unchanged. If there's none, it attaches a new zero one and returns the new request. So the first middleware in the chain creates the value, and every later one gets the same pointer back. In the example, `logging` creates it, `requestInfo` fills it in, and `logging` reads `l.RequestID` after `next.ServeHTTP` returns.

Pass the returned `r` on to `next.ServeHTTP`: the attached value travels in the request's context.

The three functions you call:

```go
func EnsureLocals[L any](r *http.Request) (*http.Request, *L) // in middleware: the attached *L, or a new one
func WithLocals[L any](r *http.Request, l *L) *http.Request    // attaches a value you built yourself
func LocalsFromRequest[L any](r *http.Request) *L             // in your handlers: the attached *L, or nil
```

- **`WithLocals`** attaches a `*L` you built elsewhere. If one is already attached, handlers further down see the new one, while middleware that ran earlier keeps its own pointer. Prefer `EnsureLocals`, so all middleware shares one value.
- **`LocalsFromRequest`** returns `nil` when no middleware attached any, rather than a detached zero value whose writes would be lost. Check for `nil` in handlers that might run without your middleware, as `prefs` does.

### Read locals in pages and components

Every page and component has an implicit `locals` of type `*locals.Locals`, the value your middleware attached. The layout that `account.orbit` uses reads it too:

```orbit file=src/layouts/Base.orbit
---
type Props struct {
	Title string
}
---
<!DOCTYPE html>
<html lang="en" data-theme={locals.Theme}>
  <head>
    <meta charset="utf-8">
    <title>{props.Title}</title>
  </head>
  <body>
    <slot />
  </body>
</html>
```

Components on a page share the page's instance, so a field set by middleware is the same everywhere in one render.

Pages never see `nil`: if no middleware attached locals (a request that bypasses your middleware, or a test), Orbit allocates a zero `Locals` lazily, the first time a page or component uses it, once per request.

Inside the frontmatter, the variable `locals` shadows the package name. Hoisted helpers that need the type import `example.com/blog/src/locals` as usual. See [Components](components.md).

### A per-request data loader

Locals are also where data that several components need comes from, without passing it down as props through every layer: middleware gives each request a memoizing loader, created with the request's context, and components call it; the first call for a key loads it, and the others get the same result.

```go
func withCatalog(api *catalog.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.Books = catalog.NewLoader(r.Context(), api) // one per request
		next.ServeHTTP(w, r)
	})
}
```

A loader shared by concurrent loads and `server:concurrent` components needs a mutex; [Loading data](data-loading.md#share-data-without-props-drilling-a-loader-in-locals) has the 20-line `Loader` and its one rule (it uses the request's context, not a load's).

## Logging the matched route

A request log usually wants the route that answered, `/blog/[slug]` with `slug=hello`, rather than the URL: one line per route instead of per URL, and nothing private from the path or its query in the log. Orbit knows the route only inside `site.Middleware`, so it hands it back through your locals: give `Locals` a field of type `orbit.RouteInfo`, as `Route` in `src/locals/locals.go` above, and Orbit fills it. The type is the marker; any exported field name works.

```go file=cmd/server/pagelog.go
package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/orbit-z/orbit"

	"example.com/blog/src/locals"
)

// pageLog logs one line per page request: the route pattern and its params,
// never the URL or its query.
func pageLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		r, l := orbit.EnsureLocals[locals.Locals](r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if l.Route.Kind != orbit.RoutePage {
			return // static files, slash redirects, islands, 404s, your own handlers
		}
		params := map[string]string{}
		for name, value := range l.Route.Params() {
			params[name] = value
		}
		slog.Info("page", "method", r.Method, "route", l.Route.Pattern, "params", params,
			"status", rec.status, "duration", time.Since(start))
	})
}
```

Wrap it around the mux like `logging`: `logging(pageLog(requestInfo(mux)))`. `GET /blog/the-long-way` (the page `src/pages/blog/[slug].orbit`) logs `route=/blog/[slug] params=map[slug:the-long-way] status=200`.

Orbit fills the field as soon as the request is matched, before rendering, so a page that redirects, returns an error or panics still leaves it set, and a 500 is logged with its route. What each request records:

| Request | `Kind` | `Pattern` | `File` | params |
|---|---|---|---|---|
| a page route (any outcome, `orbit.ErrNotFound` included) | `orbit.RoutePage` | the route's, `/blog/[slug]` | the page | the route's |
| `site.NotFound()`, with `404.orbit` or the empty 404 | `orbit.RouteNotFound` | `/404` | `src/pages/404.orbit`, or `""` | none |
| `/_orbit/island/…`, whatever the answer | `orbit.RouteIsland` | `/_orbit/island/[buildID]/[id]` | the deferred component, or `""` if the ID is unknown | `buildID`, `id` |
| static files (`/_orbit/*`, `public/`), trailing-slash redirects, requests passed to `next` | unchanged (`orbit.RouteNone`) | | | |

- **Params** are percent-decoded, like `ctx.Param`. `l.Route.Param("slug")` reads one; `Params()` gives every name and value in the route's order. The strings stay valid after the request, so you can keep them or the whole struct.
- **A request through Orbit twice** (`site.Middleware` passes it to your mux, whose catch-all is `site.NotFound()`) keeps the last value, `/404`.
- **Pages and components** can read it too: `{locals.Route.Pattern}`. That works even without middleware attaching locals, because the `Locals` Orbit allocates for the page is filled the same way.
- **Your error handler** (`orbit.WithErrorHandler`) runs after the fill, so `orbit.LocalsFromRequest[locals.Locals](r).Route` there is the failing page's route.
- **Concurrency**: Orbit writes it on the request's goroutine before rendering. Read it there or after `next.ServeHTTP` returns, not from another goroutine during the request.
- **Cost**: filling it adds no allocation (a route with more than 8 params allocates once), and a project without the field pays nothing.

A `Locals` with two `orbit.RouteInfo` fields is an error, since Orbit fills one:

```go file=src/locals/locals.go expect-error
package locals

import "github.com/orbit-z/orbit"

type Locals struct {
	RequestID string
	Theme     string
	Route     orbit.RouteInfo
	Previous  orbit.RouteInfo
}
```

```text
src/locals/locals.go:9:2: Locals has two orbit.RouteInfo fields (Route, Previous); Orbit fills one
```

An embedded, unexported or pointer `RouteInfo` field is an error at the field too.

### Knowing the route before Orbit

Orbit fills the field only when the request reaches `site.Middleware`. A request that your middleware answers first, such as a sign-in redirect, a 503 or a rate limit, never gets there, so it has no route and `pageLog` skips it. In Astro, routing happens before any middleware, so `context.routePattern` is set for those requests too. To get the same, match the request yourself with `site.Match`:

```go
func (s *Site) Match(r *http.Request) RouteInfo // the route site.Middleware would serve r with, without serving it
```

```go file=cmd/server/gate.go
package main

import (
	"net/http"

	"github.com/orbit-z/orbit"

	"example.com/blog/src/locals"
)

// signInGate sends visitors without a session from /account to /signin. It
// records the route first, so pageLog logs the redirect with its route.
func signInGate(site *orbit.Site, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.Route = site.Match(r)
		if l.Route.Kind == orbit.RoutePage && l.Route.Pattern == "/account" {
			if _, err := r.Cookie("session"); err != nil {
				http.Redirect(w, r, "/signin", http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

Put it inside `pageLog`: `logging(pageLog(signInGate(site, requestInfo(mux))))`. A visitor's `GET /account` then logs `route=/account status=303`.

`Match` follows `site.Middleware`'s own order, and returns what Orbit would record:

| Request | `site.Match(r)` |
|---|---|
| a page route, any method | `orbit.RoutePage`, the route, its file and params |
| `/_orbit/island/…`, any method | `orbit.RouteIsland`, as Orbit records it |
| a GET or HEAD of a static file (`/_orbit/*`, `public/`) | `orbit.RouteNone` |
| the other form of a page URL (`/blog/hello/`, redirected with a 308) | `orbit.RouteNone` |
| anything else | `orbit.RouteNone`: Orbit passes it to `next`, which may be `site.NotFound()` (then Orbit records `/404`) or one of your handlers |

- **Orbit fills the field again** when the request reaches it, so a request Orbit serves ends with Orbit's value, `/404` included. `Match` doesn't change the request or the locals; you assign its result.
- **`Match` sees the request as it is now.** If something between your middleware and Orbit rewrites the path (`http.StripPrefix`), call `Match` on the rewritten request; otherwise Orbit's fill corrects the value only for requests it serves. Likewise, if your mux sends a path that an Orbit page would match to its own handler, `Match` still reports the page.
- **Your own handlers' routes** aren't Orbit's: `Match` returns `orbit.RouteNone` for them. `http.ServeMux` records its pattern in `r.Pattern` (`GET /api/prefs`) on the request it serves, which is your middleware's `r`, so a log can read that after `next.ServeHTTP` returns.
- **Cost**: no allocation (a route with more than 8 params allocates once), about 50 ns. Orbit matches the request again inside `site.Middleware`, which costs about as much.

## Keeping requests out of the cache

With [route caching](caching.md), a page's response can be stored and served to every visitor. Orbit looks the request up inside `site.Middleware`, after your middleware has run, so middleware decides first. It reaches the request's cache handle through a field of type `orbit.Cache` in your `Locals` (any exported name; the type is the marker):

```go
type Locals struct {
	User  *auth.User
	Cache orbit.Cache // the request's cache handle
}
```

The usual use is to keep signed-in visitors out of the cache, so their pages can show their name:

```go
func withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		if l.User = auth.FromCookie(r); l.User != nil {
			l.Cache.Disable() // a fresh render, never stored
		}
		next.ServeHTTP(w, r)
	})
}
```

- **`Disable()` runs before the lookup.** The request is neither served from the cache nor stored, even if the page calls `ctx.Cache().Set` afterwards. In Astro, the cache sits outside the middleware, so `set(false)` there can't stop a cached copy; in Orbit it can.
- **Your middleware runs on every request, cached ones included**: authentication, redirects, logging and session refresh always run. A cached response costs what your middleware costs, plus about 0.15 µs.
- **`Set` works too**: middleware can add tags or a `MaxAge` (`l.Cache.Set(orbit.CacheOptions{Tags: []string{"tenant:" + id}})`). A [route rule](configuration.md#routerules) applies under those values, and the page's own `Set` merges over both.
- **`ctx.Cache()` is the same field**: in pages and components, `ctx.Cache()` returns it when your middleware attached the locals.
- **Before Orbit has seen the request**, `l.Cache.Enabled()` reports `false` and `l.Cache.Invalidate` returns `orbit.ErrCacheNotEnabled`. To purge from middleware or a handler, use `site.InvalidateCache`.
- **A cookie stops storing.** A response that has `Set-Cookie`, from the page or from your middleware, is never stored, so middleware that refreshes a session cookie on every request keeps those pages out of the cache. Set cookies only when they change.

Two `orbit.Cache` fields (`Locals has two orbit.Cache fields (A, B); Orbit uses one`), an embedded one, an unexported one or a `*orbit.Cache` is an error at the field. See [Caching](caching.md#safety-what-orbit-checks-and-what-you-check) for what a cached page must not read.

## Short-circuit responses

A middleware short-circuits the usual `net/http` way: it writes a response and doesn't call `next`.

```go
// maintenance answers every request with a 503 while MAINTENANCE=1 is set.
func maintenance(next http.Handler) http.Handler {
	on := os.Getenv("MAINTENANCE") == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if on {
			w.Header().Set("Retry-After", "600")
			http.Error(w, "Back soon", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

A page short-circuits from its frontmatter by returning early. The template doesn't render:

| Return | Response |
|---|---|
| `return ctx.Redirect("/blog", http.StatusFound)` | the redirect |
| `return orbit.ErrNotFound` | your `404.orbit` page, status 404 |
| `return err` | a 500, or your error handler's answer |

See [Rendering](rendering.md#status-codes-and-errors) for status codes and errors.

### Catching what the page returned

In Astro, middleware can wrap `next()` in a `try`/`catch` to answer what the page threw. In Orbit, a page's error is answered inside `site.Middleware`, so by the time `next.ServeHTTP` returns in your middleware, the response is written. Map errors to responses with an error handler instead, given to `app.New`:

```go
site := app.New(orbit.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		http.Error(w, "The page took too long. Try again.", http.StatusGatewayTimeout)
		return true
	}
	return false // the built-in 500
}))
```

It runs before anything is written, for errors from the page, any component, and deferred islands, and for recovered panics; `orbit.ErrNotFound` and redirects never reach it. Locals your middleware attached are on `r`. See [Answering errors yourself](rendering.md#answering-errors-yourself-witherrorhandler).

## Rewrite responses

To change a response after the page has rendered, wrap the `http.ResponseWriter`, as you would for any Go handler. Orbit buffers every page and writes it once, with `Content-Length`, after injecting its `<link>` and `<script>` tags, so a wrapper always sees the complete, final HTML:

```go
// stampVersion replaces %VERSION% in HTML responses.
func stampVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := &bufferedWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(bw, r)
		body := bw.body.Bytes()
		if r.Method != http.MethodHead && strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			body = bytes.ReplaceAll(body, []byte("%VERSION%"), []byte(version))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		w.WriteHeader(bw.status)
		w.Write(body)
	})
}

// bufferedWriter holds the status and body until the handler returns.
type bufferedWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (b *bufferedWriter) WriteHeader(code int)        { b.status = code }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
```

Headers the page set are already in `w.Header()` when `next.ServeHTTP` returns, because the wrapper shares the underlying header map. A body-rewriting wrapper holds the whole response in memory, so put it only around what needs it, not around streaming handlers. To only add headers, set them before calling `next`, as `requestInfo` does.

## The import rule

Every generated page and component imports `src/locals` (that's how `locals` gets its type). So `src/locals`, and anything it imports, must not import `example.com/blog/orbit_gen/app`: that would be an import cycle. Keep it a plain types package.

Orbit checks this before Go does and reports the import, for example:

```go file=src/locals/locals.go expect-error
package locals

import _ "example.com/blog/orbit_gen/app"

type Locals struct {
	RequestID string
}
```

```text
src/locals/locals.go:3:10: src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle
```

An indirect import is reported at the import in `src/locals` that leads to it, with the chain of packages. Here `internal/auth` imports `orbit_gen/app`:

```go file=src/locals/locals.go expect-error
package locals

import (
	"example.com/blog/internal/auth"
)

type Locals struct {
	User *auth.User
}
```

```text
src/locals/locals.go:4:2: src/locals can't import example.com/blog/orbit_gen/app: pages that use locals import src/locals, so this would be an import cycle (src/locals → example.com/blog/internal/auth → example.com/blog/orbit_gen/app)
```

The same goes for the generated component packages under `orbit_gen/impl/`. `orbit doctor` checks it too.

## See also

- [Server](server.md): where Orbit and your middleware sit in `main.go`
- [Rendering](rendering.md): redirects, `orbit.ErrNotFound` and errors from the frontmatter
- [Lazy loading](lazy-loading.md): island requests and your middleware
- [Caching](caching.md): route caching, and what a cached page must not read
- [Components](components.md): `locals`, `ctx` and `props` in the frontmatter
- [Testing](testing.md): rendering with `orbittest.WithLocals`
