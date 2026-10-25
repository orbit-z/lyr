# Session old (/home/bertrandkhe/go/bin/gopls)
serverInfo: {"name":"gopls","version":"{\"GoVersion\":\"go1.23.4\",\"Path\":\"golang.org/x/tools/gopls\",\"Main\":{\"Path\":\"golang.org/x/tools/gopls\",\"Version\":\"v0.18.1\",\"Sum\":\"h1:2xJBNzdImS5u/kV/ZzqDLSvlBSeZX+pWY9uKVP7Pask=\",\"Replace\":null},\"Deps\":[{\"Path\":\"github.com/BurntSushi/toml\",\"Version\":\"v1.4.1-0.20240526193622-a339e1f7089c\",\"Sum\":\"h1:pxW6RcqyfI9/kWtOwnv/G+AzdKuy2ZrqINhenH4HyNs=\",\"Replace\":null},{\"Path\":\"github.com/google/go-cmp\",\"Version\":\"v0.6.0\",\"Sum\":\"h1:ofyhxvXcZhMsU5ulbFiLKl/XBFqE1GSq7atu8tAmTRI=\",\"Replace\":null},{\"Path\":\"golang.org/x/exp/typeparams\",\"Version\":\"v0.0.0-20241210194714-1829a127f884\",\"Sum\":\"h1:1xaZTydL5Gsg78QharTwKfA9FY9CZ1VQj6D/AZEvHR0=\",\"Replace\":null},{\"Path\":\"golang.org/x/mod\",\"Version\":\"v0.23.0\",\"Sum\":\"h1:Zb7khfcRGKk+kqfxFaP5tZqCnDZMjC5VtUBs87Hr6QM=\",\"Replace\":null},{\"Path\":\"golang.org/x/sync\",\"Version\":\"v0.11.0\",\"Sum\":\"h1:GGz8+XQP4FvTTrjZPzNKTMFtSXH80RAzG+5ghFPgK9w=\",\"Replace\":null},{\"Path\":\"golang.org/x/telemetry\",\"Version\":\"v0.0.0-20241220003058-cc96b6e0d3d9\",\"Sum\":\"h1:L2k9GUV2TpQKVRGMjN94qfUMgUwOFimSQ6gipyJIjKw=\",\"Replace\":null},{\"Path\":\"golang.org/x/text\",\"Version\":\"v0.22.0\",\"Sum\":\"h1:bofq7m3/HAFvbF51jz3Q9wLg3jkvSPuiZu/pD1XwgtM=\",\"Replace\":null},{\"Path\":\"golang.org/x/tools\",\"Version\":\"v0.30.1-0.20250221230316-5055f70f240c\",\"Sum\":\"h1:Ja/5gV5a9Vvho3p2NC/T2TtxhHjrWS/2DvCKMvA0a+Y=\",\"Replace\":null},{\"Path\":\"golang.org/x/vuln\",\"Version\":\"v1.1.3\",\"Sum\":\"h1:NPGnvPOTgnjBc9HTaUx+nj+EaUYxl5SJOWqaDYGaFYw=\",\"Replace\":null},{\"Path\":\"honnef.co/go/tools\",\"Version\":\"v0.5.1\",\"Sum\":\"h1:4bH5o3b5ZULQ4UrBmP+63W9r7qIkqJClEA9ko5YKx+I=\",\"Replace\":null},{\"Path\":\"mvdan.cc/gofumpt\",\"Version\":\"v0.7.0\",\"Sum\":\"h1:bg91ttqXmi9y2xawvkuMXyvAA/1ZGJqYAEGjXuP0JXU=\",\"Replace\":null},{\"Path\":\"mvdan.cc/xurls/v2\",\"Version\":\"v2.5.0\",\"Sum\":\"h1:lyBNOm8Wo71UknhUs4QTFUNNMyxy2JEIaKKo0RWOh+8=\",\"Replace\":null}],\"Settings\":[{\"Key\":\"-buildmode\",\"Value\":\"exe\"},{\"Key\":\"-compiler\",\"Value\":\"gc\"},{\"Key\":\"CGO_ENABLED\",\"Value\":\"1\"},{\"Key\":\"CGO_CFLAGS\",\"Value\":\"\"},{\"Key\":\"CGO_CPPFLAGS\",\"Value\":\"\"},{\"Key\":\"CGO_CXXFLAGS\",\"Value\":\"\"},{\"Key\":\"CGO_LDFLAGS\",\"Value\":\"\"},{\"Key\":\"GOARCH\",\"Value\":\"amd64\"},{\"Key\":\"GOOS\",\"Value\":\"linux\"},{\"Key\":\"GOAMD64\",\"Value\":\"v1\"}],\"Version\":\"v0.18.1\"}"}, encoding utf-16
diagnostics:
- `go.mod` v0: 1 diagnostics
  params:
```json
{
  "diagnostics": [
    {
      "message": "github.com/bertrandkhe/orbit is not used in this module",
      "range": {
        "end": {
          "character": 43,
          "line": 6
        },
        "start": {
          "character": 0,
          "line": 6
        }
      },
      "severity": 2,
      "source": "go mod tidy"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/go.mod"
}
```
- `orbit_gen/decl/components/card/decl.go` v1: 0 diagnostics
- `orbit_gen/decl/pages/index/decl.go` v1: 0 diagnostics
- `orbit_gen/impl/components/card/render.go` v1: 0 diagnostics
- `orbit_gen/impl/pages/index/render.go` v1: 0 diagnostics
### Card.Props{|}
request: `orbit_gen/impl/pages/index/render.go` at 45:31 (0-based, utf-16), latency 2ms
isIncomplete=true, 58 items: [Post Featured Post.Slug Post.Title posts.Post{} orbit posts orbit__complete orbit__render Card Orbit__Render Props Render Prerender any …(+43)]
item Post:
```json
{
  "detail": "posts.Post",
  "documentation": {
    "kind": "markdown",
    "value": ""
  },
  "filterText": "Post",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Post",
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 31,
        "line": 45
      },
      "start": {
        "character": 31,
        "line": 45
      }
    },
    "newText": "Post: ${1:}",
    "replace": {
      "end": {
        "character": 31,
        "line": 45
      },
      "start": {
        "character": 31,
        "line": 45
      }
    }
  }
}
```

## Server→client requests and notifications

- request `client/registerCapability` ×2, first params: `{"registrations":[{"id":"workspace/didChangeWatchedFiles-0","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{mod,work}","kind":7},{"globPattern":{"baseUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","pattern":"**/*.{go,mod,sum,work}"},"kind":7}]}}]}`
- request `window/workDoneProgress/create` ×1, first params: `{"token":"4691889261961864746"}`
- request `workspace/configuration` ×1, first params: `{"items":[{"scopeUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","section":"gopls"}]}`
- notification `$/progress` ×2
- notification `textDocument/publishDiagnostics` ×5
- notification `window/logMessage` ×4

## Go 1.26 feature `new(expr)` in frontmatter, gopls /home/bertrandkhe/go/bin/gopls

page impl diagnostics: 1
params:
```json
{
  "diagnostics": [
    {
      "code": "NotAType",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#NotAType"
      },
      "message": "\"Héllo ✓\" is not a type",
      "range": {
        "end": {
          "character": 69,
          "line": 22
        },
        "start": {
          "character": 60,
          "line": 22
        }
      },
      "severity": 1,
      "source": "compiler"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
  "version": 1
}
```

## Go 1.26 feature `new(expr)` in frontmatter, gopls /home/bertrandkhe/Projects/rs-astro/spike/lsp/bin/gopls

page impl diagnostics: 0
