package lsp

import (
	"regexp"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// InlayHint represents an LSP 3.17 InlayHint (not in GLSP protocol_3_16).
type InlayHint struct {
	Position     Position `json:"position"`
	Label        string   `json:"label"`
	Kind         int      `json:"kind,omitempty"` // 1=Type, 2=Parameter
	PaddingLeft  bool     `json:"paddingLeft,omitempty"`
	PaddingRight bool     `json:"paddingRight,omitempty"`
}

// Position for inlay hint (matches LSP spec).
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// valDeclRegex matches val/var declarations without explicit type: val name = expr
var valDeclRegex = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s*=`)

// ComputeInlayHints returns type hints for val/var declarations in the visible range.
func ComputeInlayHints(text string, richAST *transpiler.RichAST, startLine, endLine uint32) []InlayHint {
	if richAST == nil {
		return nil
	}

	var hints []InlayHint
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lineNum := uint32(i)
		if lineNum < startLine || lineNum > endLine {
			continue
		}

		matches := valDeclRegex.FindStringSubmatchIndex(line)
		if matches == nil {
			continue
		}

		// matches[4:6] is the variable name
		varName := line[matches[4]:matches[5]]
		_ = varName

		// Check if there's already a type annotation between name and =
		eqIdx := strings.Index(line[matches[5]:], "=")
		if eqIdx < 0 {
			continue
		}
		between := strings.TrimSpace(line[matches[5] : matches[5]+eqIdx])
		if between != "" {
			continue // Has explicit type
		}

		// Infer type from RHS
		rhsStart := matches[5] + eqIdx + 1
		if rhsStart >= len(line) {
			continue
		}
		rhs := strings.TrimSpace(line[rhsStart:])
		inferredType := inferTypeFromExpression(rhs, richAST)
		if inferredType == "" {
			continue
		}

		hints = append(hints, InlayHint{
			Position:     Position{Line: lineNum, Character: uint32(matches[5])},
			Label:        ": " + inferredType,
			Kind:         1, // Type hint
			PaddingLeft:  false,
			PaddingRight: true,
		})
	}

	return hints
}

// inferTypeFromExpression tries to determine the type from expression text.
func inferTypeFromExpression(expr string, richAST *transpiler.RichAST) string {
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

	// Constructor: TypeName(...) or TypeName[T](...)
	if idx := strings.IndexAny(expr, "(["); idx > 0 {
		typeName := expr[:idx]
		if isExported(typeName) {
			for key, tm := range richAST.Types {
				name := tm.Name
				if name == "" {
					if i := strings.LastIndex(key, "."); i >= 0 {
						name = key[i+1:]
					}
				}
				if name == typeName {
					return typeName
				}
			}
			if _, ok := richAST.CompanionObjects[typeName]; ok {
				return typeName
			}
		}
	}

	// Function call
	if idx := strings.Index(expr, "("); idx > 0 {
		funcName := expr[:idx]
		if fm, ok := richAST.Functions[funcName]; ok {
			if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
				return fm.ReturnType.String()
			}
		}
	}

	return ""
}
