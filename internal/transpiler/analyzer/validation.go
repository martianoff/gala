package analyzer

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// ValidationSeverity indicates how serious a validation finding is.
type ValidationSeverity int

const (
	// SeverityWarning indicates a potential issue that may cause problems.
	SeverityWarning ValidationSeverity = iota
	// SeverityError indicates a definite metadata inconsistency.
	SeverityError
)

func (s ValidationSeverity) String() string {
	switch s {
	case SeverityWarning:
		return "WARNING"
	case SeverityError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ValidationWarning represents a single metadata validation finding.
type ValidationWarning struct {
	Severity ValidationSeverity
	Category string // e.g., "type-reference", "method-uniqueness", "dot-import-ambiguity", "import-type-consistency"
	Message  string
	Location string // file path or type/method name for context
}

func (w ValidationWarning) String() string {
	loc := ""
	if w.Location != "" {
		loc = " [" + w.Location + "]"
	}
	return fmt.Sprintf("GALA_VALIDATE %s%s: %s", w.Severity, loc, w.Message)
}

// ValidateRichAST performs read-only validation of a RichAST's metadata consistency.
// It returns any warnings or errors found. This function never modifies the RichAST.
func ValidateRichAST(ast *transpiler.RichAST) []ValidationWarning {
	if ast == nil {
		return nil
	}

	var warnings []ValidationWarning
	warnings = append(warnings, validateTypeReferences(ast)...)
	warnings = append(warnings, validateMethodUniqueness(ast)...)
	warnings = append(warnings, validateDotImportAmbiguity(ast)...)
	warnings = append(warnings, validateImportTypeConsistency(ast)...)
	return warnings
}

// validateTypeReferences checks that every type referenced in struct fields and method
// signatures exists in richAST.Types, is a builtin Go type, or has a valid import path.
func validateTypeReferences(ast *transpiler.RichAST) []ValidationWarning {
	var warnings []ValidationWarning

	for typeName, typeMeta := range ast.Types {
		// Check field types
		for fieldName, fieldType := range typeMeta.Fields {
			if w := checkTypeExists(ast, fieldType, fmt.Sprintf("field %s.%s", typeName, fieldName)); w != nil {
				warnings = append(warnings, *w)
			}
		}

		// Check method signatures
		for methodName, methodMeta := range typeMeta.Methods {
			loc := fmt.Sprintf("method %s.%s", typeName, methodName)
			for i, paramType := range methodMeta.ParamTypes {
				if w := checkTypeExists(ast, paramType, fmt.Sprintf("%s param[%d]", loc, i)); w != nil {
					warnings = append(warnings, *w)
				}
			}
			if methodMeta.ReturnType != nil && !methodMeta.ReturnType.IsNil() {
				if w := checkTypeExists(ast, methodMeta.ReturnType, fmt.Sprintf("%s return", loc)); w != nil {
					warnings = append(warnings, *w)
				}
			}
		}

		// Check sealed variant field types
		for _, variant := range typeMeta.SealedVariants {
			for i, ft := range variant.FieldTypes {
				fieldName := ""
				if i < len(variant.FieldNames) {
					fieldName = variant.FieldNames[i]
				}
				loc := fmt.Sprintf("sealed variant %s.%s field %s", typeName, variant.Name, fieldName)
				if w := checkTypeExists(ast, ft, loc); w != nil {
					warnings = append(warnings, *w)
				}
			}
		}
	}

	// Check function signatures
	for funcName, funcMeta := range ast.Functions {
		loc := fmt.Sprintf("function %s", funcName)
		for i, paramType := range funcMeta.ParamTypes {
			if w := checkTypeExists(ast, paramType, fmt.Sprintf("%s param[%d]", loc, i)); w != nil {
				warnings = append(warnings, *w)
			}
		}
		if funcMeta.ReturnType != nil && !funcMeta.ReturnType.IsNil() {
			if w := checkTypeExists(ast, funcMeta.ReturnType, fmt.Sprintf("%s return", loc)); w != nil {
				warnings = append(warnings, *w)
			}
		}
	}

	return warnings
}

// checkTypeExists verifies that a referenced type can be resolved.
// Returns nil if the type is valid, or a warning if it cannot be found.
func checkTypeExists(ast *transpiler.RichAST, t transpiler.Type, location string) *ValidationWarning {
	if t == nil || t.IsNil() || t.IsAny() {
		return nil
	}

	switch ty := t.(type) {
	case transpiler.BasicType:
		// Builtin Go types are always valid
		if transpiler.IsPrimitiveType(ty.Name) {
			return nil
		}
		// Type param placeholders (single uppercase letters or names matching type params) are valid
		if isSingleUppercase(ty.Name) {
			return nil
		}
		// Check if it's a known type in the RichAST
		if _, ok := ast.Types[ty.Name]; ok {
			return nil
		}
		// Check with all package prefixes from dot-imports
		for _, pkgName := range ast.Packages {
			if _, ok := ast.Types[pkgName+"."+ty.Name]; ok {
				return nil
			}
		}
		// Check type aliases
		if ast.TypeAliases != nil {
			if _, ok := ast.TypeAliases[ty.Name]; ok {
				return nil
			}
		}
		// Check GoTypeInfo
		if ast.GoTypeInfo != nil {
			if _, ok := ast.GoTypeInfo.Types[ty.Name]; ok {
				return nil
			}
		}
		// Not found anywhere - this is a warning (not error) because some types
		// may be resolved later via Go type inference or are type parameters.
		return &ValidationWarning{
			Severity: SeverityWarning,
			Category: "type-reference",
			Message:  fmt.Sprintf("type %q referenced in %s is not found in metadata", ty.Name, location),
			Location: location,
		}

	case transpiler.NamedType:
		if ty.Package == "" {
			// Same as BasicType check
			return checkTypeExists(ast, transpiler.BasicType{Name: ty.Name}, location)
		}
		// Package-qualified: check package exists
		if !packageKnown(ast, ty.Package) {
			return &ValidationWarning{
				Severity: SeverityWarning,
				Category: "type-reference",
				Message:  fmt.Sprintf("type %q references unknown package %q in %s", ty.String(), ty.Package, location),
				Location: location,
			}
		}
		return nil

	case transpiler.GenericType:
		// Check base type
		if w := checkTypeExists(ast, ty.Base, location); w != nil {
			return w
		}
		// Check type parameters
		for _, param := range ty.Params {
			if w := checkTypeExists(ast, param, location); w != nil {
				return w
			}
		}
		return nil

	case transpiler.ArrayType:
		return checkTypeExists(ast, ty.Elem, location)

	case transpiler.MapType:
		if w := checkTypeExists(ast, ty.Key, location); w != nil {
			return w
		}
		return checkTypeExists(ast, ty.Elem, location)

	case transpiler.PointerType:
		return checkTypeExists(ast, ty.Elem, location)

	case transpiler.FuncType:
		for _, p := range ty.Params {
			if w := checkTypeExists(ast, p, location); w != nil {
				return w
			}
		}
		for _, r := range ty.Results {
			if w := checkTypeExists(ast, r, location); w != nil {
				return w
			}
		}
		return nil

	case transpiler.VoidType, transpiler.NilType:
		return nil
	}

	return nil
}

// validateMethodUniqueness flags duplicate method definitions on the same type
// from different source files. Currently these are silently last-write-wins.
func validateMethodUniqueness(ast *transpiler.RichAST) []ValidationWarning {
	var warnings []ValidationWarning

	for typeName, typeMeta := range ast.Types {
		// Group methods by name and check DefinedIn
		// Since we only have the final state (not the merge history), we can only
		// detect duplicates if DefinedIn is set on methods AND the type has its own DefinedIn.
		// When a method's DefinedIn differs from the type's DefinedIn, that's expected
		// (methods defined in different files from the struct). But if two methods with
		// the SAME name exist, the map already de-duped them. We track this via DefinedIn.
		if typeMeta.Methods == nil {
			continue
		}

		// We can't detect duplicates from the final map alone (last-write-wins already happened).
		// However, we CAN flag a suspicious pattern: a type defined in file A having ALL its
		// methods from file B suggests the type's own methods may have been overwritten.
		typeFile := typeMeta.DefinedIn
		if typeFile == "" {
			continue
		}

		methodFiles := make(map[string][]string) // file -> method names
		for methodName, methodMeta := range typeMeta.Methods {
			file := methodMeta.DefinedIn
			if file == "" {
				file = "<unknown>"
			}
			methodFiles[file] = append(methodFiles[file], methodName)
		}

		// If methods come from 3+ different files, that's worth a warning
		if len(methodFiles) >= 3 {
			var fileList []string
			for f := range methodFiles {
				fileList = append(fileList, f)
			}
			warnings = append(warnings, ValidationWarning{
				Severity: SeverityWarning,
				Category: "method-uniqueness",
				Message:  fmt.Sprintf("type %q has methods defined across %d files: %s", typeName, len(methodFiles), strings.Join(fileList, ", ")),
				Location: typeName,
			})
		}
	}

	return warnings
}

// validateDotImportAmbiguity checks if any dot-imported packages have exported symbols
// whose names collide with each other or with the current package's symbols.
func validateDotImportAmbiguity(ast *transpiler.RichAST) []ValidationWarning {
	var warnings []ValidationWarning

	// Collect all symbol names from GoExports (Go-only packages with dot imports)
	// and from types/functions with package prefixes that match dot-imported packages.
	symbolSources := make(map[string][]string) // symbol name -> list of source packages

	// Collect symbols from GoExports
	if ast.GoExports != nil {
		for pkg, symbols := range ast.GoExports {
			for _, sym := range symbols {
				symbolSources[sym] = append(symbolSources[sym], "go:"+pkg)
			}
		}
	}

	// Collect type names from the current package (no package prefix)
	for typeName, typeMeta := range ast.Types {
		// Only consider types in the current package (no dot prefix)
		if !strings.Contains(typeName, ".") {
			pkg := typeMeta.Package
			if pkg == "" {
				pkg = ast.PackageName
			}
			symbolSources[typeName] = append(symbolSources[typeName], "gala:"+pkg)
		}
	}

	// Collect function names from the current package
	for funcName, funcMeta := range ast.Functions {
		if !strings.Contains(funcName, ".") {
			pkg := funcMeta.Package
			if pkg == "" {
				pkg = ast.PackageName
			}
			symbolSources[funcName] = append(symbolSources[funcName], "gala:"+pkg)
		}
	}

	// Find collisions: symbols that appear in 2+ different packages
	for sym, sources := range symbolSources {
		if len(sources) <= 1 {
			continue
		}
		// De-duplicate sources
		unique := make(map[string]bool)
		for _, s := range sources {
			unique[s] = true
		}
		if len(unique) > 1 {
			var pkgList []string
			for s := range unique {
				pkgList = append(pkgList, s)
			}
			warnings = append(warnings, ValidationWarning{
				Severity: SeverityWarning,
				Category: "dot-import-ambiguity",
				Message:  fmt.Sprintf("symbol %q is exported by multiple dot-imported packages: %s", sym, strings.Join(pkgList, ", ")),
				Location: sym,
			})
		}
	}

	return warnings
}

// validateImportTypeConsistency checks that every type with a package prefix has that
// package registered in imports or richAST.Packages.
func validateImportTypeConsistency(ast *transpiler.RichAST) []ValidationWarning {
	var warnings []ValidationWarning

	// Collect all package-qualified type names from the Types map
	for typeName := range ast.Types {
		if idx := strings.Index(typeName, "."); idx > 0 {
			pkg := typeName[:idx]
			if !packageKnown(ast, pkg) {
				warnings = append(warnings, ValidationWarning{
					Severity: SeverityWarning,
					Category: "import-type-consistency",
					Message:  fmt.Sprintf("type %q has package prefix %q which is not in imports", typeName, pkg),
					Location: typeName,
				})
			}
		}
	}

	// Check function package prefixes
	for funcName := range ast.Functions {
		if idx := strings.Index(funcName, "."); idx > 0 {
			pkg := funcName[:idx]
			if !packageKnown(ast, pkg) {
				warnings = append(warnings, ValidationWarning{
					Severity: SeverityWarning,
					Category: "import-type-consistency",
					Message:  fmt.Sprintf("function %q has package prefix %q which is not in imports", funcName, pkg),
					Location: funcName,
				})
			}
		}
	}

	// Check field types and method signatures for package-prefixed types
	for typeName, typeMeta := range ast.Types {
		for fieldName, fieldType := range typeMeta.Fields {
			checkPackagePrefixInType(ast, fieldType, fmt.Sprintf("field %s.%s", typeName, fieldName), &warnings)
		}
		for methodName, methodMeta := range typeMeta.Methods {
			for _, pt := range methodMeta.ParamTypes {
				checkPackagePrefixInType(ast, pt, fmt.Sprintf("method %s.%s", typeName, methodName), &warnings)
			}
			if methodMeta.ReturnType != nil {
				checkPackagePrefixInType(ast, methodMeta.ReturnType, fmt.Sprintf("method %s.%s return", typeName, methodName), &warnings)
			}
		}
	}

	return warnings
}

// checkPackagePrefixInType recursively checks if any NamedType in a type tree has an
// unknown package prefix.
func checkPackagePrefixInType(ast *transpiler.RichAST, t transpiler.Type, location string, warnings *[]ValidationWarning) {
	if t == nil || t.IsNil() {
		return
	}
	switch ty := t.(type) {
	case transpiler.NamedType:
		if ty.Package != "" && !packageKnown(ast, ty.Package) {
			*warnings = append(*warnings, ValidationWarning{
				Severity: SeverityWarning,
				Category: "import-type-consistency",
				Message:  fmt.Sprintf("type %q in %s references unknown package %q", ty.String(), location, ty.Package),
				Location: location,
			})
		}
	case transpiler.GenericType:
		checkPackagePrefixInType(ast, ty.Base, location, warnings)
		for _, p := range ty.Params {
			checkPackagePrefixInType(ast, p, location, warnings)
		}
	case transpiler.ArrayType:
		checkPackagePrefixInType(ast, ty.Elem, location, warnings)
	case transpiler.MapType:
		checkPackagePrefixInType(ast, ty.Key, location, warnings)
		checkPackagePrefixInType(ast, ty.Elem, location, warnings)
	case transpiler.PointerType:
		checkPackagePrefixInType(ast, ty.Elem, location, warnings)
	case transpiler.FuncType:
		for _, p := range ty.Params {
			checkPackagePrefixInType(ast, p, location, warnings)
		}
		for _, r := range ty.Results {
			checkPackagePrefixInType(ast, r, location, warnings)
		}
	}
}

// packageKnown checks if a package name is known via imports or the current package.
func packageKnown(ast *transpiler.RichAST, pkg string) bool {
	if pkg == ast.PackageName {
		return true
	}
	// Check if any import path maps to this package name
	for _, pkgName := range ast.Packages {
		if pkgName == pkg {
			return true
		}
	}
	// Check GoExports
	if ast.GoExports != nil {
		if _, ok := ast.GoExports[pkg]; ok {
			return true
		}
	}
	// Check GoTypeInfo for known packages
	if ast.GoTypeInfo != nil {
		for typeName := range ast.GoTypeInfo.Types {
			if idx := strings.Index(typeName, "."); idx > 0 {
				if typeName[:idx] == pkg {
					return true
				}
			}
		}
	}
	return false
}

// isSingleUppercase returns true if the name looks like a type parameter (e.g., "T", "K", "V").
func isSingleUppercase(name string) bool {
	if len(name) == 0 {
		return false
	}
	// Single uppercase letter
	if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
		return true
	}
	// Common multi-char type param names
	switch name {
	case "TKey", "TValue", "TResult", "TAcc":
		return true
	}
	return false
}

// HasErrors returns true if any of the warnings have Error severity.
func HasErrors(warnings []ValidationWarning) bool {
	for _, w := range warnings {
		if w.Severity == SeverityError {
			return true
		}
	}
	return false
}
