package config

import "testing"

func TestMatchWatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		prefix, want  bool
	}{
		{"../*.md", "../README.md", false, true},
		{"../*.md", "../reference/cli.md", false, false},
		{"../*.md", "README.md", false, false},
		{"./content/*.md", "content/a.md", false, true},
		{"content/**", "content/a.md", false, true},
		{"content/**", "content/a/b/c.json", false, true},
		{"content/**", "contents/a.md", false, false},
		{"content/**/*.md", "content/a.md", false, true},
		{"content/**/*.md", "content/x/y/a.md", false, true},
		{"content/**/*.md", "content/x/y/a.txt", false, false},
		{"**/*.md", "a/b.md", false, true},
		{"data/posts.json", "data/posts.json", false, true},
		{"content", "content/a.md", false, false},
		{"content/*/index.md", "content/a", false, false},
		{"content/*/index.md", "content/a", true, true},
		{"content/*/index.md", "other", true, false},
	}
	for _, tt := range tests {
		if got := MatchWatch(tt.pattern, tt.name, tt.prefix); got != tt.want {
			t.Errorf("MatchWatch(%q, %q, %v) = %v, want %v", tt.pattern, tt.name, tt.prefix, got, tt.want)
		}
	}
}

func TestWatchBase(t *testing.T) {
	tests := []struct {
		pattern, dir string
		recursive    bool
	}{
		{"../*.md", "..", false},
		{"../reference/*.md", "../reference", false},
		{"content/**", "content", true},
		{"content/*/index.md", "content", true},
		{"*.md", ".", false},
		{"**/*.md", ".", true},
		{"data/posts.json", "data", false},
		{"./README.md", ".", false},
	}
	for _, tt := range tests {
		dir, rec := WatchBase(tt.pattern)
		if dir != tt.dir || rec != tt.recursive {
			t.Errorf("WatchBase(%q) = %q, %v; want %q, %v", tt.pattern, dir, rec, tt.dir, tt.recursive)
		}
	}
}
