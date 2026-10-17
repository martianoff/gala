package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestImmutableAliasField: a shorthand field with no keyword whose type is an
// alias of Immutable[T] is stored as declared, as one spelled Immutable[T]
// is, and a construction passes its element. The alias may be declared after
// the struct, through another alias, or be generic; an alias of anything else, a `var`
// field and an explicit `val` are wrapped as before.
func TestImmutableAliasField(t *testing.T) {
	out, err := transpileBareVariant(t, `package main

struct S(X IInt, Y Chain, var Z IInt, M Millis, Late Later, val W Immutable[int], G Imm[bool])

type IInt Immutable[int]

type Imm[T any] Immutable[T]

type Chain IInt

type Millis int64

type Later Immutable[string]

func main() {
    val s = S(1, 2, NewImmutable(3), Millis(4), "a", NewImmutable(5), true)
    Println(s.X, s.Y, s.Z.Get(), s.M, s.Late)
}`)
	require.NoError(t, err)
	require.Contains(t, out, "type S struct {\n\tX    IInt\n\tY    Chain\n\tZ    IInt\n\tM    std.Immutable[Millis]\n\tLate Later\n\tW    std.Immutable[std.Immutable[int]]\n\tG    Imm[bool]\n}")
	require.Contains(t, out, "S{X: std.NewImmutable(1), Y: std.NewImmutable(2), Z: std.NewImmutable(3), M: std.NewImmutable(Millis(4)), Late: std.NewImmutable(\"a\"), W: std.NewImmutable(std.NewImmutable(5)), G: std.NewImmutable(true)}")
	require.Contains(t, out, "func NewS(x int, y int, z IInt, m Millis, late string, w std.Immutable[int], g bool) S {")
}
