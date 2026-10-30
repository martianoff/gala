package analyzer

import (
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

// TestBareCompanionNameIsDeterministic: when companions of several packages
// share a bare name, the bare name goes to the analyzed package's own
// companion, else to the one with the smallest key, on every run, whatever
// order the types map is iterated in.
func TestBareCompanionNameIsDeterministic(t *testing.T) {
	tests := []struct {
		name    string
		pkg     string
		types   []*transpiler.TypeMetadata
		wantPkg string
	}{
		{
			name:    "imported companions: the smallest key",
			pkg:     "main",
			types:   []*transpiler.TypeMetadata{companionType("yaml", "AsIs"), companionType("json", "AsIs"), companionType("toml", "AsIs")},
			wantPkg: "json",
		},
		{
			name:    "the analyzed package's own companion",
			pkg:     "yaml",
			types:   []*transpiler.TypeMetadata{companionType("yaml", "AsIs"), companionType("json", "AsIs")},
			wantPkg: "yaml",
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
				for _, typ := range tt.types {
					richAST.Types[typ.Package+"."+typ.Name] = typ
				}
				(&galaAnalyzer{}).discoverCompanionObjects(richAST)
				got := richAST.CompanionObjects["AsIs"]
				if got == nil || got.Package != tt.wantPkg {
					t.Fatalf("run %d: the bare name AsIs went to %+v, want package %s's", run, got, tt.wantPkg)
				}
				for _, typ := range tt.types {
					if c := richAST.CompanionObjects[typ.Package+".AsIs"]; c == nil || c.Package != typ.Package {
						t.Fatalf("run %d: %s.AsIs is filed as %+v", run, typ.Package, c)
					}
				}
			}
		})
	}
}
