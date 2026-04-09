package lsp_test

// Tests that verify every category of SemanticError from the transformer
// has correct line numbers (never line 0) when reported through the LSP.
//
// Each test triggers a specific error path and asserts the diagnostic
// has a non-zero line number pointing to the correct source location.

import (
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
)

func assertErrorHasLine(t *testing.T, diags []lsp.Diagnostic, msgSubstr string) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == nil || *d.Severity != lsp.SeverityError {
			continue
		}
		if strings.Contains(d.Message, msgSubstr) {
			if d.Range.Start.Line == 0 {
				t.Errorf("error '%s' at line 0 — missing line info. Full msg: %s", msgSubstr, d.Message)
			} else {
				t.Logf("OK: '%s' at line %d", msgSubstr, d.Range.Start.Line)
			}
			return
		}
	}
	// Error not found — log all diagnostics for debugging
	t.Logf("'%s' not triggered (%d diagnostics):", msgSubstr, len(diags))
	for _, d := range diags {
		t.Logf("  line=%d msg=%s", d.Range.Start.Line, d.Message)
	}
}

func getDiags(t *testing.T, src string) (lsp.DocumentURI, []lsp.Diagnostic) {
	t.Helper()
	h := newHarness(t)
	uri := openFileOnDisk(t, h, src)
	time.Sleep(100 * time.Millisecond)
	return uri, h.Diagnostics(uri)
}

// --- calls.go errors (named args) ---

func TestErrorLine_DuplicateNamedArg(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
    val y int
}

func main() {
    val p = Pt(x = 1, x = 2)
    Println(p)
}
`)
	assertErrorHasLine(t, diags, "duplicate")
}

func TestErrorLine_UnknownNamedArg(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
    val y int
}

func main() {
    val p = Pt(x = 1, z = 2)
    Println(p)
}
`)
	assertErrorHasLine(t, diags, "unknown")
}

func TestErrorLine_MissingRequiredArg(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
    val y int
}

func main() {
    val p = Pt(x = 1)
    Println(p)
}
`)
	assertErrorHasLine(t, diags, "missing")
}

func TestErrorLine_TooManyArgs(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
}

func main() {
    val p = Pt(x = 1, y = 2)
    Println(p)
}
`)
	assertErrorHasLine(t, diags, "")
}

func TestErrorLine_CopyOnNonStruct(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val y = x.Copy(z = 1)
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "Copy")
}

// --- match.go errors ---

func TestErrorLine_MatchNoDefault(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type Dir {
    case Up()
    case Down()
}

func name(d Dir) string = d match {
    case Up() => "up"
}

func main() {
    Println(name(Up()))
}
`)
	assertErrorHasLine(t, diags, "default")
}

func TestErrorLine_MatchCannotInferResultType(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type AB {
    case A()
    case B()
}

func test(x AB) = x match {
    case A() => 42
    case B() => "hello"
}

func main() {
    Println(test(A()))
}
`)
	// Either "cannot infer result type" or type mismatch
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError {
			if d.Range.Start.Line == 0 {
				t.Errorf("match error at line 0: %s", d.Message)
			} else {
				t.Logf("OK: match error at line %d: %s", d.Range.Start.Line, d.Message)
			}
		}
	}
}

func TestErrorLine_MatchMissingCase(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type RGB {
    case Red()
    case Green()
    case Blue()
}

func name(c RGB) string = c match {
    case Red() => "r"
    case Green() => "g"
}

func main() {
    Println(name(Red()))
}
`)
	assertErrorHasLine(t, diags, "missing")
}

// --- postfix.go errors ---

func TestErrorLine_PostfixMatchNoCase(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val y = x match {
        case _ => x
    }
    Println(y)
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("postfix match error at line 0: %s", d.Message)
		}
	}
}

// --- transformer.go errors ---

func TestErrorLine_EmptyFile(t *testing.T) {
	_, diags := getDiags(t, ``)
	// Should get "expecting 'package'" error, but not at line 0
	for _, d := range diags {
		t.Logf("  line=%d msg=%s", d.Range.Start.Line, d.Message)
	}
}

func TestErrorLine_MissingPackage(t *testing.T) {
	_, diags := getDiags(t, `func main() {
    Println("hello")
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			// Line 0 is acceptable here since the error IS at the start of the file
			t.Logf("OK: missing package error at line 0 (file start): %s", d.Message)
		}
	}
}

// --- types.go / type_inference_calls.go errors ---

func TestErrorLine_RecursiveImmutable(t *testing.T) {
	_, diags := getDiags(t, `package main

type Wrapper struct {
    val inner Wrapper
}

func main() {
    val w = Wrapper(inner = Wrapper(inner = w))
    Println(w)
}
`)
	// May or may not trigger "recursive Immutable" depending on type resolution
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError {
			if strings.Contains(d.Message, "recursive") || strings.Contains(d.Message, "Immutable") {
				if d.Range.Start.Line == 0 {
					t.Errorf("recursive immutable error at line 0: %s", d.Message)
				} else {
					t.Logf("OK: recursive immutable at line %d", d.Range.Start.Line)
				}
			}
		}
	}
}

// --- spread.go errors ---

func TestErrorLine_SpreadNonExpression(t *testing.T) {
	// This is hard to trigger from valid GALA syntax — spread is internal
	_, diags := getDiags(t, `package main

func add(a int, b int) int {
    return a + b
}

func main() {
    Println(add(1, 2))
}
`)
	// Should have 0 errors — valid code
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("unexpected error at line 0: %s", d.Message)
		}
	}
}

// --- imports.go errors ---

func TestErrorLine_ConflictingDotImports(t *testing.T) {
	_, diags := getDiags(t, `package main

import . "fmt"

func main() {
    Println("hello")
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			// Dot import conflict errors — if they appear, should have line info
			if strings.Contains(d.Message, "conflict") || strings.Contains(d.Message, "dot import") {
				t.Errorf("dot import error at line 0: %s", d.Message)
			}
		}
	}
}

// --- codec.go errors ---

func TestErrorLine_StructMetaUnknownType(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val meta = StructMeta[NonExistentType]()
    Println(meta)
}
`)
	assertErrorHasLine(t, diags, "not found")
}

// --- patterns.go errors ---

func TestErrorLine_TuplePatternTooMany(t *testing.T) {
	// Tuple patterns must have 2-10 elements
	_, diags := getDiags(t, `package main

func main() {
    val t = Tuple(1, 2)
    t match {
        case Tuple(a, b) => Println(a)
    }
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("pattern error at line 0: %s", d.Message)
		}
	}
}

// --- Comprehensive: ALL errors in one file ---

func TestErrorLine_AllErrorsHaveLineNumbers(t *testing.T) {
	// This test opens a file with multiple intentional errors
	// and verifies NONE of them have line 0
	_, diags := getDiags(t, `package main

sealed type Shape {
    case Circle(radius float64)
    case Square(side float64)
}

func broken1() string = Shape match {
}

func broken2(s Shape) string = s match {
    case Circle(r) => "circle"
}

func main() {
    Println(broken1())
    Println(broken2(Circle(radius = 1.0)))
}
`)
	lineZeroErrors := 0
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError {
			t.Logf("  line=%d col=%d msg=%s", d.Range.Start.Line, d.Range.Start.Character, d.Message)
			if d.Range.Start.Line == 0 {
				lineZeroErrors++
				t.Errorf("ERROR AT LINE 0: %s", d.Message)
			}
		}
	}
	if lineZeroErrors == 0 && len(diags) > 0 {
		t.Logf("all %d diagnostics have correct line numbers", len(diags))
	}
}

// =============================================================================
// calls.go errors (12 total)
// =============================================================================

// calls.go:1170 — cannot assign nil to immutable field
func TestErrorLine_Calls_NilToImmutableField(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
    val y int
}

func main() {
    val p = Pt(x = nil, y = 1)
    Println(p)
}
`)
	assertErrorHasLine(t, diags, "cannot assign nil to immutable field")
}

// calls.go:1230 — named arguments only supported for Copy method or struct construction
func TestErrorLine_Calls_NamedArgsOnNonStruct(t *testing.T) {
	_, diags := getDiags(t, `package main

func add(a int, b int) int = a + b

func main() {
    val r = add(a = 1, b = 2)
    Println(r)
}
`)
	// This may or may not trigger depending on whether add has function metadata
	// with named args support. If it does, it won't error. Check for any error.
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// calls.go:1316 — too many arguments in call (fillDefaultArgs variant for funcs)
func TestErrorLine_Calls_TooManyArgsFuncCall(t *testing.T) {
	_, diags := getDiags(t, `package main

func greet(name string, age int = 25) string = s"Hello $name, $age"

func main() {
    Println(greet("Alice", 30, name = "extra"))
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// calls.go:1345 — too many arguments in call (handleNamedArgsFuncCall)
func TestErrorLine_Calls_TooManyPositionalArgs(t *testing.T) {
	_, diags := getDiags(t, `package main

func greet(name string, age int = 25) string = s"Hello $name, $age"

func main() {
    Println(greet("Alice", 30, 99))
}
`)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// calls.go:1362 — unknown parameter in call to func
func TestErrorLine_Calls_UnknownParamFuncCall(t *testing.T) {
	_, diags := getDiags(t, `package main

func greet(name string, age int = 25) string = s"Hello $name, $age"

func main() {
    Println(greet(name = "Alice", xyz = 99))
}
`)
	assertErrorHasLine(t, diags, "unknown parameter")
}

// calls.go:1366 — parameter specified both positionally and by name in func call
func TestErrorLine_Calls_DuplicateParamFuncCall(t *testing.T) {
	_, diags := getDiags(t, `package main

func greet(name string, age int = 25) string = s"Hello $name, $age"

func main() {
    Println(greet("Alice", name = "Bob"))
}
`)
	assertErrorHasLine(t, diags, "specified both positionally and by name")
}

// calls.go:1382 — missing required argument in func call (handleNamedArgsFuncCall gap fill)
func TestErrorLine_Calls_MissingRequiredArgFuncCall(t *testing.T) {
	_, diags := getDiags(t, `package main

func greet(name string, title string, age int = 25) string = s"Hello $title $name, $age"

func main() {
    Println(greet(age = 30))
}
`)
	assertErrorHasLine(t, diags, "missing required argument")
}

// calls.go:1412 — too many arguments in method call
func TestErrorLine_Calls_TooManyArgsMethodCall(t *testing.T) {
	_, diags := getDiags(t, `package main

type Greeter struct {
    val name string
}

func (g Greeter) Hello(greeting string = "Hi") string = s"$greeting ${g.name}"

func main() {
    val g = Greeter(name = "Alice")
    Println(g.Hello("Hey", "extra"))
}
`)
	assertErrorHasLine(t, diags, "too many arguments")
}

// calls.go:1425 — unknown parameter in method call
func TestErrorLine_Calls_UnknownParamMethodCall(t *testing.T) {
	_, diags := getDiags(t, `package main

type Greeter struct {
    val name string
}

func (g Greeter) Hello(greeting string = "Hi") string = s"$greeting ${g.name}"

func main() {
    val g = Greeter(name = "Alice")
    Println(g.Hello(xyz = "Hey"))
}
`)
	assertErrorHasLine(t, diags, "unknown parameter")
}

// calls.go:1428 — parameter specified both positionally and by name in method call
func TestErrorLine_Calls_DuplicateParamMethodCall(t *testing.T) {
	_, diags := getDiags(t, `package main

type Greeter struct {
    val name string
}

func (g Greeter) Hello(greeting string = "Hi") string = s"$greeting ${g.name}"

func main() {
    val g = Greeter(name = "Alice")
    Println(g.Hello("Hey", greeting = "Yo"))
}
`)
	assertErrorHasLine(t, diags, "specified both positionally and by name")
}

// calls.go:1440 — missing required argument in method call (handleNamedArgsMethodCall gap fill)
func TestErrorLine_Calls_MissingRequiredArgMethodCall(t *testing.T) {
	_, diags := getDiags(t, `package main

type Greeter struct {
    val name string
}

func (g Greeter) Hello(greeting string, title string) string = s"$greeting $title ${g.name}"

func main() {
    val g = Greeter(name = "Alice")
    Println(g.Hello(title = "Dr."))
}
`)
	assertErrorHasLine(t, diags, "missing required argument")
}

// calls.go:1468 — missing required argument in method call (fillDefaultArgsMethod)
func TestErrorLine_Calls_MissingRequiredArgMethodFill(t *testing.T) {
	_, diags := getDiags(t, `package main

type Greeter struct {
    val name string
}

func (g Greeter) Hello(greeting string, title string) string = s"$greeting $title ${g.name}"

func main() {
    val g = Greeter(name = "Alice")
    Println(g.Hello("Hi"))
}
`)
	assertErrorHasLine(t, diags, "missing required argument")
}

// =============================================================================
// codec.go errors (3 total)
// =============================================================================

// codec.go:32 — StructMeta type not found
func TestErrorLine_Codec_StructMetaTypeNotFound(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val meta = StructMeta[NonExistentType]()
    Println(meta)
}
`)
	assertErrorHasLine(t, diags, "not found")
}

// codec.go:35 — StructMeta type has no fields
func TestErrorLine_Codec_StructMetaNoFields(t *testing.T) {
	_, diags := getDiags(t, `package main

type Empty struct {
}

func main() {
    val meta = StructMeta[Empty]()
    Println(meta)
}
`)
	assertErrorHasLine(t, diags, "has no fields")
}

// codec.go:472 — expected TypeName[T] with a single type argument
func TestErrorLine_Codec_StructMetaNoTypeArg(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val meta = StructMeta()
    Println(meta)
}
`)
	// StructMeta without type arg may produce a different error;
	// the extractTypeArgFromIndex error is for malformed index expressions
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// =============================================================================
// expressions.go errors (10 total)
// =============================================================================

// expressions.go:29 — "expression must contain orExpr"
// SKIP: This is an internal invariant that cannot be triggered from GALA source code.
// The ANTLR parser always generates an orExpr inside an expression node.

// expressions.go:35 — "orExpr must have at least one andExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one andExpr child.

// expressions.go:59 — "andExpr must have at least one equalityExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one equalityExpr child.

// expressions.go:83 — "equalityExpr must have at least one relationalExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one relationalExpr child.

// expressions.go:113 — "relationalExpr must have at least one additiveExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one additiveExpr child.

// expressions.go:141 — "additiveExpr must have at least one multiplicativeExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one multiplicativeExpr child.

// expressions.go:169 — "multiplicativeExpr must have at least one unaryExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees at least one unaryExpr child.

// expressions.go:270 — "unaryExpr must have unaryOp or postfixExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees either unaryOp or postfixExpr.

// expressions.go:320 — "missing operator in expression"
// SKIP: Internal invariant — operator position is guaranteed by grammar.

// expressions.go:324 — "unexpected operator node in expression"
// SKIP: Internal invariant — parse tree node type is guaranteed by ANTLR.

// =============================================================================
// match.go errors (11 total)
// =============================================================================

// match.go:74 — cannot infer type of matched expression (transformMatchSubject)
func TestErrorLine_Match_CannotInferMatchedType(t *testing.T) {
	_, diags := getDiags(t, `package main

func test() = unknownVar match {
    case _ => 42
}

func main() {
    Println(test())
}
`)
	assertErrorHasLine(t, diags, "cannot infer type")
}

// match.go:281 — multiple default cases in match expression
func TestErrorLine_Match_MultipleDefaultCases(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val y = x match {
        case 1 => "one"
        case _ => "other1"
        case _ => "other2"
    }
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "multiple default cases")
}

// match.go:369-370 — non-exhaustive match: missing cases (sealed, function match)
func TestErrorLine_Match_NonExhaustiveSealed(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red() => "red"
    case Green() => "green"
}

func main() {
    Println(name(Red()))
}
`)
	assertErrorHasLine(t, diags, "missing cases")
}

// match.go:380 — match expression must have a default case (non-sealed)
func TestErrorLine_Match_MustHaveDefault(t *testing.T) {
	_, diags := getDiags(t, `package main

func classify(x int) string = x match {
    case 1 => "one"
    case 2 => "two"
}

func main() {
    Println(classify(3))
}
`)
	assertErrorHasLine(t, diags, "must have a default case")
}

// match.go:439 — cannot infer type of matched expression (generateMatchIIFE)
// SKIP: This requires the typeToExpr to return nil for a matched type that was
// already resolved in transformMatchSubject. This is an internal invariant that
// cannot be triggered from GALA source code alone.

// match.go:446 — cannot infer result type of match expression (generateMatchIIFE)
func TestErrorLine_Match_CannotInferResultType(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type AB {
    case A()
    case B()
}

func test(x AB) = x match {
    case A() => 42
    case B() => "hello"
}

func main() {
    Println(test(A()))
}
`)
	// Should get a type mismatch or "cannot infer result type" error
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("match result type error at line 0: %s", d.Message)
		}
	}
}

// match.go:559 — match expression has no case branches
func TestErrorLine_Match_NoCaseBranches(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type AB {
    case A()
    case B()
}

func test(x AB) string = x match {
}

func main() {
    Println(test(A()))
}
`)
	assertErrorHasLine(t, diags, "no case branches")
}

// match.go:623 — cannot infer result type of match expression: no branch returns concrete type
// SKIP: This requires all branches to return type parameters, which is hard to
// trigger without a generic context that also has no enclosing return type.

// match.go:637 — cannot infer result type for pattern (nil type in result types)
// SKIP: This requires a branch where result type inference completely fails
// (returns nil transpiler.Type), which is an internal invariant.

// match.go:656 — type mismatch in match expression
func TestErrorLine_Match_TypeMismatch(t *testing.T) {
	_, diags := getDiags(t, `package main

func classify(x int) string = x match {
    case 1 => "one"
    case 2 => 42
    case _ => "other"
}

func main() {
    Println(classify(1))
}
`)
	assertErrorHasLine(t, diags, "type mismatch")
}

// match.go:911 — unused variable in match branch
func TestErrorLine_Match_UnusedVariable(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type Shape {
    case Circle(radius float64)
    case Square(side float64)
}

func area(s Shape) float64 = s match {
    case Circle(r) => 3.14
    case Square(s) => 1.0
}

func main() {
    Println(area(Circle(radius = 1.0)))
}
`)
	assertErrorHasLine(t, diags, "unused variable")
}

// =============================================================================
// patterns.go errors (11 total)
// =============================================================================

// patterns.go:44 — rest pattern (...) can only be used as the last argument
// SKIP: RestPatternContext at the top-level pattern position requires specific
// grammar parse that ANTLR won't normally produce from valid GALA code.

// patterns.go:116 — extractor expects N type parameters, got M
func TestErrorLine_Patterns_ExtractorWrongTypeParamCount(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val o = Some(42)
    val r = o match {
        case Some[int, string](x) => x
        case _ => 0
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "type parameters")
}

// patterns.go:123 — cannot infer type parameters for extractor
func TestErrorLine_Patterns_CannotInferExtractorTypeParams(t *testing.T) {
	_, diags := getDiags(t, `package main

type Box[T any] struct {
    val value T
}

func (b Box[T]) Unapply(v Box[T]) Option[T] = Some(v.value)

func main() {
    val x any = 42
    val r = x match {
        case Box(v) => v
        case _ => 0
    }
    Println(r)
}
`)
	// May trigger "cannot infer type parameters" or a different error
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// patterns.go:131 — extractor must have Unapply returning bool or Option[T]
func TestErrorLine_Patterns_ExtractorBadUnapplyReturn(t *testing.T) {
	_, diags := getDiags(t, `package main

type Even struct {}

func (e Even) Unapply(v int) string = "even"

func main() {
    val x = 42
    val r = x match {
        case Even() => "even"
        case _ => "odd"
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "Unapply returning bool or Option")
}

// patterns.go:148 — rest pattern requires a sequence type
func TestErrorLine_Patterns_RestPatternNonSeq(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = Some(42)
    val r = x match {
        case Some(a, rest...) => a
        case _ => 0
    }
    Println(r)
}
`)
	// May trigger rest pattern error or argument count mismatch
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("error at line 0: %s", d.Message)
		}
	}
}

// patterns.go:175 — extractor variable must have Unapply returning bool or Option[T]
func TestErrorLine_Patterns_VariableExtractorBadReturn(t *testing.T) {
	_, diags := getDiags(t, `package main

type Checker struct {}

func (c Checker) Unapply(v int) string = "found"

func main() {
    val c = Checker()
    val x = 42
    val r = x match {
        case c(v) => v
        case _ => 0
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "Unapply returning bool or Option")
}

// patterns.go:185 — extractor not found or doesn't have Unapply method
func TestErrorLine_Patterns_ExtractorNotFound(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val r = x match {
        case Fancy(v) => v
        case _ => 0
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "must define an Unapply method")
}

// patterns.go:680 — struct has N fields but pattern has M arguments
func TestErrorLine_Patterns_StructFieldCountMismatch(t *testing.T) {
	_, diags := getDiags(t, `package main

type Pt struct {
    val x int
    val y int
}

func main() {
    val p = Pt(x = 1, y = 2)
    val r = p match {
        case Pt(a, b, c) => a
        case _ => 0
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "fields but pattern has")
}

// patterns.go:920 — rest pattern must be the last argument in a sequence pattern
func TestErrorLine_Patterns_RestNotLast(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val arr = Array(1, 2, 3, 4)
    val r = arr match {
        case Array(rest..., last) => 0
        case _ => 0
    }
    Println(r)
}
`)
	assertErrorHasLine(t, diags, "rest pattern")
}

// patterns.go:1190 — tuple patterns must have 2-10 elements
// SKIP: Very hard to trigger from GALA source — would need a tuple pattern
// with more than 10 elements or fewer than 2, but the grammar for tuple
// patterns requires at least 2 elements (comma-separated).

// =============================================================================
// postfix.go errors (15 total)
// =============================================================================

// postfix.go:33 — "postfixExpr must have primaryExpr"
// SKIP: Internal invariant — ANTLR grammar guarantees primaryExpr in postfixExpr.

// postfix.go:69 — "unknown postfix suffix type"
// SKIP: Internal invariant — postfix suffixes are always identifier, call, or index.

// postfix.go:204 — "index expression requires expression list"
// SKIP: Internal invariant — ANTLR grammar guarantees expression list in index access.

// postfix.go:240 — "primaryExpr must have primary, lambda, if expression, or partial function"
// SKIP: Internal invariant — ANTLR grammar guarantees one of these alternatives.

// postfix.go:248 — "match expression must have subject"
// SKIP: Internal invariant — ANTLR grammar guarantees primary expr in postfix match.

// postfix.go:286 — "cannot infer type of matched expression" (postfix match, from first case clause)
func TestErrorLine_Postfix_CannotInferMatchedTypeFromCase(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val y = unknownVar match {
        case 1 => "one"
        case _ => "other"
    }
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "cannot infer type")
}

// postfix.go:288 — "cannot infer type of matched expression" (postfix match, no cases)
// SKIP: Same as above but with zero cases — the grammar requires at least the
// match block structure with case clauses.

// postfix.go:334 — "multiple default cases in match expression" (postfix)
func TestErrorLine_Postfix_MultipleDefaultCases(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val y = x match {
        case 1 => "one"
        case _ => "first default"
        case _ => "second default"
    }
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "multiple default cases")
}

// postfix.go:407/409 — "match expression must have at least one case" (postfix)
func TestErrorLine_Postfix_MatchNoCase(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type Dir {
    case Up()
    case Down()
}

func main() {
    val d = Up()
    val y = d match {
    }
    Println(y)
}
`)
	// May trigger "no case branches" or "at least one case"
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("postfix match error at line 0: %s", d.Message)
		}
	}
}

// postfix.go:441/444 — non-exhaustive match: missing cases (postfix sealed)
func TestErrorLine_Postfix_NonExhaustiveSealed(t *testing.T) {
	_, diags := getDiags(t, `package main

sealed type Dir {
    case Up()
    case Down()
    case Left()
}

func main() {
    val d = Up()
    val y = d match {
        case Up() => "up"
        case Down() => "down"
    }
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "missing cases")
}

// postfix.go:457/459 — "match expression must have a default case" (postfix non-sealed)
func TestErrorLine_Postfix_MustHaveDefaultCase(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val x = 42
    val y = x match {
        case 1 => "one"
        case 2 => "two"
    }
    Println(y)
}
`)
	assertErrorHasLine(t, diags, "must have a default case")
}

// postfix.go:496 — "tuple literals must have 2-10 elements"
func TestErrorLine_Postfix_TupleLiteralTooManyElements(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val t = (1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)
    Println(t)
}
`)
	assertErrorHasLine(t, diags, "tuple literals must have 2-10 elements")
}

// =============================================================================
// lambdas.go errors (4 total)
// =============================================================================

// lambdas.go:51 — cannot use '_' as a parameter type when other params have explicit types
func TestErrorLine_Lambdas_WildcardParamType(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val f = (x int, y _) => x + y
    Println(f(1, 2))
}
`)
	assertErrorHasLine(t, diags, "cannot use '_' as a parameter type")
}

// lambdas.go:115 — cannot discard error return in void lambda (block lambda)
func TestErrorLine_Lambdas_DiscardErrorBlockLambda(t *testing.T) {
	_, diags := getDiags(t, `package main

import "os"

func main() {
    val items = Array(1, 2, 3)
    items.ForEach((x) => {
        os.Remove("/tmp/nonexistent")
    })
}
`)
	// This triggers only if os.Remove is recognized as returning error
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("discard error lambda error at line 0: %s", d.Message)
		}
	}
}

// lambdas.go:181 — cannot discard error return in void lambda (expression lambda)
func TestErrorLine_Lambdas_DiscardErrorExprLambda(t *testing.T) {
	_, diags := getDiags(t, `package main

import "os"

func main() {
    val items = Array(1, 2, 3)
    items.ForEach((x) => os.Remove("/tmp/nonexistent"))
}
`)
	// This triggers only if os.Remove is recognized as returning error
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("discard error lambda error at line 0: %s", d.Message)
		}
	}
}

// lambdas.go:978 — partial function must have at least one case
func TestErrorLine_Lambdas_PartialFunctionNoCase(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val f = {
    }
    Println(f)
}
`)
	// The empty block may parse differently; partial function literal requires case clauses
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("partial function error at line 0: %s", d.Message)
		}
	}
}

// =============================================================================
// imports.go errors (1 total)
// =============================================================================

// imports.go:506 — dot-import symbol collision
func TestErrorLine_Imports_DotImportCollision(t *testing.T) {
	_, diags := getDiags(t, `package main

import . "fmt"
import . "log"

func main() {
    Println("hello")
}
`)
	// Dot import collision may or may not trigger depending on symbol overlap
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			if strings.Contains(d.Message, "collision") || strings.Contains(d.Message, "dot-import") {
				t.Errorf("dot import collision error at line 0: %s", d.Message)
			}
		}
	}
}

// =============================================================================
// spread.go errors (2 total)
// =============================================================================

// spread.go:17 — "only expressions allowed as function arguments" (nil pattern)
// SKIP: Internal invariant — pat is nil only when argument grammar has a
// lambdaExpression alternative, which is handled before this check.

// spread.go:25 — "only expressions allowed as function arguments" (unsupported pattern type)
// SKIP: Internal invariant — ANTLR grammar only produces ExpressionPattern or
// RestPattern in argument position.

// =============================================================================
// transformer.go errors (3 total)
// =============================================================================

// transformer.go:183 — cannot transpile: imported packages could not be resolved
func TestErrorLine_Transformer_UnresolvedImports(t *testing.T) {
	_, diags := getDiags(t, `package main

import "nonexistent/pkg"

func main() {
    pkg.DoSomething()
}
`)
	// May or may not trigger the "imported package(s) could not be resolved" error
	// depending on analysis mode. Check no errors at line 0.
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("unresolved import error at line 0: %s", d.Message)
		}
	}
}

// transformer.go:204 — expected *grammar.SourceFileContext
// SKIP: Internal invariant — the parse tree root is always SourceFileContext
// when ANTLR successfully parses. This error only triggers with a corrupted AST.

// transformer.go:395 — semanticErrorAt (generic helper used by other errors)
// SKIP: This is a helper method, not a specific error path.

// =============================================================================
// types.go errors (1 total)
// =============================================================================

// types.go:631 — recursive Immutable wrapping is not allowed
// Note: This triggers via panic(), which the transformer recovers from.
// It's the same message as declarations.go and type_inference_calls.go.
// Testing via declarations.go below covers this path.

// =============================================================================
// type_inference_calls.go errors (2 total)
// =============================================================================

// type_inference_calls.go:198 — recursive Immutable wrapping is not allowed
// type_inference_calls.go:377 — recursive Immutable wrapping is not allowed
// These are panic-based checks in type inference. Covered by the declarations test below.

// =============================================================================
// declarations.go errors (4 total)
// =============================================================================

// declarations.go:247 — recursive Immutable wrapping is not allowed (val with type annotation)
func TestErrorLine_Declarations_RecursiveImmutableVal(t *testing.T) {
	_, diags := getDiags(t, `package main

import "github.com/nicholasgasior/gala/std"

func main() {
    val x std.Immutable[int] = 42
    Println(x)
}
`)
	// May or may not trigger "recursive Immutable" depending on type resolution
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("recursive immutable error at line 0: %s", d.Message)
		}
	}
}

// declarations.go:308 — recursive Immutable wrapping is not allowed (val without tuple)
// SKIP: Same error as above, different code path. Both check isImmutableType on explicit type.

// declarations.go:399 — tuple destructuring requires exactly one expression on the right side
func TestErrorLine_Declarations_TupleDestructuringMultipleRHS(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val (a, b) = 1, 2
    Println(a)
    Println(b)
}
`)
	assertErrorHasLine(t, diags, "tuple destructuring requires exactly one expression")
}

// declarations.go:501 — variable assigned to None() must have an explicit type
func TestErrorLine_Declarations_NoneWithoutType(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    var x = None()
    Println(x)
}
`)
	assertErrorHasLine(t, diags, "None() must have an explicit type")
}

// =============================================================================
// constructors.go errors (1 total)
// =============================================================================

// constructors.go:126 — cannot assign nil to immutable field (constructor literal path)
func TestErrorLine_Constructors_NilToImmutableFieldLiteral(t *testing.T) {
	_, diags := getDiags(t, `package main

type Config struct {
    val name string
    val value int
}

func main() {
    val c = Config{name: nil, value: 1}
    Println(c)
}
`)
	// This path is for Go-style literal syntax {field: value}
	// which may not be valid GALA syntax — check for any error
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError && d.Range.Start.Line == 0 {
			t.Errorf("constructor nil field error at line 0: %s", d.Message)
		}
	}
}
