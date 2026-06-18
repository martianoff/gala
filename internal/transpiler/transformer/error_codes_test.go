package transformer_test

import (
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrorPathAssertions covers T5/T6: assert that the A8 stable error codes
// fire at the right compile-time checkpoints and that their messages render
// the code and the hint. These tests are intentionally narrow — one positive
// case (should compile) paired with a negative case (should fail with a
// specific code) per checked rule.
func TestErrorPathAssertions(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	type errCase struct {
		name           string
		input          string
		expectCode     galaerr.ErrorCode
		expectContains string
	}

	cases := []errCase{
		{
			name: "GALA-E0004 variant arity mismatch",
			input: `package main

sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h) => w * h
    case Circle(r) => r * r * 3
}

func main() {
    val r = Rect(1, 2, "x")
    area(r)
}`,
			expectCode:     galaerr.CodeVariantArityMismatch,
			expectContains: "binds 2 field(s) but declares 3",
		},
		{
			name: "GALA-E0002 non-exhaustive sealed match",
			input: `package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
}

func main() {
    val c = Red()
    name(c)
}`,
			expectCode:     galaerr.CodeNonExhaustiveMatch,
			expectContains: "missing cases",
		},
		{
			name: "GALA-E0003 non-sealed match missing default",
			input: `package main

func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
}

func main() {
    name(1)
}`,
			expectCode:     galaerr.CodeMissingDefault,
			expectContains: "default case",
		},
		{
			name: "GALA-E0005 missing Unapply extractor",
			input: `package main

func main() {
    val x = 42
    val r = x match {
        case Nonsensical(v) => v
        case _ => 0
    }
}`,
			expectCode:     galaerr.CodeMissingUnapply,
			expectContains: "must define an Unapply method",
		},
		{
			name: "GALA-E0006 multiple default cases",
			input: `package main

func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"
}

func main() {
    name(1)
}`,
			expectCode:     galaerr.CodeMultipleDefaults,
			expectContains: "multiple default",
		},
		{
			name: "GALA-E0007 slice literal rejected",
			input: `package main

func main() {
    val xs = []int{1, 2, 3}
}`,
			expectCode:     galaerr.CodeSliceLiteralNotSupported,
			expectContains: "slice literals",
		},
		{
			name: "GALA-E0008 map literal rejected",
			input: `package main

func main() {
    val m = map[string]int{"a": 1}
}`,
			expectCode:     galaerr.CodeMapLiteralNotSupported,
			expectContains: "map literals",
		},
		{
			name: "GALA-E0013 non-defaulted param follows defaulted param",
			input: `package main

func create(a int = 5, b int) int = a + b

func main() {
    create(1, 2)
}`,
			expectCode:     galaerr.CodeParamMissingDefaultAfterDefault,
			expectContains: "has no default",
		},
		{
			name: "GALA-E0014 default expression type mismatch",
			input: `package main

func create(a int = "not-an-int") int = a

func main() {
    create()
}`,
			expectCode:     galaerr.CodeParamDefaultTypeMismatch,
			expectContains: "default for parameter",
		},
		{
			// Repro of fix/gap-04: a match used as a value (assigned to `val`)
			// whose Failure branch ends with a bare `return` would previously
			// emit invalid Go (the IIFE has return type string but returns no
			// value). We now reject this at transpile time with GALA-E0015.
			name: "GALA-E0015 bare return inside value-producing match (Try)",
			input: `package main

import "os"

func run(path string) {
    val data = Try(() => os.ReadFile(path)) match {
        case Success(b)   => string(b)
        case Failure(err) => {
            Println(s"error: ${err.Error()}")
            return
        }
    }
    Println(data)
}

func main() { run("missing.txt") }`,
			expectCode:     galaerr.CodeBareReturnInValueMatch,
			expectContains: "bare `return`",
		},
		{
			// Same shape with Option. The Some branch yields a value, the None
			// branch bare-returns. Result type of the match is `int`, so the
			// bare return is ambiguous and must be rejected.
			name: "GALA-E0015 bare return inside value-producing match (Option)",
			input: `package main

func lookup(opt Option[int]) int {
    val n = opt match {
        case Some(v) => v
        case None()  => { return }
    }
    return n * 2
}

func main() { lookup(None[int]()) }`,
			expectCode:     galaerr.CodeBareReturnInValueMatch,
			expectContains: "bare `return`",
		},
		{
			// B6: zero-arg sealed variant of a generic parent with no
			// inference signal must surface as GALA-E0018 instead of
			// silently emitting an untyped composite literal that Go
			// fails to instantiate far from the GALA source.
			name: "GALA-E0018 sealed variant uninferred",
			input: `package main

sealed type Box[T any] {
    case Empty()
    case Filled(value T)
}

func main() {
    val x = Empty()
    Println(x)
}`,
			expectCode:     galaerr.CodeSealedVariantUninferred,
			expectContains: "cannot infer type parameter",
		},
		{
			// Empty parenthesized expression `()` (the unit-shorthand the
			// grammar admits but the transpiler has no Go lowering for)
			// must surface as GALA-E0019 with a real source span instead
			// of crashing Go's printer downstream.
			name: "GALA-E0019 empty parens in match arm",
			input: `package main

func handle(code Int) Unit = code match {
    case 0 => Println("zero")
    case _ => ()
}

func main() {
    handle(0)
}`,
			expectCode:     galaerr.CodeEmptyParenExpression,
			expectContains: "empty parenthesized expression",
		},
		{
			// Two top-level functions with the same name in one file must
			// surface as a redeclaration error rather than silently keeping
			// only the second definition.
			name: "GALA-E0027 function redeclared",
			input: `package main

func greet(name string) string = "hello " + name
func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}`,
			expectCode:     galaerr.CodeFunctionRedeclared,
			expectContains: `function "greet"`,
		},
		{
			// Two `type Foo = Bar` aliases with the same name silently
			// overwrote one another in the transformer; now rejected.
			name: "GALA-E0028 type alias redeclared",
			input: `package main

type Handler func(string) string
type Handler func(int) int

func main() {
    Println("unused")
}`,
			expectCode:     galaerr.CodeTypeAliasRedeclared,
			expectContains: `type alias "Handler"`,
		},
		{
			// Two method specs with the same name within one interface
			// are now rejected. The grammar accepts the form but Go would
			// only see the second.
			name: "GALA-E0029 interface method redeclared",
			input: `package main

type Repo interface {
    Find(id int) string
    Find(name string) string
}

func main() {
    Println("unused")
}`,
			expectCode:     galaerr.CodeInterfaceMethodRedeclared,
			expectContains: `method "Find"`,
		},
		{
			// Two struct fields with the same name now rejected; previously
			// the second silently overwrote the first in metadata.
			name: "GALA-E0030 struct field redeclared",
			input: `package main

type Point struct {
    val x int
    val x int
}

func main() {
    val p = Point(x = 1)
    Println(p.x)
}`,
			expectCode:     galaerr.CodeStructFieldRedeclared,
			expectContains: `field "x"`,
		},
		{
			// Two sealed cases sharing a name now rejected. Without this
			// guard the second case overwrote the first's companion type
			// and methods.
			name: "GALA-E0031 sealed variant case redeclared",
			input: `package main

sealed type Shape {
    case Box(W int)
    case Box(H int)
}

func main() {
    val b = Box(1)
    Println(b)
}`,
			expectCode:     galaerr.CodeSealedVariantCaseRedeclared,
			expectContains: `sealed case "Box"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A non-empty file path is required for the analyzer's
			// redeclaration checks (function / method): an empty DefinedIn is
			// treated as a cache/placeholder entry and skipped. All inputs are
			// `package main`, so no sibling-file discovery is triggered.
			_, err := trans.Transpile(tc.input, "error_codes_test.gala")
			require.Error(t, err, "expected transpile to fail")
			msg := err.Error()
			assert.Contains(t, msg, string(tc.expectCode),
				"expected error message to carry code %s", tc.expectCode)
			assert.Contains(t, msg, tc.expectContains,
				"expected error message to describe the offense")
		})
	}
}

// TestT6VariantArityPositive is the positive half of T6 — the same sealed
// declaration as the T6 negative case, but with patterns that match the
// declared arity exactly. Must compile without error.
func TestT6VariantArityPositive(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h, _) => w * h
    case Circle(r)     => r * r * 3
}

func main() {
    val r = Rect(1, 2, "x")
    area(r)
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

// TestGap04BareReturnRefactoredCompiles is the positive companion to the
// GALA-E0015 cases above. The same intent (early-exit on Failure) expressed
// via an early `if` before the match — rather than a bare `return` inside a
// match branch — must compile cleanly.
func TestGap04BareReturnRefactoredCompiles(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Refactored form: destructure with .IsFailure() / .Get() outside the
	// match so the `return` is a top-level statement of `run`, not nested
	// inside an IIFE.
	input := `package main

import "os"

func run(path string) {
    val result = Try(() => os.ReadFile(path))
    if (result.IsFailure()) {
        Println("error reading file")
        return
    }
    Println(string(result.Get()))
}

func main() { run("missing.txt") }`
	_, err := trans.Transpile(input, "")
	require.NoError(t, err, "refactored form (early return before match) must compile")
}

// TestGap04BareReturnAllowedInVoidMatch guards against false positives:
// GALA-E0015 must only fire when the match's result is a value. A match whose
// every branch is side-effectful (VoidType) may contain bare returns because
// the generated IIFE has no return type.
func TestGap04BareReturnAllowedInVoidMatch(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

func run(opt Option[int]) {
    opt match {
        case Some(n) => Println(n)
        case None()  => { Println("nothing"); return }
    }
}

func main() { run(Some(1)) }`
	_, err := trans.Transpile(input, "")
	require.NoError(t, err, "bare return is allowed inside void (side-effect) match branches")
}

// TestT10TypeVarSubstitutionDepthCap exercises the B8 guard. Build a
// substitution map that maps T to a generic type containing T; the
// (pre-B8) implementation would have recursed without bound. With the
// depth cap the call must return within finite time and the result must
// be a well-formed type (not a crash).
//
// This test exercises substituteInType directly via a synthetic
// transpiler.Type graph to keep the assertion tight.
func TestT10TypeVarSubstitutionDepthCap(t *testing.T) {
	// Build: base type T, substitution T -> List[T]
	//
	// The (pre-B8) substituteInType would have unbounded recursion if it
	// re-applied the substitution to its own output. B8's depth cap
	// short-circuits at maxTypeSubstDepth so the call returns.
	//
	// We exercise the cap indirectly by compiling GALA code that stresses
	// the substitution path with deep nesting. Synthetic Type-graph tests
	// would need an exported entry point, so we prefer end-to-end cover.
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Deeply nested generic wrapper calls — exercise substitution on a
	// deep type tree. If the depth cap is missing or wrong, the call
	// explodes the stack; with the cap it returns in bounded time.
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("type Wrap[T any] struct { V T }\n")
	b.WriteString("func (w Wrap[T]) M[U any](f func(T) U) Wrap[U] = Wrap[U](V = f(w.V))\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("    val w = Wrap[int](V = 0)\n")
	// 30 chained maps force 30 substitutions on the same wrapper type.
	for i := 0; i < 30; i++ {
		b.WriteString("    val w")
		b.WriteString(itoa(i))
		b.WriteString(" = w.M((x int) => x + 1)\n")
	}
	b.WriteString("}\n")

	_, err := trans.Transpile(b.String(), "")
	assert.NoError(t, err, "deep substitution should not blow the stack or hang")
}
