package parser

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStructTagIsCoded pins that a Go struct tag after a struct field's type
// is GALA-E0085 at the tag, in every form a struct field takes, instead of
// ANTLR's "extraneous input" message. GALA has no struct tags.
func TestStructTagIsCoded(t *testing.T) {
	cases := []struct {
		name  string
		input string
		field string
		line  int
		col   int
		span  int // column one past the tag's last character
	}{
		{
			name:  "block_interpreted_string",
			input: "package main\n\ntype Location struct {\n    CountryCode string \"json:\\\"country_code\\\"\"\n}\n",
			field: "CountryCode",
			line:  4, col: 23, span: 46,
		},
		{
			name:  "block_raw_string",
			input: "package main\n\ntype Location struct {\n    CountryCode string `json:\"country_code\"`\n}\n",
			field: "CountryCode",
			line:  4, col: 23, span: 44,
		},
		{
			name:  "block_var_field_generic_type",
			input: "package main\n\ntype Box struct {\n    var Items Array[int] `json:\"items\"`\n    Name string\n}\n",
			field: "Items",
			line:  4, col: 25, span: 39,
		},
		{
			name:  "shorthand_struct",
			input: "package main\n\nstruct Location(Code string `json:\"code\"`, City string)\n",
			field: "Code",
			line:  3, col: 28, span: 41,
		},
		{
			name:  "shorthand_struct_last_field",
			input: "package main\n\nstruct Location(Code string, City string \"json:\\\"city\\\"\")\n",
			field: "City",
			line:  3, col: 41, span: 56,
		},
		{
			name:  "sealed_case_field",
			input: "package main\n\nsealed type Shape {\n    case Circle(Radius float64 `json:\"r\"`)\n    case Dot()\n}\n",
			field: "Radius",
			line:  4, col: 31, span: 41,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, errs := NewAntlrGalaParser().ParseLenient(tc.input)
			require.Len(t, errs, 1, "one tag is one diagnostic, got: %v", errs)
			se, ok := errs[0].(*galaerr.SemanticError)
			require.True(t, ok, "expected a coded diagnostic, got: %v", errs[0])
			assert.Equal(t, galaerr.CodeStructTag, se.Code)
			assert.Equal(t, "struct field "+tc.field+" has a tag: GALA has no struct tags", se.Msg)
			assert.Contains(t, se.Hint, "Codec[T]")
			assert.Contains(t, se.Hint, ".go file")
			assert.Equal(t, tc.line, se.Line)
			assert.Equal(t, tc.col, se.Column)
			assert.Equal(t, tc.span, se.EndColumn)
		})
	}
}

// TestStructTagEveryFieldReported checks that the parser recovers past a tag:
// each tagged field is reported, and nothing else is.
func TestStructTagEveryFieldReported(t *testing.T) {
	input := "package main\n\ntype User struct {\n" +
		"    Name string `json:\"name\"`\n" +
		"    var Age int \"json:\\\"age\\\"\"\n" +
		"    Email string\n" +
		"}\n"
	_, _, errs := NewAntlrGalaParser().ParseLenient(input)
	require.Len(t, errs, 2, "got: %v", errs)
	for i, line := range []int{4, 5} {
		se, ok := errs[i].(*galaerr.SemanticError)
		require.True(t, ok, "got: %v", errs[i])
		assert.Equal(t, galaerr.CodeStructTag, se.Code)
		assert.Equal(t, line, se.Line)
	}
}

// TestStringOutsideStructFieldsIsNotATag keeps the diagnostic to struct fields:
// a stray string in a function's parameter list is an ordinary syntax error,
// and strings in a field's default value or in expressions still parse.
func TestStringOutsideStructFieldsIsNotATag(t *testing.T) {
	_, _, errs := NewAntlrGalaParser().ParseLenient("package main\n\nfunc f(a int \"x\") int = a\n")
	require.NotEmpty(t, errs)
	for _, e := range errs {
		if se, ok := e.(*galaerr.SemanticError); ok {
			assert.NotEqual(t, galaerr.CodeStructTag, se.Code, "a function parameter has no tag to report: %v", e)
		}
	}

	valid := []string{
		"package main\n\nstruct Config(Name string = \"default\", Path string = `C:\\tmp`)\n",
		"package main\n\nsealed type Msg {\n    case Text(Body string = \"hi\")\n}\n",
		"package main\n\ntype User struct {\n    Name string\n}\n\nfunc main() {\n    Println(\"ok\")\n}\n",
	}
	for _, in := range valid {
		_, _, errs := NewAntlrGalaParser().ParseLenient(in)
		assert.Empty(t, errs, "must parse cleanly: %s", in)
	}
}
