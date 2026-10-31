---
title: About this blog
layout: ../layouts/Base.lyr
---

# About this blog

This page is a Markdown file, `src/pages/about.md`. Lyr converts it to
HTML when it generates code, so serving it costs the same as serving a
template's "static" HTML.

## How it's built

The posts come from a Go package, and the pages are `.lyr` components:

```go
all, err := posts.All()
if err != nil {
    return err
}
```

Read the [colophon](#colophon) below, or go back to the [posts](/).

## Colophon

Set in the system font, with no client-side highlighter.
