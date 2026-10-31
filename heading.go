package lyr

// Heading is one heading of a Markdown file (PLAN.md §2.4), named after
// Astro's MarkdownHeading. Lyr fills a layout's Headings []lyr.Heading
// prop with a Markdown page's headings, and an import of a .md file exposes
// them as Post.Headings, for a table of contents.
type Heading struct {
	Depth int    // 1 for <h1>, … 6 for <h6>
	Slug  string // the heading's id
	Text  string // its text, without markup
}
