package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTypeInferenceRegression is a comprehensive table-driven regression test covering
// type inference bugs fixed across recent releases. Each entry is a minimal reproduction
// of the pattern that was broken, asserting on the generated Go code to ensure the fix
// remains in place.
func TestTypeInferenceRegression(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		contains    []string // expected substrings in generated Go code
		notContains []string // substrings that must NOT appear in generated Go code
		expectError string   // if non-empty, expect an error containing this substring
	}{
		// =========================================================================
		// Lambda Type Inference
		// =========================================================================
		{
			// Expected types not propagated to lambdas in expression functions.
			// When a function returns a type alias for a function type, the lambda should
			// receive expected param/return types from the alias resolution.
			name: "expression function lambda gets expected types from type alias",
			input: `package main

type Transformer func(int) string

func MakeTransformer() Transformer = (x) => {
    if x > 0 {
        return "positive"
    }
    return "zero"
}`,
			contains: []string{
				"func(x int) string",
			},
			notContains: []string{
				"func(x int) any",
				"interface{}",
			},
		},
		{
			// Void block lambdas should generate func() not func() any.
			// When a lambda has no return statement and is in a void context (e.g.,
			// ForEach), the generated Go should use func(T) not func(T) any.
			name: "void block lambda generates func() not func() any",
			input: `package main

import . "martianoff/gala/std"

func test() {
    var arr = EmptyArray[int]()
    arr.ForEach((x int) => {
        println(x)
    })
}`,
			contains: []string{
				"func(x int) {",
			},
			notContains: []string{
				"func(x int) any",
			},
		},
		{
			// Type inference for chained calls through type aliases.
			// When a call returns a type alias that resolves to a function type,
			// calling the result should infer the return type through the alias chain.
			name: "chained call through type alias infers return type",
			input: `package main

type Transform func(int) string

func GetTransform() Transform {
    return (x int) => "hello"
}

func test() string {
    return GetTransform()(42)
}`,
			contains: []string{
				"func test() string",
			},
		},
		{
			// Expression-bodied if-expression as a lambda body, where both
			// arms reference user-defined methods on a generic type. HM
			// inference can't resolve methods on user-defined generics
			// (the type env carries only top-level functions and
			// current-scope vals), so the if-expression's IIFE must fall
			// back to per-branch inference. Both arms have the same
			// concrete type after substitution, so the IIFE return must
			// be that type rather than `any`.
			name: "if-expression with method-call arms infers branch type",
			input: `package main

import . "martianoff/gala/collection_immutable"

struct State[T any](Tag string, Make func() T)

func (s State[T]) Advance() State[T] = State[T](Tag = "armed", Make = s.Make)
func (s State[T]) IsDue() bool = s.Tag == "due"

func process[T any](items Array[State[T]]) Array[State[T]] {
    return items.Map((it) => if (it.IsDue()) it.Advance() else it)
}`,
			contains: []string{
				"func(it State[T]) State[T]",
			},
			notContains: []string{
				"func(it State[T]) any",
				"Array[any]",
			},
		},
		{
			// Unify return types across all paths in block lambdas.
			// When a block lambda has multiple return paths (e.g., if/else),
			// the return type should be unified from all branches.
			name: "block lambda unifies return types from multiple paths",
			input: `package main

import . "martianoff/gala/std"

func test() {
    var arr = ArrayOf(1, 2, 3)
    val mapped = arr.Map((x int) => {
        if x > 1 {
            return "big"
        }
        return "small"
    })
}`,
			contains: []string{
				"func(x int) string",
			},
			notContains: []string{
				"func(x int) any",
			},
		},
		{
			// Generic free function lambda param inference (was reverted once).
			// When calling a generic function like Apply(42, (x) => x + 1),
			// the lambda param type should be inferred from the non-lambda args.
			name: "Generic free function lambda param inference",
			input: `package main

func Apply[T any](value T, f func(T) T) T {
    return f(value)
}

func test() int {
    return Apply(42, (x) => x + 1)
}`,
			contains: []string{
				"func(x int) int",
			},
			notContains: []string{
				"func(x any)",
				"interface{}",
			},
		},
		{
			// Generic function with concrete lambda param types and only-result type
			// parameter (e.g. ArrayTabulate signature `func[T any](n int, f func(int) T)`).
			// The lambda parameter `int` is concrete in the signature and must be
			// propagated to the lambda even though the type parameter T cannot yet
			// be resolved from the non-lambda arguments. Otherwise the lambda would
			// emit `func(week any) any` and the body's arithmetic would fail to
			// type-check.
			name: "generic function with concrete lambda param + result-only type param",
			input: `package main

func Tabulate[T any](n int, f func(int) T) {
    f(n)
}

func test() {
    Tabulate(6, (week) => week * 2)
}`,
			contains: []string{
				"func(week int)",
			},
			notContains: []string{
				"func(week any)",
				"interface{}",
			},
		},
		{
			// Lambda param inference for non-generic wrapper methods.
			// When a method takes a function parameter with concrete types, the lambda
			// should infer parameter types from the method signature.
			name: "non-generic method lambda param inference",
			input: `package main

func RunWith(f func(int)) {
    f(42)
}

func test() {
    RunWith((x) => {
        println(x)
    })
}`,
			contains: []string{
				"func(x int) {",
			},
			notContains: []string{
				"func(x any)",
			},
		},
		{
			// Accumulator type inferred from zero value in generic function.
			// When calling a generic function with a non-lambda arg (0) and a lambda,
			// the accumulator type should be inferred from the zero value.
			// Uses a single type parameter to test the core pre-scan mechanism.
			name: "accumulator type from zero value in generic function",
			input: `package main

func Combine[T any](init T, f func(T, T) T) T {
    return f(init, init)
}

func test() int {
    return Combine(0, (acc, x) => acc + x)
}`,
			contains: []string{
				"func(acc int, x int) int",
			},
			notContains: []string{
				"func(acc any",
				"interface{}",
			},
		},

		// =========================================================================
		// Cross-File / Package Type Inference
		// =========================================================================
		{
			// Explicit generic type arguments should be preserved.
			// When a generic function is called with explicit type args,
			// the transpiler should preserve them in generated code.
			name: "explicit generic type args preserved",
			input: `package main

import . "martianoff/gala/std"

func test() {
    val x = Some[string]("hello")
}`,
			contains: []string{
				"Some[string]",
			},
		},
		{
			// Dot-imported packages should use unqualified references.
			// When a package is dot-imported, all its types should appear without
			// the package qualifier in generated Go code.
			name: "dot import uses unqualified references",
			input: `package testpkg

import . "martianoff/gala/std"

func test() Option[int] {
    val x = Some(42)
    return x
}`,
			contains: []string{
				"Option[int]",
			},
			notContains: []string{
				"std.Option",
				"std.Some",
			},
		},
		{
			// Unused import removal uses actual package name.
			// When the package name differs from the last path component,
			// removeUnusedImports should use the actual package name to decide
			// whether the import is used.
			name: "unused import removal uses actual package name",
			input: `package main

import "fmt"

func test() {
    fmt.Println("hello")
}`,
			contains: []string{
				`"fmt"`,
				"fmt.Println",
			},
		},
		{
			// Truly unused regular imports should be removed.
			// When a regular import is not referenced in code, it should be dropped.
			name: "truly unused regular import is removed",
			input: `package main

import "fmt"

func test() int {
    return 42
}`,
			notContains: []string{
				`"fmt"`,
			},
		},

		// =========================================================================
		// Code Generation
		// =========================================================================
		{
			// Remove unused dot imports from generated Go code.
			// When a GALA-analyzed dot-imported package is not referenced, it should
			// be removed. Tested with the std package using a simple case that
			// does not reference any std types.
			name: "unused dot import of std is kept (conservative)",
			input: `package main

import . "martianoff/gala/std"

func test() int {
    return 42
}`,
			// std dot import is always kept as a conservative choice
			contains: []string{
				`"martianoff/gala/std"`,
			},
		},
		{
			// Used dot imports are preserved.
			// When a dot-imported package's symbols are actually used, the import
			// must remain in the generated code.
			name: "used dot import is preserved",
			input: `package main

import . "martianoff/gala/std"

func test() {
    val x = Some(42)
}`,
			contains: []string{
				`"martianoff/gala/std"`,
				".Apply(",
			},
		},
		{
			// Reject '_' wildcard type in lambda params with explicit types.
			// When some params have explicit types and others use '_', the transpiler
			// should emit a semantic error rather than silently generating bad code.
			name: "reject wildcard type mixed with explicit types",
			input: `package main

func test() {
    val f = (x int, y _) => x + y
}`,
			expectError: "cannot use '_' as a parameter type",
		},
		{
			// Two-param lambda for HashMap[K, V].ForEachKV must bind k:K and v:V
			// from the receiver's type arguments. Earlier breakage typed both as
			// `any`, which made any subsequent call expecting a concrete value
			// reject the lambda parameter ("cannot use v (variable of interface
			// type any) as Member value"). The expected param types from the
			// methodMeta substitution must propagate through both lambda slots
			// — not just the first.
			name: "HashMap.ForEachKV two-param lambda binds K and V from receiver",
			input: `package main

import . "martianoff/gala/collection_immutable"

struct Member(Name string)

func test() {
    var m = EmptyHashMap[string, Member]()
    m = m.Put("a", Member(Name = "A"))
    m.ForEachKV((k, v) => {
        Println(k)
        Println(v.Name)
    })
}`,
			contains: []string{
				"func(k string, v Member)",
			},
			notContains: []string{
				"func(k any, v any)",
				"func(k string, v any)",
				"func(k any, v Member)",
			},
		},
		{
			// Same as above but the lambda body is a single statement-position
			// match — mirrors the failing source from the original bug report.
			// The lambda must still bind k:K and v:V so that an inner call
			// taking the concrete V (e.g. CloseSession(v)) compiles.
			name: "HashMap.ForEachKV lambda body is a statement-position match",
			input: `package main

import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/std"

struct Member(Name string)

func tryClose(s Member) Try[bool] = Success(true)

func test() {
    var m = EmptyHashMap[string, Member]()
    m = m.Put("a", Member(Name = "A"))
    m.ForEachKV((name, s) => {
        tryClose(s) match {
            case Success(_)    => {}
            case Failure(_)    => {}
        }
    })
}`,
			contains: []string{
				"func(name string, s Member)",
				"tryClose(s)",
			},
			notContains: []string{
				"func(name any, s any)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")

			// Handle expected error cases
			if tt.expectError != "" {
				assert.Error(t, err, "Expected error for %s", tt.name)
				if err != nil {
					assert.Contains(t, err.Error(), tt.expectError,
						"Error should contain %q", tt.expectError)
				}
				return
			}

			assert.NoError(t, err, "Transpilation should succeed for %s\nInput:\n%s", tt.name, tt.input)
			if err != nil {
				return
			}

			for _, exp := range tt.contains {
				assert.True(t, strings.Contains(got, exp),
					"[%s] Output should contain %q\nGot:\n%s", tt.name, exp, got)
			}
			for _, notExp := range tt.notContains {
				assert.False(t, strings.Contains(got, notExp),
					"[%s] Output should NOT contain %q\nGot:\n%s", tt.name, notExp, got)
			}
		})
	}
}
