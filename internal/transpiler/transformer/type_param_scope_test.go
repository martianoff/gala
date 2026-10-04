package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeParamsRecognisedByScope guards how the transformer tells a type
// parameter from a type. It used to decide by spelling: any single capital
// letter was a type parameter wherever it appeared. A user type named `T`, `A`
// or `V`, and a Go type such as `testing.B`, was therefore treated as an
// unresolved parameter — the lambdas passed to its collection methods lost
// their parameter types (`func(t any) bool`), and a match yielding it was typed
// `any`. Conversely a multi-letter parameter such as `Elem` was never
// recognised at all.
//
// A name is a type parameter when an enclosing generic declaration binds it
// (and then it shadows a same-named package type), or when it is a callee's
// placeholder that no visible type answers to.
func TestTypeParamsRecognisedByScope(t *testing.T) {
	cases := []typeParamCase{
		{
			name: "struct named T as a collection element",
			input: typeParamHeader + `type T struct { N int }

func pick(xs Array[T]) Option[T] = xs.Find((t) => t.N > 1)`,
			mustContain: []string{"func(t T) bool"},
			mustNotHave: []string{"func(t any)"},
		},
		{
			name: "struct named A through Option",
			input: typeParamHeader + `type A struct { Name string }

func names(o Option[A]) Option[string] = o.Filter((a) => a.Name != "").Map((a) => a.Name)`,
			mustContain: []string{"func(a A) bool", "func(a A) string"},
			mustNotHave: []string{"func(a any)"},
		},
		{
			name: "sealed type named V",
			input: typeParamHeader + `sealed type V {
    case Small(n int)
    case Big(n int)
}

func size(v V) int = v match {
    case Small(n) => n
    case Big(n) => n * 10
}

func firstBig(vs Array[V]) Option[V] = vs.Find((v) => size(v) > 5)`,
			mustContain: []string{"func(v V) bool"},
			mustNotHave: []string{"func(v any)"},
		},
		{
			name: "match yielding a Go type named B",
			input: `package main

import (
    "testing"
    . "martianoff/gala/collection_immutable"
)

func pick(bs Array[*testing.B]) int {
    val r = bs.HeadOption() match {
        case Some(b) => b
        case None() => bs.Last()
    }
    return r.N
}`,
			mustContain: []string{"func(obj std.Option[*testing.B]) *testing.B"},
			mustNotHave: []string{") any {"},
		},
		{
			name: "type parameter A shadows a package struct A",
			input: typeParamHeader + `type A struct { Name string }

func firstOr[A any](xs Array[A], d A) A {
    val r = xs.Find((x) => true) match {
        case Some(a) => a
        case None() => d
    }
    return r
}`,
			mustContain: []string{"func(x A) bool", "func(obj std.Option[A]) A"},
			mustNotHave: []string{"func(x any)", ") any {"},
		},
		{
			name: "multi-letter type parameter",
			input: typeParamHeader + `func countIf[Elem any](xs Array[Elem], p func(Elem) bool) int = xs.Filter((x) => p(x)).Size()`,
			mustContain: []string{"func(x Elem) bool"},
			mustNotHave: []string{"func(x any)"},
		},
	}

	runTypeParamCases(t, cases)
}

// TestTypeParamShadowsStdName guards that a type parameter named like a std
// type or variant (`Some`, `Option`, `Try`, `Tuple`, `Left`) or like a type of
// an import (`Array`) means the type parameter everywhere in its declaration.
// It used to be emitted as the std type — `func id[Some any](x Some) Some`
// became `func id[Some any](x std.Some) std.Some` — which Go rejects with
// "cannot use generic type std.Some[T any] without instantiation".
func TestTypeParamShadowsStdName(t *testing.T) {
	cases := []typeParamCase{
		{
			name:        "function type parameter named like a std variant",
			input:       typeParamHeader + `func id[Some any](x Some) Some = x`,
			mustContain: []string{"func id[Some any](x Some) Some {"},
			mustNotHave: []string{"std.Some"},
		},
		{
			name:        "type parameter used as a std type argument",
			input:       typeParamHeader + `func first[Some any](xs Array[Some]) Option[Some] = xs.HeadOption()`,
			mustContain: []string{"func first[Some any](xs Array[Some]) std.Option[Some] {"},
			mustNotHave: []string{"std.Option[std.Some]"},
		},
		{
			// Name normalization is memoized across declarations; an answer
			// for Option or Try outside a generic declaration must not be
			// reused inside one that binds the name.
			name: "declarations after others that resolve the same names",
			input: typeParamHeader + `type HasLen interface {
    Size() int
}

struct Bag(N int)

func (b Bag) Size() int = b.N

func m1[Try any](xs Array[Try]) Array[Try] = xs.Map((o) => o)

func m2[Option any](xs Array[Option]) Array[Option] = xs.Map((o) => o)

func m3[Try HasLen](xs Array[Try]) Array[Try] = xs.Map((o) => o)`,
			mustContain: []string{"func(o Try) Try", "func(o Option) Option"},
			mustNotHave: []string{"std.Option", "std.Try"},
		},
		{
			// As in Go, the type parameter shadows a value name too: here
			// Wrap(3) is a conversion to the type parameter, not the struct.
			name: "type parameter shadows a package type's constructor",
			input: typeParamHeader + `struct Wrap(N int)

func mk[Wrap any](x Wrap) Wrap = Wrap(x)`,
			mustContain: []string{"func mk[Wrap any](x Wrap) Wrap {\n\treturn Wrap(x)\n}"},
		},
		{
			// The type parameter's scope is inside the package's, so it
			// shadows a package-level val as well.
			name: "type parameter shadows a package-level val",
			input: typeParamHeader + `val Left = 1

func conv[Left any](a Left) Left = Left(a)`,
			mustContain: []string{"func conv[Left any](a Left) Left {\n\treturn Left(a)\n}"},
		},
		{
			name:        "type parameters named like std types",
			input:       typeParamHeader + `func pair[Option any, Try any](a Option, b Try) Tuple[Option, Try] = (a, b)`,
			mustContain: []string{"func pair[Option any, Try any](a Option, b Try) std.Tuple[Option, Try] {"},
			mustNotHave: []string{"std.Option", "std.Try"},
		},
		{
			name:        "type parameter named like an imported type",
			input:       typeParamHeader + `func firstOr[Array any](xs List[Array], d Array) Array = xs.HeadOption().GetOrElse(d)`,
			mustContain: []string{"func firstOr[Array any](xs List[Array], d Array) Array {"},
		},
		{
			name: "struct and receiver type parameters",
			input: typeParamHeader + `struct Box[Option any](V Option)

func (b Box[Option]) Get() Option = b.V

func (b Box[Option]) Map[Try any](f func(Option) Try) Box[Try] = Box(f(b.V))`,
			mustContain: []string{
				"type Box[Option any] struct",
				"func (b Box[Option]) Get() Option {",
				"func Box_Map[Try any, Option any](b Box[Option], f func(Option) Try) Box[Try] {",
			},
			mustNotHave: []string{"std.Option", "std.Try"},
		},
		{
			name: "sealed type parameter",
			input: typeParamHeader + `sealed type Res[Some any] {
    case Ok(V Some)
    case Err(Msg string)
}`,
			// The generated Unapply still builds a std Some — of the type
			// parameter.
			mustContain: []string{
				"type Res[Some any] struct",
				"V        std.Immutable[Some]",
				"func (_ Ok[Some]) Apply(V Some) Res[Some] {",
				"std.Some[Some]{}.Apply(v.V.Get())",
			},
			mustNotHave: []string{"[std.Some]", "V std.Some"},
		},
		{
			name: "explicit type arguments in an expression",
			input: typeParamHeader + `func wrapRight[Left any](a Left) Option[Left] = Some[Left](a)

type Holder[Tuple any] struct {
    Value Tuple
}

func (h Holder[Tuple]) Pair() Array[Tuple] = ArrayOf[Tuple](h.Value, h.Value)`,
			mustContain: []string{"std.Some[Left]", "ArrayOf[Tuple]"},
			mustNotHave: []string{"std.Left", "std.Tuple"},
		},
	}
	runTypeParamCases(t, cases)
}

// TestTypeParamMisuse guards that using a type parameter where only a name it
// shadows could stand — as an extractor, or with type arguments — is a GALA
// error naming the cause, not Go that misuses the type parameter.
func TestTypeParamMisuse(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{
			name: "extractor pattern",
			input: `func fold[Left any, Right any](e Either[Left, Right]) string = e match {
    case Left(l) => "l"
    case _ => "r"
}`,
			want: "'Left' is a type parameter of the enclosing declaration and cannot be matched as a pattern",
		},
		{
			name: "struct pattern",
			input: `struct Wrap(N int)

func h[Wrap any](w Wrap, x any) int = x match {
    case Wrap(n) => n
    case _ => 0
}`,
			want: "'Wrap' is a type parameter of the enclosing declaration and cannot be matched as a pattern",
		},
		{
			name:  "type arguments in a type",
			input: `func g[Option any](x Option, o Option[int]) int = 0`,
			want:  "'Option' is a type parameter of the enclosing declaration and takes no type arguments",
		},
		{
			name:  "type arguments in an expression",
			input: `func g[Left any](x Left) Either[int, string] = Left[int, string](1)`,
			want:  "'Left' is a type parameter of the enclosing declaration and takes no type arguments",
		},
	}
	trans := newForbiddenBuiltinTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(typeParamHeader+tc.input, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestDefaultLoweredInItsOwnScope guards that a parameter's default, lowered
// at a call site inside a generic declaration, reads its names in its own
// declaration's scope: the caller's type parameters do not shadow them.
func TestDefaultLoweredInItsOwnScope(t *testing.T) {
	runTypeParamCases(t, []typeParamCase{{
		name: "default naming a std constructor",
		input: typeParamHeader + `func f(o Option[int] = None[int]()) int = 0

func g[None any](y None) int = f()`,
		mustContain: []string{"return f(std.None[int]{}.Apply())"},
	}})

	// A package-level val the use site's type parameter shadows has no
	// spelling in the generated Go there, so it is a GALA error.
	_, err := newForbiddenBuiltinTranspiler().Transpile(typeParamHeader+`val Dflt = 3

func f(n int = Dflt) int = n

func g[Dflt any](x Dflt) int = f()`, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reads 'Dflt', which the type parameter 'Dflt' of the enclosing declaration shadows here")
}

// typeParamHeader opens each typeParamCase input.
const typeParamHeader = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n"

// typeParamCase is a GALA source and the strings its generated Go must, and
// must not, contain.
type typeParamCase struct {
	name        string
	input       string
	mustContain []string
	mustNotHave []string
}

func runTypeParamCases(t *testing.T, cases []typeParamCase) {
	t.Helper()
	trans := newForbiddenBuiltinTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			for _, want := range tc.mustContain {
				require.Contains(t, out, want, "generated Go:\n%s", out)
			}
			for _, bad := range tc.mustNotHave {
				require.NotContains(t, out, bad, "generated Go:\n%s", out)
			}
		})
	}
}
