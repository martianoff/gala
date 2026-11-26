/-
  # M5, step 3d (c): divergence, the induction
-/
import Gala.M5.DivBase
namespace Gala.M5
open Gala.GoCore.Full

/-! ## The expressions -/

section ExprSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- **The lower bound and the fuel claim for expressions at depth `d`**: from a state with
    at least galaspec's steps, galaspec spends at most `Q` times GoCore's steps plus the
    expression's size; and where galaspec, its depth no less than its steps, runs out of
    fuel with `Q * (m + 1) + esz e` steps or more, GoCore with `m` steps runs out too. -/
def ExprSF (d : Nat) : Prop :=
  ∀ κ e k le, lowerC K sg κ e = some (k, le) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d ≤ N →
      (s.steps ≤ g.steps → Stut Q (esz e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g)) ∧
      (s.steps ≤ d → Q * (g.steps + 1) + esz e ≤ s.steps → ((Spec.eval ds d e).run.run s).1 = .error .fuel →
        ((evalExpr cx N env le).run.run g).1 = .error .fuel)

/-- An expression galaspec ends with a value, run by GoCore with any budget: GoCore runs out
    of fuel, or ends with a related value as with more steps (`down`), within the bound. -/
theorem expr_down {d : Nat} (hE : ExprSim ds cx P K sg d) (hS : ExprSF ds cx P K sg Q d)
    {κ : String → Option Kind} {e : Spec.Expr} {k : Kind} {le : Expr} (hl : lowerC K sg κ e = some (k, le))
    {s : Spec.St} {g : St} {env : Env} (hi : Inv3 cx P sg κ s.toCore g env) {N : Nat} (hN : K * d ≤ N)
    {v : Spec.Val} {s' : Spec.St} (hr : (Spec.eval ds d e).run.run s = (.ok v, s')) :
    ((evalExpr cx N env le).run.run g).1 = .error .fuel ∨ ∃ w g', (evalExpr cx N env le).run.run g = (.ok w, g') ∧
      VRel v w ∧ valKind v = some k ∧ (isLitE e = false → ¬ UInt w) ∧ SFrame s.toCore s'.toCore ∧
      GFrame g g' ∧ s.steps + Q * g'.steps ≤ s'.steps + Q * g.steps + esz e ∧ s'.steps ≤ s.steps := by
  have hi' : Inv3 cx P sg κ s.toCore (shift s.steps g) env := (inv3_shift _).2 hi
  have hsg : s.steps ≤ (shift s.steps g).steps := by simp []
  have h1 := hE κ e k le hl s _ env hi' hsg N hN
  have h2 := (hS κ e k le hl s _ env hi' N hN).1 hsg
  rw [hr] at h1 h2
  obtain ⟨w, g1, hg1, hvw, hkv, hu, hsf, hgf, hsp⟩ := h1
  have hmono : s'.steps ≤ s.steps := by have := hgf.2.2.2.2.1; unfold Spent at hsp; omega
  rw [hg1] at h2
  rcases down ((simAll cx s.steps N N (Nat.le_refl _)).evalExpr env le) g hg1 with hf | ⟨g0, hg0, rfl⟩
  · exact .inl hf
  · refine .inr ⟨w, g0, hg0, hvw, hkv, hu, hsf, (gframe_shift _).1 hgf, ?_, hmono⟩
    simp only [Stut] at h2
    rw [Nat.mul_add, Nat.mul_add] at h2
    omega

end ExprSF

section ExprParts
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem esz_pos (e : Spec.Expr) : 1 ≤ esz e := by cases e <;> simp [esz]

/-- galaspec's tick, paid for by the node's own size. -/
theorem stut_tick {z : Nat} {s₁ s : Spec.St} (hs : s.steps = s₁.steps + 1) {g : St}
    {sr : Except Spec.Stop β × Spec.St} {r : Except Stop α × St} (h : Stut Q z s₁ g sr r) :
    Stut Q (z + 1) s g sr r := by
  rcases sr with ⟨e | v, s'⟩ <;> rcases r with ⟨x, g'⟩
  · cases e <;> first | trivial | (simp only [Stut] at h ⊢; omega)
  · simp only [Stut] at h ⊢; omega

theorem stut_mono {z z' : Nat} (hz : z ≤ z') {s : Spec.St} {g : St}
    {sr : Except Spec.Stop β × Spec.St} {r : Except Stop α × St} (h : Stut Q z s g sr r) : Stut Q z' s g sr r := by
  rcases sr with ⟨e | v, s'⟩ <;> rcases r with ⟨x, g'⟩
  · cases e <;> first | trivial | (simp only [Stut] at h ⊢; omega)
  · simp only [Stut] at h ⊢; omega

theorem exprSF_zero : ExprSF ds cx P K sg Q 0 := by
  intro κ e k le _ s g env _ N _
  rw [eval_zero]
  exact ⟨fun _ => trivial, fun h1 h2 _ => by have := esz_pos e; omega⟩

/-- A call-free part: GoCore spends nothing, galaspec at most its size, and never runs out
    of fuel with that many steps. -/
theorem leaf_sf (κ : String → Option Kind) (e : Spec.Expr) (k : Kind) (le : Expr)
    (hl : lowerLeaf K κ e = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (d N : Nat) (hN : K ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d → Q * (g.steps + 1) + esz e ≤ s.steps → ((Spec.eval ds d e).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  have hcf : CFShape e := by
    unfold lowerLeaf at hl
    split at hl
    · exact lowerE_shape κ e k le (lowerX_some hl).2
    · cases hl
  refine ⟨fun _ => ?_, fun hd hq hf => ?_⟩
  · have h := leaf_sim ds cx P sg K κ e k le hl s g env hi d N hN
    have hsp := eval_spend ds e d hcf s
    rcases hr : (Spec.eval ds d e).run.run s with ⟨r, s'⟩
    rw [hr] at h hsp
    rcases r with er | v
    · cases er with
      | ret => exact h.elim
      | _ => trivial
    · obtain ⟨w, g', hg, -, -, -, -, hgf, -⟩ := h
      rw [hg]
      have := hgf.2.2.2.2.1
      simp only [Stut] at hsp ⊢
      have := Nat.mul_le_mul_left Q this
      omega
  · have := eval_fuelLt ds e d hcf (by omega) s hf
    omega

theorem paren_sf (d : Nat) (ih : ExprSF ds cx P K sg Q d) (hK : 1 ≤ K) (κ : String → Option Kind)
    (x : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ (.paren x) = some (k, le))
    (hc : hasCall x = true) (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env)
    (N : Nat) (hN : K * (d + 1) ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz (.paren x)) s g ((Spec.eval ds (d + 1) (.paren x)).run.run s)
      ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d + 1 → Q * (g.steps + 1) + esz (.paren x) ≤ s.steps →
      ((Spec.eval ds (d + 1) (.paren x)).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  obtain ⟨l, hlx, rfl⟩ := lowerC_paren K sg hl hc
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h⟩
  | succ k' =>
  rw [eval_paren_run ds d k' x s hs, evalExpr_paren]
  have h := ih κ x k l hlx { s with steps := k' } g env hi n (by rw [Nat.mul_succ] at hN; omega)
  refine ⟨fun hsg => ?_, fun hd hq hf => h.2 (by show k' ≤ d; omega) (by simp only [esz] at hq; show _ ≤ k'; omega) hf⟩
  exact stut_tick Q (by simp [hs]) (h.1 (by show k' ≤ g.steps; omega))

end ExprParts

section ExprParts2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- galaspec's bind ran out of fuel: its first part did, or its rest after a value. -/
theorem spec_bind_fuel {x : Spec.M α} {f : α → Spec.M β} {s : Spec.St}
    (h : ((x >>= f).run.run s).1 = .error .fuel) :
    (x.run.run s).1 = .error .fuel ∨ ∃ v s2, x.run.run s = (.ok v, s2) ∧ ((f v).run.run s2).1 = .error .fuel := by
  rw [Spec.run_bind_eq] at h
  rcases hx : x.run.run s with ⟨r, t⟩
  rw [hx] at h
  cases r with
  | error e => exact .inl (by simpa using h)
  | ok v => exact .inr ⟨v, t, rfl, h⟩

theorem spec_map_fuel {x : Spec.M α} {f : α → β} {s : Spec.St}
    (h : ((f <$> x).run.run s).1 = .error .fuel) : (x.run.run s).1 = .error .fuel := by
  rw [← bind_pure_comp, Spec.run_bind_eq] at h
  rcases hx : x.run.run s with ⟨r, t⟩
  rw [hx] at h
  cases r with
  | error e => simpa using h
  | ok a => exact absurd h (by simp)

theorem liftM_fuel {x : Spec.H α} (hx : HNoFuel x) {s : Spec.St} :
    ((liftM x : Spec.M α).run.run s).1 ≠ .error .fuel := by
  rw [Spec.run_liftM]; exact hx _

theorem unary_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d) (hK : 3 ≤ K)
    (κ : String → Option Kind) (op : String) (x : Spec.Expr) (k : Kind) (le : Expr)
    (hl : lowerC K sg κ (.unary op x) = some (k, le))
    (hc : hasCall x = true) (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env)
    (N : Nat) (hN : K * (d + 1) ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz (.unary op x)) s g ((Spec.eval ds (d + 1) (.unary op x)).run.run s)
      ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d + 1 → Q * (g.steps + 1) + esz (.unary op x) ≤ s.steps →
      ((Spec.eval ds (d + 1) (.unary op x)).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  obtain ⟨kx, l, hlx, hku, rfl⟩ := lowerC_unary K sg hl hc
  have hop : op ≠ "&" := by intro h; subst h; unfold unaryKind at hku; split at hku <;> simp_all
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h⟩
  | succ k' =>
  rw [eval_unary_run ds d k' op x s hs (isLitE_of_hasCall _ hc), evalExpr_unary _ _ _ _ _ hop]
  have hN' : K * d ≤ n + 2 := by rw [Nat.mul_succ] at hN; omega
  have h := ih κ x kx l hlx { s with steps := k' } g env hi (n + 2) hN'
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hE := ihE κ x kx l hlx { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 2) hN'
    have hS := h.1 (by show k' ≤ g.steps; omega)
    rw [Spec.run_bind_eq, grun_bind]
    rcases hr : (Spec.eval ds d x).run.run { s with steps := k' } with ⟨r, s'⟩
    rw [hr] at hE hS
    cases r with
    | error e =>
      cases e with
      | ret => exact hE.elim
      | _ => simp only; trivial
    | ok v =>
      obtain ⟨w, g', hg, hvw, hkv, hnu, -, -, -⟩ := hE
      rw [hg] at hS ⊢
      simp only
      obtain ⟨v', w', hv', hw', -, -, -⟩ := unary_sim cx n op v w kx k g' s'.toCore hvw hkv hku
        (hnu (isLitE_of_hasCall _ hc))
      rw [Spec.run_liftM, hv', hw']
      simp only [Stut] at hS ⊢
      simp only [esz]
      omega
  · rcases spec_bind_fuel hf with hf | ⟨v, s2, -, hf⟩
    · exact grun_bind_fuel (h.2 (by show k' ≤ d; omega) (by simp only [esz] at hq; show _ ≤ k'; omega) hf)
    · exact absurd hf (liftM_fuel (hnf_unop _ _))

end ExprParts2

section ExprParts3
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem binary_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d) (hK : 4 ≤ K)
    (κ : String → Option Kind) (op : String) (l r : Spec.Expr) (k : Kind) (le : Expr)
    (hl : lowerC K sg κ (.binary op l r) = some (k, le))
    (hc : (hasCall l || hasCall r) = true) (hand : op ≠ "&&") (hor : op ≠ "||") (s : Spec.St) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz (.binary op l r)) s g ((Spec.eval ds (d + 1) (.binary op l r)).run.run s)
      ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d + 1 → Q * (g.steps + 1) + esz (.binary op l r) ≤ s.steps →
      ((Spec.eval ds (d + 1) (.binary op l r)).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  obtain ⟨kl, ll, kr, lr, hll, hlr, hb, hsh, rfl⟩ := lowerC_binary K sg hl hc
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h⟩
  | succ k' =>
  rw [eval_binary_run ds d k' op l r s hs hand hor, evalExpr_binary _ _ _ _ _ _ hand hor hi.inv.pre.1]
  have hN' : K * d ≤ n + 3 := by rw [Nat.mul_succ] at hN; omega
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hL := ihE κ l kl ll hll { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 3) hN'
    have hSL := (ih κ l kl ll hll { s with steps := k' } g env hi (n + 3) hN').1 (by show k' ≤ g.steps; omega)
    rw [Spec.run_bind_eq, grun_bind]
    rcases hrl : (Spec.eval ds d l).run.run { s with steps := k' } with ⟨rl, sl⟩
    rw [hrl] at hL hSL
    cases rl with
    | error e => cases e with
      | ret => exact hL.elim
      | _ => simp only; trivial
    | ok vl =>
    obtain ⟨wl, gl, hg, hvl, hkl, hul, hsfl, hgfl, hspl⟩ := hL
    rw [hg] at hSL ⊢
    simp only
    have hsgl := spent_le hspl (by show k' ≤ g.steps; omega)
    have hR := ihE κ r kr lr hlr sl gl env (inv3_frame hi hsfl hgfl) hsgl (n + 3) hN'
    have hSR := (ih κ r kr lr hlr sl gl env (inv3_frame hi hsfl hgfl) (n + 3) hN').1 hsgl
    rw [Spec.run_bind_eq, grun_bind]
    rcases hrr : (Spec.eval ds d r).run.run sl with ⟨rr, sr⟩
    rw [hrr] at hR hSR
    cases rr with
    | error e => cases e with
      | ret => exact hR.elim
      | _ => simp only; trivial
    | ok vr =>
    obtain ⟨wr, gr, hg, hvr, hkr, hur, hsfr, hgfr, hspr⟩ := hR
    rw [hg] at hSR ⊢
    simp only
    have hnu : ¬ (UInt wl ∧ UInt wr) := by
      rintro ⟨h1, h2⟩
      simp only [Bool.or_eq_true] at hc
      rcases hc with hc | hc
      · exact nu_of_call rfl hul hc h1
      · exact nu_of_call rfl hur hc h2
    have hbs := binary_sim cx n op vl vr wl wr kl kr k gr sr.toCore hvl hvr hkl hkr hb hand hor hnu
      (fun hsh' => hul (hsh hsh'))
    rw [Spec.run_liftM]
    have hro := readOnly_binop op vl vr sr.toCore
    rcases hbr : (Spec.binop op vl vr).run.run sr.toCore with ⟨rb, cb⟩
    rw [hbr] at hbs hro
    simp only at hro
    subst hro
    cases rb with
    | error e => cases e with
      | ret v => exact (noRet_binop op vl vr sr.toCore v (by rw [hbr])).elim
      | _ => simp only; trivial
    | ok v =>
      obtain ⟨w, hg, -⟩ := hbs
      rw [hg]
      simp only [Stut] at hSL hSR ⊢
      simp only [esz]
      omega
  · simp only [Nat.mul_succ, esz] at hq
    rcases spec_bind_fuel hf with hf | ⟨vl, sl, hrl, hf⟩
    · exact grun_bind_fuel ((ih κ l kl ll hll { s with steps := k' } g env hi (n + 3) hN').2
        (by show k' ≤ d; omega) (by rw [Nat.mul_succ]; show _ ≤ k'; omega) hf)
    rcases expr_down ds cx P K sg Q ihE ih (s := { s with steps := k' }) hll hi hN' hrl with hgf | ⟨wl, gl, hg, -, -, -, hsfl, hgfl, hb, hmono⟩
    · exact grun_bind_fuel hgf
    rw [grun_bind, hg]
    simp only
    rcases spec_bind_fuel hf with hf | ⟨vr, sr, -, hf⟩
    · refine grun_bind_fuel ((ih κ r kr lr hlr sl gl env (inv3_frame hi hsfl hgfl) (n + 3) hN').2
        (by dsimp only at hmono; omega) ?_ hf)
      dsimp only at hb
      rw [Nat.mul_succ]
      omega
    · exact absurd hf (liftM_fuel (hnf_binop _ _ _))

end ExprParts3

section ExprParts4
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem andor_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d) (hK : 4 ≤ K)
    (κ : String → Option Kind) (op : String) (hop : op = "&&" ∨ op = "||") (l r : Spec.Expr) (k : Kind)
    (le : Expr) (hl : lowerC K sg κ (.binary op l r) = some (k, le))
    (hc : (hasCall l || hasCall r) = true) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz (.binary op l r)) s g ((Spec.eval ds (d + 1) (.binary op l r)).run.run s)
      ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d + 1 → Q * (g.steps + 1) + esz (.binary op l r) ≤ s.steps →
      ((Spec.eval ds (d + 1) (.binary op l r)).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  obtain ⟨kl, ll, kr, lr, hll, hlr, hb, -, rfl⟩ := lowerC_binary K sg hl hc
  have hkb : kl = .bool ∧ kr = .bool ∧ k = .bool := by
    rcases hop with rfl | rfl <;> (unfold binaryKind at hb; split at hb <;> simp_all)
  obtain ⟨rfl, rfl, rfl⟩ := hkb
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by simp [esz] at h⟩
  | succ k' =>
  have hN' : K * d ≤ n + 3 := by rw [Nat.mul_succ] at hN; omega
  have hIL := ih κ l .bool ll hll { s with steps := k' } g env hi (n + 3) hN'
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hL := ihE κ l .bool ll hll { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 3) hN'
    have hSL := hIL.1 (by show k' ≤ g.steps; omega)
    rcases hop with rfl | rfl
    all_goals
      first
      | rw [eval_and_run ds d k' l r s hs, evalExpr_and]
      | rw [eval_or_run ds d k' l r s hs, evalExpr_or]
      rw [Spec.run_bind_eq, grun_bind]
      rcases hrl : (Spec.eval ds d l).run.run { s with steps := k' } with ⟨rl, sl⟩
      rw [hrl] at hL hSL
      cases rl with
      | error e => cases e with
        | ret => exact hL.elim
        | _ => simp only; trivial
      | ok vl =>
      obtain ⟨wl, gl, hg, hvl, hkl, hul, hsfl, hgfl, hspl⟩ := hL
      obtain ⟨b, t, rfl, rfl, htb⟩ := vrel_bool hvl hkl
      rw [hg] at hSL ⊢
      simp only
      rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
      have hsgl := spent_le hspl (by show k' ≤ g.steps; omega)
      have hR := ihE κ r .bool lr hlr sl gl env (inv3_frame hi hsfl hgfl) hsgl (n + 3) hN'
      have hSR := (ih κ r .bool lr hlr sl gl env (inv3_frame hi hsfl hgfl) (n + 3) hN').1 hsgl
      cases b
      all_goals
        simp only [Bool.false_eq_true, ite_false, ite_true, run_pureM]
        first
        | (simp only [Stut] at hSL ⊢; simp only [esz]; have : ({ s with steps := k' } : Spec.St).steps = k' := rfl; show _ ≤ _; (try dsimp only at hSL ⊢); omega)
        | (rw [Spec.run_bind_eq, grun_bind]
           rcases hrr : (Spec.eval ds d r).run.run sl with ⟨rr, sr⟩
           rw [hrr] at hR hSR
           cases rr with
           | error e => cases e with
             | ret => exact hR.elim
             | _ => simp only; trivial
           | ok vr =>
           obtain ⟨wr, gr, hg, hvr, hkr, -, hsfr, hgfr, hspr⟩ := hR
           obtain ⟨b', t', rfl, rfl, htb'⟩ := vrel_bool hvr hkr
           rw [hg] at hSR ⊢
           simp only
           rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
           simp only [Stut] at hSL hSR ⊢
           simp only [esz]
           have : ({ s with steps := k' } : Spec.St).steps = k' := rfl
           show _ ≤ _
           (try dsimp only at hSL hSR ⊢)
           omega)
  · simp only [Nat.mul_succ, esz] at hq
    rcases hop with rfl | rfl
    all_goals
      first
      | rw [eval_and_run ds d k' l r s hs] at hf; rw [evalExpr_and]
      | rw [eval_or_run ds d k' l r s hs] at hf; rw [evalExpr_or]
      rcases spec_bind_fuel hf with hf | ⟨vl, sl, hrl, hf⟩
      · exact grun_bind_fuel (hIL.2 (by show k' ≤ d; omega) (by rw [Nat.mul_succ]; show _ ≤ k'; omega) hf)
      rcases expr_down ds cx P K sg Q ihE ih (s := { s with steps := k' }) hll hi hN' hrl with
        hgf | ⟨wl, gl, hg, hvl, hkl, -, hsfl, hgfl, hbd, hmono⟩
      · exact grun_bind_fuel hgf
      obtain ⟨b, t, rfl, rfl, htb⟩ := vrel_bool hvl hkl
      rw [grun_bind, hg]
      simp only
      rw [grun_bind, gasBool]
      rcases spec_bind_fuel hf with hf | ⟨b0, s3, hb0, hf⟩
      · exact absurd hf (liftM_fuel (hnf_asBool _))
      rw [Spec.run_liftM, spec_asBool_run] at hb0
      simp only [] at hb0
      obtain ⟨rfl, rfl⟩ := hb0
      dsimp only at hbd hmono
      cases b
      all_goals
        simp only [Bool.false_eq_true, ite_false, ite_true] at hf ⊢
        first
        | (simp at hf; done)
        | (rcases spec_bind_fuel hf with hf | ⟨vr, sr, -, hf⟩
           · refine grun_bind_fuel ((ih κ r .bool lr hlr sl gl env (inv3_frame hi hsfl hgfl) (n + 3) hN').2
               (by omega) (by rw [Nat.mul_succ]; omega) (by simpa using hf))
           · exact absurd (spec_map_fuel hf) (liftM_fuel (hnf_asBool _)))

end ExprParts4

section ArgsSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem stut_congr {z : Nat} {s : Spec.St} {g : St} {sr : Except Spec.Stop β × Spec.St}
    {r : Except Stop α × St} {r' : Except Stop α' × St} (he : r'.2 = r.2) (h : Stut Q z s g sr r) :
    Stut Q z s g sr r' := by
  rcases sr with ⟨e | v, s'⟩ <;> rcases r with ⟨x, g'⟩ <;> rcases r' with ⟨x', g''⟩ <;> simp only at he <;> subst he
  · cases e <;> first | trivial | exact h
  · exact h

/-- GoCore's `evalArgs` ends where the arguments' `mapM` does, and runs out of fuel with it. -/
theorem evalArgs_mapM (M : Nat) (env : Env) (las : List Expr) (g : St) :
    ((evalArgs cx (M + 1) env las).run.run g).2 = ((las.mapM (evalExpr cx M env)).run.run g).2 ∧
    (((evalArgs cx (M + 1) env las).run.run g).1 = .error .fuel ↔
      ((las.mapM (evalExpr cx M env)).run.run g).1 = .error .fuel) := by
  match las with
  | [a] =>
    simp only [evalArgs, List.mapM_cons, List.mapM_nil, grun_bind]
    rcases (evalExpr cx M env a).run.run g with ⟨r, g'⟩
    cases r <;> simp [] <;> rfl
  | [] => simp [evalArgs]
  | a :: b :: rest => simp [evalArgs]

/-- The arguments, left to right. -/
theorem mapM_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d)
    (κ : String → Option Kind) :
    ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr), lowerArgs K sg κ ks args = some las →
    ∀ (s : Spec.St) (g : St) (env : Env), Inv3 cx P sg κ s.toCore g env → ∀ M, K * d ≤ M →
    (s.steps ≤ g.steps → Stut Q (eszs args) s g ((Spec.evalList ds d args).run.run s)
      ((las.mapM (evalExpr cx M env)).run.run g)) ∧
    (s.steps ≤ d → Q * (g.steps + 1) + eszs args ≤ s.steps → ((Spec.evalList ds d args).run.run s).1 = .error .fuel →
      ((las.mapM (evalExpr cx M env)).run.run g).1 = .error .fuel)
  | [], [], las, h, s, g, env, _, M, _ => by
    simp only [lowerArgs, Option.some.injEq] at h; subst h
    rw [evalList_nil_run]
    refine ⟨fun _ => ?_, fun _ _ h => by simp at h⟩
    simp only [List.mapM_nil, run_pureM, Stut, eszs]
    omega
  | k :: ks, a :: as, las, h, s, g, env, hi, M, hM => by
    simp only [lowerArgs] at h
    split at h
    · cases h
    split at h
    · rename_i k' l hl
      split at h
      · rename_i hk; subst hk
        simp only [Option.map_eq_some_iff] at h
        obtain ⟨ls, hls, rfl⟩ := h
        have hIA := ih κ a k' l hl s g env hi M hM
        refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
        · have hA := ihE κ a k' l hl s g env hi hsg M hM
          have hSA := hIA.1 hsg
          rw [evalList_cons_run, List.mapM_cons, grun_bind]
          rcases hra : (Spec.eval ds d a).run.run s with ⟨ra, sa⟩
          rw [hra] at hA hSA
          cases ra with
          | error e => cases e with
            | ret => exact hA.elim
            | _ => simp only; trivial
          | ok v =>
          obtain ⟨w, ga, hg, -, -, -, hsfa, hgfa, hspa⟩ := hA
          rw [hg] at hSA ⊢
          simp only
          have hsga := spent_le hspa hsg
          have hR := mapM_sim ds cx P K sg d ihE κ ks as ls hls sa ga env (inv3_frame hi hsfa hgfa) hsga M hM
          have hSR := (mapM_sf d ihE ih κ ks as ls hls sa ga env (inv3_frame hi hsfa hgfa) M hM).1 hsga
          rw [grun_bind]
          rcases hrr : (Spec.evalList ds d as).run.run sa with ⟨rr, sr⟩
          rw [hrr] at hR hSR
          cases rr with
          | error e => cases e with
            | ret => exact hR.elim
            | _ => simp only; trivial
          | ok vs =>
          obtain ⟨ws, gr, hg, -⟩ := hR
          rw [hg] at hSR ⊢
          simp only [run_pureM, Stut] at hSA hSR ⊢
          simp only [eszs]
          omega
        · simp only [eszs] at hq
          rw [evalList_cons_run] at hf
          rw [List.mapM_cons]
          rcases hra : (Spec.eval ds d a).run.run s with ⟨ra, sa⟩
          rw [hra] at hf
          cases ra with
          | error e =>
            simp only at hf
            cases hf
            exact grun_bind_fuel (hIA.2 hd (by omega) (by rw [hra]))
          | ok v =>
          rcases expr_down ds cx P K sg Q ihE ih hl hi hM hra with hgf | ⟨w, ga, hg, -, -, -, hsfa, hgfa, hbd, hmono⟩
          · exact grun_bind_fuel hgf
          rw [grun_bind, hg]
          simp only at hf ⊢
          rcases hrr : (Spec.evalList ds d as).run.run sa with ⟨rr, sr⟩
          rw [hrr] at hf
          cases rr with
          | ok => simp at hf
          | error e =>
          simp only at hf
          cases hf
          exact grun_bind_fuel ((mapM_sf d ihE ih κ ks as ls hls sa ga env (inv3_frame hi hsfa hgfa) M hM).2
            (by omega) (by rw [Nat.mul_succ] at hq ⊢; omega) (by rw [hrr]))
      · cases h
    · cases h
  | [], _ :: _, _, h, _, _, _, _, _, _ | _ :: _, [], _, h, _, _, _, _, _, _ => by simp [lowerArgs] at h

/-- An argument list galaspec ends, run by GoCore's `evalArgs` with any budget. -/
theorem args_down {d : Nat} (hE : ExprSim ds cx P K sg d) (hS : ExprSF ds cx P K sg Q d)
    {κ : String → Option Kind} {ks : List Kind} {args : List Spec.Expr} {las : List Expr}
    (hl : lowerArgs K sg κ ks args = some las)
    {s : Spec.St} {g : St} {env : Env} (hi : Inv3 cx P sg κ s.toCore g env) {M : Nat} (hM : K * d ≤ M)
    {vs : List Spec.Val} {s' : Spec.St} (hr : (Spec.evalList ds d args).run.run s = (.ok vs, s')) :
    ((evalArgs cx (M + 1) env las).run.run g).1 = .error .fuel ∨ ∃ ws g',
      (evalArgs cx (M + 1) env las).run.run g = (.ok ws, g') ∧ KRel ks vs ws ∧ SFrame s.toCore s'.toCore ∧
      GFrame g g' ∧ s.steps + Q * g'.steps ≤ s'.steps + Q * g.steps + eszs args ∧ s'.steps ≤ s.steps := by
  have hi' : Inv3 cx P sg κ s.toCore (shift s.steps g) env := (inv3_shift _).2 hi
  have hsg : s.steps ≤ (shift s.steps g).steps := by simp
  have h1 := args_sim ds cx P K sg d hE κ ks args las hl s _ env hi' hsg M hM
  have h2 := (mapM_sf ds cx P K sg Q d hE hS κ ks args las hl s _ env hi' M hM).1 hsg
  have h2 := stut_congr Q (evalArgs_mapM cx M env las (shift s.steps g)).1 h2
  rw [hr] at h1 h2
  obtain ⟨ws, g1, hg1, hkr, hsf, hgf, hsp⟩ := h1
  have hmono : s'.steps ≤ s.steps := by have := hgf.2.2.2.2.1; unfold Spent at hsp; omega
  rw [hg1] at h2
  rcases down ((simAll cx s.steps (M + 1) (M + 1) (Nat.le_refl _)).evalArgs env las) g hg1 with
    hf | ⟨g0, hg0, rfl⟩
  · exact .inl hf
  · refine .inr ⟨ws, g0, hg0, hkr, hsf, (gframe_shift _).1 hgf, ?_, hmono⟩
    simp only [Stut] at h2
    rw [Nat.mul_add, Nat.mul_add] at h2
    omega

end ArgsSF

section CallSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- The fuel claim and the bound for the bodies a call runs (`BodySim`'s). -/
def BodySF (d : Nat) : Prop :=
  ∀ f F xks ρ lb, ds.funcs.lookup f = some F → sigOf F = some (xks, ρ) → lowerFun K sg f F = some lb →
    ∀ (s : Spec.St) (g : St) (env : Env) (n : Nat), Inv3 cx P sg (paramKinds xks) s.toCore g env →
      ParamCells n xks env g → ∀ N, K * d + 3 ≤ N →
        (s.steps ≤ g.steps → Stut Q (esz F.body) s g ((Spec.eval ds d F.body).run.run s)
          ((execStmts cx N env lb).run.run g)) ∧
        (s.steps ≤ d → Q * (g.steps + 1) + esz F.body ≤ s.steps → ((Spec.eval ds d F.body).run.run s).1 = .error .fuel →
          ((execStmts cx N env lb).run.run g).1 = .error .fuel)

theorem paramCells_shift {n : Nat} {xks : List (String × Kind)} {env : Env} {g : St} (k : Nat) :
    ParamCells n xks env (shift k g) ↔ ParamCells n xks env g := Iff.rfl

/-- The callee's state: related to galaspec's frame of the arguments. -/
theorem callee_setup {f : String} {F : Spec.Func} {xks : List (String × Kind)} {ρ : Kind}
    {lb : List Stmt} {fi : Nat} {p : PkgCtx} (hpk : cx.pkgs.get? P = some p)
    (hobjs : ∀ x ∈ ["true", "false", "int", "bool", "string"], p.objs.get? x = none)
    (hsig : sigOf F = some (xks, ρ)) (hlb : lowerFun K sg f F = some lb)
    {κ : String → Option Kind} {sa : Spec.St} {ga : St} {env : Env} {vs : List Spec.Val} {ws : List Val}
    (hkr : KRel (xks.map (·.2)) vs ws) (hia : Inv3 cx P sg κ sa.toCore ga env) (k2 : Nat) (hk2 : k2 ≤ ga.steps) :
    Inv3 cx P sg (paramKinds xks) { sa with locals := specFrame F.params vs }.toCore
      { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers }
      (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1 ∧
    ParamCells ga.cells.size xks (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1
      { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers } ∧
    (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2.steps = k2 ∧
    GFrame ga (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 := by
  obtain ⟨hnames, htys, hres⟩ := sigOf_facts hsig
  have hnm := lowerFun_names hsig hlb
  have hfr := frameRel_params xks F.params vs ws (fun _ => none) { sa.toCore with locals := [] }
    (calleeEnv P fi).push { ga with steps := k2 } hnames hkr (fun x hx => (hnm x hx).1)
    ⟨fun _ _ h => (by cases h), fun x _ => ⟨rfl, by simp [calleeEnv, Env.push, Env.lookup, List.findSome?]⟩⟩
  have hzip : ∀ a ∈ (xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws,
      a.1.1 ≠ "_" ∧ a.1.1 ≠ "true" ∧ a.1.1 ≠ "false" := by
    intro a ha
    have := (List.of_mem_zip ha).1
    simp only [List.mem_map] at this
    obtain ⟨xk, hxk, he⟩ := this
    rw [← he]
    exact hnm xk.1 (List.mem_map_of_mem hxk)
  obtain ⟨hepkg, hetenv, hepre⟩ := params_env _ ((calleeEnv P fi).push, { ga with steps := k2 }) cx hzip
    (predeclared_callee cx P fi p hpk hobjs)
  obtain ⟨hpst, hpsteps⟩ := params_st ((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws)
    ((calleeEnv P fi).push, { ga with steps := k2 })
  have hgac : GFrame ga { ga with steps := k2 } := gframe_refl' ga k2 hk2
  have hpst' := gframe_trans hgac hpst
  refine ⟨?_, ?_, ?_, hpst'⟩
  · refine ⟨⟨hfr.1, ?_, hepre⟩, hepkg, hetenv, fun x hx => ⟨(hfr.2 x hx).1, fun _ => (hfr.2 x hx).2⟩, ?_,
      by have := hpst'.2.2.2.1; have := hia.sync; simp only [paramsBound] at this ⊢; omega⟩
    · show sa.out = (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2.out
      rw [paramsBound, gframe_out hpst']; exact hia.inv.out
    · intro x hx; exact hia.globals x hx
  · exact params_cells xks ws (fun _ => none) _ { ga with steps := k2 } ga.cells.size
      (argsRel_length (argsRel_of_krel hkr)) (fun x hx => (hnm x hx).1) hia.sync (Nat.le_refl _)
      ⟨fun _ _ h => absurd h (by simp), fun _ _ _ _ _ h => absurd h (by simp)⟩
  · simp only [paramsBound]; rw [hpsteps]

end CallSF

section CallSF2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- galaspec's call runs out of fuel only where its body does. -/
theorem callFunc_fuel (d : Nat) (F : Spec.Func) (vs : List Spec.Val) (s : Spec.St)
    (hp : ∀ p ∈ F.params, ∃ k, p.type = some (.name (kindName k))) (ρ : Kind)
    (hr : F.result = some (.name (kindName ρ))) (hl : vs.length = F.params.length)
    (h : ((Spec.callFunc ds d F vs).run.run s).1 = .error .fuel) :
    ((Spec.eval ds d F.body).run.run { s with locals := specFrame F.params vs }).1 = .error .fuel := by
  rcases hrun : (Spec.runIn ds d (specFrame F.params vs) F.body).run.run s with ⟨r, t⟩
  have hres := runIn_res ds d (specFrame F.params vs) F.body s
  rw [hrun] at hres
  by_cases hu : r = .ok .unit
  · subst hu
    exfalso
    rw [Spec.callFunc.eq_def] at h
    simp only [hl, bne_self_eq_false, Bool.false_eq_true, ite_false] at h
    rw [Spec.run_bind_eq, forIn_fold _ (fun _ _ => PUnit.unit)
      (fun p : Spec.Param => ∃ k, p.type = some (.name (kindName k))) ?step _ _ _ hp] at h
    case step =>
      rintro p u s' ⟨k, hk⟩
      simp only [Spec.run_bind_eq, run_discard, Spec.run_liftM, hk, Spec.checkOptTy, checkTy_kind]
      rfl
    simp only [hr, Spec.run_bind_eq, run_discard, Spec.run_liftM, checkTy_kind] at h
    unfold specFrame at hrun
    simp only [hrun] at h
    revert h
    simp only [Spec.run_liftM]
    exact fun h => nomatch h
  · rw [callFunc_run ds d F vs s hp ρ hr hl r t hrun hu] at h
    simp only at h
    subst h
    revert hres
    rcases (Spec.eval ds d F.body).run.run { s with locals := specFrame F.params vs } with ⟨r', t'⟩
    rcases r' with e | v
    · cases e <;> intro hres <;> first | rfl | cases hres
    · intro hres; cases hres

end CallSF2

section CallSF3
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem call_sf (d : Nat) (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (hQ : KszOK ds Q)
    (ihE : ExprSim ds cx P K sg d) (ihB : BodySim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d)
    (ihBF : BodySF ds cx P K sg Q d) (κ : String → Option Kind) (f : String) (args : List Spec.Expr) (k : Kind)
    (le : Expr) (hl : lowerC K sg κ (.call (.name f) args) = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    (s.steps ≤ g.steps → Stut Q (esz (.call (.name f) args)) s g
      ((Spec.eval ds (d + 1) (.call (.name f) args)).run.run s) ((evalExpr cx N env le).run.run g)) ∧
    (s.steps ≤ d + 1 → Q * (g.steps + 1) + esz (.call (.name f) args) ≤ s.steps →
      ((Spec.eval ds (d + 1) (.call (.name f) args)).run.run s).1 = .error .fuel →
      ((evalExpr cx N env le).run.run g).1 = .error .fuel) := by
  obtain ⟨hκ, ks, las, hsgf, hlas, rfl⟩ := lowerC_call K sg hl
  obtain ⟨p, hpk, hobjs, hfun⟩ := hF.pkg
  obtain ⟨F, xks, ρ, lb, fi, hFl, hsig, hks, hlb, hobj⟩ := hfun f _ hsgf
  simp only [Prod.mk.injEq] at hks
  obtain ⟨rfl, hkρ⟩ := hks
  subst k
  obtain ⟨hnames, htys, hres⟩ := sigOf_facts hsig
  obtain ⟨hargsU, hargsL⟩ := lowerArgs_facts K sg κ _ args las hlas
  obtain ⟨m, rfl⟩ : ∃ m, N = m + 6 := ⟨N - 6, by rw [Nat.mul_succ] at hN; omega⟩
  have hszb := hQ.2 f F hFl
  simp only [esz]
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
  | succ k1 =>
  obtain ⟨hlocf, henvf⟩ := hi.closed f hκ
  have henvf := henvf (hF.noTmp f (by rw [hsgf]; rfl))
  have hglob := hi.globals f (by rw [hsgf]; rfl)
  have hresolve : Spec.resolve ds s.locals s.globals f = .func F := by
    simp [Spec.resolve, Spec.lookupVar, hlocf, hglob, hFl]
  rw [eval_call_run ds d k1 f args F s hs hresolve]
  have hpk' : cx.pkgs.get? env.pkg = some p := hi.pkg ▸ hpk
  rw [show m + 6 = (m + 5) + 1 from rfl, evalExpr_call, grun_bind,
    evalCall_fn cx env f p fi (lowerSigOf xks ρ) lb hpk' henvf hi.tenv hobj (m + 3), grun_bind]
  have hlenP : F.params.length = xks.length := by
    have := congrArg List.length hnames; simpa using this
  have hfa : ∀ s', (Spec.funcArgs ds d F args).run.run s' = (Spec.evalList ds d args).run.run s' := by
    intro s'
    rw [Spec.funcArgs.eq_def]
    refine bindArgs_pos ds d _ _ _ args hargsU (by simp [hargsL, hlenP]) (fun j hj => ?_) s'
    rw [htys]
    simp only [List.length_map] at hj
    exact ⟨kindName xks[j].2, by simp [List.getD_eq_getElem?_getD, hlenP ▸ hj]⟩
  rw [Spec.run_bind_eq, hfa]
  have hN' : K * d ≤ m + 3 := by rw [Nat.mul_succ] at hN; omega
  have hnames' := nameOwner_kinds cx P fi p hpk hobjs
  have hcall : ∀ (sa : Spec.St) (vs : List Spec.Val), vs.length = F.params.length → ∀ r t',
      r ≠ .ok .unit → (Spec.runIn ds d (specFrame F.params vs) F.body).run.run sa = (r, t') →
      (Spec.callFunc ds d F vs).run.run sa = (r, t') := fun sa vs hlenV r t' hr hrun =>
    callFunc_run ds d F vs sa (params_typed htys) ρ hres hlenV r t' hrun hr
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · -- The bound.
    have hA := args_sim ds cx P K sg d ihE κ _ args las hlas { s with steps := k1 } g env hi
      (by show k1 ≤ g.steps; omega) (m + 3) hN'
    have hSA := stut_congr Q (evalArgs_mapM cx (m + 3) env las g).1
      ((mapM_sf ds cx P K sg Q d ihE ih κ _ args las hlas { s with steps := k1 } g env hi (m + 3) hN').1
        (by show k1 ≤ g.steps; omega))
    rcases hra : (Spec.evalList ds d args).run.run { s with steps := k1 } with ⟨ra, sa⟩
    rw [hra] at hA hSA
    cases ra with
    | error e => cases e with
      | ret => exact hA.elim
      | _ => trivial
    | ok vs =>
    obtain ⟨ws, ga, hg, hkr, hsfa, hgfa, hspa⟩ := hA
    rw [hg] at hSA ⊢
    simp only
    rw [hi.pkg, callValue_fn cx P f p fi _ lb hpk hobj hF.notStd (m + 3) ws ga]
    obtain ⟨k2, hk2⟩ : ∃ k2, ga.steps = k2 + 1 := ⟨ga.steps - 1, by unfold Spent at hspa; simp at hspa; omega⟩
    have hia := inv3_frame hi hsfa hgfa
    have hlenV : vs.length = F.params.length := by rw [hlenP, krel_length hkr, List.length_map]
    obtain ⟨hic, hpc, hpsteps, -⟩ := callee_setup cx P K sg (fi := fi) hpk hobjs hsig hlb hkr hia k2 (by omega)
    have hsb : sa.steps ≤ k2 := by unfold Spent at hspa; simp at hspa; omega
    have hB := ihB f F xks ρ lb hFl hsig hlb { sa with locals := specFrame F.params vs } _ _ ga.cells.size hic
      (by show sa.steps ≤ _; rw [hpsteps]; exact hsb) hpc (m + 2) (by rw [Nat.mul_succ] at hN; omega)
    have hSB := (ihBF f F xks ρ lb hFl hsig hlb { sa with locals := specFrame F.params vs } _ _ ga.cells.size hic
      hpc (m + 2) (by rw [Nat.mul_succ] at hN; omega)).1 (by show sa.steps ≤ _; rw [hpsteps]; exact hsb)
    rcases hrb : (Spec.eval ds d F.body).run.run { sa with locals := specFrame F.params vs } with ⟨rb, t⟩
    rw [hrb] at hB hSB
    have hfin : ∀ v, (rb = .ok v ∨ rb = .error (.ret v)) → Stut Q (eszs args + 1 + 1) s g
        ((Spec.callFunc ds d F vs).run.run sa)
        ((do Val.single (← callBody cx (m + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some lb) none ws false)).run.run ga) := by
      intro v hv
      have hret : RetOKP ga.cells.size ρ { sa with locals := specFrame F.params vs }
          { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers } v t
          ((execStmts cx (m + 2) (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1 lb).run.run
            { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers }) := by
        rcases hv with rfl | rfl <;> exact hB
      have hSB' : sa.steps + Q * ((execStmts cx (m + 2) (paramsBound xks ws (calleeEnv P fi).push
          { ga with steps := k2 }).1 lb).run.run { (paramsBound xks ws (calleeEnv P fi).push
          { ga with steps := k2 }).2 with defers := [] :: ga.defers }).2.steps ≤
          t.steps + Q * k2 + esz F.body := by
        rcases hv with rfl | rfl <;>
        · revert hSB
          rcases (execStmts cx (m + 2) _ lb).run.run _ with ⟨x, g1⟩
          simp only [Stut]
          rw [hpsteps]
          intro h; exact h
      obtain ⟨w, g1, hgo, hvw, hkv, -, -, -, hgf1, -⟩ := hret
      rw [hgo] at hSB'
      have hv1 : v ≠ .unit := by intro h; subst h; cases hvw
      rw [hcall sa vs hlenV (.ok v) _ (fun h => hv1 (by cases h; rfl)) (by rw [runIn_res, hrb]; rcases hv with rfl | rfl <;> rfl)]
      rw [grun_bind, callBody_ret cx m P fi xks ρ lb ws ga k2 hk2 (argsRel_of_krel hkr) hnames' v w hvw hkv g1
        hgo (by rw [gframeP_defers hgf1])]
      simp only [Stut] at hSA hSB' ⊢
      have : ({ s with steps := k1 } : Spec.St).steps = k1 := rfl
      show _ ≤ _
      dsimp only at hSA hSB' ⊢
      rw [hk2, Nat.mul_succ] at hSA
      omega
    have hfin' := fun v hv => stut_mono Q (z' := 1 + eszs args + 1) (by omega) (hfin v hv)
    cases rb with
    | ok v =>
      have h := hfin' v (.inl rfl)
      rw [grun_bind] at h
      simp only [calleeEnv] at h
      exact h
    | error e =>
      cases e with
      | ret v =>
        have h := hfin' v (.inr rfl)
        rw [grun_bind] at h
        simp only [calleeEnv] at h
        exact h
      | panic pc msg =>
        rw [hcall sa vs hlenV (.error (.panic pc msg)) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
        trivial
      | fuel =>
        rw [hcall sa vs hlenV (.error .fuel) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
        trivial
      | stuck m' =>
        rw [hcall sa vs hlenV (.error (.stuck m')) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
        trivial
      | unsupported a b =>
        rw [hcall sa vs hlenV (.error (.unsupported a b)) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
        trivial
  · -- The fuel claim.
    have hq' : Q * g.steps + Q + (1 + eszs args + 1) ≤ k1 + 1 := by
      rw [Nat.mul_succ] at hq; omega
    rcases hra : (Spec.evalList ds d args).run.run { s with steps := k1 } with ⟨ra, sa⟩
    rw [hra] at hf
    cases ra with
    | error e =>
      simp only at hf
      cases hf
      have := (mapM_sf ds cx P K sg Q d ihE ih κ _ args las hlas { s with steps := k1 } g env hi (m + 3) hN').2
        (by show k1 ≤ d; omega) (by show _ ≤ k1; rw [Nat.mul_succ]; omega) (by rw [hra])
      have hgf := (evalArgs_mapM cx (m + 3) env las g).2.2 this
      rcases hga : (evalArgs cx (m + 3 + 1) env las).run.run g with ⟨r1, g1⟩
      rw [hga] at hgf; simp only at hgf; subst hgf; rfl
    | ok vs =>
    simp only at hf
    rcases args_down ds cx P K sg Q ihE ih hlas (s := { s with steps := k1 }) hi hN' hra with
      hgf | ⟨ws, ga, hg, hkr, hsfa, hgfa, hbd, hmono⟩
    · rcases hga : (evalArgs cx (m + 3 + 1) env las).run.run g with ⟨r1, g1⟩
      rw [hga] at hgf; simp only at hgf; subst hgf; rfl
    rw [hg]
    simp only
    rw [hi.pkg, callValue_fn cx P f p fi _ lb hpk hobj hF.notStd (m + 3) ws ga]
    cases hk2 : ga.steps with
    | zero =>
      have hgf := callBody_zero cx m P fi xks ρ lb ws ga hk2
      simp only [calleeEnv] at hgf
      rcases hga : (callBody cx (m + 3) { pkg := P, file := fi, tenv := [] } (lowerSigOf xks ρ) (some lb) none ws false).run.run ga with ⟨r1, g1⟩
      rw [hga] at hgf; simp only at hgf; subst hgf; rfl
    | succ k2 =>
    have hia := inv3_frame hi hsfa hgfa
    have hlenV : vs.length = F.params.length := by rw [hlenP, krel_length hkr, List.length_map]
    obtain ⟨hic, hpc, hpsteps, -⟩ := callee_setup cx P K sg (fi := fi) hpk hobjs hsig hlb hkr hia k2 (by omega)
    have hbf := callFunc_fuel ds d F vs sa (params_typed htys) ρ hres hlenV hf
    dsimp only at hbd hmono
    have := (ihBF f F xks ρ lb hFl hsig hlb { sa with locals := specFrame F.params vs } _ _ ga.cells.size hic
      hpc (m + 2) (by rw [Nat.mul_succ] at hN; omega)).2 (by show sa.steps ≤ d; omega)
      (by show _ ≤ sa.steps; rw [hpsteps, Nat.mul_succ]; rw [hk2, Nat.mul_succ] at hbd; omega) hbf
    rcases hrb : (execStmts cx (m + 2) (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1 lb).run.run
      { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers } with ⟨rb, g1⟩
    rw [hrb] at this
    simp only at this
    subst this
    have hgf := callBody_fuel cx m P fi xks ρ lb ws ga k2 hk2 (argsRel_of_krel hkr) hnames' g1 hrb
    simp only [calleeEnv] at hgf
    rcases hga : (callBody cx (m + 3) { pkg := P, file := fi, tenv := [] } (lowerSigOf xks ρ) (some lb) none ws false).run.run ga with ⟨r1, g2⟩
    rw [hga] at hgf; simp only at hgf; subst hgf; rfl

end CallSF3

section Preds
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- The bound and the fuel claim of a galaspec run `sr` from `s` and a GoCore run `x` from `g`,
    of code of size `z` at galaspec depth `d`. -/
def SF (z d : Nat) (s : Spec.St) (g : St) (sr : Except Spec.Stop Spec.Val × Spec.St) (r : Except Stop α × St) :
    Prop :=
  (s.steps ≤ g.steps → Stut Q z s g sr r) ∧
    (s.steps ≤ d → Q * (g.steps + 1) + z ≤ s.steps → sr.1 = .error .fuel → r.1 = .error .fuel)

/-- A statement, at galaspec depth `d` (`StmtSim`'s). -/
def StmtSF (d : Nat) : Prop :=
  ∀ ρ κ st κ' l, lowerS3 K sg ρ κ st = some (κ', l) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d ≤ N →
      SF Q (esz st) d s g ((Spec.eval ds d st).run.run s) ((execStmt cx N env l).run.run g)

/-- A statement list (`ListSim3`'s). -/
def ListSF (d : Nat) : Prop :=
  ∀ ρ κ ss κ' ls, lowerSs3 K sg ρ κ ss = some (κ', ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d + 1 ≤ N →
      SF Q (eszs ss) d s g ((Spec.evalBlock ds d ss).run.run s) ((execStmts cx N env ls).run.run g)

/-- A block (`BlockSim3`'s). -/
def BlockSF (d : Nat) : Prop :=
  ∀ ρ κ ss κ' ls, lowerSs3 K sg ρ κ ss = some (κ', ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d + 2 ≤ N + K →
      SF Q (esz (.block ss)) d s g ((Spec.eval ds d (.block ss)).run.run s) ((execBlock cx N env ls).run.run g)

/-- An `if` statement (`IfSim3`'s). -/
def IfSF (d : Nat) : Prop :=
  ∀ ρ κ c a els κ' l, lowerS3 K sg ρ κ (.ifStmt c a els) = some (κ', l) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d ≤ N →
      SF Q (esz (.ifStmt c a els)) d s g ((Spec.eval ds d (.ifStmt c a els)).run.run s)
        ((execStmt cx N env l).run.run g)

/-- A tail (`TailSim`'s). -/
def TailSF (d : Nat) : Prop :=
  ∀ r κ e k ls, tailOf K sg r κ e = some (k, ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d + 3 ≤ N →
      SF Q (esz e) d s g ((Spec.eval ds d e).run.run s) ((execStmts cx N env ls).run.run g)

/-- A block's elements as a tail (`TsSim`'s). -/
def TsSF (d : Nat) : Prop :=
  ∀ r κ ss k ls, lowerTs K sg r κ ss = some (k, ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → ∀ N, K * d + 3 ≤ N →
      SF Q (eszs ss) d s g ((Spec.evalBlock ds d ss).run.run s) ((execStmts cx N env ls).run.run g)

/-- The `if` of a tail or an IIFE (`IfTSim`'s): galaspec's step for the `if` already taken,
    so the bound and the claim start from `kk`, the steps after it, and the size is the
    parts'. -/
def IfTSF (d : Nat) : Prop :=
  ∀ r κ c a b k st, ifOf (lowerC K sg κ c) (tailOf K sg r κ a) (tailOf K sg r κ b) = some (k, st) →
    ∀ e, (e = .ifExpr c a b ∨ ∃ as, a = .block as ∧ e = .ifStmt c a (some b)) →
    ∀ (s : Spec.St) (kk : Nat) (g : St) (env : Env), s.steps = kk + 1 →
      Inv3 cx P sg κ s.toCore g env → ∀ N, K * d + 6 ≤ N →
        SF Q (esz c + esz a + esz b) d { s with steps := kk } g ((Spec.eval ds (d + 1) e).run.run s)
          ((execStmt cx N env st).run.run g)

end Preds

section IifeSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem callBody0_zero (c : Ctx) (n : Nat) (env : Env) (k : Kind) (body : List Stmt) (g : St)
    (hj : g.steps = 0) :
    ((callBody c (n + 3) env (lowerSigOf [] k) (some body) none [] false).run.run g).1 = .error .fuel := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  exact grun_bind_fuel (gtick_zero g hj)

theorem callBody0_fuel (c : Ctx) (n : Nat) (env : Env) (k : Kind) (body : List Stmt) (g : St) (j : Nat)
    (hj : g.steps = j + 1) (ht : env.tenv = []) (hl : env.lookup (kindName k) = none)
    (ho : nameOwner c env (kindName k) = none) (g1 : St)
    (hb : (execStmts c (n + 2) env.push body).run.run { g with steps := j, defers := [] :: g.defers } =
      (.error .fuel, g1)) :
    ((callBody c (n + 3) env (lowerSigOf [] k) (some body) none [] false).run.run g).1 = .error .fuel := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  simp only [grun_bind, gtick_succ g j hj, Bool.not_false, ite_true, Bool.false_and,
    Bool.false_eq_true, ite_false, run_pureM]
  have hty : ∀ g', (evalType c (n + 2) env (.ident (kindName k))).run.run g' = (.ok (kindTy k), g') :=
    fun g' => evalType_kind c n env k g' hl ht ho
  simp only [List.flatMap_cons, List.isEmpty_nil, ite_true, List.append_nil, List.map_nil, List.flatMap_nil,
    List.length_nil, bne_self_eq_false, Bool.false_eq_true, ite_false,
    List.forIn_nil, run_pureM, grun_bind, hty, List.mapM_cons, List.mapM_nil, List.zip_cons_cons,
    List.zip_nil_right, List.forIn_cons, emptyStr_isEmpty, Bool.not_true, gtryCatch, gget_run, gmodify_run]
  rw [hb]
  rfl

section
variable (c : Ctx) (n : Nat) (env : Env) (k : Kind) (st : Stmt) (g : St)
  (hio : env.iota = none) (ht : env.tenv = [])
  (hl : env.lookup (kindName k) = none) (ho : nameOwner c env (kindName k) = none)
include hio ht hl ho

theorem evalExpr_iife_zero (hj : g.steps = 0) :
    ((evalExpr c (n + 6) env (iife k st)).run.run g).1 = .error .fuel := by
  rw [evalExpr_iife c n env k st g hio ht hl ho]
  exact grun_bind_fuel (callBody0_zero c n env k [st] _ hj)

theorem evalExpr_iife_fuel (j : Nat) (hj : g.steps = j + 1) (g1 : St)
    (hb : (execStmts c (n + 2) env.push [st]).run.run (iifeSt env k st g j) = (.error .fuel, g1)) :
    ((evalExpr c (n + 6) env (iife k st)).run.run g).1 = .error .fuel := by
  rw [evalExpr_iife c n env k st g hio ht hl ho]
  exact grun_bind_fuel (callBody0_fuel c n env k [st] _ j hj ht hl ho g1 hb)
end

theorem iife_sf (d : Nat) (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (ihI : IfTSim ds cx P K sg d)
    (ihIF : IfTSF ds cx P K sg Q d)
    (κ : String → Option Kind) (c a b : Spec.Expr) (k : Kind) (le : Expr)
    (hl : lowerC K sg κ (.ifExpr c a b) = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    SF Q (esz (.ifExpr c a b)) (d + 1) s g ((Spec.eval ds (d + 1) (.ifExpr c a b)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨st, hst, hκ, rfl⟩ := lowerC_ifExpr K sg hl
  simp only [esz]
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
  | succ kk =>
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 6 := ⟨N - 6, by rw [Nat.mul_succ] at hN; omega⟩
  obtain ⟨p, hpk, hobjs, -⟩ := hF.pkg
  have hio : env.iota = none := hi.inv.pre.1
  have hl' : env.lookup (kindName k) = none := (hi.closed _ hκ).2 (by cases k <;> rfl)
  have ho : nameOwner cx env (kindName k) = none :=
    nameOwner_none cx env _ p (by rw [hi.pkg]; exact hpk) (hobjs _ (by cases k <;> simp [kindName]))
      (by cases k <;> decide)
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · obtain ⟨j, hj⟩ : ∃ j, g.steps = j + 1 := ⟨g.steps - 1, by omega⟩
    have hiI : Inv3 cx P sg κ s.toCore (iifeSt env k st g j) env.push :=
      inv3_same cx P sg (inv3_push cx P sg hi) rfl rfl rfl
    have h := ihI none κ c a b k st hst _ (Or.inl rfl) s kk (iifeSt env k st g j) env.push hs hiI
      (by show kk ≤ j; omega) (n + 1) (by rw [Nat.mul_succ] at hN; omega)
    have hS := (ihIF none κ c a b k st hst _ (Or.inl rfl) s kk (iifeSt env k st g j) env.push hs hiI
      (n + 1) (by rw [Nat.mul_succ] at hN; omega)).1 (by show kk ≤ j; omega)
    rcases hr : (Spec.eval ds (d + 1) (.ifExpr c a b)).run.run s with ⟨re, s'⟩
    rw [hr] at h hS
    rcases hx : (execStmt cx (n + 1) env.push st).run.run (iifeSt env k st g j) with ⟨rx, g1⟩
    rw [hx] at h hS
    cases re with
    | ok v =>
      obtain ⟨⟨w, g1', hg, hvw, hkv, -, -, -, hgf, -⟩, -⟩ := h
      cases hg
      rw [evalExpr_iife_ret cx n env k st g hio hi.tenv hl' ho j hj v w hvw hkv g1
        (execStmts_one_err cx (n + 1) env.push st _ _ g1 hx) (gframe_defers hgf)]
      simp only [Stut, iifeSt] at hS ⊢
      rw [hj, Nat.mul_succ]
      omega
    | error e =>
      cases e with
      | ret v => exact (retOK_none h).elim
      | _ => trivial
  · rcases Nat.eq_zero_or_pos g.steps with hj | hj
    · exact evalExpr_iife_zero cx n env k st g hio hi.tenv hl' ho hj
    obtain ⟨j, hj⟩ : ∃ j, g.steps = j + 1 := ⟨g.steps - 1, by omega⟩
    have hiI : Inv3 cx P sg κ s.toCore (iifeSt env k st g j) env.push :=
      inv3_same cx P sg (inv3_push cx P sg hi) rfl rfl rfl
    have h := (ihIF none κ c a b k st hst _ (Or.inl rfl) s kk (iifeSt env k st g j) env.push hs hiI
      (n + 1) (by rw [Nat.mul_succ] at hN; omega)).2 (by show kk ≤ d; omega)
      (by show _ ≤ kk; simp only [iifeSt]; rw [hj] at hq; simp only [Nat.mul_succ] at hq ⊢; omega) hf
    rcases hx : (execStmt cx (n + 1) env.push st).run.run (iifeSt env k st g j) with ⟨rx, g1⟩
    rw [hx] at h
    simp only at h
    subst h
    exact evalExpr_iife_fuel cx n env k st g hio hi.tenv hl' ho j hj g1
      (execStmts_one_err cx (n + 1) env.push st _ _ g1 hx)

end IifeSF

section ExprAllSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

/-- **Expressions with calls, at depth `d + 1`**, from the parts at depth `d`. -/
theorem exprSF_succ (d : Nat) (hF : FunRel K sg ds cx P) (hK : 11 ≤ K) (hQ : KszOK ds Q)
    (ihE : ExprSim ds cx P K sg d) (ihB : BodySim ds cx P K sg d) (ihI : IfTSim ds cx P K sg d)
    (ih : ExprSF ds cx P K sg Q d) (ihBF : BodySF ds cx P K sg Q d) (ihIF : IfTSF ds cx P K sg Q d) :
    ExprSF ds cx P K sg Q (d + 1) := by
  intro κ e k le hl s g env hi N hN
  have hKN : K ≤ N := by rw [Nat.mul_succ] at hN; omega
  match e, hl with
  | .lit l, hl => exact leaf_sf ds cx P K sg Q κ _ k le (by simpa [lowerC] using hl) s g env hi _ N hKN
  | .name x, hl => exact leaf_sf ds cx P K sg Q κ _ k le (by simpa [lowerC] using hl) s g env hi _ N hKN
  | .paren x, hl =>
    cases hc : hasCall x
    · exact leaf_sf ds cx P K sg Q κ _ k le (by simpa [lowerC, hc] using hl) s g env hi _ N hKN
    · exact paren_sf ds cx P K sg Q d ih (by omega) κ x k le hl hc s g env hi N hN
  | .unary op x, hl =>
    cases hc : hasCall x
    · exact leaf_sf ds cx P K sg Q κ _ k le (by simpa [lowerC, hc] using hl) s g env hi _ N hKN
    · exact unary_sf ds cx P K sg Q d ihE ih (by omega) κ op x k le hl hc s g env hi N hN
  | .binary op l r, hl =>
    cases hc : (hasCall l || hasCall r)
    · have hl' : lowerLeaf K κ (.binary op l r) = some (k, le) := by
        simp only [lowerC] at hl; rw [hc] at hl; simpa using hl
      exact leaf_sf ds cx P K sg Q κ _ k le hl' s g env hi _ N hKN
    · by_cases hab : op = "&&" ∨ op = "||"
      · exact andor_sf ds cx P K sg Q d ihE ih (by omega) κ op hab l r k le hl hc s g env hi N hN
      · simp only [not_or] at hab
        exact binary_sf ds cx P K sg Q d ihE ih (by omega) κ op l r k le hl hc hab.1 hab.2 s g env hi N hN
  | .call (.name f) args, hl =>
    exact call_sf ds cx P K sg Q d hF (by omega) hQ ihE ihB ih ihBF κ f args k le hl s g env hi N hN
  | .ifExpr c a b, hl => exact iife_sf ds cx P K sg Q d hF hK ihI ihIF κ c a b k le hl s g env hi N hN
  | .call (.lit _) _, hl | .call (.paren _) _, hl | .call (.unary ..) _, hl | .call (.binary ..) _, hl
  | .call (.call ..) _, hl | .call (.select ..) _, hl | .call (.tuple _) _, hl | .call (.ifExpr ..) _, hl
  | .call (.mtch ..) _, hl | .call (.block _) _, hl | .call (.ifStmt ..) _, hl | .call (.ret _) _, hl
  | .call (.assign ..) _, hl | .call (.incdec ..) _, hl | .call (.decl ..) _, hl | .call (.interp _) _, hl
  | .call (.lambda ..) _, hl | .call (.named ..) _, hl | .call (.unsupported _) _, hl
  | .select .., hl | .tuple _, hl | .mtch .., hl | .block _, hl | .ifStmt .., hl
  | .ret _, hl | .assign .., hl | .incdec .., hl | .decl .., hl | .interp _, hl | .lambda .., hl
  | .named .., hl | .unsupported _, hl => simp [lowerC] at hl

end ExprAllSF

end Gala.M5
