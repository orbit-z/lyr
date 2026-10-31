# Editor setup

Lyr's language server, `lyr lsp`, gives your editor Go completion, diagnostics, go-to-definition and rename inside `.lyr` files, by working together with gopls, TypeScript features inside their `<script>` blocks, by working together with TypeScript 7, and reports Lyr's errors in your Markdown pages as you type.

## Check your setup with `lyr doctor`

Before you configure an editor, run this in your project:

```sh
lyr doctor
```

Among other things, it checks the gopls and the TypeScript that `lyr lsp` will use. Its output looks like this:

```
✓ go: go1.27.3 (go.mod: go 1.27, toolchain go1.27.3)
✓ cli: lyr v0.1.0 hands commands to the pinned go tool lyr (v0.1.0)
✓ gopls: v0.24.0, built with go1.27.3
⚠ bun: not found (1.4.4 or later needed); lyr check will skip TypeScript type checking
    fix: curl -fsSL https://bun.sh/install | bash
⚠ typescript: not found (7.0.2 or later needed); TypeScript features in <script> blocks are off in the editor
    fix: bun add -g typescript@7.0.2 (or npm i -g typescript@7.0.2), or set typescriptPath
✓ lyr_gen: up to date
✓ gitignore: covers lyr_gen/, .lyr/ and dist/
✓ config: no lyr.json (defaults); server ./cmd/server
✓ tsconfig: rootDirs includes lyr_gen/types
```

For gopls you'll see:

- **✓** when gopls is found and recent enough.
- **⚠** when gopls is older than the recommended **v0.24.0**.
- **✗** when gopls is missing, or was built with an older Go than your project's `go` directive. Lyr can't use that gopls (see [Old gopls versions](#old-gopls-versions)).

Each ⚠ and ✗ comes with the command that fixes it. For gopls, that's:

```sh
go install golang.org/x/tools/gopls@latest
```

TypeScript is optional, like Bun: without it, `<script>` blocks keep their highlighting and everything else works. The check is a ⚠ only when a `.lyr` file has a script; see [TypeScript in `<script>` blocks](#typescript-in-script-blocks).

## VS Code

Install the **Lyr** extension, `orbit-z.lyr`. It's published on the VS Code Marketplace and on Open VSX, so VSCodium, Cursor and other VS Code-based editors can install it too. From the command line:

```sh
code --install-extension orbit-z.lyr
```

The extension:

- registers the `lyr` language for `*.lyr` files;
- highlights them: the frontmatter and `{…}` expressions as Go, `<style>` as CSS, `<script>` as TypeScript (`<script type="application/json">` as JSON), and the rest as HTML, with components and directives set apart;
- adds bracket matching, comment toggling and auto-closing;
- starts `lyr lsp` for completion, diagnostics and the rest;
- sends the Markdown files under `src/` to `lyr lsp` too, for their diagnostics only (see [Markdown files](#markdown-files)): every other Markdown feature stays VS Code's;
- highlights ```` ```lyr ```` code blocks in any Markdown file, in the editor (not in VS Code's Markdown preview, which has its own highlighter): a `---` line in the block opens Go frontmatter, the rest is the template;
- adds three commands to the Command Palette:
  - **Lyr: Restart Language Server**, for example after installing a new gopls;
  - **Lyr: Run lyr generate**;
  - **Lyr: Run lyr doctor**.

  The last two run in your project root (the nearest directory above the active `.lyr` file, or else above the workspace folder, that holds `go.mod` and `src/`), with their output in the **Lyr** output channel.

One language server runs per window. In a multi-root workspace with several Lyr projects, it serves the first one it finds: the project of the `.lyr` file that's active when it starts, else that of the first workspace folder in one.

It runs the `lyr` on your `PATH`. Inside a project that pins the CLI in `go.mod`, that `lyr` hands over to the pinned version (the binary `go tool lyr lsp` would run), so the language server always matches your project's Lyr version. Without a `lyr` on your `PATH`, it runs `go tool lyr` instead when the project pins the CLI, as projects made by `lyr new` do. To use a different binary, set `lyr.path`; to choose the gopls that Lyr starts, set `lyr.goplsPath` (see [How gopls is found](#how-gopls-is-found)); to choose the TypeScript 7 it starts for `<script>` blocks, set `lyr.typescriptPath` (see [How TypeScript is found](#how-typescript-is-found)):

```json file=.vscode/settings.json
{
  "lyr.path": "/home/me/go/bin/lyr",
  "lyr.goplsPath": "/home/me/go/bin/gopls",
  "lyr.typescriptPath": "/home/me/.bun/install/global/node_modules/typescript"
}
```

| Setting | Default | What it does |
|---|---|---|
| `lyr.path` | `lyr` | The `lyr` binary the extension runs. |
| `lyr.goplsPath` | (empty) | The gopls `lyr lsp` starts (the `goplsPath` initialization option). |
| `lyr.typescriptPath` | (empty) | The TypeScript 7 `lyr lsp` starts for `<script>` blocks (the `typescriptPath` initialization option): a native `tsc` binary or a `typescript` package directory. |

Changing any of them restarts the language server.

### The Go extension's errors in `lyr_gen/`

If you also have the VS Code Go extension installed (you probably do), it runs its **own** gopls next to Lyr's. That gopls sees the generated Go files in `lyr_gen/` and reports their errors in the Problems panel, at positions in generated files. Lyr doesn't suppress them.

These are usually the same errors Lyr already shows at the right place in your `.lyr` file: fix them there. Errors that only show up in `lyr_gen/` often mean the generated code is stale; run `lyr generate` (or keep `lyr dev` running).

Don't hide them with a gopls `directoryFilters` setting such as `"-lyr_gen"`. Lyr passes your editor's `gopls` settings to its own gopls too, so the filter would apply to Lyr's gopls as well, which needs `lyr_gen/` to give you Go features in `.lyr` files.

## Neovim

With Neovim 0.11 or later, add this to your config:

```lua file=~/.config/nvim/init.lua
vim.filetype.add({ extension = { lyr = 'lyr' } })

vim.lsp.config('lyr', {
  cmd = { 'lyr', 'lsp' },
  filetypes = { 'lyr' },
  root_markers = { 'go.mod' },
})
vim.lsp.enable('lyr')
```

The first line makes Neovim detect `.lyr` files; the rest starts `lyr lsp` for them. To choose the gopls or the TypeScript 7 that it starts, add `init_options = { goplsPath = '…', typescriptPath = '…' }` (both optional; see [How gopls is found](#how-gopls-is-found) and [How TypeScript is found](#how-typescript-is-found)). For [diagnostics in Markdown files](#markdown-files) too, use `filetypes = { 'lyr', 'markdown' }`: the language server ignores Markdown files Lyr doesn't compile, and answers other requests on Markdown files with nothing. The language server finds the project root itself (the directory with `go.mod` and `src/`).

Lyr doesn't ship syntax highlighting for Neovim: its grammar is a TextMate grammar, which only VS Code uses. Completion, diagnostics and the other language-server features work without it.

## Tailwind CSS

In a project that uses [Tailwind](tailwind.md), Tailwind's own language server completes, previews and lints classes in `.lyr` files, next to Lyr's. The two don't overlap: Lyr offers nothing inside `class` attribute values, Tailwind's server offers nothing in Go code, and when both answer a hover the editor shows both.

**VS Code.** Install [Tailwind CSS IntelliSense](https://marketplace.visualstudio.com/items?itemName=bradlc.vscode-tailwindcss). Lyr's extension sets `"tailwindCSS.includeLanguages": { "lyr": "html" }` by default (it does nothing without Tailwind's extension), so `.lyr` files work with no setup. Its default `tailwindCSS.classAttributes` covers `class`. If you set `tailwindCSS.includeLanguages` yourself, your object replaces Lyr's default: include `"lyr": "html"` in it. Tailwind finds your v4 stylesheet by itself. For `.css` files, Tailwind recommends its CSS mode, so VS Code's CSS validator stops flagging `@theme` and `@apply`:

```json file=~/.config/Code/User/settings.json
{
  "files.associations": { "*.css": "tailwindcss" }
}
```

**Neovim.** Enable the `tailwindcss` language server (from `@tailwindcss/language-server`) for `lyr` files too:

```lua file=~/.config/nvim/lua/tailwind.lua
vim.lsp.config('tailwindcss', {
  filetypes = { 'lyr', 'html', 'css' },
  settings = { tailwindCSS = { includeLanguages = { lyr = 'html' } } },
})
vim.lsp.enable('tailwindcss')
```

`lyr fmt` leaves class order alone; Tailwind's Prettier plugin sorts classes if you want that.

## How gopls is found

`lyr lsp` starts a gopls process for your project. It looks for one in this order:

1. the `goplsPath` initialization option, if your editor sets it (the VS Code extension sets it from its `lyr.goplsPath` setting);
2. `gopls` on your `PATH`;
3. `go tool gopls`, if your project's `go.mod` pins gopls as a tool.

If none is found, the language server tells you how to install gopls.

It's passed in the LSP `initializationOptions` as `{ "goplsPath": "…" }`. For example, in Neovim:

```lua
vim.lsp.config('lyr', {
  init_options = { goplsPath = '/home/me/go/bin/gopls' },
})
```

Pinning gopls in the project is optional:

```sh
go get -tool golang.org/x/tools/gopls@v0.24.0
```

It's not the default, and it's only used when there's no `gopls` on your `PATH`. A pin pulls all of `golang.org/x/tools` into your module graph, which can raise the versions of dependencies you share with it. Its first build also takes 30–60 seconds, long enough for some editors to give up on the language server at startup. A gopls installed with `go install` avoids both.

Lyr passes your editor's `gopls` settings to its gopls, so options you set there (for example `buildFlags`) apply inside `.lyr` files too. It also sets `diagnosticsDelay` to `50ms` unless you set it yourself, so diagnostics follow your edits quickly, and turns gopls's internal `zeroConfig` option off (Lyr's gopls only ever sees your project's module), which makes opening hundreds of generated files at startup much cheaper.

## Old gopls versions

The recommended minimum is **gopls v0.24.0**, the version Lyr's editor support was verified with.

When it starts gopls, `lyr lsp` reads which Go version gopls was built with. If that's older than the `go` directive in your project's `go.mod`, gopls can't type-check your code correctly: it would reject newer language features as errors. So Lyr **refuses that gopls**:

- it shuts that gopls down;
- **Go features are disabled in `.lyr` files**: no Go completion, hover, Go diagnostics, definition, references or rename;
- **template features keep working**: completion of tags, component props, slot names and directives, and Lyr's own diagnostics (syntax errors, unknown props);
- your editor shows an error with the install command, pointing to `lyr doctor`.

Fix it by installing a current gopls, then restart the language server:

```sh
go install golang.org/x/tools/gopls@latest
```

A gopls built with a recent enough Go but older than v0.24.0 still runs; `lyr doctor` warns about it.

## Startup and `lyr_gen/`

The language server never writes to `lyr_gen/`. It generates the Go code for your `.lyr` files in memory and hands it to gopls, so it works in a fresh clone too.

With an up-to-date `lyr_gen/`, gopls reads the generated code of every file you haven't edited from disk, as it would for any Go project; only the files you're editing, and those changed since `lyr generate` last ran, are handed over from memory. On a 200-component project, Go features answer about 0.2 s after the editor starts. Without `lyr_gen/`, gopls first loads every component from memory, in one go: Go features answer about 0.4 s after the editor starts on the same project, and **references and rename wait** for that load, so they don't miss any uses. Your editor shows a progress indicator meanwhile. With only a few components missing from `lyr_gen/`, gopls loads those one by one instead; completion, hover and diagnostics don't wait for them. Keeping `lyr dev` running keeps `lyr_gen/` up to date.

## What completion covers

**Go**, through gopls, everywhere you write Go: the frontmatter, `{…}` expressions, attribute expressions, `{if}`/`{for}`/`{switch}` headers and `{case}` lists. You get completion (including `props.`, `ctx.`, `slots.`, your typed `locals.` and, in pages, the route's `params.`), hover, signature help, go-to-definition, references and rename, across your `.lyr` files and your `.go` packages. Accepting a completion for an unimported package adds the import to the frontmatter.

Completion after `lyr.` (or another name you import the runtime as), and on a `*lyr.Writer`, leaves out the runtime names that only generated code calls, those whose Go doc starts with "For generated code." (see [Runtime API](reference/runtime-api.md)). Hover and go to definition still work on them.

Inside a [template literal](reference/template-syntax.md#template-literals), each `${…}` piece is Go like any other expression, nested literals and pieces on later lines of a multi-line literal included: `` href={`/posts/${post.Slug}`} `` completes, hovers and renames `post` as `href={post.Slug}` would. The literal's text around the pieces is a string, so it gets no completion, as inside a quoted value. While you type, an empty `${}` already completes, and so does an unterminated literal, up to the end of its line.

**Templates**, natively:

- **Tag names** after `<`: the components you've imported first, then other components in the project (accepting one adds its `.lyr` import: through the shortest `tsconfig.json` alias that covers the file, such as `import Card "@/components/Card.lyr"`, as TypeScript's auto-import does, otherwise with a relative path, `import Card "../components/Card.lyr"`), then `<Fragment>` and HTML elements.
- **Props** on a component tag: its `Props` fields as attribute names (`post`, `featured`), with their types and doc comments. Accepting one inserts `post={…}`. Props you've already set aren't offered again.
- **HTML attributes** on HTML elements: the element's own and the global ones. On a [dynamic tag](reference/template-syntax.md#dynamic-tags) (`<Tag>` with `Tag := …`), the global ones. Its name is Go code: hover, go to definition and references go to the variable, through gopls.
- **Directives** where they're valid: `slot` on any tag; `set:html` and `set:text` on HTML elements and `<Fragment>`; `is:inline` on `<script>` and `<style>`; `is:global` on `<style>`; `set:json` on `<script type="application/json">`. On component tags, `server:defer`, `client:load`, `client:idle`, `client:visible` and `client:media` (they're errors on HTML elements, so they're never offered there). See [Lazy loading](lazy-loading.md).
- **Control blocks** after `{` in template content: `if`, `for` and `switch` (accepting `if` inserts `{if cond}{/if}`, with the cursor on `cond`), `else if`/`else` inside an `{if}`, `case`/`default` inside a `{switch}`, and the closer of the innermost open block (`{/` offers just that one). Where the `{` could also start an expression, gopls's items follow.
- **Slot names** inside `slot="…"`: the slots of the component you're passing content to. Inside a `server:defer` component, only `fallback`.
- **Go to definition** on `<Card` opens `Card.lyr`; on a component attribute, it jumps to the `Props` field.

**Diagnostics** appear at their position in the `.lyr` file as you type, with no save needed: Lyr's own (template syntax errors, unknown props, misused directives) and gopls's Go type errors. Changes are visible across files before you save: add a field to `Card`'s `Props`, and the new attribute is accepted in a page right away.

**Import paths**: inside the quotes of a frontmatter import, once you type `./` or `../`, completion offers the project's `.lyr` files and the Markdown files under `src/`, as paths relative to the file you're in (the file itself and those you've already imported aren't offered). In an import with no alias, `import "./`, accepting one also adds the alias: `import Post "./post.md"`. After `"/` it offers them as root-relative paths (`"/src/components/Card.lyr"`), and after a `tsconfig.json` alias prefix such as `"@/`, as alias paths (`"@/components/Card.lyr"`); go to definition and hover work on root-relative and alias paths as on relative ones (see [Import aliases](components.md#import-aliases)).

**TypeScript** in `<script>` blocks, through TypeScript 7: see [TypeScript in `<script>` blocks](#typescript-in-script-blocks).

Semantic highlighting is not supported: your editor colors `.lyr` files with the grammar.

## TypeScript in `<script>` blocks

With TypeScript 7 on your machine, `lyr lsp` gives the `<script>` blocks of your `.lyr` files the TypeScript features of a `.ts` file:

- completion, including auto-imports (accepting `plural` adds `import { plural } from "../scripts/util"` at the top of the script);
- hover and signature help;
- diagnostics as you type, at their place in the `.lyr` file (an unused import is faded, as in a `.ts` file);
- go-to-definition and type definition, references, document highlights and rename;
- quick fixes ("Add missing import", "Change spelling to …").

The data components pass to scripts is typed too, from the Go types: in `LikeButton.lyr`, `import { data } from "./LikeButton.lyr"` resolves, completion inside `data(this, "` offers the `data-*` keys written with `lyr.JSON`, and `data(this, "like").likes` has the TypeScript type of the Go field. These types follow your **unsaved** Go edits: rename a `json` tag or change a field's type in the frontmatter, and the script's completion and diagnostics update within about half a second, with nothing written to disk. Go-to-definition on a key goes to its `data-like={lyr.JSON(…)}` attribute, and on a field (`likes`) to the Go field that declares it, in the frontmatter or in your `.go` file. Renaming such a field from the script is refused: it's declared in Go, so rename the Go field or its `json` tag there.

References and rename cover every `.lyr` file's scripts, closed ones included: the first such request loads all of them once (about 35 ms for the 40 scripts of a 200-component project).

`<script is:inline>` blocks, and other scripts that run as they are (no `type`, or a JavaScript `type` such as `module`), are plain JavaScript: they get the same features as JavaScript, the way a `.js` file does, with the DOM's types but without the data types or module imports (the browser runs them as written). A file's classic inline scripts share the page's global scope, so a function one of them declares is known in the others. JSON scripts (`type="application/json"`, `set:json`, `application/ld+json`) get nothing.

Formatting a script is [`lyr fmt`](#formatting)'s job, not TypeScript's.

### How TypeScript is found

TypeScript 7 is the first TypeScript whose compiler is a native binary (no Node), with a language server built in (`tsc --lsp`). `lyr lsp` starts it the first time you open a `.lyr` file with a script, and looks for it in this order:

1. the `typescriptPath` initialization option, if your editor sets it (VS Code: the `lyr.typescriptPath` setting): a native `tsc` binary, or a `typescript` package directory;
2. your project's `node_modules/typescript`, if it's version 7 or later;
3. `tsc` on your `PATH` (an npm or Bun global install: `bun add -g typescript@7.0.2` or `npm i -g typescript@7.0.2`);
4. with [Bun](https://bun.sh) installed, `bun x --package typescript@7.0.2 tsc`: Bun downloads TypeScript 7.0.2 into its cache the first time (about 10 MB), and nothing else is needed.

Node is never run: for an npm install, Lyr starts the native binary that the `typescript` package ships in its platform package (`@typescript/typescript-linux-x64` and so on). A `tsc` older than 7.0.2, such as TypeScript 5 or 6, has no language server: it's skipped, and if nothing else is found, the language server shows a notice once, when you open a `.lyr` file with a script. `lyr doctor` reports which TypeScript it found.

### What TypeScript sees

The language server writes nothing: each script is handed to TypeScript from memory, as a TypeScript document under `.lyr/lsp/` (the `.lyr` file with everything outside the script blanked out, so positions line up), next to an in-memory copy of the `tsconfig.json` that [`lyr check`](styles-and-scripts.md#type-checking-with-lyr-check) generates. That config extends your `tsconfig.json` (your compiler options apply) and adds what scripts need: relative imports such as `../scripts/util`, `import { data } from "./X.lyr"` and the `lyr:client` and `lyr:types` modules resolve the same way as in `lyr check`, even if your `tsconfig.json` has no `rootDirs` entry for `lyr_gen/types`. The data types come from memory too, so they work on a fresh clone with no `lyr_gen/`.

TypeScript sees the `.ts` files under `src/` as they are on disk: an unsaved edit in a `.ts` file reaches your scripts once you save it.

### `.ts` files

Your editor's own TypeScript support (VS Code's built-in TypeScript server, or `typescript-language-server` in Neovim) covers the **`.ts` files** under `src/`, through your `tsconfig.json`. That includes the generated types of the data components pass to scripts: in `src/scripts/like-button.ts`, `import { data } from "../components/LikeButton.lyr"` is typed: completion offers the keys, and the result has the TypeScript type of the Go value. This needs the starter `tsconfig.json`'s `rootDirs` and `include` entries for `lyr_gen/types` (see [`tsconfig.json`](styles-and-scripts.md#tsconfigjson); `lyr doctor` warns when they're missing), and an `lyr_gen/types/` that `lyr dev` or `lyr generate` has written. Before that, on a fresh clone, the editor reports `Cannot find module '../components/LikeButton.lyr'`.

Lyr's language server leaves `.ts` files alone, as Astro's does. One consequence: "find references" on a function in a `.ts` file doesn't list its uses inside `.lyr` scripts (from a script, it lists both).

The language server doesn't write `lyr_gen/types/`; `lyr dev` keeps it current as you edit, and `lyr generate` writes it once. [`lyr check`](styles-and-scripts.md#type-checking-with-lyr-check) stays the check to run in CI: it type-checks every script and `.ts` file of the project, open in an editor or not, while the editor reports TypeScript diagnostics for the `.lyr` files you have open.

## Markdown files

The language server reports Lyr's errors and warnings in the Markdown files it compiles: the pages under `src/pages/`, and the `.md` files a `.lyr` file imports (see [Markdown](markdown.md)). They update as you type, with no save needed:

- frontmatter errors: YAML syntax, anchors and tags, a key with no value, a missing or misplaced `layout`, a key the layout's `Props` doesn't have;
- links to other Markdown files: a missing target is an error, a `#fragment` with no matching heading a warning;
- route errors: brackets in a page's path, a route another page already has.

A frontmatter value of the wrong type for its layout field (`draft: "yes"` for a `bool` field) is found by Go's type checker, which the language server runs in the background: when the editor starts, after `lyr dev` or `lyr generate` rewrites `lyr_gen/`, and about 300 ms after you stop typing in a Markdown file (or change the `Props` of a layout that a Markdown file uses, or a Go file). That error stays on its key while you edit the rest of the file; once you edit the key itself, it's hidden until the next check, a moment later. `lyr dev` and `lyr check` always show it.

Other Markdown files, such as a `README.md` that nothing imports, are ignored. Everything else about Markdown files (highlighting, preview, their own completion) stays with your editor's Markdown support: the VS Code extension sends `.md` files under `src/` to the language server for diagnostics only.

The language server also follows `.md` changes on disk, so in a `.lyr` file that imports `Post "./post.md"`, `Post.Frontmatter.` completes the frontmatter's fields.

## Rename

Rename works on Go identifiers in the frontmatter and in expressions, and updates every place that refers to them:

- **A frontmatter variable**: renaming `title` updates its declaration and every `{title}` in the template.
- **A `Props` field**: renaming `Post` to `Article` in `Card.lyr` also renames the attribute at every call site, keeping its spelling style: `post={p}` becomes `article={p}`, and `is-open` would become `is-closed` rather than `isClosed`. You can start the rename from the field's declaration, from a `props.Post` use, or from a `post=` attribute at a call site; there you type the new attribute name (`is-closed`), and the field becomes `IsClosed`.
- **A dynamic tag's variable**: renaming `Tag`, from its declaration or from a `<Tag>` or `</Tag>` tag, updates the declaration and both tags of every use.
- **A `.lyr` import alias**: renaming `Card`, from its import or from a `<Card>` or `</Card>` tag, updates the import and every `<Card>` tag in that file. The new alias must start with an uppercase letter.
- **Anything in your `.go` packages** that a `.lyr` file uses, such as `posts.Post`, is renamed in both.

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

Lyr refuses a rename, with a message:

- of a name the generated code declares rather than your file: `props`, `ctx`, `slots`, `locals`, `params`, and the `Props` type itself;
- of a `params` field, such as `params.Slug`: it comes from the file path (`[slug].lyr`), so rename the file instead;
- of a layout's `Props` field that a Markdown file's frontmatter sets: the language server doesn't edit Markdown files, so rename the field and the frontmatter key yourself (the message names the file and line);
- when it would have to change generated code that doesn't correspond to anything in your files;
- of a `Props` field to an unexported name (`article`): a parent couldn't set it.

Rename waits until the language server has loaded the whole project (see [Startup and `lyr_gen/`](#startup-and-lyr_gen)), so it never misses a use.

## Formatting

The language server formats `.lyr` files with the same rules as `lyr fmt`: the frontmatter, `{…}` expressions and control blocks with `gofmt`'s rules, and `<script>` and `<style>` content with `bun fmt` when Bun 1.4.4 or later is installed. It formats what's in the editor, saved or not. It never touches the HTML, so formatting never changes what a page renders.

- It formats the **whole document** ("Format Document"). "Format Selection" isn't supported, as with gopls.
- A file with a syntax error isn't changed; the error is already in your diagnostics.
- Your editor's tab size and spaces setting don't apply: the frontmatter uses tabs, like Go, the template keeps its own indentation, and scripts and styles follow your Prettier or Bun configuration.
- Without Bun, scripts and styles are left as they are, and the language server says so once per session.
- Markdown files aren't formatted by Lyr's language server: VS Code's Markdown support, Prettier or `bun fmt` handle them.

**VS Code**: the Lyr extension is the default formatter for `.lyr` files, so "Format Document" (Shift+Alt+F) works as installed. To format on save, add this to your settings:

```json
{
  "[lyr]": {
    "editor.formatOnSave": true
  }
}
```

**Neovim**: `vim.lsp.buf.format()` formats the current buffer. To format on save:

```lua
vim.api.nvim_create_autocmd('BufWritePre', {
  pattern = '*.lyr',
  callback = function() vim.lsp.buf.format() end,
})
```

To check formatting in CI, run `lyr fmt -l` (see [Fresh clones and CI](project-structure.md#fresh-clones-and-ci)).

## See also

- [Getting started](getting-started.md): installing Lyr and running `lyr doctor`
- [Components](components.md): what `props`, `slots`, `ctx` and `locals` are
- [CLI reference](reference/cli.md#lyr-lsp): `lyr lsp`, and [`lyr doctor`](reference/cli.md#lyr-doctor)'s checks
- [Styles and scripts](styles-and-scripts.md): TypeScript and `lyr check`
- [CLI reference](reference/cli.md#lyr-fmt): `lyr fmt`, the formatter
