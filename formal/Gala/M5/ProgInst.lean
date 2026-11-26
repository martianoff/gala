/-
  # M5 step 3d (b): the program lowering on concrete programs

  * Two functions calling each other, `isEven` and `isOdd`: `lowerProg` lowers them, the
    context GoCore builds meets `FunRel` (`evenOdd_funRel`, both functions declared), and
    gc's model leaves the program as written (`evenOdd_gc`).
  * The checks refuse: two functions of one name, a function named `int`
    (`dup_not_lowered`, `int_not_lowered`).
  * `fitsSs` is not true of every body: one reading a variable a call may change fails it
    (`changeable_not_fits`), while `sw`'s, `inc`'s and the two above pass.
-/
import Gala.M5.LoopInst

namespace Gala.M5

open Gala.GoCore.Full
open Std (HashMap)

/-! ## `isEven` and `isOdd` -/

def boolParamF (name callee : String) (base : Bool) : Spec.Func :=
  { name, params := [iParam "n"], defaults := [none], result := some (.name "bool"),
    body := .ifExpr (.binary "==" (.name "n") (.lit (.int 0))) (.lit (.bool base))
      (.call (.name callee) [.binary "-" (.name "n") (.lit (.int 1))]) }

def isEvenF : Spec.Func := boolParamF "isEven" "isOdd" true
def isOddF : Spec.Func := boolParamF "isOdd" "isEven" false

def evenOddDs : Spec.Decls :=
  { funcs := [("isEven", isEvenF), ("isOdd", isOddF)], structs := [], shorthands := [], sealeds := [],
    unsupported := [] }

def evenOddSg : Sigs := fun f =>
  if f = "isEven" then some ([.int], .bool) else if f = "isOdd" then some ([.int], .bool) else none

def boolParamG (callee : String) (base : Bool) : List Stmt :=
  [.block [ifT (.binary "==" (.ident "n") (.intLit 0)) [.ret [.ident (if base then "true" else "false")]]
    [.ret [.call (.ident callee) [.binary "-" (.ident "n") (.intLit 1)] false]]]]

theorem isEven_lowerFun : lowerFun 11 evenOddSg "isEven" isEvenF = some (boolParamG "isOdd" true) := rfl
theorem isOdd_lowerFun : lowerFun 11 evenOddSg "isOdd" isOddF = some (boolParamG "isEven" false) := rfl

def evenOddFs : List (String × Sig × List Stmt) :=
  [("isEven", lowerSigOf [("n", .int)] .bool, boolParamG "isOdd" true),
   ("isOdd", lowerSigOf [("n", .int)] .bool, boolParamG "isEven" false)]

theorem evenOdd_progSigs : progSigs evenOddDs = evenOddSg := by
  funext f; unfold progSigs evenOddSg
  by_cases h1 : f = "isEven"
  · subst h1; rfl
  by_cases h2 : f = "isOdd"
  · subst h2; rfl
  have h1' : (f == "isEven") = false := by simp [h1]
  have h2' : (f == "isOdd") = false := by simp [h2]
  simp [h1, h2, h1', h2', evenOddDs, List.lookup]

theorem evenOdd_get_even (a b : Obj) :
    (((∅ : HashMap String Obj).insert "isEven" a).insert "isOdd" b)["isEven"]? = some a := by
  simp only [Std.HashMap.getElem?_insert]; simp

theorem evenOdd_get_odd (a b : Obj) :
    (((∅ : HashMap String Obj).insert "isEven" a).insert "isOdd" b)["isOdd"]? = some b := by
  simp only [Std.HashMap.getElem?_insert]; simp

attribute [local simp high] evenOdd_get_even evenOdd_get_odd in
open Hoist in
theorem evenOdd_lowerProg : lowerProg 11 evenOddDs = some (progOf evenOddFs) := by
  have hE : sigOf isEvenF = some ([("n", .int)], .bool) := rfl
  have hO : sigOf isOddF = some ([("n", .int)], .bool) := rfl
  have hb : ∀ c b, body3Ss (boolParamG c b) = true := by
    intro c b
    cases b <;> simp [boolParamG, ifT, body3Ss, body3S, body3OS, ccB, ccBs, canon, canonArgs,
      triggers, isNonzeroLit, isLit]
  have hl : lowerFuns 11 evenOddDs = some evenOddFs := by
    have hf : evenOddDs.funcs = [("isEven", isEvenF), ("isOdd", isOddF)] := rfl
    unfold lowerFuns
    rw [evenOdd_progSigs, hf]
    simp [hE, hO, lowerOne, isEven_lowerFun, isOdd_lowerFun, hb, evenOddFs]
  have hn : namesOK evenOddDs = true := by decide
  have hfit : fitsAll evenOddFs = true := by
    simp [fitsAll, evenOddFs, boolParamG, ifT, fitsSs, fitsS, fitsOS, fitsE, fitsEs, isCallKind, fieldListNames,
      gcScope, funcScope, changeableName, Hoist.callKind, Hoist.isType, Hoist.singleResult,
      Scope.isLocal, Scope.isLType, Scope.ltype?, Scope.obj?, Scope.env, nameOwner,
      Scope.enter, Scope.withIfaces, Scope.bind, sigNames, lowerSigOf, kindName, chSs, chS, chE,
      chEs, chOS, isVarObj, buildCtx, buildPkg, progOf, funDecl, Id.run, List.forIn_cons,
      List.forIn_nil, List.foldl_cons, List.foldl_nil, id_pure, idb,
      Std.HashMap.getElem?_insert, isTypeObj, resultCount, beq_iff_eq,
      Std.HashMap.get?_eq_getElem?, Std.HashMap.contains_insert, Option.bind_some]
  simp [lowerProg, hn, hl, hfit]

/-- **Two functions calling each other**: the context GoCore builds from the lowered
    program declares both, as `FunRel` requires. -/
theorem evenOdd_funRel : FunRel 11 evenOddSg evenOddDs (buildCtx (progOf evenOddFs)) "main" := by
  have h := lowerProg_funRel evenOdd_lowerProg
  rwa [evenOdd_progSigs] at h

theorem evenOdd_gc : Order.gc (progOf evenOddFs) = progOf evenOddFs := lowerProg_gc evenOdd_lowerProg

/-! ## Refusals -/

/-- Two functions of one name: not lowered (`buildPkg` would keep only the last). -/
theorem dup_not_lowered :
    lowerProg 11 { evenOddDs with funcs := [("isEven", isEvenF), ("isEven", isOddF)] } = none := by
  have hn : namesOK { evenOddDs with funcs := [("isEven", isEvenF), ("isEven", isOddF)] } = false := by decide
  simp [lowerProg, hn]

/-- A function named `int`, the lowering's name for the type: not lowered. -/
theorem int_not_lowered :
    lowerProg 11 { evenOddDs with funcs := [("int", isEvenF)] } = none := by
  have hn : namesOK { evenOddDs with funcs := [("int", isEvenF)] } = false := by decide
  simp [lowerProg, hn]

/-! ## The fit check refuses -/

/-- A scope where the local `x` is one a call may change (a function literal assigns it). -/
def chScope : Hoist.Scope :=
  { c := ⟨∅, "main"⟩, pkg := "main", file := 0, locals := ["x"], changeable := (∅ : Std.HashSet String).insert "x" }

open Hoist in
/-- `fitsSs` is not true of every body: `return x + 1` reading such an `x` does not fit
    (gc's model may hoist around a read a call could change), and the lowering refuses a
    body like it. -/
theorem changeable_not_fits : fitsSs chScope [.ret [.binary "+" (.ident "x") (.intLit 1)]] = false := by
  simp [chScope, fitsSs, fitsS, fitsEs, fitsE, changeableName, Scope.isLocal]

end Gala.M5
