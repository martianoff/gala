package lsp

import (
	"strings"
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
				{Name: "Box", FieldNames: []string{"W", "H"}, FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}, transpiler.BasicType{Name: "string"}}},
			},
		},
		"main.UserID": {Name: "UserID", IsOpaque: true, Underlying: transpiler.BasicType{Name: "int64"}},
		"main.frame": {
			Name: "frame", IsSealed: true,
			SealedVariants: []transpiler.SealedVariant{{Name: "endFrame"}, {Name: "EndAll"}},
		},
		"main.wrap": {
			Name: "wrap", IsSealed: true,
			SealedVariants: []transpiler.SealedVariant{
				{Name: "wrapped", FieldNames: []string{"F"}, FieldTypes: []transpiler.Type{transpiler.NamedType{Package: "main", Name: "frame"}}},
			},
		},
	}, PackageVals: map[string]*transpiler.PackageValMetadata{"Root": {}}}

	cases := []struct {
		name string
		line string
		want []string
	}{
		{name: "sealed type's own type param", line: "        case Ok(v) => v", want: nil},
		{name: "user type named P", line: "        case Dot(p) => p", want: []string{`": P"`}},
		{name: "opaque type binds its underlying type", line: "    case UserID(n) => n", want: []string{`": int64"`}},
		{name: "opaque type with a literal", line: "    case UserID(0) => 0", want: nil},
		{name: "opaque type with a wildcard", line: "    case UserID(_) => 0", want: nil},
		{name: "opaque type with a stable identifier", line: "    case UserID(Root) => 0", want: nil},
		// A variant of the field's sealed type tests the field: no binding,
		// whatever the case of its first letter.
		{name: "lowercase variant of the field's type", line: "    case wrapped(endFrame) => 0", want: nil},
		{name: "capitalized variant of the field's type", line: "    case wrapped(EndAll) => 0", want: nil},
		{name: "binding of a sealed-typed field", line: "    case wrapped(f) => f", want: []string{`": main.frame"`}},
		// A named sub-pattern binds the field it names, wherever it is written.
		{name: "named sub-pattern", line: "    case Box(H = h) => h", want: []string{`": string"`}},
		{name: "named sub-pattern without spaces", line: "    case Box(H=h, W=w) => h", want: []string{`": string"`, `": int"`}},
		{name: "named sub-pattern naming no field", line: "    case Box(D = d) => d", want: nil},
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

// A named sub-pattern's hint follows its bound name, not an earlier field
// label spelled the same way.
func TestCasePatternHints_NamedSubPatternPosition(t *testing.T) {
	richAST := &transpiler.RichAST{Types: map[string]*transpiler.TypeMetadata{
		"main.Shape": {
			Name: "Shape", IsSealed: true,
			SealedVariants: []transpiler.SealedVariant{
				{Name: "Box", FieldNames: []string{"W", "H"}, FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}, transpiler.BasicType{Name: "string"}}},
			},
		},
	}}
	line := "    case Box(H = x, W = H) => H"
	hints := casePatternHints(line, 0, richAST)
	if assert.Len(t, hints, 2) {
		// x ends at the column after `x`; H (bound to W) after the second H.
		assert.Equal(t, strings.Index(line, "H = x")+len("H = x"), hints[0].Position.Character)
		assert.Equal(t, strings.LastIndex(line, "W = H")+len("W = H"), hints[1].Position.Character)
	}
}
