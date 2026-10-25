package main

import (
	"time"
)

// sessionShape: completion item shape when the client supports neither
// insert/replace edits nor snippets.
func sessionShape(gopls string) *report {
	r := &report{}
	r.p("# Session shape: client without insertReplaceSupport / snippetSupport / labelDetailsSupport")
	capsHook = func(c map[string]any) {
		ci := c["textDocument"].(map[string]any)["completion"].(map[string]any)["completionItem"].(map[string]any)
		ci["insertReplaceSupport"] = false
		ci["snippetSupport"] = false
		ci["labelDetailsSupport"] = false
	}
	defer func() { capsHook = nil }()
	s, _, err := newSession(gopls, "shape.log", nil, true, nil)
	if err != nil {
		r.p("init: %v", err)
		return r
	}
	defer s.c.shutdown()
	s.r = r
	m := s.c.diagMark()
	s.openAll(defaultGen())
	s.waitVersion(m, "pageImpl", 30*time.Second)
	pi := s.docs["pageImpl"].text
	items, _ := s.completion("`Card.Props{|}`", "pageImpl", posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), "utf-16"))
	s.showItems(items, "Post")
	impl := genCardImpl(cardImplOpts{title: "props.Fe"})
	s.change("cardImpl", impl)
	items, _ = s.completion("`props.Fe|`", "cardImpl", posOf(impl, "props.Fe/*line", 0, len("props.Fe"), "utf-16"))
	s.showItems(items, "Featured")
	return r
}
