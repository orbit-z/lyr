# Runtime API

This page lists the API of the `orbit` package (`github.com/bertrandkhe/orbit`), the runtime your pages, components and server use, plus the `orbittest` helpers.

```go
import "github.com/bertrandkhe/orbit"
```

Inside `.orbit` files you don't need this import: `orbit` is always available, and so are `ctx`, `props`, `slots` and `locals` in the frontmatter body and the template, plus `params` in pages. The runtime package depends only on the Go standard library.

Many of the names below are used by the code Orbit generates and rarely appear in your own code. Each section says which ones you're likely to use.

## Request context: `Ctx`

Every page and component gets the request context as `ctx`.

```go
type Ctx struct {
    Request *http.Request // the request
    URL     *url.URL      // the request URL
    Site    *url.URL      // the "site" URL from orbit.json
    // unexported fields
}
```

`Site` is `nil` when `orbit.json` doesn't set `site`, as `Astro.site` is `undefined` in Astro.

In a component rendered with `server:defer`, `Request` and `URL` are the island request (`/_orbit/island/…`), not the page's, and `Param` returns `""` for every name. The page's URL is in the `Referer` header: `page, err := url.Parse(ctx.Request.Referer())`. See [Lazy loading](../lazy-loading.md).

A `Ctx` is pooled and reused across requests: don't keep it, or anything it holds, after the request ends (for example in a goroutine that outlives the render).

```go
func (c *Ctx) Param(name string) string
```
Returns the route parameter `name`, by the name in brackets (`"slug"` for `[slug]`, `"path"` for `[...path]`), percent-decoded; a rest parameter's segments are joined with `/`. Returns `""` when the current route has no parameter by that name, like `http.Request.PathValue`. Components, layouts and other shared code use it; a page usually reads the same values from its typed `params` (see [Route params](../routing.md#route-params-params)). It doesn't allocate.

There's no map of route params: the router stores the matched values in the pooled `Ctx`, and each page gets them as a generated struct:

```go
type Params struct { // generated per page; here for src/pages/blog/[slug].orbit
	Slug string
}
```

A page sees it as `params`, of type `Params`. A page without route parameters gets `type Params struct{}`. Field names follow the [prop name rules](template-syntax.md#name-matching): `[post-id]` is `PostID`, `[id]` is `ID`.

```go
func (c *Ctx) SetStatus(code int)
```
Sets the response status code (default 200).

```go
func (c *Ctx) Header() http.Header
```
Returns the response headers, to set or change before the page is sent. Pages and island fragments get `Content-Type: text/html; charset=utf-8` unless you set a `Content-Type` here. In a deferred component, setting `Cache-Control` here replaces the default `private, no-store`.

```go
func (c *Ctx) Redirect(to string, code int) error
```
Returns a `*RedirectError` for `to` with status `code`. Return it from the frontmatter: `return ctx.Redirect("/login", 302)`.

## Errors and control flow

The frontmatter body is a function that returns an `error`. What you return decides the response:

```go
var ErrNotFound error
```
Return `orbit.ErrNotFound` to answer with a 404. In a page, Orbit renders your `404.orbit` page. In a `server:defer` component, the island request gets a 404 with an empty body, and the placeholder keeps its fallback.

```go
type RedirectError struct {
    Location string
    Code     int
}
func (e *RedirectError) Error() string
```
The error returned by `ctx.Redirect`. Orbit answers with status `Code` and a `Location` header. The island loader doesn't follow redirects, so in a deferred component a redirect leaves the fallback in place.

Any other non-nil error is a server error: a 500 page in production, the error overlay in `orbit dev`. Panics are recovered per request and handled the same way.

## Values

```go
type HTML string
```
Trusted markup, written without escaping. `set:html` only accepts this type, so a plain `string` variable is a compile error there. Convert only content you trust: `orbit.HTML(post.Body)`.

```go
type URL string
```
A trusted URL. In URL attributes (`href`, `src`, …), Orbit replaces `javascript:` and `vbscript:` URLs with `about:invalid#orbit`; a value of type `URL` skips that check. It's still HTML-escaped.

```go
type Attrs map[string]any
```
Attributes to spread onto an HTML element with `{...attrs}`. Each value follows the [attribute rules](#attribute-values); each name must be a valid attribute name.

## Locals

Typed request-scoped data. You declare `type Locals struct` in the package `src/locals`; pages and components see it as `locals`, of type `*locals.Locals`. See [Middleware and locals](../middleware.md).

```go
func EnsureLocals[L any](r *http.Request) (*http.Request, *L)
```
The call to use in your `net/http` middleware: `r, l := orbit.EnsureLocals[locals.Locals](r)`. If `r` already carries an `*L`, it returns `r` unchanged and that pointer. Otherwise it attaches a new zero `L` and returns the new request with it. Every middleware in the chain can make the same call, and they all share one value. Pass the returned `r` on to `next`.

```go
func WithLocals[L any](r *http.Request, l *L) *http.Request
```
Returns a copy of `r` carrying `l`. Use it to attach a value you built elsewhere; otherwise prefer `EnsureLocals`.

```go
func LocalsFromRequest[L any](r *http.Request) *L
```
Returns the locals attached to `r`, or `nil` if no middleware attached any, rather than a detached zero value whose changes would be lost. Use it in your own handlers.

```go
func LocalsOf[L any](c *Ctx) *L
```
Returns the request's locals, never `nil`: it allocates a zero `L` the first time it's called for a request without locals, and every component in the request shares that value. Generated code calls this to define `locals`.

```go
type NoLocals struct{}
```
The type of `locals` when your project has no `src/locals` package declaring `Locals`.

Island requests pass through your middleware like any other request, so deferred components see the locals your middleware sets for that request. Nothing carries over from the page request.

## Writer

A `Writer` writes HTML straight into a pooled `bytes.Buffer`. Generated code calls its methods; you use it when you write an `orbit.Renderer` in Go.

The writer keeps a **sticky error**: the first error from a write (an invalid spread attribute name, a `set:json` encoding error, a failing renderer, a depth error) is kept, later writes do nothing, and `Err` returns it.

```go
func NewWriter(buf *bytes.Buffer) *Writer
```
Creates a writer that writes into `buf`. To render components from your own handlers, use [`RenderComponent`](#rendering-from-your-handlers); in tests, use [`orbittest`](#orbittest).

```go
func (w *Writer) Raw(s string)
```
Writes `s` unescaped. Generated code uses it for the precomputed static HTML.

```go
func (w *Writer) Expr(v any)
```
Writes any value with the [escaping rules](#escaping-rules) below. This is the generic path, used when an expression's static type has no typed write.

```go
func (w *Writer) JSON(v any)
```
Writes `v` encoded with `encoding/json`, escaped for a `<script>`'s content: `<`, `>` and `&` become `\u003c`, `\u003e` and `\u0026`, and U+2028/U+2029 become `\u2028`/`\u2029`, so the output can't contain `</script>` or `<!--`. Generated for `set:json={expr}`, which is only valid on `<script type="application/json">`. An encoding error is a render error, reported at the `set:json` position. See [Passing data from the server to scripts](../styles-and-scripts.md#passing-data-from-the-server-to-scripts).

```go
func (w *Writer) Err() error
```
Returns the sticky error, or `nil`.

### Typed writes

When Orbit compiles a template, it type-checks each `{…}` expression and calls the write for its type, which avoids boxing the value into `any`. The output is byte-for-byte what `Expr` would write.

```go
func (w *Writer) String(s string)         // HTML-escaped
func (w *Writer) Int(n int64)
func (w *Writer) Uint(n uint64)
func (w *Writer) Float(f float64)
func (w *Writer) Float32(f float32)       // formatted as a float32: float32(0.1) prints 0.1
func (w *Writer) Bool(b bool)
func (w *Writer) HTML(h HTML)             // raw; also used by set:html
func (w *Writer) Bytes(b []byte)          // HTML-escaped, as a string
func (w *Writer) Error(e error)           // writes e.Error(), escaped (it doesn't fail the render)
func (w *Writer) Stringer(s fmt.Stringer) // writes s.String(), escaped
func (w *Writer) Render(r Renderer)       // calls r
```

Which write an expression gets depends on its static type:

| Static type | Write |
|---|---|
| untyped `nil` | nothing |
| constants | by their default type (may be merged into the static HTML) |
| `orbit.HTML` | `HTML` |
| `string` | `String` |
| `orbit.URL` | `String` |
| `[]byte` | `Bytes` |
| ints, runes | `Int` |
| uints, `byte` | `Uint` |
| `float64` | `Float` |
| `float32` | `Float32` |
| `bool` | `Bool` |
| `orbit.Renderer`, `func(*orbit.Writer) error` | `Render` |
| `error`, or any type that implements it | `Error` |
| a concrete type with a `String() string` method (`time.Time`, `type Status int` with `String`) | `Stringer` |
| a named type with none of these methods, based on `string`, an int, a uint, `bool`, `float64` or `float32` (`type Slug string`) | the matching write, after a conversion |
| `any`, `fmt.Stringer` and other interfaces, slices, pointers such as `*string`, a `T` whose `String` method is only on `*T`, and anything else | `Expr` |

### Escaping rules

`Expr` checks the value's exact dynamic type, in this order. A named type like `type Slug string` is not a `string` here: it falls through to `fmt.Sprint`, which prints the same text.

| Value | Output |
|---|---|
| `nil` | nothing |
| `orbit.HTML` | raw |
| `string`, `orbit.URL` | HTML-escaped |
| `[]byte` | HTML-escaped, as a string |
| ints, floats, bools | formatted with `strconv` (`float32` as a float32) |
| `Renderer`, `func(*Writer) error` | called |
| `error` | `Error()`, escaped |
| `fmt.Stringer` | `String()`, escaped |
| a slice of these | each element in turn |
| anything else | `fmt.Sprint`, escaped |

### Attributes

```go
func (w *Writer) Attr(name string, v any)       // generic path
func (w *Writer) AttrString(name, s string)
func (w *Writer) AttrURL(name, s string)        // for URL attributes: scheme-filtered
func (w *Writer) AttrBool(name string, b bool)
func (w *Writer) Spread(a Attrs)                // {...attrs}
```

#### Attribute values

- `true` writes the bare attribute (`disabled`); `false` and `nil` omit it.
- Anything else is attribute-escaped and quoted.
- **URL attributes** (`action`, `background`, `cite`, `codebase`, `data`, `formaction`, `href`, `icon`, `longdesc`, `manifest`, `poster`, `profile`, `src`, `usemap`, `xmlns` and `xlink:href`: the list Go's `html/template` uses, plus `xlink:href`): a value whose scheme is `javascript:` or `vbscript:` (ignoring case, and after trimming whitespace and control characters) is replaced with `about:invalid#orbit`, unless it has type `orbit.URL`. This applies to `Attr`, `AttrURL` and `Spread`.
- **`Spread`** checks each name against `[A-Za-z_:][-A-Za-z0-9_:.]*`. An invalid name (with spaces, quotes, `>`, `/` or `=`) is a render error.

### Generated-code hooks

```go
func (w *Writer) HeadEnd()
```
Marks where the page's `</head>` is, so the page's `<link>` and `<script>` tags (and the inline loader, on pages that need it) can be inserted there without scanning the output. Generated where a template contains `</head>`.

```go
func (w *Writer) Enter(component string) error
func (w *Writer) Leave()
```
Track the component stack around each render. `Enter` returns an error naming the component stack when the depth passes 1000, which stops runaway recursion before it overflows the stack. This guard is always on.

## Renderers and slots

```go
type Renderer func(w *Writer) error
```
A piece of HTML that writes itself. A `Renderer` (or a plain `func(*orbit.Writer) error`) in a `{…}` expression is called in place. You can write one in Go:

```go file=internal/views/stars.go
package views

import "github.com/bertrandkhe/orbit"

// Stars renders a rating as n star characters.
func Stars(n int) orbit.Renderer {
	return func(w *orbit.Writer) error {
		for range n {
			w.Raw("★")
		}
		return w.Err()
	}
}
```

and use it in a template as `{views.Stars(4)}`.

```go
type NamedSlot struct {
    Name   string
    Render Renderer
}

type Slots struct {
    Default Renderer
    Named   []NamedSlot
}
```
The children passed to a component, available as `slots`. It's passed by value; the zero `Slots{}` means "no children" and costs no allocation.

```go
func (s Slots) Get(name string) Renderer
```
Returns the slot called `name` (`"default"` returns `Default`), or `nil`.

```go
func (s Slots) Has(name string) bool
```
Reports whether the slot was passed, e.g. `{if slots.Has("footer")}`.

## Generated component packages

Each `.orbit` file has a generated declaration package under `orbit_gen/decl/`, which is how Go code (handlers, tests) refers to a component. For `src/components/Card.orbit`:

```go
import card "example.com/blog/orbit_gen/decl/components/card"

type Props struct { /* your fields */ }
func Render(ctx *orbit.Ctx, w *orbit.Writer, props Props, slots orbit.Slots) error
```

`Render` only works once the component is linked, by importing `example.com/blog/orbit_gen/app` (your server already does, through `app.New()`). Otherwise it panics with `orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app`.

Pages have a declaration package too, with an empty generated `Props`: pages never take props.

The generated `orbit_gen/app` package builds your site:

```go
func New(opts ...orbit.Option) *orbit.Site
```

## Site

```go
type Site struct { /* routes, router, islands, island cipher, asset manifest, options */ }
```
Your whole Orbit site, created by `app.New()`.

```go
func (s *Site) Middleware(next http.Handler) http.Handler
```
The integration point with your server. For a request that Orbit owns, Orbit answers it, in this order:

1. **Island requests**, `/_orbit/island/<buildID>/<id>`, for `GET`, `HEAD` and `POST`; any other method gets a 405 with `Allow: GET, HEAD, POST`. See [Lazy loading](../lazy-loading.md).
2. **Static files**, `/_orbit/*` assets and `public/` files, for `GET` and `HEAD` only; other methods fall through to `next`. Files are served with `http.ServeContent` (`Range`, conditional requests). `/_orbit/*` files get `Cache-Control: public, max-age=31536000, immutable`, `public/` files `Cache-Control: public, max-age=0`, and both get `no-cache` in `orbit dev`. A directory is never served (not even its `index.html`), a file path is never redirected, and `/_orbit/island/` is never a file. In production, the static directory is indexed once at startup, so files added later are served after a restart; `orbit dev` looks them up per request.
3. **Pages**, for every method. A path with a trailing slash whose slash-less form is a page gets a 308 redirect to it, keeping the query string (`/blog/hello/` → `/blog/hello`).

Anything else goes to `next`, untouched: Orbit writes nothing and sets no header.

A page renders into a pooled buffer, gets its `<link>` and `<script>` tags inserted at its `</head>`, and is written once, with a `Content-Length`. Responses are never streamed.

```go
func (s *Site) Handler() http.Handler
```
Same as `s.Middleware(s.NotFound())`: Orbit's pages, with `404.orbit` for everything else.

```go
func (s *Site) NotFound() http.Handler
```
Renders `404.orbit` with a 404 status (a status the page sets with `ctx.SetStatus` wins), or answers an empty 404 if there is no `404.orbit`. Use it as the catch-all of your own mux when Orbit comes first.

### Options

```go
type Option func(*siteOptions)
```
Options for `app.New()`.

```go
func WithSite(rawURL string) Option
```
Sets the absolute site URL exposed as `ctx.Site`. The generated `app.New()` passes `orbit.json`'s `site` first, so your own `WithSite` wins. A value that isn't an absolute URL, with a scheme and a host, panics.

```go
func WithStaticDir(dir string) Option
```
Sets the directory of built static files, replacing the default (`client/` next to the server binary). `$ORBIT_STATIC` still wins when it's set, so `orbit dev`, which sets it, keeps serving its own assets. The full order is `$ORBIT_STATIC`, then `WithStaticDir`, then the embedded files of an `--embed` build, then `client/` next to the binary.

```go
func WithStaticFS(fsys fs.FS) Option
```
Serves the static files from a file system laid out like `dist/client` instead. `orbit build --embed` uses it automatically with the embedded files. `$ORBIT_STATIC` and `WithStaticDir` win over it. Files without a modification time, such as embedded ones, get an `ETag` from their content.

```go
func WithKey(key []byte) Option
```
Sets the 32-byte island key that encrypts `server:defer` props. Without it, the site resolves its key once, at startup, in this order:

1. `WithKey`, if given;
2. `$ORBIT_KEY`: the standard base64 encoding of exactly 32 bytes, as printed by [`orbit create-key`](cli.md#orbit-create-key). Any other value panics at startup with a message saying what's wrong;
3. the key `orbit build` embeds in the binary, new for each build;
4. otherwise (a plain `go build` or `go run`), a random key for the process, with a one-line warning on stderr.

All instances of one build share its embedded key. For rolling deploys, or several builds behind one load balancer, set the same `ORBIT_KEY` everywhere. See [Deploy](../deploy.md).

### Generated wiring

```go
func NewSite(routes []Route, islands []Island, opts ...Option) *Site // panics on invalid routes

type Route struct {
    Pattern string                    // "/blog/[slug]"
    File    string                    // the .orbit file
    Render  func(*Ctx, *Writer) error // calls the page's Render with its empty Props
    Assets  []Asset                   // CSS and JS for the page's <head>, plus lazy entries
    Islands bool                      // the page can contain server:defer placeholders
}

type Asset struct {
    Kind string // "css", "js" or "lazy"
    URL  string
    ID   string // for "lazy": the component ID whose <orbit-lazy> markers load it
}
```
Used by the generated `app.New()`. You don't build routes by hand. A route with `Islands` set, or with `lazy` assets, also gets the inline loader in its `<head>`.

```go
func ErrorAt(pos string, err error) error
```
Generated around the result of every frontmatter `return`: `return x` compiles to `return orbit.ErrorAt("src/pages/blog/[slug].orbit:14:5", x)`, the position of the `return`. A non-nil error comes back as an error whose text is `pos + ": " + err.Error()` and which unwraps to `err`, so `errors.Is` and `errors.As` still match it; that's the position in the [500 log line](../rendering.md#status-codes-and-errors). `nil`, `ErrNotFound`, a `*RedirectError` and an error that already carries a position are returned unchanged, so a redirect or 404 keeps working and an error from a component isn't prefixed twice. Only the failing case allocates.

## Deferred islands

`server:defer` renders a component in its own request, after the page is sent. See [Lazy loading](../lazy-loading.md). These names are used by generated code; you don't call them yourself.

```go
type Island struct { /* component ID, file, decode-and-render function */ }

func NewIsland[P any](id, file string, render func(*Ctx, *Writer, P, Slots) error) Island
```
One entry of the island table that `orbit_gen/app` passes to `NewSite`. Only components used with `server:defer` somewhere in your project are registered, so no other component can be rendered by URL. On an island request, the props are decoded into `P` with `encoding/json` (unknown fields are ignored, missing ones stay zero) and `render` is called with no slots.

```go
func Defer(c *Ctx, w *Writer, id string, props any, fallback Renderer) error
```
Generated at each `server:defer` call site. Encodes `props` as JSON, encrypts them with the site's island key, and writes the placeholder holding the fallback:

```html
<orbit-island src="/_orbit/island/<buildID>/<id>" props="<token>">…fallback…</orbit-island>
```

The same props, component and build always give the same token, so the same URL. A JSON encoding error is a render error at the call site's `.orbit` position.

## Rendering from your handlers

```go
func RenderComponent[P any](w http.ResponseWriter, r *http.Request,
    render func(*Ctx, *Writer, P, Slots) error, props P) error
```
Renders one component as an HTML fragment into `w`, for example an htmx partial. No `<link>` or `<script>` tags are inserted. Locals attached to `r` (with `EnsureLocals` or `WithLocals`) are visible to the component.

```go
func like(w http.ResponseWriter, r *http.Request) {
    btn := likebutton.Props{Slug: r.PathValue("slug"), Likes: countLikes(r)}
    if err := orbit.RenderComponent(w, r, likebutton.Render, btn); err != nil {
        http.Error(w, "render failed", http.StatusInternalServerError)
    }
}
```

The fragment is buffered like a page: on success it's written once, with `Content-Type: text/html; charset=utf-8` (unless the component set one) and a `Content-Length`. If rendering fails, nothing has been written yet, and you can send your own error response.

A fragment may contain `server:defer` placeholders; they're sealed with the same key as the site's (without `WithKey`). They load only if the page the fragment lands in already has the islands loader.

Rendering a full page (with its assets) from your own handler is not yet supported.

## Serving

```go
func Addr() string
```
The address to listen on: `$ORBIT_ADDR`, else `":" + $PORT`, else `":4321"`. `orbit dev` sets `ORBIT_ADDR`, so use `Addr()` if you start your own `http.Server`.

```go
func ListenAndServe(h http.Handler) error
```
Serves `h` on `Addr()` and shuts down gracefully on `SIGINT` or `SIGTERM`, giving in-flight requests up to 5 seconds to finish. It reads no command-line flags, so it never clashes with your own.

It returns `nil` after a graceful shutdown, and otherwise the error that stopped the server (for example, the address is already in use), or `orbit: shutdown: context deadline exceeded` when requests were still running after the grace period. A second signal during the shutdown stops the process at once. So `if err := orbit.ListenAndServe(h); err != nil { log.Fatal(err) }`, as in the scaffolded `main.go`, exits cleanly on Ctrl-C and logs only real failures. Don't pass the result straight to `log.Fatal`, which exits 1 even for a nil error.

| Setting | Value |
|---|---|
| `ReadHeaderTimeout` | 10 s |
| `IdleTimeout` | 120 s |
| `ReadTimeout`, `WriteTimeout` | none, so slow uploads and long-running handlers aren't cut off |
| Shutdown grace period | 5 s, then remaining connections are closed |

## orbittest

The `github.com/bertrandkhe/orbit/orbittest` package tests components and pages with `go test`. See [Testing](../testing.md) for examples.

```go
func Render[P any](t testing.TB, render func(*orbit.Ctx, *orbit.Writer, P, orbit.Slots) error,
    props P, opts ...Option) string
```
Renders a component or page (pass its generated `Render`) and returns the HTML, with the same writer and escaping as a real request. Calls `t.Fatal` if rendering fails. It calls `render` directly, with a `GET /` request unless you pass `WithRequest`: your middleware doesn't run, and no head injection happens (no `<link>` or `<script>` tags, no loader). `server:defer` components appear as their placeholders, sealed with a fixed test island key and the build ID `0000000000000000`, so the output is the same on every run.

```go
func Get(t testing.TB, h http.Handler, path string, opts ...Option) *httptest.ResponseRecorder
```
Sends a GET for `path` to `h` (for example `app.New().Handler()`) and returns the recorded response, as production would answer it: routing, middleware in `h`, head injection. Redirects aren't followed. Island placeholders use the key and build ID of the site behind `h`; under `go test`, `app.New()` uses the same fixed test key and build ID as `Render` unless `ORBIT_KEY` or `WithKey` is set. Only `WithLocals` and `WithRequest` apply to `Get`.

```go
func WithSlot(name, html string) Option
```
Fills the slot `name` (`"default"` or a named slot) with raw HTML; a later `WithSlot` for the same name replaces the content. `Render` only.

```go
func WithLocals[L any](l *L) Option
```
Sets the request's locals. With `Get`, `l` is attached to the request, so `EnsureLocals` in your middleware returns it.

```go
func WithRequest(r *http.Request) Option
```
Sets `ctx.Request`: method, URL and query, headers, cookies. Without it, `Render` uses `httptest.NewRequest("GET", "/", nil)`. With `Get`, a copy of `r` is sent, with its method set to `GET` and its URL to `path`.

```go
func WithParam(name, value string) Option
```
Sets the route parameter `name` to `value`, as the router would: `ctx.Param(name)` returns it, and a page's `params` field for it holds it. Repeat it for each parameter (`WithParam("cat", "books"), WithParam("item", "dune")`); a later call for the same name replaces the value, like `http.Request.SetPathValue`. `Render` only: with `Get`, the router sets params from the path.

An option passed to the function it doesn't apply to fails the test.

```go
func Snapshot(t testing.TB, name, html string)
```
Compares `html` with `testdata/<name>.golden` and fails the test, showing the difference, if they differ or the file is missing (with a hint to run with `-update`). Running the test with `-update` writes the file instead. Hashed asset names (`/_orbit/<name>-<HASH>.<ext>` → `/_orbit/<name>-*.<ext>`) and island build IDs (`/_orbit/island/<build ID>/` → `/_orbit/island/*/`) are normalized before comparing and writing. `name` may contain `/` (`"pages/post"`), but no `..`. `orbittest` defines the `-update` flag itself, so don't define another one in a package that imports it.

## See also

- [Components](../components.md): `props`, `ctx`, `slots`, escaping and `set:html`
- [Server](../server.md): `Middleware`, `Handler`, `NotFound`, `RenderComponent`, `ListenAndServe`
- [Middleware and locals](../middleware.md): `EnsureLocals` and typed `Locals`
- [Lazy loading](../lazy-loading.md): `server:defer`, `Defer` and the island key
- [Testing](../testing.md): `orbittest`
- [Template syntax](template-syntax.md)
