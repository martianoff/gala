package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLambdaForAnyResult pins a lambda filling a slot whose function type
// declares the result `any`: the lambda's result is `any`, not its body's type,
// which Go would not accept there, including a result holding `any`. A
// generic callee's result still comes from the body.
func TestLambdaForAnyResult(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a struct field",
			body: `struct S(f func() any)
func main() { Println(S(f = () => 1).f()) }`,
			want: "func() any {",
		},
		{
			name: "a function argument",
			body: `func run(f func(int) any) any = f(2)
func main() { Println(run((n) => n * 2)) }`,
			want: "func(n int) any {",
		},
		{
			name: "a typed val",
			body: `func main() {
    val f func() any = () => "x"
    Println(f())
}`,
			want: "func() any {",
		},
		{
			name: "a block body returning values of different types",
			body: `func pick(f func(bool) any) any = f(true)
func main() {
    Println(pick((c) => {
        if c {
            return 1
        }
        return "a"
    }))
}`,
			want: "func(c bool) any {",
		},
		{
			name: "a result holding any",
			body: `import . "martianoff/gala/collection_immutable"

func many(f func() Array[any]) int = f().Size()
func main() { Println(many(() => ArrayOf[any](1, "b"))) }`,
			want: "func() Array[any] {",
		},
		{
			name: "a generic callee infers from the body",
			body: `func apply[T any](f func() T) T = f()
func main() { Println(apply(() => 1)) }`,
			want: "func() int {",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, "package main\n\n"+tc.body+"\n")
			require.NoError(t, err)
			require.Contains(t, out, tc.want)
		})
	}
}
