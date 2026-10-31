package pin

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/config"
)

func TestFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		bi   debug.BuildInfo
		want Self
	}{
		{"go install @v1.2.3", debug.BuildInfo{Main: debug.Module{Path: Module, Version: "v1.2.3"}}, Self{Version: "v1.2.3"}},
		{"go build in a clone", debug.BuildInfo{Main: debug.Module{Path: Module, Version: "(devel)"}}, Self{Version: "(devel)"}},
		{"go tool lyr", debug.BuildInfo{Main: debug.Module{Path: "example.com/blog"}, Deps: []*debug.Module{{Path: Module, Version: "v1.2.3"}}}, Self{Pinned: true, Version: "v1.2.3"}},
		{"go tool lyr, local replace", debug.BuildInfo{Main: debug.Module{Path: "example.com/blog"}, Deps: []*debug.Module{{Path: Module, Version: "v0.0.0", Replace: &debug.Module{Path: "/src/lyr"}}}}, Self{Pinned: true, Version: "(devel)"}},
		{"no lyr module", debug.BuildInfo{Main: debug.Module{Path: "example.com/x"}}, Self{}},
	}
	for _, tt := range tests {
		if got := fromBuildInfo(&tt.bi); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestDecide(t *testing.T) {
	const (
		head    = "module example.com/blog\n\ngo 1.27\n"
		tool    = "tool github.com/orbit-z/lyr/cmd/lyr\n"
		req     = "require github.com/orbit-z/lyr v0.1.0\n"
		local   = "replace github.com/orbit-z/lyr => ../lyr\n"
		remote  = "replace github.com/orbit-z/lyr => example.com/fork v0.1.1\n"
		release = "v0.1.0"
	)
	global := Self{Version: release}
	tests := []struct {
		name     string
		gomod    string
		self     Self
		reexec   bool
		mismatch bool
	}{
		{"tool directive", head + tool + req, global, true, false},
		{"tool directive, other version", head + tool + req, Self{Version: "v0.2.0"}, true, false},
		{"tool directive, already pinned", head + tool + req, Self{Pinned: true, Version: release}, false, false},
		{"same version", head + req, global, false, false},
		{"other version", head + req, Self{Version: "v0.2.0"}, false, true},
		{"devel CLI", head + req, Self{Version: "(devel)"}, false, true},
		{"local replace", head + req + local, Self{Version: "(devel)"}, false, false},
		{"module replace", head + req + remote, Self{Version: "(devel)"}, false, true},
		{"no lyr requirement", head, Self{Version: "(devel)"}, false, false},
		{"pinned, no tool directive", head + req, Self{Pinned: true, Version: "v0.2.0"}, false, false},
	}
	for _, tt := range tests {
		mod, err := config.ParseGoMod([]byte(tt.gomod))
		if err != nil {
			t.Fatal(err)
		}
		d := Decide(mod, tt.self)
		if d.Reexec != tt.reexec || (d.Err != nil) != tt.mismatch {
			t.Errorf("%s: got %+v, want reexec %v, mismatch %v", tt.name, d, tt.reexec, tt.mismatch)
		}
	}

	mod, _ := config.ParseGoMod([]byte(head + req))
	err := Mismatch(mod, Self{Version: "v0.2.0"})
	want := "version mismatch: this lyr is v0.2.0, but go.mod requires github.com/orbit-z/lyr v0.1.0; pin the CLI with go get -tool github.com/orbit-z/lyr/cmd/lyr@v0.1.0, then run go tool lyr (see lyr doctor)"
	if err == nil || err.Error() != want {
		t.Errorf("Mismatch:\n got %v\nwant %s", err, want)
	}
	if err := Mismatch(mod, Self{}); err == nil || !strings.Contains(err.Error(), "this lyr is unknown") {
		t.Errorf("Mismatch(unknown): %v", err)
	}
}
