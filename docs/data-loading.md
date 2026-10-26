# Loading data

The recommended way to get data into a page is to **load it in the page's frontmatter, all at once, and pass it down as props**. Components then stay fast, plain and easy to test, and the page owns its queries. `orbit.All` runs a page's loads concurrently, so the page waits for its slowest load, not for the sum of them.

This page covers `orbit.All`, loads that depend on each other, sharing data between components without passing it through every layer (a per-request loader in `locals`), what code in a load may touch, and when to reach for `server:concurrent` or `server:defer` instead.

## Load the page's data with `orbit.All`

A book page needs the book, its author and its chapters. The catalog is a Go package of your own; here it fakes a 10 ms service call per method, and every method takes a `context.Context`, as `database/sql`, `net/http` clients and gRPC do:

```go file=internal/catalog/catalog.go
// Package catalog is the client of the book catalog service.
package catalog

import (
	"context"
	"errors"
	"time"
)

type Book struct {
	Slug, Title, AuthorID string
}

type Author struct {
	ID, Name string
}

type Chapter struct {
	Title string
}

var (
	ErrNotFound    = errors.New("catalog: no such book")
	ErrUnavailable = errors.New("catalog: unavailable")
)

// Client calls the catalog service.
type Client struct{}

func (c *Client) Book(ctx context.Context, slug string) (Book, error) {
	if err := call(ctx); err != nil {
		return Book{}, err
	}
	if slug != "dune" {
		return Book{}, ErrNotFound
	}
	return Book{Slug: slug, Title: "Dune", AuthorID: "herbert"}, nil
}

func (c *Client) Author(ctx context.Context, id string) (Author, error) {
	if err := call(ctx); err != nil {
		return Author{}, err
	}
	return Author{ID: id, Name: "Frank Herbert"}, nil
}

func (c *Client) Chapters(ctx context.Context, slug string) ([]Chapter, error) {
	if err := call(ctx); err != nil {
		return nil, err
	}
	return []Chapter{{"Book One: Dune"}, {"Book Two: Muad'Dib"}}, nil
}

// call stands in for a 10 ms network call that honors ctx.
func call(ctx context.Context) error {
	select {
	case <-time.After(10 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

Middleware puts the client in `locals` (see [Middleware and locals](middleware.md)); `Books` is the per-request loader of [the section below](#share-data-without-props-drilling-a-loader-in-locals):

```go file=src/locals/locals.go
// Package locals declares the request-scoped data that middleware hands to pages.
package locals

import "example.com/blog/internal/catalog"

type Locals struct {
	Catalog *catalog.Client
	Books   *catalog.Loader // per request, set by middleware
}
```

The page starts both loads at once. The author needs the book's `AuthorID`, so it's chained inside the book's load:

```orbit file=src/pages/books/[slug].orbit
---
import (
	"context"
	"errors"

	ChapterList "../../components/ChapterList.orbit"
	Layout "../../layouts/Base.orbit"
	"example.com/blog/internal/catalog"
)

var (
	book     catalog.Book
	author   catalog.Author
	chapters []catalog.Chapter
)
err := orbit.All(ctx,
	func(rc context.Context) (err error) {
		book, err = locals.Catalog.Book(rc, params.Slug)
		if errors.Is(err, catalog.ErrNotFound) {
			return orbit.ErrNotFound // the 404 page
		}
		if err != nil {
			return err
		}
		author, err = locals.Catalog.Author(rc, book.AuthorID) // needs the book
		return err
	},
	func(rc context.Context) (err error) {
		chapters, err = locals.Catalog.Chapters(rc, params.Slug)
		return err
	},
)
if err != nil {
	return err
}
---
<Layout title={book.Title}>
  <h1>{book.Title}</h1>
  <p>by {author.Name}</p>
  <ChapterList chapters={chapters} />
</Layout>
```

```orbit file=src/components/ChapterList.orbit
---
import "example.com/blog/internal/catalog"

type Props struct {
	Chapters []catalog.Chapter
}
---
<ol>
  {for _, c := range props.Chapters}
    <li>{c.Title}</li>
  {/for}
</ol>
```

`/books/dune` takes about 20 ms (the book, then its author, while the chapters load alongside) instead of 30 ms one after the other. `ChapterList` receives plain data, so it renders in a few hundred nanoseconds and its tests need no fake catalog.

It reads like Astro's `const [book, chapters] = await Promise.all([…])`. The Go shape: declare the variables with `var`, assign each one from one load, and read them after `orbit.All` returns.

```go
func All(c *Ctx, loads ...func(context.Context) error) error
```

- **Every load starts at once** and `All` **waits for all of them**, even after one fails, before it returns, so the template never reads a variable a load is still writing. A dynamic list works too: `orbit.All(ctx, loads...)`.
- **The context**: each load gets `rc`, derived from the request's context. It's cancelled as soon as a load fails, and when `All` returns. Pass `rc` to your calls (not `ctx.Request.Context()`), and a page whose chapter call failed in 5 ms doesn't wait 100 ms for its book call. Don't start background work with `rc` that outlives the load.
- **Name it `rc`**, not `ctx`: a parameter named `ctx` hides Orbit's `ctx` inside the load, so `ctx.Param` there no longer compiles.
- **No limit**: every load runs at once. One query that fetches what you need beats N loads; for a long list of calls, batch them, or use `errgroup.SetLimit` from `golang.org/x/sync` inside one load.

### Errors

`All` returns the **first error in time**, or `nil`. That's the cause: once a load fails, the others see a cancelled `rc` and usually fail with `context.Canceled`, which doesn't replace it. If two loads fail on their own at the same moment, either error may be the one returned; the page fails either way.

- **The failing load's position**: the error is prefixed with the `.orbit` line of the failing load's `func`, so the 500 log line, the dev error overlay and your [error handler](rendering.md#answering-errors-yourself-witherrorhandler) name the load that failed, such as the chapters load at line 28: `src/pages/books/[slug].orbit:28: catalog: unavailable`. `errors.Is` and `errors.As` see through it, so a handler that answers `catalog.ErrUnavailable` with a 503 works unchanged.
- **404s and redirects**: return `orbit.ErrNotFound` or `ctx.Redirect(…)` from a load, as the book load above does. They come out of `All` as they are, and the page returns them as usual.
- **It's a value**: handle it to render a partial page.

  ```go
  if errors.Is(err, catalog.ErrUnavailable) {
      unavailable = true // the template shows a notice
  } else if err != nil {
      return err
  }
  ```

  The variables of a load that failed or was cancelled hold whatever it assigned before it returned, usually zero values.

### Panics

A panic in a load doesn't crash the server: it's recovered in the load, the other loads are cancelled, and once they've all returned, `All` panics again with it on the page's goroutine. The request then fails as for any panic in a frontmatter: the 500 page (or your error handler), with the panicking line of the load in the log, for example `src/pages/books/[slug].orbit:25: panic: assignment to entry in nil map`. A panic stays a panic, so a page that handles `All`'s error can't swallow it by mistake.

## Loads that depend on each other

| Need | How |
|---|---|
| A chain next to independent loads (book, then author; chapters alongside) | chain the calls inside one load, as above |
| A fan-out after one load (book, then author and reviews) | a nested `orbit.All(ctx, …)` inside the load, after the book |
| Stages (everything in the second needs everything in the first) | two `orbit.All` calls in a row |
| A dependency several loads, or the page and its components, need | the per-request [loader in `locals`](#share-data-without-props-drilling-a-loader-in-locals) |

A fan-out after the book:

```go
err := orbit.All(ctx,
	func(rc context.Context) (err error) {
		if book, err = locals.Catalog.Book(rc, params.Slug); err != nil {
			return err
		}
		return orbit.All(ctx,
			func(rc context.Context) (err error) { author, err = locals.Catalog.Author(rc, book.AuthorID); return err },
			func(rc context.Context) (err error) { reviews, err = locals.Reviews.For(rc, book.Slug); return err },
		)
	},
	func(rc context.Context) (err error) { chapters, err = locals.Catalog.Chapters(rc, params.Slug); return err },
)
```

The inner `All` returns its first error, with its own load's position, and the outer `All` returns that. Its loads get a context derived from the request's, not from the outer `rc`: if the chapters load fails while the author and reviews are loading, those two aren't cancelled; the outer `All` waits for them, then returns the chapters error.

## Share data without props drilling: a loader in `locals`

When several components need the same data (the book, the signed-in reader, their shelves), passing it down through every layer gets tedious. Instead, give each request a **memoizing loader** in `locals`: every caller asks for `Book(slug)`; the first call loads it, and the others (on the page, in any component, at the same time or later) get the same result. It's about 20 lines of your own code:

```go file=internal/catalog/loader.go
package catalog

import (
	"context"
	"sync"
)

// Loader loads books once per request. Create one per request, in middleware.
type Loader struct {
	ctx   context.Context // the request's
	api   *Client
	mu    sync.Mutex
	books map[string]func() (Book, error)
}

func NewLoader(ctx context.Context, api *Client) *Loader {
	return &Loader{ctx: ctx, api: api, books: map[string]func() (Book, error){}}
}

// Book returns the book, loading it once per request: concurrent callers
// wait for the first one, and get its result, error included.
func (l *Loader) Book(slug string) (Book, error) {
	l.mu.Lock()
	f, ok := l.books[slug]
	if !ok {
		f = sync.OnceValues(func() (Book, error) { return l.api.Book(l.ctx, slug) })
		l.books[slug] = f
	}
	l.mu.Unlock()
	return f()
}
```

Middleware creates it for each request, with the request's context:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/orbit"

	"example.com/blog/internal/catalog"
	"example.com/blog/orbit_gen/app"
	"example.com/blog/src/locals"
)

func main() {
	site := app.New()
	api := &catalog.Client{}
	if err := orbit.ListenAndServe(withCatalog(api, site.Handler())); err != nil {
		log.Fatal(err)
	}
}

// withCatalog gives each request the catalog client and its own loader.
func withCatalog(api *catalog.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.Catalog = api
		l.Books = catalog.NewLoader(r.Context(), api)
		next.ServeHTTP(w, r)
	})
}
```

A component reads the book itself, with no `book` prop:

```orbit file=src/components/BookTitle.orbit
---
type Props struct {
	Slug string
}

book, err := locals.Books.Book(props.Slug)
if err != nil {
	return err
}
---
<cite>{book.Title}</cite>
```

And the page can warm the loader with `orbit.All`, next to its other loads, so the components then hit the cache:

```orbit file=src/pages/reading/[slug].orbit
---
import (
	"context"

	BookTitle "../../components/BookTitle.orbit"
	Layout "../../layouts/Base.orbit"
	"example.com/blog/internal/catalog"
)

var chapters []catalog.Chapter
err := orbit.All(ctx,
	func(context.Context) error { _, err := locals.Books.Book(params.Slug); return err },
	func(rc context.Context) (err error) {
		chapters, err = locals.Catalog.Chapters(rc, params.Slug)
		return err
	},
)
if err != nil {
	return err
}
---
<Layout title="Reading">
  <h1>Reading <BookTitle slug={params.Slug} /></h1>
  <p>{len(chapters)} chapters</p>
</Layout>
```

**The one rule: the loader uses the request's context, set in middleware, not a load's `rc`.** A memoized call is shared by every caller of the request. If it used the `rc` of the first caller, and that caller's `All` were cancelled by a failing sibling, the loader would cache `context.Canceled` for everyone after it, such as a page that handles `All`'s error and renders a partial page. With the request's context, shared loads still stop when the client goes away.

- It dedupes by key across the page and every component, including `server:concurrent` ones (the mutex makes it safe). A `server:defer` island is its own request, with its own loader.
- Errors are memoized too: one failing backend call per request, not one per component.
- **A load that asks for its own key deadlocks**: if loading book K (indirectly) calls `Book(K)`, `sync.OnceValues` waits for itself: the request never gets an answer, and its goroutine stays blocked even after the client gives up. Keep each key's load free of calls back into the loader for the same key.

Orbit ships no helper for this on purpose: the key types, the context rule and what to memoize are your app's decisions, and it's short.

## What's safe inside a load

Loads run on other goroutines while the frontmatter waits in `All`, so they share the render's state:

| Inside a load | Safe? |
|---|---|
| `rc`, `ctx.Request` (headers, cookies, URL, `Context()`), `ctx.URL`, `ctx.Site`, `ctx.Param`, `params`, `props` | yes: nothing changes them during the render |
| `locals` | yes for the pointer and for fields middleware set; fields that loads write need a `sync` type or a mutex (the loader above has one) |
| the frontmatter's own variables | assign each from one load only, and read them after `All` returns |
| `ctx.SetStatus`, `ctx.Header()` | no: call them after `All` returns. Returning `ctx.Redirect(…)` from a load is fine: it only builds the error |
| `orbit.RenderHTML(ctx, …)`, rendering a component | no: render after `All` |
| `ctx.Request.Body` | read it once, before `All` |

Orbit doesn't check these at run time. Run your tests with `go test -race`: `orbittest.Get` and `orbittest.Render` run loads concurrently, as in production, and the race detector reports the mistakes in the table.

## Which tool

| Tool | Use it when | The first byte waits? | Cost |
|---|---|---|---|
| **Page-level loads with `orbit.All`** (recommended) | the page knows what it needs; most pages | for the slowest load | about 0.5 µs and 5 allocations per call (3 loads), plus the closures; a single load runs inline for free |
| **A loader in `locals`** | several components (or the page and its components) need the same data; no props drilling; dedupe | for the slowest load | a mutex and a map entry per key |
| **[`server:concurrent`](rendering.md#slow-components)** | components that fetch their own data, such as code ported from Astro, or a component reused on many pages with its own data | for the slowest marked component | about half a microsecond and 1 allocation per marked component |
| **[`server:defer`](lazy-loading.md)** | the part can arrive after the page: below the fold, per-user parts, slow third parties | no | a second request, a fallback, sealed props |

They combine: a page can load its main data with `orbit.All`, warm the loader, and still have a `server:concurrent` sidebar and a `server:defer` recommendations block.

## See also

- [Rendering](rendering.md): the request lifecycle, errors, `WithErrorHandler`
- [Middleware and locals](middleware.md): setting up `locals`
- [Runtime API](reference/runtime-api.md#loading-data-all): `orbit.All`
- [Performance](performance.md#loading-data-concurrently-orbitall): the numbers
