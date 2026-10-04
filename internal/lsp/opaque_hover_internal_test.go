package lsp

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// A phantom-typed opaque type compares against itself with its type
// parameters.
func TestFormatTypeMetaOpaqueGeneric(t *testing.T) {
	got := formatTypeMeta(&transpiler.RichAST{PackageName: "ids"}, &transpiler.TypeMetadata{
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
