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
	varTypeMap := h.varTypes[uri]
	h.mu.Unlock()

	items := make([]lsp.CompletionItem, 0)

	isDot := isDotCompletion(text, line, char)

	// If no richAST or no varTypes are cached (file has syntax errors since
	// it was opened, or only partial analysis ran without the transformer),
	// use the cursor position to surgically remove the dot trigger, re-parse
	// the cleaned document, and cache the result. This is the "IntelliJ
	// trick" used by rust-analyzer — targeted at the known cursor position,
	// not a file-wide heuristic.
	if isDot && (richAST == nil || len(varTypeMap) == 0) {
		h.ensureAnalysis(uri, line, char)
		h.mu.Lock()
		richAST = h.richASTs[uri]
		varTypeMap = h.varTypes[uri]
		h.mu.Unlock()
	}

	if isDot && richAST != nil {
		receiverType := typeAtDot(text, line, char, richAST, varTypeMap)
		if strings.HasPrefix(receiverType, packagePrefix) {
			// Package dot completion — show types and functions from that package
			pkgName := strings.TrimPrefix(receiverType, packagePrefix)
			items = append(items, packageCompletions(richAST, pkgName)...)
		} else if receiverType != "" {
			items = append(items, typeSpecificCompletions(richAST, receiverType)...)
		}
	} else if isNamedArgContext(text, line, char) && richAST != nil {
		typeName := extractConstructorName(text, line, char)
		items = append(items, namedArgCompletions(richAST, typeName)...)
	} else if isMatchCaseContext(text, line, char) && richAST != nil {
		matchedType := extractMatchSubjectType(text, line, richAST, varTypeMap)
		items = append(items, matchCaseCompletions(richAST, matchedType)...)
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
	if char <= 0 {
		return false
	}

	// Direct check: is the char right before cursor a dot?
	if l[char-1] == '.' {
		return true
	}

	// Walk backwards past any partial identifier being typed (user typing after dot)
	i := char - 1
	for i >= 0 && isIdentChar(l[i]) {
		i--
	}
	if i >= 0 && l[i] == '.' {
		return true
	}

	return false
}

func typeCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
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
	items := make([]lsp.CompletionItem, 0)
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

// packageCompletions returns exported types and functions from a specific package.
func packageCompletions(richAST *transpiler.RichAST, pkgName string) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	seen := make(map[string]bool)

	// Types from this package
	for key, tm := range richAST.Types {
		name := tm.Name
		if name == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				name = key[idx+1:]
			}
		}
		if !isExported(name) || seen[name] {
			continue
		}
		// Check if this type belongs to the package
		if tm.Package == pkgName || strings.HasPrefix(key, pkgName+".") {
			seen[name] = true
			kind := lsp.CompletionItemKindClass
			items = append(items, lsp.CompletionItem{
				Label:  name,
				Kind:   kindPtr(kind),
				Detail: "type from " + pkgName,
			})
		}
	}

	// Functions from this package
	for _, fm := range richAST.Functions {
		if fm.Package == pkgName && isExported(fm.Name) && !seen[fm.Name] {
			seen[fm.Name] = true
			sig := formatFuncSig(fm)
			items = append(items, lsp.CompletionItem{
				Label:  fm.Name,
				Kind:   kindPtr(lsp.CompletionItemKindFunction),
				Detail: sig,
			})
		}
	}

	// GoExports from this package (Go-only packages)
	if exports, ok := richAST.GoExports[pkgName]; ok {
		for _, name := range exports {
			if !seen[name] && isExported(name) {
				seen[name] = true
				items = append(items, lsp.CompletionItem{
					Label: name,
					Kind:  kindPtr(lsp.CompletionItemKindVariable),
				})
			}
		}
	}

	return items
}

func methodCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	seen := make(map[string]bool)
	for _, tm := range richAST.Types {
		for name, m := range tm.Methods {
			if !isExported(name) || seen[name] {
				continue
			}
			seen[name] = true
			sig := formatMethodSig(m)
			var insertText string
			if len(m.ParamNames) == 0 {
				insertText = name + "()"
			} else {
				insertText = name + "("
			}
			items = append(items, lsp.CompletionItem{
				Label:      name + sig,
				Kind:       kindPtr(lsp.CompletionItemKindMethod),
				Detail:     sig,
				InsertText: insertText,
				FilterText: name,
				SortText:   name,
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
	items := make([]lsp.CompletionItem, 0)
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
	items := make([]lsp.CompletionItem, 0)
	if typeName == "" {
		return items
	}

	// Check regular struct fields
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
		if len(items) > 0 {
			return items
		}
	}

	// Check sealed case fields — e.g., Circle(radius = ...)
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		for _, v := range tm.SealedVariants {
			if v.Name == typeName {
				for i, fn := range v.FieldNames {
					detail := ""
					if i < len(v.FieldTypes) {
						detail = v.FieldTypes[i].String()
					}
					insertText := fn + " = "
					items = append(items, lsp.CompletionItem{
						Label:      fn,
						Kind:       kindPtr(lsp.CompletionItemKindField),
						Detail:     cleanGoTypeForDisplay(detail),
						InsertText: insertText,
					})
				}
				return items
			}
		}
	}

	return items
}

// extractMatchSubjectType finds the type of the match subject by scanning upward
// for "expr match {" and resolving the expression's type.
func extractMatchSubjectType(text string, caseLine int, richAST *transpiler.RichAST, varTypes map[string]string) string {
	lines := strings.Split(text, "\n")
	enclosingFunc := findEnclosingFunc(lines, caseLine)
	for i := caseLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if idx := strings.Index(trimmed, "match {"); idx >= 0 {
			subject := strings.TrimSpace(trimmed[:idx])
			if eqIdx := strings.LastIndex(subject, "="); eqIdx >= 0 {
				subject = strings.TrimSpace(subject[eqIdx+1:])
			}
			if subject != "" {
				return resolveReceiverType(subject, enclosingFunc, richAST, varTypes)
			}
		}
		// Stop searching at function boundaries
		if strings.HasPrefix(trimmed, "func ") {
			break
		}
	}
	return ""
}

// --- Match Case Completion ---

func isMatchCaseContext(text string, line, _ int) bool {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[line])
	return strings.HasPrefix(trimmed, "case ") || trimmed == "case"
}

func matchCaseCompletions(richAST *transpiler.RichAST, matchedType string) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	seen := make(map[string]bool)
	for _, tm := range richAST.Types {
		if !tm.IsSealed {
			continue
		}
		// If we know the matched type, only show its variants
		if matchedType != "" && tm.Name != matchedType && tm.Name != stripTypeParams(matchedType) {
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
	items := make([]lsp.CompletionItem, 0)

	tm := findType(richAST, typeName)
	if tm == nil {
		return items
	}

	// Methods — show all methods (including unexported for same-package types)
	for name, m := range tm.Methods {
		sig := formatMethodSig(m)
		// Auto-insert parentheses
		var insertText string
		if len(m.ParamNames) == 0 {
			insertText = name + "()"
		} else {
			insertText = name + "("
		}
		items = append(items, lsp.CompletionItem{
			Label:      name + sig,
			Kind:       kindPtr(lsp.CompletionItemKindMethod),
			Detail:     sig,
			InsertText: insertText,
			FilterText: name,
			SortText:   name,
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
