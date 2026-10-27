# Changelog

What's new in each Orbit release. The VS Code extension has its own changelog, in `editors/vscode/CHANGELOG.md`.

## v0.1.0

The first release of Orbit: Astro's developer experience, with Go on the server and much faster server rendering.

**Components and pages**
- `.orbit` single-file components: Go frontmatter between `---` lines, then an HTML template with `{…}` Go expressions, `{if}`, `{for}` and `{switch}` blocks, slots, `class:list`, pass-through attributes and recursive components.
- Compiled to type-checked Go: errors point at the `.orbit` line, and each expression is written by its type, with no reflection.
- `orbit.If(cond, v)` and `orbit.Cond(cond, a, b)` for conditional attribute values and text (compiled to an `if`, no allocation), and a `bool` writes `"true"`/`"false"` on `aria-*` and the other true/false attributes.
- File-based routing under `src/pages`, with dynamic and catch-all routes, typed route params (`params.Slug`), a custom `404.orbit` and no trailing slash by default (`"trailingSlash": "always"` mirrors it).
- `orbit.CSS` marks trusted CSS: a `style={…}` value of that type, or a constant, no longer warns (pass-through `style`/`on*` attributes now warn too, at the call site).
- `set:html` and `set:text` on `<script>` and `<style>` write the content in place, unprocessed, as in Astro (for example a theme script that must run before paint).
- Markdown pages and imports (`.md`), with typed frontmatter checked against the layout's props and code blocks highlighted at build time.
- Component imports and Markdown layouts can use `tsconfig.json` path aliases, as in Astro: `Card "@/components/Card.orbit"`.
- Import paths starting with `/` resolve from the project root, as in Astro and Vite: `Card "/src/components/Card.orbit"`. Accepting an unimported component in tag completion now uses its `tsconfig.json` alias when one covers it.

**Server**
- Server rendering only: every page renders per request.
- Orbit is `net/http` middleware: your `cmd/server/main.go` decides where it sits and which routes win. Typed request locals carry data from your middleware to your pages.
- Responses are buffered from pooled buffers: rendering a typical page allocates nothing in Orbit's code.
- `orbit.All` loads a page's data concurrently, `server:concurrent` renders slow components in parallel, and `server:defer` sends a page at once and fetches a slow component afterwards (props encrypted).
- An error handler hook maps your errors to your own responses; production 500s log the `.orbit` position.

**Styles and scripts**
- Scoped CSS, global styles and Tailwind CSS v4 (through its standalone CLI, no Node).
- Vanilla `<script>` blocks in TypeScript, bundled with esbuild inside the CLI, with shared chunks and `import()`. `client:load`, `client:idle`, `client:visible` and `client:media` load component scripts lazily. No framework islands.
- Typed server→client data: `orbit.JSON` attributes and `set:json`, read in scripts with types generated from Go.

**Tools**
- `orbit new`, `orbit dev` (rebuild and live reload in about 0.2 s on a 200-file project), `orbit build` (with `--embed` for a single binary), `orbit check` (TypeScript checking with Bun), `orbit fmt` and `orbit doctor`.
- `orbit lsp`: completion, diagnostics, hover, go-to-definition, references and rename in `.orbit` files, through gopls, and TypeScript in `<script>` blocks. A VS Code extension (`orbit-z.orbit`) runs it.
- `orbittest` helpers for testing pages and components with `go test`.
- `orbit new` scaffolds a Dockerfile for a distroless single-binary image.

**Performance**, measured against Astro on the same 500-card page: about 14.6× the pages per CPU, and 57× at all cores against Astro's single Node process. See the [performance page](docs/performance.md#results) for the methodology and every result.
