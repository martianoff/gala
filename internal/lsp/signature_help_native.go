package lsp

import (
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// Signature help's reads of the parse tree on galop's native tree: each
// function here is the twin of the one it is named after, in
// signature_help.go, which reads ANTLR's tree, and hands the same values to
// the same tree-free code (callShape). TestSignatureCallTreesAgree holds each
// pair together.

// The reads of a document's tree that have a native twin, as
// GalaHandler.onTreeRead names them.
const (
	treeReadSignatureCall = "signature help: the call at the caret"
	treeReadDotImports    = "hover: the dot imports"
	// The document's analysis and its transform read the tree its parse
	// gives them: the native tree of a clean parse whose handle bridges,
	// ANTLR's error-recovered tree otherwise.
	treeReadAnalysis  = "the analysis"
	treeReadTransform = "the transform"
)

// noteTreeRead tells h.onTreeRead, when set, of the read what, which took the
// native tree when onNative holds.
func (h *GalaHandler) noteTreeRead(what string, onNative bool) {
	if h.onTreeRead != nil {
		h.onTreeRead(what, onNative)
	}
}

// findCallAtOffsetNative is findCallAtOffset on root, the native tree of a
// parse of the document's text, whose byte offsets its tokens carry.
func findCallAtOffsetNative(root native.GalaNodeRef, cursorOffset int) *callContext {
	f := nativeCallFinder{cursorOffset: cursorOffset}
	f.walk(root, native.GalaNodeRef{}, 1)
	if !f.bestSuffix.Ok() {
		return nil
	}
	shape, ok := callShapeNative(f.bestSuffix, f.bestParent)
	if !ok {
		return nil
	}
	return shape.context(cursorOffset)
}

// nativeCallFinder is callFinder on the native tree: it visits the nodes
// callFinder's listener enters, in the same order and at the same depths,
// and keeps the same suffix.
type nativeCallFinder struct {
	cursorOffset int
	bestSuffix   native.GalaNodeRef
	bestParent   native.GalaNodeRef
	bestDepth    int
}

// walk visits n, the child of parent, at depth depth (the root's is 1), and
// the nodes under it. A node whose tokens do not span the cursor is skipped
// with everything under it: a suffix callFinder keeps has the cursor after
// its '(' and at or before its ')' (bracketsCursor), so inside its own tokens
// and those of every node above it.
func (f *nativeCallFinder) walk(n, parent native.GalaNodeRef, depth int) {
	if n.StopIndex() < n.StartIndex() || f.cursorOffset <= int(n.Start().Start) || f.cursorOffset > int(n.Stop().End) {
		return
	}
	if n.Kind() == native.GalaKIND_postfixSuffix {
		f.enterPostfixSuffix(n, parent, depth)
	}
	for i := range n.ChildCount() {
		if c := n.Child(i).Node(); c.Ok() {
			f.walk(c, n, depth+1)
		}
	}
}

// enterPostfixSuffix is callFinder.EnterPostfixSuffix of suffix, the child of
// parent, at depth depth.
func (f *nativeCallFinder) enterPostfixSuffix(suffix, parent native.GalaNodeRef, depth int) {
	open := childToken(suffix, "(")
	if !(native.PostfixSuffixNode{GalaNodeRef: suffix}).ArgumentList().Ok() && !open.Ok() {
		return
	}
	start, stop := openParenRangeNative(suffix, open)
	if !bracketsCursor(start, stop, f.cursorOffset) {
		return
	}
	if depth <= f.bestDepth {
		return
	}
	if !parent.Ok() || parent.Kind() != native.GalaKIND_postfixExpr {
		return
	}
	f.bestSuffix = suffix
	f.bestParent = parent
	f.bestDepth = depth
}

// openParenRangeNative is openParenRange of suffix, whose first "(" child is
// openParen (childToken).
func openParenRangeNative(suffix native.GalaNodeRef, openParen native.GalaTerminal) (openAfter, closeAt int) {
	if !openParen.Ok() {
		return -1, -1
	}
	openAfter = int(openParen.Token().Start)
	if closeParen := childToken(suffix, ")"); closeParen.Ok() {
		return openAfter, int(closeParen.Token().Start)
	}
	if suffix.StopIndex() >= 0 {
		return openAfter, int(suffix.Stop().End)
	}
	return openAfter, openAfter + 1
}

// childToken is findChildTerminal of n: its first child token whose text is
// literal, or no token.
func childToken(n native.GalaNodeRef, literal string) native.GalaTerminal {
	for i := range n.ChildCount() {
		if t := n.Child(i).Terminal(); t.Ok() && t.GetText() == literal {
			return t
		}
	}
	return native.GalaTerminal{}
}

// callShapeNative is callShapeOf the call callSuffix of parent.
func callShapeNative(callSuffix, parent native.GalaNodeRef) (callShape, bool) {
	var suffixes []native.GalaNodeRef
	callIdx := -1
	for s := range parser.NativeKids(parent, native.GalaKIND_postfixSuffix) {
		if s == callSuffix {
			callIdx = len(suffixes)
		}
		suffixes = append(suffixes, s)
		if callIdx >= 0 {
			break
		}
	}
	if callIdx < 0 {
		return callShape{}, false
	}

	shape := callShape{primary: nativeSourceText(native.PostfixExprNode{GalaNodeRef: parent}.PrimaryExpr().GalaNodeRef)}
	if callIdx > 0 {
		prev := suffixes[callIdx-1]
		if id := (native.PostfixSuffixNode{GalaNodeRef: prev}).Identifier(); id.Ok() && !childToken(prev, "(").Ok() && !childToken(prev, "[").Ok() {
			shape.method = id.GetText()
			shape.receiver = shape.primary
			for _, s := range suffixes[:callIdx-1] {
				shape.receiver += nativeSourceText(s)
			}
		}
	}

	if open := childToken(callSuffix, "("); open.Ok() {
		shape.argStarts = append(shape.argStarts, int(open.Token().Start))
	}
	argList := native.PostfixSuffixNode{GalaNodeRef: callSuffix}.ArgumentList()
	if !argList.Ok() {
		return shape, true
	}
	for i := range argList.ChildCount() {
		if t := argList.Child(i).Terminal(); t.Ok() && t.GetText() == "," {
			shape.argStarts = append(shape.argStarts, int(t.Token().Start))
		}
	}
	for arg := range parser.NativeKids(argList.GalaNodeRef, native.GalaKIND_argument) {
		if arg.StopIndex() < arg.StartIndex() { // no tokens, as walk tests it
			continue
		}
		ca := callArg{start: int(arg.Start().Start), end: int(arg.Stop().End)}
		if id := (native.ArgumentNode{GalaNodeRef: arg}).Identifier(); id.Ok() {
			ca.name = id.GetText()
		}
		shape.args = append(shape.args, ca)
	}
	return shape, true
}

// nativeSourceText is contextText of n: its source text, or "" for no node
// or one without tokens.
func nativeSourceText(n native.GalaNodeRef) string {
	if !n.Ok() {
		return ""
	}
	return n.SourceText()
}
