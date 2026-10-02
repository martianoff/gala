package transformer_test

import "testing"

// TestLocalBindingShadowsPackageFunction: a val, parameter or lambda parameter
// named like a package-level function shadows it, as in Go, so a call to the
// bare name is a call to the binding. Its result type and the types its
// arguments are lowered against must come from the binding, not from the
// function's signature: `(f func(string) string) => f("x")` used to be typed
// `int` from a package `func f(x int) int`, and a lambda argument to a
// shadowing parameter `k` had its parameter typed from the package `k`.
//
// `Some(call)` spells the call's inferred type as a type argument, so the
// cases that would otherwise leave the result type to Go pin it there.
func TestLocalBindingShadowsPackageFunction(t *testing.T) {
	tests := []struct {
		name string
		decl string
		want []string
	}{
		{
			name: "lambda parameter shadows a function",
			decl: `func Run() string {
    val g = (f func(string) string) => f("lam")
    return g((s string) => s + "#")
}`,
			want: []string{"func(f func(string) string) string {"},
		},
		{
			name: "parameter shadows a function in argument typing",
			decl: `func Run(k func(func(string) string) string) string = k((s) => s + "k")`,
			want: []string{"k(func(s string) string {"},
		},
		{
			name: "parameter shadows a function in result typing",
			decl: `func Run(f func(string) string) Option[string] = Some(f("p"))`,
			want: []string{`std.Some[string]{}.Apply(f("p"))`},
		},
		{
			name: "local val shadows a function",
			decl: `func Run() Option[bool] {
    val f = (b bool) => !b
    val r = Some(f(true))
    return r
}`,
			want: []string{"std.Some[bool]{}.Apply(f.Get()(true))"},
		},
		{
			name: "nested scope shadows a function only inside it",
			decl: `func Run() Option[int] {
    if true {
        val f = (s string) => s + "!"
        val inner = Some(f("hi"))
        Println(inner.Get() + "?")
    }
    val outer = Some(f(1))
    return outer
}`,
			want: []string{`std.Some[string]{}.Apply(f.Get()("hi"))`, "std.Some[int]{}.Apply(f(1))"},
		},
		{
			name: "parameter shadows a generic function",
			decl: `func Run(id func(string) int) Option[int] = Some(id("x"))`,
			want: []string{`std.Some[int]{}.Apply(id("x"))`},
		},
	}
	for _, pkg := range []string{"main", "lib"} {
		prelude := "package " + pkg + `

func f(x int) int = x + 1

func k(g func(int) int) int = g(1)

func id[T any](x T) T = x
`
		for _, tc := range tests {
			t.Run(pkg+"/"+tc.name, func(t *testing.T) {
				assertTranspiled(t, prelude+"\n"+tc.decl+"\n\nfunc main() {}\n", tc.want, nil)
			})
		}
	}
}
