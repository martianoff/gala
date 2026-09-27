package lsp

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

// TestIsStablePatternName pins which names in a case pattern the LSP treats as
// compared values rather than bindings: only a capitalized package val/var.
func TestIsStablePatternName(t *testing.T) {
	richAST := &transpiler.RichAST{PackageVals: map[string]*transpiler.PackageValMetadata{
		"Answer": {Name: "Answer", IsVal: true},
		"limit":  {Name: "limit", IsVal: true},
	}}
	for name, want := range map[string]bool{"Answer": true, "limit": false, "Other": false, "": false} {
		if got := isStablePatternName(name, richAST); got != want {
			t.Errorf("isStablePatternName(%q) = %v, want %v", name, got, want)
		}
	}
	if isStablePatternName("Answer", nil) {
		t.Error("isStablePatternName with no analysis must report false")
	}
}
