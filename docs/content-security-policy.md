# Content Security Policy

A Content Security Policy (CSP) is a response header that tells the browser where a page may load scripts, styles, frames and requests from. Set it in middleware, like any other header.

Lyr writes no inline script and no inline style of its own. Its loader, the `display: contents` rule for its wrapper elements and a [page partial](routing.md#page-partials)'s scripts are files under `/_lyr/`, so `'self'` covers them. A policy with `script-src 'self'; style-src 'self'` works with no hashes and no nonces, as long as your own templates add no inline scripts or styles. What you add to the policy is the origins of the third-party services your pages use.

## A complete example

This blog loads Plausible analytics from `https://plausible.io`, embeds YouTube videos from `https://www.youtube-nocookie.com`, and sets the visitor's theme before the page renders.

The analytics script is a third-party `<script>`. It has attributes, so Lyr leaves it as written (see [Which scripts Lyr processes](styles-and-scripts.md#which-scripts-lyr-processes)):

```lyr file=src/components/Analytics.lyr
<script defer data-domain="example.com" src="https://plausible.io/js/script.js"></script>
```

The video embed is an `<iframe>`:

```lyr file=src/components/YouTube.lyr
---
type Props struct {
	ID    string // the video ID, as in youtube.com/watch?v=<ID>
	Title string
}
---
<iframe
  src={`https://www.youtube-nocookie.com/embed/${props.ID}`}
  title={props.Title}
  width="560"
  height="315"
  allow="encrypted-media; picture-in-picture"
  allowfullscreen
></iframe>
```

The theme snippet used to be an `is:inline` script in the layout, which a strict policy blocks. Move its code to a file in `public/`:

```js file=public/theme-init.js
document.documentElement.dataset.theme =
  localStorage.getItem("theme") ?? "light";
```

And load it from the layout with a plain `<script src>`. A `src` that isn't a relative path (`./`, `../`) is left as written, so this is a classic script that blocks rendering, as the inline one did: the theme is set before the first paint. Like every `public/` file, it's revalidated on each visit (a `304` once it's cached).

```lyr file=src/layouts/Base.lyr
---
import Analytics "../components/Analytics.lyr"

type Props struct {
	Title string
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="icon" href="/favicon.svg">
    <title>{props.Title}</title>
    <script src="/theme-init.js"></script>
    <Analytics />
  </head>
  <body>
    <main>
      <slot />
    </main>
  </body>
</html>
```

The policy is built once, at startup, and `securityHeaders` sets it on every response. It runs first in `lyr.Sequence`, so the trailing-slash redirects get it too:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/orbit-z/lyr"

	"example.com/blog/lyr_gen/app"
)

// policy is the site's Content-Security-Policy.
var policy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self' https://plausible.io",
	"style-src 'self'",
	"img-src 'self' data:",
	"connect-src 'self' https://plausible.io",
	"frame-src https://www.youtube-nocookie.com",
	"object-src 'none'",
	"base-uri 'self'",
	"form-action 'self'",
	"frame-ancestors 'self'",
}, "; ")

func main() {
	site := app.New()

	mux := http.NewServeMux()
	mux.Handle("/", site.Handler())

	h := lyr.Sequence(securityHeaders, site.TrailingSlash, site.Rewrites)(mux)
	if err := lyr.ListenAndServe(h); err != nil {
		log.Fatal(err)
	}
}

// securityHeaders sets the Content-Security-Policy on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
```

| Directive | Why |
|---|---|
| `default-src 'self'` | Everything not listed below (fonts, manifests, workers) comes from the site itself. |
| `script-src 'self' https://plausible.io` | Your bundles and Lyr's loader under `/_lyr/`, `public/theme-init.js`, and Plausible's script. |
| `style-src 'self'` | The page stylesheets under `/_lyr/`. No inline `<style>` and no `style="…"` attribute. |
| `img-src 'self' data:` | Images from the site, `/_lyr/` and `public/`, and `data:` URLs in CSS. |
| `connect-src 'self' https://plausible.io` | `'self'` for `server:defer` island fetches (`/_lyr/island/…`) and `lyr dev`'s live reload; Plausible sends its events to its own origin. |
| `frame-src https://www.youtube-nocookie.com` | The video embeds. What runs inside the frame follows YouTube's policy, not yours. |
| `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, `frame-ancestors 'self'` | The usual hardening: no plugins, no `<base>` hijack, forms post to your site only, and only your own pages can frame yours. |

**Keep `'self'` in `connect-src`.** The loader fetches deferred components from `/_lyr/island/…`. A `connect-src` that lists only third-party origins blocks those fetches, and every `server:defer` component keeps its fallback.

To try a policy before you enforce it, send it as `Content-Security-Policy-Report-Only` instead: the browser reports what it would block in the console and blocks nothing.

## What Lyr adds to a page

Everything Lyr adds is a file under `/_lyr/` on your own origin, or data that a policy doesn't govern:

- **The page's stylesheet and module**: `<link rel="stylesheet" href="/_lyr/…">`, `<script type="module" src="/_lyr/…">`, and a `<link rel="modulepreload">` per [shared chunk](styles-and-scripts.md#shared-chunks-and-import). Lazy entries and `import()` chunks are `/_lyr/` files too.
- **The loader**, on pages with `server:defer` or `client:*` components: `<script defer src="/_lyr/loader/loader-<hash>.js">` before the stylesheet, after its settings in a `<script type="application/json" id="lyr-loader">` data block. A data block never runs, so `script-src` doesn't apply to it, as it doesn't to your own [`set:json`](styles-and-scripts.md#json-blocks-with-setjson) blocks.
- **The `display: contents` rule** for `<lyr-island>` and `<lyr-lazy>`, at the start of the page's stylesheet.
- **At the end of a page partial**: `<script src="/_lyr/loader/loader-<hash>.js" data-lyr-loader='…'>`, which runs the partial's component scripts, and `<link rel="stylesheet" href="/_lyr/loader/wrappers-<hash>.css">` when the partial has a `server:defer` or `client:*` call site in its components, even one that ends up eager.
- **Requests**: island fetches to `/_lyr/island/…`, and in `lyr dev`, the live-reload script `/__lyr/reload.js` and its event stream, on the same origin.

The loader imports and fetches only same-origin URLs under `/_lyr/`, so a settings block injected into your HTML can't make it load anything else. See [The loader and Content Security Policy](lazy-loading.md#the-loader-and-content-security-policy).

## What a missing origin looks like

The browser blocks the resource and logs an error to the console; the rest of the page keeps working. Without `https://plausible.io` in `script-src`, Chrome logs:

```text
Refused to load the script 'https://plausible.io/js/script.js' because it violates the following Content Security Policy directive: "script-src 'self'". Note that 'script-src-elem' was not explicitly set, so 'script-src' is used as a fallback.
```

The page renders, its own scripts run, and only the analytics are missing. Without it in `connect-src`, the script loads but its events fail with `Refused to connect to 'https://plausible.io/api/event'`. The error names the directive: add the origin there.

## Keeping a snippet inline: its hash

Some snippets you'd rather keep inline, such as a vendor's one-liner pasted as is. Plausible's custom-events stub is one: it queues `plausible("Signup")` calls made before the script has loaded. Put it before the Plausible script, in `src/components/Analytics.lyr`:

```lyr
<script is:inline>
  window.plausible =
    window.plausible ||
    function () {
      (window.plausible.q = window.plausible.q || []).push(arguments);
    };
</script>
<script defer data-domain="example.com" src="https://plausible.io/js/script.js"></script>
```

The policy blocks the inline script, and the console error gives the hash it needs:

```text
Refused to execute inline script because it violates the following Content Security Policy directive: "script-src 'self' https://plausible.io". Either the 'unsafe-inline' keyword, a hash ('sha256-GZtx6xGi42+/93o2Eurg4/pUe5U2g6kwpn6sGwnceAc='), or a nonce ('nonce-...') is required to enable inline execution.
```

Add that hash to `script-src`, in `cmd/server/main.go`:

```go
	"script-src 'self' https://plausible.io 'sha256-GZtx6xGi42+/93o2Eurg4/pUe5U2g6kwpn6sGwnceAc='",
```

The hash covers the exact text between the tags, whitespace included. Lyr writes a script's content as it is, but editing it, or formatting it with [`lyr fmt`](reference/cli.md#lyr-fmt), changes the hash: copy the new one from the console. An inline `<style>` works the same way, with its hash in `style-src`. A snippet whose content changes per request (a `set:html` script built in Go) has no fixed hash: move its code to a file, and pass the values as data (see [Passing data from the server to scripts](styles-and-scripts.md#passing-data-from-the-server-to-scripts)).

## What's still yours to allow

A strict policy blocks what your own templates write inline. Lyr doesn't check for it:

- `is:inline` scripts and styles, scripts with a `type` or `set:html`, and `<script>` and `<style>` in [Markdown](markdown.md#raw-html): move them to a processed `<script>` or `<style>`, to a file in `public/`, or allow their hash.
- `style={…}` attributes: use a class, or set the style from a script (`el.style.width = …` isn't blocked).
- `on…` attributes (`onclick="…"`): use `addEventListener` in a processed script.
- **htmx 2's indicator styles**: htmx 2 adds an inline `<style>` for `.htmx-indicator`, which `style-src 'self'` blocks. Turn it off and style `.htmx-indicator` in your own CSS:

  ```html
  <meta name="htmx-config" content='{"includeIndicatorStyles":false}'>
  ```

  htmx 4 needs nothing: it adds its indicator CSS with `adoptedStyleSheets`, which a policy allows.
- **htmx** features that run text as code, with `eval` or `new Function()`: trigger filters (`hx-trigger="click[ctrlKey]"`), `hx-on` attributes and `js:` values. They need `'unsafe-eval'`, so a strict policy must avoid them. htmx 4's `hx-csp` extension is an option for your site; Lyr doesn't need it. Swapping [page partials](routing.md#page-partials) needs none of these.
- Swap libraries that run a fragment's scripts by evaluating their text (jQuery's `.html()`) fail under a policy without `'unsafe-inline'` and `'unsafe-eval'`, and a partial's scripts don't run. Libraries that re-create the `<script>` elements, as htmx and Turbo do, work. See [Page partials](routing.md#page-partials).

**`'strict-dynamic'` isn't supported.** With `'strict-dynamic'`, browsers ignore `'self'` and host sources in `script-src` and only run scripts that carry a nonce or a hash, or that such a script loads. Lyr's `<script src>` tags carry neither, so the page's module and the loader are blocked.

> **Planned**: nonces for `'strict-dynamic'` policies (they conflict with caching rendered pages, so it needs a per-request option), a helper that computes the hashes of your `is:inline` scripts and styles (Astro's `security.csp`), `integrity` (SRI) attributes on Lyr's `<script>` tags, and Trusted Types (`require-trusted-types-for 'script'`; the loader inserts island HTML through a sink that needs a policy).

## See also

- [Middleware and locals](middleware.md): where middleware goes, and `lyr.Sequence`
- [The loader and Content Security Policy](lazy-loading.md#the-loader-and-content-security-policy): Lyr's loader files, their sizes and caching
- [Page partials](routing.md#page-partials): what a partial adds at its end, and swap libraries
- [Which scripts Lyr processes](styles-and-scripts.md#which-scripts-lyr-processes): processed, `is:inline` and third-party scripts
