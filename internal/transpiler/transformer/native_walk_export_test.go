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

// byTree is a native-walk hook that tells f of each node of the file's tree,
// by its node index and its first line, and parsed of each node of an
// expression parsed on its own (a default value or an interpolated
// expression, see parseExprAt), by its first line: the walk tests count the
// file tree's nodes against that tree, by node index, and another tree
// numbers its nodes from 0 too.
func (t *galaASTTransformer) byTree(f func(index, line int), parsed func(line int)) func(native.GalaNodeRef) {
	return func(n native.GalaNodeRef) {
		line := int(n.Start().Line)
		if tree, err := t.native.Tree(); err == nil && n.Tree() == tree {
			f(n.Index(), line)
		} else {
			parsed(line)
		}
	}
}

// OnNativeStmt sets tr's onNativeStmt hook for TestNativeWalkAgrees (see
// byTree).
func OnNativeStmt(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeStmt = t.byTree(f, parsed)
}

// EscapesAt is tr's escapesAt of the native node n.
func EscapesAt(tr transpiler.ASTTransformer, n native.GalaNodeRef) bool {
	return tr.(*galaASTTransformer).escapesAt(srcNode{node: n})
}

// OnNativeType sets tr's onNativeType hook for TestNativeTypeCases.
func OnNativeType(tr transpiler.ASTTransformer, f func(line int)) {
	tr.(*galaASTTransformer).onNativeType = f
}

// OnNativeLambda sets tr's onNativeLambda hook for TestNativeWalkAgrees (see
// byTree).
func OnNativeLambda(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeLambda = t.byTree(f, parsed)
}

// OnNativeCapture sets tr's onNativeCapture hook for TestNativeWalkCases.
func OnNativeCapture(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeCapture = f
}

// OnNativeExpr sets tr's onNativeExpr hook for the native walk tests (see
// byTree).
func OnNativeExpr(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeExpr = t.byTree(f, parsed)
}

// OnNativeShape sets tr's onNativeShape hook for TestNativeExprWalk.
func OnNativeShape(tr transpiler.ASTTransformer, f func(line int, shape string)) {
	tr.(*galaASTTransformer).onNativeShape = f
}

// OnHint sets tr's hint stack's observe hook for checkWalksAgree.
func OnHint(tr transpiler.ASTTransformer, f func(op string, owned bool)) {
	tr.(*galaASTTransformer).expectedArgTypes.observe = f
}

// OnNativeCase sets tr's onNativeCase hook for TestNativeWalkAgrees (see
// byTree).
func OnNativeCase(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeCase = t.byTree(f, parsed)
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
			ctx := t.native.Ctx(n).(*grammar.PostfixExprContext)
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
