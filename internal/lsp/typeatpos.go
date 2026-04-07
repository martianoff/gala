package lsp

import (
	"strings"

	"martianoff/gala/internal/transpiler"
)

// typeAtDot resolves the type of the expression before a dot at the given position.
// Uses the transpiler's resolved VarTypes map for accurate results.
func typeAtDot(text string, line, char int, richAST *transpiler.RichAST, varTypes map[string]string) string {
	if richAST == nil {
		return ""
	}

	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}

	// Extract the receiver name before the dot
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}

	// Walk backwards from cursor to find the dot
	i := char - 1
	for i >= 0 && isIdentChar(l[i]) {
		i--
	}
	if i < 0 || l[i] != '.' {
		return ""
	}
	dotPos := i

	// Extract the receiver identifier before the dot
	i = dotPos - 1
	// Skip closing parens for chained calls: expr.Method().
	if i >= 0 && l[i] == ')' {
		depth := 1
		i--
		for i >= 0 && depth > 0 {
			if l[i] == ')' {
				depth++
			} else if l[i] == '(' {
				depth--
			}
			i--
		}
		// Now i points before the method name, skip it
		for i >= 0 && isIdentChar(l[i]) {
			i--
		}
		// Skip the dot before the method
		if i >= 0 && l[i] == '.' {
			i--
		}
	}

	// Extract the base identifier
	end := i + 1
	for i >= 0 && isIdentChar(l[i]) {
		i--
	}
	start := i + 1
	if start >= end {
		return ""
	}
	receiverName := l[start:end]

	// Look up the receiver's type from the transpiler's resolved types
	if varTypes != nil {
		if typStr, ok := varTypes[receiverName]; ok {
			// Strip generic params for type lookup: "Option[Person]" → "Option"
			base := typStr
			if idx := strings.Index(base, "["); idx > 0 {
				base = base[:idx]
			}
			return base
		}
	}

	// Check if it's a type name (for static method calls like Type.Method)
	if isExported(receiverName) {
		if findType(richAST, receiverName) != nil {
			return receiverName
		}
	}

	return ""
}

// findType looks up a type in the RichAST by simple name, handling aliases.
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
	if richAST.TypeAliases != nil {
		if aliasType, ok := richAST.TypeAliases[name]; ok {
			underlying := aliasType.BaseName()
			if underlying != name {
				return findType(richAST, underlying)
			}
		}
	}
	return nil
}
