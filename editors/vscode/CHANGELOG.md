# Changelog

## Unreleased

- Markdown files under `src/` are sent to `orbit lsp` for Orbit's diagnostics (frontmatter, layout, links, routes) as you type. Every other Markdown feature stays VS Code's.
- Import path completion offers `.orbit` and Markdown files (`/` is a new completion trigger character).

## 0.1.0

- The `orbit` language for `*.orbit` files: syntax highlighting (Go frontmatter and `{…}` expressions, HTML, `<style>` as CSS, `<script>` as TypeScript), bracket matching, comment toggling and auto-closing.
- Starts `orbit lsp` for completion, diagnostics, go-to-definition and rename.
- Settings `orbit.path` and `orbit.goplsPath`.
- Commands: Orbit: Restart Language Server, Orbit: Run orbit generate, Orbit: Run orbit doctor.
