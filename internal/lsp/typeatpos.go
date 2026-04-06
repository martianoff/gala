// Package lsp provides type-at-position resolution for the GALA LSP server.
//
// This resolves the type of an expression at a given cursor position by:
// 1. Scanning the source text for local variable declarations (val/var)
// 2. Building a scope-aware map of variable -> type
// 3. Resolving method chain return types through RichAST metadata
package lsp

import (
	"regexp"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// typeAtDot resolves the type of the expression before a dot at the given position.
// For example, in "myList.Map(...).Filter(|)", it resolves the type after ".Map(...)".
func typeAtDot(text string, line, char int, richAST *transpiler.RichAST) string {
	if richAST == nil {
		return ""
	}

	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}

	// Extract the full expression chain before the dot
	chain := extractExprChain(lines[line], char)
	if len(chain) == 0 {
		return ""
	}

	// Resolve the type of the first element (variable or constructor)
	currentType := resolveBaseType(chain[0], text, line, richAST)
	if currentType == "" {
		return ""
	}

	// Walk the chain, resolving each method/field access
	for i := 1; i < len(chain); i++ {
		segment := chain[i]
		currentType = resolveChainSegment(segment, currentType, richAST)
		if currentType == "" {
			return ""
		}
	}

	return currentType
}

// extractExprChain splits "receiver.method1(...).method2(...)" into segments.
// Returns ["receiver", "method1(...)", "method2(...)"] or just ["receiver"].
func extractExprChain(lineText string, charPos int) []string {
	// Walk backwards from cursor to find the dot
	i := charPos - 1
	// Skip any partial identifier being typed
	for i >= 0 && isIdentChar(lineText[i]) {
		i--
	}

	// Find the start of the expression chain, handling nested parens/brackets
	end := i // position of the last dot or end of chain
	if end < 0 || lineText[end] != '.' {
		return nil
	}

	// Now collect the full chain from left to right
	// Walk backwards to find the start
	start := end - 1
	depth := 0
	for start >= 0 {
		ch := lineText[start]
		if ch == ')' || ch == ']' {
			depth++
		} else if ch == '(' || ch == '[' {
			depth--
			if depth < 0 {
				break
			}
		} else if ch == '.' && depth == 0 {
			// Continue backwards past the dot
		} else if depth == 0 && !isIdentChar(ch) && ch != '.' {
			break
		}
		start--
	}
	start++ // move past the non-expression character

	expr := strings.TrimSpace(lineText[start:end])
	if expr == "" {
		return nil
	}

	// Split on dots, respecting parentheses
	return splitChain(expr)
}

// splitChain splits "a.b(x).c" into ["a", "b(x)", "c"]
func splitChain(expr string) []string {
	var segments []string
	depth := 0
	segStart := 0

	for i, ch := range expr {
		switch ch {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '.':
			if depth == 0 {
				seg := strings.TrimSpace(expr[segStart:i])
				if seg != "" {
					segments = append(segments, seg)
				}
				segStart = i + 1
			}
		}
	}
	// Last segment
	seg := strings.TrimSpace(expr[segStart:])
	if seg != "" {
		segments = append(segments, seg)
	}

	return segments
}

var (
	valVarDeclRe = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s+(\w[\w\[\], ]*)?\s*=\s*(.+)$`)
	valNotypeDeclRe = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s*=\s*(.+)$`)
	paramRe      = regexp.MustCompile(`(\w+)\s+(\w[\w\[\]]*(?:\[[\w, ]+\])?)`)
)

// resolveBaseType determines the type of the first element in a chain.
func resolveBaseType(expr, text string, currentLine int, richAST *transpiler.RichAST) string {
	// Strip method call if present: "Some(42)" -> resolve as constructor
	name := expr
	if idx := strings.Index(expr, "("); idx > 0 {
		name = expr[:idx]
	}
	if idx := strings.Index(name, "["); idx > 0 {
		name = name[:idx]
	}

	// Check if it's a constructor call (starts with uppercase)
	if isExported(name) {
		return resolveConstructorReturnType(name, richAST)
	}

	// It's a variable — scan declarations backwards from current line
	lines := strings.Split(text, "\n")

	// 1. Check local val/var declarations (backwards from cursor)
	for i := currentLine; i >= 0; i-- {
		line := lines[i]

		// val name Type = ...
		if m := valVarDeclRe.FindStringSubmatch(line); m != nil {
			if m[2] == name && m[3] != "" {
				typeName := strings.TrimSpace(m[3])
				if idx := strings.Index(typeName, "["); idx > 0 {
					typeName = typeName[:idx]
				}
				return typeName
			}
		}

		// val name = Expr (infer from RHS)
		if m := valNotypeDeclRe.FindStringSubmatch(line); m != nil {
			if m[2] == name {
				rhs := strings.TrimSpace(m[3])
				return inferRHSType(rhs, richAST)
			}
		}
	}

	// 2. Check function parameters
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "func ") {
			continue
		}
		// Find params
		start := strings.Index(trimmed, "(")
		if start < 0 {
			continue
		}
		end := strings.Index(trimmed[start:], ")")
		if end < 0 {
			continue
		}
		params := trimmed[start+1 : start+end]
		for _, m := range paramRe.FindAllStringSubmatch(params, -1) {
			if m[1] == name {
				typeName := m[2]
				if idx := strings.Index(typeName, "["); idx > 0 {
					typeName = typeName[:idx]
				}
				return typeName
			}
		}
	}

	// 3. Check for-range loop variables
	for i := currentLine; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "for ") && strings.Contains(line, "range") {
			// for x range collection { ... }
			// for i, x := range collection { ... }
			// Extract the range variable and try to infer collection element type
			if strings.Contains(line, name) {
				return "" // TODO: infer element type from collection
			}
		}
	}

	return ""
}

// findType looks up a type in the RichAST by simple name.
func findType(richAST *transpiler.RichAST, name string) *transpiler.TypeMetadata {
	if tm, ok := richAST.Types[name]; ok {
		return tm
	}
	for key, tm := range richAST.Types {
		typeName := tm.Name
		if typeName == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				typeName = key[idx+1:]
			}
		}
		if typeName == name {
			return tm
		}
	}
	return nil
}

// resolveConstructorReturnType determines the type returned by a constructor.
func resolveConstructorReturnType(name string, richAST *transpiler.RichAST) string {
	// Direct type constructor
	if findType(richAST, name) != nil {
		return name
	}

	// Sealed case constructor -> returns parent sealed type
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for _, v := range tm.SealedVariants {
			if v.Name == name {
				return tm.Name
			}
		}
	}

	// Companion object
	if _, ok := richAST.CompanionObjects[name]; ok {
		// Try to find parent sealed type
		for _, tm := range richAST.Types {
			for _, v := range tm.SealedVariants {
				if v.Name == name {
					return tm.Name
				}
			}
		}
		return name
	}

	// Check functions
	if fm, ok := richAST.Functions[name]; ok {
		if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
			typeName := fm.ReturnType.BaseName()
			return typeName
		}
	}

	return name
}

// inferRHSType infers the type from the right-hand side of an assignment.
func inferRHSType(rhs string, richAST *transpiler.RichAST) string {
	rhs = strings.TrimSpace(rhs)

	// String literals
	if strings.HasPrefix(rhs, "\"") || strings.HasPrefix(rhs, "s\"") || strings.HasPrefix(rhs, "f\"") || strings.HasPrefix(rhs, "`") {
		return "string"
	}
	if rhs == "true" || rhs == "false" {
		return "bool"
	}
	if len(rhs) > 0 && rhs[0] >= '0' && rhs[0] <= '9' {
		if strings.Contains(rhs, ".") {
			return "float64"
		}
		return "int"
	}

	// Constructor: Name(...) or Name[T](...)
	if idx := strings.IndexAny(rhs, "(["); idx > 0 {
		name := rhs[:idx]
		if isExported(name) {
			return resolveConstructorReturnType(name, richAST)
		}
	}

	// Method chain: expr.Method(...) — resolve the full chain
	if strings.Contains(rhs, ".") {
		chain := splitChain(rhs)
		if len(chain) > 1 {
			baseType := resolveBaseType(chain[0], "", 0, richAST)
			if baseType != "" {
				currentType := baseType
				for i := 1; i < len(chain); i++ {
					currentType = resolveChainSegment(chain[i], currentType, richAST)
					if currentType == "" {
						break
					}
				}
				return currentType
			}
		}
	}

	return ""
}

// resolveChainSegment resolves the type after a method/field access on a known type.
// segment is like "Map(...)" or "name" or "Get()"
func resolveChainSegment(segment, currentType string, richAST *transpiler.RichAST) string {
	// Extract method/field name
	name := segment
	isCall := false
	if idx := strings.Index(segment, "("); idx > 0 {
		name = segment[:idx]
		isCall = true
	}

	tm := findType(richAST, currentType)
	if tm == nil {
		return ""
	}

	// Check methods
	if isCall {
		if method, ok := tm.Methods[name]; ok {
			if method.ReturnType != nil && !method.ReturnType.IsNil() {
				return method.ReturnType.BaseName()
			}
		}
	}

	// Check fields
	if ft, ok := tm.Fields[name]; ok {
		return ft.BaseName()
	}

	return ""
}
