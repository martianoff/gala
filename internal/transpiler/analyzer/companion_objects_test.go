package analyzer

import (
	"maps"
	"slices"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// companionType is a type of package pkg that is a companion: it has an
// Apply returning its package's T and an Unapply.
func companionType(pkg, name string) *transpiler.TypeMetadata {
	return &transpiler.TypeMetadata{
		Name:    name,
		Package: pkg,
		Methods: map[string]*transpiler.MethodMetadata{
			"Apply":   {Name: "Apply", ReturnType: transpiler.NamedType{Package: pkg, Name: "T"}},
			"Unapply": {Name: "Unapply", ReturnType: transpiler.BasicType{Name: "bool"}},
		},
	}
}

// TestCompanionKeys: a named package's companion is filed under its
// qualified key alone, never a bare one, which the transformer resolves per
// file; a keyless package's (main, test, none) under its bare name, the file's
// own winning a clash. A bare key a merged package filed for a named
// package's companion is dropped. The result is the same on every run,
// whatever order the types map is iterated in.
func TestCompanionKeys(t *testing.T) {
	type companion struct{ key, pkg string }
	tests := []struct {
		name       string
		pkg        string
		companions []companion
		// stale is a bare entry a merged package filed, of the package named.
		stale string
		want  map[string]string // key -> package of the companion filed there
	}{
		{
			name:       "named packages: qualified keys only",
			pkg:        "main",
			companions: []companion{{"json.AsIs", "json"}, {"yaml.AsIs", "yaml"}},
			stale:      "json",
			want:       map[string]string{"json.AsIs": "json", "yaml.AsIs": "yaml"},
		},
		{
			name:       "a library's own companion: its qualified key only",
			pkg:        "lib",
			companions: []companion{{"lib.AsIs", "lib"}, {"json.AsIs", "json"}},
			want:       map[string]string{"lib.AsIs": "lib", "json.AsIs": "json"},
		},
		{
			name:       "a main package's companion: its bare key",
			pkg:        "main",
			companions: []companion{{"AsIs", "main"}, {"yaml.AsIs", "yaml"}},
			want:       map[string]string{"AsIs": "main", "yaml.AsIs": "yaml"},
		},
		{
			name:       "two keyless companions: the file's own",
			pkg:        "main",
			companions: []companion{{"AsIs", "main"}, {"test.AsIs", "test"}},
			want:       map[string]string{"AsIs": "main"},
		},
		{
			name:       "a stale bare key of a keyless package stays",
			pkg:        "main",
			companions: []companion{{"yaml.AsIs", "yaml"}},
			stale:      "test",
			want:       map[string]string{"AsIs": "test", "yaml.AsIs": "yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Go randomizes map iteration per range, so many runs over fresh
			// maps meet the companions in many orders.
			for run := 0; run < 200; run++ {
				richAST := &transpiler.RichAST{
					PackageName:      tt.pkg,
					Types:            make(map[string]*transpiler.TypeMetadata),
					CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
				}
				for _, c := range tt.companions {
					richAST.Types[c.key] = companionType(c.pkg, "AsIs")
				}
				if tt.stale != "" {
					richAST.CompanionObjects["AsIs"] = &transpiler.CompanionObjectMetadata{Name: "AsIs", Package: tt.stale}
				}
				(&galaAnalyzer{}).discoverCompanionObjects(richAST)
				got := make(map[string]string)
				for k, c := range richAST.CompanionObjects {
					got[k] = c.Package
				}
				if !maps.Equal(got, tt.want) {
					t.Fatalf("run %d: filed %v (keys %v), want %v", run, got, slices.Sorted(maps.Keys(got)), tt.want)
				}
			}
		})
	}
}
