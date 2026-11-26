/-
  # M5, step 3b: the IIFE lowering on the transpiler's shapes, and a typed IIFE run

  The reference lowering of `if` used as a value (`lowerC`'s IIFE, `tailOf`'s tail `if`)
  computed on four functions, each next to the Go the transpiler emits for it (`gala
  transpile`, built from master, `//line` comments dropped); and `calls_correct` on
  `(if (true) 1 else 2) + 3`, every hypothesis discharged, galaspec's run computed to end
  with `4`.
-/
import Gala.M5.CallInst

namespace Gala.M5

open Gala.GoCore.Full

/-! ## The transpiler's shapes

```gala
func g(x int) int = x * 2
func pick(b bool, x int) int {
    val y = if (b) x else g(x)
    return y + 1
}
func blk(x int) int {
    val z = if (x > 0) {
        val t = x + 1
        t * 2
    } else {
        0
    }
    return z
}
func tailIf(b bool, x int) int = if (b) x + 1 else g(x)
func argIf(b bool, x int) int = g(if (b) x else 0) + 1
func armNest(b bool, x int) string {
    val y = if (b) {
        val t = x + 1
        if (t > 2) "big" else "small"
    } else "none"
    return y
}
```
-/

/-- The functions these bodies call: `g`. -/
def shapeSg : Sigs := fun f => if f = "g" then some ([.int], .int) else none

def bxParams : List Spec.Param := [⟨"b", some (.name "bool")⟩, ⟨"x", some (.name "int")⟩]

def iL (n : Int) : Spec.Expr := .lit (.int n)

/-- `pick`. The transpiler emits:
```go
func pick(b bool, x int) int {
	var y = func() int {
		if b {
			return x
		} else {
			return g(x)
		}
	}()
	return y + 1
}
``` -/
def pickF : Spec.Func :=
  { name := "pick", params := bxParams, defaults := [none, none], result := some (.name "int"),
    body := .block [.decl .val ["y"] false none [.ifExpr (.name "b") (.name "x") (.call (.name "g") [.name "x"])],
      .ret (some (.binary "+" (.name "y") (iL 1)))] }

def pickG : List Stmt :=
  [.decl (.value false [.mk ["y"] none
    [iife .int (ifT (.ident "b") [.ret [.ident "x"]] [.ret [.call (.ident "g") [.ident "x"] false]])]]),
   .ret [.binary "+" (.ident "y") (.intLit 1)]]

theorem pick_lowerBody : lowerBody 11 shapeSg pickF = some pickG := rfl

/-- `blk`, an arm of statements. The transpiler emits:
```go
func blk(x int) int {
	var z = func() int {
		if x > 0 {
			var t = x + 1
			return t * 2
		} else {
			return 0
		}
	}()
	return z
}
``` -/
def blkF : Spec.Func :=
  { name := "blk", params := [⟨"x", some (.name "int")⟩], defaults := [none], result := some (.name "int"),
    body := .block [.decl .val ["z"] false none [.ifExpr (.binary ">" (.name "x") (iL 0))
        (.block [.decl .val ["t"] false none [.binary "+" (.name "x") (iL 1)], .binary "*" (.name "t") (iL 2)])
        (.block [iL 0])],
      .ret (some (.name "z"))] }

def blkG : List Stmt :=
  [.decl (.value false [.mk ["z"] none
    [iife .int (ifT (.binary ">" (.ident "x") (.intLit 0))
      [.decl (.value false [.mk ["t"] none [.binary "+" (.ident "x") (.intLit 1)]]),
       .ret [.binary "*" (.ident "t") (.intLit 2)]]
      [.ret [.intLit 0]])]]),
   .ret [.ident "z"]]

theorem blk_lowerBody : lowerBody 11 shapeSg blkF = some blkG := rfl

/-- `tailIf`, an `if` as a function's value: no IIFE. The transpiler emits:
```go
func tailIf(b bool, x int) int {
	{
		if b {
			return x + 1
		} else {
			return g(x)
		}
	}
}
``` -/
def tailIfF : Spec.Func :=
  { name := "tailIf", params := bxParams, defaults := [none, none], result := some (.name "int"),
    body := .ifExpr (.name "b") (.binary "+" (.name "x") (iL 1)) (.call (.name "g") [.name "x"]) }

def tailIfG : List Stmt :=
  [.block [ifT (.ident "b") [.ret [.binary "+" (.ident "x") (.intLit 1)]] [.ret [.call (.ident "g") [.ident "x"] false]]]]

theorem tailIf_lowerBody : lowerBody 11 shapeSg tailIfF = some tailIfG := rfl

/-- `argIf`, an IIFE as an argument. The transpiler emits:
```go
func argIf(b bool, x int) int {
	return g(func() int {
		if b {
			return x
		} else {
			return 0
		}
	}()) + 1
}
``` -/
def argIfF : Spec.Func :=
  { name := "argIf", params := bxParams, defaults := [none, none], result := some (.name "int"),
    body := .binary "+" (.call (.name "g") [.ifExpr (.name "b") (.name "x") (iL 0)]) (iL 1) }

def argIfG : List Stmt :=
  [.ret [.binary "+" (.call (.ident "g") [iife .int (ifT (.ident "b") [.ret [.ident "x"]] [.ret [.intLit 0]])] false)
    (.intLit 1)]]

theorem argIf_lowerBody : lowerBody 11 shapeSg argIfF = some argIfG := rfl

/-- `armNest`, an `if` as an arm's last value: a block holding it, in the IIFE. The
    transpiler emits:
```go
func armNest(b bool, x int) string {
	var y = func() string {
		if b {
			var t = x + 1
			{
				if t > 2 {
					return "big"
				} else {
					return "small"
				}
			}
		} else {
			return "none"
		}
	}()
	return y
}
``` -/
def armNestF : Spec.Func :=
  { name := "armNest", params := bxParams, defaults := [none, none], result := some (.name "string"),
    body := .block [.decl .val ["y"] false none [.ifExpr (.name "b")
        (.block [.decl .val ["t"] false none [.binary "+" (.name "x") (iL 1)],
          .ifExpr (.binary ">" (.name "t") (iL 2)) (.lit (.str "big")) (.lit (.str "small"))])
        (.lit (.str "none"))],
      .ret (some (.name "y"))] }

def armNestG : List Stmt :=
  [.decl (.value false [.mk ["y"] none
    [iife .str (ifT (.ident "b")
      [.decl (.value false [.mk ["t"] none [.binary "+" (.ident "x") (.intLit 1)]]),
       .block [ifT (.binary ">" (.ident "t") (.intLit 2)) [.ret [.strLit "big"]] [.ret [.strLit "small"]]]]
      [.ret [.strLit "none"]])]]),
   .ret [.ident "y"]]

theorem armNest_lowerBody : lowerBody 11 shapeSg armNestF = some armNestG := rfl

/-- A `return` inside an arm is not lowered: it would leave the function in GALA and only
    the function literal in Go. -/
theorem retInArm_not_lowered :
    lowerC 11 shapeSg (fun _ => none) (.ifExpr (.lit (.bool true)) (.block [.ret (some (iL 1)), iL 2]) (iL 3)) = none :=
  rfl

/-! ## A typed IIFE, run: `(if (true) 1 else 2) + 3`

Its arms are literals, but the IIFE's value has the arms' type (`int`), so the `+` is on
typed operands: `calls_correct` says so (`ERes3 _ false`: never an untyped constant). -/

def ifCall : Spec.Expr := .binary "+" (.ifExpr (.lit (.bool true)) (iL 1) (iL 2)) (iL 3)

def ifCallG : Expr := .binary "+" (iife .int (ifT (.ident "true") [.ret [.intLit 1]] [.ret [.intLit 2]])) (.intLit 3)

theorem if_lowerC : lowerC 11 incSg (fun _ => none) ifCall = some (.int, ifCallG) := rfl

/-- **`calls_correct` on `(if (true) 1 else 2) + 3`**, every hypothesis discharged. -/
theorem iife_correct_instance (steps d : Nat) (hs : steps ≤ 10) :
    ∀ N ≥ 11 * d, ERes3 .int false { incCore with steps } incSt
      ((Spec.eval incDs d ifCall).run.run { incCore with steps })
      ((evalExpr incCtx N incEnv ifCallG).run.run incSt) :=
  calls_correct incDs incCtx "main" 11 incSg inc_funRel (by omega) (fun _ => none) ifCall .int ifCallG
    if_lowerC { incCore with steps } incSt incEnv inc_inv3 hs d

/-- **galaspec's run of `(if (true) 1 else 2) + 3` ends with `4`.** -/
theorem if_spec_ok : (Spec.eval incDs 3 ifCall).run.run { incCore with steps := 10 } =
    (.ok (.int 4), { incCore with steps := 5 }) := by
  simp only [ifCall, iL]
  rw [Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [Spec.run_bind_eq, tick_run, Spec.run_liftM, Spec.intLit, Spec.asBool, id_pure, minInt_eq, maxInt_eq]
  rw [Spec.eval.eq_def, Spec.eval.eq_def]
  simp [tick_run, Spec.intLit, Spec.binop, minInt_eq, maxInt_eq,
    Spec.wrap]
  rfl

/-- **What `calls_correct` then says**: GoCore's run of the IIFE, at every depth from
    `11 * 3` on, ends with a related `int` that is typed, not an untyped constant. -/
theorem iife_gocore_ok (N : Nat) (hN : N ≥ 33) :
    ∃ w g', (evalExpr incCtx N incEnv ifCallG).run.run incSt = (.ok w, g') ∧ VRel (.int 4) w ∧ ¬ UInt w := by
  have h := iife_correct_instance 10 3 (by decide) N hN
  rw [if_spec_ok] at h
  obtain ⟨w, g', hg, hv, -, hu, -⟩ := h
  exact ⟨w, g', hg, hv, hu rfl⟩

/-! ## The order result on an IIFE

`pick`'s lowering (`pickG`, the transpiler's own Go) with `g`: `pick` is `Body3`, its IIFE
included, and fits the scope gc's rewrite really builds for it, so `gc_runs3` leaves it in
place. A division before an IIFE at the same level is not `Body3`: the IIFE is a call, which
gc runs first (the fixture `eval_order_iife` pins it against `go run`). -/

section IifeOrder
open Hoist

def g2Body : List Stmt := [.ret [.binary "*" (.ident "x") (.intLit 2)]]
def pickSig : Sig := lowerSigOf [("b", .bool), ("x", .int)] .int
def iifePkg : Package :=
  ⟨"main", "main", false, [⟨"m.go", [], [Decl.func "g" none [] gSig (some g2Body),
    Decl.func "pick" none [] pickSig (some pickG)]⟩]⟩
def iifeProg : Program := ⟨[iifePkg]⟩

theorem pick_body3 : Body3 pickG := by
  simp [Body3, body3Ss, body3S, body3OS, ccB, ccBs, canon, canonArgs, triggers,
    isNonzeroLit, isLit, pickG, iife, ifT]

theorem pick_fits : FitsSs (gcScope iifeProg "main" 0 none [] pickSig pickG) pickG := by
  simp [FitsSs, FitsS, FitsOS, FitsE, FitsEs, fieldListNames, pickG, iife, ifT, litScope, afterS, declExpr?,
    gcScope, funcScope, changeableName, callKind, isType, Scope.isLocal, Scope.isLType, Scope.ltype?, Scope.obj?,
    Scope.env, nameOwner, singleResult, resultCount, Scope.enter, Scope.withIfaces, Scope.bind, sigNames, pickSig,
    gSig, lowerSigOf, kindName, chSs, chS, chE, chEs, chOS, chSpecs, isTypeObj, isVarObj, buildCtx, buildPkg, iifeProg,
    iifePkg, Id.run, List.forIn_cons, List.forIn_nil, List.foldl_cons, List.foldl_nil, id_pure, idb, Std.HashMap.getElem_insert, Std.HashMap.getElem?_insert]

/-- **`gc_runs3` on an IIFE**: GoCore's model of gc's order leaves `pick` as written. -/
theorem iifeProg_gc : declAt (Order.gc iifeProg) 0 0 1 = some (.func "pick" none [] pickSig (some pickG)) :=
  gc_runs3 iifeProg 0 0 1 iifePkg "pick" none [] pickSig pickG rfl pick_body3 pick_fits rfl

#guard declAt (Order.gc iifeProg) 0 0 1 == declAt iifeProg 0 0 1

/-- `x / z + func() int { .. }()`: the division may panic before a call at the same level. -/
theorem body3_div_iife :
    ¬ Body3 [.ret [.binary "+" (.binary "/" (.ident "x") (.ident "z"))
      (iife .int (ifT (.ident "b") [.ret [.intLit 1]] [.ret [.intLit 2]]))]] := by
  simp [Body3, body3Ss, body3S, body3OS, ccB, canon, canonArgs, triggers, mkNode, innerEffect, innerPanics,
    isNonzeroLit, isLit, iife, ifT, PK.differs]

/-- A division inside an IIFE's arm is a level of its own: the body is `Body3`. -/
theorem body3_iife_div_inside :
    Body3 [.ret [.binary "+" (.ident "x")
      (iife .int (ifT (.ident "b") [.ret [.binary "/" (.ident "x") (.ident "z")]] [.ret [.intLit 2]]))]] := by
  simp [Body3, body3Ss, body3S, body3OS, ccB, canon, canonArgs, triggers, mkNode, innerEffect, innerPanics,
    isNonzeroLit, isLit, iife, ifT, PK.differs]

end IifeOrder

end Gala.M5
