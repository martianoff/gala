package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompositeLiteralLambdaFieldTypes pins a lambda written for a
// function-typed field of a `T{Field: value}` literal: it takes its parameter
// types from the field, as it does when the field is passed as a named
// argument (`T(Field = value)`), never GALA-E0033.
func TestCompositeLiteralLambdaFieldTypes(t *testing.T) {
	const goSrc = "package main\n\n" +
		"type Handler func(int) string\n\n" +
		"type Stage struct {\n\tApply func(n int) int\n}\n\n" +
		"type Pipeline struct {\n\tLabel string\n\tCheck func(n int, label string) error\n\tFormat Handler\n\tFirst Stage\n}\n"
	cases := []struct {
		name string
		gala string
		want string
	}{
		{
			name: "a GALA struct",
			gala: "struct Rule(Test func(int) bool)\n\nfunc run() Rule = Rule{Test: (n) => n > 2}\n",
			want: "func(n int) bool {",
		},
		{
			name: "a generic GALA struct with its type arguments",
			gala: "struct Mapper[A any, B any](F func(A) B)\n\nfunc run() Mapper[int, string] = Mapper[int, string]{F: (n) => s\"$n\"}\n",
			want: "func(n int) string {",
		},
		{
			name: "a generic GALA struct whose type argument is a same-named type parameter",
			gala: "struct Mapper[A any, B any](F func(A) B)\n\nfunc mk[A any](z A) Mapper[int, A] = Mapper[int, A]{F: (n) => z}\n",
			want: "func(n int) A {",
		},
		{
			name: "a GALA struct's field given by position",
			gala: "struct Rule(Name string, Test func(int) bool)\n\nfunc run() Rule = Rule{\"big\", (n) => n > 2}\n",
			want: "func(n int) bool {",
		},
		{
			name: "a Go struct's field given by position",
			gala: "func run() Stage = Stage{(n) => n * 2}\n",
			want: "func(n int) int {",
		},
		{
			name: "a Go struct's plain func field",
			gala: "func run() *Pipeline = &Pipeline{Check: (n, label) => nil}\n",
			want: "func(n int, label string) error {",
		},
		{
			name: "a Go struct's field of a named func type",
			gala: "func run() Pipeline = Pipeline{Format: (n) => s\"$n\"}\n",
			want: "func(n int) string {",
		},
		{
			name: "a nested Go struct literal",
			gala: "func run() Pipeline = Pipeline{Label: \"x\", First: Stage{Apply: (n) => n * 2}}\n",
			want: "func(n int) int {",
		},
		{
			name: "an if-expression of lambdas",
			gala: "func run(up bool) Stage = Stage{Apply: if (up) (n) => n + 1 else (n) => n - 1}\n",
			want: "func(n int) int {",
		},
		{
			name: "an imported Go struct",
			gala: "import \"net/http\"\n\nfunc run() *http.Client = &http.Client{CheckRedirect: (req, via) => nil}\n",
			want: "func(req *http.Request, via []*http.Request) error {",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, galaFile := samePackageModule(".", goSrc, "package main\n\n"+tc.gala)
			out, err := transpileInModule(t, files, galaFile)
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}
}

// TestCompositeLiteralLambdaFieldUnboundTypeParam pins a generic GALA struct
// literal that does not write its type arguments: the field's type names a
// type parameter nothing binds, so it gives the lambda no parameter types and
// an untyped parameter stays GALA-E0033 rather than reaching Go as a bare `A`.
func TestCompositeLiteralLambdaFieldUnboundTypeParam(t *testing.T) {
	files, galaFile := samePackageModule(".", "package main\n",
		"package main\n\nstruct Mapper[A any](F func(A) A)\n\nfunc run() Mapper[int] = Mapper{F: (n) => n}\n")
	_, err := transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
}
