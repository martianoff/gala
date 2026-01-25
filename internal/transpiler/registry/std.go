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
			// Slice helper functions
			"SliceOf", "SliceEmpty", "SliceWithCapacity", "SliceWithSize", "SliceWithSizeAndCapacity",
			"SliceCopy", "SliceAppendAll", "SlicePrepend", "SlicePrependAll", "SliceInsert",
			"SliceRemoveAt", "SliceDrop", "SliceTake",
			// Map helper functions
			"MapEmpty", "MapWithCapacity", "MapPut", "MapDelete", "MapGet",
			"MapContains", "MapLen", "MapForEach", "MapKeys", "MapValues", "MapCopy",
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
