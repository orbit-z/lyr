package compiler

// The error fixture of the CleanGoOutput golden: hand-written pass-1 files
// with deliberate errors, as codegen lays them out.

import "testing"

const (
	tyCard   = "src/components/Card.lyr"
	tyUICard = "src/components/ui/Card.lyr"
	tyOther  = "src/components/Other.lyr"
	tyBroken = "src/components/Broken.lyr"
	tyIndex  = "src/pages/index.lyr"
)

func tyErrorUnits(t testing.TB) []Unit {
	card := tyFixture(t, tyComp{
		lyr:      tyCard,
		declBody: "\ntype Props struct{ Title string }\n",
		implTop:  "\n" + tyLine(tyCard, 4, 1) + "func helper() {}\n" + tyLine(tyCard, 5, 1) + "func helper() {}\n" + tyLineMark + "\n",
		body: tyDir(tyCard, 6, 1) + "var s string = props" + tyResetMark + "\n\t_ = s\n" +
			"\tlyr__w.Expr(\n" + tyDir(tyCard, 8, 2) + "props.Titel" + tyResetMark + ",\n\t)\n",
	})
	ui := tyFixture(t, tyComp{lyr: tyUICard, declBody: "\ntype Props struct{ Label string }\n"})
	index := tyFixture(t, tyComp{
		lyr:  tyIndex,
		page: true,
		implImports: tyLine(tyIndex, 2, 1) + "Card \"" + testModule + "/lyr_gen/decl/components/card\"\n" +
			tyLine(tyIndex, 3, 1) + "UICard \"" + testModule + "/lyr_gen/decl/components/ui/card\"\n" + tyLineMark + "\n",
		body: tyDir(tyIndex, 5, 1) + "x := Card.Props{}" + tyResetMark + "\n" +
			tyDir(tyIndex, 6, 1) + "var n int = x" + tyResetMark + "\n" +
			tyDir(tyIndex, 7, 1) + "var u UICard.Props = x" + tyResetMark + "\n\t_, _, _ = x, n, u\n" +
			"\tif lyr__err := " + tyDir(tyIndex, 9, 2) + "Card.Render" + tyResetMark + "(lyr__ctx, lyr__w, Card.Props{\n" +
			"\t\t" + tyDir(tyIndex, 9, 7) + "Titel" + tyResetMark + ": \"x\",\n" +
			"\t}, lyr__rt.Slots{}); lyr__err != nil {\n\t\treturn lyr__err\n\t}\n",
	})
	other := tyFixture(t, tyComp{
		lyr:         tyOther,
		implImports: tyLine(tyOther, 2, 8) + "\"fmt\"\n" + tyLineMark + "\n",
		body:        tyDir(tyOther, 4, 1) + "fmt.Printf(\"%d\\n\", \"s\")" + tyResetMark + "\n",
	})
	broken := tyFixture(t, tyComp{
		lyr:      tyBroken,
		declBody: "\n//line ../../../../" + tyBroken + ":2:1\ntype Props struct{ N Missing }\n" + tyLineMark + "\n",
	})
	return []Unit{card, ui, index, other, broken}
}
