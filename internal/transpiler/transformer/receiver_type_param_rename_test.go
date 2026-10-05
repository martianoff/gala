package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const renamedReceiverDecls = `package main

struct Box[T any](V T)

func (b Box[U]) Twice(f func(U) U) U = f(f(b.V))
func (b *Box[Elem]) Peek(f func(Elem) string) string = f(b.V)
func (b Box[U]) Map[V any](f func(U) V) Box[V] = Box(f(b.V))
func (b Box[U]) Clash[T any](f func(U) T) T = f(b.V)

struct Pair[A any, B any](L A, R B)

func (p Pair[X, Y]) Swap() Pair[Y, X] = Pair(p.R, p.L)
func (p Pair[X, Y]) Both(f func(X, Y) string) string = f(p.L, p.R)
`

// TestRenamedReceiverTypeParamCalls pins that a call of a method whose
// receiver names its type's parameters differently from the type's
// declaration types its lambda arguments and its result against the
// receiver's type arguments, not the receiver's parameter names.
//
// Every output also goes through the package's Go type-check oracle.
func TestRenamedReceiverTypeParamCalls(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input, want string
	}{
		{"lambda argument", "func g() int = Box(4).Twice((x) => x + 1)", "func(x int) int {"},
		{"pointer receiver", "func g(b *Box[int]) string = b.Peek((x) => s\"$x\")", "func(x int) string {"},
		{"generic method", "func g() Box[string] = Box(4).Map((x) => s\"$x\")", "func(x int) string {"},
		{"method type parameter named like the type's", "func g() string = Box(4).Clash((x) => s\"$x\")", "func(x int) string {"},
		{"swapped result", "func g() string = Pair(1, \"a\").Swap().L", `Pair_Swap[int, string](Pair[int, string]{`},
		{"two parameters", "func g() string = Pair(2, \"b\").Both((n, s) => s)", "func(n int, s string) string {"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(renamedReceiverDecls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
