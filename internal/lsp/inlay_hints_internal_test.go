package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// A case-pattern binding typed by the sealed type's own type parameter gets no
// hint, but one typed by a user type — even one named with a single capital
// letter — shows that type.
func TestCasePatternHints_TypeParamsByScope(t *testing.T) {
	richAST := &transpiler.RichAST{Types: map[string]*transpiler.TypeMetadata{
		"main.Result": {
			Name: "Result", IsSealed: true, TypeParams: []string{"T"},
			SealedVariants: []transpiler.SealedVariant{
				{Name: "Ok", FieldNames: []string{"value"}, FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "T"}}},
			},
		},
		"main.Shape": {
			Name: "Shape", IsSealed: true,
			SealedVariants: []transpiler.SealedVariant{
				{Name: "Dot", FieldNames: []string{"at"}, FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "P"}}},
			},
		},
	}}

	cases := []struct {
		name string
		line string
		want []string
	}{
		{name: "sealed type's own type param", line: "        case Ok(v) => v", want: nil},
		{name: "user type named P", line: "        case Dot(p) => p", want: []string{`": P"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, h := range casePatternHints(tc.line, 0, richAST) {
				got = append(got, string(h.Label))
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
