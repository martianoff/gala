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

	var hints []lsp.InlayHint
	lines := strings.Split(text, "\n")

	for i, line := range lines {
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
					// No explicit type — show hint from transpiler
					if typStr, ok := varTypeMap[varName]; ok {
						hints = append(hints, makeTypeHint(i, m[5], typStr))
					}
				}
			}
		}

		// Short declarations: name := expr
		if m := shortDeclRegex.FindStringSubmatchIndex(line); m != nil {
			varName := line[m[2]:m[3]]
			if typStr, ok := varTypeMap[varName]; ok {
				hints = append(hints, makeTypeHint(i, m[3], typStr))
			}
		}

		// Pattern match bindings: case Constructor(a, b) =>
		if richAST != nil {
			hints = append(hints, casePatternHints(line, i, richAST)...)
		}
	}

	return hints, nil
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

	var hints []lsp.InlayHint
	parts := strings.Split(bindings, ",")
	for i, binding := range parts {
		binding = strings.TrimSpace(binding)
		if binding == "" || binding == "_" || strings.Contains(binding, " ") {
			continue
		}
		if i < len(variant.FieldTypes) {
			typeName := cleanGoTypeForDisplay(variant.FieldTypes[i].String())
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
