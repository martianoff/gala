package lsp

import (
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"martianoff/gala/internal/transpiler"
)

// TextDocumentCompletion provides completion items based on analyzed metadata.
func (s *GalaServer) TextDocumentCompletion(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := params.TextDocument.URI
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	s.mu.Lock()
	text, ok := s.documents[uri]
	richAST := s.richASTs[uri]
	s.mu.Unlock()

	if !ok {
		return nil, nil
	}

	var items []protocol.CompletionItem

	// Check if we're completing after a dot
	isDot := isDotCompletion(text, line, char)

	if isDot && richAST != nil {
		items = append(items, methodCompletions(richAST)...)
	} else if isNamedArgContext(text, line, char) && richAST != nil {
		// Inside Type(...) — suggest field names
		typeName := extractConstructorName(text, line, char)
		items = append(items, namedArgCompletions(richAST, typeName)...)
	} else if isMatchCaseContext(text, line, char) && richAST != nil {
		// After "case " inside a match — suggest sealed type variants
		items = append(items, matchCaseCompletions(richAST)...)
		items = append(items, keywordCompletions()...)
	} else {
		// General completion: types, functions, keywords
		if richAST != nil {
			items = append(items, typeCompletions(richAST)...)
			items = append(items, functionCompletions(richAST)...)
		}
		items = append(items, keywordCompletions()...)
	}

	return &protocol.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
}

func isDotCompletion(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	lineText := lines[line]
	// Walk backwards from cursor to find a dot
	i := char - 1
	for i >= 0 && (lineText[i] == ' ' || isIdentChar(lineText[i])) {
		i--
	}
	return i >= 0 && lineText[i] == '.'
}

func typeCompletions(richAST *transpiler.RichAST) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]bool)

	for key, typeMeta := range richAST.Types {
		name := typeMeta.Name
		if name == "" {
			// Extract name from qualified key
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				name = key[idx+1:]
			} else {
				name = key
			}
		}
		if seen[name] || !isExported(name) {
			continue
		}
		seen[name] = true

		kind := protocol.CompletionItemKindClass
		detail := "type"
		if typeMeta.IsSealed {
			detail = "sealed type"
		}

		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   &kind,
			Detail: &detail,
		})

		// Add sealed case constructors
		for _, variant := range typeMeta.SealedVariants {
			if !seen[variant.Name] {
				seen[variant.Name] = true
				ck := protocol.CompletionItemKindConstructor
				cd := "case of " + name
				items = append(items, protocol.CompletionItem{
					Label:  variant.Name,
					Kind:   &ck,
					Detail: &cd,
				})
			}
		}
	}

	return items
}

func functionCompletions(richAST *transpiler.RichAST) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, funcMeta := range richAST.Functions {
		if !isExported(funcMeta.Name) {
			continue
		}
		kind := protocol.CompletionItemKindFunction
		sig := formatFuncSignature(funcMeta)
		items = append(items, protocol.CompletionItem{
			Label:  funcMeta.Name,
			Kind:   &kind,
			Detail: &sig,
		})
	}
	return items
}

func methodCompletions(richAST *transpiler.RichAST) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]bool)

	// Collect all methods from all types
	for _, typeMeta := range richAST.Types {
		for name, method := range typeMeta.Methods {
			if !isExported(name) || seen[name] {
				continue
			}
			seen[name] = true
			kind := protocol.CompletionItemKindMethod
			sig := formatMethodSignature(method)
			items = append(items, protocol.CompletionItem{
				Label:  name,
				Kind:   &kind,
				Detail: &sig,
			})
		}
	}

	return items
}

func keywordCompletions() []protocol.CompletionItem {
	keywords := []string{
		"package", "import", "val", "var", "func", "type", "struct",
		"interface", "sealed", "embed", "if", "else", "for", "range",
		"return", "match", "case", "true", "false", "nil", "map",
	}

	var items []protocol.CompletionItem
	kind := protocol.CompletionItemKindKeyword
	for _, kw := range keywords {
		items = append(items, protocol.CompletionItem{
			Label: kw,
			Kind:  &kind,
		})
	}
	return items
}

func formatFuncSignature(meta *transpiler.FunctionMetadata) string {
	var b strings.Builder
	b.WriteString("func(")
	for i, name := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(name + " " + meta.ParamTypes[i].String())
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	return b.String()
}

func formatMethodSignature(meta *transpiler.MethodMetadata) string {
	var b strings.Builder
	b.WriteString("(")
	for i, name := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(name + " " + meta.ParamTypes[i].String())
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	return b.String()
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

// isNamedArgContext checks if the cursor is inside a constructor call: Type(|)
func isNamedArgContext(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	lineText := lines[line]
	// Walk backwards from cursor to find an unmatched '('
	depth := 0
	for i := char - 1; i >= 0; i-- {
		if lineText[i] == ')' {
			depth++
		} else if lineText[i] == '(' {
			if depth == 0 {
				// Found unmatched '(' — check if preceded by a type name
				j := i - 1
				for j >= 0 && (isIdentChar(lineText[j]) || lineText[j] == '[' || lineText[j] == ']') {
					j--
				}
				name := lineText[j+1 : i]
				// Strip generic params
				if idx := strings.Index(name, "["); idx >= 0 {
					name = name[:idx]
				}
				return isExported(name)
			}
			depth--
		}
	}
	return false
}

// extractConstructorName finds the type name before the '(' at the cursor position.
func extractConstructorName(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	lineText := lines[line]
	depth := 0
	for i := char - 1; i >= 0; i-- {
		if lineText[i] == ')' {
			depth++
		} else if lineText[i] == '(' {
			if depth == 0 {
				j := i - 1
				for j >= 0 && (isIdentChar(lineText[j]) || lineText[j] == '[' || lineText[j] == ']') {
					j--
				}
				name := lineText[j+1 : i]
				if idx := strings.Index(name, "["); idx >= 0 {
					name = name[:idx]
				}
				return name
			}
			depth--
		}
	}
	return ""
}

// namedArgCompletions returns field names for a struct constructor.
func namedArgCompletions(richAST *transpiler.RichAST, typeName string) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	if typeName == "" {
		return items
	}

	// Find the type in metadata
	for key, typeMeta := range richAST.Types {
		name := typeMeta.Name
		if name == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				name = key[idx+1:]
			} else {
				name = key
			}
		}
		if name != typeName {
			continue
		}

		// Suggest field names as "name = "
		for _, fieldName := range typeMeta.FieldNames {
			fieldType := typeMeta.Fields[fieldName]
			kind := protocol.CompletionItemKindField
			insertText := fieldName + " = "
			detail := fieldType.String()
			items = append(items, protocol.CompletionItem{
				Label:      fieldName,
				Kind:       &kind,
				Detail:     &detail,
				InsertText: &insertText,
			})
		}
		break
	}

	return items
}

// isMatchCaseContext checks if cursor is after "case " inside a match block.
func isMatchCaseContext(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[line])
	return strings.HasPrefix(trimmed, "case ")
}

// matchCaseCompletions returns sealed type variants for pattern matching.
func matchCaseCompletions(richAST *transpiler.RichAST) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]bool)

	for _, typeMeta := range richAST.Types {
		if !typeMeta.IsSealed {
			continue
		}
		for _, variant := range typeMeta.SealedVariants {
			if seen[variant.Name] {
				continue
			}
			seen[variant.Name] = true

			kind := protocol.CompletionItemKindEnumMember
			detail := "case of " + typeMeta.Name

			// Build insert text with field destructuring
			var insertText string
			if len(variant.FieldNames) > 0 {
				insertText = variant.Name + "(" + strings.Join(variant.FieldNames, ", ") + ") => "
			} else {
				insertText = variant.Name + "() => "
			}

			items = append(items, protocol.CompletionItem{
				Label:      variant.Name,
				Kind:       &kind,
				Detail:     &detail,
				InsertText: &insertText,
			})
		}
	}

	// Add wildcard pattern
	kind := protocol.CompletionItemKindKeyword
	wildcard := "_ => "
	items = append(items, protocol.CompletionItem{
		Label:      "_",
		Kind:       &kind,
		InsertText: &wildcard,
	})

	return items
}
