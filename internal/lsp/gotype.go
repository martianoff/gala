package lsp

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
)

// extractTypesFromGoCode parses and type-checks the generated Go source,
// returning a map of variable name → type string for every declaration.
// Uses go/types for exact type resolution — no pattern matching needed.
func extractTypesFromGoCode(goCode string) map[string]string {
	result := make(map[string]string)
	if goCode == "" {
		return result
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "gen.go", goCode, 0)
	if err != nil {
		return result
	}

	// Type-check the Go source
	conf := types.Config{
		Importer: importer.Default(),
		Error:    func(err error) {}, // Ignore type errors — we want best-effort
	}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	conf.Check("main", fset, []*ast.File{f}, info)

	// Extract variable types from Defs
	for ident, obj := range info.Defs {
		if obj == nil {
			continue
		}
		v, ok := obj.(*types.Var)
		if !ok {
			continue
		}
		typStr := cleanGoTypeForDisplay(v.Type().String())
		if typStr != "" {
			result[ident.Name] = typStr
		}
	}

	return result
}

// cleanGoTypeForDisplay converts Go type strings to GALA display names.
// e.g., "martianoff/gala/std.Immutable[martianoff/gala/std.Option[int]]" → "Option[int]"
func cleanGoTypeForDisplay(typeStr string) string {
	// Remove full import paths: martianoff/gala/std.Option → Option
	// Keep the type name after the last /pkg.
	result := typeStr

	// Remove import paths segment by segment
	for strings.Contains(result, "/") {
		// Find a pattern like "path/to/pkg.Type" and replace with "Type"
		idx := strings.Index(result, "/")
		if idx < 0 {
			break
		}
		// Walk backwards to find the start of this import path
		start := idx
		for start > 0 && result[start-1] != '[' && result[start-1] != ',' && result[start-1] != '(' && result[start-1] != ' ' {
			start--
		}
		// Walk forwards to find the dot after the package name
		dotIdx := strings.Index(result[idx:], ".")
		if dotIdx < 0 {
			break
		}
		dotIdx += idx
		// Replace "full/path/pkg.Type" with "Type"
		result = result[:start] + result[dotIdx+1:]
	}

	// Remove "std." prefix if still present
	result = strings.ReplaceAll(result, "std.", "")

	// Unwrap Immutable[T] → T (GALA transparently wraps vals)
	for strings.HasPrefix(result, "Immutable[") && strings.HasSuffix(result, "]") {
		result = result[10 : len(result)-1]
	}

	return result
}
