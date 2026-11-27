/-
  # M5, step 3d (c): divergence, tails
-/
import Gala.M5.DivStmt
namespace Gala.M5
open Gala.GoCore.Full

/-- Closes a bound after a tick, the runs split into their cases. -/
macro "stut_close" : tactic => `(tactic| first
  | (intro; trivial)
  | (intro h; (try dsimp only at h ⊢); (try simp only [Stut, esz, eszOpt, eszs] at h ⊢); (try dsimp only at h ⊢); omega))

section TailsSF2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem execStmts_block1_snd (n : Nat) (env : Env) (st : Stmt) (g : St) :
    ((execStmts cx (n + 4) env [.block [st]]).run.run g).2 = ((execStmt cx n env.push st).run.run g).2 ∧
    (((execStmt cx n env.push st).run.run g).1 = .error .fuel →
      ((execStmts cx (n + 4) env [.block [st]]).run.run g).1 = .error .fuel) := by
  rw [execStmts_cons, execStmt_block, execBlock_run, execStmts_cons]
  rcases (execStmt cx n env.push st).run.run g with ⟨_ | e', g'⟩
  · simp [withEnv]
  · simp [withEnv, execStmts_nil]

/-- **A tail**, from its parts one level down. -/
theorem tailSF_of (d : Nat) (ihE : ExprSF ds cx P K sg Q d) (ihE' : ∀ d', d = d' + 1 → ExprSF ds cx P K sg Q d')
    (ihTs : ∀ d', d = d' + 1 → TsSF ds cx P K sg Q d') (ihI : ∀ d', d = d' + 1 → IfTSF ds cx P K sg Q d')
    (hK : 7 ≤ K) : TailSF ds cx P K sg Q d := by
  intro r κ e k ls hl s g env hi N hN
  cases d with
  | zero => rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos e; omega⟩
  | succ d' =>
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  unfold tailOf armOf at hl
  split at hl
  · -- a compound tail
    rcases lowerT_cases K sg hl with ⟨c, a, b, st, rfl, hst, rfl⟩ | ⟨c, as, b, st, rfl, hst, rfl⟩ |
      ⟨ss, rfl, hss⟩ | ⟨x, le, rfl, hle, rfl, rfl⟩
    · cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h; omega⟩
      | succ kk =>
      have h := ihI d' rfl r κ c a b k st hst _ (Or.inl rfl) s kk g env.push hs (inv3_push cx P sg hi)
        n (by rw [Nat.mul_succ] at hN; omega)
      have hb := execStmts_block1_snd cx n env st g
      refine ⟨fun hsg => ?_, fun hd hq hf => hb.2 (h.2 (by show kk ≤ d'; omega)
        (by show _ ≤ kk; simp only [esz] at hq; omega) hf)⟩
      have h1 := stut_congr Q hb.1 (h.1 (by show kk ≤ g.steps; omega))
      revert h1
      rcases (Spec.eval ds (d' + 1) (.ifExpr c a b)).run.run s with ⟨e | v, s'⟩ <;>
        rcases (execStmts cx (n + 4) env [.block [st]]).run.run g with ⟨x, g'⟩
      · cases e <;> stut_close
      · stut_close
    · cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h; omega⟩
      | succ kk =>
      have h := ihI d' rfl r κ c (.block as) b k st hst _ (Or.inr ⟨as, rfl, rfl⟩) s kk g env.push hs
        (inv3_push cx P sg hi) n (by rw [Nat.mul_succ] at hN; omega)
      have hb := execStmts_block1_snd cx n env st g
      refine ⟨fun hsg => ?_, fun hd hq hf => hb.2 (h.2 (by show kk ≤ d'; omega)
        (by show _ ≤ kk; simp only [esz, eszOpt] at hq ⊢; omega) hf)⟩
      have h1 := stut_congr Q hb.1 (h.1 (by show kk ≤ g.steps; omega))
      revert h1
      rcases (Spec.eval ds (d' + 1) (.ifStmt c (.block as) (some b))).run.run s with ⟨e | v, s'⟩ <;>
        rcases (execStmts cx (n + 4) env [.block [st]]).run.run g with ⟨x, g'⟩
      · cases e <;> stut_close
      · stut_close
    · cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h; omega⟩
      | succ kk =>
      rw [eval_block_run, tick_succ s kk hs]
      simp only
      have h := ihTs d' rfl r κ ss k ls hss { s with steps := kk } g env hi (n + 4) (by rw [Nat.mul_succ] at hN; omega)
      have hk : ({ s with steps := kk } : Spec.St).steps = kk := rfl
      simp only [esz]
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · have h1 := h.1 (by show kk ≤ g.steps; omega)
        revert h1
        rcases (Spec.evalBlock ds d' ss).run.run { s with steps := kk } with ⟨e | v, s'⟩ <;>
          rcases (execStmts cx (n + 4) env ls).run.run g with ⟨x, g'⟩
        · cases e <;> stut_close
        · stut_close
      · refine h.2 (by show kk ≤ d'; omega) (by show _ ≤ kk; omega) ?_
        revert hf
        rcases (Spec.evalBlock ds d' ss).run.run { s with steps := kk } with ⟨_ | _, _⟩ <;> simp
    · cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h; omega⟩
      | succ kk =>
      rw [eval_ret_run ds d' kk x s hs, execStmts_ret1]
      have h := ihE' d' rfl κ x k le hle { s with steps := kk } g env hi (n + 1) (by rw [Nat.mul_succ] at hN; omega)
      have hk : ({ s with steps := kk } : Spec.St).steps = kk := rfl
      simp only [esz, eszOpt]
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · have h1 := h.1 (by show kk ≤ g.steps; omega)
        revert h1
        rcases (Spec.eval ds d' x).run.run { s with steps := kk } with ⟨e | v, s'⟩ <;>
          rcases (evalExpr cx (n + 1) env le).run.run g with ⟨x | w, g'⟩
        all_goals first
          | (cases e <;> stut_close)
          | stut_close
      · have h2 := h.2 (by show kk ≤ d'; omega) (by show _ ≤ kk; omega)
        revert hf h2
        rcases (Spec.eval ds d' x).run.run { s with steps := kk } with ⟨_ | _, _⟩ <;>
          rcases (evalExpr cx (n + 1) env le).run.run g with ⟨_ | _, _⟩ <;> simp <;> intro h1 h2 <;> exact h2 h1
  · -- an expression, returned
    simp only [Option.map_eq_some_iff] at hl
    obtain ⟨⟨k', le⟩, hle, hkl⟩ := hl
    simp only [retTail, Prod.mk.injEq] at hkl
    obtain ⟨rfl, rfl⟩ := hkl
    rw [execStmts_ret1]
    have h := ihE κ e _ le hle s g env hi (n + 1) (by omega)
    refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
    · have h1 := h.1 hsg
      revert h1
      rcases (Spec.eval ds (d' + 1) e).run.run s with ⟨er | v, s'⟩ <;>
        rcases (evalExpr cx (n + 1) env le).run.run g with ⟨x | w, g'⟩
      all_goals first
        | (cases er <;> stut_close)
        | stut_close
    · have h2 := h.2 hd hq hf
      revert h2
      rcases (evalExpr cx (n + 1) env le).run.run g with ⟨_ | _, _⟩ <;> simp

/-- **A block's elements as a tail**: its statements, then its last element's tail. -/
theorem tsSF_of (d : Nat) (ihS : StmtSim ds cx P K sg d) (ihSF : StmtSF ds cx P K sg Q d)
    (ihT : TailSF ds cx P K sg Q d) : TsSF ds cx P K sg Q d := by
  intro r κ ss
  induction ss generalizing κ with
  | nil => intro k ls hl; simp [lowerTs] at hl
  | cons st rest ihl =>
    intro k ls hl s g env hi N hN
    cases rest with
    | nil =>
      rw [evalBlock_one]
      have hsz : eszs [st] = esz st := by simp [eszs]
      rw [hsz]
      exact ihT r κ st k ls (lowerTs_one hl) s g env hi N hN
    | cons st' rest' =>
      simp only [lowerTs, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
        Option.some.injEq] at hl
      obtain ⟨⟨κ₁, l⟩, hst, ⟨k', ls'⟩, hrest, hkl⟩ := hl
      simp only [Prod.mk.injEq] at hkl
      obtain ⟨rfl, rfl⟩ := hkl
      obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
      have hIS := ihSF r κ st κ₁ l hst s g env hi n (by omega)
      rw [evalBlock_cons, execStmts_cons]
      simp only [eszs]
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · have hs := ihS r κ st κ₁ l hst s g env hi hsg n (by omega)
        have hSS := hIS.1 hsg
        rcases hrs : (Spec.eval ds d st).run.run s with ⟨rs, t⟩
        rw [hrs] at hs hSS
        cases rs with
        | error e => cases e with
          | ret v =>
            obtain ⟨w, g', hg, -⟩ := hs; rw [hg] at hSS ⊢
            exact stut_mono Q (by omega) hSS
          | _ => trivial
        | ok v =>
        obtain ⟨env', g', hg, hinv, -, -, -, -, hsp⟩ := hs
        rw [hg] at hSS ⊢
        simp only [Stut] at hSS
        have h := (ihl κ₁ _ ls' hrest t g' env' hinv (n + 1) hN).1 (spent_le hsp hsg)
        exact stut_mono Q (by simp only [eszs]; omega) (stut_trans Q hSS h)
      · rcases hrs : (Spec.eval ds d st).run.run s with ⟨rs, t⟩
        rw [hrs] at hf
        cases rs with
        | error e =>
          simp only at hf
          cases hf
          have h := hIS.2 hd (by omega) (by rw [hrs])
          rcases hx : (execStmt cx n env l).run.run g with ⟨rx, g'⟩
          rw [hx] at h
          simp only at h
          subst h
          rfl
        | ok v =>
        simp only at hf
        rcases stmt_down ds cx P K sg Q ihS ihSF hst hi (by omega : K * d ≤ n) hrs with
          hgf | ⟨env', g', hg, hinv, -, -, -, -, hbd, hmono⟩
        · rcases hx : (execStmt cx n env l).run.run g with ⟨rx, g'⟩
          rw [hx] at hgf
          simp only at hgf
          subst hgf
          rfl
        rw [hg]
        simp only
        exact (ihl κ₁ _ ls' hrest t g' env' hinv (n + 1) hN).2 (by omega)
          (by simp only [eszs] at hq ⊢; rw [Nat.mul_succ] at hq ⊢; omega) hf

end TailsSF2

end Gala.M5
