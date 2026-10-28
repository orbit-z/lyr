package compiler

import (
	"go/constant"
	gotoken "go/token"
	"strings"
	"testing"
)

// TestFoldPieceFloat checks that a constant float piece folds to the text
// the runtime's Float and Float32 write (JavaScript's String(x)).
func TestFoldPieceFloat(t *testing.T) {
	for _, c := range []struct {
		lit  string
		kind WriteKind
		want string
	}{
		{"1.5", WriteFloat, "1.5"},
		{"1e6", WriteFloat, "1000000"},
		{"1e21", WriteFloat, "1e+21"},
		{"1e-7", WriteFloat, "1e-7"},
		{"0.000001", WriteFloat, "0.000001"},
		{"-2.5", WriteFloat, "-2.5"},
		{"3", WriteFloat, "3"},
		{"0.1", WriteFloat32, "0.1"},
		{"1e6", WriteFloat32, "1000000"},
	} {
		tok := gotoken.FLOAT
		if !strings.ContainsAny(c.lit, ".e") {
			tok = gotoken.INT
		}
		lit, neg := strings.CutPrefix(c.lit, "-")
		v := constant.MakeFromLiteral(lit, tok, 0)
		if neg {
			v = constant.UnaryOp(gotoken.SUB, v, 0)
		}
		got, ok := foldPiece(v, TypedWrite{Kind: c.kind})
		if !ok || got != c.want {
			t.Errorf("foldPiece(%s, %v) = %q, %v, want %q", c.lit, c.kind, got, ok, c.want)
		}
	}
}
