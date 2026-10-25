# Testing

The `orbittest` package lets you unit-test components and pages with plain `go test`, rendering exactly the HTML production would.

## A first component test

Take a `Card` component that shows a blog post:

```go file=internal/posts/posts.go
package posts

import "errors"

// ErrNotFound is returned by Find for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

type Post struct {
	Slug  string
	Title string
}

var all = []Post{
	{Slug: "hello", Title: "Hello, world"},
	{Slug: "second", Title: "A second post"},
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

// Comments returns the comments on a post.
func Comments(slug string) ([]string, error) {
	if slug == "hello" {
		return []string{"First!", "Nice post."}, nil
	}
	return nil, nil
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
  <a href={url}>{props.Post.Title}</a>
  <slot />
  {if slots.Has("footer")}
    <footer><slot name="footer" /></footer>
  {/if}
</article>
<style>.card { border: 1px solid #ccc }</style>
```

This test renders it with props and a default slot, and checks the HTML:

```go file=internal/views_test/card_test.go
package views_test

import (
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/internal/posts"
	_ "example.com/blog/orbit_gen/app"
	card "example.com/blog/orbit_gen/decl/components/card"
)

func TestCard(t *testing.T) {
	html := orbittest.Render(t, card.Render,
		card.Props{
			Post:     posts.Post{Slug: "hello", Title: "Hello, world"},
			Featured: true,
		},
		orbittest.WithSlot("default", "<em>New</em>"),
	)

	for _, want := range []string{`href="/blog/hello"`, "Hello, world", "<em>New</em>", "data-featured"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
}
```

Run it like any Go test, after generating the Go code for your components:

```sh
go tool orbit generate
go test ./internal/views_test
```

`orbittest.Render` calls the component and returns its HTML as a string. If rendering fails, it stops the test with `t.Fatal`, and that includes a page returning `orbit.ErrNotFound` or `ctx.Redirect(…)`. To test a page's 404 or redirect, use [`Get`](#testing-through-http-with-get) and check the status code. It uses the same pooled writer and the same escaping as a real request, so the string is byte-for-byte what your users get, including the `data-o-…` scope attributes.

`Render` calls the component directly. Your `net/http` middleware doesn't run, and no `<link>` or `<script>` tags are added, because a component on its own has no page assets (the same as [`orbit.RenderComponent`](reference/runtime-api.md#rendering-from-your-handlers)). To test a page the way a browser gets it, use [`Get`](#testing-through-http-with-get).

## Where test files go

Put tests in normal Go packages, such as `internal/views_test/` or next to `cmd/server`. Don't put them in `src/`, which holds your `.orbit` files and the `src/locals` package.

A test needs two imports:

- **The component's declaration package**, generated under `orbit_gen/decl/`, mirroring the path under `src/`. `src/components/Card.orbit` is `example.com/blog/orbit_gen/decl/components/card`, and `src/pages/blog/[slug].orbit` is `example.com/blog/orbit_gen/decl/pages/blog/slug_param`. Each one exports `Props` and `Render`.
- **`orbit_gen/app`**, which links every component's implementation. A blank import is enough:

  ```go
  import _ "example.com/blog/orbit_gen/app"
  ```

Without the `orbit_gen/app` import, the component's `Render` isn't linked to its code. It panics with `orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app`, and `orbittest` turns that into a test failure with the same hint:

```
orbittest: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app in this test package:

	import _ "example.com/blog/orbit_gen/app"
```

`orbit_gen/` is generated, so run `go tool orbit generate` before `go test` (in CI too). A running `orbit dev` keeps it up to date while you work. If you edit an `.orbit` file and run `go test` without regenerating, the test runs against the old version.

## Options

`Render` and `Get` take options that set up what a component sees:

| Option | `Render` | `Get` |
|---|---|---|
| `WithSlot(name, html)` | fills a slot | fails the test: a request has no slots |
| `WithParam(name, value)` | sets one route param: a page's `params` field and `ctx.Param(name)`; repeat it for each param | fails the test: the router sets params from the path |
| `WithLocals(l)` | sets `locals` | attaches `l` to the request, as [`orbit.WithLocals`](middleware.md) does, so `EnsureLocals` in your middleware returns it |
| `WithRequest(r)` | sets `ctx.Request` | sends a copy of `r` with its method set to `GET` and its URL set to `path`, so its headers and cookies carry over |

Without `WithRequest`, `Render` uses `httptest.NewRequest("GET", "/", nil)`. An option that doesn't apply fails the test with a message saying so, rather than being silently ignored.

### Slots: `WithSlot`

`orbittest.WithSlot(name, html)` fills a slot with raw HTML. Use `"default"` for the default slot, or a slot's name:

```go file=internal/views_test/card_slots_test.go
package views_test

import (
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/internal/posts"
	card "example.com/blog/orbit_gen/decl/components/card"
)

func TestCardFooter(t *testing.T) {
	props := card.Props{Post: posts.Post{Slug: "hello", Title: "Hello, world"}}

	without := orbittest.Render(t, card.Render, props)
	if strings.Contains(without, "<footer") {
		t.Errorf("footer rendered without a footer slot:\n%s", without)
	}

	with := orbittest.Render(t, card.Render, props,
		orbittest.WithSlot("footer", "<small>2 min read</small>"))
	if !strings.Contains(with, "<small>2 min read</small>") {
		t.Errorf("footer slot missing:\n%s", with)
	}
}
```

This file doesn't repeat the `orbit_gen/app` import: one file per test package is enough.

### Locals: `WithLocals`

If your project declares [typed locals](middleware.md) in `src/locals`, pass them with `orbittest.WithLocals`. Here a `Greeting` component reads `locals.User`:

```go file=internal/auth/auth.go
package auth

// User is a signed-in user.
type User struct {
	Name string
}
```

```go file=src/locals/locals.go
package locals

import "example.com/blog/internal/auth"

type Locals struct {
	User      *auth.User
	RequestID string
}
```

```orbit file=src/components/Greeting.orbit
{if locals.User != nil}
  <p>Signed in as {locals.User.Name}</p>
{else}
  <a href="/login">Sign in</a>
{/if}
```

```go file=internal/views_test/greeting_test.go
package views_test

import (
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/internal/auth"
	"example.com/blog/orbit_gen/decl/components/greeting"
	"example.com/blog/src/locals"
)

func TestGreeting(t *testing.T) {
	anon := orbittest.Render(t, greeting.Render, greeting.Props{})
	if !strings.Contains(anon, "Sign in") {
		t.Errorf("anonymous: %s", anon)
	}

	ada := orbittest.Render(t, greeting.Render, greeting.Props{},
		orbittest.WithLocals(&locals.Locals{User: &auth.User{Name: "Ada"}}))
	if !strings.Contains(ada, "Signed in as Ada") {
		t.Errorf("signed in: %s", ada)
	}
}
```

Without `WithLocals`, the component sees a zero `Locals`, as on a request where no middleware set anything.

### Route params: `WithParam`

Pages are components too. A dynamic page reads its route params from its typed `params`, and `orbittest.WithParam` sets them, one call per param. This post page also defers its comments with [`server:defer`](lazy-loading.md):

```orbit file=src/components/Comments.orbit
---
import "example.com/blog/internal/posts"

type Props struct {
    Slug string
}

list, err := posts.Comments(props.Slug)
if err != nil {
    return err
}
---
<section class="comments">
  {for _, c := range list}
    <p>{c}</p>
  {/for}
</section>
```

```orbit file=src/pages/blog/[slug].orbit
---
import (
    "errors"

    "example.com/blog/internal/posts"
    Card     "../../components/Card.orbit"
    Comments "../../components/Comments.orbit"
)

post, err := posts.Find(params.Slug)
if errors.Is(err, posts.ErrNotFound) {
    return orbit.ErrNotFound
}
if err != nil {
    return err
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>{post.Title}</title>
  </head>
  <body>
    <Card post={post} />
    <Comments server:defer slug={post.Slug}>
      <p slot="fallback">Loading comments…</p>
    </Comments>
  </body>
</html>
```

```go file=internal/views_test/post_test.go
package views_test

import (
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/orbit_gen/decl/pages/blog/slug_param"
)

func TestPostPage(t *testing.T) {
	html := orbittest.Render(t, slug_param.Render, slug_param.Props{},
		orbittest.WithParam("slug", "hello"))
	if !strings.Contains(html, "<title>Hello, world</title>") {
		t.Errorf("unexpected page:\n%s", html)
	}
}
```

The test passes `slug_param.Props{}` because pages never take props: Orbit generates an empty `Props` for every page. `WithParam` takes the name in brackets (`"slug"`), not the field name; the page's `Render` builds `params.Slug` from it, as it does for a real request. For a route with several params, repeat the option: `WithParam("cat", "books"), WithParam("item", "dune")`.

### The request: `WithRequest`

`orbittest.WithRequest` sets `ctx.Request`, for components that read the query string, headers or cookies. This search page reads `?q=`:

```orbit file=src/pages/search.orbit
---
q := ctx.Request.URL.Query().Get("q")
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Search</title>
  </head>
  <body>
    {if q == ""}
      <p>Type something to search.</p>
    {else}
      <p>Results for {q}</p>
    {/if}
  </body>
</html>
```

```go file=internal/views_test/search_test.go
package views_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/orbit_gen/decl/pages/search"
)

func TestSearch(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=go", nil)
	html := orbittest.Render(t, search.Render, search.Props{}, orbittest.WithRequest(req))
	if !strings.Contains(html, "Results for go") {
		t.Errorf("unexpected page:\n%s", html)
	}
}
```

## Testing through HTTP with `Get`

`orbittest.Get` sends a GET request to any `http.Handler` and returns the `*httptest.ResponseRecorder`. Pass `app.New().Handler()` to test pages the way a browser sees them: routing, the trailing-slash redirect, status codes, headers, the injected `<link>`/`<script>` tags and `server:defer` placeholders.

If you build your handler (custom routes and middleware around `site.Middleware`) in a function outside `package main`, pass that handler instead, to test them together with the pages.

```go file=internal/views_test/site_test.go
package views_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/orbit_gen/app"
)

func TestSite(t *testing.T) {
	h := app.New().Handler()

	rec := orbittest.Get(t, h, "/blog/hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /blog/hello: status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Hello, world") {
		t.Errorf("unexpected body:\n%s", rec.Body.String())
	}

	if rec := orbittest.Get(t, h, "/blog/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /blog/nope: status %d, want 404", rec.Code)
	}

	rec = orbittest.Get(t, h, "/search/?q=go")
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/search?q=go" {
		t.Errorf("GET /search/?q=go: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}
```

`Get` doesn't follow redirects, so you can check them, like the 308 above.

## Testing `server:defer` components

A component rendered with `server:defer` isn't rendered as part of its page. The page gets a placeholder holding the `fallback` slot, and the browser fetches the component's HTML in a second request. Test the two halves separately:

- **The page**: `Render` or `Get` returns the placeholder, `<orbit-island src="…" props="…">`, with the fallback inside it. The deferred component doesn't run. Under `go test`, both `Render` and `Get` seal the props with a fixed test island key and the build ID `0000000000000000` (unless you set `ORBIT_KEY` or `WithKey`), so the placeholder is the same on every run and pages with islands can be [snapshot-tested](#snapshot-tests).
- **The component**: render it directly with `orbittest.Render`, like any other component. That's the quickest way to check its HTML.

To test the whole round trip, including your middleware on the island request, `Get` the page, then `Get` the URL the browser would fetch (the placeholder's `src`, plus `?p=` and its `props` token):

```go file=internal/views_test/island_test.go
package views_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/orbit_gen/app"
	"example.com/blog/orbit_gen/decl/components/comments"
)

var islandRe = regexp.MustCompile(`<orbit-island src="([^"]+)" props="([^"]+)">`)

func TestComments(t *testing.T) {
	html := orbittest.Render(t, comments.Render, comments.Props{Slug: "hello"})
	if !strings.Contains(html, "<p>First!</p>") {
		t.Errorf("unexpected comments:\n%s", html)
	}
}

func TestCommentsIsland(t *testing.T) {
	h := app.New().Handler()

	page := orbittest.Get(t, h, "/blog/hello").Body.String()
	m := islandRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no island placeholder in:\n%s", page)
	}
	if !strings.Contains(page, "Loading comments…") {
		t.Errorf("fallback missing:\n%s", page)
	}

	island := orbittest.Get(t, h, m[1]+"?p="+m[2])
	if island.Code != http.StatusOK {
		t.Fatalf("island: status %d", island.Code)
	}
	if !strings.Contains(island.Body.String(), "First!") {
		t.Errorf("unexpected island:\n%s", island.Body.String())
	}
}
```

The page and the island request must reach the same `Site` (or two sites sharing one key), since the token is encrypted with the site's island key. `Get` goes through your handler, so it uses that site's own key and build ID. Under `go test`, `app.New()` uses the same fixed test key and build ID `0000000000000000` as `Render`, unless `ORBIT_KEY` is set or you pass `orbit.WithKey`, so island URLs in a `Get` response are the same on every run too.

## Snapshot tests

`orbittest.Snapshot(t, name, html)` compares HTML with the golden file `testdata/<name>.golden` in the test's package directory:

```go file=internal/views_test/card_snapshot_test.go
package views_test

import (
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/blog/internal/posts"
	card "example.com/blog/orbit_gen/decl/components/card"
)

func TestCardSnapshot(t *testing.T) {
	html := orbittest.Render(t, card.Render, card.Props{
		Post: posts.Post{Slug: "hello", Title: "Hello, world"},
	})
	orbittest.Snapshot(t, "card", html)
}
```

Create or refresh the golden files with `-update`:

```sh
go test ./internal/views_test -update
```

This writes `internal/views_test/testdata/card.golden`. Review it and commit it. Later runs without `-update` fail when the output differs from it, showing where it first differs (line, column, and both versions around it). A missing golden file also fails the test, with a hint to run with `-update`.

`name` can contain `/` to group golden files in directories: `Snapshot(t, "pages/post", html)` uses `testdata/pages/post.golden`, and `-update` creates the directories. A name that's empty, absolute, or has `..`, `.` or empty elements fails the test.

### Hashed asset URLs

Before it compares, and before it writes a golden file, `Snapshot` replaces the parts of the HTML that change from build to build:

| In the HTML | In the golden file |
|---|---|
| `/_orbit/blog-slug-5RXDJ3BM.css` (an asset's content hash) | `/_orbit/blog-slug-*.css` |
| `/_orbit/island/3f9c2a7b1e40d865/…` (an island URL's build ID) | `/_orbit/island/*/…` |

So editing a stylesheet or a script, which changes its hashed file name, doesn't break the snapshots of the pages that link to it. The hash is the 8-character uppercase one in `/_orbit/` file names; other URLs are compared as they are. The `html` you pass to your own assertions isn't changed.

`orbittest` defines the `-update` flag itself. Don't define another `-update` flag in a test package that imports `orbittest` (Go would panic with `flag redefined: update`); if your own golden tests need it, read orbittest's with `flag.Lookup("update")`.

Snapshots are stable across machines: the scope attributes (`data-o-…`) depend only on each component's path in the project, asset hashes and build IDs are normalized as above, and under `go test` `server:defer` placeholders are sealed with a fixed test key and build ID, through `Render` and `Get` alike.

## See also

- [Components](components.md): props, slots and `locals` in templates
- [Middleware and locals](middleware.md): declaring `Locals` and setting them in middleware
- [Lazy loading](lazy-loading.md): `server:defer` and the island endpoint
- [Server](server.md): `app.New()`, `site.Handler()` and your own handlers
- [Runtime API](reference/runtime-api.md#orbittest): the `orbittest` signatures
