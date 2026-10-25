# Session refs2: stability of overlay-only reference misses (50 components)

## open order: interleaved (decl, impl per component, page last)

  - round 1 (after all diagnostics + 2s quiet): 0/50 incomplete []
  - round 2 (5s later): 0/50 incomplete []
  - round 3 (after a whitespace didChange of every decl): 0/50 incomplete []
  - round 4 (after a whitespace didChange of every impl): 0/50 incomplete []

## open order: all decls first, then impls, then page

  - round 1 (after all diagnostics + 2s quiet): 0/50 incomplete []
  - round 2 (5s later): 0/50 incomplete []
  - round 3 (after a whitespace didChange of every decl): 0/50 incomplete []
  - round 4 (after a whitespace didChange of every impl): 0/50 incomplete []

## open order: page first, then impls, then decls

  - round 1 (after all diagnostics + 2s quiet): 3/50 incomplete [c8:2 c11:2 c14:2]
  - round 2 (5s later): 0/50 incomplete []
  - round 3 (after a whitespace didChange of every decl): 0/50 incomplete []
  - round 4 (after a whitespace didChange of every impl): 0/50 incomplete []
