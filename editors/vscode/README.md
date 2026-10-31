# Lyr for VS Code

Language support for [Lyr](https://github.com/orbit-z/lyr) `.lyr` components: Go frontmatter, an HTML template with `{…}` Go expressions, `<script>` in TypeScript and `<style>` in CSS.

## Features

- **Syntax highlighting**: the frontmatter and `{…}` expressions (including `{if}`/`{else if}`/`{else}`/`{/if}`, `{for}`/`{/for}`, `{switch}`/`{case}`/`{default}`/`{/switch}`, attribute values `a={…}`, the shorthand `{a}` and spreads `{...attrs}`) as Go, `<style>` as CSS, `<script>` as TypeScript (`<script type="application/json">` and `ld+json` as JSON), and the rest as HTML. Components (`<Card>`, `<Self>`, `<Fragment>`) and directives (`set:html`, `client:visible`, `server:defer`, `slot`, `is:inline`, …) get their own scopes. `{/* … */}` is a template comment; braces inside `<script>`, `<style>` and HTML comments aren't expressions.
- **Editing**: bracket matching, comment toggling (`//` in the frontmatter, `<!-- -->` in the template) and auto-closing pairs.
- **Language server**: the extension starts `lyr lsp`, which gives you Go completion, hover, diagnostics, go-to-definition, references and rename inside `.lyr` files (through gopls), plus completion of tags, component props, directives, control blocks (`{if}`, `{for}`, `{switch}` and their closers), slot names and import paths (`.lyr` and Markdown files), and formatting with `lyr fmt`'s rules ("Format Document"; the extension is the default formatter for `.lyr` files).
- **TypeScript in `<script>` blocks**: completion (with auto-imports), hover, signature help, diagnostics, go-to-definition, references, rename and quick fixes, from TypeScript 7's language server, which `lyr lsp` starts for you. The types of `lyr.JSON` data (`import { data } from "./X.lyr"`) follow your unsaved Go edits, and go-to-definition on a data key or field jumps to the Go code that declares it. `is:inline` and other plain JavaScript scripts get the same features as JavaScript. Needs TypeScript 7 (below); without it, scripts keep their highlighting and everything else works.
- **Markdown diagnostics**: for Markdown pages and imported `.md` files under `src/`, the language server reports Lyr's errors and warnings as you type (frontmatter, layout, links, routes). ```` ```lyr ```` code blocks in any Markdown file highlight as Lyr in the editor (not in the Markdown preview); highlighting, preview and every other Markdown feature stay VS Code's own; the extension sends Markdown files to `lyr lsp` for diagnostics only. See [Editor setup](https://github.com/orbit-z/lyr/blob/main/docs/editor-setup.md) for what it covers.

- **Tailwind CSS**: with the [Tailwind CSS IntelliSense](https://marketplace.visualstudio.com/items?itemName=bradlc.vscode-tailwindcss) extension installed, class completion, hover previews and linting work in `.lyr` files: this extension sets `"tailwindCSS.includeLanguages": { "lyr": "html" }` by default (it does nothing without Tailwind's extension). If you set `tailwindCSS.includeLanguages` yourself, your object replaces the default, so include `"lyr": "html"` in it. See [Using Tailwind](https://github.com/orbit-z/lyr/blob/main/docs/tailwind.md).

## Requirements

- The `lyr` CLI on your `PATH`:

  ```sh
  go install github.com/orbit-z/lyr/cmd/lyr@latest
  ```

  Inside a project that pins the CLI in `go.mod`, that `lyr` hands over to the pinned version, so the language server matches your project. Without a `lyr` on your `PATH`, the extension runs `go tool lyr` in a project that pins the CLI (as `lyr new` projects do).
- gopls v0.24.0 or later, built with a Go at least as recent as your project's `go` directive:

  ```sh
  go install golang.org/x/tools/gopls@latest
  ```

- Optional, for TypeScript features in `<script>` blocks: TypeScript 7.0.2 or later, its native `tsc` (no Node needed). `lyr lsp` finds the project's `node_modules/typescript`, or `tsc` on `PATH`, or runs `bun x --package typescript@7.0.2` if you have [Bun](https://bun.sh). To install it globally:

  ```sh
  bun add -g typescript@7.0.2   # or: npm i -g typescript@7.0.2
  ```

Run `lyr doctor` (or **Lyr: Run lyr doctor**) to check them.

## Settings

| Setting | Default | Description |
|---|---|---|
| `lyr.path` | `lyr` | The `lyr` binary. A bare name is looked up on `PATH`, a relative path is resolved against the first workspace folder, `~/` is your home directory. |
| `lyr.goplsPath` | (empty) | The gopls that `lyr lsp` starts (its `goplsPath` initialization option). Empty: `gopls` on `PATH`, then `go tool gopls` if `go.mod` pins it. |
| `lyr.typescriptPath` | (empty) | The TypeScript 7 that `lyr lsp` starts for `<script>` blocks (its `typescriptPath` initialization option): a native `tsc` binary or a `typescript` package directory. Empty: the project's `node_modules/typescript`, `tsc` on `PATH`, then `bun x`. |

```json
{
  "lyr.path": "/home/me/go/bin/lyr",
  "lyr.goplsPath": "/home/me/go/bin/gopls"
}
```

Changing any of these settings restarts the language server.

## Commands

- **Lyr: Restart Language Server**: stops and starts `lyr lsp`, for example after installing a new gopls.
- **Lyr: Run lyr generate**: regenerates `lyr_gen/`.
- **Lyr: Run lyr doctor**: checks your environment and project.

The last two run the same `lyr` as the language server, in the project root: the nearest directory above the active `.lyr` file (or else the workspace folder) that holds `go.mod` and `src/`. Their output goes to the **Lyr** output channel; a notification tells you whether they succeeded.

One language server runs per window. In a multi-root workspace, it serves the first Lyr project it finds (the one of the `.lyr` file active at startup, else the first workspace folder in one).

## Neovim

The language server works in any LSP client. With Neovim 0.11 or later:

```lua
vim.filetype.add({ extension = { lyr = 'lyr' } })

vim.lsp.config('lyr', {
  cmd = { 'lyr', 'lsp' },
  filetypes = { 'lyr' },
  root_markers = { 'go.mod' },
})
vim.lsp.enable('lyr')
```

To choose the TypeScript 7 that gives `<script>` blocks their features (optional; the server looks for one itself), add `init_options = { typescriptPath = '/path/to/tsc' }` (as for `goplsPath`).

The highlighting grammar is a TextMate grammar, which Neovim doesn't use. For Lyr's diagnostics in Markdown pages too, use `filetypes = { 'lyr', 'markdown' }`: the server ignores Markdown files Lyr doesn't compile, and answers other requests on Markdown files with nothing.

## Development

The extension uses [Bun](https://bun.sh) (1.4.4 or later):

```sh
bun install --frozen-lockfile
bun run test       # type check (bun check) + grammar snapshot tests
bun run package    # dist/extension.js and lyr-<version>.vsix
```

After changing the grammar, review the scope changes and refresh the snapshots with `bun run test:update`. To try it, run `bun run build`, then `code --extensionDevelopmentPath="$PWD"` (or open this folder in VS Code and press F5: `.vscode/launch.json` builds the extension and opens `examples/blog` with it).

## Releasing

Publishing is done by hand, by the maintainer, with two tokens:

- `VSCE_PAT`: an Azure DevOps personal access token with the **Marketplace (Manage)** scope, for the `orbit-z` publisher on the [VS Code Marketplace](https://marketplace.visualstudio.com/manage).
- `OVSX_PAT`: an [Open VSX](https://open-vsx.org) access token for the `orbit-z` namespace (used by VSCodium, Cursor and other VS Code-based editors).

Bump `version` in `package.json` and add a `CHANGELOG.md` entry, then:

```sh
bun run package    # builds lyr-<version>.vsix
VSCE_PAT=… OVSX_PAT=… bun run publish
```

`bun run publish` uploads that `.vsix` with `vsce publish --no-dependencies --packagePath lyr-<version>.vsix`, then `ovsx publish lyr-<version>.vsix`.

## License

MIT
