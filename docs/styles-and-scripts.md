# Styles and scripts

Orbit scopes each component's `<style>` to that component, bundles its `<script>` as TypeScript, and links one stylesheet and one script per page, with no Node.js required.

## A component with a style and a script

```orbit file=src/components/Counter.orbit
---
type Props struct {
    Label string
}
---
<counter-button>
  <button type="button">{props.Label}</button>
  <output>0</output>
</counter-button>

<style>
  counter-button { display: flex; gap: 0.5rem; align-items: center; }
  button { font-weight: bold; }
</style>

<script>
  class CounterButton extends HTMLElement {
    connectedCallback() {
      const output = this.querySelector("output")!;
      let count = 0;
      this.querySelector("button")!.addEventListener("click", () => {
        count++;
        output.textContent = String(count);
      });
    }
  }

  customElements.define("counter-button", CounterButton);
</script>
```

```orbit file=src/pages/index.orbit
---
import (
    Layout  "../layouts/Base.orbit"
    Counter "../components/Counter.orbit"
)
---
<Layout title="Counters">
  <Counter label="Apples" />
  <Counter label="Pears" />
</Layout>
```

When Orbit builds this page, it:

- takes the `<style>` and `<script>` out of the template, so they don't appear inside `<counter-button>`;
- rewrites the CSS so `button { … }` only matches buttons rendered by `Counter`;
- compiles the script as TypeScript, bundles and minifies it;
- adds one `<link rel="stylesheet">` and one `<script type="module">` to the page's `<head>`.

The script appears once on the page, even though `Counter` renders twice. The custom element handles that: `customElements.define` runs once, and the browser calls `connectedCallback` for every `<counter-button>`, including ones added to the page later. See [Custom elements](#custom-elements-the-recommended-pattern).

`<script>` and `<style>` contents are opaque to the template: `{…}` inside them is plain text, not a Go expression. To pass data from Go to a script, see [Passing data from the server to scripts](#passing-data-from-the-server-to-scripts).

## How scoping works

Every component with a scoped `<style>` gets a **scope ID**: `o-` plus the first 8 hex characters of the SHA-256 of its path relative to the project root, with `/` separators (`src/components/Counter.orbit` → `o-8a161775`). The ID only depends on that path, so it's the same on every machine and every OS.

Orbit then does two things:

1. It adds a `data-o-8a161775` attribute, after the element's other attributes, to every HTML element in the component's template. As in Astro, the document-metadata elements `<head>`, `<title>`, `<meta>`, `<link>`, `<base>` and `<noscript>` (and the legacy `<font>`, `<frame>`, `<frameset>`, `<noframes>`) don't get it, nor do `is:inline` `<script>`/`<style>` tags; `<html>` and `<body>` do.
2. It appends `:where([data-o-8a161775])` to the last part (the last compound selector) of each CSS selector.

So the `Counter` above renders as:

```html
<counter-button data-o-8a161775>
  <button type="button" data-o-8a161775>Apples</button>
  <output data-o-8a161775>0</output>
</counter-button>
```

and its CSS becomes (before minification):

```css
counter-button:where([data-o-8a161775]) { display: flex; gap: 0.5rem; align-items: center; }
button:where([data-o-8a161775]) { font-weight: bold; }
```

`:where(…)` adds no specificity, so `button` keeps the specificity of a type selector. Your selectors win and lose exactly as they would without scoping.

What follows from this:

- **Only the last compound is scoped.** In `.theme-dark .card`, `.card` must be in this component, but `.theme-dark` can be anywhere, for example on `<body>`.
- **Pseudo-elements stay at the end**, as in Astro: `p::first-line` becomes `p:where([data-o-8a161775])::first-line`.
- **At-rules**: rules inside `@media`, `@supports`, `@layer`, `@container`, `@scope`, `@starting-style` and `@document` are scoped too. The bodies of every other at-rule (`@keyframes`, `@font-face`, `@page`, `@property`, …) are left alone, since they contain no selectors. Keyframe names aren't scoped: they're global, as in plain CSS.
- **`html`, `body` and `:root` are never scoped**, as in Astro: `html { … }`, `body { … }` and `:root { --accent: … }` work in a scoped style, and in `body .card` only `.card` is scoped.
- **Nesting**: nested rules are scoped like top-level ones (`.card { .title { … } }` scopes both). A nested selector whose last part holds `&` (`&:hover`, `.dark &`) isn't scoped again, since `&` is the already scoped parent.
- Selector arguments of `:is()`, `:not()`, `:where()` and `:has()` aren't rewritten; the scope goes after them: `li:not(.done)` becomes `li:not(.done):where([data-o-8a161775])`.
- **Child components aren't affected.** A parent's scoped styles don't match the elements inside a child component's template, because those carry the child's scope attribute (or none).
- **Slot content belongs to the parent.** Markup you pass into a component's slot is compiled in the file where you write it, so it carries *that* file's scope attribute. Style it from the parent, not from the child. The same goes for the `fallback` of a [`server:defer`](lazy-loading.md) component.
- **No scoped style, no attributes.** A component without a scoped `<style>` gets no `data-o-…` attributes at all.

## Escaping the scope: `:global(…)`

Wrap part of a selector in `:global(…)` to leave that part unscoped:

```orbit file=src/components/Prose.orbit
<div class="prose">
  <slot />
</div>

<style>
  .prose :global(p) { line-height: 1.6; }
  .prose :global(a:hover) { text-decoration: underline; }
</style>
```

Here `.prose` must be this component's element, but `p` and `a:hover` match any paragraph or link inside it, including the ones passed in through the slot or rendered by child components. As in Astro, the scope goes on the nearest part of the selector that isn't inside `:global(…)`: the first rule becomes `.prose:where([data-o-…]) p`. A selector that is entirely `:global(…)` isn't scoped at all. A part that mixes `:global(…)` with other selectors (`.a:global(.b)`) counts as global, and the `:global(` wrapper is removed from the output (`.a.b`).

## Global styles: `is:global`

Add `is:global` to a `<style>` to skip scoping for the whole tag. This is typical in a layout:

```orbit file=src/layouts/Base.orbit
---
type Props struct {
    Title string
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
    <slot />
  </body>
</html>

<style is:global>
  body { font-family: system-ui, sans-serif; max-width: 40rem; margin: 0 auto; }
  a { color: #0b57d0; }
</style>
```

An `is:global` style is still taken out of the template, bundled and minified with the rest of the page's CSS; it just isn't rewritten. A component can have both a scoped `<style>` and an `is:global` one.

**Order in the page's stylesheet**: styles follow the import graph, the way modules are evaluated: a component's imports come first, in import order, then the component's own styles. So a page that imports `Base.orbit` and then `Card.orbit` gets the layout's styles, then the card's, then the page's own. Each component's styles appear once. Since scoped selectors keep their specificity, rely on specificity rather than on this order where you can.

## Which scripts Orbit processes

Orbit follows Astro's rule. A `<script>` is **processed** (compiled as TypeScript, bundled, deduplicated and linked from `<head>`) only if it has:

- no attributes at all, or
- only a `src` attribute that points at a local file with a relative path.

Any other attribute means the script is **left as-is**, exactly as if you had written `is:inline`:

| Tag | Processed? |
|---|---|
| `<script>…</script>` | yes |
| `<script src="./counter.ts"></script>` | yes |
| `<script is:inline>…</script>` | no |
| `<script type="module">…</script>` | no |
| `<script async src="./counter.ts"></script>` | no |
| `<script src="https://cdn.example.com/lib.js"></script>` | no |
| `<script type="application/ld+json">…</script>` | no |
| `<script type="application/json" id="…" set:json={…}></script>` | no |

So you don't need `type="module"`: processed scripts are always emitted as modules, and adding the attribute turns processing off.

A script that isn't processed stays in the template where you wrote it:

- It isn't compiled, so it must be plain JavaScript (or whatever its `type` says, such as JSON).
- It isn't bundled, so it can't `import` from your project or from npm (unless it's a module that imports from a full URL).
- It isn't deduplicated: it's part of the template, so a component rendered three times emits it three times.

Use this for JSON-LD, third-party snippets that must stay verbatim, and small scripts that must run before the page renders:

```orbit file=src/components/ThemeInit.orbit
<script is:inline>
  document.documentElement.dataset.theme =
    localStorage.getItem("theme") ?? "light";
</script>
```

As in Astro, Orbit removes the `is:inline` directive itself and leaves everything else in the tag untouched. `is:inline` also works on `<style>`: the style is emitted where it is, unscoped and unbundled.

## Bundling and deduplication per page

Each page gets its own bundle. Its contents are decided statically, from imports:

- the page's own processed scripts and styles, plus
- those of every component it imports, directly or through other components, including components it renders with `server:defer`.

A component's script and style are included **once per page**, however many times the component renders, and even if it only renders under an `{if}` that is false for this request. Like Astro, Orbit looks at which components a page *imports*, not at which ones actually render.

The bundle is built by esbuild, which runs inside the `orbit` CLI (no Node.js needed). For each page you get:

- one minified CSS file and one minified ES module, with content-hashed names under `/_orbit/`;
- a `<link rel="stylesheet">` and a `<script type="module">` inserted just before the page's first `</head>` (usually in your layout). Without a `</head>`, they go right after `<!DOCTYPE …>`, or at the very start.

A page whose bundle has no scripts gets no JS file and no `<script>` tag. A page that uses `server:defer` or `client:*` also gets a small inline loader script in its `<head>`; see [Lazy loading](lazy-loading.md).

Files that your CSS or scripts reference by URL, such as fonts and images in `url(…)` (`.woff`, `.woff2`, `.ttf`, `.otf`, `.eot`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.webp`, `.avif`, `.ico`, `.bmp`), are copied to `/_orbit/` with a hashed name, and the reference is rewritten to that URL. A relative `url(…)` in a `<style>` resolves from the `.orbit` file's directory.

Files under `/_orbit/` are served with `Cache-Control: public, max-age=31536000, immutable`, since a change produces a new name.

Each page's bundle is self-contained: code shared by several pages is repeated in each bundle. Splitting shared code into common chunks is not yet supported.

## Loading a component's scripts lazily: `client:*`

By default, a component's script is part of the page bundle and runs when the page loads. Put a `client:*` directive on a component tag to load its scripts later instead:

```orbit file=src/pages/about.orbit
---
import (
    Layout  "../layouts/Base.orbit"
    Counter "../components/Counter.orbit"
)
---
<Layout title="About">
  <p>A long page…</p>
  <Counter label="Visitors" client:visible />
</Layout>
```

Here `Counter` still renders on the server as usual, but its script loads only when the counter scrolls into view. The directives are `client:load` (the default, eager), `client:idle`, `client:visible` (with an optional `rootMargin`, `client:visible="200px"`) and `client:media="(max-width: 600px)"`. [Lazy loading](lazy-loading.md) covers each one in detail.

How it affects bundling:

- **A separate entry per component.** Each component that has a processed `<script>` and at least one lazy call site gets its own esbuild entry, built with the same settings into a hashed `/_orbit/*.js` file. The loader `import()`s it the first time an instance meets its strategy, and at most once per page.
- **The whole subtree.** The directive covers the component's own scripts and those of every component it renders, transitively.
- **Eager wins, per page.** If any call site of a component on the page loads it eagerly (no directive, or `client:load`), its scripts stay in the page bundle, and its lazy call sites on that page load nothing extra.
- **Styles are unaffected.** A lazy component's CSS stays in the page's stylesheet.
- **Shared code is duplicated.** A module imported both by a lazy entry and by the page bundle (for example `src/scripts/util.ts`) is included in both, and each copy has its own module state. Don't use module-level variables to share state between them.
- **The wrapper element.** A lazy call site is wrapped in an `<orbit-lazy>` element styled `display: contents`, so it doesn't affect layout, but it is in the DOM: a child selector such as `.list > .item` doesn't match across it.

## Custom elements: the recommended pattern

A processed script is a module that runs **once per page**. If it looks up elements with `document.querySelectorAll` when it runs, it only finds the elements present at that moment. That misses:

- elements inside a [`server:defer`](lazy-loading.md) component, whose HTML arrives after the page loads (the deferred component's scripts are part of the page bundle and run before its HTML arrives);
- elements added later by your own code or by a library such as htmx;
- for a `client:*` component, instances that arrive later inside a deferred island.

A custom element has none of these problems: the browser calls its `connectedCallback` for every instance, whenever it's added to the document. That's why the `Counter` above is written as one, and why Orbit recommends this pattern for every component script, and especially for lazy and deferred components.

## Importing TypeScript files

Processed scripts can import `.ts`, `.tsx` and `.js` files from anywhere in your project. Relative paths resolve from the `.orbit` file's directory:

```ts file=src/scripts/greet.ts
export function greet(name: string): string {
  return `Hello, ${name}!`;
}
```

```orbit file=src/components/Greeter.orbit
<greeter-button>
  <button type="button">Say hello</button>
</greeter-button>

<script>
  import { greet } from "../scripts/greet";

  class GreeterButton extends HTMLElement {
    connectedCallback() {
      const button = this.querySelector("button")!;
      button.addEventListener("click", () => {
        button.textContent = greet("Ada");
      });
    }
  }

  customElements.define("greeter-button", GreeterButton);
</script>
```

You can also point `src` at a local file instead of writing the code inline. The tag must have no other attributes:

```orbit file=src/components/Clock.orbit
<live-clock></live-clock>

<script src="../scripts/clock.ts"></script>
```

```ts file=src/scripts/clock.ts
class LiveClock extends HTMLElement {
  #timer = 0;

  connectedCallback() {
    const tick = () => { this.textContent = new Date().toLocaleTimeString(); };
    tick();
    this.#timer = setInterval(tick, 1000);
  }

  disconnectedCallback() {
    clearInterval(this.#timer);
  }
}

customElements.define("live-clock", LiveClock);
```

Scripts may import `.tsx` files too (esbuild applies your `tsconfig.json`'s `jsx` settings). That's different from a framework component: Orbit has no framework islands, and importing a `.tsx`, `.jsx`, `.svelte` or `.vue` file from a component's *frontmatter* is a compile error.

## Passing data from the server to scripts

`{…}` doesn't work inside `<script>`, and a processed script is bundled once per page, so it can't see one component's props anyway. Hand data from Go to the browser in one of two ways:

- **`data-*` attributes**, for data that belongs to one element;
- **`set:json`**, for a larger block of data, such as a list.

### `data-*` attributes and custom elements

Render the values into `data-*` attributes and read them with `el.dataset`. They're escaped like any other attribute. This pairs well with a custom element: the browser runs its `connectedCallback` for every instance on the page, including ones added later, and `this.dataset` holds that instance's data.

```orbit file=src/components/LikeButton.orbit
---
type Props struct {
    Slug  string
    Likes int
}
---
<like-button data-slug={props.Slug} data-likes={props.Likes}>
  <button type="button">♥ <span>{props.Likes}</span></button>
</like-button>

<script>
  class LikeButton extends HTMLElement {
    connectedCallback() {
      const slug = this.dataset.slug ?? "";
      let likes = Number(this.dataset.likes ?? 0);
      const count = this.querySelector("span")!;

      this.querySelector("button")!.addEventListener("click", async () => {
        const res = await fetch(`/blog/${slug}/like`, { method: "POST" });
        if (res.ok) {
          likes++;
          count.textContent = String(likes);
        }
      });
    }
  }

  customElements.define("like-button", LikeButton);
</script>
```

Attribute values are always strings in the browser: convert numbers and booleans yourself (`Number(…)`, `"x" in this.dataset`). A `bool` prop renders as a bare attribute when `true` and is left out when `false`.

### JSON blocks with `set:json`

For structured data, put `set:json={expr}` on an empty `<script type="application/json">` with an `id`. Orbit encodes the value with `encoding/json` (so `json` struct tags apply) and writes it as the script's content:

```orbit file=src/components/NavMenu.orbit
---
type link struct {
    Title string `json:"title"`
    URL   string `json:"url"`
}

links := []link{
    {Title: "Home", URL: "/"},
    {Title: "Archive", URL: "/blog"},
    {Title: "About", URL: "/about"},
}
---
<nav class="nav-menu"></nav>
<script type="application/json" id="nav-links" set:json={links}></script>

<script>
  type Link = { title: string; url: string };

  const links: Link[] = JSON.parse(document.getElementById("nav-links")!.textContent ?? "[]");
  const nav = document.querySelector(".nav-menu")!;
  for (const link of links) {
    const a = document.createElement("a");
    a.href = link.url;
    a.textContent = link.title;
    nav.append(a);
  }
</script>
```

This renders as:

```html
<script type="application/json" id="nav-links">[{"title":"Home","url":"/"},{"title":"Archive","url":"/blog"},{"title":"About","url":"/about"}]</script>
```

What you need to know:

- **It's safe for any value.** The JSON is escaped for a script's content: `<`, `>` and `&` become `\u003c`, `\u003e` and `\u0026`, and U+2028/U+2029 become `\u2028`/`\u2029`. A string containing `</script>` or `<!--` can't end the element early, and `JSON.parse` gives you back the original value.
- **Only on JSON scripts.** `set:json` is valid only on `<script type="application/json">`; anywhere else it's a compile error. Like any script with attributes, this one is never processed or bundled: it stays in the HTML where you wrote it.
- **One per id.** The script is part of the template, so a component rendered twice emits it twice. Use `set:json` in a component that renders once per page (a page or a layout), or give each instance its own `id`. For per-instance data, prefer `data-*` attributes.
- **Encoding errors fail the render.** If `encoding/json` can't encode the value (a channel, a func, a cyclic structure), the render fails with an error at the `.orbit` position of the `set:json`.

The TypeScript type of the parsed data (`Link` above) is yours to keep in sync with the Go type. Generating TypeScript types from your Go structs, with a typed helper to read them, is planned for after the first release.

## npm packages

Bare imports (`import dayjs from "dayjs"`) resolve from `node_modules` when your project has one. Install packages with any package manager, for example:

```sh
bun add dayjs
```

Then import them in a processed script:

```
<script>
  import dayjs from "dayjs";

  for (const el of document.querySelectorAll<HTMLTimeElement>("time[datetime]")) {
    el.textContent = dayjs(el.dateTime).format("D MMM YYYY");
  }
</script>
```

The package is bundled into each page that uses it. Nothing is loaded from a CDN at runtime.

If an import can't be resolved, the build fails with an error at the `.orbit` file, line and column of the import, like every other Orbit error:

```
src/components/Dates.orbit:2:21: Could not resolve "dayjs"
```

### Importing CSS from a script

A processed script can import a stylesheet, typically one that ships with an npm package:

```
<script>
  import "flatpickr/dist/flatpickr.css";
  import flatpickr from "flatpickr";

  flatpickr("input[type=date]");
</script>
```

The imported CSS joins the page's stylesheet, minified and hashed with the rest; esbuild bundles it as it does any CSS import, and Orbit links it from the same `<link rel="stylesheet">`. It isn't scoped: it applies to the whole page, as the library expects. A component whose scripts load lazily with `client:*` still has its imported CSS in the page's stylesheet, like its own `<style>`.

## TypeScript

Processed scripts are **TypeScript by default**, as in Astro. You don't need `lang="ts"` (any attribute would turn processing off anyway). Plain JavaScript is valid TypeScript, so JS scripts work unchanged.

Scripts with `is:inline`, and any other unprocessed script, aren't compiled: write those in plain JavaScript.

### `tsconfig.json`

esbuild reads the `tsconfig.json` nearest to each `.orbit` file (and to each imported `.ts` file) and honors its `paths`, `baseUrl` and `jsx` settings. Its `target` only decides how class fields are compiled (as esbuild does): the bundles are written for modern browsers (`esnext`). `orbit new` scaffolds:

- `tsconfig.json`, modeled on Astro's `strict` preset;
- `src/env.d.ts`, an empty place for your own global type declarations.

You don't need a `package.json` or the `typescript` package: [`orbit check`](#type-checking-with-orbit-check) uses Bun's built-in type checker.

The scaffolded `tsconfig.json`:

```json file=tsconfig.json
{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ESNext", "DOM", "DOM.Iterable"],
    "strict": true,
    "noEmit": true,
    "isolatedModules": true,
    "resolveJsonModule": true,
    "allowJs": true,
    "allowImportingTsExtensions": true,
    "skipLibCheck": true
  },
  "include": ["src/**/*.ts"]
}
```

It's yours to change.

### Type checking with `orbit check`

esbuild only **strips** types; it never checks them. So `orbit build` and `orbit dev` accept a script with a type error. To find type errors, run:

```sh
orbit check
```

`orbit check` checks your Go code first (see the [CLI reference](reference/cli.md#orbit-check)). For TypeScript it uses `bun check`, the TypeScript type checker built into [Bun](https://bun.sh) 1.4.4 and later:

1. It extracts each processed inline `<script>` to `.orbit/check/` as a `.ts` file, with a generated `tsconfig.json` that extends yours and checks the extracted scripts plus the files your `include` lists (`src/**/*.ts` by default), minus your `exclude`. The extracted files mirror `src/`, so relative imports (`../scripts/greet`) resolve from the `.orbit` file's directory, as they do in the bundle. A `<script src="…">` file is checked as one of your `.ts` files.
2. It runs `bun check --no-pretty -p .orbit/check`.
3. It maps every error back to the line and column in your `.orbit` file.

`bun check` reports the same errors as `tsc` and uses all your cores, and it needs no `typescript` package in your project.

For example, this script:

```orbit file=src/components/Total.orbit
<output class="total"></output>

<script>
  const total: number = document.querySelector(".total")!.textContent;
</script>
```

is reported at its `.orbit` position, in the same `file:line:col: message` format as every other error, with the TypeScript error code kept in the message:

```
src/components/Total.orbit:4:9: error TS2322: Type 'string | null' is not assignable to type 'number'.
```

**Bun 1.4.4 or later is required** for the TypeScript half. Without Bun, or with an older one, `orbit check` skips the TypeScript half, prints a notice with the command to install or upgrade Bun (`bun upgrade`), and still runs the Go checks. Its exit code then reflects the Go checks only. `orbit doctor` reports a missing or old Bun as a warning, never as an error, since nothing else in Orbit needs it.

## Source maps

In `orbit dev`, every CSS and JS bundle has a linked source map, so the browser's dev tools show your `.orbit` files, at the line and column where you wrote the code, and your `.ts` files. A breakpoint set in `src/components/Counter.orbit` stops in the bundled code for it.

Production builds have no source maps unless you set [`"sourcemap": true`](configuration.md#sourcemap) in `orbit.json`; the maps are then written next to the bundles but not linked from them. In a scoped style, the inserted `:where(…)` shifts the columns after it on the same line, so a CSS position can be a few columns off; lines are always right.

## Styles and scripts in `orbit dev`

Editing only the contents of a `<style>` or `<script>` (or a `.ts` file they import) rebuilds just the CSS and JS, with no Go rebuild, then reloads the page.

Adding or removing a component's scoped `<style>` or a processed `<script>`, or toggling `is:global`, also changes the HTML (scope attributes, bundle entries), so Orbit rebuilds the Go code too. See [Dev server](dev-server.md#what-a-change-rebuilds).

## See also

- [Lazy loading](lazy-loading.md): `client:*` strategies and `server:defer` islands
- [Components](components.md): templates, slots, and the warnings for `style={…}` and `on*={…}`
- [Dev server](dev-server.md): how style and script edits reload
- [CLI reference](reference/cli.md): `orbit check`, `orbit build`
- [Template syntax](reference/template-syntax.md): `is:inline`, `is:global`, `set:json`, `client:*` and other directives
