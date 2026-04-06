package lsp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

var valDeclRegex = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s*=`)

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

		matches := valDeclRegex.FindStringSubmatchIndex(line)
		if matches == nil {
			continue
		}

		eqIdx := strings.Index(line[matches[5]:], "=")
		if eqIdx < 0 {
			continue
		}
		between := strings.TrimSpace(line[matches[5] : matches[5]+eqIdx])
		if between != "" {
			continue
		}

		rhsStart := matches[5] + eqIdx + 1
		if rhsStart >= len(line) {
			continue
		}
		rhs := strings.TrimSpace(line[rhsStart:])
		inferredType := inferType(rhs, richAST)
		if inferredType == "" {
			continue
		}

		kind := lsp.InlayHintKindType
		label, _ := json.Marshal(": " + inferredType)
		paddingRight := true
		hints = append(hints, lsp.InlayHint{
			Position:     lsp.Position{Line: i, Character: matches[5]},
			Label:        label,
			Kind:         &kind,
			PaddingRight: &paddingRight,
		})
	}

	return hints, nil
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

	if idx := strings.Index(expr, "("); idx > 0 {
		funcName := expr[:idx]
		if fm, ok := richAST.Functions[funcName]; ok && fm.ReturnType != nil && !fm.ReturnType.IsNil() {
			return fm.ReturnType.String()
		}
	}

	return ""
}
