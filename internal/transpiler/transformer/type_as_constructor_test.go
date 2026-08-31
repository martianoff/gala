package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

func newTypeAsCtorTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestTypeCalledAsConstructorIsRejected pins B4: calling a type name as though
// it were a constructor must be a GALA error naming the constructor to use.
// It previously reached `go build`, which reported "cannot use generic type
// collection_immutable.Array[T any] without instantiation" — a sentence about
// Go generics for a mistake made in GALA.
func TestTypeCalledAsConstructorIsRejected(t *testing.T) {
	trans := newTypeAsCtorTranspiler()

	cases := []struct {
		name       string
		input      string
		wantInMsg  string
		wantInHint string
	}{
		{
			name: "Array",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = Array(1, 2, 3)
    Println(xs)
}`,
			wantInMsg:  "Array",
			wantInHint: "ArrayOf",
		},
		{
			name: "List",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = List(1, 2, 3)
    Println(xs)
}`,
			wantInMsg:  "List",
			wantInHint: "ListOf",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.Error(t, err, "a type name called as a constructor must be rejected")
			require.Contains(t, err.Error(), string(galaerr.CodeTypeUsedAsConstructor))
			require.Contains(t, err.Error(), tc.wantInMsg)
			require.Contains(t, err.Error(), tc.wantInHint,
				"the hint must name the constructor function to use")
		})
	}
}

// TestConstructiveCallsStillWork is the false-positive guard. Every reading
// that legitimately calls something named like a type is resolved before the
// check and must be untouched: a positional struct constructor, a sealed
// variant constructor, and the real collection constructors.
func TestConstructiveCallsStillWork(t *testing.T) {
	trans := newTypeAsCtorTranspiler()

	inputs := []struct {
		name  string
		input string
	}{
		{
			name: "positional_struct_ctor",
			input: `package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(p.X)
}`,
		},
		{
			name: "sealed_variant_ctor",
			input: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val c = Circle(1.0)
    Println(c)
}`,
		},
		{
			name: "real_collection_ctors",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    val ys = ListOf(1, 2, 3)
    Println(s"${xs.Size()} ${ys.Size()}")
}`,
		},
	}

	for _, tc := range inputs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "main.gala")
			require.NoError(t, err, "this call is legitimate and must still transpile")
		})
	}
}

// TestGoTypeConversionIsNotAConstructorMisuse is the regression guard for a
// false positive found by the stdlib build: `time.Duration(d.nanos)` is an
// ordinary Go type conversion, but time_utils declares its own `Duration`
// struct, and stripping the package qualifier matched it. A qualified callee
// must be looked up only under its qualified name.
func TestGoTypeConversionIsNotAConstructorMisuse(t *testing.T) {
	trans := newTypeAsCtorTranspiler()

	input := `package time_utils

import "time"

type Duration struct {
    nanos int64
}

func (d Duration) ToGoDuration() time.Duration = time.Duration(d.nanos)
`
	_, err := trans.Transpile(input, "time_utils.gala")
	require.NoError(t, err,
		"a Go type conversion must not be reported as a constructor misuse")
}
