package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultReadsUntypedArg pins a default that reads an earlier parameter
// passed an untyped constant (or a constant shifted by a value): the default
// reads it at the parameter's declared numeric type, so it sees the
// parameter's value (#1427). A constant goes through std.EvalOperand, a value
// Go does not fold, so the default wraps and truncates as the parameter does.
// examples/default_reads_untyped_arg.gala runs them.
func TestDefaultReadsUntypedArg(t *testing.T) {
	const decls = `package main

import "math"

// Every case refers to math.
func maxI8() int = math.MaxInt8

func narrow(u uint8, z int = int(u)) int = z

func half(u int64, z float64 = float64(u / 2)) float64 = z

func kind(u int64, z any = u) string = f"$z%T"

func neg(u uint8, z int = int(-u)) int = z

func kindC(u complex128, z any = u) string = f"$z%T"

func pick[U any](u U, z U = u) U = z

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name: "shift by a val",
			main: `val n = 8
    Println(narrow(u = 1 << n))`,
			contains: "narrow(1<<n, int(std.EvalOperand[uint8](1<<n)))",
		},
		{
			name: "shift by a var",
			main: `var n = 8
    Println(narrow(u = 1 << n))`,
			contains: "return narrow(1<<_tmp_1, int(uint8(1<<_tmp_1)))",
		},
		{
			name:     "Go package constant",
			main:     `Println(kind(math.MaxInt8))`,
			contains: "kind(math.MaxInt8, std.EvalOperand[int64](math.MaxInt8))",
		},
		{
			name: "caller local named like the type keeps the plain read",
			main: `val int64 = 2
    Println(int64, kind(5))`,
			contains: "kind(5, 5)",
		},
		{
			name:     "constant in a conversion",
			main:     `Println(half(7.0))`,
			contains: "half(7.0, float64(std.EvalOperand[int64](7.0)/2))",
		},
		{
			name:     "constant in an any slot",
			main:     `Println(kind(5))`,
			contains: "kind(5, std.EvalOperand[int64](5))",
		},
		{
			name:     "constant the parameter's type wraps",
			main:     `Println(neg(1))`,
			contains: "neg(1, int(-std.EvalOperand[uint8](1)))",
		},
		{
			name:     "complex parameter",
			main:     `Println(kindC(5))`,
			contains: "kindC(5, std.EvalOperand[complex128](5))",
		},
		{
			name:     "type parameter left to Go",
			main:     `Println(pick(5))`,
			contains: "pick(5, 5)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newThunkSugarTranspiler().Transpile(decls+"func main() {\n    "+tt.main+"\n}", "")
			require.NoError(t, err)
			assert.Contains(t, mainBodyOf(t, out), tt.contains)
		})
	}
}
