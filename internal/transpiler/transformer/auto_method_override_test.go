package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutoMethodOverride verifies that when the user defines their own
// Copy/Equal/Unapply method on a struct (or sealed parent), the transpiler
// suppresses the auto-generated counterpart so the resulting Go code does
// not declare the method twice. This is the regression guard for the bug
// hit while building the crypto.Digest type, where the user-defined
// constant-time Equal collided with the auto-generated deep-equality Equal.
func TestAutoMethodOverride(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	type override struct {
		method string
		// snippet appended after the struct declaration. Must define a method
		// with the given name on type Digest.
		snippet string
	}

	tests := []struct {
		name    string
		decl    string // struct declaration syntax
		over    override
	}{
		{
			name: "shorthand struct, user Equal",
			decl: "struct Digest(bytes int)",
			over: override{
				method: "Equal",
				snippet: `
func (d Digest) Equal(other Digest) bool {
    return d.bytes == other.bytes
}`,
			},
		},
		{
			name: "shorthand struct, user Copy",
			decl: "struct Digest(bytes int)",
			over: override{
				method: "Copy",
				snippet: `
func (d Digest) Copy() Digest {
    return Digest(bytes = d.bytes)
}`,
			},
		},
		{
			name: "shorthand struct, user Unapply",
			// Public field (uppercase) so the transpiler would auto-generate
			// Unapply by default — we want to confirm the user's wins instead.
			decl: "struct Digest(Bytes int)",
			over: override{
				method: "Unapply",
				snippet: `
func (d Digest) Unapply(v Digest) int {
    return v.Bytes
}`,
			},
		},
		{
			name: "block struct, user Equal",
			decl: `type Digest struct {
    bytes int
}`,
			over: override{
				method: "Equal",
				snippet: `
func (d Digest) Equal(other Digest) bool {
    return d.bytes == other.bytes
}`,
			},
		},
		{
			name: "block struct, user Copy",
			decl: `type Digest struct {
    bytes int
}`,
			over: override{
				method: "Copy",
				snippet: `
func (d Digest) Copy() Digest {
    return Digest(bytes = d.bytes)
}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "package main\n\n" + tt.decl + "\n" + tt.over.snippet + "\n"
			got, err := trans.Transpile(input, "")
			require.NoError(t, err)

			// Each method should appear exactly once in the generated Go.
			needle := ") " + tt.over.method + "("
			count := strings.Count(got, needle)
			assert.Equalf(t, 1, count,
				"expected exactly one %s method in generated Go (the user's), got %d.\n--- generated ---\n%s",
				tt.over.method, count, got)
		})
	}
}

// TestAutoMethodOverride_DefaultsStillGenerate is a sanity guard ensuring that
// when the user does NOT define Copy / Equal / Unapply, the transpiler still
// emits all three (the common case). This pairs with TestAutoMethodOverride
// to pin the auto-gen behavior from both sides.
func TestAutoMethodOverride_DefaultsStillGenerate(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Public fields (uppercase) are required to trigger Unapply auto-generation.
	input := `package main

struct Digest(Bytes int)`

	got, err := trans.Transpile(input, "")
	require.NoError(t, err)

	for _, method := range []string{"Copy", "Equal", "Unapply"} {
		needle := ") " + method + "("
		count := strings.Count(got, needle)
		assert.Equalf(t, 1, count,
			"expected auto-generated %s for plain struct, got %d occurrences.\n--- generated ---\n%s",
			method, count, got)
	}
}
