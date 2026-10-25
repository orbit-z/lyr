# Session main (gopls /home/bertrandkhe/Projects/rs-astro/spike/lsp/bin/gopls, overlay-only, client offers positionEncodings [utf-8], documentChanges=true)

## 5. Position encoding

negotiated positionEncoding: **utf-16**
initialize result (trimmed):
```json
{
  "capabilities": {
    "callHierarchyProvider": true,
    "codeActionProvider": true,
    "codeLensProvider": {},
    "completionProvider": {
      "triggerCharacters": [
        "."
      ]
    },
    "definitionProvider": true,
    "documentFormattingProvider": true,
    "documentHighlightProvider": true,
    "documentLinkProvider": {},
    "documentSymbolProvider": true,
    "executeCommandProvider": {
      "commands": [
        "gopls.add_dependency",
        "gopls.add_import",
        "gopls.add_telemetry_counters",
        "gopls.add_test",
        "gopls.apply_fix",
        "gopls.assembly",
        "gopls.change_signature",
        "gopls.check_upgrades",
        "gopls.client_open_url",
        "gopls.diagnose_files",
        "gopls.doc",
        "gopls.dragon_slayer",
        "gopls.edit_go_directive",
        "gopls.extract_to_new_file",
        "gopls.fetch_vulncheck_result",
        "gopls.free_symbols",
        "gopls.gc_details",
        "gopls.generate",
        "gopls.go_get_package",
        "gopls.implement_interface",
        "gopls.list_imports",
        "gopls.list_known_packages",
        "gopls.lsp",
        "gopls.maybe_prompt_for_telemetry",
        "gopls.mem_stats",
        "gopls.modify_tags",
        "gopls.modules",
        "gopls.move_declaration",
        "gopls.move_type",
        "gopls.package_symbols",
        "gopls.packages",
        "gopls.regenerate_cgo",
        "gopls.remove_dependency",
        "gopls.reset_go_mod_diagnostics",
        "gopls.resolve_target",
        "gopls.run_go_work_command",
        "gopls.run_govulncheck",
        "gopls.run_tests",
        "gopls.scan_imports",
        "gopls.split_package",
        "gopls.start_debugging",
        "gopls.start_profile",
        "gopls.stop_profile",
        "gopls.tidy",
        "gopls.update_go_sum",
        "gopls.upgrade_dependency",
        "gopls.vendor",
        "gopls.views",
        "gopls.vulncheck",
        "gopls.workspace_stats"
      ]
    },
    "experimental": {
      "interactiveResolveProvider": {
        "kinds": [
          "command"
        ]
      }
    },
    "foldingRangeProvider": true,
    "hoverProvider": true,
    "implementationProvider": true,
    "inlayHintProvider": {},
    "referencesProvider": true,
    "renameProvider": {
      "prepareProvider": true
    },
    "selectionRangeProvider": true,
    "semanticTokensProvider": {
      "full": true,
      "legend": {
        "tokenModifiers": [
          "definition",
          "readonly",
          "defaultLibrary",
          "static",
          "array",
          "bool",
          "chan",
          "format",
          "interface",
          "map",
          "number",
          "pointer",
          "signature",
          "slice",
          "string",
          "struct",
          "shadowing"
        ],
        "tokenTypes": [
          "namespace",
          "type",
          "typeParameter",
          "parameter",
          "property",
          "variable",
    
…(trimmed)
```

## 1. Overlay-only packages

orbit_gen on disk: false (stat err: stat /home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen: no such file or directory)
- first diagnostics for cardDecl: ok=true after 1.097s, n=0
- first diagnostics for cardImpl: ok=true after 1.097s, n=0
- first diagnostics for pageDecl: ok=true after 1.097s, n=0
- first diagnostics for pageImpl: ok=true after 83ms, n=0
diagnostics after open (settled):
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

## 2. Completion

### props. in Card impl template expr (`{props.}`)
request: `orbit_gen/impl/components/card/render.go` at 33:58 (0-based, utf-16), latency 10ms
isIncomplete=true, 4 items: [Featured Post Post.Slug Post.Title]
item Post:
```json
{
  "detail": "posts.Post",
  "filterText": "Post",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Post",
  "labelDetails": {
    "detail": " posts.Post"
  },
  "sortText": "00001",
  "textEdit": {
    "insert": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    },
    "newText": "Post",
    "replace": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    }
  }
}
```
item Featured:
```json
{
  "detail": "bool",
  "filterText": "Featured",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Featured",
  "labelDetails": {
    "detail": " bool"
  },
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    },
    "newText": "Featured",
    "replace": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    }
  }
}
```
first item:
```json
{
  "detail": "bool",
  "filterText": "Featured",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Featured",
  "labelDetails": {
    "detail": " bool"
  },
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    },
    "newText": "Featured",
    "replace": {
      "end": {
        "character": 58,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    }
  }
}
```
### partial `props.Fe` in Card impl (replace range)
request: `orbit_gen/impl/components/card/render.go` at 33:60 (0-based, utf-16), latency 2ms
isIncomplete=true, 1 items: [Featured]
item Featured:
```json
{
  "detail": "bool",
  "filterText": "Featured",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Featured",
  "labelDetails": {
    "detail": " bool"
  },
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 60,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    },
    "newText": "Featured",
    "replace": {
      "end": {
        "character": 60,
        "line": 33
      },
      "start": {
        "character": 58,
        "line": 33
      }
    }
  }
}
```
### `href(props.)` in Card impl frontmatter body
request: `orbit_gen/impl/components/card/render.go` at 21:68 (0-based, utf-16), latency 1ms
isIncomplete=true, 4 items: [Post Featured Post.Slug Post.Title]
item Post:
```json
{
  "detail": "posts.Post",
  "filterText": "Post",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Post",
  "labelDetails": {
    "detail": " posts.Post"
  },
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 68,
        "line": 21
      },
      "start": {
        "character": 68,
        "line": 21
      }
    },
    "newText": "Post",
    "replace": {
      "end": {
        "character": 68,
        "line": 21
      },
      "start": {
        "character": 68,
        "line": 21
      }
    }
  }
}
```
### synthetic `Card.Props{|}` in page impl scratch func (cross-package overlay)
request: `orbit_gen/impl/pages/index/render.go` at 45:31 (0-based, utf-16), latency 9ms
isIncomplete=true, 58 items: [Post Featured Post.Slug Post.Title posts.Post{} orbit posts orbit__complete orbit__render Card Orbit__Render Props Render Prerender any …(+43)]
item Post:
```json
{
  "detail": "posts.Post",
  "filterText": "Post",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Post",
  "labelDetails": {
    "detail": " posts.Post"
  },
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
item Featured:
```json
{
  "detail": "bool",
  "filterText": "Featured",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Featured",
  "labelDetails": {
    "detail": " bool"
  },
  "sortText": "00001",
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
    "newText": "Featured: ${1:}",
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
### synthetic `Card.Props{Post: …, Fe|}`
request: `orbit_gen/impl/pages/index/render.go` at 45:53 (0-based, utf-16), latency 1ms
isIncomplete=true, 1 items: [Featured]
item Featured:
```json
{
  "detail": "bool",
  "filterText": "Featured",
  "insertTextFormat": 2,
  "kind": 5,
  "label": "Featured",
  "labelDetails": {
    "detail": " bool"
  },
  "preselect": true,
  "sortText": "00000",
  "textEdit": {
    "insert": {
      "end": {
        "character": 53,
        "line": 45
      },
      "start": {
        "character": 51,
        "line": 45
      }
    },
    "newText": "Featured: ${1:}",
    "replace": {
      "end": {
        "character": 53,
        "line": 45
      },
      "start": {
        "character": 51,
        "line": 45
      }
    }
  }
}
```

## 3. Live Props change (nothing on disk)

page impl with `Tags:` key before Card has it: ok=true n=1
diagnostics:
```json
{
  "diagnostics": [
    {
      "code": "MissingLitField",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#MissingLitField"
      },
      "message": "unknown field Tags in struct literal of type card.Props",
      "range": {
        "end": {
          "character": 7,
          "line": 35
        },
        "start": {
          "character": 3,
          "line": 35
        }
      },
      "severity": 1,
      "source": "compiler"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
  "version": 4
}
```
after didChange of Card decl (adds `Tags []string`): page impl diagnostics cleared=true after 1.014s (n=0, version 4)
### `Card.Props{|}` right after the decl change
request: `orbit_gen/impl/pages/index/render.go` at 46:31 (0-based, utf-16), latency 1ms
isIncomplete=true, 59 items: [Post Featured Tags Post.Slug Post.Title posts.Post{} orbit posts orbit__complete orbit__render Card Orbit__Render Props Render Prerender …(+44)]
Tags present: true (latency 1ms)
### `props.` in Card impl after decl change (didChange of impl immediately before)
request: `orbit_gen/impl/components/card/render.go` at 33:58 (0-based, utf-16), latency 2ms
isIncomplete=true, 5 items: [Featured Post Tags Post.Slug Post.Title]
Tags present: true (latency incl. didChange 2ms)

## 8b. Completion latency after didChange (warm, 10 runs)

page impl didChange+completion: [3.119416ms 1.686396ms 1.878025ms 1.664024ms 1.71494ms 1.63004ms 1.739966ms 1.716502ms 1.626893ms 1.588011ms]
last run sees Tags9: true
Card decl didChange + completion in page impl (cross-package): [9.993562ms 10.304334ms 10.629986ms 10.45083ms 10.969203ms 9.335796ms 8.737834ms 8.779542ms 8.767009ms 8.845927ms]

## 4. Diagnostics under line directives

page impl errors: `title := hoem` (index.orbit:6:10), `{titel}` (9:5), key `Psot` (11:9)
expected generated-file positions (0-based utf-8): hoem={22 55} titel={27 46} Psot={33 50}
all latest diagnostics:
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
- `orbit_gen/decl/components/card/decl.go` v13: 0 diagnostics
- `orbit_gen/decl/pages/index/decl.go` v1: 0 diagnostics
- `orbit_gen/impl/components/card/render.go` v7: 0 diagnostics
- `orbit_gen/impl/pages/index/render.go` v16: 3 diagnostics
  params:
```json
{
  "diagnostics": [
    {
      "code": "UndeclaredName",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#UndeclaredName"
      },
      "message": "undefined: hoem",
      "range": {
        "end": {
          "character": 59,
          "line": 22
        },
        "start": {
          "character": 55,
          "line": 22
        }
      },
      "severity": 1,
      "source": "compiler"
    },
    {
      "code": "UndeclaredName",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#UndeclaredName"
      },
      "message": "undefined: titel",
      "range": {
        "end": {
          "character": 51,
          "line": 27
        },
        "start": {
          "character": 46,
          "line": 27
        }
      },
      "severity": 1,
      "source": "compiler"
    },
    {
      "code": "MissingLitField",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#MissingLitField"
      },
      "message": "unknown field Psot in struct literal of type card.Props",
      "range": {
        "end": {
          "character": 54,
          "line": 33
        },
        "start": {
          "character": 50,
          "line": 33
        }
      },
      "severity": 1,
      "source": "compiler"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
  "version": 16
}
```
Card decl error: `posts.Postt` (Card.orbit:4:20); expected gen pos {10 19}
all latest diagnostics:
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
- `orbit_gen/decl/components/card/decl.go` v14: 1 diagnostics
  params:
```json
{
  "diagnostics": [
    {
      "code": "UndeclaredImportedName",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#UndeclaredImportedName"
      },
      "message": "undefined: posts.Postt",
      "range": {
        "end": {
          "character": 24,
          "line": 10
        },
        "start": {
          "character": 19,
          "line": 10
        }
      },
      "severity": 1,
      "source": "compiler"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go",
  "version": 14
}
```
- `orbit_gen/decl/pages/index/decl.go` v1: 0 diagnostics
- `orbit_gen/impl/components/card/render.go` v7: 1 diagnostics
  params:
```json
{
  "diagnostics": [
    {
      "code": "IncompatibleAssign",
      "codeDescription": {
        "href": "https://pkg.go.dev/golang.org/x/tools/internal/typesinternal#IncompatibleAssign"
      },
      "message": "cannot use props.Post (variable with invalid type) as posts.Post value in argument to href",
      "range": {
        "end": {
          "character": 72,
          "line": 21
        },
        "start": {
          "character": 62,
          "line": 21
        }
      },
      "severity": 1,
      "source": "compiler"
    }
  ],
  "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go",
  "version": 7
}
```
- `orbit_gen/impl/pages/index/render.go` v17: 0 diagnostics
after revert:
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
- `orbit_gen/decl/components/card/decl.go` v15: 0 diagnostics
- `orbit_gen/decl/pages/index/decl.go` v1: 0 diagnostics
- `orbit_gen/impl/components/card/render.go` v7: 0 diagnostics
- `orbit_gen/impl/pages/index/render.go` v17: 0 diagnostics

## 4b. Hover / definition / references into generated code

### hover on `Featured` key in page impl
request `textDocument/hover` {"position":{"line":34,"character":52},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 1ms)
result:
```json
{
  "contents": {
    "kind": "markdown",
    "value": "```go\nfield Featured bool\n```\n\n---\n\n[`(card.Props).Featured` on pkg.go.dev](https://pkg.go.dev/example.com/blog/orbit_gen/decl/components/card#Props.Featured)"
  },
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
```
### hover on `Props` after non-ASCII in scratch (utf-8 range check)
request `textDocument/hover` {"position":{"line":45,"character":26},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
{
  "contents": {
    "kind": "markdown",
    "value": "```go\ntype Props struct {\n\tPost     posts.Post\n\tFeatured bool\n}\n```\n\n---\n\nline decl.go:13:24\n\n\n---\n\n[`card.Props` on pkg.go.dev](https://pkg.go.dev/example.com/blog/orbit_gen/decl/components/card#Props)"
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
expected Props range start (utf-8 bytes) {45 28} vs utf-16 {45 25}
### definition of `Featured` key (→ Card decl overlay)
request `textDocument/definition` {"position":{"line":34,"character":52},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
[
  {
    "range": {
      "end": {
        "character": 12,
        "line": 11
      },
      "start": {
        "character": 4,
        "line": 11
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go"
  }
]
```
### definition of `title` use in `{title}` (→ same file, frontmatter span)
request `textDocument/definition` {"position":{"line":27,"character":47},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
[
  {
    "range": {
      "end": {
        "character": 51,
        "line": 22
      },
      "start": {
        "character": 46,
        "line": 22
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"
  }
]
```
### definition of `posts` package ident in `[]posts.Post` (→ on-disk user pkg)
request `textDocument/definition` {"position":{"line":23,"character":62},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
[
  {
    "range": {
      "end": {
        "character": 9,
        "line": 3
      },
      "start": {
        "character": 5,
        "line": 3
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/internal/posts/posts.go"
  }
]
```
### definition of `href` call in Card impl body (→ hoisted func in same overlay)
request `textDocument/definition` {"position":{"line":21,"character":58},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go"}} (latency 9ms)
result:
```json
[
  {
    "range": {
      "end": {
        "character": 59,
        "line": 10
      },
      "start": {
        "character": 55,
        "line": 10
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go"
  }
]
```
### references of `Featured` field from Card decl
request `textDocument/references` {"context":{"includeDeclaration":true},"position":{"line":11,"character":5},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go"}} (latency 0s)
result:
```json
[
  {
    "range": {
      "end": {
        "character": 12,
        "line": 11
      },
      "start": {
        "character": 4,
        "line": 11
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go"
  },
  {
    "range": {
      "end": {
        "character": 66,
        "line": 25
      },
      "start": {
        "character": 58,
        "line": 25
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go"
  },
  {
    "range": {
      "end": {
        "character": 59,
        "line": 34
      },
      "start": {
        "character": 51,
        "line": 34
      }
    },
    "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"
  }
]
```

## 7. Rename

### prepareRename on frontmatter local `title`
request `textDocument/prepareRename` {"position":{"line":22,"character":47},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
{
  "placeholder": "title",
  "range": {
    "end": {
      "character": 51,
      "line": 22
    },
    "start": {
      "character": 46,
      "line": 22
    }
  }
}
```
### rename `title` → `heading`
request `textDocument/rename` {"newName":"heading","position":{"line":22,"character":47},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
{
  "documentChanges": [
    {
      "edits": [
        {
          "newText": "heading",
          "range": {
            "end": {
              "character": 51,
              "line": 22
            },
            "start": {
              "character": 46,
              "line": 22
            }
          }
        },
        {
          "newText": "heading",
          "range": {
            "end": {
              "character": 13,
              "line": 24
            },
            "start": {
              "character": 8,
              "line": 24
            }
          }
        },
        {
          "newText": "heading",
          "range": {
            "end": {
              "character": 51,
              "line": 27
            },
            "start": {
              "character": 46,
              "line": 27
            }
          }
        }
      ],
      "textDocument": {
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
        "version": 17
      }
    }
  ]
}
```
### prepareRename on Props field `Featured` (Card decl)
request `textDocument/prepareRename` {"position":{"line":11,"character":5},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go"}} (latency 0s)
result:
```json
{
  "placeholder": "Featured",
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
```
### rename `Featured` → `Highlighted` from Card decl
request `textDocument/rename` {"newName":"Highlighted","position":{"line":11,"character":5},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go"}} (latency 0s)
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go",
        "version": 15
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
        "version": 17
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go",
        "version": 7
      }
    }
  ]
}
```
### rename `Featured` → `Highlighted` from the key in page impl
request `textDocument/rename` {"newName":"Highlighted","position":{"line":34,"character":52},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/decl/components/card/decl.go",
        "version": 15
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go",
        "version": 17
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
        "uri": "file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/components/card/render.go",
        "version": 7
      }
    }
  ]
}
```
### prepareRename on generated glue `orbit__w` (page impl)
request `textDocument/prepareRename` {"position":{"line":25,"character":2},"textDocument":{"uri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project/orbit_gen/impl/pages/index/render.go"}} (latency 0s)
result:
```json
{
  "placeholder": "orbit__w",
  "range": {
    "end": {
      "character": 9,
      "line": 25
    },
    "start": {
      "character": 1,
      "line": 25
    }
  }
}
```

## Server→client requests and notifications

- request `client/registerCapability` ×2, first params: `{"registrations":[{"id":"workspace/didChangeWatchedFiles-0","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{mod,work}","kind":7},{"globPattern":{"baseUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","pattern":"**/*.{go,mod,sum,work}"},"kind":7}]}}]}`
- request `window/workDoneProgress/create` ×1, first params: `{"token":"974487487371204562"}`
- request `workspace/configuration` ×1, first params: `{"items":[{"scopeUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","section":"gopls"}]}`
- notification `$/progress` ×2
- notification `textDocument/publishDiagnostics` ×43
- notification `window/logMessage` ×4
- notification `window/showMessage` ×3
