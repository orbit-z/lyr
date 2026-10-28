# Migrating from Astro

This guide ports an Astro site to Orbit step by step, in the order the work usually goes: project, components, routes, data, middleware, errors, styles, scripts, then checking that the new site renders the same HTML. The examples are a small bookshop. For a one-line answer to "what's the Orbit version of X?", see the table in [Coming from Astro](coming-from-astro.md).

## 1. Check that your site fits

Orbit keeps Astro's components and file-based routing, and replaces the server side with Go. Before you start, check three things:

- **Every page renders per request.** Orbit is SSR-only: there's no `output: "static"`, `getStaticPaths()` or `prerender`. A site that is mostly static pages is better off staying on Astro. Files that never change go in `public/`, as before. See [Rendering](rendering.md#one-mode-server-rendering).
- **Client code is vanilla `<script>`.** There are no framework islands (React, Svelte, Vue): importing a `.tsx`, `.svelte` or `.vue` file is a compile error. `client:load`, `client:idle`, `client:visible` and `client:media` exist, but they delay a component's scripts, not hydration. See [No framework components](components.md#no-framework-components).
- **Your server-side TypeScript becomes Go.** Frontmatter, middleware, endpoints and the libraries they call run in Go. Code shared between the server and the browser exists twice, in Go for the server and in TypeScript for the browser. This is usually the largest part of a port, so estimate it first.

## 2. Create the project

Create the Orbit site next to the Astro one, so you can run both while you port:

```sh
orbit new blog --module example.com/blog
cd blog
orbit dev --port 4322   # the Astro dev server keeps 4321
```

Then move over what doesn't change:

- **`public/`**: copy it as is. It's served at the same paths.
- **`tsconfig.json` `paths`**: the starter maps `@/*` to `./src/*`, like Astro's usual alias. Copy any other alias you use. Orbit reads the same `compilerOptions.paths` for `.orbit` imports (`Card "@/components/Card.orbit"`) and esbuild reads them for script imports. See [Import aliases](components.md#import-aliases).
- **npm packages for the browser**: keep your `package.json` and install as usual. Scripts import from `node_modules`. Server-side packages are replaced by Go packages. See [npm packages](styles-and-scripts.md#npm-packages).
- **`astro.config`**: the few options that carry over (`site`, `trailingSlash`, `compressHTML`, `markdown`) go in `orbit.json`. See [Configuration](configuration.md).

Your own Go code (API clients, view helpers, the logic you port from `src/lib`) goes under `internal/`. The examples below use this stand-in for the shop's API client (with the module path `example.com/blog`, as on every page of these docs):

```go file=internal/shop/shop.go
// Package shop is the bookshop's catalog client: a stand-in for your API.
package shop

import (
	"context"
	"errors"
)

var (
	ErrNotFound    = errors.New("shop: no such book")
	ErrUnavailable = errors.New("shop: catalog unavailable")
)

type Book struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Author  string   `json:"author"`
	Price   int      `json:"price"` // in cents
	InStock bool     `json:"inStock"`
	Tags    []string `json:"tags"`
}

type Review struct {
	Reader, Text string
}

// Client calls the catalog API.
type Client struct{}

func (c *Client) Book(ctx context.Context, slug string) (Book, error) {
	if err := ctx.Err(); err != nil {
		return Book{}, err
	}
	if slug != "the-salt-road" {
		return Book{}, ErrNotFound
	}
	return Book{Slug: slug, Title: "The Salt Road", Author: "Mira Okafor", Price: 1850, InStock: true, Tags: []string{"travel", "history"}}, nil
}

func (c *Client) Reviews(ctx context.Context, slug string) ([]Review, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []Review{{Reader: "Sam", Text: "Read it in one sitting."}}, nil
}
```

## 3. Port layouts and components

Start with the leaves (buttons, cards, icons), then the layouts, then the pages: a page compiles only once everything it imports does. Rename `X.astro` to `X.orbit` and work through the frontmatter first.

### Frontmatter and props

A book card in Astro:

```astro
---
import type { Book } from "@/lib/shop";
import { formatPrice } from "@/lib/format";

interface Props {
  book: Book;
  featured?: boolean;
  href?: string;
}

const { book, featured = false, href } = Astro.props;
const Tag = href ? "a" : "div";
---
<Tag href={href} class:list={["card", { featured }]}>
  <h3>{book.title}</h3>
  <p>{book.author} · {formatPrice(book.price)}</p>
  {book.inStock ? <p>In stock</p> : <p class="muted">Out of stock</p>}
  {book.tags.length > 0 && (
    <ul>
      {book.tags.map((tag) => <li>{tag}</li>)}
    </ul>
  )}
  <span aria-label={featured ? "Staff pick" : undefined}>★</span>
</Tag>
```

In Orbit:

```orbit file=src/components/BookCard.orbit
---
import (
	"fmt"

	"example.com/blog/internal/shop"
)

type Props struct {
	Book     shop.Book
	Featured bool
	Href     string
}

// Hoisted to package level: compiled once.
func price(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

Tag := orbit.Cond(props.Href != "", "a", "div")
---
<Tag href={orbit.NonZero(props.Href)} class:list={"card", "featured": props.Featured}>
  <h3>{props.Book.Title}</h3>
  <p>{props.Book.Author} · {price(props.Book.Price)}</p>
  {if props.Book.InStock}
    <p>In stock</p>
  {else}
    <p class="muted">Out of stock</p>
  {/if}
  {if len(props.Book.Tags) > 0}
    <ul>
      {for _, tag := range props.Book.Tags}
        <li>{tag}</li>
      {/for}
    </ul>
  {/if}
  <span aria-label={orbit.If(props.Featured, "Staff pick")}>★</span>
</Tag>
```

Step by step:

- **Imports**: Go packages and components share one `import (…)` block. A component import needs an alias: `Card "./Card.orbit"`. An unused component import is an error, like an unused Go import.
- **`interface Props` → `type Props struct`**, with exported fields. `Astro.props.book` is `props.Book`; attributes match fields ignoring case, so callers still write `<BookCard book={b} featured />`. An unset field is Go's zero value, so `featured = false` needs nothing, and other defaults are a line of code (`title := cmp.Or(props.Title, "Untitled")`). A prop whose absence leaves an attribute out (`rel?: string`) is `Rel orbit.Opt[string]`, and `rel ?? "x"` is `props.Rel.Or("x")`. See [Props](components.md#props) and [Optional props](components.md#optional-props).
- **Helpers**: a named `func` (and `import`, `type`, `const`) is hoisted to package level, where it can't see `props` or `ctx`: pass it what it needs. Everything else runs on every render. See [The frontmatter](components.md#the-frontmatter).
- **Ternaries**: `cond ? v : undefined` on an attribute is `orbit.If(cond, v)` (an `orbit.Opt`, set or none, and none omits the attribute), `x || undefined` is `orbit.NonZero(x)`, and `a ? b : c` on a value is `orbit.Cond(a, b, c)`. When the branches are different markup, use `{if}…{else}…{/if}`. See [Conditional attributes](components.md#conditional-attributes).
- **`&&` and `.map()`** become `{if}` and `{for}` blocks. `.filter()` becomes an `if` inside the loop, or a filtered slice built in the frontmatter. A chain of conditions on one value is a `{switch}`. See [Control flow](components.md#control-flow).
- **Dynamic tags** port as they are, with `orbit.Cond` for the ternary: `Tag := orbit.Cond(props.Href != "", "a", "div")`. `orbit.Fragment` as the value renders only the children, for optional wrappers. See [Dynamic tags](components.md#one-element-or-another-dynamic-tags).
- **`class:list`** is one flat list without `[…]` and `{…}`: `{"card", "featured": props.Featured}`. `cond && "x"` becomes `"x": cond`, and a ternary `orbit.Cond(c, "a", "b")`. See the side-by-side table in [`class:list`](reference/template-syntax.md#classlist).
- **Template literals** port unchanged: `` class={`card ${extra}`} ``, `` href={`/books/${book.Slug}`} ``, `` <Card title={`Chapter ${n}`} /> ``, with pieces of any type (an `int` needs no `strconv`). Two things to check: a backslash escape JavaScript accepts silently (`\d`) is an error, and `${x}` with a nil `x` prints nothing rather than `null`. A quoted attribute value is literal text. See [Template literals](reference/template-syntax.md#template-literals).
- **`x ?? y`** is `cmp.Or(x, y)` (or `o.Or(y)` for an `orbit.Opt`), and optional chaining (`a?.b`) is an explicit `nil` check in the frontmatter.

`<Fragment>` and its `<>…</>` shorthand, `<slot />`, named slots, `slot="x"` and `<Self />` (Astro's `<Astro.self />`) work as in Astro. See [Slots](components.md#slots) and [`<Fragment>`](components.md#fragment).

### Pass-through attributes: `...rest`

A button that forwards every other attribute, in Astro:

```astro
---
interface Props extends HTMLAttributes<"button"> {
  variant?: "primary" | "quiet";
}

const { variant = "primary", ...rest } = Astro.props;
---
<button {...rest} class:list={["btn", `btn-${variant}`]}><slot /></button>
```

In Orbit, an `orbit.RestAttrs` field collects the attributes that match no other prop:

```orbit file=src/components/Button.orbit
---
import "cmp"

type Props struct {
	Variant string
	Attrs   orbit.RestAttrs
}

variant := cmp.Or(props.Variant, "primary")
---
<button {...props.Attrs} class:list={"btn", "btn-" + variant}><slot /></button>
```

`<Button variant="quiet" type="submit" name="intent" value="buy">Buy</Button>` renders `<button type="submit" name="intent" value="buy" class="btn btn-quiet">Buy</button>`. A passed `class` is merged into the element's `class:list` instead of being written twice. `rest.href` is `props.Attrs.Get("href")`. See [Passing HTML attributes through](components.md#passing-html-attributes-through).

### Slots rendered to a string

`await Astro.slots.render("default")` is `orbit.RenderHTML(ctx, slots.Default)`, which returns an `orbit.HTML` and an error:

```go
html, err := orbit.RenderHTML(ctx, slots.Default)
if err != nil {
	return err
}
```

Write it with `<Fragment set:html={html} />`. `Astro.slots.has("x")` is `slots.Has("x")`. A slot called as a function (`Astro.slots.render("default", [item])`) has no equivalent: pass the data to a component as props instead. See [Rendering a slot to a string](components.md#rendering-a-slot-to-a-string).

### Raw HTML: `set:html`

`set:html` takes only an `orbit.HTML` value, so every raw string is converted explicitly where it's trusted:

```orbit
<div class="prose" set:html={orbit.HTML(post.BodyHTML)}></div>
```

A plain `string` there is a compile error. `set:html` on `<script>` and `<style>` makes them inline, as in Astro. JSON for a script is better written with `set:json`, which escapes it for you (see [step 9](#9-port-the-scripts)). See [Raw HTML](components.md#raw-html-with-orbithtml).

## 4. Port the routes

Pages keep their paths: `src/pages/books/[slug].astro` becomes `src/pages/books/[slug].orbit`, `index`, `404` and `[...rest]` included.

- **Params are typed.** `Astro.params.slug` is `params.Slug`, a field of a `Params` struct that Orbit generates from the file path, so a typo is a build error. Components read `ctx.Param("slug")`. See [Route params](routing.md#route-params-params).
- **Pages take no props**, since there's no `getStaticPaths`: they read `params`, `ctx.Request` and `ctx.URL`.
- **A parameter is a whole segment.** `post-[id].astro` is a build error in Orbit: rename it to `[id].orbit` and parse the value in the page.
- **Not found**: `return new Response(null, { status: 404 })` or a rewrite to `/404` becomes `return orbit.ErrNotFound`, which renders `404.orbit` with status 404. See [The 404 page](routing.md#the-404-page).
- **Redirects**: `return Astro.redirect("/signin", 303)` is `return ctx.Redirect("/signin", http.StatusSeeOther)`; the status is required. See [Redirects](rendering.md#redirects).
- **`redirects` in `astro.config`** become handlers in `cmd/server/main.go`: `mux.Handle("GET /shop", http.RedirectHandler("/", http.StatusMovedPermanently))` (see the [server below](#6-port-the-middleware)).
- **Endpoints** (`src/pages/api/*.ts`) become `net/http` handlers on the same mux. See [Server](server.md#choosing-precedence).
- **Trailing slashes**: set `"trailingSlash"` in `orbit.json` to the mode your site uses, `"never"` (the default) or `"always"`. Astro's `'ignore'` has no equivalent: each page has one URL. The other form of a page URL gets a **308** redirect, for every method, where Astro's server sends a 301 for `GET`. Wrap your handler in `site.TrailingSlash`, outermost, as `orbit new`'s `main.go` does, so the redirect comes before your middleware, as in Astro; unlike Astro's, it redirects only paths whose other form is a page, never your own routes. See [Trailing slashes](routing.md#redirecting-before-your-middleware).

## 5. Move data loading into the page

In Astro, components often fetch their own data, and Astro overlaps the awaiting siblings. Orbit renders components in order, so the recommended shape is: **the page loads everything at once with `orbit.All`, and passes plain props down**. Components then stay fast and easy to test.

An Astro page:

```astro
---
const [book, reviews] = await Promise.all([
  Astro.locals.shop.book(Astro.params.slug),
  Astro.locals.shop.reviews(Astro.params.slug),
]);
if (!book) return new Response(null, { status: 404 });
---
```

In Orbit:

```orbit file=src/pages/books/[slug].orbit
---
import (
	"context"
	"errors"

	AddToCart "@/components/AddToCart.orbit"
	BookCard "@/components/BookCard.orbit"
	Base "@/layouts/Base.orbit"
	"example.com/blog/internal/shop"
)

var (
	book    shop.Book
	reviews []shop.Review
)
err := orbit.All(ctx,
	func(rc context.Context) (err error) {
		book, err = locals.Shop.Book(rc, params.Slug)
		if errors.Is(err, shop.ErrNotFound) {
			return orbit.ErrNotFound
		}
		return err
	},
	func(rc context.Context) (err error) {
		reviews, err = locals.Shop.Reviews(rc, params.Slug)
		return err
	},
)
if err != nil {
	return err
}
---
<Base title={book.Title}>
  <BookCard book={book} featured />
  <AddToCart book={book} />
  <h2>Reviews</h2>
  {for _, r := range reviews}
    <blockquote>{r.Text} ({r.Reader})</blockquote>
  {/for}
</Base>
```

Declare the variables with `var`, assign each from one load, and check `err` once. The loads get `rc`, a `context.Context` that's cancelled as soon as one fails. See [Loading data](data-loading.md).

Then, depending on how much you want to restructure:

- **Data many components need** (the signed-in reader, the cart): put a per-request memoizing loader in `locals`, so components ask for it without props drilling and each key loads once per request. See [A loader in `locals`](data-loading.md#share-data-without-props-drilling-a-loader-in-locals).
- **Components you port as they are**, still fetching in their frontmatter: mark each slow one with `server:concurrent` at its call site, and they overlap as they did in Astro. See [Slow components](rendering.md#slow-components).

  ```orbit
  <Reviews server:concurrent slug={params.Slug} />
  <Related server:concurrent slug={params.Slug} />
  ```

- **Server islands** (`server:defer` with a `slot="fallback"`) port unchanged. Props must be JSON-serializable, and rolling deploys need an `ORBIT_KEY`, as Astro's need `ASTRO_KEY`. See [Lazy loading](lazy-loading.md#serverdefer-deferred-server-rendering).

## 6. Port the middleware

`onRequest` in `src/middleware.ts` becomes plain `net/http` middleware in `cmd/server/main.go`, and `App.Locals` in `src/env.d.ts` becomes a Go struct in `src/locals`:

```go file=src/locals/locals.go
// Package locals declares the request-scoped data that middleware hands to pages.
package locals

import "example.com/blog/internal/shop"

type Locals struct {
	Shop *shop.Client
}
```

```go file=cmd/server/main.go
package main

import (
	"errors"
	"log"
	"net/http"

	"github.com/orbit-z/orbit"

	"example.com/blog/internal/shop"
	"example.com/blog/orbit_gen/app"
	"example.com/blog/src/locals"
)

func main() {
	site := app.New(orbit.WithErrorHandler(answerError))
	api := &shop.Client{}

	mux := http.NewServeMux()
	// Astro's redirects: { "/shop": "/" } in astro.config.
	mux.Handle("GET /shop", http.RedirectHandler("/", http.StatusMovedPermanently))
	mux.Handle("/", site.Handler())

	// Trailing-slash redirects first, before the middleware, as in Astro.
	if err := orbit.ListenAndServe(site.TrailingSlash(withShop(api, mux))); err != nil {
		log.Fatal(err)
	}
}

// withShop is the Astro middleware that set locals.shop.
func withShop(api *shop.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.Shop = api
		next.ServeHTTP(w, r)
	})
}

// answerError is the try/catch around next() in the Astro middleware.
func answerError(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, shop.ErrUnavailable) {
		return false // Orbit's 500 and its log line
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "The shop is closed for a minute. Try again soon.", http.StatusServiceUnavailable)
	return true
}
```

- **`Astro.locals.shop`** is `locals.Shop` in pages and components, type-checked.
- **`sequence(a, b)`** is `a(b(mux))`: the middleware listed first runs first.
- **Returning a `Response`** from middleware is writing it and not calling `next`. See [Short-circuit responses](middleware.md#short-circuit-responses).
- **Code after `await next()`** goes after `next.ServeHTTP(w, r)`, with a wrapped `http.ResponseWriter` to read the status. See [Rewrite responses](middleware.md#rewrite-responses).
- **The matched route** (Astro's `context.routePattern` and `context.params`, for logging or metrics): add a `Route orbit.RouteInfo` field to your `Locals`, and Orbit fills it in with the route it matched, so middleware can read it after `next` returns:

  ```go
  type Locals struct {
  	Shop  *shop.Client
  	Route orbit.RouteInfo
  }
  ```

  See [Middleware and locals](middleware.md) for its fields.

There's one middleware model, and Orbit ships no auth: sessions, cookies and security headers are your middleware's job, with the standard library or the Go packages you pick. See [Middleware and locals](middleware.md) and [Server](server.md).

### Route caching

If your Astro site uses `Astro.cache` or `routeRules`, the port is mostly mechanical:

- **The provider**: Astro's `cache: { provider: memoryCache({ max: 5000 }) }` becomes `app.New(orbit.WithCache(cache.Memory(cache.MemoryOptions{MaxEntries: 5000})))` in `cmd/server/main.go`, with `github.com/orbit-z/orbit/cache`.
- **`routeRules`** move from `astro.config` to `orbit.json` under the same name, still in whole seconds. Patterns use Orbit's route syntax: `/blog/[...path]` covers `/blog` and everything under it. See [`routeRules`](configuration.md#routerules).
- **`Astro.cache.set({ maxAge: 3600, tags: ["post:" + id] })`** becomes `ctx.Cache().Set(orbit.CacheOptions{MaxAge: time.Hour, Tags: []string{"post:" + id}})`: durations are `time.Duration` (`3600` seconds is `time.Hour`), not numbers. `Astro.cache.set(false)` becomes `ctx.Cache().Disable()`, and `invalidate` becomes `ctx.Cache().Invalidate` or `site.InvalidateCache` in your handlers.
- **`context.cache` in middleware** becomes a field of type `orbit.Cache` in your `Locals`:

  ```go
  type Locals struct {
  	Shop  *shop.Client
  	Cache orbit.Cache
  }
  ```

  Middleware then calls `l.Cache.Disable()` for a signed-in visitor. In Astro that only kept their response out of the store, and they could still get a cached copy; in Orbit, the lookup runs after your middleware, so they never do. If your Astro middleware worked around that, the workaround can go.
- **Your middleware runs on HITs.** Anything it does (logging, a session refresh, auth redirects) now happens for cached requests too. A middleware that sets a cookie on every request keeps every page out of the cache: Orbit never stores a response with `Set-Cookie`.
- **`X-Astro-Cache`** checks in tests or monitoring become `X-Orbit-Cache`, with the same `HIT`, `MISS` and `STALE` values.

See [Caching](caching.md) for the whole flow and the safety rules.

## 7. Map errors to responses

Astro middleware that catches a typed error around `next()` and turns it into a redirect or a 503 becomes an error handler, as `answerError` above: Orbit answers a page's error before `next` returns, so middleware can't catch it. The handler gets every error that would become the 500, from the page, any component, deferred islands and panics, before anything is written, and returns `false` for the ones it leaves to Orbit. `orbit.ErrNotFound` and redirects never reach it. See [Answering errors yourself](rendering.md#answering-errors-yourself-witherrorhandler).

Errors from the frontmatter are values: `return err` ends the render. A frontmatter `throw` becomes `return err`, and `try`/`catch` around a call becomes an `if err != nil`.

## 8. Port the styles

- **Scoped `<style>`**, `:global(…)` and `<style is:global>` work as in Astro, scoped with an attribute selector like Astro's default strategy. See [Styles and scripts](styles-and-scripts.md#how-scoping-works).
- **CSS imported from a frontmatter** (`import "../styles/global.css"`) moves into a global style block, since the frontmatter is Go:

  ```orbit
  <style is:global>
    @import "../styles/global.css";
  </style>
  ```

- **Tailwind v4** works through Tailwind's CLI, without `@tailwindcss/vite`: remove the Vite plugin, install the CLI, and keep `global.css` as it is (`@import "tailwindcss"`, `@theme`, `@utility`, `@custom-variant`). `@reference` with `@apply` in a component style works too. See [Using Tailwind](tailwind.md) and the [Tailwind steps](coming-from-astro.md#tailwind-css).
- **Dynamic `style` values**: Orbit warns on every `style={expr}` that isn't a constant, since escaping doesn't make a value safe as CSS. Build trusted values in a helper that returns `orbit.CSS`, and the warning goes away. Astro's object form (`style={{ color: "red" }}`) isn't supported: build the string. See [Event handler and style attributes](components.md#event-handler-and-style-attributes).

## 9. Port the scripts

Processed `<script>` blocks are TypeScript, bundled by esbuild, as in Astro, with `import()` code splitting and shared chunks. Most scripts move over unchanged; what changes is how they get data from the server.

`define:vars` isn't supported. Write the values into a `data-*` attribute with `orbit.JSON`, and read them back with `data(el, key)` from the component's own module, typed from the Go value:

```orbit file=src/components/AddToCart.orbit
---
import "example.com/blog/internal/shop"

type Props struct {
	Book shop.Book
}
---
<add-to-cart data-book={orbit.JSON(props.Book)}>
  <button type="button" disabled={!props.Book.InStock}>Add to cart</button>
</add-to-cart>
<script>
  import { data } from "./AddToCart.orbit";

  class AddToCart extends HTMLElement {
    connectedCallback() {
      const book = data(this, "book"); // shop.Book
      this.querySelector("button")!.addEventListener("click", () => {
        this.dispatchEvent(new CustomEvent("cart:add", { detail: book.slug }));
      });
    }
  }

  customElements.define("add-to-cart", AddToCart);
</script>
```

`orbit check` type-checks the script against the Go struct (with Bun installed), so renaming a Go field breaks the script at build time instead of in the browser. For a JSON block, `<script type="application/json" set:json={v}>` replaces `set:html={JSON.stringify(v)}`. See [Typed data with `orbit.JSON`](styles-and-scripts.md#typed-data-with-orbitjson) and [Client modules](reference/client-modules.md).

## 10. Check parity

Port page by page and compare the HTML each server sends. A differential test does this for you: run both sites against the same backend (or the same stub API), fetch a list of paths with the same cookies from each, normalize both responses, and diff them. A sketch:

```go
for _, path := range paths {
	a, o := fetch(astroURL+path), fetch(orbitURL+path)
	if a.Status != o.Status || a.Header.Get("Location") != o.Header.Get("Location") {
		t.Errorf("%s: status %d, %q; want %d, %q", path, o.Status, o.Header.Get("Location"), a.Status, a.Header.Get("Location"))
	}
	if d := diff(normalize(a.Body), normalize(o.Body)); d != "" {
		t.Errorf("%s: HTML differs:\n%s", path, d)
	}
}
```

`normalize` removes what's framework-specific and doesn't change what the browser renders:

- the asset tags (`/_astro/…` and `/_orbit/…` stylesheets, module scripts, `modulepreload` links) and the `generator` meta tag;
- the scope attributes, `data-astro-cid-…` and `data-o-…`, replaced with one placeholder;
- character references in attribute values, decoded and written back one way;
- then one tag per line, so the diff is readable.

Include the cases that matter: signed in and out, empty lists, missing records (the 404), error paths, query parameters. Keep the test after the port: it's your regression suite until Astro is switched off. [Testing](testing.md) covers Orbit's own component and HTTP tests.

Some differences are on purpose, so expect them in the diff or normalize them:

- **The trailing-slash redirect is a 308**, for every method, where Astro sends a 301 for `GET`.
- **`<` and `>` in attribute values** are written as `&lt;` and `&gt;`, which Astro leaves raw. Browsers read both the same.
- **`orbit.Attrs` spreads are sorted by name**, since a Go map has no order. Where the order matters, use an `orbit.AttrList`, which keeps it. See [Spread attributes](reference/template-syntax.md#spread-attributes).
- **Whitespace under `compressHTML: "jsx"`** (the default, as in Astro v7): Orbit also applies the JSX rules to text directly inside a component's children or a `<Fragment>`, which Astro leaves as written. See [Whitespace](reference/template-syntax.md#whitespace).
- **An empty attribute value is written `=""`** (`alt=""`), where Astro writes the bare attribute (`alt`). The DOM is the same.
- **Booleans on `data-*` and custom-element attributes** are a bare attribute or nothing, where Astro writes `"true"`/`"false"`. `aria-*` is the same in both. See [Attribute values](reference/template-syntax.md#attribute-values-on-html-elements).

## 11. Common pitfalls

- **`orbit.If` and `orbit.Cond` evaluate both arguments.** They're Go calls, not a ternary: `orbit.If(reader != nil, reader.Name)` panics when `reader` is nil. Compute the value in the frontmatter with a `nil` check, or use `{if reader != nil}…{/if}`. See [Conditional values](reference/template-syntax.md#conditional-values-orbitif-and-orbitcond).
- **A frontmatter `var` runs on every render.** Only `import`, `type`, `const` and named `func` declarations are hoisted. A lookup table such as `icons := map[string]string{…}` is rebuilt each time the component renders: make it a `const`, a hoisted `func` with a `switch`, or a package-level `var` in a Go package. See [Values computed once](components.md#values-computed-once).
- **`ctx` isn't a `context.Context`.** It's the request's `*orbit.Ctx`, roughly Astro's `Astro`. Pass `ctx.Request.Context()` to database and API calls, or the `rc` an `orbit.All` load gets. Don't name a load's parameter `ctx`, or it hides Orbit's `ctx`.
- **A `nil` slice is `null` in scripts.** A Go slice field in `orbit.JSON` data is typed `T[] | null`, because a nil slice encodes as `null`: write `book.tags?.length ?? 0`, or tag the field `omitempty`/`omitzero`. See [Type mapping](reference/client-modules.md#type-mapping).
- **Every element must be closed.** Orbit doesn't auto-close `<p>` or `<li>`: a missing closing tag is a parse error at its position.
- **A component can't be held in a variable** (`const C = cond ? A : B`): use `{switch}` or `{if}` with both tags.

## See also

- [Coming from Astro](coming-from-astro.md): every Astro concept and its Orbit equivalent
- [Components](components.md): frontmatter, props, slots and templates in depth
- [Loading data](data-loading.md): `orbit.All`, loaders in `locals`, `server:concurrent`
- [Middleware and locals](middleware.md): `net/http` middleware and typed `Locals`
- [Rendering](rendering.md): status codes, redirects and the error handler
- [Testing](testing.md): component and HTTP tests with `orbittest`
