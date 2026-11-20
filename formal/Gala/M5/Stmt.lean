/-
  # M5, step 2b: `val`/`var`, `if` statements and blocks

  docs/FORMAL_SEMANTICS.MD, "M5". The reference lowering of galaspec's statements of step 2
  into GoCore for whole programs, with step 2a's call-free expressions (`Gala/M5/Expr.lean`)
  in them, and its forward simulation (`stmts_correct`, `block_correct`).

  * **The shapes** are the transformer's: a local `val x = e` or `var x = e` is Go's
    `var x = e`, which gives an untyped constant its default type; an `if` statement is Go's
    `if`, an `else if` chain included; a block is Go's block. GALA has no bare block
    statement, so blocks are the branches of an `if` (and a function's body).
  * **The state relation** (`Inv`): step 2a's `LocRel` for the locals the lowering knows,
    equal outputs, and `true`/`false` unshadowed. A `val` is a galaspec binding, a `var` a
    galaspec heap cell (`bval` reads through it); each is a new GoCore cell. Leaving a
    block drops galaspec's bindings made in it (`popTo`) and GoCore's scope; the locals
    outside stay related because both sides only add cells (`Pre`).
  * **Depth.** GoCore's execution takes no steps here (only calls and loops do), only
    depth; the claim is for every depth from `sdepthL` on. The proof is by strong
    induction on that depth, for statement lists, blocks and `if`s together.
  * **Evaluation order.** Proved for GoCore's evaluator as it is; `Gala/M5/StmtOrder.lean`
    shows GoCore's model of gc's order (`Order.gc`) and left to right run these statements
    as written; no theorem links that to a run of the program before step 3.
  * **Unused locals.** GALA rejects a local that is never used (GALA-E0102), so the
    transformer never emits one and adds no `_ = x`; galaspec does not check it. The
    theorem covers such programs too: it is about GoCore's semantics, which runs them.
-/
import Gala.M5.Expr

namespace Gala.M5

/-! ## The lowering of statements -/

/-- The kinds after `x` is declared with kind `k`. -/
def upd (κ : String → Option Kind) (x : String) (k : Kind) : String → Option Kind :=
  fun y => if y = x then some k else κ y

/-- No operator node is a constant expression, as a check. -/
def noConstB : Spec.Expr → Bool
  | .paren e => noConstB e
  | .unary op e => (Spec.constEval (.unary op e)).isNone && noConstB e
  | .binary op l r => (Spec.constEval (.binary op l r)).isNone && noConstB l && noConstB r
  | _ => true

theorem noConstB_sound : ∀ e, noConstB e = true → NoConstOp e
  | .paren e, h => noConstB_sound e h
  | .unary op e, h => by
    simp only [noConstB, Bool.and_eq_true, Option.isNone_iff_eq_none] at h
    exact ⟨h.1, noConstB_sound e h.2⟩
  | .binary op l r, h => by
    simp only [noConstB, Bool.and_eq_true, Option.isNone_iff_eq_none] at h
    exact ⟨h.1.1, noConstB_sound l h.1.2, noConstB_sound r h.2⟩
  | .lit _, _ | .name _, _ | .call .., _ | .select .., _ | .tuple _, _ | .ifExpr .., _
  | .mtch .., _ | .block _, _ | .ifStmt .., _ | .ret _, _ | .assign .., _ | .incdec .., _
  | .decl .., _ | .interp _, _ | .lambda .., _ | .named .., _ | .unsupported _, _ => trivial

/-- A call-free expression the lowering accepts and the run evaluates (constants folded). -/
def lowerX (κ : String → Option Kind) (e : Spec.Expr) : Option (Kind × GoCore.Full.Expr) :=
  if noConstB e then lowerE κ e else none

mutual
/-- **The lowering of a statement**, with the kinds after it: a `val` or `var` of one
    name and no type is Go's `var x = e`; an `if` whose branches are blocks (an `else`
    may be another `if`) is Go's `if`; a block is Go's block. `none` outside step 2. -/
def lowerS (κ : String → Option Kind) : Spec.Expr →
    Option ((String → Option Kind) × GoCore.Full.Stmt)
  | .decl _ [x] false none [e] =>
    if x = "_" ∨ x = "true" ∨ x = "false" then none
    else (lowerX κ e).map fun (k, le) => (upd κ x k, .decl (.value false [.mk [x] none [le]]))
  | .block ss => (lowerSs κ ss).map fun (_, l) => (κ, .block l)
  | .ifStmt c (.block a) els =>
    match lowerX κ c, lowerSs κ a, lowerElse κ els with
    | some (.bool, lc), some (_, la), some le => some (κ, .if_ none lc la le)
    | _, _, _ => none
  | _ => none
/-- An `else` part: none, a block or another `if`. -/
def lowerElse (κ : String → Option Kind) : Option Spec.Expr → Option (Option GoCore.Full.Stmt)
  | none => some none
  | some (.block ss) => (lowerSs κ ss).map fun (_, l) => some (.block l)
  | some (.ifStmt c a els) => (lowerS κ (.ifStmt c a els)).map fun (_, l) => some l
  | some _ => none
/-- A block's statements, each seeing the kinds the ones before it declared. -/
def lowerSs (κ : String → Option Kind) : List Spec.Expr →
    Option ((String → Option Kind) × List GoCore.Full.Stmt)
  | [] => some (κ, [])
  | s :: ss => do
    let (κ₁, l) ← lowerS κ s
    let (κ₂, ls) ← lowerSs κ₁ ss
    pure (κ₂, l :: ls)
end

mutual
/-- How deep GoCore's execution of a lowered statement must be allowed to go. -/
def sdepth : Spec.Expr → Nat
  | .decl _ _ _ _ [e] => gdepth e + 3
  | .block ss => sdepthL ss + 2
  | .ifStmt c a els => max (gdepth c + 1) (max (sdepth a) (sdepthO els)) + 2
  | _ => 0
def sdepthO : Option Spec.Expr → Nat
  | none => 0
  | some e => sdepth e
def sdepthL : List Spec.Expr → Nat
  | [] => 1
  | s :: ss => max (sdepth s + 1) (sdepthL ss)
end

/-! ## galaspec's statements, unfolded -/

section SpecRuns
open Spec (eval evalBlock tick)

theorem evalBlock_nil (ds : Spec.Decls) (d : Nat) (s : Spec.St) :
    (evalBlock ds d []).run.run s = (.ok .unit, s) := by
  rw [evalBlock.eq_def]; rfl

theorem evalBlock_one (ds : Spec.Decls) (d : Nat) (e : Spec.Expr) :
    evalBlock ds d [e] = eval ds d e := by
  rw [evalBlock.eq_def]

theorem run_discard {σ ε α : Type} (x : ExceptT ε (StateM σ) α) (s : σ) :
    (discard x).run.run s = match x.run.run s with
      | (.ok _, t) => (.ok (), t)
      | (.error e, t) => (.error e, t) := by
  show (x >>= fun a => pure (Function.const _ () a)).run.run s = _
  simp only [ExceptT.run_bind, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  cases r <;> rfl

theorem run_pureM {σ ε α : Type} (a : α) (s : σ) :
    (pure a : ExceptT ε (StateM σ) α).run.run s = (.ok a, s) := rfl

theorem evalBlock_cons (ds : Spec.Decls) (d : Nat) (e e' : Spec.Expr) (es : List Spec.Expr)
    (s : Spec.St) :
    (evalBlock ds d (e :: e' :: es)).run.run s = match (eval ds d e).run.run s with
      | (.ok _, t) => (evalBlock ds d (e' :: es)).run.run t
      | (.error er, t) => (.error er, t) := by
  rw [evalBlock.eq_def]
  simp only [Spec.run_bind_eq, run_discard]
  rcases (eval ds d e).run.run s with ⟨r, t⟩
  cases r <;> rfl

theorem eval_block_run (ds : Spec.Decls) (d : Nat) (ss : List Spec.Expr) (s : Spec.St) :
    (eval ds (d + 1) (.block ss)).run.run s = match tick.run.run s with
      | (.ok _, s1) => match (evalBlock ds d ss).run.run s1 with
        | (.ok v, t) => (.ok v, { t with locals := Spec.exitScope t.locals s1.locals.length })
        | (.error e, t) => (.error e, t)
      | (.error e, s1) => (.error e, s1) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq]
  rcases tick.run.run s with ⟨r, s1⟩
  cases r
  · rfl
  · simp only [Spec.run_liftM, Spec.run_getCore]
    rcases (evalBlock ds d ss).run.run s1 with ⟨r', t⟩
    cases r' <;> rfl

theorem tick_run (s : Spec.St) : tick.run.run s = match s.steps with
    | 0 => (.error .fuel, s)
    | n + 1 => (.ok (), { s with steps := n }) := by
  rcases s with ⟨c, n⟩
  cases n <;> rfl

theorem tick_succ (s : Spec.St) (k : Nat) (h : s.steps = k + 1) :
    tick.run.run s = (.ok (), { s with steps := k }) := by
  rw [tick_run, h]

theorem eval_fuel (ds : Spec.Decls) (d : Nat) (e : Spec.Expr) (s : Spec.St) (h : s.steps = 0) :
    (eval ds (d + 1) e).run.run s = (.error .fuel, s) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_run, h]

theorem eval_ifStmt_err (ds : Spec.Decls) (d k : Nat) (c a : Spec.Expr) (els : Option Spec.Expr)
    (s t : Spec.St) (er : Spec.Stop) (hs : s.steps = k + 1)
    (hc : (eval ds d c).run.run { s with steps := k } = (.error er, t)) :
    (eval ds (d + 1) (.ifStmt c a els)).run.run s = (.error er, t) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]
  cases els <;> simp only [Spec.run_bind_eq, hc]

theorem eval_ifStmt_some (ds : Spec.Decls) (d k : Nat) (c a e : Spec.Expr) (b : Bool)
    (s t : Spec.St) (hs : s.steps = k + 1)
    (hc : (eval ds d c).run.run { s with steps := k } = (.ok (.bool b), t)) :
    (eval ds (d + 1) (.ifStmt c a (some e))).run.run s = (eval ds d (if b then a else e)).run.run t := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs, hc]
  cases b <;> rfl

theorem eval_ifStmt_none (ds : Spec.Decls) (d k : Nat) (c a : Spec.Expr) (b : Bool)
    (s t : Spec.St) (hs : s.steps = k + 1)
    (hc : (eval ds d c).run.run { s with steps := k } = (.ok (.bool b), t)) :
    (eval ds (d + 1) (.ifStmt c a none)).run.run s = if b then
      (match (eval ds d a).run.run t with
        | (.ok _, t') => (.ok .unit, t')
        | (.error e, t') => (.error e, t'))
      else (.ok .unit, t) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs, hc]
  cases b
  · rfl
  · simp only [ite_true, Spec.run_liftM]
    show (discard (eval ds d a) >>= fun _ => pure Spec.Val.unit).run.run t = _
    rw [Spec.run_bind_eq, run_discard]
    rcases (eval ds d a).run.run t with ⟨r, t'⟩
    cases r <;> rfl

/-- galaspec's state after declaring `x` with value `v`: a `var` gets a cell. -/
def declSt (m : Spec.Mut) (x : String) (v : Spec.Val) (t : Spec.St) : Spec.St :=
  match m with
  | .val => { t with locals := Spec.bind t.locals x ⟨v, false⟩ }
  | .var => { t with heap := t.heap.push v, locals := Spec.bind t.locals x ⟨.ref t.heap.size, true⟩ }

theorem eval_decl_run (ds : Spec.Decls) (d k : Nat) (m : Spec.Mut) (x : String) (e : Spec.Expr)
    (s : Spec.St) (hs : s.steps = k + 1) :
    (eval ds (d + 1) (.decl m [x] false none [e])).run.run s =
      match (eval ds d e).run.run { s with steps := k } with
      | (.ok v, t) => (.ok .unit, declSt m x v t)
      | (.error er, t) => (.error er, t) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]
  rw [Spec.declare.eq_def]
  simp only [Spec.run_bind_eq]
  have hl : Spec.evalList ds d [e] = (do let v ← eval ds d e; pure [v]) := by
    rw [Spec.evalList.eq_def]
    simp only
    congr; funext v
    rw [Spec.evalList.eq_def]; simp
  simp only [hl, ite_false, Spec.run_bind_eq, List.isEmpty_cons, Bool.false_eq_true,
    run_pureM]
  rcases hr : (eval ds d e).run.run { s with steps := k } with ⟨r, t⟩
  cases r with
  | error er => simp
  | ok v =>
    simp
    cases m <;> rfl

end SpecRuns

/-! ## GoCore's statements, unfolded -/

section GoCoreRuns
open Gala.GoCore.Full

/-- GoCore's result replaced by `env` when it ends normally. -/
def withEnv (env : Env) : Except Stop α × St → Except Stop Env × St
  | (.ok _, g') => (.ok env, g')
  | (.error e, g') => (.error e, g')

theorem execStmts_nil (c : Ctx) (n : Nat) (env : Env) (g : St) :
    (execStmts c (n + 1) env []).run.run g = (.ok env, g) := by
  simp [execStmts]; rfl

theorem execStmts_cons (c : Ctx) (n : Nat) (env : Env) (s : Stmt) (ss : List Stmt) (g : St) :
    (execStmts c (n + 1) env (s :: ss)).run.run g =
      match (execStmt c n env s).run.run g with
      | (.ok env', g') => (execStmts c (n + 1) env' ss).run.run g'
      | (.error e, g') => (.error e, g') := by
  simp only [execStmts, List.forIn_cons, bind_pure, grun_bind]
  rcases (execStmt c n env s).run.run g with ⟨r, g'⟩
  cases r <;> simp <;> rfl

theorem execBlock_run (c : Ctx) (n : Nat) (env : Env) (ss : List Stmt) (g : St) :
    (execBlock c (n + 1) env ss).run.run g = match (execStmts c n env.push ss).run.run g with
      | (.ok _, g') => (.ok (), g')
      | (.error e, g') => (.error e, g') := by
  simp only [execBlock, grun_bind]
  rcases (execStmts c n env.push ss).run.run g with ⟨r, g'⟩
  cases r <;> rfl

theorem execStmt_block (c : Ctx) (n : Nat) (env : Env) (ss : List Stmt) (g : St) :
    (execStmt c (n + 1) env (.block ss)).run.run g = withEnv env ((execBlock c n env ss).run.run g) := by
  simp only [execStmt, grun_bind]
  rcases (execBlock c n env ss).run.run g with ⟨r, g'⟩
  cases r <;> rfl

theorem execStmt_if_err (c : Ctx) (n : Nat) (env : Env) (cond : Expr) (body : List Stmt)
    (els : Option Stmt) (g g' : St) (e : Stop)
    (hc : (evalExpr c (n + 1) env.push cond).run.run g = (.error e, g')) :
    (execStmt c (n + 2) env (.if_ none cond body els)).run.run g = (.error e, g') := by
  rw [execStmt]
  simp only [execInit, grun_bind, run_pureM, hc]

theorem execStmt_if_true (c : Ctx) (n : Nat) (env : Env) (cond : Expr) (body : List Stmt)
    (els : Option Stmt) (g g' : St) (t : Ty)
    (hc : (evalExpr c (n + 1) env.push cond).run.run g = (.ok (.bool t true), g')) :
    (execStmt c (n + 2) env (.if_ none cond body els)).run.run g =
      withEnv env ((execBlock c (n + 1) env.push body).run.run g') := by
  rw [execStmt]
  simp only [execInit, grun_bind, run_pureM, hc]
  show (match (asBool (.bool t true)).run.run g' with | _ => _) = _
  simp only [run_pureM, ite_true, grun_bind]
  rcases (execBlock c (n + 1) env.push body).run.run g' with ⟨r, g''⟩
  cases r <;> rfl

theorem execStmt_if_false (c : Ctx) (n : Nat) (env : Env) (cond : Expr) (body : List Stmt)
    (els : Option Stmt) (g g' : St) (t : Ty)
    (hc : (evalExpr c (n + 1) env.push cond).run.run g = (.ok (.bool t false), g')) :
    (execStmt c (n + 2) env (.if_ none cond body els)).run.run g = match els with
      | some e => withEnv env ((execStmt c (n + 1) env.push e).run.run g')
      | none => (.ok env, g') := by
  rw [execStmt]
  simp only [execInit, grun_bind, run_pureM, hc]
  show (match (asBool (.bool t false)).run.run g' with | _ => _) = _
  simp only [Bool.false_eq_true, ite_false]
  cases els with
  | none => rfl
  | some e =>
    simp only [grun_bind]
    rcases (execStmt c (n + 1) env.push e).run.run g' with ⟨r, g''⟩
    cases r <;> rfl

/-- GoCore's state after `var x = e` with `e`'s value `w`: a cell holding `w` with its
    default type. -/
def declG (w : Val) (g : St) : St :=
  { g with cells := g.cells.push (defaultVal w), types := g.types.push (defaultVal w).ty }

theorem execStmt_decl_err (c : Ctx) (n : Nat) (env : Env) (x : String) (le : Expr) (g g' : St)
    (e : Stop) (hr : (evalExpr c (n + 1) env le).run.run g = (.error e, g')) :
    (execStmt c (n + 3) env (.decl (.value false [.mk [x] none [le]]))).run.run g = (.error e, g') := by
  rw [execStmt]
  simp [evalArgs, grun_bind, hr]

theorem execStmt_decl_ok (c : Ctx) (n : Nat) (env : Env) (x : String) (le : Expr) (g g' : St)
    (w : Val) (hr : (evalExpr c (n + 1) env le).run.run g = (.ok w, g')) (hw : w.results = [w]) :
    (execStmt c (n + 3) env (.decl (.value false [.mk [x] none [le]]))).run.run g =
      (.ok (env.bind x g'.cells.size), declG w g') := by
  rw [execStmt]
  simp [evalArgs, grun_bind, hr, hw]
  rfl

end GoCoreRuns

/-! ## The relations after a statement -/

section Rel
open Gala.GoCore.Full

/-- `b` extends `a`: what was there stays where it was. -/
def Pre (a b : Array α) : Prop := ∃ t, b = a ++ t

theorem pre_refl (a : Array α) : Pre a a := ⟨#[], by simp⟩

theorem pre_trans {a b c : Array α} (h1 : Pre a b) (h2 : Pre b c) : Pre a c := by
  obtain ⟨t1, rfl⟩ := h1; obtain ⟨t2, rfl⟩ := h2; exact ⟨t1 ++ t2, by simp⟩

theorem pre_push (a : Array α) (v : α) : Pre a (a.push v) := ⟨#[v], by simp⟩

theorem pre_get {a b : Array α} (h : Pre a b) {i : Nat} {v : α} (hv : a[i]? = some v) :
    b[i]? = some v := by
  obtain ⟨t, rfl⟩ := h
  rw [Array.getElem?_append_left (Array.getElem?_eq_some_iff.mp hv).1, hv]

/-- **The state relation** between statements: the locals (`LocRel`), equal outputs, and
    `true` and `false` unshadowed. -/
structure Inv (cx : Ctx) (κ : String → Option Kind) (c : Spec.Core) (g : St) (env : Env) : Prop where
  loc : LocRel κ c g.cells env
  out : c.out = g.out
  pre : Predeclared cx env

theorem bval_mono {h h' : Array Spec.Val} (hp : Pre h h') {b : Spec.Binding} {v : Spec.Val}
    (hb : bval h b = some v) : bval h' b = some v := by
  unfold bval at hb ⊢
  cases hbv : b.val <;> simp only [hbv] at hb ⊢ <;> first | exact pre_get hp hb | exact hb

/-- More cells, on either side, keep the locals related. -/
theorem locRel_mono {κ : String → Option Kind} {c c' : Spec.Core} {cells cells' : Array Val}
    {env : Env} (h : LocRel κ c cells env) (hh : Pre c.heap c'.heap) (hl : c'.locals = c.locals)
    (hc : Pre cells cells') : LocRel κ c' cells' env := by
  intro x k hk
  obtain ⟨b, cell, v, w, hb, hv, hcell, hw, rest⟩ := h x k hk
  exact ⟨b, cell, v, w, hl ▸ hb, bval_mono hh hv, hcell, pre_get hc hw, rest⟩

theorem Env.lookup_push (env : Env) (x : String) : env.push.lookup x = env.lookup x := by
  simp [Env.push, Env.lookup, List.findSome?]

theorem Env.lookup_bind (env : Env) (x y : String) (cell : Nat) (hx : x ≠ "_") :
    (env.bind x cell).lookup y = if y = x then some cell else env.lookup y := by
  unfold Env.bind Env.lookup
  have : (x == "_") = false := by simpa using hx
  rw [this]
  rcases env with ⟨p, f, sc, te, io⟩
  by_cases h : y = x
  · subst h; cases sc <;> simp [List.lookup]
  · have hb : (y == x) = false := by simpa using h
    cases sc <;> simp [h, hb, List.findSome?, List.lookup]

theorem predeclared_push {cx : Ctx} {env : Env} (h : Predeclared cx env) : Predeclared cx env.push := by
  refine ⟨h.1, fun x hx => ?_⟩
  obtain ⟨hl, p, hp, ho, hn⟩ := h.2 x hx
  exact ⟨by rw [Env.lookup_push]; exact hl, p, hp, ho, hn⟩

theorem predeclared_bind {cx : Ctx} {env : Env} (h : Predeclared cx env) (x : String) (cell : Nat)
    (hx : x ≠ "_") (ht : x ≠ "true") (hf : x ≠ "false") : Predeclared cx (env.bind x cell) := by
  refine ⟨by unfold Env.bind; split <;> (try split) <;> exact h.1, fun y hy => ?_⟩
  obtain ⟨hl, p, hp, ho, hn⟩ := h.2 y hy
  have hyx : y ≠ x := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hy
    rcases hy with rfl | rfl <;> intro e <;> simp_all
  refine ⟨by rw [Env.lookup_bind _ _ _ _ hx]; simp only [hyx, ite_false]; exact hl, p, ?_, ho, ?_⟩
  · unfold Env.bind; split <;> (try split) <;> exact hp
  · unfold Env.bind; split <;> (try split) <;> exact hn

theorem inv_push {cx : Ctx} {κ : String → Option Kind} {c : Spec.Core} {g : St} {env : Env}
    (h : Inv cx κ c g env) : Inv cx κ c g env.push :=
  ⟨fun x k hk => by
    obtain ⟨b, cell, v, w, hb, hv, hcell, rest⟩ := h.loc x k hk
    exact ⟨b, cell, v, w, hb, hv, by rw [Env.lookup_push]; exact hcell, rest⟩, h.out, predeclared_push h.pre⟩

/-- A declared variable holds its value with its default type: never an untyped constant. -/
theorem vrel_default {v : Spec.Val} {w : Val} (h : VRel v w) :
    VRel v (defaultVal w) ∧ ¬ UInt (defaultVal w) ∧ (∀ t b, defaultVal w ≠ .bool (.untyped t) b) ∧
      (∀ t s, defaultVal w ≠ .str (.untyped t) s) := by
  cases h <;> simp [defaultVal, UInt, UKind.default] <;> constructor <;> assumption

theorem results_of_vrel {v : Spec.Val} {w : Val} (h : VRel v w) : w.results = [w] := by
  cases h <;> rfl

/-- galaspec's result of a statement list and GoCore's agree: from related states, a
    galaspec run that ends normally is matched by a GoCore run ending normally in related
    states (with the kinds `κ'` the list declared), the locals only extended and the
    cells only added to; a galaspec panic by the same panic, with equal outputs. Nothing
    is claimed of a galaspec run that runs out of fuel, stops `stuck` or `unsupported`, or ends
    with a `return` on its way out (step 2 has none). -/
def ResL (cx : Ctx) (κ' : String → Option Kind) (c : Spec.Core) (g : St) :
    Except Spec.Stop Spec.Val × Spec.St → Except Stop Env × St → Prop
  | (.ok _, s'), r => ∃ env' g', r = (.ok env', g') ∧ Inv cx κ' s'.toCore g' env' ∧
      Pre c.heap s'.heap ∧ Pre g.cells g'.cells ∧ ∃ pre, s'.locals = pre ++ c.locals
  | (.error (.panic k m), s'), r => ∃ pv g', r = (.error (.panic pv true), g') ∧ PanicRel k m pv ∧
      s'.out = g'.out
  | _, _ => True

/-- The same for a block or an `if`, which leaves galaspec's locals as they were. -/
def ResU (P : α → Prop) (c : Spec.Core) (g : St) :
    Except Spec.Stop Spec.Val × Spec.St → Except Stop α × St → Prop
  | (.ok _, s'), r => ∃ a g', r = (.ok a, g') ∧ P a ∧ s'.locals = c.locals ∧ s'.out = g'.out ∧
      Pre c.heap s'.heap ∧ Pre g.cells g'.cells
  | (.error (.panic k m), s'), r => ∃ pv g', r = (.error (.panic pv true), g') ∧ PanicRel k m pv ∧
      s'.out = g'.out
  | _, _ => True

theorem resU_withEnv {P : α → Prop} {c : Spec.Core} {g : St} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop α × St} (env : Env) (h : ResU P c g sr r) :
    ResU (· = env) c g sr (withEnv env r) := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e <;> try trivial
    obtain ⟨pv, g', rfl, hp, ho⟩ := h
    exact ⟨pv, g', rfl, hp, ho⟩
  · obtain ⟨a, g', rfl, -, rest⟩ := h
    exact ⟨env, g', rfl, rfl, rest⟩

/-- A block or an `if` keeps the locals related as they were before it. -/
theorem resU_inv {cx : Ctx} {κ : String → Option Kind} {c : Spec.Core} {g : St} {env : Env}
    (hi : Inv cx κ c g env) {P : α → Prop} {s' : Spec.St} {v : Spec.Val} {r : Except Stop α × St}
    (h : ResU P c g (.ok v, s') r) :
    ∃ a g', r = (.ok a, g') ∧ P a ∧ Inv cx κ s'.toCore g' env ∧ Pre c.heap s'.heap ∧
      Pre g.cells g'.cells ∧ s'.locals = c.locals := by
  obtain ⟨a, g', rfl, hP, hl, ho, hh, hc⟩ := h
  exact ⟨a, g', rfl, hP, ⟨locRel_mono hi.loc hh hl hc, ho, hi.pre⟩, hh, hc, hl⟩

end Rel

/-! ## The simulation -/

section Sim
open Gala.GoCore.Full

theorem eval_zero (ds : Spec.Decls) (e : Spec.Expr) (s : Spec.St) :
    (Spec.eval ds 0 e).run.run s = (.error .fuel, s) := by
  rw [Spec.eval.eq_def]; rfl

theorem declSt_facts (m : Spec.Mut) (x : String) (v : Spec.Val) (t : Spec.St) (hx : x ≠ "_")
    (hv : ∀ n, v ≠ .ref n) :
    ∃ b, (declSt m x v t).locals = (x, b) :: t.locals ∧ bval (declSt m x v t).heap b = some v ∧
      (declSt m x v t).out = t.out ∧ Pre t.heap (declSt m x v t).heap := by
  cases m
  · refine ⟨⟨v, false⟩, by simp [declSt, Spec.bind, hx], ?_, rfl, pre_refl _⟩
    unfold bval; cases v <;> simp_all
  · refine ⟨⟨.ref t.heap.size, true⟩, by simp [declSt, Spec.bind, hx], ?_, rfl, pre_push _ _⟩
    simp [bval, declSt]

theorem vrel_not_ref {v : Spec.Val} {w : Val} (h : VRel v w) : ∀ n, v ≠ .ref n := by
  cases h <;> simp

theorem lowerX_some {κ : String → Option Kind} {e : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerX κ e = some (k, le)) : NoConstOp e ∧ lowerE κ e = some (k, le) := by
  unfold lowerX at h
  by_cases hn : noConstB e = true
  · simp only [hn, ite_true] at h; exact ⟨noConstB_sound e hn, h⟩
  · simp [hn] at h

/-- **A declaration** `val x = e` or `var x = e`, lowered to `var x = e`. -/
theorem decl_sim (ds : Spec.Decls) (cx : Ctx) (κ κ' : String → Option Kind) (m : Spec.Mut)
    (x : String) (e : Spec.Expr) (l : Stmt) (hl : lowerS κ (.decl m [x] false none [e]) = some (κ', l))
    (s : Spec.St) (g : St) (env : Env) (hi : Inv cx κ s.toCore g env) (d N : Nat)
    (hN : sdepth (.decl m [x] false none [e]) ≤ N) :
    ResL cx κ' s.toCore g ((Spec.eval ds d (.decl m [x] false none [e])).run.run s)
      ((execStmt cx N env l).run.run g) := by
  simp only [lowerS] at hl
  split at hl
  · cases hl
  rename_i hxs
  simp only [not_or] at hxs
  obtain ⟨hx_, hxt, hxf⟩ := hxs
  simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
  obtain ⟨⟨k, le⟩, hlx, rfl, rfl⟩ := hl
  obtain ⟨hnc, hle⟩ := lowerX_some hlx
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by simp [sdepth] at hN; omega⟩
  have hge : gdepth e ≤ n + 1 := by simp [sdepth] at hN; omega
  cases d with
  | zero => rw [eval_zero]; trivial
  | succ d =>
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  rw [eval_decl_run ds d k' m x e s hs]
  have hec := expr_correct ds cx κ e k le hle hnc { s with steps := k' } g env hi.loc hi.pre d
  rcases hr : (Spec.eval ds d e).run.run { s with steps := k' } with ⟨r, t⟩
  rw [hr] at hec
  obtain ⟨ht, hrel⟩ := hec
  have hrel := hrel (n + 1) hge
  simp only at ht
  cases r with
  | error er =>
    cases er <;> try trivial
    obtain ⟨pv, hg, hpv⟩ := hrel
    exact ⟨pv, g, execStmt_decl_err cx n env x le g g _ hg, hpv, by rw [← hi.out, ← ht]⟩
  | ok v =>
    obtain ⟨w, hg, hvw, hkv, -⟩ := hrel
    obtain ⟨b, hloc, hbv, hout, hheap⟩ := declSt_facts m x v t hx_ (vrel_not_ref hvw)
    obtain ⟨hvd, hnu, hnb, hns⟩ := vrel_default hvw
    refine ⟨_, _, execStmt_decl_ok cx n env x le g g w hg (results_of_vrel hvw), ⟨?_, ?_, ?_⟩,
      ?_, pre_push _ _, [(x, b)], ?_⟩
    · intro y k' hk'
      unfold upd at hk'
      by_cases hyx : y = x
      · subst hyx
        simp only [ite_true, Option.some.injEq] at hk'
        subst hk'
        refine ⟨b, g.cells.size, v, defaultVal w, ?_, hbv, ?_, ?_, hvd, hkv, hnu, hnb, hns⟩
        · show Spec.lookup (declSt m y v t).locals y = some b
          rw [hloc]; simp [Spec.lookup]
        · rw [Env.lookup_bind _ _ _ _ hx_]; simp
        · simp [declG]
      · simp only [hyx, ite_false] at hk'
        obtain ⟨b', cell, v', w', hb', hv', hcell, hw', rest⟩ := hi.loc y k' hk'
        refine ⟨b', cell, v', w', ?_, ?_, ?_, ?_, rest⟩
        · show Spec.lookup (declSt m x v t).locals y = some b'
          rw [hloc]; simp only [Spec.lookup, Ne.symm hyx, ite_false]
          rw [show t.locals = s.locals from congrArg Spec.Core.locals ht]; exact hb'
        · refine bval_mono hheap ?_
          rw [show t.heap = s.heap from congrArg Spec.Core.heap ht]; exact hv'
        · rw [Env.lookup_bind _ _ _ _ hx_]; simp only [hyx, ite_false]; exact hcell
        · exact pre_get (pre_push _ _) hw'
    · show (declSt m x v t).out = g.out
      rw [hout, show t.out = s.out from congrArg Spec.Core.out ht]; exact hi.out
    · exact predeclared_bind hi.pre x _ hx_ hxt hxf
    · show Pre s.heap (declSt m x v t).heap
      rw [← show t.heap = s.heap from congrArg Spec.Core.heap ht]; exact hheap
    · show (declSt m x v t).locals = [(x, b)] ++ s.locals
      rw [hloc, show t.locals = s.locals from congrArg Spec.Core.locals ht]; rfl

theorem resL_of_resU {cx : Ctx} {κ : String → Option Kind} {c : Spec.Core} {g : St} {env : Env}
    (hi : Inv cx κ c g env) {sr : Except Spec.Stop Spec.Val × Spec.St} {r : Except Stop Env × St}
    (h : ResU (· = env) c g sr r) : ResL cx κ c g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e <;> first | trivial | exact h
  · obtain ⟨a, g', rfl, rfl, hinv, hh, hc, hl⟩ := resU_inv hi h
    exact ⟨a, g', rfl, hinv, hh, hc, [], by simp [hl]⟩

theorem withEnv_withEnv (a b : Env) (r : Except Stop α × St) :
    withEnv a (withEnv b r) = withEnv a r := by
  rcases r with ⟨_ | _, _⟩ <;> rfl

/-- A block run from a state whose core is `c`, against one whose core is `c'` equal to it. -/
theorem resU_core {P : α → Prop} {c c' : Spec.Core} (h : c' = c) {g : St} {sr} {r : Except Stop α × St}
    (hr : ResU P c' g sr r) : ResU P c g sr r := h ▸ hr

/-- The parts of the simulation at GoCore depth `N`: a statement list, a block, an `if`. -/
def ListSim (ds : Spec.Decls) (cx : Ctx) (N : Nat) : Prop :=
  ∀ κ ss κ' lss, lowerSs κ ss = some (κ', lss) → sdepthL ss ≤ N → ∀ (s : Spec.St) g env,
    Inv cx κ s.toCore g env → ∀ d, ResL cx κ' s.toCore g ((Spec.evalBlock ds d ss).run.run s)
      ((execStmts cx N env lss).run.run g)

def BlockSim (ds : Spec.Decls) (cx : Ctx) (N : Nat) : Prop :=
  ∀ κ ss κ' lss, lowerSs κ ss = some (κ', lss) → sdepthL ss + 1 ≤ N → ∀ (s : Spec.St) g env,
    Inv cx κ s.toCore g env → ∀ d, ResU (fun _ => True) s.toCore g ((Spec.eval ds d (.block ss)).run.run s)
      ((execBlock cx N env lss).run.run g)

def IfSim (ds : Spec.Decls) (cx : Ctx) (N : Nat) : Prop :=
  ∀ κ c a els κ' l, lowerS κ (.ifStmt c a els) = some (κ', l) → sdepth (.ifStmt c a els) ≤ N →
    ∀ (s : Spec.St) g env, Inv cx κ s.toCore g env → ∀ d,
      ResU (· = env) s.toCore g ((Spec.eval ds d (.ifStmt c a els)).run.run s)
        ((execStmt cx N env l).run.run g)

/-- What the lowering of an `if` statement takes apart. -/
theorem lowerS_if {κ κ' : String → Option Kind} {c a : Spec.Expr} {els : Option Spec.Expr} {l : Stmt}
    (h : lowerS κ (.ifStmt c a els) = some (κ', l)) :
    κ' = κ ∧ ∃ as lc κ₁ la le, a = .block as ∧ lowerX κ c = some (.bool, lc) ∧
      lowerSs κ as = some (κ₁, la) ∧ lowerElse κ els = some le ∧ l = .if_ none lc la le := by
  cases a with
  | block as =>
    simp only [lowerS] at h
    split at h
    · rename_i lc κ₁ la le hc ha he
      simp only [Option.some.injEq, Prod.mk.injEq] at h
      obtain ⟨rfl, rfl⟩ := h
      exact ⟨rfl, as, lc, κ₁, la, le, rfl, hc, ha, he, rfl⟩
    · cases h
  | _ => simp [lowerS] at h

/-- One statement, from the block and `if` parts at depths below. -/
theorem stmt_sim (ds : Spec.Decls) (cx : Ctx) (N : Nat) (hB : ∀ m < N, BlockSim ds cx m)
    (hI : IfSim ds cx N) (κ κ' : String → Option Kind) (st : Spec.Expr) (l : Stmt)
    (hl : lowerS κ st = some (κ', l)) (hN : sdepth st ≤ N) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv cx κ s.toCore g env) (d : Nat) :
    ResL cx κ' s.toCore g ((Spec.eval ds d st).run.run s) ((execStmt cx N env l).run.run g) := by
  match st, hl with
  | .decl m [x] false none [e], hl => exact decl_sim ds cx κ κ' m x e l hl s g env hi d N hN
  | .block ss, hl =>
    simp only [lowerS, Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, lb⟩, hlb, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by simp [sdepth] at hN; omega⟩
    rw [execStmt_block]
    exact resL_of_resU hi (resU_withEnv env
      (hB n (by omega) κ ss κ₁ lb hlb (by simp [sdepth] at hN; omega) s g env hi d))
  | .ifStmt c a els, hl =>
    have hκ : κ' = κ := (lowerS_if hl).1
    subst hκ
    exact resL_of_resU hi (hI _ c a els _ l hl hN s g env hi d)
  | .decl _ [] _ _ _, hl | .decl _ (_ :: _ :: _) _ _ _, hl | .decl _ [_] true _ _, hl
  | .decl _ [_] false (some _) _, hl | .decl _ [_] false none [], hl
  | .decl _ [_] false none (_ :: _ :: _), hl
  | .lit _, hl | .name _, hl | .unary .., hl | .binary .., hl | .call .., hl | .select .., hl
  | .paren _, hl | .tuple _, hl | .ifExpr .., hl | .mtch .., hl | .ret _, hl | .assign .., hl
  | .incdec .., hl | .interp _, hl | .lambda .., hl | .named .., hl | .unsupported _, hl =>
    simp [lowerS] at hl

/-- A statement list after a first statement: what the rest does from the state the
    first left, seen from the state before it. -/
theorem resL_after {cx : Ctx} {κ' : String → Option Kind} {c : Spec.Core} {t : Spec.St} {g g₁ : St}
    {pre : List (String × Spec.Binding)} (hh : Pre c.heap t.heap) (hc : Pre g.cells g₁.cells)
    (hl : t.locals = pre ++ c.locals) {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop Env × St} (h : ResL cx κ' t.toCore g₁ sr r) : ResL cx κ' c g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e <;> first | trivial | exact h
  · obtain ⟨env', g', rfl, hinv, hh', hc', pre', hl'⟩ := h
    exact ⟨env', g', rfl, hinv, pre_trans hh hh', pre_trans hc hc', pre' ++ pre,
      by rw [hl']; simp [hl]⟩

theorem list_sim (ds : Spec.Decls) (cx : Ctx) (N : Nat)
    (ih : ∀ m < N, BlockSim ds cx m ∧ IfSim ds cx m) : ListSim ds cx N := by
  intro κ ss
  induction ss generalizing κ with
  | nil =>
    intro κ' lss hl hN s g env hi d
    simp only [lowerSs, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by simp [sdepthL] at hN; omega⟩
    rw [evalBlock_nil, execStmts_nil]
    exact ⟨env, g, rfl, hi, pre_refl _, pre_refl _, [], rfl⟩
  | cons st rest ihl =>
    intro κ' lss hl hN s g env hi d
    simp only [lowerSs, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, l⟩, hst, ⟨κ₂, ls⟩, hrest, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by simp [sdepthL] at hN; omega⟩
    have hs := stmt_sim ds cx n (fun m hm => (ih m (by omega)).1) (ih n (by omega)).2 κ κ₁ st l hst
      (by simp [sdepthL] at hN; omega) s g env hi d
    rw [execStmts_cons]
    cases rest with
    | nil =>
      simp only [lowerSs, Option.some.injEq, Prod.mk.injEq] at hrest
      obtain ⟨rfl, rfl⟩ := hrest
      rw [evalBlock_one]
      rcases hr : (Spec.eval ds d st).run.run s with ⟨r, t⟩
      rw [hr] at hs
      cases r with
      | error e =>
        cases e <;> try trivial
        obtain ⟨pv, g', hgo, hp, ho⟩ := hs
        rw [hgo]; exact ⟨pv, g', rfl, hp, ho⟩
      | ok v =>
        obtain ⟨env', g', hgo, hinv, hh, hc, pre, hl'⟩ := hs
        rw [hgo]
        simp only
        rw [execStmts_nil]
        exact ⟨env', g', rfl, hinv, hh, hc, pre, hl'⟩
    | cons st' rest' =>
      rw [evalBlock_cons]
      rcases hr : (Spec.eval ds d st).run.run s with ⟨r, t⟩
      rw [hr] at hs
      cases r with
      | error e =>
        cases e <;> try trivial
        obtain ⟨pv, g', hgo, hp, ho⟩ := hs
        rw [hgo]; exact ⟨pv, g', rfl, hp, ho⟩
      | ok v =>
        obtain ⟨env', g', hgo, hinv, hh, hc, pre, hl'⟩ := hs
        rw [hgo]
        simp only
        exact resL_after hh hc hl' (ihl κ₁ κ₂ ls hrest (by simp [sdepthL] at hN ⊢; omega) t g' env' hinv d)

theorem block_sim (ds : Spec.Decls) (cx : Ctx) (N : Nat) (hL : ∀ m < N, ListSim ds cx m) :
    BlockSim ds cx N := by
  intro κ ss κ' lss hl hN s g env hi d
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
  cases d with
  | zero => rw [eval_zero]; trivial
  | succ d =>
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k =>
  rw [eval_block_run, tick_succ s k hs]
  simp only
  rw [execBlock_run]
  have h := hL n (by omega) κ ss κ' lss hl (by omega) { s with steps := k } g env.push (inv_push hi) d
  rcases hr : (Spec.evalBlock ds d ss).run.run { s with steps := k } with ⟨r, t⟩
  rw [hr] at h
  cases r with
  | error e =>
    cases e <;> try trivial
    obtain ⟨pv, g', hgo, hp, ho⟩ := h
    rw [hgo]; exact ⟨pv, g', rfl, hp, ho⟩
  | ok v =>
    obtain ⟨env', g', hgo, hinv, hh, hc, pre, hl'⟩ := h
    rw [hgo]
    refine ⟨(), g', rfl, trivial, ?_, hinv.out, hh, hc⟩
    show Spec.exitScope t.locals s.locals.length = s.locals
    rw [hl']; simp [Spec.exitScope]

theorem if_sim (ds : Spec.Decls) (cx : Ctx) (N : Nat) (hB : ∀ m < N, BlockSim ds cx m)
    (hI : ∀ m < N, IfSim ds cx m) : IfSim ds cx N := by
  intro κ c a els κ' l hl hN s g env hi d
  obtain ⟨rfl, as, lc, κ₁, la, le, rfl, hlc, hla, hle, rfl⟩ := lowerS_if hl
  obtain ⟨hnc, hlce⟩ := lowerX_some hlc
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 2 := ⟨N - 2, by simp [sdepth] at hN; omega⟩
  have hgc : gdepth c ≤ n + 1 := by simp [sdepth] at hN; omega
  have has : sdepthL as + 1 ≤ n + 1 := by simp [sdepth] at hN; omega
  cases d with
  | zero => rw [eval_zero]; trivial
  | succ d =>
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k =>
  have hip := inv_push hi
  have hec := expr_correct ds cx κ' c .bool lc hlce hnc { s with steps := k } g env.push hip.loc
    hip.pre d
  rcases hr : (Spec.eval ds d c).run.run { s with steps := k } with ⟨r, t⟩
  rw [hr] at hec
  obtain ⟨ht, hrel⟩ := hec
  have hrel := hrel (n + 1) hgc
  simp only at ht
  cases r with
  | error er =>
    rw [eval_ifStmt_err ds d k c _ els s t er hs hr]
    cases er <;> try trivial
    obtain ⟨pv, hg, hpv⟩ := hrel
    rw [execStmt_if_err cx n env lc la le g g _ hg]
    exact ⟨pv, g, rfl, hpv, by rw [show t.out = s.out from congrArg Spec.Core.out ht]; exact hi.out⟩
  | ok v =>
    obtain ⟨w, hg, hvw, hkv, -⟩ := hrel
    obtain ⟨b, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    have hit : Inv cx κ' t.toCore g env.push := ht ▸ hip
    cases els with
    | none =>
      simp only [lowerElse, Option.some.injEq] at hle
      subst hle
      rw [eval_ifStmt_none ds d k c (.block as) b s t hs hr]
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la none g g ty hg]
        have hb := resU_core ht (hB (n + 1) (by omega) κ' as κ₁ la hla has t g env.push hit d)
        simp only [ite_true]
        rcases hrb : (Spec.eval ds d (.block as)).run.run t with ⟨rb, tb⟩
        rw [hrb] at hb
        cases rb <;> exact resU_withEnv env hb
      | false =>
        rw [execStmt_if_false cx n env lc la none g g ty hg]
        simp only [Bool.false_eq_true, ite_false]
        exact ⟨env, g, rfl, rfl, congrArg Spec.Core.locals ht, by
          rw [show t.out = s.out from congrArg Spec.Core.out ht]; exact hi.out,
          by rw [show t.heap = s.heap from congrArg Spec.Core.heap ht]; exact pre_refl _, pre_refl _⟩
    | some e =>
      rw [eval_ifStmt_some ds d k c (.block as) e b s t hs hr]
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la _ g g ty hg]
        exact resU_withEnv env (resU_core ht
          (hB (n + 1) (by omega) κ' as κ₁ la hla has t g env.push hit d))
      | false =>
        rw [execStmt_if_false cx n env lc la _ g g ty hg]
        simp only [Bool.false_eq_true, ite_false]
        match e, hle with
        | .block bs, hle =>
          simp only [lowerElse, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, lb⟩, hlb, rfl⟩ := hle
          simp only
          rw [execStmt_block, withEnv_withEnv]
          exact resU_withEnv env (resU_core ht
            (hB n (by omega) κ' bs κ₂ lb hlb (by simp [sdepth, sdepthO] at hN; omega) t g env.push hit d))
        | .ifStmt c₂ a₂ els₂, hle =>
          simp only [lowerElse, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, l₂⟩, hl₂, rfl⟩ := hle
          simp only
          exact resU_withEnv env (resU_core ht
            (hI (n + 1) (by omega) κ' c₂ a₂ els₂ κ₂ l₂ hl₂ (by simp [sdepth, sdepthO] at hN ⊢; omega)
              t g env.push hit d))
        | .lit _, hle | .name _, hle | .unary .., hle | .binary .., hle | .call .., hle
        | .select .., hle | .paren _, hle | .tuple _, hle | .ifExpr .., hle | .mtch .., hle
        | .ret _, hle | .assign .., hle | .incdec .., hle | .decl .., hle | .interp _, hle
        | .lambda .., hle | .named .., hle | .unsupported _, hle => simp [lowerElse] at hle

/-- All three parts, at every depth. -/
theorem sim_all (ds : Spec.Decls) (cx : Ctx) :
    ∀ N, ListSim ds cx N ∧ BlockSim ds cx N ∧ IfSim ds cx N := by
  intro N
  refine Nat.strongRecOn N fun N ih => ?_
  exact ⟨list_sim ds cx N fun m hm => ⟨(ih m hm).2.1, (ih m hm).2.2⟩,
      block_sim ds cx N fun m hm => (ih m hm).1,
      if_sim ds cx N (fun m hm => (ih m hm).2.1) (fun m hm => (ih m hm).2.2)⟩

/-- **M5, step 2b: `val`/`var`, `if` statements and blocks.** A statement list the
    reference lowering accepts (`lowerSs`), run by galaspec from a state related to
    GoCore's (the locals the lowering is told of, by `LocRel`; equal outputs; `true` and
    `false` unshadowed): wherever galaspec's run ends normally, GoCore's run of the
    lowering, at every depth from `sdepthL ss` on, ends normally too, in related states
    with the kinds `κ'` the list declared, galaspec's locals only extended and both sides'
    cells only added to; wherever galaspec's run panics, GoCore's panics the same way, with
    equal outputs. Nothing is claimed when galaspec runs out of fuel, stops `stuck` or
    `unsupported`, or ends with a `return` (step 2 has none).

    Covered: `val x = e` and `var x = e` of one name and no type, lowered to `var x = e`;
    `if` statements whose branches are blocks, an `else` being a block or another `if`,
    lowered to Go's `if` and `else if`; blocks. The expressions are step 2a's
    (`expr_correct`). Not covered: assignments, expression statements, calls (step 3).
    Stated for GoCore's evaluator as it is. `RunsStep2AsWritten` (`Gala/M5/StmtOrder.lean`)
    is the hypothesis step 3's theorem will take on an order: left to right and GoCore's
    model of gc's order meet it. -/
theorem stmts_correct (ds : Spec.Decls) (cx : Ctx) (κ κ' : String → Option Kind)
    (ss : List Spec.Expr) (lss : List Stmt) (hl : lowerSs κ ss = some (κ', lss)) (s : Spec.St)
    (g : St) (env : Env) (hloc : LocRel κ s.toCore g.cells env) (hout : s.out = g.out)
    (hp : Predeclared cx env) (d : Nat) :
    ∀ N ≥ sdepthL ss, ResL cx κ' s.toCore g ((Spec.evalBlock ds d ss).run.run s)
      ((execStmts cx N env lss).run.run g) :=
  fun N hN => (sim_all ds cx N).1 κ ss κ' lss hl hN s g env ⟨hloc, hout, hp⟩ d

/-- **A block**, galaspec's `{ ss }` against Go's: as `stmts_correct`, and afterwards
    galaspec's locals are those before the block, so the locals stay related as before it. -/
theorem block_correct (ds : Spec.Decls) (cx : Ctx) (κ κ' : String → Option Kind)
    (ss : List Spec.Expr) (lss : List Stmt) (hl : lowerSs κ ss = some (κ', lss)) (s : Spec.St)
    (g : St) (env : Env) (hloc : LocRel κ s.toCore g.cells env) (hout : s.out = g.out)
    (hp : Predeclared cx env) (d : Nat) :
    ∀ N ≥ sdepthL ss + 2, ResL cx κ s.toCore g ((Spec.eval ds d (.block ss)).run.run s)
      ((execStmt cx N env (.block lss)).run.run g) := by
  intro N hN
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
  rw [execStmt_block]
  exact resL_of_resU ⟨hloc, hout, hp⟩ (resU_withEnv env
    ((sim_all ds cx n).2.1 κ ss κ' lss hl (by omega) s g env ⟨hloc, hout, hp⟩ d))

end Sim


/-! ### The hypotheses can be met

`x` holds `5` on both sides (step 2a's instance), and the body
`val y = x + 1; if (y > 5) { var z = y; if (z > 0) {} } else { val w = 0; if (w > 1) {} }`,
a program GALA and Go accept: every local is read. -/

section Instance
open Gala.GoCore.Full

/-- `if (c) {}` -/
def emptyIf (c : Spec.Expr) : Spec.Expr := .ifStmt c (.block []) none

def instBody : List Spec.Expr :=
  [.decl .val ["y"] false none [instExpr],
   .ifStmt (.binary ">" (.name "y") (.lit (.int 5)))
     (.block [.decl .var ["z"] false none [.name "y"], emptyIf (.binary ">" (.name "z") (.lit (.int 0)))])
     (some (.block [.decl .val ["w"] false none [.lit (.int 0)],
       emptyIf (.binary ">" (.name "w") (.lit (.int 1)))]))]

def instLowered : List Stmt :=
  [.decl (.value false [.mk ["y"] none [.binary "+" (.ident "x") (.intLit 1)]]),
   .if_ none (.binary ">" (.ident "y") (.intLit 5))
     [.decl (.value false [.mk ["z"] none [.ident "y"]]),
      .if_ none (.binary ">" (.ident "z") (.intLit 0)) [] none]
     (some (.block [.decl (.value false [.mk ["w"] none [.intLit 0]]),
       .if_ none (.binary ">" (.ident "w") (.intLit 1)) [] none]))]

theorem inst_lower : lowerSs instKinds instBody = some (upd instKinds "y" .int, instLowered) := by
  rfl

/-- What GoCore computes on the instance: its run ends normally, having taken the `then`
    branch, with cells `x`, `y` and `z` holding `5`, `6` and `6`. -/
theorem inst_runs :
    (((execStmts instCtx 10 instEnv instLowered).run.run instSt).1 matches .ok _) ∧
    ((execStmts instCtx 10 instEnv instLowered).run.run instSt).2.cells =
      #[.int intTy 5, .int intTy 6, .int intTy 6] := by
  exact ⟨rfl, rfl⟩

/-- `stmts_correct` applied to the instance, every hypothesis discharged. -/
theorem stmts_correct_instance (ds : Spec.Decls) (steps d : Nat) :
    ∀ N ≥ sdepthL instBody, ResL instCtx (upd instKinds "y" .int) instCore instSt
      ((Spec.evalBlock ds d instBody).run.run { instCore with steps })
      ((execStmts instCtx N instEnv instLowered).run.run instSt) :=
  stmts_correct ds instCtx instKinds _ instBody instLowered inst_lower { instCore with steps } instSt
    instEnv inst_locRel rfl inst_predeclared d

/-! galaspec's side of the instance, step by step: `eval` is defined by well-founded
recursion, so its runs are computed through its unfolding lemmas, not by `rfl`. -/

theorem id_pure {α : Type} (a : α) : (pure a : Id α) = a := rfl

theorem spec_run_get (c : Spec.Core) : (get : Spec.H Spec.Core).run.run c = (.ok c, c) := rfl

/-- galaspec's locals after `val y = x + 1`. -/
def instCore₁ : Spec.Core :=
  { instCore with locals := [("y", ⟨.int 6, false⟩), ("x", ⟨.int 5, false⟩)] }

/-- ... and after `var z = y`: `z` is a heap cell. -/
def instCore₂ : Spec.Core :=
  { instCore₁ with locals := ("z", ⟨.ref 0, true⟩) :: instCore₁.locals, heap := #[.int 6] }

theorem inst_eval_y : (Spec.eval default 5 instExpr).run.run { instCore with steps := 99 } =
    (.ok (.int 6), { instCore with steps := 96 }) := by
  simp only [instExpr]
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, instCore, Spec.resolve,
    Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq, maxInt_eq,
    Spec.wrap]
  rfl

theorem inst_eval_cond :
    (Spec.eval default 5 (.binary ">" (.name "y") (.lit (.int 5)))).run.run { toCore := instCore₁, steps := 95 } =
      (.ok (.bool true), { toCore := instCore₁, steps := 92 }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, instCore₁, instCore, Spec.resolve,
    Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq, maxInt_eq]

theorem inst_eval_z : (Spec.eval default 3 (.name "y")).run.run { toCore := instCore₁, steps := 90 } =
    (.ok (.int 6), { toCore := instCore₁, steps := 89 }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, instCore₁, instCore, Spec.resolve,
    Spec.lookupVar, Spec.lookup, Spec.deref, id_pure]

theorem inst_eval_cz :
    (Spec.eval default 3 (.binary ">" (.name "z") (.lit (.int 0)))).run.run { toCore := instCore₂, steps := 88 } =
      (.ok (.bool true), { toCore := instCore₂, steps := 85 }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, instCore₂, instCore₁, instCore,
    Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq,
    maxInt_eq, spec_run_get]

/-- **galaspec's run of the instance ends normally** (with enough fuel), so the conclusion
    of `stmts_correct_instance` is not vacuous there. -/
theorem inst_spec_ok : ∃ v s', (Spec.evalBlock default 6 instBody).run.run { instCore with steps := 100 } =
    (.ok v, s') := by
  rw [instBody, evalBlock_cons, eval_decl_run _ 5 99 _ _ _ _ rfl, inst_eval_y]
  simp only
  rw [evalBlock_one, eval_ifStmt_some _ 5 95 _ _ _ true _ _ rfl inst_eval_cond]
  simp only [ite_true]
  rw [eval_block_run, tick_succ _ 91 rfl]
  simp only
  rw [evalBlock_cons, eval_decl_run _ 3 90 _ _ _ _ rfl, inst_eval_z]
  simp only
  rw [evalBlock_one, emptyIf, eval_ifStmt_none _ 3 88 _ _ true _ _ rfl inst_eval_cz]
  simp only [ite_true]
  rw [eval_block_run, tick_succ _ 84 rfl]
  simp only
  rw [evalBlock_nil]
  exact ⟨_, _, rfl⟩

/-- What `stmts_correct` then says of the instance: galaspec's run ends normally, and GoCore's
    run of the lowering ends normally too, in a state related to galaspec's. -/
theorem inst_gocore_ok (N : Nat) (hN : N ≥ sdepthL instBody) :
    ∃ v s', (Spec.evalBlock default 6 instBody).run.run { instCore with steps := 100 } = (.ok v, s') ∧
      ∃ env' g', (execStmts instCtx N instEnv instLowered).run.run instSt = (.ok env', g') ∧
        Inv instCtx (upd instKinds "y" .int) s'.toCore g' env' := by
  have h := stmts_correct_instance default 100 6 N hN
  obtain ⟨v, s', hv⟩ := inst_spec_ok
  rw [hv] at h
  obtain ⟨env', g', hg, hinv, -⟩ := h
  exact ⟨v, s', hv, env', g', hg, hinv⟩

end Instance

end Gala.M5
