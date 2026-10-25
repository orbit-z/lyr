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

Unknown keys are ignored, as `encoding/json` ignores unknown fields, so check the spelling of a key that seems to have no effect.

## All keys

| Key | Type | Default | Effect |
|---|---|---|---|
| [`site`](#site) | string | none | Your site's absolute URL, available as `ctx.Site`. |
| [`server`](#server) | string | `"./cmd/server"` if it exists, else a generated entry | The Go package of your server's `main`. |
| [`embed`](#embed) | bool | `false` | Embed the static files in the server binary, like `orbit build --embed`. |

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
log.Fatal(orbit.ListenAndServe(app.New().Handler()))
```

The server entry is used in two places:

- `orbit dev` runs it, so your routes and middleware behave as in production.
- `orbit build` compiles it to `dist/server`.

`orbit doctor` reports a `server` path that doesn't exist. See [Server](server.md).

## `embed`

Builds a single, self-contained server binary with the `/_orbit/` assets and `public/` files embedded in it:

```json
{ "embed": true }
```

It's the same as passing `--embed` to `orbit build`; the flag turns embedding on even when `embed` is `false`. `dist/client/` is still written, so you can also upload it to a CDN. See [Deploy](deploy.md#single-binary---embed).

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
