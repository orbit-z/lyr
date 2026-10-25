package main

import (
	"time"
)

// sessionMinimal checks which answers to server→client requests gopls tolerates.
func sessionMinimal(gopls string) *report {
	r := &report{}
	r.p("# Session minimal: answering server→client requests with null or errors")
	defer func() { answerMode = "" }()
	for _, mode := range []string{"null", "error"} {
		answerMode = mode
		s, _, err := newSession(gopls, "minimal-"+mode+".log", []string{"utf-8"}, true, nil)
		if err != nil {
			r.p("init (%s): %v", mode, err)
			continue
		}
		s.r = r
		m := s.c.diagMark()
		gs := defaultGen()
		s.openAll(gs)
		s.waitVersion(m, "pageImpl", 30*time.Second)
		pi := s.docs["pageImpl"].text
		items, _ := s.completion("mode "+mode+": `Card.Props{|}`", "pageImpl", posOf(pi, "_ = Card.Props{}", 0, len("_ = Card.Props{"), "utf-16"))
		r.p("Post present: %v", find(items, "Post") != nil)
		bad := genPageImpl(pageImplOpts{scratch: scratchProps, postKey: "Psot"})
		m = s.c.diagMark()
		s.change("pageImpl", bad)
		e, ok := s.waitVersion(m, "pageImpl", 30*time.Second)
		r.p("diagnostic for `Psot` published: %v (n=%d)", ok, e.n)
		s.serverRequests()
		s.c.shutdown()
	}
	return r
}
