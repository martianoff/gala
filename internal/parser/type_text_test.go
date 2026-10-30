package parser

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/grammar"
)

// TestTypeText pins the spelling of a type: its source text without
// whitespace, a function type's parameters spelled by their types only.
func TestTypeText(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{"int", "int"},
		{"Option[ Array[ int ] ]", "Option[Array[int]]"},
		{"pkg.Box[int, string]", "pkg.Box[int,string]"},
		{"map[string] []int", "map[string][]int"},
		{"*Node", "*Node"},
		{"func(int) int", "func(int)int"},
		{"func(msg string)", "func(string)"},
		{"func(prefix string, parts ...string) Option[int]", "func(string,...string)Option[int]"},
		{"func(p []byte) (int, error)", "func([]byte)(int,error)"},
		{"Option[func(x int) func(y int) int]", "Option[func(int)func(int)int]"},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			tree, _, err := NewAntlrGalaParser().ParseFile("package main\n\nval v " + tc.typ + " = nil\n").Strict()
			require.NoError(t, err)
			var typ grammar.ITypeContext
			for _, d := range tree.(*grammar.SourceFileContext).AllTopLevelDeclaration() {
				if vd := d.ValDeclaration(); vd != nil {
					typ = vd.(*grammar.ValDeclarationContext).Type_()
				}
			}
			require.NotNil(t, typ)
			require.Equal(t, tc.want, TypeText(typ))
		})
	}
}
