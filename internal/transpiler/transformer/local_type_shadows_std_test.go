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
// imported implicitly, and an import never outranks the package's own
// declarations. Every reference to the name — field, parameter, result, val
// annotation, receiver, constructor call, pattern, type argument — names the
// package's type, never `std.<Name>`. std itself stays reachable through its
// other names, and generated code that needs std's own type (a tuple
// literal's `std.Tuple`, a field's `std.Immutable`) still gets it.
//
// The rows cover each declaration kind (alias, generic alias, struct
// shorthand, `type X struct`, sealed type, interface) against std types of
// each kind (interface, sealed type, struct), in library packages and in
// package main.
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
			name: "alias of a collection type",
			pkg:  "lib",
			src: `import . "martianoff/gala/collection_immutable"

type Seq Array[int]

struct Holder(Items Seq)

func Make() Holder = Holder(ArrayOf(1, 2))

func Count(s Seq) int = s.Size()

func Items(h Holder) Seq = h.Items`,
			want:    []string{"type Seq = Array[int]", "Items std.Immutable[Seq]", "func Count(s Seq) int", "func Items(h Holder) Seq"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "generic alias of a collection type",
			pkg:  "lib",
			src: `import . "martianoff/gala/collection_immutable"

type Seq[T any] Array[T]

struct Holder(Items Seq[int])

func Make() Holder = Holder(ArrayOf(1, 2))

func Count[T any](s Seq[T]) int = s.Size()`,
			want:    []string{"Items std.Immutable[Seq[int]]", "func Count[T any](s Seq[T]) int"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "struct shorthand: params, result, val annotation, constructor, pattern",
			pkg:  "lib",
			src: `struct Option(Value int)

func Wrap(n int) Option = Option(n)

func Unwrap(o Option) int = o.Value

func Both() Option {
    val o Option = Option(1)
    o
}

func Peek(o Option) int = o match {
    case Option(v) => v
    case _ => 0
}

func Real(n int) string = Some(n).Map((v) => s"$v").GetOrElse("")`,
			want:    []string{"func Wrap(n int) Option", "func Unwrap(o Option) int", "var o std.Immutable[Option]","func Peek(o Option) int", "std.Some"},
			notWant: []string{"std.Option[Option]", "std.Option{", "o std.Option", ") std.Option\n"},
		},
		{
			name: "std's type stays reachable through a named import of std",
			pkg:  "lib",
			src: `import "martianoff/gala/std"

struct Option(Value int)

func Mine(n int) Option = Option(n)

func Real(n int) std.Option[int] = Some(n)`,
			want:    []string{"func Mine(n int) Option", "func Real(n int) std.Option[int]"},
			notWant: []string{"std.Option{"},
		},
		{
			name: "sealed variant named like a std companion",
			pkg:  "lib",
			src: `sealed type Maybe {
    case Some(V int)
    case Nothing()
}

func get(m Maybe) int = m match {
    case Some(v) => v
    case Nothing() => 0
}

func Build() Maybe = Some(1)

func Read() int = get(Build())`,
			want:    []string{"func get(m Maybe) int", "Some{}.Unapply(obj)", "func Build() Maybe {\n\treturn Some{}.Apply(1)"},
			notWant: []string{"std.Some[int]{}.Unapply", "std.Some[int]{}.Apply(1)"},
		},
		{
			name: "go-style struct with a method",
			pkg:  "lib",
			src: `type Try struct {
    N int
}

func (t Try) Twice() int = t.N * 2

func Build(n int) Try = Try{N: n}

func Run(t Try) int = t.Twice()`,
			want:    []string{"func (t Try) Twice() int", "func Build(n int) Try", "Try{N: std.NewImmutable(n)}","func Run(t Try) int"},
			notWant: []string{"std.Try"},
		},
		{
			name: "sealed type with variants named like std companions",
			pkg:  "lib",
			src: `sealed type Either {
    case Left(Msg string)
    case Right(N int)
}

func describe(e Either) string = e match {
    case Left(m) => m
    case Right(n) => s"$n"
}

func Pick(ok bool) Either = if (ok) Right(1) else Left("no")

func Show(ok bool) string = describe(Pick(ok))`,
			want:    []string{"func describe(e Either) string", "func Pick(ok bool) Either"},
			notWant: []string{"std.Either", "std.Left", "std.Right"},
		},
		{
			name: "interface named like a std interface",
			pkg:  "lib",
			src: `type Hashable interface {
    Key() string
}

struct User(Name string)

func (u User) Key() string = u.Name

func KeyOf(h Hashable) string = h.Key()

func UserKey() string = KeyOf(User("ada"))`,
			want:    []string{"type Hashable interface", "func KeyOf(h Hashable) string"},
			notWant: []string{"std.Hashable"},
		},
		{
			name: "struct named like a std type used as a type argument",
			pkg:  "lib",
			src: `struct Tuple(A int, B int)

func Wrap(t Tuple) Option[Tuple] = Some(t)

func Sum(o Option[Tuple]) int = o match {
    case Some(Tuple(a, b)) => a + b
    case _ => 0
}

func Pair() int = (1, 2) match {
    case (a, b) => a + b
}`,
			want:    []string{"func Wrap(t Tuple) std.Option[Tuple]", "func Sum(o std.Option[Tuple]) int", "std.Tuple[int, int]"},
			notWant: []string{"std.Option[std.Tuple]", "(t std.Tuple"},
		},
		{
			name: "generic struct named like a std interface in main",
			pkg:  "main",
			src: `struct Seq[T any](Head T, Size int)

func first[T any](s Seq[T]) T = s.Head

func build() Seq[int] = Seq(1, 2)`,
			want:    []string{"func first[T any](s Seq[T]) T", "func build() Seq[int]"},
			notWant: []string{"std.Seq"},
		},
		{
			name: "empty struct named like a std struct in main",
			pkg:  "main",
			src: `struct Void()

func none() Void = Void()`,
			want:    []string{"func none() Void"},
			notWant: []string{"std.Void"},
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
