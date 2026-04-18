package lsp

import (
	"regexp"
	"strings"

	"martianoff/gala/internal/transpiler"
)

var funcDeclPattern = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s+)?(\w+)`)

const packagePrefix = "__package__:"

// findEnclosingFunc scans lines backwards from the given line to find the enclosing function name.
func findEnclosingFunc(lines []string, targetLine int) string {
	for i := targetLine; i >= 0; i-- {
		if m := funcDeclPattern.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

// flattenLogicalLine returns the text up to (line, char) as a single line,
// prepending earlier lines while the expression is an obvious continuation:
// either the prior line ends with a continuation token (`.`, `(`, `,`, `=`, `=>`, etc.)
// or the accumulated text has more closing than opening parens (meaning the
// opening paren lives on an earlier line).
func flattenLogicalLine(lines []string, line, char int) string {
	if line >= len(lines) {
		return ""
	}
	cur := lines[line]
	if char > len(cur) {
		char = len(cur)
	}
	cur = cur[:char]
	// Keep a running net-parens count so we don't rescan the growing `cur`
	// on every iteration (O(n²) → O(n) over a multi-line chain).
	net := netParens(cur)
	for prev := line - 1; prev >= 0; prev-- {
		prevTrimmed := strings.TrimRight(lines[prev], " \t")
		if !shouldJoinPrev(prevTrimmed, net) {
			break
		}
		cur = prevTrimmed + strings.TrimLeft(cur, " \t")
		net += netParens(prevTrimmed)
	}
	return cur
}

func shouldJoinPrev(prevTrimmed string, curNetParens int) bool {
	if prevTrimmed == "" {
		return false
	}
	if curNetParens < 0 {
		return true
	}
	if strings.HasSuffix(prevTrimmed, "=>") {
		return true
	}
	switch prevTrimmed[len(prevTrimmed)-1] {
	case '.', '(', ',', '=', '+', '-', '*', '/', '&', '|', '?', ':':
		return true
	}
	return false
}

// netParens counts ( minus ) ignoring characters inside double-quoted strings.
func netParens(s string) int {
	n := 0
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '(' {
			n++
		} else if c == ')' {
			n--
		}
	}
	return n
}

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

	// Determine enclosing function for scoped variable lookup
	enclosingFunc := findEnclosingFunc(lines, line)
	// Flatten multi-line chained expressions into a single logical line so the
	// backward walk can cross newlines (e.g. `NewResponse(...).\n WithBody(...).`).
	l := flattenLogicalLine(lines, line, char)
	char = len(l)
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
				// Resolve the chain: find the receiver type, then get the method's return type
				receiverType := resolveChainTypeN(l[:i], enclosingFunc, richAST, varTypes, 0)
				if receiverType != "" {
					return resolveMethodReturn(richAST, receiverType, name)
				}
				// Fallback: try resolving name directly
				return resolveReceiverType(name, enclosingFunc, richAST, varTypes)
			}
			// No preceding dot — this IS the expression (Some(42)., funcName().)
			return resolveReceiverType(name, enclosingFunc, richAST, varTypes)
		}
	}

	// Case 2: Identifier before dot — variable, field, or package name
	end := i + 1
	for i >= 0 && (isIdentChar(l[i]) || l[i] == '_') {
		i--
	}
	start := i + 1
	if start >= end {
		return ""
	}
	receiverName := l[start:end]

	// Check for chain: x.field. → resolve via chain
	if i >= 0 && l[i] == '.' {
		receiverType := resolveChainTypeN(l[:i], enclosingFunc, richAST, varTypes, 0)
		if receiverType != "" {
			fieldType := resolveMethodReturn(richAST, receiverType, receiverName)
			if fieldType != "" {
				return fieldType
			}
		}
	}

	return resolveReceiverType(receiverName, enclosingFunc, richAST, varTypes)
}

// resolveReceiverType resolves a name to a type for dot completion.
func resolveReceiverType(name, funcScope string, richAST *transpiler.RichAST, varTypes map[string]string) string {
	// 1. Check transpiler's resolved var types (function-scoped)
	if typStr := lookupVarType(varTypes, funcScope, name); typStr != "" {
		return stripTypeParams(typStr)
	}

	if richAST == nil {
		return ""
	}

	// 2. Check if it's a function call return type (try bare name and package-qualified)
	if fm := findFunction(richAST, name); fm != nil {
		if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
			return stripTypeParams(cleanGoTypeForDisplay(fm.ReturnType.String()))
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
			return packagePrefix + name
		}
	}

	// 4b. Check if it's an import alias → resolve to actual package name
	if richAST.ImportAliases != nil {
		if pkgName, ok := richAST.ImportAliases[name]; ok {
			return packagePrefix + pkgName
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

// Each recursion level of resolveChainTypeN consumes at least one method
// call (a dot + identifier + paren pair) from the input text, so recursion
// is naturally bounded by text length. maxChainDepth is a pure safety net
// against accidental cycles on malformed input; set it generously so that
// real-world fluent-builder chains (e.g. gala-server's NewServer()...
// WithFilter()) never hit the limit silently and drop completion on the
// floor. 10 was not enough — the hello example chains 30+ calls.
const maxChainDepth = 200

// resolveChainTypeN walks a chain of identifiers/calls backwards, resolving
// the type of the expression at the end of the walk. Depth guards against
// runaway recursion on malformed input.
func resolveChainTypeN(text string, funcScope string, richAST *transpiler.RichAST, varTypes map[string]string, depth int) string {
	if depth > maxChainDepth {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// If ends with ')' — method/function call, resolve its return type
	if text[len(text)-1] == ')' {
		parenDepth := 1
		i := len(text) - 2
		for i >= 0 && parenDepth > 0 {
			if text[i] == ')' {
				parenDepth++
			} else if text[i] == '(' {
				parenDepth--
			}
			i--
		}
		// Extract method name before '('
		nameEnd := i + 1
		for i >= 0 && isIdentChar(text[i]) {
			i--
		}
		nameStart := i + 1
		if nameStart >= nameEnd {
			return ""
		}
		methodName := text[nameStart:nameEnd]

		// Check if there's a dot before the method name (chain)
		if i >= 0 && text[i] == '.' {
			receiverType := resolveChainTypeN(text[:i], funcScope, richAST, varTypes, depth+1)
			if receiverType != "" {
				return resolveMethodReturn(richAST, receiverType, methodName)
			}
		}
		// No dot — standalone call or variable
		return resolveReceiverType(methodName, funcScope, richAST, varTypes)
	}

	// Plain identifier — variable or type
	i := len(text) - 1
	for i >= 0 && (isIdentChar(text[i]) || text[i] == '_') {
		i--
	}
	name := text[i+1:]
	if name == "" {
		return ""
	}

	// Check for dot before identifier (pkg.Type or receiver.field)
	if i >= 0 && text[i] == '.' {
		receiverType := resolveChainTypeN(text[:i], funcScope, richAST, varTypes, depth+1)
		if receiverType != "" {
			// Could be a field access — resolve field type
			return resolveMethodReturn(richAST, receiverType, name)
		}
	}

	return resolveReceiverType(name, funcScope, richAST, varTypes)
}

// resolveMethodReturn finds the return type of a method on a given type.
func resolveMethodReturn(richAST *transpiler.RichAST, typeName, methodName string) string {
	tm := findType(richAST, typeName)
	if tm == nil {
		return ""
	}
	if m, ok := tm.Methods[methodName]; ok {
		if m.ReturnType != nil && !m.ReturnType.IsNil() {
			return stripTypeParams(cleanGoTypeForDisplay(m.ReturnType.String()))
		}
	}
	if ft, ok := tm.Fields[methodName]; ok {
		return stripTypeParams(cleanGoTypeForDisplay(ft.String()))
	}
	return ""
}

// findFunction looks up a function by bare name, then by "pkg.Name" suffix.
// Handles the common case where functions are stored as "mylib.NewGroup" but
// the call site uses the unqualified name "NewGroup".
func findFunction(richAST *transpiler.RichAST, name string) *transpiler.FunctionMetadata {
	if fm, ok := richAST.Functions[name]; ok {
		return fm
	}
	for key, fm := range richAST.Functions {
		simpleName := key
		if idx := strings.LastIndex(key, "."); idx >= 0 {
			simpleName = key[idx+1:]
		}
		if simpleName == name {
			return fm
		}
	}
	return nil
}

// findType looks up a type in the RichAST by name.
// Handles: exact key match, "std." prefix, package-qualified names ("pkg.Type"),
// simple name suffix match, and type aliases.
func findType(richAST *transpiler.RichAST, name string) *transpiler.TypeMetadata {
	// Direct lookup by exact key
	if tm, ok := richAST.Types[name]; ok {
		return tm
	}
	// Try with std. prefix
	if tm, ok := richAST.Types["std."+name]; ok {
		return tm
	}

	// Extract simple name from qualified name (e.g., "mylib.Animal" → "Animal")
	simpleName := name
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		simpleName = name[idx+1:]
	}

	// Search all types by matching either the full key, tm.Name, or simple name
	for key, tm := range richAST.Types {
		typeName := tm.Name
		if typeName == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				typeName = key[idx+1:]
			}
		}
		// Match by simple type name (handles "mylib.Animal" matching key "mylib.Animal"
		// or key "Animal" with tm.Name "Animal")
		if typeName == simpleName {
			return tm
		}
	}

	// Type alias resolution
	if richAST.TypeAliases != nil {
		if aliasType, ok := richAST.TypeAliases[name]; ok {
			underlying := aliasType.BaseName()
			if underlying != name {
				return findType(richAST, underlying)
			}
		}
		// Also try alias by simple name
		if simpleName != name {
			if aliasType, ok := richAST.TypeAliases[simpleName]; ok {
				underlying := aliasType.BaseName()
				if underlying != simpleName {
					return findType(richAST, underlying)
				}
			}
		}
	}
	return nil
}
