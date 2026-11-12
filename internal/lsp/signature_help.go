package lsp

import (
	"context"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// SignatureHelp implements textDocument/signatureHelp.
//
// Locates the call expression at the cursor by walking the native parse
// tree — not by byte-level scans — then extracts the callee name,
// receiver chain, and argument index structurally from the tree. The
// tree is parsed from a version of the source where the unclosed call
// has been surgically closed with `)` (see ensureAnalysisForSignature)
// so the parser can actually produce a well-formed postfixExpr.
func (h *GalaHandler) SignatureHelp(ctx context.Context, params *lsp.SignatureHelpParams) (*lsp.SignatureHelp, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypeMap := h.varTypes[uri]
	h.mu.Unlock()

	if text == "" {
		return nil, nil
	}
	line, char := h.index(text).toByte(params.Position)

	// Ensure richAST + varTypes are available for signature resolution.
	// This may short-circuit if the main DidChange pipeline already
	// produced them (from a lenient/partial parse), or it may parse a
	// surgically closed variant of the source.
	if richAST == nil || !hasResolvedVarType(varTypeMap) {
		h.ensureAnalysisForSignature(uri, line, char)
		h.mu.Lock()
		richAST = h.richASTs[uri]
		varTypeMap = h.varTypes[uri]
		h.mu.Unlock()
	}
	if richAST == nil {
		return nil, nil
	}

	call := h.findCallAtCaret(uri, text, line, char)
	if call == nil {
		return nil, nil
	}

	lines := strings.Split(text, "\n")
	enclosingFunc := findEnclosingFunc(lines, line)

	sig := resolveCallSignature(call, enclosingFunc, richAST, varTypeMap)
	if sig == nil {
		return nil, nil
	}

	active := call.argIndex
	if active < 0 {
		active = 0
	}
	if len(sig.Parameters) > 0 && active >= len(sig.Parameters) {
		active = len(sig.Parameters) - 1
	}
	activePtr := active
	activeSig := 0

	return &lsp.SignatureHelp{
		Signatures:      []lsp.SignatureInformation{*sig},
		ActiveSignature: &activeSig,
		ActiveParameter: &activePtr,
	}, nil
}

// callContext captures everything the signature resolver needs about
// the enclosing call.
type callContext struct {
	// name is the callee identifier: either a trailing `.Method` in a
	// chain, or the primaryExpr's identifier for a bare call.
	name string
	// receiverChain, if non-empty, is the source text of the expression
	// that precedes `.name` — used for resolving the receiver type on
	// chained method calls.
	receiverChain string
	// argIndex is the zero-based index of the argument the cursor sits
	// in, counted from structural commas in the argumentList.
	argIndex int
	// argStarts holds the offsets of this call's own `(` and top-level
	// commas, in order — the points after which a new argument begins.
	argStarts []int
	// named holds the names of arguments already passed by name, and
	// positional counts the positional ones before the cursor; neither
	// includes the argument the cursor is in.
	named      map[string]bool
	positional int
}

// findCallAtCaret finds the call whose argument list holds the caret, in the
// document as patchTextForSignature leaves it.
//
// A native tree is reused when it was parsed from exactly that text: the one
// of the document's latest parse, or the one this parsed for itself last
// (signatureTrees); any other tree — another caret's patch, an older edit —
// would put the call at the wrong offsets. Without such a tree it parses src
// afresh, its error-recovered tree when src does not parse, and keeps that
// tree for the next request on src.
func (h *GalaHandler) findCallAtCaret(uri, text string, line, char int) *callContext {
	src, offset := patchTextForSignature(text, line, char)
	if offset < 0 {
		return nil
	}
	h.mu.Lock()
	var handle *parser.Native
	switch {
	case h.parseTexts[uri] == src:
		handle = h.parseNatives[uri]
	case h.signatureTrees[uri].text == src:
		handle = h.signatureTrees[uri].handle
	}
	h.mu.Unlock()
	if handle == nil {
		handle = h.parser.ParseFile(src).RecoveredNative().TreeOnly() // converts with ANTLR's engine: not under h.mu
		h.mu.Lock()
		if h.isCurrentLocked(uri, text) {
			h.signatureTrees[uri] = patchedTree{text: src, handle: handle}
		}
		h.mu.Unlock()
	}
	root, ok := handle.Root()
	if !ok {
		return nil
	}
	return findCallAtOffsetNative(root, offset)
}

// patchedTree is a native tree alone (Native.TreeOnly) with the text it was
// parsed from.
type patchedTree struct {
	text   string
	handle *parser.Native
}

// patchTextForSignature closes the call the user is typing with a `)` at the
// cursor when the document's parentheses do not balance, so the parser can produce
// the call. Returns the text and the cursor's byte offset in it.
//
// A balanced document is left alone: editors insert the `)` as the `(` is
// typed, and closing `WithName(|)` a second time breaks the parse.
//
// Everything on either side of the cursor is preserved verbatim — no
// whitespace or comma trimming — so that (a) the grammar's trailing
// comma in `argumentList` lets the parser accept `foo(a, )` cleanly
// and (b) the active-argument index still reflects every comma the
// user has typed.
func patchTextForSignature(text string, line, char int) (patched string, cursorOffset int) {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return "", -1
	}
	offset := lineCharToOffset(text, line, min(max(char, 0), len(lines[line])))
	if unclosedParens(text) <= 0 {
		return text, offset
	}
	return text[:offset] + ")" + text[offset:], offset
}

// unclosedParens is the number of `(` in text that no `)` closes, counted by
// token so that parentheses in comments, strings and char literals do not
// count.
func unclosedParens(text string) int {
	n := 0
	parser.VisitNativeTokens(text, func(tok string) {
		switch tok {
		case "(":
			n++
		case ")":
			n--
		}
	})
	return n
}

// lineCharToOffset converts an LSP (0-indexed line, 0-indexed char) to
// a byte offset into text. Returns -1 if the position is out of range.
func lineCharToOffset(text string, line, char int) int {
	offset := 0
	curLine := 0
	for i := 0; i < len(text); i++ {
		if curLine == line {
			return offset + char // allow char == len(line) (end of line)
		}
		if text[i] == '\n' {
			curLine++
			offset = i + 1
		}
	}
	if curLine == line {
		return offset + char
	}
	return -1
}

// bracketsCursor reports whether the argument-list region of a call, from
// just after its '(' at byte openAfter to its ')' at byte closeAt, holds
// the cursor: strictly after the '(' and at or before the ')'. Using
// at-or-before for the close paren means `foo(|)` (cursor right after `(`,
// at the position of `)`) counts as inside — which matches user
// expectation when they just typed the open paren. openAfter < 0 is a
// suffix without a '('.
func bracketsCursor(openAfter, closeAt, cursorOffset int) bool {
	return openAfter >= 0 && cursorOffset > openAfter && cursorOffset <= closeAt
}

// findCallAtOffsetNative returns the innermost call-style postfixSuffix (one
// with an argument list or a `(`) whose argument list holds the byte offset
// cursorOffset, under root, the native tree of a parse of the document's
// text, whose byte offsets its tokens carry; nil when there is none.
func findCallAtOffsetNative(root native.GalaNodeRef, cursorOffset int) *callContext {
	f := nativeCallFinder{cursorOffset: cursorOffset}
	f.walk(root, native.GalaNodeRef{}, 1)
	if !f.bestSuffix.Ok() {
		return nil
	}
	shape, ok := callShapeNative(f.bestSuffix, f.bestParent)
	if !ok {
		return nil
	}
	return shape.context(cursorOffset)
}

// nativeCallFinder tracks the innermost postfixSuffix whose argument list
// brackets the cursor.
type nativeCallFinder struct {
	cursorOffset int
	bestSuffix   native.GalaNodeRef
	bestParent   native.GalaNodeRef
	bestDepth    int
}

// walk visits n, the child of parent, at depth depth (the root's is 1), and
// the nodes under it. A node whose tokens do not span the cursor is skipped
// with everything under it: a suffix the finder keeps has the cursor after
// its '(' and at or before its ')' (bracketsCursor), so inside its own tokens
// and those of every node above it.
func (f *nativeCallFinder) walk(n, parent native.GalaNodeRef, depth int) {
	if n.StopIndex() < n.StartIndex() || f.cursorOffset <= int(n.Start().Start) || f.cursorOffset > int(n.Stop().End) {
		return
	}
	if n.Kind() == native.GalaKIND_postfixSuffix {
		f.enterPostfixSuffix(n, parent, depth)
	}
	for i := range n.ChildCount() {
		if c := n.Child(i).Node(); c.Ok() {
			f.walk(c, n, depth+1)
		}
	}
}

// enterPostfixSuffix keeps suffix, the child of parent at depth depth, when it
// is a call whose argument list brackets the cursor, deeper than the suffix
// kept so far. A suffix with no argument list and no `(` is a `.id` or
// `[exprs]`, not a call.
func (f *nativeCallFinder) enterPostfixSuffix(suffix, parent native.GalaNodeRef, depth int) {
	open := childToken(suffix, "(")
	if !(native.PostfixSuffixNode{GalaNodeRef: suffix}).ArgumentList().Ok() && !open.Ok() {
		return
	}
	start, stop := openParenRangeNative(suffix, open)
	if !bracketsCursor(start, stop, f.cursorOffset) {
		return
	}
	if depth <= f.bestDepth {
		return
	}
	if !parent.Ok() || parent.Kind() != native.GalaKIND_postfixExpr {
		return
	}
	f.bestSuffix = suffix
	f.bestParent = parent
	f.bestDepth = depth
}

// openParenRangeNative returns the byte offsets of the `(` and `)` of suffix, a
// call-style postfixSuffix whose first `(` child is openParen (childToken),
// or the `(` and the suffix's end for a still-unclosed, recovered suffix;
// (-1, -1) when it has no `(`.
func openParenRangeNative(suffix native.GalaNodeRef, openParen native.GalaTerminal) (openAfter, closeAt int) {
	if !openParen.Ok() {
		return -1, -1
	}
	openAfter = int(openParen.Token().Start)
	if closeParen := childToken(suffix, ")"); closeParen.Ok() {
		return openAfter, int(closeParen.Token().Start)
	}
	if suffix.StopIndex() >= 0 {
		return openAfter, int(suffix.Stop().End)
	}
	return openAfter, openAfter + 1
}

// childToken is the first child token of n whose text is literal, or no token.
func childToken(n native.GalaNodeRef, literal string) native.GalaTerminal {
	for i := range n.ChildCount() {
		if t := n.Child(i).Terminal(); t.Ok() && t.GetText() == literal {
			return t
		}
	}
	return native.GalaTerminal{}
}

// callShapeNative is the callShape of the call callSuffix of parent, and false
// when callSuffix is none of parent's suffixes.
func callShapeNative(callSuffix, parent native.GalaNodeRef) (callShape, bool) {
	var suffixes []native.GalaNodeRef
	callIdx := -1
	for s := range parser.NativeKids(parent, native.GalaKIND_postfixSuffix) {
		if s == callSuffix {
			callIdx = len(suffixes)
		}
		suffixes = append(suffixes, s)
		if callIdx >= 0 {
			break
		}
	}
	if callIdx < 0 {
		return callShape{}, false
	}

	shape := callShape{primary: nativeSourceText(native.PostfixExprNode{GalaNodeRef: parent}.PrimaryExpr().GalaNodeRef)}
	if callIdx > 0 {
		prev := suffixes[callIdx-1]
		if id := (native.PostfixSuffixNode{GalaNodeRef: prev}).Identifier(); id.Ok() && !childToken(prev, "(").Ok() && !childToken(prev, "[").Ok() {
			shape.method = id.GetText()
			shape.receiver = shape.primary
			for _, s := range suffixes[:callIdx-1] {
				shape.receiver += nativeSourceText(s)
			}
		}
	}

	if open := childToken(callSuffix, "("); open.Ok() {
		shape.argStarts = append(shape.argStarts, int(open.Token().Start))
	}
	argList := native.PostfixSuffixNode{GalaNodeRef: callSuffix}.ArgumentList()
	if !argList.Ok() {
		return shape, true
	}
	for i := range argList.ChildCount() {
		if t := argList.Child(i).Terminal(); t.Ok() && t.GetText() == "," {
			shape.argStarts = append(shape.argStarts, int(t.Token().Start))
		}
	}
	for arg := range parser.NativeKids(argList.GalaNodeRef, native.GalaKIND_argument) {
		if arg.StopIndex() < arg.StartIndex() { // no tokens, as walk tests it
			continue
		}
		ca := callArg{start: int(arg.Start().Start), end: int(arg.Stop().End)}
		if id := (native.ArgumentNode{GalaNodeRef: arg}).Identifier(); id.Ok() {
			ca.name = id.GetText()
		}
		shape.args = append(shape.args, ca)
	}
	return shape, true
}

// nativeSourceText is the source text n covers, verbatim, or "" for no node or
// one without tokens.
func nativeSourceText(n native.GalaNodeRef) string {
	if !n.Ok() {
		return ""
	}
	return n.SourceText()
}

// callShape is what signature help reads of a call from the parse tree: the
// call-style postfixSuffix the cursor is in and the postfixExpr it is a
// suffix of, as source text and byte offsets. callShapeNative reads it, and
// context turns it into the callContext.
type callShape struct {
	// primary is the source text of the postfixExpr's primary expression.
	primary string
	// method is the identifier of the `.id` suffix right before the call,
	// and receiver the source text of the primary expression and the
	// suffixes before that one; both are "" when no `.id` suffix precedes
	// the call.
	method, receiver string
	// argStarts is the byte offsets of the call's '(' and of its argument
	// list's top-level commas, in order.
	argStarts []int
	// args is the call's arguments.
	args []callArg
}

// callArg is one argument of a call: its source range, as byte offsets, and
// its name when it is passed by name ("" for a positional one).
type callArg struct {
	start, end int
	name       string
}

// context turns the call into the data the signature resolver needs:
// callee name, receiver text, and argument index. It is nil when the shape
// is no call signature help answers for.
func (s callShape) context(cursorOffset int) *callContext {
	name := s.method
	// No preceding `.id` — this is a bare call on the primary expression.
	// Use the primary expression's text as the callee name.
	if name == "" {
		primaryText := strings.TrimSpace(s.primary)
		// Only treat it as a real call when the primary expression is a
		// simple identifier. `(x+y)(` and similar grouping-into-call
		// shapes are not what the user wants signature help for.
		if primaryText == "" || !isIdentifier(primaryText) {
			return nil
		}
		name = primaryText
	}

	call := &callContext{
		name:          name,
		receiverChain: s.receiver,
		named:         map[string]bool{},
		argStarts:     s.argStarts,
	}
	// The active argument follows the last separator before the cursor.
	for _, sep := range call.argStarts[min(1, len(call.argStarts)):] {
		if sep < cursorOffset {
			call.argIndex++
		}
	}
	for _, arg := range s.args {
		if arg.start <= cursorOffset && cursorOffset <= arg.end {
			continue
		}
		switch {
		case arg.name != "":
			call.named[arg.name] = true
		case arg.end <= cursorOffset:
			call.positional++
		}
	}
	return call
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 {
			if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
		} else {
			if !isIdentChar(c) {
				return false
			}
		}
	}
	return true
}

// resolveCallSignature resolves a callContext to a SignatureInformation
// for the callee resolveCallTarget finds.
func resolveCallSignature(call *callContext, enclosingFunc string, richAST *transpiler.RichAST, varTypes map[string]string) *lsp.SignatureInformation {
	switch target := resolveCallTarget(call, enclosingFunc, richAST, varTypes); {
	case target.fn != nil:
		return functionSignature(call.name, target.fn)
	case target.method != nil:
		return methodSignature(call.name, target.method)
	case target.typ != nil, target.variant != nil:
		names, types, _ := target.parameters()
		return constructorSignature(call.name, names, types, target.doc(), target.fieldDocs())
	}
	return nil
}

// callTarget is what a call resolves to; at most one field is set.
type callTarget struct {
	fn     *transpiler.FunctionMetadata
	method *transpiler.MethodMetadata
	// A constructor call, whose parameters are the fields: of a struct, or of
	// a sealed type's case.
	typ     *transpiler.TypeMetadata
	variant *transpiler.SealedVariant
}

// resolveCallTarget looks up the callee of a callContext in richAST. Supports:
//   - Bare function calls (`foo(`) → richAST.Functions[name]
//   - Method calls on a receiver expression (`expr.foo(`) → resolve receiver
//     type via resolveChainTypeN, then find Methods[name] on that type
//   - Constructor calls (`Type(`, `Case(`) → the struct's or the sealed
//     case's fields, treated as its parameter list
func resolveCallTarget(call *callContext, enclosingFunc string, richAST *transpiler.RichAST, varTypes map[string]string) callTarget {
	// Method call on a receiver expression.
	if call.receiverChain != "" {
		receiverType := resolveChainTypeN(call.receiverChain, enclosingFunc, richAST, varTypes, 0)
		// A package qualifier names a package-level function or constructor,
		// not a method. Resolving it here rather than through the bare-name
		// fallback below keeps the popup scoped to the package the user typed:
		// the fallback matches a simple name across every loaded package in map
		// order, so two packages exporting one name answer differently between
		// requests.
		if pkg, isPkg := strings.CutPrefix(receiverType, packagePrefix); isPkg {
			switch m := lookupPackageMember(richAST, pkg, call.name); {
			case m.Func != nil:
				return callTarget{fn: m.Func}
			case m.Variant != nil:
				return callTarget{variant: m.Variant}
			case m.Type != nil && len(m.Type.FieldNames) > 0:
				return callTarget{typ: m.Type}
			}
		}
		if receiverType != "" {
			if tm := findType(richAST, receiverType); tm != nil {
				if m, ok := tm.Methods[call.name]; ok {
					return callTarget{method: m}
				}
				if m := synthesizedMethod(richAST, tm, call.name); m != nil {
					return callTarget{method: m}
				}
			}
		}
		// Fall through to bare-name lookup — useful when receiver
		// resolution fails but the method name uniquely identifies a
		// function (e.g., dot-imported or free function).
	}

	// Free function.
	if fm := findFunction(richAST, call.name); fm != nil {
		return callTarget{fn: fm}
	}

	// Type constructor: `Person(name = "...", age = 30)` — build a
	// signature from the type's fields. Named-arg completion is the
	// primary UX for this, but showing the positional signature gives
	// useful hint text too.
	if tm := findType(richAST, call.name); tm != nil && len(tm.FieldNames) > 0 {
		return callTarget{typ: tm}
	}
	// Sealed case constructor: `Circle(radius = 1.0)`. The companion type the
	// transpiler generates for a case records no fields, so the case itself
	// answers.
	if v, _ := findSealedVariant(richAST, call.name, richAST.PackageName); v != nil {
		return callTarget{variant: v}
	}

	return callTarget{}
}

func functionSignature(name string, fm *transpiler.FunctionMetadata) *lsp.SignatureInformation {
	summary, paramDocs := splitDoc(fm.Doc, fm.ParamNames)
	params, labels := buildParamList(fm.ParamNames, fm.ParamTypes, paramDocs)
	var label strings.Builder
	label.WriteString("func " + name)
	if len(fm.TypeParams) > 0 {
		label.WriteString("[" + strings.Join(fm.TypeParams, ", ") + "]")
	}
	label.WriteString("(" + strings.Join(labels, ", ") + ")")
	label.WriteString(resultSuffix(fm.ReturnType, fm.GoResults))
	return &lsp.SignatureInformation{
		Label:         label.String(),
		Documentation: markdown(summary),
		Parameters:    params,
	}
}

func methodSignature(name string, m *transpiler.MethodMetadata) *lsp.SignatureInformation {
	summary, paramDocs := splitDoc(m.Doc, m.ParamNames)
	params, labels := buildParamList(m.ParamNames, m.ParamTypes, paramDocs)
	var label strings.Builder
	label.WriteString(name)
	if len(m.TypeParams) > 0 {
		label.WriteString("[" + strings.Join(m.TypeParams, ", ") + "]")
	}
	label.WriteString("(" + strings.Join(labels, ", ") + ")")
	label.WriteString(resultSuffix(m.ReturnType, m.GoResults))
	return &lsp.SignatureInformation{
		Label:         label.String(),
		Documentation: markdown(summary),
		Parameters:    params,
	}
}

// constructorSignature is the signature of a constructor call, whose parameters
// are the constructed value's fields.
//
// Each field carries its own doc comment — so the type comment needs no
// parameter lines, and running splitDoc over it would risk stealing a
// "Note:"-style line out of the summary whenever its label happened to match a
// field name.
func constructorSignature(name string, fields []string, types []transpiler.Type, doc string, fieldDocs map[string]string) *lsp.SignatureInformation {
	params, labels := buildParamList(fields, types, fieldDocs)
	return &lsp.SignatureInformation{
		Label:         name + "(" + strings.Join(labels, ", ") + ")",
		Documentation: markdown(doc),
		Parameters:    params,
	}
}

// buildParamList returns ParameterInformation entries plus their rendered
// "name type" labels (used to compose the full signature label). Both
// slices are returned so each Parameter's Label matches the corresponding
// substring inside the signature label — clients use that substring to
// visually highlight the active parameter.
func buildParamList(names []string, types []transpiler.Type, docs map[string]string) ([]lsp.ParameterInformation, []string) {
	params := make([]lsp.ParameterInformation, 0, len(names))
	labels := make([]string, 0, len(names))
	for i, n := range names {
		var labelPart string
		if i < len(types) && types[i] != nil && !types[i].IsNil() {
			labelPart = n + " " + types[i].String()
		} else {
			labelPart = n
		}
		labels = append(labels, labelPart)
		params = append(params, lsp.ParameterInformation{
			Label:         labelPart,
			Documentation: markdown(docs[n]),
		})
	}
	return params, labels
}
