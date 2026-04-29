package transpiler

import "testing"

// TestMergeDoesNotMutateShared documents and pins the copy-on-write contract
// of (*RichAST).Merge. Before the fix, a TypeMetadata pointer reachable from
// multiple RichASTs (e.g. the analyzer's std cache, shared across every
// per-file analysis) had its Methods/Fields maps mutated in place by every
// merge that supplied additional methods. The visible symptom was a 4.3 GB
// on-disk cache entry that pulled the same metadata into RAM at startup.
//
// The contract: Merge never observably mutates `other`. If the caller
// later inspects `other.Types[k]`, its Methods, Fields, FieldNames,
// ImmutFlags, TypeParams, TypeParamConstraints, and SealedVariants must
// match what was passed in.
func TestMergeDoesNotMutateShared(t *testing.T) {
	cached := &RichAST{
		PackageName: "std",
		Types: map[string]*TypeMetadata{
			"Option": {
				Name:    "Option",
				Package: "std",
				Methods: map[string]*MethodMetadata{
					"Map": {Name: "Map", Package: "std"},
				},
				FieldNames: []string{"value"},
			},
		},
	}
	originalMethodsCount := len(cached.Types["Option"].Methods)
	originalMethodsPtr := cached.Types["Option"].Methods

	// First file analysis merges the cache, then merges another package
	// whose RichAST also references "Option" with an extra method. Before
	// the fix, the extra method leaked back into `cached`.
	importMeta := &RichAST{
		Types: map[string]*TypeMetadata{
			"Option": {
				Name:    "Option",
				Package: "std",
				Methods: map[string]*MethodMetadata{
					"FlatMap": {Name: "FlatMap", Package: "std"},
				},
			},
		},
	}

	r := &RichAST{}
	r.Merge(cached)
	r.Merge(importMeta)

	if got := len(cached.Types["Option"].Methods); got != originalMethodsCount {
		t.Fatalf("cached.Types[Option].Methods grew to %d (was %d) — Merge mutated shared pointer", got, originalMethodsCount)
	}
	// Pointer equality on the inner Methods map: the cached entry's map
	// must still be the exact map it started with.
	if &cached.Types["Option"].Methods == nil || len(cached.Types["Option"].Methods) != len(originalMethodsPtr) {
		t.Fatalf("cached.Types[Option].Methods identity changed after Merge")
	}
	if _, leaked := cached.Types["Option"].Methods["FlatMap"]; leaked {
		t.Fatalf("FlatMap leaked from importMeta into cached.Types[Option].Methods")
	}

	// Sanity: the merged RichAST does see both methods.
	if got := len(r.Types["Option"].Methods); got != 2 {
		t.Fatalf("merged r.Types[Option].Methods has %d methods, want 2", got)
	}
}

// TestMergeNoOpWhenNothingNew exercises the fast path where `other` adds no
// new fields, methods, type params, or sealed variants for an existing key.
// In that case Merge must keep the existing pointer unchanged so callers
// can rely on pointer-equality for cache hits.
func TestMergeNoOpWhenNothingNew(t *testing.T) {
	method := &MethodMetadata{Name: "Map"}
	other := &RichAST{
		Types: map[string]*TypeMetadata{
			"Option": {
				Methods: map[string]*MethodMetadata{"Map": method},
			},
		},
	}
	existing := &TypeMetadata{
		Name:    "Option",
		Methods: map[string]*MethodMetadata{"Map": method},
	}
	r := &RichAST{Types: map[string]*TypeMetadata{"Option": existing}}
	r.Merge(other)
	if r.Types["Option"] != existing {
		t.Fatalf("Merge replaced the existing pointer even though `other` had nothing new")
	}
}
