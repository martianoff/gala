package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lambda has its own return slot. A `return`, a `bind` block and a match in
// its body take their type from the lambda — its annotation, the expected
// function type's result, or, when neither is known, from the body itself —
// and never from the enclosing function's return type.
func TestLambdaReturnSlot(t *testing.T) {
	const prelude = `package main

import "strconv"

func apply[T any](f func() T) T = f()

func parse(s string) Try[int] = Try(strconv.Atoi(s))
`
	tests := []struct {
		name        string
		body        string
		contains    []string
		notContains []string
	}{
		{
			name: "returned if-expression of lambdas ignores the enclosing function type",
			body: `
func scaler(k int) func(string) string {
    val scale = apply(() => {
        return if (k > 0) (x int) => x * k else (x int) => x
    })
    return (s) => s"$s:${scale(5)}"
}
`,
			contains:    []string{"func(x int) int {"},
			notContains: []string{"func(x int) string"},
		},
		{
			name: "a later return None() infers from the lambda's first return",
			body: `
func small(n int) Option[string] {
    val o = apply(() => {
        if (n > 5) {
            return Some(n)
        }
        return None()
    })
    return o.Map((v) => s"v=$v")
}
`,
			contains:    []string{"std.None[int]{}"},
			notContains: []string{"std.None[string]"},
		},
		{
			name: "returned match takes the lambda's type",
			body: `
func classify(n int) Option[string] {
    val code = apply(() => {
        return n match {
            case 0 => 10
            case _ => n + 1
        }
    })
    return Some(s"code=$code")
}
`,
			contains: []string{"apply(func() int {"},
		},
		{
			name: "bind in a FlatMap lambda takes its monad from its trailing value",
			body: `
func viaFlatMap(s string) Try[int] =
    parse(s).FlatMap((n) => {
        bind h = parse(s"${n + 1}")
        Success(h * 2)
    })
`,
			contains: []string{"func(_bind_h int) std.Try[int] {", "std.Success[int]{}.Apply(h.Get() * 2)"},
		},
		{
			name: "bind in a Try thunk uses the thunk's result, not the enclosing Try",
			body: `
func viaThunk(s string) Try[Try[int]] = Try(() => {
    bind n = parse(s)
    Success(n + 1)
})
`,
			contains:    []string{"func() std.Try[int] {", "std.Try_FlatMap[int, int](parse(s), func(_bind_n int) std.Try[int] {"},
			notContains: []string{"std.Try[std.Try[std.Try[int]]]", "std.Success[std.Try[int]]"},
		},
		{
			name: "bind in a lambda with a declared function type uses that type",
			body: `
func viaTyped(s string) Option[string] {
    val f func(string) Try[int] = (x) => {
        bind n = parse(x)
        Success(n * 10)
    }
    return f(s).ToOption().Map((v) => s"$v")
}
`,
			contains: []string{"func(_bind_n int) std.Try[int] {"},
		},
		{
			name: "nested lambdas each have their own slot",
			body: `
func nested(n int) string {
    val outer = apply(() => {
        val inner = apply(() => {
            return if (n > 0) n * 2 else 0
        })
        return inner > 2
    })
    return s"nested=$outer"
}
`,
			contains:    []string{"apply(func() bool {", "apply(func() int {"},
			notContains: []string{"func() string"},
		},
		{
			name: "bind in a function body still uses the function's return type",
			body: `
func run(s string) Try[int] {
    bind n = parse(s)
    Success(n + 1)
}
`,
			contains: []string{"std.Try_FlatMap[int, int](parse(s), func(_bind_n int) std.Try[int] {"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile(prelude+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, bad := range tt.notContains {
				assert.NotContains(t, got, bad)
			}
		})
	}
}

// A `bind` block in a lambda of unknown result type must end with a value of
// the block's monad; anything else is rejected with a GALA error, not handed to
// the Go compiler.
func TestLambdaReturnSlotBindErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name: "trailing value of another monad",
			input: `package main

import "strconv"

func apply[T any](f func() T) T = f()

func run(s string) string {
    val r = apply(() => {
        bind n = Try(strconv.Atoi(s))
        Some(n)
    })
    return s"$r"
}
`,
			wantErr: "must end with a std.Try value",
		},
		{
			name: "bind in a function with no return type",
			input: `package main

import "strconv"

func run(s string) {
    bind n = Try(strconv.Atoi(s))
    Success(n)
}
`,
			wantErr: "`bind` requires the enclosing function to declare a monad return type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile(tt.input, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
