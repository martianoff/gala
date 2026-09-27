package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOracleFindings pins the transpiler bugs the generated-Go oracle found in
// existing tests' output. Each produced Go that does not compile; the oracle
// also checks every case here, so these assertions only name the shape.
func TestOracleFindings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		mustHave []string
		mustMiss []string
	}{
		{
			// A named-argument variant call took a path without type-argument
			// inference and emitted an uninstantiated `box{}`.
			name: "named-argument generic variant constructor gets its type argument",
			input: `package main

sealed type Box[T any] {
    case box(value T)
}

func (b Box[T]) Apply(body func() T) Box[T] = box(value = body())
func (b Box[T]) Value() T = b.value

func main() {
    Println(Box(() => 7).Value())
}`,
			mustHave: []string{"box[T]{}.Apply(body())"},
			mustMiss: []string{"box{}.Apply("},
		},
		{
			// Hindley-Milner collapsed `func() int` to `int`, so an
			// if-expression over function values was typed as their result.
			name: "if-expression over nullary function values keeps the function type",
			input: `package main

func ident(f func() int) func() int = f

func loop(n int, acc func() int) func() int =
    if (n <= 0) acc else loop(n - 1, ident(() => n))

func main() {
    Println(loop(3, () => 0)())
}`,
			mustHave: []string{"return func() func() int {"},
		},
		{
			// Both branches void: the closure has no result and the calls are
			// statements, not `return voidA()` from a `func() any`/`func() void`.
			name: "void if-expression runs its branches as statements",
			input: `package main

func voidA() {}
func voidB() {}

func plain(flag bool) {
    if (flag) voidA() else voidB()
}

func main() {
    plain(true)
}`,
			mustHave: []string{"func() {\n\t\tif flag {\n\t\t\tvoidA()\n\t\t} else {\n\t\t\tvoidB()\n\t\t}\n\t}()"},
			mustMiss: []string{"return voidA()", "func() any", "func() void"},
		},
		{
			// `import "martianoff/gala/std"` written in the source was emitted
			// alongside the transformer's own std import.
			name: "explicit std import is not duplicated",
			input: `package main

import "martianoff/gala/std"

func pair() std.Tuple[int, int] = std.Tuple[int, int](V1 = 1, V2 = 2)

func main() {
    Println(pair())
}`,
			mustHave: []string{"import \"martianoff/gala/std\"\n\nfunc pair()"},
		},
		{
			// An alias called on a value of the type it names is a conversion,
			// not a construction that puts the value in the first field.
			name: "alias of a sealed type converts rather than constructs",
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
			mustHave: []string{"Figure(Dot{}.Apply()).Tag()"},
			mustMiss: []string{"Figure{R:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			out, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			for _, s := range tt.mustHave {
				assert.Contains(t, out, s)
			}
			for _, s := range tt.mustMiss {
				assert.NotContains(t, out, s)
			}
		})
	}
}
