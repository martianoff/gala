package transformer

import (
	"strings"
	"unicode/utf8"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A call suffix `(args)` as the call lowering reads it (applyCallSuffix and
// what it reaches): the suffix, the postfix expression it is a suffix of, and
// its arguments. The readers below find the callee: its primary expression
// (calleePos), the member the call selects (member, methodNamePos) and its
// source text (calleeText).
//
// What an argument holds — the expression or lambda it passes — is read by
// argContent (args_source.go).

// callSrc is a call suffix and the postfix expression it ends, as the postfix
// driver builds it (lowerSuffix): post carries the hint owner the call takes
// its slot's type for (postfixSrc.owner). A callSrc built only to quote or
// locate the call (recordVoidCall, lambdaArgCallee) leaves args empty.
type callSrc struct {
	post   postfixSrc
	suffix suffixSrc
	args   argsSrc // args.at is the zero srcNode for `()`
}

// at is where the call suffix is written: its `(`.
func (c callSrc) at() srcNode { return c.suffix.at() }

// start is the line and column of the call's `(`.
func (c callSrc) start() (line, col int) { return c.at().start() }

// anchor is where a diagnostic about the call points when it finds no
// better place: its argument list, or the `()` of a call with none.
func (c callSrc) anchor() srcNode {
	if c.args.at.ok() {
		return c.args.at
	}
	return c.at()
}

// before yields the suffixes of the call's postfix expression before it, in
// order, without collecting them.
func (c callSrc) before(yield func(suffixSrc) bool) {
	for s := range galaparser.NativeKids(c.post.node, native.GalaKIND_postfixSuffix) {
		if s == c.suffix.node || !yield(suffixSrc{node: s}) {
			return
		}
	}
}

// prev is the suffix just before the call, and false when the call follows
// the primary expression.
func (c callSrc) prev() (suffixSrc, bool) {
	var prev suffixSrc
	found := false
	for s := range c.before {
		prev, found = s, true
	}
	return prev, found
}

// calleePos is the start of the primary expression of the call's postfix
// expression — the callee identifier, `len` in `len(s)`, where a diagnostic
// about the callee points (exact) — or, when it has none, the start of the
// call's anchor.
func (c callSrc) calleePos() (line, col int, exact bool) {
	if c.post.hasPrimaryExpr() {
		line, col = c.post.primaryAt().start()
		return line, col, true
	}
	line, col = c.anchor().start()
	return line, col, false
}

// primaryText is GetText of the primary expression of the call's postfix
// expression.
func (c callSrc) primaryText() string { return c.post.primaryText() }

// primaryText is GetText of p's primary expression.
func (p postfixSrc) primaryText() string {
	return galaparser.NativeText(p.primaryExprNode().GalaNodeRef)
}

// member is the name of the `.name` suffix just before the call and where it
// is written, and false when the call is not of a member — `f(x)`, or
// `g()(x)`, whose suffix before it is a call.
func (c callSrc) member() (string, srcNode, bool) {
	prev, ok := c.prev()
	if !ok {
		return "", srcNode{}, false
	}
	return prev.member()
}

// methodNamePos locates the name of the method the call selects, falling
// back to the start of anchor. Unlike member, it takes the last member suffix
// anywhere before the call, so the name of `x.f[T](...)` is found past its
// type arguments.
func (c callSrc) methodNamePos() (line, col int) {
	at := c.anchor()
	for s := range c.before {
		if _, m, ok := s.member(); ok {
			at = m
		}
	}
	return at.start()
}

// methodCallName spells the callee of a method call for a diagnostic as the
// source writes it (`Cell(1).Convert`, see calleeText). A callee too long to
// quote is `(...).method`.
func (c callSrc) methodCallName(method string) string {
	if text := c.calleeText(); strings.HasSuffix(text, "."+method) {
		return text
	}
	return "(...)." + method
}

// calleeText is the source text of the callee — everything in the call's
// postfix expression before its `(` — or "" when that is not a short
// single-line expression worth quoting.
func (c callSrc) calleeText() string {
	if !c.suffix.node.Ok() {
		return ""
	}
	tree := c.post.node.Tree()
	from, to := tree.Token(c.post.node.StartIndex()), tree.Token(c.suffix.node.StartIndex())
	if to.Start < from.Start {
		return ""
	}
	text := tree.Src[from.Start:to.Start]
	// At most 60 code points.
	if utf8.RuneCountInString(text) > 60 {
		return ""
	}
	if text == "" || strings.ContainsAny(text, "\n\r") {
		return ""
	}
	return text
}

// at is where p is written.
func (p postfixSrc) at() srcNode { return srcNode{node: p.node} }

// firstSuffix is p's first suffix, and false when p has none.
func (p postfixSrc) firstSuffix() (suffixSrc, bool) {
	s, ok := firstNativeKid(p.node, native.GalaKIND_postfixSuffix)
	return suffixSrc{node: s}, ok
}

// primaryAt is where p's primary expression is written.
func (p postfixSrc) primaryAt() srcNode {
	return srcNode{node: p.primaryExprNode().GalaNodeRef}
}

// member is the name s selects and where it is written, and false when s is
// no member access.
func (s suffixSrc) member() (string, srcNode, bool) {
	if s.node.Ok() {
		if id := (native.PostfixSuffixNode{GalaNodeRef: s.node}).Identifier(); id.Ok() {
			return galaparser.NativeText(id.GalaNodeRef), srcNode{node: id.GalaNodeRef}, true
		}
	}
	return "", srcNode{}, false
}

// ok reports whether a is an argument, not the zero argSrc.
func (a argSrc) ok() bool { return a.node.Ok() }

// named reports whether a passes its value by name, `name = value`.
func (a argSrc) named() bool {
	name, _ := a.name()
	return name != ""
}

// lambda reports whether a is written as a lambda, the lambdaExpression
// alternative of rule argument.
func (a argSrc) lambda() bool {
	return a.node.Ok() && (native.ArgumentNode{GalaNodeRef: a.node}).LambdaExpression().Ok()
}

// spread reports whether a spreads a slice, `xs...`.
func (a argSrc) spread() bool {
	return a.pattern().kind() == patKindRest
}

// sourceText is n as written, whitespace included: the text of its tokens
// when it has no source span.
func (n srcNode) sourceText() string {
	if s := n.node.SourceText(); s != "" {
		return s
	}
	return n.text()
}
