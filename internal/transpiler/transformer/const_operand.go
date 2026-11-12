package transformer

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"maps"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Untyped constants and the type they take.
//
// GALA follows Go's rules for untyped constants: a constant takes the type of
// the position it fills — the other operand of a binary operator, or the slot
// of an argument, a declaration or a field — and that type must hold it.
// There is no implicit conversion, so `x * 1.5` for an `int` x and
// `val s string = 3` are errors, as in Go. GALA-E0091 reports them rather than
// leaving `go build` to report "1.5 (untyped float constant) truncated to int"
// against the generated code.

// checkConstOperand rejects a binary operation tok whose left operand (written
// from leftFrom to leftTo: in a chain `1.5 + 1 + x` the left operand of the
// last `+` is `1.5 + 1`) or right operand (rightAt) is an untyped constant
// that the type of the other operand cannot hold (constMisfit): a constant
// with a fractional part for an integer (`x * 1.5`), one out of the type's
// range (`b + 300` for a `byte` b). bin is the lowered operation.
func (t *galaASTTransformer) checkConstOperand(tok token.Token, leftFrom, leftTo, rightAt srcNode, bin ast.Expr) error {
	e, ok := bin.(*ast.BinaryExpr)
	if !ok || !isNumericOperator(tok) {
		return nil
	}
	t.countOperandMismatch(e)
	from, to, lit, typed := rightAt, rightAt, e.Y, e.X
	if !isUntypedConstExpr(lit) {
		from, to, lit, typed = leftFrom, leftTo, e.X, e.Y
	}
	if !isUntypedConstExpr(lit) || t.isUntypedConstArg(typed) {
		return nil
	}
	typ := t.probeExprType(typed)
	why := t.constMisfit(lit, typ)
	if why == "" {
		return nil
	}
	return spanErrorAt(galaerr.CodeConstantMisfit, from.rng(), to.rng(),
		fmt.Sprintf("the constant %s cannot be %s, the type of the other operand of %s: %s", types.ExprString(lit), withArticle(displayType(typ)), tok, why),
		"convert the other operand to the type you mean (e.g. `float64(x)`); GALA converts no number implicitly")
}

// checkConstSlot rejects expr, the lowered value written at at that fills a
// slot of type typ, when it is an untyped constant typ cannot hold
// (constMisfit): `3` for a `string`, `1.5` for an `int`.
func (t *galaASTTransformer) checkConstSlot(expr ast.Expr, typ transpiler.Type, at srcNode) error {
	if !at.ok() {
		return nil
	}
	why := t.constMisfit(expr, typ)
	if why == "" {
		return nil
	}
	r := at.rng()
	return spanErrorAt(galaerr.CodeConstantMisfit, r, r,
		fmt.Sprintf("the constant %s cannot be %s, the type expected here: %s", at.sourceText(), withArticle(displayType(typ)), why),
		"write a constant of the expected type; GALA converts no constant implicitly")
}

// constMisfit says why typ cannot hold expr, an untyped constant written with
// literals: a number for a string or bool, a string for a number or bool, a
// fractional number for an integer, a number out of an integer type's range.
// "" when it can, when expr is no such constant (a Go package's named
// constant's value is not computed here), or when typ is not a scalar type
// (an interface, a type parameter) or is unknown.
func (t *galaASTTransformer) constMisfit(expr ast.Expr, typ transpiler.Type) string {
	value, isString := constValue(expr), isUntypedStringConst(expr)
	if (value == nil && !isString) || typ == nil || transpiler.IsUnusableOrAny(typ) {
		return ""
	}
	end, kind, via := t.resolveScalar(typ)
	if via != nil {
		if u, ok := t.underlyingOf(via); ok {
			end, kind, _ = t.resolveScalar(u)
		}
	}
	switch {
	case kind == scalarNone:
		return ""
	case isString && kind != scalarString:
		return "it is a string"
	case isString:
		return ""
	case kind == scalarString || kind == scalarBool:
		return "it is a number"
	case kind == scalarInt || kind == scalarUint:
		return intConstMisfit(value, end.String())
	}
	return ""
}

// countOperandMismatch counts, in operandMismatches, a binary operation whose
// two typed operands are numbers of different types (`acc + x` for an `int`
// acc and a `float64` x), which Go rejects. Only a probe lowering reads the
// count, to reject a candidate type (see lowerLambdaOverWeakConst), so
// nothing is counted outside one.
func (t *galaASTTransformer) countOperandMismatch(e *ast.BinaryExpr) {
	if t.mismatchProbes == 0 || t.isUntypedConstArg(e.X) || t.isUntypedConstArg(e.Y) {
		return
	}
	numeric := func(x ast.Expr) (string, bool) {
		end, kind, via := t.resolveScalar(t.probeExprType(x))
		if via != nil || (kind != scalarInt && kind != scalarUint && kind != scalarFloat) {
			return "", false
		}
		return end.String(), true
	}
	x, okX := numeric(e.X)
	y, okY := numeric(e.Y)
	if okX && okY && x != y {
		t.operandMismatches++
	}
}

// lowerLambdaOverWeakConst lowers, with lower, a lambda argument filling a
// parameter of type param, of a generic method call in which an untyped
// constant argument alone binds a type parameter (weak, to its default type;
// typeSubst holds the bindings lower sees). `FoldLeft(0, (acc, x) => acc + x)`
// over an `Array[float64]` binds U to the `0`'s `int`, and the lambda then
// adds an `int` to a `float64`, which Go rejects. The constant takes the
// type its position needs, as in Go: when the lambda lowered with the default
// type mixes number types, each number type the lambda's other parameters
// have (`float64`) that the constant converts to is tried instead, and the
// first under which the lambda mixes none is the binding. A constant in the
// lambda the default type cannot hold (GALA-E0091, `acc * 0.5`) is such a
// mix too. Go then infers the same type argument from the lambda (the
// constant argument binds last, see constantsLast). With none, the lambda is
// lowered with the default type, and GALA-E0091 or Go reports the operation.
func (t *galaASTTransformer) lowerLambdaOverWeakConst(param transpiler.Type, weak map[string]transpiler.Type, typeSubst typeSubstMap, lower func() (ast.Expr, error)) (ast.Expr, error) {
	ft := t.resolveTranspilerTypeAsFuncType(param)
	var tps []string
	for _, tp := range slices.Sorted(maps.Keys(weak)) {
		if ft != nil && typeMentionsTypeParam(*ft, []string{tp}) {
			tps = append(tps, tp)
		}
	}
	if len(tps) == 0 {
		return lower()
	}
	t.mismatchProbes++
	defer func() { t.mismatchProbes-- }()
	// Each lowering starts from the same state: the mismatches a discarded
	// one counted, and the editor hints it recorded, are dropped.
	start, hints := t.operandMismatches, len(t.lspLambdaParamHints)
	try := func() (ast.Expr, bool, error) {
		t.operandMismatches = start
		t.lspLambdaParamHints = t.lspLambdaParamHints[:hints]
		expr, err := lower()
		if isConstMisfit(err) {
			return nil, false, err
		}
		return expr, err == nil && t.operandMismatches == start, err
	}
	expr, clean, err := try()
	if clean || (err != nil && !isConstMisfit(err)) {
		return expr, err
	}
	for _, tp := range tps {
		def := weak[tp]
		for _, candidate := range t.weakConstCandidates(*ft, tps, def) {
			typeSubst[tp] = candidate
			if probe, ok, _ := try(); ok {
				delete(weak, tp)
				t.operandMismatches = start
				return probe, nil
			}
		}
		typeSubst[tp] = def
	}
	expr, _, err = try()
	return expr, err
}

// isConstMisfit reports whether err is GALA-E0091.
func isConstMisfit(err error) bool {
	var se *galaerr.SemanticError
	return errors.As(err, &se) && se.Code == galaerr.CodeConstantMisfit
}

// weakConstCandidates lists the number types among the parameters of ft that
// name none of tps, in order, that a constant of default type def converts
// to: any number type for an integer constant, a float type for a float one.
func (t *galaASTTransformer) weakConstCandidates(ft transpiler.FuncType, tps []string, def transpiler.Type) []transpiler.Type {
	_, defKind, _ := t.resolveScalar(def)
	var out []transpiler.Type
	for _, p := range ft.Params {
		if typeMentionsTypeParam(p, tps) || p.String() == def.String() || slices.ContainsFunc(out, func(o transpiler.Type) bool { return o.String() == p.String() }) {
			continue
		}
		_, kind, via := t.resolveScalar(p)
		switch {
		case via != nil:
		case kind == scalarFloat, (kind == scalarInt || kind == scalarUint) && defKind != scalarFloat:
			out = append(out, p)
		}
	}
	return out
}

// isNumericOperator reports whether tok is an arithmetic or ordering
// operator, whose untyped constant operand takes its type from the other.
func isNumericOperator(tok token.Token) bool {
	switch tok {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
		token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return true
	}
	return false
}

// constValue is the value of expr, an untyped numeric constant built from
// literals (see untypedNumericConstDefault), or nil when it is not one.
func constValue(expr ast.Expr) constant.Value {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT, token.FLOAT, token.CHAR:
			if v := constant.MakeFromLiteral(e.Value, e.Kind, 0); v.Kind() != constant.Unknown {
				return v
			}
		}
	case *ast.ParenExpr:
		return constValue(e.X)
	case *ast.UnaryExpr:
		if x := constValue(e.X); x != nil && (e.Op == token.ADD || e.Op == token.SUB) {
			return constant.UnaryOp(e.Op, x, 0)
		}
	case *ast.BinaryExpr:
		x, y := constValue(e.X), constValue(e.Y)
		if x == nil || y == nil {
			return nil
		}
		switch e.Op {
		case token.ADD, token.SUB, token.MUL:
			return constant.BinaryOp(x, e.Op, y)
		case token.QUO:
			if constant.Sign(y) == 0 {
				return nil
			}
			if x.Kind() == constant.Int && y.Kind() == constant.Int {
				return constant.BinaryOp(x, token.QUO_ASSIGN, y)
			}
			return constant.BinaryOp(x, e.Op, y)
		}
	}
	return nil
}

// intConstMisfit says why the integer type name cannot hold the constant v,
// as Go requires of an untyped constant; "" when it can.
func intConstMisfit(v constant.Value, name string) string {
	i := constant.ToInt(v)
	if i.Kind() != constant.Int {
		return "it is not a whole number"
	}
	lo, hi, ok := intRange(name)
	if ok && (constant.Compare(i, token.LSS, lo) || constant.Compare(i, token.GTR, hi)) {
		return "it is out of the range of " + name
	}
	return ""
}

// intRange is the range of the predeclared integer type name, with int and
// uint 64 bits wide.
func intRange(name string) (lo, hi constant.Value, ok bool) {
	signed := func(bits uint) (constant.Value, constant.Value, bool) {
		one := constant.MakeInt64(1)
		hi := constant.BinaryOp(constant.Shift(one, token.SHL, bits-1), token.SUB, one)
		return constant.UnaryOp(token.SUB, constant.Shift(one, token.SHL, bits-1), 0), hi, true
	}
	unsigned := func(bits uint) (constant.Value, constant.Value, bool) {
		one := constant.MakeInt64(1)
		return constant.MakeInt64(0), constant.BinaryOp(constant.Shift(one, token.SHL, bits), token.SUB, one), true
	}
	switch name {
	case "int8":
		return signed(8)
	case "int16":
		return signed(16)
	case "int32", "rune":
		return signed(32)
	case "int", "int64":
		return signed(64)
	case "uint8", "byte":
		return unsigned(8)
	case "uint16":
		return unsigned(16)
	case "uint32":
		return unsigned(32)
	case "uint", "uint64", "uintptr":
		return unsigned(64)
	}
	return nil, nil, false
}
