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

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypeMap := h.varTypes[uri]
	snippets := h.snippetSupport
	h.mu.Unlock()

	line, char := h.index(text).toByte(params.Position)

	items := make([]lsp.CompletionItem, 0)
	incomplete := false

	isDot := isDotCompletion(text, line, char)

	// If no richAST or no varTypes are cached (file has syntax errors since
	// it was opened, or only partial analysis ran without the transformer),
	// use the cursor position to surgically remove the dot trigger, re-parse
	// the cleaned document, and cache the result. This is the "IntelliJ
	// trick" used by rust-analyzer — targeted at the known cursor position,
	// not a file-wide heuristic.
	if isDot && (richAST == nil || !hasResolvedVarType(varTypeMap)) {
		h.ensureAnalysis(uri, line, char)
		h.mu.Lock()
		richAST = h.richASTs[uri]
		varTypeMap = h.varTypes[uri]
		h.mu.Unlock()
	}

	// The parameters of the call whose next argument the caret begins.
	var paramItems []lsp.CompletionItem
	typed := ""
	if !isDot && richAST != nil {
		if call, prefix := h.callAtArgumentStart(uri, text, line, char); call != nil {
			enclosingFunc := findEnclosingFunc(strings.Split(text, "\n"), line)
			paramItems = parameterCompletions(call, resolveCallTarget(call, enclosingFunc, richAST, varTypeMap))
			typed = prefix
		}
	}

	if isDot && richAST != nil {
		receiverType := typeAtDot(text, line, char, richAST, varTypeMap)
		if strings.HasPrefix(receiverType, packagePrefix) {
			// Package dot completion — show types and functions from that package
			pkgName := strings.TrimPrefix(receiverType, packagePrefix)
			items = append(items, packageCompletions(richAST, pkgName, snippets)...)
		} else if receiverType != "" {
			items = append(items, typeSpecificCompletions(richAST, receiverType, snippets)...)
		}
	} else if len(paramItems) > 0 {
		items = append(items, paramItems...)
		if typed == "" {
			// Nothing typed yet, so the parameters are the whole answer. The
			// list is marked incomplete so that the first character typed asks
			// again, and a positional argument still completes.
			incomplete = true
		} else {
			items = append(items, globalCompletions(richAST, varTypeMap, snippets)...)
		}
	} else if isMatchCaseContext(text, line, char) && richAST != nil {
		matchedType := extractMatchSubjectType(text, line, richAST, varTypeMap)
		items = append(items, matchCaseCompletions(richAST, matchedType)...)
	} else {
		items = append(items, globalCompletions(richAST, varTypeMap, snippets)...)
	}

	// Stamp the document once, here, rather than passing it into every completion
	// helper: it is identical for every item in the list, and a per-request value
	// belongs in the request handler.
	stampRefURI(items, uri)
	return &lsp.CompletionList{IsIncomplete: incomplete, Items: items}, nil
}

// globalCompletions is what completes where no receiver or narrower context
// applies: the types, functions and package-level bindings in scope, and the
// keywords.
func globalCompletions(richAST *transpiler.RichAST, varTypeMap map[string]string, snippets bool) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	if richAST != nil {
		items = append(items, typeCompletions(richAST)...)
		items = append(items, functionCompletions(richAST, snippets)...)
		items = append(items, packageValCompletions(richAST, varTypeMap)...)
	}
	return append(items, keywordCompletions()...)
}

// isDotCompletion reports whether the cursor is completing a member of
// something — that is, whether a dot selects what is being typed.
//
// The dot is looked for on the FLATTENED expression, not the cursor's line. A
// builder chain is written one call per line with the dot trailing the previous
// line, so the moment the user presses Enter to continue the chain, a
// line-local check stopped seeing it and completion answered with the global
// type and keyword list instead of the receiver's methods. The same predicate
// answers for hover and go-to-definition.
func isDotCompletion(text string, line, char int) bool {
	_, ok := memberAccessPrefix(strings.Split(text, "\n"), line, char)
	return ok
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
		switch {
		case tm.IsSealed:
			detail = "sealed type"
		case tm.IsOpaque:
			detail = "opaque type"
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

func functionCompletions(richAST *transpiler.RichAST, snippets bool) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	for fnKey, fm := range richAST.Functions {
		if !isExported(fm.Name) {
			continue
		}
		items = append(items, withRef(functionItem(fm, snippets), completionRef{Kind: refKindFunc, Key: fnKey}))
	}
	return items
}

// functionItem is the completion item for calling a GALA function.
//
// Only a snippet client gets insert text: it completes the whole call. Without
// snippets the bare name is inserted, as before — a function is also a value,
// passed as a callback without a call.
func functionItem(fm *transpiler.FunctionMetadata, snippets bool) lsp.CompletionItem {
	item := lsp.CompletionItem{
		Label:  fm.Name,
		Kind:   kindPtr(lsp.CompletionItemKindFunction),
		Detail: formatFuncSig(fm),
	}
	if snippets {
		item.InsertText, item.InsertTextFormat = callInsertText(fm.Name, fm.ParamNames, fm.DefaultExprs, snippets)
	}
	return item
}

// packageValCompletions offers the package's own val/var bindings.
//
// Unexported ones are included, unlike types and functions: this list is
// offered inside the package that declares them, where a lowercase binding is
// exactly as referenceable as an uppercase one.
func packageValCompletions(richAST *transpiler.RichAST, varTypeMap map[string]string) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0, len(richAST.PackageVals))
	for name, pv := range richAST.PackageVals {
		// Same order as hover: the analyzer's own record, then the
		// transformer's channel for everything it has no type for.
		typeName := packageValType(pv)
		if typeName == "" {
			typeName = lookupVarType(varTypeMap, "", name)
		}
		detail := packageValSignature(pv, typeName)
		items = append(items, withRef(lsp.CompletionItem{
			Label:  name,
			Kind:   kindPtr(lsp.CompletionItemKindVariable),
			Detail: detail,
		}, completionRef{Kind: refKindPackageVal, Key: name}))
	}
	return items
}

// packageCompletions returns exported types and functions from a specific package.
func packageCompletions(richAST *transpiler.RichAST, pkgName string, snippets bool) []lsp.CompletionItem {
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
			items = append(items, withRef(functionItem(fm, snippets), completionRef{Kind: refKindFunc, Key: fnKey}))
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

func keywordCompletions() []lsp.CompletionItem {
	keywords := []string{
		"package", "import", "val", "var", "bind", "also", "use", "func", "type", "struct",
		"interface", "sealed", "default", "opaque", "embed", "if", "else", "for", "range",
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
		"FromError", "FromOption", "FromEitherError", "PanicStack",
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
	b.WriteString(resultSuffix(meta.ReturnType, meta.GoResults))
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
func typeSpecificCompletions(richAST *transpiler.RichAST, typeName string, snippets bool) []lsp.CompletionItem {
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
		items = append(items, withRef(methodCompletion(name, m, "", snippets),
			completionRef{Kind: refKindMember, Key: ownerKey, Name: name}))
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

	// Hash / Compare synthesized on an opaque type. No resolve ref: there is
	// no declaration, and so no doc comment, to resolve.
	for _, m := range tm.SynthesizedOpaqueMethods(richAST) {
		items = append(items, methodCompletion(m.Name, m, " (synthesized)", snippets))
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
	if results := goResultsDisplay(sig.Returns); results != "" {
		b.WriteString(" " + results)
	}
	return b.String()
}

// goResultsDisplay renders a Go signature's results as a GALA call of it
// yields them: one result as itself, several as the one GALA value they
// become — `([]byte, error)` as `Try[[]byte]`, `(string, string, bool)` as
// `Tuple3[string, string, bool]` (see transpiler.GoResultValueOf). Results
// whose GALA value cannot be named keep Go's `(A, B, …)` form.
func goResultsDisplay(returns []transpiler.Type) string {
	switch len(returns) {
	case 0:
		return ""
	case 1:
		return goTypeString(returns[0])
	}
	if v, ok := transpiler.GoResultValueOf(returns); ok && !v.Type.IsNil() {
		return cleanGoTypeForDisplay(v.Type.String())
	}
	return goResultsTuple(returns)
}

// goResultsTuple renders Go results the way Go writes them: `([]byte, error)`.
func goResultsTuple(returns []transpiler.Type) string {
	parts := make([]string, len(returns))
	for i, r := range returns {
		parts[i] = goTypeString(r)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// goTypeString renders a Go type for display, tolerating a nil type.
func goTypeString(t transpiler.Type) string {
	if t == nil || t.IsNil() {
		return "any"
	}
	return t.String()
}

// methodCompletion is the completion item for method name; detailSuffix is
// appended to the signature shown beside it.
func methodCompletion(name string, m *transpiler.MethodMetadata, detailSuffix string, snippets bool) lsp.CompletionItem {
	sig := formatMethodSig(m)
	insertText, format := callInsertText(name, m.ParamNames, m.DefaultExprs, snippets)
	return lsp.CompletionItem{
		Label:            name + sig,
		Kind:             kindPtr(lsp.CompletionItemKindMethod),
		Detail:           sig + detailSuffix,
		InsertText:       insertText,
		InsertTextFormat: format,
		FilterText:       name,
		SortText:         name,
	}
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
	b.WriteString(resultSuffix(meta.ReturnType, meta.GoResults))
	return b.String()
}
