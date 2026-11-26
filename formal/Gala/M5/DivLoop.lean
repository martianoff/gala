/-
  # M5, step 3d (c): divergence, loops
-/
import Gala.M5.DivTail
namespace Gala.M5
open Gala.GoCore.Full

section TmpsSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- **The temporaries of a loop step**, with any budget: galaspec's arguments ending with
    values, GoCore runs out of fuel on them, or goes on with the statements after them as
    `tmps_sim` says, within the bound; galaspec's running out of fuel on them is GoCore's. -/
theorem tmps_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (κ : String → Option Kind) (hnt : ∀ x, (κ x).isSome → isTmp x = false) :
    ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr) (i : Nat), lowerArgs K sg κ ks args = some las →
    ∀ (s : Spec.St) (g : St) (envT : Env), Inv3 cx P sg κ s.toCore g envT → TmpsFree envT i →
    ∀ M, K * d + 5 ≤ M →
    (∀ vs t, (Spec.evalList ds d args).run.run s = (.ok vs, t) →
      (∀ rest, ((execStmts cx M envT (tmpDecls i las ++ rest)).run.run g).1 = .error .fuel) ∨
      ∃ ws g' envT', (∀ rest, (execStmts cx M envT (tmpDecls i las ++ rest)).run.run g =
          (execStmts cx M envT' rest).run.run g') ∧ KRel ks vs ws ∧
        SFrame s.toCore t.toCore ∧ GFrame g g' ∧ Inv3 cx P sg κ t.toCore g' envT' ∧
        (∀ x, (∀ m, x ≠ tmpName (i + m)) → envT'.lookup x = envT.lookup x) ∧
        (∀ m (hm : m < ws.length), ∃ c, envT'.lookup (tmpName (i + m)) = some c ∧
          g'.cells[c]? = some (defaultVal ws[m])) ∧
        s.steps + Q * g'.steps ≤ t.steps + Q * g.steps + eszs args ∧ t.steps ≤ s.steps) ∧
    (s.steps ≤ d → Q * (g.steps + 1) + eszs args ≤ s.steps →
      ((Spec.evalList ds d args).run.run s).1 = .error .fuel →
      ∀ rest, ((execStmts cx M envT (tmpDecls i las ++ rest)).run.run g).1 = .error .fuel)
  | [], [], las, i, h, s, g, envT, hi, _, M, _ => by
    simp only [lowerArgs, Option.some.injEq] at h
    subst h
    rw [evalList_nil_run]
    refine ⟨fun vs t ht => ?_, fun _ _ h => by simp at h⟩
    cases ht
    exact .inr ⟨[], g, envT, fun _ => rfl, trivial, sframe_refl _, gframe_refl _, hi, fun _ _ => rfl,
      fun m hm => absurd hm (by simp), by simp [eszs], Nat.le_refl _⟩
  | k :: ks, a :: as, las, i, h, s, g, envT, hi, hfree, M, hM => by
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
          simp only [tmpDecls, List.cons_append]
          have hsimp := lowerC_simple a k' la hla
          have hcur : (envT.scopes.headD []).lookup (tmpName i) = none := by simpa using hfree 0
          have hIA := ihEF κ a k' la hla s g envT hi (m + 1) (by omega)
          rw [evalList_cons_run]
          rcases hr : (Spec.eval ds d a).run.run s with ⟨ra, t1⟩
          cases ra with
          | error e =>
            refine ⟨fun vs t ht => (by cases ht), fun hd hq hf rest => ?_⟩
            simp only at hf
            cases hf
            have h1 := hIA.2 hd (by simp only [eszs] at hq; omega) (by rw [hr])
            rcases hg : (evalExpr cx (m + 1) envT la).run.run g with ⟨rg, g1⟩
            rw [hg] at h1
            simp only at h1
            subst h1
            rw [execStmts_cons, execStmt_define_err cx m envT _ la g g1 _ (rhsSimple_ta hsimp) (rhsSimple_ix hsimp) hg]
          | ok v =>
          simp only
          rcases expr_down ds cx P K sg Q ihE ihEF hla hi (by omega : K * d ≤ m + 1) hr with
            hgf | ⟨w, g1, hg, hvw, hkv, -, hsf, hgf, hb1, hm1⟩
          · -- GoCore runs out on this argument.
            have hfu : ∀ rest, ((execStmts cx (m + 5) envT
                (.assign ":=" [.ident (tmpName i)] [la] :: (tmpDecls (i + 1) las' ++ rest))).run.run g).1 =
                .error .fuel := by
              intro rest
              rcases hg : (evalExpr cx (m + 1) envT la).run.run g with ⟨rg, g1⟩
              rw [hg] at hgf
              simp only at hgf
              subst hgf
              rw [execStmts_cons, execStmt_define_err cx m envT _ la g g1 _ (rhsSimple_ta hsimp) (rhsSimple_ix hsimp) hg]
            refine ⟨fun vs t _ => .inl hfu, fun _ _ _ => hfu⟩
          have hdef : ∀ rest, (execStmts cx (m + 5) envT
              (.assign ":=" [.ident (tmpName i)] [la] :: (tmpDecls (i + 1) las' ++ rest))).run.run g =
              (execStmts cx (m + 5) (envT.bind (tmpName i) g1.cells.size) (tmpDecls (i + 1) las' ++ rest)).run.run
                (declG w g1) := by
            intro rest
            rw [execStmts_cons, execStmt_define cx m envT _ la g g1 w (tmpName_ne_us i) hcur (rhsSimple_ta hsimp)
              (rhsSimple_ix hsimp) hg (results_of_vrel hvw)]
          have hi1 : Inv3 cx P sg κ t1.toCore (declG w g1) (envT.bind (tmpName i) g1.cells.size) :=
            inv3_tmp (inv3_frame hi hsf hgf) hnt i w
          have hgf1 : GFrame g (declG w g1) := gframe_trans hgf (gframe_push _ _ _)
          have ih := tmps_sf d ihE ihEF κ hnt ks as las' (i + 1) hlas' t1 (declG w g1) _ hi1
            (tmpsFree_bind hfree _) (m + 5) hM
          simp only [declG] at ih hgf1
          refine ⟨fun vs t ht => ?_, fun hd hq hf rest => ?_⟩
          · rcases hrs : (Spec.evalList ds d as).run.run t1 with ⟨rs, t'⟩
            rw [hrs] at ht
            cases rs with
            | error => cases ht
            | ok vs' =>
            injection ht with h1 h2
            injection h1 with h1
            subst h1 h2
            rcases ih.1 vs' t' hrs with hfu | ⟨ws, g', envT', hrest, hkr, hsf', hgf', hi', hlk, htm, hb', hm'⟩
            · exact .inl fun rest => by rw [hdef]; exact hfu rest
            have hne : ∀ m', tmpName i ≠ tmpName (i + 1 + m') := by
              intro m' he; have := tmpName_inj he; omega
            refine .inr ⟨w :: ws, g', envT', fun rest => by rw [hdef]; exact hrest rest, ⟨hvw, hkv, hkr⟩,
              sframe_trans hsf hsf', gframe_trans hgf1 hgf', hi', fun x hx => ?_, fun m' hm'' => ?_, ?_, by omega⟩
            · have hx0 : x ≠ tmpName i := by simpa using hx 0
              rw [hlk x (fun m' => by simpa [Nat.add_assoc, Nat.add_comm 1] using hx (m' + 1)),
                Env.lookup_bind _ _ _ _ (tmpName_ne_us i)]
              simp [hx0]
            · cases m' with
              | zero =>
                refine ⟨g1.cells.size, ?_, ?_⟩
                · rw [Nat.add_zero, hlk (tmpName i) hne, Env.lookup_bind _ _ _ _ (tmpName_ne_us i)]; simp
                · exact pre_get hgf'.1 (by simp)
              | succ m'' =>
                obtain ⟨c, hc, hcv⟩ := htm m'' (by simp at hm''; omega)
                refine ⟨c, by simpa [Nat.add_assoc, Nat.add_comm 1] using hc, by simpa using hcv⟩
            · simp only [eszs]; omega
          · rw [hdef]
            rcases hrs : (Spec.evalList ds d as).run.run t1 with ⟨rs, t'⟩
            rw [hrs] at hf
            cases rs with
            | ok => simp at hf
            | error =>
            simp only at hf
            cases hf
            exact ih.2 (by omega) (by simp only [eszs] at hq; rw [Nat.mul_succ] at hq ⊢; omega) (by rw [hrs]) rest
        · cases h
      · cases h
  | [], _ :: _, _, _, h, _, _, _, _, _, _, _ | _ :: _, [], _, _, h, _, _, _, _, _, _, _ => by
    simp [lowerArgs] at h

end TmpsSF

section LoopSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)
variable (f : String) (F : Spec.Func) (xks : List (String × Kind)) (ρ : Kind) (st : Stmt) (env0 : Env) (n : Nat)

/-- **The loop** (`LoopIH`'s): every iteration takes GoCore a step, which pays for the
    body's nodes galaspec evaluates in it (`KszOK`), so no size is left over. -/
def LoopSF (d : Nat) : Prop :=
  ∀ j, K * d ≤ j → ∀ (s : Spec.St) (g : St) (k : Nat), Inv3 cx P sg (paramKinds xks) s.toCore g env0 →
    ParamCells n xks env0 g → g.steps < k →
    SF Q 0 d s g ((Spec.eval ds d F.body).run.run s) ((loopM k env0.push (iterF cx j [st])).run.run g)

/-- **A part of the loop's `if` tree** (`TreeSim`'s). -/
def TreeSF (d j : Nat) : Prop :=
  ∀ e ls, lowerLoop K sg f xks ρ (paramKinds xks) e = some ls → ∀ (s : Spec.St) (g : St) (envA : Env) (k : Nat),
    Inv3 cx P sg (paramKinds xks) s.toCore g envA → (∀ x, envA.lookup x = env0.lookup x) →
    TmpsFree envA 0 → ParamCells n xks env0 g → g.steps < k → ∀ M, K * d + 5 ≤ M →
      SF Q (esz e) d s g ((Spec.eval ds d e).run.run s)
        (afterIter k env0.push (iterF cx j [st]) ((execStmts cx M envA ls).run.run g))

/-- **An `if` of the tree**, its step taken (`IfLSim`'s). -/
def IfLSF (d j : Nat) : Prop :=
  ∀ c a b lc ta tb, lowerC K sg (paramKinds xks) c = some (.bool, lc) →
    lowerLoop K sg f xks ρ (paramKinds xks) a = some ta → lowerLoop K sg f xks ρ (paramKinds xks) b = some tb →
    ∀ (s : Spec.St) (kk : Nat) (g : St) (envA : Env) (k : Nat), s.steps = kk + 1 →
    Inv3 cx P sg (paramKinds xks) s.toCore g envA → (∀ x, envA.lookup x = env0.lookup x) →
    ParamCells n xks env0 g → g.steps < k → ∀ M, K * d + 9 ≤ M →
      SF Q (esz c + esz a + esz b) d { s with steps := kk } g ((Spec.eval ds (d + 1) (.ifExpr c a b)).run.run s)
        (afterIter k env0.push (iterF cx j [st]) ((execStmts cx M envA [ifT lc ta tb]).run.run g))

theorem afterIter_fuel (k : Nat) (E : Env) (Fi : Env → GM (Option Env)) {r : Except Stop Env × St}
    (h : r.1 = .error .fuel) : (afterIter k E Fi r).1 = .error .fuel := by
  rcases r with ⟨_ | _, _⟩ <;> simp_all [afterIter]

theorem ifLSF_of (d j : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (ihT : TreeSF ds cx P K sg Q f xks ρ st env0 n d j) : IfLSF ds cx P K sg Q f xks ρ st env0 n d j := by
  intro c a b lc ta tb hc ha hb s kk g envA k hs hi henv hpc hk M hM
  obtain ⟨m, rfl⟩ : ∃ m, M = m + 5 := ⟨M - 5, by omega⟩
  rw [execStmts_single]
  have hip := inv3_push cx P sg hi
  have hIC := ihEF (paramKinds xks) c .bool lc hc { s with steps := kk } g envA.push hip (m + 3) (by omega)
  have hkk : ({ s with steps := kk } : Spec.St).steps = kk := rfl
  have henv' : ∀ x, envA.push.push.lookup x = env0.lookup x := fun x => by simp [Env.lookup_push, henv]
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hE := ihE (paramKinds xks) c .bool lc hc { s with steps := kk } g envA.push hip hsg (m + 3) (by omega)
    have hSC := hIC.1 hsg
    rcases hr : (Spec.eval ds d c).run.run { s with steps := kk } with ⟨rc, t⟩
    rw [hr] at hE hSC
    cases rc with
    | error er =>
      rw [eval_ifE_err ds d kk c a b _ (Or.inl rfl) s t er hs hr]
      cases er with
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
    obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
    obtain ⟨bv, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    rw [hg] at hSC
    simp only [Stut] at hSC
    rw [eval_ifE_ok ds d kk c a b _ (Or.inl rfl) bv s t hs hr]
    have hit : Inv3 cx P sg (paramKinds xks) t.toCore g' envA.push.push := inv3_push cx P sg (inv3_frame hip hsf hgf)
    have hstg : t.steps ≤ g'.steps := spent_le hsp hsg
    have hpc' := paramCells_mono hpc hgf.2.2.1
    have hk' : g'.steps < k := Nat.lt_of_le_of_lt hgf.2.2.2.2.1 hk
    cases bv with
    | true =>
      rw [ifT, execStmt_if_true cx (m + 2) envA lc ta _ g g' ty hg, execBlock_run]
      have h := (ihT a ta ha t g' envA.push.push k hit henv' (tmpsFree_push _ 0) hpc' hk' (m + 2) (by omega)).1 hstg
      rcases hx : (execStmts cx (m + 2) envA.push.push ta).run.run g' with ⟨rx, gx⟩
      rw [hx] at h
      cases rx <;> exact stut_mono Q (by omega) (stut_trans Q hSC h)
    | false =>
      rw [ifT, execStmt_if_false cx (m + 2) envA lc ta _ g g' ty hg]
      simp only
      rw [execStmt_block, execBlock_run, withEnv_withEnv]
      have h := (ihT b tb hb t g' envA.push.push k hit henv' (tmpsFree_push _ 0) hpc' hk' (m + 1) (by omega)).1 hstg
      rcases hx : (execStmts cx (m + 1) envA.push.push tb).run.run g' with ⟨rx, gx⟩
      rw [hx] at h
      cases rx <;> exact stut_mono Q (by omega) (stut_trans Q hSC h)
  · rw [hkk] at hd hq
    rcases hr : (Spec.eval ds d c).run.run { s with steps := kk } with ⟨rc, t⟩
    cases rc with
    | error er =>
      rw [eval_ifE_err ds d kk c a b _ (Or.inl rfl) s t er hs hr] at hf
      simp only at hf
      cases hf
      have h := hIC.2 (by rw [hkk]; omega) (by rw [hkk]; omega) (by rw [hr])
      rcases hg : (evalExpr cx (m + 3) envA.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at h
      simp only at h
      subst h
      rw [ifT, execStmt_if_err cx (m + 2) envA lc ta _ g g' _ hg]
      rfl
    | ok v =>
    rcases expr_down ds cx P K sg Q ihE ihEF (s := { s with steps := kk }) hc hip (by omega : K * d ≤ m + 3) hr with
      hgf | ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hbd, hmono⟩
    · rcases hg : (evalExpr cx (m + 3) envA.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at hgf
      simp only at hgf
      subst hgf
      rw [ifT, execStmt_if_err cx (m + 2) envA lc ta _ g g' _ hg]
      rfl
    obtain ⟨bv, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    rw [eval_ifE_ok ds d kk c a b _ (Or.inl rfl) bv s t hs hr] at hf
    have hit : Inv3 cx P sg (paramKinds xks) t.toCore g' envA.push.push := inv3_push cx P sg (inv3_frame hip hsf hgf)
    have hpc' := paramCells_mono hpc hgf.2.2.1
    have hk' : g'.steps < k := Nat.lt_of_le_of_lt hgf.2.2.2.2.1 hk
    rw [hkk] at hbd hmono
    rw [Nat.mul_succ] at hq
    cases bv with
    | true =>
      rw [ifT, execStmt_if_true cx (m + 2) envA lc ta _ g g' ty hg, execBlock_run]
      have h := (ihT a ta ha t g' envA.push.push k hit henv' (tmpsFree_push _ 0) hpc' hk' (m + 2) (by omega)).2
        (by omega) (by rw [Nat.mul_succ]; omega) hf
      rcases hx : (execStmts cx (m + 2) envA.push.push ta).run.run g' with ⟨rx, gx⟩
      rw [hx] at h
      cases rx <;> exact h
    | false =>
      rw [ifT, execStmt_if_false cx (m + 2) envA lc ta _ g g' ty hg]
      simp only
      rw [execStmt_block, execBlock_run, withEnv_withEnv]
      have h := (ihT b tb hb t g' envA.push.push k hit henv' (tmpsFree_push _ 0) hpc' hk' (m + 1) (by omega)).2
        (by omega) (by rw [Nat.mul_succ]; omega) hf
      rcases hx : (execStmts cx (m + 1) envA.push.push tb).run.run g' with ⟨rx, gx⟩
      rw [hx] at h
      cases rx <;> exact h

end LoopSF

section TmpsStut
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- The temporaries with at least galaspec's budget: `tmps_sim`'s result, within the bound. -/
theorem tmps_stut (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (κ : String → Option Kind) (hnt : ∀ x, (κ x).isSome → isTmp x = false) :
    ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr) (i : Nat), lowerArgs K sg κ ks args = some las →
    ∀ (s : Spec.St) (g : St) (envT : Env), Inv3 cx P sg κ s.toCore g envT → s.steps ≤ g.steps → TmpsFree envT i →
    ∀ M, K * d + 5 ≤ M → ∀ vs t, (Spec.evalList ds d args).run.run s = (.ok vs, t) →
      ∃ ws g' envT', (∀ rest, (execStmts cx M envT (tmpDecls i las ++ rest)).run.run g =
          (execStmts cx M envT' rest).run.run g') ∧ KRel ks vs ws ∧
        SFrame s.toCore t.toCore ∧ GFrame g g' ∧ Spent s t g g' ∧ Inv3 cx P sg κ t.toCore g' envT' ∧
        (∀ x, (∀ m, x ≠ tmpName (i + m)) → envT'.lookup x = envT.lookup x) ∧
        (∀ m (hm : m < ws.length), ∃ c, envT'.lookup (tmpName (i + m)) = some c ∧
          g'.cells[c]? = some (defaultVal ws[m])) ∧
        s.steps + Q * g'.steps ≤ t.steps + Q * g.steps + eszs args
  | [], [], las, i, h, s, g, envT, hi, _, _, M, _, vs, t, ht => by
    simp only [lowerArgs, Option.some.injEq] at h
    subst h
    rw [evalList_nil_run] at ht
    cases ht
    exact ⟨[], g, envT, fun _ => rfl, trivial, sframe_refl _, gframe_refl _, by unfold Spent; omega, hi,
      fun _ _ => rfl, fun m hm => absurd hm (by simp), by simp [eszs]⟩
  | k :: ks, a :: as, las, i, h, s, g, envT, hi, hsg, hfree, M, hM, vs, t, ht => by
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
          simp only [tmpDecls, List.cons_append]
          have hsimp := lowerC_simple a k' la hla
          have hcur : (envT.scopes.headD []).lookup (tmpName i) = none := by simpa using hfree 0
          rw [evalList_cons_run] at ht
          have hE := ihE κ a k' la hla s g envT hi hsg (m + 1) (by omega)
          have hSA := (ihEF κ a k' la hla s g envT hi (m + 1) (by omega)).1 hsg
          rcases hr : (Spec.eval ds d a).run.run s with ⟨ra, t1⟩
          rw [hr] at ht hE hSA
          cases ra with
          | error => cases ht
          | ok v =>
          simp only at ht
          obtain ⟨w, g1, hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
          rw [hg] at hSA
          simp only [Stut] at hSA
          have hdef : ∀ rest, (execStmts cx (m + 5) envT
              (.assign ":=" [.ident (tmpName i)] [la] :: (tmpDecls (i + 1) las' ++ rest))).run.run g =
              (execStmts cx (m + 5) (envT.bind (tmpName i) g1.cells.size) (tmpDecls (i + 1) las' ++ rest)).run.run
                (declG w g1) := by
            intro rest
            rw [execStmts_cons, execStmt_define cx m envT _ la g g1 w (tmpName_ne_us i) hcur (rhsSimple_ta hsimp)
              (rhsSimple_ix hsimp) hg (results_of_vrel hvw)]
          have hi1 : Inv3 cx P sg κ t1.toCore (declG w g1) (envT.bind (tmpName i) g1.cells.size) :=
            inv3_tmp (inv3_frame hi hsf hgf) hnt i w
          have hgf1 : GFrame g (declG w g1) := gframe_trans hgf (gframe_push _ _ _)
          have hsp1 : Spent s t1 g (declG w g1) := by unfold Spent at hsp ⊢; simpa [declG] using hsp
          rcases hrs : (Spec.evalList ds d as).run.run t1 with ⟨rs, t'⟩
          rw [hrs] at ht
          cases rs with
          | error => cases ht
          | ok vs' =>
          injection ht with h1 h2
          injection h1 with h1
          subst h1 h2
          obtain ⟨ws, g', envT', hrest, hkr, hsf', hgf', hsp', hi', hlk, htm, hb'⟩ :=
            tmps_stut d ihE ihEF κ hnt ks as las' (i + 1) hlas' t1 (declG w g1) _ hi1 (spent_le hsp1 hsg)
              (tmpsFree_bind hfree _) (m + 5) hM vs' _ hrs
          have hne : ∀ m', tmpName i ≠ tmpName (i + 1 + m') := by
            intro m' he; have := tmpName_inj he; omega
          refine ⟨w :: ws, g', envT', fun rest => by rw [hdef]; exact hrest rest, ⟨hvw, hkv, hkr⟩,
            sframe_trans hsf hsf', gframe_trans hgf1 hgf', spent_trans hsp1 hsp', hi', fun x hx => ?_,
            fun m' hm'' => ?_, ?_⟩
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
              obtain ⟨c, hc, hcv⟩ := htm m'' (by simp at hm''; omega)
              refine ⟨c, by simpa [Nat.add_assoc, Nat.add_comm 1] using hc, by simpa using hcv⟩
          · simp only [declG] at hb'
            simp only [eszs]; omega
        · cases h
      · cases h
  | [], _ :: _, _, _, h, _, _, _, _, _, _, _, _, _, _, _ | _ :: _, [], _, _, h, _, _, _, _, _, _, _, _, _, _, _ => by
    simp [lowerArgs] at h

/-- A part's bound and fuel claim, from after galaspec's step to before it. -/
theorem sf_tick {z d : Nat} {s : Spec.St} {kk : Nat} (hs : s.steps = kk + 1) {g : St}
    {sr : Except Spec.Stop Spec.Val × Spec.St} {r : Except Stop α × St}
    (h : SF Q z d { s with steps := kk } g sr r) : SF Q (z + 1) (d + 1) s g sr r :=
  ⟨fun hsg => stut_tick Q (by simp [hs]) (h.1 (by show kk ≤ _; omega)),
   fun hd hq hf => h.2 (by show kk ≤ d; omega) (by show _ ≤ kk; omega) hf⟩

end TmpsStut

section LeafCont
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)
variable (f : String) (F : Spec.Func) (xks : List (String × Kind)) (ρ : Kind) (st : Stmt) (env0 : Env) (n : Nat)

/-- **A loop step after its temporaries**: the parameters assigned from them, `continue`,
    and the rest of the loop from the state with the parameters stored, which is related to
    galaspec's frame of the call's arguments. -/
theorem leaf_cont (hL : LoopFn ds K sg f F xks ρ st) (hE0 : EnvOK cx P xks env0)
    {envA envT' : Env} (henv : ∀ x, envA.lookup x = env0.lookup x) {s t : Spec.St} {g g' : St}
    {vs : List Spec.Val} {ws : List Val} (hpc : ParamCells n xks env0 g)
    (hi : Inv3 cx P sg (paramKinds xks) s.toCore g envA)
    (hkr : KRel (xks.map (·.2)) vs ws) (hsf : SFrame s.toCore t.toCore) (hgf : GFrame g g')
    (hi' : Inv3 cx P sg (paramKinds xks) t.toCore g' envT')
    (hlk : ∀ x, (∀ m, x ≠ tmpName (0 + m)) → envT'.lookup x = envA.lookup x)
    (htm : ∀ m (hm : m < ws.length), ∃ c, envT'.lookup (tmpName (0 + m)) = some c ∧
      g'.cells[c]? = some (defaultVal ws[m])) (m1 k j : Nat) :
    afterIter k env0.push (iterF cx j [st]) ((execStmts cx (m1 + 1 + 6) envT'
        [.assign "=" ((xks.map (·.1)).map .ident) (tmpIdents 0 (xks.map (·.1)).length), .branch "continue"]).run.run g') =
      (loopM k env0.push (iterF cx j [st])).run.run (storeAll g' (mkAsg env0 0 xks ws)) ∧
    (storeAll g' (mkAsg env0 0 xks ws)).steps = g'.steps ∧
    Inv3 cx P sg (paramKinds xks) { t with locals := specFrame F.params vs }.toCore (storeAll g' (mkAsg env0 0 xks ws))
      env0 ∧
    ParamCells n xks env0 (storeAll g' (mkAsg env0 0 xks ws)) ∧ GFrameP n g (storeAll g' (mkAsg env0 0 xks ws)) := by
  have hnt : ∀ x, (paramKinds xks x).isSome → isTmp x = false := by
    intro x hx
    obtain ⟨k', hk'⟩ := Option.isSome_iff_exists.mp hx
    obtain ⟨m, hm⟩ := paramKinds_some hk'
    exact hL.notTmp x (List.mem_map.mpr ⟨(x, k'), List.mem_of_getElem? hm, rfl⟩)
  obtain ⟨hlv, hlw⟩ := krel_len hkr
  simp only [List.length_map] at hlv hlw
  have hcell : ∀ x k', paramKinds xks x = some k' → ∃ c, env0.lookup x = some c ∧ envT'.lookup x = some c ∧
      n ≤ c ∧ g'.types[c]? = some (kindTy k') ∧ c < g'.cells.size := by
    intro x k' hk'
    obtain ⟨c, hc, hnc, hty⟩ := hpc.1 x k' hk'
    have hxt : ∀ m', x ≠ tmpName (0 + m') := by
      intro m' he
      have := hnt x (by rw [hk']; rfl)
      rw [he, isTmp_tmpName] at this; cases this
    have hT' : envT'.lookup x = some c := by rw [hlk x hxt, henv, hc]
    obtain ⟨b', c', v', w', -, -, hc', hw', -⟩ := hi'.inv.loc x k' hk'
    rw [hT'] at hc'; cases hc'
    exact ⟨c, hc, hT', hnc, pre_get hgf.2.2.1 hty, (Array.getElem?_eq_some_iff.mp hw').1⟩
  have hasg := execStmt_assign_params cx (m1 + 2) envT' g' (mkAsg env0 0 xks ws)
    (fun a ha => by
      obtain ⟨m', x, k', w, hx, hw, rfl⟩ := mem_mkAsg env0 0 xks ws a ha
      have hk' : paramKinds xks x = some k' := paramKinds_get xks (fun _ => none) hL.names.1 m' x k' hx
      obtain ⟨c, hc, hT', -, hty, -⟩ := hcell x k' hk'
      exact ⟨(hL.names.2 x (List.mem_map.mpr ⟨(x, k'), List.mem_of_getElem? hx, rfl⟩)).1, by simp [hc, hT'],
        by simpa [hc] using hty⟩)
    (fun a ha => by
      obtain ⟨m', x, k', w, hx, hw, rfl⟩ := mem_mkAsg env0 0 xks ws a ha
      obtain ⟨v, w2, hv, hw2, hvw, hkv⟩ := krel_get hkr m' k' (by simp [hx])
      rw [hw] at hw2; cases hw2
      have hm' : m' < ws.length := (List.getElem?_eq_some_iff.mp hw).1
      obtain ⟨c, hcT, hcv⟩ := htm m' hm'
      have hws : ws[m'] = w := (List.getElem?_eq_some_iff.mp hw).2
      rw [hws] at hcv
      have hvd := (vrel_default hvw).1
      exact ⟨evalExpr_local cx (m1 + 2) envT' _ c _ g' hcT hcv (vrel_ne_poison hvd) (tmpName_ne_us _),
        results_of_vrel hvd⟩)
    (fun a ha g'' => by
      obtain ⟨m', x, k', w, hx, hw, rfl⟩ := mem_mkAsg env0 0 xks ws a ha
      obtain ⟨v, w2, hv, hw2, hvw, hkv⟩ := krel_get hkr m' k' (by simp [hx])
      rw [hw] at hw2; cases hw2
      exact convert_kind cx (m1 + 1) v (defaultVal w) k' g'' (vrel_default hvw).1 hkv)
    (fun a ha => by
      obtain ⟨m', x, k', w, hx, hw, rfl⟩ := mem_mkAsg env0 0 xks ws a ha
      have hk' : paramKinds xks x = some k' := paramKinds_get xks (fun _ => none) hL.names.1 m' x k' hx
      obtain ⟨c, hc, -, -, -, hsz⟩ := hcell x k' hk'
      simpa [hc] using hsz)
  rw [mkAsg_x env0 0 xks ws (by omega), mkAsg_t env0 0 xks ws (by omega)] at hasg
  have hrest := storeAll_rest g' (mkAsg env0 0 xks ws)
  have hty' : (storeAll g' (mkAsg env0 0 xks ws)).types = g'.types := by
    have e := congrArg St.types hrest.1; simpa using e
  have hsteps' : (storeAll g' (mkAsg env0 0 xks ws)).steps = g'.steps := by
    have e := congrArg St.steps hrest.1; simpa using e
  obtain ⟨hl1, hgl1, ho1⟩ := sframe_parts hsf
  have hinv := inv3_next (cx := cx) (sg := sg) (n := n) hL.sig hL.names hE0.pre hE0.pkg hE0.tenv hE0.closed hkr
    (paramCells_mono hpc hgf.2.2.1)
    (fun x k' c hk' hc => by obtain ⟨c', hc', -, -, -, hsz⟩ := hcell x k' hk'; rw [hc] at hc'; cases hc'; exact hsz)
    (fun f' hf' => by rw [hgl1]; exact hi.globals f' hf') (by rw [ho1]; exact hi.inv.out.trans (gframe_out hgf).symm)
    hi'.sync
  have hgP : GFrameP n g (storeAll g' (mkAsg env0 0 xks ws)) := gframeP_trans (gframeP_of hgf)
    (gframeP_storeAll g' _ (fun a ha => by
      obtain ⟨m', x, k', w, hx, hw, rfl⟩ := mem_mkAsg env0 0 xks ws a ha
      have hk' : paramKinds xks x = some k' := paramKinds_get xks (fun _ => none) hL.names.1 m' x k' hx
      obtain ⟨c, hc, -, hnc, -⟩ := hcell x k' hk'
      simpa [hc] using hnc))
  refine ⟨?_, hsteps', hinv, paramCells_mono (paramCells_mono hpc hgf.2.2.1) (by rw [hty']; exact pre_refl _), hgP⟩
  rw [execStmts_cons, show (xks.map (·.1)).map Expr.ident = xks.map (fun xk => Expr.ident xk.1) by simp,
    List.length_map, hasg]
  simp only
  rw [execStmts_cons, execStmt_continue]
  rfl

end LeafCont

section TreeSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)
variable (f : String) (F : Spec.Func) (xks : List (String × Kind)) (ρ : Kind) (st : Stmt) (env0 : Env) (n : Nat)

theorem treeLSF_of (d j : Nat) (hL : LoopFn ds K sg f F xks ρ st) (hE0 : EnvOK cx P xks env0) (hK : 11 ≤ K)
    (hQ : KszOK ds Q) (hKj : K * d ≤ j)
    (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (ihE' : ∀ d', d = d' + 1 → ExprSim ds cx P K sg d') (ihEF' : ∀ d', d = d' + 1 → ExprSF ds cx P K sg Q d')
    (ihI : ∀ d', d = d' + 1 → IfLSF ds cx P K sg Q f xks ρ st env0 n d' j)
    (ihLo : ∀ d', d = d' + 1 → LoopIH ds cx P K sg F xks ρ st env0 n d')
    (ihL : ∀ d', d = d' + 1 → LoopSF ds cx P K sg Q F xks st env0 n d') :
    TreeSF ds cx P K sg Q f xks ρ st env0 n d j := by
  intro e ls hl s g envA k hi henv hfree hpc hk M hM
  have hnt : ∀ x, (paramKinds xks x).isSome → isTmp x = false := by
    intro x hx
    obtain ⟨k', hk'⟩ := Option.isSome_iff_exists.mp hx
    obtain ⟨m, hm⟩ := paramKinds_some hk'
    exact hL.notTmp x (List.mem_map.mpr ⟨(x, k'), List.mem_of_getElem? hm, rfl⟩)
  rcases lowerLoop_cases hl with ⟨c, a, b, lc, ta, tb, rfl, hc, ha, hb, rfl⟩ | ⟨args, las, rfl, hlen, hlas, rfl⟩ |
    ⟨le, -, hle, rfl⟩
  · -- An `if`.
    cases d with
    | zero => rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.ifExpr c a b); omega⟩
    | succ d1 =>
    cases hs : s.steps with
    | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.ifExpr c a b); omega⟩
    | succ kk =>
    exact sf_tick Q hs (ihI d1 rfl c a b lc ta tb hc ha hb s kk g envA k hs hi henv hpc hk M
      (by rw [Nat.mul_succ] at hM; omega))
  · -- A loop step.
    cases d with
    | zero => rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.call (.name f) args); omega⟩
    | succ d1 =>
    cases hs : s.steps with
    | zero =>
      rw [eval_fuel _ _ _ _ hs]
      exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.call (.name f) args); omega⟩
    | succ k1 =>
    obtain ⟨hlocf, -⟩ := hi.closed f hL.kf
    have hglob := hi.globals f (by rw [hL.sgf]; rfl)
    have hres : Spec.resolve ds s.locals s.globals f = .func F := by
      simp [Spec.resolve, Spec.lookupVar, hlocf, hglob, hL.lookup]
    rw [eval_call_run ds d1 k1 f args F s hs hres, Spec.run_bind_eq]
    obtain ⟨hnames, htys, hresT⟩ := sigOf_facts hL.sig
    obtain ⟨hargsU, hargsL⟩ := lowerArgs_facts K sg (paramKinds xks) _ args las hlas
    have hlenP : F.params.length = xks.length := by
      have := congrArg List.length hnames; simpa using this
    have hfa : ∀ s', (Spec.funcArgs ds d1 F args).run.run s' = (Spec.evalList ds d1 args).run.run s' := by
      intro s'
      rw [Spec.funcArgs.eq_def]
      refine bindArgs_pos ds d1 _ _ _ args hargsU (by simp [hargsL, hlenP]) (fun j' hj => ?_) s'
      rw [htys]
      simp only [List.length_map] at hj
      exact ⟨kindName xks[j'].2, by simp [List.getD_eq_getElem?_getD, hlenP ▸ hj]⟩
    rw [hfa]
    obtain ⟨m, rfl⟩ : ∃ m, M = m + 6 := ⟨M - 6, by rw [Nat.mul_succ] at hM; omega⟩
    obtain ⟨m1, rfl⟩ : ∃ m1, m = m1 + 1 := ⟨m - 1, by rw [Nat.mul_succ] at hM; omega⟩
    simp only [stepOf]
    have hszb := hQ.2 f F hL.lookup
    have hsz : esz (.call (.name f) args) = eszs args + 2 := by simp [esz]; omega
    rw [hsz]
    have hk1 : ({ s with steps := k1 } : Spec.St).steps = k1 := rfl
    have hNt : K * d1 + 5 ≤ m1 + 1 + 6 := by rw [Nat.mul_succ] at hM; omega
    have hcall : ∀ (t : Spec.St) (vs : List Spec.Val), vs.length = xks.length → ∀ r t',
        r ≠ .ok .unit → (Spec.runIn ds d1 (specFrame F.params vs) F.body).run.run t = (r, t') →
        (Spec.callFunc ds d1 F vs).run.run t = (r, t') := fun t vs hlv r t' hr hrun =>
      callFunc_run ds d1 F vs t (params_typed htys) ρ hresT (by rw [hlv, hlenP]) r t' hrun hr
    refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
    · -- The bound.
      rcases hr : (Spec.evalList ds d1 args).run.run { s with steps := k1 } with ⟨ra, t⟩
      cases ra with
      | error er =>
        cases er with
        | ret =>
          have hT := tmps_sim ds cx P K sg d1 (ihE' d1 rfl) (paramKinds xks) hnt (xks.map (·.2)) args las 0 hlas
            { s with steps := k1 } g envA hi (by show k1 ≤ g.steps; omega) hfree (m1 + 1 + 6) hNt
            [.assign "=" ((xks.map (·.1)).map .ident) (tmpIdents 0 (xks.map (·.1)).length), .branch "continue"]
          rw [hr] at hT
          exact hT.elim
        | _ => trivial
      | ok vs =>
      simp only
      obtain ⟨ws, g', envT', hrest, hkr, hsf, hgf, hsp, hi', hlk, htm, hbd⟩ :=
        tmps_stut ds cx P K sg Q d1 (ihE' d1 rfl) (ihEF' d1 rfl) (paramKinds xks) hnt (xks.map (·.2)) args las 0
          hlas { s with steps := k1 } g envA hi (by show k1 ≤ g.steps; omega) hfree (m1 + 1 + 6) hNt vs t hr
      rw [hrest]
      obtain ⟨hcont, hsteps', hinv, hpc', hgP⟩ := leaf_cont ds cx P K sg f F xks ρ st env0 n hL hE0 henv hpc hi hkr
        hsf hgf hi' hlk htm m1 k j
      rw [hcont]
      obtain ⟨hlv, -⟩ := krel_len hkr
      simp only [List.length_map] at hlv
      have hstg : t.steps ≤ (storeAll g' (mkAsg env0 0 xks ws)).steps := by
        rw [hsteps']; exact spent_le hsp (by show k1 ≤ g.steps; omega)
      have hkS : (storeAll g' (mkAsg env0 0 xks ws)).steps < k := by
        rw [hsteps']; exact Nat.lt_of_le_of_lt hgf.2.2.2.2.1 hk
      have hIH := ihLo d1 rfl j (by rw [Nat.mul_succ] at hKj; omega) { t with locals := specFrame F.params vs }
        (storeAll g' (mkAsg env0 0 xks ws)) k hinv hstg hpc' hkS
      have hSL := (ihL d1 rfl j (by rw [Nat.mul_succ] at hKj; omega) { t with locals := specFrame F.params vs }
        (storeAll g' (mkAsg env0 0 xks ws)) k hinv hpc' hkS).1 hstg
      rcases hb : (Spec.eval ds d1 F.body).run.run { t with locals := specFrame F.params vs } with ⟨rb, t2⟩
      rw [hb] at hIH hSL
      have hrun := runIn_res ds d1 (specFrame F.params vs) F.body t
      rw [hb] at hrun
      have hfin : ∀ v w gx, VRel v w →
          (loopM k env0.push (iterF cx j [st])).run.run (storeAll g' (mkAsg env0 0 xks ws)) = (.error (.ret [w]), gx) →
          (Spec.runIn ds d1 (specFrame F.params vs) F.body).run.run t = (.ok v, { t2 with locals := t.locals }) →
          t.steps + Q * gx.steps ≤ t2.steps + Q * (storeAll g' (mkAsg env0 0 xks ws)).steps + 0 →
          Stut Q (eszs args + 2) s g ((Spec.callFunc ds d1 F vs).run.run t)
            ((loopM k env0.push (iterF cx j [st])).run.run (storeAll g' (mkAsg env0 0 xks ws))) := by
        intro v w gx hvw hgx hrn hsl
        have hv1 : v ≠ .unit := by intro h; subst h; cases hvw
        rw [hcall t vs hlv _ _ (fun h => hv1 (by cases h; rfl)) hrn, hgx]
        simp only [Stut]
        rw [hsteps'] at hsl
        (try dsimp only)
        omega
      cases rb with
      | ok v =>
        obtain ⟨w, gx, hgx, hvw, -⟩ := hIH
        exact hfin v w gx hvw hgx hrun (by rw [hgx] at hSL; exact hSL)
      | error e =>
        cases e with
        | ret v =>
          obtain ⟨w, gx, hgx, hvw, -⟩ := hIH
          exact hfin v w gx hvw hgx hrun (by rw [hgx] at hSL; exact hSL)
        | panic pc msg =>
          simp only at hrun
          rw [hcall t vs hlv (.error (.panic pc msg)) t2 (fun h => nomatch h) hrun]
          trivial
        | fuel => simp only at hrun; rw [hcall t vs hlv (.error .fuel) t2 (fun h => nomatch h) hrun]; trivial
        | stuck m' => simp only at hrun; rw [hcall t vs hlv (.error (.stuck m')) t2 (fun h => nomatch h) hrun]; trivial
        | unsupported a b =>
          simp only at hrun; rw [hcall t vs hlv (.error (.unsupported a b)) t2 (fun h => nomatch h) hrun]; trivial
    · -- The fuel claim.
      have hT := tmps_sf ds cx P K sg Q d1 (ihE' d1 rfl) (ihEF' d1 rfl) (paramKinds xks) hnt (xks.map (·.2)) args las 0
        hlas { s with steps := k1 } g envA hi hfree (m1 + 1 + 6) hNt
      have hq' : Q * g.steps + Q + eszs args + 2 ≤ k1 + 1 := by rw [Nat.mul_succ] at hq; omega
      rcases hr : (Spec.evalList ds d1 args).run.run { s with steps := k1 } with ⟨ra, t⟩
      rw [hr] at hf
      cases ra with
      | error er =>
        simp only at hf
        cases hf
        exact afterIter_fuel k _ _ (hT.2 (by rw [hk1]; omega) (by rw [hk1, Nat.mul_succ]; omega) (by rw [hr]) _)
      | ok vs =>
      simp only at hf
      rcases hT.1 vs t hr with hfu | ⟨ws, g', envT', hrest, hkr, hsf, hgf, hi', hlk, htm, hbd, hmono⟩
      · exact afterIter_fuel k _ _ (hfu _)
      rw [hrest]
      obtain ⟨hcont, hsteps', hinv, hpc', -⟩ := leaf_cont ds cx P K sg f F xks ρ st env0 n hL hE0 henv hpc hi hkr
        hsf hgf hi' hlk htm m1 k j
      rw [hcont]
      obtain ⟨hlv, -⟩ := krel_len hkr
      simp only [List.length_map] at hlv
      have hkS : (storeAll g' (mkAsg env0 0 xks ws)).steps < k := by
        rw [hsteps']; exact Nat.lt_of_le_of_lt hgf.2.2.2.2.1 hk
      have hbf := callFunc_fuel ds d1 F vs t (params_typed htys) ρ hresT (by rw [hlv, hlenP]) hf
      rw [hk1] at hbd hmono
      exact (ihL d1 rfl j (by rw [Nat.mul_succ] at hKj; omega) { t with locals := specFrame F.params vs }
        (storeAll g' (mkAsg env0 0 xks ws)) k hinv hpc' hkS).2 (by show t.steps ≤ d1; omega)
        (by show _ ≤ t.steps; rw [hsteps', Nat.mul_succ]; omega) hbf
  · -- `return` of a value.
    obtain ⟨m, rfl⟩ : ∃ m, M = m + 4 := ⟨M - 4, by omega⟩
    rw [execStmts_ret1 cx m envA le g]
    have h := ihEF (paramKinds xks) e ρ le hle s g envA hi (m + 1) (by omega)
    refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
    · have hE := ihE (paramKinds xks) e ρ le hle s g envA hi hsg (m + 1) (by omega)
      have h1 := h.1 hsg
      rcases hr : (Spec.eval ds d e).run.run s with ⟨er | v, s'⟩
      · rw [hr] at hE
        cases er with
        | ret => exact hE.elim
        | _ => trivial
      · rw [hr] at hE h1
        obtain ⟨w, g', hg, -⟩ := hE
        rw [hg] at h1 ⊢
        refine stut_congr Q ?_ h1
        rfl
    · have h2 := h.2 hd hq hf
      revert h2
      rcases (evalExpr cx (m + 1) envA le).run.run g with ⟨x | w, g'⟩ <;> intro h2 <;> cases h2 <;> rfl

end TreeSF

section LoopAllSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)
variable (f : String) (F : Spec.Func) (xks : List (String × Kind)) (ρ : Kind) (st : Stmt) (env0 : Env) (n : Nat)

/-- An iteration's step pays for the body's nodes: the bound from after both steps, to
    before them with nothing left over. -/
theorem stut_iter {E : Nat} {s : Spec.St} {kk : Nat} {g : St} {gk : Nat} (hs : s.steps = kk + 1)
    (hg : g.steps = gk + 1) (hE : E + 2 ≤ Q) {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop α × St} (h : Stut Q E { s with steps := kk } { g with steps := gk } sr r) :
    Stut Q 0 s g sr r := by
  rcases sr with ⟨e | v, s'⟩ <;> rcases r with ⟨x, g'⟩
  · cases e <;> first | trivial | (simp only [Stut] at h ⊢; rw [hs, hg, Nat.mul_succ]; omega)
  · simp only [Stut] at h ⊢; rw [hs, hg, Nat.mul_succ]; omega

theorem loop_iter_zero (k j : Nat) (e : Env) (body : List Stmt) (g : St) (hk : g.steps = 0) :
    ((loopM (k + 1) e.push (iterF cx (j + 1) body)).run.run g).1 = .error .fuel := by
  rw [loopM_succ]
  have h : ((iterF cx (j + 1) body e.push).run.run g).1 = .error .fuel := by
    unfold iterF; exact grun_bind_fuel (gtick_zero g hk)
  rcases hr : (iterF cx (j + 1) body e.push).run.run g with ⟨r, g'⟩
  rw [hr] at h
  simp only at h
  subst h
  rfl

/-- **The loop at galaspec depth `d`**: an iteration's step, the body's `if` as `IfLSF`
    one level down. -/
theorem loopSF_of (d : Nat) (hL : LoopFn ds K sg f F xks ρ st) (hK : 11 ≤ K) (hQ : KszOK ds Q)
    (ihI : ∀ d', d = d' + 1 → ∀ j, K * d ≤ j → IfLSF ds cx P K sg Q f xks ρ st env0 n d' j) :
    LoopSF ds cx P K sg Q F xks st env0 n d := by
  intro j hj s g k hi hpc hk
  obtain ⟨c, a, b, hbody⟩ := hL.body
  have hlow := hL.low
  have hszb := hQ.2 f F hL.lookup
  rw [hbody] at hlow hszb ⊢
  simp only [esz] at hszb
  rcases lowerLoop_cases hlow with ⟨c', a', b', lc, ta, tb, he, hc, ha, hb, hst⟩ | ⟨args, las, he, -⟩ | ⟨le, hne, -⟩
  · cases he
    simp only [List.cons.injEq, and_true] at hst
    subst hst
    obtain ⟨k', rfl⟩ : ∃ k', k = k' + 1 := ⟨k - 1, by omega⟩
    cases d with
    | zero => rw [eval_zero]; exact ⟨fun _ => trivial, fun h1 h2 _ => by rw [Nat.mul_succ] at h2; omega⟩
    | succ d1 =>
    cases hs : s.steps with
    | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun h1 h2 _ => by rw [Nat.mul_succ] at h2; omega⟩
    | succ kk =>
    obtain ⟨j', rfl⟩ : ∃ j', j = j' + 1 := ⟨j - 1, by rw [Nat.mul_succ] at hj; omega⟩
    have hi1 : ∀ gk, Inv3 cx P sg (paramKinds xks) s.toCore { g with steps := gk } env0.push.push := fun gk =>
      inv3_push cx P sg (inv3_push cx P sg (inv3_same cx P sg hi rfl rfl rfl))
    have hIF := fun gk (hgk : g.steps = gk + 1) => ihI d1 rfl (j' + 1) hj c a b lc ta tb hc ha hb s kk
      { g with steps := gk } env0.push.push k' hs (hi1 gk) (fun x => by simp [Env.lookup_push]) hpc
      (by show gk < k'; omega) j' (by rw [Nat.mul_succ] at hj; omega)
    refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
    · obtain ⟨gk, hgk⟩ : ∃ gk, g.steps = gk + 1 := ⟨g.steps - 1, by omega⟩
      rw [loop_iter cx k' j' env0 _ g gk hgk]
      exact stut_iter Q hs hgk (by omega) ((hIF gk hgk).1 (by show kk ≤ gk; omega))
    · rcases Nat.eq_zero_or_pos g.steps with hg0 | hg0
      · exact loop_iter_zero cx k' j' env0 _ g hg0
      obtain ⟨gk, hgk⟩ : ∃ gk, g.steps = gk + 1 := ⟨g.steps - 1, by omega⟩
      rw [loop_iter cx k' j' env0 _ g gk hgk]
      rw [hgk] at hq
      exact (hIF gk hgk).2 (by show kk ≤ d1; omega)
        (by show _ ≤ kk; simp only [Nat.mul_succ] at hq ⊢; omega) hf
  · cases he
  · exact absurd rfl (hne c a b)

/-- **The loop, at every galaspec depth up to `D`.** -/
theorem loopSF_all (D : Nat) (hL : LoopFn ds K sg f F xks ρ st) (hE0 : EnvOK cx P xks env0) (hK : 11 ≤ K)
    (hQ : KszOK ds Q) (hE : ∀ d, d ≤ D → ExprSim ds cx P K sg d) (hEF : ∀ d, d ≤ D → ExprSF ds cx P K sg Q d) :
    ∀ d, d ≤ D →
    (∀ j, K * d ≤ j → TreeSF ds cx P K sg Q f xks ρ st env0 n d j ∧ IfLSF ds cx P K sg Q f xks ρ st env0 n d j) ∧
      LoopSF ds cx P K sg Q F xks st env0 n d
  | 0, h0 => by
    have hT : ∀ j, K * 0 ≤ j → TreeSF ds cx P K sg Q f xks ρ st env0 n 0 j := fun j hj =>
      treeLSF_of ds cx P K sg Q f F xks ρ st env0 n 0 j hL hE0 hK hQ hj (hE 0 h0) (hEF 0 h0) (fun _ h => by cases h)
        (fun _ h => by cases h) (fun _ h => by cases h) (fun _ h => by cases h) (fun _ h => by cases h)
    exact ⟨fun j hj => ⟨hT j hj, ifLSF_of ds cx P K sg Q f xks ρ st env0 n 0 j (hE 0 h0) (hEF 0 h0) (hT j hj)⟩,
      loopSF_of ds cx P K sg Q f F xks ρ st env0 n 0 hL hK hQ (fun _ h => by cases h)⟩
  | d + 1, hd => by
    obtain ⟨ihTI, ihL⟩ := loopSF_all D hL hE0 hK hQ hE hEF d (by omega)
    have ihLo := (loop_all ds cx P K sg f F xks ρ st env0 n D hL hE0 hK hE d (by omega)).2
    have hT : ∀ j, K * (d + 1) ≤ j → TreeSF ds cx P K sg Q f xks ρ st env0 n (d + 1) j := fun j hj =>
      treeLSF_of ds cx P K sg Q f F xks ρ st env0 n (d + 1) j hL hE0 hK hQ hj (hE (d + 1) hd) (hEF (d + 1) hd)
        (fun d' h => by cases h; exact hE d (by omega))
        (fun d' h => by cases h; exact hEF d (by omega))
        (fun d' h => by cases h; exact (ihTI j (by rw [Nat.mul_succ] at hj; omega)).2)
        (fun d' h => by cases h; exact ihLo)
        (fun d' h => by cases h; exact ihL)
    exact ⟨fun j hj => ⟨hT j hj, ifLSF_of ds cx P K sg Q f xks ρ st env0 n (d + 1) j (hE (d + 1) hd) (hEF (d + 1) hd)
        (hT j hj)⟩,
      loopSF_of ds cx P K sg Q f F xks ρ st env0 n (d + 1) hL hK hQ
        (fun d' h j hj => by cases h; exact (ihTI j (by rw [Nat.mul_succ] at hj; omega)).2)⟩

end LoopAllSF

end Gala.M5
