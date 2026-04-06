package lsp

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"martianoff/gala/internal/transpiler"
)

// checkMatchExhaustiveness scans the source for match expressions on sealed types
// and warns if not all cases are covered.
func checkMatchExhaustiveness(text string, richAST *transpiler.RichAST) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic
	lines := strings.Split(text, "\n")

	// Find match expressions and check case coverage
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for "match {" pattern
		if !strings.Contains(trimmed, "match {") && !strings.HasSuffix(trimmed, "match {") {
			continue
		}

		// Collect case patterns in this match block
		var caseNames []string
		hasWildcard := false
		for j := i + 1; j < len(lines); j++ {
			caseLine := strings.TrimSpace(lines[j])
			if caseLine == "}" {
				break
			}
			if strings.HasPrefix(caseLine, "case ") {
				// Extract the case name
				rest := caseLine[5:]
				if strings.HasPrefix(rest, "_") {
					hasWildcard = true
				} else {
					// Extract constructor name: "case Some(x) =>" -> "Some"
					name := rest
					if idx := strings.IndexAny(name, "(: "); idx > 0 {
						name = name[:idx]
					}
					caseNames = append(caseNames, strings.TrimSpace(name))
				}
			}
		}

		if hasWildcard {
			continue // Wildcard covers everything
		}

		if len(caseNames) == 0 {
			continue
		}

		// Check if the matched cases correspond to a sealed type
		for _, typeMeta := range richAST.Types {
			if !typeMeta.IsSealed || len(typeMeta.SealedVariants) == 0 {
				continue
			}

			// Check if the case names match this sealed type's variants
			variantNames := make(map[string]bool)
			for _, v := range typeMeta.SealedVariants {
				variantNames[v.Name] = true
			}

			// Check if at least one case matches a variant of this type
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

			// Find missing variants
			coveredNames := make(map[string]bool)
			for _, cn := range caseNames {
				coveredNames[cn] = true
			}

			var missing []string
			for _, v := range typeMeta.SealedVariants {
				if !coveredNames[v.Name] {
					missing = append(missing, v.Name)
				}
			}

			if len(missing) > 0 {
				diagnostics = append(diagnostics, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: uint32(len(lines[i]))},
					},
					Severity: severityPtr(protocol.DiagnosticSeverityWarning),
					Source:   strPtr("gala"),
					Message:  fmt.Sprintf("Non-exhaustive match on %s — missing: %s", typeMeta.Name, strings.Join(missing, ", ")),
				})
			}
			break
		}
	}

	return diagnostics
}
