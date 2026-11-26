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

end Gala.M5
