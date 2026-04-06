package lsp

import (
	"context"
	"fmt"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

func (h *GalaHandler) Completion(ctx context.Context, params *lsp.CompletionParams) (*lsp.CompletionList, error) {
	uri := string(params.TextDocument.URI)
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	h.mu.Unlock()

	var items []lsp.CompletionItem

	isDot := isDotCompletion(text, line, char)

	if isDot && richAST != nil {
		// Resolve the type of the expression before the dot
		receiverType := typeAtDot(text, line, char, richAST)
		if receiverType != "" {
			items = append(items, typeSpecificCompletions(richAST, receiverType)...)
		} else {
			// Fallback: suggest all methods
			items = append(items, methodCompletions(richAST)...)
		}
	} else if isNamedArgContext(text, line, char) && richAST != nil {
		typeName := extractConstructorName(text, line, char)
		items = append(items, namedArgCompletions(richAST, typeName)...)
	} else if isMatchCaseContext(text, line, char) && richAST != nil {
		items = append(items, matchCaseCompletions(richAST)...)
	} else {
		if richAST != nil {
			items = append(items, typeCompletions(richAST)...)
			items = append(items, functionCompletions(richAST)...)
		}
		items = append(items, keywordCompletions()...)
	}

	return &lsp.CompletionList{IsIncomplete: false, Items: items}, nil
}

func isDotCompletion(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	i := char - 1
	for i >= 0 && i < len(l) && (l[i] == ' ' || isIdentChar(l[i])) {
		i--
	}
	return i >= 0 && l[i] == '.'
}

func typeCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	seen := make(map[string]bool)
	for key, tm := range richAST.Types {
		name := tm.Name
		if name == "" {
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
		kind := lsp.CompletionItemKindClass
		detail := "type"
		if tm.IsSealed {
			detail = "sealed type"
		}
		items = append(items, lsp.CompletionItem{Label: name, Kind: kindPtr(kind), Detail: detail})

		for _, v := range tm.SealedVariants {
			if !seen[v.Name] {
				seen[v.Name] = true
				items = append(items, lsp.CompletionItem{
					Label:  v.Name,
					Kind:   kindPtr(lsp.CompletionItemKindConstructor),
					Detail: "case of " + name,
				})
			}
		}
	}
	return items
}

func functionCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	for _, fm := range richAST.Functions {
		if !isExported(fm.Name) {
			continue
		}
		sig := formatFuncSig(fm)
		items = append(items, lsp.CompletionItem{
			Label:  fm.Name,
			Kind:   kindPtr(lsp.CompletionItemKindFunction),
			Detail: sig,
		})
	}
	return items
}

func methodCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	seen := make(map[string]bool)
	for _, tm := range richAST.Types {
		for name, m := range tm.Methods {
			if !isExported(name) || seen[name] {
				continue
			}
			seen[name] = true
			sig := formatMethodSig(m)
			items = append(items, lsp.CompletionItem{
				Label:  name,
				Kind:   kindPtr(lsp.CompletionItemKindMethod),
				Detail: sig,
			})
		}
	}
	return items
}

func keywordCompletions() []lsp.CompletionItem {
	keywords := []string{
		"package", "import", "val", "var", "func", "type", "struct",
		"interface", "sealed", "embed", "if", "else", "for", "range",
		"return", "match", "case", "true", "false", "nil", "map",
	}
	builtinFuncs := []string{
		"Println", "Print", "SliceOf",
		"len", "cap", "make", "append", "copy", "delete",
		"close", "panic", "recover",
	}
	var items []lsp.CompletionItem
	for _, kw := range keywords {
		items = append(items, lsp.CompletionItem{Label: kw, Kind: kindPtr(lsp.CompletionItemKindKeyword)})
	}
	for _, fn := range builtinFuncs {
		items = append(items, lsp.CompletionItem{Label: fn, Kind: kindPtr(lsp.CompletionItemKindFunction), Detail: "builtin"})
	}
	return items
}

// --- Named Arg Completion ---

func isNamedArgContext(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	depth := 0
	for i := char - 1; i >= 0; i-- {
		if l[i] == ')' {
			depth++
		} else if l[i] == '(' {
			if depth == 0 {
				j := i - 1
				for j >= 0 && (isIdentChar(l[j]) || l[j] == '[' || l[j] == ']') {
					j--
				}
				name := l[j+1 : i]
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

func extractConstructorName(text string, line, char int) string {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return ""
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	depth := 0
	for i := char - 1; i >= 0; i-- {
		if l[i] == ')' {
			depth++
		} else if l[i] == '(' {
			if depth == 0 {
				j := i - 1
				for j >= 0 && (isIdentChar(l[j]) || l[j] == '[' || l[j] == ']') {
					j--
				}
				name := l[j+1 : i]
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

func namedArgCompletions(richAST *transpiler.RichAST, typeName string) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	if typeName == "" {
		return items
	}
	for key, tm := range richAST.Types {
		name := tm.Name
		if name == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				name = key[idx+1:]
			}
		}
		if name != typeName {
			continue
		}
		for _, fn := range tm.FieldNames {
			ft := tm.Fields[fn]
			insertText := fn + " = "
			items = append(items, lsp.CompletionItem{
				Label:      fn,
				Kind:       kindPtr(lsp.CompletionItemKindField),
				Detail:     ft.String(),
				InsertText: insertText,
			})
		}
		break
	}
	return items
}

// --- Match Case Completion ---

func isMatchCaseContext(text string, line, _ int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[line]), "case ")
}

func matchCaseCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	seen := make(map[string]bool)
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for _, v := range tm.SealedVariants {
			if seen[v.Name] {
				continue
			}
			seen[v.Name] = true
			var insertText string
			if len(v.FieldNames) > 0 {
				insertText = v.Name + "(" + strings.Join(v.FieldNames, ", ") + ") => "
			} else {
				insertText = v.Name + "() => "
			}
			items = append(items, lsp.CompletionItem{
				Label:      v.Name,
				Kind:       kindPtr(lsp.CompletionItemKindEnumMember),
				Detail:     "case of " + tm.Name,
				InsertText: insertText,
			})
		}
	}
	wildcard := "_ => "
	items = append(items, lsp.CompletionItem{
		Label:      "_",
		Kind:       kindPtr(lsp.CompletionItemKindKeyword),
		InsertText: wildcard,
	})
	return items
}

// --- Helpers ---

func formatFuncSig(meta *transpiler.FunctionMetadata) string {
	var b strings.Builder
	b.WriteString("func(")
	for i, name := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(fmt.Sprintf("%s %s", name, meta.ParamTypes[i]))
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	return b.String()
}

func kindPtr(k lsp.CompletionItemKind) *lsp.CompletionItemKind { return &k }

// --- Type-Aware Completion ---
// Type resolution logic is in typeatpos.go

// typeSpecificCompletions returns methods and fields for a specific type.
func typeSpecificCompletions(richAST *transpiler.RichAST, typeName string) []lsp.CompletionItem {
	var items []lsp.CompletionItem

	tm := findType(richAST, typeName)
	if tm == nil {
		return items
	}

	// Methods
	for name, m := range tm.Methods {
		if !isExported(name) {
			continue
		}
		sig := formatMethodSig(m)
		items = append(items, lsp.CompletionItem{
			Label:  name,
			Kind:   kindPtr(lsp.CompletionItemKindMethod),
			Detail: sig,
		})
	}

	// Fields
	for _, fn := range tm.FieldNames {
		ft := tm.Fields[fn]
		items = append(items, lsp.CompletionItem{
			Label:  fn,
			Kind:   kindPtr(lsp.CompletionItemKindField),
			Detail: ft.String(),
		})
	}

	// Sealed variant IsXxx() methods
	for _, v := range tm.SealedVariants {
		items = append(items, lsp.CompletionItem{
			Label:  "Is" + v.Name,
			Kind:   kindPtr(lsp.CompletionItemKindMethod),
			Detail: "() bool",
		})
	}

	// Always suggest match
	items = append(items, lsp.CompletionItem{
		Label:  "match",
		Kind:   kindPtr(lsp.CompletionItemKindKeyword),
		Detail: "pattern match",
	})

	return items
}

func formatMethodSig(meta *transpiler.MethodMetadata) string {
	var b strings.Builder
	b.WriteString("(")
	for i, name := range meta.ParamNames {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(meta.ParamTypes) {
			b.WriteString(fmt.Sprintf("%s %s", name, meta.ParamTypes[i]))
		}
	}
	b.WriteString(")")
	if meta.ReturnType != nil && !meta.ReturnType.IsNil() {
		b.WriteString(" " + meta.ReturnType.String())
	}
	return b.String()
}
