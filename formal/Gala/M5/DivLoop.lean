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

end Gala.M5
