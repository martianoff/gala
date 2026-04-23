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

// TestSealedStringFuncField verifies the generated String() method handles
// func-typed sealed variant fields in a go-vet-safe way. Formatting a
// function value with %v trips go vet ("is a func value, not called"), which
// breaks `go test` on any package that prints such a variant. The transpiler
// must substitute a literal "<fn>" placeholder (no format verb) for func
// fields and only %v-format non-func fields.
func TestSealedStringFuncField(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name      string
		input     string
		mustHave  []string
		mustNotHv []string
	}{
		{
			name: "Variant with only a func field emits a plain string literal",
			input: `package main

sealed type Handler {
    case Dynamic(Make func(int) string)
}
`,
			// All fields are func-typed -> no %v verbs, plain string literal, no fmt.Sprintf call.
			mustHave: []string{
				`return "Dynamic(<fn>)"`,
			},
			mustNotHv: []string{
				`fmt.Sprintf("Dynamic(%v)", s.Make.Get())`,
			},
		},
		{
			name: "Variant mixing func and non-func fields keeps %v for non-func only",
			input: `package main

sealed type Handler {
    case Plain(Msg string)
    case Dynamic(Make func(int) string)
}
`,
			// Plain: %v for Msg (non-func).
			// Dynamic: literal <fn> (no %v, no arg).
			mustHave: []string{
				`return fmt.Sprintf("Plain(%v)", s.Msg.Get())`,
				`return "Dynamic(<fn>)"`,
			},
			mustNotHv: []string{
				`fmt.Sprintf("Dynamic(%v)", s.Make.Get())`,
			},
		},
		{
			name: "Variant with func field and regular field keeps only non-func %v",
			input: `package main

sealed type Callback {
    case Cb(Label string, Fn func(int) int)
}
`,
			// Label -> %v, Fn -> <fn> placeholder.
			mustHave: []string{
				`return fmt.Sprintf("Cb(%v, <fn>)", s.Label.Get())`,
			},
			mustNotHv: []string{
				`s.Fn.Get()`, // func field must not be passed as a format arg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
			for _, frag := range tt.mustNotHv {
				assert.False(t, strings.Contains(gen, frag),
					"expected generated code NOT to contain %q\n--- generated ---\n%s", frag, gen)
			}
		})
	}
}
