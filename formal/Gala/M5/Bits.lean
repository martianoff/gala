/-
  # M5: galaspec's bitwise operators are Go's

  galaspec computes `&`, `|`, `^` and `&^` on its `int`s through Lean's `Int64` (two's
  complement in 64 bits), GoCore through exact integer bit operations (`Gala/Exact.lean`)
  reduced to Go's `int` (`IntKind.wrap`, `bmod 2^64`). Through core's `BitVec` lemmas they
  agree: `BitVec.ofInt 64` turns each exact operation into the bit vector one
  (`ofInt_bitAnd`, ...), and `BitVec.toInt_ofInt` is the reduction.
-/
import Gala.Eval

namespace Gala.M5

open Gala.Exact

/-- `x &&& ~~~y` and `x &&& y` add up to `x`: they have no bit in common. -/
theorem bv_and_not_add_and (w : Nat) (x y : BitVec w) : (x &&& ~~~y) + (x &&& y) = x := by
  have hd : (x &&& ~~~y) &&& (x &&& y) = 0#w := by
    ext i hi; simp; intro _ _; simp_all
  rw [BitVec.add_eq_or_of_and_eq_zero _ _ hd]
  ext i hi; simp; cases x[i] <;> cases y[i] <;> simp

/-- `a - (a &&& b)`, which `bitAnd` uses for a negative operand, is `a &&& ~~~b` in bits. -/
theorem ofNat_sub_and (w a b : Nat) :
    BitVec.ofNat w (a - (a &&& b)) = BitVec.ofNat w a &&& ~~~(BitVec.ofNat w b) := by
  have hle : a &&& b ≤ a := Nat.and_le_left
  have h1 : BitVec.ofNat w (a - (a &&& b)) = BitVec.ofNat w a - BitVec.ofNat w (a &&& b) := by
    refine BitVec.eq_sub_iff_add_eq.mpr ?_
    rw [← BitVec.ofNat_add, Nat.sub_add_cancel hle]
  have h2 : BitVec.ofNat w a &&& ~~~(BitVec.ofNat w b) =
      BitVec.ofNat w a - BitVec.ofNat w (a &&& b) := by
    refine BitVec.eq_sub_iff_add_eq.mpr ?_
    rw [BitVec.ofNat_and]; exact bv_and_not_add_and _ _ _
  rw [h1, h2]

theorem ofInt_bitNot (w : Nat) (b : Int) : BitVec.ofInt w (bitNot b) = ~~~ BitVec.ofInt w b := by
  cases b with
  | ofNat n =>
    have : bitNot (Int.ofNat n) = Int.negSucc n := by simp [bitNot]; omega
    rw [this, BitVec.ofInt_negSucc_eq_not_ofNat]; simp
  | negSucc n =>
    have : bitNot (Int.negSucc n) = Int.ofNat n := by simp [bitNot, Int.negSucc_eq]
    rw [this, BitVec.ofInt_negSucc_eq_not_ofNat]; simp

theorem ofInt_bitAnd (w : Nat) (a b : Int) :
    BitVec.ofInt w (bitAnd a b) = BitVec.ofInt w a &&& BitVec.ofInt w b := by
  cases a <;> cases b <;> simp only [bitAnd, BitVec.ofInt_negSucc_eq_not_ofNat]
  all_goals simp only [Int.ofNat_eq_natCast, BitVec.ofInt_natCast, ofNat_sub_and, BitVec.ofNat_and,
    BitVec.ofNat_or, BitVec.not_or]
  all_goals first | rfl | exact BitVec.and_comm _ _

theorem ofInt_bitOr (w : Nat) (a b : Int) :
    BitVec.ofInt w (bitOr a b) = BitVec.ofInt w a ||| BitVec.ofInt w b := by
  cases a <;> cases b <;> simp only [bitOr, BitVec.ofInt_negSucc_eq_not_ofNat]
  all_goals simp only [Int.ofNat_eq_natCast, BitVec.ofInt_natCast, ofNat_sub_and, BitVec.ofNat_and,
    BitVec.ofNat_or, BitVec.not_and]
  all_goals (ext i hi; simp)
  all_goals (cases (BitVec.ofNat w _)[i] <;> (try cases (BitVec.ofNat w _)[i]) <;> rfl)

theorem ofInt_bitXor (w : Nat) (a b : Int) :
    BitVec.ofInt w (bitXor a b) = BitVec.ofInt w a ^^^ BitVec.ofInt w b := by
  cases a <;> cases b <;> simp only [bitXor, BitVec.ofInt_negSucc_eq_not_ofNat]
  all_goals simp only [Int.ofNat_eq_natCast, BitVec.ofInt_natCast, BitVec.ofNat_xor]
  all_goals (ext i hi; simp)
  all_goals (cases (BitVec.ofNat w _)[i] <;> (try cases (BitVec.ofNat w _)[i]) <;> rfl)

/-- A galaspec bit operation through `Int64` is the exact one reduced to 64 bits. -/
theorem bits_eq (f : Int64 → Int64 → Int64) (g : BitVec 64 → BitVec 64 → BitVec 64)
    (hf : ∀ x y, (f x y).toBitVec = g x.toBitVec y.toBitVec) (op : Int → Int → Int)
    (hop : ∀ a b, BitVec.ofInt 64 (op a b) = g (BitVec.ofInt 64 a) (BitVec.ofInt 64 b)) (a b : Int) :
    (f (Int64.ofInt a) (Int64.ofInt b)).toInt = Spec.wrap (op a b) := by
  show (f (Int64.ofInt a) (Int64.ofInt b)).toBitVec.toInt = _
  rw [hf, Int64.toBitVec_ofInt, Int64.toBitVec_ofInt, ← hop, BitVec.toInt_ofInt]; rfl

theorem binop_and (x y : Int) (c : Spec.Core) :
    (Spec.binop "&" (.int x) (.int y)).run.run c = (.ok (.int (Spec.wrap (bitAnd x y))), c) := by
  rw [← bits_eq (· &&& ·) (· &&& ·) Int64.toBitVec_and bitAnd (ofInt_bitAnd 64) x y]
  rfl

theorem binop_or (x y : Int) (c : Spec.Core) :
    (Spec.binop "|" (.int x) (.int y)).run.run c = (.ok (.int (Spec.wrap (bitOr x y))), c) := by
  rw [← bits_eq (· ||| ·) (· ||| ·) Int64.toBitVec_or bitOr (ofInt_bitOr 64) x y]
  rfl

theorem binop_xor (x y : Int) (c : Spec.Core) :
    (Spec.binop "^" (.int x) (.int y)).run.run c = (.ok (.int (Spec.wrap (bitXor x y))), c) := by
  rw [← bits_eq (· ^^^ ·) (· ^^^ ·) Int64.toBitVec_xor bitXor (ofInt_bitXor 64) x y]
  rfl

theorem binop_andNot (x y : Int) (c : Spec.Core) :
    (Spec.binop "&^" (.int x) (.int y)).run.run c =
      (.ok (.int (Spec.wrap (bitAnd x (bitNot y)))), c) := by
  rw [← bits_eq (fun p q => p &&& ~~~q) (fun p q => p &&& ~~~q)
    (fun p q => by simp [Int64.toBitVec_and, Int64.toBitVec_not]) (fun a b => bitAnd a (bitNot b))
    (fun a b => by simp only [ofInt_bitAnd, ofInt_bitNot]) x y]
  rfl

end Gala.M5
