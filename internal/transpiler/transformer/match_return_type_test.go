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

func TestMatchReturnTypePrimitiveNotQualified(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name             string
		input            string
		shouldNotContain string
		shouldContain    string
	}{
		{
			name: "Match returning uint32 should not be package-qualified",
			input: `package testpkg

func getHash(v any) uint32 {
	val result = v match {
		case i: int => uint32(i)
		case _ => uint32(0)
	}
	return result
}`,
			shouldNotContain: "testpkg.uint32",
			shouldContain:    "func(obj any) uint32",
		},
		{
			name: "Match returning int should not be package-qualified",
			input: `package testpkg

func getValue(v any) int {
	val result = v match {
		case i: int => i
		case _ => 0
	}
	return result
}`,
			shouldNotContain: "testpkg.int",
			shouldContain:    "func(obj any) int",
		},
		{
			name: "Match returning string should not be package-qualified",
			input: `package testpkg

func getString(v any) string {
	val result = v match {
		case s: string => s
		case _ => "default"
	}
	return result
}`,
			shouldNotContain: "testpkg.string",
			shouldContain:    "func(obj any) string",
		},
		{
			name: "Match returning bool should not be package-qualified",
			input: `package testpkg

func getBool(v any) bool {
	val result = v match {
		case b: bool => b
		case _ => false
	}
	return result
}`,
			shouldNotContain: "testpkg.bool",
			shouldContain:    "func(obj any) bool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)

			// Should NOT contain package-qualified primitive type
			assert.False(t, strings.Contains(got, tt.shouldNotContain),
				"Generated code should not contain %q, but got:\n%s", tt.shouldNotContain, got)

			// Should contain properly typed function signature
			assert.True(t, strings.Contains(got, tt.shouldContain),
				"Generated code should contain %q, but got:\n%s", tt.shouldContain, got)
		})
	}
}

// TestMatchReturnTypeInGenericFunction tests that match expressions in generic functions
// don't incorrectly qualify primitive return types with the package name.
func TestMatchReturnTypeInGenericFunction(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name             string
		input            string
		shouldNotContain string
	}{
		{
			name: "Generic function with match returning uint32",
			input: `package testpkg

func hash[T comparable](value T) uint32 {
	val v any = value
	val result = v match {
		case i: int => uint32(i)
		case s: string => uint32(len(s))
		case _ => uint32(0)
	}
	return result
}`,
			shouldNotContain: "testpkg.uint32",
		},
		{
			name: "Generic function with match returning int",
			input: `package testpkg

func process[T any](value T) int {
	val v any = value
	val result = v match {
		case i: int => i
		case s: string => len(s)
		case _ => 0
	}
	return result
}`,
			shouldNotContain: "testpkg.int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)

			// Should NOT contain package-qualified primitive type
			assert.False(t, strings.Contains(got, tt.shouldNotContain),
				"Generated code should not contain %q, but got:\n%s", tt.shouldNotContain, got)
		})
	}
}
