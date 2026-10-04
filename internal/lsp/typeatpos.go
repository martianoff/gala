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
//
// Comments are code to nobody: an ordinary sentence ends in `.`, so joining a
// preceding comment line would make every statement that follows one look like
// the tail of a member access. A comment-only line is skipped rather than
// treated as a terminator, so a chain may be annotated between its links.
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
		prevTrimmed := strings.TrimRight(stripLineComment(lines[prev]), " \t")
		if prevTrimmed == "" {
			// Nothing but a comment: keep looking upwards. A genuinely blank
			// line ends the walk, as does anything that is not a continuation.
			if strings.TrimSpace(lines[prev]) != "" {
				continue
			}
			break
		}
		if !shouldJoinPrev(prevTrimmed, net) {
			break
		}
		cur = prevTrimmed + strings.TrimLeft(cur, " \t")
		net += netParens(prevTrimmed)
	}
	return cur
}

// stripLineComment drops a trailing `// ...` comment. A `//` inside a string
// literal is not a comment — "http://example.com" must survive intact.
func stripLineComment(s string) string {
	inStr := false
	for i := 0; i < len(s); i++ {
		switch {
		case inStr:
			if s[i] == '\\' {
				i++
			} else if s[i] == '"' {
				inStr = false
			}
		case s[i] == '"':
			inStr = true
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			return s[:i]
		}
	}
	return s
}

// memberAccessPrefix returns the flattened expression that precedes the dot
// selecting the identifier at (line, char), and reports whether the cursor is
// on such a member access at all.
//
// One predicate for every caller: definition's package-member handler reads the
// qualifier off the prefix, and its fall-through guard asks only whether there
// was a dot. A line-local check answers "no" for a builder chain — whose
// selecting dot trails the PREVIOUS line — and the guard then lets the global
// by-name scans jump to whichever type happens to declare a same-named method.
func memberAccessPrefix(lines []string, line, char int) (string, bool) {
	if line < 0 || line >= len(lines) {
		return "", false
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	for char > 0 && isIdentChar(l[char-1]) {
		char--
	}
	return strings.CutSuffix(strings.TrimRight(flattenLogicalLine(lines, line, char), " \t"), ".")
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
					return resolveMemberType(richAST, receiverType, name)
				}
				if goType := goCallValueType(richAST, l[:i], name); goType != "" {
					return goType
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
			fieldType := resolveMemberType(richAST, receiverType, receiverName)
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
		if ret := typeDisplayName(fm.ReturnType); ret != "" {
			return ret
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

		// Check if there's a dot before the method name (chain). Skip any
		// whitespace (including newlines) — receiver text that's
		// reconstructed from parse-tree contexts preserves inter-token
		// whitespace verbatim, so multi-line chains like
		// `NewServer().\n    WithFilter(...).\n    Method(` arrive here
		// with newlines between the `)` of one call and the next `.`.
		dotIdx := skipTrailingWhitespace(text, i)
		if dotIdx >= 0 && text[dotIdx] == '.' {
			receiverType := resolveChainTypeN(text[:dotIdx], funcScope, richAST, varTypes, depth+1)
			if receiverType != "" {
				return resolveMemberType(richAST, receiverType, methodName)
			}
			if goType := goCallValueType(richAST, text[:dotIdx], methodName); goType != "" {
				return goType
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

	// Check for dot before identifier (pkg.Type or receiver.field). Skip
	// whitespace between the identifier and the dot — same reason as above.
	dotIdx := skipTrailingWhitespace(text, i)
	if dotIdx >= 0 && text[dotIdx] == '.' {
		receiverType := resolveChainTypeN(text[:dotIdx], funcScope, richAST, varTypes, depth+1)
		if receiverType != "" {
			// Could be a field access — resolve field type
			return resolveMemberType(richAST, receiverType, name)
		}
	}

	return resolveReceiverType(name, funcScope, richAST, varTypes)
}

// goCallValueType names the GALA value of `pkg.Name(...)` where pkg — the
// identifier ending receiverText — is an imported Go package and Name returns
// several results: the Try or Tuple the call is (see transpiler.GoResultValueOf).
// It returns "" for anything else.
func goCallValueType(richAST *transpiler.RichAST, receiverText, name string) string {
	if _, sig := goPackageCallee(richAST, receiverText, name); sig != nil {
		if v, ok := transpiler.GoResultValueOf(sig.Returns); ok {
			return typeDisplayName(v.Type)
		}
	}
	return ""
}

// goPackageCallee resolves `qualifier.name`, where qualifier is the identifier
// ending text, to a function of the Go package that qualifier names — directly
// or through an import alias. The signature is nil when there is no such
// function, including when the qualifier is itself selected off something
// (`a.os`).
func goPackageCallee(richAST *transpiler.RichAST, text, name string) (string, *transpiler.GoFuncSignature) {
	end := skipTrailingWhitespace(text, len(text)-1) + 1
	start := end
	for start > 0 && isIdentChar(text[start-1]) {
		start--
	}
	if start == end || (start > 0 && text[start-1] == '.') {
		return "", nil
	}
	qualifier := text[start:end]
	if sig := goPackageFunc(richAST, qualifier, name); sig != nil {
		return qualifier, sig
	}
	if richAST != nil {
		if pkg, ok := richAST.ImportAliases[qualifier]; ok {
			return qualifier, goPackageFunc(richAST, pkg, name)
		}
	}
	return qualifier, nil
}

// skipTrailingWhitespace walks backward from `start` over ASCII whitespace
// (space, tab, newline, carriage return) and returns the index of the first
// non-whitespace byte, or -1 if it runs off the left.
func skipTrailingWhitespace(text string, start int) int {
	j := start
	for j >= 0 && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
		j--
	}
	return j
}

// goSizeSugarReturn resolves the result type of GALA's `.Size()` / `.ByteSize()`
// magic methods when the receiver is a Go primitive (string, slice, or map).
// These zero-arg methods are transpiler sugar (see
// internal/transpiler/transformer/methods.go tryTransformSizeSugar): they lower
// to len(...) / utf8.RuneCountInString(...) and always yield int, even though
// the receiver has no GALA TypeMetadata in the RichAST. Mirrored rule:
//
//	.Size()     on string / []T / map[K]V  -> int
//	.ByteSize() on string                   -> int
//
// It returns ("", false) for any other receiver/method so normal method
// dispatch (e.g. a GALA Array/List/HashMap, which have their own Size()) is
// unaffected.
func goSizeSugarReturn(typeName, methodName string) (string, bool) {
	switch methodName {
	case "Size":
		if isGoSizeableType(typeName) {
			return "int", true
		}
	case "ByteSize":
		if typeName == "string" {
			return "int", true
		}
	}
	return "", false
}

// isGoSizeableType reports whether typeName is a Go string, slice, or map — the
// receivers on which `.Size()` sugar is defined. Slice types arrive as "[]T";
// map types arrive either fully spelled ("map[K]V") or reduced to "map" by
// stripTypeParams.
func isGoSizeableType(typeName string) bool {
	return typeName == "string" ||
		strings.HasPrefix(typeName, "[]") ||
		typeName == "map" || strings.HasPrefix(typeName, "map[")
}

// resolveMemberType names the type that `receiver.member` produces, where
// receiver is either a type name or a packagePrefix-marked package qualifier.
// It is the single join where a resolved receiver meets a member name, reached
// from both typeAtDot and resolveChainTypeN.
func resolveMemberType(richAST *transpiler.RichAST, typeName, methodName string) string {
	// A package qualifier is not a receiver type: the member on it is a
	// package-level function or constructor, not a method, and the head of
	// every `pkg.New().WithX(...)` builder chain arrives here in that form.
	if pkg, isPkg := strings.CutPrefix(typeName, packagePrefix); isPkg {
		return resolvePackageMemberType(richAST, pkg, methodName)
	}

	// GALA's `.Size()` / `.ByteSize()` sugar resolves to int on Go primitive
	// receivers (string/slice/map) that have no GALA TypeMetadata; check it
	// before findType so the magic methods type-resolve like the transpiler.
	if ret, ok := goSizeSugarReturn(typeName, methodName); ok {
		return ret
	}
	tm := findType(richAST, typeName)
	if tm == nil {
		return ""
	}
	if m, ok := tm.Methods[methodName]; ok {
		if ret := typeDisplayName(m.ReturnType); ret != "" {
			return ret
		}
	}
	if ft, ok := tm.Fields[methodName]; ok {
		return typeDisplayName(ft)
	}
	if m := synthesizedMethod(richAST, tm, methodName); m != nil {
		return typeDisplayName(m.ReturnType)
	}
	return ""
}

// packageMember is what `pkg.Name` names: at most one field is set.
type packageMember struct {
	Variant *transpiler.SealedVariant
	Parent  *transpiler.TypeMetadata // the sealed type Variant belongs to
	Type    *transpiler.TypeMetadata
	Func    *transpiler.FunctionMetadata
}

// lookupPackageMember resolves `pkg.Name` to the symbol it denotes. Hover
// renders the result; the chain walker takes the type it produces. One lookup
// for both, so a qualified name cannot mean one thing in a popup and another to
// the resolver behind it.
//
// Every step is keyed on pkg. The by-simple-name fallbacks in findType,
// findFunction and findSealedVariant match across every loaded package in map
// order, so `mypkg.Some` would answer with std's Some — the precise wrong
// answer the qualifier exists to prevent, and one that changes between calls.
//
// Cases come first. The analyzer registers a companion TypeMetadata under each
// sealed case's own name, carrying the generated Apply and Unapply, so a keyed
// type lookup would shadow the case the user actually wrote and turn
// `pkg.Some(x)` from an Option into a Some.
func lookupPackageMember(richAST *transpiler.RichAST, pkg, name string) packageMember {
	if v, parent := packageSealedVariant(richAST, pkg, name); v != nil {
		return packageMember{Variant: v, Parent: parent}
	}
	if tm := packageType(richAST, pkg, name); tm != nil {
		return packageMember{Type: tm}
	}
	if fm := packageFunction(richAST, pkg, name); fm != nil {
		return packageMember{Func: fm}
	}
	return packageMember{}
}

// packageType and packageFunction look a declaration up by owning package and
// simple name. The analyzer keys both tables "pkg.Name", except for the main
// and test packages, which it keys bare — hence the second lookup, guarded by
// the entry's own Package so it cannot answer for a different one.
func packageType(richAST *transpiler.RichAST, pkg, name string) *transpiler.TypeMetadata {
	if tm, ok := richAST.Types[pkg+"."+name]; ok && tm != nil {
		return tm
	}
	if tm, ok := richAST.Types[name]; ok && tm != nil && tm.Package == pkg {
		return tm
	}
	return nil
}

func packageFunction(richAST *transpiler.RichAST, pkg, name string) *transpiler.FunctionMetadata {
	if fm, ok := richAST.Functions[pkg+"."+name]; ok && fm != nil {
		return fm
	}
	if fm, ok := richAST.Functions[name]; ok && fm != nil && fm.Package == pkg {
		return fm
	}
	return nil
}

// packageSealedVariant finds a `case` declared by a sealed type in pkg.
//
// Unlike findSealedVariant this needs no sorted iteration: a case name is
// unique within its package (the generated companion type makes a duplicate a
// redefinition error), so map order cannot change the answer.
func packageSealedVariant(richAST *transpiler.RichAST, pkg, name string) (*transpiler.SealedVariant, *transpiler.TypeMetadata) {
	for _, tm := range richAST.Types {
		if tm == nil || !tm.IsSealed || tm.Package != pkg {
			continue
		}
		for i := range tm.SealedVariants {
			if tm.SealedVariants[i].Name == name {
				return &tm.SealedVariants[i], tm
			}
		}
	}
	return nil, nil
}

// resolvePackageMemberType names the type that `pkg.Name(...)` produces.
func resolvePackageMemberType(richAST *transpiler.RichAST, pkg, name string) string {
	m := lookupPackageMember(richAST, pkg, name)
	switch {
	case m.Variant != nil:
		return m.Parent.Name
	case m.Type != nil:
		return m.Type.Name
	case m.Func != nil:
		return typeDisplayName(m.Func.ReturnType)
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
