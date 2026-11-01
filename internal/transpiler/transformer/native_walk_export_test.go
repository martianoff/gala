package transformer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// OnNativeDecl sets tr's onNativeDecl hook for TestNativeWalkAgrees.
func OnNativeDecl(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeDecl = f
}

// OnNativeStmt sets tr's onNativeStmt hook for TestNativeWalkAgrees.
func OnNativeStmt(tr transpiler.ASTTransformer, f func(index, line int)) {
	tr.(*galaASTTransformer).onNativeStmt = f
}

// OnNativeType sets tr's onNativeType hook for TestNativeTypeCases.
func OnNativeType(tr transpiler.ASTTransformer, f func(line int)) {
	tr.(*galaASTTransformer).onNativeType = f
}

// OnNativeLambda sets tr's onNativeLambda hook for TestNativeWalkAgrees.
func OnNativeLambda(tr transpiler.ASTTransformer, f func(index int)) {
	tr.(*galaASTTransformer).onNativeLambda = f
}

// OnNativeCapture sets tr's onNativeCapture hook for TestNativeWalkCases.
func OnNativeCapture(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeCapture = f
}

// OnNativeExpr sets tr's onNativeExpr hook for the native walk tests.
func OnNativeExpr(tr transpiler.ASTTransformer, f func(index, line int)) {
	tr.(*galaASTTransformer).onNativeExpr = f
}

// OnNativeShape sets tr's onNativeShape hook for TestNativeExprWalk.
func OnNativeShape(tr transpiler.ASTTransformer, f func(line int, shape string)) {
	tr.(*galaASTTransformer).onNativeShape = f
}

// OnHint sets tr's hint stack's observe hook for checkWalksAgree.
func OnHint(tr transpiler.ASTTransformer, f func(op string, owned bool)) {
	tr.(*galaASTTransformer).expectedArgTypes.observe = f
}

// OnNativeCase sets tr's onNativeCase hook for TestNativeWalkAgrees.
func OnNativeCase(tr transpiler.ASTTransformer, f func(index, line int)) {
	tr.(*galaASTTransformer).onNativeCase = f
}

// HintKeyMismatches is, for the file tr is lowering on the native tree, the
// lines of the postfix expressions whose hint key read through their ANTLR
// context differs from the one read on their native node: a value lowered
// from ANTLR's tree pushes its hint under the first, and the call it is,
// lowered on the native tree, takes it under the second (see ownerKey).
func HintKeyMismatches(tr transpiler.ASTTransformer) []int {
	t := tr.(*galaASTTransformer)
	root, ok := t.native.BridgedRoot()
	if !ok {
		return nil
	}
	var lines []int
	var walk func(n native.GalaNodeRef)
	walk = func(n native.GalaNodeRef) {
		if n.Kind() == native.GalaKIND_postfixExpr {
			ctx := crossTo[*grammar.PostfixExprContext](t, n)
			if t.hintKey(postfixSrc{ctx: ctx}) != t.hintKey(postfixSrc{node: n}) {
				lines = append(lines, int(n.Start().Line))
			}
		}
		for i := range n.ChildCount() {
			if c := n.Child(i).Node(); c.Ok() {
				walk(c)
			}
		}
	}
	walk(root)
	return lines
}
