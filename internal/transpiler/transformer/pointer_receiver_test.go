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

// TestPointerReceiverMethodExtraction tests that methods with additional type parameters
// on pointer receivers are correctly extracted to standalone functions with all type params.
func TestPointerReceiverMethodExtraction(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name           string
		input          string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name: "Pointer receiver method with additional type param - extraction includes T",
			input: `package main

type Array[T any] struct {
	var elements []T
}

func (a *Array[T]) Map[U any](f func(T) U) *Array[U] {
	return nil
}
`,
			// The extracted function should have both T and U type parameters
			mustContain: []string{
				"func Array_Map[U any, T any]",
			},
			mustNotContain: []string{
				// Should NOT have just U without T
				"func Array_Map[U any](",
			},
		},
		{
			name: "Value receiver method with additional type param - extraction includes T",
			input: `package main

type Array[T any] struct {
	elements []T
}

func (a Array[T]) Map[U any](f func(T) U) Array[U] {
	var result Array[U]
	return result
}
`,
			mustContain: []string{
				"func Array_Map[U any, T any]",
			},
		},
		{
			name: "Instantiation cycle detection for pointer receivers",
			input: `package main

type Array[T any] struct {
	var elements []T
}

func (a *Array[T]) Grouped(n int) *Array[*Array[T]] {
	return nil
}
`,
			// Grouped should be extracted to a function because it causes instantiation cycle
			mustContain: []string{
				"func Array_Grouped[T any]",
			},
		},
		{
			name: "Instantiation cycle detection for ZipWithIndex",
			input: `package main

import "martianoff/gala/std"

type Array[T any] struct {
	var elements []T
}

func (a *Array[T]) ZipWithIndex() *Array[std.Tuple[T, int]] {
	return nil
}
`,
			// ZipWithIndex should be extracted because return type differs from receiver type args
			mustContain: []string{
				"func Array_ZipWithIndex[T any]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.Transpile(tt.input, "test.gala")
			assert.NoError(t, err)

			for _, s := range tt.mustContain {
				assert.True(t, strings.Contains(result, s),
					"Expected output to contain %q\nGot:\n%s", s, result)
			}

			for _, s := range tt.mustNotContain {
				assert.False(t, strings.Contains(result, s),
					"Expected output to NOT contain %q\nGot:\n%s", s, result)
			}
		})
	}
}

// TestMethodCallToFunctionConversion tests that method calls are correctly
// converted to function calls with proper type arguments.
func TestMethodCallToFunctionConversion(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name           string
		input          string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name: "Method call with explicit type arg includes receiver type arg",
			input: `package main

type Array[T any] struct {
	var elements []T
}

func EmptyArray[T any]() *Array[T] {
	return nil
}

func (a *Array[T]) Map[U any](f func(T) U) *Array[U] {
	return nil
}

func double(x int) int { return x * 2 }

func test() {
	var arr = EmptyArray[int]()
	var mapped = arr.Map[int](double)
	_ = mapped
}
`,
			// The call should include both type params: Array_Map[int, int](arr, double)
			mustContain: []string{
				"Array_Map[int, int]",
			},
			mustNotContain: []string{
				// Should NOT have just the explicit type arg
				"Array_Map[int](",
			},
		},
		{
			name: "Method call without explicit type arg for cycle-extracted method",
			input: `package main

type Array[T any] struct {
	var elements []T
}

func EmptyArray[T any]() *Array[T] {
	return nil
}

func (a *Array[T]) Grouped(n int) *Array[*Array[T]] {
	return nil
}

func test() {
	var arr = EmptyArray[int]()
	var grouped = arr.Grouped(2)
	_ = grouped
}
`,
			// The call should be converted to function with receiver type: Array_Grouped[int](arr, 2)
			mustContain: []string{
				"Array_Grouped[int](arr, 2)",
			},
			mustNotContain: []string{
				// Should NOT remain as method call
				"arr.Grouped(2)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trans.Transpile(tt.input, "test.gala")
			assert.NoError(t, err)

			for _, s := range tt.mustContain {
				assert.True(t, strings.Contains(result, s),
					"Expected output to contain %q\nGot:\n%s", s, result)
			}

			for _, s := range tt.mustNotContain {
				assert.False(t, strings.Contains(result, s),
					"Expected output to NOT contain %q\nGot:\n%s", s, result)
			}
		})
	}
}
