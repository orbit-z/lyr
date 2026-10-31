package build

import "testing"

func TestLDFlagsWithIslandKey(t *testing.T) {
	const x = "-X github.com/orbit-z/lyr.buildKey=K+/= -X github.com/orbit-z/lyr.buildID=0123456789abcdef"
	for _, tt := range []struct{ goflags, want string }{
		{"", "-ldflags=" + x},
		{"-mod=mod", "-ldflags=" + x},
		{"-ldflags=-s", "-ldflags=-s " + x},
		{"--ldflags=-w -mod=mod", "-ldflags=-w " + x},
		{`-mod=mod "-ldflags=-s -w"`, "-ldflags=-s -w " + x},
		{`-ldflags='-s -w' -ldflags=-w`, "-ldflags=-w " + x},
		{"-ldflags=-s\n", "-ldflags=-s " + x},
	} {
		if got := ldflagsWithIslandKey(tt.goflags, "K+/=", "0123456789abcdef"); got != tt.want {
			t.Errorf("GOFLAGS=%q:\n got %q\nwant %q", tt.goflags, got, tt.want)
		}
	}
}
