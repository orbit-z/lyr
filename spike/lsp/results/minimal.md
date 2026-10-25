# Session minimal: answering server→client requests with null or errors
### mode null: `Card.Props{|}`
request: `orbit_gen/impl/pages/index/render.go` at 45:31 (0-based, utf-16), latency 1ms
isIncomplete=true, 58 items: [Post Featured Post.Slug Post.Title posts.Post{} orbit posts orbit__complete orbit__render Card Orbit__Render Props Render Prerender any …(+43)]
Post present: true
diagnostic for `Psot` published: true (n=1)

## Server→client requests and notifications

- request `client/registerCapability` ×2, first params: `{"registrations":[{"id":"workspace/didChangeWatchedFiles-0","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{mod,work}","kind":7},{"globPattern":{"baseUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","pattern":"**/*.{go,mod,sum,work}"},"kind":7}]}}]}`
- request `window/workDoneProgress/create` ×1, first params: `{"token":"5457280214023303088"}`
- request `workspace/configuration` ×1, first params: `{"items":[{"scopeUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","section":"gopls"}]}`
- notification `$/progress` ×2
- notification `textDocument/publishDiagnostics` ×2
- notification `window/logMessage` ×3
- notification `window/showMessage` ×1
### mode error: `Card.Props{|}`
request: `orbit_gen/impl/pages/index/render.go` at 45:31 (0-based, utf-16), latency 0s
ERROR: textDocument/completion: {"code":0,"message":"no views"}
Post present: false
diagnostic for `Psot` published: false (n=0)

## Server→client requests and notifications

- request `client/registerCapability` ×2, first params: `{"registrations":[{"id":"workspace/didChangeWatchedFiles-0","method":"workspace/didChangeWatchedFiles","registerOptions":{"watchers":[{"globPattern":"**/*.{mod,work}","kind":7}]}}]}`
- request `window/workDoneProgress/create` ×5, first params: `{"token":"3515232131092437130"}`
- request `workspace/configuration` ×5, first params: `{"items":[{"scopeUri":"file:///home/bertrandkhe/Projects/rs-astro/spike/lsp/project","section":"gopls"}]}`
- notification `textDocument/publishDiagnostics` ×4
- notification `window/logMessage` ×6
- notification `window/showMessage` ×5
