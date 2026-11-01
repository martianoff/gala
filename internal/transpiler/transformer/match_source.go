package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// A match's case clauses as its lowering reads them (see patterns_source.go
// for their patterns). A match is reached from an expression, which is read
// on ANTLR's tree; its case clauses are read on the native one while the
// file's walk reads that tree (see matchCases). The guard, the subject and
// what an arm's body holds that is an expression are lowered on ANTLR's tree;
// an arm's block and its statements are lowered on the native one, as a
// function body's are (statements_source.go).

// caseSrc is a case clause.
type caseSrc struct {
	ctx  *grammar.CaseClauseContext
	node native.GalaNodeRef
}

// antlrCases is ctxs, on ANTLR's tree.
func antlrCases(ctxs []grammar.ICaseClauseContext) []caseSrc {
	out := make([]caseSrc, len(ctxs))
	for i, c := range ctxs {
		out[i] = caseSrc{ctx: c.(*grammar.CaseClauseContext)}
	}
	return out
}

// matchCases is the case clauses of the match ctx: read on the native tree
// when the file's walk reads it (t.native holds its handle, see walkSource)
// and ctx is a node of it, on ANTLR's tree otherwise — a match the file does
// not hold, such as a default written in another file. Once expressions are
// read on the native tree, their lowering hands a match over as its node and
// this lookup goes.
func (t *galaASTTransformer) matchCases(ctx *grammar.PostfixExprContext) []caseSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_postfixExpr); ok {
		var out []caseSrc
		for c := range galaparser.NativeKids(n, native.GalaKIND_caseClause) {
			if t.onNativeCase != nil {
				t.onNativeCase(c.Index(), int(c.Start().Line))
			}
			out = append(out, caseSrc{node: c})
		}
		return out
	}
	return antlrCases(ctx.AllCaseClause())
}

// at is where c is written.
func (c caseSrc) at() srcNode {
	if c.node.Ok() {
		return srcNode{node: c.node}
	}
	return srcNode{ctx: c.ctx}
}

// clause is c's node as a case clause.
func (c caseSrc) clause() native.CaseClauseNode { return native.CaseClauseNode{GalaNodeRef: c.node} }

// pattern is c's pattern.
func (c caseSrc) pattern() patSrc {
	if c.node.Ok() {
		return nativePat(c.clause().Pattern().GalaNodeRef)
	}
	return antlrPat(c.ctx.Pattern())
}

// hasGuard reports whether c has an `if` guard.
func (c caseSrc) hasGuard() bool {
	if c.node.Ok() {
		return c.clause().GetGuard().Ok()
	}
	return c.ctx.GetGuard() != nil
}

// guard is c's guard, the zero exprSrc when it has none.
func (c caseSrc) guard() exprSrc {
	if c.node.Ok() {
		if g := c.clause().GetGuard(); g.Ok() {
			return nativeExpr(g.GalaNodeRef)
		}
		return exprSrc{}
	}
	if g := c.ctx.GetGuard(); g != nil {
		return antlrExpr(g)
	}
	return exprSrc{}
}

// bodyBlock is c's block body, the zero blockSrc when its body is a
// statement.
func (c caseSrc) bodyBlock() blockSrc {
	if c.node.Ok() {
		if b := c.clause().GetBodyBlock(); b.Ok() {
			return blockSrc{node: b.GalaNodeRef}
		}
		return blockSrc{}
	}
	if b, ok := c.ctx.GetBodyBlock().(*grammar.BlockContext); ok {
		return antlrBlock(b)
	}
	return blockSrc{}
}

// bodyStmt is c's statement body, and false when its body is a block.
func (c caseSrc) bodyStmt() (simpleSrc, bool) {
	if c.node.Ok() {
		if s := c.clause().GetBodyStmt(); s.Ok() {
			return nativeSimple(s.GalaNodeRef), true
		}
		return simpleSrc{}, false
	}
	if s := c.ctx.GetBodyStmt(); s != nil {
		return antlrSimple(s), true
	}
	return simpleSrc{}, false
}

// antlr is c's context, for the readers of ANTLR's tree it reaches (the
// escape scan of hoisted_value.go).
func (c caseSrc) antlr(t *galaASTTransformer) *grammar.CaseClauseContext {
	if c.node.Ok() {
		return crossTo[*grammar.CaseClauseContext](t, c.node)
	}
	return c.ctx
}

// antlrClauses is the contexts of cases (see caseSrc.antlr).
func antlrClauses(t *galaASTTransformer, cases []caseSrc) []*grammar.CaseClauseContext {
	out := make([]*grammar.CaseClauseContext, len(cases))
	for i, c := range cases {
		out[i] = c.antlr(t)
	}
	return out
}
