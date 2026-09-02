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

// Completion documentation must belong to the item the user is looking at.
//
// findType and findFunction fall back to matching a simple name across every
// package, in Go map order, and the completion lists are built by a separate
// walk over the same maps. Resolving by name therefore answered with a
// different package's symbol whenever two packages export the same one — and
// this repo has several real collisions (json.Naming vs yaml.Naming,
// json.Codec vs yaml.Codec, collection_immutable.List vs collection_mutable.List).
func TestResolveRefDocIsExactAcrossPackages(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "app",
		Types: map[string]*transpiler.TypeMetadata{
			"json.Naming": {
				Name: "Naming", Package: "json", IsSealed: true,
				Doc: "Naming controls how json converts field names.",
				SealedVariants: []transpiler.SealedVariant{
					{Name: "SnakeCase", Doc: "SnakeCase is json's snake_case."},
				},
				Methods:   map[string]*transpiler.MethodMetadata{"Apply": {Name: "Apply", Doc: "json Apply."}},
				FieldDocs: map[string]string{"sep": "json separator."},
			},
			"yaml.Naming": {
				Name: "Naming", Package: "yaml", IsSealed: true,
				Doc: "Naming mirrors json.Naming for the yaml codec.",
				SealedVariants: []transpiler.SealedVariant{
					{Name: "SnakeCase", Doc: "SnakeCase is yaml's snake_case."},
				},
				Methods:   map[string]*transpiler.MethodMetadata{"Apply": {Name: "Apply", Doc: "yaml Apply."}},
				FieldDocs: map[string]string{"sep": "yaml separator."},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{
			"strings.Repeat": {Name: "Repeat", Package: "strings", Doc: "strings.Repeat repeats a string."},
			"stream.Repeat":  {Name: "Repeat", Package: "stream", Doc: "stream.Repeat repeats a stream."},
		},
	}

	cases := []struct {
		name string
		ref  completionRef
		want string
	}{
		{"type", completionRef{Kind: refKindType, Key: "yaml.Naming"}, "Naming mirrors json.Naming for the yaml codec."},
		{"func", completionRef{Kind: refKindFunc, Key: "stream.Repeat"}, "stream.Repeat repeats a stream."},
		{"method", completionRef{Kind: refKindMember, Key: "yaml.Naming", Name: "Apply"}, "yaml Apply."},
		{"field", completionRef{Kind: refKindMember, Key: "json.Naming", Name: "sep"}, "json separator."},
		{"variant", completionRef{Kind: refKindVariant, Key: "json.Naming", Name: "SnakeCase"}, "SnakeCase is json's snake_case."},
	}

	// Repeated, because the failure mode was a randomized map walk picking a
	// different package's symbol between one resolve and the next.
	for i := 0; i < 100; i++ {
		for _, tc := range cases {
			if got := resolveRefDoc(rich, tc.ref); got != tc.want {
				t.Fatalf("iteration %d, %s: resolved another package's doc\n got: %q\nwant: %q", i, tc.name, got, tc.want)
			}
		}
	}
}

// typeKey must reproduce the analyzer's map-key convention exactly, or every
// member lookup misses and documentation silently disappears.
func TestTypeKeyMatchesAnalyzerConvention(t *testing.T) {
	cases := []struct {
		pkg, name, want string
	}{
		{"json", "Naming", "json.Naming"},
		{"main", "Greeter", "Greeter"},
		{"test", "Fixture", "Fixture"},
		{"", "Bare", "Bare"},
	}
	for _, tc := range cases {
		got := typeKey(&transpiler.TypeMetadata{Name: tc.name, Package: tc.pkg})
		if got != tc.want {
			t.Errorf("typeKey(%q, %q) = %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if got := typeKey(nil); got != "" {
		t.Errorf("typeKey(nil) = %q, want empty", got)
	}
}
