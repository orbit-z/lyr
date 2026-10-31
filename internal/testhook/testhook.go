// Package testhook connects package lyrtest to the runtime's unexported
// render path, so that lyrtest renders exactly as SSR does without package
// lyr exporting test-only API.
//
// Package lyr sets the hooks in a package-level variable initializer
// (testhook.go at the repo root). lyrtest imports lyr, so the hooks are
// set before any lyrtest function runs. This package can't import lyr
// (lyr imports it), so the Ctx and Writer pass through as any.
package testhook

import "net/http"

// Render renders one component the way a page is rendered for a request:
// with a pooled *lyr.Ctx for r carrying the route params names[i] =
// vals[i], and a pooled *lyr.Writer over a pooled buffer. It calls render
// with the Ctx and the Writer (as any) and returns the HTML, or the error
// that render returned or that the Writer kept. No head injection happens.
// A panic in render propagates after the pooled values are released.
//
// Milestone 8 (server:defer): placeholders written during this render will
// be sealed with a fixed test island key and the build ID
// "0000000000000000", so the HTML is the same on every run (PLAN.md §4,
// "Component testing"). The runtime will set that up on the Ctx here, next
// to the params; nothing in lyrtest has to change.
var Render func(r *http.Request, names, vals []string, render func(ctx, w any) error) (string, error)
