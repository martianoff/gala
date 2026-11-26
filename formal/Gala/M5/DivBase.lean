/-
  # M5, step 3d (c): divergence

  docs/FORMAL_SEMANTICS.MD, M5. Steps 3a to 3c relate galaspec's runs that end to GoCore's
  (`calls_correct`), with GoCore given at least galaspec's steps. This file adds the other
  direction of the count, a lower bound: galaspec spends at most `Q` times GoCore's steps
  plus the size of the code it starts on (`Stut`), `Q` a constant of the program (`kszOf`:
  the largest function body's size, plus one). From it: galaspec running out of steps with
  `Q * (m + 1) + esz e` of them is GoCore running out with `m` (`calls_fuel`), and galaspec
  running out at every budget is GoCore never ending normally, at any depth and any budget
  (`calls_diverge`).

  The fuel claim gives GoCore a small budget, the forward theorems a large one. A part of
  the code that galaspec finishes is related at a large budget (`shift k g`) and brought
  back down with step 3d (a)'s `simAll`: with fewer steps GoCore either runs out on that
  part, or ends it as with more, the same number of steps fewer (`down`).
-/
import Gala.M5.Loop
import Gala.M5.GoFuel
namespace Gala.M5
open Gala.GoCore.Full

/-! ## Sizes and the program's constant -/

mutual
/-- An expression's size: one for each node. galaspec's `eval` ticks once per node it
    evaluates, and evaluates each node at most once outside calls, so this bounds the steps
    galaspec spends on `e`'s own nodes. -/
def esz : Spec.Expr → Nat
  | .lit _ | .name _ | .unsupported _ => 1
  | .unary _ e | .select e _ | .paren e | .named _ e | .incdec _ e => esz e + 1
  | .binary _ l r => esz l + esz r + 1
  | .call f as => esz f + eszs as + 1
  | .tuple es | .block es | .interp es => eszs es + 1
  | .ifExpr c a b => esz c + esz a + esz b + 1
  | .mtch x cs => esz x + eszArms cs + 1
  | .ifStmt c a b => esz c + esz a + eszOpt b + 1
  | .ret v => eszOpt v + 1
  | .assign _ ts vs => eszs ts + eszs vs + 1
  | .decl _ _ _ _ vs => eszs vs + 1
  | .lambda _ _ b => esz b + 1
/-- A list's: the sum. -/
def eszs : List Spec.Expr → Nat
  | [] => 0
  | e :: es => esz e + eszs es
/-- An optional expression's. -/
def eszOpt : Option Spec.Expr → Nat
  | none => 0
  | some e => esz e
/-- A `match`'s arms': guards and bodies. -/
def eszArms : List (Spec.Pat × Option Spec.Expr × Spec.Expr) → Nat
  | [] => 0
  | (_, g, b) :: cs => eszOpt g + esz b + eszArms cs
end

/-- `Q` bounds the steps one GoCore step pays for: at least one, and more than every
    function body's size. -/
def KszOK (ds : Spec.Decls) (Q : Nat) : Prop :=
  1 ≤ Q ∧ ∀ f F, ds.funcs.lookup f = some F → esz F.body + 1 ≤ Q

/-- **The program's constant**: the largest function body's size, plus one. -/
def kszOf (ds : Spec.Decls) : Nat :=
  ds.funcs.foldl (fun m p => max m (esz p.2.body)) 0 + 1

theorem foldl_max_ge : ∀ (l : List (String × Spec.Func)) (m : Nat),
    m ≤ l.foldl (fun m p => max m (esz p.2.body)) m
  | [], _ => Nat.le_refl _
  | _ :: l, _ => Nat.le_trans (Nat.le_max_left _ _) (foldl_max_ge l _)

theorem foldl_max_mem : ∀ (l : List (String × Spec.Func)) (m : Nat) (p : String × Spec.Func), p ∈ l →
    esz p.2.body ≤ l.foldl (fun m p => max m (esz p.2.body)) m
  | [], _, _, h => nomatch h
  | q :: l, m, p, h => by
    rcases List.mem_cons.mp h with rfl | h
    · exact Nat.le_trans (Nat.le_max_right _ _) (foldl_max_ge l _)
    · exact foldl_max_mem l _ p h

theorem lookup_mem' : ∀ (l : List (String × Spec.Func)) (f : String) (F : Spec.Func),
    l.lookup f = some F → (f, F) ∈ l
  | [], _, _, h => nomatch h
  | (a, b) :: l, f, F, h => by
    simp only [List.lookup] at h
    split at h
    · rename_i hb; cases h; simp only [beq_iff_eq] at hb; subst hb; exact List.mem_cons_self
    · exact List.mem_cons_of_mem _ (lookup_mem' l f F h)

/-- `kszOf` is such a constant. -/
theorem kszOf_ok (ds : Spec.Decls) : KszOK ds (kszOf ds) :=
  ⟨Nat.succ_le_succ (Nat.zero_le _), fun f F h =>
    Nat.succ_le_succ (foldl_max_mem ds.funcs 0 (f, F) (lookup_mem' _ f F h))⟩

/-! ## The call-free parts' cost -/

section Cost
open Spec (H M eval)

/-- `x` spends at most `n` steps. -/
def Spend (n : Nat) (x : M α) : Prop := ∀ s, s.steps ≤ (x.run.run s).2.steps + n

/-- `x` runs out of fuel only when started with fewer than `n` steps. -/
def FuelLt (n : Nat) (x : M α) : Prop := ∀ s, (x.run.run s).1 = .error .fuel → s.steps < n

/-- A step-free computation that never runs out of fuel. -/
def HNoFuel (x : H α) : Prop := ∀ c, (x.run.run c).1 ≠ .error .fuel

theorem spend_mono {n m : Nat} {x : M α} (h : Spend n x) (hle : n ≤ m) : Spend m x :=
  fun s => Nat.le_trans (h s) (Nat.add_le_add_left hle _)
theorem fuelLt_mono {n m : Nat} {x : M α} (h : FuelLt n x) (hle : n ≤ m) : FuelLt m x :=
  fun s hs => Nat.lt_of_lt_of_le (h s hs) hle
theorem spend_pure (a : α) : Spend 0 (pure a : M α) := fun _ => Nat.le_refl _
theorem fuelLt_pure (a : α) : FuelLt 0 (pure a : M α) := fun _ h => nomatch h
theorem spend_liftM (x : H α) : Spend 0 (liftM x : M α) := fun s => by
  rw [Spec.run_liftM]; exact Nat.le_refl _
theorem fuelLt_liftM {x : H α} (hx : HNoFuel x) : FuelLt 0 (liftM x : M α) := fun s h => by
  rw [Spec.run_liftM] at h; exact absurd h (hx _)
theorem spend_tick : Spend 1 Spec.tick := fun s => by
  rcases s with ⟨c, n⟩; cases n <;> simp [tick_run]
theorem fuelLt_tick : FuelLt 1 Spec.tick := fun s h => by
  rcases s with ⟨c, n⟩; cases n <;> simp_all [tick_run]
theorem spend_bind {a b : Nat} {x : M α} {f : α → M β} (hx : Spend a x) (hf : ∀ v, Spend b (f v)) :
    Spend (a + b) (x >>= f) := fun s => by
  rw [Spec.run_bind_eq]
  rcases h : x.run.run s with ⟨r, t⟩
  have := hx s; rw [h] at this
  cases r with
  | error => simp only at this ⊢; omega
  | ok v => have h2 := hf v t; simp only at this h2 ⊢; omega
theorem fuelLt_bind {a b : Nat} {x : M α} {f : α → M β} (hs : Spend a x) (hx : FuelLt a x)
    (hf : ∀ v, FuelLt b (f v)) : FuelLt (a + b) (x >>= f) := fun s => by
  rw [Spec.run_bind_eq]
  rcases h : x.run.run s with ⟨r, t⟩
  have h1 := hs s; rw [h] at h1
  cases r with
  | error e => intro he; simp only at he; have := hx s (by rw [h]; simpa using he); omega
  | ok v => intro he; have := hf v t he; simp only at h1; omega

theorem hnf_unop (op : String) (v : Spec.Val) : HNoFuel (Spec.unop op v) := by
  intro c; unfold Spec.unop; split <;> exact fun h => nomatch h
theorem hnf_intLit (n : Int) : HNoFuel (Spec.intLit n) := by
  intro c; unfold Spec.intLit; split <;> exact fun h => nomatch h
theorem hnf_binop (op : String) (a b : Spec.Val) : HNoFuel (Spec.binop op a b) := by
  intro c; unfold Spec.binop
  split <;> (try split) <;> (try split) <;> exact fun h => nomatch h
theorem hnf_asBool (v : Spec.Val) : HNoFuel (Spec.asBool v) := by
  intro c; unfold Spec.asBool; split <;> exact fun h => nomatch h
theorem hnf_deref (v : Spec.Val) : HNoFuel (Spec.deref v) := by
  intro c; unfold Spec.deref; split
  · rw [Spec.run_get_bind]; split <;> exact fun h => nomatch h
  · exact fun h => nomatch h

theorem hnf_getCore : HNoFuel Spec.getCore := fun _ h => nomatch h

theorem spend_throw (e : Spec.Stop) : Spend 0 (throw e : M α) := fun _ => Nat.le_refl _

/-- **A call-free part's cost**: galaspec spends at most its size. -/
theorem eval_spend (ds : Spec.Decls) : ∀ (e : Spec.Expr) (d : Nat), CFShape e → Spend (esz e) (eval ds d e)
  | e, 0, _ => by rw [eval.eq_def]; exact spend_mono (spend_throw _) (Nat.zero_le _)
  | .lit l, d + 1, _ => by
    rw [eval.eq_def]
    refine spend_mono (spend_bind spend_tick fun _ => (?_ : Spend 0 _)) (by simp [esz])
    cases l <;> first | exact spend_liftM _ | exact spend_pure _
  | .name x, d + 1, _ => by
    rw [eval.eq_def]
    refine spend_mono (spend_bind spend_tick fun _ => (?_ : Spend 0 _)) (by simp [esz])
    dsimp only
    refine spend_mono (spend_bind (spend_liftM _) fun _ => (?_ : Spend 0 _)) (by omega)
    split <;> (try split) <;> first | exact spend_liftM _ | exact spend_pure _
  | .paren x, d + 1, h => by
    have ih := eval_spend ds x d h
    rw [eval.eq_def]
    exact spend_mono (spend_bind spend_tick fun _ => ih) (by simp [esz]; omega)
  | .unary op x, d + 1, h => by
    have ih := eval_spend ds x d h
    rw [eval.eq_def]
    refine spend_mono (spend_bind spend_tick fun _ => (?_ : Spend (esz x) _)) (by simp [esz]; omega)
    dsimp only
    split
    all_goals first
      | with_reducible exact spend_mono (spend_liftM _) (Nat.zero_le _)
      | with_reducible exact spend_bind ih fun _ => spend_liftM _
      | (exfalso; simp_all; done)
      | (rename_i hh; obtain ⟨rfl, rfl⟩ := hh; exact spend_bind ih fun _ => spend_liftM _)
  | .binary op l r, d + 1, h => by
    have ihl := eval_spend ds l d h.1
    have ihr := eval_spend ds r d h.2
    rw [eval.eq_def]
    refine spend_mono (spend_bind spend_tick fun _ => (?_ : Spend (esz l + esz r) _)) (by simp [esz]; omega)
    dsimp only
    split
    case h_11 | h_12 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      refine spend_bind ihl fun _ => spend_mono (spend_bind (spend_liftM _) fun b => (?_ : Spend (esz r) _)) (by omega)
      split <;> first
        | exact spend_mono (spend_bind ihr fun _ => spend_bind (spend_liftM _) fun _ => spend_pure _) (by omega)
        | exact spend_mono (spend_pure _) (Nat.zero_le _)
    case h_13 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      exact spend_bind ihl fun _ => spend_mono (spend_bind ihr fun _ => spend_liftM _) (by omega)
    all_goals (exfalso; simp_all)
  | .call .., _ + 1, h | .select .., _ + 1, h | .tuple _, _ + 1, h | .ifExpr .., _ + 1, h | .mtch .., _ + 1, h
  | .block _, _ + 1, h | .ifStmt .., _ + 1, h | .ret _, _ + 1, h | .assign .., _ + 1, h | .incdec .., _ + 1, h
  | .decl .., _ + 1, h | .interp _, _ + 1, h | .lambda .., _ + 1, h | .named .., _ + 1, h
  | .unsupported _, _ + 1, h => h.elim

/-- **A call-free part runs out of fuel** only when started with fewer steps than its size,
    given at least its size in depth. -/
theorem eval_fuelLt (ds : Spec.Decls) : ∀ (e : Spec.Expr) (d : Nat), CFShape e → esz e ≤ d →
    FuelLt (esz e) (eval ds d e)
  | .lit l, d + 1, _, _ => by
    rw [eval.eq_def]
    refine fuelLt_mono (fuelLt_bind spend_tick fuelLt_tick fun _ => (?_ : FuelLt 0 _)) (by simp [esz])
    cases l <;> first | exact fuelLt_liftM (hnf_intLit _) | exact fuelLt_pure _
  | .name x, d + 1, _, _ => by
    rw [eval.eq_def]
    refine fuelLt_mono (fuelLt_bind spend_tick fuelLt_tick fun _ => (?_ : FuelLt 0 _)) (by simp [esz])
    dsimp only
    refine fuelLt_mono (fuelLt_bind (spend_liftM _) (fuelLt_liftM hnf_getCore) fun _ => (?_ : FuelLt 0 _)) (by omega)
    split <;> (try split) <;>
      first
      | exact fuelLt_liftM (hnf_deref _)
      | exact fuelLt_pure _
      | (refine fuelLt_liftM fun c => ?_; unfold Spec.unresolved; split <;> exact fun h => nomatch h)
      | exact fuelLt_liftM fun c h => nomatch h
  | .paren x, d + 1, h, hd => by
    have ih := eval_fuelLt ds x d h (by simp [esz] at hd; omega)
    rw [eval.eq_def]
    exact fuelLt_mono (fuelLt_bind spend_tick fuelLt_tick fun _ => ih) (by simp [esz]; omega)
  | .unary op x, d + 1, h, hd => by
    have ihs := eval_spend ds x d h
    have ih := eval_fuelLt ds x d h (by simp [esz] at hd; omega)
    rw [eval.eq_def]
    refine fuelLt_mono (fuelLt_bind spend_tick fuelLt_tick fun _ => (?_ : FuelLt (esz x) _)) (by simp [esz]; omega)
    dsimp only
    split
    all_goals first
      | with_reducible exact fuelLt_mono (fuelLt_liftM (hnf_intLit _)) (Nat.zero_le _)
      | with_reducible exact fuelLt_bind ihs ih fun _ => fuelLt_liftM (hnf_unop _ _)
      | (exfalso; simp_all; done)
      | (rename_i hh; obtain ⟨rfl, rfl⟩ := hh; exact fuelLt_bind ihs ih fun _ => fuelLt_liftM (hnf_unop _ _))
  | .binary op l r, d + 1, h, hd => by
    have ihsl := eval_spend ds l d h.1
    have ihsr := eval_spend ds r d h.2
    have ihl := eval_fuelLt ds l d h.1 (by simp [esz] at hd; omega)
    have ihr := eval_fuelLt ds r d h.2 (by simp [esz] at hd; omega)
    rw [eval.eq_def]
    refine fuelLt_mono (fuelLt_bind spend_tick fuelLt_tick fun _ => (?_ : FuelLt (esz l + esz r) _))
      (by simp [esz]; omega)
    dsimp only
    split
    case h_11 | h_12 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      refine fuelLt_bind ihsl ihl fun _ => fuelLt_mono (fuelLt_bind (spend_liftM _) (fuelLt_liftM (hnf_asBool _))
        fun b => (?_ : FuelLt (esz r) _)) (by omega)
      split <;> first
        | exact fuelLt_mono (fuelLt_bind ihsr ihr fun _ => fuelLt_bind (spend_liftM _)
            (fuelLt_liftM (hnf_asBool _)) fun _ => fuelLt_pure _) (by omega)
        | exact fuelLt_mono (fuelLt_pure _) (Nat.zero_le _)
    case h_13 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      exact fuelLt_bind ihsl ihl fun _ => fuelLt_mono (fuelLt_bind ihsr ihr fun _ => fuelLt_liftM (hnf_binop _ _ _)) (by omega)
    all_goals (exfalso; simp_all)
  | .lit _, 0, _, hd | .name _, 0, _, hd | .paren _, 0, _, hd | .unary .., 0, _, hd | .binary .., 0, _, hd => by
    simp [esz] at hd
  | .call .., _, h, _ | .select .., _, h, _ | .tuple _, _, h, _ | .ifExpr .., _, h, _ | .mtch .., _, h, _
  | .block _, _, h, _ | .ifStmt .., _, h, _ | .ret _, _, h, _ | .assign .., _, h, _ | .incdec .., _, h, _
  | .decl .., _, h, _ | .interp _, _, h, _ | .lambda .., _, h, _ | .named .., _, h, _
  | .unsupported _, _, h, _ => h.elim

end Cost

/-! ## The lower bound, and coming down from a larger budget -/

/-- **galaspec spends at most `Q` times GoCore's steps, plus `z`**: where galaspec ends
    with a value or a `return`. -/
def Stut (Q z : Nat) (s : Spec.St) (g : St) : Except Spec.Stop β × Spec.St → Except Stop α × St → Prop
  | (.ok _, s'), (_, g') => s.steps + Q * g'.steps ≤ s'.steps + Q * g.steps + z
  | (.error (.ret _), s'), (_, g') => s.steps + Q * g'.steps ≤ s'.steps + Q * g.steps + z
  | _, _ => True

/-- With fewer steps, a computation runs out, or ends as with more, that many steps fewer. -/
theorem down {x : GM α} {k : Nat} (hx : GSim k x x) (g : St) {r : Except Stop α} {g1 : St}
    (h : x.run.run (shift k g) = (r, g1)) :
    (x.run.run g).1 = .error .fuel ∨ ∃ g0, x.run.run g = (r, g0) ∧ g1 = shift k g0 := by
  rcases hg : x.run.run g with ⟨r0, g0⟩
  cases r0 with
  | error e =>
    cases e with
    | fuel => exact .inl rfl
    | _ =>
      have := hx.prop g (by rw [hg]; trivial)
      rw [hg, h] at this
      cases this
      exact .inr ⟨g0, rfl, rfl⟩
  | ok a =>
    have := hx.prop g (by rw [hg]; trivial)
    rw [hg, h] at this
    cases this
    exact .inr ⟨g0, rfl, rfl⟩

theorem inv3_shift {cx : Ctx} {P : String} {sg : Sigs} {κ : String → Option Kind} {c : Spec.Core}
    {g : St} {env : Env} (k : Nat) : Inv3 cx P sg κ c (shift k g) env ↔ Inv3 cx P sg κ c g env :=
  ⟨fun h => ⟨⟨h.inv.loc, h.inv.out, h.inv.pre⟩, h.pkg, h.tenv, h.closed, h.globals, h.sync⟩,
   fun h => ⟨⟨h.inv.loc, h.inv.out, h.inv.pre⟩, h.pkg, h.tenv, h.closed, h.globals, h.sync⟩⟩

theorem gframe_shift {g g' : St} (k : Nat) : GFrame (shift k g) (shift k g') ↔ GFrame g g' := by
  rcases g with ⟨⟩; rcases g' with ⟨⟩
  simp only [GFrame, shift, St.mk.injEq]
  constructor <;> rintro ⟨h1, h2, h3, h4, h5, h6⟩ <;> exact ⟨h1, h2, h3, h4, by omega, h6⟩

theorem gframeP_shift {n : Nat} {g g' : St} (k : Nat) : GFrameP n (shift k g) (shift k g') ↔ GFrameP n g g' := by
  rcases g with ⟨⟩; rcases g' with ⟨⟩
  simp only [GFrameP, shift, St.mk.injEq]
  constructor <;> rintro ⟨h0, h1, h2, h3, h4, h5, h6⟩ <;> exact ⟨h0, h1, h2, h3, h4, by omega, h6⟩

/-! ## GoCore runs out of fuel -/

theorem gtick_zero (g : St) (hk : g.steps = 0) : ((tick : GM Unit).run.run g).1 = .error .fuel := by
  obtain ⟨cells, types, cl, out, steps, pr, gl, de, re, ma⟩ := g
  simp only at hk; subst hk; rfl

theorem grun_bind_fuel {x : GM α} {f : α → GM β} {g : St} (h : (x.run.run g).1 = .error .fuel) :
    ((x >>= f).run.run g).1 = .error .fuel := by
  rw [grun_bind]
  rcases hx : x.run.run g with ⟨r, t⟩
  rw [hx] at h; simp only at h; subst h; rfl

/-- A call with no step left runs out of fuel. -/
theorem callBody_zero (c : Ctx) (n : Nat) (P : String) (fi : Nat) (xks : List (String × Kind))
    (ρ : Kind) (body : List Stmt) (vs : List Val) (g : St) (hk : g.steps = 0) :
    ((callBody c (n + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some body) none vs false).run.run g).1 =
      .error .fuel := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  exact grun_bind_fuel (gtick_zero g hk)

/-- A call whose body runs out of fuel runs out of fuel. -/
theorem callBody_fuel (c : Ctx) (n : Nat) (P : String) (fi : Nat) (xks : List (String × Kind))
    (ρ : Kind) (body : List Stmt) (vs : List Val) (g : St) (k : Nat) (hk : g.steps = k + 1)
    (hargs : ArgsRel xks vs) (hnames : ∀ k, nameOwner c (calleeEnv P fi) (kindName k) = none) (g1 : St)
    (hb : (execStmts c (n + 2) (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).1 body).run.run
      { (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).2 with
        defers := [] :: g.defers } = (.error .fuel, g1)) :
    ((callBody c (n + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some body) none vs false).run.run g).1 =
      .error .fuel := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  simp only [grun_bind, gtick_succ g k hk, Bool.not_false, ite_true, Bool.false_and,
    Bool.false_eq_true, ite_false, run_pureM]
  simp only [List.flatMap_map, List.isEmpty_cons, Bool.false_eq_true, ite_false, List.map_cons,
    List.map_nil, flatMap_single, List.flatMap_cons, List.flatMap_nil, List.isEmpty_nil, ite_true,
    List.append_nil]
  simp only [List.length_map, argsRel_length hargs, bne_self_eq_false, Bool.false_eq_true, ite_false]
  rw [grun_bind, forIn_steps _ paramStep (fun a => ∃ k v, a.1.2 = .ident (kindName k) ∧ VRel v a.2 ∧ valKind v = some k)
    ?step _ _ _ (zip_args hargs)]
  case step =>
    rintro ⟨⟨x, te⟩, w⟩ env g' ⟨k', v', hte, hv', hk'⟩
    simp only at hte; subst hte
    exact param_iter c n P fi hnames x k' v' w hv' hk' env g'
  have hty : ∀ g', (evalType c (n + 2) (calleeEnv P fi) (.ident (kindName ρ))).run.run g' = (.ok (kindTy ρ), g') :=
    fun g' => evalType_kind c n (calleeEnv P fi) ρ g' (by cases ρ <;> rfl) rfl (hnames ρ)
  simp only [List.mapM_cons, List.mapM_nil, grun_bind, hty, run_pureM, List.zip_cons_cons, List.zip_nil_right,
    List.forIn_cons, List.forIn_nil, emptyStr_isEmpty, Bool.not_true, Bool.false_eq_true, ite_false,
    gtryCatch, gget_run, gmodify_run, foldl_paramStep_defers]
  simp only [paramsBound] at hb
  simp only [hb]
  rfl

end Gala.M5
