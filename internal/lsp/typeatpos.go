package lsp

import (
	"strings"

	"martianoff/gala/internal/transpiler"
)

// typeAtDot resolves the type of the expression before a dot at the given position.
// Returns the type name for method/field lookup, or "__package__:name" for package completion.
func typeAtDot(text string, line, char int, richAST *transpiler.RichAST, varTypes map[string]string) string {
	if richAST == nil {
		return ""
	}

	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	if char <= 0 {
		return ""
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

	// Extract what's before the dot
	i = dotPos - 1

	// Case 1: Closing paren before dot — function/constructor call: Some(42). or expr.Method().
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
		// i now points before '(' — extract the name before it
		nameEnd := i + 1
		for i >= 0 && isIdentChar(l[i]) {
			i--
		}
		nameStart := i + 1

		if nameStart < nameEnd {
			name := l[nameStart:nameEnd]

			// Check if there's a dot before this name (chained: receiver.Method().next)
			if i >= 0 && l[i] == '.' {
				// Chain call — resolve the method's return type
				// For now, resolve the base receiver and look up the method
				return resolveReceiverType(name, richAST, varTypes)
			}
			// No preceding dot — this IS the expression (Some(42)., funcName().)
			return resolveReceiverType(name, richAST, varTypes)
		}
	}

	// Case 2: Identifier before dot — variable or package name: x. or pkg.
	end := i + 1
	// Handle underscore-containing names like collection_immutable
	for i >= 0 && (isIdentChar(l[i]) || l[i] == '_') {
		i--
	}
	start := i + 1
	if start >= end {
		return ""
	}
	receiverName := l[start:end]

	return resolveReceiverType(receiverName, richAST, varTypes)
}

// resolveReceiverType resolves a name to a type for dot completion.
func resolveReceiverType(name string, richAST *transpiler.RichAST, varTypes map[string]string) string {
	// 1. Check transpiler's resolved var types
	if varTypes != nil {
		if typStr, ok := varTypes[name]; ok {
			base := typStr
			if idx := strings.Index(base, "["); idx > 0 {
				base = base[:idx]
			}
			return base
		}
	}

	if richAST == nil {
		return ""
	}

	// 2. Check if it's a function call return type
	if fm, ok := richAST.Functions[name]; ok {
		if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
			base := cleanGoTypeForDisplay(fm.ReturnType.String())
			if idx := strings.Index(base, "["); idx > 0 {
				base = base[:idx]
			}
			return base
		}
	}

	// 3. Check if it's a sealed case constructor → parent type
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

	// 4. Check if it's a package name → return package marker for package completion
	for _, pkgName := range richAST.Packages {
		if pkgName == name {
			return "__package__:" + name
		}
	}

	// 5. Check if it's a type name (for static calls like Type.Method)
	if isExported(name) {
		if findType(richAST, name) != nil {
			return name
		}
	}

	return ""
}

// findType looks up a type in the RichAST by simple name, handling aliases and prefixes.
func findType(richAST *transpiler.RichAST, name string) *transpiler.TypeMetadata {
	if tm, ok := richAST.Types[name]; ok {
		return tm
	}
	if tm, ok := richAST.Types["std."+name]; ok {
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
