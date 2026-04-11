package lsp

import (
	"fmt"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// checkMatchExhaustiveness warns if a match on a sealed type is missing cases.
func checkMatchExhaustiveness(text string, richAST *transpiler.RichAST) []lsp.Diagnostic {
	diagnostics := make([]lsp.Diagnostic, 0)
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "match {") && !strings.HasSuffix(trimmed, "match {") {
			continue
		}

		var caseNames []string
		hasWildcard := false
		depth := 1 // already inside match {
		for j := i + 1; j < len(lines); j++ {
			caseLine := strings.TrimSpace(lines[j])
			depth += strings.Count(caseLine, "{") - strings.Count(caseLine, "}")
			if depth <= 0 {
				break
			}
			// Only check case at match level (depth == 1)
			if depth == 1 && strings.HasPrefix(caseLine, "case ") {
				rest := caseLine[5:]
				if strings.HasPrefix(rest, "_") {
					hasWildcard = true
				} else {
					name := rest
					if idx := strings.IndexAny(name, "(: "); idx > 0 {
						name = name[:idx]
					}
					caseNames = append(caseNames, strings.TrimSpace(name))
				}
			}
		}

		if hasWildcard || len(caseNames) == 0 {
			continue
		}

		for _, tm := range richAST.Types {
			if !tm.IsSealed || len(tm.SealedVariants) == 0 {
				continue
			}

			variantNames := make(map[string]bool)
			for _, v := range tm.SealedVariants {
				variantNames[v.Name] = true
			}

			matchesThisType := false
			for _, cn := range caseNames {
				if variantNames[cn] {
					matchesThisType = true
					break
				}
			}
			if !matchesThisType {
				continue
			}

			covered := make(map[string]bool)
			for _, cn := range caseNames {
				covered[cn] = true
			}

			var missing []string
			for _, v := range tm.SealedVariants {
				if !covered[v.Name] {
					missing = append(missing, v.Name)
				}
			}

			if len(missing) > 0 {
				diagnostics = append(diagnostics, lsp.Diagnostic{
					Range: lsp.Range{
						Start: lsp.Position{Line: i, Character: 0},
						End:   lsp.Position{Line: i, Character: len(lines[i])},
					},
					Severity: sevPtr(lsp.SeverityWarning),
					Source:   "gala",
					Message:  fmt.Sprintf("Non-exhaustive match on %s — missing: %s", tm.Name, strings.Join(missing, ", ")),
				})
			}
			break
		}
	}

	return diagnostics
}
