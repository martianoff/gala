package lsp

import "strings"

// stripTypeParams removes generic type parameters: "Option[int]" → "Option".
func stripTypeParams(s string) string {
	if idx := strings.Index(s, "["); idx > 0 {
		return s[:idx]
	}
	return s
}

// cleanGoTypeForDisplay converts GALA transpiler Type.String() to a display name.
// Removes package prefixes and unwraps Immutable[T] → T.
func cleanGoTypeForDisplay(typeStr string) string {
	result := typeStr

	// Remove "std." prefix
	result = strings.ReplaceAll(result, "std.", "")

	// Unwrap Immutable[T] → T
	for strings.HasPrefix(result, "Immutable[") && strings.HasSuffix(result, "]") {
		result = result[10 : len(result)-1]
	}

	return result
}
