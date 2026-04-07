package lsp

import (
	"regexp"
	"strings"
)

// goVarDeclRegex matches Go variable declarations:
// var name = expr or var name Type = expr
var goVarDeclRegex = regexp.MustCompile(`^\s*var\s+(\w+)\s*=\s*(.+)$`)

// extractTypeFromGoCode looks up a GALA variable's type by finding it in the
// generated Go source code. The transpiler generates fully-typed Go code,
// so we can extract concrete types that the text-based inferrer can't resolve.
func extractTypeFromGoCode(goCode, varName string) string {
	if goCode == "" || varName == "" {
		return ""
	}

	lines := strings.Split(goCode, "\n")
	for _, line := range lines {
		m := goVarDeclRegex.FindStringSubmatch(line)
		if m == nil || m[1] != varName {
			continue
		}
		rhs := strings.TrimSpace(m[2])
		return inferGoType(rhs)
	}
	return ""
}

// inferGoType extracts a readable type from a Go expression.
func inferGoType(expr string) string {
	// std.NewImmutable(Type{...}) → Type
	// std.NewImmutable(libalias.Wrapper[int]{...}) → Wrapper[int]
	if strings.Contains(expr, "NewImmutable(") {
		inner := extractInnerArg(expr, "NewImmutable(")
		if inner != "" {
			return inferGoType(inner)
		}
	}

	// Composite literal: Type{...} → Type
	if braceIdx := strings.Index(expr, "{"); braceIdx > 0 {
		typePart := strings.TrimSpace(expr[:braceIdx])
		return cleanGoTypeName(typePart)
	}

	// Function call: pkg.Func(args) — can't easily infer
	// But we can match known patterns
	if strings.HasPrefix(expr, "std.") {
		// std.Some[int]{}.Apply(42) → Option[int]
		if strings.Contains(expr, "Some") {
			return extractGenericFromStdCall(expr, "Some", "Option")
		}
		if strings.Contains(expr, "None") {
			return extractGenericFromStdCall(expr, "None", "Option")
		}
		if strings.Contains(expr, "Left") {
			return extractGenericFromStdCall(expr, "Left", "Either")
		}
		if strings.Contains(expr, "Right") {
			return extractGenericFromStdCall(expr, "Right", "Either")
		}
		if strings.Contains(expr, "Success") {
			return extractGenericFromStdCall(expr, "Success", "Try")
		}
		if strings.Contains(expr, "Failure") {
			return extractGenericFromStdCall(expr, "Failure", "Try")
		}
	}

	// String literal
	if strings.HasPrefix(expr, "\"") || strings.HasPrefix(expr, "fmt.Sprintf") {
		return "string"
	}

	return ""
}

// extractInnerArg extracts the argument from "FuncName(arg)"
func extractInnerArg(expr, funcPrefix string) string {
	idx := strings.Index(expr, funcPrefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(funcPrefix)
	depth := 1
	end := start
	for end < len(expr) && depth > 0 {
		if expr[end] == '(' {
			depth++
		} else if expr[end] == ')' {
			depth--
		}
		end++
	}
	if depth == 0 {
		return strings.TrimSpace(expr[start : end-1])
	}
	return ""
}

// extractGenericFromStdCall extracts type args from std.Some[int]{}.Apply(...)
func extractGenericFromStdCall(expr, caseName, parentType string) string {
	// Pattern: std.CaseName[TypeArgs]{}.Apply(...)
	caseIdx := strings.Index(expr, caseName)
	if caseIdx < 0 {
		return parentType
	}
	after := expr[caseIdx+len(caseName):]
	if strings.HasPrefix(after, "[") {
		// Extract [TypeArgs]
		end := strings.Index(after, "]")
		if end > 0 {
			typeArgs := after[:end+1]
			return parentType + cleanGoTypeName(typeArgs)
		}
	}
	return parentType
}

// cleanGoTypeName converts Go type names to GALA display names.
func cleanGoTypeName(name string) string {
	name = strings.TrimSpace(name)
	// Remove package prefixes: std.Option → Option, libalias.Wrapper → Wrapper
	if dotIdx := strings.LastIndex(name, "."); dotIdx >= 0 {
		// But preserve generic args
		bracketIdx := strings.Index(name, "[")
		if bracketIdx < 0 || dotIdx < bracketIdx {
			name = name[dotIdx+1:]
		}
	}
	// Clean std. inside generic args: [std.Immutable[int]] → [Immutable[int]]
	name = strings.ReplaceAll(name, "std.", "")
	return name
}
