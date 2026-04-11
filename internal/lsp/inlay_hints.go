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
	shortDeclRegex   = regexp.MustCompile(`^\s*(\w+)\s*:=\s*`)
	casePatternRegex = regexp.MustCompile(`^\s*case\s+(\w+)\(([^)]*)\)`)
	// Matches lambda params without explicit type: (x) =>, (x, y) =>
	lambdaParamRegex = regexp.MustCompile(`\((\w+(?:\s*,\s*\w+)*)\)\s*=>`)
)

func (h *GalaHandler) InlayHint(ctx context.Context, params *lsp.InlayHintParams) ([]lsp.InlayHint, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypeMap := h.varTypes[uri]
	h.mu.Unlock()

	if text == "" {
		return nil, nil
	}

	hints := make([]lsp.InlayHint, 0)
	lines := strings.Split(text, "\n")

	// Track enclosing function name for scoped variable lookups
	currentFunc := ""
	for i, line := range lines {
		// Detect function declarations to track current scope
		if fm := funcDeclPattern.FindStringSubmatch(line); fm != nil {
			currentFunc = fm[1]
		}

		if i < params.Range.Start.Line || i > params.Range.End.Line {
			continue
		}

		// val/var declarations without explicit type
		if m := valDeclRegex.FindStringSubmatchIndex(line); m != nil {
			varName := line[m[4]:m[5]]
			// Skip if has explicit type annotation
			eqIdx := strings.Index(line[m[5]:], "=")
			if eqIdx >= 0 {
				between := strings.TrimSpace(line[m[5] : m[5]+eqIdx])
				if between == "" {
					// No explicit type — show hint from transpiler (scoped lookup)
					if typStr := lookupVarType(varTypeMap, currentFunc, varName); typStr != "" {
						hints = append(hints, makeTypeHint(i, m[5], typStr))
					}
				}
			}
		}

		// Short declarations: name := expr
		if m := shortDeclRegex.FindStringSubmatchIndex(line); m != nil {
			varName := line[m[2]:m[3]]
			if typStr := lookupVarType(varTypeMap, currentFunc, varName); typStr != "" {
				hints = append(hints, makeTypeHint(i, m[3], typStr))
			}
		}

		// Lambda params without explicit type: (x) => or (x, y) =>
		if m := lambdaParamRegex.FindStringSubmatchIndex(line); m != nil {
			paramStr := line[m[2]:m[3]]
			params := strings.Split(paramStr, ",")
			for _, p := range params {
				p = strings.TrimSpace(p)
				if p == "" || p == "_" {
					continue
				}
				// Skip if param already has a type annotation (contains space)
				if strings.Contains(p, " ") {
					continue
				}
				if typStr := lookupVarType(varTypeMap, currentFunc, p); typStr != "" {
					pos := strings.Index(line, p)
					if pos >= 0 {
						hints = append(hints, makeTypeHint(i, pos+len(p), typStr))
					}
				}
			}
		}

		// Pattern match bindings: case Constructor(a, b) =>
		if richAST != nil {
			hints = append(hints, casePatternHints(line, i, richAST)...)
		}
	}

	return hints, nil
}

// lookupVarType looks up a variable type from the scoped varTypeMap.
// Keys are stored as "funcName.varName" for function-local vars, or "varName" for top-level.
func lookupVarType(varTypeMap map[string]string, funcName, varName string) string {
	if varTypeMap == nil {
		return ""
	}
	// Try function-scoped lookup first
	if funcName != "" {
		if typStr, ok := varTypeMap[funcName+"."+varName]; ok {
			return typStr
		}
	}
	// Fall back to unscoped lookup
	if typStr, ok := varTypeMap[varName]; ok {
		return typStr
	}
	return ""
}

func casePatternHints(line string, lineNum int, richAST *transpiler.RichAST) []lsp.InlayHint {
	m := casePatternRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	constructorName := m[1]
	bindings := m[2]

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

	parenOpen := strings.Index(line, constructorName+"(")
	if parenOpen < 0 {
		return nil
	}
	bindingsStart := parenOpen + len(constructorName) + 1

	hints := make([]lsp.InlayHint, 0)
	parts := strings.Split(bindings, ",")
	for i, binding := range parts {
		binding = strings.TrimSpace(binding)
		if binding == "" || binding == "_" || strings.Contains(binding, " ") {
			continue
		}
		if i < len(variant.FieldTypes) {
			typeName := cleanGoTypeForDisplay(variant.FieldTypes[i].String())
			// Skip unresolved type parameters (single uppercase letter like T, U, A, B)
			if len(typeName) == 1 && typeName[0] >= 'A' && typeName[0] <= 'Z' {
				continue
			}
			pos := strings.Index(line[bindingsStart:], binding)
			if pos >= 0 {
				pos += bindingsStart
				hints = append(hints, makeTypeHint(lineNum, pos+len(binding), typeName))
			}
		}
	}
	return hints
}

func makeTypeHint(line, col int, typeName string) lsp.InlayHint {
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
