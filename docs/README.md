# Orbit documentation

Orbit is a server-rendered (SSR) web framework with Astro's developer experience and Go on the server. You write `.orbit` components (Go frontmatter plus an HTML template); Orbit compiles them to fast, type-checked Go.

## Start here
- [Getting started](getting-started.md): Install, create a project, run the dev server, build
- [Project structure](project-structure.md): Every directory, generated code, fresh clones and CI
- [Editor setup](editor-setup.md): VS Code, Neovim, gopls
- [Coming from Astro](coming-from-astro.md): How Astro concepts map to Orbit
- [Example blog](../examples/blog/README.md): A small site that uses the features these pages describe

## Guides
- [Components](components.md): Frontmatter, props, templates, slots, recursion, escaping
- [Routing](routing.md): File-based routes, dynamic routes, 404
- [Markdown](markdown.md): `.md` pages, layouts, importing Markdown, code highlighting
- [Rendering](rendering.md): The request lifecycle, status codes, headers, redirects, errors
- [Loading data](data-loading.md): Page loads with `orbit.All`, per-request loaders, slow data
- [Server](server.md): Orbit as `net/http` middleware, custom routes, precedence
- [Middleware and locals](middleware.md): Plain `net/http` middleware, typed `Locals`
- [Styles and scripts](styles-and-scripts.md): Scoped CSS, TypeScript, bundling, passing data to scripts
- [Using Tailwind](tailwind.md): Tailwind CSS v4, `@apply` in components, fonts, plugins, editors
- [Lazy loading](lazy-loading.md): `server:defer` server islands, `client:*` script loading
- [Dev server](dev-server.md): `orbit dev`, live reload, error overlay, rebuild times
- [Testing](testing.md): Component tests with `orbittest`
- [Deploy](deploy.md): The server binary, single-binary `--embed`, Docker
- [Configuration](configuration.md): Every `orbit.json` option
- [Performance](performance.md): Benchmarks against Astro, and how Orbit stays fast

## Reference
- [Template syntax](reference/template-syntax.md): Expressions, control flow, elements, attributes, directives
- [Runtime API](reference/runtime-api.md): The `orbit` package
- [Client modules](reference/client-modules.md): Reading server data in scripts, typed from Go
- [CLI](reference/cli.md): Every `orbit` command and flag
- [Troubleshooting](troubleshooting.md): Common errors and how to fix them
- [Changelog](../CHANGELOG.md): What's new in each release
