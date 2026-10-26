# Editor setup

Orbit's language server, `orbit lsp`, gives your editor Go completion, diagnostics, go-to-definition and rename inside `.orbit` files, by working together with gopls, and reports Orbit's errors in your Markdown pages as you type.

## Check your setup with `orbit doctor`

Before you configure an editor, run this in your project:

```sh
orbit doctor
```

Among other things, it checks the gopls that `orbit lsp` will use. Its output looks like this:

```
✓ go: go1.27.3 (go.mod: go 1.27, toolchain go1.27.3)
✓ cli: orbit v0.1.0 hands commands to the pinned go tool orbit (v0.1.0)
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); orbit check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
✓ orbit_gen: up to date
✓ gitignore: covers orbit_gen/, .orbit/ and dist/
✓ config: no orbit.json (defaults); server ./cmd/server
✓ tsconfig: rootDirs includes orbit_gen/types
```

For gopls you'll see:

- **✓** when gopls is found and recent enough.
- **⚠** when gopls is older than the recommended **v0.24.0**.
- **✗** when gopls is missing, or was built with an older Go than your project's `go` directive. Orbit can't use that gopls (see [Old gopls versions](#old-gopls-versions)).

Each ⚠ and ✗ comes with the command that fixes it. For gopls, that's:

```sh
go install golang.org/x/tools/gopls@latest
```

## VS Code

Install the **Orbit** extension, `orbit-z.orbit`. It's published on the VS Code Marketplace and on Open VSX, so VSCodium, Cursor and other VS Code-based editors can install it too. From the command line:

```sh
code --install-extension orbit-z.orbit
```

The extension:

- registers the `orbit` language for `*.orbit` files;
- highlights them: the frontmatter and `{…}` expressions as Go, `<style>` as CSS, `<script>` as TypeScript (`<script type="application/json">` as JSON), and the rest as HTML, with components and directives set apart;
- adds bracket matching, comment toggling and auto-closing;
- starts `orbit lsp` for completion, diagnostics and the rest;
- sends the Markdown files under `src/` to `orbit lsp` too, for their diagnostics only (see [Markdown files](#markdown-files)): their highlighting and every other Markdown feature stay VS Code's;
- adds three commands to the Command Palette:
  - **Orbit: Restart Language Server**, for example after installing a new gopls;
  - **Orbit: Run orbit generate**;
  - **Orbit: Run orbit doctor**.

  The last two run in your project root (the nearest directory above the active `.orbit` file, or else above the workspace folder, that holds `go.mod` and `src/`), with their output in the **Orbit** output channel.

One language server runs per window. In a multi-root workspace with several Orbit projects, it serves the first one it finds: the project of the `.orbit` file that's active when it starts, else that of the first workspace folder in one.

It runs the `orbit` on your `PATH`. Inside a project that pins the CLI in `go.mod`, that `orbit` hands over to the pinned version (the binary `go tool orbit lsp` would run), so the language server always matches your project's Orbit version. Without an `orbit` on your `PATH`, it runs `go tool orbit` instead when the project pins the CLI, as projects made by `orbit new` do. To use a different binary, set `orbit.path`; to choose the gopls that Orbit starts, set `orbit.goplsPath` (see [How gopls is found](#how-gopls-is-found)):

```json file=.vscode/settings.json
{
  "orbit.path": "/home/me/go/bin/orbit",
  "orbit.goplsPath": "/home/me/go/bin/gopls"
}
```

### The Go extension's errors in `orbit_gen/`

If you also have the VS Code Go extension installed (you probably do), it runs its **own** gopls next to Orbit's. That gopls sees the generated Go files in `orbit_gen/` and reports their errors in the Problems panel, at positions in generated files. Orbit doesn't suppress them.

These are usually the same errors Orbit already shows at the right place in your `.orbit` file: fix them there. Errors that only show up in `orbit_gen/` often mean the generated code is stale; run `orbit generate` (or keep `orbit dev` running).

Don't hide them with a gopls `directoryFilters` setting such as `"-orbit_gen"`. Orbit passes your editor's `gopls` settings to its own gopls too, so the filter would apply to Orbit's gopls as well, which needs `orbit_gen/` to give you Go features in `.orbit` files.

## Neovim

With Neovim 0.11 or later, add this to your config:

```lua file=~/.config/nvim/init.lua
vim.filetype.add({ extension = { orbit = 'orbit' } })

vim.lsp.config('orbit', {
  cmd = { 'orbit', 'lsp' },
  filetypes = { 'orbit' },
  root_markers = { 'go.mod' },
})
vim.lsp.enable('orbit')
```

The first line makes Neovim detect `.orbit` files; the rest starts `orbit lsp` for them. For [diagnostics in Markdown files](#markdown-files) too, use `filetypes = { 'orbit', 'markdown' }`: the language server ignores Markdown files Orbit doesn't compile, and answers other requests on Markdown files with nothing. The language server finds the project root itself (the directory with `go.mod` and `src/`).

Orbit doesn't ship syntax highlighting for Neovim: its grammar is a TextMate grammar, which only VS Code uses. Completion, diagnostics and the other language-server features work without it.

## How gopls is found

`orbit lsp` starts a gopls process for your project. It looks for one in this order:

1. the `goplsPath` initialization option, if your editor sets it (the VS Code extension sets it from its `orbit.goplsPath` setting);
2. `gopls` on your `PATH`;
3. `go tool gopls`, if your project's `go.mod` pins gopls as a tool.

If none is found, the language server tells you how to install gopls.

It's passed in the LSP `initializationOptions` as `{ "goplsPath": "…" }`. For example, in Neovim:

```lua
vim.lsp.config('orbit', {
  init_options = { goplsPath = '/home/me/go/bin/gopls' },
})
```

Pinning gopls in the project is optional:

```sh
go get -tool golang.org/x/tools/gopls@v0.24.0
```

It's not the default, and it's only used when there's no `gopls` on your `PATH`. A pin pulls all of `golang.org/x/tools` into your module graph, which can raise the versions of dependencies you share with it. Its first build also takes 30–60 seconds, long enough for some editors to give up on the language server at startup. A gopls installed with `go install` avoids both.

Orbit passes your editor's `gopls` settings to its gopls, so options you set there (for example `buildFlags`) apply inside `.orbit` files too. It also sets `diagnosticsDelay` to `50ms` unless you set it yourself, so diagnostics follow your edits quickly, and turns gopls's internal `zeroConfig` option off (Orbit's gopls only ever sees your project's module), which makes opening hundreds of generated files at startup much cheaper.

## Old gopls versions

The recommended minimum is **gopls v0.24.0**, the version Orbit's editor support was verified with.

When it starts gopls, `orbit lsp` reads which Go version gopls was built with. If that's older than the `go` directive in your project's `go.mod`, gopls can't type-check your code correctly: it would reject newer language features as errors. So Orbit **refuses that gopls**:

- it shuts that gopls down;
- **Go features are disabled in `.orbit` files**: no Go completion, hover, Go diagnostics, definition, references or rename;
- **template features keep working**: completion of tags, component props, slot names and directives, and Orbit's own diagnostics (syntax errors, unknown props);
- your editor shows an error with the install command, pointing to `orbit doctor`.

Fix it by installing a current gopls, then restart the language server:

```sh
go install golang.org/x/tools/gopls@latest
```

A gopls built with a recent enough Go but older than v0.24.0 still runs; `orbit doctor` warns about it.

## Startup and `orbit_gen/`

The language server never writes to `orbit_gen/`. It generates the Go code for your `.orbit` files in memory and hands it to gopls, so it works in a fresh clone too.

With an up-to-date `orbit_gen/`, gopls reads the generated code of every file you haven't edited from disk, as it would for any Go project; only the files you're editing, and those changed since `orbit generate` last ran, are handed over from memory. On a 200-component project, Go features answer about 0.2 s after the editor starts. Without `orbit_gen/`, gopls first loads every component from memory, in one go: Go features answer about 0.4 s after the editor starts on the same project, and **references and rename wait** for that load, so they don't miss any uses. Your editor shows a progress indicator meanwhile. With only a few components missing from `orbit_gen/`, gopls loads those one by one instead; completion, hover and diagnostics don't wait for them. Keeping `orbit dev` running keeps `orbit_gen/` up to date.

## What completion covers

**Go**, through gopls, everywhere you write Go: the frontmatter, `{…}` expressions, attribute expressions and `{if}`/`{for}` headers. You get completion (including `props.`, `ctx.`, `slots.`, your typed `locals.` and, in pages, the route's `params.`), hover, signature help, go-to-definition, references and rename, across your `.orbit` files and your `.go` packages. Accepting a completion for an unimported package adds the import to the frontmatter.

**Templates**, natively:

- **Tag names** after `<`: the components you've imported first, then other components in the project (accepting one adds its `.orbit` import, such as `import Card "../components/Card.orbit"`), then `<Fragment>` and HTML elements.
- **Props** on a component tag: its `Props` fields as attribute names (`post`, `featured`), with their types and doc comments. Accepting one inserts `post={…}`. Props you've already set aren't offered again.
- **HTML attributes** on HTML elements: the element's own and the global ones.
- **Directives** where they're valid: `slot` on any tag; `set:html` and `set:text` on HTML elements and `<Fragment>`; `is:inline` on `<script>` and `<style>`; `is:global` on `<style>`; `set:json` on `<script type="application/json">`. On component tags, `server:defer`, `client:load`, `client:idle`, `client:visible` and `client:media` (they're errors on HTML elements, so they're never offered there). See [Lazy loading](lazy-loading.md).
- **Slot names** inside `slot="…"`: the slots of the component you're passing content to. Inside a `server:defer` component, only `fallback`.
- **Go to definition** on `<Card` opens `Card.orbit`; on a component attribute, it jumps to the `Props` field.

**Diagnostics** appear at their position in the `.orbit` file as you type, with no save needed: Orbit's own (template syntax errors, unknown props, misused directives) and gopls's Go type errors. Changes are visible across files before you save: add a field to `Card`'s `Props`, and the new attribute is accepted in a page right away.

**Import paths**: inside the quotes of a frontmatter import, once you type `./` or `../`, completion offers the project's `.orbit` files and the Markdown files under `src/`, as paths relative to the file you're in (the file itself and those you've already imported aren't offered). In an import with no alias, `import "./`, accepting one also adds the alias: `import Post "./post.md"`.

**TypeScript** in `<script>` gets syntax highlighting only, with no completion or diagnostics; see [TypeScript](#typescript).

Formatting `.orbit` files and semantic highlighting are not yet supported.

## TypeScript

Your editor's own TypeScript support (VS Code's built-in TypeScript server, or `typescript-language-server` in Neovim) covers the **`.ts` files** under `src/`, through your `tsconfig.json`. That includes the generated types of the data components pass to scripts: in `src/scripts/like-button.ts`, `import { data } from "../components/LikeButton.orbit"` is typed: completion offers the keys, and the result has the TypeScript type of the Go value. This needs the starter `tsconfig.json`'s `rootDirs` and `include` entries for `orbit_gen/types` (see [`tsconfig.json`](styles-and-scripts.md#tsconfigjson); `orbit doctor` warns when they're missing), and an `orbit_gen/types/` that `orbit dev` or `orbit generate` has written. Before that, on a fresh clone, the editor reports `Cannot find module '../components/LikeButton.orbit'`.

**`<script>` blocks inside `.orbit` files** get no TypeScript features yet: no completion, hovers or diagnostics. To have your editor check a script, move its logic into a `.ts` file and import it (or point `<script src="../scripts/like-button.ts">` at it); see [Importing TypeScript files](styles-and-scripts.md#importing-typescript-files). [`orbit check`](styles-and-scripts.md#type-checking-with-orbit-check) (which uses `bun check`, Bun 1.4.4 or later) type-checks both kinds, block scripts at their `.orbit` positions.

The language server doesn't write `orbit_gen/types/`; `orbit dev` keeps it current as you edit, and `orbit generate` writes it once.

## Markdown files

The language server reports Orbit's errors and warnings in the Markdown files it compiles: the pages under `src/pages/`, and the `.md` files an `.orbit` file imports (see [Markdown](markdown.md)). They update as you type, with no save needed:

- frontmatter errors: YAML syntax, anchors and tags, a key with no value, a missing or misplaced `layout`, a key the layout's `Props` doesn't have;
- links to other Markdown files: a missing target is an error, a `#fragment` with no matching heading a warning;
- route errors: brackets in a page's path, a route another page already has.

A frontmatter value of the wrong type for its layout field (`draft: "yes"` for a `bool` field) is found by Go's type checker, which the language server runs in the background: when the editor starts, after `orbit dev` or `orbit generate` rewrites `orbit_gen/`, and about 300 ms after you stop typing in a Markdown file (or change the `Props` of a layout that a Markdown file uses, or a Go file). That error stays on its key while you edit the rest of the file; once you edit the key itself, it's hidden until the next check, a moment later. `orbit dev` and `orbit check` always show it.

Other Markdown files, such as a `README.md` that nothing imports, are ignored. Everything else about Markdown files (highlighting, preview, their own completion) stays with your editor's Markdown support: the VS Code extension sends `.md` files under `src/` to the language server for diagnostics only.

The language server also follows `.md` changes on disk, so in an `.orbit` file that imports `Post "./post.md"`, `Post.Frontmatter.` completes the frontmatter's fields.

## Rename

Rename works on Go identifiers in the frontmatter and in expressions, and updates every place that refers to them:

- **A frontmatter variable**: renaming `title` updates its declaration and every `{title}` in the template.
- **A `Props` field**: renaming `Post` to `Article` in `Card.orbit` also renames the attribute at every call site, keeping its spelling style: `post={p}` becomes `article={p}`, and `is-open` would become `is-closed` rather than `isClosed`. You can start the rename from the field's declaration, from a `props.Post` use, or from a `post=` attribute at a call site; there you type the new attribute name (`is-closed`), and the field becomes `IsClosed`.
- **An `.orbit` import alias**: renaming `Card`, from its import or from a `<Card>` or `</Card>` tag, updates the import and every `<Card>` tag in that file. The new alias must start with an uppercase letter.
- **Anything in your `.go` packages** that an `.orbit` file uses, such as `posts.Post`, is renamed in both.

How call sites keep their spelling, for a field renamed to `IsClosed`:

| At the call site | Becomes |
|---|---|
| `isOpen` | `isClosed` |
| `is-open` | `is-closed` |
| `is_open` | `is_closed` |
| `isopen` | `isclosed` |
| `IsOpen` | `IsClosed` |
| `{isOpen}` (shorthand) | `isClosed={isOpen}` |

Any other spelling (`ISOPEN`) becomes the camelCase one completion inserts (`isClosed`). Renaming the variable of a shorthand keeps the attribute's name: renaming `isOpen` to `open` turns `{isOpen}` into `isOpen={open}`.

Orbit refuses a rename, with a message:

- of a name the generated code declares rather than your file: `props`, `ctx`, `slots`, `locals`, `params`, and the `Props` type itself;
- of a `params` field, such as `params.Slug`: it comes from the file path (`[slug].orbit`), so rename the file instead;
- of a layout's `Props` field that a Markdown file's frontmatter sets: the language server doesn't edit Markdown files, so rename the field and the frontmatter key yourself (the message names the file and line);
- when it would have to change generated code that doesn't correspond to anything in your files;
- of a `Props` field to an unexported name (`article`): a parent couldn't set it.

Rename waits until the language server has loaded the whole project (see [Startup and `orbit_gen/`](#startup-and-orbit_gen)), so it never misses a use.

## Formatting

The language server formats `.orbit` files with the same rules as `orbit fmt`: the frontmatter, `{…}` expressions and control blocks with `gofmt`'s rules, and `<script>` and `<style>` content with `bun fmt` when Bun 1.4.4 or later is installed. It formats what's in the editor, saved or not. It never touches the HTML, so formatting never changes what a page renders.

- It formats the **whole document** ("Format Document"). "Format Selection" isn't supported, as with gopls.
- A file with a syntax error isn't changed; the error is already in your diagnostics.
- Your editor's tab size and spaces setting don't apply: the frontmatter uses tabs, like Go, the template keeps its own indentation, and scripts and styles follow your Prettier or Bun configuration.
- Without Bun, scripts and styles are left as they are, and the language server says so once per session.
- Markdown files aren't formatted by Orbit's language server: VS Code's Markdown support, Prettier or `bun fmt` handle them.

**VS Code**: the Orbit extension is the default formatter for `.orbit` files, so "Format Document" (Shift+Alt+F) works as installed. To format on save, add this to your settings:

```json
{
  "[orbit]": {
    "editor.formatOnSave": true
  }
}
```

**Neovim**: `vim.lsp.buf.format()` formats the current buffer. To format on save:

```lua
vim.api.nvim_create_autocmd('BufWritePre', {
  pattern = '*.orbit',
  callback = function() vim.lsp.buf.format() end,
})
```

To check formatting in CI, run `orbit fmt -l` (see [Fresh clones and CI](project-structure.md#fresh-clones-and-ci)).

## See also

- [Getting started](getting-started.md): installing Orbit and running `orbit doctor`
- [Components](components.md): what `props`, `slots`, `ctx` and `locals` are
- [CLI reference](reference/cli.md#orbit-lsp): `orbit lsp`, and [`orbit doctor`](reference/cli.md#orbit-doctor)'s checks
- [Styles and scripts](styles-and-scripts.md): TypeScript and `orbit check`
- [CLI reference](reference/cli.md#orbit-fmt): `orbit fmt` (planned)
