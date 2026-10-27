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

// Comparing values Go cannot compare
//
// `==` and `!=` are Go's: they lower to Go's operator unchanged, and so does
// the comparison of a stable-identifier pattern. Go compares only comparable
// types, and a struct, sealed type, tuple or collection holding a slice, a map
// or a function is not one; nor is a type parameter constrained by `any`.
// Left to Go, such a comparison failed against the generated code, naming the
// `std.Immutable` wrapper of a `val` field. GALA-E0084 reports it in GALA's
// terms, at the comparison, when an operand's type is known not to be
// comparable. A type that is not known is left to Go: the check never rejects
// a comparison Go would compile.

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

// comparer walks a type for something Go cannot compare. visiting holds the
// declarations being walked, so a type that holds itself through another is
// read once.
type comparer struct {
	t        *galaASTTransformer
	visiting map[string]bool
}

// incomparableType reports why Go cannot compare values of typ, the type of
// an expression, or nil when it can or when that is not known.
func (t *galaASTTransformer) incomparableType(typ transpiler.Type) *incomparable {
	c := comparer{t: t}
	return c.check(typ, nil, false)
}

// check is incomparableType for typ, whose type-parameter names env binds. A
// nil env means typ is an expression's type (or one of its type arguments),
// where a bare name is a type parameter of the enclosing declaration.
func (c *comparer) check(typ transpiler.Type, env map[string]typeArg, declared bool) *incomparable {
	if transpiler.IsUnusable(typ) {
		return nil
	}
	typ, _ = transpiler.UnwrapSendable(typ)
	var args []transpiler.Type
	switch tt := typ.(type) {
	case transpiler.FuncType:
		return &incomparable{leaf: typ, kind: kindFunction}
	case transpiler.MapType:
		return &incomparable{leaf: typ, kind: "a Go map"}
	case transpiler.ArrayType:
		if declared {
			return &incomparable{leaf: typ, kind: "a Go slice"}
		}
		return nil
	case transpiler.BasicType:
		if r, done := c.bareName(tt.Name, env); done {
			return r
		}
		if isPrimitiveType(tt.Name) || tt.IsAny() {
			return nil
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
		return nil
	}
	if target, isAlias := c.t.unaliased(typ); isAlias {
		return c.check(target, env, declared)
	}
	_, meta, goData, ok := c.t.receiverTypeInfo(typ)
	switch {
	case !ok:
		return nil
	case meta != nil && meta.DefinedIn != "":
		return c.declaration(typ, meta, args, env, declared)
	case goData != nil && goData.Incomparable:
		return &incomparable{leaf: typ, kind: kindGoIncomparable}
	}
	return nil
}

// bareName resolves a type name with no package: a type parameter env binds,
// or, in an expression's type, one of the enclosing declaration. done is false
// when name is neither, and names a type.
func (c *comparer) bareName(name string, env map[string]typeArg) (r *incomparable, done bool) {
	if env != nil {
		arg, bound := env[name]
		if !bound {
			return nil, false
		}
		return c.check(arg.typ, arg.env, arg.declared), true
	}
	if !c.t.activeTypeParams[name] {
		return nil, false
	}
	if c.t.typeParamConstraints[name] == "any" {
		return &incomparable{leaf: transpiler.NamedType{Name: name}, kind: kindUnconstrainedParam}, true
	}
	return nil, true
}

// declaration walks the fields of a GALA struct, sealed type, opaque type or
// interface, meta, instantiated with args.
func (c *comparer) declaration(typ transpiler.Type, meta *transpiler.TypeMetadata, args []transpiler.Type, env map[string]typeArg, declared bool) *incomparable {
	if meta.IsInterface {
		return nil
	}
	key := typ.String()
	if c.visiting[key] {
		return nil
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
	if meta.IsOpaque {
		return c.check(meta.Underlying, fieldEnv, true)
	}
	field := func(name, desc string, fieldType transpiler.Type) *incomparable {
		r := c.check(fieldType, fieldEnv, true)
		if r == nil {
			return nil
		}
		// The outermost declaration sets these last: the message names the
		// first step from the compared type. A field another package keeps
		// to itself is no step its user can take, so the type is named
		// instead.
		r.direct = r.atLeaf()
		if !c.t.isLocalPackage(meta.Package) && !token.IsExported(name) {
			r.via, r.viaType, r.holder = "", nil, typ
		} else {
			r.via, r.viaType, r.holder = desc, c.t.substituteConcreteTypes(fieldType, meta.TypeParams, args), nil
		}
		return r
	}
	if meta.IsSealed {
		for _, v := range meta.SealedVariants {
			for i, name := range v.FieldNames {
				if i >= len(v.FieldTypes) || isSealedSelfField(v.FieldTypes[i], meta) {
					continue // held through a pointer
				}
				if r := field(name, fmt.Sprintf("field %s of case %s", name, v.Name), v.FieldTypes[i]); r != nil {
					return r
				}
			}
		}
		return nil
	}
	tuple := meta.Package == registry.StdPackageName && isTupleTypeName(meta.Name)
	for i, name := range meta.FieldNames {
		desc := "field " + name
		if tuple {
			desc = fmt.Sprintf("element %d", i+1)
		}
		if r := field(name, desc, meta.Fields[name]); r != nil {
			return r
		}
	}
	return nil
}

// checkComparableOperands rejects `left == right` or `left != right` when an
// operand's type is known not to be comparable (GALA-E0084). leftCtx and
// rightCtx are the operands as written, and cmp the lowered comparison, whose
// operands are read through their Immutable wrapper; a comparison with nil is
// left to the checks of nil.
func (t *galaASTTransformer) checkComparableOperands(leftCtx, rightCtx antlr.ParserRuleContext, cmp ast.Expr) error {
	bin, ok := cmp.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) || isNilIdent(bin.X) || isNilIdent(bin.Y) {
		return nil
	}
	tok := bin.Op
	for _, operand := range []ast.Expr{bin.X, bin.Y} {
		typ := t.probeExprType(operand)
		r := t.incomparableType(typ)
		if r == nil {
			continue
		}
		l, rt := operandText(leftCtx), operandText(rightCtx)
		var hint string
		switch {
		case r.kind == kindFunction:
			hint = functionHint(r)
		case r.atLeaf() && r.kind == kindUnconstrainedParam:
			name := t.sourceTypeName(r.leaf)
			hint = fmt.Sprintf("constrain %s with comparable where it is declared (`%s comparable`); or compare with `Equal(%s, %s)`", name, name, l, rt)
		default:
			hint = fmt.Sprintf("compare with Equal: `%s`", t.equalCall(typ, l, rt, tok == token.NEQ))
		}
		return spanError(galaerr.CodeIncomparableOperands, leftCtx, rightCtx,
			fmt.Sprintf("%s cannot compare %s values: %s", tok, t.sourceTypeName(typ), t.incomparableReason(r)), hint)
	}
	return nil
}

// functionHint is the hint for a comparison that reaches a function value,
// which Go compares with nothing but nil and Equal finds equal to nothing.
func functionHint(r *incomparable) string {
	if r.atLeaf() {
		return "a function value cannot be compared, only called: compare the values it returns"
	}
	return "a function value cannot be compared, only called: compare the other fields, or the values the function returns"
}

// checkComparablePattern rejects a stable-identifier or other value pattern,
// which the arm compares with the subject by ==, when either's type is known
// not to be comparable (GALA-E0084). pattern is the lowered value, subject the
// lowered subject and subjectType its type, when known.
func (t *galaASTTransformer) checkComparablePattern(ctx grammar.IExpressionContext, pattern, subject ast.Expr, subjectType transpiler.Type) error {
	if isNilIdent(pattern) {
		return nil
	}
	if transpiler.IsUnusable(subjectType) {
		subjectType = t.probeExprType(subject)
	}
	for _, typ := range []transpiler.Type{t.probeExprType(pattern), subjectType} {
		r := t.incomparableType(typ)
		if r == nil {
			continue
		}
		name := transpiler.SourceText(ctx)
		hint := fmt.Sprintf("bind the value and compare it in a guard: `case v if %s =>`", t.equalCall(typ, "v", name, false))
		if r.kind == kindFunction {
			hint = functionHint(r)
		}
		return spanError(galaerr.CodeIncomparableOperands, ctx, ctx,
			fmt.Sprintf("case %s compares the matched value with %s by ==, which cannot compare %s values: %s", name, name, t.sourceTypeName(typ), t.incomparableReason(r)),
			hint)
	}
	return nil
}

// equalCall is the GALA expression that compares left and right of type typ
// structurally: their Equal method, which every GALA struct, sealed type and
// tuple has, or std's Equal (an opaque type has no Equal method). negate
// prefixes `!`.
func (t *galaASTTransformer) equalCall(typ transpiler.Type, left, right string, negate bool) string {
	call := fmt.Sprintf("Equal(%s, %s)", left, right)
	if _, meta, _, ok := t.receiverTypeInfo(typ); ok && meta != nil && meta.DefinedIn != "" && !meta.IsInterface && !meta.IsOpaque {
		call = fmt.Sprintf("%s.Equal(%s)", left, right)
	}
	if negate {
		return "!" + call
	}
	return call
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
