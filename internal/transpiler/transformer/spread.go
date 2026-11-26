package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// ellipsisPos returns a non-zero token.Pos if hasSpread is true, indicating variadic expansion.
func ellipsisPos(hasSpread bool) token.Pos {
	if hasSpread {
		return token.Pos(1)
	}
	return token.NoPos
}

// spreadArgType is the type a spread argument `xs...` is lowered against when
// it fills a variadic parameter recorded as elem: a slice of elem, so a slice
// of nullable pointers filling a `...Ptr[T]` is rejected as an `Array[*T]` is
// where an `Array[Ptr[T]]` is (GALA-E0080). An unknown elem stays unknown.
func spreadArgType(elem transpiler.Type) transpiler.Type {
	if transpiler.IsUnusable(elem) {
		return elem
	}
	return transpiler.ArrayType{Elem: elem}
}

// spreadElemType is the element type of argType, the type of a spread
// argument `xs...`, for binding the type parameters of the variadic
// parameter it fills: the element of its underlying slice, whatever slice
// type it is (`[]float64`, a GALA `type Floats []float64`, Go's `net.IP`).
// argType itself when it is no slice.
func (t *galaASTTransformer) spreadElemType(argType transpiler.Type) transpiler.Type {
	under, _ := t.goUnderlying(unwrapValueMarkers(argType))
	if arr, ok := under.(transpiler.ArrayType); ok {
		return arr.Elem
	}
	return argType
}

// namedSpreadType is the type a spread passed by name as parameter i (-1 for
// none) of a callee with n parameters is lowered against: a slice of param,
// that parameter's type, when it is the variadic last one; unknown for any
// other, which no spread fills.
func namedSpreadType(param transpiler.Type, n int, variadic bool, i int) transpiler.Type {
	if i < 0 || !isVariadicParam(n, variadic, i) {
		return transpiler.NilType{}
	}
	return spreadArgType(param)
}

// What a spread argument may be (GALA-E0138)
//
// Go spreads `xs...` into a variadic parameter `...T` only when xs is a slice
// assignable to `[]T`: a slice whose elements are of type T. A GALA
// collection, any other struct, a string, a number, a map or a function, and a
// slice of another element type (`[]string` spread into `...any`), used to pass
// every GALA check and fail `go build` against the generated call ("cannot use
// xs (variable of struct type collection_immutable.Array[int]) as []int
// value"). checkSpreadArg reports them in GALA terms, at the spread argument;
// checkSpreads reports a spread filling any parameter other than the variadic
// last one.

// checkSpreadArg reports expr, the lowered spread argument e, unless it is a Go
// slice whose elements are of type elem, the variadic parameter's element type
// (unknown: any elements). Only a type known not to fit is reported: a value
// of a Go named type (`sort.StringSlice`), an interface, an opaque type or an
// unresolved type is left to Go, and so is the element type when either side
// still names a type parameter inference has not bound.
func (t *galaASTTransformer) checkSpreadArg(e exprSrc, expr ast.Expr, elem transpiler.Type) error {
	if !e.ok() {
		return nil
	}
	argType := unwrapValueMarkers(t.getExprTypeName(expr))
	if transpiler.IsUnusableOrAny(argType) {
		return nil
	}
	if slice, ok := t.followAliasChain(argType).(transpiler.ArrayType); ok && !slice.Fixed {
		if t.elemFits(slice.Elem, elem) {
			return nil
		}
		want := transpiler.ArrayType{Elem: elem}
		return spreadError(e, fmt.Sprintf("cannot spread `%s` of type %s into a variadic parameter of %s: only a %s can be spread into it",
			spreadText(e), displayType(argType), displayType(elem), displayType(want)),
			fmt.Sprintf("spread a %s, or pass the elements one by one", displayType(want)))
	}
	hint, ok := t.spreadNotSliceHint(argType, elem, e)
	if !ok {
		return nil
	}
	return spreadError(e, fmt.Sprintf("cannot spread `%s` of type %s: only a Go slice can be spread into a variadic parameter", spreadText(e), displayType(argType)), hint)
}

// checkSpreadPlacement reports a spread argument of the call c of fun that
// fills a parameter other than the callee's variadic last one, or that
// another positional argument follows (checkSpreads). It runs before the
// arguments are lowered, so a misplaced spread is told where it may go
// before what it holds is checked (GALA-E0138), which presumes a variadic
// parameter. The callee is a GALA function, a Go function passed arguments
// by name or a companion Apply; a method is checked as it is lowered
// (checkMethodSpreads), and the Apply of a value by Section 1.5
// (checkValueApplySpread).
func (t *galaASTTransformer) checkSpreadPlacement(fun ast.Expr, c callSrc, callCtx functionCallContext) error {
	if !slices.ContainsFunc(c.args.list, argSrc.spread) {
		return nil
	}
	switch {
	case callCtx.funcMeta != nil:
		return funcParamList(callCtx.funcMeta.Name, callCtx.funcMeta).checkSpreads(c.args)
	case callCtx.structLiteral || callCtx.goStruct != nil:
		// A construction: a field takes one value, by name as by position.
		if _, qualifiedName := extractTypeNameFromExpr(fun); qualifiedName != "" {
			for _, a := range c.args.list {
				if name, _ := a.name(); name != "" && a.spread() {
					return spreadFieldError(a, name, qualifiedName)
				}
			}
		}
		return nil
	case callCtx.applyMethodMeta != nil:
		return methodParamList(t.getBaseTypeName(fun)+".Apply", callCtx.applyMethodMeta).checkSpreads(c.args)
	case callCtx.goSig != nil:
		if !slices.ContainsFunc(c.args.list, argSrc.named) {
			return nil // Go checks a positional call of its own function
		}
		callee := t.argCalleeName(fun)
		if callee == "" {
			callee = "this function"
		}
		return goParamList(callee, callCtx.goSig).checkSpreads(c.args)
	}
	// A value called through its Apply is checked by Section 1.5.
	return nil
}

// checkMethodSpreads is checkSpreads for the call c of the method methodMeta
// describes, run before its arguments are lowered (see checkSpreadPlacement).
// A Go method's positional arguments keep Go's own check (see call_arity.go).
func checkMethodSpreads(c callSrc, methodMeta *transpiler.MethodMetadata) error {
	if !slices.ContainsFunc(c.args.list, argSrc.spread) ||
		methodMeta.GoDeclared && !slices.ContainsFunc(c.args.list, argSrc.named) {
		return nil
	}
	return methodParamList(methodMeta.Name, methodMeta).checkSpreads(c.args)
}

// checkValueApplySpread is checkSpreads for the Apply a call of fun makes
// through a value (`joiner(parts...)`, `Joiner(",")(parts...)`, a call's
// result, one given type arguments); nothing when the value's type is not
// known exactly (calleeApplyType).
func (t *galaASTTransformer) checkValueApplySpread(fun ast.Expr, c callSrc) error {
	base, _ := splitCallFunTypeArgs(fun)
	meta := t.calleeApplyType(base)
	if meta == nil {
		return nil
	}
	// The type as the file names it, `lib.Joiner` (under an import alias too),
	// as a same-named type of this package may have an Apply.
	name := meta.Name
	if cl, ok := base.(*ast.CompositeLit); ok {
		if written, qualified := qualifiedLitType(cl); qualified {
			name = written
		}
	} else if meta.Package != "" {
		name = t.diagQualifier(meta.Package) + name
	}
	return t.checkApplySpread(meta, name, c)
}

// checkApplySpread is checkSpreads for the call c of the Apply of meta's
// type, which diagnostics spell name.
func (t *galaASTTransformer) checkApplySpread(meta *transpiler.TypeMetadata, name string, c callSrc) error {
	apply := meta.Methods["Apply"]
	if apply == nil {
		return nil
	}
	return methodParamList(name+".Apply", apply).checkSpreads(c.args)
}

// spreadSlotElem is the element type of the variadic parameter a spread
// argument filling s takes: s is typed by spreadArgType. Unknown when s is
// untyped or open, its `any` then standing for type parameters not bound yet.
func spreadSlotElem(s slot) transpiler.Type {
	if slice, ok := s.typ.(transpiler.ArrayType); ok && !slice.Fixed && !s.open {
		return slice.Elem
	}
	return transpiler.NilType{}
}

// goVariadicElem is the element type of parameter i of the Go function or
// method sig when it is the variadic last one, which a spread argument fills;
// unknown for any other parameter, an unknown signature and a generic one,
// whose element type may name a type parameter.
func goVariadicElem(sig *transpiler.GoFuncSignature, i int) transpiler.Type {
	if sig == nil || !sig.IsVariadic || len(sig.TypeParams) > 0 || i != len(sig.Params)-1 {
		return transpiler.NilType{}
	}
	return goSigParamType(sig, i)
}

// elemFits reports whether a slice of have spreads into a variadic parameter
// of element type want: they are the same type, or that is not known.
func (t *galaASTTransformer) elemFits(have, want transpiler.Type) bool {
	if t.mentionsUnboundTypeParam(have) || t.mentionsUnboundTypeParam(want) {
		return true
	}
	same, known := t.identicalTypes(have, want)
	return same || !known
}

// spreadNotSliceHint is the hint for spreading e, of argType, which is not a
// slice, into a variadic parameter of element type elem; ok is false when
// argType is not known not to be a slice. A GALA type with a ToGoSlice method
// is told to spread the Go slice it converts to; a string spread into `...byte`
// or `...rune`, go_interop's conversion to one; a value of the element type
// itself, to be passed without `...`.
func (t *galaASTTransformer) spreadNotSliceHint(argType, elem transpiler.Type, e exprSrc) (hint string, ok bool) {
	plain := "pass it without `...`, or spread a Go slice"
	same, known := t.identicalTypes(argType, elem)
	if same && known {
		plain = "pass it as one argument, without `...`"
	}
	if impl, isGala := t.implTypeOf(argType); isGala {
		if impl.meta.IsOpaque {
			return "", false
		}
		if !same && t.hasMemberOrGoMethod(impl.meta, "ToGoSlice") && t.goSliceFits(impl, elem) {
			return fmt.Sprintf("spread the Go slice it converts to; here, `%s.ToGoSlice()...`", receiverText(spreadText(e))), true
		}
		return plain, true
	}
	switch v := t.followAliasChain(argType).(type) {
	case transpiler.BasicType:
		if !transpiler.IsPrimitiveType(v.Name) {
			return "", false
		}
		if v.Name == "string" && !transpiler.IsUnusable(elem) {
			switch canonicalScalar(elem.String()) {
			case "uint8":
				return fmt.Sprintf("spread the Go slice of its bytes; here, `go_interop.ToBytes(%s)...`", spreadText(e)), true
			case "int32":
				return fmt.Sprintf("spread the Go slice of its runes; here, `go_interop.ToRunes(%s)...`", spreadText(e)), true
			}
		}
		return plain, true
	case transpiler.ArrayType, transpiler.MapType, transpiler.FuncType, transpiler.ChanType:
		return plain, true
	}
	return "", false
}

// goSliceFits reports whether the Go slice impl's ToGoSlice method gives
// spreads into a variadic parameter of element type elem, or that is not
// known: a method declared in a hand-written .go file is not read.
func (t *galaASTTransformer) goSliceFits(impl implType, elem transpiler.Type) bool {
	m := impl.meta.Methods["ToGoSlice"]
	if m == nil {
		return true
	}
	slice, ok := t.followAliasChain(unwrapValueMarkers(t.substituteConcreteTypes(m.ReturnType, impl.meta.TypeParams, impl.args))).(transpiler.ArrayType)
	return !ok || t.elemFits(slice.Elem, elem)
}

// spreadText is the spread argument e as written, on one line.
func spreadText(e exprSrc) string { return oneLine(e.at().sourceText()) }

// spreadError is GALA-E0138 at the spread argument e.
func spreadError(e exprSrc, msg, hint string) error {
	at := e.at().rng()
	return spanErrorAt(galaerr.CodeSpreadNotSlice, at, at, msg, hint)
}
