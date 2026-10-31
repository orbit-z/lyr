// Package data holds the benchmark's fixed data set. It mirrors
// bench/astro-site/src/data.ts exactly, so both sites render the same text.
package data

import (
	"strconv"
	"time"
)

// Item is one card.
type Item struct {
	ID       int
	Title    string
	Summary  string
	Href     string
	Likes    int
	Featured bool
	Tags     []string
}

var tagSet = []string{"go", "astro", "ssr", "html", "css"}

func makeItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		id := i + 1
		s := strconv.Itoa(id)
		items[i] = Item{
			ID:       id,
			Title:    "Card " + s + " <&> friends",
			Summary:  "Summary of card " + s + ": \"quotes\" & <tags> are escaped.",
			Href:     "/items/" + s + "?ref=bench&page=" + strconv.Itoa(id%7),
			Likes:    (id * 37) % 1000,
			Featured: id%3 == 0,
			Tags:     []string{tagSet[id%5], tagSet[(id+2)%5]},
		}
	}
	return items
}

// Cards is the 500-card list of the render-heavy page.
var Cards = makeItems(500)

// fetched is the list returned by Fetch.
var fetched = makeItems(20)

// Fetch simulates a 10 ms data fetch and returns a modest list.
func Fetch() []Item {
	time.Sleep(10 * time.Millisecond)
	return fetched
}
