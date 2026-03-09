package registry

// StdPackageInfo returns the PackageInfo for the standard library.
// This is the single source of truth for std package metadata.
func StdPackageInfo() PackageInfo {
	return PackageInfo{
		Name:       "std",
		ImportPath: "martianoff/gala/std",
		Types: []string{
			// Core types
			"Option",
			"Immutable",
			"Either",
			"Try",
			// Tuple types (Tuple is the 2-tuple, Tuple3+ are higher arities)
			"Tuple", "Tuple3", "Tuple4", "Tuple5", "Tuple6", "Tuple7", "Tuple8", "Tuple9", "Tuple10",
			// Collection traits
			"Traversable",
			"Iterable",
			// Companion objects also act as types
			"Some", "None", "Left", "Right", "Success", "Failure",
		},
		Functions: []string{
			"NewImmutable",
			"Copy",
			"Equal",
			// Companion constructors
			"Some", "None", "Left", "Right", "Success", "Failure",
			// Try conversion functions
			"FromOption", "FromEitherError",
		},
		Companions: []string{
			"Some", "None", "Left", "Right", "Success", "Failure",
		},
		IsPrelude: true,
	}
}

// DefaultRegistry returns a registry pre-configured with the standard library
// as a prelude package. This is the recommended way to get a registry instance.
func DefaultRegistry() *PackageRegistry {
	r := NewRegistry()
	r.RegisterPrelude(StdPackageInfo())
	return r
}

// Global is the default global registry instance.
// It is initialized with the standard library as a prelude package.
//
// For most use cases, use this global instance. Only create custom registries
// when you need isolation (e.g., in tests).
var Global = DefaultRegistry()

// Std package constants for backward compatibility.
// These mirror the values in StdPackageInfo() and should be used
// during the migration period. New code should use the registry.
const (
	StdPackageName = "std"
	StdImportPath  = "martianoff/gala/std"
)

// IsStdType checks if a type name is exported by the std package.
// This is a convenience function that uses the global registry.
func IsStdType(typeName string) bool {
	info, ok := Global.IsPreludeType(typeName)
	return ok && info.Name == StdPackageName
}

// IsStdFunction checks if a function name is exported by the std package.
// This is a convenience function that uses the global registry.
func IsStdFunction(funcName string) bool {
	info, ok := Global.IsPreludeFunction(funcName)
	return ok && info.Name == StdPackageName
}

// IsStdCompanion checks if a companion object name is exported by the std package.
// This is a convenience function that uses the global registry.
func IsStdCompanion(name string) bool {
	info, ok := Global.IsPreludeCompanion(name)
	return ok && info.Name == StdPackageName
}

// CheckStdConflict checks if a name conflicts with std library exports.
// This is a convenience function that uses the global registry.
func CheckStdConflict(name, currentPkg string) error {
	return Global.CheckConflict(name, currentPkg)
}

// ConstructorInferenceRule describes how to infer the parent generic type from a constructor call.
// For example, calling Some(x) produces Option[typeof(x)].
type ConstructorInferenceRule struct {
	ParentType   string // The parent sealed/generic type (e.g., "Option", "Either")
	InferFromArg int    // Which argument index to infer type param from (-1 = none)
}

// stdConstructorRules maps constructor function names to their inference rules.
// This covers both direct constructors (e.g., "Some") and _Apply variants (e.g., "Some_Apply").
var stdConstructorRules = map[string]ConstructorInferenceRule{
	// Option constructors
	"Some": {ParentType: "Option", InferFromArg: 0},
	"None": {ParentType: "Option", InferFromArg: -1},

	// Either constructors
	"Left":  {ParentType: "Either", InferFromArg: -1},
	"Right": {ParentType: "Either", InferFromArg: -1},

	// Try constructors
	"Success": {ParentType: "Try", InferFromArg: 0},
	"Failure": {ParentType: "Try", InferFromArg: -1},
}

// StdConstructorRule returns the inference rule for a std constructor function name, if any.
func StdConstructorRule(name string) (ConstructorInferenceRule, bool) {
	rule, ok := stdConstructorRules[name]
	return rule, ok
}

// StdParentTypeForPrefix checks if the given name starts with a known std type or constructor
// prefix followed by "_". Returns the parent type name and true if found.
// This handles patterns like "Option_Map", "Some_Apply", "Left_FlatMap" etc.
func StdParentTypeForPrefix(name string) (string, bool) {
	// Direct constructors and their parent types
	prefixes := map[string]string{
		"Option_":  "Option",
		"Some_":    "Option",
		"None_":    "Option",
		"Either_":  "Either",
		"Left_":    "Either",
		"Right_":   "Either",
		"Try_":     "Try",
		"Success_": "Try",
		"Failure_": "Try",
	}
	for prefix, parentType := range prefixes {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return parentType, true
		}
	}
	return "", false
}
