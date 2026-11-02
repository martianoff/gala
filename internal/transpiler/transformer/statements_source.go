package transformer

import (
	"go/token"
	"unicode/utf8"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A body's blocks and statements as their lowering reads them, on galop's
// native tree (lowerBlock, lowerStatement, lowerIfStatement,
// lowerForStatement, lowerSimpleStatement), and so are the `bind` chains
// (bind.go) and the blocks of if-expression branches. The values of returns,
// `use` declarations, assignments, increments, short variable declarations
// and loop conditions are read as exprSrc and exprListSrc values
// (expressions_source.go, exprlist_source.go).
//
// The readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go). A block is reached from a function declaration's
// body (readFuncDeclNative), from a lambda's (lambdas_source.go), from a
// match arm's (match_source.go), from an if-expression branch's
// (ifBranchSrc.block) and from the if and for statements inside them.

// blockSrc is a block.
type blockSrc struct {
	node native.GalaNodeRef
}

// ok reports whether b is a block, not the zero blockSrc.
func (b blockSrc) ok() bool { return b.node.Ok() }

// at is where b is written.
func (b blockSrc) at() srcNode { return srcNode{node: b.node} }

// statements is b's statements, in order.
func (b blockSrc) statements() []stmtSrc {
	out := make([]stmtSrc, 0, b.node.ChildCount())
	for n := range galaparser.NativeKids(b.node, native.GalaKIND_statement) {
		out = append(out, nativeStmt(n))
	}
	return out
}

// lastStatement is b's last statement, and false when it has none.
func (b blockSrc) lastStatement() (stmtSrc, bool) {
	for i := b.node.ChildCount() - 1; i >= 0; i-- {
		if n := b.node.Child(i).Node(); n.Ok() && n.Kind() == native.GalaKIND_statement {
			return nativeStmt(n), true
		}
	}
	return stmtSrc{}, false
}

// stop is the line and column of b's closing brace, and false when it has
// none.
func (b blockSrc) stop() (line, col int, ok bool) {
	i := b.node.StopIndex()
	if i < 0 {
		return 0, 0, false
	}
	stop := b.node.Tree().Token(i)
	return int(stop.Line), int(stop.Column), true
}

// is reports whether b is the block o, a lambda's body (see
// returnSlot.body); false when o is the zero blockSrc.
func (b blockSrc) is(o blockSrc) bool {
	return b.node.Ok() && b.node == o.node
}

// stmtKind is what a statement is: a `return`, or the kind of its
// declaration.
type stmtKind uint8

const (
	stmtNone stmtKind = iota // no statement (parse-error recovery)
	stmtReturn
	stmtVal
	stmtVar
	stmtBind
	stmtAlso
	stmtUse
	stmtFunc
	stmtType
	stmtImport
	stmtIf
	stmtFor
	stmtSimple
)

// stmtSrc is a statement: where it is written, what it is, and kid, the
// statement's return statement or its declaration's alternative (a
// valDeclaration, an ifStatement, a simpleStatement, ...).
type stmtSrc struct {
	at   srcNode
	kind stmtKind
	kid  srcNode
	// simpleS is kid read as a simple statement, when s is one.
	simpleS simpleSrc
}

// nativeStmt reads n, a statement node.
func nativeStmt(n native.GalaNodeRef) stmtSrc {
	s := stmtSrc{at: srcNode{node: n}}
	kid, ok := firstKidNode(n)
	if !ok {
		return s
	}
	if kid.Kind() == native.GalaKIND_returnStatement {
		s.kind, s.kid = stmtReturn, srcNode{node: kid}
		return s
	}
	if kid, ok = firstKidNode(kid); !ok {
		return s
	}
	s.kid = srcNode{node: kid}
	switch kid.Kind() {
	case native.GalaKIND_valDeclaration:
		s.kind = stmtVal
	case native.GalaKIND_varDeclaration:
		s.kind = stmtVar
	case native.GalaKIND_bindDeclaration:
		s.kind = stmtBind
	case native.GalaKIND_alsoDeclaration:
		s.kind = stmtAlso
	case native.GalaKIND_useDeclaration:
		s.kind = stmtUse
	case native.GalaKIND_functionDeclaration:
		s.kind = stmtFunc
	case native.GalaKIND_typeDeclaration:
		s.kind = stmtType
	case native.GalaKIND_importDeclaration:
		s.kind = stmtImport
	case native.GalaKIND_ifStatement:
		s.kind = stmtIf
	case native.GalaKIND_forStatement:
		s.kind = stmtFor
	case native.GalaKIND_simpleStatement:
		s.kind, s.simpleS = stmtSimple, nativeSimple(kid)
	}
	return s
}

// firstKidNode is n's first child node, and false when it has none: a
// statement's or a declaration's one alternative.
func firstKidNode(n native.GalaNodeRef) (native.GalaNodeRef, bool) {
	for i := range n.ChildCount() {
		if kid := n.Child(i).Node(); kid.Ok() {
			return kid, true
		}
	}
	return native.GalaNodeRef{}, false
}

// line is the line s starts on, and false when it has no first token.
func (s stmtSrc) line() (int, bool) {
	if !s.at.node.Ok() {
		return 0, false
	}
	return int(s.at.node.Start().Line), true
}

// ifStmt is s's if-statement; the zero ifSrc when s is none.
func (s stmtSrc) ifStmt() ifSrc {
	if s.kind != stmtIf {
		return ifSrc{}
	}
	return ifSrc{node: s.kid.node}
}

// forStmt is s's for-statement; the zero forSrc when s is none.
func (s stmtSrc) forStmt() forSrc {
	if s.kind != stmtFor {
		return forSrc{}
	}
	return forSrc{node: s.kid.node}
}

// valueExpr is the expression of s when s is an expression statement, and
// the zero exprSrc otherwise.
func (s stmtSrc) valueExpr() exprSrc { return s.simpleS.exprSrc() }

// returnExpr is the value of s, a return statement, and the zero exprSrc
// when it has none.
func (s stmtSrc) returnExpr() exprSrc {
	if e := (native.ReturnStatementNode{GalaNodeRef: s.kid.node}).Expression(); e.Ok() {
		return nativeExpr(e.GalaNodeRef)
	}
	return exprSrc{}
}

// bindClause reads s, a bind or an also declaration.
func (s stmtSrc) bindClause() bindEntry {
	n := s.kid.node
	var (
		id   native.IdentifierNode
		expr native.ExpressionNode
		typ  native.TypeNode
	)
	if s.kind == stmtBind {
		d := native.BindDeclarationNode{GalaNodeRef: n}
		id, expr, typ = d.Identifier(), d.Expression(), d.Type_()
	} else {
		d := native.AlsoDeclarationNode{GalaNodeRef: n}
		id, expr, typ = d.Identifier(), d.Expression(), d.Type_()
	}
	e := bindEntry{name: galaparser.NativeText(id.GalaNodeRef), expr: nativeExpr(expr.GalaNodeRef), at: s.kid}
	if typ.Ok() {
		e.typ = nativeType(typ)
	}
	return e
}

// hoistsInitializer reports whether the value of s may be lowered as
// statements emitted before it: a `val` or `var` declaration, or an
// assignment statement of its own to a variable. Not the clauses of an `if`
// or `for`: a `for` post statement runs on every iteration, not once before
// the loop. Nor an assignment to an element or field: its index and receiver
// operands are evaluated before its value, so the value's statements cannot
// go first.
func (s stmtSrc) hoistsInitializer() bool {
	switch s.kind {
	case stmtVal, stmtVar:
		return true
	case stmtSimple:
		return s.simpleS.kind == simpleAssign && s.simpleS.assignsOneName()
	}
	return false
}

// use reads s, a use declaration.
func (s stmtSrc) use() useDecl {
	n := native.UseDeclarationNode{GalaNodeRef: s.kid.node}
	d := useDecl{name: n.Identifier().GetText(), expr: nativeExpr(n.Expression().GalaNodeRef)}
	if typ := n.Type_(); typ.Ok() {
		d.typ = nativeType(typ)
	}
	return d
}

// useDecl is `use name (typ) = expr`.
type useDecl struct {
	name string
	typ  typeSrc // no type when it has no annotation
	expr exprSrc
}

// binding reads s, a local val or var declaration.
func (s stmtSrc) binding(t *galaASTTransformer) bindingDecl {
	if s.kind == stmtVal {
		return t.readBindingDeclNative(native.ValDeclarationNode{GalaNodeRef: s.kid.node}, "val")
	}
	return t.readBindingDeclNative(native.VarDeclarationNode{GalaNodeRef: s.kid.node}, "var")
}

// nestedFunc reads s, a function declaration in a body.
func (s stmtSrc) nestedFunc(t *galaASTTransformer) nestedFunc {
	t.nativeShape(s.kid.node, "nested")
	n := native.FunctionDeclarationNode{GalaNodeRef: s.kid.node}
	id := n.Identifier()
	name, start := id.GetText(), n.Start()
	return nestedFunc{
		name:    name,
		line:    int(start.Line),
		col:     int(start.Column),
		endCol:  int(id.Stop().Column) + utf8.RuneCountInString(name),
		sig:     nativeSig(n.Signature()),
		method:  n.Receiver().Ok(),
		generic: n.TypeParameters().Ok(),
	}
}

// typeDecl reads s, a type declaration in a body.
func (s stmtSrc) typeDecl(t *galaASTTransformer) typeDecl {
	return t.readTypeDeclNative(native.TypeDeclarationNode{GalaNodeRef: s.kid.node})
}

// importSpecs reads s, an import declaration in a body.
func (s stmtSrc) importSpecs() []importSpecDecl {
	return readImportDeclNative(s.kid.node)
}

// simpleKind is what a simple statement is.
type simpleKind uint8

const (
	simpleNone simpleKind = iota // no statement (parse-error recovery)
	simpleIncDec
	simpleAssign
	simpleShortVar
	simpleExpr
)

// simpleSrc is a simple statement: where it is written, what it is, and its
// alternative kid (an incDecStmt, an assignment, a shortVarDecl or an
// expression).
type simpleSrc struct {
	at   srcNode
	kind simpleKind
	kid  srcNode
}

// nativeSimple reads n, a simpleStatement node.
func nativeSimple(n native.GalaNodeRef) simpleSrc {
	s := simpleSrc{at: srcNode{node: n}}
	kid, ok := firstKidNode(n)
	if !ok {
		return s
	}
	s.kid = srcNode{node: kid}
	switch kid.Kind() {
	case native.GalaKIND_incDecStmt:
		s.kind = simpleIncDec
	case native.GalaKIND_assignment:
		s.kind = simpleAssign
	case native.GalaKIND_shortVarDecl:
		s.kind = simpleShortVar
	case native.GalaKIND_expression:
		s.kind = simpleExpr
	}
	return s
}

// ok reports whether s is a simple statement, not the zero simpleSrc.
func (s simpleSrc) ok() bool { return s.at.ok() }

// exprSrc is the expression of s, an expression statement, and the zero
// exprSrc when s is another kind.
func (s simpleSrc) exprSrc() exprSrc {
	if s.kind != simpleExpr {
		return exprSrc{}
	}
	return nativeExpr(s.kid.node)
}

// assignsOneName reports whether s, an assignment, has one target and the
// target is a name.
func (s simpleSrc) assignsOneName() bool {
	lhs := native.AssignmentNode{GalaNodeRef: s.kid.node}.ExpressionList(0)
	target, ok := galaparser.OnlyNativeKid(lhs.GalaNodeRef, native.GalaKIND_expression)
	return ok && token.IsIdentifier(galaparser.NativeText(target))
}

// incDecDecl is an increment or decrement statement: where it is written,
// its operand, and whether it decrements.
type incDecDecl struct {
	at   srcNode
	expr exprSrc
	dec  bool
}

// incDec reads s, an increment or decrement.
func (s simpleSrc) incDec() incDecDecl {
	n := s.kid.node
	d := incDecDecl{at: s.kid, expr: nativeExpr(native.IncDecStmtNode{GalaNodeRef: n}.Expression().GalaNodeRef)}
	if n.ChildCount() >= 2 {
		if c := n.Child(1); c.IsToken() && c.Terminal().GetText() == "--" {
			d.dec = true
		}
	}
	return d
}

// assignDecl is an assignment: where it is written, its targets, its
// operator as written and its values.
type assignDecl struct {
	at       srcNode
	lhs, rhs exprListSrc
	op       string
}

// assign reads s, an assignment.
func (s simpleSrc) assign() assignDecl {
	n := s.kid.node
	return assignDecl{
		at:  s.kid,
		lhs: nativeExprList(n.Child(0).Node()),
		op:  n.Child(1).Terminal().GetText(),
		rhs: nativeExprList(n.Child(2).Node()),
	}
}

// shortVarDecl is `names := exprs`: where it is written, its names and its
// values.
type shortVarDecl struct {
	at    srcNode
	names []declName
	exprs exprListSrc
	// blankHint is GALA-E0060's hint when the names are a lone `_`.
	blankHint string
}

// shortVar reads s, a short variable declaration.
func (s simpleSrc) shortVar() shortVarDecl {
	n := s.kid.node
	sv := native.ShortVarDeclNode{GalaNodeRef: n}
	d := shortVarDecl{
		at:    s.kid,
		names: nativeDeclNames(sv.IdentifierList().GalaNodeRef),
		exprs: nativeExprList(sv.ExpressionList().GalaNodeRef),
	}
	if isLoneBlank(d.names) {
		d.blankHint = nativeBlankDeclarationHint(n, false)
	}
	return d
}

// isTopLevelDeclNative reports whether n is a package-level declaration.
func isTopLevelDeclNative(n native.GalaNodeRef) bool {
	p := n.Parent()
	return p.Ok() && p.Kind() == native.GalaKIND_topLevelDeclaration
}

// nativeBlankDeclarationHint is GALA-E0060's hint for n, by where the
// declaration sits: a package-level one when topLevel
// (isTopLevelDeclNative), else in a function or a lambda body.
func nativeBlankDeclarationHint(n native.GalaNodeRef, topLevel bool) string {
	if topLevel {
		return topLevelBlankHint
	}
	for p := n.Parent(); p.Ok(); p = p.Parent() {
		switch p.Kind() {
		case native.GalaKIND_functionDeclaration:
			return bodyBlankHint
		case native.GalaKIND_lambdaExpression:
			return lambdaBlankHint
		}
	}
	return bodyBlankHint
}

// ifSrc is an if-statement.
type ifSrc struct {
	node native.GalaNodeRef
}

// ok reports whether s is an if-statement, not the zero ifSrc.
func (s ifSrc) ok() bool { return s.node.Ok() }

// at is where s is written.
func (s ifSrc) at() srcNode { return srcNode{node: s.node} }

// native is s's native handle.
func (s ifSrc) native() native.IfStatementNode { return native.IfStatementNode{GalaNodeRef: s.node} }

// hasInit reports whether s has an initializer statement (`if init; cond`).
func (s ifSrc) hasInit() bool { return s.native().SimpleStatement().Ok() }

// cond is s's condition.
func (s ifSrc) cond() exprSrc { return nativeExpr(s.native().Expression().GalaNodeRef) }

// block is s's i-th block: 0 its body, 1 its else block; the zero blockSrc
// when it has none.
func (s ifSrc) block(i int) blockSrc {
	if b := s.native().Block(i); b.Ok() {
		return blockSrc{node: b.GalaNodeRef}
	}
	return blockSrc{}
}

// elseIf is the if-statement of s's `else if`; the zero ifSrc when it has
// none.
func (s ifSrc) elseIf() ifSrc {
	if e := s.native().IfStatement(); e.Ok() {
		return ifSrc{node: e.GalaNodeRef}
	}
	return ifSrc{}
}

// forSrc is a for-statement.
type forSrc struct {
	node native.GalaNodeRef
}

// native is s's native handle.
func (s forSrc) native() native.ForStatementNode { return native.ForStatementNode{GalaNodeRef: s.node} }

// body is s's block.
func (s forSrc) body() blockSrc { return blockSrc{node: s.native().Block().GalaNodeRef} }

// cond is the condition of s, a condition-only loop (`for cond { }`), and
// the zero exprSrc when s is another kind of loop.
func (s forSrc) cond() exprSrc {
	if c := s.native().ForCondition(); c.Ok() {
		return nativeExpr(c.Expression().GalaNodeRef)
	}
	return exprSrc{}
}

// rangeDecl is the range clause of a loop: where it is written, the names it
// binds (none, the key, or the key and the value), whether it assigns them
// (`=`) rather than declaring them (`:=`), and what it ranges over.
type rangeDecl struct {
	at     srcNode
	names  []declName
	assign bool
	expr   exprSrc
}

// rangeClause reads s's range clause, and false when s has none.
func (s forSrc) rangeClause() (rangeDecl, bool) {
	rc := s.native().RangeClause()
	if !rc.Ok() {
		return rangeDecl{}, false
	}
	d := rangeDecl{at: srcNode{node: rc.GalaNodeRef}, expr: nativeExpr(rc.Expression().GalaNodeRef)}
	for i := range rc.ChildCount() {
		if c := rc.Child(i); c.IsToken() && c.Terminal().GetText() == "=" {
			d.assign = true
			break
		}
	}
	if ids := rc.IdentifierList(); ids.Ok() {
		d.names = nativeDeclNames(ids.GalaNodeRef)
	}
	return d, true
}

// forClauseDecl is `for init; cond; post`: its init and post statements (the
// zero simpleSrc when omitted) and its condition (the zero exprSrc when
// omitted).
type forClauseDecl struct {
	init, post simpleSrc
	cond       exprSrc
}

// clause reads s's for clause, and false when s has none.
//
// Both statement slots are optional, so the statements are assigned by
// counting the `;` separators that precede each one rather than by their
// position in the list: nothing before the first separator is the init,
// anything after the second is the post.
func (s forSrc) clause() (forClauseDecl, bool) {
	fc := s.native().ForClause()
	if !fc.Ok() {
		return forClauseDecl{}, false
	}
	var d forClauseDecl
	if e := fc.Expression(); e.Ok() {
		d.cond = nativeExpr(e.GalaNodeRef)
	}
	semis := 0
	for i := range fc.ChildCount() {
		c := fc.Child(i)
		if c.IsToken() {
			if c.Terminal().GetText() == ";" {
				semis++
			}
			continue
		}
		if n := c.Node(); n.Ok() && n.Kind() == native.GalaKIND_simpleStatement {
			if semis == 0 {
				d.init = nativeSimple(n)
			} else {
				d.post = nativeSimple(n)
			}
		}
	}
	return d, true
}

// text is n's tokens' text, joined.
func (n srcNode) text() string { return galaparser.NativeText(n.node) }

// noteNativeStmt tells the onNativeStmt hook, when set, that the walk lowers
// s, as it starts to: each place that lowers a statement of a block (a
// block's, a `bind` chain's, an if-expression branch's) calls it.
func (t *galaASTTransformer) noteNativeStmt(s stmtSrc) {
	if t.onNativeStmt != nil && s.at.node.Ok() {
		t.onNativeStmt(s.at.node)
	}
}
