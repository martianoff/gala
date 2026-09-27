package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic struct's field may share its name with another generic type of the
// same package (`struct Box[T any](Mode Mode[T])`). When such a field is the
// scrutinee of a `match`, the match lowers to an IIFE whose parameter type is
// the field's type. That type must be the field's declared generic, `Mode[T]`,
// and never the field's type re-applied to the type arguments, `Mode[T][T]`,
// which is not valid Go.
func TestGenericFieldNamedLikeGenericTypeMatchParam(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	const types = `
sealed type Mode[T any] {
    case A(Fn func(int) T)
    case B(Fn func(string) T)
}

struct Model[M any](State M)
`
	tests := []struct {
		name string
		pkg  string
		decl string
		want []string
	}{
		{
			name: "match on the field in a method",
			pkg:  "main",
			decl: `struct Box[T any](Mode Mode[T])

func (b Box[T]) Run(x int) T = b.Mode match {
    case A(fn) => fn(x)
    case B(fn) => fn("")
}`,
			want: []string{"Mode std.Immutable[Mode[T]]", "func(obj Mode[T]) T {", "}(b.Mode.Get())"},
		},
		{
			name: "match on the field in a generic function",
			pkg:  "main",
			decl: `struct Box[T any](Mode Mode[T])

func kind[T any](b Box[T]) string = b.Mode match {
    case A(_) => "a"
    case B(_) => "b"
}`,
			want: []string{"func(obj Mode[T]) string {"},
		},
		{
			name: "match through a field access chain",
			pkg:  "main",
			decl: `struct Box[T any](Mode Mode[T])
struct Outer[T any](Box Box[T])

func (o Outer[T]) Deep(x int) T = o.Box.Mode match {
    case A(fn) => fn(x)
    case B(fn) => fn("")
}`,
			want: []string{"Box std.Immutable[Box[T]]", "func(obj Mode[T]) T {", "}(o.Box.Get().Mode.Get())"},
		},
		{
			name: "two type parameters in a library package",
			pkg:  "lib",
			decl: `struct Harness[M any, T any](Model Model[M], Mode Mode[T])

func (h Harness[M, T]) Step(n int) T = h.Mode match {
    case A(fn) => fn(n)
    case B(fn) => fn("")
}

func (h Harness[M, T]) State() M = h.Model.State`,
			want: []string{"Model std.Immutable[Model[M]]", "Mode  std.Immutable[Mode[T]]", "func(obj Mode[T]) T {", "h.Model.Get().State.Get()"},
		},
		{
			name: "sealed-variant field named like a generic type",
			pkg:  "main",
			decl: `sealed type W[T any] {
    case Wrap(Mode Mode[T])
    case Bare()
}

func name[T any](w W[T]) string = w match {
    case Wrap(m) => m match {
        case A(_) => "a"
        case B(_) => "b"
    }
    case Bare() => "bare"
}`,
			want: []string{"Mode     std.Immutable[Mode[T]]", "Apply(Mode Mode[T]) W[T]", "Unapply(v W[T]) std.Option[Mode[T]]", "func(obj W[T]) string {", "func(obj Mode[T]) string {"},
		},
		{
			name: "non-generic struct with a field named like a generic type",
			pkg:  "main",
			decl: `struct Plain(Mode Mode[int])

func (p Plain) Kind() string = p.Mode match {
    case A(_) => "a"
    case B(_) => "b"
}`,
			want: []string{"Mode std.Immutable[Mode[int]]", "func(obj Mode[int]) string {"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package " + tc.pkg + "\n" + types + "\n" + tc.decl + "\n"
			if tc.pkg == "main" {
				src += "\nfunc main() {}\n"
			}
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			assert.NotContains(t, got, "][", "a field's generic type must not be re-applied to its type arguments")
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
		})
	}
}
