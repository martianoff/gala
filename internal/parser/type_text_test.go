package parser

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/native"
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
			p := parseGalop(t, "package main\n\nval v "+tc.typ+" = nil\n")
			require.NoError(t, p.Err())
			root, ok := p.Native.Root()
			require.True(t, ok)
			var typ native.TypeNode
			for _, d := range (native.SourceFileNode{GalaNodeRef: root}).AllTopLevelDeclaration() {
				if vd := d.ValDeclaration(); vd.Ok() {
					typ = vd.Type_()
				}
			}
			require.True(t, typ.Ok())
			require.Equal(t, tc.want, NativeTypeText(typ))
		})
	}
}
