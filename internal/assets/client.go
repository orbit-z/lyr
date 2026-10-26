package assets

import _ "embed"

// The client modules of the typed bridge (PLAN.md §5c).

// clientTS is orbit:client, the runtime behind every component's module.
//
//go:embed client/client.ts
var clientTS string

// clientNamespace holds orbit:client and orbit:types (empty: types only).
const clientNamespace = "orbit-client"

// componentModule is what a script gets for import "./X.orbit": the same
// re-export for every component, so esbuild's input never depends on the
// types (only orbit_gen/types/<path>.orbit.d.ts does).
const componentModule = `export { data, json } from "orbit:client";`
