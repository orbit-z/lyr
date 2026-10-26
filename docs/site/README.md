# Orbit's docs site

Orbit's website, built with Orbit. The pages are the Markdown files in `docs/` (and the repository's `CHANGELOG.md`), rendered at startup by `internal/content`; `src/` holds the layout, the home page, search, the sitemap and `robots.txt`.

## Develop

```sh
cd docs/site
go tool orbit dev        # http://localhost:4321, reloads on .orbit and docs/*.md edits
go test ./...            # includes the link and anchor checker for docs/
```

`orbit dev` reads the Markdown from disk. Elsewhere, set `DOCS_DIR=..` (the `docs/` directory) to do the same, or run `go generate ./internal/content` to copy it into the binary.

After changing the Chroma themes, refresh the highlighting stylesheet with `go test ./internal/content -update`.

## Deploy

Build the image from the repository root, since the site needs `docs/`, `CHANGELOG.md` and the Orbit module itself (`go.mod` replaces it with `../..`):

```sh
docker build -f docs/site/Dockerfile -t orbit-docs .
docker run -p 8080:8080 -e SITE_URL=https://docs.example.com orbit-docs
```

The image holds one self-contained binary (`orbit build --embed`, Markdown embedded) on distroless. Without Docker: `go generate ./internal/content && go tool orbit build --embed`, then run `dist/server`.

| Setting | What it does |
|---|---|
| `SITE_URL` | The site's public URL, for canonical links, Open Graph tags, `sitemap.xml` and `robots.txt`. Overrides `site` in `orbit.json`, which is the placeholder `https://orbit.example` until the domain is decided: set one or the other before launch. |
| `PORT` / `ORBIT_ADDR` | Where to listen (`PORT=8080` in the image). |
| `DOCS_DIR` | Read the Markdown from this directory instead of the embedded copy (development). |

No `ORBIT_KEY` is needed: the site has no `server:defer` components.

- **Health check**: `GET /healthz` answers `ok`. The distroless image has no shell, so point your platform's probe at it rather than a Docker `HEALTHCHECK`.
- **Compression**: the server gzips text responses itself (`cmd/server/gzip.go`), with `Vary: Accept-Encoding`. A CDN in front may compress too.
- **Caching**: HTML is sent with `Cache-Control: private, no-cache`, because the theme comes from a cookie: keep CDNs from caching it. `/_orbit/*` assets are fingerprinted and `immutable`; `sitemap.xml` and `robots.txt` may be cached for an hour.
- **No analytics** and no third-party requests.
