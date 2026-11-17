/-
  # Exact arithmetic on untyped constants

  Go evaluates an expression of untyped constants exactly, at compile time, and gives the
  result a type only where the program uses it (Go spec, "Constants", "Constant
  expressions"). These are the integer operations of that arithmetic, on mathematical
  integers, for both semantics: galaspec folds a program's constant subexpressions with
  them (`Gala/Const.lean`), and GoCore computes untyped constants with the same
  definitions.

  This module imports nothing, so either semantics can import it without depending on the
  other.
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
  /-- A shift count above `maxShift`, or a value of `maxBits` bits or more. Go's spec lets
      an implementation bound its constants ("Constants": at least 256 bits), and gc
      rejects a constant of more than 512 bits and a shift count above 1074. These bounds
      are this model's, no larger than gc's, so a constant they stop is one gc rejects
      too, and it is never given a value. -/
  | tooLarge
  deriving Repr, DecidableEq

/-- A constant's value, or the compile-time error computing it is. -/
inductive Res (α : Type) where
  | val (v : α)
  | err (e : Err)
  deriving Repr, DecidableEq

/-- The largest shift count computed. -/
def maxShift : Nat := 1024

/-- Values of this many bits or more are not computed. -/
def maxBits : Nat := 512

/-- `x`, or `tooLarge` if it has `maxBits` bits or more. -/
def bounded (x : Int) : Res Int :=
  if x.natAbs ≥ 2 ^ maxBits then .err .tooLarge else .val x

/-- An integer operator on untyped constants, exactly (Go spec, "Constant expressions"):
    `/` and `%` truncate toward zero, `>>` floors; `none` for an operator that is not an
    integer operator. -/
def intOp (op : String) (x y : Int) : Option (Res Int) :=
  match op with
  | "+" => some (bounded (x + y))
  | "-" => some (bounded (x - y))
  | "*" => some (bounded (x * y))
  | "/" => some (if y == 0 then .err .divByZero else bounded (x.tdiv y))
  | "%" => some (if y == 0 then .err .divByZero else bounded (x.tmod y))
  | "&" => some (bounded (bitAnd x y))
  | "|" => some (bounded (bitOr x y))
  | "^" => some (bounded (bitXor x y))
  | "&^" => some (bounded (bitAnd x (bitNot y)))
  | "<<" =>
    some (if y < 0 then .err .negativeShift
      else if y.toNat > maxShift then .err .tooLarge
      else bounded (x * 2 ^ y.toNat))
  | ">>" =>
    some (if y < 0 then .err .negativeShift
      else if y.toNat > maxShift then .err .tooLarge
      else bounded (x.shiftRight y.toNat))
  | _ => none

-- Spot checks against Go's constant arithmetic.
#guard intOp "/" (-7) 2 == some (.val (-3)) && intOp "%" (-7) 3 == some (.val (-1))
#guard intOp ">>" (-5) 1 == some (.val (-3)) && intOp ">>" (-1) 1000 == some (.val (-1))
-- gc rejects these: `1 >> 2000` (shift count), `1 << 600` (constant overflow).
#guard intOp ">>" 1 2000 == some (.err .tooLarge) && intOp "<<" 1 600 == some (.err .tooLarge)
#guard intOp "<<" 1 70 == some (.val 1180591620717411303424)
#guard intOp "/" 1 0 == some (.err .divByZero) && intOp "<<" 1 (-1) == some (.err .negativeShift)

end Gala.Exact
