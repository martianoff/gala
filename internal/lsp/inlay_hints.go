package lsp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

var (
	valDeclRegex     = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s*=`)
	shortDeclRegex   = regexp.MustCompile(`^\s*(\w+)\s*:=\s*(.+)$`)
	lambdaParamRegex = regexp.MustCompile(`\(([^)]*)\)\s*=>`)
	forRangeRegex    = regexp.MustCompile(`^\s*for\s+(\w+(?:\s*,\s*\w+)?)\s+(?::=\s+)?range\s+(\w+)`)
	casePatternRegex = regexp.MustCompile(`^\s*case\s+(\w+)\(([^)]*)\)`)
)

func (h *GalaHandler) InlayHint(ctx context.Context, params *lsp.InlayHintParams) ([]lsp.InlayHint, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	h.mu.Unlock()

	if text == "" || richAST == nil {
		return nil, nil
	}

	var hints []lsp.InlayHint
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i < params.Range.Start.Line || i > params.Range.End.Line {
			continue
		}

		// 1. val/var declarations without explicit type
		hints = append(hints, valDeclHints(line, i, text, richAST)...)

		// 1b. Short declarations: name := expr
		hints = append(hints, shortDeclHints(line, i, text, richAST)...)

		// 2. Lambda parameters without types: (x) => or (x, y) =>
		hints = append(hints, lambdaParamHints(line, i, text, richAST)...)

		// 3. Pattern match bindings: case Some(x) =>
		hints = append(hints, casePatternHints(line, i, richAST)...)
	}

	return hints, nil
}

func valDeclHints(line string, lineNum int, fullText string, richAST *transpiler.RichAST) []lsp.InlayHint {
	matches := valDeclRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	eqIdx := strings.Index(line[matches[5]:], "=")
	if eqIdx < 0 {
		return nil
	}
	between := strings.TrimSpace(line[matches[5] : matches[5]+eqIdx])
	if between != "" {
		return nil // Has explicit type
	}

	rhsStart := matches[5] + eqIdx + 1
	if rhsStart >= len(line) {
		return nil
	}
	rhs := strings.TrimSpace(line[rhsStart:])
	// Use inferRHSType for full chain resolution (opt.Map(...) → Option)
	inferredType := inferRHSTypeInContext(rhs, fullText, lineNum, richAST)
	if inferredType == "" {
		// Fallback to simple inference
		inferredType = inferType(rhs, richAST)
	}
	if inferredType == "" {
		return nil
	}

	return []lsp.InlayHint{makeTypeHint(lineNum, matches[5], inferredType)}
}

func shortDeclHints(line string, lineNum int, fullText string, richAST *transpiler.RichAST) []lsp.InlayHint {
	m := shortDeclRegex.FindStringSubmatchIndex(line)
	if m == nil {
		return nil
	}
	// m[2]:m[3] is the variable name, m[4]:m[5] is the RHS
	rhs := strings.TrimSpace(line[m[4]:m[5]])
	inferredType := inferRHSTypeInContext(rhs, fullText, lineNum, richAST)
	if inferredType == "" {
		inferredType = inferType(rhs, richAST)
	}
	if inferredType == "" {
		return nil
	}
	return []lsp.InlayHint{makeTypeHint(lineNum, m[3], inferredType)}
}

func lambdaParamHints(line string, lineNum int, fullText string, richAST *transpiler.RichAST) []lsp.InlayHint {
	// Find lambda patterns: (x) => or (x, y) =>
	matches := lambdaParamRegex.FindAllStringSubmatchIndex(line, -1)
	if matches == nil {
		return nil
	}

	var hints []lsp.InlayHint
	for _, m := range matches {
		// m[2]:m[3] is the params group
		paramsStr := line[m[2]:m[3]]
		params := strings.Split(paramsStr, ",")

		for _, param := range params {
			param = strings.TrimSpace(param)
			if param == "" {
				continue
			}
			// Skip if already has a type annotation (contains space after name)
			parts := strings.Fields(param)
			if len(parts) > 1 {
				continue // Has explicit type
			}
			paramName := parts[0]

			// Try to infer from context — look for the method being called
			// e.g., list.Map((x) => ...) — x is the element type of list
			lambdaType := inferLambdaParamType(line, paramName, fullText, lineNum, richAST)
			if lambdaType != "" {
				// Find the param position in the line
				paramPos := strings.Index(line[m[2]:m[3]], paramName)
				if paramPos >= 0 {
					col := m[2] + paramPos + len(paramName)
					hints = append(hints, makeTypeHint(lineNum, col, lambdaType))
				}
			}
		}
	}
	return hints
}

func casePatternHints(line string, lineNum int, richAST *transpiler.RichAST) []lsp.InlayHint {
	// Match: case Some(x) => or case Left(val) =>
	m := casePatternRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	constructorName := m[1] // e.g., "Some", "Left"
	bindings := m[2]         // e.g., "x" or "val, key"

	// Find the sealed type and variant
	var variant *transpiler.SealedVariant
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for idx := range tm.SealedVariants {
			if tm.SealedVariants[idx].Name == constructorName {
				variant = &tm.SealedVariants[idx]
				break
			}
		}
		if variant != nil {
			break
		}
	}

	if variant == nil {
		return nil
	}

	// Find the position of the bindings within parentheses: case Name(bindings)
	parenOpen := strings.Index(line, constructorName+"(")
	if parenOpen < 0 {
		return nil
	}
	bindingsStart := parenOpen + len(constructorName) + 1 // position after '('

	var hints []lsp.InlayHint
	bindingParts := strings.Split(bindings, ",")
	for i, binding := range bindingParts {
		binding = strings.TrimSpace(binding)
		if binding == "" || binding == "_" {
			continue
		}
		// Skip if already has type annotation
		if strings.Contains(binding, " ") {
			continue
		}
		if i < len(variant.FieldTypes) {
			typeName := variant.FieldTypes[i].String()
			// Find position within the parenthesized bindings only
			pos := strings.Index(line[bindingsStart:], binding)
			if pos >= 0 {
				pos += bindingsStart // adjust to absolute position
				hints = append(hints, makeTypeHint(lineNum, pos+len(binding), typeName))
			}
		}
	}
	return hints
}

// inferLambdaParamType tries to infer a lambda parameter's type from the calling context.
// e.g., in list.Map((x) => ...), if list is Array[Person], x is Person.
func inferLambdaParamType(line, paramName, fullText string, lineNum int, richAST *transpiler.RichAST) string {
	// Look for pattern: expr.MethodName((params) => ...)
	// Find the method name before the lambda
	lambdaIdx := strings.Index(line, "("+paramName)
	if lambdaIdx < 0 {
		lambdaIdx = strings.Index(line, "( "+paramName)
	}
	if lambdaIdx <= 0 {
		return ""
	}

	// Walk backwards to find .MethodName(
	i := lambdaIdx - 1
	if i >= 0 && line[i] == '(' {
		i--
	}
	// Find method name
	methodEnd := i + 1
	for i >= 0 && isIdentChar(line[i]) {
		i--
	}
	if i < 0 || line[i] != '.' {
		return ""
	}
	methodName := line[i+1 : methodEnd]

	// Find receiver before the dot
	dotPos := i
	i = dotPos - 1
	for i >= 0 && isIdentChar(line[i]) {
		i--
	}
	receiverName := line[i+1 : dotPos]

	// Resolve the receiver's type
	receiverType := resolveBaseType(receiverName, fullText, lineNum, richAST)
	if receiverType == "" {
		return ""
	}

	// Find the method's first parameter type
	tm := findType(richAST, receiverType)
	if tm == nil {
		return ""
	}
	method, ok := tm.Methods[methodName]
	if !ok || len(method.ParamTypes) == 0 {
		return ""
	}

	// For Map/Filter/ForEach etc., the param type is typically the element type
	// The method's param is a function type like func(T) U
	paramType := method.ParamTypes[0]
	if paramType != nil {
		typStr := paramType.String()
		// If it's a func type, extract the param type
		if strings.HasPrefix(typStr, "func(") {
			inner := typStr[5:]
			end := strings.Index(inner, ")")
			if end > 0 {
				return inner[:end]
			}
		}
	}

	return ""
}

// cleanTypeName strips package prefixes like "std." for display.
func cleanTypeName(name string) string {
	name = strings.ReplaceAll(name, "std.", "")
	// Also clean nested types: Immutable[std.Option[T]] → Immutable[Option[T]]
	return name
}

func makeTypeHint(line, col int, typeName string) lsp.InlayHint {
	typeName = cleanTypeName(typeName)
	kind := lsp.InlayHintKindType
	label, _ := json.Marshal(": " + typeName)
	paddingRight := true
	return lsp.InlayHint{
		Position:     lsp.Position{Line: line, Character: col},
		Label:        label,
		Kind:         &kind,
		PaddingRight: &paddingRight,
	}
}

func inferType(expr string, richAST *transpiler.RichAST) string {
	expr = strings.TrimSpace(expr)

	if strings.HasPrefix(expr, "\"") || strings.HasPrefix(expr, "s\"") || strings.HasPrefix(expr, "f\"") || strings.HasPrefix(expr, "`") {
		return "string"
	}
	if expr == "true" || expr == "false" {
		return "bool"
	}
	if expr == "nil" {
		return ""
	}
	if len(expr) > 0 && expr[0] >= '0' && expr[0] <= '9' {
		if strings.Contains(expr, ".") {
			return "float64"
		}
		return "int"
	}

	// If expression: if (cond) expr else expr
	if strings.HasPrefix(expr, "if ") || strings.HasPrefix(expr, "if(") {
		// Try to infer from the "else" branch (simpler expression usually)
		if elseIdx := strings.LastIndex(expr, "else "); elseIdx > 0 {
			elseBranch := strings.TrimSpace(expr[elseIdx+5:])
			if t := inferType(elseBranch, richAST); t != "" {
				return t
			}
		}
	}

	// Constructor with explicit type args: Left[string, int](...) → Either[string, int]
	if bracketIdx := strings.Index(expr, "["); bracketIdx > 0 {
		parenIdx := strings.Index(expr, "(")
		if parenIdx > bracketIdx {
			name := expr[:bracketIdx]
			typeArgs := expr[bracketIdx:parenIdx]
			if isExported(name) {
				parentType := resolveConstructorReturnType(name, richAST)
				return parentType + typeArgs
			}
		}
	}

	if idx := strings.IndexAny(expr, "(["); idx > 0 {
		name := expr[:idx]
		// Handle pkg.TypeName(...)
		if dotIdx := strings.LastIndex(name, "."); dotIdx > 0 {
			simpleName := name[dotIdx+1:]
			if isExported(simpleName) {
				return resolveConstructorReturnType(simpleName, richAST)
			}
		}
		if isExported(name) {
			return resolveConstructorReturnType(name, richAST)
		}
	}

	if idx := strings.Index(expr, "("); idx > 0 {
		funcName := expr[:idx]
		if fm, ok := richAST.Functions[funcName]; ok && fm.ReturnType != nil && !fm.ReturnType.IsNil() {
			return cleanTypeName(fm.ReturnType.String())
		}
	}

	return ""
}
