# Lyr for Neovim and Vim

Filetype detection, syntax highlighting and language-server configuration for Lyr's `.lyr` files. Neovim 0.11 or later; the highlighting also works in Vim 9.

## Install

The `lyr` CLI embeds these files and installs them into the editor's start package, so there's nothing to configure:

```sh
lyr editor nvim   # Neovim 0.11+: highlighting and the language server
lyr editor vim    # Vim 9: highlighting
```

See [Editor setup](../../docs/editor-setup.md#neovim) and [`lyr editor`](../../docs/reference/cli.md#lyr-editor).

### Working on the plugin

To try changes without reinstalling, put this directory first on the `runtimepath` (and don't keep an installed copy, which would load too):

```lua
vim.opt.rtp:prepend('/path/to/lyr/editors/nvim')
```

## Contents

- `ftdetect/lyr.vim`: `*.lyr` files get the `lyr` filetype.
- `syntax/lyr.vim`: highlighting built on Vim's bundled syntax files: HTML for the template, Go for the frontmatter and `{…}`, TypeScript for `<script>`, JavaScript for `<script is:inline>`, JSON for `<script type="application/json">`, CSS for `<style>`. A missing bundled file only loses that part's colors.
- `ftplugin/lyr.vim`: `commentstring` (`<!-- %s -->`) and matchit pairs for `{if}`/`{else}`/`{/if}`, `{for}`, `{switch}` and HTML tags.
- `lsp/lyr.lua`: the `lyr` language-server config for `vim.lsp.enable`.
- `plugin/lyr.lua`: enables that config on Neovim 0.11+ (unless `vim.g.lyr_lsp = false`); a no-op in Vim.
- `nvim.go`: embeds the files above (only those) for `lyr editor`, and installs them.

## Tests

```sh
editors/nvim/test/run.sh                       # Neovim and Vim, when installed
LYR_TEST_LINES=500 editors/nvim/test/run.sh    # also times a 500-line file
```

`go test ./editors/nvim` runs the same script, and `go test ./cmd/lyr -run TestEditorInstalled` starts Neovim and Vim on an install made by `lyr editor`. The checks in `test/syntax.vim` assert the syntax item at positions of `test/constructs.lyr` and of the VS Code grammar's samples in `editors/vscode/test/`.
