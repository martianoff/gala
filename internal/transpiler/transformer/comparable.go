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

// equality is how == compares values of a type: mode, and for
// eqIncomparable why Go cannot compare them and whether that is inside the
// type's own Equal.
type equality struct {
	mode     eqMode
	why      *incomparable
	ownEqual bool
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
// Equal's answer (native), why Go cannot compare it at all (bad; nil when it
// can or that is not known), a function a synthesized Equal would compare
// (fn: such an Equal finds a function equal to nothing, not even itself), and
// whether it holds the type whose Equal is being transformed (holdsOwner).
type typeEq struct {
	native     bool
	bad        *incomparable
	fn         *incomparable
	holdsOwner bool
}

// comparer walks a type for how == compares it. visiting holds the
// declarations being walked, so a type that holds itself through another is
// read once. exhaustive walks every field, as finding the owner needs.
type comparer struct {
	t          *galaASTTransformer
	visiting   map[string]bool
	exhaustive bool
}

// equalityOf is how == compares two values of typ, the type of an expression.
// A type that declares a usable Equal compares with it without a walk; one
// whose Equal is synthesized is walked to see whether Go's == gives the same
// answer, and whether that Equal would compare a function, which it finds
// equal to nothing: such a type is GALA-E0084, as before.
//
// Inside a type's own Equal, == on that type, or on a type that holds it by
// value (Option[T], a tuple, a List of T), is Go's: its Equal would call the
// method being defined.
func (t *galaASTTransformer) equalityOf(typ transpiler.Type) equality {
	if transpiler.IsUnusable(typ) {
		return equality{mode: eqGo}
	}
	meta, isDecl := t.equalDeclaration(typ)
	declared, usable := false, false
	if isDecl {
		declared, usable = t.equalMethodOf(meta)
	}
	inEqual := t.equalOwner != nil
	if declared && usable && !inEqual {
		return equality{mode: eqMethod}
	}
	c := comparer{t: t, exhaustive: inEqual}
	w := c.walk(typ, nil, false)
	switch {
	case w.native:
		return equality{mode: eqGo}
	case w.holdsOwner && w.bad != nil:
		return equality{mode: eqIncomparable, why: w.bad, ownEqual: true}
	case w.holdsOwner:
		return equality{mode: eqGo}
	case usable && (declared || w.fn == nil):
		return equality{mode: eqMethod}
	case w.fn != nil:
		return equality{mode: eqIncomparable, why: w.fn}
	case w.bad != nil:
		return equality{mode: eqIncomparable, why: w.bad}
	}
	return equality{mode: eqGo}
}

// goIncomparable reports why Go cannot compare values of typ, or nil when it
// can or that is not known.
func (t *galaASTTransformer) goIncomparable(typ transpiler.Type) *incomparable {
	c := comparer{t: t}
	return c.walk(typ, nil, false).bad
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
	return o != nil && o.Name == meta.Name && o.Package == meta.Package
}

// equalMethodOf reports whether the declaration meta declares an Equal method
// itself, in GALA or in a .go file of its package, and whether it has one ==
// can call, `Equal(other T) bool`. A struct or sealed type that declares none
// has a synthesized one; an opaque type has one only when it declares it.
func (t *galaASTTransformer) equalMethodOf(meta *transpiler.TypeMetadata) (declared, usable bool) {
	if m, ok := meta.Methods["Equal"]; ok {
		return true, len(m.ParamTypes) == 1 && len(m.TypeParams) == 0 && m.ReturnType != nil &&
			m.ReturnType.String() == "bool" && stripPackagePrefix(m.ParamTypes[0].BaseName()) == meta.Name
	}
	if sig, ok := t.goMethodsOnGalaType(meta)["Equal"]; ok {
		return true, len(sig.Params) == 1 && len(sig.Returns) == 1 && sig.Returns[0].String() == "bool" &&
			stripPackagePrefix(sig.Params[0].Type.BaseName()) == meta.Name
	}
	return false, !meta.IsOpaque
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
		r := &incomparable{leaf: typ, kind: kindFunction}
		return typeEq{bad: r, fn: r}
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
	declaresEqual, _ := c.t.equalMethodOf(meta)
	res := typeEq{native: !declaresEqual, holdsOwner: c.t.isEqualOwner(meta)}
	if meta.IsOpaque {
		r := c.walk(meta.Underlying, fieldEnv, true)
		r.native = r.native && res.native
		r.holdsOwner = r.holdsOwner || res.holdsOwner
		return r
	}
	// step records the first step from typ down to r's leaf, through the field
	// name described by desc: the outermost declaration sets it last, so the
	// message names the first step from the compared type. A field another
	// package keeps to itself is no step its user can take, so the type is
	// named instead.
	step := func(r *incomparable, name, desc string, fieldType transpiler.Type) {
		r.direct = r.atLeaf()
		if !c.t.isLocalPackage(meta.Package) && !token.IsExported(name) {
			r.via, r.viaType, r.holder = "", nil, typ
		} else {
			r.via, r.viaType, r.holder = desc, c.t.substituteConcreteTypes(fieldType, meta.TypeParams, args), nil
		}
	}
	// field walks one field; it reports false once no answer can change. A
	// function inside a type with its own Equal is that Equal's concern.
	field := func(name, desc string, fieldType transpiler.Type) bool {
		r := c.walk(fieldType, fieldEnv, true)
		res.native = res.native && r.native
		res.holdsOwner = res.holdsOwner || r.holdsOwner
		if r.bad != nil && res.bad == nil {
			res.bad = r.bad
			step(res.bad, name, desc, fieldType)
		}
		if r.fn != nil && res.fn == nil && !declaresEqual {
			res.fn = r.fn
			if res.fn != res.bad {
				step(res.fn, name, desc, fieldType)
			}
		}
		return c.exhaustive || res.native || res.bad == nil || (res.fn == nil && !declaresEqual)
	}
	if meta.IsSealed {
		for _, v := range meta.SealedVariants {
			for i, name := range v.FieldNames {
				if i >= len(v.FieldTypes) || isSealedSelfField(v.FieldTypes[i], meta) {
					res.native = false // held through a pointer
					continue
				}
				if !field(name, fmt.Sprintf("field %s of case %s", name, v.Name), v.FieldTypes[i]) {
					return res
				}
			}
		}
		return res
	}
	tuple := meta.Package == registry.StdPackageName && isTupleTypeName(meta.Name)
	for i, name := range meta.FieldNames {
		desc := "field " + name
		if tuple {
			desc = fmt.Sprintf("element %d", i+1)
		}
		if !field(name, desc, meta.Fields[name]) {
			return res
		}
	}
	return res
}

// operandsEquality is how `x == y` compares, given the operands' types: by
// x's Equal when x's type has one that Go's == is not, else by y's Equal when
// x is a literal (swapped: `y.Equal(x)` evaluates the same operands), else
// with Go's ==. Go's == compares an interface with a value of a type that
// implements it, so a comparison with an interface operand is Go's. An
// operand of a type that has neither Equal nor Go's == is eqIncomparable,
// and badType is its type.
func (t *galaASTTransformer) operandsEquality(xType, yType transpiler.Type, xLiteral bool) (eq equality, badType transpiler.Type, swapped bool) {
	ex := t.equalityOf(xType)
	if ex.mode == eqIncomparable {
		return ex, xType, false
	}
	ey := ex
	if transpiler.IsUnusable(xType) || transpiler.IsUnusable(yType) || xType.String() != yType.String() {
		ey = t.equalityOf(yType)
	}
	if ey.mode == eqIncomparable {
		return ey, yType, false
	}
	for _, side := range []struct {
		eq                equality
		typ, other        transpiler.Type
		swapped, eligible bool
	}{{ex, xType, yType, false, true}, {ey, yType, xType, true, xLiteral}} {
		if side.eq.mode != eqMethod || !side.eligible {
			continue
		}
		if !t.isInterfaceType(side.other) {
			return side.eq, nil, side.swapped
		}
		if r := t.goIncomparable(side.typ); r != nil {
			return equality{mode: eqIncomparable, why: r}, side.typ, false
		}
	}
	return equality{mode: eqGo}, nil, false
}

// isLiteral reports whether expr is a literal, optionally negated: an operand
// whose evaluation order does not matter.
func isLiteral(expr ast.Expr) bool {
	if u, ok := expr.(*ast.UnaryExpr); ok && (u.Op == token.SUB || u.Op == token.ADD) {
		expr = u.X
	}
	_, ok := expr.(*ast.BasicLit)
	return ok
}

// lowerEquality lowers `left == right` or `left != right`, cmp, to
// `left.Equal(right)` or `!left.Equal(right)` when that is how its operands
// compare (operandsEquality), and rejects a comparison of a type that has
// neither Equal nor Go's == (GALA-E0084). leftCtx and rightCtx are the
// operands as written; cmp's operands are read through their Immutable
// wrapper. A comparison with nil is left to the checks of nil.
func (t *galaASTTransformer) lowerEquality(leftCtx, rightCtx antlr.ParserRuleContext, cmp ast.Expr) (ast.Expr, error) {
	bin, ok := cmp.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) || isNilIdent(bin.X) || isNilIdent(bin.Y) {
		return cmp, nil
	}
	xType, yType := t.probeExprType(bin.X), t.probeExprType(bin.Y)
	eq, badType, swapped := t.operandsEquality(xType, yType, isLiteral(bin.X))
	switch {
	case eq.mode == eqIncomparable:
		return nil, t.incomparableOperandsError(leftCtx, rightCtx, bin.Op, badType, eq)
	case eq.mode == eqMethod && swapped:
		return t.equalCallExpr(bin.Y, yType, bin.X, bin.Op == token.NEQ, rightCtx)
	case eq.mode == eqMethod:
		return t.equalCallExpr(bin.X, xType, bin.Y, bin.Op == token.NEQ, leftCtx)
	}
	return cmp, nil
}

// equalCallExpr is `x.Equal(y)`, negated when negate is set. An Equal with a
// pointer receiver is called on x's address, or on a copy when Go cannot
// address x (addressableReceiver); pos locates x.
func (t *galaASTTransformer) equalCallExpr(x ast.Expr, xType transpiler.Type, y ast.Expr, negate bool, pos antlr.ParserRuleContext) (ast.Expr, error) {
	recv, err := t.addressableReceiver(x, xType, "Equal", pos.GetStart().GetLine(), pos.GetStart().GetColumn())
	if err != nil {
		return nil, err
	}
	var call ast.Expr = &ast.CallExpr{Fun: &ast.SelectorExpr{X: recv, Sel: ast.NewIdent("Equal")}, Args: []ast.Expr{y}}
	if negate {
		call = &ast.UnaryExpr{Op: token.NOT, X: call}
	}
	return call, nil
}

// incomparableOperandsError is GALA-E0084 for `left tok right`, whose operand
// of type typ has equality eq.
func (t *galaASTTransformer) incomparableOperandsError(leftCtx, rightCtx antlr.ParserRuleContext, tok token.Token, typ transpiler.Type, eq equality) error {
	r := eq.why
	name, reason := t.sourceTypeName(typ), t.incomparableReason(r)
	l, rt := operandText(leftCtx), operandText(rightCtx)
	msg := fmt.Sprintf("%s cannot compare %s values: %s", tok, name, reason)
	var hint string
	switch {
	case eq.ownEqual:
		msg = fmt.Sprintf("%s inside %s's own Equal is Go's ==, which cannot compare %s values: %s", tok, t.equalOwner.Name, name, reason)
		hint = "compare the fields one by one: calling Equal here would call the method being defined"
	case r.kind == kindFunction:
		hint = functionHint(r)
	case r.atLeaf() && r.kind == kindUnconstrainedParam:
		param := t.sourceTypeName(r.leaf)
		hint = fmt.Sprintf("constrain %s with comparable where it is declared (`%s comparable`); or compare with `Equal(%s, %s)`", param, param, l, rt)
	case tok == token.NEQ:
		hint = fmt.Sprintf("compare with Equal: `!Equal(%s, %s)`", l, rt)
	default:
		hint = fmt.Sprintf("compare with Equal: `Equal(%s, %s)`", l, rt)
	}
	return spanError(galaerr.CodeIncomparableOperands, leftCtx, rightCtx, msg, hint)
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
// pattern, which the arm compares with the subject as == does
// (operandsEquality): `subject.Equal(pattern)` or `subject == pattern`. A
// type with neither Equal nor Go's == is GALA-E0084. pattern is the lowered
// value, subject the lowered subject and subjectType its type, when known.
func (t *galaASTTransformer) lowerPatternEquality(ctx grammar.IExpressionContext, pattern, subject ast.Expr, subjectType transpiler.Type) (ast.Expr, error) {
	cond := &ast.BinaryExpr{X: subject, Op: token.EQL, Y: pattern}
	if isNilIdent(pattern) {
		return cond, nil
	}
	if transpiler.IsUnusable(subjectType) {
		subjectType = t.probeExprType(subject)
	}
	eq, badType, _ := t.operandsEquality(subjectType, t.probeExprType(pattern), false)
	switch eq.mode {
	case eqIncomparable:
		r := eq.why
		name := transpiler.SourceText(ctx)
		hint := fmt.Sprintf("bind the value and compare it in a guard: `case v if Equal(v, %s) =>`", name)
		if r.kind == kindFunction {
			hint = functionHint(r)
		}
		return nil, spanError(galaerr.CodeIncomparableOperands, ctx, ctx,
			fmt.Sprintf("case %s compares the matched value with %s by ==, which cannot compare %s values: %s", name, name, t.sourceTypeName(badType), t.incomparableReason(r)),
			hint)
	case eqMethod:
		return t.equalCallExpr(subject, subjectType, pattern, false, ctx)
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
