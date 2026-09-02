package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// richWithDocs is a package whose declarations all carry doc comments.
func richWithDocs() *transpiler.RichAST {
	return &transpiler.RichAST{
		PackageName: "shapes",
		PackageDoc:  "Package shapes draws things.",
		Types: map[string]*transpiler.TypeMetadata{
			"shapes.Box": {
				Name:       "Box",
				Package:    "shapes",
				Doc:        "Box holds a width and a height.",
				FieldNames: []string{"Width", "Height"},
				Fields: map[string]transpiler.Type{
					"Width":  transpiler.BasicType{Name: "int"},
					"Height": transpiler.BasicType{Name: "int"},
				},
				FieldDocs: map[string]string{"Width": "Width is the horizontal extent."},
				Methods: map[string]*transpiler.MethodMetadata{
					"Area": {
						Name:       "Area",
						Doc:        "Area returns width times height.",
						ReturnType: transpiler.BasicType{Name: "int"},
					},
				},
			},
			"shapes.Shape": {
				Name:     "Shape",
				Package:  "shapes",
				Doc:      "Shape is a closed set of drawable things.",
				IsSealed: true,
				SealedVariants: []transpiler.SealedVariant{
					{Name: "Circle", Doc: "Circle is round."},
					{Name: "Square"},
				},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{
			"shapes.NewBox": {
				Name:    "NewBox",
				Package: "shapes",
				Doc:     "NewBox builds a Box.",
			},
		},
	}
}

func TestDocCarriesProse(t *testing.T) {
	pkg := collectPackageDoc("shapes", richWithDocs())

	if pkg.Doc != "Package shapes draws things." {
		t.Errorf("package doc = %q", pkg.Doc)
	}

	var box, shape *docType
	for i := range pkg.Types {
		switch pkg.Types[i].Name {
		case "Box":
			box = &pkg.Types[i]
		case "Shape":
			shape = &pkg.Types[i]
		}
	}
	if box == nil || shape == nil {
		t.Fatalf("types missing: %+v", pkg.Types)
	}

	if box.Doc != "Box holds a width and a height." {
		t.Errorf("Box.Doc = %q", box.Doc)
	}
	if len(box.Fields) == 0 || box.Fields[0].Doc != "Width is the horizontal extent." {
		t.Errorf("field docs missing: %+v", box.Fields)
	}
	if len(box.Methods) == 0 || box.Methods[0].Doc != "Area returns width times height." {
		t.Errorf("method doc missing: %+v", box.Methods)
	}
	if shape.Doc != "Shape is a closed set of drawable things." {
		t.Errorf("Shape.Doc = %q", shape.Doc)
	}
	if len(shape.Variants) == 0 || shape.Variants[0].Doc != "Circle is round." {
		t.Errorf("variant doc missing: %+v", shape.Variants)
	}
	if len(pkg.Functions) == 0 || pkg.Functions[0].Doc != "NewBox builds a Box." {
		t.Errorf("function doc missing: %+v", pkg.Functions)
	}
}

// The JSON form is a machine-readable contract; documentation has to be in it.
func TestDocJSONCarriesProse(t *testing.T) {
	pkg := collectPackageDoc("shapes", richWithDocs())
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Package shapes draws things.",
		"Box holds a width and a height.",
		"Area returns width times height.",
		"Width is the horizontal extent.",
		"Circle is round.",
		"NewBox builds a Box.",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %q", want)
		}
	}
}

// An undocumented declaration must not gain an empty "doc" key, and the text
// rendering must not print a blank line where prose would be.
func TestDocOmitsEmptyProse(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "bare",
		Types: map[string]*transpiler.TypeMetadata{
			"bare.Thing": {Name: "Thing", Package: "bare"},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}
	pkg := collectPackageDoc("bare", rich)
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"doc"`) {
		t.Errorf("undocumented package emitted a doc key: %s", raw)
	}
}

// The transpiler generates a companion type per sealed case, carrying Apply and
// Unapply and registered under the case's own name. It is lowering detail, not
// something the user wrote, and must not appear in the documented API.
func TestDocHidesGeneratedCaseCompanions(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "shapes",
		Types: map[string]*transpiler.TypeMetadata{
			"shapes.Shape": {
				Name: "Shape", Package: "shapes", Doc: "Shape is drawable.", IsSealed: true,
				FieldNames: []string{"Radius", "Side"},
				Fields: map[string]transpiler.Type{
					"Radius": transpiler.BasicType{Name: "float64"},
					"Side":   transpiler.BasicType{Name: "float64"},
				},
				SealedVariants: []transpiler.SealedVariant{
					{
						Name: "Circle", Doc: "Circle is round.",
						FieldNames: []string{"Radius"},
						FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "float64"}},
					},
					{
						Name:       "Square",
						FieldNames: []string{"Side"},
						FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "float64"}},
					},
				},
			},
			// The generated companions.
			"shapes.Circle": {
				Name: "Circle", Package: "shapes",
				Methods: map[string]*transpiler.MethodMetadata{
					"Apply": {Name: "Apply"}, "Unapply": {Name: "Unapply"},
				},
			},
			"shapes.Square": {
				Name: "Square", Package: "shapes",
				Methods: map[string]*transpiler.MethodMetadata{
					"Apply": {Name: "Apply"}, "Unapply": {Name: "Unapply"},
				},
			},
			// A genuine type must survive the filter.
			"shapes.Canvas": {Name: "Canvas", Package: "shapes", Doc: "Canvas is real."},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	pkg := collectPackageDoc("shapes", rich)

	names := make([]string, 0, len(pkg.Types))
	for _, dt := range pkg.Types {
		names = append(names, dt.Name)
	}
	for _, leaked := range []string{"Circle", "Square"} {
		for _, got := range names {
			if got == leaked {
				t.Errorf("generated companion %q documented as a type; types = %v", leaked, names)
			}
		}
	}
	var canvas, shape *docType
	for i := range pkg.Types {
		switch pkg.Types[i].Name {
		case "Canvas":
			canvas = &pkg.Types[i]
		case "Shape":
			shape = &pkg.Types[i]
		}
	}
	if canvas == nil {
		t.Fatalf("a real type was filtered out; types = %v", names)
	}
	if shape == nil {
		t.Fatal("the sealed parent went missing")
	}

	// A case carries its own fields, so the parent must not also list them as
	// its own — they are the merged representation, not Shape's API.
	for _, f := range shape.Fields {
		if f.Name == "Radius" || f.Name == "Side" {
			t.Errorf("variant field %q listed on the sealed parent; fields = %+v", f.Name, shape.Fields)
		}
	}
	if len(shape.Variants) != 2 || shape.Variants[0].Params == nil {
		t.Fatalf("variants lack their fields: %+v", shape.Variants)
	}
	if shape.Variants[0].Params[0].Name != "Radius" || shape.Variants[0].Params[0].Type != "float64" {
		t.Errorf("case fields wrong: %+v", shape.Variants[0].Params)
	}
}

// A sealed case is not a type a user can name — its companion is filtered as
// lowering detail — but asking about one is reasonable, and the answer is the
// type that declares it.
func TestDocNarrowsCaseToItsSealedType(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "shapes",
		Types: map[string]*transpiler.TypeMetadata{
			"shapes.Shape": {
				Name: "Shape", Package: "shapes", Doc: "Shape is drawable.", IsSealed: true,
				SealedVariants: []transpiler.SealedVariant{{Name: "Circle", Doc: "Circle is round."}},
			},
			"shapes.Circle": {Name: "Circle", Package: "shapes"},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}
	pkg := collectPackageDoc("shapes", rich)

	narrowed, ok := narrowToType(pkg, "Circle")
	if !ok {
		t.Fatal("asking about a sealed case reported no such type")
	}
	if len(narrowed.Types) != 1 || narrowed.Types[0].Name != "Shape" {
		t.Fatalf("case did not resolve to its declaring type: %+v", narrowed.Types)
	}

	// A name that is neither a type nor a case still reports nothing.
	if _, ok := narrowToType(pkg, "Nonexistent"); ok {
		t.Error("an unknown name resolved to something")
	}
	// The declaring type still resolves by its own name.
	if got, ok := narrowToType(pkg, "Shape"); !ok || got.Types[0].Name != "Shape" {
		t.Error("the sealed type no longer resolves by its own name")
	}
}

// The text rendering is the default, human-facing output. It previously
// dropped the package's own documentation and every function's — collected,
// present in --json, and invisible where people actually read it.
func TestRenderPackageDocPrintsAllProse(t *testing.T) {
	var buf bytes.Buffer
	renderPackageDoc(&buf, collectPackageDoc("shapes", richWithDocs()))
	got := buf.String()

	for _, want := range []string{
		"Package shapes draws things.",     // the package's own doc
		"Box holds a width and a height.",  // a type
		"Width is the horizontal extent.",  // a field
		"Area returns width times height.", // a method
		"Circle is round.",                 // a sealed case
		"NewBox builds a Box.",             // a function
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// An undocumented declaration must not push a blank line into the output, and
// neither must a doc comment that ends in a bare `//`.
func TestRenderProseTrimsAndOmits(t *testing.T) {
	var buf bytes.Buffer
	renderProse(&buf, "", "    ")
	if buf.String() != "" {
		t.Errorf("an absent doc produced output: %q", buf.String())
	}

	buf.Reset()
	renderProse(&buf, "\nCircle is round.\n", "    ")
	if got, want := buf.String(), "    Circle is round.\n"; got != want {
		t.Errorf("surrounding blank lines were not trimmed\n got: %q\nwant: %q", got, want)
	}

	buf.Reset()
	renderProse(&buf, "First.\n\nSecond.", "  ")
	if got, want := buf.String(), "  First.\n\n  Second.\n"; got != want {
		t.Errorf("paragraph break not preserved\n got: %q\nwant: %q", got, want)
	}
}

// When two cases declare the same field name with different types, the analyzer
// prefixes the merged struct field with the case name. Those synthesised names
// must not surface as fields of the sealed type.
func TestDocHidesMergedSealedFields(t *testing.T) {
	rich := &transpiler.RichAST{
		PackageName: "expr",
		Types: map[string]*transpiler.TypeMetadata{
			"expr.Expr": {
				Name: "Expr", Package: "expr", IsSealed: true,
				// What the analyzer actually produces for
				//   case Add(Left int, Right int) / case Scale(Left float64)
				FieldNames: []string{"AddLeft", "Right", "ScaleLeft", "_variant"},
				Fields: map[string]transpiler.Type{
					"AddLeft":   transpiler.BasicType{Name: "int"},
					"Right":     transpiler.BasicType{Name: "int"},
					"ScaleLeft": transpiler.BasicType{Name: "float64"},
					"_variant":  transpiler.BasicType{Name: "uint8"},
				},
				SealedVariants: []transpiler.SealedVariant{
					{
						Name:       "Add",
						FieldNames: []string{"Left", "Right"},
						FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}, transpiler.BasicType{Name: "int"}},
					},
					{
						Name:       "Scale",
						FieldNames: []string{"Left"},
						FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "float64"}},
					},
				},
			},
		},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}

	pkg := collectPackageDoc("expr", rich)
	if len(pkg.Types) != 1 {
		t.Fatalf("types = %+v", pkg.Types)
	}
	if len(pkg.Types[0].Fields) != 0 {
		t.Errorf("a sealed type reported fields of its own: %+v", pkg.Types[0].Fields)
	}

	var buf bytes.Buffer
	renderPackageDoc(&buf, pkg)
	got := buf.String()
	for _, synthesised := range []string{"AddLeft", "ScaleLeft", "_variant"} {
		if strings.Contains(got, synthesised) {
			t.Errorf("synthesised field %q reached the output\n--- got ---\n%s", synthesised, got)
		}
	}
	if !strings.Contains(got, "case Add(Left int, Right int)") {
		t.Errorf("case fields not rendered\n--- got ---\n%s", got)
	}
}
