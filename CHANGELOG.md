# Changelog

What's new in each Lyr release. The VS Code extension has its own changelog, in `editors/vscode/CHANGELOG.md`.

## v0.1.0

The first release of Lyr: Astro's developer experience, with Go on the server and much faster server rendering.

Before this release the project was called Orbit. Everything moved to the new name: `.orbit` files are `.lyr`, the CLI is `lyr`, the Go package and module are `lyr` and `github.com/orbit-z/lyr`, `orbit.json` is `lyr.json`, generated code is in `lyr_gen/`, assets are served under `/_lyr/`, environment variables start with `LYR_` (`LYR_KEY`, `LYR_DEV`, …), scoped-CSS attributes are `data-l-…`, and the VS Code extension is `orbit-z.lyr`.

**Components and pages**
- `.lyr` single-file components: Go frontmatter between `---` lines, then an HTML template with `{…}` Go expressions, `{if}`, `{for}` and `{switch}` blocks, slots, `class:list`, pass-through attributes and recursive components.
- Compiled to type-checked Go: errors point at the `.lyr` line, and each expression is written by its type, with no reflection.
- Floats print as JavaScript (and so Astro) prints numbers: `{1e6}` renders `1000000` (it was `1e+06`), `{1e-7}` renders `1e-7`, `{1e21}` renders `1e+21`, `-0` renders `0`, and `NaN`/`Infinity`/`-Infinity` as in JavaScript. A named float type prints the same way; a float inside a struct still prints as `fmt.Sprint` does.
- `lyr.If(cond, v)`, `lyr.NonZero(x)` and `lyr.Cond(cond, a, b)` for conditional attribute values and text (compiled to an `if`, no allocation), and a `bool` writes `"true"`/`"false"` on `aria-*` and the other true/false attributes.
- Typed optional values, `lyr.Opt[T]` (Astro's `undefined`): `lyr.Some`, `lyr.None`, `Get`, `Or`, `IsSome`; none omits an attribute and writes nothing in content, and it's `null` in JSON. Props of type `lyr.Opt[T]` are optional props, as Astro's `rel?: string`: callers pass `rel="x"`, `rel={s}` or `rel={lyr.If(c, s)}`. `lyr.If` now returns a `lyr.Opt[T]` instead of an `any`, so comparing its result with `nil` or a type assertion on it no longer compiles (use `Get`), and `fmt.Sprint(lyr.If(false, x))` prints `""` instead of `<nil>`. An `Opt` is lowered like `lyr.If` on attributes, in content, in `set:html` (a `lyr.Opt[lyr.HTML]`: none writes nothing) and in pass-through attributes: no allocation.
- Template literals, as in Astro: `` href={`/fiction/${book.Slug}`} ``, `` <Card title={`Chapter ${n}`} /> ``, `` {` · ${n} words`} `` and `` lyr.If(open, `Close ${name}`) `` port unchanged. Pieces are Go expressions of any type, printed as `{…}` prints them; JavaScript's escapes (an unknown one is an error). On an element attribute or in content each piece is written straight to the buffer, with no allocation, and a URL attribute still filters the whole value; elsewhere a literal is one string concatenation. A literal whose pieces are numbers, bools, constants or `lyr.CSS` values doesn't get the `style` warning, and can be a `lyr.URL`/`lyr.HTML`/`lyr.CSS` prop. In the template, a backtick string is now always a template literal, never a Go raw string (the frontmatter is unchanged): a raw string with a backslash, such as a regular expression, is an error there, and moves to the frontmatter.
- Spreading props into a component, as in Astro: `<NovelCard {...novelcard.BookCard(book)} />`, `<BookActions {...actions} questionID="top" />`, `<Inner {...props} />`. A struct (or pointer to one) sets the props of the same field names, checked by the build: a field that isn't a prop, a mismatched type, or an attribute before a spread that would always overwrite it is an error at the `.lyr` position (in the editor too, a moment after typing). A `T` field sets a `lyr.Opt[T]` prop, a nil pointer spreads nothing, and a `lyr.Attrs`/`AttrList`/`RestAttrs` spread still passes HTML attributes through. Compiled to field assignments: no allocation, no reflection.
- File-based routing under `src/pages`, with dynamic and catch-all routes, typed route params (`params.Slug`), a custom `404.lyr` and no trailing slash by default (`"trailingSlash": "always"` mirrors it).
- The fragment shorthand `<>…</>`, as in Astro: the same as `<Fragment>…</Fragment>`, without attributes.
- Dynamic tags, as in Astro: `Tag := lyr.Cond(link, "a", "button")`, then `<Tag …>…</Tag>` renders the element the variable names, and `lyr.Fragment` renders no element (an optional wrapper). The name is checked at render time; no allocation.
- `lyr.AttrList`, an ordered spread built with `lyr.A(name, value)`: `{...list}` writes its attributes in list order (a `lyr.Attrs` map stays sorted), for Astro's byte order, with no allocation for a list built in the page. It also passes through `lyr.RestAttrs` and `server:defer`.
- `lyr.CSS` marks trusted CSS: a `style={…}` value of that type, or a constant, no longer warns (pass-through `style`/`on*` attributes now warn too, at the call site).
- `set:html` and `set:text` on `<script>` and `<style>` write the content in place, unprocessed, as in Astro (for example a theme script that must run before paint).
- A `set:html` value that isn't a `lyr.HTML` (a `string` variable, say) is now a Lyr error at the value, shown in the editor a moment after typing: `set:html needs lyr.HTML or lyr.Opt[lyr.HTML], not string: convert a trusted value with lyr.HTML(…), or use set:text`. It was Go's `cannot use body (variable of type string) as lyr.HTML value in argument to lyr__w.HTML`.
- Markdown pages and imports (`.md`), with typed frontmatter checked against the layout's props and code blocks highlighted at build time.
- Component imports and Markdown layouts can use `tsconfig.json` path aliases, as in Astro: `Card "@/components/Card.lyr"`.
- Import paths starting with `/` resolve from the project root, as in Astro and Vite: `Card "/src/components/Card.lyr"`. Accepting an unimported component in tag completion now uses its `tsconfig.json` alias when one covers it.

**Server**
- Server rendering only: every page renders per request.
- Lyr is `net/http` middleware: your `cmd/server/main.go` decides where it sits and which routes win. Typed request locals carry data from your middleware to your pages.
- Responses are buffered from pooled buffers: rendering a typical page allocates nothing in Lyr's code.
- `lyr.All` loads a page's data concurrently, `server:concurrent` renders slow components in parallel, and `server:defer` sends a page at once and fetches a slow component afterwards (props encrypted).
- An error handler hook maps your errors to your own responses; production 500s log the `.lyr` position.
- The matched route for request logs, like Astro's `context.routePattern`: a `lyr.RouteInfo` field in your `Locals` (`Route lyr.RouteInfo`) is filled with the page, 404 or island route and its params before rendering, so middleware logs `/blog/[slug]` instead of the URL. No allocation. `site.Match(r)` returns the same route without serving the request, for middleware that answers before Lyr (a sign-in redirect, a 503) and still logs the route, as Astro's middleware sees `context.routePattern`. A GET or HEAD of a `public/` file or `/_lyr/` asset is `lyr.RouteFile`, so middleware can tell a file from an unknown path.
- Route caching, like Astro 7's: a page calls `ctx.Cache().Set(lyr.CacheOptions{MaxAge: time.Hour, Tags: …})` or matches a `routeRules` entry in `lyr.json`, and `app.New(lyr.WithCache(cache.NewMemory(…)))` stores its response and serves it again without rendering (`X-Lyr-Cache: HIT`, about 0.15 µs and no allocation), with stale-while-revalidate, tag and path purges, automatic `ETag`s and 304s. Unlike Astro, your middleware runs on every HIT, and a `lyr.Cache` field in your `Locals` lets it `Disable()` caching for signed-in visitors before the lookup. GET `server:defer` islands can be cached too. CDN providers write headers instead (`lyr.CacheHeaderWriter`). Off in `lyr dev`.
- `site.TrailingSlash(next)` sends the trailing-slash 308 for a page URL before your middleware runs, as Astro does, so auth gates and sessions only see the canonical form. Same rule as `site.Middleware`'s redirect (pages only); one byte check and no allocation on canonical URLs. `lyr new`'s `main.go` uses it.
- `github.com/orbit-z/lyr/cache`: the in-memory route cache provider (`cache.NewMemory`, with byte bounds and a hook to forward purges to other instances) and `cache.Store[V]`, a data and fragment cache with single-flight `Load`.

**Styles and scripts**
- Scoped CSS, global styles and Tailwind CSS v4 (through its standalone CLI, no Node).
- Vanilla `<script>` blocks in TypeScript, bundled with esbuild inside the CLI, with shared chunks and `import()`. `client:load`, `client:idle`, `client:visible` and `client:media` load component scripts lazily. No framework islands.
- Typed server→client data: `lyr.JSON` attributes and `set:json`, read in scripts with types generated from Go.

**Tools**
- `lyr new`, `lyr dev` (rebuild and live reload in about 0.2 s on a 200-file project), `lyr build` (with `--embed` for a single binary), `lyr check` (TypeScript checking with Bun), `lyr fmt` and `lyr doctor`.
- `lyr lsp`: completion, diagnostics, hover, go-to-definition, references and rename in `.lyr` files, through gopls, and TypeScript in `<script>` blocks. A VS Code extension (`orbit-z.lyr`) runs it.
- `lyrtest` helpers for testing pages and components with `go test`.
- `lyr new` scaffolds a Dockerfile for a distroless single-binary image.

**Performance**, measured against Astro on the same 500-card page: about 14.6× the pages per CPU, and 57× at all cores against Astro's single Node process. See the [performance page](docs/performance.md#results) for the methodology and every result.
