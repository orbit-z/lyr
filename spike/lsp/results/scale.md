# Session scale: 50 components + a page using all of them, all overlay-only (102 overlays)

## run 0 (cold: empty GOPLSCACHE)

- open→all overlays have diagnostics: 1.772s (missing 0, total diagnostics 0)
- first completion `C7.Props{|}` sent right after the didOpens: latency 352ms, process start→result 386ms, items 102, Post present true
- references of C0.Props.Featured: 2 locations (expect 3: decl, c0 impl, big page) in 0s
  - after settle: references of c0.Props.Featured: 3 locations: decl/components/c0/decl.go, impl/components/c0/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c0/decl.go, impl/components/c0/render.go, impl/pages/big/render.go
  - after settle: references of c1.Props.Featured: 3 locations: decl/components/c1/decl.go, impl/components/c1/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c1/decl.go, impl/components/c1/render.go, impl/pages/big/render.go
  - after settle: references of c25.Props.Featured: 3 locations: decl/components/c25/decl.go, impl/components/c25/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c25/decl.go, impl/components/c25/render.go, impl/pages/big/render.go
  - after settle: references of c49.Props.Featured: 3 locations: decl/components/c49/decl.go, impl/components/c49/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c49/decl.go, impl/components/c49/render.go, impl/pages/big/render.go
- C7 decl didChange → completion in big page sees new field: [9.338401ms 8.826951ms 9.08192ms 8.66075ms 8.559068ms]
- gopls RSS: 652212 kB

## run 1 (warm GOPLSCACHE)

- open→all overlays have diagnostics: 1.124s (missing 0, total diagnostics 0)
- first completion `C7.Props{|}` sent right after the didOpens: latency 122ms, process start→result 132ms, items 106, Post present true
- references of C0.Props.Featured: 2 locations (expect 3: decl, c0 impl, big page) in 10ms
  - after settle: references of c0.Props.Featured: 3 locations: decl/components/c0/decl.go, impl/components/c0/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c0/decl.go, impl/components/c0/render.go, impl/pages/big/render.go
  - after settle: references of c1.Props.Featured: 3 locations: decl/components/c1/decl.go, impl/components/c1/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c1/decl.go, impl/components/c1/render.go, impl/pages/big/render.go
  - after settle: references of c25.Props.Featured: 3 locations: decl/components/c25/decl.go, impl/components/c25/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c25/decl.go, impl/components/c25/render.go, impl/pages/big/render.go
  - after settle: references of c49.Props.Featured: 3 locations: decl/components/c49/decl.go, impl/components/c49/render.go, impl/pages/big/render.go
  - same, queried from the impl's `props.Featured`: 3 locations: decl/components/c49/decl.go, impl/components/c49/render.go, impl/pages/big/render.go
- C7 decl didChange → completion in big page sees new field: [8.505538ms 8.580128ms 9.31076ms 9.015165ms 8.385963ms]
- gopls RSS: 259092 kB
