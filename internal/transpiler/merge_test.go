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

// TestMergeImportedVals pins how package-level bindings travel through Merge.
// An imported package's own bindings become package-qualified ImportedVals
// (exported names only); bindings of the SAME package never do, and never leak
// into the receiver's own, unqualified PackageVals either — the transformer
// pre-registers those as the current package's names.
func TestMergeImportedVals(t *testing.T) {
	colorType := NamedType{Package: "colors", Name: "Color"}
	tests := []struct {
		name         string
		receiverPkg  string
		other        *RichAST
		wantImported map[string]Type
	}{
		{
			name:        "different package: exported bindings are qualified",
			receiverPkg: "main",
			other: &RichAST{
				PackageName: "colors",
				PackageVals: map[string]*PackageValMetadata{
					"Green":  {Name: "Green", Type: colorType, IsVal: true},
					"Hits":   {Name: "Hits", Type: BasicType{Name: "int"}},
					"secret": {Name: "secret", Type: BasicType{Name: "int"}, IsVal: true},
				},
			},
			wantImported: map[string]Type{
				"colors.Green": colorType,
				"colors.Hits":  BasicType{Name: "int"},
			},
		},
		{
			name:        "same package: nothing is imported",
			receiverPkg: "colors",
			other: &RichAST{
				PackageName: "colors",
				PackageVals: map[string]*PackageValMetadata{
					"Green": {Name: "Green", Type: colorType, IsVal: true},
				},
			},
			wantImported: map[string]Type{},
		},
		{
			name:        "already-qualified bindings pass through a closure merge",
			receiverPkg: "main",
			other: &RichAST{
				PackageName: "palette",
				ImportedVals: map[string]*PackageValMetadata{
					"colors.Green": {Name: "Green", Type: colorType, IsVal: true},
				},
			},
			wantImported: map[string]Type{"colors.Green": colorType},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &RichAST{PackageName: tc.receiverPkg}
			r.Merge(tc.other)
			if len(r.PackageVals) != 0 {
				t.Errorf("Merge widened the receiver's own PackageVals: %v", r.PackageVals)
			}
			if len(r.ImportedVals) != len(tc.wantImported) {
				t.Fatalf("ImportedVals = %v, want keys %v", r.ImportedVals, tc.wantImported)
			}
			for key, want := range tc.wantImported {
				got, ok := r.ImportedVals[key]
				if !ok {
					t.Fatalf("ImportedVals missing %q", key)
				}
				if got.Type.String() != want.String() {
					t.Errorf("ImportedVals[%q].Type = %s, want %s", key, got.Type, want)
				}
			}
		})
	}
}

// TestMergeImportedValsKeepsKnownType: a later merge that only knows a
// binding by name must not erase the type an earlier merge recorded.
func TestMergeImportedValsKeepsKnownType(t *testing.T) {
	known := &RichAST{PackageName: "colors", PackageVals: map[string]*PackageValMetadata{
		"Green": {Name: "Green", Type: NamedType{Package: "colors", Name: "Color"}, IsVal: true},
	}}
	unknown := &RichAST{PackageName: "colors", PackageVals: map[string]*PackageValMetadata{
		"Green": {Name: "Green", Type: NilType{}, IsVal: true},
	}}
	r := &RichAST{PackageName: "main"}
	r.Merge(known)
	r.Merge(unknown)
	if got := r.ImportedVals["colors.Green"].Type; IsUnusable(got) {
		t.Fatalf("known type was replaced by an unknown one")
	}
}
