//go:build race

package lyr

// raceEnabled is set under -race, where sync.Pool drops items at random, so
// allocation counts of pooled paths aren't meaningful.
const raceEnabled = true
