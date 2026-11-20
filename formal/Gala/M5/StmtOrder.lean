/-
  # M5, step 2b: the orders step 2's statements allow

  docs/FORMAL_SEMANTICS.MD, "Evaluation order". An order of evaluation is a rewrite of the
  program before it runs (`Order`). Step 2's theorem (`Gala/M5/Stmt.lean`) is about the
  lowered statements as written; `RunsStep2AsWritten` is the hypothesis on an order that
  says it runs them as written. No theorem consumes it yet: linking a function body to a
  run of the program (`runProgramAt ord`) needs calls, step 3, whose theorem will take it
  (widened to the whole program). It only constrains step 2's function bodies without a
  shift (`Step2Body`). Both orders GoCore has satisfy it: left to right (the identity),
  and GoCore's model of gc 1.25.5's order (`Order.gc`, the rewrite in
  `Gala/GoCore/Hoist.lean`), which rewrites a statement only where its operands' order can
  be observed (`Hoist.triggers`), and on these statements it cannot be: they have no
  calls, nothing in them changes state, and the only panic is an integer division by
  zero, whose text is the same wherever it happens.

  A shift can panic with another text (a negative count), so a statement with a shift
  and a division can trigger the rewrite. Bodies with a shift are covered by
  `stmts_correct` for GoCore's evaluator, left to right, but are outside `Step2Body`:
  whether gc's model or real gc runs them as written is carried to step 3.

  This is narrower than every order the Go spec allows. An order that copies an `&&` or
  `||` into a temporary before the statement, as gc's order.go does, rewrites step 2's
  statements and is outside the predicate, although in this fragment the copy cannot be
  observed. That such full hoisting agrees with left to right is the obligation step 3
  carries (with `untriggered_orders_agree`); the differential run tests the gc model
  against `go run` meanwhile.
-/
import Gala.M5.Stmt
import Gala.GoCore.Hoist

namespace Gala.M5

open Gala.GoCore.Full
open Gala.GoCore.Full.Hoist

/-! ## The lowered expressions: no calls, and only divisions panic -/

/-- The shape of a lowered call-free expression in GoCore. -/
def CF : Expr → Prop
  | .ident _ | .intLit _ | .strLit _ => True
  | .paren x => CF x
  | .unary op x => op ≠ "<-" ∧ CF x
  | .binary op x y => op ≠ "<<" ∧ op ≠ ">>" ∧ CF x ∧ CF y
  | _ => False

/-- No shift in a GoCore expression: a shift can panic with another text than a division,
    which can trigger gc's rewrite, so the order result leaves bodies with shifts out. -/
def eNoShift : Expr → Bool
  | .paren x => eNoShift x
  | .unary _ x => eNoShift x
  | .binary op x y => !isShift op && eNoShift x && eNoShift y
  | _ => true

mutual
/-- No shift in a lowered statement. -/
def sNoShift : Stmt → Bool
  | .decl (.value false [.mk _ none [e]]) => eNoShift e
  | .block ss => ssNoShift ss
  | .if_ none c b els => eNoShift c && ssNoShift b && osNoShift els
  | _ => true
def ssNoShift : List Stmt → Bool
  | [] => true
  | s :: ss => sNoShift s && ssNoShift ss
def osNoShift : Option Stmt → Bool
  | none => true
  | some s => sNoShift s
end

theorem lowerE_CF (κ : String → Option Kind) : ∀ (e : Spec.Expr) (k : Kind) (le : Expr),
    lowerE κ e = some (k, le) → eNoShift le = true → CF le
  | .lit l, k, le, h, _ => by
    simp only [lowerE, Option.some.injEq] at h
    cases l with
    | int v =>
      simp only [lowerLit, Prod.mk.injEq] at h; obtain ⟨-, rfl⟩ := h
      split <;> simp [CF]
    | bool b => simp only [lowerLit, Prod.mk.injEq] at h; obtain ⟨-, rfl⟩ := h; trivial
    | str s => simp only [lowerLit, Prod.mk.injEq] at h; obtain ⟨-, rfl⟩ := h; trivial
  | .name x, k, le, h, _ => by
    simp only [lowerE] at h
    split at h
    · cases h
    simp only [Option.map_eq_some_iff, Prod.mk.injEq] at h
    obtain ⟨_, _, _, rfl⟩ := h; trivial
  | .paren x, k, le, h, hn => by
    simp only [lowerE, Option.map_eq_some_iff] at h
    obtain ⟨⟨k', lx⟩, hx, he⟩ := h
    simp only [Prod.mk.injEq] at he; obtain ⟨-, rfl⟩ := he
    exact lowerE_CF κ x k' lx hx hn
  | .unary op x, k, le, h, hn => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨⟨kx, lx⟩, hx, k', hu, he⟩ := h
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at he
    obtain ⟨-, rfl⟩ := he
    refine ⟨?_, lowerE_CF κ x kx lx hx hn⟩
    intro hop; subst hop; simp [unaryKind] at hu
  | .binary op l r, k, le, h, hn => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨⟨kl, ll⟩, hll, ⟨kr, lr⟩, hlr, k', hb, he⟩ := h
    split at he
    · cases he
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at he
    obtain ⟨-, rfl⟩ := he
    simp only [eNoShift, isShift, Bool.and_eq_true, Bool.not_eq_true', Bool.or_eq_false_iff,
      beq_eq_false_iff_ne] at hn
    obtain ⟨⟨⟨h1, h2⟩, hnl⟩, hnr⟩ := hn
    exact ⟨h1, h2, lowerE_CF κ l kl ll hll hnl, lowerE_CF κ r kr lr hlr hnr⟩
  | .call .., _, _, h, _ | .select .., _, _, h, _ | .tuple _, _, _, h, _ | .ifExpr .., _, _, h, _
  | .mtch .., _, _, h, _ | .block _, _, _, h, _ | .ifStmt .., _, _, h, _ | .ret _, _, _, h, _
  | .assign .., _, _, h, _ | .incdec .., _, _, h, _ | .decl .., _, _, h, _ | .interp _, _, _, h, _
  | .lambda .., _, _, h, _ | .named .., _, _, h, _ | .unsupported _, _, _, h, _ => by simp [lowerE] at h

/-- The rewrite with hoisting off leaves a lowered expression as it is. -/
theorem hE_off (s : Scope) (top : Bool) : ∀ (e : Expr), CF e → ∀ h, (hE s false top e).run h = (e, h)
  | .ident _, _, h | .intLit _, _, h | .strLit _, _, h => by simp [hE]; rfl
  | .paren x, hc, h => by
    simp only [hE]
    show (do let x' ← hE s false top x; pure (Expr.paren x')).run h = _
    simp only [StateT.run_bind, hE_off s top x hc h]; rfl
  | .unary op x, ⟨hop, hc⟩, h => by
    rw [hE]
    simp only [StateT.run_bind, hE_off s false x hc h, Bool.false_and]
    rfl
  | .binary op x y, ⟨_, _, hx, hy⟩, h => by
    rw [hE]
    simp [hE_off s false x hx h]
    show (fun p => (Expr.binary op x p.fst, p.snd)) <$> (hE s false false y).run h = _
    rw [hE_off s false y hy h]; rfl

/-! ## Their events never trigger the rewrite -/

/-- A panic of a division by zero, or none. -/
def divOnly : Option PK → Bool
  | none | some .div => true
  | _ => false

mutual
/-- Events of an evaluation that changes no state and panics only on division by zero. -/
def goodEv : Ev → Bool
  | .read _ pk => divOnly pk
  | .slot _ => true
  | .node eff pks inner => !eff && pks.all (divOnly ∘ some) && goodEvs inner
def goodEvs : List Ev → Bool
  | [] => true
  | e :: es => goodEv e && goodEvs es
end

theorem goodEvs_append : ∀ (a b : List Ev), goodEvs (a ++ b) = (goodEvs a && goodEvs b)
  | [], b => by simp [goodEvs]
  | e :: a, b => by simp [goodEvs, goodEvs_append a b, Bool.and_assoc]

theorem innerEffect_good : ∀ (evs : List Ev), goodEvs evs = true → innerEffect evs = false
  | [], _ => rfl
  | e :: es, h => by
    simp only [goodEvs, Bool.and_eq_true] at h
    have ih := innerEffect_good es h.2
    simp only [innerEffect, List.any_cons] at ih ⊢
    rw [ih]
    cases e with
    | node eff _ _ => simp only [goodEv, Bool.and_eq_true, Bool.not_eq_true'] at h; simp [h.1.1.1]
    | _ => rfl

theorem innerPanics_good : ∀ (evs : List Ev), goodEvs evs = true →
    (innerPanics evs).all (divOnly ∘ some) = true
  | [], _ => rfl
  | e :: es, h => by
    simp only [goodEvs, Bool.and_eq_true] at h
    have ih := innerPanics_good es h.2
    simp only [innerPanics, List.flatMap_cons, List.all_append] at ih ⊢
    refine Bool.and_eq_true_iff.mpr ⟨?_, ih⟩
    cases e with
    | read ch pk =>
      simp only [goodEv] at h
      cases pk with
      | none => rfl
      | some p => simp [divOnly] at h ⊢; cases p <;> simp_all
    | slot _ => rfl
    | node eff pks inner => simp only [goodEv, Bool.and_eq_true] at h; exact h.1.1.2

theorem divOnly_some {p : PK} (h : divOnly (some p) = true) : p = .div := by
  cases p <;> simp_all [divOnly]

/-- No good event's level triggers: no O-node in it changes state, and every panic in it
    is a division's. -/
theorem triggers_good : ∀ (evs : List Ev), goodEvs evs = true → triggers evs = false
  | [], _ => by unfold triggers; rfl
  | e :: es, h => by
    simp only [goodEvs, Bool.and_eq_true] at h
    obtain ⟨he, hes⟩ := h
    have hmem : ∀ ev ∈ es, goodEv ev = true := by
      intro ev hev
      clear he
      induction es with
      | nil => cases hev
      | cons a as ih =>
        simp only [goodEvs, Bool.and_eq_true] at hes
        rcases List.mem_cons.mp hev with rfl | hm
        · exact hes.1
        · exact ih hes.2 hm
    unfold triggers
    rw [triggers_good es hes]
    cases e with
    | read ch pk =>
      simp only [goodEv] at he
      simp only [Bool.or_false]
      apply Bool.eq_false_iff.mpr
      intro hc
      simp only [Bool.and_eq_true, List.any_eq_true] at hc
      obtain ⟨-, ev, hev, hp⟩ := hc
      have hg := hmem ev hev
      cases ev with
      | node eff pks inner =>
        simp only [goodEv, Bool.and_eq_true, Bool.not_eq_true'] at hg
        obtain ⟨⟨rfl, hpks⟩, -⟩ := hg
        cases pk with
        | none => simp at hp
        | some p =>
          have := divOnly_some he; subst this
          simp only [Bool.false_and, Bool.false_or, List.any_eq_true] at hp
          obtain ⟨q, hq, hd⟩ := hp
          have := divOnly_some (List.all_eq_true.mp hpks q hq); subst this
          cases hd
      | _ => simp at hp
    | slot _ => rfl
    | node eff pks inner =>
      simp only [goodEv, Bool.and_eq_true] at he
      simp [triggers_good inner he.2]

theorem slotEv_good (s : Scope) (e : Expr) (evs : List Ev) : goodEvs (slotEv s e evs) = true := by
  unfold slotEv; split
  · rfl
  · split <;> rfl

theorem scanE_good (s : Scope) : ∀ (top : Bool) (e : Expr), CF e → goodEvs (scanE s top e) = true
  | _, .ident _, _ => by simp [scanE, goodEvs, goodEv, divOnly]
  | _, .intLit _, _ | _, .strLit _, _ => by simp [scanE, goodEvs]
  | top, .paren x, hc => by simp only [scanE]; exact scanE_good s top x hc
  | _, .unary op x, ⟨hop, hc⟩ => by
    have : (op == "<-") = false := by simpa using hop
    simp only [scanE, this, Bool.false_eq_true, ite_false]
    exact scanE_good s false x hc
  | _, .binary op x y, ⟨h1, h2, hx, hy⟩ => by
    have gx := scanE_good s false x hx
    have gy := scanE_good s false y hy
    simp only [scanE]
    split
    · simp [goodEvs, gx, gy, mkNode, goodEv, innerEffect_good, innerPanics_good]
    · have hsh : (op == "<<" || op == ">>") = false := by simp [h1, h2]
      simp only [hsh, Bool.false_and, Bool.false_eq_true, ite_false, goodEvs_append, gx, gy,
        Bool.true_and]
      split <;> (try split) <;> simp [goodEvs, goodEv, divOnly, slotEv_good]

theorem triggers_CF (s : Scope) (top : Bool) (e : Expr) (h : CF e) : triggers (scanE s top e) = false :=
  triggers_good _ (scanE_good s top e h)

theorem blocked_CF (s : Scope) (top : Bool) (e : Expr) (h : CF e) : blocked (scanE s top e) = none := by
  simp [blocked, triggers_CF s top e h]

/-! ## The rewrite leaves step 2's statements as they are -/

theorem isolated_run {α : Type} (m : HM α) (a : α) (hm : ∀ h, m.run h = (a, h)) (h : HState) :
    (isolated m).run h = ((a, []), h) := by
  simp [isolated, hm]
  rfl

theorem lowerX_CF {κ : String → Option Kind} {e : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerX κ e = some (k, le)) (hn : eNoShift le = true) : CF le :=
  lowerE_CF κ e k le (lowerX_some h).2 hn

theorem hOperands_one (s : Scope) (top : Bool) (le : Expr) (hc : CF le) (h : HState) :
    (hOperands s false top [le]).run h = ([le], h) := by
  simp only [hOperands, hEs, StateT.run_bind, List.isEmpty_nil, Bool.and_true]
  rw [hE_off s top le hc h]
  rfl

theorem hS_decl (s : Scope) (x : String) (le : Expr) (hc : CF le) (h : HState) :
    (hS s (.decl (.value false [.mk [x] none [le]]))).run h =
      (([.decl (.value false [.mk [x] none [le]])], s.bind [x]), h) := by
  simp only [hS, hSpecs, scanOperands]
  simp only [blocked_CF s true le hc, triggers_CF s true le hc, StateT.run_bind]
  rw [isolated_run _ [le] (hOperands_one s true le hc) h]
  rfl

private theorem id_bind_pure {α β : Type} (x : Id α) (g : α → β) :
    (x >>= fun p => pure (g p) : Id β) = g x := rfl

private theorem id_bind' {α β : Type} (a : α) (f : α → Id β) : (a >>= f : Id β) = f a := rfl

theorem hS_block (s : Scope) (ss : List Stmt) (h : HState) (hss : (hSs s ss).run h = (ss, h)) :
    (hS s (.block ss)).run h = (([.block ss], s), h) := by
  simp only [hS, StateT.run_bind, hss]
  rfl

theorem hS_if (s : Scope) (lc : Expr) (la : List Stmt) (els : Option Stmt) (h : HState)
    (hc : CF lc) (hb : (hSs s la).run h = (la, h))
    (he : ∃ s', (hOS s els).run h = ((els.toList, s'), h)) :
    (hS s (.if_ none lc la els)).run h = (([.if_ none lc la els], s), h) := by
  obtain ⟨s', he⟩ := he
  simp only [hS, hOS, StateT.run_bind, StateT.run_pure, id_bind', id_pure]
  simp only [blocked_CF s false lc hc, triggers_CF s false lc hc, StateT.run_bind,
    isolated_run _ lc (hE_off s false lc hc), id_bind', hb, he]
  cases els <;> rfl

/-- gc's rewrite at nesting bound `N`: a lowered statement list, and a lowered statement. -/
def HoistSs (N : Nat) : Prop :=
  ∀ κ ss κ' lss, lowerSs κ ss = some (κ', lss) → ssNoShift lss = true → sdepthL ss ≤ N → ∀ s h,
    (hSs s lss).run h = (lss, h)

def HoistS (N : Nat) : Prop :=
  ∀ κ st κ' l, lowerS κ st = some (κ', l) → sNoShift l = true → sdepth st ≤ N → ∀ s h,
    ∃ s', (hS s l).run h = (([l], s'), h)

theorem hoistS_of (N : Nat) (ih : ∀ m < N, HoistSs m ∧ HoistS m) : HoistS N := by
  intro κ st κ' l hl hns hN s h
  match st, hl with
  | .decl m [x] false none [e], hl =>
    simp only [lowerS] at hl
    split at hl
    · cases hl
    simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨⟨k, le⟩, hlx, -, rfl⟩ := hl
    exact ⟨_, hS_decl s x le (lowerX_CF hlx (by simpa [sNoShift] using hns)) h⟩
  | .block ss, hl =>
    simp only [lowerS, Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, lb⟩, hlb, -, rfl⟩ := hl
    exact ⟨s, hS_block s lb h ((ih (N - 1) (by simp [sdepth] at hN; omega)).1 κ ss κ₁ lb hlb
      (by simpa [sNoShift] using hns) (by simp [sdepth] at hN; omega) s h)⟩
  | .ifStmt c a els, hl =>
    obtain ⟨-, as, lc, κ₁, la, le, rfl, hlc, hla, hle, rfl⟩ := lowerS_if hl
    have hN' : N - 1 < N := by simp [sdepth] at hN; omega
    simp only [sNoShift, Bool.and_eq_true] at hns
    obtain ⟨⟨hnc, hna⟩, hne⟩ := hns
    have hb := (ih (N - 1) hN').1 κ as κ₁ la hla hna (by simp [sdepth] at hN; omega) s h
    refine ⟨s, hS_if s lc la le h (lowerX_CF hlc hnc) hb ?_⟩
    cases els with
    | none =>
      simp only [lowerElse, Option.some.injEq] at hle
      subst hle; exact ⟨s, rfl⟩
    | some e =>
      match e, hle with
      | .block bs, hle =>
        simp only [lowerElse, Option.map_eq_some_iff] at hle
        obtain ⟨⟨κ₂, lb⟩, hlb, rfl⟩ := hle
        obtain ⟨s', hs'⟩ := (ih (N - 1) hN').2 κ (.block bs) κ (.block lb)
          (by simp [lowerS, hlb]) (by simpa [osNoShift] using hne) (by simp [sdepth, sdepthO] at hN ⊢; omega) s h
        exact ⟨s', hs'⟩
      | .ifStmt c₂ a₂ els₂, hle =>
        simp only [lowerElse, Option.map_eq_some_iff] at hle
        obtain ⟨⟨κ₂, l₂⟩, hl₂, rfl⟩ := hle
        obtain ⟨s', hs'⟩ := (ih (N - 1) hN').2 κ _ κ₂ l₂ hl₂ (by simpa [osNoShift] using hne)
          (by simp [sdepth, sdepthO] at hN ⊢; omega) s h
        exact ⟨s', hs'⟩
      | .lit _, hle | .name _, hle | .unary .., hle | .binary .., hle | .call .., hle
      | .select .., hle | .paren _, hle | .tuple _, hle | .ifExpr .., hle | .mtch .., hle
      | .ret _, hle | .assign .., hle | .incdec .., hle | .decl .., hle | .interp _, hle
      | .lambda .., hle | .named .., hle | .unsupported _, hle => simp [lowerElse] at hle
  | .decl _ [] _ _ _, hl | .decl _ (_ :: _ :: _) _ _ _, hl | .decl _ [_] true _ _, hl
  | .decl _ [_] false (some _) _, hl | .decl _ [_] false none [], hl
  | .decl _ [_] false none (_ :: _ :: _), hl
  | .lit _, hl | .name _, hl | .unary .., hl | .binary .., hl | .call .., hl | .select .., hl
  | .paren _, hl | .tuple _, hl | .ifExpr .., hl | .mtch .., hl | .ret _, hl | .assign .., hl
  | .incdec .., hl | .interp _, hl | .lambda .., hl | .named .., hl | .unsupported _, hl =>
    simp [lowerS] at hl

theorem hoistSs_of (N : Nat) (hS' : ∀ m < N, HoistS m) : HoistSs N := by
  intro κ ss
  induction ss generalizing κ with
  | nil =>
    intro κ' lss hl _ _ s h
    simp only [lowerSs, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨-, rfl⟩ := hl
    rfl
  | cons st rest ihl =>
    intro κ' lss hl hns hN s h
    simp only [lowerSs, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, l⟩, hst, ⟨κ₂, ls⟩, hrest, -, rfl⟩ := hl
    simp only [ssNoShift, Bool.and_eq_true] at hns
    obtain ⟨s', hs'⟩ := hS' (N - 1) (by simp [sdepthL] at hN; omega) κ st κ₁ l hst hns.1
      (by simp [sdepthL] at hN; omega) s h
    have ht := ihl κ₁ κ₂ ls hrest hns.2 (by simp [sdepthL] at hN ⊢; omega) s' h
    simp only [hSs, StateT.run_bind, hs', id_bind', ht]
    rfl

/-- **gc's rewrite leaves a lowered statement list as it is**, in every scope: no
    statement of step 2 triggers it. -/
theorem hSs_lowered (κ κ' : String → Option Kind) (ss : List Spec.Expr) (lss : List Stmt)
    (hl : lowerSs κ ss = some (κ', lss)) (hns : ssNoShift lss = true) (s : Scope) (h : HState) :
    (hSs s lss).run h = (lss, h) := by
  have hall : ∀ N, HoistSs N ∧ HoistS N := by
    intro N
    refine Nat.strongRecOn N fun N ih => ?_
    have hs : HoistS N := hoistS_of N ih
    exact ⟨hoistSs_of N fun m hm => (ih m hm).2, hs⟩
  exact (hall (sdepthL ss)).1 κ ss κ' lss hl hns (Nat.le_refl _) s h

/-! ## The orders step 2 allows -/

/-- The `i`th declaration of the `fi`th file of the `pi`th package. -/
def declAt (p : Program) (pi fi i : Nat) : Option Decl := do
  let pk ← p.packages[pi]?
  let f ← pk.files[fi]?
  f.decls[i]?

/-- A function body the reference lowering produces in step 2. -/
def Step2Body (body : List Stmt) : Prop :=
  (∃ κ ss κ', lowerSs κ ss = some (κ', body)) ∧ ssNoShift body = true

/-- **An order step 2's theorem holds for:** it runs every function body made of step 2's
    lowered statements as written, in its place. -/
def RunsStep2AsWritten (ord : Order) : Prop :=
  ∀ p pi fi i name recv tps sig body, Step2Body body →
    declAt p pi fi i = some (.func name recv tps sig (some body)) →
    declAt (ord p) pi fi i = some (.func name recv tps sig (some body))

/-- Left to right, GoCore's evaluator as it is, is such an order. -/
theorem leftToRight_runsStep2 : RunsStep2AsWritten Order.leftToRight :=
  fun _ _ _ _ _ _ _ _ _ _ h => h

theorem mapM_getElem {α β σ : Type} (f : α → StateM σ β) (P : β → Prop) :
    ∀ (xs : List α) (h : σ) (i : Nat) (x : α), xs[i]? = some x → (∀ h', P ((f x).run h').1) →
      ∃ y, ((xs.mapM f).run h).1[i]? = some y ∧ P y
  | [], _, _, _, hx, _ => by simp at hx
  | a :: as, h, i, x, hx, hP => by
    simp only [List.mapM_cons, StateT.run_bind, StateT.run_pure]
    cases i with
    | zero =>
      simp only [List.getElem?_cons_zero, Option.some.injEq] at hx
      subst hx
      exact ⟨_, rfl, hP h⟩
    | succ i =>
      simp only [List.getElem?_cons_succ] at hx
      obtain ⟨y, hy, hPy⟩ := mapM_getElem f P as ((f a).run h).2 i x hx hP
      exact ⟨y, hy, hPy⟩

/-- **GoCore's model of gc 1.25.5's order is such an order:** its rewrite
    (`Hoist.gcOrder`) leaves every function body of step 2 as it is. Real gc copies an `&&`
    or `||` into a temporary, which the model leaves out where it cannot be observed; that
    the two agree is not proved here (see the module header). -/
theorem gc_runsStep2 : RunsStep2AsWritten Order.gc := by
  intro p pi fi i name recv tps sig body ⟨⟨κ, ss, κ', hl⟩, hns⟩ hd
  simp only [declAt, Option.bind_eq_bind, Option.bind_eq_some_iff] at hd
  obtain ⟨pk, hpk, f, hf, hdi⟩ := hd
  simp only [Order.gc, gcOrder, declAt, Option.bind_eq_bind, Option.bind_eq_some_iff,
    StateT.run']
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩) (mapM_getElem (σ := HState) _ _ p.packages {} pi pk hpk ?_)
  intro h'
  simp only [StateT.run_bind, StateT.run_pure, id_bind_pure]
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩)
    (mapM_getElem (σ := HState) _ _ pk.files.zipIdx h' fi (f, fi) (by simp [List.getElem?_zipIdx, hf]) ?_)
  intro h''
  simp only [StateT.run_bind, StateT.run_pure, id_bind_pure]
  obtain ⟨d', hd', hP⟩ := mapM_getElem
    (hDecl (buildCtx p) (structFields (buildCtx p)) (allMethods (buildCtx p)) pk.path fi) (· = Decl.func name recv tps sig (some body))
    f.decls h'' i _ hdi (by
      intro h3
      simp only [hDecl, StateT.run_bind, StateT.run_pure, hSs_lowered κ κ' ss body hl hns]
      rfl)
  subst hP
  exact hd'

end Gala.M5
