/-
  # M5, step 3c: the self-tail-call loop on the transpiler's shape, and a run of it

  The reference lowering `lowerFun` computed on a function the transformer loops, next to
  the Go the transpiler emits for it (`gala transpile`, built from master, `//line`
  comments dropped); its refusals where a parameter or a called function is named as the
  transformer's temporaries; and `calls_correct` on `sw(1, 2, 2)`, every hypothesis
  discharged, galaspec's run computed to end with `-1` after two iterations that swap the
  parameters (in-place assignment without the temporaries would give `0`).
-/
import Gala.M5.IifeInst

namespace Gala.M5

open Gala.GoCore.Full
open Std (HashMap)

/-! ## `sw`

```gala
func sw(a int, b int, n int) int = if (n == 0) a - b else sw(b, a, n - 1)
```

The transpiler emits:
```go
func sw(a int, b int, n int) int {
	for {
		if n == 0 {
			return a - b
		} else {
			_tmp_1 := b
			_tmp_2 := a
			_tmp_3 := n - 1
			a, b, n = _tmp_1, _tmp_2, _tmp_3
			continue
		}
	}
}
```
-/

def iParam (x : String) : Spec.Param := ⟨x, some (.name "int")⟩

def swBodyS : Spec.Expr :=
  .ifExpr (.binary "==" (.name "n") (.lit (.int 0))) (.binary "-" (.name "a") (.name "b"))
    (.call (.name "sw") [.name "b", .name "a", .binary "-" (.name "n") (.lit (.int 1))])

def swF : Spec.Func :=
  { name := "sw", params := [iParam "a", iParam "b", iParam "n"], defaults := [none, none, none],
    result := some (.name "int"), body := swBodyS }

def swDs : Spec.Decls := { funcs := [("sw", swF)], structs := [], shorthands := [], sealeds := [], unsupported := [] }

def swSg : Sigs := fun f => if f = "sw" then some ([.int, .int, .int], .int) else none

/-- The temporaries' names, `_tmp_1`, `_tmp_2`, `_tmp_3`. -/
theorem sw_tmps : tmpName 0 = "_tmp_1" ∧ tmpName 1 = "_tmp_2" ∧ tmpName 2 = "_tmp_3" := by decide

/-- The Go above, in GoCore (`tmpName i` is `_tmp_{i+1}`, `sw_tmps`). -/
def swGo : List Stmt :=
  [.for_ none none none
    [.if_ none (.binary "==" (.ident "n") (.intLit 0)) [.ret [.binary "-" (.ident "a") (.ident "b")]]
      (some (.block
        [.assign ":=" [.ident (tmpName 0)] [.ident "b"],
         .assign ":=" [.ident (tmpName 1)] [.ident "a"],
         .assign ":=" [.ident (tmpName 2)] [.binary "-" (.ident "n") (.intLit 1)],
         .assign "=" [.ident "a", .ident "b", .ident "n"] [.ident (tmpName 0), .ident (tmpName 1), .ident (tmpName 2)],
         .branch "continue"]))]]

/-- **The loop, as the transpiler emits it.** -/
theorem sw_lowerFun : lowerFun 11 swSg "sw" swF = some swGo := rfl

/-! ## Temporaries' names: the lowering refuses a collision (M6 renames temporaries) -/

/-- A parameter named as a temporary: the function is not lowered. -/
theorem tmpParam_not_lowered :
    lowerFun 11 swSg "sw" { swF with params := [iParam "_tmp_1", iParam "b", iParam "n"] } = none := rfl

def tmpCallSg : Sigs := fun f => if f = "sw" then some ([.int, .int, .int], .int)
  else if f = "_tmp_1" then some ([.int], .int) else none

/-- A function named as a temporary, called in a step's argument: no program calling it meets
    `FunRel`, so the theorems take none (Go would see the temporary, not the function). -/
theorem tmpCall_no_funRel (K : Nat) (ds : Spec.Decls) (cx : Ctx) (P : String) : ¬ FunRel K tmpCallSg ds cx P := by
  intro h
  have := h.noTmp "_tmp_1" rfl
  exact absurd this (by decide)

def okCallSg : Sigs := fun f => if f = "sw" then some ([.int, .int, .int], .int)
  else if f = "g" then some ([.int], .int) else none

def okCallBody : Spec.Expr :=
  .ifExpr (.binary "==" (.name "n") (.lit (.int 0))) (.name "a")
    (.call (.name "sw") [.call (.name "g") [.name "b"], .name "a", .name "n"])

/-- The same function calling a function of another name is lowered. -/
theorem okCall_lowered : (lowerFun 11 okCallSg "sw" { swF with body := okCallBody }).isSome = true := rfl

/-! ## `sw(1, 2, 2)` -/

def swPkg : PkgCtx :=
  ⟨"main", "main", #[[]], (∅ : HashMap String Obj).insert "sw"
    (.func 0 [] (lowerSigOf [("a", .int), ("b", .int), ("n", .int)] .int) (some swGo)), {}, #[], #[], {}, false⟩

def swCtx : Ctx := ⟨(∅ : HashMap String PkgCtx).insert "main" swPkg, "main"⟩

theorem sw_funRel : FunRel 11 swSg swDs swCtx "main" := by
  refine ⟨by decide, fun f hf => ?_, swPkg, by simp [swCtx], fun x hx => ?_, fun f sig h => ?_⟩
  · unfold swSg at hf
    split at hf
    · rename_i hf'; subst hf'; rfl
    · cases hf
  · simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl | rfl | rfl | rfl <;> simp [swPkg]
  · unfold swSg at h
    split at h
    · rename_i hf
      subst hf
      simp only [Option.some.injEq] at h
      subst h
      exact ⟨swF, [("a", .int), ("b", .int), ("n", .int)], .int, swGo, 0, rfl, rfl, rfl, sw_lowerFun,
        by simp [swPkg]⟩
    · cases h

def swCall : Spec.Expr := .call (.name "sw") [.lit (.int 1), .lit (.int 2), .lit (.int 2)]
def swCallG : Expr := .call (.ident "sw") [.intLit 1, .intLit 2, .intLit 2] false

theorem sw_lowerC : lowerC 11 swSg (fun _ => none) swCall = some (.int, swCallG) := rfl

theorem sw_inv3 (k : Nat) : Inv3 swCtx "main" swSg (fun _ => none) incCore { incSt with steps := k } incEnv := by
  refine ⟨⟨(fun _ _ h => by cases h), rfl, ?_⟩, rfl, rfl, fun x _ => ⟨rfl, fun _ => by simp [incEnv, Env.lookup]⟩,
    fun _ _ => rfl, rfl⟩
  refine ⟨rfl, fun x hx => ?_⟩
  have ho : swPkg.objs.get? x = none := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> simp [swPkg]
  have hexp : exported x = false := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> decide
  have hpk : swCtx.pkgs.get? incEnv.pkg = some swPkg := by simp [swCtx, incEnv]
  exact ⟨by simp [incEnv, Env.lookup], swPkg, hpk, ho, nameOwner_none swCtx incEnv x swPkg hpk ho hexp⟩

/-- **`calls_correct` on `sw(1, 2, 2)`**, every hypothesis discharged: the callee is the loop. -/
theorem loop_calls_instance (steps d : Nat) (hs : steps ≤ 40) :
    ∀ N ≥ 11 * d, ERes3 .int (isLitE swCall) { incCore with steps } { incSt with steps := 40 }
      ((Spec.eval swDs d swCall).run.run { incCore with steps })
      ((evalExpr swCtx N incEnv swCallG).run.run { incSt with steps := 40 }) :=
  calls_correct swDs swCtx "main" 11 swSg sw_funRel (by omega) (fun _ => none) swCall .int swCallG
    sw_lowerC { incCore with steps } { incSt with steps := 40 } incEnv (sw_inv3 40) hs d

/-! galaspec's side, iteration by iteration through the unfolding lemmas (`eval` is
well-founded, so the kernel does not compute it): `sw_step` is one iteration of the
recursion, `sw_last` the one that returns. -/

def swFr (a b n : Int) : Spec.Env := specFrame swF.params [.int a, .int b, .int n]

theorem sw_name (d k : Nat) (a b n : Int) (x : String) (v : Int)
    (hx : (x = "a" ∧ v = a) ∨ (x = "b" ∧ v = b) ∨ (x = "n" ∧ v = n)) :
    (Spec.eval swDs (d + 1) (.name x)).run.run { incCore with steps := k + 1, locals := swFr a b n } =
      (.ok (.int v), { incCore with steps := k, locals := swFr a b n }) := by
  rw [Spec.eval.eq_def]
  rcases hx with ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩ <;>
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, swFr, specFrame, swF, iParam, Spec.bind,
    Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref] <;> rfl

theorem sw_cond (d k : Nat) (a b n : Int) :
    (Spec.eval swDs (d + 2) (.binary "==" (.name "n") (.lit (.int 0)))).run.run
        { incCore with steps := k + 3, locals := swFr a b n } =
      (.ok (.bool (n == 0)), { incCore with steps := k, locals := swFr a b n }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, swFr, specFrame, swF, iParam, Spec.bind,
    Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq, maxInt_eq,
    Spec.valEq]

theorem sw_sub (d k : Nat) (a b n : Int) :
    (Spec.eval swDs (d + 2) (.binary "-" (.name "a") (.name "b"))).run.run
        { incCore with steps := k + 3, locals := swFr a b n } =
      (.ok (.int (Spec.wrap (a - b))), { incCore with steps := k, locals := swFr a b n }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, swFr, specFrame, swF, iParam, Spec.bind,
    Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref, Spec.binop, id_pure]

theorem sw_dec (d k : Nat) (a b n : Int) :
    (Spec.eval swDs (d + 2) (.binary "-" (.name "n") (.lit (.int 1)))).run.run
        { incCore with steps := k + 3, locals := swFr a b n } =
      (.ok (.int (Spec.wrap (n - 1))), { incCore with steps := k, locals := swFr a b n }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, swFr, specFrame, swF, iParam, Spec.bind,
    Spec.resolve, Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq, maxInt_eq]

theorem sw_resolve (fr : Spec.Env) (hfr : Spec.lookupVar fr [] "sw" = none) :
    Spec.resolve swDs fr [] "sw" = .func swF := by
  simp [Spec.resolve, hfr, swDs]

theorem sw_args (m k : Nat) (a b n : Int) :
    (Spec.funcArgs swDs (m + 2) swF [.name "b", .name "a", .binary "-" (.name "n") (.lit (.int 1))]).run.run
        { incCore with steps := k + 5, locals := swFr a b n } =
      (.ok [.int b, .int a, .int (Spec.wrap (n - 1))], { incCore with steps := k, locals := swFr a b n }) := by
  rw [Spec.funcArgs.eq_def]
  rw [bindArgs_pos swDs (m + 2) _ _ _ _ (by intro x hx; simp at hx; rcases hx with rfl | rfl | rfl <;> rfl) rfl
    (by intro j hj; simp [swF] at hj; rcases (by omega : j = 0 ∨ j = 1 ∨ j = 2) with rfl | rfl | rfl <;>
      exact ⟨"int", rfl⟩)]
  rw [evalList_cons_run, sw_name (m + 1) (k + 4) a b n "b" b (by simp)]
  simp only
  rw [evalList_cons_run, sw_name (m + 1) (k + 3) a b n "a" a (by simp)]
  simp only
  rw [evalList_cons_run, sw_dec m k a b n]
  simp only
  rw [evalList_nil_run]

theorem sw_step (m k k' : Nat) (a b n : Int) (hn : n ≠ 0) (v : Spec.Val) (hv : v ≠ .unit)
    (h : (Spec.eval swDs (m + 2) swBodyS).run.run
        { incCore with steps := k, locals := swFr b a (Spec.wrap (n - 1)) } =
      (.ok v, { incCore with steps := k', locals := swFr b a (Spec.wrap (n - 1)) })) :
    (Spec.eval swDs (m + 4) swBodyS).run.run { incCore with steps := k + 10, locals := swFr a b n } =
      (.ok v, { incCore with steps := k', locals := swFr a b n }) := by
  rw [swBodyS, eval_ifE_ok swDs (m + 3) (k + 9) _ _ _ _ (.inl rfl) false _
    { incCore with steps := k + 6, locals := swFr a b n } rfl
    (by rw [sw_cond (m + 1) (k + 6) a b n, show (n == 0) = false from beq_eq_false_iff_ne.mpr hn])]
  simp only [Bool.false_eq_true, ite_false]
  rw [eval_call_run swDs (m + 2) (k + 5) "sw" _ swF _ rfl (sw_resolve (swFr a b n) (by simp [swFr, specFrame, swF,
    iParam, Spec.bind, Spec.lookupVar, Spec.lookup])), Spec.run_bind_eq, sw_args m k a b n]
  simp only
  rw [callFunc_run swDs (m + 2) swF _ _ (by simp [swF, iParam]; exact ⟨.int, rfl⟩) .int rfl rfl (.ok v) _
    (by rw [runIn_res]; erw [h]) (by simpa using hv)]

theorem sw_last (m k : Nat) (a b : Int) :
    (Spec.eval swDs (m + 3) swBodyS).run.run { incCore with steps := k + 7, locals := swFr a b 0 } =
      (.ok (.int (Spec.wrap (a - b))), { incCore with steps := k, locals := swFr a b 0 }) := by
  rw [swBodyS, eval_ifE_ok swDs (m + 2) (k + 6) _ _ _ _ (.inl rfl) true _
    { incCore with steps := k + 3, locals := swFr a b 0 } rfl
    (by rw [sw_cond m (k + 3) a b 0]; rfl)]
  simp only [ite_true]
  exact sw_sub m k a b 0

theorem sw_lit (d k : Nat) (c : Int) (hc : -9223372036854775808 ≤ c ∧ c ≤ 9223372036854775807) :
    (Spec.eval swDs (d + 1) (.lit (.int c))).run.run { incCore with steps := k + 1 } =
      (.ok (.int c), { incCore with steps := k }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.intLit, minInt_eq, maxInt_eq, hc]
  rfl

theorem sw_call_args : (Spec.funcArgs swDs 7 swF [.lit (.int 1), .lit (.int 2), .lit (.int 2)]).run.run
      { incCore with steps := 39 } = (.ok [.int 1, .int 2, .int 2], { incCore with steps := 36 }) := by
  rw [Spec.funcArgs.eq_def]
  rw [bindArgs_pos swDs 7 _ _ _ _ (by intro x hx; simp at hx; rcases hx with rfl | rfl | rfl <;> rfl) rfl
    (by intro j hj; simp [swF] at hj; rcases (by omega : j = 0 ∨ j = 1 ∨ j = 2) with rfl | rfl | rfl <;>
      exact ⟨"int", rfl⟩)]
  rw [evalList_cons_run, sw_lit 6 38 1 (by decide)]
  simp only
  rw [evalList_cons_run, sw_lit 6 37 2 (by decide)]
  simp only
  rw [evalList_cons_run, sw_lit 6 36 2 (by decide)]
  simp only
  rw [evalList_nil_run]

/-- **galaspec's run of `sw(1, 2, 2)` ends with `-1`**, after two iterations that swap the
    parameters. -/
theorem sw_spec_ok : (Spec.eval swDs 8 swCall).run.run { incCore with steps := 40 } =
    (.ok (.int (-1)), { incCore with steps := 9 }) := by
  have h3 := sw_last 0 9 1 2
  rw [show Spec.wrap (1 - 2) = -1 by decide, show (0 : Int) = Spec.wrap (Spec.wrap (2 - 1) - 1) by decide] at h3
  have h5 := sw_step 1 16 9 2 1 (Spec.wrap (2 - 1)) (by decide) _ (by simp) h3
  have h7 := sw_step 3 26 9 1 2 2 (by decide) _ (by simp) h5
  rw [swCall, eval_call_run swDs 7 39 "sw" _ swF _ rfl (sw_resolve [] rfl), Spec.run_bind_eq, sw_call_args]
  simp only
  rw [callFunc_run swDs 7 swF _ _ (by simp [swF, iParam]; exact ⟨.int, rfl⟩) .int rfl rfl (.ok (.int (-1))) _
    (by rw [runIn_res]; erw [h7]) (by simp)]

/-- **What `calls_correct` then says**: GoCore's run of the loop, at every depth from
    `11 * 8` on, ends with a related `int`. -/
theorem sw_gocore_ok (N : Nat) (hN : N ≥ 88) :
    ∃ w g', (evalExpr swCtx N incEnv swCallG).run.run { incSt with steps := 40 } = (.ok w, g') ∧ VRel (.int (-1)) w := by
  have h := loop_calls_instance 40 8 (by decide) N hN
  rw [sw_spec_ok] at h
  obtain ⟨w, g', hg, hv, -⟩ := h
  exact ⟨w, g', hg, hv⟩

/-! ## The order result on the loop

`sw`'s loop is `Body3` and fits the scope gc's rewrite gives it, so `gc_runs3` says `Order.gc`
leaves it in place: the temporaries' `:=`, the parameters' `=` and `continue` fire no trigger. -/

section LoopOrder
open Hoist

def swSig : Sig := lowerSigOf [("a", .int), ("b", .int), ("n", .int)] .int
def swProgPkg : Package := ⟨"main", "main", false, [⟨"m.go", [], [Decl.func "sw" none [] swSig (some swGo)]⟩]⟩
def swProg : Program := ⟨[swProgPkg]⟩

theorem sw_body3 : Body3 swGo := by
  simp [Body3, body3Ss, body3S, body3OS, ccB, ccBs, canon, canonArgs, canonOps, triggers,
    isNonzeroLit, isLit, isIdentE, swGo]

theorem sw_fits : FitsSs (gcScope swProg "main" 0 none [] swSig swGo) swGo := by
  simp [FitsSs, FitsS, FitsOS, FitsE, FitsEs, fieldListNames, swGo, afterS, gcScope, funcScope, changeableName,
    Scope.isLocal, Scope.isLType, Scope.ltype?, Scope.obj?, Scope.env, nameOwner,
    Scope.enter, Scope.withIfaces, Scope.bind, sigNames, swSig, lowerSigOf, kindName, chSs, chS, chE,
    chEs, chOS, chOE, isVarObj, buildCtx, buildPkg, swProg, swProgPkg, Id.run, List.forIn_cons,
    List.forIn_nil, List.foldl_cons, List.foldl_nil, id_pure, idb,
    Std.HashMap.getElem?_insert, declaredBy, sw_tmps.1, sw_tmps.2.1, sw_tmps.2.2]

/-- **`gc_runs3` on the loop**: GoCore's model of gc's order leaves `sw`'s loop as written. -/
theorem swProg_gc : declAt (Order.gc swProg) 0 0 0 = some (.func "sw" none [] swSig (some swGo)) :=
  gc_runs3 swProg 0 0 0 swProgPkg "sw" none [] swSig swGo rfl sw_body3 sw_fits rfl

#guard declAt (Order.gc swProg) 0 0 0 == declAt swProg 0 0 0

end LoopOrder

end Gala.M5
