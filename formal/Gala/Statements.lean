/-
  # The statements of the required theorems

  Each theorem that COVERAGE.md calls proved (`requiredTheorems` in
  `scripts/ProofCheck.lean`, `REQUIRED_THEOREMS` in `BUILD.bazel`) is restated here,
  in full, and checked against its proof. Weakening a theorem's statement then fails the
  build until this file changes too, so the change shows in review.
  `tools/check_repo_policy.sh` checks that every required theorem appears here.
-/
import Gala.MatchLowering
import Gala.Theorems
import Gala.Gala2Lean.Theorems

open Gala.MatchLowering in
example : ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compile e) = encR (seval ρ e) :=
  Gala.MatchLowering.compile_correct

open Gala.MatchLowering in
example : ∀ (e : SExpr), teval [] (compile e) = encR (seval [] e) :=
  Gala.MatchLowering.program_correct

open Gala.MatchLowering in
example : ¬ ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compileLeaky e) = encR (seval ρ e) :=
  Gala.MatchLowering.leaky_not_correct

open Gala.Spec in
example : ∀ (ρ : Env), cellsMutable ρ → capturedEnv (captured ρ) = ρ :=
  Gala.Spec.capturedEnv_captured

open Gala.Spec in
example : ∀ (earlier : List (String × Val)) (x : String),
    x ∉ earlier.map (·.1) → lookup (defaultFrame earlier) x = none :=
  Gala.Spec.defaultFrame_other

open Gala.Spec in
example : ∀ (earlier : List (String × Val)) (x : String) (v : Val), x ≠ "_" →
    lookup (defaultFrame (earlier ++ [(x, v)])) x = some ⟨v, false⟩ :=
  Gala.Spec.defaultFrame_last

open Gala.Gala2Lean.Examples.Match Gala.Gen in
example : ∀ (e : Expr) (s : _gala_St), (eval e).run.run s = some (.ok (denote e), s) :=
  Gala.Gala2Lean.Examples.Match.eval_total
