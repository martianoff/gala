/-
  # Theorems about the S1 semantics

  Proved here (docs/FORMAL_SEMANTICS.MD, "Theorems"):

  * **T1, determinism.** `run_deterministic`: a program and a fuel budget have exactly
    one meaning: one output and one outcome. `eval_deterministic` says the same of a
    single evaluation from a given state. The evaluator is a total Lean function, so
    the proofs are short; they are stated so the property is checked, not assumed.
  * **T2, alpha-invariance, for name resolution.** Renaming the variables of an
    environment with an injective renaming does not change what a renamed name resolves
    to (`lookup_rename`), what an assignment updates (`update_rename`), or which names a
    scope keeps when it ends (`exitScope_rename`). These are the facts about scoping that
    alpha-invariance of whole programs rests on.
  * **Scoping.** A binding shadows only its own name (`lookup_bind_self`,
    `lookup_bind_other`); an assignment keeps the shape of the scope stack
    (`update_length`), so leaving a block drops exactly the block's own bindings and keeps
    its assignments to outer variables (`exitScope_push`).

  Not proved yet: T2 for whole programs (renaming every binder of a program does not
  change its outcome). It needs a renaming on the syntax and an induction over `eval`.
-/
import Gala.Eval

namespace Gala.Spec

/-! ## T1: determinism -/

/-- **T1.** A program run with a fuel budget has one meaning. -/
theorem run_deterministic (p : Program) (fuel : Nat) (r₁ r₂ : Outcome × String)
    (h₁ : run p fuel = r₁) (h₂ : run p fuel = r₂) : r₁ = r₂ :=
  h₁ ▸ h₂

/-- **T1** for one evaluation: from the same state, an expression evaluates to one result
    and one final state. -/
theorem eval_deterministic (ds : Decls) (d : Nat) (e : Expr) (s : St)
    (r₁ r₂ : Except Stop Val × St)
    (h₁ : (eval ds d e).run.run s = r₁) (h₂ : (eval ds d e).run.run s = r₂) : r₁ = r₂ :=
  h₁ ▸ h₂

/-! ## Scoping -/

/-- A new binding is what its own name resolves to. -/
theorem lookup_bind_self (ρ : Env) (x : String) (b : Binding) (hx : x ≠ "_") :
    lookup (bind ρ x b) x = some b := by
  simp [bind, hx, lookup]

/-- A new binding does not change what any other name resolves to. -/
theorem lookup_bind_other (ρ : Env) (x y : String) (b : Binding) (h : x ≠ y) :
    lookup (bind ρ x b) y = lookup ρ y := by
  unfold bind
  split
  · rfl
  · simp [lookup, h]

/-- An assignment keeps the number of bindings. -/
theorem update_length : ∀ (ρ ρ' : Env) (x : String) (v : Val),
    update ρ x v = some ρ' → ρ'.length = ρ.length
  | [], _, _, _, h => by simp [update] at h
  | (k, b) :: r, ρ', x, v, h => by
    unfold update at h
    split at h
    · cases h; rfl
    · cases hr : update r x v with
      | none => simp [hr] at h
      | some r' =>
        simp [hr] at h
        subst h
        simp [update_length r r' x v hr]

/-- Leaving a scope drops the bindings made in it and keeps the ones outside it. -/
theorem exitScope_push (inner ρ : Env) : exitScope (inner ++ ρ) ρ.length = ρ := by
  simp [exitScope]

/-! ## T2 for name resolution: renaming -/

/-- Rename every variable of an environment. -/
def renameEnv (σ : String → String) (ρ : Env) : Env := ρ.map fun (k, b) => (σ k, b)

/-- What a renamed name resolves to in a renamed environment is what the name resolved
    to before, for an injective renaming. -/
theorem lookup_rename (σ : String → String) (hσ : ∀ a b, σ a = σ b → a = b) :
    ∀ (ρ : Env) (x : String), lookup (renameEnv σ ρ) (σ x) = lookup ρ x
  | [], _ => rfl
  | (k, b) :: r, x => by
    simp only [renameEnv, List.map, lookup] at *
    by_cases h : k = x
    · simp [h]
    · have h' : σ k ≠ σ x := fun e => h (hσ _ _ e)
      simp only [h, h', ite_false]
      exact lookup_rename σ hσ r x

/-- An assignment to a renamed name in a renamed environment updates the same binding. -/
theorem update_rename (σ : String → String) (hσ : ∀ a b, σ a = σ b → a = b) :
    ∀ (ρ : Env) (x : String) (v : Val),
      update (renameEnv σ ρ) (σ x) v = (update ρ x v).map (renameEnv σ)
  | [], _, _ => rfl
  | (k, b) :: r, x, v => by
    simp only [renameEnv, List.map, update] at *
    by_cases h : k = x
    · simp [h, renameEnv]
    · have h' : σ k ≠ σ x := fun e => h (hσ _ _ e)
      simp only [h, h', ite_false]
      have ih := update_rename σ hσ r x v
      simp only [renameEnv] at ih
      rw [ih]
      cases update r x v <;> simp [renameEnv]

/-- Leaving a scope commutes with renaming. -/
theorem exitScope_rename (σ : String → String) (ρ : Env) (depth : Nat) :
    exitScope (renameEnv σ ρ) depth = renameEnv σ (exitScope ρ depth) := by
  simp [exitScope, renameEnv, List.map_drop]

end Gala.Spec
