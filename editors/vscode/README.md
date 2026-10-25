# Orbit for VS Code

Language support for [Orbit](https://github.com/bertrandkhe/orbit) `.orbit` components: Go frontmatter, an HTML template with `{…}` Go expressions, `<script>` in TypeScript and `<style>` in CSS.

## Features

- **Syntax highlighting**: the frontmatter and `{…}` expressions (including `{if}`/`{else if}`/`{else}`/`{/if}`, `{for}`/`{/for}`, attribute values `a={…}`, the shorthand `{a}` and spreads `{...attrs}`) as Go, `<style>` as CSS, `<script>` as TypeScript (`<script type="application/json">` and `ld+json` as JSON), and the rest as HTML. Components (`<Card>`, `<Self>`, `<Fragment>`) and directives (`set:html`, `client:visible`, `server:defer`, `slot`, `is:inline`, …) get their own scopes. `{/* … */}` is a template comment; braces inside `<script>`, `<style>` and HTML comments aren't expressions.
- **Editing**: bracket matching, comment toggling (`//` in the frontmatter, `<!-- -->` in the template) and auto-closing pairs.
- **Language server**: the extension starts `orbit lsp`, which gives you Go completion, hover, diagnostics, go-to-definition, references and rename inside `.orbit` files (through gopls), plus completion of tags, component props, directives and slot names. See [Editor setup](https://github.com/bertrandkhe/orbit/blob/main/docs/editor-setup.md) for what it covers.

## Requirements

- The `orbit` CLI on your `PATH`:

  ```sh
  go install github.com/bertrandkhe/orbit/cmd/orbit@latest
  ```

  Inside a project that pins the CLI in `go.mod`, that `orbit` hands over to the pinned version, so the language server matches your project.
- gopls v0.24.0 or later, built with a Go at least as recent as your project's `go` directive:

  ```sh
  go install golang.org/x/tools/gopls@latest
  ```

Run `orbit doctor` (or **Orbit: Run orbit doctor**) to check both.

## Settings

| Setting | Default | Description |
|---|---|---|
| `orbit.path` | `orbit` | The `orbit` binary. A bare name is looked up on `PATH`, a relative path is resolved against the first workspace folder, `~/` is your home directory. |
| `orbit.goplsPath` | (empty) | The gopls that `orbit lsp` starts (its `goplsPath` initialization option). Empty: `gopls` on `PATH`, then `go tool gopls` if `go.mod` pins it. |

```json
{
  "orbit.path": "/home/me/go/bin/orbit",
  "orbit.goplsPath": "/home/me/go/bin/gopls"
}
```

Changing either setting restarts the language server.

## Commands

- **Orbit: Restart Language Server**: stops and starts `orbit lsp`, for example after installing a new gopls.
- **Orbit: Run orbit generate**: regenerates `orbit_gen/`.
- **Orbit: Run orbit doctor**: checks your environment and project.

The last two run the same `orbit` as the language server, in the project root: the nearest directory above the active `.orbit` file (or else the workspace folder) that holds `go.mod` and `src/`. Their output goes to the **Orbit** output channel; a notification tells you whether they succeeded.

One language server runs per window. In a multi-root workspace, it serves the first Orbit project it finds (the one of the `.orbit` file active at startup, else the first workspace folder in one).

## Neovim

The language server works in any LSP client. With Neovim 0.11 or later:

```lua
vim.filetype.add({ extension = { orbit = 'orbit' } })

vim.lsp.config('orbit', {
  cmd = { 'orbit', 'lsp' },
  filetypes = { 'orbit' },
  root_markers = { 'go.mod' },
})
vim.lsp.enable('orbit')
```

The highlighting grammar is a TextMate grammar, which Neovim doesn't use.

## Development

The extension uses [Bun](https://bun.sh) (1.4.4 or later):

```sh
bun install --frozen-lockfile
bun run test       # type check (bun check) + grammar snapshot tests
bun run package    # dist/extension.js and orbit-<version>.vsix
```

After changing the grammar, review the scope changes and refresh the snapshots with `bun run test:update`. To try it, run `bun run build`, then `code --extensionDevelopmentPath="$PWD"` (or press F5 in VS Code with this folder open and pick the extension development debugger).

## Releasing

Publishing is done by hand, by the maintainer, with two tokens:

- `VSCE_PAT`: an Azure DevOps personal access token with the **Marketplace (Manage)** scope, for the `bertrandkhe` publisher on the [VS Code Marketplace](https://marketplace.visualstudio.com/manage).
- `OVSX_PAT`: an [Open VSX](https://open-vsx.org) access token for the `bertrandkhe` namespace (used by VSCodium, Cursor and other VS Code-based editors).

Bump `version` in `package.json` and add a `CHANGELOG.md` entry, then:

```sh
bun run package    # builds orbit-<version>.vsix
VSCE_PAT=… OVSX_PAT=… bun run publish
```

`bun run publish` uploads that `.vsix` with `vsce publish --no-dependencies --packagePath orbit-<version>.vsix`, then `ovsx publish orbit-<version>.vsix`.

## License

MIT
