package transpiler

import (
	"strings"
	"testing"
)

// A method the type declares itself, in GALA or in a .go file of its package,
// replaces the synthesized one, and an opaque type over bool — directly,
// through an alias or through a Go named type — gets no Compare.
func TestSynthesizedOpaqueMethods(t *testing.T) {
	tests := []struct {
		name string
		meta *TypeMetadata
		rich *RichAST
		want []string
	}{
		{
			name: "int64",
			meta: &TypeMetadata{Name: "UserID", IsOpaque: true, Underlying: BasicType{Name: "int64"}},
			want: []string{"Hash", "Compare"},
		},
		{
			name: "bool has no Compare",
			meta: &TypeMetadata{Name: "Flag", IsOpaque: true, Underlying: BasicType{Name: "bool"}},
			want: []string{"Hash"},
		},
		{
			name: "alias of bool has no Compare",
			meta: &TypeMetadata{Name: "Flag", Package: "app", IsOpaque: true, Underlying: NamedType{Name: "Switch"}},
			rich: &RichAST{TypeAliases: map[string]Type{"Switch": BasicType{Name: "bool"}}},
			want: []string{"Hash"},
		},
		{
			name: "Go named bool has no Compare",
			meta: &TypeMetadata{Name: "Flag", Package: "app", IsOpaque: true, Underlying: NamedType{Package: "cfg", Name: "Toggle"}},
			rich: &RichAST{GoTypeInfo: &GoTypeInfo{Types: map[string]*GoTypeData{
				"cfg.Toggle": {Underlying: BasicType{Name: "bool"}},
			}}},
			want: []string{"Hash"},
		},
		{
			name: "Hash declared in GALA",
			meta: &TypeMetadata{
				Name: "Key", IsOpaque: true, Underlying: BasicType{Name: "string"},
				Methods: map[string]*MethodMetadata{"Hash": {Name: "Hash"}},
			},
			want: []string{"Compare"},
		},
		{
			name: "Hash declared in a .go file of the package",
			meta: &TypeMetadata{Name: "UserID", Package: "ids", IsOpaque: true, Underlying: BasicType{Name: "int64"}},
			rich: &RichAST{GoTypeInfo: &GoTypeInfo{GalaTypeMethods: map[string]*GoTypeData{
				"ids.UserID": {Methods: map[string]*GoFuncSignature{"Hash": {}}},
			}}},
			want: []string{"Compare"},
		},
		{
			name: "not opaque",
			meta: &TypeMetadata{Name: "Point"},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, m := range tt.meta.SynthesizedOpaqueMethods(tt.rich) {
				got = append(got, m.Name)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
