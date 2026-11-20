/-
  # T2, part b2: `eval` commutes with renaming

  `eval_alpha`: an expression and its renaming (`AlphaE`, Gala/Alpha.lean), evaluated with
  related declarations from related states, give related results or related `Stop`s, and
  leave related states: the same output, related locals, package-level variables and heap,
  and exactly the same steps left, so this composes with T1. `Stop`s are compared up to the
  text of `stuck` and `unsupported` (`StopR`). This is a lemma toward T2, not T2: T2 is
  about whole programs (`run`), which part c adds.

  The proof is an induction on `eval`'s depth `d`, not on its mutual block: every function
  of the block calls `eval` at its own depth and nothing at a greater one, so each is
  related once `eval` is, at that depth (`EvalIH`, and the `_alpha_of` lemmas). Each case of
  `eval` is a lemma of its own (`eval_…_alpha`), on `eval`'s equation for that case.
-/
import Gala.AlphaStep

namespace Gala.Spec

/-- Related evaluation states: related locals, package-level variables, heaps and output,
    and exactly the same steps left. -/
def StR (pkg : List String) (pm : String → Ren) (Γ : Ren) (s s' : St) : Prop :=
  CoreR pkg pm Γ s.toCore s'.toCore ∧ s.steps = s'.steps

/-- Two evaluator computations do related things: from related states they give related
    results (or related `Stop`s) and leave related states, along some scope; after a
    result (not a `Stop`), that scope satisfies `Q`. After a `Stop` the states are still
    related, so the output printed before a panic is the same on both sides. The steps
    are exactly equal, so this composes with T1. -/
def RelM (pkg : List String) (pm : String → Ren) (Γ : Ren) (Q : Ren → Prop)
    (R : α → β → Prop) (x : M α) (y : M β) : Prop :=
  ∀ s s', StR pkg pm Γ s s' →
    ResR pkg pm R (x.run.run s).1 (y.run.run s').1 ∧
      ∃ Γ', StR pkg pm Γ' (x.run.run s).2 (y.run.run s').2 ∧
        ((∃ a, (x.run.run s).1 = .ok a) → Q Γ')

theorem relM_pure {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → β → Prop}
    {a : α} {b : β} (h : R a b) : RelM pkg pm Γ (· = Γ) R (pure a) (pure b) :=
  fun _ _ hs => ⟨h, Γ, hs, fun _ => rfl⟩

theorem relM_throw {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {R : α → β → Prop} {e e' : Stop} (h : StopR pkg pm e e') :
    RelM pkg pm Γ Q R (throw e) (throw e') :=
  fun _ _ hs => ⟨h, Γ, hs, fun ⟨_, ha⟩ => nomatch ha⟩

/-- Bind, with the scope the first computation leaves. -/
theorem relM_bind_gen {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q₁ Q : Ren → Prop}
    {R : α → α' → Prop} {S : β → β' → Prop} {x : M α} {x' : M α'} {f : α → M β}
    {f' : α' → M β'} (hx : RelM pkg pm Γ Q₁ R x x')
    (hf : ∀ Γ' a a', Q₁ Γ' → R a a' → RelM pkg pm Γ' Q S (f a) (f' a')) :
    RelM pkg pm Γ Q S (x >>= f) (x' >>= f') := by
  intro s s' hs
  rw [run_bind_eq, run_bind_eq]
  obtain ⟨h1, Γ', h2, h3⟩ := hx s s' hs
  rcases e : x.run.run s with ⟨r, t⟩
  rcases e' : x'.run.run s' with ⟨r', t'⟩
  rw [e, e'] at h1 h2
  rw [e] at h3
  cases r <;> cases r' <;> simp only at h1 h2 h3 ⊢
  · exact ⟨h1, Γ', h2, fun ⟨_, ha⟩ => nomatch ha⟩
  · exact absurd h1 id
  · exact absurd h1 id
  · exact hf Γ' _ _ (h3 ⟨_, rfl⟩) h1 t t' h2

/-- Bind, when the first computation keeps the scope. -/
theorem relM_bind {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {R : α → α' → Prop} {S : β → β' → Prop} {x : M α} {x' : M α'} {f : α → M β}
    {f' : α' → M β'} (hx : RelM pkg pm Γ (· = Γ) R x x')
    (hf : ∀ a a', R a a' → RelM pkg pm Γ Q S (f a) (f' a')) :
    RelM pkg pm Γ Q S (x >>= f) (x' >>= f') :=
  relM_bind_gen hx fun _ a a' hq h => hq ▸ hf a a' h

theorem relM_tick {pkg : List String} {pm : String → Ren} {Γ : Ren} :
    RelM pkg pm Γ (· = Γ) Eq tick tick := by
  intro ⟨c, n⟩ ⟨c', n'⟩ ⟨hc, hn⟩
  simp only at hn
  subst hn
  cases n with
  | zero => exact ⟨.fuel, Γ, ⟨hc, rfl⟩, fun ⟨_, ha⟩ => nomatch ha⟩
  | succ n => exact ⟨rfl, Γ, ⟨hc, rfl⟩, fun _ => rfl⟩

/-- An `H` step related by results and states, lifted. -/
theorem relM_liftH {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → β → Prop}
    {x : H α} {y : H β}
    (h : ∀ c c', CoreR pkg pm Γ c c' → ResR pkg pm R (x.run.run c).1 (y.run.run c').1 ∧
      CoreR pkg pm Γ (x.run.run c).2 (y.run.run c').2) :
    RelM pkg pm Γ (· = Γ) R (liftM x) (liftM y) := by
  intro s s' ⟨hc, hn⟩
  rw [run_liftM, run_liftM]
  obtain ⟨h1, h2⟩ := h _ _ hc
  exact ⟨h1, Γ, ⟨h2, hn⟩, fun _ => rfl⟩

/-- A read-only `H` step with related results, lifted. -/
theorem relM_liftH_ro {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → β → Prop}
    {x : H α} {y : H β} (hx : ReadOnly x) (hy : ReadOnly y)
    (h : ∀ c c', CoreR pkg pm Γ c c' → ResR pkg pm R (x.run.run c).1 (y.run.run c').1) :
    RelM pkg pm Γ (· = Γ) R (liftM x) (liftM y) :=
  relM_liftH fun c c' hc => ⟨h c c' hc, by rw [hx c, hy c']; exact hc⟩

/-- Weaken what is known of the scope afterwards. -/
theorem RelM.mono {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q Q' : Ren → Prop}
    {R : α → β → Prop} {x : M α} {y : M β} (h : RelM pkg pm Γ Q R x y) (hq : ∀ Γ', Q Γ' → Q' Γ') :
    RelM pkg pm Γ Q' R x y := fun s s' hs => by
  obtain ⟨h1, Γ', h2, h3⟩ := h s s' hs
  exact ⟨h1, Γ', h2, fun ha => hq _ (h3 ha)⟩

/-- `runIn`'s handler: a `return` of related values is related values; any other related
    `Stop` passes on. -/
theorem relM_tryCatch_ret {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {x y : M Val} (h : RelM pkg pm Γ Q (ValR pkg pm) x y) :
    RelM pkg pm Γ (fun _ => True) (ValR pkg pm)
      (tryCatch x fun e => match e with | .ret v => pure v | e => throw e)
      (tryCatch y fun e => match e with | .ret v => pure v | e => throw e) := by
  intro s s' hs
  rw [run_tryCatch_eq, run_tryCatch_eq]
  obtain ⟨h1, Γ', h2, _⟩ := h s s' hs
  rcases e : x.run.run s with ⟨r, t⟩
  rcases e' : y.run.run s' with ⟨r', t'⟩
  rw [e, e'] at h1 h2
  cases r <;> cases r' <;> simp only at h1 h2 ⊢
  · rename_i a b
    cases h1 with
    | ret hv => exact ⟨hv, Γ', h2, fun _ => trivial⟩
    | panic => exact ⟨.panic _ _, Γ', h2, fun ⟨_, ha⟩ => nomatch ha⟩
    | fuel => exact ⟨.fuel, Γ', h2, fun ⟨_, ha⟩ => nomatch ha⟩
    | unsupported => exact ⟨.unsupported _ _ _, Γ', h2, fun ⟨_, ha⟩ => nomatch ha⟩
    | stuck => exact ⟨.stuck _ _, Γ', h2, fun ⟨_, ha⟩ => nomatch ha⟩
  · exact absurd h1 id
  · exact absurd h1 id
  · exact ⟨h1, Γ', h2, fun _ => trivial⟩

/-! ## Scopes -/

theorem envR_length {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    (h : EnvR pkg pm Γ ρ ρ') : ρ.length = Γ.length ∧ ρ'.length = Γ.length := by
  induction h with
  | nil => exact ⟨rfl, rfl⟩
  | cons _ _ _ ih => simp [ih.1, ih.2]

/-- Locals related along `Γ` have exactly `Γ`'s names. -/
theorem envR_names {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    (h : EnvR pkg pm Γ ρ ρ') : Γ = (ρ.map (·.1)).zip (ρ'.map (·.1)) := by
  induction h with
  | nil => rfl
  | cons _ _ _ ih => simp [ih]

theorem envR_append {pkg : List String} {pm : String → Ren} :
    ∀ {Δ Γ : Ren} {ρ ρ' : Env}, EnvR pkg pm (Δ ++ Γ) ρ ρ' →
      ∃ a a' b b', ρ = a ++ b ∧ ρ' = a' ++ b' ∧ a.length = Δ.length ∧ a'.length = Δ.length ∧
        EnvR pkg pm Γ b b'
  | [], _, _, _, h => ⟨[], [], _, _, rfl, rfl, rfl, rfl, h⟩
  | _ :: Δ, _, _, _, .cons hm hv t => by
    obtain ⟨a, a', b, b', rfl, rfl, ha, ha', hb⟩ := envR_append t
    exact ⟨_ :: a, _ :: a', b, b', rfl, rfl, by simp [ha], by simp [ha'], hb⟩

/-- Leaving a scope that pushed `Δ`: the locals are related along the scope before. -/
theorem exitScope_envR {pkg : List String} {pm : String → Ren} {Δ Γ : Ren} {ρ ρ' : Env}
    (h : EnvR pkg pm (Δ ++ Γ) ρ ρ') :
    EnvR pkg pm Γ (exitScope ρ Γ.length) (exitScope ρ' Γ.length) := by
  obtain ⟨a, a', b, b', rfl, rfl, ha, ha', hb⟩ := envR_append h
  have hl := envR_length hb
  unfold exitScope
  simp only [List.length_append, hl.1, hl.2, ha, ha', Nat.add_sub_cancel]
  rw [List.drop_left' ha, List.drop_left' ha']
  exact hb

theorem pushAll_prefix (Γ Δ : Ren) : ∃ P, Γ.pushAll Δ = P ++ Γ := by
  induction Δ generalizing Γ with
  | nil => exact ⟨[], rfl⟩
  | cons xx Δ ih =>
    obtain ⟨P, hP⟩ := ih (Γ.push xx)
    show ∃ P, Ren.pushAll (Γ.push xx) Δ = P ++ Γ
    rw [hP]
    unfold Ren.push
    split
    · exact ⟨P, rfl⟩
    · exact ⟨P ++ [xx], by simp⟩

/-- Binding related names to related values, immutably, in order: the frames a function,
    a closure or a default runs in. -/
theorem envR_foldl {pkg : List String} {pm : String → Ren} :
    ∀ {xs xs' : List (String × Val)} {Γ : Ren} {ρ ρ' : Env},
      Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) xs xs' →
      EnvR pkg pm Γ ρ ρ' →
      EnvR pkg pm (Γ.pushAll ((xs.map (·.1)).zip (xs'.map (·.1))))
        (xs.foldl (fun ρ (xv : String × Val) => bind ρ xv.1 ⟨xv.2, false⟩) ρ)
        (xs'.foldl (fun ρ (xv : String × Val) => bind ρ xv.1 ⟨xv.2, false⟩) ρ')
  | _, _, _, _, _, .nil, h => h
  | (x, v) :: xs, (x', v') :: xs', Γ, ρ, ρ', .cons ⟨hu, hv⟩ t, h => by
    simp only [List.foldl, List.map_cons, List.zip_cons_cons]
    exact envR_foldl t (envR_push h ⟨rfl, hv⟩ hu)

/-- `runIn`: the body runs in related frames, and the caller's related locals come back. -/
theorem relM_runIn {pkg : List String} {pm : String → Ren} {Γ Γf : Ren} {ds ds' : Decls}
    {d d' : Nat} {fr fr' : Env} {b b' : Expr} (hfr : EnvR pkg pm Γf fr fr')
    (hb : RelM pkg pm Γf (fun _ => True) (ValR pkg pm) (eval ds d b) (eval ds' d' b')) :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (runIn ds d fr b) (runIn ds' d' fr' b') := by
  intro s s' ⟨hc, hn⟩
  rw [runIn_run, runIn_run]
  have hs2 : StR pkg pm Γf { s with locals := fr } { s' with locals := fr' } :=
    ⟨⟨hfr, hc.2.1, hc.2.2.1, hc.2.2.2⟩, hn⟩
  obtain ⟨h1, Γ', ⟨hc', hn'⟩, _⟩ := relM_tryCatch_ret hb _ _ hs2
  revert h1 hc' hn'
  generalize (tryCatch (eval ds d b) (fun e => match e with
      | .ret v => pure v
      | e => throw e) : M Val).run.run { s with locals := fr } = p
  generalize (tryCatch (eval ds' d' b') (fun e => match e with
      | .ret v => pure v
      | e => throw e) : M Val).run.run { s' with locals := fr' } = p'
  obtain ⟨r, t⟩ := p
  obtain ⟨r', t'⟩ := p'
  intro h1 hc' hn'
  cases r <;> cases r' <;> simp only at h1 hc' hn' ⊢
  · exact ⟨h1, Γ', ⟨hc', hn'⟩, fun ⟨_, ha⟩ => nomatch ha⟩
  · exact absurd h1 id
  · exact absurd h1 id
  · exact ⟨h1, Γ, ⟨⟨hc.1, hc'.2.1, hc'.2.2.1, hc'.2.2.2⟩, hn'⟩, fun _ => rfl⟩

/-! ## Lifted steps -/

theorem relM_getCore {pkg : List String} {pm : String → Ren} {Γ : Ren} :
    RelM pkg pm Γ (· = Γ) (CoreR pkg pm Γ) (liftM getCore) (liftM getCore) := by
  intro s s' ⟨hc, hn⟩
  rw [run_liftM, run_liftM]
  exact ⟨hc, Γ, ⟨hc, hn⟩, fun _ => rfl⟩

theorem relM_discard {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {R : α → β → Prop} {x : M α} {y : M β} (h : RelM pkg pm Γ Q R x y) :
    RelM pkg pm Γ Q Eq (discard x) (discard y) := by
  simp only [discard, Functor.mapConst]
  exact relM_bind_gen h fun _ _ _ hq _ s s' hs => ⟨rfl, _, hs, fun _ => hq⟩

theorem readOnly_printable (ds : Decls) (vs : List Val) : ReadOnly (printable ds vs) :=
  fun c => by unfold printable; split <;> rfl

theorem readOnly_printed (r : Print.P String) (ds : Decls) : ReadOnly (printed r ds) :=
  fun c => by unfold printed; split <;> (try split) <;> rfl

theorem readOnly_unop (op : String) (v : Val) : ReadOnly (unop op v) :=
  fun c => by unfold unop; split <;> rfl

theorem readOnly_binop (op : String) (a b : Val) : ReadOnly (binop op a b) :=
  fun c => by unfold binop; split <;> (try split) <;> (try split) <;> rfl

theorem readOnly_asBool (v : Val) : ReadOnly (asBool v) := fun c => by cases v <;> rfl

theorem relM_asBool {pkg : List String} {pm : String → Ren} {Γ : Ren} {v v' : Val}
    (hv : ValR pkg pm v v') : RelM pkg pm Γ (· = Γ) Eq (liftM (asBool v)) (liftM (asBool v')) := by
  exact relM_liftH_ro (readOnly_asBool v) (readOnly_asBool v') fun c c' _ => asBool_rel hv c c'

theorem relM_intLit {pkg : List String} {pm : String → Ren} {Γ : Ren} (n : Int) :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (liftM (intLit n)) (liftM (intLit n)) := by
  have hro : ReadOnly (intLit n) := fun c => by unfold intLit; split <;> rfl
  refine relM_liftH_ro hro hro fun c c' _ => ?_
  unfold intLit
  split
  · exact .int _
  · exact .unsupported _ _ _

theorem capR_captured {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    (h : EnvR pkg pm Γ ρ ρ') : CapR pkg pm (captured ρ) (captured ρ') := by
  induction h with
  | nil => exact .nil
  | cons _ hv _ ih => exact .cons hv ih

/-- The names a closure captures are the scope's. -/
theorem envR_names_captured {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    (h : EnvR pkg pm Γ ρ ρ') :
    ((captured ρ).map (·.1)).zip ((captured ρ').map (·.1)) = Γ := by
  rw [envR_names h]
  simp only [captured, List.map_map]
  rfl

theorem relM_forIn {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → β → Prop}
    {f : α → PUnit → M (ForInStep PUnit)} {g : β → PUnit → M (ForInStep PUnit)}
    (hf : ∀ a b, R a b → RelM pkg pm Γ (· = Γ) Eq (f a PUnit.unit) (g b PUnit.unit)) :
    ∀ {xs : List α} {ys : List β}, Fall2 R xs ys →
      RelM pkg pm Γ (· = Γ) Eq (forIn xs PUnit.unit f) (forIn ys PUnit.unit g)
  | _, _, .nil => relM_pure rfl
  | _, _, .cons h t => by
    rw [List.forIn_cons, List.forIn_cons]
    exact relM_bind (hf _ _ h) fun r r' hr => by
      subst hr
      cases r
      · exact relM_pure rfl
      · exact relM_forIn hf t

/-- A `for` loop over related lists, carrying related state. -/
theorem relM_forIn_st {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → β → Prop} {S : σ → σ' → Prop}
    {f : α → σ → M (ForInStep σ)} {g : β → σ' → M (ForInStep σ')}
    (hf : ∀ a b x y, R a b → S x y → RelM pkg pm Γ (· = Γ)
      (fun r r' => match r, r' with
        | .done x, .done y => S x y
        | .yield x, .yield y => S x y
        | _, _ => False) (f a x) (g b y)) :
    ∀ {xs : List α} {ys : List β}, Fall2 R xs ys → ∀ {x : σ} {y : σ'}, S x y →
      RelM pkg pm Γ (· = Γ) S (forIn xs x f) (forIn ys y g)
  | _, _, .nil, _, _, h => by simp only [List.forIn_nil]; exact relM_pure h
  | _, _, .cons hab t, _, _, h => by
    simp only [List.forIn_cons]
    refine relM_bind (hf _ _ _ _ hab h) fun r r' hr => ?_
    cases r <;> cases r' <;> simp only at hr
    · exact relM_pure hr
    · exact relM_forIn_st hf t hr

theorem alphaL_fall2 {pkg : List String} {pm : String → Ren} {Γ : Ren} :
    ∀ {es es' : List Expr}, AlphaL pkg pm Γ es es' → Fall2 (AlphaE pkg pm Γ) es es'
  | _, _, .nil => .nil
  | _, _, .cons h t => .cons h (alphaL_fall2 t)

theorem fall2_zip {R : α → β → Prop} {S : γ → δ → Prop} :
    ∀ {as : List α} {bs : List β} {cs : List γ} {ds : List δ}, Fall2 R as bs → Fall2 S cs ds →
      Fall2 (fun p q => R p.1 q.1 ∧ S p.2 q.2) (as.zip cs) (bs.zip ds)
  | _, _, _, _, .nil, _ => .nil
  | _, _, _, _, .cons _ _, .nil => .nil
  | _, _, _, _, .cons h t, .cons h' t' => .cons ⟨h, h'⟩ (fall2_zip t t')

theorem fall2_of_valsR {pkg : List String} {pm : String → Ren} :
    ∀ {vs vs' : List Val}, ValsR pkg pm vs vs' → Fall2 (ValR pkg pm) vs vs'
  | _, _, .nil => .nil
  | _, _, .cons h t => .cons h (fall2_of_valsR t)

theorem relM_assignTo {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    (hs : SameLayout ds ds') {t t' : Expr} {v v' : Val} (ht : AlphaE pkg pm Γ t t')
    (hv : ValR pkg pm v v') :
    RelM pkg pm Γ (· = Γ) Eq (liftM (assignTo ds t v)) (liftM (assignTo ds' t' v')) :=
  relM_liftH fun _ _ hc => assignTo_rel hs hc ht hv

theorem relM_readPlace {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    (hs : SameLayout ds ds') {t t' : Expr} (ht : AlphaE pkg pm Γ t t') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (liftM (readPlace ds t)) (liftM (readPlace ds' t')) :=
  relM_liftH_ro (readOnly_readPlace _ _) (readOnly_readPlace _ _) fun _ _ hc =>
    readPlace_rel hs hc ht

theorem relM_binop {pkg : List String} {pm : String → Ren} {Γ : Ren} (op : String)
    {a a' b b' : Val} (ha : ValR pkg pm a a') (hb : ValR pkg pm b b') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (liftM (binop op a b)) (liftM (binop op a' b')) :=
  relM_liftH_ro (readOnly_binop _ _ _) (readOnly_binop _ _ _) fun c c' _ =>
    binop_rel op c c' ha hb

/-! ## More combinators -/

/-- Weaken the relation on results. -/
theorem RelM.res {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {R R' : α → β → Prop} {x : M α} {y : M β} (h : RelM pkg pm Γ Q R x y)
    (hr : ∀ a b, R a b → R' a b) : RelM pkg pm Γ Q R' x y := fun s s' hs => by
  obtain ⟨h1, Γ', h2, h3⟩ := h s s' hs
  refine ⟨?_, Γ', h2, h3⟩
  revert h1
  cases (x.run.run s).1 <;> cases (y.run.run s').1 <;> simp only [ResR] <;>
    first | exact id | exact hr _ _

/-- An `H` step that stops on both sides. -/
theorem relM_stop {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {R : α → α' → Prop} {x : H α} {x' : H α'}
    (h : ∀ c c', ∃ e e', x.run.run c = (.error e, c) ∧ x'.run.run c' = (.error e', c') ∧
      StopR pkg pm e e') :
    RelM pkg pm Γ Q R (liftM x) (liftM x') := by
  intro s s' hs
  rw [run_liftM, run_liftM]
  obtain ⟨e, e', h1, h2, he⟩ := h s.toCore s'.toCore
  rw [h1, h2]
  exact ⟨he, Γ, hs, fun ⟨_, ha⟩ => nomatch ha⟩

/-- An `H` step that stops on both sides, leaving the state as it was: whatever follows it
    is never run. -/
theorem relM_stop_bind {pkg : List String} {pm : String → Ren} {Γ : Ren} {Q : Ren → Prop}
    {S : β → β' → Prop} {x : H α} {x' : H α'} {f : α → M β} {f' : α' → M β'}
    (h : ∀ c c', ∃ e e', x.run.run c = (.error e, c) ∧ x'.run.run c' = (.error e', c') ∧
      StopR pkg pm e e') :
    RelM pkg pm Γ Q S (liftM x >>= f) (liftM x' >>= f') :=
  relM_bind_gen (relM_stop (Q := fun _ => True) (R := fun (_ : α) (_ : α') => False) h)
    fun _ _ _ _ h => h.elim

theorem fall2_map {R : α → β → Prop} {S : γ → δ → Prop} {f : α → γ} {g : β → δ}
    (h : ∀ a b, R a b → S (f a) (g b)) :
    ∀ {l : List α} {l' : List β}, Fall2 R l l' → Fall2 S (l.map f) (l'.map g)
  | _, _, .nil => .nil
  | _, _, .cons hr t => .cons (h _ _ hr) (fall2_map h t)

/-! ## The evaluator at one depth, from `eval` at that depth

Every function of `eval`'s mutual block other than `eval` calls `eval` only at its own
depth and calls nothing at a greater one. So each is related on related inputs once `eval`
is, at that depth (`EvalIH`), and the induction over `eval` is an induction on the depth. -/

/-- `eval` at depth `d` relates related expressions, and related declarations (a block's
    statements), after which the declared names are in scope. -/
structure EvalIH (pkg : List String) (pm : String → Ren) (ds ds' : Decls) (d : Nat) : Prop where
  expr : ∀ {Γ : Ren} {e e' : Expr}, AlphaE pkg pm Γ e e' →
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds d e) (eval ds' d e')
  decl : ∀ {Γ : Ren} (m : Mut) {ns ns' : List String} (destr : Bool) (ty : Option Ty)
    {vs vs' : List Expr}, NamesR pkg ns ns' → AlphaL pkg pm Γ vs vs' →
    RelM pkg pm Γ (· = Γ.pushAll (ns.zip ns')) (ValR pkg pm)
      (eval ds d (.decl m ns destr ty vs)) (eval ds' d (.decl m ns' destr ty vs'))

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {d : Nat}

theorem evalList_alpha_of (ih : EvalIH pkg pm ds ds' d) :
    ∀ {Γ : Ren} {es es' : List Expr}, Fall2 (AlphaE pkg pm Γ) es es' →
      RelM pkg pm Γ (· = Γ) (ValsR pkg pm) (evalList ds d es) (evalList ds' d es')
  | _, _, _, .nil => by rw [evalList.eq_1, evalList.eq_1]; exact relM_pure .nil
  | _, _, _, .cons he hes => by
    rw [evalList.eq_2, evalList.eq_2]
    exact relM_bind (ih.expr he) fun _ _ hv =>
      relM_bind (evalList_alpha_of ih hes) fun _ _ hvs => relM_pure (.cons hv hvs)

/-! ### The cases of `eval` -/

theorem eval_paren_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {x x' : Expr}
    (h : AlphaE pkg pm Γ x x') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.paren x)) (eval ds' (d + 1) (.paren x')) := by
  rw [eval.eq_6, eval.eq_6]
  exact relM_bind relM_tick fun _ _ _ => ih.expr h

theorem eval_lit_alpha {Γ : Ren} (l : Lit) :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.lit l)) (eval ds' (d + 1) (.lit l)) := by
  cases l with
  | int n => rw [eval.eq_2, eval.eq_2]; exact relM_bind relM_tick fun _ _ _ => relM_intLit n
  | bool b => rw [eval.eq_3, eval.eq_3]; exact relM_bind relM_tick fun _ _ _ => relM_pure (.bool b)
  | str s => rw [eval.eq_4, eval.eq_4]; exact relM_bind relM_tick fun _ _ _ => relM_pure (.str s)

theorem eval_unsupported_alpha {Γ : Ren} (k : String) :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.unsupported k))
      (eval ds' (d + 1) (.unsupported k)) := by
  rw [eval.eq_5, eval.eq_5]
  exact relM_bind relM_tick fun _ _ _ => relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩

theorem readOnly_unresolved (ds : Decls) (x : String) : ReadOnly (unresolved ds x : H α) :=
  fun _ => by unfold unresolved; split <;> rfl

theorem eval_name_alpha (hD : DeclsR pkg pm ds ds') {Γ : Ren} {x x' : String}
    (hx : NameR Γ x x') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.name x)) (eval ds' (d + 1) (.name x')) := by
  rw [eval.eq_7, eval.eq_7]
  refine relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c c' hc => ?_
  have hr := resolve_rel hD hc.1 hc.2.1 hx
  generalize resolve ds c.locals c.globals x = r at hr ⊢
  generalize resolve ds' c'.locals c'.globals x' = r' at hr ⊢
  cases r <;> cases r' <;> simp only [RefR] at hr <;> (try exact absurd hr id)
  · exact relM_liftH_ro (readOnly_deref _) (readOnly_deref _) fun _ _ hc => deref_rel hc.2.2.1 hr.2
  · exact relM_pure (.fn hr)
  · exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
  · obtain ⟨rfl, hv⟩ := hr
    obtain rfl := VarR.eq hv
    dsimp only
    split
    · exact relM_pure (ValR.sealed _ _ .nil)
    · exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
  · exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
  · refine relM_liftH_ro (readOnly_unresolved _ _) (readOnly_unresolved _ _) fun _ _ _ => ?_
    unfold unresolved
    rw [← hD.unsupported]
    split <;> exact .unsupported _ _ _

theorem eval_tuple_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {es es' : List Expr}
    (h : AlphaL pkg pm Γ es es') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.tuple es)) (eval ds' (d + 1) (.tuple es')) := by
  rw [eval.eq_15, eval.eq_15]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (evalList_alpha_of ih (alphaL_fall2 h))
    fun _ _ hv => relM_pure (.tuple hv)

theorem eval_select_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {t t' : Expr} (f : String) (h : AlphaE pkg pm Γ t t') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.select t f))
      (eval ds' (d + 1) (.select t' f)) := by
  rw [eval.eq_17, eval.eq_17]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr h) fun _ _ hv =>
    relM_liftH_ro (readOnly_getField _ _ _) (readOnly_getField _ _ _)
      fun _ _ _ => getField_rel hD.same f hv

theorem eval_ifExpr_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {c c' a a' b b' : Expr}
    (hc : AlphaE pkg pm Γ c c') (ha : AlphaE pkg pm Γ a a') (hb : AlphaE pkg pm Γ b b') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.ifExpr c a b))
      (eval ds' (d + 1) (.ifExpr c' a' b')) := by
  rw [eval.eq_22, eval.eq_22]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hc) fun _ _ hv =>
    relM_bind (relM_asBool hv) fun b _ h => by
      subst h; cases b
      · exact ih.expr hb
      · exact ih.expr ha

theorem eval_ifStmt_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {c c' a a' : Expr}
    {b b' : Option Expr} (hc : AlphaE pkg pm Γ c c') (ha : AlphaE pkg pm Γ a a')
    (hb : OptR pkg pm Γ b b') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.ifStmt c a b))
      (eval ds' (d + 1) (.ifStmt c' a' b')) := by
  cases hb with
  | some hb =>
    rw [eval.eq_23, eval.eq_23]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hc) fun _ _ hv =>
      relM_bind (relM_asBool hv) fun b _ h => by
        subst h; cases b
        · exact ih.expr hb
        · exact ih.expr ha
  | none =>
    rw [eval.eq_24, eval.eq_24]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hc) fun _ _ hv =>
      relM_bind (relM_asBool hv) fun b _ h => by
        subst h; cases b
        · exact relM_pure .unit
        · exact relM_bind (relM_discard (ih.expr ha)) fun _ _ _ => relM_pure .unit

theorem eval_ret_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {v v' : Option Expr}
    (h : OptR pkg pm Γ v v') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.ret v)) (eval ds' (d + 1) (.ret v')) := by
  cases h with
  | none => rw [eval.eq_27, eval.eq_27]; exact relM_bind relM_tick fun _ _ _ => relM_throw (.ret .unit)
  | some h =>
    rw [eval.eq_28, eval.eq_28]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr h) fun _ _ hv => relM_throw (.ret hv)

theorem eval_interp_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {es es' : List Expr} (h : AlphaL pkg pm Γ es es') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.interp es))
      (eval ds' (d + 1) (.interp es')) := by
  rw [eval.eq_16, eval.eq_16]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (evalList_alpha_of ih (alphaL_fall2 h))
    fun _ _ hv => relM_bind (relM_liftH_ro (readOnly_printable _ _) (readOnly_printable _ _)
      fun c c' _ => printable_rel hD.same hv c c')
      fun _ _ _ => relM_bind (relM_liftH_ro (readOnly_printed _ _) (readOnly_printed _ _)
        fun c c' _ => by
          rw [show Print.interp ds _ = Print.interp ds' _ from by
            unfold Print.interp; rw [fmtVs_rel hD.same true hv]]
          exact printed_rel hD.same _ c c') fun _ _ h => by subst h; exact relM_pure (.str _)

theorem eval_lambda_alpha {Γ : Ren} {ps ps' : List Param} (r : Option Ty) {b b' : Expr}
    (htys : ps.map (·.type) = ps'.map (·.type))
    (hns : NamesR pkg (ps.map (·.name)) (ps'.map (·.name)))
    (hb : AlphaE pkg pm (Γ.pushAll ((ps.map (·.name)).zip (ps'.map (·.name)))) b b') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.lambda ps r b))
      (eval ds' (d + 1) (.lambda ps' r b')) := by
  rw [eval.eq_9, eval.eq_9]
  exact relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c c' hc =>
    relM_pure (.closure (capR_captured hc.1) htys hns (by rw [envR_names_captured hc.1]; exact hb))

theorem eval_unary_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} (op : String) {x x' : Expr}
    (h : AlphaE pkg pm Γ x x') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.unary op x))
      (eval ds' (d + 1) (.unary op x')) := by
  by_cases hn : ∃ n, op = "-" ∧ x = .lit (.int n)
  · obtain ⟨n, rfl, rfl⟩ := hn
    cases h
    rw [eval.eq_10, eval.eq_10]
    exact relM_bind relM_tick fun _ _ _ => relM_intLit _
  · have hn' : ∀ n, op = "-" → x' = .lit (.int n) → False := fun n h1 h2 => by
      subst h2; cases h; exact hn ⟨n, h1, rfl⟩
    rw [eval.eq_11 _ _ _ _ (fun n h1 h2 => hn ⟨n, h1, h2⟩), eval.eq_11 _ _ _ _ hn']
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr h) fun _ _ hv =>
      relM_liftH_ro (readOnly_unop _ _) (readOnly_unop _ _) fun c c' _ => unop_rel _ c c' hv

theorem eval_binary_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} (op : String)
    {l l' r r' : Expr} (hl : AlphaE pkg pm Γ l l') (hr : AlphaE pkg pm Γ r r') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.binary op l r))
      (eval ds' (d + 1) (.binary op l' r')) := by
  by_cases h1 : op = "&&"
  · subst h1
    rw [eval.eq_12, eval.eq_12]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hl) fun _ _ ha =>
      relM_bind (relM_asBool ha) fun b _ h => by
        subst h; cases b
        · exact relM_pure (.bool false)
        · exact relM_bind (ih.expr hr) fun _ _ hb =>
            relM_bind (relM_asBool hb) fun _ _ h => by subst h; exact relM_pure (.bool _)
  by_cases h2 : op = "||"
  · subst h2
    rw [eval.eq_13, eval.eq_13]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hl) fun _ _ ha =>
      relM_bind (relM_asBool ha) fun b _ h => by
        subst h; cases b
        · exact relM_bind (ih.expr hr) fun _ _ hb =>
            relM_bind (relM_asBool hb) fun _ _ h => by subst h; exact relM_pure (.bool _)
        · exact relM_pure (.bool true)
  rw [eval.eq_14 _ _ _ _ _ h1 h2, eval.eq_14 _ _ _ _ _ h1 h2]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hl) fun _ _ ha =>
    relM_bind (ih.expr hr) fun _ _ hb => relM_binop op ha hb

theorem eval_incdec_alpha (hD : DeclsR pkg pm ds ds') {Γ : Ren} (op : String) {t t' : Expr}
    (ht : AlphaE pkg pm Γ t t') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.incdec op t))
      (eval ds' (d + 1) (.incdec op t')) := by
  rw [eval.eq_33, eval.eq_33]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (relM_readPlace hD.same ht) fun _ _ hv =>
    relM_bind (relM_binop _ hv (.int 1)) fun _ _ hw =>
      relM_bind (relM_assignTo hD.same ht hw) fun _ _ _ => relM_pure .unit

theorem eval_assign_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    (op : String) {ts ts' vs vs' : List Expr} (hts : AlphaL pkg pm Γ ts ts')
    (hvs : AlphaL pkg pm Γ vs vs') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.assign op ts vs))
      (eval ds' (d + 1) (.assign op ts' vs')) := by
  by_cases h1 : op = "="
  · subst h1
    rw [eval.eq_30, eval.eq_30]
    have hlt := fall2_length (alphaL_fall2 hts)
    refine relM_bind relM_tick fun _ _ _ =>
      relM_bind (evalList_alpha_of ih (alphaL_fall2 hvs)) fun vs vs' hv => ?_
    have hz := fall2_zip (alphaL_fall2 hts) (fall2_of_valsR hv)
    rw [valsR_length hv, hlt]
    split
    · exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
    · refine relM_bind (relM_forIn (fun p q h => ?_) hz) fun _ _ _ => relM_pure .unit
      exact relM_bind (relM_assignTo hD.same h.1 h.2) fun _ _ _ => relM_pure rfl
  · by_cases h2 : ∃ t x, ts = [t] ∧ vs = [x]
    · obtain ⟨t, x, rfl, rfl⟩ := h2
      cases hts with
      | cons ht h0 =>
        cases h0
        cases hvs with
        | cons hx h0 =>
          cases h0
          rw [eval.eq_31 _ _ _ _ _ h1, eval.eq_31 _ _ _ _ _ h1]
          exact relM_bind relM_tick fun _ _ _ => relM_bind (relM_readPlace hD.same ht) fun _ _ hc =>
            relM_bind (ih.expr hx) fun _ _ hv => relM_bind (relM_binop _ hc hv) fun _ _ hw =>
              relM_bind (relM_assignTo hD.same ht hw) fun _ _ _ => relM_pure .unit
    · have h2' : ∀ t x, ts' = [t] → vs' = [x] → False := fun t x e1 e2 => by
        subst e1 e2
        cases hts with | cons _ h0 => cases h0; cases hvs with | cons _ h0 => cases h0; exact h2 ⟨_, _, rfl, rfl⟩
      rw [eval.eq_32 _ _ _ _ _ h1 (fun t x e1 e2 => h2 ⟨t, x, e1, e2⟩), eval.eq_32 _ _ _ _ _ h1 h2']
      exact relM_bind relM_tick fun _ _ _ => relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩


end

/-! ## Helpers for the remaining cases -/

section
variable {pkg : List String} {pm : String → Ren}

theorem binderR_us {x x' : String} (h : BinderR pkg x x') : x = "_" ↔ x' = "_" := by
  rcases h with rfl | ⟨h1, h2, _⟩
  · exact Iff.rfl
  · exact ⟨fun e => absurd e h1, fun e => absurd e h2⟩

theorem namesR_fall2 : ∀ {ns ns' : List String}, NamesR pkg ns ns' →
    Fall2 (fun a b => a = "_" ↔ b = "_") ns ns'
  | [], [], _ => .nil
  | a :: as, b :: bs, ⟨hl, hp⟩ =>
    .cons (binderR_us (hp (a, b) (by simp)))
      (namesR_fall2 ⟨by simpa using hl, fun p hp' => hp p (by
        simp only [List.zip_cons_cons]; exact List.mem_cons_of_mem _ hp')⟩)
  | [], _ :: _, ⟨hl, _⟩ | _ :: _, [], ⟨hl, _⟩ => by simp at hl

theorem fall2_refl {R : α → α → Prop} (h : ∀ a, R a a) : ∀ (l : List α), Fall2 R l l
  | [] => .nil
  | a :: l => .cons (h a) (fall2_refl h l)

theorem map_fst_zip_take : ∀ (ns : List String) (vs : List Val),
    (ns.zip vs).map (·.1) = ns.take vs.length
  | [], _ => by simp
  | _ :: _, [] => by simp
  | n :: ns, v :: vs => by simp [map_fst_zip_take ns vs]

/-- A declaration's bindings: the declared names, with related values. -/
def BindsR (pkg : List String) (pm : String → Ren) (ns ns' : List String)
    (bs bs' : List (String × Val)) : Prop :=
  Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) bs bs' ∧
    bs.map (·.1) = ns ∧ bs'.map (·.1) = ns'

/-- A declaration's bindings `names.zip vs`. -/
theorem bindsR_zip {ns ns' : List String} {vs vs' : List Val} (hn : NamesR pkg ns ns')
    (hv : ValsR pkg pm vs vs') (hl : vs.length = ns.length) :
    BindsR pkg pm ns ns' (ns.zip vs) (ns'.zip vs') := by
  have hl' : vs'.length = ns'.length := by rw [← valsR_length hv, hl, hn.1]
  exact ⟨fall2_zip (namesR_fall2 hn) (fall2_of_valsR hv), List.map_fst_zip (Nat.le_of_eq hl.symm),
    List.map_fst_zip (Nat.le_of_eq hl'.symm)⟩

theorem bindsR_map {ns ns' : List String} {z z' : Val} (hn : NamesR pkg ns ns')
    (hz : ValR pkg pm z z') :
    BindsR pkg pm ns ns' (ns.map (·, z)) (ns'.map (·, z')) := by
  refine ⟨?_, by simp [Function.comp_def], by simp [Function.comp_def]⟩
  have := namesR_fall2 hn
  clear hn
  induction this with
  | nil => exact .nil
  | cons h _ ih => exact .cons ⟨h, hz⟩ ih

/-- An argument that is not named is its own value expression. -/
theorem unnamed_of_not_named {e : Expr} (h : isNamed e = false) : unnamed e = e := by
  cases e <;> first | rfl | simp [isNamed] at h

/-- Each argument's value expression, related. -/
theorem argsR_unnamed {m Γ : Ren} : ∀ {as as' : List Expr}, ArgsR pkg pm m Γ as as' →
    Fall2 (fun a a' => AlphaE pkg pm Γ (unnamed a) (unnamed a')) as as'
  | _, _, .nil => .nil
  | _, _, .pos (e := e) (e' := e') hn he t => by
    have hn' := alphaE_not_named he
    refine .cons ?_ (argsR_unnamed t)
    rw [unnamed_of_not_named hn, unnamed_of_not_named hn']
    exact he
  | _, _, .named _ he t => .cons he (argsR_unnamed t)

theorem argsR_any_named {m Γ : Ren} : ∀ {as as' : List Expr}, ArgsR pkg pm m Γ as as' →
    as.any isNamed = as'.any isNamed
  | _, _, .nil => rfl
  | _, _, .pos hn he t => by
    simp only [List.any_cons, hn, alphaE_not_named he, Bool.false_or]; exact argsR_any_named t
  | _, _, .named _ _ _ => by simp [isNamed]

/-- Without named arguments, the parameter renaming a call's arguments follow does not
    matter. -/
theorem argsR_retarget {m Γ : Ren} (m' : Ren) : ∀ {as as' : List Expr}, as.any isNamed = false →
    ArgsR pkg pm m Γ as as' → ArgsR pkg pm m' Γ as as'
  | _, _, _, .nil => .nil
  | _, _, h, .pos hn he t => .pos hn he (argsR_retarget m' (by simp_all) t)
  | _, _, h, .named _ _ _ => by simp [isNamed] at h

theorem argsR_pos {m Γ : Ren} : ∀ {as as' : List Expr}, as.any isNamed = false →
    ArgsR pkg pm m Γ as as' → Fall2 (AlphaE pkg pm Γ) as as'
  | _, _, _, .nil => .nil
  | _, _, h, .pos _ he t => .cons he (argsR_pos (by simp_all) t)
  | _, _, h, .named _ _ _ => by simp [isNamed] at h

/-- An optional value, related. -/
def OptValR (pkg : List String) (pm : String → Ren) : Option Val → Option Val → Prop
  | none, none => True
  | some v, some v' => ValR pkg pm v v'
  | _, _ => False

theorem lookup_zip_rel : ∀ (slots : List Nat) {vs vs' : List Val}, ValsR pkg pm vs vs' →
    ∀ j, OptValR pkg pm ((slots.zip vs).lookup j) ((slots.zip vs').lookup j)
  | [], _, _, _, _ => trivial
  | _ :: _, _, _, .nil, _ => trivial
  | k :: ks, _, _, .cons hv t, j => by
    simp only [List.zip_cons_cons, List.lookup_cons]
    split
    · exact hv
    · exact lookup_zip_rel ks t j

/-- A closure's captured locals, as a scope again. -/
theorem envR_capturedEnv : ∀ {env env' : List (String × Val)}, CapR pkg pm env env' →
    EnvR pkg pm ((env.map (·.1)).zip (env'.map (·.1))) (capturedEnv env) (capturedEnv env')
  | _, _, .nil => .nil
  | _, _, .cons hv t => by
    refine .cons ?_ hv (envR_capturedEnv t)
    cases hv <;> rfl

/-- A frame binding parameters to values, related. `F` and `G` are the evaluator's steps,
    which bind the parameter's name immutably. -/
theorem envR_foldl_params {F G : Env → Param × Val → Env}
    (hF : ∀ ρ p v, F ρ (p, v) = bind ρ p.name ⟨v, false⟩)
    (hG : ∀ ρ p v, G ρ (p, v) = bind ρ p.name ⟨v, false⟩) :
    ∀ {ps ps' : List Param} {vs vs' : List Val} {Γ : Ren} {ρ ρ' : Env},
      Fall2 (fun a b => a = "_" ↔ b = "_") (ps.map (·.name)) (ps'.map (·.name)) →
      ValsR pkg pm vs vs' → vs.length = ps.length → EnvR pkg pm Γ ρ ρ' →
      EnvR pkg pm (Γ.pushAll ((ps.map (·.name)).zip (ps'.map (·.name))))
        ((ps.zip vs).foldl F ρ) ((ps'.zip vs').foldl G ρ')
  | [], [], _, _, _, _, _, _, _, _, h => h
  | p :: ps, p' :: ps', v :: vs, v' :: vs', Γ, ρ, ρ', .cons hu hn, .cons hv hvs, hl, h => by
    simp only [List.zip_cons_cons, List.foldl_cons, List.map_cons, hF, hG]
    exact envR_foldl_params hF hG hn hvs (by simpa using hl) (envR_push h ⟨rfl, hv⟩ hu)
  | [], _ :: _, _, _, _, _, _, hn, _, _, _ | _ :: _, [], _, _, _, _, _, hn, _, _, _ => nomatch hn
  | _ :: _, _ :: _, [], _, _, _, _, _, hv, hl, _ => by simp at hl
  | _ :: _, _ :: _, _ :: _, [], _, _, _, _, hv, _, _ => nomatch hv

/-- A function's default's frame: the parameters before it, bound to related values. -/
theorem envR_defaultFrame {names names' : List String} {vals vals' : List Val}
    (hn : NamesR pkg names names') (hv : ValsR pkg pm vals vals') :
    EnvR pkg pm (Ren.pushAll [] ((pairsOf names names').take vals.length))
      (defaultFrame (names.zip vals)) (defaultFrame (names'.zip vals')) := by
  have h := envR_foldl (pkg := pkg) (pm := pm) (Γ := []) (ρ := []) (ρ' := [])
    (fall2_zip (namesR_fall2 hn) (fall2_of_valsR hv)) .nil
  have hl := valsR_length hv
  rw [map_fst_zip_take, map_fst_zip_take, ← hl] at h
  rw [show ∀ j (as bs : List String), (as.take j).zip (bs.take j) = (as.zip bs).take j from
    fun _ _ _ => by simp only [List.zip, List.take_zipWith]] at h
  exact h

/-- The default-value frames of a call or construction, related: for each parameter `j`,
    once the parameters before it have related values, its default's frames are related and
    so are its defaults. -/
def DfltR (pkg : List String) (pm : String → Ren) (fs : Bool) (names names' : List String)
    (dflts dflts' : List (Option Expr)) : Prop :=
  ∀ j (vals vals' : List Val), ValsR pkg pm vals vals' → vals.length = j →
    ∃ Γj, EnvR pkg pm Γj (if fs then [] else defaultFrame (names.zip vals))
        (if fs then [] else defaultFrame (names'.zip vals')) ∧
      OptR pkg pm Γj (dflts.getD j none) (dflts'.getD j none)

theorem dfltR_none {names names' : List String} (hn : NamesR pkg names names') :
    DfltR pkg pm false names names' [] [] := fun j _ _ hv hl =>
  ⟨_, envR_defaultFrame hn hv, by simp only [List.getD_nil]; exact .none⟩

theorem readOnly_byNameOk (ds : Decls) (fr : Env) (ty : Option Ty) (e : Expr) :
    ReadOnly (byNameOk ds fr ty e) := fun c => by rw [byNameOk_run]

theorem readOnly_construct (ds : Decls) (name : String) (fs : List Field) (vs : List Val) :
    ReadOnly (construct ds name fs vs) := by
  unfold construct
  refine readOnly_bind (PureH.readOnly (pureH_forIn fs fun f => pureH_bind (pureH_bind
    (pureH_checkOptTy ds _ _) fun _ => pureH_pure _) fun _ => pureH_pure _)) fun _ => ?_
  intro c
  split <;> rfl

theorem relM_checkTy {Γ : Ren} {ds ds' : Decls} (hs : SameLayout ds ds') (t : Ty) :
    RelM pkg pm Γ (· = Γ) Eq (liftM (checkTy ds t)) (liftM (checkTy ds' t)) :=
  relM_liftH_ro (pureH_checkTy ds t).readOnly (pureH_checkTy ds' t).readOnly fun c c' _ => by
    rw [← checkTy_congr hs t, (pureH_checkTy ds t c c').1]
    exact checkTy_self ds c' t

theorem relM_checkOptTy {Γ : Ren} {ds ds' : Decls} (hs : SameLayout ds ds') (w w' : String)
    (t : Option Ty) :
    RelM pkg pm Γ (· = Γ) Eq (liftM (checkOptTy ds w t)) (liftM (checkOptTy ds' w' t)) := by
  cases t with
  | none => exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
  | some t => exact relM_checkTy hs t

theorem print_go_rel (isStr : Val → Bool) (hi : ∀ v v', ValR pkg pm v v' → isStr v = isStr v') :
    ∀ {vs vs' : List Val} (ss : List String) {p p' : Option Val}, ValsR pkg pm vs vs' →
      OptValR pkg pm p p' → Print.print.go isStr vs ss p = Print.print.go isStr vs' ss p'
  | _, _, [], _, _, .nil, _ => rfl
  | _, _, _ :: _, _, _, .nil, _ => rfl
  | _, _, [], _, _, .cons _ _, _ => rfl
  | _, _, s :: ss, p, p', .cons hv t, hp => by
    simp only [Print.print.go]
    rw [print_go_rel isStr hi ss t (show OptValR pkg pm (some _) (some _) from hv)]
    cases p <;> cases p' <;> simp only [OptValR] at hp
    · rfl
    · dsimp only
      rw [hi _ _ hp, hi _ _ hv]

theorem print_rel {ds ds' : Decls} (hs : SameLayout ds ds') (nl : Bool) {vs vs' : List Val}
    (hv : ValsR pkg pm vs vs') :
    (if nl then Print.println ds vs else Print.print ds vs) =
      (if nl then Print.println ds' vs' else Print.print ds' vs') := by
  cases nl
  · simp only [Bool.false_eq_true, ↓reduceIte, Print.print.eq_1]
    rw [fmtVs_rel hs true hv]
    congr 1
    funext strs
    exact congrArg pure (print_go_rel _ (fun v v' h => by cases h <;> rfl) strs hv
      (show OptValR pkg pm none none from trivial))
  · simp only [↓reduceIte, Print.println]
    rw [fmtVs_rel hs true hv]

theorem funcs_name {ds ds' : Decls} (hD : DeclsR pkg pm ds ds') {x : String} {f : Func}
    (h : ds.funcs.lookup x = some f) : f.name = x := by
  have hm := mem_of_lookup h
  have hD' := hD.funcs
  generalize ds.funcs = l at hm hD'
  generalize ds'.funcs = l' at hD'
  induction hD' with
  | nil => simp at hm
  | cons hab _ ih =>
    rcases List.mem_cons.mp hm with rfl | hm
    · rw [hab.2.1]
    · exact ih hm

theorem resolve_func {ds : Decls} {l g : Env} {x : String} {f : Func}
    (h : resolve ds l g x = .func f) : ds.funcs.lookup x = some f := by
  unfold resolve at h
  split at h
  · cases h
  · split at h
    · cases h; assumption
    · split at h
      · cases h
      · split at h
        · cases h
        · split at h <;> (try split at h) <;> cases h

theorem resolve_noFunc {ds : Decls} {l g : Env} {x : String}
    (h : ∀ f, resolve ds l g x ≠ .func f) (hv : ∀ b, resolve ds l g x ≠ .var b) :
    ds.funcs.lookup x = none := by
  cases hl : lookupVar l g x with
  | some b => exact absurd (by unfold resolve; rw [hl]) (hv b)
  | none =>
    cases hf : ds.funcs.lookup x with
    | none => rfl
    | some f => exact absurd (by unfold resolve; rw [hl, hf]) (h f)

theorem resolve_var {ds : Decls} {l g : Env} {x : String} {b : Binding} (h : lookup l x = some b) :
    resolve ds l g x = .var b := by
  unfold resolve lookupVar
  rw [h]
  rfl

theorem relM_map {Γ : Ren} {R : α → α' → Prop} {S : β → β' → Prop} {x : M α} {x' : M α'}
    {f : α → β} {f' : α' → β'} (h : RelM pkg pm Γ (· = Γ) R x x')
    (hf : ∀ a a', R a a' → S (f a) (f' a')) : RelM pkg pm Γ (· = Γ) S (f <$> x) (f' <$> x') := by
  rw [← bind_pure_comp, ← bind_pure_comp]
  exact relM_bind h fun a a' h => relM_pure (hf a a' h)

theorem relM_pushAll {Γ : Ren} (m : Bool) {bs bs' : List (String × Val)}
    (h : Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) bs bs') :
    RelM pkg pm Γ (· = Γ.pushAll ((bs.map (·.1)).zip (bs'.map (·.1)))) Eq
      (liftM (pushAll bs m)) (liftM (pushAll bs' m)) := by
  intro s s' ⟨hc, hn⟩
  rw [run_liftM, run_liftM]
  obtain ⟨h1, h2⟩ := pushAll_rel m h hc
  exact ⟨h1, _, ⟨h2, hn⟩, fun _ => rfl⟩

theorem relM_popTo {P Γ : Ren} :
    RelM pkg pm (P ++ Γ) (· = Γ) Eq (liftM (popTo Γ.length)) (liftM (popTo Γ.length)) := by
  intro s s' ⟨hc, hn⟩
  rw [run_liftM, run_liftM, popTo_run, popTo_run]
  exact ⟨rfl, Γ, ⟨⟨exitScope_envR hc.1, hc.2.1, hc.2.2.1, hc.2.2.2⟩, hn⟩, fun _ => rfl⟩

/-- What a computation does from two given related states. `RelM` is this from every pair of
    related states. -/
def RelAt (pkg : List String) (pm : String → Ren) (Q : Ren → Prop) (R : α → β → Prop)
    (x : M α) (y : M β) (s s' : St) : Prop :=
  ResR pkg pm R (x.run.run s).1 (y.run.run s').1 ∧
    ∃ Γ', StR pkg pm Γ' (x.run.run s).2 (y.run.run s').2 ∧ ((∃ a, (x.run.run s).1 = .ok a) → Q Γ')

/-- Reading the state: the rest may use that the state read is the one it runs in. -/
theorem relM_getCore_at {Γ : Ren} {Q : Ren → Prop} {S : β → β' → Prop} {f : Core → M β}
    {f' : Core → M β'}
    (h : ∀ s s', StR pkg pm Γ s s' → RelAt pkg pm Q S (f s.toCore) (f' s'.toCore) s s') :
    RelM pkg pm Γ Q S (liftM getCore >>= f) (liftM getCore >>= f') := by
  intro s s' hs
  rw [run_bind_eq, run_bind_eq, run_liftM, run_liftM]
  exact h s s' hs

theorem relAt_bind_ro {Γ : Ren} {Q : Ren → Prop} {R : α → α' → Prop} {S : β → β' → Prop}
    {x : H α} {x' : H α'} {f : α → M β} {f' : α' → M β'} {s s' : St} (hs : StR pkg pm Γ s s')
    (hro : ReadOnly x) (hro' : ReadOnly x')
    (hx : ResR pkg pm R (x.run.run s.toCore).1 (x'.run.run s'.toCore).1)
    (hf : ∀ a a', R a a' → RelAt pkg pm Q S (f a) (f' a') s s') :
    RelAt pkg pm Q S (liftM x >>= f) (liftM x' >>= f') s s' := by
  unfold RelAt
  rw [run_bind_eq, run_bind_eq, run_liftM, run_liftM]
  rcases h : x.run.run s.toCore with ⟨r, t⟩
  rcases h' : x'.run.run s'.toCore with ⟨r', t'⟩
  have ht : t = s.toCore := by have := hro s.toCore; rw [h] at this; exact this
  have ht' : t' = s'.toCore := by have := hro' s'.toCore; rw [h'] at this; exact this
  subst ht ht'
  rw [h, h'] at hx
  cases r <;> cases r' <;> simp only at hx ⊢
  · exact ⟨hx, Γ, hs, fun ⟨_, ha⟩ => nomatch ha⟩
  · exact absurd hx id
  · exact absurd hx id
  · exact hf _ _ hx

/-- A `for` loop over `names.zipIdx k` building a list, one related value per name. -/
theorem relM_forIn_zipIdx {Γ : Ren} {f g : String × Nat → List Val → M (ForInStep (List Val))}
    (hf : ∀ n n' j (vals vals' : List Val), ValsR pkg pm vals vals' → vals.length = j →
      RelM pkg pm Γ (· = Γ) (fun s s' => ∃ v v', s = .yield (vals ++ [v]) ∧
        s' = .yield (vals' ++ [v']) ∧ ValR pkg pm v v') (f (n, j) vals) (g (n', j) vals')) :
    ∀ {ns ns' : List String} (k : Nat) {vals vals' : List Val}, ns.length = ns'.length →
      ValsR pkg pm vals vals' → vals.length = k →
      RelM pkg pm Γ (· = Γ) (fun r r' => ValsR pkg pm r r' ∧ r.length = k + ns.length)
        (forIn (ns.zipIdx k) vals f) (forIn (ns'.zipIdx k) vals' g)
  | [], [], k, _, _, _, hv, hl => by
    simp only [List.zipIdx_nil, List.forIn_nil]
    exact relM_pure ⟨hv, by simp [hl]⟩
  | n :: ns, n' :: ns', k, vals, vals', hn, hv, hl => by
    simp only [List.zipIdx_cons, List.forIn_cons]
    refine relM_bind (hf n n' k vals vals' hv hl) fun s s' ⟨v, v', e, e', hvv⟩ => ?_
    subst e e'
    exact (relM_forIn_zipIdx hf (k + 1) (by simpa using hn) (valsR_append hv (.cons hvv .nil))
      (by simp [hl])).res fun _ _ h => ⟨h.1, by rw [h.2]; simp; omega⟩
  | [], _ :: _, _, _, _, hn, _, _ | _ :: _, [], _, _, _, hn, _, _ => by simp at hn

/-- The slots of patterns' `bind` binders are never `_`. -/
def Slot.ok : Slot → Prop
  | .bind x x' => x ≠ "_" ∧ x' ≠ "_"
  | .maybe _ => True

mutual
theorem patR_ok {Γ : Ren} : ∀ {p p' : Pat} {ss : List Slot}, PatR pkg Γ p p' ss → ∀ s ∈ ss, s.ok
  | _, _, _, .wild | _, _, _, .lit _ | _, _, _, .unsupported _ => by simp
  | _, _, _, .bind h1 h2 _ _ _ _ => by simp [Slot.ok, h1, h2]
  | _, _, _, .same _ => by simp [Slot.ok]
  | _, _, _, .tuple h | _, _, _, .extract _ h => patsR_ok h
theorem patsR_ok {Γ : Ren} : ∀ {ps ps' : List Pat} {ss : List Slot}, PatsR pkg Γ ps ps' ss →
    ∀ s ∈ ss, s.ok
  | _, _, _, .nil => by simp
  | _, _, _, .cons h t => fun s hs => by
    rcases List.mem_append.mp hs with hs | hs
    · exact patR_ok h s hs
    · exact patsR_ok t s hs
end

theorem outs_us : ∀ {ss : List Slot}, (∀ s ∈ ss, s.ok) → ∀ Δ ∈ Slot.outs ss,
    ∀ p ∈ Δ, (p.1 = "_" ↔ p.2 = "_")
  | [], _, Δ, hΔ, p, hp => by simp [Slot.outs] at hΔ; subst hΔ; simp at hp
  | .bind x x' :: ss, hok, Δ, hΔ, p, hp => by
    simp only [Slot.outs, List.mem_map] at hΔ
    obtain ⟨Δ', hΔ', rfl⟩ := hΔ
    rcases List.mem_cons.mp hp with rfl | hp
    · have := hok _ (List.mem_cons_self ..)
      simp only [Slot.ok] at this
      exact ⟨fun e => absurd e this.1, fun e => absurd e this.2⟩
    · exact outs_us (fun s hs => hok s (List.mem_cons_of_mem _ hs)) Δ' hΔ' p hp
  | .maybe x :: ss, hok, Δ, hΔ, p, hp => by
    simp only [Slot.outs, List.mem_append, List.mem_map] at hΔ
    rcases hΔ with ⟨Δ', hΔ', rfl⟩ | hΔ
    · rcases List.mem_cons.mp hp with rfl | hp
      · exact Iff.rfl
      · exact outs_us (fun s hs => hok s (List.mem_cons_of_mem _ hs)) Δ' hΔ' p hp
    · exact outs_us (fun s hs => hok s (List.mem_cons_of_mem _ hs)) Δ hΔ p hp

/-- A match's bindings, from what `matchPat_rel` says of them. -/
theorem bindings_fall2 : ∀ {bs bs' : List (String × Val)}, bs.length = bs'.length →
    (∀ p ∈ (bs.map (·.1)).zip (bs'.map (·.1)), (p.1 = "_" ↔ p.2 = "_")) →
    ValsR pkg pm (bs.map (·.2)) (bs'.map (·.2)) →
    Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) bs bs'
  | [], [], _, _, _ => .nil
  | (a, _) :: _, (a', _) :: _, hl, hu, .cons hv t =>
    .cons ⟨hu (a, a') (by simp), hv⟩ (bindings_fall2 (by simpa using hl)
      (fun p hp => hu p (by simp only [List.map_cons, List.zip_cons_cons]; exact List.mem_cons_of_mem _ hp)) t)
  | [], _ :: _, hl, _, _ | _ :: _, [], hl, _, _ => by simp at hl

theorem blockR_nil {Γ : Ren} {rest rest' : List Expr} (h : BlockR pkg pm Γ rest rest') :
    rest = [] ↔ rest' = [] := by
  cases h <;> simp

end

/-! ## The functions of `eval`'s mutual block, at one depth -/

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {d : Nat}

theorem evalBlock_alpha_of (ih : EvalIH pkg pm ds ds' d) :
    ∀ {Γ : Ren} {ss ss' : List Expr}, BlockR pkg pm Γ ss ss' →
      RelM pkg pm Γ (fun Γ' => ∃ P, Γ' = P ++ Γ) (ValR pkg pm) (evalBlock ds d ss) (evalBlock ds' d ss')
  | _, _, _, .nil => by
    rw [evalBlock.eq_1, evalBlock.eq_1]
    exact (relM_pure .unit).mono fun _ h => ⟨[], h⟩
  | Γ, _, _, .decl (ns := ns) (ns' := ns') (rest := rest) (rest' := rest') m destr ty hn hvs hrest => by
    have hd := ih.decl m destr ty hn hvs
    by_cases hr : rest = []
    · subst hr
      obtain rfl := (blockR_nil hrest).mp rfl
      rw [evalBlock.eq_2, evalBlock.eq_2]
      exact hd.mono fun _ h => by subst h; exact pushAll_prefix _ _
    · have hr' : rest' ≠ [] := fun e => hr ((blockR_nil hrest).mpr e)
      rw [evalBlock.eq_3 _ _ _ _ hr, evalBlock.eq_3 _ _ _ _ hr']
      refine relM_bind_gen (relM_discard hd) fun Γ' _ _ hq _ => ?_
      subst hq
      exact (evalBlock_alpha_of ih hrest).mono fun _ ⟨P, hP⟩ => by
        obtain ⟨P0, hP0⟩ := pushAll_prefix Γ (ns.zip ns')
        exact ⟨P ++ P0, by rw [hP, hP0, List.append_assoc]⟩
  | Γ, _, _, .stmt (rest := rest) (rest' := rest') he hrest => by
    by_cases hr : rest = []
    · subst hr
      obtain rfl := (blockR_nil hrest).mp rfl
      rw [evalBlock.eq_2, evalBlock.eq_2]
      exact (ih.expr he).mono fun _ h => ⟨[], h⟩
    · have hr' : rest' ≠ [] := fun e => hr ((blockR_nil hrest).mpr e)
      rw [evalBlock.eq_3 _ _ _ _ hr, evalBlock.eq_3 _ _ _ _ hr']
      exact relM_bind (relM_discard (ih.expr he)) fun _ _ _ => evalBlock_alpha_of ih hrest

theorem eval_block_alpha (ih : EvalIH pkg pm ds ds' d) {Γ : Ren} {ss ss' : List Expr}
    (h : BlockR pkg pm Γ ss ss') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.block ss)) (eval ds' (d + 1) (.block ss')) := by
  rw [eval.eq_25, eval.eq_25]
  refine relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c c' hc => ?_
  dsimp only
  rw [(envR_length hc.1).1, (envR_length hc.1).2]
  exact relM_bind_gen (evalBlock_alpha_of ih h) fun Γ' v v' ⟨P, hP⟩ hv => by
    subst hP
    exact relM_bind_gen relM_popTo fun _ _ _ hq _ => by subst hq; exact relM_pure hv

theorem evalCases_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {v v' : Val}
    (hv : ValR pkg pm v v') :
    ∀ {Γ : Ren} {cs cs' : List (Pat × Option Expr × Expr)}, CasesR pkg pm Γ cs cs' →
      RelM pkg pm Γ (· = Γ) (ValR pkg pm) (evalCases ds d v cs) (evalCases ds' d v' cs')
  | _, _, _, .nil => by
    rw [evalCases.eq_1, evalCases.eq_1]
    exact relM_throw (.panic _ _)
  | Γ, _, _, .cons (ss := ss) hp hg hb hrest => by
    rw [evalCases.eq_2, evalCases.eq_2]
    have ihr := evalCases_alpha_of hD ih hv hrest
    refine relM_getCore_at fun s s' hs => ?_
    refine relAt_bind_ro hs (matchPat_readOnly _ _ _ _ _) (matchPat_readOnly _ _ _ _ _)
      (matchPat_rel hD hs.1 hp hv) fun r r' hr => ?_
    cases r <;> cases r' <;> simp only [MatchR] at hr
    · exact ihr s s' hs
    · rename_i bs bs'
      obtain ⟨hl, hΔ, hvs⟩ := hr
      have hus := outs_us (patR_ok hp) _ hΔ
      have hf := bindings_fall2 hl hus hvs
      dsimp only
      rw [(envR_length hs.1.1).1, (envR_length hs.1.1).2]
      obtain ⟨P, hP⟩ := pushAll_prefix Γ ((bs.map (·.1)).zip (bs'.map (·.1)))
      -- The arm's body, or the next arm, once the guard has decided.
      have hjp : ∀ b : Bool, RelM pkg pm (Γ.pushAll ((bs.map (·.1)).zip (bs'.map (·.1)))) (· = Γ)
          (ValR pkg pm)
          (if b = true then do
              let r ← eval ds d _
              liftM (popTo Γ.length)
              pure r
            else do
              liftM (popTo Γ.length)
              evalCases ds d v _)
          (if b = true then do
              let r ← eval ds' d _
              liftM (popTo Γ.length)
              pure r
            else do
              liftM (popTo Γ.length)
              evalCases ds' d v' _) := fun b => by
        rw [hP]
        cases b
        · exact relM_bind_gen relM_popTo fun _ _ _ hq _ => by subst hq; exact ihr
        · exact relM_bind (by rw [← hP]; exact ih.expr (hb _ hΔ)) fun _ _ hr =>
            relM_bind_gen relM_popTo fun _ _ _ hq _ => by subst hq; exact relM_pure hr
      refine relM_bind_gen (relM_pushAll false hf) (fun Γ' _ _ hq _ => ?_) s s' hs
      subst hq
      have hgΔ := hg _ hΔ
      cases hgΔ with
      | none => exact relM_bind (relM_pure rfl) fun b _ h => by subst h; exact hjp b
      | some hge =>
        exact relM_bind (ih.expr hge) fun _ _ hgv => relM_bind (relM_asBool hgv) fun b _ h => by
          subst h; exact hjp b

theorem eval_mtch_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {s s' : Expr} {cs cs' : List (Pat × Option Expr × Expr)} (hs : AlphaE pkg pm Γ s s')
    (hc : CasesR pkg pm Γ cs cs') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.mtch s cs)) (eval ds' (d + 1) (.mtch s' cs')) := by
  rw [eval.eq_26, eval.eq_26]
  exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hs) fun _ _ hv =>
    evalCases_alpha_of hD ih hv hc

end


/-! ## Calls -/

section
variable {pkg : List String} {pm : String → Ren}

theorem map_eq_fall2 {g : α → β} : ∀ {l l' : List α}, l.map g = l'.map g →
    Fall2 (fun a b => g a = g b) l l'
  | [], [], _ => .nil
  | _ :: _, _ :: _, h => by
    simp only [List.map_cons, List.cons.injEq] at h
    exact .cons h.1 (map_eq_fall2 h.2)
  | [], _ :: _, h | _ :: _, [], h => by simp at h

/-- A struct's or case's field defaults, each related to itself with no local in scope. -/
theorem fieldDefaults_self : ∀ {fs fs' : List Field}, Fall2 (FieldR pkg pm) fs fs' →
    ∀ j, OptR pkg pm [] ((fs.map (·.default)).getD j none) ((fs'.map (·.default)).getD j none)
  | _, _, .nil, _ => by simp only [List.map_nil, List.getD_nil]; exact .none
  | _, _, .cons ⟨rfl, h⟩ _, 0 => by simp only [List.map_cons, List.getD_cons_zero]; exact h
  | _, _, .cons _ t, j + 1 => by
    simp only [List.map_cons, List.getD_cons_succ]; exact fieldDefaults_self t j

/-- Without named arguments, only the number of parameters decides the slots. -/
theorem argSlots_go_pos {names names' : List String} (hn : names.length = names'.length) :
    ∀ (args : List Expr) (nm : Bool) (acc : List Nat), args.any isNamed = false →
      argSlots.go names args nm acc = argSlots.go names' args nm acc
  | [], nm, acc, _ => by simp only [argSlots.go, hn]
  | e :: as, nm, acc, h => by
    simp only [List.any_cons, Bool.or_eq_false_iff] at h
    have ih := fun nm acc => argSlots_go_pos hn as nm acc h.2
    cases e <;> simp only [isNamed, Bool.true_eq_false, false_and] at h <;> simp only [argSlots.go, ih]

theorem argSlots_pos_rel {Γ m : Ren} {names names' : List String} {args args' : List Expr}
    (hn : names.length = names'.length) (h : args.any isNamed = false)
    (ha : ArgsR pkg pm m Γ args args') :
    ResR pkg pm Eq (argSlots names args) (argSlots names' args') := by
  have h' : args'.any isNamed = false := (argsR_any_named ha) ▸ h
  have := argSlots_same_rel names (argsR_retarget [] h ha)
  unfold argSlots at this ⊢
  rwa [argSlots_go_pos hn args' false [] h'] at this

theorem optBindR_isSome {a b : Option Binding} (h : OptBindR pkg pm a b) : a.isSome = b.isSome := by
  cases a <;> cases b <;> simp_all [OptBindR]

theorem relM_ite {Γ : Ren} {Q : Ren → Prop} {R : α → β → Prop} {c : Bool} {x y : M α}
    {x' y' : M β} (h1 : c = true → RelM pkg pm Γ Q R x x') (h2 : c = false → RelM pkg pm Γ Q R y y') :
    RelM pkg pm Γ Q R (if c = true then x else y) (if c = true then x' else y') := by
  cases c
  · exact h2 rfl
  · exact h1 rfl

end

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {d : Nat}

/-- A type check is related to itself whatever it names the checked thing in its
    message: only a `stuck` text tells them apart. -/
theorem checkOptTy_msg (ds : Decls) (c : Core) (w w' : String) (t : Option Ty) :
    ResR pkg pm Eq ((checkOptTy ds w t).run.run c).1 ((checkOptTy ds w' t).run.run c).1 := by
  cases t with
  | none => exact .stuck _ _
  | some t => exact checkTy_self ds c t

/-- A declared field default, looked up by its owner and slot, is related to itself with
    no local in scope. -/
theorem fieldDefault_rel (hD : DeclsR pkg pm ds ds') (x : String) (j : Nat) :
    OptR pkg pm [] (fieldDefault ds x j) (fieldDefault ds' x j) := by
  unfold fieldDefault fieldsOf
  rcases lookup_fall2 (S := fun (a b : String × List Field) => a.1 = b.1 ∧ Fall2 (FieldR pkg pm) a.2 b.2)
      (R := Fall2 (FieldR pkg pm)) (fun _ _ h => h) x hD.structs with
    ⟨s1, s2⟩ | ⟨fs, fs', s1, s2, hfs⟩
  · simp only [s1, s2]
    rcases variant_rel hD x with ⟨v1, v2⟩ | ⟨ty, v, w, v1, v2, hvw⟩
    · simp only [v1, v2, Option.map_none, Option.bind_none]; exact .none
    · simp only [v1, v2, Option.map_some, Option.bind_some]
      exact fieldDefaults_self hvw.2.2 j
  · simp only [s1, s2, Option.bind_some]
    exact fieldDefaults_self hfs j

mutual
/-- A default value filled with related values is related. -/
theorem fill_rel : ∀ (dv : Dflt) {vs vs' : List Val}, ValsR pkg pm vs vs' →
    ValR pkg pm (dv.fill vs).1 (dv.fill vs').1 ∧ ValsR pkg pm (dv.fill vs).2 (dv.fill vs').2
  | .lit (.int n), _, _, h => by simp only [Dflt.fill]; exact ⟨.int n, h⟩
  | .lit (.bool b), _, _, h => by simp only [Dflt.fill]; exact ⟨.bool b, h⟩
  | .lit (.str s), _, _, h => by simp only [Dflt.fill]; exact ⟨.str s, h⟩
  | .field .., _, _, .nil => by simp only [Dflt.fill]; exact ⟨.unit, .nil⟩
  | .field .., _, _, .cons hv hs => by simp only [Dflt.fill]; exact ⟨hv, hs⟩
  | .struct_ t fs, _, _, h => by
    simp only [Dflt.fill]
    exact ⟨.struct_ t (fillL_rel fs h).1, (fillL_rel fs h).2⟩
  | .sealed t c fs, _, _, h => by
    simp only [Dflt.fill]
    exact ⟨.sealed t c (fillL_rel fs h).1, (fillL_rel fs h).2⟩
/-- `fill_rel` of each. -/
theorem fillL_rel : ∀ (l : List Dflt) {vs vs' : List Val}, ValsR pkg pm vs vs' →
    ValsR pkg pm (Dflt.fillL l vs).1 (Dflt.fillL l vs').1 ∧
      ValsR pkg pm (Dflt.fillL l vs).2 (Dflt.fillL l vs').2
  | [], _, _, h => by simp only [Dflt.fillL]; exact ⟨.nil, h⟩
  | dv :: l, _, _, h => by
    simp only [Dflt.fillL]
    exact ⟨.cons (fill_rel dv h).1 (fillL_rel l (fill_rel dv h).2).1,
      (fillL_rel l (fill_rel dv h).2).2⟩
end

theorem bindDefaults_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {names names' : List String} {tys : List (Option Ty)} {dflts dflts' : List (Option Expr)}
    {fs : Bool} {given given' : List (Nat × Val)} (hn : names.length = names'.length)
    (hdf : DfltR pkg pm fs names names' dflts dflts')
    (hg : ∀ j, OptValR pkg pm (given.lookup j) (given'.lookup j)) :
    RelM pkg pm Γ (· = Γ) (fun vs vs' => ValsR pkg pm vs vs' ∧ vs.length = names.length)
      (bindDefaults ds d names tys dflts fs given) (bindDefaults ds' d names' tys dflts' fs given') := by
  rw [bindDefaults.eq_1, bindDefaults.eq_1]
  refine relM_bind ((relM_forIn_zipIdx ?_ 0 hn .nil rfl).res fun _ _ h => ⟨h.1, by simpa using h.2⟩)
    fun _ _ h => relM_pure h
  intro n n' j vals vals' hv hl
  dsimp only
  have hgj := hg j
  obtain ⟨Γj, hfr, hod⟩ := hdf j vals vals' hv hl
  generalize given.lookup j = g at hgj ⊢
  generalize given'.lookup j = g' at hgj ⊢
  generalize dflts.getD j none = o at hod ⊢
  generalize dflts'.getD j none = o' at hod ⊢
  cases g <;> cases g' <;> simp only [OptValR] at hgj
  · cases hod with
    | none =>
      cases fs
      · simp only [Bool.false_eq_true, ↓reduceIte]
        exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
      · simp only [↓reduceIte]
        -- A left-out field without a declared default: its type's default value, the
        -- same template on both sides, its declared defaults related to themselves.
        refine relM_bind (relM_liftH_ro (pureH_checkOptTy ds _ _).readOnly
          (pureH_checkOptTy ds' _ _).readOnly fun c c' _ => by
            rw [← checkOptTy_congr hD.same, (pureH_checkOptTy ds _ _ c c').1]
            exact checkOptTy_msg ds c' _ _ _) fun t _ h => ?_
        subst h
        refine relM_bind (relM_liftH_ro (pureH_defaultOf ds 64 t).readOnly
          (pureH_defaultOf ds' 64 t).readOnly fun c c' _ => by
            rw [← defaultOf_congr hD.same, (pureH_defaultOf ds 64 t c c').1]
            exact defaultOf_self ds 64 t c') fun dv _ h => ?_
        subst h
        refine relM_bind (relM_forIn_st (R := Eq) (S := ValsR pkg pm) ?_
          (fall2_refl (fun _ => rfl) _) .nil) fun _ _ h => relM_pure ⟨_, _, rfl, rfl, (fill_rel dv h).1⟩
        intro ⟨x, k, ty⟩ _ acc acc' h hacc
        subst h
        dsimp only
        have hr := fieldDefault_rel hD x k
        generalize fieldDefault ds x k = o at hr ⊢
        generalize fieldDefault ds' x k = o' at hr ⊢
        cases hr with
        | none => exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
        | some he =>
          dsimp only
          refine relM_bind (relM_liftH_ro (readOnly_byNameOk _ _ _ _) (readOnly_byNameOk _ _ _ _)
            fun c c' hc => byNameOk_rel hD .nil hc.2.1 hc.2.2.1 he) fun b _ h => ?_
          subst h
          cases b
          · exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
          · exact relM_bind (relM_runIn .nil ((ih.expr he).mono fun _ _ => trivial))
              fun v v' hvv => relM_pure (valsR_append hacc (.cons hvv .nil))
    | some he =>
      dsimp only
      refine relM_bind (relM_liftH_ro (readOnly_byNameOk _ _ _ _) (readOnly_byNameOk _ _ _ _)
        fun c c' hc => byNameOk_rel hD hfr hc.2.1 hc.2.2.1 he) fun b _ h => ?_
      subst h
      cases b
      · exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
      · exact relM_bind (relM_runIn hfr ((ih.expr he).mono fun _ _ => trivial))
          fun v v' hvv => relM_pure ⟨v, v', rfl, rfl, hvv⟩
  · exact relM_pure ⟨_, _, rfl, rfl, hgj⟩

theorem bindArgs_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {names names' : List String} {tys : List (Option Ty)} {dflts dflts' : List (Option Expr)}
    {fs : Bool} {args args' : List Expr} (hn : names.length = names'.length)
    (hsl : ResR pkg pm Eq (argSlots names args) (argSlots names' args'))
    (ha : Fall2 (fun a a' => AlphaE pkg pm Γ (unnamed a) (unnamed a')) args args')
    (hdf : DfltR pkg pm fs names names' dflts dflts') :
    RelM pkg pm Γ (· = Γ) (fun vs vs' => ValsR pkg pm vs vs' ∧ vs.length = names.length)
      (bindArgs ds d names tys dflts fs args) (bindArgs ds' d names' tys dflts' fs args') := by
  rw [bindArgs.eq_1, bindArgs.eq_1]
  generalize argSlots names args = r at hsl ⊢
  generalize argSlots names' args' = r' at hsl ⊢
  cases r with
  | error e =>
    cases r' with
    | error e' => exact relM_bind (R := fun (_ _ : List Nat) => False) (relM_throw hsl) fun _ _ h => h.elim
    | ok _ => exact absurd hsl id
  | ok slots =>
    cases r' with
    | error _ => exact absurd hsl id
    | ok slots' =>
    have hsl' : slots = slots' := hsl
    subst hsl'
    refine relM_bind (R := fun a a' => a = slots ∧ a' = slots) (relM_pure ⟨rfl, rfl⟩) fun _ _ ⟨h1, h2⟩ => ?_
    subst h1 h2
    dsimp only
    refine relM_bind relM_getCore fun c c' hc => ?_
    refine relM_bind (relM_forIn (R := fun (p q : Expr × Nat) =>
        AlphaE pkg pm Γ (unnamed p.1) (unnamed q.1) ∧ p.2 = q.2) ?_
        (fall2_zip ha (fall2_refl (fun _ => rfl) _))) fun _ _ _ => ?_
    · intro ⟨a, j⟩ ⟨a', j'⟩ ⟨he, hj⟩
      simp only at hj
      subst hj
      dsimp only
      refine relM_bind (relM_liftH_ro (readOnly_byNameOk _ _ _ _) (readOnly_byNameOk _ _ _ _)
        fun c0 c0' hc0 => byNameOk_rel hD hc.1 hc0.2.1 hc0.2.2.1 he) fun b _ h => ?_
      subst h
      cases b
      · exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
      · exact relM_pure rfl
    · exact relM_bind (evalList_alpha_of ih (fall2_map (f := unnamed) (g := unnamed) (fun _ _ h => h) ha))
        fun vs vs' hv => bindDefaults_alpha_of hD ih hn hdf (lookup_zip_rel _ hv)

theorem funcArgs_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {f f' : Func} (hf : FuncR pkg pm f f') {args args' : List Expr}
    (ha : ArgsR pkg pm (pm f.name) Γ args args') :
    RelM pkg pm Γ (· = Γ) (fun vs vs' => ValsR pkg pm vs vs' ∧ vs.length = f.params.length)
      (funcArgs ds d f args) (funcArgs ds' d f' args') := by
  rw [funcArgs.eq_1, funcArgs.eq_1, ← hf.types]
  rw [hf.map] at ha
  refine (bindArgs_alpha_of hD ih hf.names.1 (argSlots_rel hf.names.1 hf.nodup ha) (argsR_unnamed ha)
    ?_).res fun _ _ h => ⟨h.1, by simpa using h.2⟩
  intro j vals vals' hv hl
  subst hl
  exact ⟨_, envR_defaultFrame hf.names hv, hf.defaults vals.length⟩

theorem fieldArgs_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {fs fs' : List Field} (hfs : Fall2 (FieldR pkg pm) fs fs') {args args' : List Expr}
    (ha : ArgsR pkg pm [] Γ args args') :
    RelM pkg pm Γ (· = Γ) (ValsR pkg pm) (fieldArgs ds d fs args) (fieldArgs ds' d fs' args') := by
  have hd := fieldDefaults_self hfs
  obtain rfl := fieldsR_eq hfs
  rw [fieldArgs.eq_1, fieldArgs.eq_1]
  exact (bindArgs_alpha_of hD ih rfl (argSlots_same_rel _ ha) (argsR_unnamed ha)
    fun j _ _ _ _ => ⟨[], .nil, hd j⟩).res fun _ _ h => h.1

theorem callFunc_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {f f' : Func} (hf : FuncR pkg pm f f') {vs vs' : List Val} (hv : ValsR pkg pm vs vs') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (callFunc ds d f vs) (callFunc ds' d f' vs') := by
  rw [callFunc.eq_1, callFunc.eq_1]
  have hpl : f.params.length = f'.params.length := by simpa using hf.names.1
  have hc : (vs.length != f.params.length) = (vs'.length != f'.params.length) := by
    rw [valsR_length hv, hpl]
  rw [hc]
  refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩) fun h => ?_
  have hl : vs.length = f.params.length := by
    simp only [bne_eq_false_iff_eq] at h; rw [valsR_length hv, hpl]; exact h
  dsimp only
  refine relM_bind (relM_forIn (R := fun (p p' : Param) => p.type = p'.type) (fun p p' hp => ?_)
    (map_eq_fall2 hf.types)) fun _ _ _ => ?_
  · rw [hp]
    exact relM_bind (relM_discard (relM_checkOptTy hD.same _ _ _)) fun _ _ _ => relM_pure rfl
  · have hfr := envR_foldl_params (pkg := pkg) (pm := pm) (Γ := [])
      (F := fun ρ x => match x with | (p, v) => bind ρ p.name { val := v, mutable := false })
      (G := fun ρ x => match x with | (p, v) => bind ρ p.name { val := v, mutable := false })
      (fun _ _ _ => rfl) (fun _ _ _ => rfl) (namesR_fall2 hf.names) hv hl .nil
    have hbody := relM_runIn (Γ := Γ) (ds := ds) (ds' := ds') (d := d) (d' := d) hfr
      ((ih.expr hf.body).mono fun _ _ => trivial)
    rw [← hf.result]
    generalize f.result = res
    cases res with
    | none => exact relM_bind hbody fun _ _ _ => relM_pure .unit
    | some t =>
      refine relM_bind (relM_discard (relM_checkTy hD.same t)) fun _ _ _ => relM_bind hbody fun r r' hr => ?_
      have hr0 := hr
      cases hr <;> first
        | exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
        | exact relM_pure hr0

theorem callValue_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {v v' : Val} (hv : ValR pkg pm v v') {m : Ren} {args args' : List Expr}
    (ha : ArgsR pkg pm m Γ args args') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (callValue ds d v args) (callValue ds' d v' args') := by
  have hn := argsR_any_named ha
  have hv0 := hv
  cases hv with
  | fn hf =>
    rw [callValue.eq_1, callValue.eq_1, hn]
    -- A named argument to a function value: `unsupported "named-arg"` on both sides
    -- (`callValue`, Gala/Eval.lean).
    refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
      fun h => ?_
    dsimp only
    rw [← hf.types]
    exact relM_bind (bindArgs_alpha_of hD ih hf.names.1 (argSlots_pos_rel hf.names.1 (hn ▸ h) ha)
      (argsR_unnamed ha) (dfltR_none hf.names)) fun _ _ h => callFunc_alpha_of hD ih hf h.1
  | closure hcap htys hns hb =>
    rw [callValue.eq_2, callValue.eq_2, hn]
    refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
      fun h => ?_
    dsimp only
    rw [← htys]
    refine relM_bind (bindArgs_alpha_of hD ih hns.1 (argSlots_pos_rel hns.1 (hn ▸ h) ha)
      (argsR_unnamed ha) (dfltR_none hns)) fun vs vs' hvs => ?_
    exact relM_runIn (envR_foldl_params (fun _ _ _ => rfl) (fun _ _ _ => rfl) (namesR_fall2 hns)
      hvs.1 (by simpa using hvs.2) (envR_capturedEnv hcap)) ((ih.expr hb).mono fun _ _ => trivial)
  | _ =>
    rw [callValue.eq_3 _ _ _ _ (by intro _ h; cases h) (by intro _ _ _ h; cases h),
      callValue.eq_3 _ _ _ _ (by intro _ h; cases h) (by intro _ _ _ h; cases h)]
    exact relM_liftH_ro (readOnly_outsideS1 _ _) (readOnly_outsideS1 _ _) fun _ _ _ =>
      outsideS1_rel hD.unsupported _ _

/-- A call of a free name: a top-level function, a constructor, `Println`/`Print`, or a
    package-level variable holding a function. -/
theorem eval_callFree_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {x : String} {as as' : List Expr} (hfree : Γ.free x) (ha : ArgsR pkg pm (pm x) Γ as as') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.call (.name x) as))
      (eval ds' (d + 1) (.call (.name x) as')) := by
  rw [eval.eq_18, eval.eq_18]
  refine relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c c' hc => ?_
  have hr := resolve_rel hD hc.1 hc.2.1 (Or.inr ⟨rfl, hfree⟩ : NameR Γ x x)
  have hnf : (∀ f, resolve ds c.locals c.globals x ≠ .func f) →
      (∀ b, resolve ds c.locals c.globals x ≠ .var b) → pm x = [] :=
    fun h1 h2 => hD.noFunc x (resolve_noFunc h1 h2)
  generalize hres : resolve ds c.locals c.globals x = r at hr hnf ⊢
  generalize resolve ds' c'.locals c'.globals x = r' at hr ⊢
  cases r <;> cases r' <;> simp only [RefR] at hr <;> (try exact absurd hr id)
  · exact relM_bind (relM_liftH_ro (readOnly_deref _) (readOnly_deref _)
      fun _ _ hc0 => deref_rel hc0.2.2.1 hr.2) fun _ _ hv => callValue_alpha_of hD ih hv ha
  · rename_i fn fn'
    have hfx := funcs_name hD (resolve_func hres)
    exact relM_bind (funcArgs_alpha_of hD ih hr (by rw [hfx]; exact ha)) fun _ _ h =>
      callFunc_alpha_of hD ih hr h.1
  · obtain ⟨rfl, hfs⟩ := hr
    rw [hnf (fun _ h => nomatch h) (fun _ h => nomatch h)] at ha
    have hfs0 := hfs
    obtain rfl := fieldsR_eq hfs
    exact relM_bind (fieldArgs_alpha_of hD ih hfs0 ha) fun _ _ hvs =>
      relM_bind (relM_liftH_ro (readOnly_construct _ _ _ _) (readOnly_construct _ _ _ _)
        fun c0 c0' _ => construct_rel hD.same _ _ hvs c0 c0') fun _ _ h => relM_pure (.struct_ _ h)
  · obtain ⟨rfl, hvr⟩ := hr
    rw [hnf (fun _ h => nomatch h) (fun _ h => nomatch h)] at ha
    have hfs := hvr.2.2
    obtain rfl := VarR.eq hvr
    exact relM_bind (fieldArgs_alpha_of hD ih hfs ha) fun _ _ hvs =>
      relM_bind (relM_liftH_ro (readOnly_construct _ _ _ _) (readOnly_construct _ _ _ _)
        fun c0 c0' _ => construct_rel hD.same _ _ hvs c0 c0') fun _ _ h => relM_pure (.sealed _ _ h)
  · subst hr
    dsimp only
    rw [argsR_any_named ha]
    refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩) fun h => ?_
    refine relM_bind (evalList_alpha_of ih (argsR_pos ((argsR_any_named ha) ▸ h) ha))
      fun vs vs' hvs => ?_
    refine relM_bind (relM_liftH_ro (readOnly_printable _ _) (readOnly_printable _ _)
      fun c0 c0' _ => printable_rel hD.same hvs c0 c0') fun _ _ _ => ?_
    refine relM_bind (relM_liftH (R := Eq) fun c0 c0' hc0 => ?_) fun _ _ _ => relM_pure .unit
    rw [print_rel hD.same _ hvs]
    exact emit_rel hD.same _ hc0
  · refine relM_liftH_ro (readOnly_unresolved _ _) (readOnly_unresolved _ _) fun _ _ _ => ?_
    unfold unresolved
    rw [← hD.unsupported]
    split <;> exact .unsupported _ _ _

/-- A call of anything else: a local variable, a field, an expression. -/
theorem eval_call_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {c c' : Expr} {as as' : List Expr} (hnf : ∀ f, c = .name f → ¬ Γ.free f)
    (hc : AlphaE pkg pm Γ c c') (ha : ArgsR pkg pm [] Γ as as') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds (d + 1) (.call c as))
      (eval ds' (d + 1) (.call c' as')) := by
  have hc0 := hc
  cases hc with
  | name hx =>
    rename_i x x'
    rw [eval.eq_18, eval.eq_18]
    refine relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c0 c0' hc1 => ?_
    rcases hx with hb | ⟨rfl, hfr⟩
    · obtain ⟨b, hl⟩ := Option.isSome_iff_exists.mp (bound_lookup_isSome hc1.1 hb)
      have hlr := lookup_rel hc1.1 (Or.inl hb)
      rw [hl] at hlr
      cases hl' : lookup c0'.locals x' with
      | none => rw [hl'] at hlr; exact hlr.elim
      | some b' =>
        rw [hl'] at hlr
        rw [resolve_var hl, resolve_var hl']
        exact relM_bind (relM_liftH_ro (readOnly_deref _) (readOnly_deref _)
          fun _ _ hc2 => deref_rel hc2.2.2.1 hlr.2) fun _ _ hv => callValue_alpha_of hD ih hv ha
    · exact absurd hfr (hnf _ rfl)
  | select m ht =>
    rw [eval.eq_19, eval.eq_19]
    refine relM_bind relM_tick fun _ _ _ => relM_bind relM_getCore fun c0 c0' hc1 => ?_
    dsimp only
    have ht0 := ht
    -- The field's value, then the call.
    have hk : ∀ tv tv', ValR pkg pm tv tv' → RelM pkg pm Γ (· = Γ) (ValR pkg pm)
        (do let g ← liftM (getField ds tv m); callValue ds d g as)
        (do let g ← liftM (getField ds' tv' m); callValue ds' d g as') := fun _ _ hv =>
      relM_bind (relM_liftH_ro (readOnly_getField _ _ _) (readOnly_getField _ _ _)
        fun _ _ _ => getField_rel hD.same m hv) fun _ _ hg => callValue_alpha_of hD ih hg ha
    cases ht with
    | name hx =>
      dsimp only
      simp only [optBindR_isSome (lookupVar_rel hc1.1 hc1.2.1 hx)]
      refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
        fun _ => relM_bind (ih.expr ht0) fun tv tv' hv => ?_
      have hv0 := hv
      cases hv <;> dsimp only <;> (try simp only [hD.same.structs]) <;> (try simp only [caseFields_congr hD.same]) <;>
        exact relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
          fun _ => hk _ _ hv0
    | _ =>
      dsimp only
      refine relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
        fun _ => relM_bind (ih.expr ht0) fun tv tv' hv => ?_
      have hv0 := hv
      cases hv <;> dsimp only <;> (try simp only [hD.same.structs]) <;> (try simp only [caseFields_congr hD.same]) <;>
        exact relM_ite (fun _ => relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩)
          fun _ => hk _ _ hv0
  | unsupported k =>
    rw [eval.eq_20, eval.eq_20]
    exact relM_bind relM_tick fun _ _ _ => relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
  | _ =>
    rw [eval.eq_21 _ _ _ _ (by intro _ h; cases h) (by intro _ _ h; cases h) (by intro _ h; cases h),
      eval.eq_21 _ _ _ _ (by intro _ h; cases h) (by intro _ _ h; cases h) (by intro _ h; cases h)]
    exact relM_bind relM_tick fun _ _ _ => relM_bind (ih.expr hc0) fun _ _ hv =>
      callValue_alpha_of hD ih hv ha

theorem declare_alpha_of (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    {ns ns' : List String} (destr : Bool) (ty : Option Ty) {vs vs' : List Expr}
    (hn : NamesR pkg ns ns') (hvs : AlphaL pkg pm Γ vs vs') :
    RelM pkg pm Γ (· = Γ) (BindsR pkg pm ns ns') (declare ds d ns destr ty vs)
      (declare ds' d ns' destr ty vs') := by
  rw [declare.eq_def, declare.eq_def]
  dsimp only
  cases ty
  case' none =>
    dsimp only
    refine relM_bind (relM_pure (R := Eq) rfl) fun tn _ h => ?_
  case' some =>
    dsimp only
    refine relM_bind (relM_map (relM_checkTy hD.same _) fun a a' (h : a = a') => congrArg some h)
      fun tn _ h => ?_
  all_goals
    subst h
    have hnl := hn.1
    cases destr
    · simp only [Bool.false_eq_true, ↓reduceIte]
      cases hvs with
      | nil =>
        simp only [List.isEmpty_nil, ↓reduceIte]
        cases tn with
        | none => exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
        | some t =>
          dsimp only
          simp only [List.map_const']
          rw [← hnl]
          exact relM_bind (bindDefaults_alpha_of hD ih hnl
            (fun j _ _ _ _ => ⟨[], by simp only [↓reduceIte]; exact .nil,
              by simp only [List.getD_nil]; exact .none⟩)
            (fun _ => by simp only [List.lookup, OptValR])) fun _ _ hv =>
              relM_pure (bindsR_zip hn hv.1 hv.2)
      | cons he hes =>
        simp only [List.isEmpty_cons, Bool.false_eq_true, ↓reduceIte]
        refine relM_bind (evalList_alpha_of ih (.cons he (alphaL_fall2 hes))) fun vs vs' hv => ?_
        rw [valsR_length hv, hnl]
        split
        · rename_i hl
          exact relM_pure (bindsR_zip hn hv (by simpa [valsR_length hv, hnl] using hl))
        · exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
    · simp only [↓reduceIte]
      cases hvs with
      | nil => exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
      | cons he hes =>
        cases hes with
        | cons _ _ => exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
        | nil =>
          dsimp only
          refine relM_bind (ih.expr he) fun v v' hv => ?_
          cases hv with
          | tuple hvs =>
            dsimp only
            rw [valsR_length hvs, hnl]
            split
            · rename_i hl
              exact relM_pure (bindsR_zip hn hvs (by simpa [valsR_length hvs, hnl] using hl))
            · exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩
          | _ => exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .stuck _ _⟩

theorem eval_decl_alpha (hD : DeclsR pkg pm ds ds') (ih : EvalIH pkg pm ds ds' d) {Γ : Ren}
    (m : Mut) {ns ns' : List String} (destr : Bool) (ty : Option Ty) {vs vs' : List Expr}
    (hn : NamesR pkg ns ns') (hvs : AlphaL pkg pm Γ vs vs') :
    RelM pkg pm Γ (· = Γ.pushAll (ns.zip ns')) (ValR pkg pm)
      (eval ds (d + 1) (.decl m ns destr ty vs)) (eval ds' (d + 1) (.decl m ns' destr ty vs')) := by
  rw [eval.eq_29, eval.eq_29]
  refine relM_bind relM_tick fun _ _ _ => ?_
  refine relM_bind (declare_alpha_of hD ih destr ty hn hvs) fun bs bs' ⟨hb, h1, h2⟩ => ?_
  have hp := relM_pushAll (pkg := pkg) (pm := pm) (Γ := Γ) (m == .var) hb
  rw [h1, h2] at hp
  exact relM_bind_gen hp fun Γ' _ _ hq _ => by subst hq; exact relM_pure .unit

end

/-! ## The induction over `eval` -/

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls}

theorem evalIH_all (hD : DeclsR pkg pm ds ds') : ∀ d, EvalIH pkg pm ds ds' d
  | 0 => ⟨fun _ => by rw [eval.eq_1, eval.eq_1]; exact relM_throw .fuel,
      @fun _ _ _ _ _ _ _ _ _ _ => by rw [eval.eq_1, eval.eq_1]; exact relM_throw .fuel⟩
  | d + 1 => by
    have ih := evalIH_all hD d
    refine ⟨fun {Γ e e'} he => ?_, @fun _ m _ _ destr ty _ _ hn hvs =>
      eval_decl_alpha hD ih m destr ty hn hvs⟩
    cases he with
    | lit l => exact eval_lit_alpha l
    | name hx => exact eval_name_alpha hD hx
    | unsupported k => exact eval_unsupported_alpha k
    | unary op h => exact eval_unary_alpha ih op h
    | binary op hl hr => exact eval_binary_alpha ih op hl hr
    | callFree hf ha => exact eval_callFree_alpha hD ih hf ha
    | call hnf hc ha => exact eval_call_alpha hD ih hnf hc ha
    | select f h => exact eval_select_alpha hD ih f h
    | paren h => exact eval_paren_alpha ih h
    | tuple h => exact eval_tuple_alpha ih h
    | interp h => exact eval_interp_alpha hD ih h
    | ifExpr hc ha hb => exact eval_ifExpr_alpha ih hc ha hb
    | ifStmt hc ha hb => exact eval_ifStmt_alpha ih hc ha hb
    | mtch hs hc => exact eval_mtch_alpha hD ih hs hc
    | block h => exact eval_block_alpha ih h
    | ret h => exact eval_ret_alpha ih h
    | assign op hts hvs => exact eval_assign_alpha hD ih op hts hvs
    | incdec op h => exact eval_incdec_alpha hD op h
    | lambda r htys hns hb => exact eval_lambda_alpha r htys hns hb

/-- **`eval` commutes with the renaming** (T2, part b2): related expressions, evaluated from
    related states, give related results (or related `Stop`s), leave related states with
    the same output and exactly the same steps left, and, after a value, the same scope. -/
theorem eval_alpha (hD : DeclsR pkg pm ds ds') (d : Nat) {Γ : Ren} {e e' : Expr}
    (he : AlphaE pkg pm Γ e e') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds d e) (eval ds' d e') :=
  (evalIH_all hD d).expr he

/-- A function call commutes with the renaming. -/
theorem callFunc_alpha (hD : DeclsR pkg pm ds ds') (d : Nat) {Γ : Ren} {f f' : Func}
    (hf : FuncR pkg pm f f') {vs vs' : List Val} (hv : ValsR pkg pm vs vs') :
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (callFunc ds d f vs) (callFunc ds' d f' vs') :=
  callFunc_alpha_of hD (evalIH_all hD d) hf hv

/-- A declaration commutes with the renaming. -/
theorem declare_alpha (hD : DeclsR pkg pm ds ds') (d : Nat) {Γ : Ren} {ns ns' : List String}
    (destr : Bool) (ty : Option Ty) {vs vs' : List Expr} (hn : NamesR pkg ns ns')
    (hvs : AlphaL pkg pm Γ vs vs') :
    RelM pkg pm Γ (· = Γ) (BindsR pkg pm ns ns') (declare ds d ns destr ty vs)
      (declare ds' d ns' destr ty vs') :=
  declare_alpha_of hD (evalIH_all hD d) destr ty hn hvs

end

/-! ## Not vacuous

`{ val x = 1; { val x = 2; Println(x) }; Println(x) }` and the same block with the inner
`x` renamed to `y` are related, so `eval_alpha` gives them the same output and related
results. -/

section
/-- No declarations. -/
private def ds₀ : Decls :=
  { funcs := [], structs := [], shorthands := [], sealeds := [], unsupported := [] }

private def pm₀ : String → Ren := fun _ => []

private def shadowed (inner : String) : Expr :=
  .block [.decl .val ["x"] false none [.lit (.int 1)],
    .block [.decl .val [inner] false none [.lit (.int 2)], .call (.name "Println") [.name inner]],
    .call (.name "Println") [.name "x"]]

private def s₀ (n : Nat) : St := { locals := [], globals := [], out := "", heap := #[], steps := n }

private theorem decls₀ : DeclsR [] pm₀ ds₀ ds₀ where
  funcs := .nil
  noFunc := fun _ _ => rfl
  structs := .nil
  shorthands := rfl
  sealeds := .nil
  unsupported := rfl
  inPkg := fun x h => by simp [ds₀, Decls.variant?] at h

private theorem shadowed_alpha : AlphaE [] pm₀ [] (shadowed "x") (shadowed "y") := by
  have hx : NamesR [] ["x"] ["x"] := ⟨rfl, by simp [BinderR]⟩
  have hy : NamesR [] ["x"] ["y"] := ⟨rfl, by simp [BinderR, capitalized]⟩
  have hfree : ∀ Γ : Ren, ("Println" ∉ Γ.map (·.1) ∧ "Println" ∉ Γ.map (·.2)) → Γ.free "Println" :=
    fun _ h => h
  refine .block (.decl .val false none hx (.cons (.lit _) .nil) (.stmt (.block ?_) (.stmt ?_ .nil)))
  · refine .decl .val false none hy (.cons (.lit _) .nil) (.stmt ?_ .nil)
    refine .callFree (hfree _ (by decide)) (.pos rfl (.name ?_) .nil)
    exact Or.inl (by simp [Ren.pushAll, Ren.push, Ren.bound])
  · refine .callFree (hfree _ (by decide)) (.pos rfl (.name ?_) .nil)
    exact Or.inl (by simp [Ren.pushAll, Ren.push, Ren.bound])

/-- The two blocks print the same and end alike, for every amount of fuel. -/
example (n : Nat) :
    ((eval ds₀ n (shadowed "x")).run.run (s₀ n)).2.out =
        ((eval ds₀ n (shadowed "y")).run.run (s₀ n)).2.out ∧
      ResR [] pm₀ (ValR [] pm₀) ((eval ds₀ n (shadowed "x")).run.run (s₀ n)).1
        ((eval ds₀ n (shadowed "y")).run.run (s₀ n)).1 := by
  obtain ⟨h1, _, ⟨hc, _⟩, _⟩ := eval_alpha decls₀ n shadowed_alpha (s₀ n) (s₀ n)
    ⟨⟨.nil, .nil, ⟨rfl, fun _ h => absurd h (by simp [s₀])⟩, rfl⟩, rfl⟩
  exact ⟨hc.2.2.2, h1⟩

end

end Gala.Spec
