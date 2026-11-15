package parser

import (
	"cmp"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// The functions here turn the errors of galop's recovering parse
// (native.GalaParseWithRecovery) into GALA's diagnostics: the coded hints
// (GALA-E0040, GALA-E0042, GALA-E0085, and GALA-E0117 in go_func_literal.go),
// read off the recovered native tree and
// its tokens, their cascade suppression, and the plain syntax errors. galop's
// recovery reports its errors as ANTLR's does (galop's repository holds it to
// ANTLR's): the same messages, in the same order, each with the rule node the
// parser was in and how many children it had then, which the hints read.

// nativeSyntaxErrors is the diagnostics of a recovering parse of ts: its
// lexical errors and the parse's errors, in the order a lexer that runs as the
// parser reads tokens reports them, through nativeErrorReporter. tree is the
// parse's tree, nil when it has none.
//
// Lexical error i is reported when the parser first reads token
// ts.ErrorAt[i]. Each parse error says how far the parser had read when it
// was reported (Read), so the lexical errors up to there come before it.
// After the last parse error, those the parse read up to come (tree.Read);
// the rest the parse never reaches, as when an expression's parse stops
// before them, and they are not reported.
//
// The Go function literals of the tree (GALA-E0117, goFuncLiterals) and the
// other Go constructs of the tokens (GALA-E0121 to GALA-E0132, goHabits) are
// merged in by position, so a construct leads the syntax errors after it.
//
// When there are constructs of goHabits, the syntax errors are those of
// reparse, the parse of ts, run on the tokens without them (withoutHabits):
// the errors they caused, and the recovery's cascade after them, are gone.
// reparse is nil for no retry; a galop panic in it keeps the first parse.
func nativeSyntaxErrors(tree *native.GalaTree, ts *native.GalaTokens, parseErrs []native.GalaParseError,
	reparse func(*native.GalaTokens) (*native.GalaTree, []native.GalaParseError)) []error {
	habits := goHabits(ts)
	if len(habits) > 0 && reparse != nil {
		retry := withoutHabits(ts, habits)
		var retryTree *native.GalaTree
		var retryErrs []native.GalaParseError
		if underGalop("parser", ts.Src, 0, 0, func() { retryTree, retryErrs = reparse(retry) }) == nil {
			tree, ts, parseErrs = retryTree, retry, retryErrs
		}
	}
	errs := nativeParseErrors(tree, ts, parseErrs)
	var found []*galaerr.SemanticError
	if tree != nil {
		found = goFuncLiterals(tree)
	}
	for _, h := range habits {
		found = append(found, h.err)
	}
	if len(habits) > 0 {
		slices.SortStableFunc(found, func(a, b *galaerr.SemanticError) int {
			return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
		})
	}
	return mergeByPosition(errs, found)
}

// nativeParseErrors is the lexical and parse errors of nativeSyntaxErrors.
func nativeParseErrors(tree *native.GalaTree, ts *native.GalaTokens, parseErrs []native.GalaParseError) []error {
	if len(parseErrs) == 0 && len(ts.Errors) == 0 {
		return nil
	}
	r := nativeErrorReporter{tree: tree, reportedGoType: -1}
	lex := 0
	lexicalUpTo := func(read int) {
		for ; lex < len(ts.Errors) && int(ts.ErrorAt[lex]) <= read; lex++ {
			r.lexical(ts.Errors[lex])
		}
	}
	for _, e := range parseErrs {
		lexicalUpTo(e.Read)
		r.syntax(e)
	}
	if tree == nil {
		lexicalUpTo(len(ts.Tokens))
	} else {
		lexicalUpTo(tree.Read)
	}
	return r.errs
}

// nativeErrorReporter turns each error of galop's recovering parse into a
// diagnostic, in order, with the coded hints and their cascade suppression.
type nativeErrorReporter struct {
	tree *native.GalaTree
	errs []error
	// reportedGoType is the node of the composite literal a GALA-E0040 has
	// been reported for, or -1. Once E0040 is reported, the recovery INSERTS
	// the '{' it wanted and carries on parsing the rest of the file as that
	// literal's element list, so every following token is re-reported against
	// the same node ("mismatched input 'Println' expecting {',', '}'}"). Those
	// follow-ups describe a brace the user never wrote, so they are dropped.
	// Keyed on the node, so an unrelated error elsewhere in the file is
	// untouched.
	reportedGoType int32
	// bareLambdaLine is the line a GALA-E0042 has been reported on, or 0. An
	// unparenthesized lambda parameter derails the parser for the rest of the
	// expression, so the parse re-reports it against each enclosing rule it
	// unwinds through — four errors for one missing pair of parentheses, all
	// on the same line.
	//
	// Keyed on the LINE, unlike reportedGoType: the follow-on errors are
	// reported from the different rules the parser unwinds through, so no
	// single node is common to them. The line is what they share. The
	// tradeoff is the usual one for cascade suppression: a genuinely
	// unrelated later error on the same line is swallowed too, and a lambda
	// whose body wraps onto the next line can still leak one follow-on. The
	// first error on any line is always reported, so nothing is hidden that
	// would not resurface on the next compile.
	bareLambdaLine int
}

// lexical reports a lexical error: no hint, but the bare-lambda cascade
// suppression applies.
func (r *nativeErrorReporter) lexical(l native.GalaSyntaxError) {
	if r.bareLambdaLine != 0 && l.Line == r.bareLambdaLine {
		return
	}
	r.errs = append(r.errs, galaerr.NewSyntaxError(l.Line, l.Column, hideNewlineTokens(l.Msg)))
}

// syntax reports a parse error: as GALA-E0040, GALA-E0042 or GALA-E0085 when
// it is one of theirs, else as the parser's message.
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

// compositeLiteral is the node e was reported in when it is a
// compositeLiteral, else no node.
func (r *nativeErrorReporter) compositeLiteral(e native.GalaParseError) native.CompositeLiteralNode {
	if n := r.node(e); n.Ok() && n.Rule() == native.GalaRULE_compositeLiteral {
		return native.CompositeLiteralNode{GalaNodeRef: n}
	}
	return native.CompositeLiteralNode{}
}

// nativeGoTypeInExpressionError turns the parse failure behind
// "missing '{' at …" into GALA-E0040 when — and only when — its cause is a Go
// slice or map type written in expression position. It returns nil for every
// other failure, so the raw syntax error still reaches the user unchanged.
//
// The gate is structural rather than message-based, because the recovery's
// wording is not part of any contract:
//
//   - the rule the error was reported in is `compositeLiteral`
//     (`type ('{' … '}')`). The parser only lands there for a bracketed
//     expression when the bracket contents are not an expression list, which
//     for `f[…](…)` means a type the expression grammar cannot spell;
//   - that node had exactly one child, the `type`, when the error was
//     reported (kids) — so the parse died at the '{' slot, before consuming a
//     brace. A malformed but genuine composite literal (`[]int{1, 2,`) has
//     consumed its brace by the time it fails and is left to the ordinary
//     syntax error. The recovered tree's node has more children by then (the
//     brace the recovery conjured, the tokens it skipped), so its ChildCount
//     does not say where the parse died;
//   - and that `type` actually contains a Go slice or map type. Without this
//     the code would fire on a func type, which is a different problem.
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

// findNativeGoSliceOrMapType is the first type node at or below n, in source
// order, whose text starts with "[]" or "map[", or no node. Searching the
// whole subtree is what lets the outer type be an ordinary generic — the
// offender in `EmptyHashMap[string, []byte]` is the type argument, not the
// literal's type.
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

// bareLambdaParamError recognizes a lambda written without parentheses around
// its single parameter — `x => e` where GALA requires `(x) => e` — and
// returns GALA-E0042 for it, or nil when the error is something else.
//
// The shape is identified from the tokens rather than the tree, because by
// the time the parser reports this the tree is error-recovered: the
// offending token is `=>` and the code token immediately before it is a
// plain IDENTIFIER. That is enough, since every legal `=>` in GALA is
// preceded by either `)` (a lambda's parameter list) or a pattern in a `case`
// arm — and a `case` arm's own `=>` is reached only after `case`, which
// parses fine. Comments are hidden tokens, not in Tokens, so the code token
// before the offending one is simply the one before it in Tokens: a comment
// between the parameter and the arrow does not mask the diagnostic.
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

// structTagError recognizes a Go struct tag written after a struct field's
// type, in either string form (`Name string "json:\"name\""`), and returns
// GALA-E0085 for it, or nil when the error is something else.
//
// GALA has no struct tags, so the grammar has no slot for one and the parser
// reports the string as extraneous input. The shape is a string literal that
// the parser hit in the list of a struct's fields, directly after a field and
// on its line: the field list of a block-form struct, the parameter list of a
// shorthand struct, or the field list of a sealed case. A string anywhere
// deeper, such as inside a field's default value, fails in a nested rule and
// is left to the ordinary syntax error, and so is one in a function's
// parameter list.
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

// lastNativeStructField is, when n, the rule the parser was in when it
// rejected a string literal, is a struct's field list (or, at a closing ')',
// the rule that holds the list), the last field parsed in it so far, else no
// node. n had kids
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

// isNativeShorthandStruct reports whether params is a shorthand struct's
// field list rather than a function's or a lambda's parameter list.
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
