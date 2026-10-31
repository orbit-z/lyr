# Session utf16 (no positionEncodings offered, documentChanges=false)
positionEncoding in initialize result: <nil> (nil = field absent → utf-16)
### hover on `Props` after non-ASCII (utf-16 range)
request `textDocument/hover` {"position":{"line":45,"character":26},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/impl/pages/index/render.go"}} (latency 17ms)
result:
```json
{
  "contents": {
    "kind": "markdown",
    "value": "```go\ntype Props struct {\n\tPost     posts.Post\n\tFeatured bool\n}\n```\n\n---\n\nline decl.go:13:24\n\n\n---\n\n[`card.Props` on pkg.go.dev](https://pkg.go.dev/example.com/blog/lyr_gen/decl/components/card#Props)"
  },
  "range": {
    "end": {
      "character": 30,
      "line": 45
    },
    "start": {
      "character": 25,
      "line": 45
    }
  }
}
```
expected Props start utf-16 {45 25} / utf-8 {45 28}
### rename `Featured` → `Highlighted` (documentChanges=false), issued right after the page impl's first diagnostics
request `textDocument/rename` {"newName":"Highlighted","position":{"line":11,"character":5},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/decl/components/card/decl.go"}} (latency 0s)
result:
```json
{
  "documentChanges": [
    {
      "edits": [
        {
          "newText": "Highlighted",
          "range": {
            "end": {
              "character": 12,
              "line": 11
            },
            "start": {
              "character": 4,
              "line": 11
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/decl/components/card/decl.go",
        "version": 1
      }
    },
    {
      "edits": [
        {
          "newText": "Highlighted",
          "range": {
            "end": {
              "character": 59,
              "line": 34
            },
            "start": {
              "character": 51,
              "line": 34
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/impl/pages/index/render.go",
        "version": 1
      }
    }
  ]
}
```
diagnostics after settle:
- `go.mod` v0: 1 diagnostics
  params:
```json
{
  "diagnostics": [
    {
      "message": "github.com/bertrandkhe/lyr is not used in this module",
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
- `lyr_gen/decl/components/card/decl.go` v1: 0 diagnostics
- `lyr_gen/decl/pages/index/decl.go` v1: 0 diagnostics
- `lyr_gen/impl/components/card/render.go` v1: 0 diagnostics
- `lyr_gen/impl/pages/index/render.go` v1: 0 diagnostics
### same rename after diagnostics settled
request `textDocument/rename` {"newName":"Highlighted","position":{"line":11,"character":5},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/decl/components/card/decl.go"}} (latency 9ms)
result:
```json
{
  "documentChanges": [
    {
      "edits": [
        {
          "newText": "Highlighted",
          "range": {
            "end": {
              "character": 12,
              "line": 11
            },
            "start": {
              "character": 4,
              "line": 11
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/decl/components/card/decl.go",
        "version": 1
      }
    },
    {
      "edits": [
        {
          "newText": "Highlighted",
          "range": {
            "end": {
              "character": 59,
              "line": 34
            },
            "start": {
              "character": 51,
              "line": 34
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/impl/pages/index/render.go",
        "version": 1
      }
    },
    {
      "edits": [
        {
          "newText": "Highlighted",
          "range": {
            "end": {
              "character": 66,
              "line": 25
            },
            "start": {
              "character": 58,
              "line": 25
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/lyr_gen/impl/components/card/render.go",
        "version": 1
      }
    }
  ]
}
```
client offers [utf-16, utf-8] → positionEncoding <absent>
client offers [utf-32, utf-8] → positionEncoding <absent>

## Server→client requests and notifications

- request `client/registerCapability` ×2, first params: `{"registrations":[{"id":"workspace/didChangeWatchedFiles-0","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{mod,work}","kind":7},{"globPattern":{"baseUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","pattern":"**/*.{go,mod,sum,work}"},"kind":7}]}}]}`
- request `window/workDoneProgress/create` ×1, first params: `{"token":"4562441431551715228"}`
- request `workspace/configuration` ×1, first params: `{"items":[{"scopeUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","section":"gopls"}]}`
- notification `$/progress` ×2
- notification `textDocument/publishDiagnostics` ×5
- notification `window/logMessage` ×4
