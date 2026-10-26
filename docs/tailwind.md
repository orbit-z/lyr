# Using Tailwind

Orbit supports [Tailwind CSS](https://tailwindcss.com) v4 out of the box. There's no integration to install and no `orbit.json` key: put `@import "tailwindcss"` in a stylesheet, and Orbit runs it through Tailwind's CLI before bundling it like any other CSS. Projects that don't use Tailwind don't need the CLI and pay nothing for it.

This page shows the setup, then how classes are found, `@apply` in component styles, fonts and other CSS your stylesheet imports, plugins, editors, the dev server and deployment.

## Install the Tailwind CLI

Orbit runs Tailwind's own CLI, v4. The **standalone CLI** is a single native binary: no Node, no `node_modules`. Install it one of these ways:

```sh
# Linux x64 (use tailwindcss-linux-arm64, -macos-arm64, -macos-x64 or -windows-x64.exe elsewhere)
curl -sLo ~/.local/bin/tailwindcss https://github.com/tailwindlabs/tailwindcss/releases/download/v4.3.3/tailwindcss-linux-x64
chmod +x ~/.local/bin/tailwindcss

# Homebrew (macOS, Linux)
brew install tailwindcss

# or pin it in the project, with any package manager
bun add -d @tailwindcss/cli@4.3.3   # or: npm i -D @tailwindcss/cli@4.3.3
```

Orbit looks for the CLI in this order, only when a stylesheet uses Tailwind:

1. the project's `node_modules/.bin/tailwindcss` (from `@tailwindcss/cli`), run with Node, or with Bun when Node isn't installed;
2. `tailwindcss` on your `PATH`;
3. with [Bun](https://bun.sh) installed: `bun x --package @tailwindcss/cli@4.3.3 tailwindcss` (Bun downloads it into its cache the first time).

Tailwind v3 isn't supported: a v3 CLI is skipped, with a message if nothing else is found. A v3-style `tailwind.config.js` still works through v4's [`@config`](https://tailwindcss.com/docs/functions-and-directives#config-directive). Run `orbit doctor` to see which CLI Orbit uses:

```
✓ tailwind: tailwindcss 4.3.3 (/home/me/.local/bin/tailwindcss), used by src/styles/global.css
```

## Add a stylesheet

Tailwind v4 is configured in CSS. Create one stylesheet for the site, for example `src/styles/global.css`:

```css file=src/styles/global.css
@import "tailwindcss";
@import "@fontsource-variable/figtree";

@theme {
  --color-ember: #d1703f;
  --font-ui: "Figtree Variable", system-ui, sans-serif;
}

@utility card-row {
  @apply flex items-baseline gap-2 border-b border-zinc-700 py-4;
}

@custom-variant hocus (&:hover, &:focus-visible);
```

Then include it from your layout. In Astro you'd write `import "../styles/global.css"` in the layout's frontmatter; an Orbit frontmatter is Go, so the import goes in a global style block instead:

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
    <title>{props.Title}</title>
  </head>
  <body class="bg-zinc-900 font-ui text-zinc-100">
    <main class="mx-auto max-w-2xl p-4">
      <slot />
    </main>
  </body>
</html>

<style is:global>
  @import "../styles/global.css";
</style>
```

That's the whole setup. A CSS file is a **Tailwind input** when it imports `tailwindcss` (or `tailwindcss/…`), has a `@tailwind` directive, or uses one of Tailwind's at-rules (`@apply`, `@reference`, `@theme`, `@utility`, `@variant`, `@custom-variant`, `@source`, `@plugin`, `@config`). Comments and strings don't count. Every other CSS file is bundled as before.

Tailwind's output is a stylesheet like any other: Orbit bundles it into the page's stylesheet, in import order, minified and hashed under `/_orbit/` in production, and links it from the page's `<head>`. Nothing runs at request time.

## Use utilities in templates

Write Tailwind's classes in your templates, in plain `class` attributes or in Go strings:

```orbit file=src/components/Card.orbit
---
type Props struct {
	Title  string
	Active bool
}
size := "p-8"
if !props.Active {
	size = "p-2"
}
---
<article class={"card-row rounded-lg " + size}>
  <h2 class="title hocus:underline">{props.Title}</h2>
</article>

<style>
  @reference "../styles/global.css";
  .title {
    @apply text-lg font-bold hover:text-ember;
  }
</style>
```

```orbit file=src/pages/index.orbit
---
import (
	Card "../components/Card.orbit"
	Base "../layouts/Base.orbit"
)
---
<Base title="Home">
  <Card title="One" active={true} />
  <Card title="Two" />
</Base>
```

Tailwind finds the classes by scanning your project's files ([automatic source detection](https://tailwindcss.com/docs/detecting-classes-in-source-files)), the same way it scans an Astro project:

- `.orbit` files are scanned, and classes are found in attributes, in `{…}` expressions and in Go string literals in the frontmatter (`size := "p-8"`, `map[bool]string{true: "ring-2"}`). `.go` files elsewhere in your module (a `internal/ui` package that builds class strings) and `.md` files are scanned too.
- Files your `.gitignore` ignores are skipped, even without a Git repository. The starter's `.gitignore` already ignores `orbit_gen/`, `.orbit/`, `dist/` and `node_modules/`; keep those lines, or Tailwind also scans the generated code (the same classes, only slower). `orbit doctor` warns when they're missing.
- Write complete class names. A class built at runtime, such as `fmt.Sprintf("w-%d", n)`, isn't found, as with every framework Tailwind supports. Use a map or a `switch` that spells out each class instead.
- To scan more, or less, use Tailwind's own directives in the stylesheet: `@source "../../packages/ui";` for classes outside the project, `@source not "../../internal/api";` to skip a large Go tree, `@source inline("bg-red-500");` to always generate a class, or `@import "tailwindcss" source(none);` with your own `@source` list.

## `@apply` in component styles

A component's `<style>` can use Tailwind's at-rules too. To use `@apply` with your theme, reference the global stylesheet with [`@reference`](https://tailwindcss.com/docs/functions-and-directives#reference-directive), as in Astro: it makes the theme, custom utilities and variants available without emitting the stylesheet a second time. Card's style above becomes:

```css
.title[data-o-1a2b3c4d] { font-size: var(--text-lg, 1.125rem); …; font-weight: var(--font-weight-bold, 700) }
@media (hover: hover) { .title[data-o-1a2b3c4d]:hover { color: var(--color-ember, #d1703f) } }
```

Orbit [scopes](styles-and-scripts.md#how-scoping-works) the block first and runs Tailwind after, which is Astro's order. So:

- `@apply` in a scoped rule applies to the scoped selector, variants included (`.title[data-o-…]:hover`).
- The bodies of `@theme`, `@utility`, `@custom-variant`, `@plugin` and `@source` aren't scoped.
- Utilities themselves are never scoped. A scoped block that imports `tailwindcss` emits global utilities next to its scoped rules, so import `tailwindcss` once, from a global stylesheet, and use `@reference` in components.
- Tailwind puts utilities in `@layer utilities`, while your component styles aren't in a layer, so a component's rule wins over a utility on the same property, whatever the specificity ([cascade layers](https://developer.mozilla.org/docs/Learn_web_development/Core/Styling_basics/Cascade_layers)). This is the same in Astro with Tailwind v4.
- Each block that `@apply`s a utility gets its own copy of Tailwind's fallback declarations for it (`@property --tw-…`). That's harmless; for a style you reuse in many components, prefer a `@utility` in the global stylesheet.

## Fonts and other CSS your stylesheet imports

A Tailwind stylesheet can `@import` other CSS files, from your project or from npm packages, and their `url()`s keep working. In the example above, `global.css` imports [Fontsource](https://fontsource.org)'s Figtree:

```sh
bun add @fontsource-variable/figtree
```

Its `index.css` refers to `url(./files/figtree-latin-wght-normal.woff2)`, relative to `node_modules/@fontsource-variable/figtree/`. Before running Tailwind, Orbit inlines the CSS files a Tailwind stylesheet imports and rewrites their relative `url()`s, as Astro's Tailwind integration does. The font then ends up in your build like any other asset: `url(/_orbit/figtree-latin-wght-normal-HASH.woff2)`, served from `/_orbit/` with the rest.

In detail:

- An `@import` is inlined when it resolves to a CSS file: a relative path, or an npm package (`@fontsource-variable/figtree`, `pkg/dist/x.css`), resolved like a CSS import in a script. Its conditions are kept: `layer(…)`, `supports(…)` and media queries become `@layer`, `@supports` and `@media` blocks, in import order.
- In the inlined files, relative paths in `url()`, `@source`, `@plugin`, `@config` and `@reference` are rewritten so they still point at the same files.
- `url(data:…)`, absolute URLs (`https://…`), root-relative paths (`/images/x.png`, served from `public/`) and `#fragment`s are left as written.
- Imports of `tailwindcss` itself, remote imports (`@import url("https://fonts.googleapis.com/…")`) and imports with Tailwind's own conditions (`theme(…)`, `prefix(…)`) are left to Tailwind.

You can still import a font's CSS from a script (`import "@fontsource-variable/figtree"`) or a `<style is:global>` instead; both work the same.

## Plugins

Tailwind's first-party plugins are built into the standalone CLI, so these work without `node_modules`:

```css
@plugin "@tailwindcss/typography";
@plugin "@tailwindcss/forms";
```

Local JavaScript plugins and configs work too (`@plugin "./tailwind/plugin.mjs";`, `@config "./tailwind.config.js";`): the standalone CLI runs them itself. Third-party plugins from npm, such as daisyUI, need the package in `node_modules`, installed by any package manager; the standalone CLI then loads it, with no Node at build time:

```sh
bun add -d daisyui
```

```css
@plugin "daisyui";
```

Without `node_modules`, Tailwind's own error (`Can't resolve 'daisyui'`) is shown at the stylesheet. Container queries are part of Tailwind v4's core: there's no plugin to add.

## Editor setup

In VS Code, install [Tailwind CSS IntelliSense](https://marketplace.visualstudio.com/items?itemName=bradlc.vscode-tailwindcss) for class completion, hover previews and linting. Orbit's extension sets `"tailwindCSS.includeLanguages": { "orbit": "html" }` by default, so it works in `.orbit` files with no setup; if you set `tailwindCSS.includeLanguages` yourself, your object replaces Orbit's default, so include `"orbit": "html"`. Tailwind recommends associating `.css` files with its CSS language mode, so VS Code's CSS validator doesn't flag `@theme` and `@apply`:

```json file=~/.config/Code/User/settings.json
{
  "files.associations": { "*.css": "tailwindcss" }
}
```

In Neovim, enable the `tailwindcss` language server for `orbit` files: see [Editor setup](editor-setup.md#tailwind-css). Orbit's own language server doesn't complete or check Tailwind classes; the two servers run side by side.

## In `orbit dev`

The dev server reruns Tailwind only when a change can change its output:

| You change | Tailwind |
|---|---|
| a template, frontmatter, `.go` or `.md` file that brings a word Tailwind hasn't seen | reruns your Tailwind stylesheets **in parallel** with the Go rebuild, then rebuilds the CSS if Tailwind's output changed. The page reloads once both are done. |
| a template, frontmatter or `.go` file that only removes, moves or repeats words | doesn't run. |
| a `.css` file, `tsconfig.json` or `package.json` | reruns every Tailwind input, then rebuilds the CSS. |
| a component's `<style>` that uses Tailwind | reruns that block (and the stylesheets, if the edit brings a new word). |

Orbit decides by keeping the set of words (whitespace-separated tokens) of the files Tailwind scans: Tailwind's classes never contain whitespace, so a new class always comes with a new word. As with Tailwind's own watch mode, the dev stylesheet only grows: a class you stop using stays in it until the next full run (restarting `orbit dev`). Production builds always start from scratch. A failed Tailwind run shows the error overlay and keeps the last good CSS.

A Tailwind run takes about 0.1 s on a 200-file project. For a template edit it overlaps the Go rebuild, so the reload comes about 5–20 ms later than without Tailwind; a CSS-only edit (no Go rebuild) waits for it, about 90 ms instead of 20 ms. See [Performance](performance.md#tailwind-css).

## `orbit build`, `orbit check` and deployment

`orbit build`, `orbit generate` and `orbit check` run every Tailwind input once, from scratch, before bundling: about 0.1 s more on a 200-file project. The output is the same for the same sources, so builds stay reproducible. `orbit check` reports Tailwind's errors, so an `@apply` of an unknown class fails it (and CI).

The build needs the CLI. In Docker, add it to the build stage of the [Dockerfile](deploy.md#docker) that `orbit new` writes; `ADD --checksum` verifies the download:

```dockerfile
ADD --checksum=sha256:dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a \
    https://github.com/tailwindlabs/tailwindcss/releases/download/v4.3.3/tailwindcss-linux-x64 /usr/local/bin/tailwindcss
RUN chmod +x /usr/local/bin/tailwindcss
```

Use `tailwindcss-linux-arm64` (sha256 `55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195`) for an arm64 build stage. With npm plugins, copy `package.json` and the lockfile and install them in the build stage too.

## Errors

Tailwind's errors are reported at your file, line and column, like Orbit's own:

```
src/components/Card.orbit:18:5: Cannot apply unknown utility class `font-boldest`
```

Without a v4 CLI, a project that uses Tailwind fails to build with an error at its first Tailwind rule, naming the install commands for your platform (see [Troubleshooting](troubleshooting.md#the-tailwind-css-cli-isnt-found)):

```
src/styles/global.css:1:1: @import "tailwindcss" needs the Tailwind CSS CLI v4, which wasn't found; install it: curl -sLo ~/.local/bin/tailwindcss https://github.com/tailwindlabs/tailwindcss/releases/download/v4.3.3/tailwindcss-linux-x64 && chmod +x ~/.local/bin/tailwindcss, or brew install tailwindcss, or bun add -d @tailwindcss/cli@4.3.3; see orbit doctor
```

## What's not supported

- Tailwind v3 (`@tailwind base`, a `tailwind.config.js` as the entry point), PostCSS and other CSS preprocessors.
- Source maps through Tailwind: in dev and with [`"sourcemap": true`](configuration.md#sourcemap), a Tailwind input's map shows Tailwind's output, not your stylesheet's lines.
- Removing unused classes from the dev stylesheet before the next full run.

## See also

- [Styles and scripts](styles-and-scripts.md): scoped CSS, bundling, npm packages
- [Coming from Astro](coming-from-astro.md#tailwind-css): moving an Astro + Tailwind site
- [Dev server](dev-server.md#what-a-change-rebuilds): what each change rebuilds
- [Deploy](deploy.md#docker): the Dockerfile
- Tailwind's docs: [functions and directives](https://tailwindcss.com/docs/functions-and-directives), [detecting classes](https://tailwindcss.com/docs/detecting-classes-in-source-files)
