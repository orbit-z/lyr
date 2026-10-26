package posts

import "time"

type Post struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags"`
	Published time.Time `json:"published"`
	Likes     int       `json:"likes"`
	Author    *Author   `json:"author,omitempty"`
	draft     bool
}

type Author struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Summary struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// Page is a generic page of items.
type Page[T any] struct {
	Items []T      `json:"items"`
	Next  *Page[T] `json:"next,omitempty"`
}

// Tree is recursive.
type Tree struct {
	Name     string  `json:"name"`
	Children []*Tree `json:"children,omitempty"`
}

// Default's name is a TS keyword.
type Default struct {
	Base
	Kind string `json:"kind"`
}

type Base struct {
	ID   int    `json:"id"`
	Kind string `json:"-"`
}
