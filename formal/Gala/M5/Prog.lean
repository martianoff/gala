/-
  # M5 step 3d (b): the lowering of a whole program

  `lowerProg K ds`: the GoCore program the reference lowering gives the functions of a
  galaspec program, one package `main` with one file, a `func` per function, each lowered
  by `lowerFun` with the program's own signatures (`progSigs`). A function whose signature
  is not of the lowered kinds (`sigOf` is `none`: `main`, a function returning nothing, one
  with a parameter or result of another type) is skipped: no lowered body can call it, as
  `lowerC` lowers only calls of functions `progSigs` declares. Step 5 widens this to
  `main`. Every other function must lower, or the program does not.

  Checks, each what `FunRel` or the order result needs:
  * the names are pairwise distinct (`buildPkg` keeps the last of two);
  * no name is `true`, `false`, `int`, `bool` or `string` (the lowering's names for them),
    or `init` (which Go does not declare as a function);
  * no name is a temporary's (`isTmp`);
  * every lowered body is `Body3`, the order predicate, and fits the scope gc's rewrite
    gives it (`fitsSs`, proved sound by `fitsSs_sound`): requirements, so the theorems
    about lowered programs take no hypothesis about the order.

  `lowerProg_funRel`: the context GoCore builds from the lowered program meets `FunRel`.
  `lowerProg_gc`: gc's model of the order leaves the lowered program as written. `Inv3`'s
  hypothesis that no galaspec global is named after a lowered function stays a hypothesis,
  which step 5 discharges from the initial state.
-/
import Gala.M5.CallOrder
import Gala.M5.Loop

namespace Gala.M5

open Gala.GoCore.Full

/-! ## Fitting gc's scope, decided

`FitsSs` (the order result's hypothesis that a body fits the scope gc's rewrite gives it)
as a check the lowering runs (`fitsSs`), proved sound (`fitsSs_sound`). -/

open Hoist in
/-- A call of a function, not a conversion or a builtin. -/
def isCallKind : CallKind → Bool
  | .call => true
  | _ => false

theorem isCallKind_eq {k : Hoist.CallKind} (h : isCallKind k = true) : k = .call := by
  cases k <;> simp_all [isCallKind]

open Hoist in
mutual
/-- `FitsE`, decided. -/
def fitsE (s : Scope) : Expr → Bool
  | .ident x => !changeableName s x
  | .paren x | .unary _ x => fitsE s x
  | .binary _ x y => fitsE s x && fitsE s y
  | .call (.ident f) args _ => isCallKind (callKind s (.ident f)) && !changeableName s f &&
      singleResult s (.ident f) && fitsEs s args
  | .call (.funcLit sig b) args _ => resultCount sig == 1 && fitsSs (litScope s sig) b && fitsEs s args
  | _ => true
def fitsEs (s : Scope) : List Expr → Bool
  | [] => true
  | a :: as => fitsE s a && fitsEs s as
/-- `FitsS`, decided. -/
def fitsS (s : Scope) : Stmt → Bool
  | .decl (.value false [.mk [_] none [e]]) => fitsE s e
  | .ret es => fitsEs s es
  | .block ss => fitsSs s ss
  | .if_ _ c b els => fitsE s c && fitsSs s b && fitsOS s els
  | .assign _ lhs rhs => fitsEs s lhs && fitsEs s rhs
  | .for_ _ _ _ b => fitsSs s b
  | _ => true
def fitsSs (s : Scope) : List Stmt → Bool
  | [] => true
  | st :: ss => fitsS s st && fitsSs (afterS s st) ss
def fitsOS (s : Scope) : Option Stmt → Bool
  | none => true
  | some st => fitsS s st
end

open Hoist in
mutual
theorem fitsE_sound (s : Scope) : ∀ e, fitsE s e = true → FitsE s e
  | .ident x, h => by simpa [fitsE, FitsE] using h
  | .paren x, h => by simp only [fitsE, FitsE] at h ⊢; exact fitsE_sound s x h
  | .unary _ x, h => by simp only [fitsE, FitsE] at h ⊢; exact fitsE_sound s x h
  | .binary _ x y, h => by
    simp only [fitsE, FitsE, Bool.and_eq_true] at h ⊢
    exact ⟨fitsE_sound s x h.1, fitsE_sound s y h.2⟩
  | .call (.ident f) args _, h => by
    simp only [fitsE, FitsE, Bool.and_eq_true, Bool.not_eq_true'] at h ⊢
    exact ⟨isCallKind_eq h.1.1.1, h.1.1.2, h.1.2, fitsEs_sound s args h.2⟩
  | .call (.funcLit sig b) args _, h => by
    simp only [fitsE, FitsE, Bool.and_eq_true, beq_iff_eq] at h ⊢
    exact ⟨h.1.1, fitsSs_sound _ b h.1.2, fitsEs_sound s args h.2⟩
  | .call (.intLit _) _ _, _
  | .call (.strLit _) _ _, _
  | .call (.charLit _) _ _, _
  | .call (.floatLit ..) _ _, _
  | .call (.composite ..) _ _, _
  | .call (.keyValue ..) _ _, _
  | .call (.paren _) _ _, _
  | .call (.selector ..) _ _, _
  | .call (.index ..) _ _, _
  | .call (.sliceExpr ..) _ _, _
  | .call (.typeAssert ..) _ _, _
  | .call (.call ..) _ _, _
  | .call (.star _) _ _, _
  | .call (.unary ..) _ _, _
  | .call (.binary ..) _ _, _
  | .call (.structType _) _ _, _
  | .call (.interfaceType _) _ _, _
  | .call (.funcType _) _ _, _
  | .call (.arrayType ..) _ _, _
  | .call (.mapType ..) _ _, _
  | .call (.ellipsis) _ _, _
  | .call (.unsupported _) _ _, _ => trivial
  | .intLit _, _
  | .strLit _, _
  | .charLit _, _
  | .floatLit .., _
  | .composite .., _
  | .keyValue .., _
  | .funcLit .., _
  | .selector .., _
  | .index .., _
  | .sliceExpr .., _
  | .typeAssert .., _
  | .star _, _
  | .structType _, _
  | .interfaceType _, _
  | .funcType _, _
  | .arrayType .., _
  | .mapType .., _
  | .ellipsis, _
  | .unsupported _, _ => trivial
theorem fitsEs_sound (s : Scope) : ∀ es, fitsEs s es = true → FitsEs s es
  | [], _ => trivial
  | e :: es, h => by
    simp only [fitsEs, FitsEs, Bool.and_eq_true] at h ⊢
    exact ⟨fitsE_sound s e h.1, fitsEs_sound s es h.2⟩
theorem fitsS_sound (s : Scope) : ∀ st, fitsS s st = true → FitsS s st
  | .decl (.value false [.mk [_] none [e]]), h => by
    simp only [fitsS, FitsS] at h ⊢; exact fitsE_sound s e h
  | .ret es, h => by simp only [fitsS, FitsS] at h ⊢; exact fitsEs_sound s es h
  | .block ss, h => by simp only [fitsS, FitsS] at h ⊢; exact fitsSs_sound s ss h
  | .if_ _ c b els, h => by
    simp only [fitsS, FitsS, Bool.and_eq_true] at h ⊢
    exact ⟨fitsE_sound s c h.1.1, fitsSs_sound s b h.1.2, fitsOS_sound s els h.2⟩
  | .assign _ lhs rhs, h => by
    simp only [fitsS, FitsS, Bool.and_eq_true] at h ⊢
    exact ⟨fitsEs_sound s lhs h.1, fitsEs_sound s rhs h.2⟩
  | .for_ _ _ _ b, h => by simp only [fitsS, FitsS] at h ⊢; exact fitsSs_sound s b h
  | .expr _, _
  | .incDec .., _
  | .branch _, _
  | .switch .., _
  | .typeSwitch .., _
  | .range .., _
  | .defer_ _, _
  | .localType _, _
  | .empty, _
  | .unsupported _, _
  | .decl (.func ..), _
  | .decl (.type _), _
  | .decl (.unsupported _), _
  | .decl (.value true _), _
  | .decl (.value false []), _
  | .decl (.value false (_ :: _ :: _)), _
  | .decl (.value false [.mk [] _ _]), _
  | .decl (.value false [.mk (_ :: _ :: _) _ _]), _
  | .decl (.value false [.mk [_] (some _) _]), _
  | .decl (.value false [.mk [_] none []]), _
  | .decl (.value false [.mk [_] none (_ :: _ :: _)]), _ => by unfold FitsS; first | trivial | (split <;> first | trivial | simp_all)
theorem fitsSs_sound (s : Scope) : ∀ ss, fitsSs s ss = true → FitsSs s ss
  | [], _ => trivial
  | st :: ss, h => by
    simp only [fitsSs, FitsSs, Bool.and_eq_true] at h ⊢
    exact ⟨fitsS_sound s st h.1, fitsSs_sound _ ss h.2⟩
theorem fitsOS_sound (s : Scope) : ∀ els, fitsOS s els = true → FitsOS s els
  | none, _ => trivial
  | some st, h => by simp only [fitsOS, FitsOS] at h ⊢; exact fitsS_sound s st h
end

/-- The signatures of a program's functions of the lowered kinds. -/
def progSigs (ds : Spec.Decls) : Sigs := fun f =>
  (ds.funcs.lookup f).bind fun F => (sigOf F).map fun (xks, ρ) => (xks.map (·.2), ρ)

/-- Names a lowered function must not have: the lowering's names for the constants and
    types it uses, and `init`. -/
def reservedNames : List String := ["true", "false", "int", "bool", "string", "init"]

/-- One function lowered: its name, Go signature and body, if the body is `Body3`. -/
def lowerOne (K : Nat) (sg : Sigs) (f : String) (F : Spec.Func) : Option (String × Sig × List Stmt) := do
  let (xks, ρ) ← sigOf F
  let lb ← lowerFun K sg f F
  if body3Ss lb then some (f, lowerSigOf xks ρ, lb) else none

/-- A lowered function's declaration. -/
def funDecl (d : String × Sig × List Stmt) : Decl := .func d.1 none [] d.2.1 (some d.2.2)

/-- The program of lowered functions: one package `main`, one file. -/
def progOf (fs : List (String × Sig × List Stmt)) : Program :=
  ⟨[⟨"main", "main", false, [⟨"main.go", [], fs.map funDecl⟩]⟩]⟩

/-- The checks on the names (see the module docstring). -/
def namesOK (ds : Spec.Decls) : Bool :=
  let names := ds.funcs.map (·.1)
  decide names.Nodup && !names.any reservedNames.contains && !names.any isTmp

/-- The functions of the lowered kinds, each lowered. -/
def lowerFuns (K : Nat) (ds : Spec.Decls) : Option (List (String × Sig × List Stmt)) :=
  (ds.funcs.filter fun x => (sigOf x.2).isSome).mapM fun x => lowerOne K (progSigs ds) x.1 x.2

/-- Every lowered body fits the scope gc's rewrite gives it in the program. -/
def fitsAll (fs : List (String × Sig × List Stmt)) : Bool :=
  fs.all fun d => fitsSs (gcScope (progOf fs) "main" 0 none [] d.2.1 d.2.2) d.2.2

/-- The program's lowering: see the module docstring. -/
def lowerProg (K : Nat) (ds : Spec.Decls) : Option Program :=
  if namesOK ds then
    match lowerFuns K ds with
    | some fs => if fitsAll fs then some (progOf fs) else none
    | none => none
  else none

/-- What a lowering that succeeds gives: the checks passed, and the lowered functions. -/
theorem lowerProg_some {K : Nat} {ds : Spec.Decls} {p : Program} (h : lowerProg K ds = some p) :
    ∃ fs, namesOK ds = true ∧ lowerFuns K ds = some fs ∧ fitsAll fs = true ∧ p = progOf fs := by
  unfold lowerProg at h
  split at h
  · rename_i hok
    split at h
    · rename_i fs hfs
      split at h
      · rename_i hfit; cases h; exact ⟨fs, hok, hfs, hfit, rfl⟩
      · cases h
    · cases h
  · cases h

/-! ## `buildCtx` of a lowered program -/

theorem forIn_funs (ds : List (String × Sig × List Stmt)) (h : ∀ d ∈ ds, d.1 ≠ "init") (i : Nat)
    (init : Std.HashMap String Obj × Std.HashMap String Method × Array VarDecl × Array (Nat × Option (List Stmt))) :
    (forIn (m := Id) ds init fun a y =>
          if a.fst = "init" then
            pure (ForInStep.yield (y.fst, y.snd.fst, y.snd.snd.fst, y.snd.snd.snd.push (i, some a.snd.snd)))
          else
            pure
              (ForInStep.yield
                (y.fst.insert a.fst (Obj.func i [] a.snd.fst (some a.snd.snd)), y.snd.fst, y.snd.snd.fst,
                  y.snd.snd.snd))).run.fst =
      ds.foldl (fun m d => m.insert d.1 (Obj.func i [] d.2.1 (some d.2.2))) init.fst := by
  induction ds generalizing init with
  | nil => rfl
  | cons d ds ih =>
    simp only [List.forIn_cons, List.foldl_cons]
    have hd : d.1 ≠ "init" := h d (List.mem_cons_self ..)
    simp only [hd, ite_false]
    exact ih (fun e he => h e (List.mem_cons_of_mem _ he)) _

theorem buildPkg_objs (names : Std.HashMap String String) (fs : List (String × Sig × List Stmt))
    (h : ∀ d ∈ fs, d.1 ≠ "init") :
    (buildPkg names ⟨"main", "main", false, [⟨"main.go", [], fs.map funDecl⟩]⟩).objs =
      fs.foldl (fun m d => m.insert d.1 (Obj.func 0 [] d.2.1 (some d.2.2))) ∅ := by
  simp [buildPkg, funDecl]
  exact forIn_funs fs h 0 _

/-! ## Lookups in the tables -/

theorem foldl_insert_not_mem (g : α → Obj) (key : α → String) (ds : List α) (m : Std.HashMap String Obj)
    (x : String) (h : ∀ d ∈ ds, key d ≠ x) :
    (ds.foldl (fun m d => m.insert (key d) (g d)) m)[x]? = m[x]? := by
  induction ds generalizing m with
  | nil => rfl
  | cons d ds ih =>
    simp only [List.foldl_cons]
    rw [ih _ (fun e he => h e (List.mem_cons_of_mem _ he)), Std.HashMap.getElem?_insert]
    have : (key d == x) = false := by simp [h d (List.mem_cons_self ..)]
    simp [this]

theorem foldl_insert_mem (g : α → Obj) (key : α → String) (ds : List α) (m : Std.HashMap String Obj)
    (hn : (ds.map key).Nodup) (d : α) (hd : d ∈ ds) :
    (ds.foldl (fun m d => m.insert (key d) (g d)) m)[key d]? = some (g d) := by
  induction ds generalizing m with
  | nil => cases hd
  | cons e es ih =>
    simp only [List.foldl_cons]
    simp only [List.map_cons, List.nodup_cons] at hn
    rcases List.mem_cons.mp hd with rfl | he
    · rw [foldl_insert_not_mem _ _ _ _ _ (fun e' he' heq => hn.1 (by rw [← heq]; exact List.mem_map_of_mem he'))]
      simp
    · exact ih _ hn.2 he

theorem mapM_some_mem {f : α → Option β} {xs : List α} {ys : List β} (h : xs.mapM f = some ys) {x : α}
    (hx : x ∈ xs) : ∃ y ∈ ys, f x = some y := by
  induction xs generalizing ys with
  | nil => cases hx
  | cons a as ih =>
    simp only [List.mapM_cons, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq] at h
    obtain ⟨b, hb, bs, hbs, rfl⟩ := h
    rcases List.mem_cons.mp hx with rfl | hx
    · exact ⟨b, List.mem_cons_self .., hb⟩
    · obtain ⟨y, hy, hy'⟩ := ih hbs hx
      exact ⟨y, List.mem_cons_of_mem _ hy, hy'⟩

theorem mapM_some_mem' {f : α → Option β} {xs : List α} {ys : List β} (h : xs.mapM f = some ys) {y : β}
    (hy : y ∈ ys) : ∃ x ∈ xs, f x = some y := by
  induction xs generalizing ys with
  | nil => simp at h; subst h; cases hy
  | cons a as ih =>
    simp only [List.mapM_cons, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq] at h
    obtain ⟨b, hb, bs, hbs, rfl⟩ := h
    rcases List.mem_cons.mp hy with rfl | hy
    · exact ⟨a, List.mem_cons_self .., hb⟩
    · obtain ⟨x, hx, hx'⟩ := ih hbs hy
      exact ⟨x, List.mem_cons_of_mem _ hx, hx'⟩

theorem mapM_some_map {f : α → Option β} {xs : List α} {ys : List β} (h : xs.mapM f = some ys)
    (k : α → γ) (k' : β → γ) (hk : ∀ x y, f x = some y → k' y = k x) : ys.map k' = xs.map k := by
  induction xs generalizing ys with
  | nil => simp at h; subst h; rfl
  | cons a as ih =>
    simp only [List.mapM_cons, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq] at h
    obtain ⟨b, hb, bs, hbs, rfl⟩ := h
    simp [hk a b hb, ih hbs]

theorem lowerOne_fst {K : Nat} {sg : Sigs} {f : String} {F : Spec.Func} {y : String × Sig × List Stmt}
    (h : lowerOne K sg f F = some y) : y.1 = f := by
  unfold lowerOne at h
  simp only [Option.bind_eq_bind, Option.bind_eq_some_iff] at h
  obtain ⟨_, _, lb, _, h⟩ := h
  split at h
  · cases h; rfl
  · cases h

theorem lowerOne_some {K : Nat} {sg : Sigs} {f : String} {F : Spec.Func} {y : String × Sig × List Stmt}
    (h : lowerOne K sg f F = some y) : ∃ xks ρ, sigOf F = some (xks, ρ) ∧ y.2.1 = lowerSigOf xks ρ ∧
      lowerFun K sg f F = some y.2.2 ∧ body3Ss y.2.2 = true := by
  unfold lowerOne at h
  simp only [Option.bind_eq_bind, Option.bind_eq_some_iff] at h
  obtain ⟨⟨xks, ρ⟩, hs, lb, hl, h⟩ := h
  split at h
  · rename_i hb; cases h; exact ⟨xks, ρ, hs, rfl, hl, hb⟩
  · cases h

theorem lookup_mem {l : List (String × β)} {a : String} {b : β} (h : l.lookup a = some b) :
    (a, b) ∈ l := by
  induction l with
  | nil => cases h
  | cons kv l ih =>
    obtain ⟨k, v⟩ := kv
    simp only [List.lookup] at h
    by_cases hk : a = k
    · subst hk; simp at h; subst h; exact List.mem_cons_self ..
    · have : (a == k) = false := by simp [hk]
      rw [this] at h
      exact List.mem_cons_of_mem _ (ih h)

/-- The context of a lowered program: its package `main` holds every lowered function. -/
theorem buildCtx_progOf (fs : List (String × Sig × List Stmt)) :
    (buildCtx (progOf fs)).pkgs.get? "main" =
      some (buildPkg ((∅ : Std.HashMap String String).insert "main" "main")
        ⟨"main", "main", false, [⟨"main.go", [], fs.map funDecl⟩]⟩) := by
  simp [buildCtx, progOf]

/-- **`FunRel` for lowered programs.** The context GoCore builds from a lowered program
    (`buildCtx`) meets every side condition of `FunRel`, with the program's own
    signatures: its package is not the standard library's, no function is a temporary's,
    none shadows a name the lowering uses, and each function of the lowered kinds is
    declared with its signature and the body `lowerFun` gives it. -/
theorem lowerProg_funRel {K : Nat} {ds : Spec.Decls} {p : Program} (h : lowerProg K ds = some p) :
    FunRel K (progSigs ds) ds (buildCtx p) "main" := by
  obtain ⟨fs, hok, hfs, -, rfl⟩ := lowerProg_some h
  simp only [namesOK, Bool.and_eq_true, Bool.not_eq_true', decide_eq_true_eq, List.any_eq_false] at hok
  obtain ⟨⟨hnd, hres⟩, htmp⟩ := hok
  -- the lowered functions' names are the kinds-filtered functions' names
  have hkeys : fs.map (·.1) = (ds.funcs.filter fun x => (sigOf x.2).isSome).map (·.1) :=
    mapM_some_map hfs _ _ fun x y hy => lowerOne_fst hy
  have hnd' : (fs.map (·.1)).Nodup := by
    rw [hkeys]; exact (List.Sublist.map _ List.filter_sublist).nodup hnd
  have hnames : ∀ d ∈ fs, d.1 ∈ ds.funcs.map (·.1) := by
    intro d hd
    have : d.1 ∈ fs.map (·.1) := List.mem_map_of_mem hd
    rw [hkeys] at this
    exact (List.Sublist.map _ List.filter_sublist).subset this
  have hinit : ∀ d ∈ fs, d.1 ≠ "init" := fun d hd he =>
    hres _ (hnames d hd) (by simp [he, reservedNames])
  refine ⟨by decide, ?_, ?_⟩
  · -- no called name is a temporary's
    intro f hf
    unfold progSigs at hf
    rcases hF : ds.funcs.lookup f with _ | F
    · simp [hF] at hf
    have hm : f ∈ ds.funcs.map (·.1) := List.mem_map.mpr ⟨(f, F), lookup_mem hF, rfl⟩
    simpa using htmp f hm
  refine ⟨_, buildCtx_progOf fs, ?_, ?_⟩
  · intro x hx
    have hx' : reservedNames.contains x = true := by
      simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
      rcases hx with rfl | rfl | rfl | rfl | rfl <;> decide
    rw [Std.HashMap.get?_eq_getElem?, buildPkg_objs _ fs hinit,
      foldl_insert_not_mem _ _ _ _ _ fun d hd he => hres _ (hnames d hd) (by rw [show d.1 = x from he]; exact hx')]
    simp
  · intro f sig hsig
    unfold progSigs at hsig
    simp only [Option.bind_eq_some_iff, Option.map_eq_some_iff] at hsig
    obtain ⟨F, hF, ⟨xks, ρ⟩, hs, rfl⟩ := hsig
    have hmem : (f, F) ∈ ds.funcs.filter fun x => (sigOf x.2).isSome :=
      List.mem_filter.mpr ⟨lookup_mem hF, by simp [hs]⟩
    obtain ⟨y, hy, hly⟩ := mapM_some_mem hfs hmem
    obtain ⟨xks', ρ', hs', hsig', hl, -⟩ := lowerOne_some hly
    rw [hs] at hs'
    cases hs'
    have hy1 : y.1 = f := lowerOne_fst hly
    refine ⟨F, xks, ρ, y.2.2, 0, hF, hs, rfl, hl, ?_⟩
    rw [Std.HashMap.get?_eq_getElem?, buildPkg_objs _ fs hinit, ← hy1,
      foldl_insert_mem _ _ _ _ hnd' y hy, hsig']

/-! ## gc's order leaves a lowered program as written -/

open Hoist in
theorem mapM_run_id {σ α : Type} (f : α → StateM σ α) :
    ∀ (xs : List α), (∀ x ∈ xs, ∀ h, ((f x).run h).1 = x) → ∀ h, ((xs.mapM f).run h).1 = xs
  | [], _, _ => rfl
  | a :: as, hf, h => by
    simp only [List.mapM_cons, StateT.run_bind, StateT.run_pure]
    have h1 := hf a (List.mem_cons_self ..) h
    have h2 := mapM_run_id f as (fun x hx => hf x (List.mem_cons_of_mem _ hx)) ((f a).run h).2
    show (_ :: _) = _
    rw [h1, h2]

/-- A lowered program whose bodies are `Body3` and fit gc's scope: gc's model of the order
    of evaluation (`Order.gc`) leaves it as written, every body in its place. -/
theorem gc_progOf (fs : List (String × Sig × List Stmt))
    (hb : ∀ d ∈ fs, Body3 d.2.2) (hf : ∀ d ∈ fs, FitsSs (gcScope (progOf fs) "main" 0 none [] d.2.1 d.2.2) d.2.2) :
    Order.gc (progOf fs) = progOf fs := by
  have hdecls : ∀ h, ((List.mapM (Hoist.hDecl (buildCtx (progOf fs)) (Hoist.structFields (buildCtx (progOf fs)))
      (Hoist.allMethods (buildCtx (progOf fs))) "main" 0) (fs.map funDecl)).run h).1 = fs.map funDecl := by
    refine mapM_run_id _ _ ?_
    intro x hx h
    obtain ⟨d, hd, rfl⟩ := List.mem_map.mp hx
    simp only [funDecl, Hoist.hDecl, StateT.run_bind, StateT.run_pure]
    have hid := hSs_noTrig _ d.2.2 (noTrigSs_of _ d.2.2 (hb d hd) (hf d hd)) h
    rw [hid]
    rfl
  simp only [Order.gc, Hoist.gcOrder, StateT.run']
  generalize buildCtx (progOf fs) = c at hdecls ⊢
  unfold progOf
  simp only [List.zipIdx_cons, List.zipIdx_nil, List.mapM_cons, List.mapM_nil, 
    bind_pure_comp, map_pure]
  have h0 := hdecls {}
  generalize List.mapM (Hoist.hDecl c (Hoist.structFields c) (Hoist.allMethods c) "main" 0)
    (List.map funDecl fs) = m at h0 ⊢
  simp only [StateT.run] at h0
  show Program.mk [⟨"main", "main", false, [⟨"main.go", [], (m {}).1⟩]⟩] = _
  rw [h0]

/-- **gc's model leaves every lowered program as written.** The program `lowerProg` gives
    has `Body3` bodies (checked by `lowerOne`) that fit gc's scope (checked by `fitsAll`,
    sound by `fitsSs_sound`), so `Order.gc` returns it unchanged (`gc_runs3`'s argument, for
    the whole program). -/
theorem lowerProg_gc {K : Nat} {ds : Spec.Decls} {p : Program} (h : lowerProg K ds = some p) :
    Order.gc p = p := by
  obtain ⟨fs, -, hfs, hfit, rfl⟩ := lowerProg_some h
  refine gc_progOf fs (fun d hd => ?_) (fun d hd => fitsSs_sound _ _ (List.all_eq_true.mp hfit d hd))
  unfold lowerFuns at hfs
  obtain ⟨x, hx, hxy⟩ := mapM_some_mem' hfs hd
  obtain ⟨_, _, -, -, -, hb⟩ := lowerOne_some hxy
  exact hb


end Gala.M5
