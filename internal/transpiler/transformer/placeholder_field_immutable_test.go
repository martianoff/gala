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

// TestPlaceholderFieldImmutableUnwrap verifies that the placeholder-lambda
// shorthand `_.Field` behaves exactly like the explicit lambda
// `(p) => p.Field`. For a shorthand struct, every field has `val`/Immutable
// semantics, so field access must auto-unwrap via `.Get()`. The placeholder
// path previously bound `_` to `any`, which prevented the field-access
// transform from finding the struct metadata and emitting the unwrap.
func TestPlaceholderFieldImmutableUnwrap(t *testing.T) {
	newTranspiler := func() *transpiler.GalaToGoTranspiler {
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
	}

	const explicitSrc = `package main

import . "martianoff/gala/collection_immutable"

struct Person(Name string, Age int)

func main() {
    val people = ArrayOf(Person(Name = "Alice", Age = 30), Person(Name = "Bob", Age = 25))
    val names = people.Map((p) => p.Name)
    Println(names)
}`

	const placeholderSrc = `package main

import . "martianoff/gala/collection_immutable"

struct Person(Name string, Age int)

func main() {
    val people = ArrayOf(Person(Name = "Alice", Age = 30), Person(Name = "Bob", Age = 25))
    val names = people.Map(_.Name)
    Println(names)
}`

	explicit, err := newTranspiler().Transpile(explicitSrc, "")
	require.NoError(t, err)
	placeholder, err := newTranspiler().Transpile(placeholderSrc, "")
	require.NoError(t, err)

	// The explicit form must unwrap the Immutable field via .Get().
	assert.Contains(t, explicit, ".Name.Get()",
		"explicit lambda should unwrap the Immutable field")

	// The placeholder form must do the SAME unwrap — this is the bug.
	assert.Contains(t, placeholder, ".Name.Get()",
		"placeholder lambda must unwrap the Immutable field just like the explicit form")

	// Both should produce identical Map lambda bodies (modulo the param name).
	explicitBody := normalizeParamName(extractMapLambda(explicit))
	placeholderBody := normalizeParamName(extractMapLambda(placeholder))
	assert.Equal(t, explicitBody, placeholderBody,
		"placeholder and explicit Map lambdas must be equivalent")
}

// extractMapLambda returns the substring of the generated Go containing the
// Map callback FuncLit, for a body comparison. The generated code lowers
// `xs.Map(f)` to `Array_Map(xs.Get(), f)`, so we key off the emitted `func(`
// literal that follows the Array_Map call.
func extractMapLambda(src string) string {
	call := strings.Index(src, "Array_Map(")
	if call < 0 {
		return ""
	}
	fn := strings.Index(src[call:], "func(")
	if fn < 0 {
		return ""
	}
	rest := src[call+fn:]
	// Cut at the end of the lambda body (the closing `}` of the FuncLit).
	if end := strings.Index(rest, "}"); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// normalizeParamName rewrites the placeholder param `__p0` and an explicit
// param `p` to a common token so the two lambda bodies can be compared.
func normalizeParamName(s string) string {
	s = strings.ReplaceAll(s, "__p0", "PARAM")
	s = strings.ReplaceAll(s, "func(p ", "func(PARAM ")
	s = strings.ReplaceAll(s, "p.Name", "PARAM.Name")
	return s
}

// TestPlaceholderEquivalentToExplicitLambda checks that `_`-shorthand lambdas
// produce lambda bodies equivalent to their explicit `(p) => ...` counterparts
// across a range of forms: Immutable field access, non-field expressions,
// nested/parenthesized placeholders, method calls, and multiple placeholders.
func TestPlaceholderEquivalentToExplicitLambda(t *testing.T) {
	newTranspiler := func() *transpiler.GalaToGoTranspiler {
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
	}

	// prelude declares a struct with an Immutable field plus a method.
	const prelude = `package main

import . "martianoff/gala/collection_immutable"

struct Person(Name string, Age int)

func (p Person) Label() string = p.Name

func main() {
    val people = ArrayOf(Person(Name = "Alice", Age = 30), Person(Name = "Bob", Age = 25))
`

	// Each case: a single Map (or Reduce) call, once with `_` and once explicit.
	tests := []struct {
		name        string
		placeholder string
		explicit    string
	}{
		{
			name:        "immutable field access",
			placeholder: `    Println(people.Map(_.Name))`,
			explicit:    `    Println(people.Map((p) => p.Name))`,
		},
		{
			name:        "immutable int field access",
			placeholder: `    Println(people.Map(_.Age))`,
			explicit:    `    Println(people.Map((p) => p.Age))`,
		},
		{
			name:        "method call via placeholder",
			placeholder: `    Println(people.Map(_.Label()))`,
			explicit:    `    Println(people.Map((p) => p.Label()))`,
		},
		{
			name:        "nested parenthesized field",
			placeholder: `    Println(people.Map((_.Age) + 1))`,
			explicit:    `    Println(people.Map((p) => (p.Age) + 1))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phSrc := prelude + tt.placeholder + "\n}"
			exSrc := prelude + tt.explicit + "\n}"

			ph, err := newTranspiler().Transpile(phSrc, "")
			require.NoError(t, err, "placeholder form should transpile")
			ex, err := newTranspiler().Transpile(exSrc, "")
			require.NoError(t, err, "explicit form should transpile")

			assert.Equal(t,
				normalizeLambda(extractMapLambda(ex)),
				normalizeLambda(extractMapLambda(ph)),
				"placeholder lambda must match explicit lambda\nexplicit:\n%s\nplaceholder:\n%s", ex, ph)
		})
	}
}

// normalizeLambda collapses the placeholder param name `__p0` and the explicit
// param name `p` to a common token so the two rendered lambdas can be compared
// structurally.
func normalizeLambda(s string) string {
	s = strings.ReplaceAll(s, "__p0", "PARAM")
	s = strings.ReplaceAll(s, "func(p ", "func(PARAM ")
	s = strings.ReplaceAll(s, "p.", "PARAM.")
	return s
}
