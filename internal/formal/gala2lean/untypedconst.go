package gala2lean

import (
	"math"
	"math/big"

	"martianoff/gala/internal/formal/coreexport"
)

// An operator expression whose operands are all literals is a Go constant
// expression: the transpiler emits it as it is, and Go evaluates it exactly,
// at compile time, giving only the result a type (#1237). gala2lean translates
// integer operators to Lean's Int64 ones, which wrap. The two agree when no
// step of the exact evaluation leaves int64 and no shift count is outside
// 0..63; checkUntypedConstant reports every other such expression as
// `untyped-constant`, so a translation never prints a wrapped value Go does
// not. Folding the expression exactly would translate it instead.

var (
	minInt64 = big.NewInt(math.MinInt64)
	maxInt64 = big.NewInt(math.MaxInt64)
	// The largest integer literal intLit translates: 2^63, which Int64 wraps to
	// its minimum, so that `-9223372036854775808` is the minimum, as in Go.
	maxIntLit = new(big.Int).Lsh(big.NewInt(1), 63)
)

// constState is how far an expression is a constant expression whose Int64
// translation is exact.
type constState int

const (
	notConst  constState = iota // an operand is not a literal
	exact                       // a constant; every integer step fits int64
	mayDiffer                   // a constant whose Int64 translation may differ from Go's value
)

// checkUntypedConstant stops the translation of e, an operator expression, if
// it is a constant expression whose Int64 translation may differ from Go's
// value.
func checkUntypedConstant(e coreexport.Node, pos coreexport.Pos) {
	if s, _ := constValue(e); s == mayDiffer {
		unsup(pos, "untyped-constant")
	}
}

// constValue evaluates e exactly if it is a constant expression; the value is
// nil for one that is not an integer.
func constValue(e coreexport.Node) (constState, *big.Int) {
	switch e := e.(type) {
	case *coreexport.Int:
		v, ok := new(big.Int).SetString(normalizedIntText(e.Text), 0)
		if !ok || v.Cmp(maxIntLit) > 0 {
			return mayDiffer, nil
		}
		return exact, v
	case *coreexport.Bool, *coreexport.String:
		return exact, nil
	case *coreexport.Paren:
		return constValue(e.Expr)
	case *coreexport.Unary:
		s, v := constValue(e.Operand)
		if s != exact || v == nil {
			return s, nil
		}
		switch e.Op {
		case "-":
			return inInt64(new(big.Int).Neg(v))
		case "^":
			return inInt64(new(big.Int).Not(v))
		}
		return exact, v
	case *coreexport.Binary:
		sl, l := constValue(e.Left)
		sr, r := constValue(e.Right)
		switch {
		case sl == notConst || sr == notConst:
			return notConst, nil
		case sl == mayDiffer || sr == mayDiffer:
			return mayDiffer, nil
		case l == nil || r == nil:
			return exact, nil
		}
		return intBinary(e.Op, l, r)
	}
	return notConst, nil
}

// intBinary is `l op r` on integer constants, exactly.
func intBinary(op string, l, r *big.Int) (constState, *big.Int) {
	z := new(big.Int)
	switch op {
	case "+":
		return inInt64(z.Add(l, r))
	case "-":
		return inInt64(z.Sub(l, r))
	case "*":
		return inInt64(z.Mul(l, r))
	case "/", "%":
		if r.Sign() == 0 {
			return mayDiffer, nil
		}
		if op == "/" {
			return inInt64(z.Quo(l, r))
		}
		return inInt64(z.Rem(l, r))
	case "<<", ">>":
		if r.Sign() < 0 || r.Cmp(big.NewInt(63)) > 0 {
			return mayDiffer, nil
		}
		if op == "<<" {
			return inInt64(z.Lsh(l, uint(r.Int64())))
		}
		return inInt64(z.Rsh(l, uint(r.Int64())))
	case "&":
		return inInt64(z.And(l, r))
	case "|":
		return inInt64(z.Or(l, r))
	case "^":
		return inInt64(z.Xor(l, r))
	case "&^":
		return inInt64(z.AndNot(l, r))
	}
	// A comparison: its value is a bool.
	return exact, nil
}

func inInt64(v *big.Int) (constState, *big.Int) {
	if v.Cmp(minInt64) < 0 || v.Cmp(maxInt64) > 0 {
		return mayDiffer, nil
	}
	return exact, v
}
