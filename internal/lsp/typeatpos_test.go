package lsp

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

func TestResolveReceiverType_PackageAlias(t *testing.T) {
	richAST := &transpiler.RichAST{
		Packages:      map[string]string{"martianoff/gala/collection_immutable": "collection_immutable"},
		ImportAliases: map[string]string{"im": "collection_immutable"},
		Types:         map[string]*transpiler.TypeMetadata{},
		Functions:     map[string]*transpiler.FunctionMetadata{},
	}

	// Alias "im" should resolve to __package__:collection_immutable
	result := resolveReceiverType("im", richAST, nil)
	if result != "__package__:collection_immutable" {
		t.Errorf("expected '__package__:collection_immutable', got '%s'", result)
	}

	// Direct package name should still work
	result2 := resolveReceiverType("collection_immutable", richAST, nil)
	if result2 != "__package__:collection_immutable" {
		t.Errorf("expected '__package__:collection_immutable', got '%s'", result2)
	}

	// Unknown name should return empty
	result3 := resolveReceiverType("unknown", richAST, nil)
	if result3 != "" {
		t.Errorf("expected empty string for unknown name, got '%s'", result3)
	}
}

func TestResolveReceiverType_MultipleAliases(t *testing.T) {
	richAST := &transpiler.RichAST{
		Packages: map[string]string{
			"martianoff/gala/collection_immutable": "collection_immutable",
			"martianoff/gala/collection_mutable":   "collection_mutable",
		},
		ImportAliases: map[string]string{
			"immut": "collection_immutable",
			"mut":   "collection_mutable",
		},
		Types:     map[string]*transpiler.TypeMetadata{},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	result := resolveReceiverType("immut", richAST, nil)
	if result != "__package__:collection_immutable" {
		t.Errorf("expected '__package__:collection_immutable', got '%s'", result)
	}

	result2 := resolveReceiverType("mut", richAST, nil)
	if result2 != "__package__:collection_mutable" {
		t.Errorf("expected '__package__:collection_mutable', got '%s'", result2)
	}
}
