package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenericMethodNamedArgOrder pins the evaluation order of named arguments
// to a method with type parameters of its own, lowered to a free function
// (`Box_Gen(b, ...)`): they are evaluated in the order they are written, the
// receiver first, as for a plain method and a generic function (#1301).
// examples/generic_method_named_order.gala runs them.
func TestGenericMethodNamedArgOrder(t *testing.T) {
	const decls = `package main

import . "martianoff/gala/collection_immutable"

struct Box[T any](V T)

func (b Box[T]) Gen[U any](u U, w int) string = s"$u $w"

func t(name string, v int) int = v

func mk() func(int, int) int = (acc, x) => acc + x

func mkBox() Box[int] = Box(0)

func mk64() func(int64, int) int64 = (acc, x) => acc + int64(x)

func nums() List[int] = ListOf(1, 2)

func (b Box[T]) Write[U any](u U, p int) (int, error) = Success(p)

func bumpAt(p *Box[int], n int) int {
    *p = Box(n)
    n
}

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name:     "named out of order",
			main:     `val b = Box(0)` + "\n    " + `Println(b.Gen(w = t("w", 2), u = t("u", 1)))`,
			contains: "_tmp_1 := t(\"w\", 2)\n\t\t_tmp_2 := t(\"u\", 1)\n\t\treturn Box_Gen(b, _tmp_2, _tmp_1)",
		},
		{
			name:     "named in parameter order stays as it is",
			main:     `val b = Box(0)` + "\n    " + `Println(b.Gen(u = t("u", 1), w = t("w", 2)))`,
			contains: `Box_Gen(b, t("u", 1), t("w", 2))`,
		},
		{
			name:     "receiver that makes a call goes first",
			main:     `Println(mkBox().Gen(w = t("w", 2), u = t("u", 1)))`,
			contains: "_tmp_1 := mkBox()\n\t\t_tmp_2 := t(\"w\", 2)\n\t\t_tmp_3 := t(\"u\", 1)\n\t\treturn Box_Gen(_tmp_1, _tmp_3, _tmp_2)",
		},
		{
			name:     "std collection method",
			main:     `Println(nums().FoldLeft(f = mk(), initial = t("i", 0)))`,
			contains: "_tmp_2 := mk()\n\t\t_tmp_3 := t(\"i\", 0)",
		},
		{
			// Not reordered until #1365: a function literal around the
			// call would hide it from the Go-results lift.
			name:     "method with Go results is not wrapped",
			main:     `val b = Box(0)` + "\n    " + `Println(b.Write(p = t("p", 2), u = t("u", 1)))`,
			contains: `Box_Write(b, t("u", 1), t("p", 2))`,
		},
		{
			name:     "receiver read through a pointer goes first",
			main:     "var bx = Box(0)\n    val p = &bx\n    Println(p.Gen(w = bumpAt(p, 7), u = t(\"u\", 1)))",
			contains: "_tmp_1 := *p\n\t\t_tmp_2 := bumpAt(p, 7)",
		},
		{
			name:     "untyped shift keeps its place and the parameter's type",
			main:     "var n = 2\n    val w int64 = nums().FoldLeft(f = mk64(), initial = 1 << n)\n    Println(w)",
			contains: "List_FoldLeft[int64, int](nums(), 1<<n, mk64())",
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
