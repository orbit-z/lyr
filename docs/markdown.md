# Markdown

Orbit renders Markdown files the way Astro does: a `.md` file in `src/pages/` is a page, and an `.orbit` component can import a `.md` file and render it as `<Post />`. The difference is *when* it renders. Orbit converts the Markdown to HTML (and highlights its code blocks) **when it generates code**, not per request. The HTML ends up as string constants in the generated Go, the same as a template's static HTML, so serving a Markdown page costs the same as serving a static template, and your server binary never parses Markdown.

A Markdown page with a layout:

````md file=src/pages/blog/hello.md
---
title: Hello, Orbit
date: 2026-10-26
tags: [news, go]
layout: ../../layouts/Post.orbit
---

Orbit compiles this file to Go when it generates code, so serving it costs
the same as serving a template's static HTML.

## Why Markdown

Write prose in **Markdown**, and keep the page's markup in an `.orbit` layout.

```go
fmt.Println("highlighted when Orbit generates code")
```

## What's next

Read the [routing guide](./routing-guide.md).
````

The layout is an ordinary `.orbit` component. Its `Props` receive the frontmatter, and its default slot receives the rendered Markdown:

```orbit file=src/layouts/Post.orbit
---
import "time"

type Props struct {
	Title    string
	Date     time.Time
	Tags     []string
	Headings []orbit.Heading // filled by Orbit for Markdown pages
}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{props.Title}</title>
  </head>
  <body>
    <article class="prose">
      <h1>{props.Title}</h1>
      <p><time datetime={props.Date.Format("2006-01-02")}>{props.Date.Format("January 2, 2006")}</time></p>
      {if len(props.Headings) > 0}
        <nav class="toc">
          <ul>
            {for _, h := range props.Headings}
              {if h.Depth == 2}
                <li><a href={"#" + h.Slug}>{h.Text}</a></li>
              {/if}
            {/for}
          </ul>
        </nav>
      {/if}
      <slot />
    </article>
  </body>
</html>

<style>
  .prose {
    max-width: 40rem;
    margin: 2rem auto;
    line-height: 1.6;
  }
  .prose :global(h2) {
    margin-top: 2.5rem;
  }
  .prose :global(pre) {
    padding: 1rem;
    border-radius: 6px;
    overflow-x: auto;
  }
</style>
```

and the page it links to:

```md file=src/pages/blog/routing-guide.md
---
title: File-based routing
date: 2026-10-27
layout: ../../layouts/Post.orbit
---

Every file in `src/pages/` is a route.
```

`/blog/hello` now renders the layout with `Title` set to `"Hello, Orbit"`, the date and tags, a table of contents built from the headings, and the Markdown's HTML where the layout has `<slot />`.

## Markdown pages

A Markdown file under `src/pages/` is a route, with the same rules as an `.orbit` page (see [Routing](routing.md)):

| File | URL |
|---|---|
| `src/pages/about.md` | `/about` |
| `src/pages/blog/index.md` | `/blog` |
| `src/pages/blog/hello.md` | `/blog/hello` |
| `src/pages/404.md` | none: the not-found page |
| `src/pages/_drafts/next.md` | none: a `_` prefix keeps it out of the routes |

As in Astro, the extensions `.md`, `.markdown`, `.mdown`, `.mkdn`, `.mkd` and `.mdwn` are all Markdown. The examples on this page use `.md`.

- **Collisions**: an `.md` and an `.orbit` file with the same route are a build error that names both files, as two `.orbit` files are: `src/pages/about.orbit: route /about is already defined by src/pages/about.md; remove or rename one of them`. The same goes for `about.md` next to `about/index.orbit`.
- **No route params**: a Markdown page has no frontmatter code to read `params` with, so a bracket in its name is a build error at the file: `src/pages/blog/[slug].md: a Markdown page can't have route params; rename it, or write a [slug].orbit page`.
- **Every method**: like any page, a Markdown page answers every HTTP method with the same HTML, renders per request (through your middleware, with your `locals`), and gets its stylesheet injected before the layout's `</head>`.

A Markdown page **without a layout** renders only the Markdown's HTML, with no `<html>`, `<head>` or `<body>`, as in Astro. You almost always want a layout.

## Frontmatter

The frontmatter is **YAML** between `---` lines at the very top of the file, as in Astro. It's optional.

```yaml
---
title: Hello, Orbit          # a string; quotes are optional
date: 2026-10-26             # a date
draft: false                 # a boolean
order: 3                     # a number
tags: [news, go]             # a list, or one "- item" per line
author:                      # a nested map
  name: Ada
  url: https://example.com/ada
layout: ../../layouts/Post.orbit
---
```

`layout` is special: it's the path of an `.orbit` layout, relative to the Markdown file, and it must stay inside `src/`. Orbit uses it to compile the page and doesn't pass it on. Every other key goes to the layout (see [Layouts](#layouts)) and to files that import this one (see [`Post.Frontmatter`](#postfrontmatter)).

Keys become Go field names with the same rules as props and route params: `title` → `Title`, `post-id` and `postId` → `PostID`, `url` → `URL`. So a key must start with a letter and use only letters, digits, `-` and `_`, and two keys that become the same field are an error. The keys of nested maps follow the same rules.

The frontmatter must be keys and values. YAML anchors (`&name`), aliases (`*name`) and tags (`!!str`) aren't supported, and are errors at their position: repeat the value instead.

Values are checked when Orbit generates code, and errors point at the line and column in the `.md` file:

```text
src/pages/blog/hello.md:4:8: frontmatter draft: "yes" isn't a bool (Post.Props.Draft is bool)
src/pages/blog/hello.md:2:1: unknown frontmatter key "tilte" for layout Post (props: title, date, tags, headings)
src/pages/blog/hello.md:6:9: layout ../layouts/Post.orbit: no such file
```

A key with no value (`subtitle:`) is an error too: delete the line or give it a value.

## Layouts

The `layout` of a Markdown page is an ordinary `.orbit` component, so you can also use it from `.orbit` pages (`<Post title="Hi">…</Post>`). For a Markdown page, Orbit calls it as if you had written:

```orbit
<Post title="Hello, Orbit" date={…} tags={[]string{"news", "go"}} headings={…}>
  …the Markdown's HTML…
</Post>
```

- **Frontmatter → props**: each key sets the `Props` field of the same name, matched as attributes are (ignoring case, `-` and `_`). A key that matches no field is an error listing the props, like an [unknown prop](components.md#unknown-props), so a typo such as `tilte` doesn't silently disappear.
- **Types**: each value is converted to the field's Go type when Orbit generates code:

  | Field type | Accepted values |
  |---|---|
  | `string` (and named string types) | any scalar, as written: `3` gives `"3"`, `2026-10-26` gives `"2026-10-26"` |
  | `bool` | `true`, `false` |
  | ints, uints, floats | numbers that fit the type |
  | `time.Time` | a YAML date (`2026-10-26`, UTC midnight) or timestamp (`2026-10-26T09:30:00+02:00`) |
  | `orbit.HTML` | a string, written as raw HTML (you wrote it, as with a string constant in `set:html`) |
  | `[]T` | a list of values accepted by `T` |
  | `map[string]T` | a map whose values are accepted by `T` |
  | a struct | a map whose keys match its exported fields, by the same rules |

  Any other field type (`any`, pointers, interfaces, funcs) can't be set from frontmatter, and a key for it is an error at the key.
- **The content** is the layout's **default slot**. Named slots stay empty.
- **Headings**: if the layout's `Props` has a field `Headings []orbit.Heading`, Orbit fills it with the page's headings (see [Heading IDs](#heading-ids)), so the layout can build a table of contents. A frontmatter key `headings` is then an error.
- **Fields the frontmatter doesn't set** keep their zero value, as unset props do.

The frontmatter values are built once, when your server starts, and every request passes the same `Props` to the layout. So a Markdown page renders with no extra allocations, but treat slices and maps from frontmatter as read-only in your layout.

`orbit.Heading` is a small struct in the runtime package, named after Astro's `MarkdownHeading`:

```go
type Heading struct {
	Depth int    // 1 for <h1>, … 6 for <h6>
	Slug  string // the heading's id
	Text  string // its text, without markup
}
```

### Styling Markdown content

Scoped styles apply only to elements written in the component's own template (see [How scoping works](styles-and-scripts.md#how-scoping-works)). Markdown elements are written in the `.md` file, which has no styles of its own, so they carry no scope attribute and a layout's plain `h2 { … }` rule doesn't reach them. This is the same rule as for slot content, and the same as in Astro. Style Markdown with `:global(…)` under an element of the layout, as the `Post` layout above does with `.prose :global(h2)`, or with a `<style is:global>` block.

## Importing Markdown into a component

Any `.orbit` file can import a Markdown file with an alias, like a component:

```orbit file=src/pages/blog/index.orbit
---
import (
	Base "../../layouts/Base.orbit"
	Hello "./hello.md"
	Routing "./routing-guide.md"
)

type entry struct {
	Title string
	URL   string
}

posts := []entry{
	{Hello.Frontmatter.Title, Hello.URL},
	{Routing.Frontmatter.Title, Routing.URL},
}
---
<Base title="Blog">
  <ul>
    {for _, p := range posts}
      <li><a href={p.URL}>{p.Title}</a></li>
    {/for}
  </ul>
</Base>
```

The same rules apply as for `.orbit` imports: the alias is required and starts with an uppercase letter, the path is relative and stays inside `src/`, and an import that's never used is an error. The import gives you:

| Name | What it is |
|---|---|
| `<Hello />` | The rendered Markdown: its HTML, inside its `layout` if it has one. It takes no attributes or children. |
| `Hello.Frontmatter` | The frontmatter values, as a struct (below). |
| `Hello.Headings` | Its headings, a `[]orbit.Heading`, for a table of contents. |
| `Hello.URL` | For a Markdown page, its canonical URL path (`"/blog/hello"`, or `"/blog/hello/"` with `"trailingSlash": "always"`). Only Markdown pages have it. |

A Markdown file outside `src/pages/` isn't a route; it's compiled only when something imports it. Keep such files anywhere under `src/`, for example `src/content/`, and render them inside a page:

```orbit
---
import (
	Intro "../content/intro.md"
	Base "../layouts/Base.orbit"
)
---
<Base title="Welcome">
  <div class="prose"><Intro /></div>
</Base>
```

### `Post.Frontmatter`

`Frontmatter` is a Go struct generated from the YAML values, so `Hello.Frontmatter.Title` is checked by the compiler and completed by your editor. Its field types follow the values:

| YAML value | Go type |
|---|---|
| `Hello, Orbit`, `"3"` | `string` |
| `true`, `false` | `bool` |
| `3` | `int` |
| `3.5` | `float64` |
| `2026-10-26`, `2026-10-26T09:30:00Z` | `time.Time` |
| `[news, go]` | `[]string` (a list's items must all have the same type) |
| `[]` | `[]string` |
| a nested map | a nested struct |

Two Markdown files with the same keys and value types have the same `Frontmatter` type, so you can put them in one slice. The `layout` key isn't in it.

## What you can write

Markdown is [CommonMark](https://commonmark.org) plus, as in Astro, **GitHub Flavored Markdown**: tables, task lists (`- [x] done`), strikethrough (`~~old~~`), footnotes and bare URLs turned into links. **Smart punctuation** is on too, as in Astro: straight quotes become curly, `--` becomes an em dash (—) and `...` an ellipsis (text in backticks and code blocks is never changed). As in Astro, only two hyphens make a dash: `-`, `---` and longer runs stay as written. Both can be turned off in `orbit.json` (see [Options](#options)).

There are no expressions: `{` and `}` are plain text, and there's no frontmatter code. For data, use an `.orbit` page that imports the Markdown.

### Raw HTML

HTML in Markdown is passed through, as in Astro:

```md
<details>
<summary>Why Go?</summary>

Fast compiles, one binary, and a type checker for your templates.
</details>
```

Markdown files are part of your project: Orbit reads them from `src/` when it generates code, and nothing at runtime can render Markdown through this feature. So raw HTML in them is as trusted as your templates, and like static values in a template, links are left as written (Orbit's `javascript:` filter applies to values computed at runtime). To render Markdown that users submit, use a Markdown library in your own Go code with raw HTML turned off (or a sanitizer), and pass the result to `set:html` as `orbit.HTML` (see [Raw HTML with `orbit.HTML`](components.md#raw-html-with-orbithtml)).

A `<script>` or `<style>` in Markdown is left exactly as written: it isn't bundled, processed or scoped, like an `is:inline` one. A capitalized tag such as `<Card />` is HTML too, not a component; components in Markdown are MDX, which Orbit doesn't support.

### Heading IDs

Every heading gets an `id`, made as GitHub and Astro make them (github-slugger): lowercase, punctuation removed except `-` and `_`, spaces turned into `-`, and `-1`, `-2`, … appended to repeats. `## Why Markdown` becomes `<h2 id="why-markdown">`, so `[see above](#why-markdown)` links to it. Orbit doesn't add `#` self-links next to headings; add them with CSS and a small script if you want them.

The same headings, with `Depth`, `Slug` and `Text`, are in a layout's `Headings` field and in an import's `Headings`.

### Links between Markdown files

A relative link to another Markdown **page** is rewritten to that page's URL, so links work both on GitHub and on your site:

| In `src/pages/blog/hello.md` | Renders as |
|---|---|
| `[routing](./routing-guide.md)` | `<a href="/blog/routing-guide">` |
| `[home](../index.md)` | `<a href="/">` |
| `[install](../docs/install.md#requirements)` | `<a href="/docs/install#requirements">` |

The URL is the page's canonical URL, so it follows `orbit.json`'s [`trailingSlash`](configuration.md#trailingslash): with `"always"`, the links above render as `/blog/routing-guide/`, `/` and `/docs/install/#requirements`, and `Post.URL` is `/blog/hello/`. Changing the mode regenerates them.

The link is checked when Orbit generates code. A link to a Markdown file that doesn't exist, or that isn't a page, is an error at the link's `[`: `src/pages/blog/hello.md:21:10: link ./routing.md: no such file`, or `link ../content/intro.md: src/content/intro.md isn't a page, so it has no URL; link to a page, or import it into one`. A `#fragment` that no heading of the target has is a warning (it may be an `id` your layout writes): `src/pages/blog/hello.md:21:10: warning: link ./routing-guide.md#setup: src/pages/blog/routing-guide.md has no heading #setup`. Absolute links (`/about`), external URLs and links to other files are left as written.

Images use the URL you write. Orbit has no image pipeline yet, so put images in `public/` and link them from the root: `![A cat](/images/cat.png)`.

## Code blocks

Fenced code blocks with a language are **highlighted when Orbit generates code**, with [Chroma](https://github.com/alecthomas/chroma) (the highlighter Hugo uses), so no highlighting script runs in the browser:

````md
```go
func main() { fmt.Println("hi") }
```
````

renders as a `<pre class="chroma" tabindex="0" data-language="go"><code>…</code></pre>` whose tokens are `<span>`s with Chroma's class names (`<span class="kd">func</span>`). ```` ```orbit ```` blocks are highlighted too, by Orbit's own lexer: the frontmatter as Go, the template as HTML with its `{…}` expressions as Go and its control tags (`{if …}`, `{for …}`, `{/if}`, …) as keywords, component tags (`<Card>`) apart from HTML tags, `<script>` as TypeScript and `<style>` as CSS. A block whose language Chroma doesn't know (`data-language="mermaid"`), or that has none (`data-language="plaintext"`, indented code blocks included), is escaped plain text in the same `<pre>`. Anything after the language on the fence line (```` ```go title="main.go" ````) is ignored, as in Astro.

The token colors are CSS that Orbit adds to the stylesheet of every page that renders highlighted code, before your own styles, so yours win. By default it uses Chroma's `github` theme in light mode and `github-dark` in dark mode, with the CSS `light-dark()` function, so the colors follow your page's [`color-scheme`](https://developer.mozilla.org/docs/Web/CSS/color-scheme):

```css
:root { color-scheme: light dark; }                  /* follow the system setting */
:root[data-theme="dark"] { color-scheme: dark; }     /* or a theme toggle */
:root[data-theme="light"] { color-scheme: light; }
```

Without a `color-scheme` on the page, code is shown in the light theme. A theme that sets no text or background color of its own (`github` has no text color) uses the page's `CanvasText` and `Canvas` system colors for it. Pick other themes, or turn the CSS off to write your own, in `orbit.json` (see [Options](#options)). Chroma's themes are listed in its [style gallery](https://xyproto.github.io/splash/docs/).

## Options

Markdown has one `orbit.json` key, `markdown`, with Astro's option names. Every field is optional; these are the defaults:

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

- `gfm`: GitHub Flavored Markdown (tables, task lists, strikethrough, footnotes, bare URLs).
- `smartypants`: smart punctuation.
- `syntaxHighlight`: `false` turns highlighting off, leaving `<pre><code class="language-go">` for a client-side highlighter. `themes` names Chroma themes; with `"dark": null`, the light theme is used everywhere. `"css": false` keeps the token classes but adds no CSS, for writing your own.

An unknown theme name is an error that lists the available ones. Changing an option regenerates every Markdown file.

## What isn't supported

- **Expressions and components in Markdown** (MDX). Markdown is static; put dynamic parts in an `.orbit` page that imports it.
- **Content collections** (typed sets of Markdown files with a schema, `getCollection`) and **glob imports** (`import.meta.glob`): import each file by name. A Markdown page can't be dynamic (`[slug].md`).
- **remark and rehype plugins**: the Markdown features are fixed, apart from the [options](#options).
- **Image optimization**: images aren't processed (see [Links](#links-between-markdown-files)).
- **Markdown outside `src/`**: imports must stay inside `src/`. If your server reads Markdown files itself at runtime, list them in `orbit.json`'s `watch` option so `orbit dev` reloads on changes (see [Configuration](configuration.md)).

## In the dev server

`orbit dev` watches `.md` files under `src/` like `.orbit` files. Saving one regenerates just that file's Go package and rebuilds your server, so it shows up in about the time of a template edit (about 0.2 s, see [Dev server](dev-server.md#how-long-a-rebuild-takes)). Changing a page's frontmatter or headings also rebuilds the components that import it. Errors (bad frontmatter, a missing layout, a broken link) show in the error overlay with their `.md` position.

In your editor, the Orbit language server reports the same errors and warnings in `.md` files as you type: frontmatter problems, a missing layout, broken links, route errors. A frontmatter value of the wrong type for its layout field (`draft: "yes"` for a `bool`) needs your Go types, so it appears once the language server's type check has run: when the editor starts, and after `orbit dev` or `orbit generate` rewrites `orbit_gen/`; editing that key hides it until then, and `orbit dev` shows it right away. Everything else about `.md` files (highlighting, preview) is your editor's own Markdown support. In `.orbit` files, completion works on imports: `import Hello "./` completes `.md` paths, and `Hello.Frontmatter.` completes the frontmatter's fields. See [Editor setup](editor-setup.md#markdown-files).

## Performance

- **At request time, nothing.** A Markdown page is a string constant written into the response buffer, plus its layout's render. The frontmatter props are built once at startup, so the page adds no allocations.
- **When generating code, a few milliseconds.** Orbit's own docs (23 files, 560 KB of Markdown with about 420 code blocks) convert in about 10 ms, and highlighting adds about 22 ms (AMD Ryzen 7 9700X, one thread); `orbit generate` converts files in parallel, about 11 ms for all of them on 8 cores. A typical page takes 1–2 ms, a small part of a rebuild, which is dominated by `go build`.
- **Your server binary** grows only by the HTML of your pages. The Markdown and highlighting libraries are part of the `orbit` CLI, not of your server.

## See also

- [Routing](routing.md): how file paths become URLs, and route collisions
- [Components](components.md): layouts, props and slots
- [Styles and scripts](styles-and-scripts.md): scoping and `:global(…)`
- [Coming from Astro](coming-from-astro.md): Markdown in Astro and in Orbit
