/-
  # T3: left to right

  docs/FORMAL_SEMANTICS.MD, "M2": the output and effects of an operand list, a call
  (named arguments included), a compound assignment and a pattern are those of its parts,
  in source order, and a failed sub-pattern stops the ones after it.

  The statements are about what a program does, not about how `eval` is written. A part's
  behaviour is given as an `Eff`: from the state the program sees (`Core`), its result (a
  value or a `Stop`), the state it leaves, and the steps it spends. `Acts x p` says the
  evaluator code `x` does exactly `p`, from every state with enough steps: the same
  result, the same state, and exactly `p.cost` steps. Steps are exact, so no statement
  here holds by running out of fuel. A part's result and effect may depend on the state it
  starts from, so a later part sees the earlier parts' effects.

  Each theorem takes the `Acts` of the parts as hypotheses (the "given parts") and gives
  the `Acts` of the compound, built from them with `Eff.bind` and `Eff.seq`, which do the
  parts in source order: each from the state the one before left, and none after the first
  `Stop`.

  * T3a, operands: `evalList_acts`, and from it a binary operator's operands
    (`eval_binary_acts`), a tuple's elements (`eval_tuple_acts`) and an interpolated
    string's parts (`eval_interp_acts`).
  * T3b, `&&` and `||`: `eval_and_acts`, `eval_or_acts`. The right operand runs only when
    the left one does not decide the result.
  * T3c, a call's arguments: `bindArgs_acts`. The written arguments run in the order
    written, named ones included, then the left-out parameters' defaults in parameter
    order (`bindDefaults_acts`). Parameter `j` gets the value of the argument whose slot is
    `j` (`defaultStep`), and `argSlots_eq` says which slot each argument has.
  * T3d, compound assignment: `eval_compound_acts`, `compound_eff`. `t op= x` reads `t`
    before `x` runs.
  * T3e, sub-patterns: `matchPats_append`, `matchPats_stops`. In S1 a pattern has no
    effect, so this is only about a later sub-pattern's `stuck` or `unsupported` verdict.
  * T3f, the arms of a `match`: `evalCases_nil`, `evalCases_nomatch`,
    `evalCases_guard_false`, `evalCases_guard_true`, `evalCases_match`. A failed arm leaves
    nothing behind but its guard's effects; the first arm whose pattern matches and whose
    guard holds runs.

  `eval_bool_acts` and the example after it show the hypotheses can be met.

  Not covered: calls of function values beyond their arguments (`callValue`), `if`, blocks
  and declarations, which run their parts in order by `eval`'s definition too but have no
  theorem here.
-/
import Gala.Defaults

namespace Gala.Spec

/-! ## What a part does -/

/-- What a part of a program does, from the state the program sees: its result (a value or
    a `Stop`), the state it leaves, and the steps it spends. -/
structure Eff (α : Type) where
  res : Core → Except Stop α
  eff : Core → Core
  cost : Core → Nat

/-- `x`, then, if it gave a value, `f` of it from the state `x` left. After a `Stop`,
    nothing more runs. -/
def Eff.bind (x : Eff α) (f : α → Eff β) : Eff β where
  res c := match x.res c with
    | .ok a => (f a).res (x.eff c)
    | .error e => .error e
  eff c := match x.res c with
    | .ok a => (f a).eff (x.eff c)
    | .error _ => x.eff c
  cost c := match x.res c with
    | .ok a => x.cost c + (f a).cost (x.eff c)
    | .error _ => x.cost c

/-- A value, with no effect. -/
def Eff.pure (a : α) : Eff α := ⟨fun _ => .ok a, id, fun _ => 0⟩

/-- A step that spends no steps (an `H` step): what it does to the state. -/
def Eff.ofH (x : H α) : Eff α := ⟨fun c => (x.run.run c).1, fun c => (x.run.run c).2, fun _ => 0⟩

/-- One evaluation step. -/
def Eff.tick : Eff Unit := ⟨fun _ => .ok (), id, fun _ => 1⟩

/-- The parts in order, each from the state the one before left; the first `Stop` ends
    the sequence. -/
def Eff.seq : List (Eff α) → Eff (List α)
  | [] => Eff.pure []
  | p :: ps => p.bind fun v => (Eff.seq ps).bind fun vs => Eff.pure (v :: vs)

theorem Eff.ext' {p q : Eff α} (h1 : ∀ c, p.res c = q.res c) (h2 : ∀ c, p.eff c = q.eff c)
    (h3 : ∀ c, p.cost c = q.cost c) : p = q := by
  cases p; cases q
  simp only [Eff.mk.injEq]
  exact ⟨funext h1, funext h2, funext h3⟩

theorem Eff.pure_bind (a : α) (f : α → Eff β) : (Eff.pure a).bind f = f a :=
  Eff.ext' (fun _ => rfl) (fun _ => rfl) (fun _ => by simp [Eff.bind, Eff.pure])

theorem Eff.bind_pure (x : Eff α) : x.bind Eff.pure = x :=
  Eff.ext' (fun c => by simp only [Eff.bind, Eff.pure]; split <;> simp_all)
    (fun c => by simp only [Eff.bind, Eff.pure]; split <;> rfl)
    (fun c => by simp only [Eff.bind, Eff.pure]; split <;> rfl)

set_option hygiene false in
macro "bind_assoc_tac" : tactic => `(tactic| (
  simp only [Eff.bind]
  cases hx : x.res c <;> simp only [hx]
  all_goals (rename_i a; cases hf : (f a).res (x.eff c) <;> simp only [hf] <;> try omega)))

theorem Eff.bind_assoc (x : Eff α) (f : α → Eff β) (g : β → Eff γ) :
    (x.bind f).bind g = x.bind fun a => (f a).bind g :=
  Eff.ext' (fun c => by bind_assoc_tac) (fun c => by bind_assoc_tac) (fun c => by bind_assoc_tac)

/-- The evaluator code `x` does exactly what `p` says: from any state with at least
    `p.cost` steps left, it gives `p.res`, leaves `p.eff`, and spends exactly `p.cost`
    steps. -/
def Acts (x : M α) (p : Eff α) : Prop :=
  ∀ s : St, p.cost s.toCore ≤ s.steps →
    x.run.run s = (p.res s.toCore, { toCore := p.eff s.toCore, steps := s.steps - p.cost s.toCore })

/-! ## `Acts` of the building blocks of `M` -/

/-- `x >>= f` does `p.bind q`, needing `f a` only for the values `x` can give. -/
theorem acts_bind_of {x : M α} {f : α → M β} {p : Eff α} {q : α → Eff β}
    (hx : Acts x p) (hf : ∀ c a, p.res c = .ok a → Acts (f a) (q a)) :
    Acts (x >>= f) (p.bind q) := by
  intro s hs
  rw [run_bind_eq]
  simp only [Eff.bind] at hs ⊢
  cases hr : p.res s.toCore with
  | error e =>
    rw [hr] at hs
    rw [hx s hs, hr]
  | ok a =>
    rw [hr] at hs
    simp only at hs
    rw [hx s (by omega), hr]
    simp only
    rw [hf _ a hr _ (by simp only; omega)]
    simp only
    congr 2
    omega

theorem acts_bind {x : M α} {f : α → M β} {p : Eff α} {q : α → Eff β}
    (hx : Acts x p) (hf : ∀ a, Acts (f a) (q a)) : Acts (x >>= f) (p.bind q) :=
  acts_bind_of hx fun _ a _ => hf a

theorem acts_congr {x : M α} {p q : Eff α} (h : Acts x p) (he : p = q) : Acts x q := he ▸ h

theorem acts_pure (a : α) : Acts (pure a : M α) (Eff.pure a) := by
  intro s _
  simp [Eff.pure]
  rfl

theorem acts_liftM (x : H α) : Acts (liftM x : M α) (Eff.ofH x) := by
  intro s _
  rw [run_liftM]
  rfl

theorem acts_tick : Acts tick Eff.tick := by
  intro ⟨c, n⟩ h
  simp only [Eff.tick] at h
  obtain ⟨n, rfl⟩ : ∃ m, n = m + 1 := ⟨n - 1, by omega⟩
  rfl

/-! ## T3a: operands -/

/-- **T3a, operands.** A list of expressions is evaluated left to right: if each part
    `es[i]` does `ps[i]`, the list does `Eff.seq ps`. Each part runs from the state the one
    before it left, and none runs after one that stopped (a panic, say). -/
theorem evalList_acts (ds : Decls) (d : Nat) :
    ∀ (es : List Expr) (ps : List (Eff Val)), es.length = ps.length →
      (∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) →
      Acts (evalList ds d es) (Eff.seq ps)
  | [], [], _, _ => by rw [evalList.eq_1]; exact acts_pure _
  | e :: es, p :: ps, hl, h => by
    rw [evalList.eq_2]
    exact acts_bind (h e p (List.mem_cons_self ..)) fun v =>
      acts_bind (evalList_acts ds d es ps (by simpa using hl)
        (fun e' p' hm => h e' p' (List.mem_cons_of_mem _ hm))) fun vs => acts_pure _
  | [], _ :: _, hl, _ | _ :: _, [], hl, _ => by simp at hl

set_option hygiene false in
/-- Follow `eval`'s match to the one case an expression of known shape takes. -/
macro "eval_case" : tactic => `(tactic| (
  rw [eval.eq_def ds (d + 1) _]
  dsimp only
  refine acts_bind acts_tick fun _ => ?_
  try split
  all_goals try first
    | (exfalso; simp_all; done)
    | (injections; subst_vars)))

/-- A binary operator other than `&&` and `||`: its left operand, then its right one,
    then the operator on their values. -/
theorem eval_binary_acts (ds : Decls) (d : Nat) (op : String) (l r : Expr) (pl pr : Eff Val)
    (h1 : op ≠ "&&") (h2 : op ≠ "||")
    (hl : Acts (eval ds d l) pl) (hr : Acts (eval ds d r) pr) :
    Acts (eval ds (d + 1) (.binary op l r))
      (Eff.tick.bind fun _ => pl.bind fun a => pr.bind fun b => Eff.ofH (binop op a b)) := by
  eval_case
  all_goals exact acts_bind hl fun a => acts_bind hr fun b => acts_liftM _

/-- A tuple: its elements left to right. -/
theorem eval_tuple_acts (ds : Decls) (d : Nat) (es : List Expr) (ps : List (Eff Val))
    (hl : es.length = ps.length) (h : ∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) :
    Acts (eval ds (d + 1) (.tuple es))
      (Eff.tick.bind fun _ => (Eff.seq ps).bind fun vs => Eff.pure (.tuple vs)) := by
  eval_case
  all_goals exact acts_bind (evalList_acts ds d _ ps hl h) fun _ => acts_pure _

/-- An interpolated string `s"..."`: its parts left to right, then the text. -/
theorem eval_interp_acts (ds : Decls) (d : Nat) (es : List Expr) (ps : List (Eff Val))
    (hl : es.length = ps.length) (h : ∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) :
    Acts (eval ds (d + 1) (.interp es))
      (Eff.tick.bind fun _ => (Eff.seq ps).bind fun vs => (Eff.ofH (printable ds vs)).bind
        fun _ => (Eff.ofH (printed (Print.interp ds vs) ds)).bind fun t => Eff.pure (.str t)) := by
  eval_case
  all_goals exact acts_bind (evalList_acts ds d _ ps hl h) fun _ =>
    acts_bind (acts_liftM _) fun _ => acts_bind (acts_liftM _) fun _ => acts_pure _

/-! ## T3b: `&&` and `||` -/

/-- **T3b, `&&`.** The left operand, then the right one only if the left one is `true`. -/
theorem eval_and_acts (ds : Decls) (d : Nat) (l r : Expr) (pl pr : Eff Val)
    (hl : Acts (eval ds d l) pl) (hr : Acts (eval ds d r) pr) :
    Acts (eval ds (d + 1) (.binary "&&" l r))
      (Eff.tick.bind fun _ => pl.bind fun a => (Eff.ofH (asBool a)).bind fun b =>
        if b then pr.bind fun c => (Eff.ofH (asBool c)).bind fun b' => Eff.pure (.bool b')
        else Eff.pure (.bool false)) := by
  eval_case
  all_goals refine acts_bind hl fun a => acts_bind (acts_liftM _) fun b => ?_
  all_goals cases b
  all_goals simp only [Bool.false_eq_true, ite_true, ite_false]
  all_goals first
    | exact acts_pure _
    | exact acts_bind hr fun c => acts_bind (acts_liftM _) fun _ => acts_pure _

/-- **T3b, `||`.** The left operand, then the right one only if the left one is `false`. -/
theorem eval_or_acts (ds : Decls) (d : Nat) (l r : Expr) (pl pr : Eff Val)
    (hl : Acts (eval ds d l) pl) (hr : Acts (eval ds d r) pr) :
    Acts (eval ds (d + 1) (.binary "||" l r))
      (Eff.tick.bind fun _ => pl.bind fun a => (Eff.ofH (asBool a)).bind fun b =>
        if b then Eff.pure (.bool true)
        else pr.bind fun c => (Eff.ofH (asBool c)).bind fun b' => Eff.pure (.bool b')) := by
  eval_case
  all_goals refine acts_bind hl fun a => acts_bind (acts_liftM _) fun b => ?_
  all_goals cases b
  all_goals simp only [Bool.false_eq_true, ite_true, ite_false]
  all_goals first
    | exact acts_pure _
    | exact acts_bind hr fun c => acts_bind (acts_liftM _) fun _ => acts_pure _

/-! ## The hypotheses can be met

A boolean literal does exactly one step and gives its value, so the theorems above apply to
real expressions: here `true && false` from its two literals. -/

theorem eval_bool_acts (ds : Decls) (d : Nat) (b : Bool) :
    Acts (eval ds (d + 1) (.lit (.bool b))) (Eff.tick.bind fun _ => Eff.pure (.bool b)) := by
  eval_case
  all_goals exact acts_pure _

example (ds : Decls) : Acts (eval ds 2 (.binary "&&" (.lit (.bool true)) (.lit (.bool false))))
    ⟨fun _ => .ok (.bool false), id, fun _ => 3⟩ :=
  acts_congr (eval_and_acts ds 1 _ _ _ _ (eval_bool_acts ds 0 true) (eval_bool_acts ds 0 false))
    (Eff.ext' (fun _ => rfl) (fun _ => rfl) (fun _ => rfl))

/-! ## T3d: compound assignment -/

/-- `t op= x` given what `x` does: one step, read `t`, then `x`, then the operator, then
    store into `t`. -/
def compoundEff (ds : Decls) (op : String) (t : Expr) (px : Eff Val) : Eff Val :=
  Eff.tick.bind fun _ => (Eff.ofH (readPlace ds t)).bind fun cur => px.bind fun v =>
    (Eff.ofH (binop (compoundOp op) cur v)).bind fun w =>
      (Eff.ofH (assignTo ds t w)).bind fun _ => Eff.pure .unit

/-- **T3d, compound assignment.** `t op= x` reads `t` first, from the state before `x`
    runs, then runs `x`, then stores the result. So when `x` itself assigns `t` (through a
    function, say), the result is computed from `t`'s value before that assignment. -/
theorem eval_compound_acts (ds : Decls) (d : Nat) (op : String) (t x : Expr) (px : Eff Val)
    (hop : op ≠ "=") (hx : Acts (eval ds d x) px) :
    Acts (eval ds (d + 1) (.assign op [t] [x])) (compoundEff ds op t px) := by
  unfold compoundEff
  eval_case

  all_goals first
    | exact acts_bind (acts_liftM _) fun _ => acts_bind hx fun _ =>
        acts_bind (acts_liftM _) fun _ => acts_bind (acts_liftM _) fun _ => acts_pure _
    | (exfalso; rename_i h; exact h _ _ rfl rfl)

/-- **T3d, what `t op= x` computes.** From a state `c` where `t` holds `x₀` and `x` gives
    `v`: the result `w` of `x₀ op v` is stored into the state `x` left. So `x₀` is `t`'s
    value before `x` runs, even when `x` itself assigns `t`. -/
theorem compound_eff (ds : Decls) (op : String) (t : Expr) (px : Eff Val) (c : Core)
    (x₀ v w : Val)
    (h0 : (readPlace ds t).run.run c = (.ok x₀, c)) (hv : px.res c = .ok v)
    (hw : (binop (compoundOp op) x₀ v).run.run (px.eff c) = (.ok w, px.eff c)) :
    (compoundEff ds op t px).eff c = ((assignTo ds t w).run.run (px.eff c)).2 ∧
      (compoundEff ds op t px).res c =
        (((assignTo ds t w).run.run (px.eff c)).1.map fun _ => Val.unit) := by
  simp only [compoundEff, Eff.bind, Eff.tick, Eff.ofH, Eff.pure, id, h0, hv, hw]
  constructor
  · split <;> rfl
  · split <;> rename_i h <;> simp [h, Except.map]

/-! ## T3e: sub-patterns -/

/-- Match `ps₁` against `vs₁`, then, only if they all matched, `ps₂` against `vs₂`. -/
def matchThen (ds : Decls) (locals globals : Env) (ps₁ ps₂ : List Pat) (vs₁ vs₂ : List Val) :
    H (Option (List (String × Val))) := do
  match ← matchPats ds locals globals ps₁ vs₁ with
  | none => pure none
  | some b => match ← matchPats ds locals globals ps₂ vs₂ with
    | none => pure none
    | some bs => pure (some (b ++ bs))

/-- **T3e, sub-patterns.** Sub-patterns are matched left to right: matching
    `ps₁ ++ ps₂` against `vs₁ ++ vs₂` matches `ps₁` first, and `ps₂` only if all of `ps₁`
    matched. In S1 a pattern prints nothing and changes no state, so the only observable
    content is that a later sub-pattern's `stuck` or `unsupported` never happens after an
    earlier one failed (`matchPats_stops`). -/
theorem matchPats_append (ds : Decls) (locals globals : Env) :
    ∀ (ps₁ ps₂ : List Pat) (vs₁ vs₂ : List Val), ps₁.length = vs₁.length →
      matchPats ds locals globals (ps₁ ++ ps₂) (vs₁ ++ vs₂) =
        matchThen ds locals globals ps₁ ps₂ vs₁ vs₂
  | [], ps₂, [], vs₂, _ => by
    simp only [matchThen, List.nil_append]
    rw [matchPats.eq_def ds locals globals [] [], pure_bind]
    conv => lhs; rw [← bind_pure (matchPats ds locals globals ps₂ vs₂)]
    congr 1
    funext r
    cases r <;> rfl
  | p :: ps₁, ps₂, v :: vs₁, vs₂, h => by
    have ih := matchPats_append ds locals globals ps₁ ps₂ vs₁ vs₂ (by simpa using h)
    rw [List.cons_append, List.cons_append, matchPats.eq_def ds locals globals (p :: _) (v :: _)]
    simp only [matchThen]
    rw [matchPats.eq_def ds locals globals (p :: _) (v :: _), bind_assoc]
    refine bind_congr fun r => ?_
    cases r with
    | none => simp
    | some b =>
      simp only [ih, matchThen, bind_assoc]
      refine bind_congr fun r' => ?_
      cases r' with
      | none => simp
      | some b' =>
        simp only [pure_bind, bind_assoc]
        refine bind_congr fun r'' => ?_
        cases r'' <;> simp
  | [], _, _ :: _, _, h | _ :: _, _, [], _, h => by simp at h

/-- **T3e, a failed sub-pattern stops the ones after it.** When `ps₁` fails to match `vs₁`,
    matching `ps₁ ++ ps₂` fails the same way, whatever `ps₂` is: a later sub-pattern that
    would be `stuck` or `unsupported` is never tried. -/
theorem matchPats_stops (ds : Decls) (locals globals : Env) (ps₁ ps₂ : List Pat)
    (vs₁ vs₂ : List Val) (hl : ps₁.length = vs₁.length) (c c' : Core)
    (h : (matchPats ds locals globals ps₁ vs₁).run.run c = (.ok none, c')) :
    (matchPats ds locals globals (ps₁ ++ ps₂) (vs₁ ++ vs₂)).run.run c = (.ok none, c') := by
  rw [matchPats_append ds locals globals ps₁ ps₂ vs₁ vs₂ hl, matchThen, run_bind_eq, h]
  rfl

/-! ## T3f: the arms of a `match` -/

/-- An `H` step that changes nothing: it only reads the state. -/
def ReadOnly (x : H α) : Prop := ∀ c, (x.run.run c).2 = c

theorem readOnly_bind {x : H α} {f : α → H β} (hx : ReadOnly x) (hf : ∀ a, ReadOnly (f a)) :
    ReadOnly (x >>= f) := by
  intro c
  rw [run_bind_eq]
  rcases h : x.run.run c with ⟨r, t⟩
  have ht : t = c := by have := hx c; rw [h] at this; exact this
  subst ht
  cases r with
  | ok a => exact hf a _
  | error e => rfl

theorem readOnly_pure (a : α) : ReadOnly (pure a : H α) := fun _ => rfl

theorem readOnly_throw (e : Stop) : ReadOnly (throw e : H α) := fun _ => rfl

theorem readOnly_deref (v : Val) : ReadOnly (deref v) := fun c => by rw [deref_run]

theorem readOnly_litMatches (l : Lit) (v : Val) : ReadOnly (litMatches l v) := by
  intro c; unfold litMatches; split <;> rfl

theorem readOnly_outsideS1 (ds : Decls) (msg : String) : ReadOnly (outsideS1 ds msg : H α) := by
  intro c; unfold outsideS1; split <;> rfl

set_option hygiene false in
macro "ro_step" : tactic => `(tactic| (first
  | with_reducible exact readOnly_pure _
  | exact readOnly_throw _
  | exact readOnly_deref _
  | exact readOnly_litMatches _ _
  | exact readOnly_outsideS1 _ _
  | with_reducible apply ihP
  | with_reducible apply ihPs
  | with_reducible apply readOnly_bind
  | with_reducible intro _
  | split
  | dsimp only))

mutual
theorem matchPat_readOnly (ds : Decls) (locals globals : Env) :
    ∀ (p : Pat) (v : Val), ReadOnly (matchPat ds locals globals p v)
  | .tuple ps, _ | .extract _ ps, _ => by
    have ihPs := fun vs => matchPats_readOnly ds locals globals ps vs
    rw [matchPat.eq_def ds locals globals _ _]
    dsimp only
    repeat' ro_step
  | .wild, _ | .unsupported _, _ | .lit _, _ | .name _, _ => by
    rw [matchPat.eq_def ds locals globals _ _]
    dsimp only
    repeat' ro_step
theorem matchPats_readOnly (ds : Decls) (locals globals : Env) :
    ∀ (ps : List Pat) (vs : List Val), ReadOnly (matchPats ds locals globals ps vs)
  | p :: ps, v :: vs => by
    have ihP := matchPat_readOnly ds locals globals p v
    have ihPs := matchPats_readOnly ds locals globals ps vs
    rw [matchPats.eq_def]
    repeat' ro_step
  | [], _ => by rw [matchPats.eq_def]; exact readOnly_pure _
  | _ :: _, [] => by rw [matchPats.eq_def]; exact readOnly_pure _
end

/-- The locals after a pattern's bindings are pushed. -/
def pushed (bs : List (String × Val)) (ρ : Env) : Env :=
  bs.foldl (fun ρ (x, v) => bind ρ x ⟨v, false⟩) ρ

theorem forIn_run_step (xs : List γ) (f : γ → PUnit → H (ForInStep PUnit)) (g : γ → Core → Core)
    (hf : ∀ a c, (f a ⟨⟩).run.run c = (.ok (.yield ⟨⟩), g a c)) (c : Core) :
    (forIn xs ⟨⟩ f).run.run c = (.ok ⟨⟩, xs.foldl (fun c a => g a c) c) := by
  induction xs generalizing c with
  | nil => rfl
  | cons a as ih =>
    rw [List.forIn_cons, run_bind_eq, hf]
    exact ih _

theorem pushAll_run (bs : List (String × Val)) (c : Core) :
    (pushAll bs false).run.run c = (.ok (), { c with locals := pushed bs c.locals }) := by
  unfold pushAll
  rw [run_bind_eq, forIn_run_step bs _ (fun xv c => { c with locals := bind c.locals xv.1 ⟨xv.2, false⟩ })
    (fun _ _ => rfl)]
  show (Except.ok (), _) = _
  congr 1
  unfold pushed
  induction bs generalizing c with
  | nil => rfl
  | cons xv bs ih => exact ih _

theorem pushed_append (bs : List (String × Val)) (ρ : Env) :
    ∃ pre, pushed bs ρ = pre ++ ρ := by
  induction bs generalizing ρ with
  | nil => exact ⟨[], rfl⟩
  | cons xv bs ih =>
    obtain ⟨x, v⟩ := xv
    obtain ⟨pre, h⟩ := ih (bind ρ x ⟨v, false⟩)
    simp only [pushed, List.foldl] at h ⊢
    rw [h]
    unfold bind
    split
    · exact ⟨pre, rfl⟩
    · exact ⟨pre ++ [(x, ⟨v, false⟩)], by simp⟩

theorem exitScope_pushed (bs : List (String × Val)) (ρ : Env) :
    exitScope (pushed bs ρ) ρ.length = ρ := by
  obtain ⟨pre, h⟩ := pushed_append bs ρ
  rw [h, exitScope]
  simp

theorem asBool_run (b : Bool) (c : Core) : (asBool (.bool b)).run.run c = (.ok b, c) := rfl

theorem popTo_run (depth : Nat) (c : Core) :
    (popTo depth).run.run c = (.ok (), { c with locals := exitScope c.locals depth }) := rfl

theorem matchPat_run (ds : Decls) (p : Pat) (v : Val) (c : Core) :
    (matchPat ds c.locals c.globals p v).run.run c =
      (((matchPat ds c.locals c.globals p v).run.run c).1, c) := by
  have := matchPat_readOnly ds c.locals c.globals p v c
  rcases h : (matchPat ds c.locals c.globals p v).run.run c with ⟨r, t⟩
  rw [h] at this
  simp only at this
  rw [this]

/-- **T3f, a false guard.** When an arm's pattern matches with bindings `bs` but its guard
    is false, the next arm runs from the state the guard left, with exactly the locals from
    before the arm: the arm's bindings are gone, while the guard's other effects (its
    output, heap, package-level variables) stay. Given: the guard does `pg` and leaves the
    locals as it found them. -/
theorem evalCases_guard_false (ds : Decls) (d : Nat) (v : Val) (p : Pat) (ge b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val)) (pg : Eff Val)
    (hm : ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs))
    (hg : Acts (eval ds d ge) pg)
    (hres : pg.res { s.toCore with locals := pushed bs s.locals } = .ok (.bool false))
    (hloc : (pg.eff { s.toCore with locals := pushed bs s.locals }).locals = pushed bs s.locals)
    (hcost : pg.cost { s.toCore with locals := pushed bs s.locals } ≤ s.steps) :
    (evalCases ds d v ((p, some ge, b) :: rest)).run.run s =
      (evalCases ds d v rest).run.run
        ⟨{ pg.eff { s.toCore with locals := pushed bs s.locals } with locals := s.locals },
         s.steps - pg.cost { s.toCore with locals := pushed bs s.locals }⟩ := by
  rw [evalCases.eq_2]
  simp only [run_bind_eq, run_liftM, run_getCore]
  rw [matchPat_run, hm]
  simp only [run_bind_eq, run_liftM, pushAll_run]
  rw [hg ⟨{ s.toCore with locals := pushed bs s.locals }, s.steps⟩ hcost]
  dsimp only at hres hloc ⊢
  rw [hres]
  dsimp only
  rw [asBool_run]
  simp only [Bool.false_eq_true, ite_false, run_bind_eq, run_liftM, popTo_run, hloc, exitScope_pushed]
  done

/-- **T3f, no arm left.** A `match` with no arm left panics (`panic match`), with the state
    unchanged. -/
theorem evalCases_nil (ds : Decls) (d : Nat) (v : Val) (s : St) :
    (evalCases ds d v []).run.run s = (.error (.panic .match_ "no match arm matched"), s) := by
  rw [evalCases.eq_1]
  rfl

/-- **T3f, a pattern that fails.** When an arm's pattern does not match, the next arm runs
    from the same state: nothing of the failed arm is left. -/
theorem evalCases_nomatch (ds : Decls) (d : Nat) (v : Val) (p : Pat) (g : Option Expr) (b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St)
    (hm : ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok none) :
    (evalCases ds d v ((p, g, b) :: rest)).run.run s = (evalCases ds d v rest).run.run s := by
  rw [evalCases.eq_2]
  simp only [run_bind_eq, run_liftM, run_getCore]
  rw [matchPat_run, hm]

/-- **T3f, the arm that runs.** When an arm's pattern matches with bindings `bs` and its
    guard is true, its body runs with the bindings in scope (after the guard's effects), and
    when the body ends normally the arm's bindings are dropped again. Given: the guard does
    `pg` and keeps the locals, the body does `pb`. -/
theorem evalCases_guard_true (ds : Decls) (d : Nat) (v : Val) (p : Pat) (ge b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val))
    (pg pb : Eff Val)
    (hm : ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs))
    (hg : Acts (eval ds d ge) pg) (hb : Acts (eval ds d b) pb)
    (hres : pg.res { s.toCore with locals := pushed bs s.locals } = .ok (.bool true))
    (hcost : pg.cost { s.toCore with locals := pushed bs s.locals } +
      pb.cost (pg.eff { s.toCore with locals := pushed bs s.locals }) ≤ s.steps) :
    let cg := pg.eff { s.toCore with locals := pushed bs s.locals }
    let n := s.steps - pg.cost { s.toCore with locals := pushed bs s.locals } - pb.cost cg
    (evalCases ds d v ((p, some ge, b) :: rest)).run.run s =
      match pb.res cg with
      | .ok r => (.ok r, ⟨{ pb.eff cg with locals := exitScope (pb.eff cg).locals s.locals.length }, n⟩)
      | .error e => (.error e, ⟨pb.eff cg, n⟩) := by
  intro cg n
  have h2 : pb.cost (pg.eff { s.toCore with locals := pushed bs s.locals }) ≤
      s.steps - pg.cost { s.toCore with locals := pushed bs s.locals } := by omega
  have h1 : pg.cost { s.toCore with locals := pushed bs s.locals } ≤ s.steps := by omega
  simp only [cg, n]
  rw [evalCases.eq_2]
  simp only [run_bind_eq, run_liftM, run_getCore]
  rw [matchPat_run, hm]
  simp only [run_bind_eq, run_liftM, pushAll_run]
  rw [hg ⟨{ s.toCore with locals := pushed bs s.locals }, s.steps⟩ h1]
  dsimp only at hres hcost ⊢
  rw [hres]
  dsimp only
  rw [asBool_run]
  simp only [ite_true, run_bind_eq]
  rw [hb _ h2]
  dsimp only
  generalize pb.res _ = r
  cases r <;> rfl

/-- **T3f, an arm without a guard.** When an arm with no guard matches with bindings `bs`,
    its body runs with them in scope, and when it ends normally they are dropped again.
    Given: the body does `pb`. With `evalCases_nomatch`, `evalCases_guard_false` and
    `evalCases_guard_true`, induction on the arms gives that the first arm whose pattern
    matches and whose guard holds is the one that runs, and `evalCases_nil` that with none
    left the match panics. -/
theorem evalCases_match (ds : Decls) (d : Nat) (v : Val) (p : Pat) (b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val)) (pb : Eff Val)
    (hm : ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs))
    (hb : Acts (eval ds d b) pb)
    (hcost : pb.cost { s.toCore with locals := pushed bs s.locals } ≤ s.steps) :
    let cb := { s.toCore with locals := pushed bs s.locals }
    let n := s.steps - pb.cost cb
    (evalCases ds d v ((p, none, b) :: rest)).run.run s =
      match pb.res cb with
      | .ok r => (.ok r, ⟨{ pb.eff cb with locals := exitScope (pb.eff cb).locals s.locals.length }, n⟩)
      | .error e => (.error e, ⟨pb.eff cb, n⟩) := by
  intro cb n
  simp only [cb, n]
  rw [evalCases.eq_2]
  simp only [run_bind_eq, run_liftM, run_getCore]
  rw [matchPat_run, hm]
  simp only [run_bind_eq, run_liftM, pushAll_run]
  rw [show ∀ st : St, (pure true : M Bool).run.run st = (.ok true, st) from fun _ => rfl]
  simp only [ite_true, run_bind_eq]
  rw [hb ⟨{ locals := pushed bs s.locals, globals := s.globals, out := s.out, heap := s.heap }, s.steps⟩ hcost]
  dsimp only
  generalize pb.res _ = r
  cases r <;> rfl

/-! ## T3c: named arguments -/

/-- An `H` step that gives `a` and changes nothing does what `Eff.pure a` does. -/
theorem acts_liftM_pure {x : H α} {a : α} (h : ∀ c, x.run.run c = (.ok a, c)) :
    Acts (liftM x : M α) (Eff.pure a) := by
  intro s _
  rw [run_liftM, h]
  rfl

/-- A loop whose every step only computes the next accumulator. -/
theorem acts_forIn_fold {β : Type} (xs : List γ) (b : β) {f : γ → β → M (ForInStep β)}
    (g : β → γ → β) (hf : ∀ a b, a ∈ xs → Acts (f a b) (Eff.pure (.yield (g b a)))) :
    Acts (forIn xs b f) (Eff.pure (xs.foldl g b)) := by
  induction xs generalizing b with
  | nil => exact acts_pure _
  | cons a as ih =>
    rw [List.forIn_cons]
    intro s hs
    rw [run_bind_eq, hf a b (List.mem_cons_self ..) s (by simp [Eff.pure])]
    exact ih (g b a) (fun a' b' h' => hf a' b' (List.mem_cons_of_mem _ h')) _ hs

theorem seq_length : ∀ (ps : List (Eff α)) (c : Core) (vs : List α),
    (Eff.seq ps).res c = .ok vs → vs.length = ps.length
  | [], c, vs, h => by simp [Eff.seq, Eff.pure] at h; subst h; rfl
  | p :: ps, c, vs, h => by
    simp only [Eff.seq, Eff.bind, Eff.pure] at h
    split at h
    · split at h
      · simp at h; subst h; simp only [List.length_cons]; congr 1
        exact seq_length ps _ _ (by assumption)
      · simp at h
    · simp at h
/-- The parameter argument number `i` goes to: its position if positional, else the
    parameter it names. -/
def slotOf (names : List String) : Expr × Nat → Nat
  | (.named n _, _) => (names.idxOf? n).getD 0
  | (_, i) => i

theorem argSlots_go_eq (names : List String) :
    ∀ (as : List Expr) (named : Bool) (acc slots : List Nat),
      argSlots.go names as named acc = .ok slots →
        slots = acc ++ (as.zipIdx acc.length).map (slotOf names)
  | [], _, acc, slots, h => by
    simp only [argSlots.go] at h
    split at h
    · simp at h
    · simp at h; subst h; simp
  | .named n v :: as, _, acc, slots, h => by
    simp only [argSlots.go] at h
    split at h
    · rename_i j hj
      rw [argSlots_go_eq names as _ _ _ h]
      simp [List.zipIdx_cons, slotOf, hj]
    · simp at h
  | a :: as, named, acc, slots, h => by
    unfold argSlots.go at h
    split at h
    · simp_all
    · rename_i heq
      injection heq with ha has
      subst ha; subst has
      split at h
      · rename_i j hj
        rw [argSlots_go_eq names _ _ _ _ h]
        simp [List.zipIdx_cons, slotOf, hj]
      · simp at h
    · rename_i hnamed heq
      injection heq with ha has
      subst ha; subst has
      split at h
      · simp at h
      · rw [argSlots_go_eq names _ _ _ _ h]
        simp only [List.zipIdx_cons, List.map_cons, List.length_append, List.length_singleton,
          List.append_assoc, List.singleton_append]
        congr 2
        cases a <;> first | rfl | (exfalso; exact hnamed _ _ rfl)

/-- **What `argSlots` computes.** Argument number `i` goes to parameter `i` if it is
    positional, and to the parameter it names otherwise. -/
theorem argSlots_eq (names : List String) (args : List Expr) (slots : List Nat)
    (h : argSlots names args = .ok slots) : slots = args.zipIdx.map (slotOf names) := by
  simpa using argSlots_go_eq names args false [] slots h

theorem argSlots_length (names : List String) (args : List Expr) (slots : List Nat)
    (h : argSlots names args = .ok slots) : slots.length = args.length := by
  rw [argSlots_eq names args slots h]
  simp

theorem lookup_zip_isSome : ∀ (l : List Nat) (vs : List Val) (j : Nat), j ∈ l →
    l.length = vs.length → ((l.zip vs).lookup j).isSome
  | k :: l, v :: vs, j, hm, hl => by
    simp only [List.zip_cons_cons, List.lookup_cons]
    by_cases hjk : j = k
    · subst hjk; simp
    · have : (j == k) = false := by simp [hjk]
      rw [this]
      exact lookup_zip_isSome l vs j (by simpa [hjk] using hm) (by simpa using hl)
  | [], _, _, hm, _ => by simp at hm
  | _ :: _, [], _, _, hl => by simp at hl

/-- `q` over `xs` in order, threading an accumulator: each step from the state the one
    before left; the first `Stop` ends it. -/
def Eff.foldlM (q : γ → β → Eff β) : List γ → β → Eff β
  | [], b => Eff.pure b
  | a :: as, b => (q a b).bind fun b' => Eff.foldlM q as b'

/-- A loop whose every step does `q` and goes on. -/
theorem acts_forIn_eff {β : Type} (xs : List γ) (b : β) {f : γ → β → M (ForInStep β)}
    (q : γ → β → Eff β)
    (hf : ∀ a b, a ∈ xs → Acts (f a b) ((q a b).bind fun b' => Eff.pure (.yield b'))) :
    Acts (forIn xs b f) (Eff.foldlM q xs b) := by
  induction xs generalizing b with
  | nil => exact acts_pure _
  | cons a as ih =>
    rw [List.forIn_cons]
    have ih' := fun b' => ih b' (fun a' b'' h' => hf a' b'' (List.mem_cons_of_mem _ h'))
    intro s hs
    rw [run_bind_eq]
    have hq := hf a b (List.mem_cons_self ..)
    simp only [Eff.foldlM, Eff.bind] at hs ⊢
    cases hr : (q a b).res s.toCore with
    | error e =>
      rw [hr] at hs
      rw [hq s (by simp only [Eff.bind, hr]; exact hs)]
      simp only [Eff.bind, hr]
    | ok b' =>
      rw [hr] at hs
      simp only at hs
      rw [hq s (by simp only [Eff.bind, hr, Eff.pure]; omega)]
      simp only [Eff.bind, hr, Eff.pure]
      rw [ih' b' _ (by simp only [id] at hs ⊢; omega)]
      simp only [id]
      congr 2
      omega

/-- One parameter of the defaults phase: the value the call gave it (`gvn`, by parameter index), or else its
    default, run in its frame (the earlier parameters' values, or no local for a field's). -/
def defaultStep (names : List String) (fs : Bool) (gvn : List (Nat × Val))
    (pd : Nat → Env → Eff Val) (nj : String × Nat) (vals : List Val) : Eff (List Val) :=
  match gvn.lookup nj.2 with
  | some v => Eff.pure (vals ++ [v])
  | none => (pd nj.2 (if fs then [] else defaultFrame (names.zip vals))).bind fun v =>
      Eff.pure (vals ++ [v])

/-- **T3c, defaults.** The defaults phase goes through the parameters in order: a gvn
    one's value, or else its default, each default run in its frame after the defaults
    before it. Given: every parameter the call leaves out has a default; each such default
    passes its by-name check and, run in a frame, does `pd j frame`. -/
theorem bindDefaults_acts (ds : Decls) (d : Nat) (names : List String) (tys : List (Option Ty))
    (dflts : List (Option Expr)) (fs : Bool) (gvn : List (Nat × Val)) (pd : Nat → Env → Eff Val)
    (hd : ∀ j, j < names.length → gvn.lookup j = none → ∃ e, dflts.getD j none = some e ∧
      (∀ frame, Acts (runIn ds d frame e) (pd j frame)) ∧
      (∀ frame (c : Core), (byNameOk ds frame (tys.getD j none) e).run.run c = (.ok true, c))) :
    Acts (bindDefaults ds d names tys dflts fs gvn)
      (Eff.foldlM (defaultStep names fs gvn pd) names.zipIdx []) := by
  rw [bindDefaults.eq_1]
  refine acts_congr (acts_bind (acts_forIn_eff _ _ (defaultStep names fs gvn pd) ?_)
    fun r => acts_pure r) ?_
  · intro nj vals hm
    obtain ⟨n, j⟩ := nj
    have hj : j < names.length := by simpa using (List.mem_zipIdx hm).2.1
    simp only [defaultStep]
    cases hg : gvn.lookup j with
    | some v =>
      simp only
      exact acts_pure _
    | none =>
      obtain ⟨e, he, hrun, hbn⟩ := hd j hj hg
      simp only [he]
      generalize (if fs = true then [] else defaultFrame (names.zip vals)) = frame
      refine acts_congr (acts_bind_of (q := fun _ => (pd j frame).bind fun v =>
          Eff.pure (ForInStep.yield (vals ++ [v])))
        (acts_liftM_pure (hbn frame)) fun c b hb => ?_) ?_
      · have : b = true := by simpa [Eff.pure] using hb.symm
        subst this
        simp only [ite_true]
        exact acts_bind (hrun frame) fun v => acts_pure _
      · rw [Eff.pure_bind, Eff.bind_assoc]
        simp only [Eff.pure_bind]
  · exact Eff.bind_pure _

theorem lookup_zip_none (l : List Nat) (vs : List Val) (j : Nat) (hl : l.length = vs.length)
    (h : (l.zip vs).lookup j = none) : j ∉ l := fun hm => by
  simpa [h] using lookup_zip_isSome l vs j hm hl

/-- **T3c, a call's arguments.** The arguments a call writes run first, in the order
    written (named ones included), then the defaults of the parameters it leaves out, in
    parameter order, each in its frame. Given: the arguments are well formed (`argSlots`
    gives each its parameter, `slots`); each written argument passes its by-name check and
    its value expression does `ps[i]`; each left-out parameter has a default that passes its
    by-name check and, in a frame, does `pd j frame`. -/
theorem bindArgs_acts (ds : Decls) (d : Nat) (names : List String) (tys : List (Option Ty))
    (dflts : List (Option Expr)) (fs : Bool) (args : List Expr) (slots : List Nat)
    (ps : List (Eff Val)) (pd : Nat → Env → Eff Val)
    (hs : argSlots names args = .ok slots)
    (hbn : ∀ a j, (a, j) ∈ args.zip slots → ∀ (locals : Env) (c : Core),
      (byNameOk ds locals (tys.getD j none) (unnamed a)).run.run c = (.ok true, c))
    (hl : args.length = ps.length)
    (hp : ∀ a p, (a, p) ∈ args.zip ps → Acts (eval ds d (unnamed a)) p)
    (hd : ∀ j, j < names.length → j ∉ slots → ∃ e, dflts.getD j none = some e ∧
      (∀ frame, Acts (runIn ds d frame e) (pd j frame)) ∧
      (∀ frame (c : Core), (byNameOk ds frame (tys.getD j none) e).run.run c = (.ok true, c))) :
    Acts (bindArgs ds d names tys dflts fs args)
      ((Eff.seq ps).bind fun vs =>
        Eff.foldlM (defaultStep names fs (slots.zip vs) pd) names.zipIdx []) := by
  have hsl := argSlots_length names args slots hs
  rw [bindArgs.eq_1]
  simp only [hs, pure_bind]
  refine acts_congr (acts_bind (acts_liftM getCore) fun c0 =>
    acts_bind (acts_forIn_fold _ _ (fun _ _ => ⟨⟩) ?_) fun _ =>
    acts_bind_of (evalList_acts ds d _ ps (by simpa using hl) ?_) fun c vs hvs =>
    bindDefaults_acts ds d names tys dflts fs _ pd fun j hj hn =>
      hd j hj (lookup_zip_none slots vs j (by rw [seq_length ps c vs hvs]; omega) hn)) ?_
  · intro aj _ hm
    obtain ⟨a, j⟩ := aj
    refine acts_congr (acts_bind_of (q := fun _ => Eff.pure (.yield ⟨⟩))
      (acts_liftM_pure (hbn a j hm c0.locals)) fun c b hb => ?_) ?_
    · have : b = true := by simpa [Eff.pure] using hb.symm
      subst this
      exact acts_pure _
    · rfl
  · intro e p hm
    rw [List.zip_map_left] at hm
    obtain ⟨⟨a, p'⟩, hm', heq⟩ := List.mem_map.mp hm
    simp only [Prod.map, id, Prod.mk.injEq] at heq
    obtain ⟨rfl, rfl⟩ := heq
    exact hp _ _ hm'
  · simp only [Eff.bind, Eff.pure, Eff.ofH, run_getCore, Nat.zero_add]
    congr

end Gala.Spec
