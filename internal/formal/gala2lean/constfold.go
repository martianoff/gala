package gala2lean

import (
	"go/constant"
	"go/token"

	"martianoff/gala/internal/formal/coreexport"
)

// Constant expressions. An operator tree whose leaves are all literals is a
// Go untyped constant once transpiled: the transformer writes it as such,
// and Go evaluates it exactly, on unbounded integers, before it takes a
// type (#1237). So `(1 << 70) >> 68` is 4, where int64 operations would
// give 0. Each maximal such tree is evaluated here with Go's own constant
// arithmetic (go/constant, as Go's type checker does) and written as its
// value: an int that fits int64, a bool or a string. An int outside int64
// does not compile in Go as an int, and is reported (untyped-constant). A
// name is never a constant: a GALA `val` lowers to a Go variable.

// constTree reports whether e is an operator tree of literals: a unary or
// binary operator or parentheses over literals, not a literal alone.
func constTree(e coreexport.Node) bool {
	switch e := e.(type) {
	case *coreexport.Unary, *coreexport.Binary:
		return true
	case *coreexport.Paren:
		return constTree(e.Expr)
	}
	return false
}

// goConstValue is the value of constant expression e, as Go's type checker
// computes it; ok is false when e is not a constant expression Go accepts:
// a name, a call, operands of different kinds, a division by zero.
func goConstValue(e coreexport.Node) (v constant.Value, ok bool) {
	switch e := e.(type) {
	case *coreexport.Int:
		v = constant.MakeFromLiteral(e.Text, token.INT, 0)
		return v, v.Kind() == constant.Int
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
		switch {
		case x.Kind() == constant.Int && (e.Op == "-" || e.Op == "+" || e.Op == "^"):
			op := map[string]token.Token{"-": token.SUB, "+": token.ADD, "^": token.XOR}[e.Op]
			return constant.UnaryOp(op, x, 0), true
		case x.Kind() == constant.Bool && e.Op == "!":
			return constant.UnaryOp(token.NOT, x, 0), true
		}
		return nil, false
	case *coreexport.Binary:
		x, ok := goConstValue(e.Left)
		if !ok {
			return nil, false
		}
		y, ok := goConstValue(e.Right)
		if !ok || x.Kind() != y.Kind() {
			return nil, false
		}
		return binaryConst(e.Op, x, y)
	}
	return nil, false
}

// binaryConst is `x op y` of constants of one kind, as Go computes it.
func binaryConst(op string, x, y constant.Value) (constant.Value, bool) {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		if x.Kind() == constant.Bool && op != "==" && op != "!=" {
			return nil, false
		}
		tok := map[string]token.Token{"==": token.EQL, "!=": token.NEQ, "<": token.LSS,
			"<=": token.LEQ, ">": token.GTR, ">=": token.GEQ}[op]
		return constant.MakeBool(constant.Compare(x, tok, y)), true
	case "&&", "||":
		if x.Kind() != constant.Bool {
			return nil, false
		}
		return constant.BinaryOp(x, map[string]token.Token{"&&": token.LAND, "||": token.LOR}[op], y), true
	case "+":
		if x.Kind() == constant.Bool {
			return nil, false
		}
		return constant.BinaryOp(x, token.ADD, y), true
	}
	if x.Kind() != constant.Int {
		return nil, false
	}
	switch op {
	case "<<", ">>":
		n, exact := constant.Uint64Val(y)
		if !exact || constant.Sign(y) < 0 || n > 1074 {
			return nil, false
		}
		return constant.Shift(x, map[string]token.Token{"<<": token.SHL, ">>": token.SHR}[op], uint(n)), true
	case "/", "%":
		if constant.Sign(y) == 0 {
			return nil, false
		}
		// QUO_ASSIGN is integer (truncated) division, as for Go's int constants.
		return constant.BinaryOp(x, map[string]token.Token{"/": token.QUO_ASSIGN, "%": token.REM}[op], y), true
	}
	tok, ok := map[string]token.Token{"-": token.SUB, "*": token.MUL, "&": token.AND, "|": token.OR,
		"^": token.XOR, "&^": token.AND_NOT}[op]
	if !ok {
		return nil, false
	}
	return constant.BinaryOp(x, tok, y), true
}

// constTerm is constant tree e written as its value, when it is one (ok).
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
		return term{code: "(" + constant.MakeInt64(n).ExactString() + " : Int64)", pure: true}, true
	case constant.Bool:
		if constant.BoolVal(v) {
			return term{code: "true", pure: true}, true
		}
		return term{code: "false", pure: true}, true
	case constant.String:
		return term{code: strLit(constant.StringVal(v), posOf(e)), pure: true}, true
	}
	return term{}, false
}
