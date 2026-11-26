/-
  # M5, step 3d (c): divergence, the induction and the theorems

  docs/FORMAL_SEMANTICS.MD, M5. The bound (`Stut`) and the fuel claim of every part, by
  induction on galaspec's depth (`sf_all`), alongside steps 3a to 3c's simulation
  (`sim3_all`), which this induction reads at a larger budget and brings back down
  (`expr_down`, `stmt_down`, `args_down`). Then the three theorems: `calls_stutter`,
  `calls_fuel`, `calls_diverge`.
-/
import Gala.M5.DivLoop
namespace Gala.M5
open Gala.GoCore.Full

section BodiesSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- **A function's body, as `lowerFun` lowers it**: a loop as `loopSF_all` says (its
    iterations' steps pay for the body), any other body as its tail. -/
theorem bodySF_of (d : Nat) (hK : 11 ≤ K) (hQ : KszOK ds Q) (hE : ∀ d', d' ≤ d → ExprSim ds cx P K sg d')
    (hEF : ∀ d', d' ≤ d → ExprSF ds cx P K sg Q d') (ihT : TailSF ds cx P K sg Q d) : BodySF ds cx P K sg Q d := by
  intro f F xks ρ lb hFl hsig hlb s g env n hi hpc N hN
  unfold lowerFun at hlb
  rw [hsig] at hlb
  simp only at hlb
  split at hlb
  · rename_i hgate
    split at hlb
    · rename_i hc
      obtain ⟨hpok, hnt, hkf, hsgf⟩ := hc
      split at hlb
      · rename_i ls hls
        split at hlb
        · simp only [Option.some.injEq] at hlb
          subst hlb
          obtain ⟨c, a, b, hbody⟩ : ∃ c a b, F.body = .ifExpr c a b := by
            unfold loopGate at hgate; split at hgate
            · rename_i e0 c a b he; exact ⟨c, a, b, he⟩
            · cases hgate
          have hls' := hls
          rw [hbody] at hls'
          obtain ⟨st, rfl⟩ : ∃ st, ls = [st] := by
            rcases lowerLoop_cases hls' with ⟨_, _, _, lc, ta, tb, -, -, -, -, rfl⟩ | ⟨_, _, he, -⟩ | ⟨_, hne, -⟩
            · exact ⟨_, rfl⟩
            · cases he
            · exact absurd rfl (hne c a b)
          have hL : LoopFn ds K sg f F xks ρ st :=
            ⟨hFl, hsig, hsgf, hkf, hpok, fun x hx => by
              have := List.all_eq_true.mp hnt
              obtain ⟨xk, hxk, rfl⟩ := List.mem_map.mp hx
              simpa using this xk hxk, hls, ⟨c, a, b, hbody⟩⟩
          have hE0 : EnvOK cx P xks env := ⟨hi.pkg, hi.tenv, hi.inv.pre, fun x hx ht => (hi.closed x hx).2 ht⟩
          obtain ⟨m, rfl⟩ : ∃ m, N = m + 3 := ⟨N - 3, by omega⟩
          rw [execStmts_for]
          have h := (loopSF_all ds cx P K sg Q f F xks ρ st env n d hL hE0 hK hQ hE hEF d (Nat.le_refl _)).2 m
            (by omega) s g (g.steps + 1) hi hpc (Nat.lt_succ_self _)
          exact ⟨fun hsg => stut_congr Q (withEnv_snd _ _) (stut_mono Q (Nat.zero_le _) (h.1 hsg)),
            fun hd hq hf => withEnv_fuel env (h.2 hd (by omega) hf)⟩
        · cases hlb
      · cases hlb
    · cases hlb
  · simp only [lowerBody, hsig] at hlb
    split at hlb
    · split at hlb
      · rename_i k lb' htl
        split at hlb
        · rename_i hk
          obtain ⟨rfl, -⟩ := hk
          simp only [Option.some.injEq] at hlb
          subst hlb
          exact ihT (some k) _ F.body k lb' htl s g env hi N hN
        · cases hlb
      · cases hlb
    · cases hlb

end BodiesSF

section MainSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- **Every part's bound and fuel claim, at every galaspec depth**: induction on the depth,
    beside `sim3_all`. -/
theorem sf_all (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (hQ : KszOK ds Q) : ∀ d,
    (∀ d', d' ≤ d → ExprSF ds cx P K sg Q d') ∧ BodySF ds cx P K sg Q d ∧ StmtSF ds cx P K sg Q d ∧
      ListSF ds cx P K sg Q d ∧ BlockSF ds cx P K sg Q d ∧ IfSF ds cx P K sg Q d ∧ TailSF ds cx P K sg Q d ∧
      TsSF ds cx P K sg Q d ∧ IfTSF ds cx P K sg Q d
  | 0 => by
    have hS0 := sim3_all ds cx P K sg hF hK 0
    have hE := exprSF_zero ds cx P K sg Q
    have hE' : ∀ d', d' ≤ 0 → ExprSF ds cx P K sg Q d' := fun d' h => by
      rw [show d' = 0 by omega]; exact hE
    have hS := stmtSF_zero ds cx P K sg Q
    have hT := tailSF_of ds cx P K sg Q 0 hE (fun _ h => by cases h) (fun _ h => by cases h)
      (fun _ h => by cases h) (by omega)
    exact ⟨hE', bodySF_of ds cx P K sg Q 0 hK hQ hS0.1 hE' hT, hS, listSF_of ds cx P K sg Q 0 hS0.2.2.1 hS,
      blockSF_zero ds cx P K sg Q, ifSF_zero ds cx P K sg Q, hT, tsSF_of ds cx P K sg Q 0 hS0.2.2.1 hS hT,
      ifTSF_of ds cx P K sg Q 0 (hS0.1 0 (Nat.le_refl _)) hE hT⟩
  | d + 1 => by
    obtain ⟨hEs, hB, hS, hL, hBl, hI, hT, hTs, hIT⟩ := sf_all hF hK hQ d
    have hSd := sim3_all ds cx P K sg hF hK d
    have hSd1 := sim3_all ds cx P K sg hF hK (d + 1)
    have hE := hEs d (Nat.le_refl _)
    have hEd := hSd.1 d (Nat.le_refl _)
    have hE1 := exprSF_succ ds cx P K sg Q d hF hK hQ hEd hSd.2.1 hSd.2.2.2.2.2.2.2.2 hE hB hIT
    have hEs1 : ∀ d', d' ≤ d + 1 → ExprSF ds cx P K sg Q d' := fun d' h => by
      rcases Nat.lt_or_ge d' (d + 1) with h' | h'
      · exact hEs d' (by omega)
      · rw [show d' = d + 1 by omega]; exact hE1
    have hBl1 := blockSF_succ ds cx P K sg Q d hL
    have hI1 := ifSF_succ ds cx P K sg Q d hEd hE hBl hI (by omega)
    have hS1 := stmtSF_succ ds cx P K sg Q d hEd hE hBl1 hI1 (by omega)
    have hT1 := tailSF_of ds cx P K sg Q (d + 1) hE1 (fun d' h => by cases h; exact hE)
      (fun d' h => by cases h; exact hTs) (fun d' h => by cases h; exact hIT) (by omega)
    exact ⟨hEs1, bodySF_of ds cx P K sg Q (d + 1) hK hQ hSd1.1 hEs1 hT1, hS1,
      listSF_of ds cx P K sg Q (d + 1) hSd1.2.2.1 hS1, hBl1, hI1, hT1,
      tsSF_of ds cx P K sg Q (d + 1) hSd1.2.2.1 hS1 hT1, ifTSF_of ds cx P K sg Q (d + 1) (hSd1.1 _ (Nat.le_refl _)) hE1 hT1⟩

/-- **M5, step 3d (c): the lower bound.** An expression the reference lowering accepts,
    evaluated by galaspec and GoCore from related states with at least galaspec's steps
    for GoCore (as in `calls_correct`): wherever galaspec ends with a value, it has spent
    at most `Q` times the steps GoCore spent, plus the expression's size (`esz`), for any
    `Q` larger than every function body's size (`KszOK`; `kszOf` computes one). GoCore
    takes a step per call and per loop iteration; galaspec spends at most a body's size
    between two of them. -/
theorem calls_stutter (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (hQ : KszOK ds Q) (κ : String → Option Kind)
    (e : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ e = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (d : Nat) :
    ∀ N ≥ K * d, Stut Q (esz e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g) :=
  fun N hN => ((sf_all ds cx P K sg Q hF hK hQ d).1 d (Nat.le_refl _) κ e k le hl s g env hi N hN).1 hsg

/-- **M5, step 3d (c): running out of fuel.** As `calls_stutter`, but GoCore given `m`
    steps: where galaspec, from a state with `Q * (m + 1) + esz e` steps or more and a
    depth no smaller than its steps (so that it runs out of steps, not depth), runs out of
    fuel, GoCore runs out of fuel too, at every depth from `K * d` on. -/
theorem calls_fuel (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (hQ : KszOK ds Q) (κ : String → Option Kind)
    (e : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ e = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (d : Nat) (hsd : s.steps ≤ d)
    (hq : Q * (g.steps + 1) + esz e ≤ s.steps) (hf : ((Spec.eval ds d e).run.run s).1 = .error .fuel) :
    ∀ N ≥ K * d, ((evalExpr cx N env le).run.run g).1 = .error .fuel :=
  fun N hN => ((sf_all ds cx P K sg Q hF hK hQ d).1 d (Nat.le_refl _) κ e k le hl s g env hi N hN).2 hsd hq hf

theorem inv3_steps {κ : String → Option Kind} {c : Spec.Core} {g : St} {env : Env} (m : Nat)
    (h : Inv3 cx P sg κ c g env) : Inv3 cx P sg κ c { g with steps := m } env :=
  ⟨⟨h.inv.loc, h.inv.out, h.inv.pre⟩, h.pkg, h.tenv, h.closed, h.globals, h.sync⟩

theorem shift_zero (g : St) : shift 0 g = g := by cases g; rfl

/-- **M5, step 3d (c): divergence.** If galaspec diverges on an expression the reference
    lowering accepts (it runs out of fuel with every budget `n`, at depth `n`), then
    GoCore's run of the lowering, from a related state, never ends normally: it runs out of
    fuel at every depth `N` and with every step budget `m`.

    The proof takes `n := kszOf ds * (m + 1) + esz e`: galaspec runs out at depth and
    budget `n`, so by `calls_fuel` GoCore does with `m` steps at every depth from
    `K * n` on, in particular at `N' := max N (K * n)`. At a depth `N` below `K * n`
    GoCore runs out too: were its run at `N` to end otherwise, step 3d (a)'s `simAll`
    (with no extra steps, from depth `N` to `N' ≥ N`) would end the run at `N'` the same
    way, not out of fuel. So "every `N`" is earned from the large depths by adequacy. -/
theorem calls_diverge (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (κ : String → Option Kind)
    (e : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ e = some (k, le)) (c : Spec.Core) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ c g env)
    (hdiv : ∀ n, ((Spec.eval ds n e).run.run { toCore := c, steps := n }).1 = .error .fuel) :
    ∀ N m, ((evalExpr cx N env le).run.run { g with steps := m }).1 = .error .fuel := by
  intro N m
  let n := kszOf ds * (m + 1) + esz e
  have hbig := calls_fuel ds cx P K sg (kszOf ds) hF hK (kszOf_ok ds) κ e k le hl { toCore := c, steps := n }
    { g with steps := m } env (inv3_steps cx P sg m hi) n (Nat.le_refl _) (Nat.le_refl _) (hdiv n)
    (max N (K * n)) (Nat.le_max_right _ _)
  rcases hr : (evalExpr cx N env le).run.run { g with steps := m } with ⟨r, g'⟩
  cases r with
  | error er =>
    cases er with
    | fuel => rfl
    | _ =>
      exfalso
      have h := ((simAll cx 0 N (max N (K * n)) (Nat.le_max_left _ _)).evalExpr env le).prop { g with steps := m }
        (by rw [hr]; trivial)
      rw [shift_zero, hr] at h
      rw [h] at hbig
      cases hbig
  | ok w =>
    exfalso
    have h := ((simAll cx 0 N (max N (K * n)) (Nat.le_max_left _ _)).evalExpr env le).prop { g with steps := m }
      (by rw [hr]; trivial)
    rw [shift_zero, hr] at h
    rw [h] at hbig
    cases hbig

end MainSF

end Gala.M5

