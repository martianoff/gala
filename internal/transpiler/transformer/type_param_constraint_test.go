package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typeParamConstraintDecls declares an F-bounded interface, Seq[T, C], and
// two types that implement it with C their own type, plus Odd, whose Keep
// returns another type.
const typeParamConstraintDecls = `package main

type Seq[T any, C any] interface {
    Rest() C
    Keep(p func(T) bool) C
}

struct Nums(n int)

func (s Nums) Rest() Nums = Nums(s.n - 1)
func (s Nums) Keep(p func(int) bool) Nums = if (p(s.n)) s else Nums(0)

struct Word(w string)

func (s Word) Rest() Word = s
func (s Word) Keep(p func(string) bool) Word = if (p(s.w)) s else Word("")

struct Odd(n int)

func (s Odd) Rest() Odd = s
func (s Odd) Keep(p func(int) bool) Nums = Nums(s.n)

func dropFirstKeep[T any, C Seq[T, C]](xs C, p func(T) bool) C = xs.Rest().Keep(p)
`

// TestTypeParamConstraintWithTypeArgs: a constraint with type arguments that
// name the list's own parameters lowers verbatim; the constraint's methods
// type-check on a value of the parameter's type, a result typed C keeps its
// type through a chain, and a call infers T from the methods of C's type
// argument. The generated Go type-checks (the oracle runs over every program
// transpiled here).
func TestTypeParamConstraintWithTypeArgs(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "F-bounded constraint lowers verbatim",
			body:     `func main() = Println(dropFirstKeep(Nums(3), (x) => x > 1))`,
			contains: []string{"func dropFirstKeep[T any, C Seq[T, C]](xs C, p func(T) bool) C"},
		},
		{
			name:     "T inferred from C's methods types the lambda",
			body:     `func main() = Println(dropFirstKeep(Word("ab"), (w) => w.Size() > 1))`,
			contains: []string{"func(w string) bool"},
		},
		{
			name: "fixed type argument, lambda typed by the constraint",
			body: `func evens[C Seq[int, C]](xs C) C = xs.Rest().Keep((x) => x % 2 == 0)

func main() = Println(evens(Nums(3)))`,
			contains: []string{"func evens[C Seq[int, C]](xs C) C", "func(x int) bool"},
		},
		{
			name: "a val of a constraint method's result keeps C",
			body: `func twice[T any, C Seq[T, C]](xs C) C {
    val once = xs.Rest()
    once.Rest()
}

func main() = Println(twice(Nums(3)))`,
		},
		{
			name: "generic type with an F-bounded parameter and its methods",
			body: `struct Wrap[T any, C Seq[T, C]](inner C)

func (w Wrap[T, C]) Next() Wrap[T, C] = Wrap[T, C](w.inner.Rest())

func (w Wrap[A, B]) Keep(p func(A) bool) B = w.inner.Keep(p)

func main() = Println(Wrap[int, Nums](Nums(3)).Next().Keep((x) => x > 0))`,
			contains: []string{"type Wrap[T any, C Seq[T, C]] struct"},
		},
		{
			name: "generic struct construction infers T from C's methods",
			body: `struct Box[T any, C Seq[T, C]](inner C)

func main() = Println(Box(Nums(3)))`,
			contains: []string{"Box[int, Nums]"},
		},
		{
			name: "a written leading type argument drives constraint inference",
			body: `func g[C Seq[T, C], T any](xs C, p func(T) bool) C = xs.Keep(p)

func main() = Println(g[Nums](Nums(3), (x) => x > 1))`,
			contains: []string{"func(x int) bool"},
		},
		{
			name: "an annotated lambda needs no inference from the constraint",
			body: `type Labeled[T any] interface {
    Label() string
}

func (s Nums) Label() string = "n"

func describe[T any, C Labeled[T]](c C, show func(T) string) string = c.Label()

func main() = Println(describe(Nums(1), (x int) => s"$x"))`,
		},
		{
			name: "bare constraints keep working",
			body: `type Shower interface {
    Show() string
}

func (s Nums) Show() string = "n"

func show[S Shower](s S) string = s.Show()

func same[K comparable](a K, b K) bool = a == b

func main() {
    Println(show(Nums(1)))
    Println(same(1, 2))
}`,
			contains: []string{"func show[S Shower](s S) string", "func same[K comparable](a K, b K) bool"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newInterfaceImplTranspiler().Transpile(typeParamConstraintDecls+"\n"+tc.body+"\n", "")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}
}

// TestTypeParamConstraintDiagnostics: a method the constraint does not give a
// value of the parameter's type (#1046), a type argument that does not
// implement its instantiated constraint, and a type parameter that only a
// constraint mentions and inference cannot bind are GALA errors, not `go
// build` failures.
func TestTypeParamConstraintDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		code     galaerr.ErrorCode
		contains string
	}{
		{
			name: "method on a parameter constrained by any",
			body: `func size[C any](c C) int = c.Size()

func main() = Println(size(Nums(3)))`,
			code:     galaerr.CodeUnknownMethod,
			contains: "C has no method Size: it is a type parameter constrained by any, which declares no methods",
		},
		{
			name: "method with arguments on a parameter constrained by comparable",
			body: `func has[K comparable](k K) bool = k.Contains(1)

func main() = Println(has(1))`,
			code:     galaerr.CodeUnknownMethod,
			contains: "K has no method Contains: it is a type parameter constrained by comparable",
		},
		{
			name: "method the interface constraint lacks",
			body: `func size[T any, C Seq[T, C]](c C) int = c.Size()

func main() = Println(size(Nums(3)))`,
			code:     galaerr.CodeUnknownMethod,
			contains: "Seq has no method Size",
		},
		{
			name:     "type argument does not implement its constraint",
			body:     `func main() = Println(dropFirstKeep(Odd(1), (x) => x > 0))`,
			code:     galaerr.CodeInterfaceNotImplemented,
			contains: "Odd, the type argument for C of dropFirstKeep, does not implement its constraint Seq[int, Odd]: method Keep returns Nums, not Odd",
		},
		{
			name: "interface type argument does not implement its constraint",
			body: `func dropFirst[T any, C Seq[T, C]](xs C) C = xs.Rest()

func main() {
    val s Seq[int, Nums] = Nums(1)
    Println(dropFirst(s))
}`,
			code:     galaerr.CodeInterfaceNotImplemented,
			contains: "Seq[int, Nums], the type argument for C of dropFirst, does not implement its constraint Seq[int, Seq[int, Nums]]",
		},
		{
			name: "type parameter only a constraint mentions, and no method determines",
			body: `type Labeled[T any] interface {
    Label() string
}

func (s Nums) Label() string = "n"

func describe[T any, C Labeled[T]](c C, show func(T) string) string = c.Label()

func main() = Println(describe(Nums(1), (x) => s"$x"))`,
			code:     galaerr.CodeConstraintInference,
			contains: "cannot infer T in this call of describe: T appears only in Labeled[T], the constraint of C, and the methods of C's type argument Nums do not determine it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newInterfaceImplTranspiler().Transpile(typeParamConstraintDecls+"\n"+tc.body+"\n", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(tc.code))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}
