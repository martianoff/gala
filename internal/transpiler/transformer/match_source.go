package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A match's case clauses as its lowering reads them (see patterns_source.go
// for their patterns), on the native tree (lowerMatch): its subject, and its
// case clauses with their patterns, guards and bodies.

// caseSrc is a case clause.
type caseSrc struct {
	node native.GalaNodeRef
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

// casesOf is the case clauses of n, a match's postfix expression or a
// partial function (nativeCases).
func (t *galaASTTransformer) casesOf(n native.GalaNodeRef) []caseSrc {
	return t.nativeCases(n)
}

// at is where c is written.
func (c caseSrc) at() srcNode { return srcNode{node: c.node} }

// clause is c's node as a case clause.
func (c caseSrc) clause() native.CaseClauseNode { return native.CaseClauseNode{GalaNodeRef: c.node} }

// pattern is c's pattern.
func (c caseSrc) pattern() patSrc { return nativePat(c.clause().Pattern().GalaNodeRef) }

// hasGuard reports whether c has an `if` guard.
func (c caseSrc) hasGuard() bool { return c.clause().GetGuard().Ok() }

// guard is c's guard, the zero exprSrc when it has none.
func (c caseSrc) guard() exprSrc {
	if g := c.clause().GetGuard(); g.Ok() {
		return nativeExpr(g.GalaNodeRef)
	}
	return exprSrc{}
}

// bodyBlock is c's block body, the zero blockSrc when its body is a
// statement.
func (c caseSrc) bodyBlock() blockSrc {
	if b := c.clause().GetBodyBlock(); b.Ok() {
		return blockSrc{node: b.GalaNodeRef}
	}
	return blockSrc{}
}

// bodyStmt is c's statement body, and false when its body is a block.
func (c caseSrc) bodyStmt() (simpleSrc, bool) {
	if s := c.clause().GetBodyStmt(); s.Ok() {
		return nativeSimple(s.GalaNodeRef), true
	}
	return simpleSrc{}, false
}

// casesEscape reports whether a clause of cases escapesConstruct
// (escapesAt).
func (t *galaASTTransformer) casesEscape(cases []caseSrc) bool {
	for _, c := range cases {
		if t.escapesAt(c.at()) {
			return true
		}
	}
	return false
}
