//go:build race

package cache

// raceEnabled is set under -race, where allocation counts are not
// meaningful.
const raceEnabled = true
