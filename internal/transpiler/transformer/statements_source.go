package transformer

import (
	"go/token"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// A body's blocks and statements as their lowering reads them. Each source
// below is a node of ANTLR's tree (ctx) or of galop's native one (node), and
// each of its readers has an ANTLR half and a native half that give the same
// value for the same node, so blocks and statements are lowered once, the
// same way, whichever tree they come from (lowerBlock, lowerStatement,
// lowerIfStatement, lowerForStatement, lowerSimpleStatement). Expressions,
// types and the bind chains below a statement are read on ANTLR's tree: the
// native readers cross to it with crossTo.
//
// The native halves dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go). A native block is reached only from a function
// declaration's body (readFuncDeclNative) and from the if and for statements
// inside it: a lambda's or a match arm's block is below an expression, which
// is still read on ANTLR's tree.

// blockSrc is a block.
type blockSrc struct {
	ctx  *grammar.BlockContext
	node native.GalaNodeRef
}

// antlrBlock is the block ctx, on ANTLR's tree.
func antlrBlock(ctx *grammar.BlockContext) blockSrc { return blockSrc{ctx: ctx} }

// ok reports whether b is a block, not the zero blockSrc.
func (b blockSrc) ok() bool { return b.ctx != nil || b.node.Ok() }

// at is where b is written.
func (b blockSrc) at() srcNode {
	switch {
	case b.node.Ok():
		return srcNode{node: b.node}
	case b.ctx != nil:
		return srcNode{ctx: b.ctx}
	}
	return srcNode{}
}

// statements is b's statements, in order.
func (b blockSrc) statements() []stmtSrc {
	if b.node.Ok() {
		out := make([]stmtSrc, 0, b.node.ChildCount())
		for n := range galaparser.NativeKids(b.node, native.GalaKIND_statement) {
			out = append(out, nativeStmt(n))
		}
		return out
	}
	all := b.ctx.AllStatement()
	out := make([]stmtSrc, len(all))
	for i, s := range all {
		out[i] = antlrStmt(s.(*grammar.StatementContext))
	}
	return out
}

// lastStatement is b's last statement, and false when it has none.
func (b blockSrc) lastStatement() (stmtSrc, bool) {
	if b.node.Ok() {
		for i := b.node.ChildCount() - 1; i >= 0; i-- {
			if n := b.node.Child(i).Node(); n.Ok() && n.Kind() == native.GalaKIND_statement {
				return nativeStmt(n), true
			}
		}
		return stmtSrc{}, false
	}
	all := b.ctx.AllStatement()
	if len(all) == 0 {
		return stmtSrc{}, false
	}
	return antlrStmt(all[len(all)-1].(*grammar.StatementContext)), true
}

// stop is the line and column of b's closing brace, and false when it has
// none.
func (b blockSrc) stop() (line, col int, ok bool) {
	if b.node.Ok() {
		i := b.node.StopIndex()
		if i < 0 {
			return 0, 0, false
		}
		stop := b.node.Tree().Token(i)
		return int(stop.Line), int(stop.Column), true
	}
	stop := b.ctx.GetStop()
	if stop == nil {
		return 0, 0, false
	}
	return stop.GetLine(), stop.GetColumn(), true
}

// is reports whether b is the block ctx, a lambda's body (see
// returnSlot.body); false when ctx is nil.
func (b blockSrc) is(t *galaASTTransformer, ctx *grammar.BlockContext) bool {
	if ctx == nil {
		return false
	}
	if b.node.Ok() {
		return crossTo[*grammar.BlockContext](t, b.node) == ctx
	}
	return b.ctx == ctx
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

// antlrStmt reads the statement ctx on ANTLR's tree.
func antlrStmt(ctx *grammar.StatementContext) stmtSrc {
	s := stmtSrc{at: srcNode{ctx: ctx}}
	kid := ruleKid(ctx)
	if kid == nil {
		return s
	}
	if _, ok := kid.(*grammar.ReturnStatementContext); ok {
		s.kind, s.kid = stmtReturn, srcNode{ctx: kid}
		return s
	}
	if kid = ruleKid(kid); kid == nil {
		return s
	}
	s.kid = srcNode{ctx: kid}
	switch kid.(type) {
	case *grammar.ValDeclarationContext:
		s.kind = stmtVal
	case *grammar.VarDeclarationContext:
		s.kind = stmtVar
	case *grammar.BindDeclarationContext:
		s.kind = stmtBind
	case *grammar.AlsoDeclarationContext:
		s.kind = stmtAlso
	case *grammar.UseDeclarationContext:
		s.kind = stmtUse
	case *grammar.FunctionDeclarationContext:
		s.kind = stmtFunc
	case *grammar.TypeDeclarationContext:
		s.kind = stmtType
	case *grammar.ImportDeclarationContext:
		s.kind = stmtImport
	case *grammar.IfStatementContext:
		s.kind = stmtIf
	case *grammar.ForStatementContext:
		s.kind = stmtFor
	case *grammar.SimpleStatementContext:
		s.kind, s.simpleS = stmtSimple, antlrSimple(kid.(*grammar.SimpleStatementContext))
	}
	return s
}

// nativeStmt is antlrStmt of n, a statement node.
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

// ruleKid is ctx's first child context, or nil when it has none: a
// statement's or a declaration's one alternative.
func ruleKid(ctx antlr.ParserRuleContext) antlr.ParserRuleContext {
	for _, c := range ctx.GetChildren() {
		if kid, ok := c.(antlr.ParserRuleContext); ok {
			return kid
		}
	}
	return nil
}

// firstKidNode is ruleKid of n.
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
	if s.at.node.Ok() {
		return int(s.at.node.Start().Line), true
	}
	if start := s.at.ctx.GetStart(); start != nil {
		return start.GetLine(), true
	}
	return 0, false
}

// antlr is s's context: where a lowering that reads ANTLR's tree whole (a
// bind chain) takes it over.
func (s stmtSrc) antlr(t *galaASTTransformer) *grammar.StatementContext {
	if s.at.node.Ok() {
		return crossTo[*grammar.StatementContext](t, s.at.node)
	}
	return s.at.ctx.(*grammar.StatementContext)
}

// antlrStmts is the contexts of stmts (see stmtSrc.antlr).
func antlrStmts(t *galaASTTransformer, stmts []stmtSrc) []grammar.IStatementContext {
	out := make([]grammar.IStatementContext, len(stmts))
	for i, s := range stmts {
		out[i] = s.antlr(t)
	}
	return out
}

// ifStmt is s's if-statement; the zero ifSrc when s is none.
func (s stmtSrc) ifStmt() ifSrc {
	switch {
	case s.kind != stmtIf:
		return ifSrc{}
	case s.kid.node.Ok():
		return ifSrc{node: s.kid.node}
	}
	return ifSrc{ctx: s.kid.ctx.(*grammar.IfStatementContext)}
}

// forStmt is s's for-statement; the zero forSrc when s is none.
func (s stmtSrc) forStmt() forSrc {
	switch {
	case s.kind != stmtFor:
		return forSrc{}
	case s.kid.node.Ok():
		return forSrc{node: s.kid.node}
	}
	return forSrc{ctx: s.kid.ctx.(*grammar.ForStatementContext)}
}

// valueExpr is the expression of s when s is an expression statement, and
// nil otherwise.
func (s stmtSrc) valueExpr(t *galaASTTransformer) grammar.IExpressionContext {
	return s.simpleS.expr(t)
}

// returnExpr is the value of s, a return statement, and nil when it has
// none.
func (s stmtSrc) returnExpr(t *galaASTTransformer) grammar.IExpressionContext {
	if s.kid.node.Ok() {
		if e := (native.ReturnStatementNode{GalaNodeRef: s.kid.node}).Expression(); e.Ok() {
			return crossTo[*grammar.ExpressionContext](t, e.GalaNodeRef)
		}
		return nil
	}
	return s.kid.ctx.(*grammar.ReturnStatementContext).Expression()
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
func (s stmtSrc) use(t *galaASTTransformer) useDecl {
	if s.kid.node.Ok() {
		n := native.UseDeclarationNode{GalaNodeRef: s.kid.node}
		d := useDecl{name: n.Identifier().GetText(), expr: crossTo[*grammar.ExpressionContext](t, n.Expression().GalaNodeRef)}
		if typ := n.Type_(); typ.Ok() {
			d.typ = crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
		}
		return d
	}
	ctx := s.kid.ctx.(*grammar.UseDeclarationContext)
	return useDecl{name: ctx.Identifier().GetText(), typ: ctx.Type_(), expr: ctx.Expression()}
}

// useDecl is `use name (typ) = expr`.
type useDecl struct {
	name string
	typ  grammar.ITypeContext // nil when it has no annotation
	expr grammar.IExpressionContext
}

// binding reads s, a local val or var declaration.
func (s stmtSrc) binding(t *galaASTTransformer) bindingDecl {
	keyword := "var"
	if s.kind == stmtVal {
		keyword = "val"
	}
	if s.kid.node.Ok() {
		if s.kind == stmtVal {
			return t.readBindingDeclNative(native.ValDeclarationNode{GalaNodeRef: s.kid.node}, keyword)
		}
		return t.readBindingDeclNative(native.VarDeclarationNode{GalaNodeRef: s.kid.node}, keyword)
	}
	return readBindingDecl(s.kid.ctx.(tupleDeclaration), keyword)
}

// nestedFunc reads s, a function declaration in a body.
func (s stmtSrc) nestedFunc(t *galaASTTransformer) nestedFunc {
	if s.kid.node.Ok() {
		n := native.FunctionDeclarationNode{GalaNodeRef: s.kid.node}
		id := n.Identifier()
		name, start := id.GetText(), n.Start()
		return nestedFunc{
			name:    name,
			line:    int(start.Line),
			col:     int(start.Column),
			endCol:  int(id.Stop().Column) + utf8.RuneCountInString(name),
			sig:     crossTo[*grammar.SignatureContext](t, n.Signature().GalaNodeRef),
			method:  n.Receiver().Ok(),
			generic: n.TypeParameters().Ok(),
		}
	}
	return readNestedFunc(s.kid.ctx.(*grammar.FunctionDeclarationContext))
}

// typeDecl reads s, a type declaration in a body.
func (s stmtSrc) typeDecl(t *galaASTTransformer) typeDecl {
	if s.kid.node.Ok() {
		return t.readTypeDeclNative(native.TypeDeclarationNode{GalaNodeRef: s.kid.node})
	}
	return readTypeDecl(s.kid.ctx.(*grammar.TypeDeclarationContext))
}

// importSpecs reads s, an import declaration in a body.
func (s stmtSrc) importSpecs() []importSpecDecl {
	if s.kid.node.Ok() {
		return readImportDeclNative(s.kid.node)
	}
	return readImportDecl(s.kid.ctx.(*grammar.ImportDeclarationContext))
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

// antlrSimple reads the simple statement ctx on ANTLR's tree.
func antlrSimple(ctx grammar.ISimpleStatementContext) simpleSrc {
	s := simpleSrc{at: srcNode{ctx: ctx}}
	kid := ruleKid(ctx)
	if kid == nil {
		return s
	}
	s.kid = srcNode{ctx: kid}
	switch kid.(type) {
	case *grammar.IncDecStmtContext:
		s.kind = simpleIncDec
	case *grammar.AssignmentContext:
		s.kind = simpleAssign
	case *grammar.ShortVarDeclContext:
		s.kind = simpleShortVar
	case *grammar.ExpressionContext:
		s.kind = simpleExpr
	}
	return s
}

// nativeSimple is antlrSimple of n, a simpleStatement node.
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

// expr is the expression of s, an expression statement, and nil when s is
// another kind.
func (s simpleSrc) expr(t *galaASTTransformer) grammar.IExpressionContext {
	switch {
	case s.kind != simpleExpr:
		return nil
	case s.kid.node.Ok():
		return crossTo[*grammar.ExpressionContext](t, s.kid.node)
	}
	return s.kid.ctx.(grammar.IExpressionContext)
}

// assignsOneName reports whether s, an assignment, has one target and the
// target is a name.
func (s simpleSrc) assignsOneName() bool {
	if s.kid.node.Ok() {
		lhs := native.AssignmentNode{GalaNodeRef: s.kid.node}.ExpressionList(0)
		target, ok := galaparser.OnlyNativeKid(lhs.GalaNodeRef, native.GalaKIND_expression)
		return ok && token.IsIdentifier(galaparser.NativeText(target))
	}
	targets := s.kid.ctx.(*grammar.AssignmentContext).AllExpressionList()[0].AllExpression()
	return len(targets) == 1 && token.IsIdentifier(targets[0].GetText())
}

// incDecDecl is an increment or decrement statement: where it is written,
// its operand, and whether it decrements.
type incDecDecl struct {
	at   srcNode
	expr grammar.IExpressionContext
	dec  bool
}

// incDec reads s, an increment or decrement.
func (s simpleSrc) incDec(t *galaASTTransformer) incDecDecl {
	if n := s.kid.node; n.Ok() {
		d := incDecDecl{at: s.kid, expr: crossTo[*grammar.ExpressionContext](t, native.IncDecStmtNode{GalaNodeRef: n}.Expression().GalaNodeRef)}
		if n.ChildCount() >= 2 {
			if c := n.Child(1); c.IsToken() && c.Terminal().GetText() == "--" {
				d.dec = true
			}
		}
		return d
	}
	ctx := s.kid.ctx.(*grammar.IncDecStmtContext)
	d := incDecDecl{at: s.kid, expr: ctx.Expression()}
	if ctx.GetChildCount() >= 2 {
		if term, ok := ctx.GetChild(1).(antlr.TerminalNode); ok && term.GetText() == "--" {
			d.dec = true
		}
	}
	return d
}

// assignDecl is an assignment: where it is written, its targets, its
// operator as written and its values.
type assignDecl struct {
	at       srcNode
	lhs, rhs *grammar.ExpressionListContext
	op       string
}

// assign reads s, an assignment.
func (s simpleSrc) assign(t *galaASTTransformer) assignDecl {
	if n := s.kid.node; n.Ok() {
		return assignDecl{
			at:  s.kid,
			lhs: crossTo[*grammar.ExpressionListContext](t, n.Child(0).Node()),
			op:  n.Child(1).Terminal().GetText(),
			rhs: crossTo[*grammar.ExpressionListContext](t, n.Child(2).Node()),
		}
	}
	ctx := s.kid.ctx.(*grammar.AssignmentContext)
	return assignDecl{
		at:  s.kid,
		lhs: ctx.GetChild(0).(*grammar.ExpressionListContext),
		op:  ctx.GetChild(1).(antlr.TerminalNode).GetText(),
		rhs: ctx.GetChild(2).(*grammar.ExpressionListContext),
	}
}

// shortVarDecl is `names := exprs`: where it is written, its names and its
// values.
type shortVarDecl struct {
	at    srcNode
	names []declName
	exprs *grammar.ExpressionListContext
	// blankHint is GALA-E0060's hint when the names are a lone `_`.
	blankHint string
}

// shortVar reads s, a short variable declaration.
func (s simpleSrc) shortVar(t *galaASTTransformer) shortVarDecl {
	if n := s.kid.node; n.Ok() {
		sv := native.ShortVarDeclNode{GalaNodeRef: n}
		d := shortVarDecl{
			at:    s.kid,
			names: nativeDeclNames(sv.IdentifierList().GalaNodeRef),
			exprs: crossTo[*grammar.ExpressionListContext](t, sv.ExpressionList().GalaNodeRef),
		}
		if isLoneBlank(d.names) {
			d.blankHint = nativeBlankDeclarationHint(n, false)
		}
		return d
	}
	ctx := s.kid.ctx.(*grammar.ShortVarDeclContext)
	d := shortVarDecl{
		at:    s.kid,
		names: ctxDeclNames(ctx.IdentifierList()),
		exprs: ctx.ExpressionList().(*grammar.ExpressionListContext),
	}
	if isLoneBlank(d.names) {
		d.blankHint = blankDeclarationHint(ctx)
	}
	return d
}

// isTopLevelDeclNative is isTopLevelDecl of n.
func isTopLevelDeclNative(n native.GalaNodeRef) bool {
	p := n.Parent()
	return p.Ok() && p.Kind() == native.GalaKIND_topLevelDeclaration
}

// nativeBlankDeclarationHint is blankDeclarationHint of n, a package-level
// declaration when topLevel (isTopLevelDeclNative).
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
	ctx  *grammar.IfStatementContext
	node native.GalaNodeRef
}

// ok reports whether s is an if-statement, not the zero ifSrc.
func (s ifSrc) ok() bool { return s.ctx != nil || s.node.Ok() }

// at is where s is written.
func (s ifSrc) at() srcNode {
	switch {
	case s.node.Ok():
		return srcNode{node: s.node}
	case s.ctx != nil:
		return srcNode{ctx: s.ctx}
	}
	return srcNode{}
}

// native is s's native handle.
func (s ifSrc) native() native.IfStatementNode { return native.IfStatementNode{GalaNodeRef: s.node} }

// hasInit reports whether s has an initializer statement (`if init; cond`).
func (s ifSrc) hasInit() bool {
	if s.node.Ok() {
		return s.native().SimpleStatement().Ok()
	}
	return s.ctx.SimpleStatement() != nil
}

// cond is s's condition.
func (s ifSrc) cond(t *galaASTTransformer) grammar.IExpressionContext {
	if s.node.Ok() {
		return crossTo[*grammar.ExpressionContext](t, s.native().Expression().GalaNodeRef)
	}
	return s.ctx.Expression()
}

// block is s's i-th block: 0 its body, 1 its else block; the zero blockSrc
// when it has none.
func (s ifSrc) block(i int) blockSrc {
	if s.node.Ok() {
		if b := s.native().Block(i); b.Ok() {
			return blockSrc{node: b.GalaNodeRef}
		}
		return blockSrc{}
	}
	if b, ok := s.ctx.Block(i).(*grammar.BlockContext); ok && b != nil {
		return blockSrc{ctx: b}
	}
	return blockSrc{}
}

// elseIf is the if-statement of s's `else if`; the zero ifSrc when it has
// none.
func (s ifSrc) elseIf() ifSrc {
	if s.node.Ok() {
		if e := s.native().IfStatement(); e.Ok() {
			return ifSrc{node: e.GalaNodeRef}
		}
		return ifSrc{}
	}
	if e, ok := s.ctx.IfStatement().(*grammar.IfStatementContext); ok && e != nil {
		return ifSrc{ctx: e}
	}
	return ifSrc{}
}

// forSrc is a for-statement.
type forSrc struct {
	ctx  *grammar.ForStatementContext
	node native.GalaNodeRef
}

// native is s's native handle.
func (s forSrc) native() native.ForStatementNode { return native.ForStatementNode{GalaNodeRef: s.node} }

// body is s's block.
func (s forSrc) body() blockSrc {
	if s.node.Ok() {
		return blockSrc{node: s.native().Block().GalaNodeRef}
	}
	return blockSrc{ctx: s.ctx.Block().(*grammar.BlockContext)}
}

// cond is the condition of s, a condition-only loop (`for cond { }`), and nil
// when s is another kind of loop.
func (s forSrc) cond(t *galaASTTransformer) grammar.IExpressionContext {
	if s.node.Ok() {
		if c := s.native().ForCondition(); c.Ok() {
			return crossTo[*grammar.ExpressionContext](t, c.Expression().GalaNodeRef)
		}
		return nil
	}
	if c := s.ctx.ForCondition(); c != nil {
		return c.(*grammar.ForConditionContext).Expression()
	}
	return nil
}

// rangeDecl is the range clause of a loop: where it is written, the names it
// binds (none, the key, or the key and the value), whether it assigns them
// (`=`) rather than declaring them (`:=`), and what it ranges over.
type rangeDecl struct {
	at     srcNode
	names  []declName
	assign bool
	expr   grammar.IExpressionContext
}

// rangeClause reads s's range clause, and false when s has none.
func (s forSrc) rangeClause(t *galaASTTransformer) (rangeDecl, bool) {
	if s.node.Ok() {
		rc := s.native().RangeClause()
		if !rc.Ok() {
			return rangeDecl{}, false
		}
		d := rangeDecl{at: srcNode{node: rc.GalaNodeRef}, expr: crossTo[*grammar.ExpressionContext](t, rc.Expression().GalaNodeRef)}
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
	rc, ok := s.ctx.RangeClause().(*grammar.RangeClauseContext)
	if !ok || rc == nil {
		return rangeDecl{}, false
	}
	d := rangeDecl{at: srcNode{ctx: rc}, expr: rc.Expression()}
	for _, c := range rc.GetChildren() {
		if term, ok := c.(antlr.TerminalNode); ok && term.GetText() == "=" {
			d.assign = true
			break
		}
	}
	if ids := rc.IdentifierList(); ids != nil {
		d.names = ctxDeclNames(ids)
	}
	return d, true
}

// forClauseDecl is `for init; cond; post`: its init and post statements (the
// zero simpleSrc when omitted) and its condition (nil when omitted).
type forClauseDecl struct {
	init, post simpleSrc
	cond       grammar.IExpressionContext
}

// clause reads s's for clause, and false when s has none.
//
// Both statement slots are optional, so the statements are assigned by
// counting the `;` separators that precede each one rather than by their
// position in the list: nothing before the first separator is the init,
// anything after the second is the post.
func (s forSrc) clause(t *galaASTTransformer) (forClauseDecl, bool) {
	if s.node.Ok() {
		fc := s.native().ForClause()
		if !fc.Ok() {
			return forClauseDecl{}, false
		}
		var d forClauseDecl
		if e := fc.Expression(); e.Ok() {
			d.cond = crossTo[*grammar.ExpressionContext](t, e.GalaNodeRef)
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
	fc, ok := s.ctx.ForClause().(*grammar.ForClauseContext)
	if !ok || fc == nil {
		return forClauseDecl{}, false
	}
	d := forClauseDecl{cond: fc.Expression()}
	semis := 0
	for _, child := range fc.GetChildren() {
		switch node := child.(type) {
		case antlr.TerminalNode:
			if node.GetText() == ";" {
				semis++
			}
		case *grammar.SimpleStatementContext:
			if semis == 0 {
				d.init = antlrSimple(node)
			} else {
				d.post = antlrSimple(node)
			}
		}
	}
	return d, true
}
