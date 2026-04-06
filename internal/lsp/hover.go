package lsp

import (
	"fmt"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"martianoff/gala/internal/transpiler"
)

// TextDocumentHover returns type/signature info when hovering over a symbol.
func (s *GalaServer) TextDocumentHover(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	uri := params.TextDocument.URI
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	s.mu.Lock()
	text, ok := s.documents[uri]
	richAST := s.richASTs[uri]
	s.mu.Unlock()

	if !ok || richAST == nil {
		return nil, nil
	}

	// Find the word at the cursor position
	word := wordAtPosition(text, line, char)
	if word == "" {
		return nil, nil
	}

	// Look up the word in type metadata
	if info := lookupSymbol(richAST, word); info != "" {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: info,
			},
		}, nil
	}

	return nil, nil
}

// lookupSymbol finds type/function info for a symbol name in the RichAST.
func lookupSymbol(richAST *transpiler.RichAST, name string) string {
	// Check types
	if typeMeta, ok := richAST.Types[name]; ok {
		return formatTypeMeta(typeMeta)
	}

	// Check with package prefix (e.g., "std.Option")
	for key, typeMeta := range richAST.Types {
		if strings.HasSuffix(key, "."+name) {
			return formatTypeMeta(typeMeta)
		}
	}

	// Check functions
	if funcMeta, ok := richAST.Functions[name]; ok {
		return formatFuncMeta(funcMeta)
	}

	// Check companion objects (sealed type constructors like Some, None, Left, Right)
	if companion, ok := richAST.CompanionObjects[name]; ok {
		return formatCompanionMeta(companion)
	}

	return ""
}

func formatTypeMeta(meta *transpiler.TypeMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	if meta.IsSealed {
		b.WriteString(fmt.Sprintf("sealed type %s", meta.Name))
	} else {
		b.WriteString(fmt.Sprintf("type %s", meta.Name))
	}
	if len(meta.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(meta.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString("\n```\n")

	// Show fields
	if len(meta.FieldNames) > 0 {
		b.WriteString("\n**Fields:**\n")
		for i, fieldName := range meta.FieldNames {
			fieldType := meta.Fields[fieldName]
			mut := "val"
			if i < len(meta.ImmutFlags) && !meta.ImmutFlags[i] {
				mut = "var"
			}
			b.WriteString(fmt.Sprintf("- `%s %s %s`\n", mut, fieldName, fieldType))
		}
	}

	// Show sealed variants
	if len(meta.SealedVariants) > 0 {
		b.WriteString("\n**Cases:**\n")
		for _, v := range meta.SealedVariants {
			b.WriteString(fmt.Sprintf("- `case %s`\n", v.Name))
		}
	}

	// Show methods
	if len(meta.Methods) > 0 {
		b.WriteString("\n**Methods:**\n")
		for name, method := range meta.Methods {
			b.WriteString(fmt.Sprintf("- `%s(%s) %s`\n", name, formatParams(method), method.ReturnType))
		}
	}

	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}

	return b.String()
}

func formatFuncMeta(meta *transpiler.FunctionMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	b.WriteString(fmt.Sprintf("func %s", meta.Name))
	if len(meta.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(meta.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString("(")
	for i, paramName := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(fmt.Sprintf("%s %s", paramName, meta.ParamTypes[i]))
		} else {
			b.WriteString(paramName)
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(fmt.Sprintf(" %s", meta.ReturnType))
	}
	b.WriteString("\n```\n")

	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}

	return b.String()
}

func formatCompanionMeta(meta *transpiler.CompanionObjectMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	b.WriteString(fmt.Sprintf("%s — sealed case constructor\n", meta.Name))
	b.WriteString("```\n")
	return b.String()
}

func formatParams(method *transpiler.MethodMetadata) string {
	var parts []string
	for i, name := range method.ParamNames {
		if i < len(method.ParamTypes) {
			parts = append(parts, fmt.Sprintf("%s %s", name, method.ParamTypes[i]))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

// wordAtPosition extracts the identifier at the given line/char position.
func wordAtPosition(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	lineText := lines[line]
	if char >= len(lineText) {
		return ""
	}

	// Expand left
	start := char
	for start > 0 && isIdentChar(lineText[start-1]) {
		start--
	}
	// Expand right
	end := char
	for end < len(lineText) && isIdentChar(lineText[end]) {
		end++
	}

	if start == end {
		return ""
	}
	return lineText[start:end]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
