package main

import "github.com/orbit-z/orbit/bench/devloop/synth"

// The synthetic project lives in package synth, shared with bench/lsp;
// these wrappers keep this command's names.

type shape struct{ synth.Shape }

func newShape(files int) shape { return shape{synth.NewShape(files)} }

func (s shape) chain() int            { return s.Chain() }
func (s shape) target() int           { return s.Target() }
func (s shape) badgeImporters() []int { return s.BadgeImporters() }

func compPath(i int) string { return synth.CompPath(i) }

const (
	badgePath = synth.BadgePath
	dataPath  = synth.DataPath
)

func projectFiles(s shape, salt string) map[string]string { return synth.ProjectFiles(s.Shape, salt) }

func dataGo(salt, api string) string { return synth.DataGo(salt, api) }
func badgeOrbit(extra string) string { return synth.BadgeOrbit(extra) }

func componentOrbit(i, n int, badge bool, tmpl, front, css, ts string) string {
	return synth.ComponentOrbit(i, n, badge, tmpl, front, css, ts)
}

func newPage(k int) (name, src string) { return synth.NewPage(k) }

func writeProject(dir, orbitRoot string, files map[string]string) error {
	return synth.WriteProject(dir, orbitRoot, files)
}

func writeFile(dir, rel, content string) error { return synth.WriteFile(dir, rel, content) }
