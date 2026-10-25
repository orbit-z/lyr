// Package data holds the data that every engine in bench/go renders: the
// same 500 cards, built once at init so rendering measures only the
// template engine.
package data

import "strconv"

// Item is one card.
type Item struct {
	ID       int
	Title    string
	Count    int
	Featured bool
	Href     string
	Body     string // contains <, & and >, so every engine escapes it
}

// NumCards is the number of cards on the page.
const NumCards = 500

// PageTitle is the page's <title> and header text; it needs escaping.
const PageTitle = "500 cards & more"

// Items are the page's cards.
var Items = func() []Item {
	items := make([]Item, NumCards)
	for i := range items {
		n := strconv.Itoa(i)
		items[i] = Item{
			ID:       i,
			Title:    "Card number " + n,
			Count:    i * 37,
			Featured: i%3 == 0,
			Href:     "/items/" + n,
			Body:     "Tom & Jerry <" + n + "> chase each other > twice.",
		}
	}
	return items
}()

// Link is a header link.
type Link struct {
	Href, Text string
}

// Nav is the header's links.
var Nav = []Link{
	{Href: "/", Text: "Home"},
	{Href: "/cards", Text: "Cards"},
	{Href: "/about", Text: "About"},
}
