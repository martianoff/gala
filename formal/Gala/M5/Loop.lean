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
end Gala.M5
