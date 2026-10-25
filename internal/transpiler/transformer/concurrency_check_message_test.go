package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/concurrency"
)

// TestUnshareableCaptureMessage: GALA-E0037 says a capture's type "could not
// be inferred" exactly when the unknown part of the type is what makes it
// unshareable. An immutable collection of an unresolved element would be
// shareable if the element were known, so it reports the unknown type; a Go
// slice of one is a mutable slice whatever its element, so it reports the data
// race. A field path follows the same rule for the field's type.
func TestUnshareableCaptureMessage(t *testing.T) {
	const unknown = "could not be inferred"
	const race = "data race on its mutable contents"
	checker := concurrency.NewChecker(nil)
	capture := concurrency.Capture{Name: "v"}
	immArrayOf := func(elem transpiler.Type) transpiler.Type {
		return transpiler.GenericType{
			Base:   transpiler.NamedType{Package: "collection_immutable", Name: "Array"},
			Params: []transpiler.Type{elem},
		}
	}
	goSliceOf := func(elem transpiler.Type) transpiler.Type { return transpiler.ArrayType{Elem: elem} }
	intType := transpiler.BasicType{Name: "int"}
	tests := []struct {
		name     string
		err      error
		want     string
		dontWant string
	}{
		{
			name:     "whole capture of an unknown type",
			err:      unshareableValCaptureError(checker, capture, transpiler.NilType{}),
			want:     unknown,
			dontWant: race,
		},
		{
			name:     "whole capture of an immutable collection of an unknown element",
			err:      unshareableValCaptureError(checker, capture, immArrayOf(transpiler.NilType{})),
			want:     unknown,
			dontWant: race,
		},
		{
			name:     "whole capture of a Go slice of an unknown element",
			err:      unshareableValCaptureError(checker, capture, goSliceOf(transpiler.NilType{})),
			want:     race,
			dontWant: unknown,
		},
		{
			name:     "field path ending at a field of unknown type",
			err:      unshareableFieldPathCaptureError(checker, capture, "Names", transpiler.NilType{}, true),
			want:     unknown,
			dontWant: race,
		},
		{
			name:     "field path ending at an immutable collection of an unknown element",
			err:      unshareableFieldPathCaptureError(checker, capture, "Names", immArrayOf(transpiler.NilType{}), true),
			want:     unknown,
			dontWant: race,
		},
		{
			name:     "field path ending at a Go slice",
			err:      unshareableFieldPathCaptureError(checker, capture, "Names", goSliceOf(intType), true),
			want:     race,
			dontWant: unknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorContains(t, tc.err, "GALA-E0037")
			assert.ErrorContains(t, tc.err, tc.want)
			assert.NotContains(t, tc.err.Error(), tc.dontWant)
			assert.NotContains(t, tc.err.Error(), "val v T", "the hint must not suggest an explicit type annotation")
		})
	}
}
