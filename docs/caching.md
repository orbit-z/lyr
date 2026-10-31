# Caching

Lyr can keep a page's rendered response and serve it again without rendering it. A page says how long its response stays fresh with `ctx.Cache().Set`, `lyr.json` can set defaults per route, and a **provider** you pass in `cmd/server/main.go` stores the responses in memory or turns the options into CDN headers. It's Astro 7's route caching, in Go.

A render is fast in Lyr (from well under a microsecond to tens of microseconds), so caching pays off for pages that wait on data: a database, a catalogue API, a CMS. A cached copy is served in about 0.15 µs, without the wait.

## A cached blog

The example caches every blog page, keeps signed-in visitors out of the cache, and purges a post's pages when it changes.

<details>
<summary><code>internal/posts/posts.go</code>, the tiny post store used by the examples on this page</summary>

```go file=internal/posts/posts.go
// Package posts is a tiny in-memory post store for the examples.
package posts

import "errors"

// ErrNotFound is returned by Find for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

type Post struct {
	Slug, Title, Body string
}

var all = []Post{
	{Slug: "hello", Title: "Hello, Lyr", Body: "The first post."},
	{Slug: "routing", Title: "File-based routing", Body: "Pages are files."},
}

// All returns every post.
func All() []Post { return all }

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

`cmd/server/main.go` configures the provider, an in-memory store from the `github.com/orbit-z/lyr/cache` package, and adds an admin route that purges a post:

```go file=cmd/server/main.go
package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"

	"example.com/blog/internal/session"
	"example.com/blog/lyr_gen/app"
)

func main() {
	site := app.New(lyr.WithCache(cache.NewMemory(cache.MemoryOptions{
		MaxEntries: 5000,
		MaxBytes:   256 << 20, // 256 MiB
	})))

	mux := http.NewServeMux()
	mux.Handle("POST /admin/purge/{slug}", purge(site))
	mux.Handle("/", site.Handler())

	if err := lyr.ListenAndServe(site.TrailingSlash(session.Middleware(mux))); err != nil {
		log.Fatal(err)
	}
}

// purge drops a post's cached page and the blog index, after the post
// was edited.
func purge(site *lyr.Site) http.Handler {
	token := os.Getenv("PURGE_TOKEN")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := []byte(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare(auth, []byte("Bearer "+token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		err := site.InvalidateCache(r.Context(), lyr.InvalidateOptions{
			Tags: []string{"post:" + r.PathValue("slug")},
			Path: "/blog",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
```

`lyr.json` gives every page under `/blog` a default, in seconds as in Astro: fresh for 5 minutes, then served stale for up to an hour while it's refreshed, tagged `blog`:

```json file=lyr.json
{
  "routeRules": {
    "/blog/[...path]": { "maxAge": 300, "swr": 3600, "tags": ["blog"] }
  }
}
```

A post page sets its own lifetime, and a tag per post so one post can be purged:

```lyr file=src/pages/blog/[slug].lyr
---
import (
	"errors"
	"time"

	Layout "../../layouts/Base.lyr"
	"example.com/blog/internal/posts"
)

post, err := posts.Find(params.Slug)
if errors.Is(err, posts.ErrNotFound) {
	return lyr.ErrNotFound
}
if err != nil {
	return err
}
ctx.Cache().Set(lyr.CacheOptions{
	MaxAge: time.Hour,
	Tags:   []string{"post:" + post.Slug},
})
---
<Layout title={post.Title}>
  <h1>{post.Title}</h1>
  <p>{post.Body}</p>
</Layout>
```

The index sets nothing: the route rule alone caches it.

```lyr file=src/pages/blog/index.lyr
---
import (
	Layout "../../layouts/Base.lyr"
	"example.com/blog/internal/posts"
)
---
<Layout title="Blog">
  <ul>
    {for _, p := range posts.All()}
      <li><a href={"/blog/" + p.Slug}>{p.Title}</a></li>
    {/for}
  </ul>
</Layout>
```

Middleware reaches the request's cache handle through a field of type `lyr.Cache` in your `Locals`:

```go file=src/locals/locals.go
// Package locals declares the request-scoped data that middleware hands to pages.
package locals

import "github.com/orbit-z/lyr"

type Locals struct {
	User  string      // the signed-in user, "" for a visitor
	Cache lyr.Cache // the request's cache handle
}
```

The session middleware keeps signed-in visitors out of the cache, so their pages can show their name:

```go file=internal/session/session.go
// Package session reads the visitor's session.
package session

import (
	"net/http"

	"github.com/orbit-z/lyr"

	"example.com/blog/src/locals"
)

// Middleware sets locals.User for signed-in visitors and keeps their
// requests out of the cache.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := lyr.EnsureLocals[locals.Locals](r)
		if c, err := r.Cookie("session"); err == nil && c.Value != "" {
			l.User = c.Value // stands in for a session lookup
			l.Cache.Disable()
		}
		next.ServeHTTP(w, r)
	})
}
```

The first `GET /blog/hello` renders the page and stores it: the response has `X-Lyr-Cache: MISS`. For the next hour, requests get the stored bytes without a render (`HIT`). For the hour after that, from the rule's `swr`, they get the old copy while one background render refreshes it (`STALE`). A signed-in visitor always gets a fresh render, and no `X-Lyr-Cache` header. `POST /admin/purge/hello` drops the post's page and the index; the next request for each is a `MISS` again.

## How a cached request is served

The cache sits inside `site.Middleware`, after the route is matched and before the page renders:

```text
Request → your middleware (session, logging, …) → site.Middleware
                                                    ├─ static file, slash redirect, no match → as usual
                                                    └─ page or GET island
                                                         ├─ fresh copy stored     → HIT
                                                         ├─ stale copy, within SWR → STALE (+ one background render)
                                                         └─ none                   → render → stored? MISS
```

So **your middleware runs on every request, HITs included**: authentication, logging, session refresh and `RouteInfo` all see cached requests. This is the main difference from Astro, where a HIT skips the middleware. A `Disable()` in your middleware runs before the lookup, so a signed-in visitor never gets the anonymous copy.

### What's looked up

A request is looked up when all of these hold:

- it's a `GET` (a `HEAD` or `POST` always renders, as in Astro);
- the site has a provider, and isn't running under `lyr dev`;
- the request's handle isn't disabled;
- its route has stored a response before, or matches a [route rule](#route-rules-routerules). A page that never caches is never looked up.

### The key

The key is the request's host, its escaped path and its normalized query:

- the query parameters are sorted by name;
- tracking parameters are dropped, Astro's list: `utm_*`, `fbclid`, `gclid`, `ref` and others. So `/blog?utm_source=x` gets the same copy as `/blog`.
- `cache.NewMemory`'s `Query` option chooses the parameters (see [`cache.NewMemory`](#the-memory-provider-cachenewmemory)).

The scheme isn't part of the key: behind a proxy, Go doesn't see it.

A stored response with a `Vary` header keys its variants by the request's values for those headers, as in Astro: a page that sends `Vary: Accept-Language` gets one entry per language.

### What's stored

A response is stored when the request's options have a `MaxAge` above zero (tags alone store nothing) and the handle isn't disabled, at whatever status the page set (200, a 404 through `ctx.SetStatus`, a 410…). A redirect a page returns with `ctx.Redirect` is stored too.

These are never stored:

- render errors and panics, and the answers of your [`WithErrorHandler`](rendering.md#answering-errors-yourself-witherrorhandler);
- the `404.lyr` page shown for `lyr.ErrNotFound`, and URLs no route matches;
- a response with a `Set-Cookie` header, whether the page or your middleware set it;
- a response with `Vary: Cookie` or `Vary: *`.

The last two are refused with a warning on stderr, once per route and reason:

```text
lyr: GET /blog/hello: not cached because it sets a cookie (Set-Cookie); later refusals for this route aren't logged
```

The entry keeps the page's own headers (those set with `ctx.Header()`), plus `Content-Type`, `Content-Length` and `ETag`. Headers your middleware set on the response aren't stored: your middleware runs on every HIT, so it sets them again.

### `X-Lyr-Cache`

| Value | When |
|---|---|
| `HIT` | A fresh stored copy was served. |
| `STALE` | A stale copy was served, and a background render refreshes it. |
| `MISS` | The page rendered, and its response was stored. |
| no header | Nothing was stored or served from the store. |

There's no `Age` header, as in Astro.

### Stale-while-revalidate

From `MaxAge` to `MaxAge + SWR` after a response was stored, requests get the stale copy at once, and **one** background render per key refreshes it. If that render fails or panics, the error is logged and the stale copy is kept until `MaxAge + SWR` runs out:

```text
lyr: revalidating GET /blog/hello: posts: database unavailable; the stale copy is kept
```

Past `MaxAge + SWR`, a request is a miss. The background render runs with the values of the request that triggered it, including the same `locals` pointer. So if a page writes to `locals` (a memoizing loader, say) while your middleware reads them after `next` returns, the two race. Run `go test -race` on such code.

### Concurrent misses

When several requests miss on a key whose route is known to cache (it stored before, or a rule matches), one renders and the others wait for it. If its response was stored, the others are served it as a `HIT`. If not (it set a cookie, or turned out to be per-user), each renders its own. A route's first-ever request never waits, and neither do pages that never cache. A waiting request whose client goes away gets an empty 503.

## Setting options: `ctx.Cache()`

Every page and component can reach the request's cache handle with `ctx.Cache()`. All of a request's components share it.

```go
type CacheOptions struct {
	MaxAge       time.Duration // how long a stored response stays fresh
	SWR          time.Duration // how long after MaxAge a stale copy is still served
	Tags         []string      // labels for invalidation
	ETag         string        // a quoted entity tag, `"v1"` or `W/"v1"`
	LastModified time.Time
}
```

`Set` merges options with Astro's rules, so a layout and a component can each add theirs:

| Field | Merge |
|---|---|
| `MaxAge`, `SWR`, `ETag` | the last non-zero value wins |
| `LastModified` | the latest time wins |
| `Tags` | accumulate; duplicates are dropped |

A zero value doesn't reset a field: `Set(lyr.CacheOptions{})` changes nothing. To keep a request out of the cache, call `Disable()`.

Durations are `time.Duration` (`time.Hour`), where Astro takes seconds. CDN headers carry whole seconds, so sub-second values are truncated there.

The other methods:

- **`Disable()`** is Astro's `set(false)`: it clears the options and tags, and Lyr neither looks the request up nor stores it. In middleware it runs before the lookup. A later `Set` enables the handle again, but a request disabled before the lookup stays out of the cache: it isn't stored and gets no cache headers.
- **`Enabled()`** reports whether the request can be cached: a provider is configured, the site isn't under `lyr dev`, and `Disable` wasn't called.
- **`Options()`** returns a snapshot of the merged options; its `Tags` is a copy.
- **`Invalidate(ctx, o)`** purges through the provider, like `site.InvalidateCache` (see [Invalidation](#invalidation)).

A `Cache` belongs to one request: don't keep it or share it with other goroutines.

### Where options can be set

- **Pages, layouts, components and slot content**: `ctx.Cache()`.
- **`server:concurrent` components**: each sets on its own copy, merged into the page's when the page assembles them, in source order.
- **Content captured with `lyr.RenderHTML`** sets options only when it renders. A fragment served from a fragment cache must re-apply its tags, or a purge of those tags misses the page. See [Caching fragments](rendering.md#caching-fragments).
- **Middleware**: through the `lyr.Cache` field of your `Locals`, as in `session.Middleware` above. `ctx.Cache()` is that field when your middleware attached the locals.
- **Your own handlers**: through `site.CacheHandler` (see [below](#custom-handlers-sitecachehandler)).

### The `Locals` field

Give `Locals` a field of type `lyr.Cache` (any exported name works; the type is the marker) and `app.New()` wires it up. `Set` and `Disable` on it work from the first middleware on. `Enabled()` reports `false` and `Invalidate` returns `lyr.ErrCacheNotEnabled` on it until Lyr has seen the request, so in middleware, purge with `site.InvalidateCache`. Two `lyr.Cache` fields, an embedded one, an unexported one or a `*lyr.Cache` is an error at the field, as for [`RouteInfo`](middleware.md#logging-the-matched-route). See [Middleware and locals](middleware.md#keeping-requests-out-of-the-cache).

## Route rules: `routeRules`

`lyr.json`'s `routeRules` sets default options per route, without touching the pages:

```json
{
  "routeRules": {
    "/blog/[...path]": { "maxAge": 300, "swr": 3600, "tags": ["blog"] },
    "/products/[id]": { "maxAge": 60 }
  }
}
```

- **Patterns** use Lyr's [route syntax](routing.md) (`[param]`, `[...rest]`, no globs) and its priority (static segments, then params, then rest params). They match the **request path**, whichever page serves it: `/blog/[...path]` covers `/blog`, `/blog/hello` and `/blog/2026/recap`. They also match the paths of a [`site.CacheHandler`](#custom-handlers-sitecachehandler), and never `/_lyr/` (islands set their own).
- **Values**: `maxAge` and `swr` are whole seconds, as in Astro; `tags` is an array of strings.
- **Order**: the matching rule is applied as a first `Set`, under what your middleware already set, before the page renders. Your middleware's values win over the rule, and the page's `Set` merges over both. A request your middleware disabled stays disabled.
- **Without a provider**, rules do nothing.
- **Errors**: `lyr.json` is checked when it loads. An invalid pattern, a negative or fractional number of seconds, an unknown key in a rule, or two rules that match the same paths is an error, such as `lyr.json: routeRules["/blog/[slug"]: …`.

See [Configuration](configuration.md#routerules).

## Invalidation

```go
err := site.InvalidateCache(r.Context(), lyr.InvalidateOptions{Tags: []string{"post:hello"}})
```

- **`Tags`** drops every stored response carrying any of them.
- **`Path`** is an escaped request path, such as `/blog/hello`. It drops every stored variant of that path: any query, any host, any `Vary` value.

`site.InvalidateCache` is for your handlers and webhooks; in a page or component, `ctx.Cache().Invalidate(ctx.Request.Context(), o)` does the same. Both return `lyr.ErrCacheNotEnabled` when the site has no provider, and `nil` under `lyr dev`.

A purge reaches only the process it runs in. With several instances, see [Several instances](#several-instances).

## Conditional requests and `ETag`

With a provider configured, Lyr answers conditional requests with a `304 Not Modified`, headers only:

- **Stored responses** get a strong `ETag` unless the page set one: the base64 of a checksum of the body, so every instance gives the same one.
- **Which requests**: `HIT`s, `STALE`s, and any successful page or island response that carries an `ETag` or `Last-Modified`, from `Set` or from `ctx.Header()`. So a page can set an `ETag` without being stored, and still get 304s.
- **How**: `If-None-Match` first (weak comparison, `*` and lists), then `If-Modified-Since` against `Last-Modified`.
- **`Cache-Control: no-cache`** is added to a response that has an `ETag` or `Last-Modified` but no `Cache-Control` or `Expires`, as Astro does, so browsers revalidate rather than guess a lifetime.

A site without a provider answers no 304s for pages: its responses are as before.

## `Cache-Control` for browsers

Lyr never derives a browser `Cache-Control` from `MaxAge` when the provider stores responses: `MaxAge` is how long Lyr keeps the copy, not how long a browser may. Set `Cache-Control` yourself with `ctx.Header()` when you want browsers or a CDN to keep the page; the stored entry repeats it:

```go
ctx.Header().Set("Cache-Control", "public, max-age=60")
```

## Islands

GET requests for [`server:defer`](lazy-loading.md) islands go through the cache like pages, when the deferred component calls `ctx.Cache().Set` with a `MaxAge`. The island URL is deterministic and holds the build ID, so the same props share one entry, and every deploy starts with new keys.

- The browser default, `Cache-Control: private, no-store`, stays on the stored response unless the component sets its own `Cache-Control`.
- `POST` island requests (props over about 8 KB) are never cached.
- Route rules don't apply to islands.
- The island request carries the visitor's cookies, and that's often why a component is deferred. A component that reads them, or per-user `locals`, must not call `Set`.

See [Response headers and caching](lazy-loading.md#response-headers-and-caching).

## Custom handlers: `site.CacheHandler`

`site.CacheHandler(h)` puts one of your own handlers on the same flow: GET requests are looked up and stored, route rules match its paths, and 304s are answered.

```go
mux.Handle("GET /fragments/latest", site.CacheHandler(http.HandlerFunc(latest)))
```

- `h` sets options through the `lyr.Cache` field of your `Locals` (`lyr.LocalsFromRequest[locals.Locals](r).Cache.Set(…)`), or through `ctx.Cache()` in a component it renders with `lyr.RenderComponent`.
- `h`'s response is buffered, and its `Content-Type` is sniffed when it sets none. The headers `h` sets are stored with it; whatever status `h` writes is stored when the options ask, so don't `Set` on an error path.
- Without a provider, or under `lyr dev`, `CacheHandler` returns `h` unchanged.

## Providers

`lyr.WithCache(p)` in `cmd/server/main.go` sets the site's provider; with several, the last one wins. Lyr runs the whole flow (keys, freshness, stale-while-revalidate, `Vary`, the cookie refusals, 304s, `X-Lyr-Cache`) for every provider. A provider is one of three kinds:

| Kind | Interface | What Lyr does |
|---|---|---|
| A store, such as `cache.MemoryProvider` | `lyr.CacheStore` | Looks requests up with `Get`, stores responses with `Put`. Writes no CDN headers. |
| A CDN header writer | `lyr.CacheHeaderWriter` | Calls `WriteCacheHeaders` on each response whose options are active, so the CDN caches. |
| Neither | `lyr.CacheProvider` | Writes Astro's default headers: `CDN-Cache-Control: max-age=N, stale-while-revalidate=M`, `Cache-Tag: a, b`, `Last-Modified` and `ETag`. |

A provider that implements both `CacheStore` and `CacheHeaderWriter` is used as a store.

Every provider implements `Invalidate`:

```go
type CacheProvider interface {
	Invalidate(ctx context.Context, o InvalidateOptions) error
}
```

### The memory provider: `cache.NewMemory`

`cache.NewMemory` from `github.com/orbit-z/lyr/cache` makes an in-process store, Astro's `memoryCache()`:

```go
p := cache.NewMemory(cache.MemoryOptions{
	MaxEntries: 5000,      // default 1000
	MaxBytes:   256 << 20, // optional
	Query:      lyr.CacheQuery{Exclude: []string{"utm_*", "sort"}},
})
```

- **`MaxEntries`** bounds the stored responses (each `Vary` variant counts as one). The default is 1000, Astro's.
- **`MaxBytes`**, when set, bounds the bytes held: bodies, headers, keys and tags. The store is split into shards and the bounds are checked per shard, so they're approximate, and a response bigger than `MaxBytes` divided by the number of shards isn't stored.
- **Eviction** is close to least recently used (CLOCK, per shard), and expiry is lazy: an entry is dropped when it's read after `MaxAge + SWR`, or evicted. There's no timer.
- **`Query`** says which query parameters are part of a key. `Include` and `Exclude` are [`path.Match`](https://pkg.go.dev/path#Match) patterns, matched case-insensitively against parameter names. With `Include`, only matching parameters are kept. `Exclude` drops the matching ones and replaces the default list of tracking parameters, so list `utm_*` again if you still want it dropped, as above (an empty, non-nil `Exclude` keeps every parameter). Setting both makes `cache.NewMemory` panic. Parameters are sorted by name unless `NoSort` is set.
- **`OnInvalidate`** forwards purges to other instances (see [Several instances](#several-instances)).
- **`Now`** replaces the clock, for tests.

The `*cache.MemoryProvider` also has `Clear()` (drop everything, without `OnInvalidate`), `Len()` and `Bytes()`, for metrics.

A store's memory per entry is the body plus about half a kilobyte. The cache lives in the process: a restart or a deploy starts it empty.

### CDN header providers

A provider that implements `lyr.CacheHeaderWriter` writes the headers its CDN reads. Lyr calls it after the page's own headers are set, for responses whose options are active (a `MaxAge` or tags, and not disabled); `o` is read-only. It writes every header it wants, `ETag` and `Last-Modified` included. To purge by path on a CDN that only purges by tag, tag each response with its path, as Astro's CDN adapters do with `astro-path:`:

```go file=internal/cdn/cdn.go
// Package cdn is a cache provider for a CDN that reads Surrogate-Control
// and Surrogate-Key.
package cdn

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/orbit-z/lyr"
)

// Provider writes the CDN's headers; Purge calls its purge API.
type Provider struct {
	Purge func(ctx context.Context, keys []string) error
}

func (p *Provider) WriteCacheHeaders(h http.Header, o *lyr.CacheOptions, r *http.Request) {
	if o.MaxAge > 0 {
		v := "max-age=" + strconv.FormatInt(int64(o.MaxAge.Seconds()), 10)
		if o.SWR > 0 {
			v += ", stale-while-revalidate=" + strconv.FormatInt(int64(o.SWR.Seconds()), 10)
		}
		h.Set("Surrogate-Control", v)
	}
	keys := append(slices.Clone(o.Tags), "path:"+r.URL.EscapedPath())
	h.Set("Surrogate-Key", strings.Join(keys, " "))
	if o.ETag != "" {
		h.Set("ETag", o.ETag)
	}
	if !o.LastModified.IsZero() {
		h.Set("Last-Modified", o.LastModified.UTC().Format(http.TimeFormat))
	}
}

func (p *Provider) Invalidate(ctx context.Context, o lyr.InvalidateOptions) error {
	keys := slices.Clone(o.Tags)
	if o.Path != "" {
		keys = append(keys, "path:"+o.Path)
	}
	return p.Purge(ctx, keys)
}
```

With a header provider, the CDN does the caching, so `X-Lyr-Cache` never appears and every request that reaches your server renders. The cookie refusals apply here too: a response that sets a cookie or varies on `Cookie` gets no CDN headers, and neither does a request your middleware disabled (see [Safety](#safety-what-lyr-checks-and-what-you-check)).

### Writing a store

A store implements `lyr.CacheStore`:

```go
type CacheStore interface {
	CacheProvider
	Get(key CacheKey) *CachedResponse // nil: a miss; Lyr checks freshness
	Put(key CacheKey, e *CachedResponse)
}
```

- **Keys**: `key.Hash()` is a hash for sharding and map lookups, stable within a process only. `key.String()` is the key itself, valid only during the call: copy it (`strings.Clone`) to keep it. `lyr.NewCacheKey(s)` makes a key for your store's tests.
- **Entries**: a `CachedResponse` holds `Status`, `Header`, `Body`, `Stored`, `MaxAge`, `SWR`, `Tags` and `Path`. It's immutable once passed to `Put`: keep it as given. Keep it at least until `Stored + MaxAge + SWR`; Lyr decides whether it's fresh or stale.
- **`Vary` markers**: a response with a `Vary` header is stored under a variant key, with a marker under the URL's own key (an entry whose `Vary` field is set, with no body). Store markers like any other entry.
- **`Invalidate`** drops the entries carrying any of `o.Tags`, and every entry whose `Path` is `o.Path`.
- **Query parameters**: a store may also implement `CacheQuery() lyr.CacheQuery`, read once when the site is created.
- **Speed**: `Get` runs on every lookup, so keep it free of allocations; `cache.MemoryProvider`'s takes about 43 ns.

A persistent or shared store (Redis, disk) is a provider in your own code or another module; Lyr ships only the memory one.

## Several instances

Each instance's `cache.MemoryProvider` is its own, so a purge must reach every instance. `OnInvalidate` is called after each local purge, so you can publish it with whatever your instances already share (Redis, NATS, a database), and each instance applies the purges it receives with `InvalidateLocal`, which doesn't call `OnInvalidate` again:

```go
var mem *cache.MemoryProvider
mem = cache.NewMemory(cache.MemoryOptions{
	MaxEntries: 5000,
	OnInvalidate: func(ctx context.Context, o lyr.InvalidateOptions) error {
		return bus.Publish(ctx, o) // your own code: send o to the other instances
	},
})
site := app.New(lyr.WithCache(mem))

go bus.Subscribe(func(o lyr.InvalidateOptions) {
	mem.InvalidateLocal(o) // a purge from another instance
})
```

`OnInvalidate`'s error is `Invalidate`'s, so `site.InvalidateCache` reports a failed publish. The instance that purges has already dropped its own entries by then.

## Caching data and fragments: `cache.Store`

The route cache keeps whole responses. For values that several pages share (a product, a sidebar's HTML), `cache.NewStore` makes a general in-process cache on the same engine:

```go
var products = cache.NewStore(cache.StoreOptions[shop.Product]{MaxEntries: 10_000})

p, _, err := products.Load(ctx.Request.Context(), id, time.Minute, []string{"product:" + id},
	func(lc context.Context) (shop.Product, error) { return api.Product(lc, id) })
```

- **`Get(key)`** and **`Set(key, v, ttl, tags...)`** read and write directly; `Get` doesn't allocate (about 46 ns on a hit).
- **`Load`** returns the stored value, or calls `load` once per key however many requests miss at the same time. The first caller runs it with `context.WithoutCancel` of its context; the others wait under their own context and get `ctx.Err()` if it ends first. An error is returned to every caller waiting on that load, and nothing is stored. A panic goes on in the first caller, and the others get an error wrapping `cache.ErrLoadPanicked`. `Load` also returns the entry's tags.
- **`Delete(key)`**, **`InvalidateTags(tags...)`** and **`Clear()`** drop values; a load running at the time doesn't store its result.
- **`MaxEntries`** (default 1000) and **`MaxBytes`** bound it as for `cache.NewMemory`; `Size` measures a value (by default, the length of a `string`, `[]byte` or `lyr.HTML`, else 0). `Len()` and `Bytes()` report it.
- **Values are stored as given**, not copied: don't change a value after storing it, or one you got from the store (a slice's elements, the fields behind a pointer).

[Loading data](data-loading.md#cache-data-across-requests-cachestore) shows a data cache, and [Caching fragments](rendering.md#caching-fragments) a fragment cache that re-applies its tags to the page.

## Safety: what Lyr checks and what you check

A stored page is served to everyone. Lyr enforces what it can see:

- responses that set a cookie, or vary on `Cookie` or `*`, aren't stored;
- only `GET` requests are looked up and stored;
- nothing is stored under `lyr dev`, for errors, or for URLs no route matches;
- your middleware runs on every HIT, so its authentication and redirects always run;
- `Disable()` in middleware runs before the lookup, and a request disabled there is never stored and gets no CDN headers, whatever the page sets later.

It can't see into your `locals`, so this part is yours: **a page that calls `Set` or matches a rule must not read cookies, the session or per-user `locals`.** A miss (and a background refresh) renders with the request of whichever visitor triggered it, and that copy is served to everyone. The patterns:

- **Disable in middleware for signed-in visitors**, as `session.Middleware` does. Their pages render fresh every time, and can show their name.
- **Per-user parts as `server:defer` islands.** The cached page holds a placeholder that's the same for everyone, and each visitor's browser loads the island with its own cookies, through your middleware (see [Lazy loading](lazy-loading.md#serverdefer-inside-cached-fragments)).
- **A guard on shared renders**: set a flag on your `locals` while a cacheable page renders, and make your per-user accessors refuse while it's set, as in [Caching fragments](rendering.md#caching-fragments).

With a header provider, the same checks decide whether the response gets CDN headers: none for a response that sets a cookie or has `Vary: Cookie`/`Vary: *`, and none for a request your middleware disabled, even if the page calls `Set` afterwards.

## In `lyr dev` and in tests

Under `lyr dev`, caching is off, as in Astro: nothing is stored or served from the store, no cache headers are written (a header provider's included), `Enabled()` reports `false` and `Invalidate` returns `nil`. Every save shows its result at once.

Under `go test`, a site built with `lyr.WithCache` caches like production, so `lyrtest.Get` is how you test caching:

```go file=internal/views_test/cache_test.go
package views_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"
	"github.com/orbit-z/lyr/lyrtest"

	"example.com/blog/internal/session"
	"example.com/blog/lyr_gen/app"
)

func TestCaching(t *testing.T) {
	site := app.New(lyr.WithCache(cache.NewMemory(cache.MemoryOptions{})))
	h := session.Middleware(site.Handler())
	state := func(path string, opts ...lyrtest.Option) string {
		t.Helper()
		return lyrtest.Get(t, h, path, opts...).Header().Get("X-Lyr-Cache")
	}

	for _, want := range []string{"MISS", "HIT"} {
		if got := state("/blog/hello"); got != want {
			t.Errorf("GET /blog/hello: X-Lyr-Cache %q, want %q", got, want)
		}
	}
	if got := state("/blog"); got != "MISS" { // cached by the route rule
		t.Errorf("GET /blog: X-Lyr-Cache %q, want MISS", got)
	}

	signedIn := httptest.NewRequest(http.MethodGet, "/", nil)
	signedIn.AddCookie(&http.Cookie{Name: "session", Value: "ana"})
	if got := state("/blog/hello", lyrtest.WithRequest(signedIn)); got != "" {
		t.Errorf("signed in: X-Lyr-Cache %q, want none", got)
	}

	err := site.InvalidateCache(t.Context(), lyr.InvalidateOptions{Tags: []string{"post:hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := state("/blog/hello"); got != "MISS" {
		t.Errorf("after the purge: X-Lyr-Cache %q, want MISS", got)
	}
}
```

Give each test its own site (or its own `cache.MemoryProvider`), so tests don't see each other's entries. `cache.MemoryOptions{Now: …}` moves time forward without waiting, to test expiry.

## Without a provider

A site without `lyr.WithCache` renders every request, as before. `Set` still records the options (`Options()` returns them), and Lyr logs once per process:

```text
lyr: ctx.Cache().Set was called, but caching is not enabled: configure a provider with lyr.WithCache in app.New()
```

The warning comes from pages and islands; options set only on the `Locals` field in middleware don't trigger it. `Invalidate` and `site.InvalidateCache` return `lyr.ErrCacheNotEnabled`, and route rules do nothing.

## Cost

Measured on an AMD Ryzen 7 9700X with Go 1.27.3, at `-cpu 1`, on the 1.1 KB page of the runtime's allocation tests:

| Case | Time | Allocations |
|---|---:|---:|
| HIT | 154 ns | 0 |
| 304 on a HIT | 181 ns | 0 |
| HIT with a query to normalize (`?b=2&a=1&utm_source=x`) | 228 ns | 0 |
| STALE (the stale copy served; the background render not counted) | 155 ns | 0 |
| MISS that stores (render, entry, copies) | ~1.9 µs | 13 |
| a page without `Set`, on a site with a provider | +~25 ns | the same 1 |
| a site without a provider | unchanged (~620 ns) | 1 |

On one hot key with 16 threads, a HIT costs about 30 ns per request in aggregate. A HIT serves fewer allocations than a render: rendering the page costs 1 (the `Content-Length` value), a HIT 0. See [Performance](performance.md#cached-pages-route-caching).

## Coming from Astro

| Astro | Lyr |
|---|---|
| `Astro.cache.set({ maxAge: 3600, swr: 60, tags: ["a"] })` | `ctx.Cache().Set(lyr.CacheOptions{MaxAge: time.Hour, SWR: time.Minute, Tags: []string{"a"}})` |
| `Astro.cache.set(false)` | `ctx.Cache().Disable()` |
| `Astro.cache.invalidate({ tags })`, `{ path }` | `ctx.Cache().Invalidate(rc, lyr.InvalidateOptions{Tags: …})`, `site.InvalidateCache` |
| `context.cache` in middleware | a `lyr.Cache` field in `Locals` |
| `routeRules` in `astro.config` | `routeRules` in `lyr.json`, with Lyr's route patterns; still whole seconds |
| `memoryCache()` | `cache.NewMemory(cache.MemoryOptions{…})` |
| `cache: { provider }` | `app.New(lyr.WithCache(p))` |
| `X-Astro-Cache` | `X-Lyr-Cache` |

What works differently:

- **Your middleware runs on HITs.** Astro's cache wraps the middleware, so a HIT skips it and `set(false)` in middleware can't stop one.
- **One background refresh per stale key**, and concurrent misses on a cached route wait for one render. Astro starts a refresh per stale request, and renders every miss.
- **304s**: Lyr answers `If-None-Match` and `If-Modified-Since` itself, and gives stored responses an automatic `ETag`.
- **A path purge** drops every query variant and host of the path, as Astro's CDN providers do; Astro's memory provider drops only the exact path and query.
- **Refusal warnings** are logged once per route and reason, not on every request.
- **Byte bounds**: `MaxBytes` on top of Astro's entry count.

## See also

- [Configuration](configuration.md#routerules): `routeRules`
- [Middleware and locals](middleware.md#keeping-requests-out-of-the-cache): the `Locals` `Cache` field
- [Rendering](rendering.md#caching-fragments): caching fragments with `cache.Store`
- [Lazy loading](lazy-loading.md#response-headers-and-caching): cached islands
- [Deploy](deploy.md#caching-in-production): CDNs and several instances
- [Runtime API](reference/runtime-api.md#route-caching): every type and option
