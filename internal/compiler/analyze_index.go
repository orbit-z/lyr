package compiler

import (
	"path"
	"strings"
)

func newComponentInfo(f *File, a *Analysis) *ComponentInfo {
	pkg, _ := PackageFor(a.Info.Path)
	ci := &ComponentInfo{
		Path:  a.Info.Path,
		Role:  a.Info.Role,
		Pkg:   pkg,
		Name:  strings.TrimSuffix(path.Base(a.Info.Path), ".orbit"),
		ID:    ComponentID(a.Info.Path),
		Props: a.Props,
	}
	if f == nil || f.Template == nil {
		return ci
	}
	seen := map[string]bool{}
	Walk(f.Template, func(n Node) bool {
		sl, ok := n.(*Slot)
		if !ok {
			return true
		}
		name := "default"
		if sl.NameAttr != nil {
			if !sl.NameAttr.IsStatic() {
				return true
			}
			name = sl.NameAttr.Value
		}
		if !seen[name] {
			seen[name] = true
			ci.Slots = append(ci.Slots, name)
		}
		return true
	})
	return ci
}
