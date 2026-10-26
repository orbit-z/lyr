# Orbit documentation

Orbit is a server-rendered (SSR) web framework with Astro's developer experience and Go on the server. You write `.orbit` components (Go frontmatter plus an HTML template); Orbit compiles them to fast, type-checked Go.

## Start here
- [Getting started](getting-started.md): install, create a project, run the dev server, build
- [Project structure](project-structure.md)
- [Editor setup](editor-setup.md): VS Code, Neovim, gopls
- [Coming from Astro](coming-from-astro.md): how Astro concepts map to Orbit
- [Example blog](../examples/blog/README.md): a small site that uses the features these pages describe

## Guides
- [Components](components.md): frontmatter, props, templates, slots, recursion, escaping
- [Routing](routing.md): file-based routes, dynamic routes, 404
- [Markdown](markdown.md): `.md` pages, layouts, importing Markdown, code highlighting
- [Rendering](rendering.md): the request lifecycle, status codes, headers, redirects, errors
- [Server](server.md): Orbit as `net/http` middleware, custom routes, precedence
- [Middleware and locals](middleware.md): net/http middleware, typed `Locals`
- [Styles and scripts](styles-and-scripts.md): scoped CSS, TypeScript, bundling, passing data to scripts
- [Lazy loading](lazy-loading.md): `server:defer` server islands, `client:*` script loading
- [Dev server](dev-server.md)
- [Testing](testing.md): `orbittest`
- [Deploy](deploy.md): the server binary, single-binary `--embed`, Docker
- [Configuration](configuration.md): `orbit.json`
- [Performance](performance.md)

## Reference
- [Template syntax](reference/template-syntax.md)
- [Runtime API](reference/runtime-api.md): the `orbit` package
- [CLI](reference/cli.md)
- [Troubleshooting](troubleshooting.md): common errors and how to fix them

## Decisions
- [Decisions log](DECISIONS.md): the small decisions made by rule, to skim and override
