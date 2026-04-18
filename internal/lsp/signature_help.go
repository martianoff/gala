package lsp

import (
	"context"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// SignatureHelp implements textDocument/signatureHelp.
//
// Walks backwards from the cursor to find the unbalanced `(` that opens the
// current call, extracts the method/function name before it, resolves the
// callee's parameter list (via richAST), and returns a SignatureInformation
// with ActiveParameter set from the comma count between `(` and the cursor.
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

	// The cursor is inside an unclosed call, so the raw document often
	// fails to parse. Surgically close the call and re-analyze so we
	// have richAST + varTypes for type resolution.
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

	call := findEnclosingCall(text, line, char)
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

// callContext captures the information about the enclosing call at a cursor
// position needed to resolve signature help.
type callContext struct {
	// name is the callee identifier immediately before the enclosing `(`.
	name string
	// receiverChain, if non-empty, is the text of the receiver expression
	// that precedes `name.` — used for resolving method signatures on
	// chained expressions like `NewServer().WithFilter(` where the chain
	// before `.WithFilter(` is `NewServer()`.
	receiverChain string
	// argIndex is the zero-based index of the argument the cursor is in,
	// computed by counting top-level commas between the enclosing `(` and
	// the cursor.
	argIndex int
}

// findEnclosingCall walks backwards from the cursor to find the unbalanced
// `(` that opens the currently-being-edited call, then extracts the callee
// name and counts the top-level commas between that `(` and the cursor to
// determine which argument the cursor is inside.
//
// Returns nil if there is no enclosing call, if the text before `(` isn't a
// call-like expression (no identifier), or if the cursor is inside a string
// literal.
func findEnclosingCall(text string, line, char int) *callContext {
	// Compute absolute byte offset of the cursor in text.
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return nil
	}
	if char > len(lines[line]) {
		char = len(lines[line])
	}
	cursorOffset := 0
	for i := 0; i < line; i++ {
		cursorOffset += len(lines[i]) + 1 // +1 for the stripped '\n'
	}
	cursorOffset += char

	// Walk backwards from the cursor, tracking nested parens/braces/brackets
	// and skipping over string literals. We want the first `(` that is not
	// matched by a `)` to its right (within [0, cursorOffset)).
	openParenPos := findEnclosingOpenParen(text, cursorOffset)
	if openParenPos < 0 {
		return nil
	}

	// Extract the callee name before the `(`. Skip any whitespace.
	i := openParenPos - 1
	for i >= 0 && (text[i] == ' ' || text[i] == '\t') {
		i--
	}
	if i < 0 {
		return nil
	}
	nameEnd := i + 1
	for i >= 0 && isIdentChar(text[i]) {
		i--
	}
	nameStart := i + 1
	if nameStart >= nameEnd {
		// No identifier before `(` — e.g. `(expr)` grouping, or lambda
		// parameter list. Not a call.
		return nil
	}
	name := text[nameStart:nameEnd]

	// Check for a dot before the name — if present, the thing before it is
	// the receiver expression (needed to look up the method's parameter
	// list on the receiver's type). Skip whitespace/newlines between the
	// name and the dot so multi-line chains (`NewServer().\n    .Method(`)
	// are handled.
	var receiverChain string
	j := nameStart - 1
	for j >= 0 && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
		j--
	}
	if j >= 0 && text[j] == '.' {
		receiverChain = text[:j]
	}

	// Count top-level commas from just after `(` to the cursor.
	argIndex := countTopLevelCommas(text, openParenPos+1, cursorOffset)

	return &callContext{
		name:          name,
		receiverChain: receiverChain,
		argIndex:      argIndex,
	}
}

// findEnclosingOpenParen returns the byte offset of the `(` that opens the
// call containing position `end`, or -1 if no such paren exists. Skips over
// balanced `()`, `{}`, `[]` pairs and string literals while walking back.
func findEnclosingOpenParen(text string, end int) int {
	parenDepth := 0
	braceDepth := 0
	bracketDepth := 0
	i := end - 1
	for i >= 0 {
		c := text[i]
		// Skip over double-quoted strings (walking backwards).
		if c == '"' && !isEscapedAt(text, i) {
			j := i - 1
			for j >= 0 {
				if text[j] == '"' && !isEscapedAt(text, j) {
					break
				}
				j--
			}
			if j < 0 {
				return -1
			}
			i = j - 1
			continue
		}
		switch c {
		case ')':
			parenDepth++
		case '(':
			if parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				return i
			}
			parenDepth--
		case '}':
			braceDepth++
		case '{':
			braceDepth--
		case ']':
			bracketDepth++
		case '[':
			bracketDepth--
		}
		i--
	}
	return -1
}

// isEscapedAt reports whether the byte at position i is preceded by an odd
// number of backslashes (i.e. the character is escaped).
func isEscapedAt(text string, i int) bool {
	count := 0
	for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
		count++
	}
	return count%2 == 1
}

// countTopLevelCommas counts commas in text[start:end] that are not inside
// nested (), {}, [] pairs and not inside string literals. Returns the
// argument index for the cursor at `end` — 0 means "first argument".
func countTopLevelCommas(text string, start, end int) int {
	if start >= end {
		return 0
	}
	n := 0
	parenDepth := 0
	braceDepth := 0
	bracketDepth := 0
	inString := false
	for i := start; i < end; i++ {
		c := text[i]
		if inString {
			if c == '\\' && i+1 < end {
				i++
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case ',':
			if parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				n++
			}
		}
	}
	return n
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
	params, labels := buildParamList(fm.ParamNames, fm.ParamTypes)
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
		Label:      label.String(),
		Parameters: params,
	}
}

func methodSignature(name string, m *transpiler.MethodMetadata) *lsp.SignatureInformation {
	params, labels := buildParamList(m.ParamNames, m.ParamTypes)
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
		Label:      label.String(),
		Parameters: params,
	}
}

func typeConstructorSignature(name string, tm *transpiler.TypeMetadata) *lsp.SignatureInformation {
	types := make([]transpiler.Type, 0, len(tm.FieldNames))
	for _, fn := range tm.FieldNames {
		types = append(types, tm.Fields[fn])
	}
	params, labels := buildParamList(tm.FieldNames, types)
	var label strings.Builder
	label.WriteString(name + "(" + strings.Join(labels, ", ") + ")")
	return &lsp.SignatureInformation{
		Label:      label.String(),
		Parameters: params,
	}
}

// buildParamList returns ParameterInformation entries plus their rendered
// "name type" labels (used to compose the full signature label). Both
// slices are returned so the Parameters' Label strings match the
// corresponding substring inside the signature label, which is what
// clients use to visually highlight the active parameter.
func buildParamList(names []string, types []transpiler.Type) ([]lsp.ParameterInformation, []string) {
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
		params = append(params, lsp.ParameterInformation{Label: labelPart})
	}
	return params, labels
}
