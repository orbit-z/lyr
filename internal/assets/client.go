package assets

import _ "embed"

// The client modules of the typed bridge (PLAN.md §5c).

// clientTS is lyr:client, the runtime behind every component's module.
//
//go:embed client/client.ts
var clientTS string

// clientNamespace holds lyr:client and lyr:types (empty: types only).
const clientNamespace = "lyr-client"

// componentModule is what a script gets for import "./X.lyr": the same
// re-export for every component, so esbuild's input never depends on the
// types (only lyr_gen/types/<path>.lyr.d.ts does).
const componentModule = `export { data, json } from "lyr:client";`
