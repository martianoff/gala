package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// AddImportPathKeys records a package's entries under its import path too, so
// two packages of one name stay apart; HasQualified finds either key.
func TestGoTypeInfoImportPathKeys(t *testing.T) {
	g := NewGoTypeInfo()
	g.Functions["util.Get"] = &GoFuncSignature{Returns: []Type{BasicType{Name: "string"}}}
	g.Types["util.Box"] = &GoTypeData{Kind: "struct"}
	g.Variables["util.Default"] = BasicType{Name: "int"}
	g.Functions["other.Get"] = &GoFuncSignature{}

	g.AddImportPathKeys("util", "example.com/b/util")

	cases := []struct {
		key  string
		want bool
	}{
		{"example.com/b/util.Get", true},
		{"example.com/b/util.Box", true},
		{"example.com/b/util.Default", true},
		{"util.Get", true},
		{"example.com/b/other.Get", false},
		{"example.com/b/util.Missing", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, g.HasQualified(tc.key), tc.key)
	}
	assert.Same(t, g.Functions["util.Get"], g.Functions["example.com/b/util.Get"])

	t.Run("no-op when the path is the name", func(t *testing.T) {
		g := NewGoTypeInfo()
		g.Functions["strings.Cut"] = &GoFuncSignature{}
		g.AddImportPathKeys("strings", "strings")
		assert.Len(t, g.Functions, 1)
	})
	t.Run("nil info", func(t *testing.T) {
		var g *GoTypeInfo
		g.AddImportPathKeys("util", "example.com/util")
		assert.False(t, g.HasQualified("example.com/util.Get"))
	})
}
