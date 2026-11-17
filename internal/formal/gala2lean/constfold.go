package gala2lean

import (
	"go/constant"
	"go/token"
	"strconv"

	"martianoff/gala/internal/formal/coreexport"
)

// Constant expressions. An operator tree whose leaves are all literals is a
// Go untyped constant once transpiled: the transformer writes it as such,
// and Go evaluates it exactly, on unbounded integers, before it takes a
// type (#1237). So `(1 << 70) >> 68` is 4, where int64 operations would
// give 0. Each maximal such tree is evaluated here with Go's own constant
// arithmetic (go/constant, as Go's type checker does) and written as its
// value: an int that fits int64, a bool or a string. What Go rejects is
// reported (untyped-constant), never wrapped: an int result outside int64,
// which does not compile as an int, and an operation Go refuses on
// constants (a division by zero, a negative shift count, operands of
// different kinds). A name is never a constant: a GALA `val` lowers to a Go
// variable.

// shiftBound is the largest constant shift count Go's type checker accepts.
const shiftBound = 1023 - 1 + 52

var (
	constUnaryOps = map[string]token.Token{"-": token.SUB, "+": token.ADD, "^": token.XOR, "!": token.NOT}
	constCmpOps   = map[string]token.Token{"==": token.EQL, "!=": token.NEQ, "<": token.LSS,
		"<=": token.LEQ, ">": token.GTR, ">=": token.GEQ}
	// QUO_ASSIGN is integer (truncated) division, as for Go's int constants.
	constIntOps = map[string]token.Token{"+": token.ADD, "-": token.SUB, "*": token.MUL,
		"/": token.QUO_ASSIGN, "%": token.REM, "&": token.AND, "|": token.OR, "^": token.XOR,
		"&^": token.AND_NOT, "<<": token.SHL, ">>": token.SHR}
)

// constTree reports whether e is an operator tree, possibly parenthesized:
// a unary or binary operator, not a literal alone.
func constTree(e coreexport.Node) bool {
	switch e := e.(type) {
	case *coreexport.Unary, *coreexport.Binary:
		return true
	case *coreexport.Paren:
		return constTree(e.Expr)
	}
	return false
}

// goConstValue is the value of e as Go's type checker computes it, when e
// is built of literals and operators only (ok). An operation Go rejects on
// such operands does not compile in Go, and is reported.
func goConstValue(e coreexport.Node) (constant.Value, bool) {
	switch e := e.(type) {
	case *coreexport.Int:
		v := constant.MakeFromLiteral(e.Text, token.INT, 0)
		if v.Kind() != constant.Int {
			unsup(e.Pos, "int-literal")
		}
		return v, true
	case *coreexport.Bool:
		return constant.MakeBool(e.Value), true
	case *coreexport.String:
		return constant.MakeString(e.Value), true
	case *coreexport.Paren:
		return goConstValue(e.Expr)
	case *coreexport.Unary:
		x, ok := goConstValue(e.Operand)
		if !ok {
			return nil, false
		}
		return checkedConst(unaryConst(e.Op, x), e), true
	case *coreexport.Binary:
		x, ok := goConstValue(e.Left)
		if !ok {
			return nil, false
		}
		y, ok := goConstValue(e.Right)
		if !ok {
			return nil, false
		}
		return checkedConst(binaryConst(e.Op, x, y), e), true
	}
	return nil, false
}

// checkedConst is v, the value of operator e, reporting e when Go rejects it
// (v nil).
func checkedConst(v constant.Value, e coreexport.Node) constant.Value {
	if v == nil {
		unsup(posOf(e), "untyped-constant")
	}
	return v
}

// unaryConst is `op x` as Go computes it, nil when Go rejects it.
func unaryConst(op string, x constant.Value) constant.Value {
	tok, ok := constUnaryOps[op]
	if !ok || (x.Kind() == constant.Bool) != (op == "!") || x.Kind() == constant.String {
		return nil
	}
	return constant.UnaryOp(tok, x, 0)
}

// binaryConst is `x op y` as Go computes it, nil when Go rejects it.
func binaryConst(op string, x, y constant.Value) constant.Value {
	if x.Kind() != y.Kind() {
		return nil
	}
	if tok, ok := constCmpOps[op]; ok {
		if x.Kind() == constant.Bool && op != "==" && op != "!=" {
			return nil
		}
		return constant.MakeBool(constant.Compare(x, tok, y))
	}
	switch x.Kind() {
	case constant.Bool:
		switch op {
		case "&&":
			return constant.BinaryOp(x, token.LAND, y)
		case "||":
			return constant.BinaryOp(x, token.LOR, y)
		}
		return nil
	case constant.String:
		if op != "+" {
			return nil
		}
		return constant.BinaryOp(x, token.ADD, y)
	}
	tok, ok := constIntOps[op]
	switch {
	case !ok:
		return nil
	case tok == token.SHL || tok == token.SHR:
		n, exact := constant.Uint64Val(y)
		if !exact || n > shiftBound {
			return nil
		}
		return constant.Shift(x, tok, uint(n))
	case (tok == token.QUO_ASSIGN || tok == token.REM) && constant.Sign(y) == 0:
		return nil
	}
	return constant.BinaryOp(x, tok, y)
}

// constTerm is e written as its value, when e is a constant operator tree
// (ok).
func constTerm(e coreexport.Node) (term, bool) {
	if !constTree(e) {
		return term{}, false
	}
	v, ok := goConstValue(e)
	if !ok {
		return term{}, false
	}
	switch v.Kind() {
	case constant.Int:
		n, exact := constant.Int64Val(v)
		if !exact {
			unsup(posOf(e), "untyped-constant")
		}
		return term{code: "(" + strconv.FormatInt(n, 10) + " : Int64)", pure: true}, true
	case constant.Bool:
		return term{code: strconv.FormatBool(constant.BoolVal(v)), pure: true}, true
	}
	return term{code: strLit(constant.StringVal(v), posOf(e)), pure: true}, true
}
