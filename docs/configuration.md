# Configuration

Lyr reads its settings from an optional `lyr.json` file at the root of your project, next to `go.mod`; every key has a default, so most projects need few settings or none.

## Example

A blog with a canonical site URL and a single-binary build:

```json file=lyr.json
{
  "site": "https://blog.example.com",
  "embed": true
}
```

The file is plain JSON, with no comments. `lyr doctor` checks that it's valid and that the `server` entry exists, and during `lyr dev` a change to it triggers a full rebuild and a reload.

Unknown keys are ignored, as `encoding/json` ignores unknown fields, so check the spelling of a key that seems to have no effect. A key set to `null` counts as unset.

An invalid file stops `lyr generate`, `lyr build` and `lyr dev` with an error that starts with `lyr.json`. A JSON syntax error, such as a trailing comma after the last key, carries its line and column:

```json file=lyr.json expect-error
{
  "site": "https://blog.example.com",
}
```

```text
lyr: lyr.json:3:1: invalid character '}' looking for beginning of object key string
```

## All keys

| Key | Type | Default | Effect |
|---|---|---|---|
| [`site`](#site) | string | none | Your site's absolute URL, available as `ctx.Site`. |
| [`server`](#server) | string | `"./cmd/server"` if it exists, else a generated entry | The Go package of your server's `main`. |
| [`embed`](#embed) | bool | `false` | Embed the static files in the server binary, like `lyr build --embed`. |
| [`compressHTML`](#compresshtml) | `"jsx"`, `"html"` (or `true`) or `false` | `"jsx"` | How template whitespace is compressed. |
| [`trailingSlash`](#trailingslash) | `"never"` or `"always"` | `"never"` | Whether page URLs end in `/`. |
| [`sourcemap`](#sourcemap) | bool | `false` | Write source maps for the production CSS and JS bundles. |
| [`watch`](#watch) | array of strings | `[]` | Extra files whose changes restart your server in `lyr dev`. |
| [`routeRules`](#routerules) | object | none | Default cache options per route. |
| [`markdown`](#markdown) | object | GFM and smart punctuation on, `github` / `github-dark` highlighting | How Markdown files are converted. |

## `site`

The absolute URL where your site is deployed, including the scheme:

```json
{ "site": "https://blog.example.com" }
```

Pages and components read it as `ctx.Site`, a `*url.URL`, for example to build canonical URLs, sitemaps or feed links:

```lyr
{if ctx.Site != nil}
  <link rel="canonical" href={ctx.Site.JoinPath(ctx.URL.Path).String()}>
{/if}
```

When `site` isn't set, `ctx.Site` is `nil`, so check it, as above, if your project might not set it. A value that isn't an absolute URL with a scheme and a host (such as `"blog.example.com"` or `"/blog"`) is an error when Lyr loads `lyr.json`, in `lyr dev`, `lyr build` and `lyr generate`.

## `server`

The Go package that holds your server's `main`, as a path relative to the project root:

```json
{ "server": "./cmd/myserver" }
```

The default is `"./cmd/server"` when that directory exists. Without it, Lyr generates an entry in `lyr_gen/cmd/server` that serves your pages and nothing else, as if your `main` were:

```go
if err := lyr.ListenAndServe(app.New().Handler()); err != nil {
	log.Fatal(err)
}
```

The server entry is used in two places:

- `lyr dev` runs it, so your routes and middleware behave as in production.
- `lyr build` compiles it to `dist/server`.

The path must be relative to the project root and not empty; remove the key to get the default. `lyr generate` doesn't check that the directory exists; `lyr doctor` reports a `server` path that doesn't. See [Server](server.md).

## `embed`

Builds a single, self-contained server binary with the `/_lyr/` assets and `public/` files embedded in it:

```json
{ "embed": true }
```

It's the same as passing `--embed` to `lyr build`; the flag turns embedding on even when `embed` is `false`. `dist/client/` is still written, so you can also upload it to a CDN. See [Deploy](deploy.md#single-binary---embed).

## `trailingSlash`

Whether a page's canonical URL ends in `/`. Every page has exactly one URL, and the other form gets a 308 redirect to it:

```json
{ "trailingSlash": "always" }
```

| Value | Canonical URL | Redirected |
|---|---|---|
| `"never"` (default) | `/blog/hello`, `/blog` for `blog/index.lyr` | `/blog/hello/` → `/blog/hello` |
| `"always"` | `/blog/hello/`, `/blog/` | `/blog/hello` → `/blog/hello/` |

`/` is `/` in both modes, and a page whose file name has an extension, such as `src/pages/rss.xml.lyr`, is always at `/rss.xml`. The mode also sets Markdown's `Post.URL` and the URLs of rewritten `.md` links. Lyr doesn't rewrite the links in your templates: in `"always"`, write `href="/blog/{slug}/"`. [Routing: Trailing slashes](routing.md#trailing-slashes) has the details: what's never redirected, extension pages and dotted URLs.

There's no `"ignore"` mode, Astro's default: it gives every page two URLs. Any other value is an error when `lyr.json` loads, such as `lyr.json: trailingSlash must be "never" or "always", not "ignore"; Lyr has no "ignore" mode: each page has one URL`. A site that moves from Astro's `"ignore"` picks one of the two.

## `sourcemap`

Makes `lyr build` write a source map next to each CSS and JS bundle in `dist/client/_lyr/` (`index-5RXDJ3BM.js.map` next to `index-5RXDJ3BM.js`):

```json
{ "sourcemap": true }
```

The maps aren't linked from the bundles (there's no `sourceMappingURL` comment), so browsers don't download them. Upload them to your error tracker, or serve them yourself if you want them in the browser's dev tools. Their sources are paths from your project root, such as `/src/components/Card.lyr` and `/src/scripts/util.ts`, and positions point at the line and column of the `.lyr` file itself, not at the extracted `<script>` or `<style>`. Each map includes the source text, so a public map exposes your component sources.

In `lyr dev`, source maps are always on and linked, whatever this key says. Any value other than `true`, `false` or `null` is an error: `lyr.json: sourcemap must be true or false`.

## `watch`

Glob patterns for extra files that `lyr dev` watches. When a matching file changes, `lyr dev` restarts your server and reloads the browser, as it does after a `.go` edit, but without regenerating or rebuilding: the same binary starts again. Use it for files your server reads at startup or per request that Lyr doesn't know about, such as Markdown content or a JSON data file:

```json
{ "watch": ["content/**", "../*.md"] }
```

- Patterns are relative to the project root (the directory of `lyr.json`) and use `/` on every OS. They may start with `../` to watch files outside the project, such as docs in a parent directory.
- Each part between slashes is matched like Go's [`path.Match`](https://pkg.go.dev/path#Match): `*` matches any run of characters except `/`, `?` matches one, `[a-z]` a class. A part that is exactly `**` matches any number of directories, including none: `content/**` matches every file under `content/`, and `content/**/*.md` every `.md` file there. There are no `{a,b}` alternatives; write two patterns.
- A pattern matches files, not directories: write `content/**`, not `content`.
- Lyr watches each pattern's leading directory (the part before the first `*`, `?` or `[`, such as `content/` or `../`), and the directories under it when the pattern can match deeper. Below that directory, the directories `lyr dev` always skips (`node_modules/`, directories starting with `.`, …) are skipped. If the leading directory doesn't exist when `lyr dev` starts, `lyr dev` prints a warning and doesn't watch it.
- A file that Lyr already watches (`src/`, `public/`, `.go` files) keeps its usual rebuild; `watch` only adds files. Files under `lyr_gen/`, `dist/` and `.lyr/` never match.
- In a project that uses [Tailwind](tailwind.md), a matching file is also one Tailwind scans: when it brings a word Tailwind hasn't seen, Tailwind reruns too. That's how to watch a [`@source`](tailwind.md#use-utilities-in-templates) directory outside the module, such as `{ "watch": ["../packages/ui/**"] }`.
- `watch` changes nothing outside `lyr dev`.

Each pattern is checked when `lyr.json` is loaded. An empty or absolute pattern, or one with a bad `[` class, is an error, such as `lyr.json: watch[0] "content/[a-": segment "[a-": syntax error in pattern`. A value that isn't an array of strings is an error too: `lyr.json: watch must be an array of strings`.

## `compressHTML`

How Lyr compresses the whitespace in your templates. It's applied when the Go code is generated, so it costs nothing per request:

```json
{ "compressHTML": "html" }
```

| Value | Effect |
|---|---|
| `"jsx"` (default) | JSX's whitespace rules, Astro v7's default: whitespace that contains a newline is removed when it's next to a tag or an expression, and whitespace within a line is kept. Write `{" "}` where you need a space across lines. |
| `"html"` | HTML-aware and lossless, like Astro's `compressHTML: true`. Each run of whitespace becomes one space, or one newline if it contains one. Whitespace-only text between block-level tags is dropped, but whitespace next to text, inline elements or `{expr}` always keeps one character, so words never join. |
| `true` | The same as `"html"`, so an Astro config value works as is. |
| `false` | Template text is written exactly as it is. |

The default is the same as Astro's, so a template ported from Astro renders the same whitespace. The default used to be `"html"`. A project that relied on it, with words or inline elements on separate lines, can keep its output by setting `"compressHTML": "html"`, or add `{" "}` where a space must survive a line break.

Any other value is an error:

```text
lyr: lyr.json: compressHTML must be "jsx", "html" (or true) or false, not "none"
```

In every mode, the content of `<pre>`, `<textarea>`, `<script>` and `<style>`, HTML comments, attribute values and the output of `{expr}` are never altered. The exact rules are in [Template syntax: Whitespace](reference/template-syntax.md#whitespace).

This template:

```lyr
<ul>
  <li><a href="/">Home</a></li>
  <li>Posts by <b>{name}</b>
      and friends</li>
</ul>
<p>
  <a href="/a">One</a>
  <a href="/b">Two</a>
</p>
```

renders, with `name` set to `"Ana"`, as follows. With `"jsx"` (the default), every whitespace run with a newline is gone, so `Anaand` and `OneTwo` are joined; write `<b>{name}</b>{" "}` and `<a href="/a">One</a>{" "}` to keep the spaces:

```html
<ul><li><a href="/">Home</a></li><li>Posts by <b>Ana</b>and friends</li></ul><p><a href="/a">One</a><a href="/b">Two</a></p>
```

With `"html"`, the whitespace between block tags is gone, and each run between inline content became one newline:

```html
<ul><li><a href="/">Home</a></li><li>Posts by <b>Ana</b>
and friends</li></ul><p>
<a href="/a">One</a>
<a href="/b">Two</a>
</p>
```

With `false`, the output is the template as written.

## `routeRules`

Default [route caching](caching.md) options per route, so pages are cached without calling `ctx.Cache().Set` themselves. Each key is a route pattern, and each value has up to three keys:

```json
{
  "routeRules": {
    "/blog/[...path]": { "maxAge": 300, "swr": 3600, "tags": ["blog"] },
    "/products/[id]": { "maxAge": 60 }
  }
}
```

| Key | Type | Effect |
|---|---|---|
| `maxAge` | whole seconds | How long a stored response stays fresh. Nothing is stored without it. |
| `swr` | whole seconds | How long after `maxAge` a stale copy is still served while one background render refreshes it. |
| `tags` | array of strings | Tags for [invalidation](caching.md#invalidation). |

- **Patterns** use Lyr's route syntax (`[param]`, `[...rest]`) and priority, and match the request path, whichever page serves it: `/blog/[...path]` covers `/blog` and everything under it. They never match `/_lyr/` paths.
- **Seconds**, as in Astro, where `ctx.Cache().Set` takes `time.Duration` values.
- **The rule comes first**: it's applied before the page renders, under what your middleware set, so the middleware's values and then the page's `Set` win over it, and tags accumulate.
- **Without a cache provider** (`lyr.WithCache` in `cmd/server/main.go`), rules do nothing.
- **`lyr dev`** turns caching off, so rules change nothing there.

The rules are compiled into the generated `app.New()`, so nothing is parsed at run time. They're checked when `lyr.json` loads: an invalid pattern, a negative or fractional number, an unknown key in a rule (unlike unknown top-level keys) or two patterns that match the same paths is an error:

```json file=lyr.json expect-error
{
  "routeRules": {
    "/blog/[slug": { "maxAge": 300 }
  }
}
```

```text
lyr: lyr.json: routeRules["/blog/[slug"]: segment "[slug" must be text, [param] or [...rest]
```

See [Caching](caching.md) for the whole flow.

## `markdown`

How Lyr converts Markdown files, with Astro's option names. Every field is optional; these are the defaults:

```json
{
  "markdown": {
    "gfm": true,
    "smartypants": true,
    "syntaxHighlight": {
      "themes": { "light": "github", "dark": "github-dark" },
      "css": true
    }
  }
}
```

| Field | Effect |
|---|---|
| `gfm` | GitHub Flavored Markdown: tables, task lists, strikethrough, footnotes, bare URLs. |
| `smartypants` | Smart punctuation: curly quotes, dashes, ellipses. |
| `syntaxHighlight` | `false` leaves code blocks as `<pre><code class="language-go">`, for a client-side highlighter. `themes` names Chroma themes (`"dark": null` uses the light theme everywhere), and `"css": false` keeps the token classes but adds no stylesheet. |

An unknown theme name is an error that lists the available themes, such as `lyr.json: markdown.syntaxHighlight.themes.dark: unknown theme "githbu"; the themes are abap, algol, …`. A field of the wrong type is an error too: `lyr.json: markdown.gfm must be true or false`. Changing an option regenerates every Markdown file. See [Markdown: Options](markdown.md#options).

## Settings that aren't in `lyr.json`

- **Runtime settings** are environment variables that the server binary reads at startup: the listen address (`LYR_ADDR`, or `PORT`), the static files directory (`LYR_STATIC`), and the island key (`LYR_KEY`, created with `lyr create-key`). See [Deploy](deploy.md#environment-variables).
- **The island key and build ID** that a build embeds are random, created fresh by every `lyr build`; there's nothing to configure. To share one key across builds, set `LYR_KEY`. See [Deploy](deploy.md#island-key-lyr_key).
- **The dev server port** is a flag: `lyr dev --port 3000`.
- **Tailwind CSS** has no key: it's configured in CSS (`@theme`, `@source`, `@plugin`, …), turned on by `@import "tailwindcss"` in a stylesheet, and Lyr only needs to find its CLI. See [Using Tailwind](tailwind.md).
- **Rendering** isn't configurable either: every page renders on the server, per request. See [Rendering](rendering.md).
- A `base` path (serving the site under a subdirectory) is not yet supported.

## See also

- [Server](server.md): the server entry
- [Deploy](deploy.md): output layout, `--embed` and environment variables
- [Rendering](rendering.md): how pages render per request
- [CLI reference](reference/cli.md)
