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

// A type the package declares shadows a std type of the same name: std is
// imported implicitly, and an implicit import never outranks the package's
// own declarations. Every reference to the name — field, parameter, result,
// val annotation, constructor call, pattern, type argument — must name the
// package's type, never `std.<Name>`.
func TestLocalTypeShadowsStdType(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	tests := []struct {
		name    string
		pkg     string
		src     string
		want    []string
		notWant []string
	}{
		{
			name: "alias of a collection type in a library",
			pkg:  "lib",
			src: `import . "martianoff/gala/collection_immutable"

type Seq Array[int]

struct Holder(Items Seq)

func Make() Holder = Holder(ArrayOf(1, 2))`,
			want:    []string{"type Seq = Array[int]", "Items std.Immutable[Seq]"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "generic alias in a library",
			pkg:  "lib",
			src: `import . "martianoff/gala/collection_immutable"

type Seq[T any] Array[T]

struct Holder(Items Seq[int])

func Make() Holder = Holder(ArrayOf(1, 2))`,
			want:    []string{"Items std.Immutable[Seq[int]]"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "struct named like a std type in a library",
			pkg:  "lib",
			src: `struct Option(Value int)

func Wrap(n int) Option = Option(n)

func Unwrap(o Option) int = o.Value

func Both() Option {
    val o Option = Option(1)
    o
}`,
			want:    []string{"func Wrap(n int) Option", "func Unwrap(o Option) int", "var o Option"},
			notWant: []string{"std.Option"},
		},
		{
			name: "generic struct named like a std type in main",
			pkg:  "main",
			src: `struct Seq[T any](Head T, Size int)

func first[T any](s Seq[T]) T = s.Head

func make() Seq[int] = Seq(1, 2)`,
			want:    []string{"func first[T any](s Seq[T]) T", "func make() Seq[int]"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "sealed type named like a std type in a library",
			pkg:  "lib",
			src: `sealed type Either {
    case Left(Msg string)
    case Right(N int)
}

func describe(e Either) string = e match {
    case Left(m) => m
    case Right(n) => s"$n"
}

func Pick(ok bool) Either = if (ok) Right(1) else Left("no")`,
			want:    []string{"func describe(e Either) string", "func Pick(ok bool) Either"},
			notWant: []string{"std.Either", "std.Left", "std.Right"},
		},
		{
			name: "struct named like a std type used as a type argument",
			pkg:  "lib",
			src: `struct Tuple(A int, B int)

func Wrap(t Tuple) Option[Tuple] = Some(t)`,
			want:    []string{"func Wrap(t Tuple) std.Option[Tuple]"},
			notWant: []string{"std.Tuple"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package " + tc.pkg + "\n\n" + tc.src + "\n"
			if tc.pkg == "main" {
				src += "\nfunc main() {}\n"
			}
			got, err := trans.Transpile(src, "")
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
			for _, w := range tc.notWant {
				assert.NotContains(t, got, w)
			}
		})
	}
}
