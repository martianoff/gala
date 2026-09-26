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

// TestMergeLeavesPackageValsAlone: package-level bindings never travel through
// Merge — the receiver's own PackageVals are pre-registered unqualified by the
// transformer, and imported bindings arrive only via AddImportedVals.
func TestMergeLeavesPackageValsAlone(t *testing.T) {
	r := &RichAST{PackageName: "main"}
	r.Merge(&RichAST{
		PackageName: "colors",
		PackageVals: map[string]*PackageValMetadata{
			"Green": {Name: "Green", Type: NamedType{Package: "colors", Name: "Color"}, IsVal: true},
		},
	})
	if len(r.PackageVals) != 0 || len(r.ImportedVals) != 0 {
		t.Fatalf("Merge carried bindings: PackageVals=%v ImportedVals=%v", r.PackageVals, r.ImportedVals)
	}
}

// TestAddImportedValsKeyedByPath: two imported packages sharing a name keep
// their bindings apart, and a package already recorded is not overwritten.
func TestAddImportedValsKeyedByPath(t *testing.T) {
	a := map[string]*PackageValMetadata{"Limit": {Name: "Limit", Type: BasicType{Name: "int"}, IsVal: true}}
	b := map[string]*PackageValMetadata{"Limit": {Name: "Limit", Type: BasicType{Name: "string"}, IsVal: true}}
	r := &RichAST{PackageName: "main"}
	r.AddImportedVals("example.com/a/util", a)
	r.AddImportedVals("example.com/b/util", b)
	r.AddImportedVals("example.com/a/util", b) // closure walks revisit packages

	tests := []struct{ path, want string }{
		{"example.com/a/util", "int"},
		{"example.com/b/util", "string"},
	}
	for _, tc := range tests {
		if got := r.ImportedVals[tc.path]["Limit"].Type.String(); got != tc.want {
			t.Errorf("ImportedVals[%q][Limit] = %s, want %s", tc.path, got, tc.want)
		}
	}
}

// TestPreferPackageVal: a known type is never traded for an unknown one.
func TestPreferPackageVal(t *testing.T) {
	known := &PackageValMetadata{Type: BasicType{Name: "int"}}
	unknown := &PackageValMetadata{Type: NilType{}}
	tests := []struct {
		name                string
		existing, candidate *PackageValMetadata
		want                bool
	}{
		{"nothing recorded", nil, unknown, true},
		{"unknown replaced by known", unknown, known, true},
		{"known kept over unknown", known, unknown, false},
		{"known kept over known", known, &PackageValMetadata{Type: BasicType{Name: "string"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PreferPackageVal(tc.existing, tc.candidate); got != tc.want {
				t.Errorf("PreferPackageVal = %v, want %v", got, tc.want)
			}
		})
	}
}
