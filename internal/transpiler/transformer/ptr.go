package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Non-null pointers
//
// `Ptr[T]` is a pointer that is never nil. It is a compile-time marker: the
// generated Go spells it `*T`, so receivers, field access, method sets and Go
// calls are those of `*T`. In the type system it is a transpiler.PointerType
// with NonNull set. `&x` produces one, a `Ptr[T]` fills a `*T` slot as it is,
// and a nullable `*T` (or `nil`) never fills a `Ptr[T]` slot (GALA-E0080): a
// Go pointer becomes a `Ptr` only through RequirePtr, which checks it, or an
// OptionPtr through OptionPtrOf. A Ptr has no default value (GALA-E0075).
//
// The generated `*T` of a `Ptr[T]` is recorded in ptrStars, so a type read
// back from generated Go (astTypeToTranspilerType) keeps the marker.

// ptrMayBeAbsentHint is the hint for a nil where a Ptr is needed.
const ptrMayBeAbsentHint = "a pointer that may be absent is an OptionPtr[T] (EmptyOptionPtr(), OptionPtrOf(p)); a Ptr always points somewhere"

// namesPtrType reports whether expr, the base of an index expression in
// expression position (a type argument, `None[Ptr[T]]()`), names the `Ptr`
// marker: `Ptr` or `std.Ptr`, as no binding names it.
func (t *galaASTTransformer) namesPtrType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == galaparser.PtrTypeName && t.bindingScope(e.Name) == nil
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && e.Sel.Name == galaparser.PtrTypeName && t.bindingScope(pkg.Name) == nil &&
			registry.Global.IsPreludePackage(pkg.Name)
	}
	return false
}

// ptrStar is the Go type `*elem` of a non-null `Ptr[elem]`.
func (t *galaASTTransformer) ptrStar(elem ast.Expr) *ast.StarExpr {
	star := &ast.StarExpr{X: elem}
	if t.ptrStars == nil {
		t.ptrStars = make(map[*ast.StarExpr]bool)
	}
	t.ptrStars[star] = true
	return star
}

// isPtrTypeExpr reports whether typeExpr is the generated `*T` of a `Ptr[T]`.
func (t *galaASTTransformer) isPtrTypeExpr(typeExpr ast.Expr) bool {
	star, ok := typeExpr.(*ast.StarExpr)
	return ok && t.ptrStars[star]
}

// ptrError is a GALA-E0080 at ctx.
func ptrError(ctx antlr.ParserRuleContext, msg, hint string) error {
	return ptrErrorAt(srcNode{ctx: ctx}, msg, hint)
}

// ptrErrorAt is ptrError at a node of either tree.
func ptrErrorAt(at srcNode, msg, hint string) error {
	r := at.rng()
	return spanErrorAt(galaerr.CodePtrNullability, r, r, msg, hint)
}

// checkPtrSlot rejects a value that may be nil filling a slot whose type has
// a `Ptr` where the value's type has a nullable pointer (GALA-E0080): `nil`
// or a `*T` where a `Ptr[T]` is expected, or an `Array[*T]` where an
// `Array[Ptr[T]]` is. A value whose type is unknown is left alone.
func (t *galaASTTransformer) checkPtrSlot(expr ast.Expr, expected transpiler.Type, at srcNode) error {
	if transpiler.IsUnusable(expected) || !at.ok() {
		return nil
	}
	expected = t.followAliasChain(expected)
	if err := t.checkPtrFuncRefSlot(expr, expected, at); err != nil {
		return err
	}
	// Most slots hold no Ptr: skip the expression's type inference for them.
	if !hasPtr(expected) {
		return t.checkPtrInterfaceSlot(expr, expected, at)
	}
	if isNilIdent(expr) {
		if !transpiler.IsPtr(expected) {
			return nil
		}
		return ptrErrorAt(at, fmt.Sprintf("cannot use nil as %s: a Ptr is never nil", displayType(expected)), ptrMayBeAbsentHint)
	}
	actual := t.probeExprType(expr)
	if transpiler.IsUnusable(actual) || !nullableWherePtr(expected, actual) {
		return nil
	}
	text := at.sourceText()
	hint := fmt.Sprintf("pass RequirePtr(%s); RequirePtr panics on nil, and a pointer that may be nil is kept as OptionPtrOf(%s)", text, text)
	if !transpiler.IsPtr(expected) {
		hint = "build the value from Ptr values: `&x`, or RequirePtr(p) for a pointer from Go"
	}
	return ptrErrorAt(at,
		fmt.Sprintf("cannot use %s (%s) as %s: a pointer that may be nil cannot fill a Ptr", text, displayType(actual), displayType(expected)),
		hint)
}

// ptrReceiverMethodNames returns the names of the methods of types declared
// on a `Ptr` receiver. Only a method of one of these names can need a
// non-null receiver, so the receiver checks skip every other one.
func ptrReceiverMethodNames(types map[string]*transpiler.TypeMetadata) map[string]bool {
	var names map[string]bool
	for _, meta := range types {
		for name, m := range meta.Methods {
			if m == nil || !m.PtrReceiver {
				continue
			}
			if names == nil {
				names = make(map[string]bool)
			}
			names[name] = true
		}
	}
	return names
}

// nullablePtrReceiver reports whether recvType is a pointer that may be nil
// whose type declares method on a `Ptr` receiver, and returns the pointer. A
// `T` value is addressed (`&x` is a Ptr) and a Ptr already is one.
func (t *galaASTTransformer) nullablePtrReceiver(recvType transpiler.Type, method string) (transpiler.PointerType, bool) {
	ptr, ok := recvType.(transpiler.PointerType)
	if !ok || ptr.NonNull || !t.ptrReceiverMethods[method] {
		return ptr, false
	}
	_, meta, _, ok := t.receiverTypeInfo(t.methodReceiverType(ptr.Elem, method))
	if !ok || meta == nil {
		return ptr, false
	}
	m := meta.Methods[method]
	return ptr, m != nil && m.PtrReceiver
}

// ptrOf displays the `Ptr` to ptr's element.
func ptrOf(ptr transpiler.PointerType) string {
	return displayType(transpiler.PointerType{Elem: ptr.Elem, NonNull: true})
}

// checkPtrReceiver rejects selecting a method declared on a `Ptr[T]`
// receiver — a call `p.M()` or a method value `p.M` — on a pointer that may be
// nil (GALA-E0080): the receiver fills a Ptr slot. recv is the lowered
// receiver and recvType its type; m is the member access, whose receiver is
// read, on m's tree, only to report the error.
func (t *galaASTTransformer) checkPtrReceiver(recv ast.Expr, recvType transpiler.Type, method string, m memberSrc) error {
	return t.checkPtrReceiverIn(recv, recvType, method, func() (srcNode, srcNode, string) { return m.receiver(t) })
}

// checkPtrReceiverIn is checkPtrReceiver for a receiver located by source,
// which returns the nodes, of either tree, spanning it and its source text,
// and is called only to report the error: a member access's receiver, or the
// value a `bind` or `also` clause lowers to the receiver of FlatMap or ZipN.
func (t *galaASTTransformer) checkPtrReceiverIn(recv ast.Expr, recvType transpiler.Type, method string, source func() (from, to srcNode, text string)) error {
	ptr, ok := t.nullablePtrReceiver(recvType, method)
	if !ok {
		return nil
	}
	from, to, text := source()
	hint := fmt.Sprintf("call it on RequirePtr(%s), which panics on nil; a pointer that may be nil is kept as OptionPtrOf(%s)", text, text)
	if id, isIdent := recv.(*ast.Ident); isIdent && t.fixedBindingOf(id.Name) == fixedPointerReceiver {
		hint = fmt.Sprintf("declare the receiver `%s %s`, or call it on RequirePtr(%s)", id.Name, ptrOf(ptr), text)
	}
	return spanErrorAt(galaerr.CodePtrNullability, from.rng(), to.rng(),
		fmt.Sprintf("cannot call %s on %s (%s): %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr", method, text, displayType(ptr), method, ptrOf(ptr)),
		hint)
}

// checkPtrZipResult rejects an `also` group lowered to `ZipN(...).FlatMap(...)`
// when the ZipN returns a pointer that may be nil and FlatMap has a `Ptr`
// receiver (GALA-E0080): the FlatMap would run on whatever the Zip returns.
// The call is the lowering's, not the user's, so the fix is in the monad.
// zip is the ZipN and zipResultType its result; from and to span the group.
func (t *galaASTTransformer) checkPtrZipResult(zipResultType transpiler.Type, zip *transpiler.MethodMetadata, from, to srcNode) error {
	ptr, ok := t.nullablePtrReceiver(zipResultType, "FlatMap")
	if !ok {
		return nil
	}
	declared, _ := zip.ReturnType.(transpiler.PointerType)
	return spanErrorAt(galaerr.CodePtrNullability, from.rng(), to.rng(),
		fmt.Sprintf("cannot bind this group through %s: it returns %s, a pointer that may be nil, and FlatMap has a %s receiver", zip.Name, displayType(ptr), ptrOf(ptr)),
		fmt.Sprintf("declare %s to return %s or a value, or bind the clauses one after another with `bind`", zip.Name, ptrOf(declared)))
}

// checkPtrViewReceiverAt rejects a match through the sealed view of a pointer
// that may be nil when its MatchView has a `Ptr` receiver (GALA-E0080), as
// checkPtrReceiver rejects calling it. at locates the match.
func (t *galaASTTransformer) checkPtrViewReceiverAt(subjectType transpiler.Type, at srcNode) error {
	ptr, ok := t.nullablePtrReceiver(subjectType, sealedViewMethod)
	if !ok {
		return nil
	}
	return ptrErrorAt(at,
		fmt.Sprintf("cannot match a %s through %s: %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr",
			displayType(ptr), sealedViewMethod, sealedViewMethod, ptrOf(ptr)),
		"match RequirePtr(p), which panics on nil, or an OptionPtrOf(p)")
}

// checkPtrInterfaceSlot rejects a pointer that may be nil filling an
// interface slot (GALA-E0080) when its type implements a method of the
// interface on a `Ptr` receiver: a call through the interface would run that
// method on whatever pointer the interface holds.
func (t *galaASTTransformer) checkPtrInterfaceSlot(expr ast.Expr, expected transpiler.Type, at srcNode) error {
	if len(t.ptrReceiverMethods) == 0 || isNilIdent(expr) {
		return nil
	}
	methods := t.ptrInterfaceMethods(expected)
	if len(methods) == 0 {
		return nil
	}
	ptr, m, ok := t.nullablePtrMethod(t.probeExprType(expr), methods)
	if !ok {
		return nil
	}
	text := at.sourceText()
	return ptrErrorAt(at,
		fmt.Sprintf("cannot use %s (%s) as %s: its method %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr",
			text, displayType(ptr), displayType(expected), m, ptrOf(ptr)),
		fmt.Sprintf("pass RequirePtr(%s); RequirePtr panics on nil", text))
}

// ptrInterfaceMethods returns, sorted, the methods of the interface iface
// that some type declares on a `Ptr` receiver.
func (t *galaASTTransformer) ptrInterfaceMethods(iface transpiler.Type) []string {
	methods, _ := t.interfaceMethodNames(iface)
	methods = slices.DeleteFunc(methods, func(m string) bool { return !t.ptrReceiverMethods[m] })
	slices.Sort(methods)
	return methods
}

// nullablePtrMethod returns the first of methods that typ, a pointer that
// may be nil, declares on a `Ptr` receiver, and the pointer.
func (t *galaASTTransformer) nullablePtrMethod(typ transpiler.Type, methods []string) (transpiler.PointerType, string, bool) {
	for _, m := range methods {
		if ptr, ok := t.nullablePtrReceiver(typ, m); ok {
			return ptr, m, true
		}
	}
	return transpiler.PointerType{}, "", false
}

// typeParamOwner is a generic function, method or type whose type
// parameters a call or a type fills.
type typeParamOwner struct {
	name        string            // as the source names it
	params      []string          // its type parameters, in order
	constraints map[string]string // the constraint of each, named in pkg
	pkg         string            // the declaring package
}

// ptrConstraint returns the constraint of o's type parameter tp, and its
// methods some type declares on a `Ptr` receiver, when it has any.
func (t *galaASTTransformer) ptrConstraint(o typeParamOwner, tp string) (transpiler.Type, []string) {
	c := o.constraints[tp]
	if c == "" || c == "any" || c == "comparable" {
		return nil, nil
	}
	// A function's or method's constraint is recorded qualified where it is
	// not the package's own (declaredTypeParams); a type's as written, in its
	// own package.
	var iface transpiler.Type = transpiler.NamedType{Name: c}
	if dot := strings.LastIndex(c, "."); dot >= 0 {
		iface = transpiler.NamedType{Package: c[:dot], Name: c[dot+1:]}
	} else if !t.isLocalPackage(o.pkg) {
		iface = transpiler.NamedType{Package: o.pkg, Name: c}
	}
	key := iface.String()
	methods, ok := t.ptrConstraintMethods[key]
	if !ok {
		methods = t.ptrInterfaceMethods(iface)
		if t.ptrConstraintMethods == nil {
			t.ptrConstraintMethods = make(map[string][]string)
		}
		t.ptrConstraintMethods[key] = methods
	}
	return iface, methods
}

// hasPtrConstraint reports whether a type parameter of o has a constraint
// with a method some type declares on a `Ptr` receiver: only then can a type
// argument of o be rejected, so the checks below skip every other owner
// before looking at a type argument.
func (t *galaASTTransformer) hasPtrConstraint(o typeParamOwner) bool {
	if len(t.ptrReceiverMethods) == 0 {
		return false
	}
	for _, tp := range o.params {
		if _, methods := t.ptrConstraint(o, tp); len(methods) > 0 {
			return true
		}
	}
	return false
}

// typeArgSource tells where a type argument comes from: written when text is
// "", else inferred from what text describes — an argument of a pointer type
// when ptr is set.
type typeArgSource struct {
	text string
	ptr  bool
}

// argSource is the typeArgSource of a type argument inferred from the
// argument at at, of type argType.
func argSource(at srcNode, argType transpiler.Type) typeArgSource {
	_, isPtr := argType.(transpiler.PointerType)
	return typeArgSource{text: at.sourceText(), ptr: isPtr}
}

// checkPtrTypeArg rejects typeArg as the type argument of o's type parameter
// tp when it is a pointer that may be nil and tp's constraint has a method
// its type declares on a `Ptr` receiver (GALA-E0080): the generic code calls
// that method on whatever pointer the type argument holds. at locates the
// type argument, or what it is inferred from (src).
func (t *galaASTTransformer) checkPtrTypeArg(o typeParamOwner, tp string, typeArg transpiler.Type, src typeArgSource, at srcNode) error {
	msg, hint, bad := t.ptrTypeArgMisuse(o, tp, typeArg, src)
	if !bad {
		return nil
	}
	return ptrErrorAt(at, msg, hint)
}

// ptrTypeArgMisuse returns the GALA-E0080 message and hint of checkPtrTypeArg
// when typeArg cannot be the type argument of o's tp.
func (t *galaASTTransformer) ptrTypeArgMisuse(o typeParamOwner, tp string, typeArg transpiler.Type, src typeArgSource) (msg, hint string, bad bool) {
	iface, methods := t.ptrConstraint(o, tp)
	if len(methods) == 0 {
		return "", "", false
	}
	ptr, m, ok := t.nullablePtrMethod(typeArg, methods)
	if !ok {
		return "", "", false
	}
	msg = fmt.Sprintf("type argument %s (%s) of %s is %s", tp, displayType(iface), o.name, displayType(ptr))
	switch {
	case src.text == "":
		hint = fmt.Sprintf("make the type argument %s", ptrOf(ptr))
	case src.ptr:
		hint = fmt.Sprintf("pass RequirePtr(%s); RequirePtr panics on nil", src.text)
	default:
		hint = fmt.Sprintf("make it a %s: `&x`, or RequirePtr(p) for a pointer that may be nil", ptrOf(ptr))
	}
	if src.text != "" {
		msg += ", inferred from " + src.text
	}
	msg += fmt.Sprintf(": its method %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr", m, ptrOf(ptr))
	return msg, hint, true
}

// checkPtrSlotTypeArgs checks, as checkPtrTypeArg does, the type arguments
// of the generic function funcMeta (named name) that the slot of a call at
// line:col fills — those of resolved that no argument binds (argBound).
func (t *galaASTTransformer) checkPtrSlotTypeArgs(name string, funcMeta *transpiler.FunctionMetadata, resolved map[string]transpiler.Type, argBound map[string]bool, line, col int) error {
	o := funcTypeParamOwner(name, funcMeta)
	if !t.hasPtrConstraint(o) {
		return nil
	}
	src := typeArgSource{text: "the type the call is expected to have"}
	for _, tp := range o.params {
		typeArg, ok := resolved[tp]
		if !ok || argBound[tp] {
			continue
		}
		if msg, hint, bad := t.ptrTypeArgMisuse(o, tp, typeArg, src); bad {
			return galaerr.NewCodedSemanticError(galaerr.CodePtrNullability, line, col, msg, hint)
		}
	}
	return nil
}

// checkPtrFuncRefSlot rejects a reference to a generic GALA function (`bumpIt`)
// filling a slot of function type when the function's type arguments, taken
// from that type, fail checkPtrTypeArg: the function's body would call a Ptr
// receiver's method on whatever pointer it is called with.
func (t *galaASTTransformer) checkPtrFuncRefSlot(expr ast.Expr, expected transpiler.Type, at srcNode) error {
	expectedFT, isFT := expected.(transpiler.FuncType)
	if !isFT || len(t.ptrReceiverMethods) == 0 {
		return nil
	}
	fm := t.lookupBareFuncRefMeta(expr)
	if fm == nil || len(fm.TypeParams) == 0 {
		return nil
	}
	o := funcTypeParamOwner(at.sourceText(), fm)
	if !t.hasPtrConstraint(o) {
		return nil
	}
	bound := make(map[string]transpiler.Type)
	t.unifyForInference(t.funcMetaToRawType(fm), expectedFT, fm.TypeParams, bound)
	src := typeArgSource{text: fmt.Sprintf("the function type it fills, %s", displayType(expectedFT))}
	return t.checkPtrBoundTypeArgs(o, 0, bound, src, at)
}

// funcTypeParamOwner is the typeParamOwner of the function m, which name names.
func funcTypeParamOwner(name string, m *transpiler.FunctionMetadata) typeParamOwner {
	return typeParamOwner{name: name, params: m.TypeParams, constraints: m.TypeParamConstraints, pkg: m.Package}
}

// methodTypeParamOwner is the typeParamOwner of the method m's own type
// parameters, which name names.
func methodTypeParamOwner(name string, m *transpiler.MethodMetadata) typeParamOwner {
	return typeParamOwner{name: name, params: m.TypeParams, constraints: m.TypeParamConstraints, pkg: m.Package}
}

// typeTypeParamOwner is the typeParamOwner of the generic type m, which name names.
func typeTypeParamOwner(name string, m *transpiler.TypeMetadata) typeParamOwner {
	return typeParamOwner{name: name, params: m.TypeParams, constraints: m.TypeParamConstraints, pkg: m.Package}
}

// checkPtrWrittenTypeArgsAt checks the written type arguments args of o, the
// i-th located on either tree by at(i), with checkPtrTypeArg.
func (t *galaASTTransformer) checkPtrWrittenTypeArgsAt(o typeParamOwner, args []ast.Expr, at func(i int) srcNode) error {
	if !t.hasPtrConstraint(o) {
		return nil
	}
	for i, arg := range args[:min(len(args), len(o.params))] {
		if err := t.checkPtrTypeArg(o, o.params[i], t.astTypeToTranspilerType(arg), typeArgSource{}, at(i)); err != nil {
			return err
		}
	}
	return nil
}

// checkPtrBoundTypeArgs checks, with checkPtrTypeArg, the type arguments
// bound binds to o's type parameters, inferred from src at at. The first
// written of o's type parameters are not inferred.
func (t *galaASTTransformer) checkPtrBoundTypeArgs(o typeParamOwner, written int, bound map[string]transpiler.Type, src typeArgSource, at srcNode) error {
	for i, tp := range o.params {
		typeArg, ok := bound[tp]
		if !ok || i < written {
			continue
		}
		if err := t.checkPtrTypeArg(o, tp, typeArg, src, at); err != nil {
			return err
		}
	}
	return nil
}

// checkPtrTypeArgsOfType checks the type arguments args, written as written,
// of the generic type s names (`Holder[*Counter]`) with checkPtrTypeArg.
func (t *galaASTTransformer) checkPtrTypeArgsOfType(s typeSrc, args []ast.Expr, written []typeSrc) error {
	if len(t.ptrReceiverMethods) == 0 {
		return nil
	}
	name := s.nameText()
	if t.activeTypeParams[name] {
		return nil
	}
	meta := t.getTypeMeta(name)
	if meta == nil || len(meta.TypeParamConstraints) == 0 {
		return nil
	}
	o := typeTypeParamOwner(name, meta)
	return t.checkPtrWrittenTypeArgsAt(o, args, func(i int) srcNode { return written[i].at() })
}

// checkPtrCallTypeArgs checks, with checkPtrTypeArg, the type arguments the
// arguments of a call of a generic function (funcMeta), or of a construction
// of a generic struct, bind, and those of a generic function an argument
// refers to. args are its positional arguments and named its named ones,
// lowered.
func (t *galaASTTransformer) checkPtrCallTypeArgs(fun ast.Expr, list argsSrc, args []ast.Expr, named map[string]ast.Expr, callCtx functionCallContext) error {
	if len(t.ptrReceiverMethods) == 0 {
		return nil
	}
	base, written := splitCallFunTypeArgs(fun)
	o, names, types, ok := t.callTypeParams(base, callCtx)
	if !ok || len(o.params) == 0 {
		return nil
	}
	constrained := t.hasPtrConstraint(o)
	if !constrained && !slices.ContainsFunc(args, t.isGenericFuncRef) && !t.anyGenericFuncRef(named) {
		return nil
	}
	type callArg struct {
		expr   ast.Expr
		at     srcNode
		param  transpiler.Type
		spread bool // `xs...`, whose elements fill param
	}
	var plain, funcRefs []callArg
	pos := 0
	for _, a := range list.list {
		arg, idx := ast.Expr(nil), -1
		if name, _ := a.name(); name != "" {
			arg, idx = named[name], slices.Index(names, name)
		} else {
			if pos < len(args) {
				arg, idx = args[pos], pos
			}
			pos++
		}
		// A variadic function records its last parameter as the element
		// type, which every argument past it fills.
		if idx >= len(types) && callCtx.funcMeta != nil {
			idx = len(types) - 1
		}
		v, err := t.argContent(a)
		if err != nil || arg == nil || idx < 0 || idx >= len(types) || types[idx] == nil {
			continue
		}
		at := v.at()
		if !at.ok() {
			continue
		}
		if t.isGenericFuncRef(arg) {
			funcRefs = append(funcRefs, callArg{arg, at, types[idx], v.spread})
		} else {
			plain = append(plain, callArg{arg, at, types[idx], v.spread})
		}
	}
	// The call's type arguments: written, then bound by its arguments.
	all := t.writtenTypeArgs(o.params, written[:min(len(written), len(o.params))])
	for _, a := range plain {
		argType := t.probeExprType(a.expr)
		if arr, isArr := argType.(transpiler.ArrayType); a.spread && isArr {
			argType = arr.Elem
		}
		if transpiler.IsUnusable(argType) {
			continue
		}
		bound := make(map[string]transpiler.Type)
		t.unifyForInference(a.param, argType, o.params, bound)
		if err := t.checkPtrBoundTypeArgs(o, len(written), bound, argSource(a.at, argType), a.at); err != nil {
			return err
		}
		for tp, typ := range bound {
			if _, seen := all[tp]; !seen {
				all[tp] = typ
			}
		}
	}
	// A reference to a generic function takes its type arguments from the
	// parameter it fills once the call's own are known (`apply(bumpIt, p)`
	// for `func apply[T any](f func(T), x T)`).
	for _, ref := range funcRefs {
		if err := t.checkPtrFuncRefSlot(ref.expr, t.followAliasChain(t.substituteInType(ref.param, all)), ref.at); err != nil {
			return err
		}
	}
	return nil
}

// isGenericFuncRef reports whether expr is a bare reference to a generic
// GALA function (`bumpIt`), whose type arguments the slot it fills gives.
func (t *galaASTTransformer) isGenericFuncRef(expr ast.Expr) bool {
	fm := t.lookupBareFuncRefMeta(expr)
	return fm != nil && len(fm.TypeParams) > 0
}

// anyGenericFuncRef reports whether a value of named is isGenericFuncRef.
func (t *galaASTTransformer) anyGenericFuncRef(named map[string]ast.Expr) bool {
	for _, e := range named {
		if t.isGenericFuncRef(e) {
			return true
		}
	}
	return false
}

// callTypeParams returns the generic function or struct a call of base
// calls or constructs, with the names and declared types of what its
// arguments fill: the function's parameters, the struct's fields, or the
// parameters of its companion Apply when the arguments do not build the
// struct itself.
func (t *galaASTTransformer) callTypeParams(base ast.Expr, callCtx functionCallContext) (typeParamOwner, []string, []transpiler.Type, bool) {
	if m := callCtx.funcMeta; m != nil {
		_, name := extractTypeNameFromExpr(base)
		return funcTypeParamOwner(name, m), m.ParamNames, m.ParamTypes, true
	}
	if len(callCtx.structTypeParams) == 0 {
		return typeParamOwner{}, nil, nil, false
	}
	_, name := extractTypeNameFromExpr(base)
	meta, resolved := t.getTypeMetaResolved(name)
	if meta == nil {
		return typeParamOwner{}, nil, nil, false
	}
	o := typeTypeParamOwner(name, meta)
	if !callCtx.structLiteral {
		if apply := meta.Methods["Apply"]; apply != nil {
			return o, apply.ParamNames, apply.ParamTypes, true
		}
	}
	resolved = t.resolveStructTypeName(resolved)
	fields := t.structFields[resolved]
	types := make([]transpiler.Type, len(fields))
	for i, f := range fields {
		types[i] = t.structFieldTypes[resolved][f]
	}
	return o, fields, types, true
}

// writtenTypeArgOwner returns the generic function, method or type that
// base, the base of an index expression, names, whose type parameters the
// index's type arguments fill. genericMethod tells that base selects a
// method with its own type parameters (namesGenericMethod).
func (t *galaASTTransformer) writtenTypeArgOwner(base ast.Expr, genericMethod bool) (typeParamOwner, bool) {
	name := ""
	switch e := base.(type) {
	case *ast.Ident:
		if t.bindingScope(e.Name) != nil {
			return typeParamOwner{}, false
		}
		name = e.Name
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok && t.importManager.IsPackage(pkg.Name) && t.bindingScope(pkg.Name) == nil {
			name = pkg.Name + "." + e.Sel.Name
			break
		}
		// A generic method of the receiver's type (`b.Map[int]`).
		if !genericMethod {
			return typeParamOwner{}, false
		}
		_, lookupBaseName := t.resolveReceiverTypeAndLookupKey(e.X, e.Sel.Name)
		if meta, _ := t.getTypeMetaResolved(lookupBaseName); meta != nil {
			if m := meta.Methods[e.Sel.Name]; m != nil {
				return methodTypeParamOwner(m.Name, m), true
			}
		}
		return typeParamOwner{}, false
	default:
		return typeParamOwner{}, false
	}
	if m := t.getFunction(name); m != nil {
		return funcTypeParamOwner(name, m), true
	}
	if m := t.getTypeMeta(name); m != nil && len(m.TypeParams) > 0 {
		return typeTypeParamOwner(name, m), true
	}
	return typeParamOwner{}, false
}

// hasPtr reports whether typ has a `Ptr` anywhere in it.
func hasPtr(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.PointerType:
		return v.NonNull || hasPtr(v.Elem)
	case transpiler.ArrayType:
		return hasPtr(v.Elem)
	case transpiler.ChanType:
		return hasPtr(v.Elem)
	case transpiler.MapType:
		return hasPtr(v.Key) || hasPtr(v.Elem)
	case transpiler.GenericType:
		for _, p := range v.Params {
			if hasPtr(p) {
				return true
			}
		}
	case transpiler.FuncType:
		for _, r := range v.Results {
			if hasPtr(r) {
				return true
			}
		}
	}
	return false
}

// nullableWherePtr reports whether actual has a nullable pointer at a
// position where expected has a `Ptr`. Function results are compared; a
// function parameter is not, since a function taking a Ptr may take any
// pointer the caller checked.
func nullableWherePtr(expected, actual transpiler.Type) bool {
	if transpiler.IsUnusable(expected) || transpiler.IsUnusable(actual) {
		return false
	}
	switch e := expected.(type) {
	case transpiler.PointerType:
		a, ok := actual.(transpiler.PointerType)
		if !ok {
			return false
		}
		return (e.NonNull && !a.NonNull) || nullableWherePtr(e.Elem, a.Elem)
	case transpiler.ArrayType:
		a, ok := actual.(transpiler.ArrayType)
		return ok && nullableWherePtr(e.Elem, a.Elem)
	case transpiler.ChanType:
		a, ok := actual.(transpiler.ChanType)
		return ok && nullableWherePtr(e.Elem, a.Elem)
	case transpiler.MapType:
		a, ok := actual.(transpiler.MapType)
		return ok && (nullableWherePtr(e.Key, a.Key) || nullableWherePtr(e.Elem, a.Elem))
	case transpiler.GenericType:
		a, ok := actual.(transpiler.GenericType)
		if !ok || len(a.Params) != len(e.Params) || stripPackagePrefix(a.Base.BaseName()) != stripPackagePrefix(e.Base.BaseName()) {
			return false
		}
		for i := range e.Params {
			if nullableWherePtr(e.Params[i], a.Params[i]) {
				return true
			}
		}
	case transpiler.FuncType:
		a, ok := actual.(transpiler.FuncType)
		if !ok || len(a.Results) != len(e.Results) {
			return false
		}
		for i := range e.Results {
			if nullableWherePtr(e.Results[i], a.Results[i]) {
				return true
			}
		}
	}
	return false
}

// checkPtrNilComparison rejects `p == nil` and `p != nil` on a `Ptr`
// (GALA-E0080): a Ptr is never nil, so the comparison asks nothing.
func (t *galaASTTransformer) checkPtrNilComparison(tok token.Token, at srcNode, left, right ast.Expr) error {
	if tok != token.EQL && tok != token.NEQ {
		return nil
	}
	operand := left
	switch {
	case isNilIdent(right):
	case isNilIdent(left):
		operand = right
	default:
		return nil
	}
	typ := t.probeExprType(operand)
	if !transpiler.IsPtr(typ) {
		return nil
	}
	return ptrErrorAt(at,
		fmt.Sprintf("%s compares a %s with nil, but a Ptr is never nil", at.sourceText(), displayType(typ)),
		"drop the comparison; "+ptrMayBeAbsentHint)
}

// ptrEntryChecks returns, for each parameter of params that is a `Ptr` and
// whose position guarded selects, the statement `if p == nil { panic(...) }`.
// It keeps a Ptr parameter non-null when Go is the caller: of an exported
// function or method, which Go code can call, and of a lambda filling a
// function slot whose parameter is a nullable `*T`, which its caller fills
// with whatever pointer it has. A GALA-to-GALA call needs no check.
func (t *galaASTTransformer) ptrEntryChecks(params *ast.FieldList, owner string, guarded func(pos int) bool) []ast.Stmt {
	if params == nil {
		return nil
	}
	var checks []ast.Stmt
	pos := 0
	for _, field := range params.List {
		if !t.isPtrTypeExpr(field.Type) {
			pos += max(len(field.Names), 1)
			continue
		}
		typ := displayType(t.astTypeToTranspilerType(field.Type))
		for _, n := range field.Names {
			if n.Name != "_" && guarded(pos) {
				checks = append(checks, ptrNilCheck(n.Name, fmt.Sprintf("%s: parameter %s is a %s, but the caller passed nil", owner, n.Name, typ)))
			}
			pos++
		}
	}
	return checks
}

// ptrReceiverEntryCheck returns, for a `Ptr` receiver recv of an exported
// method, the statement `if r == nil { panic(...) }`. GALA never calls such a
// method on a pointer that may be nil (checkPtrReceiver) nor turns one into an
// interface it implements (checkPtrSlot), so only Go code can call it with a
// nil receiver.
func (t *galaASTTransformer) ptrReceiverEntryCheck(recv *ast.Field, owner string) []ast.Stmt {
	if recv == nil || !t.isPtrTypeExpr(recv.Type) || len(recv.Names) != 1 || recv.Names[0].Name == "_" {
		return nil
	}
	name := recv.Names[0].Name
	typ := displayType(t.astTypeToTranspilerType(recv.Type))
	return []ast.Stmt{ptrNilCheck(name, fmt.Sprintf("%s: receiver %s is a %s, but the caller passed nil", owner, name, typ))}
}

// ptrNilCheck is `if name == nil { panic(msg) }`.
func ptrNilCheck(name, msg string) ast.Stmt {
	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{X: ast.NewIdent(name), Op: token.EQL, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: panicStmt(msg)},
	}
}

// withPtrEntryChecks prepends checks to body.
func withPtrEntryChecks(body *ast.BlockStmt, checks []ast.Stmt) *ast.BlockStmt {
	if body == nil || len(checks) == 0 {
		return body
	}
	return &ast.BlockStmt{Lbrace: body.Lbrace, List: append(checks, body.List...), Rbrace: body.Rbrace}
}
