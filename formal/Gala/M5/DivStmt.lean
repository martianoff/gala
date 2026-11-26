/-
  # M5, step 3d (c): divergence, statements, tails and bodies
-/
import Gala.M5.DivExpr
namespace Gala.M5
open Gala.GoCore.Full

section StmtsSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem decl_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d) (hK : 3 ≤ K)
    (ρ : Option Kind) (κ κ' : String → Option Kind)
    (m : Spec.Mut) (x : String) (e : Spec.Expr) (l : Stmt)
    (hl : lowerS3 K sg ρ κ (.decl m [x] false none [e]) = some (κ', l)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    SF Q (esz (.decl m [x] false none [e])) (d + 1) s g ((Spec.eval ds (d + 1) (.decl m [x] false none [e])).run.run s)
      ((execStmt cx N env l).run.run g) := by
  simp only [lowerS3] at hl
  split at hl
  · cases hl
  simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
  obtain ⟨⟨k, le⟩, hle, rfl, rfl⟩ := hl
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
  simp only [esz, eszs]
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
  | succ k' =>
  rw [eval_decl_run ds d k' m x e s hs]
  have hN' : K * d ≤ n + 1 := by rw [Nat.mul_succ] at hN; omega
  have hIE := ih κ e k le hle { s with steps := k' } g env hi (n + 1) hN'
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hE := ihE κ e k le hle { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 1) hN'
    have hS := hIE.1 (by show k' ≤ g.steps; omega)
    rcases hr : (Spec.eval ds d e).run.run { s with steps := k' } with ⟨r, t⟩
    rw [hr] at hE hS
    cases r with
    | error er =>
      cases er with
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
      obtain ⟨w, g', hg, hvw, -, -, -, -, -⟩ := hE
      rw [hg] at hS
      rw [execStmt_decl_ok cx n env x le g g' w hg (results_of_vrel hvw)]
      have : (declSt m x v t).steps = t.steps := by cases m <;> rfl
      simp only [Stut, declG] at hS ⊢
      rw [this]
      have : ({ s with steps := k' } : Spec.St).steps = k' := rfl
      omega
  · rcases hr : (Spec.eval ds d e).run.run { s with steps := k' } with ⟨r, t⟩
    rw [hr] at hf
    cases r with
    | ok => simp at hf
    | error er =>
    simp only at hf
    cases hf
    have h := hIE.2 (by show k' ≤ d; omega) (by show _ ≤ k'; omega) (by rw [hr])
    rcases hg : (evalExpr cx (n + 1) env le).run.run g with ⟨rg, g'⟩
    rw [hg] at h
    simp only at h
    subst h
    rw [execStmt_decl_err cx n env x le g g' _ hg]

theorem ret_sf (d : Nat) (ihE : ExprSim ds cx P K sg d) (ih : ExprSF ds cx P K sg Q d) (hK : 3 ≤ K)
    (ρ : Option Kind) (κ κ' : String → Option Kind)
    (x : Spec.Expr) (l : Stmt) (hl : lowerS3 K sg ρ κ (.ret (some x)) = some (κ', l)) (s : Spec.St) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (N : Nat) (hN : K * (d + 1) ≤ N) :
    SF Q (esz (.ret (some x))) (d + 1) s g ((Spec.eval ds (d + 1) (.ret (some x))).run.run s)
      ((execStmt cx N env l).run.run g) := by
  simp only [lowerS3] at hl
  split at hl
  · rename_i k le hle
    split at hl
    · rename_i hk
      subst hk
      simp only [Option.some.injEq, Prod.mk.injEq] at hl
      obtain ⟨rfl, rfl⟩ := hl
      obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
      simp only [esz, eszOpt]
      cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
      | succ k' =>
      rw [eval_ret_run ds d k' x s hs, execStmt_ret]
      have hN' : K * d ≤ n + 1 := by rw [Nat.mul_succ] at hN; omega
      have hIE := ih κ x k le hle { s with steps := k' } g env hi (n + 1) hN'
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · have hE := ihE κ x k le hle { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 1) hN'
        have hS := hIE.1 (by show k' ≤ g.steps; omega)
        rcases hr : (Spec.eval ds d x).run.run { s with steps := k' } with ⟨r, t⟩
        rw [hr] at hE hS
        cases r with
        | error er =>
          cases er with
          | ret => exact hE.elim
          | _ => trivial
        | ok v =>
          obtain ⟨w, g', hg, -⟩ := hE
          rw [hg] at hS ⊢
          simp only [Stut] at hS ⊢
          have : ({ s with steps := k' } : Spec.St).steps = k' := rfl
          omega
      · rcases hr : (Spec.eval ds d x).run.run { s with steps := k' } with ⟨r, t⟩
        rw [hr] at hf
        cases r with
        | ok => simp at hf
        | error er =>
        simp only at hf
        cases hf
        have h := hIE.2 (by show k' ≤ d; omega) (by show _ ≤ k'; omega) (by rw [hr])
        rcases hg : (evalExpr cx (n + 1) env le).run.run g with ⟨rg, g'⟩
        rw [hg] at h
        simp only at h
        subst h
        rfl
    · cases hl
  · cases hl

end StmtsSF

section StmtsSF2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem withEnv_snd (env : Env) (r : Except Stop α × St) : (withEnv env r).2 = r.2 := by
  rcases r with ⟨_ | _, _⟩ <;> rfl

theorem withEnv_fuel (env : Env) {r : Except Stop α × St} (h : r.1 = .error .fuel) :
    (withEnv env r).1 = .error .fuel := by
  rcases r with ⟨_ | _, _⟩ <;> simp_all [withEnv]

/-- The bound across a finished first part. -/
theorem stut_trans {z1 z2 : Nat} {s t : Spec.St} {g g' : St}
    (h1 : s.steps + Q * g'.steps ≤ t.steps + Q * g.steps + z1) {sr : Except Spec.Stop β × Spec.St}
    {r : Except Stop α × St} (h2 : Stut Q z2 t g' sr r) : Stut Q (z1 + z2) s g sr r := by
  rcases sr with ⟨e | v, s'⟩ <;> rcases r with ⟨x, g''⟩
  · cases e <;> first | trivial | (simp only [Stut] at h2 ⊢; omega)
  · simp only [Stut] at h2 ⊢; omega

/-- A statement galaspec ends normally, run by GoCore with any budget. -/
theorem stmt_down {d : Nat} (hS : StmtSim ds cx P K sg d) (hF : StmtSF ds cx P K sg Q d)
    {ρ : Option Kind} {κ κ' : String → Option Kind} {st : Spec.Expr} {l : Stmt}
    (hl : lowerS3 K sg ρ κ st = some (κ', l))
    {s : Spec.St} {g : St} {env : Env} (hi : Inv3 cx P sg κ s.toCore g env) {N : Nat} (hN : K * d ≤ N)
    {v : Spec.Val} {t : Spec.St} (hr : (Spec.eval ds d st).run.run s = (.ok v, t)) :
    ((execStmt cx N env l).run.run g).1 = .error .fuel ∨ ∃ env' g', (execStmt cx N env l).run.run g = (.ok env', g') ∧
      Inv3 cx P sg κ' t.toCore g' env' ∧ Pre s.heap t.heap ∧ t.globals = s.globals ∧
      (∃ pre, t.locals = pre ++ s.locals) ∧ GFrame g g' ∧
      s.steps + Q * g'.steps ≤ t.steps + Q * g.steps + esz st ∧ t.steps ≤ s.steps := by
  have hi' : Inv3 cx P sg κ s.toCore (shift s.steps g) env := (inv3_shift _).2 hi
  have hsg : s.steps ≤ (shift s.steps g).steps := by simp
  have h1 := hS ρ κ st κ' l hl s _ env hi' hsg N hN
  have h2 := (hF ρ κ st κ' l hl s _ env hi' N hN).1 hsg
  rw [hr] at h1 h2
  obtain ⟨env', g1, hg1, hinv, hh, hgl, hpre, hgf, hsp⟩ := h1
  have hmono : t.steps ≤ s.steps := by have := hgf.2.2.2.2.1; unfold Spent at hsp; omega
  rw [hg1] at h2
  rcases down ((simAll cx s.steps N N (Nat.le_refl _)).execStmt env l) g hg1 with hf | ⟨g0, hg0, rfl⟩
  · exact .inl hf
  · refine .inr ⟨env', g0, hg0, (inv3_shift _).1 hinv, hh, hgl, hpre, (gframe_shift _).1 hgf, ?_, hmono⟩
    simp only [Stut] at h2
    rw [Nat.mul_add, Nat.mul_add] at h2
    omega

theorem ifSF_zero : IfSF ds cx P K sg Q 0 := by
  intro ρ κ c a els κ' l _ s g env _ N _
  rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.ifStmt c a els); omega⟩

theorem blockSF_zero : BlockSF ds cx P K sg Q 0 := by
  intro ρ κ ss κ' ls _ s g env _ N _
  rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos (.block ss); omega⟩

theorem stmtSF_zero : StmtSF ds cx P K sg Q 0 := by
  intro ρ κ st κ' l _ s g env _ N _
  rw [eval_zero]; exact ⟨fun _ => trivial, fun _ h _ => by have := esz_pos st; omega⟩

/-- **A block**, from the statement lists one level down. -/
theorem blockSF_succ (d : Nat) (ihL : ListSF ds cx P K sg Q d) : BlockSF ds cx P K sg Q (d + 1) := by
  intro ρ κ ss κ' ls hl s g env hi N hN
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
  simp only [esz]
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
  | succ k =>
  rw [eval_block_run, tick_succ s k hs]
  simp only
  rw [execBlock_run]
  have h := ihL ρ κ ss κ' ls hl { s with steps := k } g env.push (inv3_push cx P sg hi) n
    (by rw [Nat.mul_succ] at hN; omega)
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hS := h.1 (by show k ≤ g.steps; omega)
    rcases hr : (Spec.evalBlock ds d ss).run.run { s with steps := k } with ⟨r, t⟩
    rw [hr] at hS
    rcases hx : (execStmts cx n env.push ls).run.run g with ⟨rx, g'⟩
    rw [hx] at hS
    cases r with
    | ok v =>
      cases rx <;> (simp only [Stut] at hS ⊢; have : ({ s with steps := k } : Spec.St).steps = k := rfl; omega)
    | error e =>
      cases e <;> cases rx <;> first | trivial | (simp only [Stut] at hS ⊢; have : ({ s with steps := k } : Spec.St).steps = k := rfl; omega)
  · rcases hr : (Spec.evalBlock ds d ss).run.run { s with steps := k } with ⟨r, t⟩
    rw [hr] at hf
    cases r with
    | ok => simp at hf
    | error e =>
    simp only at hf
    cases hf
    have := h.2 (by show k ≤ d; omega) (by show _ ≤ k; omega) (by rw [hr])
    rcases hx : (execStmts cx n env.push ls).run.run g with ⟨rx, g'⟩
    rw [hx] at this
    simp only at this
    subst this
    rfl

end StmtsSF2

section StmtsSF3
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem stut_unit {z : Nat} {t : Spec.St} {g : St} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop α × St} (h : Stut Q z t g sr r) :
    Stut Q z t g (match sr with
      | (.ok _, t') => ((.ok .unit : Except Spec.Stop Spec.Val), t')
      | (.error e, t') => (.error e, t')) r := by
  rcases sr with ⟨e | v, s'⟩ <;> exact h

theorem fuel_unit {sr : Except Spec.Stop Spec.Val × Spec.St}
    (h : (match sr with
      | (.ok _, t') => ((.ok .unit : Except Spec.Stop Spec.Val), t')
      | (.error e, t') => (.error e, t')).1 = .error .fuel) : sr.1 = .error .fuel := by
  rcases sr with ⟨e | v, s'⟩ <;> simp_all

/-- **An `if`**, its condition and branches one level down. -/
theorem ifSF_succ (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (ihB : BlockSF ds cx P K sg Q d) (ihI : IfSF ds cx P K sg Q d) (hK : 3 ≤ K) :
    IfSF ds cx P K sg Q (d + 1) := by
  intro ρ κ c a els κ' l hl s g env hi N hN
  obtain ⟨rfl, as, lc, κ₁, la, le, rfl, hlc, hla, hle, rfl⟩ := lowerS3_if K sg hl
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 2 := ⟨N - 2, by rw [Nat.mul_succ] at hN; omega⟩
  have hsz : esz (.ifStmt c (.block as) els) = esz c + esz (.block as) + eszOpt els + 1 := rfl
  rw [hsz]
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; exact ⟨fun _ => trivial, fun _ h _ => by omega⟩
  | succ k =>
  have hip := inv3_push cx P sg hi
  have hN' : K * d ≤ n + 1 := by rw [Nat.mul_succ] at hN; omega
  have hNb : K * d + 2 ≤ n + 1 + K := by rw [Nat.mul_succ] at hN; omega
  have hIC := ihEF κ' c .bool lc hlc { s with steps := k } g env.push hip (n + 1) hN'
  have hk : ({ s with steps := k } : Spec.St).steps = k := rfl
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hE := ihE κ' c .bool lc hlc { s with steps := k } g env.push hip (by show k ≤ g.steps; omega) (n + 1) hN'
    have hSC := hIC.1 (by show k ≤ g.steps; omega)
    rcases hr : (Spec.eval ds d c).run.run { s with steps := k } with ⟨r, t⟩
    rw [hr] at hE hSC
    cases r with
    | error er =>
      rw [eval_ifStmt_err ds d k c _ els s t er hs hr]
      cases er with
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
    obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
    obtain ⟨b, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    rw [hg] at hSC
    simp only [Stut] at hSC
    have hb1 : s.steps + Q * g'.steps ≤ t.steps + Q * g.steps + (esz c + 1) := by omega
    have hit : Inv3 cx P sg κ' t.toCore g' env.push := inv3_frame hip hsf hgf
    have hstg : t.steps ≤ g'.steps := spent_le hsp (by show k ≤ g.steps; omega)
    cases els with
    | none =>
      simp only [lowerElse3, Option.some.injEq] at hle
      subst hle
      rw [eval_ifStmt_none ds d k c (.block as) b s t hs hr]
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la none g g' ty hg]
        simp only [ite_true]
        have h := (ihB ρ κ' as κ₁ la hla t g' env.push hit (n + 1) hNb).1 hstg
        exact stut_mono Q (by simp [eszOpt]; omega) (stut_trans Q hb1 (stut_unit Q (stut_congr Q (withEnv_snd _ _) h)))
      | false =>
        rw [execStmt_if_false cx n env lc la none g g' ty hg]
        simp only [Bool.false_eq_true, ite_false, Stut]
        omega
    | some e =>
      rw [eval_ifStmt_some ds d k c (.block as) e b s t hs hr]
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la _ g g' ty hg]
        simp only [ite_true]
        have h := (ihB ρ κ' as κ₁ la hla t g' env.push hit (n + 1) hNb).1 hstg
        exact stut_mono Q (by simp [eszOpt]; omega) (stut_trans Q hb1 (stut_congr Q (withEnv_snd _ _) h))
      | false =>
        rw [execStmt_if_false cx n env lc la _ g g' ty hg]
        simp only [Bool.false_eq_true, ite_false]
        match e, hle with
        | .block bs, hle =>
          simp only [lowerElse3, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, lb⟩, hlb, rfl⟩ := hle
          simp only
          rw [execStmt_block, withEnv_withEnv]
          have h := (ihB ρ κ' bs κ₂ lb hlb t g' env.push hit n (by rw [Nat.mul_succ] at hN; omega)).1 hstg
          exact stut_mono Q (by simp [eszOpt]; omega) (stut_trans Q hb1 (stut_congr Q (withEnv_snd _ _) h))
        | .ifStmt c₂ a₂ els₂, hle =>
          simp only [lowerElse3, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, l₂⟩, hl₂, rfl⟩ := hle
          simp only
          have h := (ihI ρ κ' c₂ a₂ els₂ κ₂ l₂ hl₂ t g' env.push hit (n + 1) hN').1 hstg
          exact stut_mono Q (by simp [eszOpt]; omega) (stut_trans Q hb1 (stut_congr Q (withEnv_snd _ _) h))
        | .lit _, hle | .name _, hle | .unary .., hle | .binary .., hle | .call .., hle
        | .select .., hle | .paren _, hle | .tuple _, hle | .ifExpr .., hle | .mtch .., hle
        | .ret _, hle | .assign .., hle | .incdec .., hle | .decl .., hle | .interp _, hle
        | .lambda .., hle | .named .., hle | .unsupported _, hle => simp [lowerElse3] at hle
  · rcases hr : (Spec.eval ds d c).run.run { s with steps := k } with ⟨r, t⟩
    cases r with
    | error er =>
      rw [eval_ifStmt_err ds d k c _ els s t er hs hr] at hf
      simp only at hf
      cases hf
      have h := hIC.2 (by show k ≤ d; omega) (by show _ ≤ k; omega) (by rw [hr])
      rcases hg : (evalExpr cx (n + 1) env.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at h
      simp only at h
      subst h
      rw [execStmt_if_err cx n env lc la le g g' _ hg]
    | ok v =>
    rcases expr_down ds cx P K sg Q ihE ihEF (s := { s with steps := k }) hlc hip hN' hr with hgf | ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hbd, hmono⟩
    · rcases hg : (evalExpr cx (n + 1) env.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at hgf
      simp only at hgf
      subst hgf
      rw [execStmt_if_err cx n env lc la le g g' _ hg]
    obtain ⟨b, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    have hit : Inv3 cx P sg κ' t.toCore g' env.push := inv3_frame hip hsf hgf
    rw [hk] at hbd hmono
    rw [Nat.mul_succ] at hq
    cases els with
    | none =>
      simp only [lowerElse3, Option.some.injEq] at hle
      subst hle
      rw [eval_ifStmt_none ds d k c (.block as) b s t hs hr] at hf
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la none g g' ty hg]
        simp only [ite_true] at hf ⊢
        exact withEnv_fuel env ((ihB ρ κ' as κ₁ la hla t g' env.push hit (n + 1) hNb).2 (by omega)
          (by rw [Nat.mul_succ]; omega) (fuel_unit hf))
      | false => simp at hf
    | some e =>
      rw [eval_ifStmt_some ds d k c (.block as) e b s t hs hr] at hf
      cases b with
      | true =>
        rw [execStmt_if_true cx n env lc la _ g g' ty hg]
        simp only [ite_true] at hf ⊢
        exact withEnv_fuel env ((ihB ρ κ' as κ₁ la hla t g' env.push hit (n + 1) hNb).2 (by omega)
          (by rw [Nat.mul_succ]; omega) hf)
      | false =>
        rw [execStmt_if_false cx n env lc la _ g g' ty hg]
        simp only [Bool.false_eq_true, ite_false] at hf ⊢
        match e, hle with
        | .block bs, hle =>
          simp only [lowerElse3, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, lb⟩, hlb, rfl⟩ := hle
          simp only
          rw [execStmt_block, withEnv_withEnv]
          exact withEnv_fuel env ((ihB ρ κ' bs κ₂ lb hlb t g' env.push hit n
            (by rw [Nat.mul_succ] at hN; omega)).2 (by omega) (by simp only [eszOpt] at hq; rw [Nat.mul_succ]; omega) hf)
        | .ifStmt c₂ a₂ els₂, hle =>
          simp only [lowerElse3, Option.map_eq_some_iff] at hle
          obtain ⟨⟨κ₂, l₂⟩, hl₂, rfl⟩ := hle
          simp only
          exact withEnv_fuel env ((ihI ρ κ' c₂ a₂ els₂ κ₂ l₂ hl₂ t g' env.push hit (n + 1) hN').2 (by omega)
            (by simp only [eszOpt] at hq; rw [Nat.mul_succ]; omega) hf)
        | .lit _, hle | .name _, hle | .unary .., hle | .binary .., hle | .call .., hle
        | .select .., hle | .paren _, hle | .tuple _, hle | .ifExpr .., hle | .mtch .., hle
        | .ret _, hle | .assign .., hle | .incdec .., hle | .decl .., hle | .interp _, hle
        | .lambda .., hle | .named .., hle | .unsupported _, hle => simp [lowerElse3] at hle

end StmtsSF3

section StmtsSF4
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem listSF_of (d : Nat) (ihS : StmtSim ds cx P K sg d) (ihSF : StmtSF ds cx P K sg Q d) :
    ListSF ds cx P K sg Q d := by
  intro ρ κ ss
  induction ss generalizing κ with
  | nil =>
    intro κ' ls hl s g env hi N hN
    simp only [lowerSs3, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
    rw [evalBlock_nil, execStmts_nil]
    exact ⟨fun _ => by simp only [Stut, eszs]; omega, fun _ _ h => by simp at h⟩
  | cons st rest ihl =>
    intro κ' ls hl s g env hi N hN
    simp only [lowerSs3, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, l⟩, hst, ⟨κ₂, ls'⟩, hrest, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
    have hIS := ihSF ρ κ st κ₁ l hst s g env hi n (by omega)
    rw [execStmts_cons]
    cases rest with
    | nil =>
      simp only [lowerSs3, Option.some.injEq, Prod.mk.injEq] at hrest
      obtain ⟨rfl, rfl⟩ := hrest
      rw [evalBlock_one]
      have hsz : eszs [st] = esz st := by simp [eszs]
      rw [hsz]
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · refine stut_congr Q ?_ (hIS.1 hsg)
        rcases (execStmt cx n env l).run.run g with ⟨_ | _, _⟩
        · rfl
        · simp only; rw [execStmts_nil]
      · have h := hIS.2 hd hq hf
        rcases hx : (execStmt cx n env l).run.run g with ⟨rx, g'⟩
        rw [hx] at h
        simp only at h
        subst h
        rfl
    | cons st' rest' =>
      rw [evalBlock_cons]
      simp only [eszs]
      refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
      · have hs := ihS ρ κ st κ₁ l hst s g env hi hsg n (by omega)
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
        have h := (ihl κ₁ κ₂ ls' hrest t g' env' hinv (n + 1) hN).1 (spent_le hsp hsg)
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
        exact (ihl κ₁ κ₂ ls' hrest t g' env' hinv (n + 1) hN).2 (by omega)
          (by simp only [eszs] at hq ⊢; rw [Nat.mul_succ] at hq ⊢; omega) hf

/-- **A statement** at depth `d + 1`. -/
theorem stmtSF_succ (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (ihB : BlockSF ds cx P K sg Q (d + 1)) (ihI : IfSF ds cx P K sg Q (d + 1)) (hK : 3 ≤ K) :
    StmtSF ds cx P K sg Q (d + 1) := by
  intro ρ κ st κ' l hl s g env hi N hN
  match st, hl with
  | .decl m [x] false none [e], hl => exact decl_sf ds cx P K sg Q d ihE ihEF hK ρ κ κ' m x e l hl s g env hi N hN
  | .ret (some x), hl => exact ret_sf ds cx P K sg Q d ihE ihEF hK ρ κ κ' x l hl s g env hi N hN
  | .block ss, hl =>
    simp only [lowerS3, Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, lb⟩, hlb, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
    rw [execStmt_block]
    have h := ihB ρ κ ss κ₁ lb hlb s g env hi n (by rw [Nat.mul_succ] at hN ⊢; omega)
    exact ⟨fun hsg => stut_congr Q (withEnv_snd _ _) (h.1 hsg), fun hd hq hf => withEnv_fuel env (h.2 hd hq hf)⟩
  | .ifStmt c a els, hl =>
    have hκ : κ' = κ := (lowerS3_if K sg hl).1
    subst hκ
    exact ihI ρ κ' c a els _ l hl s g env hi N hN
  | .decl _ [] _ _ _, hl | .decl _ (_ :: _ :: _) _ _ _, hl | .decl _ [_] true _ _, hl
  | .decl _ [_] false (some _) _, hl | .decl _ [_] false none [], hl
  | .decl _ [_] false none (_ :: _ :: _), hl | .ret none, hl
  | .lit _, hl | .name _, hl | .unary .., hl | .binary .., hl | .call .., hl | .select .., hl
  | .paren _, hl | .tuple _, hl | .ifExpr .., hl | .mtch .., hl | .assign .., hl
  | .incdec .., hl | .interp _, hl | .lambda .., hl | .named .., hl | .unsupported _, hl =>
    simp [lowerS3] at hl

end StmtsSF4

section TailsSF
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs) (Q : Nat)

theorem ifTSF_of (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihEF : ExprSF ds cx P K sg Q d)
    (ihT : TailSF ds cx P K sg Q d) : IfTSF ds cx P K sg Q d := by
  intro r κ c a b k st hif e he s kk g env hs hi N hN
  obtain ⟨lc, ta, tb, hlc, hta, htb, rfl⟩ := ifOf_some hif
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by omega⟩
  have hip := inv3_push cx P sg hi
  have hIC := ihEF κ c .bool lc hlc { s with steps := kk } g env.push hip (n + 3) (by omega)
  have hk : ({ s with steps := kk } : Spec.St).steps = kk := rfl
  refine ⟨fun hsg => ?_, fun hd hq hf => ?_⟩
  · have hE := ihE κ c .bool lc hlc { s with steps := kk } g env.push hip hsg (n + 3) (by omega)
    have hSC := hIC.1 hsg
    rcases hr : (Spec.eval ds d c).run.run { s with steps := kk } with ⟨rc, t⟩
    rw [hr] at hE hSC
    cases rc with
    | error er =>
      rw [eval_ifE_err ds d kk c a b e he s t er hs hr]
      cases er with
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
    obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
    obtain ⟨bv, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    rw [hg] at hSC
    simp only [Stut] at hSC
    rw [eval_ifE_ok ds d kk c a b e he bv s t hs hr]
    have hit : Inv3 cx P sg κ t.toCore g' env.push.push := inv3_push cx P sg (inv3_frame hip hsf hgf)
    have hstg : t.steps ≤ g'.steps := spent_le hsp hsg
    cases bv with
    | true =>
      rw [ifT, execStmt_if_true cx (n + 2) env lc ta _ g g' ty hg, execBlock_run]
      have h := (ihT r κ a k ta hta t g' env.push.push hit (n + 2) (by omega)).1 hstg
      refine stut_mono Q (by omega) (stut_trans Q hSC (stut_congr Q ?_ h))
      rcases (execStmts cx (n + 2) env.push.push ta).run.run g' with ⟨_ | _, _⟩ <;> rfl
    | false =>
      rw [ifT, execStmt_if_false cx (n + 2) env lc ta _ g g' ty hg]
      simp only
      rw [execStmt_block, execBlock_run]
      have h := (ihT r κ b k tb htb t g' env.push.push hit (n + 1) (by omega)).1 hstg
      refine stut_mono Q (by omega) (stut_trans Q hSC (stut_congr Q ?_ h))
      rcases (execStmts cx (n + 1) env.push.push tb).run.run g' with ⟨_ | _, _⟩ <;> rfl
  · rcases hr : (Spec.eval ds d c).run.run { s with steps := kk } with ⟨rc, t⟩
    cases rc with
    | error er =>
      rw [eval_ifE_err ds d kk c a b e he s t er hs hr] at hf
      simp only at hf
      cases hf
      have h := hIC.2 hd (by omega) (by rw [hr])
      rcases hg : (evalExpr cx (n + 3) env.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at h
      simp only at h
      subst h
      rw [ifT, execStmt_if_err cx (n + 2) env lc ta _ g g' _ hg]
    | ok v =>
    rcases expr_down ds cx P K sg Q ihE ihEF (s := { s with steps := kk }) hlc hip (by omega : K * d ≤ n + 3) hr with
      hgf | ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hbd, hmono⟩
    · rcases hg : (evalExpr cx (n + 3) env.push lc).run.run g with ⟨rg, g'⟩
      rw [hg] at hgf
      simp only at hgf
      subst hgf
      rw [ifT, execStmt_if_err cx (n + 2) env lc ta _ g g' _ hg]
    obtain ⟨bv, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
    rw [eval_ifE_ok ds d kk c a b e he bv s t hs hr] at hf
    have hit : Inv3 cx P sg κ t.toCore g' env.push.push := inv3_push cx P sg (inv3_frame hip hsf hgf)
    rw [hk] at hbd hmono hd hq
    rw [Nat.mul_succ] at hq
    cases bv with
    | true =>
      rw [ifT, execStmt_if_true cx (n + 2) env lc ta _ g g' ty hg, execBlock_run]
      have h := (ihT r κ a k ta hta t g' env.push.push hit (n + 2) (by omega)).2 (by omega)
        (by rw [Nat.mul_succ]; omega) hf
      revert h
      rcases (execStmts cx (n + 2) env.push.push ta).run.run g' with ⟨_ | _, _⟩ <;> simp [withEnv]
    | false =>
      rw [ifT, execStmt_if_false cx (n + 2) env lc ta _ g g' ty hg]
      simp only
      rw [execStmt_block, execBlock_run]
      have h := (ihT r κ b k tb htb t g' env.push.push hit (n + 1) (by omega)).2 (by omega)
        (by rw [Nat.mul_succ]; omega) hf
      revert h
      rcases (execStmts cx (n + 1) env.push.push tb).run.run g' with ⟨_ | _, _⟩ <;> simp [withEnv]

end TailsSF

end Gala.M5
