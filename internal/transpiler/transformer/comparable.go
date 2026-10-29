package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Equality
//
// `a == b` means `a.Equal(b)`, and `a != b` means `!a.Equal(b)`, for every
// type with an Equal method: every GALA struct, sealed type and tuple (Equal
// is synthesized) and every type that declares its own (the collections,
// user types). A stable-identifier pattern compares the same way. Values
// compare by value, as Scala's case classes do: two Lists built separately
// with the same elements are ==.
//
// The comparison stays Go's `==` where that gives Equal's answer: a type made
// of primitives only (by value, through fields, cases and type arguments),
// with no Equal of its own anywhere in it. Go's == on a float is IEEE's, as
// std.Equal's is, so a NaN equals nothing either way. Such comparisons, the
// hot ones, cost what they did.
//
// Go's `==` is kept, with Go's meaning, for what has no Equal: pointers
// (`*T`, Ptr), interfaces, channels, Go types, nil, and type parameters, whose
// type is not known statically (`T comparable` compares with Go's ==). A type
// that is not known is left to Go as well.
//
// Inside a type's own Equal method, == on that type is Go's: lowering it to
// Equal would call the method being defined. A type Go cannot compare is
// GALA-E0084 there.
//
// GALA-E0084 reports == on what Go cannot compare and that has no Equal: a
// function, a Go slice or map, a Go type holding one, and a type parameter
// constrained by any. Left to Go, the comparison failed against the generated
// code, naming the internal std.Immutable wrapper of a `val` field.

// eqMode is how `==` compares values of a type.
type eqMode int

const (
	eqGo           eqMode = iota // Go's ==: it gives Equal's answer, or the type has no Equal
	eqMethod                     // the Equal method
	eqIncomparable               // neither: GALA-E0084
)

// equality is how == compares values of a type: mode, why Go cannot compare
// them (eqIncomparable), and the Equal method's receiver (eqMethod).
type equality struct {
	mode       eqMode
	why        *incomparable
	ptrReceiver bool // Equal has a pointer receiver
	ownEqual   bool // eqIncomparable inside the type's own Equal
}

// incomparable says why a type cannot be compared: the type at the bottom
// (leaf) and what it is (kind), and the first step from the compared type down
// to it: a field (via, of type viaType; direct when viaType is the leaf), or a
// type of another package whose unexported field holds the leaf (holder).
type incomparable struct {
	leaf    transpiler.Type
	kind    string
	via     string
	viaType transpiler.Type
	direct  bool
	holder  transpiler.Type
}

const (
	kindFunction           = "a function"
	kindUnconstrainedParam = "a type parameter constrained by any"
	kindGoIncomparable     = "a Go type that is not comparable"
)

// atLeaf reports whether no step has been recorded yet: the type r was found
// on is the leaf itself.
func (r *incomparable) atLeaf() bool {
	return r.via == "" && r.holder == nil
}

// incomparableReason is the clause of the message that says why the compared
// type cannot be compared.
func (t *galaASTTransformer) incomparableReason(r *incomparable) string {
	kind := r.kind
	// A type parameter or Go type is named; "a Go slice" alone says what the
	// leaf is.
	if r.kind == kindUnconstrainedParam || r.kind == kindGoIncomparable {
		kind = t.sourceTypeName(r.leaf) + ", " + r.kind
	}
	switch {
	case r.via != "" && r.direct:
		return fmt.Sprintf("%s has type %s, %s", r.via, t.sourceTypeName(r.viaType), r.kind)
	case r.via != "":
		return fmt.Sprintf("%s has type %s, which holds %s", r.via, t.sourceTypeName(r.viaType), kind)
	case r.holder != nil:
		return fmt.Sprintf("%s holds %s", t.sourceTypeName(r.holder), kind)
	}
	return fmt.Sprintf("%s is %s", t.sourceTypeName(r.leaf), r.kind)
}

// sourceTypeName is typ as this file's source spells it (diagTypeName), with
// std's types by their bare name.
func (t *galaASTTransformer) sourceTypeName(typ transpiler.Type) string {
	return transpiler.DisplayTypeNamed(typ, func(leaf transpiler.Type) string {
		if pkg, name, _ := leafQualifier(leaf); pkg == registry.StdPackageName {
			return name
		}
		return t.diagLeafName(leaf)
	})
}

// typeArg is the type a type parameter of a declaration stands for, with the
// type arguments its own names refer to (env). declared reports whether it
// was written in GALA source, where `[]T` is a slice: a type read off an
// expression may be a Go array, which the type model spells the same way.
type typeArg struct {
	typ      transpiler.Type
	env      map[string]typeArg
	declared bool
}

// typeEq is what the walk of a type finds: whether Go's == on it gives
// Equal's answer (native), and why Go cannot compare it at all (bad; nil when
// it can or that is not known).
type typeEq struct {
	native bool
	bad    *incomparable
}

// comparer walks a type for how == compares it. visiting holds the
// declarations being walked, so a type that holds itself through another is
// read once.
type comparer struct {
	t        *galaASTTransformer
	visiting map[string]bool
}

// equalityOf is how == compares two values of typ, the type of an expression.
func (t *galaASTTransformer) equalityOf(typ transpiler.Type) equality {
	if transpiler.IsUnusable(typ) {
		return equality{mode: eqGo}
	}
	c := comparer{t: t}
	w := c.walk(typ, nil, false)
	if w.native {
		return equality{mode: eqGo}
	}
	if meta, ok := t.equalDeclaration(typ); ok {
		if t.isEqualOwner(meta) {
			if w.bad != nil {
				return equality{mode: eqIncomparable, why: w.bad, ownEqual: true}
			}
			return equality{mode: eqGo}
		}
		if ptr, has := t.equalMethodOf(meta); has {
			return equality{mode: eqMethod, ptrReceiver: ptr}
		}
	}
	if w.bad != nil {
		return equality{mode: eqIncomparable, why: w.bad}
	}
	return equality{mode: eqGo}
}

// equalDeclaration returns the GALA declaration of typ when it is a struct,
// sealed type, tuple or opaque type, which may have an Equal method.
func (t *galaASTTransformer) equalDeclaration(typ transpiler.Type) (*transpiler.TypeMetadata, bool) {
	typ, _ = transpiler.UnwrapSendable(typ)
	if target, isAlias := t.unaliased(typ); isAlias {
		return t.equalDeclaration(target)
	}
	switch typ.(type) {
	case transpiler.NamedType, transpiler.BasicType, transpiler.GenericType:
	default:
		return nil, false
	}
	if t.activeTypeParams[typ.String()] {
		return nil, false
	}
	_, meta, _, ok := t.receiverTypeInfo(typ)
	if !ok || meta == nil || meta.DefinedIn == "" || meta.IsInterface {
		return nil, false
	}
	return meta, true
}

// isEqualOwner reports whether the Equal method being transformed is meta's.
func (t *galaASTTransformer) isEqualOwner(meta *transpiler.TypeMetadata) bool {
	o := t.equalOwner
	return o != nil && (o == meta || (o.Name == meta.Name && o.Package == meta.Package))
}

// equalMethodOf reports whether the declaration meta has an Equal method that
// == can call, `Equal(other T) bool`, and whether its receiver is a pointer.
// A struct or sealed type has one unless it declares an Equal of another
// shape; an opaque type has one only when it declares it.
func (t *galaASTTransformer) equalMethodOf(meta *transpiler.TypeMetadata) (ptrReceiver, has bool) {
	if m, ok := meta.Methods["Equal"]; ok {
		if len(m.ParamTypes) != 1 || len(m.TypeParams) != 0 || m.ReturnType == nil || m.ReturnType.String() != "bool" ||
			stripPackagePrefix(m.ParamTypes[0].BaseName()) != meta.Name {
			return false, false
		}
		return m.PointerReceiver, true
	}
	if sig, ok := t.goMethodsOnGalaType(meta)["Equal"]; ok {
		if len(sig.Params) != 1 || len(sig.Returns) != 1 || sig.Returns[0].String() != "bool" ||
			stripPackagePrefix(sig.Params[0].Type.BaseName()) != meta.Name {
			return false, false
		}
		return t.galaTypeGoMethods(meta).PointerMethods["Equal"], true
	}
	return false, !meta.IsOpaque
}

// declaresEqual reports whether meta's type declares an Equal method itself,
// in GALA or in a .go file of its package, rather than having it synthesized.
func (t *galaASTTransformer) declaresEqual(meta *transpiler.TypeMetadata) bool {
	if _, ok := meta.Methods["Equal"]; ok {
		return true
	}
	_, ok := t.goMethodsOnGalaType(meta)["Equal"]
	return ok
}

// walk is what == finds on typ, whose type-parameter names env binds. A nil
// env means typ is an expression's type (or one of its type arguments),
// where a bare name is a type parameter of the enclosing declaration.
func (c *comparer) walk(typ transpiler.Type, env map[string]typeArg, declared bool) typeEq {
	if transpiler.IsUnusable(typ) {
		return typeEq{}
	}
	typ, _ = transpiler.UnwrapSendable(typ)
	var args []transpiler.Type
	switch tt := typ.(type) {
	case transpiler.FuncType:
		return typeEq{bad: &incomparable{leaf: typ, kind: kindFunction}}
	case transpiler.MapType:
		return typeEq{bad: &incomparable{leaf: typ, kind: "a Go map"}}
	case transpiler.ArrayType:
		if declared {
			return typeEq{bad: &incomparable{leaf: typ, kind: "a Go slice"}}
		}
		return typeEq{}
	case transpiler.BasicType:
		if r, done := c.bareName(tt.Name, env); done {
			return r
		}
		if tt.IsAny() || tt.Name == "error" {
			return typeEq{} // an interface: Go compares its dynamic value
		}
		if isPrimitiveType(tt.Name) {
			return typeEq{native: true}
		}
	case transpiler.NamedType:
		if tt.Package == "" {
			if r, done := c.bareName(tt.Name, env); done {
				return r
			}
		}
	case transpiler.GenericType:
		args = tt.Params
	default:
		return typeEq{} // a pointer, a channel: Go compares their identity
	}
	if target, isAlias := c.t.unaliased(typ); isAlias {
		return c.walk(target, env, declared)
	}
	_, meta, goData, ok := c.t.receiverTypeInfo(typ)
	switch {
	case !ok:
		return typeEq{}
	case meta != nil && meta.DefinedIn != "":
		return c.declaration(typ, meta, args, env, declared)
	case goData != nil && goData.Incomparable:
		return typeEq{bad: &incomparable{leaf: typ, kind: kindGoIncomparable}}
	case goData != nil:
		return typeEq{native: goScalar(goData)}
	}
	return typeEq{}
}

// goScalar reports whether a Go type is a named primitive with no Equal
// method (time.Duration), whose == is std.Equal's. A Go struct is not: std.Equal
// compares it with its Equal method (time.Time) or field by field.
func goScalar(goData *transpiler.GoTypeData) bool {
	if goData.Kind != "named" || goData.Underlying == nil {
		return false
	}
	if _, ok := goData.Methods["Equal"]; ok {
		return false
	}
	basic, ok := goData.Underlying.(transpiler.BasicType)
	return ok && !basic.IsAny() && basic.Name != "error" && isPrimitiveType(basic.Name)
}

// bareName resolves a type name with no package: a type parameter env binds,
// or, in an expression's type, one of the enclosing declaration. done is false
// when name is neither, and names a type. A type parameter of the enclosing
// declaration may stand for any type, so Go's == on it is not known to be
// Equal's.
func (c *comparer) bareName(name string, env map[string]typeArg) (r typeEq, done bool) {
	if env != nil {
		arg, bound := env[name]
		if !bound {
			return typeEq{}, false
		}
		return c.walk(arg.typ, arg.env, arg.declared), true
	}
	if !c.t.activeTypeParams[name] {
		return typeEq{}, false
	}
	if c.t.typeParamConstraints[name] == "any" {
		return typeEq{bad: &incomparable{leaf: transpiler.NamedType{Name: name}, kind: kindUnconstrainedParam}}, true
	}
	return typeEq{}, true
}

// declaration walks the fields of a GALA struct, sealed type, opaque type or
// interface, meta, instantiated with args. Go's == on it is Equal's when its
// Equal is synthesized and every field's is; a field of the type itself is
// held through a pointer, which Go compares by identity.
func (c *comparer) declaration(typ transpiler.Type, meta *transpiler.TypeMetadata, args []transpiler.Type, env map[string]typeArg, declared bool) typeEq {
	if meta.IsInterface {
		return typeEq{}
	}
	key := typ.String()
	if c.visiting[key] {
		return typeEq{native: true} // the outer walk decides
	}
	if c.visiting == nil {
		c.visiting = map[string]bool{} // most comparisons never reach a declaration
	}
	c.visiting[key] = true
	defer delete(c.visiting, key)

	fieldEnv := make(map[string]typeArg, len(meta.TypeParams))
	for i, p := range meta.TypeParams {
		if i < len(args) {
			fieldEnv[p] = typeArg{typ: args[i], env: env, declared: declared}
		} else {
			fieldEnv[p] = typeArg{} // unbound: not known
		}
	}
	native := !c.t.declaresEqual(meta)
	if meta.IsOpaque {
		r := c.walk(meta.Underlying, fieldEnv, true)
		r.native = r.native && native
		return r
	}
	var bad *incomparable
	field := func(name, desc string, fieldType transpiler.Type) {
		r := c.walk(fieldType, fieldEnv, true)
		native = native && r.native
		if r.bad == nil || bad != nil {
			return
		}
		bad = r.bad
		// The outermost declaration sets these last: the message names the
		// first step from the compared type. A field another package keeps
		// to itself is no step its user can take, so the type is named
		// instead.
		bad.direct = bad.atLeaf()
		if !c.t.isLocalPackage(meta.Package) && !token.IsExported(name) {
			bad.via, bad.viaType, bad.holder = "", nil, typ
		} else {
			bad.via, bad.viaType, bad.holder = desc, c.t.substituteConcreteTypes(fieldType, meta.TypeParams, args), nil
		}
	}
	if meta.IsSealed {
		for _, v := range meta.SealedVariants {
			for i, name := range v.FieldNames {
				if i >= len(v.FieldTypes) || isSealedSelfField(v.FieldTypes[i], meta) {
					native = false // held through a pointer
					continue
				}
				field(name, fmt.Sprintf("field %s of case %s", name, v.Name), v.FieldTypes[i])
			}
		}
		return typeEq{native: native, bad: bad}
	}
	tuple := meta.Package == registry.StdPackageName && isTupleTypeName(meta.Name)
	for i, name := range meta.FieldNames {
		desc := "field " + name
		if tuple {
			desc = fmt.Sprintf("element %d", i+1)
		}
		field(name, desc, meta.Fields[name])
	}
	return typeEq{native: native, bad: bad}
}

// lowerEquality lowers `left == right` or `left != right`, cmp, by the
// equality of the left operand's type: to `left.Equal(right)` or
// `!left.Equal(right)` when that is not Go's ==, and rejects a comparison of
// a type that has neither (GALA-E0084). leftCtx and rightCtx are the operands
// as written; cmp's operands are read through their Immutable wrapper. A
// comparison with nil is left to the checks of nil.
func (t *galaASTTransformer) lowerEquality(leftCtx, rightCtx antlr.ParserRuleContext, cmp ast.Expr) (ast.Expr, error) {
	bin, ok := cmp.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) || isNilIdent(bin.X) || isNilIdent(bin.Y) {
		return cmp, nil
	}
	tok := bin.Op
	var lowered ast.Expr
	for i, operand := range []ast.Expr{bin.X, bin.Y} {
		typ := t.probeExprType(operand)
		eq := t.equalityOf(typ)
		switch {
		case eq.mode == eqMethod && i == 0 && !t.isInterfaceOperand(bin.Y):
			lowered = t.equalCallExpr(bin.X, bin.Y, eq.ptrReceiver, tok == token.NEQ)
		case eq.mode == eqIncomparable:
			return nil, t.incomparableOperandsError(leftCtx, rightCtx, tok, typ, eq)
		}
	}
	if lowered != nil {
		return lowered, nil
	}
	return cmp, nil
}

// isInterfaceOperand reports whether expr is of an interface type: Go's ==
// compares an interface with a value of a type that implements it.
func (t *galaASTTransformer) isInterfaceOperand(expr ast.Expr) bool {
	typ := t.probeExprType(expr)
	if transpiler.IsUnusable(typ) {
		return false
	}
	if typ.IsAny() || typ.String() == "error" {
		return true
	}
	_, meta, goData, ok := t.receiverTypeInfo(typ)
	return ok && ((meta != nil && meta.IsInterface) || (meta == nil && goData != nil && goData.Kind == "interface"))
}

// equalCallExpr is `x.Equal(y)`, negated when negate is set. An Equal with a
// pointer receiver is called on a copy of x, which Go may not address.
func (t *galaASTTransformer) equalCallExpr(x, y ast.Expr, ptrReceiver, negate bool) ast.Expr {
	if ptrReceiver {
		x = &ast.CallExpr{Fun: t.stdIdent("AddrOfCopy"), Args: []ast.Expr{x}}
	}
	var call ast.Expr = &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("Equal")}, Args: []ast.Expr{y}}
	if negate {
		call = &ast.UnaryExpr{Op: token.NOT, X: call}
	}
	return call
}

// incomparableOperandsError is GALA-E0084 for `left tok right`, whose operand
// of type typ has equality eq.
func (t *galaASTTransformer) incomparableOperandsError(leftCtx, rightCtx antlr.ParserRuleContext, tok token.Token, typ transpiler.Type, eq equality) error {
	r := eq.why
	l, rt := operandText(leftCtx), operandText(rightCtx)
	msg := fmt.Sprintf("%s cannot compare %s values: %s", tok, t.sourceTypeName(typ), t.incomparableReason(r))
	var hint string
	switch {
	case eq.ownEqual:
		msg = fmt.Sprintf("%s inside %s's own Equal is Go's ==, which cannot compare %s values: %s",
			tok, t.sourceTypeName(typ), t.sourceTypeName(typ), t.incomparableReason(r))
		hint = "compare the fields one by one: calling Equal here would call the method being defined"
	case r.kind == kindFunction:
		hint = functionHint(r)
	case r.atLeaf() && r.kind == kindUnconstrainedParam:
		name := t.sourceTypeName(r.leaf)
		hint = fmt.Sprintf("constrain %s with comparable where it is declared (`%s comparable`); or compare with `Equal(%s, %s)`", name, name, l, rt)
	default:
		hint = fmt.Sprintf("compare with Equal: `%s`", negated(fmt.Sprintf("Equal(%s, %s)", l, rt), tok == token.NEQ))
	}
	return spanError(galaerr.CodeIncomparableOperands, leftCtx, rightCtx, msg, hint)
}

// negated prefixes call with `!` when negate is set.
func negated(call string, negate bool) string {
	if negate {
		return "!" + call
	}
	return call
}

// functionHint is the hint for a comparison that reaches a function value,
// which Go compares with nothing but nil and Equal finds equal to nothing.
func functionHint(r *incomparable) string {
	if r.atLeaf() {
		return "a function value cannot be compared, only called: compare the values it returns"
	}
	return "a function value cannot be compared, only called: compare the other fields, or the values the function returns"
}

// lowerPatternEquality is the condition of a stable-identifier or other value
// pattern, which the arm compares with the subject as == does:
// `subject.Equal(pattern)` when the subject's type has an Equal that Go's ==
// is not, else `subject == pattern`. A type with neither is GALA-E0084.
// pattern is the lowered value, subject the lowered subject and subjectType
// its type, when known.
func (t *galaASTTransformer) lowerPatternEquality(ctx grammar.IExpressionContext, pattern, subject ast.Expr, subjectType transpiler.Type) (ast.Expr, error) {
	cond := &ast.BinaryExpr{X: subject, Op: token.EQL, Y: pattern}
	if isNilIdent(pattern) {
		return cond, nil
	}
	if transpiler.IsUnusable(subjectType) {
		subjectType = t.probeExprType(subject)
	}
	var lowered ast.Expr
	for i, typ := range []transpiler.Type{subjectType, t.probeExprType(pattern)} {
		eq := t.equalityOf(typ)
		switch {
		case eq.mode == eqMethod && i == 0 && !t.isInterfaceOperand(pattern):
			lowered = t.equalCallExpr(subject, pattern, eq.ptrReceiver, false)
		case eq.mode == eqIncomparable:
			r := eq.why
			name := transpiler.SourceText(ctx)
			hint := fmt.Sprintf("bind the value and compare it in a guard: `case v if Equal(v, %s) =>`", name)
			if r.kind == kindFunction {
				hint = functionHint(r)
			}
			return nil, spanError(galaerr.CodeIncomparableOperands, ctx, ctx,
				fmt.Sprintf("case %s compares the matched value with %s by ==, which cannot compare %s values: %s", name, name, t.sourceTypeName(typ), t.incomparableReason(r)),
				hint)
		}
	}
	if lowered != nil {
		return lowered, nil
	}
	return cond, nil
}

// operandText is the source of an operand of == as it reads in a call or
// receiver position: parenthesized unless it is a single postfix expression.
func operandText(ctx antlr.ParserRuleContext) string {
	text := transpiler.SourceText(ctx)
	if rel, ok := ctx.(*grammar.RelationalExprContext); ok && singlePostfix(rel) {
		return text
	}
	return "(" + text + ")"
}

// singlePostfix reports whether rel is one postfix expression, with no binary
// or unary operator.
func singlePostfix(rel *grammar.RelationalExprContext) bool {
	adds := rel.AllAdditiveExpr()
	if len(adds) != 1 {
		return false
	}
	muls := adds[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(muls) != 1 {
		return false
	}
	unaries := muls[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	return len(unaries) == 1 && unaries[0].(*grammar.UnaryExprContext).UnaryOp() == nil
}
