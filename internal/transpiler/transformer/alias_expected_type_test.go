package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An expected type spelled as an alias (`type Checked Try[Email]`) binds a
// constructor's type arguments exactly as the type it names does. Without
// that, `func f() Checked = Failure(err)` left Failure's T unbound and emitted
// `std.Failure{}.Apply(...)` — or `std.Try[T]` for an if/match result — which
// does not compile, and None(), EmptyArray() and other return-only type
// parameters were reported as uninferable.
//
// Every output also goes through the package's Go type-check oracle.

const aliasExpectedDecls = `package main

import (
    "errors"
    . "martianoff/gala/collection_immutable"
)

struct Email(v string)
struct Box[T any](items Array[T])
func mkBox[T any]() Box[T] = Box[T](EmptyArray[T]())

type Checked Try[Email]
type Checked2 Checked
type MaybeInt Option[int]
type EI Either[string, int]
type Ints Array[int]
type IntList List[int]
type Counts HashMap[string, int]
type IntBox Box[int]
type Res[T any] Try[T]

func takeChecked(c Checked) Checked = c
func takeMaybe(m MaybeInt) MaybeInt = m
func takeInts(xs Ints) Ints = xs
func applyChecked(f func(int) Checked) Checked = f(1)
struct Holder(c Checked)
`

func newAliasExpectedTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	return newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}

// TestAliasExpectedTypeConstructorMatrix crosses each position an expected
// type reaches a constructor from with each constructor whose type arguments
// come only from that expected type.
func TestAliasExpectedTypeConstructorMatrix(t *testing.T) {
	trans := newAliasExpectedTranspiler()

	ctors := []struct {
		alias, expr, want string
	}{
		{"Checked", `Failure(errors.New("bad"))`, "std.Failure[Email]{}"},
		{"Checked2", `Failure(errors.New("bad"))`, "std.Failure[Email]{}"},
		{"Res[int]", `Failure(errors.New("bad"))`, "std.Failure[int]{}"},
		{"MaybeInt", `None()`, "std.None[int]{}"},
		{"EI", `Left("x")`, "std.Left[string, int]{}"},
		{"EI", `Right(1)`, "std.Right[string, int]{}"},
		{"Ints", `EmptyArray()`, "EmptyArray[int]()"},
		{"IntList", `EmptyList()`, "EmptyList[int]()"},
		{"Counts", `EmptyHashMap()`, "EmptyHashMap[string, int]()"},
		{"IntBox", `mkBox()`, "mkBox[int]()"},
	}
	positions := []struct {
		name string
		src  func(alias, expr string) string
	}{
		{"expression body", func(al, e string) string { return "func f() " + al + " = " + e }},
		{"block trailing value", func(al, e string) string { return "func f() " + al + " {\n    " + e + "\n}" }},
		{"explicit return", func(al, e string) string { return "func f() " + al + " {\n    return " + e + "\n}" }},
		{"val annotation", func(al, e string) string {
			return "func f() " + al + " {\n    val x " + al + " = " + e + "\n    x\n}"
		}},
		{"if-else branches", func(al, e string) string { return "func f(b bool) " + al + " = if (b) " + e + " else " + e }},
		{"match arms", func(al, e string) string {
			return "func f(n int) " + al + " = n match {\n    case 0 => " + e + "\n    case _ => " + e + "\n}"
		}},
	}
	for _, pos := range positions {
		for _, c := range ctors {
			t.Run(pos.name+"/"+c.alias+"/"+c.expr, func(t *testing.T) {
				got, err := trans.Transpile(aliasExpectedDecls+pos.src(c.alias, c.expr)+"\n", "")
				require.NoError(t, err)
				assert.Contains(t, got, c.want)
			})
		}
	}
}

// TestAliasExpectedTypeOtherPositions covers the remaining positions an
// alias-typed expected type reaches a constructor from, and the uses of an
// alias-typed value that read the structure of the type it names.
func TestAliasExpectedTypeOtherPositions(t *testing.T) {
	trans := newAliasExpectedTranspiler()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "argument to an alias-typed parameter",
			input: `func f() Checked = takeChecked(Failure(errors.New("bad")))`,
			want:  []string{"takeChecked(std.Failure[Email]{}"},
		},
		{
			name:  "zero-arg constructor as an argument",
			input: `func f() MaybeInt = takeMaybe(None())`,
			want:  []string{"takeMaybe(std.None[int]{}"},
		},
		{
			name:  "return-only type parameter as an argument",
			input: `func f() Ints = takeInts(EmptyArray())`,
			want:  []string{"takeInts(EmptyArray[int]())"},
		},
		{
			name:  "struct field of an alias type",
			input: `func f() Holder = Holder(Failure(errors.New("bad")))`,
			want:  []string{"std.Failure[Email]{}"},
		},
		{
			name:  "lambda expression result",
			input: `func f() Checked = applyChecked((n) => Failure(errors.New("bad")))`,
			want:  []string{"std.Failure[Email]{}"},
		},
		{
			name:  "lambda block result",
			input: "func f() Checked = applyChecked((n) => {\n    Failure(errors.New(\"bad\"))\n})",
			want:  []string{"std.Failure[Email]{}"},
		},
		{
			name:  "match subject of an alias type",
			input: "func f(m MaybeInt) MaybeInt = m match {\n    case Some(x) => Some(x + 1)\n    case _ => None()\n}",
			want:  []string{"std.None[int]{}"},
		},
		{
			name:  "tuple literal for a tuple alias takes its element types",
			input: "type Pair Tuple[int64, string]\nfunc f() Pair = (1, \"a\")",
			want:  []string{"std.Tuple[int64, string]{"},
		},
		{
			name:  "bind over an alias of a monad",
			input: "func g() Checked = Success(Email(\"a\"))\nfunc f() Checked {\n    bind e = g()\n    Success(e)\n}",
			want:  []string{"std.Try_FlatMap[Email, Email](g()"},
		},
		{
			name:  "generic alias in a val annotation, then a method of the type it names",
			input: "func f() int {\n    val r Res[int] = Failure(errors.New(\"bad\"))\n    r.GetOrElse(0)\n}",
			want:  []string{"std.Failure[int]{}", "r.Get().GetOrElse(0)"},
		},
		{
			name:  "method of the type an alias names, on an alias-typed value",
			input: "func f() string {\n    val c Checked = Failure(errors.New(\"bad\"))\n    c.GetOrElse(Email(\"z\")).v\n}",
			// The method's result type is known, so its val field unwraps.
			want: []string{"c.Get().GetOrElse(Email{v: std.NewImmutable(\"z\")}).v.Get()"},
		},
		{
			name:  "a match subject typed by an alias with its own methods keeps them",
			input: "struct Point(X int, Y int)\ntype Coord Point\nfunc (c Coord) Sum() int = c.X + c.Y\nfunc f(c Coord) int = c match {\n    case p => p.Sum()\n}",
			want:  []string{".Sum()"},
		},
		{
			name:  "a guarded binding of an alias-typed subject keeps the alias's methods",
			input: "struct Point(X int, Y int)\ntype Coord Point\nfunc (c Coord) Sum() int = c.X + c.Y\nfunc f(c Coord) int = c match {\n    case p if p.Sum() > 0 => p.Sum()\n    case _ => 0\n}",
			want:  []string{".Sum() > 0"},
		},
		{
			name:  "exhaustive match on an alias-typed subject",
			input: "func f(c Checked) string = c match {\n    case Success(e) => e.v\n    case Failure(err) => err.Error()\n}",
			want:  []string{"std.Try[Email]"},
		},
		{
			name:  "a method declared on the alias itself still wins",
			input: "struct Point(X int, Y int)\ntype Coord Point\nfunc (c Coord) Sum() int = c.X + c.Y\nfunc f() int {\n    val c Coord = Coord(1, 2)\n    c.Sum()\n}",
			want:  []string{"c.Get().Sum()"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(aliasExpectedDecls+tt.input+"\n", "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestAliasExpectedTypeCrossPackage: an alias imported from another package
// is recorded under its qualified name and binds type arguments the same way.
func TestAliasExpectedTypeCrossPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/aliasx\n\ngala dev\n")
	write("lib/lib.gala", `package lib

struct Email(V string)
type Checked Try[Email]
type Checked2 Checked
type MaybeInt Option[int]
type Res[T any] Try[T]
`)

	tests := []struct {
		name, body string
		want       []string
	}{
		{"result", `func f() lib.Checked = Failure(errors.New("bad"))`, []string{"std.Failure[lib.Email]{}"}},
		{"alias of an alias", `func f() lib.Checked2 = Failure(errors.New("bad"))`, []string{"std.Failure[lib.Email]{}"}},
		{"zero-arg constructor", `func f() lib.MaybeInt = None()`, []string{"std.None[int]{}"}},
		{"generic alias", `func f() lib.Res[int] = Failure(errors.New("bad"))`, []string{"std.Failure[int]{}"}},
		{
			"val annotation and a method of the type it names",
			"func f() lib.Email {\n    val c lib.Checked = Failure(errors.New(\"bad\"))\n    c.GetOrElse(lib.Email(\"z\"))\n}",
			[]string{"std.Failure[lib.Email]{}", "c.Get().GetOrElse("},
		},
		{
			"bind",
			"func g() lib.Checked = Success(lib.Email(\"a\"))\nfunc f() lib.Checked {\n    bind e = g()\n    Success(e)\n}",
			[]string{"std.Try_FlatMap[lib.Email, lib.Email](g()"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    \"errors\"\n    \"example.com/aliasx/lib\"\n)\n\n" + tt.body + "\n"
			got, err := transpileCrossPkg(t, root, src)
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestSealedVariantWithArgsUninferred: a sealed variant whose arguments do not
// carry the parent's type parameter, in a position nothing else names it in,
// is GALA-E0018 — as for a zero-arg variant — rather than an uninstantiated
// `Failure{}` and a `Try[T]` naming an unbound T in the generated Go. A sibling
// branch whose type is known still supplies it.
func TestSealedVariantWithArgsUninferred(t *testing.T) {
	trans := newAliasExpectedTranspiler()

	errCases := []struct{ name, input, want string }{
		{"val", "func f() {\n    val x = Failure(errors.New(\"bad\"))\n    Println(x)\n}", `"Failure(...)"`},
		{"if-expression", "func f(b bool) {\n    val x = if (b) Failure(errors.New(\"bad\")) else Failure(errors.New(\"bad\"))\n    Println(x)\n}", `"Failure(...)"`},
		{"match", "func f(n int) {\n    val x = n match {\n        case 0 => Left(\"x\")\n        case _ => Left(\"y\")\n    }\n    Println(x)\n}", `cannot infer type parameter B for sealed variant constructor "Left(...)"`},
		// The hint names every type argument of the parent, the known ones as known.
		{"hint arity", "func f() {\n    val x = Left(\"x\")\n    Println(x)\n}", "Either[string, int] = std.Left(...)"},
		{"hint spells a known std type bare", "func f() {\n    val x = Left(Some(1))\n    Println(x)\n}", "Either[Option[int], int] = std.Left(...)"},
	}
	for _, tt := range errCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(aliasExpectedDecls+tt.input+"\n", "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, galaerr.CodeSealedVariantUninferred, se.Code)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	// A cycle of aliases (malformed) is reported or lowered, never recursed
	// into forever.
	t.Run("alias cycle", func(t *testing.T) {
		_, err := trans.Transpile(aliasExpectedDecls+"type A B\ntype B A\nfunc f() A = Failure(errors.New(\"bad\"))\n", "")
		var se *galaerr.SemanticError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, galaerr.CodeSealedVariantUninferred, se.Code)
	})

	okCases := []struct{ name, input string }{
		{"if-expression with a typed sibling", "func f(b bool) {\n    val x = if (b) Success(1) else Failure(errors.New(\"bad\"))\n    Println(x)\n}"},
		{"match with a typed sibling", "func f(n int) {\n    val x = n match {\n        case 0 => Failure(errors.New(\"bad\"))\n        case _ => Success(1)\n    }\n    Println(x)\n}"},
		// The sibling, not the enclosing function's result type, types it.
		{"typed sibling inside a function with another result type", "func f(b bool) Try[string] {\n    val x = if (b) Success(1) else Failure(errors.New(\"bad\"))\n    x.Map((n) => s\"$n\")\n}"},
	}
	for _, tt := range okCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(aliasExpectedDecls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, "std.Failure[int]{}")
		})
	}
}

// TestGenericFuncAliasField covers a generic alias of a function type used as
// a struct field's type. The alias is emitted as a Go generic alias, and a
// lambda in the field's place is typed by the alias's signature with the
// constructor's type arguments substituted, whether they are written or
// inferred from the other fields — never the alias's own parameter names.
func TestGenericFuncAliasField(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	const decls = "package main\n\ntype Conv[A any, B any] func(A) B\n\n" +
		"struct Step[A any, B any](In A, Run Conv[A, B])\n\n"
	tests := []struct{ name, body, want string }{
		{"explicit type arguments", `func f() int = Step[int, string](In = 3, Run = (x) => s"${x}").In`, "func(x int) string {"},
		{"inferred type arguments", `func f() int = Step(In = 3, Run = (x) => x * 2).Run(1)`, "func(x int) int {"},
		{"conversion to an instantiated alias", `func f() Conv[int, string] = Conv[int, string]((x) => s"${x}")`, "Conv[int, string](func(x int) string {"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.body+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, "type Conv[A any, B any] = func(A) B")
			assert.Contains(t, got, tt.want)
		})
	}

	// A conversion to the generic alias with no type arguments has no
	// function type to give the lambda; it is never typed by the alias's own
	// parameter names.
	_, err := trans.Transpile(decls+"func f() int {\n    val c = Conv((x) => x)\n    1\n}\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
}
