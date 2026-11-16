/-
  # The statements of the required theorems

  Each theorem that COVERAGE.md calls proved (`requiredTheorems` in
  `scripts/ProofCheck.lean`, `REQUIRED_THEOREMS` in `BUILD.bazel`) is restated here in
  full, as a theorem of its own whose proof is the original. Changing a theorem's
  statement then fails the build until this file changes too, so the change shows in
  review. The proof check covers these theorems like any other (complete proofs, standard
  axioms only), and `tools/check_repo_policy.sh` checks that every required theorem is
  restated here.

  What this pins is the text of each statement. A definition a statement mentions (such
  as `cellsMutable`) is not pinned: changing it changes what the theorem means.
-/
import Gala.MatchLowering
import Gala.Theorems
import Gala.Gala2Lean.Theorems
import Gala.Fuel
import Gala.Defaults

namespace Gala.Statements

open Gala.MatchLowering in
theorem compile_correct : ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compile e) = encR (seval ρ e) :=
  Gala.MatchLowering.compile_correct

open Gala.MatchLowering in
theorem program_correct : ∀ (e : SExpr), teval [] (compile e) = encR (seval [] e) :=
  Gala.MatchLowering.program_correct

open Gala.MatchLowering in
theorem leaky_not_correct : ¬ ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compileLeaky e) = encR (seval ρ e) :=
  Gala.MatchLowering.leaky_not_correct

open Gala.Spec in
theorem capturedEnv_captured : ∀ (ρ : Env), cellsMutable ρ → capturedEnv (captured ρ) = ρ :=
  Gala.Spec.capturedEnv_captured

open Gala.Spec in
theorem defaultFrame_other : ∀ (earlier : List (String × Val)) (x : String),
    x ∉ earlier.map (·.1) → lookup (defaultFrame earlier) x = none :=
  Gala.Spec.defaultFrame_other

open Gala.Spec in
theorem defaultFrame_last : ∀ (earlier : List (String × Val)) (x : String) (v : Val),
    x ≠ "_" → lookup (defaultFrame (earlier ++ [(x, v)])) x = some ⟨v, false⟩ :=
  Gala.Spec.defaultFrame_last

open Gala.Gala2Lean.Examples.Match Gala.Gen in
theorem eval_total : ∀ (e : Expr) (s : _gala_St),
    (eval e).run.run s = some (.ok (denote e), s) :=
  Gala.Gala2Lean.Examples.Match.eval_total

open Gala.Spec in
theorem run_fuel_adequate : ∀ (p : Program) (n m : Nat), n ≤ m → (run p n).1 ≠ .fuel →
    run p m = run p n :=
  Gala.Spec.run_fuel_adequate

open Gala.Spec in
theorem defaultFrame_lookup : ∀ (earlier : List (String × Val)) (x : String),
    lookup (defaultFrame earlier) x =
      if x = "_" then none else (earlier.reverse.lookup x).map (⟨·, false⟩) :=
  Gala.Spec.defaultFrame_lookup

open Gala.Spec in
theorem bindDefaults_callerLocals : ∀ (ds : Decls) (d : Nat) (names : List String)
    (tys : List (Option Ty)) (dflts : List (Option Expr)) (fieldScope : Bool)
    (given : List (Nat × Val)) (s : St) (l : Env),
    let a := (bindDefaults ds d names tys dflts fieldScope given).run.run s
    let b := (bindDefaults ds d names tys dflts fieldScope given).run.run { s with locals := l }
    a.1 = b.1 ∧ { a.2 with locals := [] } = { b.2 with locals := [] } ∧
      ∀ v, a.1 = .ok v → a.2.locals = s.locals ∧ b.2.locals = l :=
  Gala.Spec.bindDefaults_callerLocals

end Gala.Statements
