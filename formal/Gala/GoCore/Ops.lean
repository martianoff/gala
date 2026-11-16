/-
  # GoCore: operators on values

  Go's operators on the values of `Gala/GoCore/Value.lean`, apart from the evaluator.
  Each cites the Go specification section it follows. They never evaluate code; the
  conversion of an untyped constant operand to the other operand's type happens before
  them (Go spec, "Operators": "the untyped operand is implicitly converted to the type of
  the other operand").
-/
import Gala.GoCore.Value

namespace Gala.GoCore.Full

/-- A runtime panic, with the text Go's runtime prints (Go spec, "Run-time panics"). -/
def runtimePanic (text : String) : GM α :=
  throw (.panic (.str .string ("runtime error: " ++ text)) ("runtime error: " ++ text))

/-- A nil pointer dereferenced: through `*p`, a field, a method's value receiver or a
    nil function or interface (Go spec, "Address operators", "Run-time panics"). -/
def nilDeref : GM α := runtimePanic "invalid memory address or nil pointer dereference"

/-- The default type of an untyped constant (Go spec, "Constants"). -/
def UKind.default : UKind → Option Ty
  | .int => some (.int .int)
  | .rune => some (.int .int32)
  | .bool => some .bool
  | .string => some .string
  | .nil => none

/-- An untyped value given its default type, for `x := 1` and an interface operand. -/
def defaultVal : Val → Val
  | .int (.untyped k) n => .int ((k.default).getD (.int .int)) n
  | .bool (.untyped _) b => .bool .bool b
  | .str (.untyped _) s => .str .string s
  | v => v

/-- `x << s` and `x >> s` for an integer of kind `k`: `>>` is arithmetic for signed
    integers, logical for unsigned ones; the count may exceed the width (Go spec,
    "Integer operators"). -/
def shiftL (k : IntKind) (x : Int) (s : Nat) : Int :=
  if s ≥ k.bits then 0 else k.wrap (x * 2 ^ s)
def shiftR (x : Int) (s : Nat) : Int :=
  -- Floor division is the arithmetic shift for a negative x, and x is never negative
  -- for an unsigned kind.
  x / 2 ^ s

/-- Bitwise operators on mathematical integers in two's complement, with infinitely many
    sign bits (Go spec, "Arithmetic operators": exact for untyped constants; a typed
    result is wrapped after). Built from `Nat`'s operators and `~x = -x - 1`. -/
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

/-- An arithmetic or bitwise operator on two integers of kind `k`, wrapping (Go spec,
    "Arithmetic operators", "Integer overflow"). `k` is `none` for untyped constants,
    which are exact. -/
def intOp (op : String) (k : Option IntKind) (x y : Int) : GM Int := do
  let w := fun r => match k with | some k => k.wrap r | none => r
  if (op == "/" || op == "%") && y == 0 then
    if k.isNone then stuck "constant division by zero" else runtimePanic "integer divide by zero"
  match op with
  | "+" => pure (w (x + y))
  | "-" => pure (w (x - y))
  | "*" => pure (w (x * y))
  -- Truncated toward zero (Go spec, "Integer operators").
  | "/" => pure (w (x.tdiv y))
  | "%" => pure (w (x.tmod y))
  | "&" => pure (w (bitAnd x y))
  | "|" => pure (w (bitOr x y))
  | "^" => pure (w (bitXor x y))
  | "&^" => pure (w (bitAnd x (bitNot y)))
  | _ => stuck s!"integer operator {op}"

/-- A shift `x op s` (Go spec, "Arithmetic operators", "Integer operators"): a negative
    count panics. `k` is `none` for an untyped constant left operand, which is exact. -/
def shiftOp (op : String) (k : Option IntKind) (x s : Int) : GM Int := do
  if s < 0 then runtimePanic "negative shift amount"
  let s := s.toNat
  match op, k with
  | "<<", some k => pure (shiftL k x s)
  | "<<", none => pure (x * 2 ^ s)
  | ">>", _ => pure (shiftR x s)
  | _, _ => stuck s!"shift operator {op}"

/-- Ordered comparison (Go spec, "Comparison operators"); strings compare bytewise,
    which for UTF-8 strings is code point order, Lean's `<` on `String`. -/
def cmpOp (op : String) (c : Ordering) : GM Bool :=
  match op with
  | "<" => pure (c == .lt)
  | "<=" => pure (c != .gt)
  | ">" => pure (c == .gt)
  | ">=" => pure (c != .lt)
  | _ => stuck s!"comparison {op}"

end Gala.GoCore.Full
