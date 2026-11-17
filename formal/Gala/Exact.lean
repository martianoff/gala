/-
  # Exact arithmetic on untyped constants

  Go evaluates an expression of untyped constants exactly, at compile time, and gives the
  result a type only where the program uses it (Go spec, "Constants", "Constant
  expressions"). These are the integer operations of that arithmetic, on mathematical
  integers, for both semantics: galaspec folds a program's constant subexpressions with
  them (`Gala/Const.lean`), and GoCore computes untyped constants with the same
  definitions.

  This module imports nothing, so either semantics can import it without depending on the
  other. For now `Gala/GoCore/Ops.lean` still has its own copies of the bitwise
  operations; they move here, with the rational constants floats need.
-/
namespace Gala.Exact

/-- Bitwise operators on mathematical integers in two's complement, with infinitely many
    sign bits (Go spec, "Arithmetic operators": exact for untyped constants). Built from
    `Nat`'s operators and `~x = -x - 1`. -/
def bitNot (x : Int) : Int := -x - 1
def bitAnd : Int → Int → Int
  | .ofNat a, .ofNat b => .ofNat (a &&& b)
  | .ofNat a, .negSucc b => .ofNat (a - (a &&& b))
  | .negSucc a, .ofNat b => .ofNat (b - (b &&& a))
  | .negSucc a, .negSucc b => .negSucc (a ||| b)
def bitOr : Int → Int → Int
  | .ofNat a, .ofNat b => .ofNat (a ||| b)
  | .ofNat a, .negSucc b => .negSucc (b - (b &&& a))
  | .negSucc a, .ofNat b => .negSucc (a - (a &&& b))
  | .negSucc a, .negSucc b => .negSucc (a &&& b)
def bitXor : Int → Int → Int
  | .ofNat a, .ofNat b => .ofNat (a ^^^ b)
  | .ofNat a, .negSucc b => .negSucc (a ^^^ b)
  | .negSucc a, .ofNat b => .negSucc (a ^^^ b)
  | .negSucc a, .negSucc b => .ofNat (a ^^^ b)

-- Spot checks against Go: `-6 & 5 == 0`, `-6 | 5 == -1`, `-6 ^ 5 == -1`, `-6 &^ 5 == -6`.
#guard bitAnd (-6) 5 == 0 && bitOr (-6) 5 == -1 && bitXor (-6) 5 == -1
#guard bitAnd (-6) (bitNot 5) == -6 && bitAnd (-6) (-3) == -8 && bitOr (-6) (-3) == -1
#guard bitAnd 12 (-5) == 8 && bitOr 12 (-5) == -1 && bitXor (-6) (-3) == 7

/-- Why a constant expression is not a value: each is an error in Go, at compile time. -/
inductive Err
  /-- `x / 0` or `x % 0` (Go spec, "Arithmetic operators": "If the divisor is a constant,
      it must not be zero"). -/
  | divByZero
  /-- A negative shift count (Go spec, "Operators": the count must be non-negative). -/
  | negativeShift
  /-- A left shift by more than `maxShift`: a bound of this model, not of Go, which keeps
      the exact result's size bounded. gc's own limit on constants is lower. -/
  | shiftTooLarge
  deriving Repr, BEq, DecidableEq

/-- A constant's value, or the compile-time error computing it is. -/
inductive Res (α : Type) where
  | val (v : α)
  | err (e : Err)
  deriving Repr, BEq, DecidableEq

/-- The largest count `x << s` is computed for exactly. -/
def maxShift : Nat := 1024

/-- An integer operator on untyped constants, exactly (Go spec, "Constant expressions"):
    `/` and `%` truncate toward zero, `>>` floors; `none` for an operator that is not an
    integer operator. -/
def intOp (op : String) (x y : Int) : Option (Res Int) :=
  match op with
  | "+" => some (.val (x + y))
  | "-" => some (.val (x - y))
  | "*" => some (.val (x * y))
  | "/" => some (if y == 0 then .err .divByZero else .val (x.tdiv y))
  | "%" => some (if y == 0 then .err .divByZero else .val (x.tmod y))
  | "&" => some (.val (bitAnd x y))
  | "|" => some (.val (bitOr x y))
  | "^" => some (.val (bitXor x y))
  | "&^" => some (.val (bitAnd x (bitNot y)))
  | "<<" =>
    some (if y < 0 then .err .negativeShift
      else if y.toNat > maxShift then .err .shiftTooLarge
      else .val (x * 2 ^ y.toNat))
  | ">>" => some (if y < 0 then .err .negativeShift else .val (x.shiftRight y.toNat))
  | _ => none

-- Spot checks against Go's constant arithmetic.
#guard intOp "/" (-7) 2 == some (.val (-3)) && intOp "%" (-7) 3 == some (.val (-1))
#guard intOp ">>" (-5) 1 == some (.val (-3)) && intOp ">>" (-1) 3000 == some (.val (-1))
#guard intOp "<<" 1 70 == some (.val 1180591620717411303424)
#guard intOp "/" 1 0 == some (.err .divByZero) && intOp "<<" 1 (-1) == some (.err .negativeShift)

end Gala.Exact
