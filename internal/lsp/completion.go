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
