package compiler

// The fixture front end of the codegen tests: the real parser and Analyze
// (part A), with the FileInfo derived from the path as project discovery
// (milestone 5) will derive it.

import (
	"strings"
	"testing"
)

// cgFileInfo derives a file's role and route from its project-relative path.
func cgFileInfo(lyrPath string) FileInfo {
	info := FileInfo{Path: lyrPath}
	if strings.HasPrefix(lyrPath, "src/pages/") {
		info.Role = RolePage
		route := strings.TrimSuffix(strings.TrimPrefix(lyrPath, "src/pages"), ".lyr")
		route = strings.TrimSuffix(route, "/index")
		if route == "" {
			route = "/"
		}
		info.Route = route
	}
	return info
}

// cgAnalyze parses src and analyzes it with the real Analyze.
func cgAnalyze(t testing.TB, lyrPath, src string, mode Mode) (*File, *Analysis) {
	t.Helper()
	f := Parse(src, mode)
	return f, Analyze(f, cgFileInfo(lyrPath))
}
