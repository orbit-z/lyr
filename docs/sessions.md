# Sessions

A session keeps data for one visitor on the server, such as who is signed in, a cart or a message to show once, behind a single random cookie. It's Astro's sessions, typed: you declare the data as a Go struct, read and change it as a struct, and Lyr saves it at the end of the request.

```lyr
---
d, err := session.Data()
if err != nil {
	return err
}
d.Cart = append(d.Cart, item) // saved automatically
---
```

`Data()` returns a pointer to the request's own copy of the data: change it in place, and don't keep it after the request ends (don't hand it to a goroutine that outlives the request), because Lyr reuses it.

Sessions are off until you give Lyr a store in `cmd/server/main.go`. A request that never touches the session (a blog post, a static file) reads no cookie and calls no store.

## A shop with sign-in and a cart

The example has a cart that anonymous visitors can fill, sign-in and sign-out, and messages shown once ("flash" messages). It uses two small packages:

<details>
<summary><code>internal/users/users.go</code> and <code>internal/cart/cart.go</code>, the tiny packages used by the examples on this page</summary>

```go file=internal/users/users.go
// Package users is a tiny in-memory user store for the examples.
package users

import (
	"context"
	"errors"
)

// ErrBadCredentials is returned by Check for a wrong email or password.
var ErrBadCredentials = errors.New("users: wrong email or password")

type User struct {
	ID   int64
	Name string
}

type account struct {
	User
	email, password string // a real store keeps a password hash
}

var accounts = []account{
	{User: User{ID: 1, Name: "Ada"}, email: "ada@example.com", password: "correct horse"},
}

// Find returns the user with the given ID, or nil.
func Find(ctx context.Context, id int64) *User {
	for i := range accounts {
		if accounts[i].ID == id {
			return &accounts[i].User
		}
	}
	return nil
}

// Check returns the user with this email and password, or ErrBadCredentials.
func Check(ctx context.Context, email, password string) (*User, error) {
	for i := range accounts {
		if accounts[i].email == email && accounts[i].password == password {
			return &accounts[i].User, nil
		}
	}
	return nil, ErrBadCredentials
}
```

```go file=internal/cart/cart.go
// Package cart holds the shop's cart line type.
package cart

// Item is a line in the cart.
type Item struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}
```

</details>

### Declare the session's data

Declare `type SessionData struct` in the Go package `src/locals`, next to `Locals`:

```go file=src/locals/locals.go
// Package locals declares the request-scoped data that middleware hands to pages.
package locals

import (
	"github.com/orbit-z/lyr"

	"example.com/blog/internal/cart"
	"example.com/blog/internal/users"
)

// SessionData is what Lyr keeps on the server for each visitor.
type SessionData struct {
	UserID int64       `json:"userId,omitempty"`
	Cart   []cart.Item `json:"cart,omitempty"`
	Flash  string      `json:"flash,omitempty"` // a message shown once
}

type Locals struct {
	Session lyr.Session[SessionData] // for middleware and handlers; bound by site.Sessions
	User    *users.User              // set by the auth middleware
}
```

Lyr finds `SessionData` by name, as it finds `Locals`. It's stored as JSON, so the `json` tags name the stored keys. Use `omitempty` on every field: empty fields then take no room in the store, and a visitor whose data is all empty never gets a session (see [Saving](#saving-is-automatic)).

The `Session` field is only needed for sessions in middleware and your own handlers. Pages and components don't need it, and a project that uses sessions only in pages doesn't need `Locals` at all.

### Turn sessions on

`cmd/server/main.go` gives Lyr a store and puts `site.Sessions` in the middleware chain:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"

	"example.com/blog/lyr_gen/app"
)

func main() {
	store := cache.NewSessionStore(cache.SessionStoreOptions{MaxEntries: 100_000})
	site := app.New(lyr.WithSessions(store, lyr.SessionOptions{
		TTL: 30 * 24 * time.Hour, // a session lasts 30 days from its last change
	}))

	mux := http.NewServeMux()
	mux.Handle("POST /login", login(site))
	mux.Handle("POST /logout", logout(site))
	mux.Handle("/", site.Handler())

	// site.Sessions goes before the middleware and handlers that use the session.
	h := lyr.Sequence(site.TrailingSlash, site.Sessions, auth(site))(mux)
	if err := lyr.ListenAndServe(h); err != nil {
		log.Fatal(err)
	}
}
```

- **`lyr.WithSessions(store, options)`** turns sessions on. There's no default store and no `lyr.json` key: a store is Go code, like a cache provider.
- **`cache.NewSessionStore`** is the built-in store, in the server's memory. It suits one instance; see [Stores](#stores) for Redis and several instances.
- **`site.Sessions`** binds the `Session` field of `Locals` for everything inside it, and saves changes made there before the response goes out. Put it after `site.TrailingSlash` and before any middleware or handler that uses the session.

### Read and change it in middleware and handlers

The auth middleware reads the session on every request and fills `locals.User`:

```go file=cmd/server/auth.go
package main

import (
	"net/http"

	"github.com/orbit-z/lyr"

	"example.com/blog/internal/users"
	"example.com/blog/src/locals"
)

// auth sets locals.User for signed-in visitors.
func auth(site *lyr.Site) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := lyr.EnsureLocals[locals.Locals](r)
			d, err := l.Session.Data()
			if err != nil {
				site.ServeError(w, r, err)
				return
			}
			if d.UserID != 0 {
				l.User = users.Find(r.Context(), d.UserID)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// login signs a visitor in, keeping their cart.
func login(site *lyr.Site) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, l := lyr.EnsureLocals[locals.Locals](r)
		u, err := users.Check(r.Context(), r.FormValue("email"), r.FormValue("password"))
		if err != nil {
			d, err := l.Session.Data()
			if err != nil {
				site.ServeError(w, r, err)
				return
			}
			d.Flash = "Wrong email or password."
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		// A new session ID with the same data, against session fixation.
		if err := l.Session.Regenerate(); err != nil {
			site.ServeError(w, r, err)
			return
		}
		d, err := l.Session.Data()
		if err != nil {
			site.ServeError(w, r, err)
			return
		}
		d.UserID, d.Flash = u.ID, "Welcome back, "+u.Name+"!"
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// logout signs a visitor out: the session and its cart are deleted.
func logout(site *lyr.Site) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, l := lyr.EnsureLocals[locals.Locals](r)
		l.Session.Destroy()
		d, err := l.Session.Data() // empty after Destroy; a change starts a new session
		if err != nil {
			site.ServeError(w, r, err)
			return
		}
		d.Flash = "You're signed out."
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
```

There's no `Save` call. `site.Sessions` saves the changes just before the handler's redirect is written, and adds the `Set-Cookie` header when the visitor gets a new session ID.

### Read and change it in pages and components

Every page and component has an implicit `session`, as it has `locals`: a `*lyr.Session[locals.SessionData]`. The cart page adds an item on `POST`:

```lyr file=src/pages/cart.lyr
---
import (
	Layout "../layouts/Base.lyr"
	"example.com/blog/internal/cart"
)

d, err := session.Data()
if err != nil {
	return err
}
if ctx.Request.Method == "POST" {
	d.Cart = append(d.Cart, cart.Item{SKU: ctx.Request.FormValue("sku"), Qty: 1})
	d.Flash = "Added to your cart."
	return ctx.Redirect("/cart", 303)
}
---
<Layout title="Cart">
  <h1>Your cart</h1>
  <ul>
    {for _, it := range d.Cart}
      <li>{it.SKU} × {it.Qty}</li>
    {/for}
  </ul>
  <form method="post">
    <input type="hidden" name="sku" value="lyr-mug">
    <button>Add a mug</button>
  </form>
</Layout>
```

Components read the same session. The layout shows the cart's size and the flash message on every page:

```lyr file=src/layouts/Base.lyr
---
import (
	CartBadge "../components/CartBadge.lyr"
	Flash "../components/Flash.lyr"
)

type Props struct {
	Title string
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>{props.Title}</title>
  </head>
  <body>
    <header>
      <a href="/">Shop</a>
      {if locals.User != nil}<span>{locals.User.Name}</span>{/if}
      <CartBadge />
    </header>
    <main>
      <Flash />
      <slot />
    </main>
  </body>
</html>
```

```lyr file=src/components/CartBadge.lyr
---
d, err := session.Data()
if err != nil {
	return err
}
n := 0
for _, it := range d.Cart {
	n += it.Qty
}
---
<a href="/cart">Cart ({n})</a>
```

A flash message is just a field: whoever shows it clears it.

```lyr file=src/components/Flash.lyr
---
d, err := session.Data()
if err != nil {
	return err
}
msg := d.Flash
d.Flash = "" // shown once
---
{if msg != ""}<p class="flash" role="status">{msg}</p>{/if}
```

The sign-in form posts to the `login` handler:

```lyr file=src/pages/login.lyr
---
import Layout "../layouts/Base.lyr"
---
<Layout title="Sign in">
  <form method="post" action="/login">
    <input name="email" type="email" required>
    <input name="password" type="password" required>
    <button>Sign in</button>
  </form>
</Layout>
```

A visitor who adds a mug gets a session, a `lyr-session` cookie, and the message "Added to your cart." on the next page. Signing in keeps the cart and replaces the session ID; signing out deletes the session and starts a new one for the "You're signed out." message.

## Where the session is reached

| Where | How |
|---|---|
| Pages, layouts, components, slot content | the implicit `session` |
| `server:defer` islands | the implicit `session`, from the island request's own cookie |
| Middleware and your own handlers | `l.Session`, a `lyr.Session[SessionData]` field of your `Locals`, inside `site.Sessions` |
| Components rendered with `lyr.RenderComponent` | the implicit `session`, which is the request's `l.Session`: put `site.Sessions` in front of the handler |

One request has one session. A page, its components and the middleware before it share the same handle, and `Data()` returns the same `*SessionData` to all of them: a field the auth middleware reads is the one the page changes.

- **Only where it's used.** Lyr declares `session` only in the pages and components that mention it. A component that never says `session` costs nothing. A variable or import of your own named `session` shadows it, as for `locals`.
- **The field's name** is yours: the type `lyr.Session[SessionData]` is the marker, as for `lyr.RouteInfo` and `lyr.Cache`. Two such fields, an embedded or pointer one, or a type argument other than `SessionData`, is an error at the field.
- **`site.Sessions` is needed only outside pages.** Without it, pages and components still have their session, and save it themselves; `l.Session` in middleware returns `lyr.ErrSessionUnbound`. `site.Sessions` attaches `Locals` itself if no earlier middleware did, so its order relative to your `EnsureLocals` calls doesn't matter. It panics when it's set up if `Locals` has no `Session` field. Without `lyr.WithSessions`, the field's methods return `lyr.ErrSessionsNotEnabled`.
- **`server:concurrent` components and `lyr.All` loads** share the request's handle, and the data loads once. Changing the same fields from several of them at once is a data race, as for `locals`: change the session in the page, or guard it.

## The methods

```go
func (s *Session[D]) Data() (*D, error)      // the data, loaded once per request; change it in place
func (s *Session[D]) ID() string             // the session ID, or "" when there's no session
func (s *Session[D]) Regenerate() error      // a new ID for the same data
func (s *Session[D]) Destroy()               // deletes the session and its cookie
func (s *Session[D]) Load(id string) error   // switches to the stored session id
func (s *Session[D]) Save() error            // saves now; optional
```

- **`Data()`** reads the cookie and the store the first time it's called in a request, and returns the same pointer after that. A visitor without a session cookie, with an invalid one, or with an ID the store doesn't know gets a zero `SessionData`, without a store call when there's no valid cookie.
- **`ID()`** is Astro's `sessionID`. It loads like `Data()`.
- **`Regenerate()`** moves the data to a new ID: the new ID is sent in the cookie, and the old one is deleted from the store when the request saves. Call it whenever a visitor's privileges change, at sign-in above all (see [Security](#security)).
- **`Destroy()`** deletes the session from the store and expires the cookie when the request saves. `Data()` then returns a zero `SessionData`; changing it starts a new session with a new ID, as `logout` does.
- **`Load(id)`** switches the request to the stored session `id`, and sends its cookie: for example, to resume a session whose ID came another way than the cookie. Changes the request made before are dropped. An ID the store doesn't have returns `lyr.ErrSessionNotFound` and changes nothing.
- **`Save()`** saves now. You rarely need it; see [Saving](#saving-is-automatic).

The zero value of a `lyr.Session` isn't bound to anything: use the implicit `session` or the `Locals` field, not a `lyr.Session` you declare.

## Saving is automatic

Change the struct, and Lyr saves it. At the end of the request, Lyr encodes the data again and compares it with what it loaded. If nothing changed, it does nothing; otherwise it saves before the response is written:

- **Pages and islands** save after the render and before the response is written, whether the frontmatter returned normally, returned `ctx.Redirect`, or failed: a session change made before an error is kept, as in Astro.
- **A page that rewrites** (`ctx.Rewrite`, see [Rewrites](routing.md#rewrites)) saves its changes before the target runs, and the target gets the same session, with those changes: one session and at most one cookie per request, even when the target is served from the route cache. The target's own changes are saved at its end. A `site.Sessions` that runs again for the target (inside `site.Rewrites`) passes through.
- **Middleware and handlers** inside `site.Sessions` save when the response starts (the first `WriteHeader` or `Write`), so the `Set-Cookie` header goes out with it, and once more after the handler returns, for changes made after that. A change made after the response started that needs a new cookie (a first save, `Regenerate`) can't send it any more: it's dropped and logged (`lyr: session changed after the response was written`). Change the session before you write.
- **A new session** is created only when the data differs from a zero `SessionData`. An anonymous visitor whose page reads the session but changes nothing never gets a session or a cookie.

`Save()` saves at once and returns the store's error, so a handler can answer it itself, or save before it streams a response. The automatic save then has nothing left to do:

```go
d.UserID = u.ID
if err := l.Session.Save(); err != nil {
	http.Error(w, "Sign-in is unavailable, try again.", http.StatusServiceUnavailable)
	return
}
```

## Requests at the same time

A visitor can send two requests at once: two tabs, or a page and its islands. Each request loads the session, changes some fields and saves. Lyr saves **only the fields the request changed**, over what's in the store at that moment:

- Tab A adds a mug to `Cart`, while tab B clears `Flash`. Both changes are kept: A writes `Cart`, B writes `Flash`.
- Tab A and tab B both change `Cart`. The last one to save wins for `Cart`, since a field is replaced as a whole: a slice, a map or a nested struct is one field.

So keep fields that change independently in separate fields. Fields of the stored object that your struct doesn't have (written by another version of your code) are kept too. Saving isn't atomic: two saves that land in the same instant can still lose a change to a field.

## Expiry

`SessionOptions.TTL` is how long the store keeps a session after its **last save**. Reading doesn't extend it: a visitor who only reads is signed out `TTL` after their last change, and any change starts the count again. `0` means no expiry. Lyr passes the TTL to the store with every save, so the store expires sessions itself.

There's no expiry per field, unlike Astro's `set(key, value, { ttl })`. Keep an expiry time in a field instead:

```go
type SessionData struct {
	ResetCode    string    `json:"resetCode,omitempty"`
	ResetExpires time.Time `json:"resetExpires,omitzero"`
}
```

The cookie itself lasts until the browser closes, unless you set `SessionCookie.MaxAge` (see [Options](#options)).

## Errors

Every method that can reach the store returns an error. In a frontmatter, `return err` answers it as any other error (the 500 or your [error handler](rendering.md#answering-errors-yourself-witherrorhandler)); in middleware and handlers, `site.ServeError(w, r, err)`.

| Error | When |
|---|---|
| `lyr: session store: …` | the store failed; it wraps the store's error, for `errors.Is` |
| `*lyr.SessionDecodeError` | the stored data doesn't decode into `SessionData` (see [Changing `SessionData`](#changing-sessiondata)) |
| `lyr.ErrSessionNotFound` | `Load` with an ID the store doesn't have |
| `lyr.ErrSessionsNotEnabled` | the site has no `lyr.WithSessions`; logged once per process the first time |
| `lyr.ErrSessionUnbound` | a `Locals` field used without `site.Sessions` in front, or `lyr.RenderComponent` without it |

A failed save on a page becomes the request's error: the built-in 500 (or your error handler), logged as `lyr: saving session: …`. A failed automatic save in a handler can't change a response that's already going out: it's logged, and the cookie isn't sent. Call `Save()` first when you want to answer it.

Using `session` in a project whose `src/locals` declares no `SessionData` fails `lyr build` at that line of the `.lyr` file:

```text
session needs type SessionData in src/locals (see Sessions)
```

## Changing `SessionData`

Stored sessions outlive a deploy, so a new version of your code reads data the old one wrote. Since it's JSON:

- **An added field** decodes as its zero value.
- **A removed field** is ignored, and stays in the store until the session expires.
- **A field whose type changed** in a way JSON can't convert (a `string` that's now an `int`) makes `Data()` return a `*lyr.SessionDecodeError`. The stored data is left as it is.

To change a field's type, give it a new name and a new `json` tag, so old sessions decode with the new field empty. If old sessions can't be read anyway, start them over in your middleware:

```go
d, err := l.Session.Data()
var de *lyr.SessionDecodeError
if errors.As(err, &de) {
	l.Session.Destroy() // an old shape: sign the visitor out
	d, err = l.Session.Data()
}
```

## Options

```go
type SessionOptions struct {
	TTL    time.Duration // the session's lifetime from its last save; 0: no expiry
	Cookie SessionCookie
}

type SessionCookie struct {
	Name     string        // default "lyr-session"
	Domain   string
	Path     string        // default "/"
	MaxAge   time.Duration // default 0: the cookie lasts until the browser closes
	SameSite http.SameSite // default http.SameSiteLaxMode
	Insecure bool          // drop Secure in production
}
```

The cookie is always `HttpOnly`, and `Secure` except under `lyr dev` or with `Insecure`. A name may use letters, digits, `-` and `_`. `SameSite: http.SameSiteNoneMode` with `Insecure` panics in `lyr.WithSessions`, since browsers drop such cookies, and so does a `MaxAge` between 0 and one second (`Max-Age` is whole seconds, and `0` deletes the cookie).

Lyr sends the cookie when a session is created, after `Regenerate` and `Load`, and, while `MaxAge` is set, with every save, to push the expiry back. `Destroy` sends an expired cookie. A response carries at most one `Set-Cookie` for the session.

## Stores

A store keeps each session's bytes under its ID:

```go
type SessionStore interface {
	// Get returns the stored bytes, or nil, nil when id is unknown or expired.
	// Lyr never modifies the returned slice.
	Get(ctx context.Context, id string) ([]byte, error)
	// Set stores data under id for ttl (0: no expiry). Lyr never modifies
	// data afterwards, so the store may keep it.
	Set(ctx context.Context, id string, data []byte, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
}
```

Loads use the request's context. Saves use it without its cancellation, so a visitor who closes the tab mid-request doesn't lose a sign-in.

### The built-in store: `cache.NewSessionStore`

```go
store := cache.NewSessionStore(cache.SessionStoreOptions{
	MaxEntries: 100_000,   // default 10 000
	MaxBytes:   256 << 20, // optional
})
```

It keeps sessions in the server's memory, on the same engine as [`cache.NewMemory`](caching.md#the-memory-provider-cachenewmemory): split into shards, bounded by `MaxEntries` and optionally `MaxBytes`, expired lazily, evicted roughly least recently used first. **Every eviction signs a visitor out**, so size it for your visitors, not the 10 000 default. `Now` replaces the clock, for tests.

Its sessions live in one process: a restart in production signs everyone out, and several instances don't share them. Under `lyr dev` it also writes them to disk (see [In `lyr dev`](#in-lyr-dev)).

### Redis, and several instances

Behind a load balancer, every instance must see the same sessions, so use a shared store. A Redis store is a dozen lines with the client you already use (Lyr has no Redis dependency); here with `github.com/redis/go-redis/v9`:

```go
type redisSessions struct{ c *redis.Client }

func (s redisSessions) Get(ctx context.Context, id string) ([]byte, error) {
	b, err := s.c.Get(ctx, "sess:"+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return b, err
}

func (s redisSessions) Set(ctx context.Context, id string, b []byte, ttl time.Duration) error {
	return s.c.Set(ctx, "sess:"+id, b, ttl).Err() // ttl 0: no expiry
}

func (s redisSessions) Delete(ctx context.Context, id string) error {
	return s.c.Del(ctx, "sess:"+id).Err()
}
```

```go
site := app.New(lyr.WithSessions(redisSessions{c: rdb}, lyr.SessionOptions{TTL: 30 * 24 * time.Hour}))
```

Redis expires the keys itself, and the values are the JSON objects, readable with `redis-cli`. A request that loads costs one round trip; one that changes the session costs two (Lyr reads the stored object again to save only the changed fields, then writes it).

## In `lyr dev`

Sessions work in `lyr dev` as in production, unlike caching. Two differences:

- **`cache.NewSessionStore` writes through to `.lyr/sessions/`**, one file per session, so the restart after each Go or template change doesn't sign you out. Delete the directory to start over. Production never writes sessions to disk. Other stores, like Redis, survive restarts anyway.
- **The cookie has no `Secure` flag**, so it works on `http://localhost`.

## Sessions and route caching

Caching with [route caching](caching.md) is manual, as in Astro: Lyr doesn't track who reads the session. A page that calls `ctx.Cache().Set(…)` or matches a `routeRules` cache rule is stored and served to **everyone**, with or without a session, even if its render read the session. A cached page that shows "Signed in as Ann" shows it to the next visitor too.

You keep personal pages out:

- **Don't cache them**: no `Set` and no matching rule, or call `ctx.Cache().Disable()` in the page (also conditionally, `if d.UserID != 0`).
- **Disable in middleware**: give `Locals` a `Cache lyr.Cache` field, and in the auth middleware above call `l.Cache.Disable()` for signed-in visitors before `next`; their requests skip the cache, whatever the page sets (see [Keeping requests out of the cache](middleware.md#keeping-requests-out-of-the-cache)).
- **Cache a copy per session**: with a store provider (`cache.NewMemory`), set a [context value](caching.md#caching-by-context) in the middleware after `site.Sessions`, `l.Cache.Context("session", l.Session.ID())`, and give the personal page `VaryBy: []string{"session"}`. Each visitor gets their own copy, and visitors without a session share one. There's no built-in session key: you choose it, and a user ID (`"user"`) works too and shares a copy across a user's devices. `ID` loads the session, so this reads the store on every request. Mind the memory: a copy per active session (see [Caching by context](caching.md#caching-by-context)).
- **Move the personal bits into a `server:defer` island** (the cart badge, the user's name), so the page itself stays cacheable for everyone. Each visitor's browser loads the island with its own cookie (see [Sessions in islands](lazy-loading.md#sessions-in-islands)).

One rule is automatic: **a response that sends the session cookie is never stored** (a new session, `Regenerate`, `Destroy`, `Load`, or any save when the cookie has a `MaxAge`), and gets no CDN headers. A save that changes the data without sending the cookie doesn't stop storing. A background refresh of a stale copy never saves the session, and sees it as an anonymous visitor's, or as the triggering visitor's (read-only) when the copy varies by context. See [Sessions](caching.md#sessions).

## Security

- **The cookie** holds only the session ID: the data stays on the server. It's `HttpOnly` (scripts can't read it), `Secure` outside `lyr dev`, and `SameSite=Lax`. `Lax` still sends the cookie on links from other sites, so never change state on a `GET`; for forms, add [`http.CrossOriginProtection`](server.md#the-island-endpoint) to your chain.
- **IDs** are random UUIDs (122 bits from `crypto/rand`), not signed, as in Astro: they can't be guessed, and there's nothing to learn from them.
- **Session fixation**: Lyr never adopts an ID it didn't create. A cookie ID the store doesn't know is replaced by a new one at the next save, and `Load` refuses unknown IDs. Call `Regenerate` when privileges change (sign-in, a role change), so an ID someone saw before the change is worthless after it. `Destroy` on sign-out deletes the session; to sign out but keep the cart, set `d.UserID = 0` and call `Regenerate` instead.
- **What to keep there**: IDs and small values. Every save writes the whole object, so keep large data (a full user record, a file) in your database, and the key to it in the session. Don't keep secrets you never need back.
- **Islands**: never pass a session ID as a `server:defer` prop. The island request carries the visitor's own cookie, and its `session` reads it (see [Lazy loading](lazy-loading.md#sessions-in-islands)).

## Testing

`lyrtest.WithSession` sets the session's data for `lyrtest.Render`:

```go file=internal/views_test/flash_test.go
package views_test

import (
	"strings"
	"testing"

	"github.com/orbit-z/lyr/lyrtest"

	_ "example.com/blog/lyr_gen/app"
	"example.com/blog/lyr_gen/decl/components/flash"
	"example.com/blog/src/locals"
)

func TestFlash(t *testing.T) {
	html := lyrtest.Render(t, flash.Render, flash.Props{},
		lyrtest.WithSession(func(d *locals.SessionData) { d.Flash = "Saved." }))
	if !strings.Contains(html, `<p class="flash" role="status">Saved.</p>`) {
		t.Errorf("flash missing:\n%s", html)
	}
}
```

To test a whole flow, `lyrtest.Get` a site built with `lyr.WithSessions`, take the cookie from the first response, and send it with the next request. See [Testing sessions](testing.md#sessions-withsession-and-two-request-tests).

## Coming from Astro

| Astro | Lyr |
|---|---|
| `App.SessionData` in `src/env.d.ts` | `type SessionData struct` in `src/locals` |
| `await Astro.session.get("cart")` | `d, err := session.Data()`, then `d.Cart` |
| `Astro.session.set("cart", v)` | `d.Cart = v`, saved automatically |
| `delete("cart")` | `d.Cart = nil` (the zero value; with `omitempty` it isn't stored) |
| `has("cart")` | a zero check: `len(d.Cart) > 0` |
| `regenerate()`, `destroy()`, `load(id)` | `Regenerate()`, `Destroy()`, `Load(id)`; `Load` refuses unknown IDs |
| `sessionID` | `ID()` |
| `context.session` in middleware and endpoints | `l.Session`, inside `site.Sessions` |
| `session: { driver }` in `astro.config` | `app.New(lyr.WithSessions(store, lyr.SessionOptions{…}))` |
| `session.cookie` | `lyr.SessionCookie` |
| `session.ttl` in seconds, per value | `SessionOptions.TTL`, a `time.Duration`, per session |
| `set(key, value, { ttl })` | no equivalent: keep an expiry time in a field |
| devalue (`Map`, `Set`, `Date`, …) | JSON: use Go types that marshal |

What works differently:

- **One typed struct** instead of keys: a typo is a build error, and the editor completes `d.`.
- **Saving merges fields.** Astro writes back the whole map after a read, so two requests that read lose each other's changes; Lyr saves only the fields a request changed.
- **Route caching: same as Astro, you decide.** A cached page that reads the session is served to everyone; keep personal pages out with `Disable()` or a `server:defer` island. Lyr only refuses to store a response that sends the session cookie.
- **`load(id)`** with an unknown ID starts an empty session under that ID in Astro; Lyr returns `lyr.ErrSessionNotFound`, since adopting an ID from elsewhere is a fixation risk.

## See also

- [Middleware and locals](middleware.md#sessions-in-middleware): the `Session` field and `site.Sessions`
- [Caching](caching.md#sessions): keeping personal pages out of the route cache
- [Lazy loading](lazy-loading.md#sessions-in-islands): sessions in `server:defer` islands
- [Deploy](deploy.md#sessions-in-production): a shared store for several instances
- [Testing](testing.md#sessions-withsession-and-two-request-tests): `lyrtest.WithSession`
- [Runtime API](reference/runtime-api.md#sessions): every type and option
