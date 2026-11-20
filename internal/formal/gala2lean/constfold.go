package gala2lean

import (
	"go/constant"
	"go/token"
	"strconv"

	"martianoff/gala/internal/formal/coreexport"
)

// Constant expressions. An operator tree whose leaves are all literals is a
// Go untyped constant once transpiled: the transformer writes it as such,
// and Go evaluates it exactly before it takes a type (#1237). So
// `(1 << 70) >> 68` is 4, where int64 operations would give 0. Each maximal
// such tree is evaluated here as Go's type checker evaluates it: with
// go/constant, under the checker's limits, and written as its value: an int
// that fits int64, a bool or a string. What Go rejects is reported
// (untyped-constant), never wrapped: an int result outside int64, which
// does not compile as an int; an integer of more than 512 bits at any step
// (go/types: "constant overflow"); and an operation Go refuses on constants
// (a division by zero, a shift count that is negative or above shiftBound,
// operands of different kinds). A name is never a constant: a GALA `val`
// lowers to a Go variable.

const (
	// shiftBound is the largest constant shift count Go's type checker
	// accepts.
	shiftBound = 1023 - 1 + 52
	// constBits is the most bits an untyped integer constant may have in Go's
	// type checker, at every step of its evaluation.
	constBits = 512
)

var (
	constUnaryOps = map[string]token.Token{"-": token.SUB, "+": token.ADD, "^": token.XOR, "!": token.NOT}
	constCmpOps   = map[string]token.Token{"==": token.EQL, "!=": token.NEQ, "<": token.LSS,
		"<=": token.LEQ, ">": token.GTR, ">=": token.GEQ}
	// QUO_ASSIGN is integer (truncated) division, as for Go's int constants.
	constIntOps = map[string]token.Token{"+": token.ADD, "-": token.SUB, "*": token.MUL,
		"/": token.QUO_ASSIGN, "%": token.REM, "&": token.AND, "|": token.OR, "^": token.XOR,
		"&^": token.AND_NOT, "<<": token.SHL, ">>": token.SHR}
)

// constValue is the value of e as Go's type checker computes it, nil when e
// is not built of literals and operators only. An operation Go rejects on
// such operands does not compile in Go, and is reported. Each node is
// evaluated once: g.expr asks again for every subtree of a tree that is not
// a constant.
func (g *gen) constValue(e coreexport.Node) constant.Value {
	if v, seen := g.consts[e]; seen {
		return v
	}
	var v constant.Value
	switch e := e.(type) {
	case *coreexport.Int:
		v = constant.MakeFromLiteral(e.Text, token.INT, 0)
		if v.Kind() != constant.Int {
			unsup(e.Pos, "int-literal")
		}
		v = checkedConst(v, e)
	case *coreexport.Bool:
		v = constant.MakeBool(e.Value)
	case *coreexport.String:
		v = constant.MakeString(e.Value)
	case *coreexport.Paren:
		v = g.constValue(e.Expr)
	case *coreexport.Unary:
		if x := g.constValue(e.Operand); x != nil {
			v = checkedConst(unaryConst(e.Op, x), e)
		}
	case *coreexport.Binary:
		if x := g.constValue(e.Left); x != nil {
			if y := g.constValue(e.Right); y != nil {
				v = checkedConst(binaryConst(e.Op, x, y), e)
			}
		}
	}
	if g.consts == nil {
		g.consts = map[coreexport.Node]constant.Value{}
	}
	g.consts[e] = v
	return v
}

// checkedConst is v, the value of e, reporting e when Go rejects it: v is
// nil, or an integer of more than constBits bits.
func checkedConst(v constant.Value, e coreexport.Node) constant.Value {
	if v == nil || (v.Kind() == constant.Int && constant.BitLen(v) > constBits) {
		unsup(posOf(e), "untyped-constant")
	}
	return v
}

// unaryConst is `op x` as Go computes it, nil when Go rejects it.
func unaryConst(op string, x constant.Value) constant.Value {
	want := constant.Int
	if op == "!" {
		want = constant.Bool
	}
	tok, ok := constUnaryOps[op]
	if !ok || x.Kind() != want {
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

// constTerm is e written as its value, when e is an int literal or a
// constant operator tree (ok). A bool or string literal alone is left to
// g.expr.
func (g *gen) constTerm(e coreexport.Node) (term, bool) {
	switch e.(type) {
	case *coreexport.Bool, *coreexport.String:
		return term{}, false
	}
	v := g.constValue(e)
	if v == nil {
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
