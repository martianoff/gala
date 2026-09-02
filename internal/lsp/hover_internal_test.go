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

// A sealed case declaration matches two metadata entries at the same position:
// the case itself, and the companion type the analyzer generates for it, whose
// only methods are Apply and Unapply. Resolution must be stable and must always
// pick the case — interleaved with the type-name check, Go's map order decided
// it and roughly one hover in ten showed the generated plumbing.
func TestDeclarationAtPrefersCaseOverGeneratedCompanion(t *testing.T) {
	const path = "/proj/shapes.gala"
	rich := &transpiler.RichAST{
		PackageName: "main",
		Types: map[string]*transpiler.TypeMetadata{
			"Shape": {
				Name: "Shape", Package: "main", DefinedIn: path, IsSealed: true,
				Pos: transpiler.SourcePos{Line: 2, Column: 12},
				SealedVariants: []transpiler.SealedVariant{
					{Name: "Circle", Doc: "Circle is round.", Pos: transpiler.SourcePos{Line: 3, Column: 9}},
				},
			},
			// The generated companion: same name, same position as the case.
			"Circle": {
				Name: "Circle", Package: "main", DefinedIn: path,
				Pos: transpiler.SourcePos{Line: 3, Column: 9},
				Methods: map[string]*transpiler.MethodMetadata{
					"Apply":   {Name: "Apply"},
					"Unapply": {Name: "Unapply"},
				},
				Fields: map[string]transpiler.Type{},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	for i := 0; i < 200; i++ {
		got := declarationAt(rich, path, 2, 10, "Circle")
		if !strings.Contains(got, "case Circle") {
			t.Fatalf("iteration %d: case declaration resolved to the companion type\n--- got ---\n%s", i, got)
		}
		if strings.Contains(got, "Unapply") {
			t.Fatalf("iteration %d: hover leaked generated plumbing\n--- got ---\n%s", i, got)
		}
	}
}

// A method may be declared in a different file from the type it extends. Gating
// the method search on the TYPE's file skipped it, after which a bare-name
// fallback could answer with an unrelated same-named type.
func TestDeclarationAtFindsMethodDeclaredInAnotherFile(t *testing.T) {
	const typePath, methodPath = "/proj/widget.gala", "/proj/fluent.gala"
	rich := &transpiler.RichAST{
		PackageName: "main",
		Types: map[string]*transpiler.TypeMetadata{
			"Widget": {
				Name: "Widget", Package: "main", DefinedIn: typePath,
				Pos: transpiler.SourcePos{Line: 1, Column: 5},
				Methods: map[string]*transpiler.MethodMetadata{
					"AsFixed": {
						Name: "AsFixed", Package: "main", DefinedIn: methodPath,
						Doc: "AsFixed pins the widget.",
						Pos: transpiler.SourcePos{Line: 4, Column: 20},
					},
				},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	got := declarationAt(rich, methodPath, 3, 21, "AsFixed")
	if !strings.Contains(got, "AsFixed pins the widget.") {
		t.Errorf("method declared in another file than its type did not resolve\n--- got ---\n%s", got)
	}
}
