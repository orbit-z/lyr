# Lyr example: a blog

A small blog that uses Lyr's main features the way the [docs](../../docs/README.md) recommend. Its `go.mod` points at this repository with `replace github.com/orbit-z/lyr => ../..`, so it always runs the Lyr next to it.

## Run it

```sh
cd examples/blog
go tool lyr dev        # http://localhost:4321, reloads on save
```

Other commands:

```sh
go tool lyr generate && go test ./...   # the lyrtest tests in internal/views_test
go tool lyr build && dist/server        # production build, on :4321
go tool lyr build --embed               # dist/server alone serves everything
```

`lyr_gen/` is generated and not committed: after a fresh clone, run `go tool lyr generate` before `go build`, `go vet` or `go test`.

## What each page shows

| URL | File | Shows |
|---|---|---|
| `/` | `src/pages/index.lyr` | The post list: a `Card` component per post with a default slot, a scoped `<style>`, and a `<script type="application/ld+json">` sent to the layout's `head` slot, left untouched |
| `/blog/hello` | `src/pages/blog/[slug].lyr` | A dynamic route: `posts.Find(params.Slug)`, `return lyr.ErrNotFound` for an unknown slug, trusted HTML with `set:html`. `ShareButtons` loads its script with `client:visible="200px"`. `Comments` is deferred with `server:defer` (its query takes 200 ms) and shows a fallback until it arrives; it renders the recursive `CommentThread` (`<Self>`), three replies deep, where a commenter's `javascript:` website is filtered out |
| `POST /blog/hello/like` | `cmd/server/main.go` | A custom route that returns a `LikeButton` fragment with `lyr.RenderComponent`. The button's script (TypeScript, importing `src/scripts/util.ts`) swaps it in |
| `/api/posts` | `cmd/server/main.go` | A custom JSON route. Custom routes take precedence over pages: Lyr is the mux's `/` catch-all |
| `/search?q=go` | `src/pages/search.lyr` | Reads `?q=` from `ctx.Request`. Post titles reach the page script through a `set:json` block |
| `/account` | `src/pages/account.lyr` | Typed locals (`src/locals`): the request ID and theme set by the `requestInfo` middleware with `lyr.EnsureLocals`. Try `curl -b theme=dark localhost:4321/account`, or the buttons, which set the `theme` cookie |
| `/nope` | `src/pages/404.lyr` | The 404 page, also used for `/blog/nope` |

Also:

- `cmd/server/main.go` wraps everything in `logging(requestInfo(mux))`: every request, island requests included, is logged with its status and request ID.
- `src/layouts/Base.lyr` holds the page shell, a global style with the light and dark colors (`data-theme={locals.Theme}`) and a scoped one.
- `internal/posts` is an in-memory store of posts, likes and comments.
- `public/` files (`favicon.svg`, `robots.txt`) are served as-is.
- `internal/views_test` tests components and pages with `lyrtest`: `Render` with props, slots, params and locals, a `Snapshot` (`go test ./internal/views_test -update` rewrites `testdata/card.golden`), and `Get` through `app.New().Handler()`, island round trip included.
- The `Dockerfile` is the one `lyr new` scaffolds. It can't build this copy as it is, because of the `replace` directive; see [Deploy](../../docs/deploy.md#docker).
