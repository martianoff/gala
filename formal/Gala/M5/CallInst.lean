/-
  # M5, step 3a: the hypotheses can be met

  `calls_correct` and the order result `gc_runs3` on concrete programs: `inc(x) = x + 1`
  called as `inc(5)`, every hypothesis discharged, and galaspec's run computed to end with
  `6`, so the theorem's conclusion speaks of a run that ends.
-/
import Gala.M5.CallOrder

namespace Gala.M5

open Gala.GoCore.Full
open Std (HashMap)

/-! ## `inc(5)` -/

def incF : Spec.Func :=
  { name := "inc", params := [⟨"x", some (.name "int")⟩], defaults := [none], result := some (.name "int"),
    body := .binary "+" (.name "x") (.lit (.int 1)) }

def incDs : Spec.Decls := { funcs := [("inc", incF)], structs := [], shorthands := [], sealeds := [], unsupported := [] }

def incSg : Sigs := fun f => if f = "inc" then some ([.int], .int) else none

def incBody : List Stmt := [.ret [.binary "+" (.ident "x") (.intLit 1)]]

theorem inc_lowerBody : lowerBody 8 incSg incF = some incBody := rfl

def incPkg : PkgCtx :=
  ⟨"main", "main", #[[]], (∅ : HashMap String Obj).insert "inc" (.func 0 [] (lowerSigOf [("x", .int)] .int) (some incBody)),
    {}, #[], #[], {}, false⟩

def incCtx : Ctx := ⟨(∅ : HashMap String PkgCtx).insert "main" incPkg, "main"⟩

theorem inc_funRel : FunRel 8 incSg incDs incCtx "main" := by
  refine ⟨by decide, incPkg, by simp [incCtx], fun x hx => ?_, fun f sig h => ?_⟩
  · simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl | rfl | rfl | rfl <;> simp [incPkg]
  · unfold incSg at h
    split at h
    · rename_i hf
      subst hf
      simp only [Option.some.injEq] at h
      subst h
      exact ⟨incF, [("x", .int)], .int, incBody, 0, rfl, rfl, rfl, inc_lowerBody, by simp [incPkg]⟩
    · cases h

def incEnv : Env := { pkg := "main", file := 0 }
def incCore : Spec.Core := { locals := [], globals := [], out := "" }
def incSt : St := { cells := #[], closures := #[], out := "", steps := 10 }
def incCall : Spec.Expr := .call (.name "inc") [.lit (.int 5)]
def incCallG : Expr := .call (.ident "inc") [.intLit 5] false

theorem inc_lowerC : lowerC 8 incSg (fun _ => none) incCall = some (.int, incCallG) := rfl

theorem inc_inv3 : Inv3 incCtx "main" incSg (fun _ => none) incCore incSt incEnv := by
  refine ⟨⟨(fun _ _ h => by cases h), rfl, ?_⟩, rfl, rfl, fun x _ => ⟨rfl, by simp [incEnv, Env.lookup]⟩,
    fun _ _ => rfl⟩
  refine ⟨rfl, fun x hx => ?_⟩
  have ho : incPkg.objs.get? x = none := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> simp [incPkg]
  have hexp : exported x = false := by
    simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
    rcases hx with rfl | rfl <;> decide
  have hpk : incCtx.pkgs.get? incEnv.pkg = some incPkg := by simp [incCtx, incEnv]
  exact ⟨by simp [incEnv, Env.lookup], incPkg, hpk, ho, nameOwner_none incCtx incEnv x incPkg hpk ho hexp⟩

/-- **`calls_correct` on `inc(5)`**, every hypothesis discharged. -/
theorem calls_correct_instance (steps d : Nat) (hs : steps ≤ 10) :
    ∀ N ≥ 8 * d, ERes3 .int (isLitE incCall) { incCore with steps } incSt
      ((Spec.eval incDs d incCall).run.run { incCore with steps })
      ((evalExpr incCtx N incEnv incCallG).run.run incSt) :=
  calls_correct incDs incCtx "main" 8 incSg inc_funRel (by omega) (fun _ => none) incCall .int incCallG
    inc_lowerC { incCore with steps } incSt incEnv inc_inv3 hs d



/-! galaspec's side, step by step through the unfolding lemmas (`eval` is well-founded). -/

theorem inc_resolve : Spec.resolve incDs [] [] "inc" = .func incF := rfl

theorem inc_eval_arg : (Spec.eval incDs 3 (.lit (.int 5))).run.run { incCore with steps := 9 } =
    (.ok (.int 5), { incCore with steps := 8 }) := by
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.intLit, minInt_eq, maxInt_eq]
  rfl

theorem inc_eval_body :
    (Spec.eval incDs 3 incF.body).run.run { incCore with steps := 8, locals := specFrame incF.params [.int 5] } =
      (.ok (.int 6), { incCore with steps := 5, locals := specFrame incF.params [.int 5] }) := by
  simp only [incF]
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.run_getCore, specFrame, Spec.bind, Spec.resolve,
    Spec.lookupVar, Spec.lookup, Spec.deref, Spec.intLit, Spec.binop, id_pure, minInt_eq, maxInt_eq, Spec.wrap]
  rfl

theorem inc_funcArgs (s : Spec.St) : (Spec.funcArgs incDs 3 incF [.lit (.int 5)]).run.run s =
    (Spec.evalList incDs 3 [.lit (.int 5)]).run.run s := by
  rw [Spec.funcArgs.eq_def]
  refine bindArgs_pos incDs 3 _ _ _ _ ?_ rfl ?_ s
  · intro a ha; simp at ha; subst ha; rfl
  · intro j hj
    have : j = 0 := by simp [incF] at hj; omega
    subst this
    exact ⟨"int", rfl⟩

/-- **galaspec's run of `inc(5)` ends with `6`**, so `calls_correct_instance` speaks of a run
    that ends. -/
theorem inc_spec_ok : (Spec.eval incDs 4 incCall).run.run { incCore with steps := 10 } =
    (.ok (.int 6), { incCore with steps := 5 }) := by
  rw [incCall, eval_call_run incDs 3 9 "inc" _ incF _ rfl inc_resolve, Spec.run_bind_eq]
  rw [inc_funcArgs, evalList_cons_run, inc_eval_arg]
  simp only
  rw [evalList_nil_run]
  simp only
  rw [callFunc_run incDs 3 incF [.int 5] _ (by simp [incF]; exact ⟨.int, rfl⟩) .int rfl rfl
    (.ok (.int 6)) { incCore with steps := 5 } (by rw [runIn_res]; erw [inc_eval_body]) (by simp)]

/-- **What `calls_correct` then says**: GoCore's run of the lowering, at every depth from
    `8 * 4` on, ends with a related `int`. -/
theorem inc_gocore_ok (N : Nat) (hN : N ≥ 32) :
    ∃ w g', (evalExpr incCtx N incEnv incCallG).run.run incSt = (.ok w, g') ∧ VRel (.int 6) w := by
  have h := calls_correct_instance 10 4 (by decide) N hN
  rw [show ({ incCore with steps := 10 } : Spec.St) = { incCore with steps := 10 } from rfl, inc_spec_ok] at h
  obtain ⟨w, g', hg, hv, -⟩ := h
  exact ⟨w, g', hg, hv⟩

end Gala.M5
