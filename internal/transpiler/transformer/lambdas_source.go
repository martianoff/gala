package transformer

import (
	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// A lambda as its lowering reads it (lowerLambda): a lambdaExpression of
// ANTLR's tree (ctx) or of galop's native one (node), with readers that have
// an ANTLR half and a native half giving the same value for the same lambda,
// as the statement sources do (statements_source.go). A native lambda's
// block body is a native blockSrc, so its statements are lowered on the
// native tree too. Its parameters' types, its result type and an expression
// body are read on ANTLR's tree: the native readers cross to it with crossTo.
//
// Lambdas are reached from expressions and calls, which are still lowered on
// ANTLR's tree. So the ANTLR entry points (transformLambdaWithExpectedType,
// the Sendable capture check) take the lambda's node on the native tree when
// the walk reads it (nativeNodeOf).

// lambdaSrc is a lambda expression.
type lambdaSrc struct {
	ctx  *grammar.LambdaExpressionContext
	node native.LambdaExpressionNode
}

// nativeNodeOf is ctx's node on the native tree, of kind, when the file's
// walk reads that tree (t.native, set only for a bridged tree), and false
// otherwise: the crossing from ANTLR's tree to the native one, for the
// lowerings the native walk reaches through an expression that is still
// lowered on ANTLR's tree.
func (t *galaASTTransformer) nativeNodeOf(ctx antlr.ParserRuleContext, kind native.GalaKind) (native.GalaNodeRef, bool) {
	if t.native == nil || ctx == nil {
		return native.GalaNodeRef{}, false
	}
	n := t.native.Node(ctx)
	if !n.Ok() || n.Kind() != kind {
		return native.GalaNodeRef{}, false
	}
	return n, true
}

// lambdaOf is the lambda ctx, on the native tree when the walk reads it.
func (t *galaASTTransformer) lambdaOf(ctx *grammar.LambdaExpressionContext) lambdaSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_lambdaExpression); ok {
		return lambdaSrc{node: native.LambdaExpressionNode{GalaNodeRef: n}}
	}
	return lambdaSrc{ctx: ctx}
}

// at is where l is written.
func (l lambdaSrc) at() srcNode {
	if l.node.Ok() {
		return srcNode{node: l.node.GalaNodeRef}
	}
	return srcNode{ctx: l.ctx}
}

// antlr is l's context on ANTLR's tree, for the readers not ported yet
// (lambdaArgCallee, the Try thunk's identity).
func (l lambdaSrc) antlr(t *galaASTTransformer) *grammar.LambdaExpressionContext {
	if l.node.Ok() {
		return crossTo[*grammar.LambdaExpressionContext](t, l.node.GalaNodeRef)
	}
	return l.ctx
}

// lambdaParam is a lambda parameter: its context, lowered on ANTLR's tree
// (transformParameter), its name and where the name is written, its `var`,
// and its type (nil when it has none).
type lambdaParam struct {
	ctx    *grammar.ParameterContext
	at     srcNode
	name   string
	nameAt transpiler.SourcePos
	isVar  bool
	typ    grammar.ITypeContext
}

// params is l's parameters, in order.
func (l lambdaSrc) params(t *galaASTTransformer) []lambdaParam {
	var out []lambdaParam
	if l.node.Ok() {
		for p := range galaparser.NativeKids(l.node.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			param := native.ParameterNode{GalaNodeRef: p}
			lp := lambdaParam{ctx: crossTo[*grammar.ParameterContext](t, p), at: srcNode{node: p}, isVar: param.VAR().Ok()}
			if id := param.Identifier(); id.Ok() {
				s := id.Start()
				lp.name, lp.nameAt = id.GetText(), transpiler.SourcePos{Line: int(s.Line), Column: int(s.Column)}
			}
			if typ := param.Type_(); typ.Ok() {
				lp.typ = crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
			}
			out = append(out, lp)
		}
		return out
	}
	list, ok := l.ctx.Parameters().(*grammar.ParametersContext).ParameterList().(*grammar.ParameterListContext)
	if !ok {
		return nil
	}
	for _, p := range list.AllParameter() {
		param := p.(*grammar.ParameterContext)
		lp := lambdaParam{ctx: param, at: srcNode{ctx: param}, isVar: param.VAR() != nil, typ: param.Type_()}
		if id := param.Identifier(); id != nil {
			lp.name, lp.nameAt = id.GetText(), transpiler.PosFromToken(id.GetStart())
		}
		out = append(out, lp)
	}
	return out
}

// resultType is l's declared result type, and nil when it declares none.
func (l lambdaSrc) resultType(t *galaASTTransformer) *grammar.TypeContext {
	if l.node.Ok() {
		if typ := l.node.Type_(); typ.Ok() {
			return crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
		}
		return nil
	}
	typ, _ := l.ctx.Type_().(*grammar.TypeContext)
	return typ
}

// block is l's block body, and the zero blockSrc when its body is an
// expression.
func (l lambdaSrc) block() blockSrc {
	if l.node.Ok() {
		if b := l.node.Block(); b.Ok() {
			return blockSrc{node: b.GalaNodeRef}
		}
		return blockSrc{}
	}
	if b, ok := l.ctx.Block().(*grammar.BlockContext); ok {
		return antlrBlock(b)
	}
	return blockSrc{}
}

// antlr is b's context on ANTLR's tree, for returnSlot.body.
func (b blockSrc) antlr(t *galaASTTransformer) *grammar.BlockContext {
	if b.node.Ok() {
		return crossTo[*grammar.BlockContext](t, b.node)
	}
	return b.ctx
}

// expression is l's expression body, and nil when its body is a block.
func (l lambdaSrc) expression(t *galaASTTransformer) grammar.IExpressionContext {
	if l.node.Ok() {
		if e := l.node.Expression(); e.Ok() {
			return crossTo[*grammar.ExpressionContext](t, e.GalaNodeRef)
		}
		return nil
	}
	if e := l.ctx.Expression(); e != nil {
		return e
	}
	return nil
}
