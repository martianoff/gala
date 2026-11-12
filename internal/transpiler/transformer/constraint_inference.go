package transformer

import (
	"fmt"
	"go/ast"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Inferring a type argument from a constraint.
//
// A constraint with type arguments can name other type parameters of the
// same list: `func f[T any, C Iterable[T, C]](xs C)`. A call `f(nums)` binds
// C from the argument, and nothing binds T directly; T is what makes the
// type of nums implement Iterable[T, C]. As Go does, inference then matches
// each method the constraint requires against the same method of C's type
// argument: `Filter(p func(T) bool) C` against `Filter(p func(int) bool)
// Nums` binds T to int.

// inferFromConstraints adds to bound the type parameters of typeParams that
// the constraints of the bound ones determine (see above), until no more are
// found.
func (t *galaASTTransformer) inferFromConstraints(typeParams []string, constraints map[string]transpiler.Type, bound map[string]transpiler.Type) {
	if len(constraints) == 0 || len(bound) == len(typeParams) {
		return
	}
	for grew := true; grew; {
		grew = false
		for _, tp := range typeParams {
			c, arg := constraints[tp], bound[tp]
			if arg == nil || !transpiler.IsInterfaceConstraint(c) || !typeMentionsTypeParam(c, typeParams) {
				continue
			}
			if t.inferFromConstraint(c, arg, typeParams, bound) {
				grew = true
			}
		}
	}
}

// inferFromConstraint binds, in bound, the type parameters of typeParams
// that unifying the methods the constraint c requires with those of arg, the
// type argument c constrains, determines. It reports whether it bound any.
func (t *galaASTTransformer) inferFromConstraint(c, arg transpiler.Type, typeParams []string, bound map[string]transpiler.Type) bool {
	required, ok := t.requiredMethods(c)
	if !ok {
		return false
	}
	impl, ok := t.implTypeOf(arg)
	if !ok {
		return false
	}
	before := len(bound)
	for _, want := range required() {
		m := impl.meta.Methods[want.name]
		if m == nil || len(m.TypeParams) > 0 || !want.exact {
			continue
		}
		have := t.instantiateMethod(m, impl.meta.TypeParams, impl.args)
		if !have.exact {
			continue
		}
		t.unifyForInference(
			transpiler.FuncType{Params: want.params, Results: want.results},
			transpiler.FuncType{Params: have.params, Results: have.results},
			typeParams, bound)
	}
	return len(bound) > before
}

// checkConstraintTypeArgs rejects a call of a generic function (callCtx's)
// whose type argument for a parameter constrained by an interface does not
// implement that interface, instantiated with the call's type arguments
// (GALA-E0100): `dropFirstKeep(words, p)` for `C Seq[T, C]` needs the
// argument's type to implement Seq[T, C] with T and C bound. args are the
// call's lowered positional arguments; hasSpread marks an `xs...` one.
func (t *galaASTTransformer) checkConstraintTypeArgs(fun ast.Expr, c callSrc, args []ast.Expr, hasSpread bool, callCtx functionCallContext) error {
	fm := callCtx.funcMeta
	if fm == nil || !slices.ContainsFunc(fm.TypeParams, func(tp string) bool {
		return transpiler.IsInterfaceConstraint(fm.TypeParamConstraints[tp])
	}) {
		return nil
	}
	bound := t.argTypeArgs(fm, args, hasSpread)
	for i, typeArg := range t.extractFuncCallTypeArgs(fun) {
		if i < len(fm.TypeParams) {
			bound[fm.TypeParams[i]] = transpiler.ParseType(typeArg)
		}
	}
	for _, tp := range fm.TypeParams {
		constraint, typeArg := fm.TypeParamConstraints[tp], bound[tp]
		if typeArg == nil || !transpiler.IsInterfaceConstraint(constraint) {
			continue
		}
		inst := t.substituteInType(constraint, bound)
		if typeMentionsTypeParam(inst, fm.TypeParams) {
			continue
		}
		required, ok := t.requiredMethods(t.followAliasChain(inst))
		if !ok {
			continue
		}
		impl, ok := t.implTypeOf(typeArg)
		if !ok {
			continue
		}
		for _, m := range required() {
			why, hint := t.implMismatch(impl, m)
			if why == "" {
				continue
			}
			r := c.anchor().rng()
			return spanErrorAt(galaerr.CodeInterfaceNotImplemented, r, r,
				fmt.Sprintf("%s, the type argument for %s of %s, does not implement its constraint %s: %s",
					displayType(typeArg), tp, fm.Name, displayType(inst), why),
				hint)
		}
	}
	return nil
}

// checkConstraintInference rejects a call of a generic function (callCtx's)
// that inference leaves a type parameter of unbound although a lambda
// argument needs it, when the parameter appears only in the constraint of
// another, bound one (GALA-E0104): `f(s, (x) => x > 1)` for
// `func f[T any, C Seq[T, C]](xs C, p func(T) bool)` with an `s` whose type's
// methods do not reveal T. The lambda would otherwise be lowered against
// `any`. The diagnostic names the type arguments to write.
func (t *galaASTTransformer) checkConstraintInference(fun ast.Expr, c callSrc, callCtx functionCallContext) error {
	fm := callCtx.funcMeta
	if fm == nil || !callCtx.typeArgPlaceholders || len(fm.TypeParamConstraints) == 0 {
		return nil
	}
	written := len(t.extractFuncCallTypeArgs(fun))
	subst := callCtx.inferredTypeSubst
	placeholder := func(i int) bool {
		v := subst[fm.TypeParams[i]]
		return i >= written && v != nil && v.String() == anyTypeArg.String()
	}
	for i, tp := range fm.TypeParams {
		if !placeholder(i) {
			continue
		}
		// The bound parameter whose constraint names tp.
		var owner string
		var constraint transpiler.Type
		for j, other := range fm.TypeParams {
			if oc := fm.TypeParamConstraints[other]; j != i && !placeholder(j) && transpiler.IsInterfaceConstraint(oc) && typeMentionsTypeParam(oc, []string{tp}) {
				owner, constraint = other, oc
				break
			}
		}
		if owner == "" || !t.lambdaNeedsTypeParam(fm, c.args, tp) {
			continue
		}
		spelled := make([]string, len(fm.TypeParams))
		for k, p := range fm.TypeParams {
			if placeholder(k) {
				spelled[k] = "<" + p + ">"
			} else {
				spelled[k] = displayType(subst[p])
			}
		}
		r := c.anchor().rng()
		return spanErrorAt(galaerr.CodeConstraintInference, r, r,
			fmt.Sprintf("cannot infer %s in this call of %s: %s appears only in %s, the constraint of %s, and the methods of %s's type argument %s do not determine it",
				tp, fm.Name, tp, displayType(constraint), owner, owner, displayType(subst[owner])),
			fmt.Sprintf("write the type arguments: %s[%s](...)", fm.Name, strings.Join(spelled, ", ")))
	}
	return nil
}

// lambdaNeedsTypeParam reports whether a lambda among args with a parameter
// that declares no type fills a parameter of fm whose type mentions the type
// parameter tp. A lambda whose parameters are all annotated binds tp itself.
func (t *galaASTTransformer) lambdaNeedsTypeParam(fm *transpiler.FunctionMetadata, args argsSrc, tp string) bool {
	for _, a := range t.callArgs(args, fm.ParamNames) {
		pi := paramIndex(a.slot, fm.ParamTypes, fm.Variadic)
		if !a.lambda.ok() || pi < 0 || !typeMentionsTypeParam(fm.ParamTypes[pi], []string{tp}) {
			continue
		}
		if slices.ContainsFunc(a.lambda.params(), func(p paramDecl) bool { return !p.typ.ok() }) {
			return true
		}
	}
	return false
}
