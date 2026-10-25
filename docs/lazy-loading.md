# Lazy loading

Two directives on a component tag keep pages fast: `server:defer` renders a slow component in its own request after the page is sent, and `client:*` loads a component's scripts only when they're needed.

| Directive | What's delayed | Astro equivalent |
|---|---|---|
| `server:defer` | The component's **server rendering**. The page shows a fallback until the HTML arrives. | Server islands (`server:defer`) |
| `client:idle`, `client:visible`, `client:media` | The **`<script>` modules** of the component and every component it renders. The HTML is rendered with the page as usual. | `client:*` directives, which Astro has only for framework components |

Both work only on `.orbit` components, and they can't be used together on one tag (see [Combining the two](#combining-the-two)).

## `server:defer`: deferred server rendering

### Defer a slow component

Here, loading a post's comments takes 300 ms:

```go file=internal/posts/posts.go
// Package posts holds the blog's content.
package posts

import (
	"errors"
	"time"
)

// ErrNotFound is returned by Find for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

// Post is one blog post.
type Post struct {
	Slug  string
	Title string
	Body  string // trusted HTML
}

// Comment is a reader comment.
type Comment struct {
	Author string
	Text   string
}

var all = []Post{
	{Slug: "hello", Title: "Hello, Orbit", Body: "<p>First post.</p>"},
}

// Find returns the post with the given slug, or ErrNotFound.
func Find(slug string) (Post, error) {
	for _, p := range all {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Post{}, ErrNotFound
}

// Comments returns the comments of a post. It's slow, like a call to a remote API.
func Comments(slug string) ([]Comment, error) {
	time.Sleep(300 * time.Millisecond)
	if slug != "hello" {
		return nil, nil
	}
	return []Comment{{Author: "Ada", Text: "Nice!"}}, nil
}
```

The component that renders them is an ordinary component:

```orbit file=src/components/Comments.orbit
---
import "example.com/blog/internal/posts"

type Props struct {
    PostID string
}

comments, err := posts.Comments(props.PostID)
if err != nil {
    return err
}
---
<section class="comments">
  {if len(comments) == 0}
    <p>No comments yet.</p>
  {else}
    <ul>
      {for _, c := range comments}
        <li><strong>{c.Author}</strong>: {c.Text}</li>
      {/for}
    </ul>
  {/if}
</section>
```

The page adds `server:defer` to the tag and gives it a `fallback` slot:

```orbit file=src/pages/blog/[slug].orbit
---
import (
    "errors"

    "example.com/blog/internal/posts"
    Base "../../layouts/Base.orbit"
    Comments "../../components/Comments.orbit"
    ShareButtons "../../components/ShareButtons.orbit"
)

post, err := posts.Find(params.Slug)
if errors.Is(err, posts.ErrNotFound) {
    return orbit.ErrNotFound
}
if err != nil {
    return err
}
---
<Base title={post.Title}>
  <article>
    <h1>{post.Title}</h1>
    <div set:html={orbit.HTML(post.Body)}></div>
  </article>
  <ShareButtons title={post.Title} client:visible="200px" />
  <Comments server:defer postID={post.Slug}>
    <p slot="fallback">Loading comments…</p>
  </Comments>
</Base>
```

```orbit file=src/layouts/Base.orbit
---
type Props struct {
    Title string
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>{props.Title} · My blog</title>
  </head>
  <body>
    <main><slot /></main>
  </body>
</html>
```

(`ShareButtons` is lazy-loaded with `client:visible`; it's shown in [`client:*`: lazy scripts](#client-lazy-scripts).)

The page no longer waits for the comments. In its HTML, the `<Comments>` tag becomes a placeholder that holds the fallback:

```html
<orbit-island src="/_orbit/island/3f9a2c1d7e4b5a60/Comments-9b1e44a2" props="…"><p>Loading comments…</p></orbit-island>
```

Once the page is parsed, a small inline loader fetches the component's HTML from the `src` URL and replaces the whole placeholder with it, so no wrapper remains. The URL holds the build ID (`3f9a2c1d7e4b5a60`) and the component ID (`Comments-9b1e44a2`: the file name plus a short hash of its path), and the `props` attribute holds the props, encrypted (see [How props are protected](#how-props-are-protected)).

### Why it matters: responses are buffered

Orbit renders the whole page into a buffer and sends it in one write, never streamed. So the first byte of a page waits for its slowest part: without `server:defer`, the page above takes over 300 ms to start arriving.

With `server:defer`, the page renders the fallback and is sent at once, and the comments render in their own request, in parallel with anything else the browser does. Use it for the parts of a page that depend on slow work (a database query, a third-party API) and that the reader can wait for.

The cost on the page is small: one `encoding/json` encode and one encryption per deferred instance (about 1 µs for 1 KB of props), plus the loader, a few hundred bytes. Pages without deferred components pay nothing. See [Performance](performance.md).

### The fallback slot

The children of a deferred component are its fallback: elements or `<Fragment>`s with `slot="fallback"`, plus whitespace. The fallback is rendered by the page, so it can use the page's variables and gets the page's scoped styles. Without a fallback, the placeholder is empty until the HTML arrives.

No other slot can be passed to a deferred component. Any other child is a compile error at the child; if the `<p>` above had no `slot="fallback"`, Orbit would report:

```text
src/pages/blog/[slug].orbit:21:5: only slot="fallback" content is allowed inside a server:defer component
```

`server:defer` takes no value, and works only on a component tag (`<Self>` included). On an HTML element, `<slot>` or `<Fragment>`, it's a compile error: `server:defer only works on .orbit components`.

### Props must be JSON-serializable

A deferred component's props travel through the browser, encoded with `encoding/json`. So when Orbit generates code, it checks the component's whole `Props` type, every field whether the call site sets it or not, with `encoding/json`'s rules. These field types are errors:

- functions (so also `orbit.Renderer` and `orbit.Slots`), channels, `unsafe.Pointer` and complex numbers;
- interface types, including `any` and `error`, because Orbit can't check that a decoded value has the type that was sent;
- maps whose keys aren't strings, integers or `encoding.TextMarshaler`s.

The error is reported at the `server:defer` attribute and names the field path:

```text
src/pages/blog/[slug].orbit:20:13: <Comments server:defer>: Props.OnLoad has type func(), which can't be sent to a deferred component; props must be JSON-serializable
```

Types that implement both `json.Marshaler` and `json.Unmarshaler`, or both `encoding.TextMarshaler` and `encoding.TextUnmarshaler`, are fine as they are; `time.Time` is one. Fields tagged `json:"-"` and unexported fields of nested structs are skipped, as `encoding/json` skips them, so they arrive as zero values.

Prefer small props, such as an ID, and load the data in the component: the props are part of the page's HTML and of the island URL.

When the island request decodes the props, unknown JSON fields are ignored and missing ones stay zero. So a page rendered before a deploy still loads its islands after you add a field to `Props`.

### How props are protected

The props are written into the page, and the browser sends them back, so anyone can see and change what's in the HTML. Orbit therefore seals them before writing them:

- **Confidential**: they're encrypted with AES-256-GCM, so a reader of the page can't see them.
- **Tamper-proof**: a token that was changed in any way, or sealed with another key, is rejected with a 400. The token is also bound to its component ID and build ID, so it can't be moved to another component's URL.
- **Deterministic**: the nonce is an HMAC-SHA256 of the input, so the same props for the same component and build always give the same token, and so the same island URL. This makes island URLs ready for HTTP caching later. A nonce repeats only for identical inputs, which give identical ciphertexts, so the only thing an observer learns is whether two tokens hold the same props.

All the primitives are in Go's standard library and are FIPS-approved.

**A token isn't bound to a user or a time.** Anyone who has one can replay it, for as long as the key is valid. So never use props to decide who may see something: check that in the component, from cookies or `locals`, which come from the island request itself (see [The island request goes through your middleware](#the-island-request-goes-through-your-middleware)):

```orbit
---
type Props struct {
    PostID string
}

if _, err := ctx.Request.Cookie("session"); err != nil {
    return orbit.ErrNotFound
}
---
```

### The key: `ORBIT_KEY` and rolling deploys

The key that seals props is resolved once, at startup, in this order:

1. `orbit.WithKey(key)`, if you pass it to `app.New` (32 bytes).
2. The `ORBIT_KEY` environment variable: 32 bytes in standard base64. Any other value stops the server at startup with a clear message.
3. The key that `orbit build` embeds in the binary: new random bytes for each build. It's never written to `orbit_gen/` or to your repository.
4. Otherwise (a plain `go build` or `go run`), a random key for each process, with a one-line warning on stderr.

`orbit dev` sets one `ORBIT_KEY` for the whole session, so islands on open pages keep working when the app restarts; an `ORBIT_KEY` you set yourself wins.

Every instance of one build shares its embedded key, so instances behind a load balancer open each other's tokens. A **new build has a new key**. During a rolling deploy, a page rendered by an old instance can send its island request to a new one, which can't open the token: the island gets a 400 and keeps its fallback. The same happens with several builds behind one load balancer.

To avoid this, create a key once and give the same `ORBIT_KEY` to every instance of every build:

```sh
go tool orbit create-key
```

The island URL also holds the build ID, so each deploy gets fresh URLs. Orbit accepts any build ID whose token opens with its key, so with a shared `ORBIT_KEY`, islands from the previous build keep loading. See [Deploy](deploy.md).

### The island request goes through your middleware

The browser's request to `/_orbit/island/…` is an ordinary request, handled inside `site.Middleware`. So it passes through every middleware you wrap around Orbit (logging, auth, cookies, `orbit.EnsureLocals`), exactly like a page request. When your custom routes take precedence (Orbit behind a mux's `/` catch-all), it reaches Orbit through that catch-all. See [Server](server.md).

- **Locals** come from the island request's own pass through your middleware; nothing carries over from the page request. The browser sends the same cookies, so the same middleware sets the same values. See [Middleware and locals](middleware.md).
- **CSRF**: when props are large, the loader uses a POST (see [Large props](#large-props-get-and-post)). A CSRF middleware that requires a token on every POST blocks those requests. Exempt `/_orbit/island/`, or use Go's `http.CrossOriginProtection`, which lets same-origin requests through:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"

	"example.com/blog/orbit_gen/app"
	"github.com/bertrandkhe/orbit"
)

func main() {
	site := app.New()
	csrf := http.NewCrossOriginProtection()
	log.Fatal(orbit.ListenAndServe(csrf.Handler(site.Handler())))
}
```

### `ctx` in a deferred component

A deferred component renders with a fresh `ctx` for the island request, as Astro does:

- `ctx.Request` and `ctx.URL` are the island request (`/_orbit/island/…`), not the page.
- There are no route params: `ctx.Param` returns `""` for every name. Pass what the component needs, such as the post's slug, as props.
- The page's URL is in the `Referer` header:

  ```orbit
  ---
  import "net/url"

  page, err := url.Parse(ctx.Request.Referer())
  if err != nil {
      return err
  }
  ---
  <p>Comments on {page.Path}</p>
  ```

  Browsers send the full page URL on same-origin requests by default. A `Referrer-Policy: no-referrer` header on the page removes it; pass what you need as a prop instead.

### Status codes and errors

The island response is buffered and written once, with `Content-Type: text/html; charset=utf-8`, a `Content-Length`, and no CSS or JS injected. The loader puts the HTML in place only on a 2xx status. On any other status, or a network error, the fallback stays.

| Status | When |
|---|---|
| 200 | The component rendered. |
| 3xx | The component returned `ctx.Redirect(…)`. The loader doesn't follow it. |
| 400 | The token is missing, malformed, changed, sealed with another key, or doesn't decode into `Props`. Empty body. |
| 404 | The component ID isn't one used with `server:defer` (only those can be rendered by URL), or the component returned `orbit.ErrNotFound`. Empty body: `404.orbit` is a full page, so it isn't used here. |
| 405 | A method other than GET, HEAD or POST. The response has `Allow: GET, HEAD, POST`. |
| 413 | A POST body over 1 MiB. |
| 500 | The component returned an error or panicked. |

In `orbit dev`, a 500 body holds the error and its `.orbit` position, and the loader logs the status and body with `console.error`. In production, the loader stays silent.

### Response headers and caching

Island responses carry:

- `Cache-Control: private, no-store`. A component can set its own through `ctx.Header()`.
- `X-Robots-Tag: noindex`, so search engines don't index the fragments.

The island URLs are already deterministic, with the build ID in the path. HTTP caching of islands (`ETag` and `304` responses, and guidance on caching them publicly) is planned after the first release.

### Large props: GET and POST

The loader requests `GET <src>?p=<token>`. When that URL would be longer than 8000 characters (about 8 KB of props), it sends a `POST` to the same URL instead, with the token as a `text/plain` body. The server reads at most 1 MiB of body.

### Nested islands and lazy components inside islands

A deferred component can render `server:defer` components and `client:*` components of its own. When a fragment arrives, the loader scans it for both, so nested islands load and lazy scripts are observed.

A placeholder inside an HTML fragment that you insert yourself (for example an htmx partial from `orbit.RenderComponent`) loads only if the page already has the loader, which happens when the page has a deferred component of its own.

### Scripts and styles of deferred components

A deferred component, and every component it renders, is still part of the page's asset graph: its CSS and scripts load with the page, and the fragment brings none of its own. Those scripts run **before** the fragment arrives, so a script that queries the DOM when it loads doesn't see the island's elements. Write such scripts as custom elements; see [Write scripts as custom elements](#write-scripts-as-custom-elements). An `is:inline` script inside a fragment runs when the fragment is inserted.

### Without JavaScript

Without JavaScript, nothing fetches the island, so the fallback stays. Make it useful on its own where it matters, for example with a link to a page that renders the content directly.

## `client:*`: lazy scripts

A component's `<script>` normally goes into the page's bundle and runs when the page loads. A `client:*` directive on the component tag delays it. The component's HTML is rendered on the server as usual; only its scripts wait.

```orbit file=src/components/ShareButtons.orbit
---
type Props struct {
    Title string
}
---
<share-buttons data-title={props.Title}>
  <button type="button" data-action="copy">Copy link</button>
  <button type="button" data-action="share" hidden>Share…</button>
</share-buttons>
<script>
  class ShareButtons extends HTMLElement {
    connectedCallback() {
      const title = this.dataset.title ?? document.title;
      const copy = this.querySelector<HTMLButtonElement>('[data-action="copy"]')!;
      const share = this.querySelector<HTMLButtonElement>('[data-action="share"]')!;
      copy.addEventListener("click", () => navigator.clipboard.writeText(location.href));
      if ("share" in navigator) {
        share.hidden = false;
        share.addEventListener("click", () => navigator.share({ title, url: location.href }));
      }
    }
  }
  customElements.define("share-buttons", ShareButtons);
</script>
```

The post page above renders it near the bottom with `client:visible="200px"`, so its script loads only when the buttons come within 200 px of the viewport.

### Strategies

| Directive | The scripts load |
|---|---|
| `client:load` | With the page. This is the same as no directive. |
| `client:idle` | Once the browser is idle (`requestIdleCallback`, or `setTimeout` where it's missing). |
| `client:visible` | When an instance scrolls into view (`IntersectionObserver`). An optional value is the `rootMargin`: `client:visible="200px"` loads a little earlier. |
| `client:media="(max-width: 600px)"` | When the media query matches, at page load or later (`matchMedia`). The value is required. |

Values must be static strings, and a tag takes at most one `client:*` directive. The compile errors are listed in [Template syntax](reference/template-syntax.md#client). `client:only` is rejected, because it's for framework components, which Orbit doesn't have.

Each script loads at most once per page: when the first instance meets its strategy. If instances on one page use different strategies, the first one to fire loads it.

### The directive covers the whole subtree

The directive applies to the component's own scripts **and** to the scripts of every component it renders, at any depth, as an Astro island does. With

```orbit
<PostFooter client:idle />
```

where `PostFooter` renders `LikeButton` and `ShareButtons`, the scripts of all three load when the browser is idle.

A `client:*` directive inside a lazy subtree applies its own strategy once the outer one has fired: a `<Map client:visible />` inside `<PostFooter client:idle />` loads after the page is idle **and** the map is visible.

### Eager wins

A component's script is one module per page. If the same component is also rendered eagerly anywhere on the page (with no directive or with `client:load`, in the page or in any component it renders, deferred ones included), its script stays in the page bundle and runs with the page:

```orbit
<Counter />
<Counter client:visible />
```

Here the `Counter` script loads with the page, for both instances. Orbit works this out per page when it builds, so a component can be lazy on one page and eager on another.

### The `<orbit-lazy>` wrapper

To observe a component, Orbit wraps its output in an element (a component can render zero or several root elements):

```html
<orbit-lazy component="ShareButtons-4c1d2e3f" client="visible" opts="200px">…the component's HTML…</orbit-lazy>
```

- The wrapper has `display: contents`, so it doesn't affect layout. It is in the DOM, though: a child selector such as `.list > .item` doesn't match across it.
- `client:visible` observes the wrapper's element children, because a `display: contents` element has no box of its own.
- The wrapper is written even on pages where the script ends up eager. `client:load` writes no wrapper.
- The wrapper is static HTML, merged with the surrounding markup when Orbit compiles the template, so it costs nothing at render time. Styles aren't affected: a lazy component's CSS is in the page's stylesheet.

### Shared modules are duplicated

A lazy script is bundled on its own, without code splitting. A module that both a lazy script and the page bundle import (say `src/scripts/util.ts`) is included in both, and each copy has its own module state. Keep shared state out of such modules, or put it on the DOM. Code splitting is planned after the first release.

### Write scripts as custom elements

A module that runs `document.querySelectorAll(…)` when it loads sees only the elements present at that moment, not those that arrive later in a deferred island or that a lazy script loads for. For lazy and deferred components, define a custom element (`customElements.define` with `connectedCallback`), as `ShareButtons` does: the browser calls `connectedCallback` for every instance, whenever it appears. See [Styles and scripts](styles-and-scripts.md) and [Passing data to client scripts](components.md#passing-data-to-client-scripts).

### Without JavaScript

Lazy components are rendered on the server like any other, so their HTML is there. Only their scripts never run.

## Combining the two

`server:defer` and `client:*` on the same tag is a compile error. With `<Comments server:defer client:idle postID={post.Slug}>`, Orbit reports:

```text
src/pages/blog/[slug].orbit:20:26: server:defer and client:* can't be combined; put client:* on a component inside the deferred one
```

To lazy-load scripts inside deferred HTML, put the `client:*` directive on a component that the deferred component renders.

## The loader and Content Security Policy

Pages that need it get one small inline `<script type="module">` (a few hundred bytes) and a one-line `<style>` that gives `<orbit-island>` and `<orbit-lazy>` `display: contents`, both in `<head>`. Orbit decides this when it builds, from the components a page can render: pages without deferred or lazy components get neither.

The loader is inline, as Astro's island script is, which saves a request before the island fetches can start. A strict Content Security Policy must allow it, with `'unsafe-inline'` or the script's hash. Generated CSP hashes are planned after the first release.

## Coming from Astro

| Astro | Orbit |
|---|---|
| `server:defer` with a `fallback` slot | The same. Other slots can't be passed to a deferred component yet. |
| `ASTRO_KEY`, `astro create-key` | `ORBIT_KEY`, `orbit create-key` |
| `Astro.url` in a server island is the island URL; the page URL is in `Referer` | The same, with `ctx.URL` and `ctx.Request.Referer()` |
| Island props switch to POST above 2048 characters | Above 8000 characters |
| `client:*` hydrates a React, Svelte, … component | `client:*` delays the vanilla `<script>`s of an `.orbit` component and its subtree |
| `client:only` | Not supported: there are no framework components |

## See also

- [Components](components.md): props, slots and passing data to scripts
- [Template syntax](reference/template-syntax.md#directives): every directive and its compile errors
- [Styles and scripts](styles-and-scripts.md): bundling, TypeScript and custom elements
- [Middleware and locals](middleware.md): what island requests see
- [Server](server.md): where `/_orbit/island/` is handled
- [Deploy](deploy.md): `ORBIT_KEY` in production
- [Performance](performance.md): buffered responses and the `server:defer` benchmark
- [CLI](reference/cli.md): `orbit create-key`
