package coreexport

import "martianoff/gala/internal/parser/native"

// lambda exports `(params) [result] => body`. Its parameters are exported as
// a function's are, a left-out type as null.
func (e *exporter) lambda(l native.LambdaExpressionNode) Node {
	out := of(e, &Lambda{Params: mapNodes(l.Parameters().ParameterList().AllParameter(), e.param),
		Result: e.optType(l.Type_())}, l.GalaNodeRef)
	if l.Block().Ok() {
		out.Body = e.block(l.Block())
	} else {
		out.Body = e.expr(l.Expression().GalaNodeRef)
	}
	return out
}

// funcType exports the type `func(T1, T2) R`, t's signature.
func (e *exporter) funcType(t native.TypeNode) Node {
	sig := t.Signature()
	out := of(e, &TypeFunc{Params: mapNodes(sig.Parameters().ParameterList().AllParameter(), e.funcTypeParam)},
		t.GalaNodeRef)
	switch {
	case sig.GoResults().Ok():
		out.Result = e.unsupported(UnsupportedGoResults, sig.GoResults().GalaNodeRef)
	case sig.Type_().Ok():
		out.Result = e.typ(sig.Type_())
	}
	return out
}

// funcTypeParam is the type of one parameter of a function type: `int`, or
// `x int` with a name, which the type ignores. A bare name parses as a
// parameter without a type, and is the type's name.
func (e *exporter) funcTypeParam(p native.ParameterNode) Node {
	switch {
	case p.VAL().Ok() || p.VAR().Ok():
		return e.unsupported(UnsupportedParamModifier, p.GalaNodeRef)
	case p.ELLIPSIS().Ok():
		return e.unsupported(UnsupportedVariadicParam, p.GalaNodeRef)
	case p.ParamDefault().Ok():
		return e.unsupported(UnsupportedParamDefault, p.GalaNodeRef)
	case p.Type_().Ok():
		return e.typ(p.Type_())
	}
	return of(e, &TypeName{Name: p.Identifier().GetText()}, p.Identifier().GalaNodeRef)
}
