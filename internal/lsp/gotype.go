package lsp

import (
	"regexp"
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

// wildcardTypeParam matches the name the transpiler gives a type parameter a
// receiver writes as `_` (see transpiler.IsWildcardTypeParam).
var wildcardTypeParam = regexp.MustCompile(`\b` + transpiler.WildcardTypeParamPrefix + `\w*`)

// cleanGoTypeForDisplay converts GALA transpiler Type.String() to a display name.
// Removes package prefixes, unwraps Immutable[T] → T, and spells a type
// parameter a receiver writes as `_` as `_`.
func cleanGoTypeForDisplay(typeStr string) string {
	result := typeStr

	// Remove "std." prefix
	result = strings.ReplaceAll(result, "std.", "")
	if strings.Contains(result, transpiler.WildcardTypeParamPrefix) {
		result = wildcardTypeParam.ReplaceAllString(result, "_")
	}

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
