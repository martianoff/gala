package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A plain-struct destructuring pattern over an `any`-typed subject must emit a
// Go type assertion (`v.(Person)`) before reading fields; otherwise the
// generated code fails to compile with "type any has no field or method Name".
// Conversely, a concretely-typed subject must NOT be asserted — `p.(Person)` on
// a non-interface value is itself a Go compile error.
func TestMatchStructPatternAnySubjectAssertsType(t *testing.T) {
	newTranspiler := func() *transpiler.GalaToGoTranspiler {
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
	}

	t.Run("any subject inserts type assertion", func(t *testing.T) {
		input := `package main

struct Person(Name string, Age int)

func describe(v any) string = v match {
    case i: int            => s"int: $i"
    case Person(name, age) => s"$name is $age"
    case _                 => "unknown"
}
`
		got, err := newTranspiler().Transpile(input, "")
		require.NoError(t, err, "any-subject struct destructuring must transpile")
		// The match subject is bound to `obj` in the generated closure; it must be
		// type-asserted (`obj.(Person)`) before field access. Note the always-present
		// Unapply method asserts `v.(Person)`, so we match on the `obj.` subject.
		assert.Contains(t, got, "obj.(Person)", "must type-assert the any subject before field access")
		// Fields must be read off the asserted value, never off the bare `any` subject.
		assert.NotContains(t, got, "obj.Name", "must not read fields directly off the any subject")
	})

	t.Run("concrete subject does not assert", func(t *testing.T) {
		input := `package main

struct Person(Name string, Age int)

func describe(p Person) string = p match {
    case Person(name, age) => s"$name is $age"
    case _                 => "unknown"
}
`
		got, err := newTranspiler().Transpile(input, "")
		require.NoError(t, err, "concrete-subject struct destructuring must transpile")
		// A concretely-typed subject must read fields straight off `obj` with no
		// assertion — `obj.(Person)` on a non-interface value is a Go compile error.
		// (The Unapply method still asserts `v.(Person)`; that's why we check `obj.`.)
		assert.NotContains(t, got, "obj.(Person)", "a concretely-typed subject must not be type-asserted (Go rejects asserting a non-interface)")
		assert.Contains(t, got, "obj.Name.Get()", "a concretely-typed subject reads fields directly")
	})
}
