/-
  # M5, step 3d (c): instances of the divergence theorems

  * `kszOf` of `sw` (step 3c's loop, `sw(a, b, n) = if (n == 0) a - b else sw(b, a, n - 1)`)
    is 15 and of `spin` 7 (`sw_kszOf`, `spin_kszOf`).
  * `spin(n) = if (false) 0 else spin(n)`, which the transformer loops: galaspec runs out of
    fuel on `spin(0)` with every budget (`spin_spec_diverges`), so GoCore's run of the loop
    never ends normally, at any depth and step budget (`spin_gocore_diverges`, from
    `calls_diverge`).
  * `sw(1, 2, 2)`: galaspec spends 31 steps, so GoCore spends at least 2 (`sw_gocore_steps`,
    from `calls_stutter`).
  * `sw(1, 2, 2)` with too few steps: galaspec, at depth and budget 20, runs out of fuel
    (it needs 31 steps, `sw_spec_ok`), so GoCore with no step runs out too
    (`sw_gocore_fuel`, from `calls_fuel` with `Q = 15`). The bound is sound but loose: its
    factor `Q` is the largest body's size, so a budget below `Q` steps per call is what it
    can say runs out.
-/
import Gala.M5.DivMain
import Gala.M5.LoopInst
namespace Gala.M5
open Gala.GoCore.Full
open Std (HashMap)

/-! ## `sw` -/

theorem sw_kszOf : kszOf swDs = 15 := rfl

/-- galaspec runs out of fuel on `sw(1, 2, 2)` with 20 steps at depth 20: were it to end
    otherwise, it would end the same way with 20 more steps (galaspec's `eval_sim`), and
    so as with 40 steps at depth 8 (`sw_spec_ok`) — with 9 steps left, fewer than 20. -/
theorem sw_spec_fuel : ((Spec.eval swDs 20 swCall).run.run { incCore with steps := 20 }).1 = .error .fuel := by
  rcases hr : (Spec.eval swDs 20 swCall).run.run { incCore with steps := 20 } with ⟨r, t⟩
  have hbig : (Spec.eval swDs 20 swCall).run.run { incCore with steps := 40 } = (.ok (.int (-1)), { incCore with steps := 9 }) := by
    have h := (Spec.eval_sim swDs 0 8 20 swCall (by omega)).prop { incCore with steps := 40 } (by rw [sw_spec_ok]; trivial)
    rw [sw_spec_ok] at h
    exact h
  cases r with
  | error e =>
    cases e with
    | fuel => rfl
    | _ =>
      exfalso
      have h := (Spec.eval_sim swDs 20 20 20 swCall (Nat.le_refl _)).prop { incCore with steps := 20 }
        (by rw [hr]; trivial)
      rw [hr] at h
      have h2 : Spec.shift 20 { incCore with steps := 20 } = { incCore with steps := 40 } := rfl
      rw [h2, hbig] at h
      cases h
  | ok v =>
    exfalso
    have h := (Spec.eval_sim swDs 20 20 20 swCall (Nat.le_refl _)).prop { incCore with steps := 20 }
      (by rw [hr]; trivial)
    rw [hr] at h
    have h2 : Spec.shift 20 { incCore with steps := 20 } = { incCore with steps := 40 } := rfl
    rw [h2, hbig] at h
    have := congrArg (fun p => p.2.steps) h
    simp [Spec.shift] at this

/-- **`calls_fuel` on `sw(1, 2, 2)`**: galaspec runs out with 20 steps (`sw_spec_fuel`), and
    `15 * (0 + 1) + 5 ≤ 20` (`Q = kszOf swDs = 15`, `esz swCall = 5`), so GoCore with no step
    runs out, at every depth from `11 * 20` on. -/
theorem sw_gocore_fuel : ∀ N ≥ 11 * 20,
    ((evalExpr swCtx N incEnv swCallG).run.run { incSt with steps := 0 }).1 = .error .fuel :=
  calls_fuel swDs swCtx "main" 11 swSg (kszOf swDs) sw_funRel (by omega) (kszOf_ok swDs) (fun _ => none) swCall
    .int swCallG sw_lowerC { incCore with steps := 20 } { incSt with steps := 0 } incEnv (sw_inv3 0) 20
    (Nat.le_refl _) (by rw [sw_kszOf]; decide) sw_spec_fuel

/-- **`calls_stutter` on `sw(1, 2, 2)`**: galaspec spends 31 steps (40 to 9, `sw_spec_ok`), so
    GoCore spends at least 2 (`31 ≤ 15 * spent + 5`): its run ends with at most 38 of its
    40 steps left. (It spends 4: one for the call, one per iteration.) -/
theorem sw_gocore_steps (N : Nat) (hN : N ≥ 88) :
    ∃ w g', (evalExpr swCtx N incEnv swCallG).run.run { incSt with steps := 40 } = (.ok w, g') ∧
      VRel (.int (-1)) w ∧ g'.steps ≤ 38 := by
  obtain ⟨w, g', hg, hv⟩ := sw_gocore_ok N hN
  have h := calls_stutter swDs swCtx "main" 11 swSg (kszOf swDs) sw_funRel (by omega) (kszOf_ok swDs)
    (fun _ => none) swCall .int swCallG sw_lowerC { incCore with steps := 40 } { incSt with steps := 40 } incEnv
    (sw_inv3 40) (Nat.le_refl _) 8 N (by omega)
  rw [sw_spec_ok, hg, sw_kszOf] at h
  simp only [Stut] at h
  exact ⟨w, g', hg, hv, by simp [esz, eszs, swCall] at h; omega⟩

/-! ## `spin` -/

def spinBodyS : Spec.Expr := .ifExpr (.lit (.bool false)) (.lit (.int 0)) (.call (.name "spin") [.name "n"])

def spinF : Spec.Func :=
  { name := "spin", params := [iParam "n"], defaults := [none], result := some (.name "int"), body := spinBodyS }

def spinDs : Spec.Decls :=
  { funcs := [("spin", spinF)], structs := [], shorthands := [], sealeds := [], unsupported := [] }

def spinSg : Sigs := fun f => if f = "spin" then some ([.int], .int) else none

theorem spin_kszOf : kszOf spinDs = 7 := rfl

/-- The loop the transpiler emits for `spin`: `for { if false { return 0 } else
    { _tmp_1 := n; n = _tmp_1; continue } }`. -/
def spinGo : List Stmt :=
  [.for_ none none none
    [.if_ none (.ident "false") [.ret [.intLit 0]]
      (some (.block
        [.assign ":=" [.ident (tmpName 0)] [.ident "n"],
         .assign "=" [.ident "n"] [.ident (tmpName 0)],
         .branch "continue"]))]]

theorem spin_lowerFun : lowerFun 11 spinSg "spin" spinF = some spinGo := rfl

def spinPkg : PkgCtx :=
  ⟨"main", "main", #[[]], (∅ : HashMap String Obj).insert "spin"
    (.func 0 [] (lowerSigOf [("n", .int)] .int) (some spinGo)), {}, #[], #[], {}, false⟩

def spinCtx : Ctx := ⟨(∅ : HashMap String PkgCtx).insert "main" spinPkg, "main"⟩

def spinFs : List (String × Sig × List Stmt) := [("spin", lowerSigOf [("n", .int)] .int, spinGo)]

theorem spin_buildCtx : buildCtx (progOf spinFs) = spinCtx := by
  simp [buildCtx, progOf, spinFs, spinCtx, spinPkg, buildPkg, funDecl, Std.HashMap.fold_eq_foldl_toList]

theorem spin_progSigs : progSigs spinDs = spinSg := by
  funext f; unfold progSigs spinSg; by_cases h : f = "spin"
  · subst h; rfl
  · have h' : (f == "spin") = false := by simp [h]
    simp [h, h', spinDs, List.lookup]

open Hoist in
theorem spin_lowerOne : lowerOne 11 spinSg "spin" spinF = some ("spin", lowerSigOf [("n", .int)] .int, spinGo) := by
  have h1 : sigOf spinF = some ([("n", .int)], .int) := rfl
  have h3 : body3Ss spinGo = true := by
    simp [body3Ss, body3S, body3OS, ccB, ccBs, canon, canonArgs, canonOps, triggers,
      isIdentE, spinGo]
  simp [lowerOne, h1, spin_lowerFun, h3]

theorem spin_lowerFuns : lowerFuns 11 spinDs = some spinFs := by
  have h1 : (sigOf spinF).isSome = true := rfl
  have hf : spinDs.funcs = [("spin", spinF)] := rfl
  unfold lowerFuns
  rw [spin_progSigs, hf]
  simp [h1, spin_lowerOne, spinFs]

open Hoist in
theorem spin_lowerProg : lowerProg 11 spinDs = some (progOf spinFs) := by
  have hn : namesOK spinDs = true := by decide
  have hfit : fitsAll spinFs = true := by
    simp [fitsAll, spinFs, fitsSs, fitsS, fitsOS, fitsE, fitsEs, fieldListNames, spinGo, afterS,
      gcScope, funcScope, changeableName,
      Scope.isLocal, Scope.isLType, Scope.ltype?, Scope.obj?, Scope.env, nameOwner,
      Scope.enter, Scope.withIfaces, Scope.bind, sigNames, lowerSigOf, kindName, chSs, chS, chE,
      chEs, chOS, chOE, isVarObj, buildCtx, buildPkg, progOf, funDecl, Id.run, List.forIn_cons,
      List.forIn_nil, List.foldl_cons, List.foldl_nil, id_pure, idb,
      Std.HashMap.getElem?_insert, declaredBy, sw_tmps.1]
  simp [lowerProg, hn, spin_lowerFuns, hfit]

theorem spin_funRel : FunRel 11 spinSg spinDs spinCtx "main" := by
  have h := lowerProg_funRel spin_lowerProg
  rwa [spin_buildCtx, spin_progSigs] at h

def spinCall : Spec.Expr := .call (.name "spin") [.lit (.int 0)]
def spinCallG : Expr := .call (.ident "spin") [.intLit 0] false

theorem spin_lowerC : lowerC 11 spinSg (fun _ => none) spinCall = some (.int, spinCallG) := rfl

theorem spin_inv3 : Inv3 spinCtx "main" spinSg (fun _ => none) incCore incSt incEnv := by
  refine ⟨⟨(fun _ _ h => by cases h), rfl, ?_⟩, rfl, rfl, fun x _ => ⟨rfl, fun _ => by simp [incEnv, Env.lookup]⟩,
    fun _ _ => rfl, rfl⟩
  refine ⟨rfl, fun x hx => ?_⟩
  have ho : spinPkg.objs.get? x = none := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> simp [spinPkg]
  have hexp : exported x = false := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> decide
  have hpk : spinCtx.pkgs.get? incEnv.pkg = some spinPkg := by simp [spinCtx, incEnv]
  exact ⟨by simp [incEnv, Env.lookup], spinPkg, hpk, ho, nameOwner_none spinCtx incEnv x spinPkg hpk ho hexp⟩

/-! galaspec's side: every run of `spin`'s body runs out of fuel, by induction on the depth
(each iteration is a call one level deeper). -/

def spinFr (v : Int) : Spec.Env := specFrame spinF.params [.int v]

theorem spin_name (d k : Nat) (v : Int) (s : Spec.St) (hs : s.steps = k + 1) (hl : s.locals = spinFr v) :
    (Spec.eval spinDs (d + 1) (.name "n")).run.run s = (.ok (.int v), { s with steps := k }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, hs, Spec.run_liftM, Spec.run_getCore, hl, spinFr, specFrame, spinF, iParam,
    Spec.bind, Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref]
  rfl

theorem spin_false (d k : Nat) (s : Spec.St) (hs : s.steps = k + 1) :
    (Spec.eval spinDs (d + 1) (.lit (.bool false))).run.run s = (.ok (.bool false), { s with steps := k }) := by
  rw [Spec.eval.eq_def]
  simp [tick_run, hs]
  rfl

theorem spin_lit0 (d k : Nat) (s : Spec.St) (hs : s.steps = k + 1) :
    (Spec.eval spinDs (d + 1) (.lit (.int 0))).run.run s = (.ok (.int 0), { s with steps := k }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, hs, Spec.run_liftM, Spec.intLit, minInt_eq, maxInt_eq]
  rfl

theorem spin_resolve (fr : Spec.Env) (hfr : Spec.lookupVar fr [] "spin" = none) :
    Spec.resolve spinDs fr [] "spin" = .func spinF := by
  simp [Spec.resolve, hfr, spinDs]

theorem spin_funcArgs (d : Nat) (a : Spec.Expr) (ha : Spec.isNamed a = false) (s : Spec.St) :
    (Spec.funcArgs spinDs d spinF [a]).run.run s = (Spec.evalList spinDs d [a]).run.run s := by
  rw [Spec.funcArgs.eq_def]
  exact bindArgs_pos spinDs d _ _ _ _ (by intro x hx; simp at hx; subst hx; exact ha) rfl
    (by intro j hj; simp [spinF] at hj; subst hj; exact ⟨"int", rfl⟩) s

/-- A call of `spin` whose body runs out of fuel runs out of fuel. -/
theorem spin_callFunc (d : Nat) (v : Int) (t : Spec.St)
    (h : ((Spec.eval spinDs d spinBodyS).run.run { t with locals := spinFr v }).1 = .error .fuel) :
    ((Spec.callFunc spinDs d spinF [.int v]).run.run t).1 = .error .fuel := by
  rcases hb : (Spec.eval spinDs d spinBodyS).run.run { t with locals := spinFr v } with ⟨r, t'⟩
  rw [hb] at h
  simp only at h
  subst h
  rw [callFunc_run spinDs d spinF [.int v] t (by simp [spinF, iParam]; exact ⟨.int, rfl⟩) .int rfl rfl
    (.error .fuel) t' (by rw [runIn_res]; erw [hb]) (fun h => nomatch h)]

theorem spin_resolve' (s : Spec.St) (v : Int) (hl : s.locals = spinFr v) (hg : s.globals = []) :
    Spec.resolve spinDs s.locals s.globals "spin" = .func spinF := by
  rw [hl, hg]
  exact spin_resolve _ (by simp [spinFr, specFrame, spinF, iParam, Spec.bind, Spec.lookupVar, Spec.lookup])

/-- **Every run of `spin`'s body runs out of fuel**, by induction on the depth: an iteration
    is a call one level deeper. -/
theorem spin_body_fuel (d : Nat) (s : Spec.St) (v : Int) (hl : s.locals = spinFr v) (hg : s.globals = []) :
    ((Spec.eval spinDs d spinBodyS).run.run s).1 = .error .fuel := by
  match d with
  | 0 => rw [eval_zero]
  | d1 + 1 =>
    cases hs : s.steps with
    | zero => rw [eval_fuel _ _ _ _ hs]
    | succ k1 =>
    match d1 with
    | 0 => rw [spinBodyS, eval_ifE_err spinDs 0 k1 _ _ _ _ (.inl rfl) _ _ .fuel hs (by rw [eval_zero])]
    | d2 + 1 =>
    cases k1 with
    | zero => rw [spinBodyS, eval_ifE_err spinDs (d2 + 1) 0 _ _ _ _ (.inl rfl) _ _ .fuel hs (by rw [eval_fuel _ _ _ _ rfl])]
    | succ k2 =>
    rw [spinBodyS, eval_ifE_ok spinDs (d2 + 1) (k2 + 1) _ _ _ _ (.inl rfl) false _ _ hs
      (spin_false d2 k2 { s with steps := k2 + 1 } rfl)]
    simp only [Bool.false_eq_true, ite_false]
    cases k2 with
    | zero => rw [eval_fuel _ _ _ _ rfl]
    | succ k3 =>
    rw [eval_call_run spinDs d2 k3 "spin" _ spinF { s with steps := k3 + 1 } rfl
      (spin_resolve' { s with steps := k3 + 1 } v hl hg), Spec.run_bind_eq,
      spin_funcArgs d2 _ rfl]
    match d2 with
    | 0 => rw [evalList_cons_run, eval_zero]
    | d4 + 1 =>
    cases k3 with
    | zero => rw [evalList_cons_run, eval_fuel _ _ _ _ rfl]
    | succ k4 =>
    have hn := spin_name d4 k4 v ⟨({ toCore := s.toCore, steps := k4 + 1 + 1 } : Spec.St).toCore, k4 + 1⟩ rfl hl
    rw [evalList_cons_run, hn]
    simp only
    rw [evalList_nil_run]
    simp only
    exact spin_callFunc (d4 + 1) v _ (spin_body_fuel (d4 + 1) _ v rfl hg)
  termination_by d

/-- **galaspec diverges on `spin(0)`**: it runs out of fuel with every budget `n`, at
    depth `n`. -/
theorem spin_spec_diverges : ∀ n, ((Spec.eval spinDs n spinCall).run.run { incCore with steps := n }).1 = .error .fuel := by
  intro n
  match n with
  | 0 => rw [eval_zero]
  | d + 1 =>
    rw [spinCall, eval_call_run spinDs d d "spin" _ spinF _ rfl (spin_resolve [] rfl), Spec.run_bind_eq,
      spin_funcArgs d _ rfl]
    match d with
    | 0 => rw [evalList_cons_run, eval_zero]
    | d1 + 1 =>
      rw [evalList_cons_run, spin_lit0 d1 d1 _ rfl]
      simp only
      rw [evalList_nil_run]
      simp only
      exact spin_callFunc (d1 + 1) 0 _ (spin_body_fuel (d1 + 1) _ 0 rfl rfl)

/-- **`calls_diverge` on `spin(0)`**: GoCore's run of the loop the transpiler emits never
    ends normally: it runs out of fuel at every depth and with every step budget. -/
theorem spin_gocore_diverges : ∀ N m,
    ((evalExpr spinCtx N incEnv spinCallG).run.run { incSt with steps := m }).1 = .error .fuel :=
  calls_diverge spinDs spinCtx "main" 11 spinSg spin_funRel (by omega) (fun _ => none) spinCall .int spinCallG
    spin_lowerC incCore incSt incEnv spin_inv3 spin_spec_diverges

end Gala.M5
