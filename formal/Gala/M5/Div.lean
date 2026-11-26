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
  have hsg : s.steps ≤ (shift s.steps g).steps := by simp [shift]
  have h1 := hE κ e k le hl s _ env hi' hsg N hN
  have h2 := (hS κ e k le hl s _ env hi' N hN).1 hsg
  rw [hr] at h1 h2
  obtain ⟨w, g1, hg1, hvw, hkv, hu, hsf, hgf, hsp⟩ := h1
  have hmono : s'.steps ≤ s.steps := by have := hgf.2.2.2.2.1; unfold Spent at hsp; omega
  rw [hg1] at h2
  rcases down ((simAll cx s.steps N N (Nat.le_refl _)).evalExpr env le) g hg1 with hf | ⟨g0, hg0, rfl⟩
  · exact .inl hf
  · refine .inr ⟨w, g0, hg0, hvw, hkv, hu, hsf, (gframe_shift _).1 hgf, ?_, hmono⟩
    simp only [Stut, shift] at h2
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
      simp only [Prod.mk.injEq, Except.ok.injEq] at hb0
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

end Gala.M5
