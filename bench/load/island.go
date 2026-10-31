package main

import (
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var (
	// Lyr: <lyr-island src="/_lyr/island/<build>/<id>" props="<token>">;
	// the loader fetches src + "?p=" + encodeURIComponent(props).
	lyrIsland = regexp.MustCompile(`<lyr-island src="([^"]+)" props="([^"]*)"`)
	// Astro: <link rel="preload" as="fetch" href="/_server-islands/<Name>?e=…&amp;p=…&amp;s=">.
	astroIsland = regexp.MustCompile(`<link rel="preload" as="fetch" href="(/_server-islands/[^"]+)"`)
)

// IslandURL returns the absolute URL of the first server island on a page
// (Lyr or Astro), as the browser's loader would request it.
func IslandURL(pageURL string, body []byte) (string, error) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return "", err
	}
	var ref string
	if m := lyrIsland.FindSubmatch(body); m != nil {
		src := html.UnescapeString(string(m[1]))
		props := html.UnescapeString(string(m[2]))
		ref = src + "?p=" + strings.ReplaceAll(url.QueryEscape(props), "+", "%20")
	} else if m := astroIsland.FindSubmatch(body); m != nil {
		ref = html.UnescapeString(string(m[1]))
	} else {
		return "", errors.New(pageURL + ": no server island found in the page")
	}
	u, err := base.Parse(ref)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
