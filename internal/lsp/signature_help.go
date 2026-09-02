package lsp

import (
	"context"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"github.com/owenrumney/go-lsp/lsp"

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
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	varTypeMap := h.varTypes[uri]
	h.mu.Unlock()

	if text == "" {
		return nil, nil
	}

	// Ensure richAST + varTypes are available for signature resolution.
	// This may short-circuit if the main DidChange pipeline already
	// produced them (from a lenient/partial parse), or it may parse a
	// surgically closed variant of the source.
	if richAST == nil || len(varTypeMap) == 0 {
		h.ensureAnalysisForSignature(uri, line, char)
		h.mu.Lock()
		richAST = h.richASTs[uri]
		varTypeMap = h.varTypes[uri]
		h.mu.Unlock()
	}
	if richAST == nil {
		return nil, nil
	}

	// Always parse a freshly-patched version of the source for the tree
	// walk. Relying on h.parseTrees would risk reading back a tree that
	// was parsed from a different cursor's patched text (or from a
	// lenient, error-recovered tree of the raw unclosed source), making
	// call-site offsets misaligned with the current cursor.
	treeText, cursorOffset := patchTextForSignature(text, line, char)
	if cursorOffset < 0 {
		return nil, nil
	}
	tree, _, _ := h.parser.ParseLenient(treeText)
	if tree == nil {
		return nil, nil
	}

	call := findCallAtOffset(tree, treeText, cursorOffset)
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
}

// patchTextForSignature inserts a closing `)` at the cursor so the call
// the user is typing can be parsed to completion. Returns the patched
// text and the cursor's byte offset inside it (which, by construction,
// equals the offset of the inserted `)`).
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
	l := lines[line]
	if char < 0 {
		char = 0
	}
	if char > len(l) {
		char = len(l)
	}
	lines[line] = l[:char] + ")" + l[char:]
	patched = strings.Join(lines, "\n")
	cursorOffset = lineCharToOffset(patched, line, char)
	return patched, cursorOffset
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
	f := &callFinder{cursorOffset: cursorOffset, text: text}
	antlr.ParseTreeWalkerDefault.Walk(f, tree)
	if f.bestSuffix == nil {
		return nil
	}
	return buildCallContext(f.bestSuffix, f.bestParent, text, cursorOffset)
}

// callFinder is an ANTLR listener that tracks the innermost postfixSuffix
// whose argumentList range brackets the cursor.
type callFinder struct {
	*grammar.BasegalaListener
	cursorOffset int
	text         string
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
	start, stop := openParenRange(ctx)
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
func openParenRange(ctx *grammar.PostfixSuffixContext) (openAfter, closeAt int) {
	openParen := findChildTerminal(ctx, "(")
	if openParen == nil {
		return -1, -1
	}
	openAfter = openParen.GetSymbol().GetStart()
	closeParen := findChildTerminal(ctx, ")")
	if closeParen != nil {
		closeAt = closeParen.GetSymbol().GetStart()
		return openAfter, closeAt
	}
	// Recovered tree without a matching ')' — treat end of the suffix as
	// the close position.
	if stop := ctx.GetStop(); stop != nil {
		closeAt = stop.GetStop() + 1
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
func buildCallContext(callSuffix *grammar.PostfixSuffixContext, parent *grammar.PostfixExprContext, text string, cursorOffset int) *callContext {
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
			receiverChain = contextText(parent.PrimaryExpr(), text)
			for i := 0; i < callIdx-1; i++ {
				receiverChain += contextText(suffixes[i], text)
			}
		}
	}
	// No preceding `.id` — this is a bare call on the primary expression.
	// Use the primary expression's text as the callee name.
	if name == "" {
		primaryText := strings.TrimSpace(contextText(parent.PrimaryExpr(), text))
		// Only treat it as a real call when the primary expression is a
		// simple identifier. `(x+y)(` and similar grouping-into-call
		// shapes are not what the user wants signature help for.
		if primaryText == "" || !isIdentifier(primaryText) {
			return nil
		}
		name = primaryText
	}

	argIndex := argIndexAt(callSuffix.ArgumentList(), cursorOffset)
	return &callContext{
		name:          name,
		receiverChain: receiverChain,
		argIndex:      argIndex,
	}
}

// contextText returns the original source text covered by the given
// parse-tree context. Uses start/stop character indices from the tokens
// so whitespace and punctuation are preserved verbatim.
func contextText(ctx antlr.RuleContext, text string) string {
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
	if s < 0 || e < 0 || s > e || e >= len(text) {
		return ""
	}
	return text[s : e+1]
}

// argIndexAt counts the commas that are direct children of argumentList
// (i.e. top-level commas for this call) and end strictly before the
// cursor. Returns the zero-based active-argument index.
func argIndexAt(argList grammar.IArgumentListContext, cursorOffset int) int {
	if argList == nil {
		return 0
	}
	rc, ok := argList.(antlr.RuleContext)
	if !ok {
		return 0
	}
	n := 0
	for _, child := range rc.GetChildren() {
		term, ok := child.(antlr.TerminalNode)
		if !ok {
			continue
		}
		if term.GetText() != "," {
			continue
		}
		if term.GetSymbol().GetStart() < cursorOffset {
			n++
		}
	}
	return n
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

// resolveCallSignature resolves a callContext to a SignatureInformation by
// looking up the callee in richAST. Supports:
//   - Bare function calls (`foo(`) → richAST.Functions[name]
//   - Method calls on a receiver expression (`expr.foo(`) → resolve receiver
//     type via resolveChainTypeN, then find Methods[name] on that type
//   - Constructor-like calls on types (`Type(`) → fall back to type fields
//     treated as a positional parameter list (named args still work via
//     completion)
func resolveCallSignature(call *callContext, enclosingFunc string, richAST *transpiler.RichAST, varTypes map[string]string) *lsp.SignatureInformation {
	// Method call on a receiver expression.
	if call.receiverChain != "" {
		receiverType := resolveChainTypeN(call.receiverChain, enclosingFunc, richAST, varTypes, 0)
		if receiverType != "" {
			if tm := findType(richAST, receiverType); tm != nil {
				if m, ok := tm.Methods[call.name]; ok {
					return methodSignature(call.name, m)
				}
			}
		}
		// Fall through to bare-name lookup — useful when receiver
		// resolution fails but the method name uniquely identifies a
		// function (e.g., dot-imported or free function).
	}

	// Free function.
	if fm := findFunction(richAST, call.name); fm != nil {
		return functionSignature(call.name, fm)
	}

	// Type constructor: `Person(name = "...", age = 30)` — build a
	// signature from the type's fields. Named-arg completion is the
	// primary UX for this, but showing the positional signature gives
	// useful hint text too.
	if tm := findType(richAST, call.name); tm != nil && len(tm.FieldNames) > 0 {
		return typeConstructorSignature(call.name, tm)
	}

	return nil
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

func typeConstructorSignature(name string, tm *transpiler.TypeMetadata) *lsp.SignatureInformation {
	types := make([]transpiler.Type, 0, len(tm.FieldNames))
	for _, fn := range tm.FieldNames {
		types = append(types, tm.Fields[fn])
	}
	summary, fieldDocs := splitDoc(tm.Doc, tm.FieldNames)
	// A constructor's parameters are the type's fields, so each field's own doc
	// comment documents the corresponding argument.
	for fn, fd := range tm.FieldDocs {
		if fieldDocs == nil {
			fieldDocs = make(map[string]string)
		}
		if _, ok := fieldDocs[fn]; !ok {
			fieldDocs[fn] = fd
		}
	}
	params, labels := buildParamList(tm.FieldNames, types, fieldDocs)
	var label strings.Builder
	label.WriteString(name + "(" + strings.Join(labels, ", ") + ")")
	return &lsp.SignatureInformation{
		Label:         label.String(),
		Documentation: markdown(summary),
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
