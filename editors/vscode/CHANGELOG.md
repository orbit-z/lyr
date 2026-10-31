# Changelog

## 0.1.0

The first release.

- The `lyr` language for `*.lyr` files: syntax highlighting (Go frontmatter and `{…}` expressions, HTML, `<style>` as CSS, `<script>` as TypeScript), bracket matching, comment toggling and auto-closing.
- Template literals (`` `/posts/${slug}` ``) in template expressions highlight as strings with their `${…}` pieces as Go, nested literals included; the frontmatter keeps Go's raw strings.
- Starts `lyr lsp` for completion, diagnostics, go-to-definition and rename.
- Settings `lyr.path` and `lyr.goplsPath`.
- Commands: Lyr: Restart Language Server, Lyr: Run lyr generate, Lyr: Run lyr doctor.
- Tailwind CSS: the extension sets `"tailwindCSS.includeLanguages": { "lyr": "html" }` by default, so the Tailwind CSS IntelliSense extension completes, previews and lints classes in `.lyr` files. A `tailwindCSS.includeLanguages` of your own replaces it: include `"lyr": "html"`.
- TypeScript in `<script>` blocks: completion, hover, signature help, diagnostics, go-to-definition (into the Go behind `lyr.JSON` data), references, rename and quick fixes, through TypeScript 7's language server, which `lyr lsp` starts. `is:inline` scripts get them as JavaScript. Setting `lyr.typescriptPath`.
- Formatting: "Format Document" formats `.lyr` files with `lyr fmt`'s rules (through `lyr lsp`), and the extension is the default formatter for `[lyr]`. Format on save stays off unless you turn it on.
- Markdown files under `src/` are sent to `lyr lsp` for Lyr's diagnostics (frontmatter, layout, links, routes) as you type. Every other Markdown feature stays VS Code's.
- The extension activates in any workspace that contains a `.lyr` file, so Markdown diagnostics work before a `.lyr` file is opened.
- ```` ```lyr ```` code blocks in Markdown files highlight as Lyr in the editor (a `---` line in the block opens Go frontmatter). VS Code's Markdown preview keeps its own highlighter.
- Import path completion offers `.lyr` and Markdown files (`/` triggers it).
- Highlighting for `{switch}` blocks: `{switch …}`, `{case …}`, `{default}` and `{/switch}`, with the header and case lists as Go.
- `class:list` highlights as a directive, with its list as Go; completion offers it on HTML elements and on components with a `class` prop.
