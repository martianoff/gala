package parser

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestBareLambdaParamIsCoded pins L1: `x => e` is reported as GALA-E0042 with a
// hint naming the parenthesized form, instead of the raw ANTLR cascade (up to
// four errors, each dumping the full expected-token set and none of them
// mentioning parentheses).
func TestBareLambdaParamIsCoded(t *testing.T) {
	cases := []struct {
		name  string
		input string
		param string
	}{
		{
			name:  "argument_position",
			input: "package main\n\nfunc main() {\n    val ys = xs.Map(x => x * 2)\n}",
			param: "x",
		},
		{
			name:  "val_position",
			input: "package main\n\nfunc main() {\n    val f = value => value * 2\n}",
			param: "value",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := NewAntlrGalaParser()
			_, errs := p.ParseLenient(tc.input)
			require.NotEmpty(t, errs, "the bare lambda form must be rejected")
			var coded *galaerr.SemanticError
			for _, e := range errs {
				if se, ok := e.(*galaerr.SemanticError); ok && se.Code == galaerr.CodeBareLambdaParam {
					coded = se
					break
				}
			}
			require.NotNil(t, coded, "expected GALA-E0042, got: %v", errs)
			require.Contains(t, coded.Hint, "("+tc.param+") =>",
				"the hint must spell the corrected form")
			// Cascade suppression: one missing paren pair is one diagnostic.
			require.Len(t, errs, 1, "expected a single diagnostic, got: %v", errs)
		})
	}
}

// TestCaseArmWithBareIdentifierStillParses is the guard that made this a
// diagnostic rather than a grammar change. `pattern` reaches `expression`
// reaches `primaryExpr`, whose first alternative is `lambdaExpression`, so
// admitting a bare-identifier lambda would have made `case x => body` parse as
// a lambda and silently mean something else. It must keep parsing cleanly.
func TestCaseArmWithBareIdentifierStillParses(t *testing.T) {
	inputs := []string{
		"package main\n\nfunc f(n int) string = n match {\n    case x => \"got\"\n}",
		"package main\n\nfunc f(n int) string = n match {\n    case x if x > 0 => \"pos\"\n    case y => \"other\"\n}",
	}
	for _, in := range inputs {
		p := NewAntlrGalaParser()
		_, errs := p.ParseLenient(in)
		require.Empty(t, errs, "a bare-identifier case arm must still parse: %s", in)
	}
}
