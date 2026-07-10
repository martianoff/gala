package lsp

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestKeywordCompletions_NoForbiddenBuiltins verifies that keyword/builtin
// completion never offers a bare Go builtin the compiler forbids (GALA-E0035),
// cross-checking against the authoritative set in the transformer so the two
// cannot drift. The sanctioned names (Println/Print/SliceOf) must still appear.
func TestKeywordCompletions_NoForbiddenBuiltins(t *testing.T) {
	forbidden := transformer.ForbiddenGoBuiltins()
	if len(forbidden) == 0 {
		t.Fatal("expected a non-empty forbidden-builtin set from the transformer")
	}

	offered := make(map[string]bool)
	for _, item := range keywordCompletions() {
		offered[item.Label] = true
	}

	for name := range forbidden {
		if offered[name] {
			t.Errorf("keywordCompletions offers forbidden Go builtin %q (GALA-E0035)", name)
		}
	}
	for _, want := range []string{"Println", "Print", "SliceOf"} {
		if !offered[want] {
			t.Errorf("keywordCompletions missing sanctioned builtin %q", want)
		}
	}
}

// TestResolveMethodReturn_SizeSugar verifies that GALA's `.Size()` / `.ByteSize()`
// magic methods on Go primitive receivers (string, slice, map) type-resolve to
// int in the LSP, mirroring the transpiler's tryTransformSizeSugar. These
// receivers have no GALA TypeMetadata, so resolveMethodReturn must special-case
// them before the findType lookup.
func TestResolveMethodReturn_SizeSugar(t *testing.T) {
	// Empty RichAST: string/slice/map are Go primitives, never GALA types, so
	// the result must come purely from the size-sugar path.
	richAST := &transpiler.RichAST{
		Types:     map[string]*transpiler.TypeMetadata{},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	tests := []struct {
		name     string
		typeName string
		method   string
		want     string
	}{
		{"string Size", "string", "Size", "int"},
		{"string ByteSize", "string", "ByteSize", "int"},
		{"slice Size", "[]int", "Size", "int"},
		{"slice of struct Size", "[]Person", "Size", "int"},
		{"map Size (stripped)", "map", "Size", "int"},
		{"map Size (spelled)", "map[string]int", "Size", "int"},
		// ByteSize is string-only: slices/maps must NOT resolve it.
		{"slice ByteSize not resolved", "[]int", "ByteSize", ""},
		{"map ByteSize not resolved", "map", "ByteSize", ""},
		// A non-primitive receiver with no metadata resolves to nothing.
		{"unknown type Size", "Person", "Size", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMethodReturn(richAST, tt.typeName, tt.method)
			if got != tt.want {
				t.Errorf("resolveMethodReturn(%q, %q) = %q, want %q",
					tt.typeName, tt.method, got, tt.want)
			}
		})
	}
}

// TestGoSizeSugarCompletions verifies the completion items offered on Go
// primitive receivers: Size() on string/slice/map, ByteSize() only on string.
func TestGoSizeSugarCompletions(t *testing.T) {
	hasLabel := func(items []completionLabel, want string) bool {
		for _, it := range items {
			if it.label == want {
				return true
			}
		}
		return false
	}

	toLabels := func(typeName string) []completionLabel {
		var out []completionLabel
		for _, it := range goSizeSugarCompletions(typeName) {
			out = append(out, completionLabel{label: it.Label, detail: it.Detail})
		}
		return out
	}

	// string: Size() and ByteSize(), both typed () int.
	strItems := toLabels("string")
	if !hasLabel(strItems, "Size()") {
		t.Errorf("string: missing Size() completion, got %v", strItems)
	}
	if !hasLabel(strItems, "ByteSize()") {
		t.Errorf("string: missing ByteSize() completion, got %v", strItems)
	}
	for _, it := range strItems {
		if it.detail != "() int" {
			t.Errorf("string %s: detail = %q, want %q", it.label, it.detail, "() int")
		}
	}

	// slice: Size() only, no ByteSize().
	sliceItems := toLabels("[]int")
	if !hasLabel(sliceItems, "Size()") {
		t.Errorf("slice: missing Size() completion, got %v", sliceItems)
	}
	if hasLabel(sliceItems, "ByteSize()") {
		t.Errorf("slice: ByteSize() should NOT be offered, got %v", sliceItems)
	}

	// map: Size() only.
	mapItems := toLabels("map")
	if !hasLabel(mapItems, "Size()") {
		t.Errorf("map: missing Size() completion, got %v", mapItems)
	}
	if hasLabel(mapItems, "ByteSize()") {
		t.Errorf("map: ByteSize() should NOT be offered, got %v", mapItems)
	}

	// non-sizeable receiver: nothing.
	if items := toLabels("Person"); len(items) != 0 {
		t.Errorf("Person: expected no size-sugar completions, got %v", items)
	}
}

type completionLabel struct {
	label  string
	detail string
}
