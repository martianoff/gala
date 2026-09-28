package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newValueCalledTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	return newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}

// valueCalledProgram declares values of several non-function types, and of
// function types, around the statements in body.
func valueCalledProgram(body string) string {
	return `package main

struct Color(N int, F func(int) int)

sealed type Shape {
    case Circle(R int)
}

struct Adder(K int)

struct Counter(var Hits int)

func (a Adder) Apply(x int) int = a.K + x

struct Ticker(K int)

func (k Ticker) Apply() int = k.K

struct Slot(var H Option[func(int) int])

struct Item(N int)

struct Box[Item any](V Item)

type Op func(int) int

val Red = Color(1, (x int) => x + 1)
val Dflt Shape = Circle(1)
val AddTwo = Adder(2)
val Inc = (x int) => x + 1
val Twice Op = (x int) => x * 2

func main() {
    ` + body + `
}
`
}

// Calling a value whose type is not a function is a GALA error at the call.
// It used to reach go build, which reported the generated `Red.Get()()` —
// "cannot call Red.Get() (value of struct type Color): Color is not a function".
func TestValueCalledAsFunctionIsRejected(t *testing.T) {
	trans := newValueCalledTranspiler()
	cases := []struct {
		name     string
		body     string
		wantMsg  string
		wantHint string
	}{
		{"package val, no arguments", `Println(Red())`, "Red is a val of type Color, not a function", "`Red`"},
		{"package val, with arguments", `Println(Red(1))`, "Red is a val of type Color, not a function", "`Red`"},
		{"sealed val", `Println(Dflt())`, "Dflt is a val of type Shape, not a function", "`Dflt`"},
		{"local val", "val c = Color(2, Inc)\n    Println(c())", "c is a val of type Color, not a function", "`c`"},
		{"local int val", "val n = 3\n    Println(n(1))", "n is a val of type int, not a function", "`n`"},
		{"local string val", "val s = \"x\"\n    Println(s())", "s is a val of type string, not a function", "`s`"},
		{"local var", "var v = Color(2, Inc)\n    v = Red\n    Println(v())", "v is a value of type Color, not a function", "`v`"},
		{"field, no arguments", `Println(Red.N())`, "Color.N is a field of type int, not a function", "`.N`"},
		{"field, with arguments", `Println(Red.N(1))`, "Color.N is a field of type int, not a function", "`.N`"},
		{"var field", "val k = Counter(1)\n    Println(k.Hits())", "Counter.Hits is a field of type int, not a function", "`.Hits`"},
		// Checked before the arguments, so a named argument cannot preempt it.
		{"named argument", `Println(Red(N = 2))`, "Red is a val of type Color, not a function", "`Red`"},
		{"std type", "val o = Some(1)\n    Println(o())", "o is a val of type Option[int], not a function", "`o`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(valueCalledProgram(tc.body), "main.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeValueCalledAsFunction))
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Contains(t, err.Error(), tc.wantHint)
		})
	}
}

// A parameter is checked like any other binding.
func TestValueCalledAsFunctionParameter(t *testing.T) {
	trans := newValueCalledTranspiler()
	src := valueCalledProgram(`Println(useParam(Red))`) + "\nfunc useParam(c Color) int = c()\n"
	_, err := trans.Transpile(src, "main.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "c is a value of type Color, not a function")
}

// Values that ARE callable are untouched: a val holding a lambda, a val of a
// named function type, a val whose type has an Apply method, and a field of
// function type.
func TestCallableValuesStillWork(t *testing.T) {
	trans := newValueCalledTranspiler()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"val holding a lambda", `Println(Inc(1))`, "Inc.Get()(1)"},
		{"val of a named function type", `Println(Twice(1))`, "Twice.Get()(1)"},
		{"val with an Apply method", `Println(AddTwo(1))`, "AddTwo.Get().Apply(1)"},
		{"local lambda val", "val f = (x int) => x + 1\n    Println(f(1))", "f.Get()(1)"},
		{"field of function type", `Println(Red.F(1))`, "Red.Get().F.Get()(1)"},
		{"val with a zero-argument Apply method", "val tick = Ticker(3)\n    Println(tick())", "tick.Get().Apply()"},
		// The user's own .Get() on a var field has the shape of a val field's
		// unwrap; the call is of what it returned, not of the field.
		{"call of a var field's Get", "val s = Slot(Some((x int) => x + 1))\n    Println(s.H.Get()(1))", "s.Get().H.Get()(1)"},
		// A field typed by a type parameter is not judged, even when the
		// parameter shares its name with a struct.
		{"field typed by a type parameter", "val b = Box((x int) => x + 1)\n    Println(b.V(1))", "b.Get().V.Get()(1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(valueCalledProgram(tc.body), "main.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
			checkGeneratedGo(t, out)
		})
	}
}

// Another package's vals are checked too, through a qualified reference and a
// dot import.
func TestImportedValueCalledAsFunctionIsRejected(t *testing.T) {
	root := crossPkgValFixture(t)
	cases := []struct {
		name    string
		src     string
		wantMsg string
	}{
		{
			name: "qualified",
			src: `package main

import "example.com/xpkg/colors"

func main() {
    Println(colors.Green())
}`,
			wantMsg: "colors.Green is a val of type",
		},
		{
			name: "dot import",
			src: `package main

import . "example.com/xpkg/colors"

func main() {
    Println(Green())
}`,
			wantMsg: "Green is a val of type",
		},
		{
			name: "imported int val",
			src: `package main

import "example.com/xpkg/colors"

func main() {
    Println(colors.Limit())
}`,
			wantMsg: "colors.Limit is a val of type int, not a function",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeValueCalledAsFunction))
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}

	// An imported val whose type has Apply is still called through it.
	out, err := transpileCrossPkg(t, root, `package main

import "example.com/xpkg/colors"

func main() {
    Println(colors.AddTen(1))
}`)
	require.NoError(t, err)
	assert.Contains(t, out, "colors.AddTen.Get().Apply(1)")
}
