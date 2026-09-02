package lsp

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// richASTWithVariantClash builds a RichAST where the current package declares a
// type whose name is also a sealed case in an imported package — the shape that
// makes an unscoped variant lookup answer with the wrong symbol.
func richASTWithVariantClash() *transpiler.RichAST {
	return &transpiler.RichAST{
		PackageName: "app",
		Types: map[string]*transpiler.TypeMetadata{
			"Marker": {
				Name:    "Marker",
				Package: "app",
				Doc:     "Marker is the application's own type.",
				Fields:  map[string]transpiler.Type{},
			},
			"dep.Flow": {
				Name:     "Flow",
				Package:  "dep",
				IsSealed: true,
				SealedVariants: []transpiler.SealedVariant{
					{Name: "Marker", Doc: "Marker is a case of dep.Flow."},
				},
			},
			"app.Signal": {
				Name:     "Signal",
				Package:  "app",
				IsSealed: true,
				SealedVariants: []transpiler.SealedVariant{
					{Name: "Ping", Doc: "Ping is a case of app.Signal."},
				},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}
}

func TestLookupSymbolPrefersOwnTypeOverForeignVariant(t *testing.T) {
	got := lookupSymbol(richASTWithVariantClash(), "Marker")
	if !strings.Contains(got, "Marker is the application's own type.") {
		t.Errorf("own type was shadowed by an imported package's sealed case\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "Case of") {
		t.Errorf("own type rendered as a sealed case\n--- got ---\n%s", got)
	}
}

// A case with no same-named type still renders as a case.
func TestLookupSymbolRendersVariantWhenNoTypeShadows(t *testing.T) {
	got := lookupSymbol(richASTWithVariantClash(), "Ping")
	if !strings.Contains(got, "case Ping()") || !strings.Contains(got, "Case of") {
		t.Errorf("sealed case did not render as a case\n--- got ---\n%s", got)
	}
}

// findSealedVariant must prefer the requested package and be deterministic:
// Go map order would otherwise make the chosen parent differ between calls.
func TestFindSealedVariantIsScopedAndDeterministic(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "app",
		Types: map[string]*transpiler.TypeMetadata{
			"app.Local":  {Name: "Local", Package: "app", IsSealed: true, SealedVariants: []transpiler.SealedVariant{{Name: "Dup", Doc: "app"}}},
			"dep.Remote": {Name: "Remote", Package: "dep", IsSealed: true, SealedVariants: []transpiler.SealedVariant{{Name: "Dup", Doc: "dep"}}},
			"zzz.Other":  {Name: "Other", Package: "zzz", IsSealed: true, SealedVariants: []transpiler.SealedVariant{{Name: "Dup", Doc: "zzz"}}},
		},
	}
	for i := 0; i < 50; i++ {
		if _, parent := findSealedVariant(rich, "Dup", "app"); parent == nil || parent.Package != "app" {
			t.Fatalf("iteration %d: preferred package ignored, got %v", i, parent)
		}
		if _, parent := findSealedVariant(rich, "Dup", "dep"); parent == nil || parent.Package != "dep" {
			t.Fatalf("iteration %d: preferred package ignored, got %v", i, parent)
		}
		// With no match in the preferred package the fallback must still be
		// stable rather than whichever key the map yielded first.
		_, parent := findSealedVariant(rich, "Dup", "nosuch")
		if parent == nil || parent.Package != "app" {
			t.Fatalf("iteration %d: fallback not deterministic, got %v", i, parent)
		}
	}
}

// posCovers translates the analyzer's 1-based line / 0-based column onto the
// LSP's 0-based line, and must not claim a neighbouring identifier.
func TestPosCovers(t *testing.T) {
	pos := transpiler.SourcePos{Line: 10, Column: 5}
	cases := []struct {
		name       string
		line, char int
		want       bool
	}{
		{"start of identifier", 9, 5, true},
		{"inside identifier", 9, 7, true},
		{"end of identifier", 9, 9, true},
		{"before identifier", 9, 4, false},
		{"past identifier", 9, 10, false},
		{"wrong line", 10, 5, false},
	}
	for _, tc := range cases {
		if got := posCovers(pos, tc.line, tc.char, "abcd"); got != tc.want {
			t.Errorf("%s: posCovers = %v, want %v", tc.name, got, tc.want)
		}
	}
	if posCovers(transpiler.SourcePos{}, 0, 0, "x") {
		t.Error("an unset position must not match")
	}
}
