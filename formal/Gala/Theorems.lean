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

  * **T3, for operand lists.** `evalList_cons`, `evalList_nil`: arguments, tuple
    elements and interpolation parts are evaluated first to last (the definition's own
    equation, recorded as T1 is).
  * **Closures (S2).** `capturedEnv_captured`, `lookup_captured`: a lambda's call sees
    exactly the scope it was written in, each `var` by the same cell.

  * **T4, defaults in the callee's scope.** `defaultFrame_other`, `defaultFrame_last`: a
    default value sees only the parameters before it, the last one bound to the value the
    call gave it; never a local of the caller's.

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

/-! ## T3 for lists of operands -/

/-- **T3, for operand lists.** The arguments of a call (a function's, a lambda's, a
    constructor's, `Println`'s), the elements of a tuple and the parts of an interpolated
    string are evaluated by `evalList`: the first one completely, then the rest, so every
    effect of an earlier operand happens before any effect of a later one. -/
theorem evalList_cons (ds : Decls) (d : Nat) (e : Expr) (es : List Expr) :
    evalList ds d (e :: es) = (do
      let v ← eval ds d e
      let vs ← evalList ds d es
      pure (v :: vs)) := by
  rw [evalList]

/-- An empty operand list evaluates nothing. -/
theorem evalList_nil (ds : Decls) (d : Nat) : evalList ds d [] = pure [] := by
  rw [evalList]

/-! ## Closures (S2) -/

/-- A scope as a lambda's evaluation keeps it: each binding is mutable exactly when it is
    a `var`'s cell. -/
def cellsMutable (ρ : Env) : Prop := ∀ k b, (k, b) ∈ ρ → b.mutable = (match b.val with
  | .ref _ => true
  | _ => false)

/-- **Closures capture their scope.** A lambda's call sees exactly the bindings of the
    scope it was written in: the same names, shadowed the same way, each `var` by the same
    cell (so assignments on either side are seen by the other), each `val` by its value. -/
theorem capturedEnv_captured (ρ : Env) (h : cellsMutable ρ) : capturedEnv (captured ρ) = ρ := by
  induction ρ with
  | nil => rfl
  | cons kb r ih =>
    obtain ⟨k, ⟨v, m⟩⟩ := kb
    have hm := h k ⟨v, m⟩ (List.mem_cons_self ..)
    have hr : cellsMutable r := fun k' b' hb => h k' b' (List.mem_cons_of_mem _ hb)
    simp only [captured, capturedEnv, List.map] at ih ⊢
    rw [ih hr]
    simp only at hm
    cases v <;> simp_all

/-- What a name resolves to inside a lambda is what it resolved to where the lambda was
    written (T2's lookup lemma for closures). -/
theorem lookup_captured (ρ : Env) (h : cellsMutable ρ) (x : String) :
    lookup (capturedEnv (captured ρ)) x = lookup ρ x := by
  rw [capturedEnv_captured ρ h]

/-! ## T4: defaults in the callee's scope -/

private theorem lookup_foldl_bind_other (x : String) :
    ∀ (earlier : List (String × Val)) (ρ : Env), x ∉ earlier.map (·.1) →
      lookup (earlier.foldl (fun ρ (y, v) => bind ρ y ⟨v, false⟩) ρ) x = lookup ρ x
  | [], _, _ => rfl
  | (y, v) :: rest, ρ, h => by
    simp only [List.map, List.mem_cons, not_or] at h
    simp only [List.foldl]
    rw [lookup_foldl_bind_other x rest _ h.2, lookup_bind_other ρ y x _ (Ne.symm h.1)]

/-- **T4, scoping.** A default value is evaluated in a scope holding only the parameters
    before it: any other name, a local of the caller's included, is not bound there, so it
    resolves to a package-level declaration or to nothing. -/
theorem defaultFrame_other (earlier : List (String × Val)) (x : String)
    (h : x ∉ earlier.map (·.1)) : lookup (defaultFrame earlier) x = none :=
  lookup_foldl_bind_other x earlier [] h

/-- **T4, values.** In that scope the parameter just before is bound to the value the call
    gave it (explicitly or by its own default). -/
theorem defaultFrame_last (earlier : List (String × Val)) (x : String) (v : Val)
    (hx : x ≠ "_") : lookup (defaultFrame (earlier ++ [(x, v)])) x = some ⟨v, false⟩ := by
  simp only [defaultFrame, List.foldl_append, List.foldl]
  exact lookup_bind_self _ x _ hx

end Gala.Spec
