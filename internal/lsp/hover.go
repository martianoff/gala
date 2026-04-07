package lsp

import (
	"context"
	"fmt"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

func (h *GalaHandler) Hover(ctx context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	h.mu.Unlock()

	if text == "" || richAST == nil {
		return nil, nil
	}

	word := wordAtPosition(text, int(params.Position.Line), int(params.Position.Character))
	if word == "" {
		return nil, nil
	}

	info := lookupSymbol(richAST, word)
	if info == "" {
		return nil, nil
	}

	return &lsp.Hover{
		Contents: lsp.MarkupContent{
			Kind:  lsp.Markdown,
			Value: info,
		},
	}, nil
}

func lookupSymbol(richAST *transpiler.RichAST, name string) string {
	if typeMeta, ok := richAST.Types[name]; ok {
		return formatTypeMeta(typeMeta)
	}
	for key, typeMeta := range richAST.Types {
		if strings.HasSuffix(key, "."+name) {
			return formatTypeMeta(typeMeta)
		}
	}
	if funcMeta, ok := richAST.Functions[name]; ok {
		return formatFuncMeta(funcMeta)
	}
	if companion, ok := richAST.CompanionObjects[name]; ok {
		return fmt.Sprintf("```gala\n%s — sealed case constructor\n```\n", companion.Name)
	}

	// Built-in functions
	if info, ok := builtinFuncDocs[name]; ok {
		return info
	}

	return ""
}

var builtinFuncDocs = map[string]string{
	"Println": "```gala\nPrintln(args ...any)\n```\n\n*Built-in* — prints arguments followed by a newline. Rewritten to `fmt.Println`.\n",
	"Print":   "```gala\nPrint(args ...any)\n```\n\n*Built-in* — prints arguments. Rewritten to `fmt.Print`.\n",
	"SliceOf": "```gala\nSliceOf[T](elems ...T) []T\n```\n\n*Built-in* — creates a Go slice from the given elements.\n",
	"len":     "```gala\nlen(v) int\n```\n\n*Go built-in* — returns the length of a string, slice, array, map, or channel.\n",
	"cap":     "```gala\ncap(v) int\n```\n\n*Go built-in* — returns the capacity of a slice or channel.\n",
	"make":    "```gala\nmake(T, size ...int) T\n```\n\n*Go built-in* — allocates and initializes a slice, map, or channel.\n",
	"append":  "```gala\nappend(slice []T, elems ...T) []T\n```\n\n*Go built-in* — appends elements to a slice.\n",
	"copy":    "```gala\ncopy(dst []T, src []T) int\n```\n\n*Go built-in* — copies elements from src to dst slice.\n",
	"delete":  "```gala\ndelete(m map[K]V, key K)\n```\n\n*Go built-in* — deletes a key from a map.\n",
	"close":   "```gala\nclose(ch chan T)\n```\n\n*Go built-in* — closes a channel.\n",
	"panic":   "```gala\npanic(v any)\n```\n\n*Go built-in* — stops normal execution and begins panicking.\n",
	"recover": "```gala\nrecover() any\n```\n\n*Go built-in* — regains control of a panicking goroutine.\n",
}

func formatTypeMeta(meta *transpiler.TypeMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\n")
	if meta.IsSealed {
		b.WriteString("sealed type " + meta.Name)
	} else {
		b.WriteString("type " + meta.Name)
	}
	if len(meta.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(meta.TypeParams, ", ") + "]")
	}
	b.WriteString("\n```\n")

	if len(meta.FieldNames) > 0 {
		b.WriteString("\n**Fields:**\n")
		for i, fn := range meta.FieldNames {
			ft := meta.Fields[fn]
			mut := "val"
			if i < len(meta.ImmutFlags) && !meta.ImmutFlags[i] {
				mut = "var"
			}
			b.WriteString(fmt.Sprintf("- `%s %s %s`\n", mut, fn, ft))
		}
	}
	if len(meta.SealedVariants) > 0 {
		b.WriteString("\n**Cases:**\n")
		for _, v := range meta.SealedVariants {
			if len(v.FieldNames) > 0 {
				var fields []string
				for j, fn := range v.FieldNames {
					if j < len(v.FieldTypes) {
						fields = append(fields, fn+" "+v.FieldTypes[j].String())
					} else {
						fields = append(fields, fn)
					}
				}
				b.WriteString(fmt.Sprintf("- `case %s(%s)`\n", v.Name, strings.Join(fields, ", ")))
			} else {
				b.WriteString(fmt.Sprintf("- `case %s()`\n", v.Name))
			}
		}
	}
	if len(meta.Methods) > 0 {
		b.WriteString("\n**Methods:**\n")
		for name, m := range meta.Methods {
			b.WriteString(fmt.Sprintf("- `%s(%s) %s`\n", name, formatMethodParams(m), m.ReturnType))
		}
	}
	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}
	return b.String()
}

func formatFuncMeta(meta *transpiler.FunctionMetadata) string {
	var b strings.Builder
	b.WriteString("```gala\nfunc " + meta.Name)
	if len(meta.TypeParams) > 0 {
		b.WriteString("[" + strings.Join(meta.TypeParams, ", ") + "]")
	}
	b.WriteString("(")
	for i, pn := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(pn + " " + meta.ParamTypes[i].String())
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	b.WriteString("\n```\n")
	if meta.Package != "" {
		b.WriteString(fmt.Sprintf("\n*Package: %s*\n", meta.Package))
	}
	return b.String()
}

func formatMethodParams(m *transpiler.MethodMetadata) string {
	var parts []string
	for i, name := range m.ParamNames {
		if i < len(m.ParamTypes) {
			parts = append(parts, name+" "+m.ParamTypes[i].String())
		}
	}
	return strings.Join(parts, ", ")
}

func wordAtPosition(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	l := lines[line]
	if char >= len(l) {
		return ""
	}
	start, end := char, char
	for start > 0 && isIdentChar(l[start-1]) {
		start--
	}
	for end < len(l) && isIdentChar(l[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return l[start:end]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isExported(name string) bool {
	return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
}
