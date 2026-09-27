package lsp

import (
	"context"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/parser"
	grammar "martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// SignatureHelp implements textDocument/signatureHelp.
//
// Locates the call expression at the cursor by walking the ANTLR parse
// tree — not by byte-level scans — then extracts the callee name,
// receiver chain, and argument index structurally from the tree. The
// tree is parsed from a version of the source where the unclosed call
// has been surgically closed with `)` (see ensureAnalysisForSignature)
// so ANTLR can actually produce a well-formed postfixExpr.
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
// The tree parsed for the document's latest analysis is reused when it was
// parsed from exactly that text; any other tree — another caret's patch, an
// older edit — would put the call at the wrong offsets.
func (h *GalaHandler) findCallAtCaret(uri, text string, line, char int) *callContext {
	src, offset := patchTextForSignature(text, line, char)
	if offset < 0 {
		return nil
	}
	h.mu.Lock()
	tree := h.parseTrees[uri]
	if h.parseTexts[uri] != src {
		tree = nil
	}
	h.mu.Unlock()
	if tree == nil {
		if tree, _, _ = h.parser.ParseLenient(src); tree == nil {
			return nil
		}
	}
	return findCallAtOffset(tree, src, offset)
}

// patchTextForSignature closes the call the user is typing with a `)` at the
// cursor when the document's parentheses do not balance, so ANTLR can produce
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
	parser.VisitTokens(text, func(tok antlr.Token) {
		switch tok.GetText() {
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

// findCallAtOffset returns the innermost call-style postfixSuffix (one
// that has an argumentList) whose argumentList's source range contains
// the given byte offset. Returns nil if no such call is found.
func findCallAtOffset(tree antlr.Tree, text string, cursorOffset int) *callContext {
	f := &callFinder{cursorOffset: cursorOffset, offs: newTokenOffsets(text)}
	antlr.ParseTreeWalkerDefault.Walk(f, tree)
	if f.bestSuffix == nil {
		return nil
	}
	return buildCallContext(f.bestSuffix, f.bestParent, text, f.offs, cursorOffset)
}

// callFinder is an ANTLR listener that tracks the innermost postfixSuffix
// whose argumentList range brackets the cursor.
type callFinder struct {
	*grammar.BasegalaListener
	cursorOffset int          // bytes
	offs         tokenOffsets // token indexes (code points) -> bytes
	bestSuffix   *grammar.PostfixSuffixContext
	bestParent   *grammar.PostfixExprContext
	bestDepth    int
	depth        int
}

func (f *callFinder) EnterEveryRule(ctx antlr.ParserRuleContext) { f.depth++ }
func (f *callFinder) ExitEveryRule(ctx antlr.ParserRuleContext)  { f.depth-- }

func (f *callFinder) EnterPostfixSuffix(ctx *grammar.PostfixSuffixContext) {
	argList := ctx.ArgumentList()
	// A postfixSuffix that has no argumentList and no '(' terminal is
	// either a `.id` or `[exprs]` — not a call. Fast-skip it.
	openParen := findChildTerminal(ctx, "(")
	if argList == nil && openParen == nil {
		return
	}
	// The argument-list region runs from just after the '(' to the
	// matching ')'. We consider the cursor "inside" the call when it's
	// strictly after the '(' and at-or-before the ')'. Using at-or-before
	// for the close paren means `foo(|)` (cursor right after `(`, at the
	// position of `)`) counts as inside — which matches user expectation
	// when they just typed the open paren.
	start, stop := openParenRange(ctx, f.offs)
	if start < 0 {
		return
	}
	if f.cursorOffset <= start || f.cursorOffset > stop {
		return
	}
	if f.depth <= f.bestDepth {
		return
	}
	parent, _ := ctx.GetParent().(*grammar.PostfixExprContext)
	if parent == nil {
		return
	}
	f.bestSuffix = ctx
	f.bestParent = parent
	f.bestDepth = f.depth
}

// openParenRange returns the byte offsets of the '(' and ')' of a
// call-style postfixSuffix (or the '(' start and the argument list's
// stop for a still-unclosed/recovered suffix). Returns (-1, -1) if the
// suffix has no '('.
func openParenRange(ctx *grammar.PostfixSuffixContext, offs tokenOffsets) (openAfter, closeAt int) {
	openParen := findChildTerminal(ctx, "(")
	if openParen == nil {
		return -1, -1
	}
	openAfter = offs.byteOf(openParen.GetSymbol().GetStart())
	closeParen := findChildTerminal(ctx, ")")
	if closeParen != nil {
		closeAt = offs.byteOf(closeParen.GetSymbol().GetStart())
		return openAfter, closeAt
	}
	// Recovered tree without a matching ')' — treat end of the suffix as
	// the close position.
	if stop := ctx.GetStop(); stop != nil {
		closeAt = offs.byteOf(stop.GetStop() + 1)
		return openAfter, closeAt
	}
	return openAfter, openAfter + 1
}

// findChildTerminal returns the first direct terminal-node child whose
// text equals `literal`, or nil.
func findChildTerminal(ctx antlr.RuleContext, literal string) antlr.TerminalNode {
	for _, child := range ctx.GetChildren() {
		if term, ok := child.(antlr.TerminalNode); ok && term.GetText() == literal {
			return term
		}
	}
	return nil
}

// buildCallContext turns a matched call-suffix + its enclosing postfixExpr
// into the data the signature resolver needs: callee name, receiver text,
// and argument index.
func buildCallContext(callSuffix *grammar.PostfixSuffixContext, parent *grammar.PostfixExprContext, text string, offs tokenOffsets, cursorOffset int) *callContext {
	suffixes := parent.AllPostfixSuffix()
	callIdx := -1
	for i, s := range suffixes {
		if s == callSuffix {
			callIdx = i
			break
		}
	}
	if callIdx < 0 {
		return nil
	}

	var name, receiverChain string
	// If the immediately-preceding suffix is `.id`, the callee is that
	// identifier and the receiver is primaryExpr + suffixes before it.
	if callIdx > 0 {
		prev, _ := suffixes[callIdx-1].(*grammar.PostfixSuffixContext)
		if prev != nil && prev.Identifier() != nil && findChildTerminal(prev, "(") == nil && findChildTerminal(prev, "[") == nil {
			name = prev.Identifier().GetText()
			receiverChain = contextText(parent.PrimaryExpr(), text, offs)
			for i := 0; i < callIdx-1; i++ {
				receiverChain += contextText(suffixes[i], text, offs)
			}
		}
	}
	// No preceding `.id` — this is a bare call on the primary expression.
	// Use the primary expression's text as the callee name.
	if name == "" {
		primaryText := strings.TrimSpace(contextText(parent.PrimaryExpr(), text, offs))
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
		receiverChain: receiverChain,
		named:         map[string]bool{},
	}
	if open := findChildTerminal(callSuffix, "("); open != nil {
		call.argStarts = append(call.argStarts, offs.byteOf(open.GetSymbol().GetStart()))
	}
	argList, _ := callSuffix.ArgumentList().(*grammar.ArgumentListContext)
	if argList != nil {
		for _, child := range argList.GetChildren() {
			if term, ok := child.(antlr.TerminalNode); ok && term.GetText() == "," {
				call.argStarts = append(call.argStarts, offs.byteOf(term.GetSymbol().GetStart()))
			}
		}
	}
	// The active argument follows the last separator before the cursor.
	for _, sep := range call.argStarts[min(1, len(call.argStarts)):] {
		if sep < cursorOffset {
			call.argIndex++
		}
	}
	if argList == nil {
		return call
	}
	for _, a := range argList.AllArgument() {
		arg, ok := a.(*grammar.ArgumentContext)
		if !ok || arg.GetStart() == nil || arg.GetStop() == nil {
			continue
		}
		start, end := offs.byteOf(arg.GetStart().GetStart()), offs.byteOf(arg.GetStop().GetStop()+1)
		if start <= cursorOffset && cursorOffset <= end {
			continue
		}
		switch {
		case arg.Identifier() != nil:
			call.named[arg.Identifier().GetText()] = true
		case end <= cursorOffset:
			call.positional++
		}
	}
	return call
}

// contextText returns the original source text covered by the given
// parse-tree context. Uses start/stop character indices from the tokens
// so whitespace and punctuation are preserved verbatim.
func contextText(ctx antlr.RuleContext, text string, offs tokenOffsets) string {
	if ctx == nil {
		return ""
	}
	rc, ok := ctx.(interface {
		GetStart() antlr.Token
		GetStop() antlr.Token
	})
	if !ok {
		return ""
	}
	start, stop := rc.GetStart(), rc.GetStop()
	if start == nil || stop == nil {
		return ""
	}
	s, e := start.GetStart(), stop.GetStop()
	if s < 0 || e < 0 || s > e {
		return ""
	}
	bs, be := offs.byteOf(s), offs.byteOf(e+1)
	if be > len(text) || bs >= be {
		return ""
	}
	return text[bs:be]
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
	if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
		label.WriteString(" " + fm.ReturnType.String())
	}
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
	if m.ReturnType != nil && !m.ReturnType.IsNil() {
		label.WriteString(" " + m.ReturnType.String())
	}
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
