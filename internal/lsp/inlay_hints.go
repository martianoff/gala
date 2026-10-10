package lsp

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

var (
	valDeclRegex     = regexp.MustCompile(`^\s*(val|var)\s+(\w+)\s*=`)
	// bind/also bind a local name exactly like val/var. No trailing `=` here so
	// the "explicit type annotation" check below can inspect what sits between
	// the name and the `=` (e.g. `bind n Type = ...`).
	bindDeclRegex = regexp.MustCompile(`^\s*(bind|also)\s+(\w+)\s*`)
	shortDeclRegex   = regexp.MustCompile(`^\s*(\w+)\s*:=\s*`)
	casePatternRegex = regexp.MustCompile(`^\s*case\s+(\w+)\(([^)]*)\)`)
)

func (h *GalaHandler) InlayHint(ctx context.Context, params *lsp.InlayHintParams) ([]lsp.InlayHint, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypeMap := h.varTypes[uri]
	lambdaHints := h.lambdaHints[uri]
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

		// bind/also declarations without explicit type. The transpiler records
		// the bound name's inferred element type into the same VarTypes map, so
		// these behave exactly like val/var for inlay hints.
		if m := bindDeclRegex.FindStringSubmatchIndex(line); m != nil {
			varName := line[m[4]:m[5]]
			// Skip if has explicit type annotation (`bind n Type = ...`).
			eqIdx := strings.Index(line[m[5]:], "=")
			if eqIdx >= 0 {
				between := strings.TrimSpace(line[m[5] : m[5]+eqIdx])
				if between == "" {
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

		// Pattern match bindings: case Constructor(a, b) =>
		if richAST != nil {
			hints = append(hints, casePatternHints(line, i, richAST)...)
		}
	}

	// Lambda param hints come from the transformer, which records the exact
	// (line, column) of each param whose type was inferred from the expected
	// call-site type. No text-based parsing needed.
	for _, lh := range lambdaHints {
		line0 := lh.Line - 1 // transformer is 1-based
		if line0 < params.Range.Start.Line || line0 > params.Range.End.Line {
			continue
		}
		if line0 >= len(lines) {
			continue
		}
		// The transformer reports an ANTLR column, which counts code points.
		absEnd := runeToByte(lines[line0], lh.Column) + len(lh.Name)
		typeStr := cleanGoTypeForDisplay(lh.Type.String())
		if typeStr == "" {
			continue
		}
		hints = append(hints, makeTypeHint(line0, absEnd, typeStr))
	}

	x := h.index(text)
	for i := range hints {
		hints[i].Position = x.toWire(hints[i].Position)
	}
	return hints, nil
}

// lookupVarType looks up a variable type from the scoped varTypeMap.
// Keys are stored as "funcName.varName" for function-local vars, or "varName" for top-level.
func lookupVarType(varTypeMap map[string]string, funcName, varName string) string {
	typStr, _ := lookupVarTypeScoped(varTypeMap, funcName, varName)
	return typStr
}

// lookupVarTypeScoped additionally reports whether the hit came from the
// function-scoped key — that is, whether the name is a local rather than a
// package-level binding. Callers that must tell a local apart from a
// same-named package val (hover, which documents only the latter) need the
// distinction, and deriving it from a second lookup elsewhere would put the
// "funcName.varName" key format in two places.
func lookupVarTypeScoped(varTypeMap map[string]string, funcName, varName string) (string, bool) {
	if varTypeMap == nil {
		return "", false
	}
	// Try function-scoped lookup first
	if funcName != "" {
		if typStr, ok := varTypeMap[funcName+"."+varName]; ok {
			if typStr != "" {
				return typStr, true
			}
			// A binding recorded with no type still answers "this name is a
			// local" — but it must not answer "and its type is nothing", or a
			// top-level declaration would lose its hint to a same-named local
			// whose type failed to resolve. The unscoped entry, if any, is the
			// best type available; the local verdict stands either way.
			typStr, _ := varTypeMap[varName]
			return typStr, true
		}
	}
	// Fall back to unscoped lookup
	if typStr, ok := varTypeMap[varName]; ok {
		return typStr, false
	}
	return "", false
}

func casePatternHints(line string, lineNum int, richAST *transpiler.RichAST) []lsp.InlayHint {
	m := casePatternRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	constructorName := m[1]
	bindings := m[2]

	var variant *transpiler.SealedVariant
	var owner *transpiler.TypeMetadata
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for idx := range tm.SealedVariants {
			if tm.SealedVariants[idx].Name == constructorName {
				variant = &tm.SealedVariants[idx]
				owner = tm
				break
			}
		}
		if variant != nil {
			break
		}
	}
	if variant == nil {
		return opaquePatternHint(line, lineNum, constructorName, bindings, richAST)
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
		// A named sub-pattern `Field = x` binds x to the field of that name.
		if field, sub, named := strings.Cut(binding, "="); named {
			i = slices.Index(variant.FieldNames, strings.TrimSpace(field))
			binding = strings.TrimSpace(sub)
			if i < 0 {
				continue
			}
		}
		if binding == "" || binding == "_" || strings.Contains(binding, " ") || isStablePatternName(binding, richAST) {
			continue
		}
		if i < len(variant.FieldTypes) {
			if isVariantOf(binding, variant.FieldTypes[i], richAST) {
				// `case wrap(endFrame)` tests the field for that variant; it
				// binds nothing to annotate.
				continue
			}
			// A field typed by the sealed type's own type parameter has no
			// type to show until the subject is known; any other name — a
			// user type called `A` included — is a real type. Compare the
			// display form: metadata loaded from another package can spell
			// the parameter `std.T` or `Immutable[T]`.
			typeName := cleanGoTypeForDisplay(variant.FieldTypes[i].String())
			if slices.Contains(owner.TypeParams, typeName) {
				continue
			}
			pos := findWholeWord(line[bindingsStart:], binding)
			if pos >= 0 {
				pos += bindingsStart
				hints = append(hints, makeTypeHint(lineNum, pos+len(binding), typeName))
			}
		}
	}
	return hints
}

// opaquePatternHint is the hint for the name an opaque-type pattern binds,
// `case UserID(n)`: the underlying type, `int64`.
func opaquePatternHint(line string, lineNum int, typeName, bindings string, richAST *transpiler.RichAST) []lsp.InlayHint {
	binding := strings.TrimSpace(bindings)
	switch {
	case binding == "_" || binding == "true" || binding == "false" || binding == "nil":
		return nil
	case !isIdentifier(binding) || isStablePatternName(binding, richAST):
		return nil
	}
	for _, tm := range richAST.Types {
		if !tm.IsOpaque || tm.Name != typeName {
			continue
		}
		underlying, ok := tm.OpaqueUnderlying()
		if !ok {
			return nil
		}
		bindingsStart := strings.Index(line, typeName+"(") + len(typeName) + 1
		pos := findWholeWord(line[bindingsStart:], binding)
		if pos < 0 {
			return nil
		}
		return []lsp.InlayHint{makeTypeHint(lineNum, bindingsStart+pos+len(binding), cleanGoTypeForDisplay(underlying.String()))}
	}
	return nil
}

// isStablePatternName reports whether name, written in a case pattern, is a
// stable identifier rather than a binding: a capitalized name of one of the
// package's vals/vars, which the pattern compares against instead of binding.
func isStablePatternName(name string, richAST *transpiler.RichAST) bool {
	if !isExported(name) || richAST == nil {
		return false
	}
	_, ok := richAST.PackageVals[name]
	return ok
}

// isVariantOf reports whether name, written as a sub-pattern against a value
// of type typ, is a variant of that sealed type — so the pattern tests the
// value instead of binding it, whatever the case of the name's first letter
// (as the transpiler decides it).
func isVariantOf(name string, typ transpiler.Type, richAST *transpiler.RichAST) bool {
	if typ == nil || richAST == nil {
		return false
	}
	tm := findType(richAST, typ.BaseName())
	return tm != nil && tm.IsSealed &&
		slices.ContainsFunc(tm.SealedVariants, func(v transpiler.SealedVariant) bool { return v.Name == name })
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

// hasResolvedVarType reports whether the channel carries at least one type that
// actually resolved.
//
// The transformer also records bindings whose type it could not infer, so that
// the LSP can tell a local from a package-level val (see recordLSPVarType).
// Those entries render as the empty string, and a map holding only them means
// analysis produced no type information — which is what the callers of this are
// really asking.
func hasResolvedVarType(varTypeMap map[string]string) bool {
	for _, typStr := range varTypeMap {
		if typStr != "" {
			return true
		}
	}
	return false
}
