# Configuration

Orbit reads its settings from an optional `orbit.json` file at the root of your project, next to `go.mod`; every key has a default, so most projects need few settings or none.

## Example

A blog with a canonical site URL and a single-binary build:

```json file=orbit.json
{
  "site": "https://blog.example.com",
  "embed": true
}
```

The file is plain JSON, with no comments. `orbit doctor` checks that it's valid and that the `server` entry exists, and during `orbit dev` a change to it triggers a full rebuild and a reload.

Unknown keys are ignored, as `encoding/json` ignores unknown fields, so check the spelling of a key that seems to have no effect. A key set to `null` counts as unset.

An invalid file stops `orbit generate`, `orbit build` and `orbit dev` with an error that starts with `orbit.json`. A JSON syntax error, such as a trailing comma after the last key, carries its line and column:

```json file=orbit.json expect-error
{
  "site": "https://blog.example.com",
}
```

```text
orbit: orbit.json:3:1: invalid character '}' looking for beginning of object key string
```

## All keys

| Key | Type | Default | Effect |
|---|---|---|---|
| [`site`](#site) | string | none | Your site's absolute URL, available as `ctx.Site`. |
| [`server`](#server) | string | `"./cmd/server"` if it exists, else a generated entry | The Go package of your server's `main`. |
| [`embed`](#embed) | bool | `false` | Embed the static files in the server binary, like `orbit build --embed`. |
| [`compressHTML`](#compresshtml) | `"jsx"`, `"html"` (or `true`) or `false` | `"jsx"` | How template whitespace is compressed. |
| [`sourcemap`](#sourcemap) | bool | `false` | Write source maps for the production CSS and JS bundles. |
| [`watch`](#watch) | array of strings | `[]` | Extra files whose changes restart your server in `orbit dev`. |
| [`markdown`](#markdown) | object | GFM and smart punctuation on, `github` / `github-dark` highlighting | How Markdown files are converted. |

## `site`

The absolute URL where your site is deployed, including the scheme:

```json
{ "site": "https://blog.example.com" }
```

Pages and components read it as `ctx.Site`, a `*url.URL`, for example to build canonical URLs, sitemaps or feed links:

```orbit
{if ctx.Site != nil}
  <link rel="canonical" href={ctx.Site.JoinPath(ctx.URL.Path).String()}>
{/if}
```

When `site` isn't set, `ctx.Site` is `nil`, so check it, as above, if your project might not set it. A value that isn't an absolute URL with a scheme and a host (such as `"blog.example.com"` or `"/blog"`) is an error when Orbit loads `orbit.json`, in `orbit dev`, `orbit build` and `orbit generate`.

## `server`

The Go package that holds your server's `main`, as a path relative to the project root:

```json
{ "server": "./cmd/myserver" }
```

The default is `"./cmd/server"` when that directory exists. Without it, Orbit generates an entry in `orbit_gen/cmd/server` that serves your pages and nothing else, as if your `main` were:

```go
if err := orbit.ListenAndServe(app.New().Handler()); err != nil {
	log.Fatal(err)
}
```

The server entry is used in two places:

- `orbit dev` runs it, so your routes and middleware behave as in production.
- `orbit build` compiles it to `dist/server`.

The path must be relative to the project root and not empty; remove the key to get the default. `orbit generate` doesn't check that the directory exists; `orbit doctor` reports a `server` path that doesn't. See [Server](server.md).

## `embed`

Builds a single, self-contained server binary with the `/_orbit/` assets and `public/` files embedded in it:

```json
{ "embed": true }
```

It's the same as passing `--embed` to `orbit build`; the flag turns embedding on even when `embed` is `false`. `dist/client/` is still written, so you can also upload it to a CDN. See [Deploy](deploy.md#single-binary---embed).

## `sourcemap`

Makes `orbit build` write a source map next to each CSS and JS bundle in `dist/client/_orbit/` (`index-5RXDJ3BM.js.map` next to `index-5RXDJ3BM.js`):

```json
{ "sourcemap": true }
```

The maps aren't linked from the bundles (there's no `sourceMappingURL` comment), so browsers don't download them. Upload them to your error tracker, or serve them yourself if you want them in the browser's dev tools. Their sources are paths from your project root, such as `/src/components/Card.orbit` and `/src/scripts/util.ts`, and positions point at the line and column of the `.orbit` file itself, not at the extracted `<script>` or `<style>`. Each map includes the source text, so a public map exposes your component sources.

In `orbit dev`, source maps are always on and linked, whatever this key says. Any value other than `true`, `false` or `null` is an error: `orbit.json: sourcemap must be true or false`.

## `watch`

Glob patterns for extra files that `orbit dev` watches. When a matching file changes, `orbit dev` restarts your server and reloads the browser, as it does after a `.go` edit, but without regenerating or rebuilding: the same binary starts again. Use it for files your server reads at startup or per request that Orbit doesn't know about, such as Markdown content or a JSON data file:

```json
{ "watch": ["content/**", "../*.md"] }
```

- Patterns are relative to the project root (the directory of `orbit.json`) and use `/` on every OS. They may start with `../` to watch files outside the project, such as docs in a parent directory.
- Each part between slashes is matched like Go's [`path.Match`](https://pkg.go.dev/path#Match): `*` matches any run of characters except `/`, `?` matches one, `[a-z]` a class. A part that is exactly `**` matches any number of directories, including none: `content/**` matches every file under `content/`, and `content/**/*.md` every `.md` file there. There are no `{a,b}` alternatives; write two patterns.
- A pattern matches files, not directories: write `content/**`, not `content`.
- Orbit watches each pattern's leading directory (the part before the first `*`, `?` or `[`, such as `content/` or `../`), and the directories under it when the pattern can match deeper. Below that directory, the directories `orbit dev` always skips (`node_modules/`, directories starting with `.`, …) are skipped. If the leading directory doesn't exist when `orbit dev` starts, `orbit dev` prints a warning and doesn't watch it.
- A file that Orbit already watches (`src/`, `public/`, `.go` files) keeps its usual rebuild; `watch` only adds files. Files under `orbit_gen/`, `dist/` and `.orbit/` never match.
- `watch` changes nothing outside `orbit dev`.

Each pattern is checked when `orbit.json` is loaded. An empty or absolute pattern, or one with a bad `[` class, is an error, such as `orbit.json: watch[0] "content/[a-": segment "[a-": syntax error in pattern`. A value that isn't an array of strings is an error too: `orbit.json: watch must be an array of strings`.

## `compressHTML`

How Orbit compresses the whitespace in your templates. It's applied when the Go code is generated, so it costs nothing per request:

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
orbit: orbit.json: compressHTML must be "jsx", "html" (or true) or false, not "none"
```

In every mode, the content of `<pre>`, `<textarea>`, `<script>` and `<style>`, HTML comments, attribute values and the output of `{expr}` are never altered. The exact rules are in [Template syntax: Whitespace](reference/template-syntax.md#whitespace).

This template:

```orbit
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

## `markdown`

How Orbit converts Markdown files, with Astro's option names. Every field is optional; these are the defaults:

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

An unknown theme name is an error that lists the available themes, such as `orbit.json: markdown.syntaxHighlight.themes.dark: unknown theme "githbu"; the themes are abap, algol, …`. A field of the wrong type is an error too: `orbit.json: markdown.gfm must be true or false`. Changing an option regenerates every Markdown file. See [Markdown: Options](markdown.md#options).

## Settings that aren't in `orbit.json`

- **Runtime settings** are environment variables that the server binary reads at startup: the listen address (`ORBIT_ADDR`, or `PORT`), the static files directory (`ORBIT_STATIC`), and the island key (`ORBIT_KEY`, created with `orbit create-key`). See [Deploy](deploy.md#environment-variables).
- **The island key and build ID** that a build embeds are random, created fresh by every `orbit build`; there's nothing to configure. To share one key across builds, set `ORBIT_KEY`. See [Deploy](deploy.md#island-key-orbit_key).
- **The dev server port** is a flag: `orbit dev --port 3000`.
- **Trailing slashes** aren't configurable: a page's canonical URL never ends in `/` (except `/` itself), and the server redirects `/blog/hello/` to `/blog/hello` with a 308. See [Routing](routing.md).
- **Rendering** isn't configurable either: every page renders on the server, per request. See [Rendering](rendering.md).
- A `base` path (serving the site under a subdirectory) and `trailingSlash` modes are not yet supported.

## See also

- [Server](server.md): the server entry
- [Deploy](deploy.md): output layout, `--embed` and environment variables
- [Rendering](rendering.md): how pages render per request
- [CLI reference](reference/cli.md)
