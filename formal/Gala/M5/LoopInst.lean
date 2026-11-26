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

end Gala.M5
