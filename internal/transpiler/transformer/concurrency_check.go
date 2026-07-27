package transformer

import (
	"fmt"
	"strings"

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
//   - a binding DECLARED `Sendable[F]` → safe: it is a caller-vouched function
//     value. The `Sendable` bound means the closure the caller passed already
//     had ITS captures checked (at the call site where the real closure literal
//     was supplied), so forwarding it across a further boundary is safe. This is
//     the propagation that makes the canonical concurrency-wrapper pattern
//     (forwarding a `compute Sendable[func() R]` into a Future) type-check.
//   - a `val` used WHOLE (as a value, or with a method called on it) whose type
//     is not Shareable → error (mutable pointee race): the goroutine and the
//     enclosing scope alias the same mutable payload. A bare `func` capture lands
//     here (a function type is never Shareable on its own), with a message
//     hinting that the parameter be made `Sendable`.
//   - a `val` used ONLY via pure field-read access paths → checked
//     FIELD-SENSITIVELY: accepted iff every accessed path reads through immutable
//     (`val`) fields to a Shareable type. Reading only the immutable fields of an
//     otherwise-unshareable value is genuinely race-free, so no `val`-snapshot
//     workaround is needed. The first path that reads through a mutable (`var`)
//     field, or bottoms out at an unshareable type, is rejected and named.
//   - a `val` of a Shareable type used whole → safe.
func (t *galaASTTransformer) checkSendableCaptures(caps []concurrency.Capture) error {
	if len(caps) == 0 {
		return nil
	}
	checker := concurrency.NewChecker(t.sendableMetaResolver())
	// Recognise Go scalar value types (time.Duration, os.FileMode, …) as
	// shareable via the shared Go-named-type underlying resolver; the predicate
	// itself accepts only a primitive-scalar underlying.
	checker.SetGoUnderlyingResolver(t.goNamedUnderlying)
	for _, c := range caps {
		typ, bound := t.lookupLocalBinding(c.Name)
		if !bound {
			// Resolves to a top-level function / type / package symbol — not a
			// captured local, so not a capture race.
			continue
		}
		// A reassignable `var` is a race even when only its immutable fields are
		// read: the binding slot itself may be rebound while the goroutine runs.
		if t.isMutableVar(c.Name) {
			return unshareableVarCaptureError(c)
		}
		// A `Sendable[F]`-declared binding is a vouched-safe function value. The
		// marker was erased from `typ` during inference (transformType unwraps
		// Sendable), so we consult the scope's recorded Sendable-ness rather than
		// the type. This must precede the IsShareable check, which would reject
		// the bare `func` type the binding actually stores.
		if t.isSendableBinding(c.Name) {
			continue
		}
		if err := t.checkCaptureShareable(checker, c, typ); err != nil {
			return err
		}
	}
	return nil
}

// checkCaptureShareable enforces the shareability of a single non-var, non-
// Sendable `val` capture. A capture used WHOLE (as a value, call callee, method
// receiver, index/match subject) must have its ENTIRE type Shareable. A capture
// used only via pure field-read access paths is checked path-by-path: every path
// must read through immutable fields to a Shareable type.
func (t *galaASTTransformer) checkCaptureShareable(checker *concurrency.Checker, c concurrency.Capture, typ transpiler.Type) error {
	if c.Whole || len(c.Paths) == 0 {
		if !checker.IsShareable(typ) {
			return unshareableValCaptureError(c, typ)
		}
		return nil
	}
	for _, path := range c.Paths {
		fieldType, allImmutable, ok := t.resolveFieldPathType(typ, path)
		if !ok {
			// A hop could not be resolved (opaque Go type, unknown field): fall
			// back to the conservative whole-type check for this capture.
			if !checker.IsShareable(typ) {
				return unshareableValCaptureError(c, typ)
			}
			return nil
		}
		if !allImmutable || !checker.IsShareable(fieldType) {
			return unshareableFieldPathCaptureError(c, path, fieldType, allImmutable)
		}
	}
	return nil
}

// resolveFieldPathType walks a dotted field-read path (e.g. "policy.workspaceMode")
// off a binding's type using the transformer's struct metadata, returning the
// type the path resolves to, whether EVERY field hop along it is immutable
// (`val`), and whether the whole path resolved. A `var` field anywhere on the
// path clears allImmutable (reading a reassignable field races); an unresolvable
// hop (opaque Go-interop type, unknown field) returns ok=false so the caller can
// fall back conservatively.
func (t *galaASTTransformer) resolveFieldPathType(base transpiler.Type, path string) (finalType transpiler.Type, allImmutable bool, ok bool) {
	cur := base
	allImmutable = true
	for _, field := range strings.Split(path, ".") {
		ft, immutable, hopOK := t.resolveStructFieldType(cur, field)
		if !hopOK {
			return transpiler.NilType{}, false, false
		}
		if !immutable {
			allImmutable = false
		}
		cur = ft
	}
	return cur, allImmutable, true
}

// resolveStructFieldType resolves a single field of a GALA struct/sealed type via
// its metadata, returning the field's type (with the owner's type arguments
// substituted for the declaration's type parameters), whether the field is an
// immutable (`val`) field, and whether resolution succeeded. Go-synthesized
// (opaque) metadata — recognised by an empty DefinedIn, exactly as
// sendableMetaResolver does — is treated as unresolvable, the safe direction.
func (t *galaASTTransformer) resolveStructFieldType(owner transpiler.Type, field string) (fieldType transpiler.Type, immutable bool, ok bool) {
	if transpiler.IsUnusable(owner) {
		return transpiler.NilType{}, false, false
	}
	meta := t.getTypeMeta(owner.BaseName())
	if meta == nil || meta.DefinedIn == "" {
		return transpiler.NilType{}, false, false
	}
	ft, found := meta.Fields[field]
	if !found {
		return transpiler.NilType{}, false, false
	}
	immutable = true
	for i, fn := range meta.FieldNames {
		if fn == field {
			if i < len(meta.ImmutFlags) {
				immutable = meta.ImmutFlags[i]
			}
			break
		}
	}
	if len(meta.TypeParams) > 0 {
		if g, isGeneric := owner.(transpiler.GenericType); isGeneric && len(g.Params) > 0 {
			ft = t.substituteConcreteTypes(ft, meta.TypeParams, g.Params)
		}
	}
	return ft, immutable, true
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
// whose type is not deeply immutable (Shareable). A captured function value gets
// a distinct message: a bare `func` type carries no guarantee about its own
// captures, so the fix is to make the forwarded parameter `Sendable`, whose
// bound propagates the safety obligation to the caller (the Send-style rule).
func unshareableValCaptureError(c concurrency.Capture, typ transpiler.Type) error {
	if ft, ok := typ.(transpiler.FuncType); ok {
		return galaerr.NewCodedSemanticError(
			galaerr.CodeUnshareableCapture,
			c.Pos.Line, c.Pos.Column,
			fmt.Sprintf("closure crossing a concurrency boundary captures function value %q (type %s) — a function value may itself close over mutable state, so it cannot cross the boundary unless it is Sendable", c.Name, ft.String()),
			fmt.Sprintf("declare the parameter as `%s Sendable[%s]` — the Sendable bound requires each caller to pass a closure whose own captures are shareable, and propagates the check to the call site where the closure is written", c.Name, ft.String()),
		)
	}
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

// unshareableFieldPathCaptureError builds the GALA-E0037 error for a capture used
// ONLY via field-read paths where one accessed path is unsafe: it either reads
// through a mutable (`var`) field — a race with the field's reassignment — or
// bottoms out at a type that is itself not deeply immutable. The message names
// the specific offending field path (e.g. `m.sessions`) and why, and the caret
// points at the captured base identifier.
func unshareableFieldPathCaptureError(c concurrency.Capture, path string, fieldType transpiler.Type, allImmutable bool) error {
	fullPath := c.Name + "." + path
	var why, hint string
	if !allImmutable {
		why = "it reads through a mutable (`var`) field — a data race, because the enclosing scope may reassign that field while the goroutine reads it"
		hint = fmt.Sprintf("read only immutable (`val`) fields of %q across the boundary, or snapshot the needed field into an immutable `val` before it", c.Name)
	} else {
		typeDesc := ""
		if !transpiler.IsUnusable(fieldType) {
			typeDesc = fmt.Sprintf(" (type %s)", fieldType.String())
		}
		why = fmt.Sprintf("its type%s is not safe to share — a data race on its mutable contents", typeDesc)
		hint = fmt.Sprintf("read a deeply-immutable field of %q instead, or snapshot the needed data into an immutable `val` before the boundary", c.Name)
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUnshareableCapture,
		c.Pos.Line, c.Pos.Column,
		fmt.Sprintf("closure crossing a concurrency boundary reads field path %q on captured %q, which is not safe to share: %s", fullPath, c.Name, why),
		hint,
	)
}

// typeCtxIsSendable reports whether a parameter/binding's declared type
// annotation is the `Sendable[F]` boundary marker, in any qualified form:
// `Sendable[...]`, a package-qualified `std.Sendable[...]`, or a dot-imported
// re-export. transformType erases the transparent marker before the type reaches
// scope (the stored type is the bare inner `F`), so this purely syntactic check
// is how a Sendable-declared binding is remembered — see markSendable.
func typeCtxIsSendable(ctx grammar.ITypeContext) bool {
	if ctx == nil {
		return false
	}
	qid, ok := ctx.QualifiedIdentifier().(*grammar.QualifiedIdentifierContext)
	if !ok || qid == nil {
		return false
	}
	ta, ok := ctx.TypeArguments().(*grammar.TypeArgumentsContext)
	if !ok || ta == nil {
		return false
	}
	ids := qid.AllIdentifier()
	if len(ids) == 0 {
		return false
	}
	// The marker's own name is the last identifier; any leading qualifier (a
	// package alias) is ignored, matching transpiler.UnwrapSendable's
	// package-agnostic base-name comparison.
	if ids[len(ids)-1].GetText() != transpiler.TypeSendable {
		return false
	}
	tl, ok := ta.TypeList().(*grammar.TypeListContext)
	if !ok || tl == nil {
		return false
	}
	return len(tl.AllType_()) == 1
}
