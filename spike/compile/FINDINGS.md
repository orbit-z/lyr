# Milestone 0, part A: compile-model spike findings

Toolchain: `go version go1.27.3 linux/amd64` (auto-switched from go1.23.4). The coordinator re-verified the build, vet, tests and the injection harness independently.

| # | Check | Result |
|---|---|---|
| 1 | build/run, no cycles with A↔B | PASS |
| 2a | frontmatter type error | PASS `A.orbit:21:16` |
| 2b | undefined name in `{expr}` | PASS `A.orbit:25:8` |
| 2c | unknown prop `Psot:` | PASS `index.orbit:9:6` (wording: D6) |
| 2d | error in a hoisted helper | PASS `B.orbit:16:54` |
| 2d' | continuation line of a multi-line decl span | PASS `A.orbit:9:11` |
| 2e | `{if}` header (undefined field / non-bool) | PASS `A.orbit:29:13` / `26:7` |
| 2 | relative paths: compile and vet agree | PASS on Go ≥1.27 only |
| 2 | absolute-path alternative | works, worse trade-offs |
| 3 | reset directives | PASS |
| 4 | unexported decl name | Go says only `undefined: shout`, so Orbit needs its own error |
| 5 | test that imports decl only | PASS (clear "not linked" panic) |
| 6 | surprises | D1–D6 below |

## Evidence
```
$ go build ./... && go vet ./... && go test ./...   # ok (9×A, 6×B rendered; depth guard: "render depth exceeded 1000: src/components/A.orbit > …")
$ go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./orbit_gen/...
impl/components/a -> decl/components/a decl/components/b orbitrt strings
impl/components/b -> decl/components/a decl/components/b orbitrt strconv
decl/components/a -> orbitrt strings
```
`go build ./...` and `go vet ./...` print identical errors from the module root. From `src/` they print `../src/…`, and from the package dir `../../../../src/…`:
```
(a) ./src/components/A.orbit:21:16: cannot use "x" (untyped string constant) as int value in argument to label
(b) ./src/components/A.orbit:25:8: undefined: titel
(c) ./src/pages/index.orbit:9:6: unknown field Psot in struct literal of type a.Props
(d) ./src/components/B.orbit:16:54: cannot use "n" (untyped string constant) as int value in argument to strconv.Itoa
(d') ./src/components/A.orbit:9:11: undefined: intt
(e) ./src/components/A.orbit:29:13: props.Dpth undefined (type "example.com/blog/orbit_gen/decl/components/a".Props has no field or method Dpth)
(e') ./src/components/A.orbit:26:7: non-boolean condition in if statement
(3)  with reset:    orbit_gen/impl/components/a/render.go:29:81: undefined: orbit__glue
     without reset: ./src/components/A.orbit:25:16: undefined: orbit__glue   (wrong position)
(4)  ./src/components/A.orbit:21:10: undefined: shout
(5)  recovered: orbit: src/components/A.orbit not linked; import example.com/blog/orbit_gen/app
missing `_ = x`: ./src/components/A.orbit:22:1: declared and not used: unused
```
Reproduce with `python3 tools/inject.py <scratch-dir> all`. `tools/lines.py` recomputes reset positions and checks all 45 `.orbit` directives.

## How resolution works
- **cmd/compile 1.27** joins `filepath.Dir(file)` with the relative name. cmd/go runs compile from the invoking cwd with `./orbit_gen/impl/components/a/render.go`, which resolves to `src/components/A.orbit`, and cmd/go prints it relative to the cwd.
- **go/scanner** (vet, gopls) joins with the file's directory in every version tested (1.23.4, 1.24.0, 1.27.0, 1.27.3).
- **Go 1.23.4 and 1.24.0** (with go.mod lowered to `go 1.23`) print `../../../../src/components/A.orbit:25:8` unresolved, which is wrong. **`go 1.27` is a hard minimum.**

## Directive format that works
- `/*line ../../../../src/components/A.orbit:L:C*/span/*line render.go:L:C*/`
- Always use the `:L:C` form. Columns are 1-based bytes (a tab counts as 1).
- The reset uses the bare generated basename with the true position of the next character.
- Field keys: `/*line …index.orbit:9:6*/Item/*line render.go:…*/: /*line …:9:12*/it/*line …*/,`
- Component calls: put the directive before `A.Render`, pointing at the tag name.

## Relative vs absolute paths
- **Correct diagnostics on Go ≥1.27**: both work from any cwd, including `go build -C` from outside the module.
- **Correct diagnostics on Go ≤1.24**: absolute works, relative doesn't.
- **Reproducible/committable `orbit_gen/` and shared build cache**: relative works, absolute doesn't.
- **`-trimpath`**: neither form trims `.orbit` paths in stack traces (`/abs/…/src/components/A.orbit:25`), although `render.go` frames are trimmed.
- **Recommendation**: relative.

## Other observations
- "Other declaration" lines print as `./src/components/A.orbit:7:6[/abs/…/decl.go:10:53]`; the CLI error parser must strip the `[…]` part.
- Sibling packages are initialized in import-path order.
- Shadowing a decl export in the body (`Item := 42`) is legal.

## Design issues
- **D1**: user names can break generated glue code.
  - `orbit := 42` in the body gives `render.go:41:12: orbit.Slots is not a type`.
  - A nested `{for _, ctx := range …}` around a component call gives `cannot use ctx (variable of type int) as *orbit.Ctx value`.
  - Verified fix: glue code imports the runtime as `orbit__rt`.
- **D2**: `return nil` as the last frontmatter statement builds, but vet reports `unreachable code` in the generated file.
- **D3**: linking via `init()` breaks user `func init` code.
  - A user `func init` that renders Self panics with "not linked".
  - A user init in A that renders B panics, because B isn't initialized yet.
  - Verified fix: link in a package-level var initializer, `var _ = func() int { Orbit__Render = orbit__render; return 0 }()`. Self-render then works. Rendering another component during init stays unsupported.
- **D4**: name clashes are reported at generated positions.
  - An impl `type Item` gives `Item already declared through dot-import`.
  - An alias `Item "./Item.orbit"` gives `Item redeclared`.
- **D5**: decl and impl sharing a package name prints `"example.com/…/decl/components/a".Props`. Naming the impl package `a_impl` gives `a.Props`.
- **D6**: unknown-prop errors name the generated key `Psot`, not the user's `psot`.

## Proposed PLAN.md changes
1. **§3.3**: correct the resolution text. A relative name is resolved against the directory of the file containing the directive (go/scanner always; cmd/compile from Go 1.27). `go 1.27` is a hard minimum. Columns are 1-based bytes. `-trimpath` doesn't trim directive names.
2. **§3.3**: related positions appear as `…orbit:L:C[<gen>:L:C]`; the CLI strips the brackets.
3. **§2.2/§3.2**: glue code never refers to names users can shadow. It imports the runtime as `orbit__rt`, and `ctx`/`slots` are handled per choice C1. Add `orbit` to the redeclaration error list.
4. **§2.2**: if the last top-level body statement is terminating, emit the `_ = name` lines before it and no template code after it. A non-empty template after it is an Orbit error.
5. **§3.1**: link via a package-level var initializer instead of `init()`.
6. **§3.1**: an alias or impl-side hoisted name that equals a decl export is an Orbit error, reported at that name.

## Choices for the user
- **C1**: nested shadowing of `ctx`/`slots`: (a) rename the params to `orbit__ctx` and bind `ctx := orbit__ctx` (zero cost); (b) forbid nested shadowing.
- **C2**: user `func init` in frontmatter: (a) allow it with documented limits; (b) reject it.
- **C3**: type names in errors: (a) a distinct impl package name; (b) the CLI rewrites messages to the user's alias; (c) both.
- **C4**: unknown props: (a) accept Go's wording; (b) Orbit pre-checks attributes against `Props` (`unknown prop "psot" on <A> (props: item, depth)`).
- **C5**: relative (recommended) vs absolute directive paths.
