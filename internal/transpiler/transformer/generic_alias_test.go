package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic type alias (`type Result[T any] Either[AppError, T]`) is a second
// name for every instance of the type it names. It lowers to a Go generic
// alias, and everywhere GALA reads the structure of a type — constructors
// typed from an expected type, match subjects, method calls, struct fields,
// collection elements, function types — it stands for its target with its
// arguments substituted.
//
// Every output also goes through the package's Go type-check oracle.

const genericAliasDecls = `package main

import (
    "errors"
    . "martianoff/gala/collection_immutable"
)

struct AppError(Code int)

type Result[T any] Either[AppError, T]
type Res[T any] Try[T]
type Res2[T any] Res[T]
type Strs Res[string]
type Outcome[E any, T any] Either[E, T]
type Pairs[K comparable, V any] Array[Tuple[K, V]]
type StrMap[V any] HashMap[string, V]
type Handler[T any] func(string) Result[T]

type Labeled interface {
    Label() string
}

type Labels[L Labeled] Array[L]

struct Inventory(Items Pairs[string, int], Last Result[int])

func code(r Result[int]) int = r match {
    case Right(v) => v
    case Left(e) => -e.Code
}
`

func TestGenericAlias(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "declarations lower to Go generic aliases",
			input: "func f() int = 0",
			want: []string{
				"type Result[T any] = std.Either[AppError, T]",
				"type Pairs[K comparable, V any] = Array[std.Tuple[K, V]]",
				"type Res2[T any] = Res[T]",
				"type Labels[L Labeled] = Array[L]",
			},
		},
		{
			name:  "Left and Right typed from a one-parameter alias with a fixed argument",
			input: "func f(b bool) Result[int] = if (b) Right(1) else Left(AppError(2))",
			want:  []string{"std.Right[AppError, int]{}", "std.Left[AppError, int]{}"},
		},
		{
			name:  "two-parameter alias",
			input: `func f(b bool) Outcome[string, int] = if (b) Right(1) else Left("x")`,
			want:  []string{"std.Right[string, int]{}", "std.Left[string, int]{}"},
		},
		{
			name:  "alias of a generic alias",
			input: `func f() Res2[int] = Failure(errors.New("bad"))`,
			want:  []string{"std.Failure[int]{}"},
		},
		{
			name:  "non-generic alias of an instantiated generic alias",
			input: `func f() Strs = Failure(errors.New("bad"))`,
			want:  []string{"std.Failure[string]{}"},
		},
		{
			name:  "partially applied alias",
			input: "func f() StrMap[int] = EmptyHashMap()",
			want:  []string{"EmptyHashMap[string, int]()"},
		},
		{
			name:  "alias whose target nests the parameter",
			input: "func f() Pairs[string, int] = EmptyArray()",
			want:  []string{"EmptyArray[std.Tuple[string, int]]()"},
		},
		{
			name:  "parameter and result types",
			input: "func f(r Result[int]) Result[int] = r",
			want:  []string{"func f(r Result[int]) Result[int]"},
		},
		{
			name:  "struct fields typed by generic aliases",
			input: "func f() Inventory = Inventory(EmptyArray(), Right(1))",
			want:  []string{"EmptyArray[std.Tuple[string, int]]()", "std.Right[AppError, int]{}"},
		},
		{
			name:  "match on an alias-typed subject",
			input: "func f(r Result[int]) int = code(r)",
			want:  []string{"func code(r Result[int]) int"},
		},
		{
			name:  "method of the target on an alias-typed val",
			input: "func f() int {\n    val r Res[int] = Success(2)\n    r.Map((v) => v + 1).GetOrElse(0)\n}",
			want:  []string{"std.Success[int]{}", "GetOrElse(0)"},
		},
		{
			name:  "collection element typed by a generic alias",
			input: "func one() Result[int] = Right(1)\nfunc f() Array[Result[int]] = ArrayOf(one(), Left(AppError(1)))",
			want:  []string{"func f() Array[Result[int]]", "std.Left[AppError, int]{}"},
		},
		{
			name:  "function-typed generic alias",
			input: "func f() Handler[int] = (s) => Right(s.Size())",
			want:  []string{"func(s string) Result[int]", "std.Right[AppError, int]{}"},
		},
		{
			name:  "interface-constrained alias in a generic function",
			input: "func f[L Labeled](ls Labels[L]) int = ls.Size()",
			want:  []string{"func f[L Labeled](ls Labels[L]) int"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(genericAliasDecls+tt.input+"\n", "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestGenericAliasWrongArity: a generic alias spelled with the wrong number of
// type arguments names no instance of its target, so a method call on a value
// of it cannot be judged. It used to be reported as a missing method
// (GALA-E0044, "Res declares no methods"); the type itself is what is wrong,
// and Go reports it. This output does not type-check by design, so it skips
// the oracle.
func TestGenericAliasWrongArity(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	for _, call := range []string{"r.IsSuccess()", "r.GetOrElse(0)"} {
		t.Run(call, func(t *testing.T) {
			_, err := trans.Transpile(`package main

type Res[T any] Try[T]

func main() {
    val r Res[int, string] = Success(1)
    Println(`+call+`)
}
`, "")
			if err != nil {
				assert.NotContains(t, err.Error(), string(galaerr.CodeUnknownMethod))
			}
		})
	}
}
