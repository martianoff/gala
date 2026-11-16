/-
  # Theorems about the S1 semantics

  What is proved here (docs/FORMAL_SEMANTICS.MD, "Theorems"):

  * **Closures (S2).** `capturedEnv_captured`: a lambda's call sees exactly the scope it
    was written in, each `var` by the same cell. It assumes every binding of the scope is
    mutable exactly when it holds a cell (`cellsMutable`); that evaluation keeps this
    invariant is not proved.
  * **T4, the frame of a default value.** `defaultFrame_lookup` (with its special cases
    `defaultFrame_other`, `defaultFrame_last`): the frame a default value is evaluated in
    binds exactly the parameters before it, and no local of the caller's. That
    `bindDefaults` evaluates defaults in this frame is its definition; that doing so never
    reads the caller's locals is `bindDefaults_callerLocals` (Gala/Defaults.lean).

  `lookup_bind_self` and `lookup_bind_other` are lemmas for those proofs.

  T1 is proved in Gala/Fuel.lean. T2 and T3 are not proved; docs/FORMAL_SEMANTICS.MD
  gives the statements they need.
-/
import Gala.Eval

namespace Gala.Spec

/-! ## Lemmas: a binding shadows its own name and no other -/

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

/-! ## T4: the frame of a default value -/

/-- **T4, the frame of a default.** The scope a default value is evaluated in binds exactly
    the parameters before it: a name is bound to the value of the latest earlier parameter
    of that name, as an immutable binding, and is unbound if no earlier parameter has it
    (or if it is `_`, which binds nothing). `defaultFrame_other` and `defaultFrame_last` are
    special cases. -/
theorem defaultFrame_lookup (earlier : List (String × Val)) (x : String) :
    lookup (defaultFrame earlier) x =
      if x = "_" then none else (earlier.reverse.lookup x).map (⟨·, false⟩) := by
  unfold defaultFrame
  rw [← List.reverse_reverse earlier]
  generalize earlier.reverse = r
  rw [List.reverse_reverse]
  induction r with
  | nil => split <;> rfl
  | cons yv rest ih =>
    obtain ⟨y, v⟩ := yv
    rw [List.reverse_cons, List.foldl_append, List.foldl_cons, List.foldl_nil]
    dsimp only
    by_cases hxy : y = x
    · subst hxy
      by_cases hx : y = "_"
      · subst hx
        rw [show ∀ ρ b, bind ρ "_" b = ρ from fun _ _ => by simp [bind], ih]
        simp
      · rw [lookup_bind_self _ _ _ hx]
        simp [hx]
    · rw [lookup_bind_other _ _ _ _ hxy, ih]
      have : (x == y) = false := by simp [Ne.symm hxy]
      simp [List.lookup_cons, this]

/-- **T4, scoping.** A default value is evaluated in a scope holding only the parameters
    before it: any other name, a local of the caller's included, is not bound there, so it
    resolves to a package-level declaration or to nothing. -/
theorem defaultFrame_other (earlier : List (String × Val)) (x : String)
    (h : x ∉ earlier.map (·.1)) : lookup (defaultFrame earlier) x = none := by
  rw [defaultFrame_lookup]
  split
  · rfl
  · have : earlier.reverse.lookup x = none := by
      induction earlier with
      | nil => rfl
      | cons yv rest ih =>
        simp only [List.map, List.mem_cons, not_or] at h
        rw [List.reverse_cons, List.lookup_append, ih h.2]
        simp [h.1]
    simp [this]

/-- **T4, values.** In that scope the parameter just before is bound to the value the call
    gave it (explicitly or by its own default). -/
theorem defaultFrame_last (earlier : List (String × Val)) (x : String) (v : Val)
    (hx : x ≠ "_") : lookup (defaultFrame (earlier ++ [(x, v)])) x = some ⟨v, false⟩ := by
  simp [defaultFrame_lookup, hx]

end Gala.Spec
