package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A struct type called with no arguments, `Foo()`, used to lower to a bare Go
// call `Foo()`, which Go reads as a type conversion needing one argument
// ("missing argument in conversion to Foo"). It must lower to a composite
// literal: `Foo{}` for a zero-field shorthand struct, and for a block-form
// struct with fields the literal in which every field takes its type's
// default, as a positional call gives the fields it leaves out. Non-empty
// shorthand structs (`Foo(x)`) already worked and must keep working. A sealed
// parent is not a plain struct: no literal of it is a value of one of its
// cases, so `Shape()` is GALA-E0043, pointing at a case.
func TestZeroArgStructConstructor(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	cases := []struct {
		name        string
		input       string
		contains    string
		notContains string
		wantErr     []string // substrings of the error; empty means success
	}{
		{
			name: "zero-field shorthand Foo() lowers to composite literal",
			input: `package main

struct Mock()

func (m Mock) Tag() string = "mock"

func main() {
    val m = Mock()
    Println(m.Tag())
}`,
			contains:    "Mock{}",
			notContains: "= Mock()",
		},
		{
			name: "non-empty shorthand struct still uses field literal",
			input: `package main

struct Mock(N int)

func (m Mock) Tag() string = "mock"

func main() {
    val m = Mock(0)
    Println(m.Tag())
}`,
			contains: "Mock{N:",
		},
		{
			name: "block-form Box() lowers to composite literal",
			input: `package main

type Box struct {
    N     int
    Label string
}

func main() {
    val b = Box()
    Println(b.N, b.Label)
}`,
			contains:    "var b = Box{}",
			notContains: "= Box()",
		},
		{
			name: "block-form Box() leaves an Option field at its zero value",
			input: `package main

type Tagged struct {
    Name string
    Note Option[string]
}

func main() {
    val t = Tagged()
    Println(t.Name, t.Note.IsEmpty())
}`,
			contains:    "var t = Tagged{}",
			notContains: "= Tagged()",
		},
		{
			name: "generic block-form struct takes type args from the slot",
			input: `package main

type Pair[A any, B any] struct {
    First  A
    Second B
}

func main() {
    val p Pair[int, string] = Pair()
    Println(p.First, p.Second)
}`,
			contains:    "Pair[int, string]{}",
			notContains: "= Pair()",
		},
		{
			name: "sealed parent Shape() is GALA-E0043, naming a case",
			input: `package main

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}

func main() {
    val s = Shape()
    Println(s)
}`,
			wantErr: []string{"GALA-E0043", "Shape is a type, not a constructor", "`Circle(...)`"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile(tc.input, "")
			if len(tc.wantErr) > 0 {
				if assert.Error(t, err) {
					for _, want := range tc.wantErr {
						assert.Contains(t, err.Error(), want)
					}
				}
				return
			}
			assert.NoError(t, err)
			assert.Contains(t, got, tc.contains)
			if tc.notContains != "" {
				assert.NotContains(t, got, tc.notContains, "Foo() must not lower to a Go type conversion")
			}
		})
	}
}
