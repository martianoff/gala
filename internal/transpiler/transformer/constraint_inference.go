package transformer

import (
	"fmt"
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
	if len(bound) == 0 || len(bound) == len(typeParams) {
		return
	}
	// Only a constraint that names another parameter of the list can bind it.
	var owners []string
	for _, tp := range typeParams {
		if c := constraints[tp]; transpiler.IsInterfaceConstraint(c) && typeMentionsTypeParam(c, typeParams) {
			owners = append(owners, tp)
		}
	}
	// Each owner is read once its type argument is known.
	done := make(map[string]bool, len(owners))
	for grew := true; grew; {
		grew = false
		for _, tp := range owners {
			if arg := bound[tp]; arg != nil && !done[tp] {
				done[tp] = true
				if t.inferFromConstraint(constraints[tp], arg, typeParams, bound) {
					grew = true
				}
			}
		}
	}
}

// inferMethodFromConstraints adds to bound the type parameters of the generic
// method m that the constraints of its bound ones determine, as
// inferFromConstraints does for a function. typeParams names m's type
// parameters, position by position, as bound does; bound may hold other
// names, and only typeParams are added. The constraints are read with the
// receiver's type parameters, recvParams, bound to its type arguments,
// recvArgs. m's own parameters are renamed to fresh names first, so a
// receiver type argument that names a type parameter of the caller spelled
// like one of m's (`T` in `Wrap[T]` inside a `func g[T any]`) stays apart
// from it (see resolveMethodCallTypeWithParams).
func (t *galaASTTransformer) inferMethodFromConstraints(m *transpiler.MethodMetadata, typeParams []string, recvParams []string, recvArgs []transpiler.Type, bound map[string]transpiler.Type) {
	// Only a constraint that names another of the method's type parameters
	// can bind it (see inferFromConstraints).
	if len(typeParams) != len(m.TypeParams) || !slices.ContainsFunc(m.TypeParams, func(tp string) bool {
		c := m.TypeParamConstraints[tp]
		return transpiler.IsInterfaceConstraint(c) && typeMentionsTypeParam(c, m.TypeParams)
	}) {
		return
	}
	fresh := make([]string, len(m.TypeParams))
	rename := make(map[string]transpiler.Type, len(m.TypeParams))
	for i, tp := range m.TypeParams {
		fresh[i] = freshMethodTypeParamName(i)
		rename[tp] = transpiler.NamedType{Name: fresh[i]}
	}
	constraints := make(map[string]transpiler.Type, len(m.TypeParams))
	own := make(map[string]transpiler.Type, len(m.TypeParams))
	for i, tp := range m.TypeParams {
		if c := m.TypeParamConstraints[tp]; c != nil {
			constraints[fresh[i]] = t.substituteConcreteTypes(t.substituteInType(c, rename), recvParams, recvArgs)
		}
		if typ, ok := bound[typeParams[i]]; ok {
			own[fresh[i]] = typ
		}
	}
	t.inferFromConstraints(fresh, constraints, own)
	for i, name := range fresh {
		if _, ok := bound[typeParams[i]]; !ok && own[name] != nil {
			bound[typeParams[i]] = own[name]
		}
	}
}

// inferFromConstraint binds, in bound, the type parameters of typeParams
// that unifying the methods the constraint c requires with those of arg, the
// type argument c constrains, determines. It reports whether it bound any.
func (t *galaASTTransformer) inferFromConstraint(c, arg transpiler.Type, typeParams []string, bound map[string]transpiler.Type) bool {
	required, ok := t.requiredMethods(t.followAliasChain(c))
	if !ok {
		return false
	}
	method, ok := t.methodsOf(arg)
	if !ok {
		return false
	}
	before := len(bound)
	for _, want := range required() {
		have, ok := method(want.name)
		if !ok || !want.exact || !have.exact {
			continue
		}
		t.unifyForInference(
			transpiler.FuncType{Params: want.params, Results: want.results},
			transpiler.FuncType{Params: have.params, Results: have.results},
			typeParams, bound)
	}
	return len(bound) > before
}

// methodsOf returns the lookup of the methods a value of type typ has,
// instantiated with its type arguments: those of a GALA type (implTypeOf) or
// of an interface (requiredMethods). A method with type parameters of its own
// is not found. ok is false for any other type.
func (t *galaASTTransformer) methodsOf(typ transpiler.Type) (func(name string) (ifaceMethod, bool), bool) {
	if impl, ok := t.implTypeOf(typ); ok {
		return func(name string) (ifaceMethod, bool) {
			m := impl.meta.Methods[name]
			if m == nil || len(m.TypeParams) > 0 || m.IsGeneric {
				return ifaceMethod{}, false
			}
			return t.instantiateMethod(m, impl.meta.TypeParams, impl.args), true
		}, true
	}
	listed, ok := t.requiredMethods(t.followAliasChain(typ))
	if !ok {
		return nil, false
	}
	byName := make(map[string]ifaceMethod)
	for _, m := range listed() {
		byName[m.name] = m
	}
	return func(name string) (ifaceMethod, bool) {
		m, ok := byName[name]
		return m, ok
	}, true
}

// checkConstraintTypeArgs rejects a call of a generic function (callCtx's)
// whose type argument for a parameter constrained by an interface does not
// implement that interface, instantiated with the call's type arguments
// (GALA-E0100): `dropFirstKeep(words, p)` for `C Seq[T, C]` needs the
// argument's type to implement Seq[T, C] with T and C bound. The call's type
// arguments are the ones its lambdas were lowered against
// (inferFuncTypeSubstFromArgs), placeholders left out.
func (t *galaASTTransformer) checkConstraintTypeArgs(c callSrc, callCtx functionCallContext) error {
	fm := callCtx.funcMeta
	if fm == nil || len(callCtx.inferredTypeSubst) == 0 || !slices.ContainsFunc(fm.TypeParams, func(tp string) bool {
		return transpiler.IsInterfaceConstraint(fm.TypeParamConstraints[tp])
	}) {
		return nil
	}
	bound := boundTypeArgs(callCtx)
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
		why, hint := t.constraintMismatch(typeArg, required())
		if why == "" {
			continue
		}
		r := c.anchor().rng()
		return spanErrorAt(galaerr.CodeInterfaceNotImplemented, r, r,
			fmt.Sprintf("%s, the type argument for %s of %s, does not implement its constraint %s: %s",
				displayType(typeArg), tp, fm.Name, displayType(inst), why),
			hint)
	}
	return nil
}

// boundTypeArgs is the type arguments of callCtx's call that are known: those
// its lambdas were lowered against (inferFuncTypeSubstFromArgs), placeholders
// left out.
func boundTypeArgs(callCtx functionCallContext) typeSubstMap {
	bound := make(typeSubstMap, len(callCtx.inferredTypeSubst))
	for tp, typ := range callCtx.inferredTypeSubst {
		if !callCtx.placeholderTypeParams[tp] {
			bound[tp] = typ
		}
	}
	return bound
}

// constraintMismatch says why typeArg lacks one of required, the methods of
// its instantiated constraint, and how to fix it; "" when it has them all or
// that cannot be told exactly. A GALA type is judged as checkInterfaceSlot
// judges it (implMismatch); an interface by its methods' signatures.
func (t *galaASTTransformer) constraintMismatch(typeArg transpiler.Type, required []ifaceMethod) (why, hint string) {
	if impl, ok := t.implTypeOf(typeArg); ok {
		for _, m := range required {
			if why, hint := t.implMismatch(impl, m); why != "" {
				return why, hint
			}
		}
		return "", ""
	}
	method, ok := t.methodsOf(typeArg)
	if !ok {
		return "", ""
	}
	for _, want := range required {
		signatureHint := "pass a type whose " + want.name + " has the constraint's exact signature"
		have, ok := method(want.name)
		if !ok {
			return "it has no method " + want.name, signatureHint
		}
		if !want.exact || !have.exact {
			continue
		}
		if why := t.signatureMismatch(have, want); why != "" {
			return why, signatureHint
		}
	}
	return "", ""
}

// checkConstraintInference rejects a call of a generic function (callCtx's)
// that inference leaves a type parameter of unbound although a lambda
// argument needs it, when the parameter appears only in the constraint of
// another, bound one (GALA-E0104): `f(s, (x) => x > 1)` for
// `func f[T any, C Seq[T, C]](xs C, p func(T) bool)` with an `s` whose type's
// methods do not reveal T. The lambda would otherwise be lowered against
// `any`. The diagnostic names the type arguments to write.
func (t *galaASTTransformer) checkConstraintInference(c callSrc, callCtx functionCallContext) error {
	fm := callCtx.funcMeta
	placeholder := callCtx.placeholderTypeParams
	if fm == nil || len(placeholder) == 0 {
		return nil
	}
	subst := callCtx.inferredTypeSubst
	for _, tp := range fm.TypeParams {
		if !placeholder[tp] {
			continue
		}
		// The bound parameter whose constraint names tp.
		var owner string
		var constraint transpiler.Type
		for _, other := range fm.TypeParams {
			if oc := fm.TypeParamConstraints[other]; !placeholder[other] && transpiler.IsInterfaceConstraint(oc) && typeMentionsTypeParam(oc, []string{tp}) {
				owner, constraint = other, oc
				break
			}
		}
		if owner == "" || !t.lambdaNeedsTypeParam(fm, c.args, tp) {
			continue
		}
		spelled := make([]string, len(fm.TypeParams))
		for k, p := range fm.TypeParams {
			if placeholder[p] {
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
