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
// breaks `go test` on any package that prints such a variant. A case's
// fields go to std.FormatStruct, which prints a func field as <fn>, so no
// format string ever meets a function value.
func TestSealedStringFuncField(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	tests := []struct {
		name      string
		input     string
		mustHave  []string
		mustNotHv []string
	}{
		{
			name: "Variant with only a func field",
			input: `package main

sealed type Handler {
    case Dynamic(Make func(int) string)
}
`,
			mustHave:  []string{`return std.FormatStruct("Dynamic", s.Make.Get())`},
			mustNotHv: []string{`fmt.Sprintf`},
		},
		{
			name: "Variant mixing func and non-func fields",
			input: `package main

sealed type Handler {
    case Plain(Msg string)
    case Dynamic(Make func(int) string)
}
`,
			mustHave: []string{
				`return std.FormatStruct("Plain", s.Msg.Get())`,
				`return std.FormatStruct("Dynamic", s.Make.Get())`,
			},
			mustNotHv: []string{`fmt.Sprintf`},
		},
		{
			name: "Variant with func field and regular field",
			input: `package main

sealed type Callback {
    case Cb(Label string, Fn func(int) int)
}
`,
			mustHave:  []string{`return std.FormatStruct("Cb", s.Label.Get(), s.Fn.Get())`},
			mustNotHv: []string{`fmt.Sprintf`},
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
