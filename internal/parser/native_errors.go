package parser

import (
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// The functions here turn the errors of galop's recovering parse
// (native.GalaParseWithRecovery) into GALA's diagnostics, as GalaErrorListener
// turns ANTLR's: the same coded hints (GALA-E0040, GALA-E0042, GALA-E0085),
// read off the recovered native tree and its tokens instead of ANTLR's parser,
// the same cascade suppression, and the same plain syntax errors. galop's
// recovery reports ANTLR's errors, in ANTLR's order, with the rule node ANTLR's
// parser was in and how many children it had then, so each hint sees what its
// ANTLR counterpart sees.

// nativeSyntaxErrors is the diagnostics of a recovering parse of ts: its
// lexical errors and the parse's errors, in the order ANTLR's lexer and parser
// report them (see lexicalFirst), through nativeErrorReporter. tree is the
// parse's tree, nil when it has none. upTo, when not negative, is the index
// of the first token the parse never read: a lexical error after its start is
// left out, as ANTLR's lexer never reaches it (an expression's parse stops
// after the expression).
func nativeSyntaxErrors(tree *native.GalaTree, ts *native.GalaTokens, parseErrs []native.GalaParseError, upTo int) []error {
	if len(parseErrs) == 0 && len(ts.Errors) == 0 {
		return nil
	}
	r := nativeErrorReporter{tree: tree, reportedGoType: -1}
	lexErrs := ts.Errors
	if upTo >= 0 && upTo < len(ts.Tokens) {
		end := ts.Tokens[upTo]
		for k, l := range lexErrs {
			if !before(l.Line, l.Column, end) {
				lexErrs = lexErrs[:k]
				break
			}
		}
	}
	for _, e := range parseErrs {
		for len(lexErrs) > 0 && lexicalFirst(lexErrs[0], e, ts) {
			r.lexical(lexErrs[0])
			lexErrs = lexErrs[1:]
		}
		r.syntax(e)
	}
	for _, l := range lexErrs {
		r.lexical(l)
	}
	return r.errs
}

// lexicalFirst reports whether ANTLR reports lexical error l before the
// parse error e. ANTLR's lexer runs as the parser asks for tokens, and reports
// an error when it lexes the token after it; the parser has read the token it
// reports an error at. So a lexical error before e's token comes first.
func lexicalFirst(l native.GalaSyntaxError, e native.GalaParseError, ts *native.GalaTokens) bool {
	if e.Token < 0 || e.Token >= len(ts.Tokens) {
		return true
	}
	return before(l.Line, l.Column, ts.Tokens[e.Token])
}

// before reports whether line:col comes before tok's start.
func before(line, col int, tok native.GalaToken) bool {
	return line < int(tok.Line) || line == int(tok.Line) && col < int(tok.Column)
}

// nativeErrorReporter is GalaErrorListener for galop's recovering parse: it
// turns each error into a diagnostic, in order, with the same hints and
// cascade suppression.
type nativeErrorReporter struct {
	tree *native.GalaTree
	errs []error
	// reportedGoType is the node of the composite literal a GALA-E0040 has
	// been reported for, or -1 (GalaErrorListener.reportedGoType).
	reportedGoType int32
	// bareLambdaLine is the line a GALA-E0042 has been reported on, or 0
	// (GalaErrorListener.bareLambdaLine).
	bareLambdaLine int
}

// lexical reports a lexical error, as GalaErrorListener reports one of
// ANTLR's lexer: no hint, but the bare-lambda cascade suppression applies.
func (r *nativeErrorReporter) lexical(l native.GalaSyntaxError) {
	if r.bareLambdaLine != 0 && l.Line == r.bareLambdaLine {
		return
	}
	r.errs = append(r.errs, galaerr.NewSyntaxError(l.Line, l.Column, hideNewlineTokens(l.Msg)))
}

// syntax reports a parse error, as GalaErrorListener.SyntaxError does.
func (r *nativeErrorReporter) syntax(e native.GalaParseError) {
	lit := r.compositeLiteral(e)
	if lit.Ok() && int32(lit.Index()) == r.reportedGoType {
		return
	}
	if err := nativeGoTypeInExpressionError(lit, e.Kids); err != nil {
		r.reportedGoType = int32(lit.Index())
		r.errs = append(r.errs, err)
		return
	}
	if r.bareLambdaLine != 0 && e.Line == r.bareLambdaLine {
		return
	}
	if err := r.bareLambdaParamError(e); err != nil {
		r.bareLambdaLine = e.Line
		r.errs = append(r.errs, err)
		return
	}
	if err := r.structTagError(e); err != nil {
		r.errs = append(r.errs, err)
		return
	}
	r.errs = append(r.errs, galaerr.NewSyntaxError(e.Line, e.Column, hideNewlineTokens(e.Msg)))
}

// node is the rule node e was reported in, or no node.
func (r *nativeErrorReporter) node(e native.GalaParseError) native.GalaNodeRef {
	if r.tree == nil || e.Node < 0 || int(e.Node) >= len(r.tree.Nodes) {
		return native.GalaNodeRef{}
	}
	return r.tree.Node(int(e.Node))
}

// offending is e's offending token, and whether it has one.
func (r *nativeErrorReporter) offending(e native.GalaParseError) (native.GalaToken, bool) {
	if r.tree == nil || e.Token < 0 || e.Token >= len(r.tree.Tokens) {
		return native.GalaToken{}, false
	}
	return r.tree.Tokens[e.Token], true
}

// compositeLiteral is currentCompositeLiteral: the node e was reported in
// when it is a compositeLiteral, else no node.
func (r *nativeErrorReporter) compositeLiteral(e native.GalaParseError) native.CompositeLiteralNode {
	if n := r.node(e); n.Ok() && n.Rule() == native.GalaRULE_compositeLiteral {
		return native.CompositeLiteralNode{GalaNodeRef: n}
	}
	return native.CompositeLiteralNode{}
}

// nativeGoTypeInExpressionError is goTypeInExpressionError on the native
// tree. kids is how many children lit had when the error was reported: the
// recovered tree's node has more by then (the brace the recovery conjured, the
// tokens it skipped), so its ChildCount does not say where the parse died.
func nativeGoTypeInExpressionError(lit native.CompositeLiteralNode, kids int) error {
	if !lit.Ok() || kids != 1 {
		return nil
	}
	typ := lit.Type_()
	if !typ.Ok() {
		return nil
	}
	offender := findNativeGoSliceOrMapType(typ.GalaNodeRef)
	if !offender.Ok() {
		return nil
	}
	var args []string
	for _, a := range offender.AllType_() {
		args = append(args, a.GetText())
	}
	start, stop := offender.Start(), offender.StopIndex()
	return goTypeDiagnostic(int(start.Line), int(start.Column), offender.GetText(), args,
		stop >= offender.StartIndex() && offender.Tree().Token(stop).Line == start.Line)
}

// findNativeGoSliceOrMapType is findGoSliceOrMapType on the native tree: the
// first type node at or below n, in source order, whose text starts with "[]"
// or "map[", or no node.
func findNativeGoSliceOrMapType(n native.GalaNodeRef) native.TypeNode {
	if n.Rule() == native.GalaRULE_type {
		if txt := n.GetText(); strings.HasPrefix(txt, "[]") || strings.HasPrefix(txt, "map[") {
			return native.TypeNode{GalaNodeRef: n}
		}
	}
	for i := range n.ChildCount() {
		if c := n.Child(i); !c.IsToken() {
			if found := findNativeGoSliceOrMapType(c.Node()); found.Ok() {
				return found
			}
		}
	}
	return native.TypeNode{}
}

// bareLambdaParamError is the package-level bareLambdaParamError on galop's
// tokens. Comments are hidden tokens, not in Tokens, so the code token before
// the offending one is simply the one before it in Tokens.
func (r *nativeErrorReporter) bareLambdaParamError(e native.GalaParseError) *galaerr.SemanticError {
	if _, ok := r.offending(e); !ok || e.Token == 0 || r.tree.Text(e.Token) != "=>" {
		return nil
	}
	prev := r.tree.Tokens[e.Token-1]
	if prev.Type != native.GalaIDENTIFIER {
		return nil
	}
	return bareLambdaError(int(prev.Line), int(prev.Column), r.tree.Text(e.Token-1))
}

// structTagError is the package-level structTagError on the native tree.
func (r *nativeErrorReporter) structTagError(e native.GalaParseError) *galaerr.SemanticError {
	tok, ok := r.offending(e)
	if !ok || tok.Type != native.GalaSTRING && tok.Type != native.GalaRAW_STRING {
		return nil
	}
	field := lastNativeStructField(r.node(e), e.Kids)
	if !field.Ok() {
		return nil
	}
	name := nativeFieldName(field)
	if !name.Ok() {
		return nil
	}
	// A tag sits right after the field, on its line.
	if e.Token == 0 || field.StopIndex() != e.Token-1 || field.Stop().Line != tok.Line {
		return nil
	}
	return structTagDiagnostic(int(tok.Line), int(tok.Column), name.GetText(), r.tree.Text(e.Token))
}

// nativeFieldName is the name of field, a structField, parameter or
// sealedCaseField node, or no node.
func nativeFieldName(field native.GalaNodeRef) native.IdentifierNode {
	switch field.Rule() {
	case native.GalaRULE_structField:
		return native.StructFieldNode{GalaNodeRef: field}.Identifier()
	case native.GalaRULE_parameter:
		return native.ParameterNode{GalaNodeRef: field}.Identifier()
	case native.GalaRULE_sealedCaseField:
		return native.SealedCaseFieldNode{GalaNodeRef: field}.Identifier()
	}
	return native.IdentifierNode{}
}

// lastNativeStructField is lastStructField on the native tree: when n, the
// rule the parser was in when it rejected a string literal, is a struct's
// field list, the last field parsed in it so far, else no node. n had kids
// children then; the recovered tree's n can have more, as recovery goes on in
// the same rule, so only those are read. A nested list (a shorthand struct's
// parameterList, a sealed case's sealedCaseFieldList) was finished by then,
// so all of its fields are.
func lastNativeStructField(n native.GalaNodeRef, kids int) native.GalaNodeRef {
	if !n.Ok() {
		return native.GalaNodeRef{}
	}
	switch n.Rule() {
	case native.GalaRULE_structType:
		return lastKid(n, kids, native.GalaRULE_structField)
	case native.GalaRULE_parameterList:
		if params := n.Parent(); params.Ok() && params.Rule() == native.GalaRULE_parameters && isNativeShorthandStruct(params) {
			return lastKid(n, kids, native.GalaRULE_parameter)
		}
	case native.GalaRULE_parameters:
		if list := firstKid(n, kids, native.GalaRULE_parameterList); list.Ok() && isNativeShorthandStruct(n) {
			return lastKid(list, list.ChildCount(), native.GalaRULE_parameter)
		}
	case native.GalaRULE_sealedCaseFieldList:
		return lastKid(n, kids, native.GalaRULE_sealedCaseField)
	case native.GalaRULE_sealedCase:
		if list := firstKid(n, kids, native.GalaRULE_sealedCaseFieldList); list.Ok() {
			return lastKid(list, list.ChildCount(), native.GalaRULE_sealedCaseField)
		}
	}
	return native.GalaNodeRef{}
}

// isNativeShorthandStruct is isShorthandStruct on the native tree.
func isNativeShorthandStruct(params native.GalaNodeRef) bool {
	p := params.Parent()
	return p.Ok() && p.Rule() == native.GalaRULE_structShorthandDeclaration
}

// firstKid is the first node of rule among n's first kids children, or no
// node.
func firstKid(n native.GalaNodeRef, kids, rule int) native.GalaNodeRef {
	for i := range min(kids, n.ChildCount()) {
		if c := n.Child(i); !c.IsToken() && c.Node().Rule() == rule {
			return c.Node()
		}
	}
	return native.GalaNodeRef{}
}

// lastKid is the last node of rule among n's first kids children, or no
// node.
func lastKid(n native.GalaNodeRef, kids, rule int) native.GalaNodeRef {
	for i := min(kids, n.ChildCount()) - 1; i >= 0; i-- {
		if c := n.Child(i); !c.IsToken() && c.Node().Rule() == rule {
			return c.Node()
		}
	}
	return native.GalaNodeRef{}
}
