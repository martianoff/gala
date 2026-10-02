package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic struct constructor or companion Apply called with only its leading
// type arguments binds those as written and infers the rest from the
// arguments, as a generic function call does. It never emits the partial list,
// which leaves Go a type parameter with no argument.
func TestPartialConstructorTypeArgs(t *testing.T) {
	const prelude = `package main

struct Pair[A any, B any](First A, Second B)

struct Fn[A any, B any](In A, Run func(A) B)

type Mk[A any, B any] struct {}

func (m Mk[A, B]) Apply(a A, b B) Pair[A, B] = Pair[A, B](a, b)

`
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "positional struct constructor",
			body:     "func main() { Println(Pair[int](1, \"a\").Second) }\n",
			contains: []string{"Pair[int, string]{First: std.NewImmutable(1), Second: std.NewImmutable(\"a\")}"},
		},
		{
			name:     "named struct constructor",
			body:     "func main() { Println(Pair[int64](Second = \"b\", First = 1).Second) }\n",
			contains: []string{"Pair[int64, string]{First: std.NewImmutable[int64](1), Second: std.NewImmutable(\"b\")}"},
		},
		{
			name:     "the written type argument wins over the argument's own type",
			body:     "func main() { Println(Pair[float64](1, true).First) }\n",
			contains: []string{"Pair[float64, bool]{"},
		},
		{
			name:     "a lambda typed by a field over the written type argument",
			body:     "func main() { Println(Fn[int](1, (x) => s\"${x}\").In) }\n",
			contains: []string{"Fn[int, string]{", "func(x int) string"},
		},
		{
			name:     "companion Apply",
			body:     "func main() { Println(Mk[int](2, \"c\").Second) }\n",
			contains: []string{"Mk[int, string]{}.Apply(2, \"c\")"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, prelude+tt.body, tt.contains, []string{"Pair[int]{", "Pair[int64]{", "Mk[int]{"})
		})
	}
}

// A partial list the arguments cannot complete is reported, naming the type
// parameter left without an argument.
func TestPartialConstructorTypeArgsUninferable(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "companion Apply",
			body: "type Mk[A any, B any] struct {}\n\nfunc (m Mk[A, B]) Apply(a A) int = 1\n\nfunc main() { Println(Mk[int](2)) }\n",
			want: "cannot infer type argument B of Mk",
		},
		{
			name: "struct constructor, a type parameter no field names",
			body: "struct Holder[A any, B any](First A)\n\nfunc main() { Println(Holder[int](First = 1).First) }\n",
			want: "cannot infer type argument B of generic struct Holder",
		},
		{
			name: "struct constructor",
			body: "struct Holder[A any, B any](First A, Rest Option[B])\n\nfunc main() { Println(Holder[int](First = 1, Rest = None()).First) }\n",
			// None() takes no type from a field whose type names the unbound B.
			want: "GALA-E0018",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newBindTranspiler().Transpile("package main\n\n"+tt.body, "")
			require.Error(t, err, out)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
