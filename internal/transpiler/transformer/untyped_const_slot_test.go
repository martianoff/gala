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
func zero() *big.Int = big.NewInt(0)
func itoa(n int) string = strconv.Itoa(n)
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
		{"no slot", "func g() int {\n    val x = Id(7)\n    x\n}", "var x = Id(7)"},
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

// TestUntypedConstKinds pins that a type parameter only untyped constants
// bind takes the default type of the largest kind among them (int < rune <
// float64 < complex128), and that `true` and `false` are untyped constants
// that bind after a typed argument, as Go infers them (#1360). The lambda's
// result type shows the binding.
func TestUntypedConstKinds(t *testing.T) {
	const decls = `package main

import "cmp"

type MyBool bool

struct Cell[T any](V T)

func (c Cell[T]) Pick[U any](a U, b U) U = b

func Same[U any](a U, b U) U = b

func order(a int, b int) int = cmp.Compare(a, b)
`
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input, want string
	}{
		{"Go callee, int then float", "func g() float64 {\n    val z = cmp.Or(0, 2.5)\n    val w = () => z\n    w()\n}", "func() float64"},
		{"GALA callee, int then float", "func g() float64 {\n    val w = () => Same(0, 2.5)\n    w()\n}", "func() float64"},
		{"GALA callee, int then rune", "func g() rune {\n    val w = () => Same(1, 'a')\n    w()\n}", "func() rune"},
		{"GALA method, int then float", "func g() float64 {\n    val w = () => Cell(1).Pick(0, 2.5)\n    w()\n}", "func() float64"},
		{"a typed argument still wins", "func g(x float32) float32 {\n    val w = () => Same(1, x)\n    w()\n}", "func() float32"},
		{"bool constant before a typed argument", "func g(flag MyBool) MyBool {\n    val w = () => Same(true, flag)\n    w()\n}", "func() MyBool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
