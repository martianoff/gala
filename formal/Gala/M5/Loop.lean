import Std.Data.String.ToNat
import Gala.M5.Call
namespace Gala.M5
open Gala.GoCore.Full

section LoopRuns
variable (cx : Ctx)

theorem loopM_succ (k : Nat) (env : Env) (body : Env → GM (Option Env)) (g : St) :
    (loopM (k + 1) env body).run.run g = match (body env).run.run g with
      | (.ok (some env'), g') => (loopM k env' body).run.run g'
      | (.ok none, g') => (.ok (), g')
      | (.error e, g') => (.error e, g') := by
  rw [loopM]
  simp only [grun_bind]
  rcases (body env).run.run g with ⟨r, g'⟩
  cases r with
  | error => rfl
  | ok o => cases o <;> rfl

/-- One iteration of `for { body }` (`forLoop` with no condition and no post statement). -/
def iterF (n : Nat) (body : List Stmt) : Env → GM (Option Env) := fun env => do
  tick
  let r ← tryCatch (do execBlock cx n env body; pure true) fun
    | .brk => pure false
    | .cont => pure true
    | e => throw e
  if !r then return none
  let fresh ← (env.scopes.headD []).mapM fun (x, cell) => do
    pure (x, ← allocAs (← cellType cell) (← loadCell cell))
  let env := { env with scopes := fresh :: env.scopes.tail }
  return some env

theorem forLoop_run (n : Nat) (env : Env) (body : List Stmt) (g : St) :
    (forLoop cx (n + 1) env none none body).run.run g = (loopM (g.steps + 1) env (iterF cx n body)).run.run g := by
  rw [forLoop]
  rfl

/-- An iteration's outcome from its body's: ending or `continue` go on, `break` ends the
    loop, anything else stops it. -/
def iterRes (env : Env) : Except Stop Env × St → Except Stop (Option Env) × St
  | (.ok _, g') => (.ok (some env), g')
  | (.error .cont, g') => (.ok (some env), g')
  | (.error .brk, g') => (.ok none, g')
  | (.error e, g') => (.error e, g')

theorem iterF_run (j : Nat) (e : Env) (body : List Stmt) (g : St) (k : Nat) (hk : g.steps = k + 1) :
    (iterF cx (j + 1) body e.push).run.run g =
      iterRes e.push ((execStmts cx j e.push.push body).run.run { g with steps := k }) := by
  unfold iterF
  simp only [grun_bind, gtick_succ g k hk, gtryCatch, execBlock_run]
  rcases (execStmts cx j e.push.push body).run.run { g with steps := k } with ⟨r, g'⟩
  rcases r with ex | _
  · cases ex <;> first | rfl | (simp [Env.push]; rfl)
  · simp [Env.push]; rfl

/-- What follows an iteration whose body gave `r`: the rest of the loop. -/
def afterIter (k : Nat) (env : Env) (F : Env → GM (Option Env)) : Except Stop Env × St → Except Stop Unit × St
  | (.ok _, g') => (loopM k env F).run.run g'
  | (.error .cont, g') => (loopM k env F).run.run g'
  | (.error .brk, g') => (.ok (), g')
  | (.error e, g') => (.error e, g')

theorem loop_iter (k j : Nat) (e : Env) (body : List Stmt) (g : St) (gk : Nat) (hk : g.steps = gk + 1) :
    (loopM (k + 1) e.push (iterF cx (j + 1) body)).run.run g =
      afterIter k e.push (iterF cx (j + 1) body) ((execStmts cx j e.push.push body).run.run { g with steps := gk }) := by
  rw [loopM_succ, iterF_run cx j e body g gk hk]
  rcases (execStmts cx j e.push.push body).run.run { g with steps := gk } with ⟨r, g'⟩
  rcases r with ex | _
  · cases ex <;> rfl
  · rfl

/-- `for { body }` as a function's body. -/
theorem execStmts_for (n : Nat) (env : Env) (body : List Stmt) (g : St) :
    (execStmts cx (n + 3) env [.for_ none none none body]).run.run g =
      withEnv env ((loopM (g.steps + 1) env.push (iterF cx n body)).run.run g) := by
  rw [execStmts_cons]
  have h : (execStmt cx (n + 2) env (.for_ none none none body)).run.run g =
      withEnv env ((forLoop cx (n + 1) env.push none none body).run.run g) := by
    rw [execStmt]
    simp only [execInit, grun_bind, run_pureM]
    rcases (forLoop cx (n + 1) env.push none none body).run.run g with ⟨r, g'⟩
    cases r <;> rfl
  rw [h, forLoop_run]
  rcases (loopM (g.steps + 1) env.push (iterF cx n body)).run.run g with ⟨r, g'⟩
  cases r with
  | error => rfl
  | ok => simp only [withEnv]; rw [execStmts_nil]

theorem rhsValues_one (n : Nat) (env : Env) (le : Expr) (hta : ∀ x te, le ≠ .typeAssert x te)
    (hix : ∀ x i, le ≠ .index x [i]) (g : St) :
    (rhsValues cx (n + 1) env 1 [le]).run.run g = match (evalExpr cx n env le).run.run g with
      | (.ok w, g') => if w.results.length != 1 then (.error (.stuck "assignment count mismatch"), g')
          else (.ok w.results, g')
      | (.error e, g') => (.error e, g') := by
  rw [rhsValues]
  · simp only [grun_bind]
    rcases (evalExpr cx n env le).run.run g with ⟨r, g'⟩
    cases r with
    | error => rfl
    | ok w =>
      simp only
      split <;> rfl
  · exact fun x te h => hta x te h
  · exact fun x i h => hix x i h

/-- `y := le`, `y` new in the innermost scope: a new cell holding `le`'s value. -/
theorem execStmt_define (n : Nat) (env : Env) (y : String) (le : Expr) (g g' : St) (w : Val)
    (hy : y ≠ "_") (hcur : (env.scopes.headD []).lookup y = none) (hta : ∀ x te, le ≠ .typeAssert x te)
    (hix : ∀ x i, le ≠ .index x [i]) (hr : (evalExpr cx (n + 1) env le).run.run g = (.ok w, g'))
    (hw : w.results = [w]) :
    (execStmt cx (n + 4) env (.assign ":=" [.ident y] [le])).run.run g = (.ok (env.bind y g'.cells.size), declG w g') := by
  rw [execStmt]
  show (assign cx (n + 3) env ":=" [.ident y] [le]).run.run g = _
  rw [assign]
  simp only [beq_self_eq_true, ite_true, grun_bind, List.length_singleton, rhsValues_one cx (n + 1) env le hta hix, hr, hw]
  have hcur' : List.lookup y (env.scopes.head?.getD []) = none := by simpa [List.headD_eq_head?_getD] using hcur
  simp [hcur', alloc, allocAs_run, declG]
  rfl

theorem execStmt_define_err (n : Nat) (env : Env) (y : String) (le : Expr) (g g' : St) (e : Stop)
    (hta : ∀ x te, le ≠ .typeAssert x te) (hix : ∀ x i, le ≠ .index x [i])
    (hr : (evalExpr cx (n + 1) env le).run.run g = (.error e, g')) :
    (execStmt cx (n + 4) env (.assign ":=" [.ident y] [le])).run.run g = (.error e, g') := by
  rw [execStmt]
  show (assign cx (n + 3) env ":=" [.ident y] [le]).run.run g = _
  rw [assign]
  simp only [beq_self_eq_true, ite_true, grun_bind, List.length_singleton, rhsValues_one cx (n + 1) env le hta hix, hr]

theorem execStmt_continue (n : Nat) (env : Env) (g : St) :
    (execStmt cx (n + 1) env (.branch "continue")).run.run g = (.error .cont, g) := by
  rw [execStmt]; rfl

/-- One parameter assigned from its temporary: the parameter `x`, its cell and type, the
    temporary `t`, its value `u`, and `u` converted to the type, `v`. -/
structure Asg where
  x : String
  cell : Nat
  ty : Ty
  t : String
  u : Val
  v : Val

/-- The state after the stores of `p1, .., pk = t1, .., tk`. -/
def storeAll (g : St) : List Asg → St
  | [] => g
  | a :: as => storeAll { g with cells := g.cells.modify a.cell (·.setAt [] a.v) } as

theorem evalPlace_ident (n : Nat) (env : Env) (x : String) (cell : Nat) (t : Ty) (g : St)
    (hl : env.lookup x = some cell) (ht : g.types[cell]? = some t) :
    (evalPlace cx (n + 2) env (.ident x)).run.run g = (.ok (.loc ⟨cell, []⟩ t), g) := by
  rw [evalPlace]
  · simp only [evalAddr, hl, grun_bind, cellType, gget_run, ht]; rfl
  all_goals intros; simp_all

theorem match_ident (x : String) (hx : x ≠ "_") (A : GM (Option Place)) (B : Expr → GM (Option Place)) :
    (match Expr.ident x with
      | .ident "_" => A
      | y => B y) = B (.ident x) := by
  split
  · rename_i heq; cases heq; exact absurd rfl hx
  · rfl

theorem targets_run (n : Nat) (env : Env) (g : St) : ∀ (A : List Asg),
    (∀ a ∈ A, a.x ≠ "_" ∧ env.lookup a.x = some a.cell ∧ g.types[a.cell]? = some a.ty) →
    ((A.map fun (a : Asg) => Expr.ident a.x).mapM (fun (x : Expr) => (match x with
        | .ident "_" => pure none
        | x => do pure (some (← evalPlace cx (n + 2) env x)) : GM (Option Place)))).run.run g =
      (Except.ok (A.map fun (a : Asg) => some (Place.loc ⟨a.cell, []⟩ a.ty)), g)
  | [], _ => rfl
  | a :: A, h => by
    obtain ⟨hx, hl, ht⟩ := h a (by simp)
    simp only [List.map_cons, List.mapM_cons, grun_bind, match_ident a.x hx]
    simp only [evalPlace_ident cx n env a.x a.cell a.ty g hl ht, run_pureM]
    rw [targets_run n env g A (fun b hb => h b (by simp [hb]))]

theorem mapM_loads (n : Nat) (env : Env) (g : St) : ∀ (A : List Asg),
    (∀ a ∈ A, (evalExpr cx (n + 1) env (.ident a.t)).run.run g = (.ok a.u, g)) →
    ((A.map fun (a : Asg) => Expr.ident a.t).mapM (evalExpr cx (n + 1) env)).run.run g =
      (Except.ok (A.map (·.u)), g)
  | [], _ => rfl
  | a :: A, h => by
    simp only [List.map_cons, List.mapM_cons, grun_bind, h a (by simp)]
    rw [mapM_loads n env g A (fun b hb => h b (by simp [hb]))]
    rfl

theorem rhs_loads (n : Nat) (env : Env) (g : St) (A : List Asg)
    (h : ∀ a ∈ A, (evalExpr cx (n + 1) env (.ident a.t)).run.run g = (.ok a.u, g) ∧ a.u.results = [a.u]) :
    (rhsValues cx (n + 2) env A.length (A.map fun (a : Asg) => Expr.ident a.t)).run.run g =
      (Except.ok (A.map (·.u)), g) := by
  match A, h with
  | [a], h =>
    obtain ⟨h1, h2⟩ := h a (by simp)
    simp only [List.map_cons, List.map_nil, List.length_singleton]
    rw [rhsValues_one cx (n + 1) env _ (by intro x te h; cases h) (by intro x i h; cases h), h1]
    simp [h2]
  | [], _ =>
    rw [rhsValues]
    · rfl
    all_goals intros; simp_all
  | a :: b :: A, h =>
    rw [rhsValues]
    · exact mapM_loads cx n env g _ (fun c hc => (h c hc).1)
    all_goals intros; simp_all

theorem mapM_A {β : Type} (F : Expr → GM β) (proj : Asg → Expr) (r : Asg → β) (g : St) : ∀ (A : List Asg),
    (∀ a ∈ A, (F (proj a)).run.run g = (.ok (r a), g)) → ((A.map proj).mapM F).run.run g = (.ok (A.map r), g)
  | [], _ => rfl
  | a :: A, h => by
    simp only [List.map_cons, List.mapM_cons, grun_bind, h a (by simp)]
    rw [mapM_A F proj r g A (fun b hb => h b (by simp [hb]))]
    rfl

theorem store_run (cell : Nat) (w : Val) (g : St) (hc : cell < g.cells.size) :
    (store ⟨cell, []⟩ w).run.run g = (.ok (), { g with cells := g.cells.modify cell (·.setAt [] w) }) := by
  unfold store
  simp only [grun_bind, gget_run]
  rw [Array.getElem?_eq_getElem hc]
  simp only [Val.at]
  split
  · rename_i h; cases h
  · simp only [List.isEmpty_nil, Bool.not_true, Bool.false_eq_true, ite_false, gmodify_run]
  · simp only [gmodify_run]

theorem forIn_stores (F : Option Place × Val → PUnit → GM (ForInStep PUnit)) : ∀ (A : List Asg) (g : St),
    (∀ a ∈ A, ∀ g', a.cell < g'.cells.size → (F (some (.loc ⟨a.cell, []⟩ a.ty), a.u) PUnit.unit).run.run g' =
      (.ok (.yield PUnit.unit), { g' with cells := g'.cells.modify a.cell (·.setAt [] a.v) })) →
    (∀ a ∈ A, a.cell < g.cells.size) →
    (forIn ((A.map fun (a : Asg) => some (Place.loc ⟨a.cell, []⟩ a.ty)).zip (A.map (·.u))) PUnit.unit F).run.run g =
      (.ok PUnit.unit, storeAll g A)
  | [], g, _, _ => rfl
  | a :: A, g, hF, hs => by
    simp only [List.map_cons, List.zip_cons_cons, List.forIn_cons, grun_bind, hF a (by simp) g (hs a (by simp))]
    exact forIn_stores F A _ (fun b hb => hF b (by simp [hb])) (fun b hb => by simpa using hs b (by simp [hb]))

theorem execStmt_assign_params (n : Nat) (env : Env) (g : St) (A : List Asg)
    (hp : ∀ a ∈ A, a.x ≠ "_" ∧ env.lookup a.x = some a.cell ∧ g.types[a.cell]? = some a.ty)
    (hl : ∀ a ∈ A, (evalExpr cx (n + 1) env (.ident a.t)).run.run g = (.ok a.u, g) ∧ a.u.results = [a.u])
    (hc : ∀ a ∈ A, ∀ g', (convert cx (n + 1) a.ty a.u).run.run g' = (.ok a.v, g'))
    (hs : ∀ a ∈ A, a.cell < g.cells.size) :
    (execStmt cx (n + 4) env (.assign "=" (A.map fun (a : Asg) => Expr.ident a.x) (A.map fun (a : Asg) => Expr.ident a.t))).run.run g =
      (.ok env, storeAll g A) := by
  rw [execStmt]
  show (assign cx (n + 3) env "=" _ _).run.run g = _
  rw [assign]
  simp only [List.length_map]
  simp only [show ("=" == ":=") = false from rfl, show ("=" == "=") = true from rfl, Bool.false_eq_true, ite_false, ite_true]
  simp only [grun_bind]
  rw [mapM_A _ (fun a => Expr.ident a.x) (fun a => some (Place.loc ⟨a.cell, []⟩ a.ty)) g A ?ht]
  case ht =>
    intro a ha
    obtain ⟨hx, hl', ht'⟩ := hp a ha
    split
    · rename_i heq; injection heq with h; exact absurd h hx
    · simp only [grun_bind, evalPlace_ident cx n env a.x a.cell a.ty g hl' ht', run_pureM]
  simp only
  rw [rhs_loads cx n env g A hl]
  simp only
  rw [forIn_stores _ A g ?hF hs]
  case hF =>
    intro a ha g' hc'
    simp only [storePlace, grun_bind, hc a ha g', store_run a.cell a.v g' hc', run_pureM]
  rfl

end LoopRuns

section Shapes
open Gala.GoCore.Full

/-- An expression `rhsValues` takes as one value: no type assertion and no index. -/
def rhsSimple : Expr → Bool
  | .typeAssert .. => false
  | .index .. => false
  | _ => true

theorem rhsSimple_ta {le : Expr} (h : rhsSimple le = true) : ∀ x te, le ≠ .typeAssert x te := by
  intro x te he; subst he; cases h

theorem rhsSimple_ix {le : Expr} (h : rhsSimple le = true) : ∀ x i, le ≠ .index x [i] := by
  intro x i he; subst he; cases h

theorem lowerE_simple (κ : String → Option Kind) : ∀ (e : Spec.Expr) (k : Kind) (le : Expr),
    lowerE κ e = some (k, le) → rhsSimple le = true
  | .lit l, k, le, h => by
    simp only [lowerE, Option.some.injEq] at h
    cases l <;> simp only [lowerLit] at h <;> (try split at h) <;> (obtain ⟨-, rfl⟩ := Prod.mk.inj h; rfl)
  | .name x, k, le, h => by
    simp only [lowerE] at h
    split at h
    · cases h
    · simp only [Option.map_eq_some_iff] at h
      obtain ⟨_, -, h⟩ := h
      cases h; rfl
  | .paren x, k, le, h => by
    simp only [lowerE, Option.map_eq_some_iff] at h
    obtain ⟨_, -, h⟩ := h
    cases h; rfl
  | .unary op x, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq] at h
    obtain ⟨_, -, _, -, h⟩ := h
    cases h; rfl
  | .binary op l r, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨_, -, _, -, _, -, h⟩ := h
    split at h
    · cases h
    · simp only [Option.pure_def, Option.some.injEq] at h; cases h; rfl
  | .call .., _, _, h | .select .., _, _, h | .tuple _, _, _, h | .ifExpr .., _, _, h | .mtch .., _, _, h
  | .block _, _, _, h | .ifStmt .., _, _, h | .ret _, _, _, h | .assign .., _, _, h | .incdec .., _, _, h
  | .decl .., _, _, h | .interp _, _, _, h | .lambda .., _, _, h | .named .., _, _, h
  | .unsupported _, _, _, h => by simp [lowerE] at h

theorem lowerLeaf_simple {K : Nat} {κ : String → Option Kind} {e : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerLeaf K κ e = some (k, le)) : rhsSimple le = true := by
  simp only [lowerLeaf, lowerX] at h
  split at h
  · split at h
    · exact lowerE_simple κ e k le h
    · cases h
  · cases h

theorem lowerC_simple {K : Nat} {sg : Sigs} {κ : String → Option Kind} :
    ∀ (e : Spec.Expr) (k : Kind) (le : Expr), lowerC K sg κ e = some (k, le) → rhsSimple le = true
  | .paren x, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.map_eq_some_iff] at h
      obtain ⟨_, -, h⟩ := h
      cases h; rfl
    · exact lowerLeaf_simple h
  | .unary op x, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq] at h
      obtain ⟨_, -, _, -, h⟩ := h
      cases h; rfl
    · exact lowerLeaf_simple h
  | .binary op l r, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.bind_eq_bind, Option.bind_eq_some_iff] at h
      obtain ⟨_, -, _, -, _, -, h⟩ := h
      split at h
      · cases h
      · simp only [Option.pure_def, Option.some.injEq] at h; cases h; rfl
    · exact lowerLeaf_simple h
  | .call (.name f) args, k, le, h => by
    obtain ⟨-, _, _, -, -, rfl⟩ := lowerC_call K sg h
    rfl
  | .ifExpr c a b, k, le, h => by
    simp only [lowerC, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨_, -, h⟩ := h
    split at h
    · cases h
    · simp only [Option.pure_def, Option.some.injEq] at h; cases h; rfl
  | .lit l, k, le, h | .name x, k, le, h => by
    simp only [lowerC] at h
    exact lowerLeaf_simple h
  | .call (.lit _) _, _, _, h | .call (.unary ..) _, _, _, h | .call (.binary ..) _, _, _, h
  | .call (.call ..) _, _, _, h | .call (.select ..) _, _, _, h | .call (.paren _) _, _, _, h
  | .call (.tuple _) _, _, _, h | .call (.ifExpr ..) _, _, _, h | .call (.mtch ..) _, _, _, h
  | .call (.block _) _, _, _, h | .call (.ifStmt ..) _, _, _, h | .call (.ret _) _, _, _, h
  | .call (.assign ..) _, _, _, h | .call (.incdec ..) _, _, _, h | .call (.decl ..) _, _, _, h
  | .call (.interp _) _, _, _, h | .call (.lambda ..) _, _, _, h | .call (.named ..) _, _, _, h
  | .call (.unsupported _) _, _, _, h
  | .select .., _, _, h | .tuple _, _, _, h | .mtch .., _, _, h
  | .block _, _, _, h | .ifStmt .., _, _, h | .ret _, _, _, h | .assign .., _, _, h | .incdec .., _, _, h
  | .decl .., _, _, h | .interp _, _, _, h | .lambda .., _, _, h | .named .., _, _, h
  | .unsupported _, _, _, h => by simp [lowerC] at h

end Shapes
section Tmps
open Gala.GoCore.Full

theorem isTmp_tmpName (i : Nat) : isTmp (tmpName i) = true := by
  simp [isTmp, tmpName, String.toList_append]

theorem tmpName_inj {i j : Nat} (h : tmpName i = tmpName j) : i = j := by
  unfold tmpName at h
  have h2 := congrArg String.toList h
  simp only [String.toList_append, List.append_cancel_left_eq] at h2
  have h3 : toString (i + 1) = toString (j + 1) := String.toList_inj.mp h2
  have := Nat.repr_injective h3
  omega

theorem tmpName_ne_us (i : Nat) : tmpName i ≠ "_" := by
  intro h; have := isTmp_tmpName i; rw [h] at this; cases this

theorem tmpName_ne_true (i : Nat) : tmpName i ≠ "true" := by
  intro h; have := isTmp_tmpName i; rw [h] at this; cases this

theorem tmpName_ne_false (i : Nat) : tmpName i ≠ "false" := by
  intro h; have := isTmp_tmpName i; rw [h] at this; cases this

/-- After `_tmp_ := e` with `e`'s value `w`: the state relation, the temporary a GoCore local
    galaspec does not have. -/
theorem inv3_tmp {cx : Ctx} {P : String} {sg : Sigs} {κ : String → Option Kind} {c : Spec.Core} {g : St}
    {env : Env} (hi : Inv3 cx P sg κ c g env) (hnt : ∀ x, (κ x).isSome → isTmp x = false) (i : Nat) (w : Val) :
    Inv3 cx P sg κ c (declG w g) (env.bind (tmpName i) g.cells.size) := by
  have hy := tmpName_ne_us i
  refine ⟨⟨?_, hi.inv.out, predeclared_bind hi.inv.pre _ _ hy (tmpName_ne_true i) (tmpName_ne_false i)⟩,
    (env_bind_parts env _ _).1.trans hi.pkg, (env_bind_parts env _ _).2.trans hi.tenv, ?_, hi.globals,
    by simp [declG, hi.sync]⟩
  · intro x k hk
    have hxt : x ≠ tmpName i := by
      intro he; have := hnt x (by rw [hk]; rfl); rw [he, isTmp_tmpName] at this; cases this
    obtain ⟨b, cell, v, w', hb, hv, hcell, hw', rest⟩ := hi.inv.loc x k hk
    exact ⟨b, cell, v, w', hb, hv, by rw [Env.lookup_bind _ _ _ _ hy]; simp only [hxt, ite_false]; exact hcell,
      pre_get (pre_push _ _) hw', rest⟩
  · intro x hx
    obtain ⟨h1, h2⟩ := hi.closed x hx
    refine ⟨h1, fun ht => ?_⟩
    have hxt : x ≠ tmpName i := by intro he; rw [he, isTmp_tmpName] at ht; cases ht
    rw [Env.lookup_bind _ _ _ _ hy]; simp only [hxt, ite_false]; exact h2 ht

/-- The innermost scope has no temporary from `_tmp_{i+1}` on. -/
def TmpsFree (env : Env) (i : Nat) : Prop := ∀ m, (env.scopes.headD []).lookup (tmpName (i + m)) = none

theorem tmpsFree_push (env : Env) (i : Nat) : TmpsFree env.push i := fun _ => rfl

theorem tmpsFree_bind {env : Env} {i : Nat} (h : TmpsFree env i) (c : Nat) :
    TmpsFree (env.bind (tmpName i) c) (i + 1) := by
  intro m
  have hne : (tmpName (i + 1 + m) == tmpName i) = false := by
    simp only [beq_eq_false_iff_ne]; intro he; have := tmpName_inj he; omega
  have := h (m + 1)
  rw [show i + (m + 1) = i + 1 + m by omega] at this
  unfold Env.bind
  rw [show (tmpName i == "_") = false by simpa using tmpName_ne_us i]
  simp only [Bool.false_eq_true, ite_false]
  split
  · rename_i s rest hs
    simp only [hs, List.headD] at this
    simp only [List.headD, List.lookup, hne]
    exact this
  · simp only [List.headD, List.lookup, hne]

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- **The temporaries' results agree** with galaspec's arguments: GoCore goes on with the
    statements after them, its temporaries from `_tmp_{i+1}` on holding the arguments'
    values; or the same panic. -/
def TmpRes (κ : String → Option Kind) (i : Nat) (ks : List Kind) (s : Spec.St) (g : St) (envT : Env)
    (rest : List Stmt) (M : Nat) : Except Spec.Stop (List Spec.Val) × Spec.St → Except Stop Env × St → Prop
  | (.ok vs, t), r => ∃ ws g' envT', r = (execStmts cx M envT' rest).run.run g' ∧ KRel ks vs ws ∧
      SFrame s.toCore t.toCore ∧ GFrame g g' ∧ Spent s t g g' ∧ Inv3 cx P sg κ t.toCore g' envT' ∧
      (∀ x, (∀ m, x ≠ tmpName (i + m)) → envT'.lookup x = envT.lookup x) ∧
      (∀ m (hm : m < ws.length), ∃ c, envT'.lookup (tmpName (i + m)) = some c ∧ g'.cells[c]? = some (defaultVal ws[m]))
  | (.error (.panic pc m), t), r => PanOK s g pc m t r
  | (.error (.ret _), _), _ => False
  | _, _ => True

/-- **The temporaries of a loop step** (`_tmp_{i+1} := a_1; ..`), against galaspec's
    arguments, left to right, one level down. -/
theorem tmps_sim (d : Nat) (ihE : ExprSim ds cx P K sg d) (κ : String → Option Kind)
    (hnt : ∀ x, (κ x).isSome → isTmp x = false) :
    ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr) (i : Nat), lowerArgs K sg κ ks args = some las →
    ∀ (s : Spec.St) (g : St) (envT : Env), Inv3 cx P sg κ s.toCore g envT → s.steps ≤ g.steps → TmpsFree envT i →
    ∀ M, K * d + 5 ≤ M → ∀ rest, TmpRes cx P sg κ i ks s g envT rest M ((Spec.evalList ds d args).run.run s)
      ((execStmts cx M envT (tmpDecls i las ++ rest)).run.run g)
  | [], [], las, i, h, s, g, envT, hi, hsg, _, M, _, rest => by
    simp only [lowerArgs, Option.some.injEq] at h
    subst h
    rw [evalList_nil_run]
    refine ⟨[], g, envT, rfl, trivial, sframe_refl _, gframe_refl _, by unfold Spent; omega, hi, fun _ _ => rfl,
      fun m hm => absurd hm (by simp)⟩
  | k :: ks, a :: as, las, i, h, s, g, envT, hi, hsg, hfree, M, hM, rest => by
    simp only [lowerArgs] at h
    split at h
    · cases h
    · split at h
      · rename_i k' la hla
        split at h
        · rename_i hk
          subst hk
          simp only [Option.map_eq_some_iff] at h
          obtain ⟨las', hlas', rfl⟩ := h
          obtain ⟨m, rfl⟩ : ∃ m, M = m + 5 := ⟨M - 5, by omega⟩
          rw [evalList_cons_run]
          simp only [tmpDecls, List.cons_append]
          rw [execStmts_cons]
          have hE := ihE κ a k' la hla s g envT hi hsg (m + 1) (by omega)
          have hsimp := lowerC_simple a k' la hla
          rcases hr : (Spec.eval ds d a).run.run s with ⟨ra, t1⟩
          rw [hr] at hE
          cases ra with
          | error e =>
            cases e with
            | panic pc msg =>
              obtain ⟨pv, g1, hg, hp, ho, hgf⟩ := hE
              rw [execStmt_define_err cx m envT _ la g g1 _ (rhsSimple_ta hsimp) (rhsSimple_ix hsimp) hg]
              exact ⟨pv, g1, rfl, hp, ho, hgf⟩
            | ret => exact hE.elim
            | _ => trivial
          | ok v =>
          obtain ⟨w, g1, hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
          have hcur : (envT.scopes.headD []).lookup (tmpName i) = none := by simpa using hfree 0
          rw [execStmt_define cx m envT _ la g g1 w (tmpName_ne_us i) hcur (rhsSimple_ta hsimp) (rhsSimple_ix hsimp)
            hg (results_of_vrel hvw)]
          simp only
          have hi1 : Inv3 cx P sg κ t1.toCore (declG w g1) (envT.bind (tmpName i) g1.cells.size) :=
            inv3_tmp (inv3_frame hi hsf hgf) hnt i w
          have hgf1 : GFrame g (declG w g1) := gframe_trans hgf (gframe_push _ _ _)
          have hsp1 : Spent s t1 g (declG w g1) := by unfold Spent at hsp ⊢; simpa [declG] using hsp
          have ih := tmps_sim d ihE κ hnt ks as las' (i + 1) hlas' t1 (declG w g1) _ hi1
            (spent_le hsp1 hsg) (tmpsFree_bind hfree _) (m + 5) hM rest
          rcases hrs : (Spec.evalList ds d as).run.run t1 with ⟨rs, t⟩
          rw [hrs] at ih
          cases rs with
          | error e =>
            cases e with
            | panic pc msg =>
              obtain ⟨pv, g', hg', hp, ho, hgf'⟩ := ih
              exact ⟨pv, g', hg', hp, by rw [ho, (sframe_parts hsf).2.2], gframe_trans hgf1 hgf'⟩
            | ret => exact ih.elim
            | _ => trivial
          | ok vs =>
          obtain ⟨ws, g', envT', hg', hkr, hsf', hgf', hsp', hi', hlk, htm⟩ := ih
          have hne : ∀ m', tmpName i ≠ tmpName (i + 1 + m') := by
            intro m' he; have := tmpName_inj he; omega
          refine ⟨w :: ws, g', envT', hg', ⟨hvw, hkv, hkr⟩, sframe_trans hsf hsf', gframe_trans hgf1 hgf',
            spent_trans hsp1 hsp', hi', fun x hx => ?_, fun m' hm' => ?_⟩
          · have hx0 : x ≠ tmpName i := by simpa using hx 0
            rw [hlk x (fun m' => by simpa [Nat.add_assoc, Nat.add_comm 1] using hx (m' + 1)),
              Env.lookup_bind _ _ _ _ (tmpName_ne_us i)]
            simp [hx0]
          · cases m' with
            | zero =>
              refine ⟨g1.cells.size, ?_, ?_⟩
              · rw [Nat.add_zero, hlk (tmpName i) hne, Env.lookup_bind _ _ _ _ (tmpName_ne_us i)]; simp
              · exact pre_get hgf'.1 (by simp [declG])
            | succ m'' =>
              obtain ⟨c, hc, hcv⟩ := htm m'' (by simp at hm'; omega)
              refine ⟨c, by simpa [Nat.add_assoc, Nat.add_comm 1] using hc, by simpa using hcv⟩
        · cases h
      · cases h
  | [], _ :: _, _, _, h, _, _, _, _, _, _, _, _, _ | _ :: _, [], _, _, h, _, _, _, _, _, _, _, _, _ => by
    simp [lowerArgs] at h

end Tmps

section Stores
open Gala.GoCore.Full

theorem storeAll_rest : ∀ (g : St) (A : List Asg),
    { storeAll g A with cells := g.cells } = g ∧ (storeAll g A).cells.size = g.cells.size
  | g, [] => ⟨by cases g; rfl, rfl⟩
  | g, a :: A => by
    obtain ⟨h1, h2⟩ := storeAll_rest { g with cells := g.cells.modify a.cell (·.setAt [] a.v) } A
    refine ⟨?_, by rw [storeAll, h2]; simp⟩
    rw [storeAll]
    have e := congrArg (fun (x : St) => { x with cells := g.cells }) h1
    simp only at e
    rw [e]

theorem storeAll_other : ∀ (g : St) (A : List Asg) (c : Nat), (∀ a ∈ A, a.cell ≠ c) →
    (storeAll g A).cells[c]? = g.cells[c]?
  | g, [], _, _ => rfl
  | g, a :: A, c, h => by
    rw [storeAll, storeAll_other _ A c (fun b hb => h b (by simp [hb]))]
    simp only [Array.getElem?_modify, h a (by simp), ite_false]

theorem storeAll_get : ∀ (g : St) (A : List Asg), A.Pairwise (fun a b => a.cell ≠ b.cell) →
    ∀ a ∈ A, a.cell < g.cells.size → (storeAll g A).cells[a.cell]? = some a.v
  | g, [], _, a, ha, _ => by simp at ha
  | g, b :: A, hp, a, ha, hs => by
    rw [List.pairwise_cons] at hp
    rw [storeAll]
    rcases List.mem_cons.mp ha with rfl | ha'
    · rw [storeAll_other _ A a.cell (fun c hc => (hp.1 c hc).symm)]
      simp only [Array.getElem?_modify, ite_true, Array.getElem?_eq_getElem hs, Option.map_some]
      cases a.v <;> rfl
    · exact storeAll_get _ A hp.2 a ha' (by simpa using hs)

theorem gframeP_storeAll {n : Nat} : ∀ (g : St) (A : List Asg), (∀ a ∈ A, n ≤ a.cell) → GFrameP n g (storeAll g A)
  | g, [], _ => gframeP_of (gframe_refl g)
  | g, a :: A, h => by
    rw [storeAll]
    refine gframeP_trans ?_ (gframeP_storeAll _ A (fun b hb => h b (by simp [hb])))
    have hc := h a (by simp)
    refine ⟨by simp, fun i hn hi => ?_, pre_refl _, pre_refl _, by simp, Nat.le_refl _, by cases g; rfl⟩
    simp only [Array.getElem?_modify]
    split
    · omega
    · rfl

/-- The assignments of a step: parameter `x_i` from `_tmp_{i+1}`, which holds `w_i`'s default
    value, converted to `x_i`'s type. -/
def mkAsg (env : Env) : Nat → List (String × Kind) → List Val → List Asg
  | i, (x, k) :: xks, w :: ws =>
    ⟨x, (env.lookup x).getD 0, kindTy k, tmpName i, defaultVal w, defaultVal (defaultVal w)⟩ :: mkAsg env (i + 1) xks ws
  | _, _, _ => []

theorem mkAsg_x (env : Env) : ∀ (i : Nat) (xks : List (String × Kind)) (ws : List Val), xks.length = ws.length →
    (mkAsg env i xks ws).map (fun (a : Asg) => Expr.ident a.x) = xks.map (fun xk => Expr.ident xk.1)
  | _, [], [], _ => rfl
  | i, (x, k) :: xks, w :: ws, h => by
    simp only [mkAsg, List.map_cons, mkAsg_x env (i + 1) xks ws (by simpa using h)]
  | _, [], _ :: _, h | _, _ :: _, [], h => by simp at h

theorem mkAsg_t (env : Env) : ∀ (i : Nat) (xks : List (String × Kind)) (ws : List Val), xks.length = ws.length →
    (mkAsg env i xks ws).map (fun (a : Asg) => Expr.ident a.t) = tmpIdents i xks.length
  | _, [], [], _ => rfl
  | i, (x, k) :: xks, w :: ws, h => by
    simp only [mkAsg, List.map_cons, List.length_cons, tmpIdents, mkAsg_t env (i + 1) xks ws (by simpa using h)]
  | _, [], _ :: _, h | _, _ :: _, [], h => by simp at h

theorem mem_mkAsg (env : Env) : ∀ (i : Nat) (xks : List (String × Kind)) (ws : List Val) (a : Asg),
    a ∈ mkAsg env i xks ws → ∃ m x k w, xks[m]? = some (x, k) ∧ ws[m]? = some w ∧
      a = ⟨x, (env.lookup x).getD 0, kindTy k, tmpName (i + m), defaultVal w, defaultVal (defaultVal w)⟩
  | _, [], _, _, h => by simp [mkAsg] at h
  | _, _ :: _, [], _, h => by simp [mkAsg] at h
  | i, (x, k) :: xks, w :: ws, a, h => by
    simp only [mkAsg, List.mem_cons] at h
    rcases h with rfl | h
    · exact ⟨0, x, k, w, rfl, rfl, rfl⟩
    · obtain ⟨m, x', k', w', h1, h2, rfl⟩ := mem_mkAsg env (i + 1) xks ws a h
      exact ⟨m + 1, x', k', w', by simpa using h1, by simpa using h2, by simp [Nat.add_assoc, Nat.add_comm 1]⟩

end Stores

section LoopRel
open Gala.GoCore.Full

/-! ### Lists of parameters -/

theorem foldl_upd_some : ∀ (xks : List (String × Kind)) (κ : String → Option Kind) (x : String) (k : Kind),
    xks.foldl (fun κ xk => upd κ xk.1 xk.2) κ x = some k → (∃ m : Nat, xks[m]? = some (x, k)) ∨ κ x = some k
  | [], _, _, _, h => Or.inr h
  | (y, ky) :: xks, κ, x, k, h => by
    rcases foldl_upd_some xks (upd κ y ky) x k h with ⟨m, hm⟩ | h'
    · exact Or.inl ⟨m + 1, by simpa using hm⟩
    · unfold upd at h'
      by_cases hxy : x = y
      · subst hxy; simp at h'; subst h'; exact Or.inl ⟨0, rfl⟩
      · simp only [hxy, ite_false] at h'; exact Or.inr h'

theorem paramKinds_some {xks : List (String × Kind)} {x : String} {k : Kind} (h : paramKinds xks x = some k) :
    ∃ m : Nat, xks[m]? = some (x, k) := by
  rcases foldl_upd_some xks _ x k h with h' | h'
  · exact h'
  · cases h'

theorem foldl_upd_notin : ∀ (xks : List (String × Kind)) (κ : String → Option Kind) (x : String),
    x ∉ xks.map (·.1) → xks.foldl (fun κ xk => upd κ xk.1 xk.2) κ x = κ x
  | [], _, _, _ => rfl
  | (y, ky) :: xks, κ, x, h => by
    simp only [List.map_cons, List.mem_cons, not_or] at h
    rw [List.foldl_cons, foldl_upd_notin xks _ x h.2]
    simp [upd, h.1]

theorem paramKinds_get : ∀ (xks : List (String × Kind)) (κ : String → Option Kind), (xks.map (·.1)).Nodup →
    ∀ (m : Nat) (x : String) (k : Kind), xks[m]? = some (x, k) → xks.foldl (fun κ xk => upd κ xk.1 xk.2) κ x = some k
  | [], _, _, m, _, _, h => by simp at h
  | (y, ky) :: xks, κ, hn, m, x, k, h => by
    simp only [List.map_cons, List.nodup_cons] at hn
    rw [List.foldl_cons]
    cases m with
    | zero =>
      simp at h; obtain ⟨rfl, rfl⟩ := h
      rw [foldl_upd_notin xks _ _ hn.1]; simp [upd]
    | succ m => exact paramKinds_get xks _ hn.2 m x k (by simpa using h)

theorem krel_get : ∀ {ks : List Kind} {vs : List Spec.Val} {ws : List Val}, KRel ks vs ws →
    ∀ (m : Nat) (k : Kind), ks[m]? = some k → ∃ v w, vs[m]? = some v ∧ ws[m]? = some w ∧ VRel v w ∧ valKind v = some k
  | [], [], [], _, _, _, h => by simp at h
  | k' :: ks, v :: vs, w :: ws, ⟨hv, hk, h⟩, m, k, hm => by
    cases m with
    | zero => simp at hm; subst hm; exact ⟨v, w, rfl, rfl, hv, hk⟩
    | succ m => exact krel_get h m k (by simpa using hm)
  | [], _ :: _, _, h, _, _, _ | [], [], _ :: _, h, _, _, _ | _ :: _, [], _, h, _, _, _
  | _ :: _, _ :: _, [], h, _, _, _ => by simp [KRel] at h

theorem krel_len : ∀ {ks : List Kind} {vs : List Spec.Val} {ws : List Val}, KRel ks vs ws →
    vs.length = ks.length ∧ ws.length = ks.length
  | [], [], [], _ => ⟨rfl, rfl⟩
  | _ :: _, _ :: _, _ :: _, ⟨_, _, h⟩ => by simp [krel_len h]
  | [], _ :: _, _, h | [], [], _ :: _, h | _ :: _, [], _, h | _ :: _, _ :: _, [], h => by simp [KRel] at h

/-! ### galaspec's frame of the parameters -/

theorem frame_notin : ∀ (ps : List Spec.Param) (vs : List Spec.Val) (ρ0 : Spec.Env) (x : String),
    x ∉ ps.map (·.name) →
    Spec.lookup ((ps.zip vs).foldl (fun ρ pv => Spec.bind ρ pv.1.name ⟨pv.2, false⟩) ρ0) x = Spec.lookup ρ0 x
  | [], _, _, _, _ => by simp
  | _ :: _, [], _, _, _ => by simp
  | p :: ps, v :: vs, ρ0, x, h => by
    simp only [List.map_cons, List.mem_cons, not_or] at h
    rw [List.zip_cons_cons, List.foldl_cons, frame_notin ps vs _ x h.2]
    unfold Spec.bind
    split
    · rfl
    · simp [Spec.lookup, Ne.symm h.1]

theorem frame_get : ∀ (ps : List Spec.Param) (vs : List Spec.Val) (ρ0 : Spec.Env), (ps.map (·.name)).Nodup →
    (∀ p ∈ ps, p.name ≠ "_") → ∀ (m : Nat) (p : Spec.Param) (v : Spec.Val), ps[m]? = some p → vs[m]? = some v →
    Spec.lookup ((ps.zip vs).foldl (fun ρ pv => Spec.bind ρ pv.1.name ⟨pv.2, false⟩) ρ0) p.name = some ⟨v, false⟩
  | [], _, _, _, _, m, _, _, h, _ => by simp at h
  | _ :: _, [], _, _, _, m, _, _, _, h => by simp at h
  | q :: ps, w :: vs, ρ0, hn, hu, m, p, v, hp, hv => by
    simp only [List.map_cons, List.nodup_cons] at hn
    rw [List.zip_cons_cons, List.foldl_cons]
    cases m with
    | zero =>
      simp at hp hv; subst hp; subst hv
      rw [frame_notin ps vs _ _ hn.1]
      unfold Spec.bind
      simp [hu _ (by simp : q ∈ q :: ps), Spec.lookup]
    | succ m =>
      exact frame_get ps vs _ hn.2 (fun p' hp' => hu p' (by simp [hp'])) m p v (by simpa using hp) (by simpa using hv)

/-! ### Results -/

theorem resBP_tick {s₁ s : Spec.St} (ht : Ticked s₁ s) {n : Nat} {ρ : Kind} {g : St}
    {sr : Except Spec.Stop Spec.Val × Spec.St} {X : Except Stop α × St} (h : ResBP n ρ s₁ g sr X) : ResBP n ρ s g sr X := by
  have hc := ht.core
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic =>
      obtain ⟨pv, g', hr, hp, ho, hg⟩ := h
      exact ⟨pv, g', hr, hp, by rw [ho, show s₁.out = s.out from congrArg Spec.Core.out hc], hg⟩
    | ret =>
      obtain ⟨w, g', hr, hv, hk, hh, hgl, ho, hg, hsp⟩ := h
      refine ⟨w, g', hr, hv, hk, ?_, ?_, ?_, hg, ?_⟩
      · rw [← show s₁.heap = s.heap from congrArg Spec.Core.heap hc]; exact hh
      · rw [hgl, show s₁.globals = s.globals from congrArg Spec.Core.globals hc]
      · rw [ho, show s₁.out = s.out from congrArg Spec.Core.out hc]
      · unfold Spent at hsp ⊢; have := ht.le; omega
    | _ => trivial
  · obtain ⟨w, g', hr, hv, hk, hh, hgl, ho, hg, hsp⟩ := h
    refine ⟨w, g', hr, hv, hk, ?_, ?_, ?_, hg, ?_⟩
    · rw [← show s₁.heap = s.heap from congrArg Spec.Core.heap hc]; exact hh
    · rw [hgl, show s₁.globals = s.globals from congrArg Spec.Core.globals hc]
    · rw [ho, show s₁.out = s.out from congrArg Spec.Core.out hc]
    · unfold Spent at hsp ⊢; have := ht.le; omega

theorem resBP_after {n : Nat} {ρ : Kind} {s t : Spec.St} {g g₁ : St} (hh : Pre s.heap t.heap)
    (hgl : t.globals = s.globals) (ho : t.out = s.out) (hg : GFrameP n g g₁) (hsp : Spent s t g g₁)
    {sr : Except Spec.Stop Spec.Val × Spec.St} {X : Except Stop α × St} (h : ResBP n ρ t g₁ sr X) : ResBP n ρ s g sr X := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic =>
      obtain ⟨pv, g', hr, hp, ho', hg'⟩ := h
      exact ⟨pv, g', hr, hp, ho'.trans ho, gframeP_trans hg hg'⟩
    | ret =>
      obtain ⟨w, g', hr, hv, hk, hh', hgl', ho', hg', hsp'⟩ := h
      exact ⟨w, g', hr, hv, hk, pre_trans hh hh', hgl'.trans hgl, ho'.trans ho, gframeP_trans hg hg', spent_trans hsp hsp'⟩
    | _ => trivial
  · obtain ⟨w, g', hr, hv, hk, hh', hgl', ho', hg', hsp'⟩ := h
    exact ⟨w, g', hr, hv, hk, pre_trans hh hh', hgl'.trans hgl, ho'.trans ho, gframeP_trans hg hg', spent_trans hsp hsp'⟩

theorem resBP_of_resB {n : Nat} {ρ : Kind} {s : Spec.St} {g : St} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {X : Except Stop Env × St} (h : ResB ρ s g sr X) : ResBP n ρ s g sr X := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => obtain ⟨pv, g', hr, hp, ho, hg⟩ := h; exact ⟨pv, g', hr, hp, ho, gframeP_of hg⟩
    | ret => obtain ⟨w, g', hr, hv, hk, hh, hgl, ho, hg, hsp⟩ := h; exact ⟨w, g', hr, hv, hk, hh, hgl, ho, gframeP_of hg, hsp⟩
    | _ => trivial
  · obtain ⟨w, g', hr, hv, hk, hh, hgl, ho, hg, hsp⟩ := h; exact ⟨w, g', hr, hv, hk, hh, hgl, ho, gframeP_of hg, hsp⟩

/-- A `ResBP` passes from one GoCore result to another that is the same where it is an
    error. -/
theorem resBP_err {n : Nat} {ρ : Kind} {s : Spec.St} {g : St} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {X : Except Stop α × St} {Y : Except Stop β × St} (hY : ∀ e g', X = (.error e, g') → Y = (.error e, g'))
    (h : ResBP n ρ s g sr X) : ResBP n ρ s g sr Y := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => obtain ⟨pv, g', hr, rest⟩ := h; exact ⟨pv, g', hY _ _ hr, rest⟩
    | ret => obtain ⟨w, g', hr, rest⟩ := h; exact ⟨w, g', hY _ _ hr, rest⟩
    | _ => trivial
  · obtain ⟨w, g', hr, rest⟩ := h; exact ⟨w, g', hY _ _ hr, rest⟩

end LoopRel

section LoopCases
open Gala.GoCore.Full

variable {K : Nat} {sg : Sigs} {f : String} {xks : List (String × Kind)} {ρ : Kind} {κ : String → Option Kind}

theorem lowerLoop_leaf {e : Spec.Expr} (hne : ∀ c a b, e ≠ .ifExpr c a b) :
    lowerLoop K sg f xks ρ κ e = match selfArgs f xks.length e with
      | some args =>
        if args.all noIf then (lowerArgs K sg κ (xks.map (·.2)) args).map (stepOf (xks.map (·.1))) else none
      | none =>
        if isCompound e || parenHides e then none
        else match lowerC K sg κ e with
          | some (k, le) => if k = ρ then some [.ret [le]] else none
          | none => none := by
  cases e <;> first | exact absurd rfl (hne _ _ _) | rfl

theorem selfArgs_some {n : Nat} {e : Spec.Expr} {args : List Spec.Expr} (h : selfArgs f n e = some args) :
    e = .call (.name f) args ∧ args.length = n := by
  unfold selfArgs at h
  split at h
  · split at h
    · rename_i g args' hc
      simp only [Option.some.injEq] at h
      subst h
      exact ⟨by rw [hc.1], hc.2⟩
    · cases h
  · cases h

/-- What `lowerLoop` lowers: an `if`, a loop step, or `return` of a value. -/
theorem lowerLoop_cases {e : Spec.Expr} {ls : List Stmt} (h : lowerLoop K sg f xks ρ κ e = some ls) :
    (∃ c a b lc ta tb, e = .ifExpr c a b ∧ lowerC K sg κ c = some (.bool, lc) ∧
      lowerLoop K sg f xks ρ κ a = some ta ∧ lowerLoop K sg f xks ρ κ b = some tb ∧ ls = [ifT lc ta tb]) ∨
    (∃ args las, e = .call (.name f) args ∧ args.length = xks.length ∧
      lowerArgs K sg κ (xks.map (·.2)) args = some las ∧ ls = stepOf (xks.map (·.1)) las) ∨
    (∃ le, lowerC K sg κ e = some (ρ, le) ∧ ls = [.ret [le]]) := by
  by_cases hif : ∃ c a b, e = .ifExpr c a b
  · obtain ⟨c, a, b, rfl⟩ := hif
    simp only [lowerLoop] at h
    split at h
    · rename_i lc ta tb hc ha hb
      simp only [Option.some.injEq] at h
      exact Or.inl ⟨c, a, b, lc, ta, tb, rfl, hc, ha, hb, h.symm⟩
    · cases h
  · rw [lowerLoop_leaf (fun c a b he => hif ⟨c, a, b, he⟩)] at h
    split at h
    · rename_i args hs
      split at h
      · simp only [Option.map_eq_some_iff] at h
        obtain ⟨las, hlas, rfl⟩ := h
        obtain ⟨rfl, hl⟩ := selfArgs_some hs
        exact Or.inr (Or.inl ⟨args, las, rfl, hl, hlas, rfl⟩)
      · cases h
    · split at h
      · cases h
      · split at h
        · rename_i k le hle
          split at h
          · rename_i hk; subst hk
            simp only [Option.some.injEq] at h
            exact Or.inr (Or.inr ⟨le, hle, h.symm⟩)
          · cases h
        · cases h

end LoopCases

section LoopNext
open Gala.GoCore.Full

theorem mkAsg_get (env : Env) : ∀ (i : Nat) (xks : List (String × Kind)) (ws : List Val) (m : Nat) (x : String)
    (k : Kind) (w : Val), xks[m]? = some (x, k) → ws[m]? = some w →
    (mkAsg env i xks ws)[m]? = some ⟨x, (env.lookup x).getD 0, kindTy k, tmpName (i + m), defaultVal w,
      defaultVal (defaultVal w)⟩
  | _, [], _, _, _, _, _, h, _ => by simp at h
  | _, _ :: _, [], _, _, _, _, _, h => by simp at h
  | i, (y, ky) :: xks, w' :: ws, m, x, k, w, hx, hw => by
    cases m with
    | zero => simp at hx hw; obtain ⟨rfl, rfl⟩ := hx; subst hw; rfl
    | succ m =>
      simp only [mkAsg, List.getElem?_cons_succ]
      rw [mkAsg_get env (i + 1) xks ws m x k w (by simpa using hx) (by simpa using hw)]
      simp [Nat.add_assoc, Nat.add_comm 1]

theorem mkAsg_pairwise (env : Env) : ∀ (i : Nat) (xks : List (String × Kind)) (ws : List Val),
    (xks.map (·.1)).Nodup → (∀ x ∈ xks.map (·.1), (env.lookup x).isSome) →
    (∀ x y c, x ∈ xks.map (·.1) → y ∈ xks.map (·.1) → env.lookup x = some c → env.lookup y = some c → x = y) →
    (mkAsg env i xks ws).Pairwise (fun a b => a.cell ≠ b.cell)
  | _, [], _, _, _, _ => by simp [mkAsg]
  | _, _ :: _, [], _, _, _ => by simp [mkAsg]
  | i, (x, k) :: xks, w :: ws, hn, hs, hinj => by
    simp only [List.map_cons, List.nodup_cons] at hn
    simp only [mkAsg, List.pairwise_cons]
    refine ⟨fun b hb => ?_, mkAsg_pairwise env (i + 1) xks ws hn.2 (fun y hy => hs y (by simp [hy]))
      (fun y z c hy hz => hinj y z c (by simp [hy]) (by simp [hz]))⟩
    obtain ⟨m, y, ky, wy, hy, -, rfl⟩ := mem_mkAsg env (i + 1) xks ws b hb
    have hym : y ∈ xks.map (·.1) := List.mem_map.mpr ⟨(y, ky), List.mem_of_getElem? hy, rfl⟩
    intro he
    simp only at he
    obtain ⟨cx', hcx⟩ := Option.isSome_iff_exists.mp (hs x (by simp))
    obtain ⟨cy, hcy⟩ := Option.isSome_iff_exists.mp (hs y (by simp [hym]))
    rw [hcx, hcy] at he
    simp only [Option.getD_some] at he
    subst he
    exact hn.1 (hinj x y cx' (by simp) (by simp [hym]) hcx hcy ▸ hym)

theorem vrel_ne_poison {v : Spec.Val} {w : Val} (h : VRel v w) : ∀ what, w ≠ .poison what := by
  intro what he; subst he; cases h

theorem defaultVal_vrel2 {v : Spec.Val} {w : Val} (h : VRel v w) :
    VRel v (defaultVal (defaultVal w)) ∧ ¬ UInt (defaultVal (defaultVal w)) ∧
      (∀ t b, defaultVal (defaultVal w) ≠ .bool (.untyped t) b) ∧ (∀ t s, defaultVal (defaultVal w) ≠ .str (.untyped t) s) :=
  vrel_default (vrel_default h).1

/-- **The state relation at the next iteration**: galaspec's new frame of the parameters,
    GoCore's parameters' cells holding the temporaries' values. -/
theorem inv3_next {cx : Ctx} {P : String} {sg : Sigs} {xks : List (String × Kind)} {F : Spec.Func} {ρ : Kind}
    {env0 : Env} {n : Nat} (hsig : sigOf F = some (xks, ρ)) (hnames : ParamsOK (xks.map (·.1)))
    (hpre : Predeclared cx env0) (hpkg : env0.pkg = P) (htenv : env0.tenv = [])
    (hcl : ∀ x, paramKinds xks x = none → isTmp x = false → env0.lookup x = none)
    {t : Spec.St} {g' : St} {vs : List Spec.Val} {ws : List Val} (hkr : KRel (xks.map (·.2)) vs ws)
    (hpc : ParamCells n xks env0 g') (hsz : ∀ x k c, paramKinds xks x = some k → env0.lookup x = some c → c < g'.cells.size)
    (hglob : ∀ f, (sg f).isSome → Spec.lookup t.globals f = none) (hout : t.out = g'.out)
    (hsync : g'.cells.size = g'.types.size) :
    Inv3 cx P sg (paramKinds xks) { t with locals := specFrame F.params vs }.toCore
      (storeAll g' (mkAsg env0 0 xks ws)) env0 := by
  obtain ⟨hpn, -, -⟩ := sigOf_facts hsig
  obtain ⟨hlv, hlw⟩ := krel_len hkr
  have hrest := storeAll_rest g' (mkAsg env0 0 xks ws)
  have hnd : (F.params.map (·.name)).Nodup := by rw [hpn]; exact hnames.1
  have hpu : ∀ p ∈ F.params, p.name ≠ "_" := by
    intro p hp; exact (hnames.2 p.name (by rw [← hpn]; exact List.mem_map_of_mem hp)).1
  have hmem : ∀ x ∈ xks.map (·.1), ∃ k, paramKinds xks x = some k := by
    intro x hx
    obtain ⟨⟨x', k⟩, hxk, rfl⟩ := List.mem_map.mp hx
    obtain ⟨m, hm⟩ := List.getElem?_of_mem hxk
    exact ⟨k, paramKinds_get xks _ hnames.1 m x' k hm⟩
  refine ⟨⟨?_, ?_, hpre⟩, hpkg, htenv, ?_, hglob, ?_⟩
  · intro x k hk
    obtain ⟨m, hm⟩ := paramKinds_some hk
    have hmk : (xks.map (·.2))[m]? = some k := by simp [hm]
    obtain ⟨v, w, hv, hw, hvw, hkv⟩ := krel_get hkr m k hmk
    have hpm : (F.params.map (·.name))[m]? = some x := by rw [hpn]; simp [hm]
    simp only [List.getElem?_map] at hpm
    obtain ⟨p, hp, hpx⟩ := Option.map_eq_some_iff.mp hpm
    obtain ⟨c, hc, -, -⟩ := hpc.1 x k hk
    have hA := mkAsg_get env0 0 xks ws m x k w hm hw
    have hcell : (storeAll g' (mkAsg env0 0 xks ws)).cells[c]? = some (defaultVal (defaultVal w)) := by
      have := storeAll_get g' (mkAsg env0 0 xks ws)
        (mkAsg_pairwise env0 0 xks ws hnames.1 (fun y hy => by
            obtain ⟨ky, hky⟩ := hmem y hy
            obtain ⟨cy, hcy, -⟩ := hpc.1 y ky hky
            rw [hcy]; rfl)
          (fun y z c' hy hz hyc hzc => by
            obtain ⟨ky, hky⟩ := hmem y hy
            obtain ⟨kz, hkz⟩ := hmem z hz
            exact hpc.2 y z ky kz c' hky hkz hyc hzc))
        _ (List.mem_of_getElem? hA) (by simp only [hc, Option.getD_some]; exact hsz x k c hk hc)
      simpa [hc] using this
    obtain ⟨h1, h2, h3, h4⟩ := defaultVal_vrel2 hvw
    refine ⟨⟨v, false⟩, c, v, defaultVal (defaultVal w), ?_, bval_plain _ v (vrel_not_ref hvw), hc, hcell, h1, hkv,
      h2, h3, h4⟩
    show Spec.lookup (specFrame F.params vs) x = _
    unfold specFrame
    rw [← hpx]
    exact frame_get F.params vs [] hnd hpu m p v hp hv
  · show t.out = (storeAll g' (mkAsg env0 0 xks ws)).out
    rw [hout]
    have e := congrArg St.out hrest.1
    exact e.symm
  · intro x hx
    refine ⟨?_, hcl x hx⟩
    show Spec.lookup (specFrame F.params vs) x = none
    unfold specFrame
    have hxn : x ∉ F.params.map (·.name) := by
      rw [hpn]; intro hm; obtain ⟨k, hk⟩ := hmem x hm; rw [hx] at hk; cases hk
    rw [frame_notin F.params vs [] x hxn]; rfl
  · have e := congrArg St.types hrest.1
    simp only at e
    rw [hrest.2, e, hsync]

end LoopNext

end Gala.M5
