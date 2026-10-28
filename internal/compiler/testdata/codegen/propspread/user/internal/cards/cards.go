// Package cards is the propspread case's user package (written into the
// test module by TestSpreadGolden).
package cards

import "github.com/orbit-z/orbit"

// Card is a card's props, built by a helper.
type Card struct {
	Href, Title string
	Note        orbit.Opt[string]
	Lines       int
}

// Calls counts Counted's calls: a spread operand is evaluated once.
var Calls int

// Counted returns card n and counts the call.
func Counted(n int) Card {
	Calls++
	return Of(n)
}

// hrefs and titles are constants, so that Of allocates nothing.
var hrefs, titles = [...]string{"/c/0", "/c/1", "/c/2", "/c/3", "/c/4", "/c/5", "/c/6", "/c/7"},
	[...]string{"Card 0", "Card 1", "Card 2", "Card 3", "Card 4", "Card 5", "Card 6", "Card 7"}

// Of returns card n (0 to 7).
func Of(n int) Card {
	c := Card{Href: hrefs[n], Title: titles[n], Lines: n}
	if n%2 == 0 {
		c.Note = orbit.Some("even")
	}
	return c
}

// Ref is a subset of Cached's props.
type Ref struct{ Slug, Version string }

// Options are embedded in Cached's props.
type Options struct {
	Credits bool
	Lines   int
}

// Extra sets an Opt prop with a plain field.
type Extra struct{ Note string }
