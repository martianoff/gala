package lsp

import (
	"strings"

	"martianoff/gala/internal/transpiler"
)

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

// typeDisplayName renders a declared type as the bare name the resolvers key
// on, or "" when the declaration has none.
func typeDisplayName(t transpiler.Type) string {
	if !transpiler.HasResult(t) {
		return ""
	}
	return stripTypeParams(cleanGoTypeForDisplay(t.String()))
}
