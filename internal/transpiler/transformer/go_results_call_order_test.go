package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoResultsNamedArgOrder pins a call of a function or method declaring a
// Go result list whose arguments are evaluated into temporaries: named out of
// order, or read by a default. The function literal holding them returns the
// call's Go results, the conversion to one GALA value takes the literal's
// call, and so do the places that read the raw results (#1365).
// examples/go_results_named_order.gala runs them.
func TestGoResultsNamedArgOrder(t *testing.T) {
	const decls = `package main

struct Box[T any](V T)

func (b Box[T]) PlainWrite(u int, p int) (int, error) = Success(p)

func (b Box[T]) Write[U any](u U, p int, q int = p) (int, error) = Success(p + q)

func (b Box[T]) Pair(u int, p int) (int, int) = (u, p)

func (b Box[T]) Conv[U any](u U, p int) (Tuple[T, U], error) = Success((b.V, u))

func write(u int, p int) (int, error) = Success(u * 10 + p)

struct Labeled[T any](Label string, Value T)

func (l Labeled[T]) Pick(a int, b int) (T, error) = Success(l.Value)

func t(name string, v int) int = v

`
	const ordered = "{\n\t\t_tmp_1 := t(\"p\", 1)\n\t\t_tmp_2 := t(\"u\", 2)\n\t\treturn "
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name:     "method",
			main:     `Println(Box(0).PlainWrite(p = t("p", 1), u = t("u", 2)))`,
			contains: "std.GoTry(func() (int, error) " + ordered + "Box[int]{V: std.NewImmutable(0)}.PlainWrite(_tmp_2, _tmp_1)\n\t}())",
		},
		{
			name:     "function",
			main:     `Println(write(p = t("p", 1), u = t("u", 2)))`,
			contains: "std.GoTry(func() (int, error) " + ordered + "write(_tmp_2, _tmp_1)\n\t}())",
		},
		{
			name:     "results naming the receiver's type parameter",
			main:     `Println(Labeled("x", 1).Pick(b = t("b", 1), a = t("a", 2)))`,
			contains: "std.GoTry(func() (int, error) {\n\t\t_tmp_1 := t(\"b\", 1)\n\t\t_tmp_2 := t(\"a\", 2)\n\t\treturn Labeled[int]{Label: std.NewImmutable(\"x\"), Value: std.NewImmutable(1)}.Pick(_tmp_2, _tmp_1)\n\t}())",
		},
		{
			name:     "results without an error",
			main:     `Println(Box(0).Pair(p = t("p", 1), u = t("u", 2)))`,
			contains: "std.GoTuple(func() (int, int) " + ordered,
		},
		{
			name:     "default reading an earlier argument, in written order",
			main:     `Println(Box(0).Write(u = t("u", 1), p = t("p", 2)))`,
			contains: "std.GoTry(func() (int, error) {\n\t\t_tmp_2 := t(\"u\", 1)\n\t\t_tmp_1 := t(\"p\", 2)\n\t\treturn Box_Write(",
		},
		{
			name:     "results bound by name",
			main:     "val n, err = write(p = t(\"p\", 1), u = t(\"u\", 2))\n    Println(n, err)",
			contains: "var n, err = func() (int, error) " + ordered + "write(_tmp_2, _tmp_1)\n\t}()",
		},
		{
			name:     "results assigned by name",
			main:     "var n = 0\n    var e error = nil\n    n, e = write(p = t(\"p\", 1), u = t(\"u\", 2))\n    Println(n, e)",
			contains: "n, e = func() (int, error) " + ordered + "write(_tmp_2, _tmp_1)\n\t}()",
		},
		{
			name:     "Try thunk",
			main:     `Println(Try(() => write(p = t("p", 1), u = t("u", 2))))`,
			contains: "_v0, _err := func() (int, error) {\n\t\t\t_tmp_1 := t(\"p\", 1)",
		},
		{
			name:     "discarded",
			main:     `write(p = t("p", 1), u = t("u", 2))`,
			contains: "\tfunc() (int, error) " + ordered + "write(_tmp_2, _tmp_1)\n\t}()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newThunkSugarTranspiler().Transpile(decls+"func main() {\n    "+tt.main+"\n}", "")
			require.NoError(t, err)
			assert.Contains(t, mainBodyOf(t, out), tt.contains)
		})
	}
	t.Run("receiver type argument spelled like the method's type parameter", func(t *testing.T) {
		const body = `func conv[U any](b Box[U], x int) Try[Tuple[U, int]] = b.Conv(p = t("p", 1), u = t("u", x))`
		out, err := newThunkSugarTranspiler().Transpile(decls+body+"\n\nfunc main() {\n    Println(conv(Box(\"s\"), 2))\n}", "")
		require.NoError(t, err)
		assert.Contains(t, out, "std.GoTry(func() (std.Tuple[U, int], error) {")
	})
	t.Run("function body returning the results", func(t *testing.T) {
		const body = `func passThrough() (int, error) = write(p = t("p", 1), u = t("u", 2))`
		out, err := newThunkSugarTranspiler().Transpile(decls+body+"\n\nfunc main() {\n    Println(passThrough())\n}", "")
		require.NoError(t, err)
		assert.Contains(t, out, "func passThrough() (int, error) {\n\treturn func() (int, error) {\n\t\t_tmp_1 := t(\"p\", 1)\n\t\t_tmp_2 := t(\"u\", 2)\n\t\treturn write(_tmp_2, _tmp_1)\n\t}()\n}")
	})
}
