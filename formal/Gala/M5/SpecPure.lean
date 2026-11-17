/-
  # M5, step 2a: galaspec's side of a call-free expression

  A call-free expression whose names are locals does, in galaspec, what a step-free
  program `pureEval` does, apart from spending steps: `eval` either runs out of fuel or
  gives `pureEval`'s result, and it leaves the state the program sees as it was
  (`eval_pure`). M5's simulation for these expressions (`Gala/M5/Expr.lean`) is then
  about `pureEval` alone.
-/
import Gala.Order

namespace Gala.M5

open Gala.Spec

/-- What `eval` does with a call-free expression whose names are locals, apart from
    spending steps: the same cases, in `H`. -/
def pureEval (ds : Decls) : Expr → H Val
  | .lit (.int n) => intLit n
  | .lit (.bool b) => pure (.bool b)
  | .lit (.str s) => pure (.str s)
  | .paren x => pureEval ds x
  | .name x => do
    match lookup (← getCore).locals x with
    | some b => deref b.val
    | none => throw (.stuck "not a local")
  | .unary "-" (.lit (.int n)) => intLit (-n)
  | .unary op x => do unop op (← pureEval ds x)
  | .binary "&&" l r => do
    if ← asBool (← pureEval ds l) then pure (.bool (← asBool (← pureEval ds r)))
    else pure (.bool false)
  | .binary "||" l r => do
    if ← asBool (← pureEval ds l) then pure (.bool true)
    else pure (.bool (← asBool (← pureEval ds r)))
  | .binary op l r => do binop op (← pureEval ds l) (← pureEval ds r)
  | _ => throw (.stuck "outside step 2a")

/-- The forms `pureEval` describes, with every name a local in `ρ`. -/
def Pure (ρ : Env) : Expr → Prop
  | .lit _ => True
  | .name x => (lookup ρ x).isSome
  | .paren e | .unary _ e => Pure ρ e
  | .binary _ l r => Pure ρ l ∧ Pure ρ r
  | _ => False

/-- `x` does what the step-free `h` does, apart from spending steps, and changes nothing
    the program sees: it runs out of fuel, or gives `h`'s result. -/
def Follows (P : Core → Prop) (x : M α) (h : H α) : Prop :=
  ∀ s : St, P s.toCore → ((x.run.run s).1 = .error .fuel ∨ (x.run.run s).1 = (h.run.run s.toCore).1) ∧
    (x.run.run s).2.toCore = s.toCore

theorem follows_fuel (P : Core → Prop) (h : H α) : Follows P (throw .fuel : M α) h :=
  fun _ _ => ⟨.inl rfl, rfl⟩

theorem follows_liftM (P : Core → Prop) {h : H α} (hr : ReadOnly h) : Follows P (liftM h : M α) h := by
  intro s _
  rw [run_liftM]
  exact ⟨.inr rfl, hr _⟩

theorem follows_bind {P : Core → Prop} {x : M α} {f : α → M β} {h : H α} {k : α → H β}
    (hx : Follows P x h) (hh : ReadOnly h) (hf : ∀ a, Follows P (f a) (k a)) :
    Follows P (x >>= f) (h >>= k) := by
  intro s hP
  obtain ⟨hr, hs⟩ := hx s hP
  rw [run_bind_eq]
  rcases hxr : x.run.run s with ⟨r, s'⟩
  rw [hxr] at hr hs
  simp only at hr hs
  rcases hr with hr | hr
  · subst hr; exact ⟨.inl rfl, hs⟩
  · cases r with
    | error e =>
      refine ⟨.inr ?_, hs⟩
      rw [run_bind_eq]
      rcases hhr : h.run.run s.toCore with ⟨q, c⟩
      rw [hhr] at hr
      simp only at hr ⊢
      rw [← hr]
    | ok a =>
      obtain ⟨hr2, hs2⟩ := hf a s' (hs ▸ hP)
      refine ⟨?_, hs2.trans hs⟩
      simp only
      rcases hr2 with hr2 | hr2
      · exact .inl hr2
      · refine .inr (hr2.trans ?_)
        rw [run_bind_eq]
        rcases hhr : h.run.run s.toCore with ⟨q, c⟩
        rw [hhr] at hr
        simp only at hr
        rw [← hr, hs]
        have hc : c = s.toCore := by
          have := hh s.toCore; rw [hhr] at this; exact this
        rw [hc]

theorem follows_tick {P : Core → Prop} {x : M α} {h : H α} (hx : Follows P x h) :
    Follows P (tick >>= fun _ => x) h := by
  intro ⟨c, n⟩ hP
  rw [run_bind_eq]
  cases n with
  | zero => exact ⟨.inl rfl, rfl⟩
  | succ n =>
    obtain ⟨hr, hs⟩ := hx ⟨c, n⟩ hP
    exact ⟨hr, hs⟩

/-! ## The step-free parts only read the state -/

theorem readOnly_intLit (n : Int) : ReadOnly (intLit n) := by
  intro c; unfold intLit; split <;> rfl

theorem readOnly_unop (op : String) (v : Val) : ReadOnly (unop op v) := by
  intro c; unfold unop; split <;> rfl

theorem readOnly_binop (op : String) (a b : Val) : ReadOnly (binop op a b) := by
  intro c; unfold binop; split <;> (try split) <;> (try split) <;> rfl

theorem readOnly_asBool (v : Val) : ReadOnly (asBool v) := by
  intro c; unfold asBool; split <;> rfl

theorem pureEval_readOnly (ds : Decls) : ∀ e, ReadOnly (pureEval ds e)
  | .lit (.int _) => readOnly_intLit _
  | .lit (.bool _) | .lit (.str _) => readOnly_pure _
  | .paren x => pureEval_readOnly ds x
  | .name x => by
    intro c
    show ((match lookup c.locals x with
      | some b => deref b.val
      | none => (throw (Stop.stuck "not a local") : H Val)).run.run c).2 = c
    split
    · exact readOnly_deref _ c
    · rfl
  | .unary op x => by
    have ih := pureEval_readOnly ds x
    unfold pureEval
    split
    all_goals first
      | (exfalso; simp_all; done)
      | exact readOnly_intLit _
      | (rename_i heq; simp only [Expr.unary.injEq] at heq; obtain ⟨rfl, rfl⟩ := heq
         exact readOnly_bind ih fun _ => readOnly_unop _ _)
  | .binary op l r => by
    have ihl := pureEval_readOnly ds l
    have ihr := pureEval_readOnly ds r
    have hb : ∀ b : Bool, ReadOnly (do pure (Val.bool (← asBool (← pureEval ds r)))) := fun _ =>
      readOnly_bind ihr fun _ => readOnly_bind (readOnly_asBool _) fun _ => readOnly_pure _
    unfold pureEval
    split
    all_goals first
      | (exfalso; simp_all; done)
      | (rename_i heq; simp only [Expr.binary.injEq] at heq; obtain ⟨rfl, rfl, rfl⟩ := heq
         first
          | exact readOnly_bind ihl fun _ => readOnly_bind ihr fun _ => readOnly_binop _ _ _
          | exact readOnly_bind ihl fun _ => readOnly_bind (readOnly_asBool _) fun b => by
              cases b <;> first | exact readOnly_pure _ | exact hb true)
  | .call .. | .select .. | .tuple _ | .ifExpr .. | .mtch .. | .block _ | .ifStmt ..
  | .ret _ | .assign .. | .incdec .. | .decl .. | .interp _ | .lambda .. | .named ..
  | .unsupported _ => readOnly_throw _

theorem follows_mono {P Q : Core → Prop} {x : M α} {h : H α} (hPQ : ∀ c, P c → Q c)
    (hx : Follows Q x h) : Follows P x h := fun s hP => hx s (hPQ _ hP)

theorem follows_pure (P : Core → Prop) (a : α) : Follows P (pure a : M α) (pure a) :=
  fun _ _ => ⟨.inr rfl, rfl⟩

theorem follows_vacuous {P : Core → Prop} {x : M α} {h : H α} (hP : ∀ c, ¬ P c) : Follows P x h :=
  fun s h => absurd h (hP _)

/-- `&&` and `||` evaluated from their operands' computations, in `M` and in `H` alike. -/
theorem follows_and {P : Core → Prop} {xl xr : M Val} {hl hr : H Val}
    (fl : Follows P xl hl) (fr : Follows P xr hr) (rl : ReadOnly hl) (rr : ReadOnly hr) :
    Follows P (do if ← asBool (← xl) then pure (Val.bool (← asBool (← xr))) else pure (Val.bool false))
      (do if ← asBool (← hl) then pure (Val.bool (← asBool (← hr))) else pure (Val.bool false)) := by
  refine follows_bind fl rl fun a => follows_bind (follows_liftM _ (readOnly_asBool a))
    (readOnly_asBool a) fun b => ?_
  cases b
  · exact follows_pure _ _
  · exact follows_bind fr rr fun c => follows_bind (follows_liftM _ (readOnly_asBool c))
      (readOnly_asBool c) fun _ => follows_pure _ _

theorem follows_or {P : Core → Prop} {xl xr : M Val} {hl hr : H Val}
    (fl : Follows P xl hl) (fr : Follows P xr hr) (rl : ReadOnly hl) (rr : ReadOnly hr) :
    Follows P (do if ← asBool (← xl) then pure (Val.bool true) else pure (Val.bool (← asBool (← xr))))
      (do if ← asBool (← hl) then pure (Val.bool true) else pure (Val.bool (← asBool (← hr)))) := by
  refine follows_bind fl rl fun a => follows_bind (follows_liftM _ (readOnly_asBool a))
    (readOnly_asBool a) fun b => ?_
  cases b
  · exact follows_bind fr rr fun c => follows_bind (follows_liftM _ (readOnly_asBool c))
      (readOnly_asBool c) fun _ => follows_pure _ _
  · exact follows_pure _ _

theorem pureEval_unary (ds : Decls) (op : String) (x : Expr)
    (h : ∀ n, ¬ (op = "-" ∧ x = .lit (.int n))) :
    pureEval ds (.unary op x) = (do unop op (← pureEval ds x)) := by
  rw [pureEval.eq_def]
  split
  all_goals first
    | (exfalso; simp_all; done)
    | (rename_i hne heq; simp only [Expr.unary.injEq] at heq; obtain ⟨rfl, rfl⟩ := heq; rfl)
    | (rename_i n heq; simp only [Expr.unary.injEq] at heq; exact absurd ⟨heq.1.symm, heq.2.symm⟩ (h n))

theorem pureEval_binary (ds : Decls) (op : String) (l r : Expr) (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    pureEval ds (.binary op l r) = (do binop op (← pureEval ds l) (← pureEval ds r)) := by
  rw [pureEval.eq_def]
  split
  all_goals first
    | (exfalso; simp_all; done)
    | (rename_i heq; simp only [Expr.binary.injEq] at heq; obtain ⟨rfl, rfl, rfl⟩ := heq; rfl)

/-- **galaspec's side of step 2a.** A call-free expression whose names are locals runs out
    of fuel or gives `pureEval`'s result, and leaves the state the program sees as it was. -/
theorem eval_pure (ds : Decls) : ∀ (e : Expr) (d : Nat),
    Follows (fun c => Pure c.locals e) (eval ds d e) (pureEval ds e)
  | e, 0 => by rw [eval.eq_def]; exact follows_fuel _ _
  | .lit (.int n), d + 1 => by
    rw [eval.eq_def]; exact follows_tick (follows_liftM _ (readOnly_intLit n))
  | .lit (.bool b), d + 1 => by
    rw [eval.eq_def]; exact follows_tick (follows_liftM _ (readOnly_pure _))
  | .lit (.str s), d + 1 => by
    rw [eval.eq_def]; exact follows_tick (follows_liftM _ (readOnly_pure _))
  | .paren x, d + 1 => by
    rw [eval.eq_def]; exact follows_tick (eval_pure ds x d)
  | .name x, d + 1 => by
    rw [eval.eq_def]
    refine follows_tick fun s hP => ?_
    simp only [Pure, Option.isSome_iff_exists] at hP
    obtain ⟨b, hb⟩ := hP
    have hres : resolve ds s.locals s.globals x = .var b := by
      simp [resolve, lookupVar, hb]
    rw [run_bind_eq, run_liftM]
    have hg : StateT.run (ExceptT.run (get : H Core)) s.toCore = (.ok s.toCore, s.toCore) := rfl
    have hp : (pureEval ds (.name x)).run.run s.toCore = (deref b.val).run.run s.toCore := by
      unfold pureEval getCore
      rw [run_get_bind]
      show ((match lookup s.locals x with
        | some b => deref b.val
        | none => (throw (Stop.stuck "not a local") : H Val)).run.run s.toCore) = _
      rw [hb]
    simp only [getCore, hg, hres, run_liftM, hp]
    exact ⟨.inr trivial, readOnly_deref _ _⟩
  | .unary op x, d + 1 => by
    have ih := eval_pure ds x d
    rw [eval.eq_def]
    refine follows_tick ?_
    dsimp only
    split
    all_goals first
      | (exfalso; simp_all; done)
      | (rename_i n heq; simp only [Expr.unary.injEq] at heq; obtain ⟨rfl, rfl⟩ := heq
         exact follows_liftM _ (readOnly_intLit _))
      | (rename_i hne heq; simp only [Expr.unary.injEq] at heq; obtain ⟨rfl, rfl⟩ := heq
         rw [pureEval_unary ds _ _ fun n h => hne n h.1 h.2]
         exact follows_bind ih (pureEval_readOnly ds _) fun v =>
           follows_liftM _ (readOnly_unop _ _))
  | .binary op l r, d + 1 => by
    have ihl := follows_mono (fun c (h : Pure c.locals (.binary op l r)) => h.1) (eval_pure ds l d)
    have ihr := follows_mono (fun c (h : Pure c.locals (.binary op l r)) => h.2) (eval_pure ds r d)
    rw [eval.eq_def]
    refine follows_tick ?_
    dsimp only
    split
    all_goals first
      | (exfalso; simp_all; done)
      | (rename_i heq; simp only [Expr.binary.injEq] at heq; obtain ⟨rfl, rfl, rfl⟩ := heq
         first
          | exact follows_and ihl ihr (pureEval_readOnly ds _) (pureEval_readOnly ds _)
          | exact follows_or ihl ihr (pureEval_readOnly ds _) (pureEval_readOnly ds _))
      | (rename_i h1 h2 heq; simp only [Expr.binary.injEq] at heq; obtain ⟨rfl, rfl, rfl⟩ := heq
         rw [pureEval_binary ds _ _ _ h1 h2]
         exact follows_bind ihl (pureEval_readOnly ds _) fun a =>
           follows_bind ihr (pureEval_readOnly ds _) fun b => follows_liftM _ (readOnly_binop _ _ _))
  | .call .., _ + 1 | .select .., _ + 1 | .tuple _, _ + 1 | .ifExpr .., _ + 1 | .mtch .., _ + 1
  | .block _, _ + 1 | .ifStmt .., _ + 1 | .ret _, _ + 1 | .assign .., _ + 1 | .incdec .., _ + 1
  | .decl .., _ + 1 | .interp _, _ + 1 | .lambda .., _ + 1 | .named .., _ + 1
  | .unsupported _, _ + 1 => follows_vacuous fun _ h => h

end Gala.M5
