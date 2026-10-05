package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const untypedConstSlotDecls = `package main

import (
    "math/big"
    "strconv"
)

struct Cell[T any](V T)

func (c Cell[T]) Const[U any](z U) U = z

func Id[U any](z U) U = z
func Wrap[U any](z U) Cell[U] = Cell(z)
func First[A any, B any](a A, b B) A = a
func Same[U any](a U, b U) U = b
func takes(x int64) int64 = x
func echo(s string) string = s
`

// TestUntypedConstGenericSlot pins that a generic call whose type parameter
// only untyped constant arguments bind spells the type the slot it fills
// gives that parameter: Go would infer the constant's default type, which the
// slot cannot hold. A typed argument, a slot that agrees with the default and
// a call with no slot leave the parameter to Go.
//
// Every output also goes through the package's Go type-check oracle.
func TestUntypedConstGenericSlot(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input, want string
	}{
		{"function result", "func g() int64 = Id(0)", "return Id[int64](0)"},
		{"generic result type", "func g() Cell[int64] = Wrap(0)", "return Wrap[int64](0)"},
		{"annotated val", "func g() int64 {\n    val x int64 = Id(1)\n    x\n}", "Id[int64](1)"},
		{"float slot", "func g() float64 = Id(1)", "return Id[float64](1)"},
		{"GALA function argument", "func g() int64 = takes(Id(2))", "takes(Id[int64](2))"},
		{"Go function argument", "func g() string = strconv.FormatInt(Id(3), 10)", "strconv.FormatInt(Id[int64](3), 10)"},
		{"Go method argument", "func g(b *big.Int) *big.Int = b.SetInt64(Id(9))", "b.SetInt64(Id[int64](9))"},
		{"Go function argument to a generic method", "func g() string = strconv.FormatInt(Cell(1).Const(4), 10)", "strconv.FormatInt(Cell_Const[int64, int]("},
		{"only the type arguments up to the constant's are spelled", "func g() int64 = First(5, \"x\")", `First[int64](5, "x")`},
		{"a typed argument binds the parameter", "func g(y int64) int64 = Same(y, 2)", "Same(y, 2)"},
		{"the slot agrees with the default", "func g() int = Id(6)", "return Id(6)"},
		{"no slot", "func g() int {\n    val x = Id(7)\n    x\n}", "std.NewImmutable(Id(7))"},
		{"an any slot", "func g() any = Id(8)", "return Id(8)"},
		{"a string constant in a slot of its default", "func g() string = echo(Id(\"s\"))", `echo(Id("s"))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(untypedConstSlotDecls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
