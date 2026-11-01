package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// A match's case clauses as its lowering reads them (see patterns_source.go
// for their patterns). A match is lowered on the tree its expression is read
// on (lowerMatch): its subject, and its case clauses with their patterns,
// guards and bodies. A match that a lowering still on ANTLR's tree reaches is
// lowered on its native node when the file's walk reads that tree
// (transformPostfixMatchExpression).

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

// nativeCases is the case clauses of n, a match's postfix expression or a
// partial function, each reported to the onNativeCase hook.
func (t *galaASTTransformer) nativeCases(n native.GalaNodeRef) []caseSrc {
	var out []caseSrc
	for c := range galaparser.NativeKids(n, native.GalaKIND_caseClause) {
		if t.onNativeCase != nil {
			t.onNativeCase(c)
		}
		out = append(out, caseSrc{node: c})
	}
	return out
}

// caseHolder is a context with case clauses: a match's postfix expression
// or a partial function.
type caseHolder interface {
	AllCaseClause() []grammar.ICaseClauseContext
}

// casesOf is the case clauses of a match or a partial function, on its tree:
// those of n on the native tree, each reported to the onNativeCase hook
// (nativeCases), and those of ctx otherwise.
func (t *galaASTTransformer) casesOf(n native.GalaNodeRef, ctx caseHolder) []caseSrc {
	if n.Ok() {
		return t.nativeCases(n)
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

// casesEscape reports whether a clause of cases escapesConstruct, each read
// on its tree (escapesAt).
func (t *galaASTTransformer) casesEscape(cases []caseSrc) bool {
	for _, c := range cases {
		if t.escapesAt(c.at()) {
			return true
		}
	}
	return false
}
