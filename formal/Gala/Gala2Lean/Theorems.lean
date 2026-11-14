/-
  # Theorems about programs gala2lean translated

  These are proved about the Lean that `internal/formal/gala2lean` writes for a GALA
  program, exactly as written: `Gala/Gala2Lean/Examples/` holds the translator's output,
  and a test (`internal/formal/gala2lean`) fails when it differs from what the translator
  writes today, so no proof here rests on a hand edit.

  They are theorems about the translation of one program, under the translation's
  model of GALA (the prelude, `Gala/Gala2Lean/Prelude.lean`). They say nothing about the
  transpiler: the program's Go build is tested, not proved.
-/
import Gala.Gala2Lean.Examples.Match

namespace Gala.Gala2Lean.Examples.Match

/-- The obvious fold over an expression tree, with Go's wrapping `int64` arithmetic. -/
def denote : Expr → Int64
  | .Num v => v
  | .Add l r => denote l + denote r
  | .Neg e => - denote e
  | .Zero => 0

/-- `eval` from `formal/tests/src/match.gala` (a `match` with nested patterns, recursion
    and a default panic) never panics or diverges, leaves the state (the printed output
    and the package-level variables) unchanged, and computes `denote`. `eval` is defined
    by `partial_fixpoint`; its equation (`rw [eval]`) is what makes this provable. -/
theorem eval_total (e : Expr) (s : _gala_St) :
    (eval e).run.run s = some (.ok (denote e), s) := by
  induction e generalizing s with
  | Num v => rw [eval]; rfl
  | Add l r ihl ihr =>
    rw [eval]
    simp only [ExceptT.run_bind, StateT.run_bind, ihl, Option.bind_eq_bind, Option.bind_some]
    simp only [ihr, Option.bind_some]
    rfl
  | Neg e ih =>
    -- `Neg(Num(v))` is its own arm, so each other operand first fails that arm.
    cases e with
    | Num v => rw [eval]; rfl
    | Add l r =>
      rw [eval]
      simp only [ExceptT.run_bind, StateT.run_bind, ih, Option.bind_eq_bind, Option.bind_some]
      · rfl
      · intro v h; cases h
    | Neg x =>
      rw [eval]
      simp only [ExceptT.run_bind, StateT.run_bind, ih, Option.bind_eq_bind, Option.bind_some]
      · rfl
      · intro v h; cases h
    | Zero =>
      rw [eval]
      simp only [ExceptT.run_bind, StateT.run_bind, ih, Option.bind_eq_bind, Option.bind_some]
      · rfl
      · intro v h; cases h
  | Zero => rw [eval]; rfl

end Gala.Gala2Lean.Examples.Match
