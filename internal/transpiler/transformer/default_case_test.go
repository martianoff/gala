package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestDefaultCase pins how a sealed type's default case shapes its tags and the
// tail of an exhaustive match: the default case is tag 0, other cases count
// from 1, and only a type without a default case has a zero value no arm
// matches.
func TestDefaultCase(t *testing.T) {
	const decls = `package main

sealed type Mode {
    case On(Level int)
    default case Off()
}

sealed type Bit {
    case One()
    case Zero()
}
`
	cases := []struct {
		name      string
		body      string
		contains  []string
		wantError string
	}{
		{
			name:     "the default case is tag 0",
			body:     `func main() { Println(Off()) }`,
			contains: []string{"_Mode_On  uint8 = 1", "_Mode_Off uint8 = 0", "_Bit_One  uint8 = 1", "_Bit_Zero uint8 = 2"},
		},
		{
			name: "a type with a default case ends in unreachable",
			body: `func f(m Mode) int = m match {
    case On(l) => l
    case Off() => 0
}
func main() { Println(f(Off())) }`,
			contains: []string{`panic("unreachable")`},
		},
		{
			name: "a default case holding the type itself",
			body: `sealed type Tree {
    default case Node(L Tree, R Tree)
    case Leaf()
}
func main() { Println(Leaf()) }`,
			wantError: `the default case "Node" of sealed type "Tree" has a field "L" of type Tree itself`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), string(galaerr.CodeInvalidDefaultCase))
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.contains {
				require.Contains(t, out, want)
			}
		})
	}
}

// TestCaseFieldDefaults pins sealed case field defaults: an omitted field
// takes its default, a field without one cannot follow one with, and a
// construction leaving out a required field, naming no field or passing too
// many arguments is reported.
func TestCaseFieldDefaults(t *testing.T) {
	const decls = `package main

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}

sealed type Shape {
    case Circle(Radius int, Label string = "circle")
    case Square(Side int)
}
`
	cases := []struct {
		name      string
		body      string
		contains  []string
		wantCode  galaerr.ErrorCode
		wantError string
	}{
		{
			name:     "omitted fields take their defaults",
			body:     `func main() { Println(On(), On(Power = 5), Circle(Radius = 2)) }`,
			contains: []string{`On{}.Apply(30, "Living")`, `On{}.Apply(5, "Living")`, `Circle{}.Apply(2, "circle")`},
		},
		{
			name:      "a required field left out of an empty call",
			body:      `func main() { Println(Square()) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `missing required field "Side" in construction of "Square"`,
		},
		{
			name:      "a named argument matching no field",
			body:      `func main() { Println(Circle(Radius = 1, Labl = "x")) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `unknown field "Labl" in construction of "Circle"`,
		},
		{
			name:      "a positional argument after a named one",
			body:      `func main() { Println(Circle(Label = "x", 5)) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `a positional argument follows a named one in construction of "Circle"`,
		},
		{
			name: "a generic case built by position and by name",
			body: `sealed type Box[T any] {
    case Wrap(V T, Tag string = "x")
    case Nothing()
}
func main() { Println(Wrap(5, Tag = "y"), Wrap(6)) }`,
			contains: []string{`Wrap[int]{}.Apply(5, "y")`, `Wrap[int]{}.Apply(6, "x")`},
		},
		{
			name: "a generic case called empty takes its type from the slot",
			body: `sealed type Opt[T any] {
    case Empty(N int = 0)
    case Full(V T)
}
func main() {
    val o Opt[string] = Empty()
    Println(o)
}`,
			contains: []string{`Empty[string]{}.Apply(0)`},
		},
		{
			name:      "more arguments than fields",
			body:      `func main() { Println(Circle(1, "a", "b")) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `"Circle" has 2 fields, but 3 arguments are given`,
		},
		{
			name:      "a required field left out by name",
			body:      `func main() { Println(Circle(Label = "x")) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `missing required field "Radius" in construction of "Circle"`,
		},
		{
			name: "a field without a default after one with",
			body: `sealed type Bad {
    case B(A int = 1, C int)
}
func main() { Println(B(C = 2)) }`,
			wantCode:  galaerr.CodeParamMissingDefaultAfterDefault,
			wantError: `parameter "C" in B has no default but follows a parameter with a default`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), string(tc.wantCode))
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.contains {
				require.Contains(t, out, want)
			}
		})
	}
}
