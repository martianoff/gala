package analyzer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

func newTestRichAST() *transpiler.RichAST {
	return &transpiler.RichAST{
		PackageName:      "myapp",
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
	}
}

func TestValidateRichAST_NilAST(t *testing.T) {
	warnings := analyzer.ValidateRichAST(nil)
	assert.Empty(t, warnings)
}

func TestValidateRichAST_ValidMetadata(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["User"] = &transpiler.TypeMetadata{
		Name:       "User",
		Package:    "myapp",
		Fields:     map[string]transpiler.Type{"Name": transpiler.BasicType{Name: "string"}, "Age": transpiler.BasicType{Name: "int"}},
		FieldNames: []string{"Name", "Age"},
		Methods: map[string]*transpiler.MethodMetadata{
			"GetName": {
				Name:       "GetName",
				ParamTypes: []transpiler.Type{},
				ReturnType: transpiler.BasicType{Name: "string"},
				DefinedIn:  "user.gala",
			},
		},
		DefinedIn: "user.gala",
	}
	ast.Functions["NewUser"] = &transpiler.FunctionMetadata{
		Name:       "NewUser",
		Package:    "myapp",
		ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}, transpiler.BasicType{Name: "int"}},
		ReturnType: transpiler.BasicType{Name: "User"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	// "User" is returned from NewUser but also exists in Types, so no warnings for it.
	// Builtin types (string, int) should not generate warnings.
	assert.Empty(t, warnings)
}

func TestValidateTypeReferences_MissingFieldType(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Order"] = &transpiler.TypeMetadata{
		Name:    "Order",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Item": transpiler.BasicType{Name: "UnknownType"},
		},
		FieldNames: []string{"Item"},
		DefinedIn:  "order.gala",
	}

	warnings := analyzer.ValidateRichAST(ast)
	require.NotEmpty(t, warnings)

	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" {
			assert.Contains(t, w.Message, "UnknownType")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for UnknownType")
}

func TestValidateTypeReferences_BuiltinTypes(t *testing.T) {
	ast := newTestRichAST()
	builtins := []string{"int", "string", "bool", "float64", "byte", "rune", "error", "any"}
	fields := make(map[string]transpiler.Type)
	var fieldNames []string
	for _, b := range builtins {
		fields[b+"Field"] = transpiler.BasicType{Name: b}
		fieldNames = append(fieldNames, b+"Field")
	}
	ast.Types["AllBuiltins"] = &transpiler.TypeMetadata{
		Name:       "AllBuiltins",
		Package:    "myapp",
		Fields:     fields,
		FieldNames: fieldNames,
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" {
			t.Errorf("unexpected type-reference warning for builtin: %s", w.Message)
		}
	}
}

func TestValidateTypeReferences_GenericType(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Container"] = &transpiler.TypeMetadata{
		Name:    "Container",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Items": transpiler.GenericType{
				Base:   transpiler.BasicType{Name: "UnknownGeneric"},
				Params: []transpiler.Type{transpiler.BasicType{Name: "string"}},
			},
		},
		FieldNames: []string{"Items"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" && w.Message != "" {
			assert.Contains(t, w.Message, "UnknownGeneric")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for UnknownGeneric")
}

func TestValidateTypeReferences_TypeParamPlaceholders(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Wrapper"] = &transpiler.TypeMetadata{
		Name:    "Wrapper",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Value": transpiler.BasicType{Name: "T"},
		},
		FieldNames: []string{"Value"},
		TypeParams: []string{"T"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" {
			t.Errorf("unexpected type-reference warning for type param T: %s", w.Message)
		}
	}
}

func TestValidateTypeReferences_PackageQualifiedUnknownPackage(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Handler"] = &transpiler.TypeMetadata{
		Name:    "Handler",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Req": transpiler.NamedType{Package: "unknownpkg", Name: "Request"},
		},
		FieldNames: []string{"Req"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" {
			assert.Contains(t, w.Message, "unknownpkg")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for unknown package")
}

func TestValidateTypeReferences_KnownPackageQualifiedType(t *testing.T) {
	ast := newTestRichAST()
	ast.Packages["martianoff/gala/std"] = "std"
	ast.Types["Handler"] = &transpiler.TypeMetadata{
		Name:    "Handler",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Result": transpiler.NamedType{Package: "std", Name: "Option"},
		},
		FieldNames: []string{"Result"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" {
			t.Errorf("unexpected type-reference warning for known package-qualified type: %s", w.Message)
		}
	}
}

func TestValidateTypeReferences_ArrayAndMapTypes(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Data"] = &transpiler.TypeMetadata{
		Name:    "Data",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Items":  transpiler.ArrayType{Elem: transpiler.BasicType{Name: "string"}},
			"Lookup": transpiler.MapType{Key: transpiler.BasicType{Name: "string"}, Elem: transpiler.BasicType{Name: "int"}},
		},
		FieldNames: []string{"Items", "Lookup"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" {
			t.Errorf("unexpected type-reference warning for array/map of builtins: %s", w.Message)
		}
	}
}

func TestValidateTypeReferences_FuncType(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Processor"] = &transpiler.TypeMetadata{
		Name:    "Processor",
		Package: "myapp",
		Methods: map[string]*transpiler.MethodMetadata{
			"Apply": {
				Name: "Apply",
				ParamTypes: []transpiler.Type{
					transpiler.FuncType{
						Params:  []transpiler.Type{transpiler.BasicType{Name: "string"}},
						Results: []transpiler.Type{transpiler.BasicType{Name: "MissingResult"}},
					},
				},
				ReturnType: transpiler.BasicType{Name: "int"},
				DefinedIn:  "proc.gala",
			},
		},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" && w.Message != "" {
			assert.Contains(t, w.Message, "MissingResult")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for MissingResult inside FuncType")
}

func TestValidateMethodUniqueness_ManyFiles(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Server"] = &transpiler.TypeMetadata{
		Name:    "Server",
		Package: "myapp",
		Methods: map[string]*transpiler.MethodMetadata{
			"Start":  {Name: "Start", DefinedIn: "server.gala"},
			"Stop":   {Name: "Stop", DefinedIn: "lifecycle.gala"},
			"Handle": {Name: "Handle", DefinedIn: "handlers.gala"},
		},
		DefinedIn: "server.gala",
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "method-uniqueness" {
			found = true
		}
	}
	assert.True(t, found, "expected a method-uniqueness warning for methods across 3 files")
}

func TestValidateMethodUniqueness_SameFile(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Simple"] = &transpiler.TypeMetadata{
		Name:    "Simple",
		Package: "myapp",
		Methods: map[string]*transpiler.MethodMetadata{
			"Foo": {Name: "Foo", DefinedIn: "simple.gala"},
			"Bar": {Name: "Bar", DefinedIn: "simple.gala"},
		},
		DefinedIn: "simple.gala",
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "method-uniqueness" {
			t.Errorf("unexpected method-uniqueness warning when all methods in same file: %s", w.Message)
		}
	}
}

func TestValidateDotImportAmbiguity_NoCollision(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Foo"] = &transpiler.TypeMetadata{Name: "Foo", Package: "myapp"}
	ast.GoExports = map[string][]string{
		"http": {"Server", "Client"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "dot-import-ambiguity" {
			t.Errorf("unexpected dot-import-ambiguity warning: %s", w.Message)
		}
	}
}

func TestValidateDotImportAmbiguity_Collision(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Handler"] = &transpiler.TypeMetadata{Name: "Handler", Package: "myapp"}
	ast.GoExports = map[string][]string{
		"http": {"Handler", "Server"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "dot-import-ambiguity" {
			assert.Contains(t, w.Message, "Handler")
			found = true
		}
	}
	assert.True(t, found, "expected a dot-import-ambiguity warning for Handler")
}

func TestValidateImportTypeConsistency_UnknownPackagePrefix(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["unknown.SomeType"] = &transpiler.TypeMetadata{
		Name:    "SomeType",
		Package: "unknown",
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "import-type-consistency" {
			assert.Contains(t, w.Message, "unknown")
			found = true
		}
	}
	assert.True(t, found, "expected an import-type-consistency warning for unknown package prefix")
}

func TestValidateImportTypeConsistency_KnownPackagePrefix(t *testing.T) {
	ast := newTestRichAST()
	ast.Packages["martianoff/gala/std"] = "std"
	ast.Types["std.Option"] = &transpiler.TypeMetadata{
		Name:    "Option",
		Package: "std",
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "import-type-consistency" && w.Message != "" {
			if w.Location == "std.Option" {
				t.Errorf("unexpected import-type-consistency warning for known package: %s", w.Message)
			}
		}
	}
}

func TestValidateImportTypeConsistency_FieldWithUnknownPackage(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["MyType"] = &transpiler.TypeMetadata{
		Name:    "MyType",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Conn": transpiler.NamedType{Package: "db", Name: "Connection"},
		},
		FieldNames: []string{"Conn"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "import-type-consistency" || w.Category == "type-reference" {
			if w.Message != "" {
				found = true
			}
		}
	}
	assert.True(t, found, "expected a warning for unknown package 'db' in field type")
}

func TestValidateRichAST_SealedVariantFieldTypes(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Result"] = &transpiler.TypeMetadata{
		Name:    "Result",
		Package: "myapp",
		IsSealed: true,
		SealedVariants: []transpiler.SealedVariant{
			{
				Name:       "Ok",
				FieldNames: []string{"value"},
				FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}},
			},
			{
				Name:       "Err",
				FieldNames: []string{"err"},
				FieldTypes: []transpiler.Type{transpiler.BasicType{Name: "MissingError"}},
			},
		},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" {
			assert.Contains(t, w.Message, "MissingError")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for MissingError in sealed variant")
}

func TestValidateRichAST_FunctionReturnUnknownType(t *testing.T) {
	ast := newTestRichAST()
	ast.Functions["GetWidget"] = &transpiler.FunctionMetadata{
		Name:       "GetWidget",
		Package:    "myapp",
		ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}},
		ReturnType: transpiler.BasicType{Name: "Widget"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" {
			assert.Contains(t, w.Message, "Widget")
			found = true
		}
	}
	assert.True(t, found, "expected a type-reference warning for Widget return type")
}

func TestValidateRichAST_TypeAlias(t *testing.T) {
	ast := newTestRichAST()
	ast.TypeAliases = map[string]transpiler.Type{
		"Handler": transpiler.FuncType{
			Params:  []transpiler.Type{transpiler.BasicType{Name: "string"}},
			Results: []transpiler.Type{transpiler.BasicType{Name: "error"}},
		},
	}
	ast.Types["Server"] = &transpiler.TypeMetadata{
		Name:    "Server",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"OnRequest": transpiler.BasicType{Name: "Handler"},
		},
		FieldNames: []string{"OnRequest"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" && w.Message != "" {
			t.Errorf("unexpected type-reference warning for type alias: %s", w.Message)
		}
	}
}

func TestValidateRichAST_DotImportedTypeResolution(t *testing.T) {
	ast := newTestRichAST()
	ast.Packages["martianoff/gala/std"] = "std"
	// Type registered with package prefix (as dot imports do)
	ast.Types["std.Option"] = &transpiler.TypeMetadata{Name: "Option", Package: "std"}
	// Field references bare name "Option" which should resolve via dot import
	ast.Types["MyType"] = &transpiler.TypeMetadata{
		Name:    "MyType",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Result": transpiler.BasicType{Name: "Option"},
		},
		FieldNames: []string{"Result"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "type-reference" && w.Message != "" {
			t.Errorf("unexpected type-reference warning for dot-imported type Option: %s", w.Message)
		}
	}
}

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name     string
		warnings []analyzer.ValidationWarning
		want     bool
	}{
		{
			name:     "no warnings",
			warnings: nil,
			want:     false,
		},
		{
			name: "only warnings",
			warnings: []analyzer.ValidationWarning{
				{Severity: analyzer.SeverityWarning, Message: "test"},
			},
			want: false,
		},
		{
			name: "has error",
			warnings: []analyzer.ValidationWarning{
				{Severity: analyzer.SeverityWarning, Message: "warning"},
				{Severity: analyzer.SeverityError, Message: "error"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, analyzer.HasErrors(tt.warnings))
		})
	}
}

func TestValidationWarning_String(t *testing.T) {
	w := analyzer.ValidationWarning{
		Severity: analyzer.SeverityWarning,
		Category: "type-reference",
		Message:  "type \"Foo\" not found",
		Location: "field Bar.Baz",
	}
	s := w.String()
	assert.Contains(t, s, "WARNING")
	assert.Contains(t, s, "field Bar.Baz")
	assert.Contains(t, s, "type \"Foo\" not found")
}

func TestValidateRichAST_PointerType(t *testing.T) {
	ast := newTestRichAST()
	ast.Types["Config"] = &transpiler.TypeMetadata{
		Name:    "Config",
		Package: "myapp",
		Fields: map[string]transpiler.Type{
			"Logger": transpiler.PointerType{Elem: transpiler.BasicType{Name: "MissingLogger"}},
		},
		FieldNames: []string{"Logger"},
	}

	warnings := analyzer.ValidateRichAST(ast)
	found := false
	for _, w := range warnings {
		if w.Category == "type-reference" {
			assert.Contains(t, w.Message, "MissingLogger")
			found = true
		}
	}
	assert.True(t, found, "expected type-reference warning for pointer to unknown type")
}

func TestValidateRichAST_CurrentPackagePrefix(t *testing.T) {
	ast := newTestRichAST()
	// Type with current package prefix should not trigger import-type-consistency warning
	ast.Types["myapp.Config"] = &transpiler.TypeMetadata{
		Name:    "Config",
		Package: "myapp",
	}

	warnings := analyzer.ValidateRichAST(ast)
	for _, w := range warnings {
		if w.Category == "import-type-consistency" && w.Location == "myapp.Config" {
			t.Errorf("unexpected import-type-consistency warning for current package prefix: %s", w.Message)
		}
	}
}
