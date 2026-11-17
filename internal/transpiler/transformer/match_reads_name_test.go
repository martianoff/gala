package transformer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadsNameStopsAtRedeclaration: a nested match declares its own subject
// with `_subj := ...` or, when its cases give the subject's type, with
// `var _subj T = ...`. After either, `_subj` reads the new one; only the
// value it is declared with can read the outer one.
func TestReadsNameStopsAtRedeclaration(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"read", "_ = _subj", true},
		{"redeclared with :=", "_subj := y\n_ = _subj", false},
		{"redeclared with var", "var _subj any = y\n_ = _subj", false},
		{"read by the value of a var redeclaration", "var _subj any = _subj\n_ = _subj", true},
		{"redeclared with var in a nested block", "{\nvar _subj any = y\n_ = _subj\n}", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc f() {\n"+tc.body+"\n}\n", 0)
			require.NoError(t, err)
			body := file.Decls[0].(*ast.FuncDecl).Body.List
			assert.Equal(t, tc.want, readsName(body, "_subj"))
		})
	}
}
