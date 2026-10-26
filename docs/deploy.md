# Deploy

`orbit build` produces a Go server binary that renders every page per request; this page covers what to ship, the single-binary `--embed` build, the scaffolded Dockerfile, the island key, and the environment variables the server reads.

## What `orbit build` produces

```sh
go tool orbit build
```

```
dist/
  server         your cmd/server/main.go, compiled, with every page in it
  client/        /_orbit/ assets (hashed CSS and JS) and public/ files
```

There are no HTML files: Orbit is server-rendered only, so every page renders when it's requested. (For a fully static site, Astro is the better tool.) Before deploying, `go tool orbit preview` runs `dist/server` locally, on <http://localhost:4321>.

Each build also gets a fresh random **island key** and **build ID**, embedded in the binary. They matter only if you use [`server:defer`](lazy-loading.md); see [Island key](#island-key-orbit_key) below.

## Server binary: `dist/server` and `dist/client`

Deploy both, side by side, and run the binary:

```sh
./dist/server
```

The server looks for its static files in the `client/` directory next to the binary (in the binary's own directory, not the working directory, after resolving symlinks to the binary), so you can start it from anywhere. It listens on `:4321` unless you set `ORBIT_ADDR` or `PORT`, and it shuts down gracefully on SIGINT and SIGTERM (when your `main` uses `orbit.ListenAndServe`, as the scaffolded one does).

To deploy, copy the binary and the directory together:

```sh
rsync -a dist/server dist/client deploy@example.com:/srv/blog/
```

Files under `/_orbit/` have content hashes in their names and are served with `Cache-Control: public, max-age=31536000, immutable`, so a CDN in front of the server can cache them indefinitely. You can also upload `dist/client/` to a CDN directly. With [`"sourcemap": true`](configuration.md#sourcemap), `dist/client/_orbit/` also holds a `.map` file per bundle; nothing links to them, but they're served like the bundles if someone asks for their URL, so delete them before deploying if your sources are private. Pages and [`server:defer`](lazy-loading.md) island responses are rendered per request; islands default to `Cache-Control: private, no-store`, so shared caches don't store them.

### How static files are served

- **Caching**: `/_orbit/*` files get `Cache-Control: public, max-age=31536000, immutable`, and `public/` files `Cache-Control: public, max-age=0`, so browsers revalidate them. In `orbit dev`, both get `no-cache`.
- **Validators**: files from a directory get `Last-Modified`. Embedded files (`--embed`) have no modification time, so they get a strong `ETag` from their content instead. Conditional and `Range` requests work for both.
- **Exact paths only**: a file is served only at its exact path. A directory is never served, not even its `index.html`, so `public/docs/index.html` is at `/docs/index.html`, not `/docs`. Paths that aren't clean (with `..`, `.`, `//` or a trailing slash) never match a file.
- **Inside the directory**: a lookup can't leave the static directory, through `..` or a symlink. Dotfiles (`public/.well-known/…`) are served like any other file.
- **Indexed at startup**: the server lists the static files once, when it starts, so every request checks them without a system call. A file added to `dist/client/` while the server runs isn't served until you restart it. `orbit dev` looks files up on every request instead.
- **A missing directory**: if `ORBIT_STATIC` or `orbit.WithStaticDir` names a directory that doesn't exist, the server logs one line at startup and serves no static files from it:

  ```text
  orbit: ORBIT_STATIC directory /srv/blog/client doesn't exist; no static files are served from it
  ```

Orbit doesn't compress responses. Add a gzip middleware in `cmd/server/main.go`, or let your reverse proxy or CDN compress.

### Building for another OS or architecture

`orbit build` runs `go build` with your environment, so `GOOS` and `GOARCH` choose the target of `dist/server`:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go tool orbit build
```

The binary is always named `dist/server`, even for `GOOS=windows` (no `.exe` is added).

Or build inside Docker, as the scaffolded `Dockerfile` does.

## Single binary: `--embed`

```sh
go tool orbit build --embed
```

`--embed` (or `"embed": true` in [`orbit.json`](configuration.md#embed)) builds a `dist/server` that contains the `/_orbit/` assets and `public/` files, so it's the only file you need to deploy. Copy it anywhere and run it. `dist/client/` is still written, in case you also want to upload it to a CDN.

If `ORBIT_STATIC` is set when the binary starts, it serves files from that directory instead of the embedded ones. `orbit.WithStaticDir` in your `main.go` wins over them too.

Under the hood, `--embed` copies `dist/client` to `orbit_gen/static/client/` and builds the server with the `orbit_embed` build tag (added to any `-tags` in your `GOFLAGS`), which makes `app.New()` serve the embedded copy. Its `static.go` and `client/` stay out of git along with the rest of `orbit_gen/` (`orbit generate` always writes `orbit_gen/static/doc.go`, so the package exists for `go mod tidy` even without the tag). A plain `go build` of your server, without the tag, doesn't need it and serves `client/` next to the binary as usual. The embedded files are indexed and hashed for their `ETag` once, at startup.

## Docker

`orbit new` scaffolds a `Dockerfile` and a `.dockerignore`. Build and run the image:

```sh
docker build -t blog .
docker run -p 8080:8080 blog
```

The image contains only the single binary from `orbit build --embed`, on a distroless base, and it's typically 10–20 MB.

```dockerfile file=Dockerfile
# Build stage: the Go version matches the toolchain line in go.mod.
FROM golang:1.27.3 AS build
WORKDIR /src

# Download modules first, so this layer is cached until go.mod or go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# No Node or Bun needed: esbuild runs inside the orbit CLI.
RUN CGO_ENABLED=0 go tool orbit build --embed

# Final stage: only the self-contained binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/dist/server /server
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
```

- **Build stage**: the `golang` image tag is the exact Go version the CLI was built with, the same as the `toolchain` line `orbit new` writes in `go.mod`. When you upgrade Go, update both, so the image builds with the same Go as your machine. `go tool orbit` builds the CLI version pinned in `go.mod`, so the image needs no global `orbit` install.
- **`CGO_ENABLED=0`** produces a static binary, which the distroless `static` image requires.
- **Final stage**: `gcr.io/distroless/static-debian12:nonroot` has no shell or package manager and runs as a non-root user. With `--embed`, `dist/server` is the only file it needs.
- **`ENV PORT=8080`** sets the default port. A platform that assigns the port through `PORT` (Cloud Run, Fly.io, Heroku and others) overrides it at run time, so the image works there unchanged. To listen on a specific address instead, set `ORBIT_ADDR`, which takes precedence over `PORT`.
- **The island key** isn't baked into the image by hand: `orbit build` embeds a fresh one in each build. Pass a shared `ORBIT_KEY` at run time when you need one (`docker run -e ORBIT_KEY=… -p 8080:8080 blog`); see [below](#island-key-orbit_key).

The `.dockerignore` keeps local build output out of the build context. Orbit regenerates all of it inside the image:

```text file=.dockerignore
.git
.orbit
dist
orbit_gen
```

`node_modules/` stays in the context, because esbuild resolves npm imports in your scripts from it and the image has no Node or Bun to install them.

A project created before Orbit's first release has a `replace` directive in `go.mod` pointing at a local clone of Orbit (see [Getting started](getting-started.md#trying-orbit-before-its-first-release)). The build stage can't see that directory, so `go mod download` fails. To build the image anyway (in a copy of the project, since local builds need the original `replace`), point the `replace` at a path inside the image, such as `replace github.com/orbit-z/orbit => /orbit`, add `COPY --from=orbit . /orbit` after `WORKDIR /src`, and pass the clone as a named build context: `docker build --build-context orbit=/path/to/orbit -t blog .`.

## Island key: `ORBIT_KEY`

Components rendered with [`server:defer`](lazy-loading.md) load in a separate request after the page, at a URL such as `/_orbit/island/ee678fa277a9c2c3/Comments-9b1e44a2?p=…`. The `p` token holds the component's props, encrypted and authenticated with the **island key**, and the path holds the **build ID**. You don't need to do anything for a single server, or for several instances of the same build: they share the key that `orbit build` embedded.

The key matters when instances from **different builds** serve the same site at once:

- during a **rolling deploy**, when old and new instances run side by side;
- with **several builds behind one load balancer**.

Each build has its own random key, so an instance can't open a token sealed by an instance of another build. The island request gets a 400, and the page keeps showing the component's fallback content. To avoid this, create one key and give it to every instance:

```sh
go tool orbit create-key
```

```
ux2hIy3mawmeow6rK/mU0jIVhsFl4yTuOsZ2B+xMaWM=
```

Set the printed value as `ORBIT_KEY` in your platform's secret settings, for every instance and every future deploy:

```sh
ORBIT_KEY='ux2hIy3mawmeow6rK/mU0jIVhsFl4yTuOsZ2B+xMaWM=' ./dist/server
```

- **Keep it secret**, like a session key: anyone with it can read and forge island props. Don't commit it.
- **The format** is standard base64 of exactly 32 bytes, as `orbit create-key` prints it. Anything else makes the server stop at startup with an error that names `ORBIT_KEY`.
- **The build ID** still changes with every build, so every deploy gets fresh island URLs. A server accepts any build ID whose token it can open, so with a shared `ORBIT_KEY`, pages rendered by the old build keep loading their islands from the new one.
- **Changing the key** (to rotate it) breaks the islands of pages that visitors already have open; they keep their fallback until reloaded.

The key is resolved once, at startup, in this order: the `orbit.WithKey` option passed to `app.New`, then `ORBIT_KEY`, then the key `orbit build` embedded. A binary built with plain `go build` or run with `go run` has no embedded key; it creates a random key per process and prints a warning, so don't deploy one. `orbit dev` sets its own `ORBIT_KEY` for each session (unless you set one), so open pages' islands keep working across rebuilds.

Tokens aren't tied to a user or an expiry time: anyone who has one can replay it. Check authorization inside the deferred component (cookies, `locals`), never through its props. See [Lazy loading](lazy-loading.md).

## Environment variables

The server binary reads these at startup:

| Variable | Default | Effect |
|---|---|---|
| `ORBIT_ADDR` | unset | The listen address, as `host:port` (`:8080`, `127.0.0.1:4321`). Takes precedence over `PORT`. |
| `PORT` | unset (`8080` in the scaffolded Docker image) | Used only when `ORBIT_ADDR` is unset: the server listens on `:$PORT`. Many hosting platforms set it. |
| `ORBIT_STATIC` | `client/` next to the binary, or the embedded files with `--embed` | The directory to serve `/_orbit/` assets and `public/` files from. It also overrides embedded files. |
| `ORBIT_KEY` | the key embedded by `orbit build` | The island key, as standard base64 of 32 bytes (`orbit create-key`). Set the same value on every instance when builds overlap. |

With neither `ORBIT_ADDR` nor `PORT`, the server listens on `:4321`.

`orbit dev` also sets `ORBIT_DEV` and `ORBIT_PUBLIC` when it runs your server. They switch on development behavior; don't set them in production.

### If you write your own `http.Server`

`orbit.ListenAndServe` reads the address with `orbit.Addr()`. If your `main` creates its own `http.Server`, use `orbit.Addr()` too, so the variables above, and `orbit dev`, keep working:

```go file=cmd/server/main.go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/orbit-z/orbit"

	"example.com/blog/orbit_gen/app"
)

func main() {
	site := app.New()

	srv := &http.Server{
		Addr:              orbit.Addr(), // ORBIT_ADDR, else ":"+PORT, else ":4321"
		Handler:           site.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
```

You then handle graceful shutdown yourself. See [Server](server.md).

### Setting the static directory or the key in code

To fix the static directory in code, pass `orbit.WithStaticDir` to `app.New`. It replaces the default (`client/` next to the binary), but `ORBIT_STATIC` still wins when it's set, so `orbit dev`, which sets it, keeps serving its own assets:

```go
site := app.New(orbit.WithStaticDir("/srv/blog/client"))
```

`orbit.WithKey` sets the 32-byte island key from your own secret store, and wins over `ORBIT_KEY`:

```go
site := app.New(orbit.WithKey(key)) // key is a []byte of length 32
```

## See also

- [Rendering](rendering.md): per-request rendering and the `dist/` layout
- [Lazy loading](lazy-loading.md): `server:defer` islands, their tokens and the key
- [Server](server.md): `cmd/server/main.go`, `ListenAndServe` and `Addr`
- [Configuration](configuration.md): `server` and `embed`
- [CLI reference](reference/cli.md): `orbit build`, `orbit preview` and `orbit create-key`
