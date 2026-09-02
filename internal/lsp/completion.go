package lsp

import (
	"context"
	"fmt"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
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

	// Stamp the document once, here, rather than passing it into every completion
	// helper: it is identical for every item in the list, and a per-request value
	// belongs in the request handler.
	stampRefURI(items, uri)
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
		items = append(items, withRef(
			lsp.CompletionItem{Label: name, Kind: kindPtr(kind), Detail: detail},
			completionRef{Kind: refKindType, Key: key}))

		for _, v := range tm.SealedVariants {
			if !seen[v.Name] {
				seen[v.Name] = true
				items = append(items, withRef(lsp.CompletionItem{
					Label:  v.Name,
					Kind:   kindPtr(lsp.CompletionItemKindConstructor),
					Detail: "case of " + name,
				}, completionRef{Kind: refKindVariant, Key: key, Name: v.Name}))
			}
		}
	}
	return items
}

func functionCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	for fnKey, fm := range richAST.Functions {
		if !isExported(fm.Name) {
			continue
		}
		sig := formatFuncSig(fm)
		items = append(items, withRef(lsp.CompletionItem{
			Label:  fm.Name,
			Kind:   kindPtr(lsp.CompletionItemKindFunction),
			Detail: sig,
		}, completionRef{Kind: refKindFunc, Key: fnKey}))
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
			items = append(items, withRef(lsp.CompletionItem{
				Label:  name,
				Kind:   kindPtr(kind),
				Detail: "type from " + pkgName,
			}, completionRef{Kind: refKindType, Key: key}))
		}
	}

	// Functions from this package
	for fnKey, fm := range richAST.Functions {
		if fm.Package == pkgName && isExported(fm.Name) && !seen[fm.Name] {
			seen[fm.Name] = true
			sig := formatFuncSig(fm)
			items = append(items, withRef(lsp.CompletionItem{
				Label:  fm.Name,
				Kind:   kindPtr(lsp.CompletionItemKindFunction),
				Detail: sig,
			}, completionRef{Kind: refKindFunc, Key: fnKey}))
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
		"package", "import", "val", "var", "bind", "also", "use", "func", "type", "struct",
		"interface", "sealed", "embed", "if", "else", "for", "range",
		"return", "match", "case", "true", "false", "nil", "map",
	}
	// Only genuinely-available names belong here. The bare Go builtins
	// (len/append/make/…) are forbidden on GALA's surface (GALA-E0035), so
	// suggesting them would complete code the compiler rejects — use `.Size()`
	// / `.ByteSize()` (offered as method completions) or the go_interop
	// wrappers (ordinary symbols) instead.
	builtinFuncs := []string{
		"Println", "Print", "SliceOf",
		// Auto-imported std prelude constructors / converters
		// (see internal/transpiler/registry/std.go and std/*.gala).
		"NewImmutable", "NewConstPtr", "NewEmbeddedFS",
		"FromError", "FromOption", "FromEitherError",
	}
	items := make([]lsp.CompletionItem, 0)
	for _, kw := range keywords {
		items = append(items, lsp.CompletionItem{Label: kw, Kind: kindPtr(lsp.CompletionItemKindKeyword)})
	}
	// Defensive filter against the authoritative forbidden set so this list can
	// never drift back into suggesting an E0035 builtin.
	forbidden := transformer.ForbiddenGoBuiltins()
	for _, fn := range builtinFuncs {
		if forbidden[fn] {
			continue
		}
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
			items = append(items, withRef(lsp.CompletionItem{
				Label:      fn,
				Kind:       kindPtr(lsp.CompletionItemKindField),
				Detail:     ft.String(),
				InsertText: insertText,
			}, completionRef{Kind: refKindMember, Key: key, Name: fn}))
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
	for key, tm := range richAST.Types {
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
			items = append(items, withRef(lsp.CompletionItem{
				Label:      v.Name,
				Kind:       kindPtr(lsp.CompletionItemKindEnumMember),
				Detail:     "case of " + tm.Name,
				InsertText: insertText,
			}, completionRef{Kind: refKindVariant, Key: key, Name: v.Name}))
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

// goSizeSugarCompletions returns the `.Size()` / `.ByteSize()` completion items
// that apply to a Go primitive receiver (string, slice, or map). `ByteSize()` is
// only offered on strings. Mirrors the transpiler's tryTransformSizeSugar
// (internal/transpiler/transformer/methods.go). These are surfaced even though
// the receiver has no GALA TypeMetadata, because the transpiler lowers them to
// len(...) / utf8.RuneCountInString(...) at compile time.
func goSizeSugarCompletions(typeName string) []lsp.CompletionItem {
	if !isGoSizeableType(typeName) {
		return nil
	}
	items := []lsp.CompletionItem{{
		Label:      "Size()",
		Kind:       kindPtr(lsp.CompletionItemKindMethod),
		Detail:     "() int",
		InsertText: "Size()",
		FilterText: "Size",
		SortText:   "Size",
	}}
	if typeName == "string" {
		items = append(items, lsp.CompletionItem{
			Label:      "ByteSize()",
			Kind:       kindPtr(lsp.CompletionItemKindMethod),
			Detail:     "() int",
			InsertText: "ByteSize()",
			FilterText: "ByteSize",
			SortText:   "ByteSize",
		})
	}
	return items
}

// typeSpecificCompletions returns methods and fields for a specific type.
func typeSpecificCompletions(richAST *transpiler.RichAST, typeName string) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)

	// GALA's `.Size()` / `.ByteSize()` sugar is available on Go primitive
	// receivers (string/slice/map) that have no GALA TypeMetadata. Offer it up
	// front so it shows up even when findType below returns nil.
	items = append(items, goSizeSugarCompletions(typeName)...)

	tm := findType(richAST, typeName)
	// The owner's own map key, so resolve looks the type up exactly rather than
	// repeating findType's cross-package simple-name search — which runs in Go
	// map order and could hand back a same-named type from another package.
	ownerKey := typeKey(tm)
	if tm == nil {
		// No GALA TypeMetadata — the receiver may be a Go type (e.g. a value
		// returned from an imported Go package like bytes.Buffer or hash.Hash).
		// Surface its method set from the analyzer's Go type info.
		items = append(items, goTypeCompletions(richAST, typeName)...)
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
		items = append(items, withRef(lsp.CompletionItem{
			Label:      name + sig,
			Kind:       kindPtr(lsp.CompletionItemKindMethod),
			Detail:     sig,
			InsertText: insertText,
			FilterText: name,
			SortText:   name,
		}, completionRef{Kind: refKindMember, Key: ownerKey, Name: name}))
	}

	// Fields
	for _, fn := range tm.FieldNames {
		ft := tm.Fields[fn]
		items = append(items, withRef(lsp.CompletionItem{
			Label:  fn,
			Kind:   kindPtr(lsp.CompletionItemKindField),
			Detail: ft.String(),
		}, completionRef{Kind: refKindMember, Key: ownerKey, Name: fn}))
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

// goTypeCompletions surfaces the method set and fields of a Go type (struct or
// interface) recorded in richAST.GoTypeInfo. It backs dot completion on values
// whose type comes from an imported Go package and therefore has no GALA
// TypeMetadata — e.g. `val b = bytes.NewBufferString("x"); b.` or a hash.Hash.
func goTypeCompletions(richAST *transpiler.RichAST, typeName string) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	gi := richAST.GoTypeInfo
	if gi == nil {
		return items
	}

	// Normalize the receiver type to the "pkg.Name" key used by GoTypeInfo:
	// drop a leading pointer star and any generic arguments.
	key := stripTypeParams(strings.TrimPrefix(strings.TrimSpace(typeName), "*"))

	td := gi.Types[key]
	if td == nil {
		// Follow a single Go type-alias hop (type X = Y).
		if under := gi.TypeAliases[key]; under != nil {
			td = gi.Types[stripTypeParams(strings.TrimPrefix(under.String(), "*"))]
		}
	}
	if td == nil {
		return items
	}

	seen := make(map[string]bool, len(td.Methods))
	for name, sig := range td.Methods {
		if !isExported(name) || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, goMethodCompletion(name, sig))
	}
	for _, fn := range td.FieldOrder {
		if !isExported(fn) {
			continue
		}
		items = append(items, lsp.CompletionItem{
			Label:  fn,
			Kind:   kindPtr(lsp.CompletionItemKindField),
			Detail: goTypeString(td.Fields[fn]),
		})
	}
	return items
}

// goMethodCompletion builds a completion item for a Go method from its
// signature, mirroring the parenthesis-insertion behavior of GALA methods.
func goMethodCompletion(name string, sig *transpiler.GoFuncSignature) lsp.CompletionItem {
	label := name + goFuncSigString(sig)
	insertText := name + "("
	if sig == nil || len(sig.Params) == 0 {
		insertText = name + "()"
	}
	return lsp.CompletionItem{
		Label:      label,
		Kind:       kindPtr(lsp.CompletionItemKindMethod),
		Detail:     goFuncSigString(sig),
		InsertText: insertText,
		FilterText: name,
		SortText:   name,
	}
}

// goFuncSigString renders a Go function/method signature like "(w []byte) int".
func goFuncSigString(sig *transpiler.GoFuncSignature) string {
	if sig == nil {
		return "()"
	}
	var b strings.Builder
	b.WriteString("(")
	for i, p := range sig.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if p.Name != "" {
			b.WriteString(p.Name)
			b.WriteString(" ")
		}
		b.WriteString(goTypeString(p.Type))
	}
	b.WriteString(")")
	switch len(sig.Returns) {
	case 0:
	case 1:
		b.WriteString(" " + goTypeString(sig.Returns[0]))
	default:
		b.WriteString(" (")
		for i, r := range sig.Returns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(goTypeString(r))
		}
		b.WriteString(")")
	}
	return b.String()
}

// goTypeString renders a Go type for display, tolerating a nil type.
func goTypeString(t transpiler.Type) string {
	if t == nil || t.IsNil() {
		return "any"
	}
	return t.String()
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
