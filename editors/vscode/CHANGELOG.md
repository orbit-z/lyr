# Changelog

## Unreleased

- TypeScript in `<script>` blocks: completion, hover, signature help, diagnostics, go-to-definition (into the Go behind `orbit.JSON` data), references, rename and quick fixes, through TypeScript 7's language server, which `orbit lsp` starts. `is:inline` scripts get them as JavaScript. New setting `orbit.typescriptPath`.

- Formatting: "Format Document" formats `.orbit` files with `orbit fmt`'s rules (through `orbit lsp`), and the extension is the default formatter for `[orbit]`. Format on save stays off unless you turn it on.
- Markdown files under `src/` are sent to `orbit lsp` for Orbit's diagnostics (frontmatter, layout, links, routes) as you type. Every other Markdown feature stays VS Code's.
- The extension activates in any workspace that contains an `.orbit` file, so Markdown diagnostics work before an `.orbit` file is opened.
- Import path completion offers `.orbit` and Markdown files (`/` is a new completion trigger character).
- Highlighting for `{switch}` blocks: `{switch …}`, `{case …}`, `{default}` and `{/switch}`, with the header and case lists as Go.

## 0.1.0

- The `orbit` language for `*.orbit` files: syntax highlighting (Go frontmatter and `{…}` expressions, HTML, `<style>` as CSS, `<script>` as TypeScript), bracket matching, comment toggling and auto-closing.
- Starts `orbit lsp` for completion, diagnostics, go-to-definition and rename.
- Settings `orbit.path` and `orbit.goplsPath`.
- Commands: Orbit: Restart Language Server, Orbit: Run orbit generate, Orbit: Run orbit doctor.
