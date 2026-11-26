package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUntypedShiftArgOrder pins an untyped constant shifted by a value
// (`1 << n`) passed where its arguments are evaluated in order: it stays in
// place, taking its type there as Go gives it, and only its shift count is
// evaluated into a temporary or through EvalOperand. A temporary of the whole
// shift would take its default type, int (#1361, #1363).
// examples/untyped_shift_arg_order.gala runs them.
func TestUntypedShiftArgOrder(t *testing.T) {
	const decls = `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/go_interop"
)

struct Box[T any](V T)

func (b Box[T]) Pick[U any](u U, w int, z U = u) U = z

func t(name string, v int) int = v

func mk64() func(int64, int) int64 = (acc, x) => acc + int64(x)

func nums() List[int] = ListOf(1, 2)

var shift = 2

func bumpInt() int {
    shift = 5
    1
}

func sum(a int64, b int) int64 = a + int64(b)

func half(u int64, w int, z float64 = float64(u) / 2) float64 = z

func (b Box[T]) Fold[U any](u U, f func(U, int) U, z U = u) U = f(z, 1)

func keys() map[int64]int = go_interop.MapEmpty[int64, int]()

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name:     "named out of order to a generic method, U left to Go",
			main:     `Println(nums().FoldLeft(f = mk64(), initial = 1 << t("s", 3)))`,
			contains: "_tmp_2 := mk64()\n\t\t_tmp_3 := t(\"s\", 3)\n\t\treturn List_FoldLeft(_tmp_1, 1<<_tmp_3, _tmp_2)",
		},
		{
			name: "read by a default, U inferred by Go from another argument",
			main: `var n = 2
    Println(Box(0).Fold(f = mk64(), u = 1 << n))`,
			contains: "_tmp_2 := mk64()\n\t\t_tmp_1 := n\n\t\treturn Box_Fold(Box[int]{V: std.NewImmutable(0)}, 1<<_tmp_1, _tmp_2, 1<<_tmp_1)",
		},
		{
			name: "read by a default",
			main: `val bx = Box(0)
    var n = 2
    val r int64 = bx.Pick(w = t("w", 1), u = 1 << n)
    Println(r)`,
			contains: "_tmp_2 := t(\"w\", 1)\n\t\t_tmp_1 := n\n\t\treturn Box_Pick[int64, int](bx, 1<<_tmp_1, _tmp_2, 1<<_tmp_1)",
		},
		{
			name: "shift by a call inside a sum, read by a default",
			main: `val bx = Box(0)
    val r int64 = bx.Pick(w = t("w", 1), u = (1 << t("s", 2)) + 1)
    Println(r)`,
			contains: "_tmp_2 := t(\"w\", 1)\n\t\t_tmp_1 := t(\"s\", 2)\n\t\treturn Box_Pick[int64, int](bx, (1<<_tmp_1)+1, _tmp_2, (1<<_tmp_1)+1)",
		},
		{
			name: "read by a default in another type's context",
			main: `var n = 2
    Println(half(w = t("w", 1), u = 1 << n))`,
			contains: "return half(1<<_tmp_1, _tmp_2, float64(int64(1<<_tmp_1))/2)",
		},
		{
			name: "map key read ahead of the right side's call",
			main: `val m = keys()
    m[1 << shift] += bumpInt()
    Println(m)`,
			contains: "m[1<<std.EvalOperand(shift)]",
		},
		{
			name:     "operand read ahead of a later call",
			main:     `Println(sum(1 << shift, bumpInt()))`,
			contains: "sum(1<<std.EvalOperand(shift), bumpInt())",
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
