package config

import (
	"fmt"
	"path"
	"strings"
)

// Watch patterns ("watch" in lyr.json): slash-separated globs relative to
// the project root, which may start with ../ to reach outside it. Each
// segment is a path.Match pattern, and a segment that is exactly "**"
// matches zero or more directories ("content/**", "../docs/**/*.md").

// CheckWatch reports whether pattern is a valid watch pattern.
func CheckWatch(pattern string) error {
	switch {
	case pattern == "":
		return fmt.Errorf("must not be empty")
	case strings.HasPrefix(pattern, "/") || len(pattern) > 1 && pattern[1] == ':' || strings.Contains(pattern, `\`):
		return fmt.Errorf("must be a slash-separated path relative to the project root, such as \"content/**\"")
	}
	for _, seg := range strings.Split(path.Clean(pattern), "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("segment %q: %v", seg, err)
		}
	}
	return nil
}

// MatchWatch reports whether name, a cleaned slash-separated path relative
// to the project root, matches the watch pattern. With prefix set, it also
// reports true when a path below name could match (for a directory).
func MatchWatch(pattern, name string, prefix bool) bool {
	return matchSegs(strings.Split(path.Clean(pattern), "/"), strings.Split(path.Clean(name), "/"), prefix)
}

func matchSegs(pat, name []string, prefix bool) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:], prefix) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return prefix
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// WatchBase returns the directory to watch for pattern, relative to the
// project root ("." for the root itself): its leading segments without
// glob characters. recursive is set when files below that directory's
// direct children can match.
func WatchBase(pattern string) (dir string, recursive bool) {
	segs := strings.Split(path.Clean(pattern), "/")
	n := 0
	for n < len(segs) && !strings.ContainsAny(segs[n], `*?[`) {
		n++
	}
	if n == len(segs) { // a literal file path
		return path.Dir(path.Clean(pattern)), false
	}
	return path.Join(append([]string{"."}, segs[:n]...)...), len(segs)-n > 1 || segs[n] == "**"
}
