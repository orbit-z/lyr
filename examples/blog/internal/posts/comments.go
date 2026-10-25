package posts

import "time"

// Comment is a reader comment, with its replies.
type Comment struct {
	Author  string
	Website string // as the reader typed it: untrusted
	Text    string
	Replies []Comment
}

// CommentDelay is how long Comments takes, like a query to a slow database.
var CommentDelay = 200 * time.Millisecond

var comments = map[int][]Comment{
	4: {
		{
			Author: "Ada", Website: "https://ada.example.com", Text: "Nice! How fast is it?",
			Replies: []Comment{
				{
					Author: "Bertrand", Text: "Fast enough that the comments are the slow part.",
					Replies: []Comment{
						{Author: "Ada", Text: "Which is why they're deferred. Got it."},
					},
				},
			},
		},
		{Author: "Mallory", Website: "javascript:alert(1)", Text: "Click my name!"},
	},
	1: {
		{Author: "Grace", Text: "The fallback is a nice touch."},
	},
}

// Comments returns the comment threads of a post. It sleeps for
// CommentDelay first, to simulate a slow query.
func Comments(postID int) ([]Comment, error) {
	time.Sleep(CommentDelay)
	return comments[postID], nil
}
