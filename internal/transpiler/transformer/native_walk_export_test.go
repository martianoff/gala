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

// inFileTree reports whether n is a node of the native tree the file's walk
// reads, not of an expression parsed on its own (a default value or an
// interpolated expression, see parseExprAt): the walk tests count the file
// tree's nodes against that tree, by node index.
func (t *galaASTTransformer) inFileTree(n native.GalaNodeRef) bool {
	tree, err := t.native.Tree()
	return err == nil && n.Tree() == tree
}

// OnNativeStmt sets tr's onNativeStmt hook for TestNativeWalkAgrees: f is
// told of each statement of the file's tree, by its node index and its first
// line.
func OnNativeStmt(tr transpiler.ASTTransformer, f func(index, line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeStmt = func(n native.GalaNodeRef) {
		if t.inFileTree(n) {
			f(n.Index(), int(n.Start().Line))
		}
	}
}

// OnNativeType sets tr's onNativeType hook for TestNativeTypeCases.
func OnNativeType(tr transpiler.ASTTransformer, f func(line int)) {
	tr.(*galaASTTransformer).onNativeType = f
}

// OnNativeLambda sets tr's onNativeLambda hook for TestNativeWalkAgrees: f
// is told of each lambda of the file's tree, by its node index, and parsed of
// each one of an expression parsed on its own, by its first line.
func OnNativeLambda(tr transpiler.ASTTransformer, f func(index int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeLambda = func(n native.GalaNodeRef) {
		if t.inFileTree(n) {
			f(n.Index())
		} else {
			parsed(int(n.Start().Line))
		}
	}
}

// OnNativeCapture sets tr's onNativeCapture hook for TestNativeWalkCases.
func OnNativeCapture(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeCapture = f
}

// OnNativeExpr sets tr's onNativeExpr hook for the native walk tests: f is
// told of each expression of the file's tree, by its node index and its first
// line, and parsed of each one of an expression parsed on its own (a default
// value or an interpolated expression), by its first line.
func OnNativeExpr(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeExpr = func(n native.GalaNodeRef) {
		if line := int(n.Start().Line); t.inFileTree(n) {
			f(n.Index(), line)
		} else {
			parsed(line)
		}
	}
}

// OnNativeShape sets tr's onNativeShape hook for TestNativeExprWalk.
func OnNativeShape(tr transpiler.ASTTransformer, f func(line int, shape string)) {
	tr.(*galaASTTransformer).onNativeShape = f
}

// OnHint sets tr's hint stack's observe hook for checkWalksAgree.
func OnHint(tr transpiler.ASTTransformer, f func(op string, owned bool)) {
	tr.(*galaASTTransformer).expectedArgTypes.observe = f
}

// OnNativeCase sets tr's onNativeCase hook for TestNativeWalkAgrees: f is
// told of each case clause of the file's tree, by its node index and its
// first line, and parsed of each one of an expression parsed on its own, by
// its first line.
func OnNativeCase(tr transpiler.ASTTransformer, f func(index, line int), parsed func(line int)) {
	t := tr.(*galaASTTransformer)
	t.onNativeCase = func(n native.GalaNodeRef) {
		if line := int(n.Start().Line); t.inFileTree(n) {
			f(n.Index(), line)
		} else {
			parsed(line)
		}
	}
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
