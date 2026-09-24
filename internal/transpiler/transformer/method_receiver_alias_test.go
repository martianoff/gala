package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestLegalMethodReceiverAliasesAccepted pins the other side of GALA-E0048:
// the receiver shapes Go accepts must keep transpiling.
//
// The error cases live in the GALA-E0048 rows of TestErrorPathAssertions. This
// is the complement, because a check that rejects a receiver Go would have
// allowed is the more damaging failure — it turns working code into a hard
// error, and the corpus contains no method-on-alias to catch it.
func TestLegalMethodReceiverAliasesAccepted(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name: "alias to a struct declared in this package",
			input: `package main

struct Point(X int, Y int)

type Coord Point

func (c Coord) Sum() int = c.X + c.Y

func main() {
    Println(Coord(1, 2).Sum())
}`,
			expect: "func (c Coord) Sum() int",
		},
		{
			name: "alias to a sealed type declared in this package",
			input: `package main

sealed type Shape {
    case Circle(R float64)
    case Dot()
}

type Figure Shape

func (f Figure) Tag() string = "f"

func main() {
    Println(Figure(Dot()).Tag())
}`,
			expect: "func (f Figure) Tag() string",
		},
		{
			name: "alias to a pointer to a local type puts the method on that type",
			input: `package main

struct Point(X int, Y int)

type PP *Point

func (p PP) Doubled() int = 2

func main() {
    Println(1)
}`,
			expect: "func (p PP) Doubled() int",
		},
		{
			name: "a chain of aliases ending at a local struct",
			input: `package main

struct Point(X int, Y int)

type A Point
type B A

func (b B) Sum() int = b.X + b.Y

func main() {
    Println(B(1, 2).Sum())
}`,
			expect: "func (b B) Sum() int",
		},
		{
			name: "a plain struct receiver is untouched by the check",
			input: `package main

struct Point(X int, Y int)

func (p Point) Sum() int = p.X + p.Y

func main() {
    Println(Point(1, 2).Sum())
}`,
			expect: "func (p Point) Sum() int",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "method_receiver_alias_test.gala")
			require.NoError(t, err, "a receiver Go accepts must not be rejected")
			require.True(t, strings.Contains(out, tc.expect),
				"expected generated Go to declare %q, got:\n%s", tc.expect, out)
		})
	}
}
