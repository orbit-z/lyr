# Coming from Astro

If you know Astro, you already know most of Orbit; this page maps your Astro knowledge to Orbit, shows what's different, and lists what doesn't exist and why.

Orbit keeps Astro's component model and its file-based routing: a frontmatter fence, then a template with `{expressions}`, slots, scoped `<style>` and bundled `<script>` tags, with pages in `src/pages/`. What changes is the server side. The frontmatter is **Go**, not JavaScript, and Orbit compiles every `.orbit` file to Go, so your components, your Go packages and your `cmd/server/main.go` become **one server binary**. Orbit is **SSR-only**: every page renders per request, and there's no static build. The reasons: server rendering designed to do as little work per request as possible (precomputed static HTML, typed writes, no reflection; see [Performance](performance.md) for the benchmarks against Astro), the Go compiler checking your props and templates, and a single binary to deploy, with no Node.js at build time or at runtime.

## The same component, side by side

A card with props, an expression, a loop, a default and a named slot, and a scoped style. In Astro:

```astro
---
interface Props {
  title: string;
  tags: string[];
  featured?: boolean;
}

const { title, tags, featured = false } = Astro.props;
const heading = title.toUpperCase();
---
<article class:list={["card", { featured }]}>
  <h2>{heading}</h2>
  {tags.length > 0 && (
    <ul>
      {tags.map((tag) => <li>{tag}</li>)}
    </ul>
  )}
  <slot />
  {Astro.slots.has("footer") && <footer><slot name="footer" /></footer>}
</article>

<style>
  .card { border: 1px solid #ccc; padding: 1rem; }
  .featured { border-color: gold; }
  h2 { margin: 0; }
</style>
```

In Orbit:

```orbit file=src/components/Card.orbit
---
import "strings"

type Props struct {
	Title    string
	Tags     []string
	Featured bool
}

heading := strings.ToUpper(props.Title)
---
<article class:list={"card", "featured": props.Featured}>
  <h2>{heading}</h2>
  {if len(props.Tags) > 0}
    <ul>
      {for _, tag := range props.Tags}
        <li>{tag}</li>
      {/for}
    </ul>
  {/if}
  <slot />
  {if slots.Has("footer")}
    <footer><slot name="footer" /></footer>
  {/if}
</article>

<style>
  .card {
    border: 1px solid #ccc;
    padding: 1rem;
  }
  .featured {
    border-color: gold;
  }
  h2 {
    margin: 0;
  }
</style>
```

What changed:

- `interface Props` became `type Props struct`, with exported (capitalized) fields, and `Astro.props` became `props`. There are no optional props: a field the parent doesn't set is Go's zero value, so `Featured` is simply `false`.
- `&&` and `.map()` became `{if}` and `{for}` blocks.
- `class:list`'s array and object became one list: `"featured": props.Featured` is the object's `{ featured }`.
- The template, the slots and the `<style>` are unchanged. The style is scoped exactly as in Astro.

Using it looks the same, apart from Go literals: `<Card title="Hello" tags={[]string{"go", "web"}} featured>` instead of `tags={["go", "web"]}`. Attribute names match the fields ignoring case, so `title` sets `Title`.

## How Astro maps to Orbit

### Components and frontmatter

| Astro | Orbit | Notes |
|---|---|---|
| `---` frontmatter in JS/TS | `---` frontmatter in Go | `import`, `type`, `const` and named `func` declarations are hoisted to package level, where they can't see `props`, `ctx`, `slots` or `locals`. Everything else runs on every render, in order. See [Components](components.md#the-frontmatter). |
| `import Card from "./Card.astro"` | `Card "./Card.orbit"` in the frontmatter's `import (…)` | The alias is required and must start with an uppercase letter; the path is relative to the file. Go packages go in the same block. See [Importing components](components.md#importing-components). |
| `interface Props { … }` | `type Props struct { … }` | Fields must be exported. No required or optional props: unset fields are zero values, so write defaults as `if title == "" { title = "Untitled" }`. See [Props](components.md#props). |
| `Astro.props` | `props` | A typed `Props` value. Values are type-checked by the Go compiler. |
| `<Card title="Hi" featured />` | The same | A quoted value is a Go string, so pass numbers as `count={3}`. `{post}` is shorthand for `post={post}`. |
| `<Astro.self />` | `<Self />` | Mutual recursion works too. Runaway recursion stops at a depth of 1000 with an error. See [Recursion](components.md#recursion-with-self). |
| `<slot />`, `<slot name="x">fallback</slot>`, `slot="x"` | The same | See [Slots](components.md#slots). |
| `Astro.slots.has("x")` | `slots.Has("x")` | `"default"` names the default slot. Whitespace-only children don't count. |
| `await Astro.slots.render("default")` | `orbit.RenderHTML(ctx, slots.Default)` | Returns `(orbit.HTML, error)`, written with `set:html`; `slots.Get("x")` for a named slot. Unlike Astro, a slot you don't render isn't built at all, so a fragment cache skips the work on a hit. See [Rendering a slot to a string](components.md#rendering-a-slot-to-a-string) and [Caching fragments](rendering.md#caching-fragments). |
| `Astro.slots.render("default", [item])` (a slot as a function) | No equivalent | Slots take no arguments. Pass the data as props to a component instead, or call a component's `Render` from Go code. |
| `<Fragment>` | `<Fragment>` | As in Astro, it takes only `slot`, `set:html` and `set:text`. See [`<Fragment>`](components.md#fragment). |

### Templates

| Astro | Orbit | Notes |
|---|---|---|
| `{expr}` | `{expr}` | Any Go expression, escaped. How each type prints (numbers, errors, `fmt.Stringer`, …) is in [How values render](reference/template-syntax.md#how-values-render). |
| `{cond && <p />}`, `{a ? <x /> : <y />}` | `{if cond}…{else}…{/if}` | Go has no ternary and no markup inside expressions. `{else if}` works too. See [Control flow](components.md#control-flow). |
| A chain of conditions on one value, or `switch` in the frontmatter | `{switch post.Status}{case "draft"}…{default}…{/switch}` | A Go `switch`: with a tag, an init statement, no tag, or a type switch. No fall-through. See [`{switch}`](reference/template-syntax.md#switch). |
| `{list.map((x) => <li>{x}</li>)}` | `{for _, x := range list}<li>{x}</li>{/for}` | Any Go `for` clause: `range`, three-clause, condition only. |
| `` class={`card ${extra}`} `` | `class={"card " + extra}` | A quoted value is literal text, with no interpolation. |
| `class:list={["a", extra, { b: on }]}` | `class:list={"a", extra, "b": on}` | One flat list, without the `[…]`: strings, `[]string` values (Astro's nested arrays) and `"class": condition` pairs (Astro's object form). Duplicates are kept, as in Astro, and whitespace is normalized; static classes are joined at build time. On a component, it sets the `class` prop, so `Props` needs a `Class string` field. See [`class:list`](reference/template-syntax.md#classlist). |
| `set:html={str}` | `set:html={orbit.HTML(str)}` | Only an `orbit.HTML` value (or a string constant) is accepted, so a plain `string` variable is a compile error: raw HTML is always an explicit choice. `set:text` is the same as in Astro. See [Raw HTML](components.md#raw-html-with-orbithtml). |
| No equivalent | `set:json={value}` on `<script type="application/json">` | New in Orbit: encodes a Go value with `encoding/json`, escaped so it can't break out of the script. Read it with `json(id)` from the component's module, typed from the Go value. See [JSON blocks](components.md#json-blocks-with-setjson). |
| `define:vars={{ … }}` | `data-post={orbit.JSON(post)}` (or plain `data-*` attributes, or `set:json`) | `define:vars` isn't supported (a compile error with a hint). Write the values into `data-*` attributes with `orbit.JSON`, and read them in a custom element with `data(this, "post")` from `import { data } from "./X.orbit"`: the script gets them with their Go types, checked by `orbit check`. See [Typed data with `orbit.JSON`](components.md#typed-data-with-orbitjson). |

### Styles and scripts

| Astro | Orbit | Notes |
|---|---|---|
| Scoped `<style>` | The same | Orbit adds a `data-o-…` attribute to the component's elements and `[data-o-…]` to selectors, like Astro's default `scopedStyleStrategy: "attribute"` (+1 attribute specificity). There's no `scopedStyleStrategy` option. See [How scoping works](styles-and-scripts.md#how-scoping-works). |
| `:global(…)` | The same | See [`:global`](styles-and-scripts.md#escaping-the-scope-global). |
| `<style is:global>` | The same | See [`is:global`](styles-and-scripts.md#global-styles-isglobal). |
| `is:inline` | The same | On `<script>` and `<style>`. The directive is removed and the rest of the tag is left as written. |
| `<script>`, processed and bundled, TypeScript by default | The same rule | A script with no attributes, or only a local `src`, is processed; any other attribute leaves it as-is. Bundled per page by esbuild inside the CLI (no Node.js), with npm imports from `node_modules`. See [Which scripts Orbit processes](styles-and-scripts.md#which-scripts-orbit-processes). |
| Tailwind CSS v4 through `@tailwindcss/vite` | Built in, through Tailwind's CLI | No integration to install. See [Tailwind CSS](#tailwind-css) below. |

### Tailwind CSS

An Astro site on Tailwind v4 moves over almost as is. To port it:

1. Remove `@tailwindcss/vite` from `astro.config` and `package.json`. Orbit needs nothing in its place but the Tailwind CLI: the standalone binary, `brew install tailwindcss`, or `@tailwindcss/cli` in `package.json` (see [Install the Tailwind CLI](tailwind.md#install-the-tailwind-cli)).
2. Keep `src/styles/global.css` as it is: `@import "tailwindcss"`, `@theme`, `@utility`, `@custom-variant`, `@plugin` and `@source` work the same.
3. Move `import "../styles/global.css"` from the layout's frontmatter (which is Go in Orbit) to a global style block in the layout: `<style is:global> @import "../styles/global.css"; </style>`.
4. Fonts and other CSS that `global.css` imports (`@import "@fontsource-variable/figtree"`) keep working: Orbit inlines them and rewrites their `url()`s, as `@tailwindcss/vite` does. Importing them from a script or the layout's style block works too.
5. `@reference "../styles/global.css"` with `@apply` in a component's `<style>` works the same, scoped the same way. `class:list` and classes in Go strings are found by Tailwind's scan, like classes in `.astro` files.

See [Using Tailwind](tailwind.md).

### Routing and the request

| Astro | Orbit | Notes |
|---|---|---|
| `src/pages/about.astro` → `/about` | `src/pages/about.orbit` → `/about` | `index.orbit` is its directory's root. Only `.orbit` and `.md` files are pages, and as in Astro, a `_` prefix on a file or directory (`_parts/Byline.orbit`) keeps it out of the routes. See [Routing](routing.md#file-based-routes). |
| `[slug].astro` | `[slug].orbit` | A parameter must be a whole segment: `post-[id].orbit` is a build error for now. See [Dynamic routes](routing.md#dynamic-routes-with-param). |
| `[...path].astro` | `[...path].orbit` | Must be the last segment. With zero segments the value is `""`, not `undefined`. See [Catch-all routes](routing.md#catch-all-routes-with-rest). |
| `Astro.params.slug` | `params.Slug` | Typed: Orbit generates a `Params` struct per page from its file path, so `params.Slg` is a compile error at its line and your editor completes `params.`. Values are percent-decoded strings. Components read `ctx.Param("slug")`. See [Route params](routing.md#route-params-params). |
| `404.astro` | `404.orbit` | A custom `500` page isn't supported yet: production errors get a built-in 500 page and a log line, unless an [error handler](rendering.md#answering-errors-yourself-witherrorhandler) answers them. See [The 404 page](routing.md#the-404-page). |
| `Astro.request` | `ctx.Request` | A standard `*http.Request`: method, headers, cookies (`ctx.Request.Cookie("theme")`), form. See [The request](rendering.md#the-request-ctx). |
| `Astro.url` | `ctx.URL` | The query string is `ctx.URL.Query().Get("q")`. |
| `Astro.site` | `ctx.Site` | From `site` in `orbit.json`; `nil` when unset. |
| `return Astro.redirect("/login")` | `return ctx.Redirect("/login", 302)` | The status code is required. See [Redirects](rendering.md#redirects). |
| `Astro.response.status = 422` | `ctx.SetStatus(422)` | For a not-found page, `return orbit.ErrNotFound`, which renders `404.orbit`. See [Status codes](rendering.md#status-codes-and-errors). |
| `Astro.response.headers.set(…)` | `ctx.Header().Set(…)` | An `http.Header`. See [Response headers](rendering.md#response-headers). |
| `trailingSlash: 'never'`, `'always'` | `"trailingSlash": "never"` (default), `"always"` in `orbit.json` | Astro's default, `'ignore'`, has no equivalent: each Orbit page has one URL, so pick one of the two. Orbit redirects only when the other form of the path is a page (Astro redirects every path, then 404s), always with a 308 (Astro sends 301 for `GET`), and the same in `orbit dev` (Astro's dev server shows a 404 hint page instead). A page with an extension, such as `rss.xml.orbit`, keeps `/rss.xml` in both modes, like an Astro endpoint. See [Trailing slashes](routing.md#trailing-slashes). |

### Middleware, locals and endpoints

| Astro | Orbit | Notes |
|---|---|---|
| `Astro.locals` | `locals` | A typed `*locals.Locals`, checked by the compiler and completed by your editor. |
| `App.Locals` in `src/env.d.ts` | `type Locals struct` in the Go package `src/locals` | Orbit's `src/env.d.ts` is only for your client scripts' global types. See [Typed locals](middleware.md#typed-locals). |
| `onRequest` in `src/middleware.ts` | Plain `net/http` middleware in `cmd/server/main.go` | A `func(http.Handler) http.Handler` that sets locals with `r, l := orbit.EnsureLocals[locals.Locals](r)`. There's no Orbit-specific middleware type. See [Middleware and locals](middleware.md). |
| `sequence(a, b)` | `a(b(mux))` | The middleware listed first runs first. |
| Returning a `Response` from middleware | Write the response and don't call `next` | See [Short-circuit responses](middleware.md#short-circuit-responses). |
| `try { return await next() } catch (e) { … }` in middleware | `app.New(orbit.WithErrorHandler(answerError))` | Orbit answers a page's error before `next` returns, so middleware can't catch it. The handler gets every error that would become the 500 (from the page, its components, deferred islands, and panics), before anything is written, and returns `false` for the ones it leaves to Orbit. See [Answering errors yourself](rendering.md#answering-errors-yourself-witherrorhandler). |
| Endpoints: `export const GET` in `src/pages/api/posts.ts` | A handler in `cmd/server/main.go`: `mux.HandleFunc("GET /api/posts", listPosts)` | There are no endpoint files. Your `main.go` decides whether your routes or the pages win. For HTML fragments (htmx), use `orbit.RenderComponent`. Pages also receive every method, so a page can handle its own form. See [Server](server.md#choosing-precedence). |

### Markdown

| Astro | Orbit | Notes |
|---|---|---|
| `src/pages/about.md` → `/about` | The same | Rendered to HTML when Orbit generates code, so a Markdown page costs nothing extra per request. An `.md` and an `.orbit` page with the same route are a build error. See [Markdown pages](markdown.md#markdown-pages). |
| YAML frontmatter | The same | Values are checked when Orbit generates code, with errors at their `.md` line. See [Frontmatter](markdown.md#frontmatter). |
| `layout: ../layouts/Post.astro`, `Astro.props.frontmatter.title` | `layout: ../layouts/Post.orbit`, `props.Title` | Each frontmatter key sets the layout's `Props` field of the same name, type-checked; an unknown key is an error. The content is the default slot. See [Layouts](markdown.md#layouts). |
| `Astro.props.headings` in a layout | A `Headings []orbit.Heading` field in the layout's `Props` | `orbit.Heading` has `Depth`, `Slug` and `Text`, like Astro's `MarkdownHeading`. |
| `import * as post from "./post.md"`, `<post.Content />` | `Post "./post.md"` in the `import (…)` block, `<Post />` | `post.frontmatter.title` is `Post.Frontmatter.Title` (a generated struct), `post.getHeadings()` is `Post.Headings`, `post.url` is `Post.URL`. See [Importing Markdown](markdown.md#importing-markdown-into-a-component). |
| `import.meta.glob("./posts/*.md")` | Not supported | Import each file by name. |
| `markdown.gfm`, `markdown.smartypants` | The same keys under `markdown` in `orbit.json` | Both on by default, as in Astro. See [Options](markdown.md#options). |
| Shiki (`markdown.shikiConfig`) | Chroma, at generate time (`markdown.syntaxHighlight`) | Class-based token colors with light and dark themes; the CSS is added to the page's stylesheet. See [Code blocks](markdown.md#code-blocks). |
| Heading ids (github-slugger) | The same | |
| remark/rehype plugins, MDX | Not supported | Markdown has no expressions or components; put dynamic parts in an `.orbit` page that imports it. |

### Lazy loading

| Astro | Orbit | Notes |
|---|---|---|
| `server:defer` with `slot="fallback"` | The same | As in Astro, props are encrypted with a key that each build embeds, `ctx.URL` is the island request and the page URL is in `ctx.Request.Referer()`. Different in Orbit: only the `fallback` slot can be passed; every `Props` field must be JSON-serializable, checked when Orbit generates code; tokens are deterministic (same props, same URL) and bound to the component and the build ID; and the loader switches to `POST` above 8000 characters, not 2048. See [How props are protected](lazy-loading.md#how-props-are-protected). |
| `ASTRO_KEY`, `astro create-key` | `ORBIT_KEY`, `orbit create-key` | Needed for rolling deploys. See [The key](lazy-loading.md#the-key-orbit_key-and-rolling-deploys). |
| `client:load`, `client:idle`, `client:visible`, `client:media` | The same names, on `.orbit` component tags | Astro uses them to hydrate framework components. In Orbit they delay the vanilla `<script>`s of the component **and of its whole subtree**; the HTML is always rendered on the server. If the component is also rendered eagerly on the page, its scripts stay eager. See [`client:*`](lazy-loading.md#client-lazy-scripts). |
| `client:only` | Not supported | It's for framework components, which Orbit doesn't have, and it's a compile error: `client:only is for framework components, which Orbit doesn't support; use client:load, client:idle, client:visible or client:media`. |

### Rendering modes

| Astro | Orbit | Notes |
|---|---|---|
| `output: "server"` | The only mode | Every page, `404.orbit` included, renders per request. See [Rendering](rendering.md#one-mode-server-rendering). |
| `output: "static"`, `getStaticPaths()`, `export const prerender = true` | Not supported | Orbit is SSR-only, by design. For a static site, Astro is the better tool. Files you want served as-is go in `public/`. |

### Build, config and tooling

| Astro | Orbit | Notes |
|---|---|---|
| `npm create astro@latest` | `orbit new blog --module example.com/blog` | See [Getting started](getting-started.md). |
| `astro dev` | `orbit dev` | On `localhost:4321`, with `--port` and `--host`. Full-page live reload, no HMR. See [Dev server](dev-server.md). |
| `astro build` | `orbit build` | Produces `dist/server` (your server binary) and `dist/client` (assets). No HTML files. |
| `astro preview` | `orbit preview` | Runs `dist/server`. |
| `astro check` | `orbit check` | Go through `go vet` on the generated code; TypeScript through `bun check` (Bun 1.4.4 or later). See [CLI](reference/cli.md#orbit-check). |
| `astro add tailwind` | Nothing to add | Tailwind v4 works out of the box once its CLI is installed. See [Using Tailwind](tailwind.md). |
| `astro add`, integrations, adapters | None | The output is a Go binary, not a Node app, so there's nothing to adapt. `orbit build --embed` makes it one self-contained file, and `orbit new` scaffolds a Dockerfile. See [Deploy](deploy.md). |
| Content collections, `astro:assets` images | Not supported yet | Import Markdown files one by one (see [Markdown](markdown.md)), or load content from your own Go package under `internal/` and render trusted HTML with `orbit.HTML`. |
| `astro.config.mjs` | `orbit.json` (optional) | Plain JSON: `site`, `server`, `embed`, `compressHTML`, `trailingSlash`, `sourcemap`, `watch` and `markdown`. No `base` yet. See [Configuration](configuration.md). |
| `compressHTML: 'jsx'`, `true`, `false` | `"compressHTML": "jsx"`, `"html"` (or `true`), `false` | The same three behaviors, and `true` is accepted as `"html"`. The default is `"jsx"`, as in Astro v7, with the same JSX rules: `{" "}` keeps a space across lines. Two edge cases differ: under `"jsx"`, Astro leaves text that's a direct child of a component (or `<Fragment>`) as written, while Orbit applies the JSX rules there too; and Orbit's `"html"` collapses every whitespace run and drops whitespace between block tags, where Astro's `true` keeps runs inside multi-line text and a space between blocks. See [Whitespace](reference/template-syntax.md#whitespace). |
| The Astro VS Code extension | The Orbit extension, `orbit-z.orbit` | On the Marketplace and Open VSX. It starts `orbit lsp`, which works with gopls for Go completion, diagnostics and rename in `.orbit` files (the frontmatter is Go), and with TypeScript 7 for TypeScript in `<script>` blocks, as Astro's does; `is:inline` scripts get JavaScript features, as in Astro. See [Editor setup](editor-setup.md). |

## Things that work differently

- **Types are checked at compile time.** A misspelled prop is an error at its attribute, listing the props that work: `src/pages/index.orbit:9:6: unknown prop "titel" on <Card> (props: title, tags, featured)`. A wrong value type, or a typo in `{props.Titel}`, is a Go compiler error at its `.orbit` line. See [Unknown props](components.md#unknown-props).
- **Route params are typed.** `Astro.params` is an untyped object; an Orbit page gets `params`, a struct generated from its file path (`[post-id]` becomes `params.PostID`). A misspelled param is caught at build time instead of rendering `undefined`. See [Route params](routing.md#route-params-params).
- **Pages don't take props.** There's no `getStaticPaths` to pass them, so declaring `type Props` in a page is a build error: `pages don't receive props; move this to a component or read params`. Pages read `params` and `ctx.Request`. See [Pages](components.md#pages).
- **Responses are buffered, never streamed.** A page is sent in one write once it has fully rendered, so one slow query delays the first byte. Move slow parts into a `server:defer` component. See [Responses are always buffered](rendering.md#responses-are-always-buffered).
- **URLs have no trailing slash by default.** `/blog/hello/` gets a 308 redirect to `/blog/hello`, keeping the query string; `"trailingSlash": "always"` is the mirror image. There's no `"ignore"` mode. See [Trailing slashes](routing.md#trailing-slashes).
- **No framework components, by design.** Importing a `.tsx`, `.jsx`, `.svelte` or `.vue` file from the frontmatter is a compile error. Client code is a vanilla `<script>`, best written as a custom element. See [No framework components](components.md#no-framework-components).
- **`.orbit` imports need an alias**, in Go import syntax: `Card "../components/Card.orbit"`, not `import Card from …`. An unused component import is an error, like an unused Go import: `component Card is imported but not used; remove the import`.
- **Errors point at your `.orbit` lines.** Orbit's own errors, Go compiler errors, esbuild and TypeScript errors all print as `src/…orbit:line:col: message`, with generated type names rewritten to your aliases (`Card.Props`). See [Error output](reference/cli.md#error-output).
- **Every element must be closed.** Orbit doesn't auto-close `<p>` or `<li>`; a missing closing tag is a parse error with its position. `<div />` works, as in Astro.
- **Your server is your code.** `cmd/server/main.go` is an ordinary Go `main`: add routes, middleware and other handlers there, with any `net/http` package. See [Server](server.md).

## See also

- [Getting started](getting-started.md): install Orbit and create a project
- [Components](components.md): frontmatter, props, slots and templates in depth
- [Template syntax](reference/template-syntax.md): the full grammar and every directive
- [Routing](routing.md) and [Rendering](rendering.md): pages, `ctx` and the request lifecycle
- [Middleware and locals](middleware.md): `net/http` middleware and typed `Locals`
- [Lazy loading](lazy-loading.md): `server:defer` and `client:*`, with their own Astro comparison
- [Styles and scripts](styles-and-scripts.md): scoping, bundling and TypeScript
- [Performance](performance.md): why Orbit's SSR is fast, and the benchmarks against Astro
