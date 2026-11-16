/-
  # The statements of the required theorems

  Each theorem that COVERAGE.md calls proved (`requiredTheorems` in
  `scripts/ProofCheck.lean`, `REQUIRED_THEOREMS` in `BUILD.bazel`) is restated here in
  full, as a theorem of its own whose proof is the original. Changing a theorem's
  statement then fails the build until this file changes too, so the change shows in
  review. The proof check covers these theorems like any other (complete proofs, standard
  axioms only), and `tools/check_repo_policy.sh` checks that every required theorem is
  restated here.

  What this pins is the text of each statement. A definition a statement mentions (such
  as `cellsMutable`) is not pinned: changing it changes what the theorem means.
-/
import Gala.MatchLowering
import Gala.Theorems
import Gala.Gala2Lean.Theorems
import Gala.Fuel
import Gala.Defaults
import Gala.Order

namespace Gala.Statements

open Gala.MatchLowering in
theorem compile_correct : ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compile e) = encR (seval ρ e) :=
  Gala.MatchLowering.compile_correct

open Gala.MatchLowering in
theorem program_correct : ∀ (e : SExpr), teval [] (compile e) = encR (seval [] e) :=
  Gala.MatchLowering.program_correct

open Gala.MatchLowering in
theorem leaky_not_correct : ¬ ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compileLeaky e) = encR (seval ρ e) :=
  Gala.MatchLowering.leaky_not_correct

open Gala.Spec in
theorem capturedEnv_captured : ∀ (ρ : Env), cellsMutable ρ → capturedEnv (captured ρ) = ρ :=
  Gala.Spec.capturedEnv_captured

open Gala.Spec in
theorem defaultFrame_other : ∀ (earlier : List (String × Val)) (x : String),
    x ∉ earlier.map (·.1) → lookup (defaultFrame earlier) x = none :=
  Gala.Spec.defaultFrame_other

open Gala.Spec in
theorem defaultFrame_last : ∀ (earlier : List (String × Val)) (x : String) (v : Val),
    x ≠ "_" → lookup (defaultFrame (earlier ++ [(x, v)])) x = some ⟨v, false⟩ :=
  Gala.Spec.defaultFrame_last

open Gala.Gala2Lean.Examples.Match Gala.Gen in
theorem eval_total : ∀ (e : Expr) (s : _gala_St),
    (eval e).run.run s = some (.ok (denote e), s) :=
  Gala.Gala2Lean.Examples.Match.eval_total

open Gala.Spec in
theorem run_fuel_adequate : ∀ (p : Program) (n m : Nat), n ≤ m → (run p n).1 ≠ .fuel →
    run p m = run p n :=
  Gala.Spec.run_fuel_adequate

open Gala.Spec in
theorem defaultFrame_lookup : ∀ (earlier : List (String × Val)) (x : String),
    lookup (defaultFrame earlier) x =
      if x = "_" then none else (earlier.reverse.lookup x).map (⟨·, false⟩) :=
  Gala.Spec.defaultFrame_lookup

open Gala.Spec in
theorem bindDefaults_callerLocals : ∀ (ds : Decls) (d : Nat) (names : List String)
    (tys : List (Option Ty)) (dflts : List (Option Expr)) (fieldScope : Bool)
    (given : List (Nat × Val)) (s : St) (l : Env),
    let a := (bindDefaults ds d names tys dflts fieldScope given).run.run s
    let b := (bindDefaults ds d names tys dflts fieldScope given).run.run { s with locals := l }
    a.1 = b.1 ∧ { a.2 with locals := [] } = { b.2 with locals := [] } ∧
      ∀ v, a.1 = .ok v → a.2.locals = s.locals ∧ b.2.locals = l :=
  Gala.Spec.bindDefaults_callerLocals

open Gala.Spec in
theorem evalList_acts : ∀ (ds : Decls) (d : Nat) (es : List Expr) (ps : List (Eff Val)),
    es.length = ps.length → (∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) →
    Acts (evalList ds d es) (Eff.seq ps) :=
  Gala.Spec.evalList_acts

open Gala.Spec in
theorem eval_and_acts : ∀ (ds : Decls) (d : Nat) (l r : Expr) (pl pr : Eff Val),
    Acts (eval ds d l) pl → Acts (eval ds d r) pr →
    Acts (eval ds (d + 1) (.binary "&&" l r))
      (Eff.tick.bind fun _ => pl.bind fun a => (Eff.ofH (asBool a)).bind fun b =>
        if b then pr.bind fun c => (Eff.ofH (asBool c)).bind fun b' => Eff.pure (.bool b')
        else Eff.pure (.bool false)) :=
  Gala.Spec.eval_and_acts

open Gala.Spec in
theorem eval_or_acts : ∀ (ds : Decls) (d : Nat) (l r : Expr) (pl pr : Eff Val),
    Acts (eval ds d l) pl → Acts (eval ds d r) pr →
    Acts (eval ds (d + 1) (.binary "||" l r))
      (Eff.tick.bind fun _ => pl.bind fun a => (Eff.ofH (asBool a)).bind fun b =>
        if b then Eff.pure (.bool true)
        else pr.bind fun c => (Eff.ofH (asBool c)).bind fun b' => Eff.pure (.bool b')) :=
  Gala.Spec.eval_or_acts

open Gala.Spec in
theorem argSlots_eq : ∀ (names : List String) (args : List Expr) (slots : List Nat),
    argSlots names args = .ok slots → slots = args.zipIdx.map (slotOf names) :=
  Gala.Spec.argSlots_eq

open Gala.Spec in
theorem bindArgs_acts : ∀ (ds : Decls) (d : Nat) (names : List String) (tys : List (Option Ty))
    (dflts : List (Option Expr)) (fs : Bool) (args : List Expr) (slots : List Nat)
    (ps : List (Eff Val)) (pd : Nat → Env → Eff Val),
    argSlots names args = .ok slots →
    (∀ a j, (a, j) ∈ args.zip slots → ∀ (locals : Env) (c : Core),
      (byNameOk ds locals (tys.getD j none) (unnamed a)).run.run c = (.ok true, c)) →
    args.length = ps.length →
    (∀ a p, (a, p) ∈ args.zip ps → Acts (eval ds d (unnamed a)) p) →
    (∀ j, j < names.length → j ∉ slots → ∃ e, dflts.getD j none = some e ∧
      (∀ frame, Acts (runIn ds d frame e) (pd j frame)) ∧
      (∀ frame (c : Core), (byNameOk ds frame (tys.getD j none) e).run.run c = (.ok true, c))) →
    Acts (bindArgs ds d names tys dflts fs args)
      ((Eff.seq ps).bind fun vs =>
        Eff.foldlM (defaultStep names fs (slots.zip vs) pd) names.zipIdx []) :=
  Gala.Spec.bindArgs_acts

open Gala.Spec in
theorem eval_compound_acts : ∀ (ds : Decls) (d : Nat) (op : String) (t x : Expr) (px : Eff Val),
    op ≠ "=" → Acts (eval ds d x) px →
    Acts (eval ds (d + 1) (.assign op [t] [x]))
      (Eff.tick.bind fun _ => (Eff.ofH (readPlace ds t)).bind fun cur => px.bind fun v =>
        (Eff.ofH (binop (compoundOp op) cur v)).bind fun w =>
          (Eff.ofH (assignTo ds t w)).bind fun _ => Eff.pure .unit) :=
  Gala.Spec.eval_compound_acts

open Gala.Spec in
theorem compound_eff : ∀ (ds : Decls) (op : String) (t : Expr) (px : Eff Val) (c : Core)
    (x₀ v w : Val),
    (readPlace ds t).run.run c = (.ok x₀, c) → px.res c = .ok v →
    (binop (compoundOp op) x₀ v).run.run (px.eff c) = (.ok w, px.eff c) →
    let E := Eff.tick.bind fun _ => (Eff.ofH (readPlace ds t)).bind fun cur => px.bind fun v =>
      (Eff.ofH (binop (compoundOp op) cur v)).bind fun w =>
        (Eff.ofH (assignTo ds t w)).bind fun _ => Eff.pure Val.unit
    E.eff c = ((assignTo ds t w).run.run (px.eff c)).2 ∧
      E.res c = (((assignTo ds t w).run.run (px.eff c)).1.map fun _ => Val.unit) :=
  Gala.Spec.compound_eff

open Gala.Spec in
theorem matchPats_stops : ∀ (ds : Decls) (locals globals : Env) (ps₁ ps₂ : List Pat)
    (vs₁ vs₂ : List Val), ps₁.length = vs₁.length → ∀ (c c' : Core),
    (matchPats ds locals globals ps₁ vs₁).run.run c = (.ok none, c') →
    (matchPats ds locals globals (ps₁ ++ ps₂) (vs₁ ++ vs₂)).run.run c = (.ok none, c') :=
  Gala.Spec.matchPats_stops

open Gala.Spec in
theorem evalCases_nomatch : ∀ (ds : Decls) (d : Nat) (v : Val) (p : Pat) (g : Option Expr)
    (b : Expr) (rest : List (Pat × Option Expr × Expr)) (s : St),
    ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok none →
    (evalCases ds d v ((p, g, b) :: rest)).run.run s = (evalCases ds d v rest).run.run s :=
  Gala.Spec.evalCases_nomatch

open Gala.Spec in
theorem evalCases_guard_false : ∀ (ds : Decls) (d : Nat) (v : Val) (p : Pat) (ge b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val)) (pg : Eff Val),
    ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs) →
    Acts (eval ds d ge) pg →
    pg.res { s.toCore with locals := pushed bs s.locals } = .ok (.bool false) →
    (pg.eff { s.toCore with locals := pushed bs s.locals }).locals = pushed bs s.locals →
    pg.cost { s.toCore with locals := pushed bs s.locals } ≤ s.steps →
    (evalCases ds d v ((p, some ge, b) :: rest)).run.run s =
      (evalCases ds d v rest).run.run
        ⟨{ pg.eff { s.toCore with locals := pushed bs s.locals } with locals := s.locals },
         s.steps - pg.cost { s.toCore with locals := pushed bs s.locals }⟩ :=
  Gala.Spec.evalCases_guard_false

open Gala.Spec in
theorem evalCases_guard_true : ∀ (ds : Decls) (d : Nat) (v : Val) (p : Pat) (ge b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val))
    (pg pb : Eff Val),
    ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs) →
    Acts (eval ds d ge) pg → Acts (eval ds d b) pb →
    pg.res { s.toCore with locals := pushed bs s.locals } = .ok (.bool true) →
    pg.cost { s.toCore with locals := pushed bs s.locals } +
      pb.cost (pg.eff { s.toCore with locals := pushed bs s.locals }) ≤ s.steps →
    let cg := pg.eff { s.toCore with locals := pushed bs s.locals }
    let n := s.steps - pg.cost { s.toCore with locals := pushed bs s.locals } - pb.cost cg
    (evalCases ds d v ((p, some ge, b) :: rest)).run.run s =
      match pb.res cg with
      | .ok r => (.ok r, ⟨{ pb.eff cg with locals := exitScope (pb.eff cg).locals s.locals.length }, n⟩)
      | .error e => (.error e, ⟨pb.eff cg, n⟩) :=
  Gala.Spec.evalCases_guard_true

end Gala.Statements
