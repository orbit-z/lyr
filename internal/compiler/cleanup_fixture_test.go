package compiler

// The error fixture of the CleanGoOutput golden: hand-written pass-1 files
// with deliberate errors, as codegen lays them out.

import "testing"

const (
	tyCard   = "src/components/Card.orbit"
	tyUICard = "src/components/ui/Card.orbit"
	tyOther  = "src/components/Other.orbit"
	tyBroken = "src/components/Broken.orbit"
	tyIndex  = "src/pages/index.orbit"
)

func tyErrorUnits(t testing.TB) []Unit {
	card := tyFixture(t, tyComp{
		orbit:    tyCard,
		declBody: "\ntype Props struct{ Title string }\n",
		implTop:  "\n" + tyLine(tyCard, 4, 1) + "func helper() {}\n" + tyLine(tyCard, 5, 1) + "func helper() {}\n" + tyLineMark + "\n",
		body: tyDir(tyCard, 6, 1) + "var s string = props" + tyResetMark + "\n\t_ = s\n" +
			"\torbit__w.Expr(\n" + tyDir(tyCard, 8, 2) + "props.Titel" + tyResetMark + ",\n\t)\n",
	})
	ui := tyFixture(t, tyComp{orbit: tyUICard, declBody: "\ntype Props struct{ Label string }\n"})
	index := tyFixture(t, tyComp{
		orbit: tyIndex,
		page:  true,
		implImports: tyLine(tyIndex, 2, 1) + "Card \"" + testModule + "/orbit_gen/decl/components/card\"\n" +
			tyLine(tyIndex, 3, 1) + "UICard \"" + testModule + "/orbit_gen/decl/components/ui/card\"\n" + tyLineMark + "\n",
		body: tyDir(tyIndex, 5, 1) + "x := Card.Props{}" + tyResetMark + "\n" +
			tyDir(tyIndex, 6, 1) + "var n int = x" + tyResetMark + "\n" +
			tyDir(tyIndex, 7, 1) + "var u UICard.Props = x" + tyResetMark + "\n\t_, _, _ = x, n, u\n" +
			"\tif orbit__err := " + tyDir(tyIndex, 9, 2) + "Card.Render" + tyResetMark + "(orbit__ctx, orbit__w, Card.Props{\n" +
			"\t\t" + tyDir(tyIndex, 9, 7) + "Titel" + tyResetMark + ": \"x\",\n" +
			"\t}, orbit__rt.Slots{}); orbit__err != nil {\n\t\treturn orbit__err\n\t}\n",
	})
	other := tyFixture(t, tyComp{
		orbit:       tyOther,
		implImports: tyLine(tyOther, 2, 8) + "\"fmt\"\n" + tyLineMark + "\n",
		body:        tyDir(tyOther, 4, 1) + "fmt.Printf(\"%d\\n\", \"s\")" + tyResetMark + "\n",
	})
	broken := tyFixture(t, tyComp{
		orbit:    tyBroken,
		declBody: "\n//line ../../../../" + tyBroken + ":2:1\ntype Props struct{ N Missing }\n" + tyLineMark + "\n",
	})
	return []Unit{card, ui, index, other, broken}
}
