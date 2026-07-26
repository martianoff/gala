package transformer

import (
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/concurrency"
)

// Compile-time data-race-safety enforcement (PR3). A parameter typed
// `Sendable[F]` marks a concurrency boundary: an explicit lambda or a by-name
// thunk passed there may only capture bindings that are safe to share across a
// goroutine. "Safe" means a `val` binding (never reassigned) of a deeply
// immutable (Shareable) type. This file wires PR1 (the Shareable predicate) and
// PR2 (capture analysis) into the call-argument path and rejects unsafe
// captures with GALA-E0037.
//
// The check is entirely type-driven: it recognises the boundary from the
// `Sendable` marker on the parameter, never from a hardcoded function name, so
// any library — not just std's Future / go_interop.Spawn — gets enforcement by
// annotating its own boundary parameters.

// sendableMetaResolver builds the concurrency.MetadataResolver the Shareable
// predicate uses to look up user-defined struct / sealed field metadata. It is
// backed by the transformer's live type metadata (richAST.Types), so struct and
// sealed shareability is decided structurally. Types it cannot resolve (opaque
// Go-interop types, bare type params) are treated as not shareable — the safe
// direction.
func (t *galaASTTransformer) sendableMetaResolver() concurrency.MetadataResolver {
	return func(named transpiler.Type) (*transpiler.TypeMetadata, bool) {
		if transpiler.IsUnusable(named) {
			return nil, false
		}
		meta := t.getTypeMeta(named.BaseName())
		if meta == nil {
			return nil, false
		}
		// Reject metadata synthesized from an imported Go package: such a type
		// is opaque to GALA's shareability reasoning. Go has no `val`/`var`
		// field distinction, and the synthesizer only records the type's
		// EXPORTED fields — a Go struct with unexported mutable state (a
		// pointer, a mutex, a slice header) surfaces here with an empty or
		// partial field list. Structurally analysing that partial view is
		// unsound: an all-unexported Go struct would look like a field-less
		// (hence vacuously shareable) type and wrongly pass the boundary check.
		//
		// Every GALA-authored struct / sealed type records the source file it
		// was parsed from in DefinedIn (preserved across packages by the
		// metadata cache); only Go-synthesized metadata leaves it empty. So an
		// empty DefinedIn is the reliable marker of a non-GALA type, which we
		// treat as unresolvable — the checker then classifies it as not
		// shareable, the safe direction. A genuinely empty GALA struct still
		// has DefinedIn set, so it stays resolvable and shareable.
		if meta.DefinedIn == "" {
			return nil, false
		}
		return meta, true
	}
}

// checkSendableArg runs the capture-safety check for a positional call argument
// when — and only when — the declared parameter at argIdx is a `Sendable[F]`
// boundary. exprCtx / lambdaCtx are the argument's parse contexts (exactly one
// is non-nil): an explicit lambda is analysed directly, a by-name expression is
// analysed as a thunk body. Returns nil for non-boundary parameters.
func (t *galaASTTransformer) checkSendableArg(ctx callContext, argIdx int, exprCtx grammar.IExpressionContext, lambdaCtx *grammar.LambdaExpressionContext) error {
	raw, _, _, ok := t.boundaryParamAt(ctx, argIdx)
	if !ok {
		return nil
	}
	if !transpiler.IsSendable(raw) {
		return nil
	}
	if lambdaCtx != nil {
		return t.enforceSendableCapturesInLambda(lambdaCtx)
	}
	return t.enforceSendableCapturesInExpr(exprCtx)
}

// checkSendableNamedArg is the named-argument counterpart of checkSendableArg:
// it gates the capture-safety check on the declared type of the parameter whose
// name matches argName being a `Sendable[F]` boundary.
func (t *galaASTTransformer) checkSendableNamedArg(ctx callContext, argName string, exprCtx grammar.IExpressionContext, lambdaCtx *grammar.LambdaExpressionContext) error {
	raw, ok := boundaryParamByName(ctx, argName)
	if !ok || !transpiler.IsSendable(raw) {
		return nil
	}
	if lambdaCtx != nil {
		return t.enforceSendableCapturesInLambda(lambdaCtx)
	}
	return t.enforceSendableCapturesInExpr(exprCtx)
}

// boundaryParamByName returns the declared parameter type for the named
// parameter argName, drawn from the method or function metadata in ctx.
func boundaryParamByName(ctx callContext, argName string) (transpiler.Type, bool) {
	if ctx.methodMeta != nil {
		for i, name := range ctx.methodMeta.ParamNames {
			if name == argName && i < len(ctx.methodMeta.ParamTypes) {
				return ctx.methodMeta.ParamTypes[i], true
			}
		}
	}
	if ctx.applyMethodMeta != nil {
		for i, name := range ctx.applyMethodMeta.ParamNames {
			if name == argName && i < len(ctx.applyMethodMeta.ParamTypes) {
				return ctx.applyMethodMeta.ParamTypes[i], true
			}
		}
	}
	if ctx.funcMeta != nil {
		for i, name := range ctx.funcMeta.ParamNames {
			if name == argName && i < len(ctx.funcMeta.ParamTypes) {
				return ctx.funcMeta.ParamTypes[i], true
			}
		}
	}
	return transpiler.NilType{}, false
}

// enforceSendableCapturesInLambda runs the capture-safety check for an explicit
// lambda argument passed to a `Sendable` parameter. It returns a GALA-E0037
// error for the first unsafe capture, or nil when every capture is safe.
func (t *galaASTTransformer) enforceSendableCapturesInLambda(lambda grammar.ILambdaExpressionContext) error {
	return t.checkSendableCaptures(concurrency.FreeVariablesInLambda(lambda))
}

// enforceSendableCapturesInExpr runs the capture-safety check for a by-name /
// thunk argument (`Future(counter + 1)` desugars to `() => counter + 1`). If the
// expression is itself an explicit lambda (or contains one), that lambda is
// analysed directly; otherwise the whole expression is analysed as a thunk body.
func (t *galaASTTransformer) enforceSendableCapturesInExpr(exprCtx grammar.IExpressionContext) error {
	if lam := t.findLambdaInExpression(exprCtx); lam != nil {
		return t.checkSendableCaptures(concurrency.FreeVariablesInLambda(lam))
	}
	return t.checkSendableCaptures(concurrency.FreeVariablesInExpression(nil, exprCtx))
}

// checkSendableCaptures classifies each captured free variable and returns a
// GALA-E0037 error for the first one that is unsafe to share across a goroutine.
//
// Classification per capture:
//   - not a local binding (top-level function, type, package, import) → ignored;
//     these are not per-goroutine state and cannot race.
//   - a genuinely reassignable `var` → error (reassignment race): the enclosing
//     scope could rebind the variable slot while the goroutine reads it.
//   - a `val` whose type is not Shareable → error (mutable pointee race): the
//     goroutine and the enclosing scope alias the same mutable payload.
//   - a `val` of a Shareable type → safe.
func (t *galaASTTransformer) checkSendableCaptures(caps []concurrency.Capture) error {
	if len(caps) == 0 {
		return nil
	}
	checker := concurrency.NewChecker(t.sendableMetaResolver())
	for _, c := range caps {
		typ, bound := t.lookupLocalBinding(c.Name)
		if !bound {
			// Resolves to a top-level function / type / package symbol — not a
			// captured local, so not a capture race.
			continue
		}
		if t.isMutableVar(c.Name) {
			return unshareableVarCaptureError(c)
		}
		if !checker.IsShareable(typ) {
			return unshareableValCaptureError(c, typ)
		}
	}
	return nil
}

// unshareableVarCaptureError builds the GALA-E0037 error for a captured
// reassignable `var`. The position points at the exact offending identifier.
func unshareableVarCaptureError(c concurrency.Capture) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUnshareableCapture,
		c.Pos.Line, c.Pos.Column,
		fmt.Sprintf("closure crossing a concurrency boundary captures reassignable var %q — a data race, because the enclosing scope may reassign it while the goroutine runs", c.Name),
		fmt.Sprintf("snapshot it into an immutable `val` before the boundary (e.g. `val %s = %s` outside the closure) so the goroutine captures a stable copy", c.Name, c.Name),
	)
}

// unshareableValCaptureError builds the GALA-E0037 error for a captured `val`
// whose type is not deeply immutable (Shareable).
func unshareableValCaptureError(c concurrency.Capture, typ transpiler.Type) error {
	typeDesc := ""
	if !transpiler.IsUnusable(typ) {
		typeDesc = fmt.Sprintf(" (type %s)", typ.String())
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUnshareableCapture,
		c.Pos.Line, c.Pos.Column,
		fmt.Sprintf("closure crossing a concurrency boundary captures %q%s, whose type is not safe to share — a data race on its mutable contents", c.Name, typeDesc),
		"use an immutable collection (collection_immutable) or snapshot the needed data into an immutable `val`; or restructure so the closure returns the value instead of capturing it",
	)
}
