package transformer

import (
	"path/filepath"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// OnNativeDecl sets tr's onNativeDecl hook for TestNativeWalkAgrees.
func OnNativeDecl(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeDecl = f
}

// ParsedOrigin is an expression parsed on its own (parseExprAt): what
// parseExprAt parsed it as ("default value", "interpolated expression") and
// the file it is written in, slash-separated.
type ParsedOrigin struct{ What, File string }

// NativeNodeHooks is told of each statement, lambda, expression and case
// clause the native walk reads. One of the file's tree goes to Stmt, Lambda,
// Expr or Case, by its node index and first line. One of an expression parsed
// on its own goes to Parsed, by its kind (stmt, lambda, expr, case), that
// expression's origin and its first line: the walk tests count the file
// tree's nodes against that tree, by node index, and another tree numbers its
// nodes from 0 too.
type NativeNodeHooks struct {
	Stmt, Lambda, Expr, Case func(index, line int)
	Parsed                   func(kind string, o ParsedOrigin, line int)
}

// OnNativeNodes sets tr's onNativeStmt, onNativeLambda, onNativeExpr and
// onNativeCase hooks to h, and its onParsedExpr hook to tell the trees
// parseExprAt parses from the file's.
func OnNativeNodes(tr transpiler.ASTTransformer, h NativeNodeHooks) {
	t := tr.(*galaASTTransformer)
	parsed := map[*native.GalaTree]ParsedOrigin{}
	t.onParsedExpr = func(tree *native.GalaTree, what, file string) {
		parsed[tree] = ParsedOrigin{What: what, File: filepath.ToSlash(filepath.Clean(file))}
	}
	byTree := func(kind string, f func(index, line int)) func(native.GalaNodeRef) {
		return func(n native.GalaNodeRef) {
			line := int(n.Start().Line)
			if o, ok := parsed[n.Tree()]; ok {
				h.Parsed(kind, o, line)
			} else {
				f(n.Index(), line)
			}
		}
	}
	t.onNativeStmt = byTree("stmt", h.Stmt)
	t.onNativeLambda = byTree("lambda", h.Lambda)
	t.onNativeExpr = byTree("expr", h.Expr)
	t.onNativeCase = byTree("case", h.Case)
}

// RefuseNativeParse makes tr's parseExprAt take its ANTLR fallback for every
// text, as if its native parse had not settled it.
func RefuseNativeParse(tr transpiler.ASTTransformer) {
	tr.(*galaASTTransformer).refuseNativeParse = true
}

// EscapesAt is tr's escapesAt of the native node n.
func EscapesAt(tr transpiler.ASTTransformer, n native.GalaNodeRef) bool {
	return tr.(*galaASTTransformer).escapesAt(srcNode{node: n})
}

// OnNativeType sets tr's onNativeType hook for TestNativeTypeCases.
func OnNativeType(tr transpiler.ASTTransformer, f func(line int)) {
	tr.(*galaASTTransformer).onNativeType = f
}

// OnNativeCapture sets tr's onNativeCapture hook for TestNativeWalkCases.
func OnNativeCapture(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeCapture = f
}

// OnNativeShape sets tr's onNativeShape hook for TestNativeExprWalk.
func OnNativeShape(tr transpiler.ASTTransformer, f func(line int, shape string)) {
	tr.(*galaASTTransformer).onNativeShape = f
}

// OnHint sets tr's hint stack's observe hook for checkWalksAgree.
func OnHint(tr transpiler.ASTTransformer, f func(op string, owned bool)) {
	tr.(*galaASTTransformer).expectedArgTypes.observe = f
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
