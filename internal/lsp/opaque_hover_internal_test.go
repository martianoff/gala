package lsp

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// A method the type declares itself replaces the synthesized one, and an
// opaque type over bool gets no Compare.
func TestSynthesizedOpaqueMethods(t *testing.T) {
	tests := []struct {
		name string
		meta *transpiler.TypeMetadata
		want []string
	}{
		{
			name: "int64",
			meta: &transpiler.TypeMetadata{Name: "UserID", IsOpaque: true, Underlying: transpiler.BasicType{Name: "int64"}},
			want: []string{"Hash", "Compare"},
		},
		{
			name: "bool has no Compare",
			meta: &transpiler.TypeMetadata{Name: "Flag", IsOpaque: true, Underlying: transpiler.BasicType{Name: "bool"}},
			want: []string{"Hash"},
		},
		{
			name: "declared Hash wins",
			meta: &transpiler.TypeMetadata{
				Name: "Key", IsOpaque: true, Underlying: transpiler.BasicType{Name: "string"},
				Methods: map[string]*transpiler.MethodMetadata{"Hash": {Name: "Hash"}},
			},
			want: []string{"Compare"},
		},
		{
			name: "not opaque",
			meta: &transpiler.TypeMetadata{Name: "Point"},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, m := range tt.meta.SynthesizedOpaqueMethods() {
				got = append(got, m.Name)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A phantom-typed opaque type compares against itself with its type
// parameters.
func TestFormatTypeMetaOpaqueGeneric(t *testing.T) {
	got := formatTypeMeta(&transpiler.TypeMetadata{
		Name: "Id", Package: "ids", IsOpaque: true,
		TypeParams: []string{"T"},
		Underlying: transpiler.BasicType{Name: "int64"},
	})
	for _, want := range []string{"opaque type Id[T] int64", "`Compare(other Id[T]) int` *(synthesized)*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
}
