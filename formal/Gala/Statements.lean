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
import Gala.Theorems
import Gala.Gala2Lean.Theorems
import Gala.Fuel
import Gala.Defaults
import Gala.Order
import Gala.Alpha
import Gala.AlphaStep
import Gala.MatchLower
import Gala.AlphaEval
import Gala.AlphaProg
import Gala.M5.Expr
import Gala.M5.StmtOrder

namespace Gala.Statements

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
    Acts (eval ds (d + 1) (.assign op [t] [x])) (compoundEff ds op t px) :=
  Gala.Spec.eval_compound_acts

open Gala.Spec in
theorem compound_eff : ∀ (ds : Decls) (op : String) (t : Expr) (px : Eff Val) (c : Core)
    (x₀ v w : Val),
    (readPlace ds t).run.run c = (.ok x₀, c) → px.res c = .ok v →
    (binop (compoundOp op) x₀ v).run.run (px.eff c) = (.ok w, px.eff c) →
    (compoundEff ds op t px).eff c = ((assignTo ds t w).run.run (px.eff c)).2 ∧
      (compoundEff ds op t px).res c =
        (((assignTo ds t w).run.run (px.eff c)).1.map fun _ => Val.unit) :=
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

open Gala.Spec in
theorem evalCases_match : ∀ (ds : Decls) (d : Nat) (v : Val) (p : Pat) (b : Expr)
    (rest : List (Pat × Option Expr × Expr)) (s : St) (bs : List (String × Val)) (pb : Eff Val),
    ((matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs) →
    Acts (eval ds d b) pb →
    pb.cost { s.toCore with locals := pushed bs s.locals } ≤ s.steps →
    let cb := { s.toCore with locals := pushed bs s.locals }
    let n := s.steps - pb.cost cb
    (evalCases ds d v ((p, none, b) :: rest)).run.run s =
      match pb.res cb with
      | .ok r => (.ok r, ⟨{ pb.eff cb with locals := exitScope (pb.eff cb).locals s.locals.length }, n⟩)
      | .error e => (.error e, ⟨pb.eff cb, n⟩) :=
  Gala.Spec.evalCases_match

open Gala.Spec in
theorem evalCases_nil : ∀ (ds : Decls) (d : Nat) (v : Val) (s : St),
    (evalCases ds d v []).run.run s = (.error (.panic .match_ "no match arm matched"), s) :=
  Gala.Spec.evalCases_nil

open Gala.Spec in
theorem eval_binary_acts : ∀ (ds : Decls) (d : Nat) (op : String) (l r : Expr) (pl pr : Eff Val),
    op ≠ "&&" → op ≠ "||" → Acts (eval ds d l) pl → Acts (eval ds d r) pr →
    Acts (eval ds (d + 1) (.binary op l r))
      (Eff.tick.bind fun _ => pl.bind fun a => pr.bind fun b => Eff.ofH (binop op a b)) :=
  Gala.Spec.eval_binary_acts

open Gala.Spec in
theorem eval_tuple_acts : ∀ (ds : Decls) (d : Nat) (es : List Expr) (ps : List (Eff Val)),
    es.length = ps.length → (∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) →
    Acts (eval ds (d + 1) (.tuple es))
      (Eff.tick.bind fun _ => (Eff.seq ps).bind fun vs => Eff.pure (.tuple vs)) :=
  Gala.Spec.eval_tuple_acts

open Gala.Spec in
theorem eval_interp_acts : ∀ (ds : Decls) (d : Nat) (es : List Expr) (ps : List (Eff Val)),
    es.length = ps.length → (∀ e p, (e, p) ∈ es.zip ps → Acts (eval ds d e) p) →
    Acts (eval ds (d + 1) (.interp es))
      (Eff.tick.bind fun _ => (Eff.seq ps).bind fun vs => (Eff.ofH (printable ds vs)).bind
        fun _ => (Eff.ofH (printed (Print.interp ds vs) ds)).bind fun t => Eff.pure (.str t)) :=
  Gala.Spec.eval_interp_acts

open Gala.Spec in
theorem bindDefaults_acts : ∀ (ds : Decls) (d : Nat) (names : List String)
    (tys : List (Option Ty)) (dflts : List (Option Expr)) (fs : Bool) (gvn : List (Nat × Val))
    (pd : Nat → Env → Eff Val),
    (∀ j, j < names.length → gvn.lookup j = none → ∃ e, dflts.getD j none = some e ∧
      (∀ frame, Acts (runIn ds d frame e) (pd j frame)) ∧
      (∀ frame (c : Core), (byNameOk ds frame (tys.getD j none) e).run.run c = (.ok true, c))) →
    Acts (bindDefaults ds d names tys dflts fs gvn)
      (Eff.foldlM (defaultStep names fs gvn pd) names.zipIdx []) :=
  Gala.Spec.bindDefaults_acts

open Gala.Spec in
theorem matchPats_append : ∀ (ds : Decls) (locals globals : Env) (ps₁ ps₂ : List Pat)
    (vs₁ vs₂ : List Val), ps₁.length = vs₁.length →
    matchPats ds locals globals (ps₁ ++ ps₂) (vs₁ ++ vs₂) =
      matchThen ds locals globals ps₁ ps₂ vs₁ vs₂ :=
  Gala.Spec.matchPats_append

open Gala.Spec in
theorem lookup_rel : ∀ {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    {x x' : String}, EnvR pkg pm Γ ρ ρ' → NameR Γ x x' →
    OptBindR pkg pm (lookup ρ x) (lookup ρ' x') :=
  Gala.Spec.lookup_rel

open Gala.Spec in
theorem resolve_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {l l' g g' : Env} {x x' : String}, DeclsR pkg pm ds ds' → EnvR pkg pm Γ l l' →
    GlobR pkg pm g g' → NameR Γ x x' →
    RefR pkg pm (resolve ds l g x) (resolve ds' l' g' x') :=
  Gala.Spec.resolve_rel

open Gala.Spec in
theorem matchPat_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {c c' : Core} {p p' : Pat} {ss : List Slot} {v v' : Val},
    DeclsR pkg pm ds ds' → CoreR pkg pm Γ c c' → PatR pkg Γ p p' ss → ValR pkg pm v v' →
    ResR pkg pm (MatchR pkg pm ss) ((matchPat ds c.locals c.globals p v).run.run c).1
      ((matchPat ds' c'.locals c'.globals p' v').run.run c').1 :=
  Gala.Spec.matchPat_rel

open Gala.Spec in
theorem argSlots_rel : ∀ {pkg : List String} {pm : String → Ren} {Γ : Ren}
    {names names' : List String} {args args' : List Expr}, names.length = names'.length →
    names.Nodup ∧ names'.Nodup → ArgsR pkg pm (pairsOf names names') Γ args args' →
    ResR pkg pm Eq (argSlots names args) (argSlots names' args') :=
  Gala.Spec.argSlots_rel

open Gala.Spec in
theorem argSlots_same_rel : ∀ {pkg : List String} {pm : String → Ren} {Γ : Ren}
    (names : List String) {args args' : List Expr}, ArgsR pkg pm [] Γ args args' →
    ResR pkg pm Eq (argSlots names args) (argSlots names args') :=
  Gala.Spec.argSlots_same_rel

open Gala.Spec in
theorem byNameOk_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {l l' : Env} {c c' : Core} {ty : Option Ty} {a a' : Expr},
    DeclsR pkg pm ds ds' → EnvR pkg pm Γ l l' → GlobR pkg pm c.globals c'.globals →
    HeapR pkg pm c.heap c'.heap → AlphaE pkg pm Γ a a' →
    ResR pkg pm Eq ((byNameOk ds l ty a).run.run c).1 ((byNameOk ds' l' ty a').run.run c').1 :=
  Gala.Spec.byNameOk_rel

open Gala.Spec in
theorem binop_rel : ∀ {pkg : List String} {pm : String → Ren} (op : String) {a a' b b' : Val}
    (c c' : Core), ValR pkg pm a a' → ValR pkg pm b b' →
    ResR pkg pm (ValR pkg pm) ((binop op a b).run.run c).1 ((binop op a' b').run.run c').1 :=
  Gala.Spec.binop_rel

open Gala.Spec in
theorem unop_rel : ∀ {pkg : List String} {pm : String → Ren} (op : String) {a a' : Val}
    (c c' : Core), ValR pkg pm a a' →
    ResR pkg pm (ValR pkg pm) ((unop op a).run.run c).1 ((unop op a').run.run c').1 :=
  Gala.Spec.unop_rel

open Gala.Spec in
theorem fmtVs_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls},
    SameLayout ds ds' → ∀ (m : Bool) {vs vs' : List Val}, ValsR pkg pm vs vs' →
    Print.fmtVs ds m vs = Print.fmtVs ds' m vs' :=
  Gala.Spec.fmtVs_rel

open Gala.Spec in
theorem getField_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {c c' : Core},
    SameLayout ds ds' → ∀ {v v' : Val} (f : String), ValR pkg pm v v' →
    ResR pkg pm (ValR pkg pm) ((getField ds v f).run.run c).1 ((getField ds' v' f).run.run c').1 :=
  Gala.Spec.getField_rel

open Gala.Spec in
theorem readPlace_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {c c' : Core}, SameLayout ds ds' → CoreR pkg pm Γ c c' → ∀ {t t' : Expr},
    AlphaE pkg pm Γ t t' →
    ResR pkg pm (ValR pkg pm) ((readPlace ds t).run.run c).1 ((readPlace ds' t').run.run c').1 :=
  Gala.Spec.readPlace_rel

open Gala.Spec in
theorem assignTo_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren},
    SameLayout ds ds' → ∀ {t t' : Expr} {v v' : Val} {c c' : Core}, CoreR pkg pm Γ c c' →
    AlphaE pkg pm Γ t t' → ValR pkg pm v v' →
    ResR pkg pm Eq ((assignTo ds t v).run.run c).1 ((assignTo ds' t' v').run.run c').1 ∧
      CoreR pkg pm Γ ((assignTo ds t v).run.run c).2 ((assignTo ds' t' v').run.run c').2 :=
  Gala.Spec.assignTo_rel

open Gala.Spec in
theorem pushAll_rel : ∀ {pkg : List String} {pm : String → Ren} (m : Bool)
    {bs bs' : List (String × Val)} {Γ : Ren} {c c' : Core},
    Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) bs bs' →
    CoreR pkg pm Γ c c' →
    ResR pkg pm Eq ((pushAll bs m).run.run c).1 ((pushAll bs' m).run.run c').1 ∧
      CoreR pkg pm (Γ.pushAll ((bs.map (·.1)).zip (bs'.map (·.1))))
        ((pushAll bs m).run.run c).2 ((pushAll bs' m).run.run c').2 :=
  Gala.Spec.pushAll_rel

open Gala.Spec in
theorem construct_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls},
    SameLayout ds ds' → ∀ (name : String) (fs : List Field) {vs vs' : List Val},
    ValsR pkg pm vs vs' → ∀ (c c' : Core),
    ResR pkg pm (ValsR pkg pm) ((construct ds name fs vs).run.run c).1
      ((construct ds' name fs vs').run.run c').1 :=
  Gala.Spec.construct_rel

open Gala.Spec in
theorem emit_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren},
    SameLayout ds ds' → ∀ (r : Print.P String) {c c' : Core}, CoreR pkg pm Γ c c' →
    ResR pkg pm Eq ((emit r ds).run.run c).1 ((emit r ds').run.run c').1 ∧
      CoreR pkg pm Γ ((emit r ds).run.run c).2 ((emit r ds').run.run c').2 :=
  Gala.Spec.emit_rel

open Gala.Spec in
theorem setField_rel : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {c c' : Core},
    SameLayout ds ds' → ∀ {v v' x x' : Val} (f : String), ValR pkg pm v v' → ValR pkg pm x x' →
    ResR pkg pm (ValR pkg pm) ((setField ds v f x).run.run c).1
      ((setField ds' v' f x').run.run c').1 :=
  Gala.Spec.setField_rel

open Gala.Spec in
theorem alloc_rel : ∀ {pkg : List String} {pm : String → Ren} {Γ : Ren} {c c' : Core}
    {v v' : Val}, CoreR pkg pm Γ c c' → ValR pkg pm v v' →
    ResR pkg pm (ValR pkg pm) ((alloc v).run.run c).1 ((alloc v').run.run c').1 ∧
      CoreR pkg pm Γ ((alloc v).run.run c).2 ((alloc v').run.run c').2 :=
  Gala.Spec.alloc_rel

open Gala Gala.M3 in
theorem valEq_enc : ∀ a b : Spec.Val, Spec.valEq a b = GoCore.geq (enc a) (enc b) :=
  Gala.M3.valEq_enc

open Gala Gala.M3 in
theorem lowerPat_correct : ∀ (ds : Spec.Decls) (inScope : String → Bool) (c : Spec.Core)
    (look : GoCore.GVar → Option GoCore.GVal), ReadsOk c look → ScopeOk inScope c →
    ∀ (t : STy) (p : Spec.Pat) (e : GoCore.GExpr) (v : Spec.Val) (r : Option (List (String × Spec.Val))),
      HasTy ds t v → peval look e = some (enc v) →
      ((Spec.matchPat ds c.locals c.globals p v).run.run c).1 = .ok r →
      PatOk look (lowerPat ds inScope t p e) r :=
  Gala.M3.lowerPat_correct

open Gala Gala.M3 in
theorem lowerMatch_correct : ∀ (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (subj : Spec.Expr) (subj' : GoCore.GExpr) (arms : List Arm),
    Part (ScopeOk inScope) (HasTy ds t) (Spec.eval ds d subj) (GoCore.geval X subj') →
    (∀ a ∈ arms, ArmOk ds d inScope t X a) →
    Part (ScopeOk inScope) (fun _ => True) (Spec.eval ds (d + 1) (.mtch subj (arms.map Arm.src)))
      (GoCore.geval X (lowerMatch ds inScope t subj' arms)) :=
  Gala.M3.lowerMatch_correct

open Gala Gala.M3 Gala.M3.Counterexample in
theorem leaky_not_correct :
    ¬ Part (ScopeOk inScope) (fun _ => True) (Spec.eval ds 10 (.mtch (.name "s") (arms.map Arm.src)))
      (GoCore.geval X (lowerMatchLeaky ds inScope (.sealed "Shape") (.var (.user "s")) arms)) :=
  Gala.M3.leaky_not_correct

open Gala Gala.M3 in
theorem part_lit_int : ∀ (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop) (n : Int), Spec.minInt ≤ n ∧ n ≤ Spec.maxInt →
    Part pre (fun v => v = .int n) (Spec.eval ds d (.lit (.int n))) (GoCore.geval X (.lit (.int n))) :=
  Gala.M3.part_lit_int

open Gala Gala.M3 in
theorem part_var : ∀ (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal) (x : String),
    Part (fun c => (Spec.lookupVar c.locals c.globals x).isSome) (fun _ => True)
      (Spec.eval ds d (.name x)) (GoCore.geval X (.var (.user x))) :=
  Gala.M3.part_var

open Gala Gala.M3 in
theorem part_add : ∀ (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop), (∀ c c', pre c → FrameS c c' → pre c') →
    ∀ (l r : Spec.Expr) (l' r' : GoCore.GExpr),
    Part pre IsInt (Spec.eval ds d l) (GoCore.geval X l') →
    Part pre IsInt (Spec.eval ds d r) (GoCore.geval X r') →
    Part pre IsInt (Spec.eval ds (d + 1) (.binary "+" l r)) (GoCore.geval X (.bin .add l' r')) :=
  Gala.M3.part_add

open Gala Gala.M3 in
theorem part_gt : ∀ (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop), (∀ c c', pre c → FrameS c c' → pre c') →
    ∀ (l r : Spec.Expr) (l' r' : GoCore.GExpr),
    Part pre IsInt (Spec.eval ds d l) (GoCore.geval X l') →
    Part pre IsInt (Spec.eval ds d r) (GoCore.geval X r') →
    Part pre (fun _ => True) (Spec.eval ds (d + 1) (.binary ">" l r)) (GoCore.geval X (.bin .gt l' r')) :=
  Gala.M3.part_gt

open Gala Gala.M3 in
theorem lowerMatch_instance : ∀ (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal),
    Part (ScopeOk fun _ => false) (fun _ => True)
      (Spec.eval ds (d + 1) (.mtch (.lit (.int 3)) (instanceArms.map Arm.src)))
      (GoCore.geval X (lowerMatch ds (fun _ => false) .other (.lit (.int 3)) instanceArms)) :=
  Gala.M3.lowerMatch_instance

open Gala.Spec in
theorem eval_alpha : ∀ {pkg : List String} {pm : String → Ren} {ds ds' : Decls},
    DeclsR pkg pm ds ds' → ∀ (d : Nat) {Γ : Ren} {e e' : Expr}, AlphaE pkg pm Γ e e' →
    RelM pkg pm Γ (· = Γ) (ValR pkg pm) (eval ds d e) (eval ds' d e') :=
  Gala.Spec.eval_alpha

open Gala.Spec in
theorem run_alpha : ∀ {pkg : List String} {pm : String → Ren} {p p' : Program},
    AlphaProgram pkg pm p p' → ∀ (n : Nat),
    OutcomeR (run p n).1 (run p' n).1 ∧ (run p n).2 = (run p' n).2 :=
  Gala.Spec.run_alpha

open Gala.Spec in
theorem alphaCheck_sound : ∀ {p p' : Program}, alphaCheck p p' = true →
    AlphaProgram (alphaPkg (collect p).1 (collect p).2) (alphaPm (collect p).1 (collect p').1) p p' :=
  Gala.Spec.alphaCheck_sound

open Gala.Spec in
theorem alphaCheck_run : ∀ {p p' : Program}, alphaCheck p p' = true → ∀ (n : Nat),
    OutcomeR (run p n).1 (run p' n).1 ∧ (run p n).2 = (run p' n).2 :=
  Gala.Spec.alphaCheck_run

open Gala.Spec Gala.M5 in
theorem eval_pure : ∀ (ds : Decls) (e : Expr) (d : Nat),
    Follows (fun c => Pure c.locals e) (eval ds d e) (pureEval ds e) :=
  Gala.M5.eval_pure

open Gala Gala.M5 in
theorem expr_correct : ∀ (ds : Spec.Decls) (cx : GoCore.Full.Ctx) (κ : String → Option Kind)
    (e : Spec.Expr) (k : Kind) (le : GoCore.Full.Expr), lowerE κ e = some (k, le) → NoConstOp e →
    ∀ (s : Spec.St) (g : GoCore.Full.St) (env : GoCore.Full.Env), LocRel κ s.toCore g.cells env →
      Predeclared cx env → ∀ (d : Nat),
      ((Spec.eval ds d e).run.run s).2.toCore = s.toCore ∧
      ∀ N ≥ gdepth e, ERel k g (isLitE e) ((Spec.eval ds d e).run.run s).1
        ((GoCore.Full.evalExpr cx N env le).run.run g) :=
  Gala.M5.expr_correct

open Gala Gala.M5 in
theorem expr_correct_instance : ∀ (ds : Spec.Decls) (steps d : Nat),
    ((Spec.eval ds d instExpr).run.run { instCore with steps }).2.toCore = instCore ∧
    ∀ N ≥ gdepth instExpr, ERel .int instSt (isLitE instExpr)
      ((Spec.eval ds d instExpr).run.run { instCore with steps }).1
      ((GoCore.Full.evalExpr instCtx N instEnv (.binary "+" (.ident "x") (.intLit 1))).run.run instSt) :=
  Gala.M5.expr_correct_instance

open Gala Gala.M5 GoCore.Full in
theorem inst_bitwise :
    (["&", "|", "^", "&^"].map fun op =>
      ((pureEval default (.binary op (.name "x") (.unary "-" (.lit (.int 3))))).run.run instCore).1 matches
        .ok (.int _)) = [true, true, true, true] ∧
    ((pureEval default (.binary "&" (.name "x") (.unary "-" (.lit (.int 3))))).run.run instCore).1 =
      .ok (.int 5) ∧
    ((pureEval default (.binary "^" (.name "x") (.unary "-" (.lit (.int 3))))).run.run instCore).1 =
      .ok (.int (-8)) ∧
    ((pureEval default (.binary "|" (.name "x") (.unary "-" (.lit (.int 3))))).run.run instCore).1 =
      .ok (.int (-3)) ∧
    ((pureEval default (.binary "&^" (.name "x") (.unary "-" (.lit (.int 3))))).run.run instCore).1 =
      .ok (.int 0) ∧
    (evalExpr instCtx 8 instEnv (.binary "&" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy 5), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "|" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy (-3)), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "^" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy (-8)), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "&^" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy 0), instSt) ∧
    (lowerE instKinds (.binary "&^" (.name "x") (.lit (.int (-3))))).isSome :=
  Gala.M5.inst_bitwise

open Gala Gala.M5 in
theorem stmts_correct : ∀ (ds : Spec.Decls) (cx : GoCore.Full.Ctx) (κ κ' : String → Option Kind)
    (ss : List Spec.Expr) (lss : List GoCore.Full.Stmt), lowerSs κ ss = some (κ', lss) →
    ∀ (s : Spec.St) (g : GoCore.Full.St) (env : GoCore.Full.Env), LocRel κ s.toCore g.cells env →
      s.out = g.out → Predeclared cx env → ∀ (d : Nat),
      ∀ N ≥ sdepthL ss, ResL cx κ' s.toCore g ((Spec.evalBlock ds d ss).run.run s)
        ((GoCore.Full.execStmts cx N env lss).run.run g) :=
  Gala.M5.stmts_correct

open Gala Gala.M5 in
theorem block_correct : ∀ (ds : Spec.Decls) (cx : GoCore.Full.Ctx) (κ κ' : String → Option Kind)
    (ss : List Spec.Expr) (lss : List GoCore.Full.Stmt), lowerSs κ ss = some (κ', lss) →
    ∀ (s : Spec.St) (g : GoCore.Full.St) (env : GoCore.Full.Env), LocRel κ s.toCore g.cells env →
      s.out = g.out → Predeclared cx env → ∀ (d : Nat),
      ∀ N ≥ sdepthL ss + 2, ResL cx κ s.toCore g ((Spec.eval ds d (.block ss)).run.run s)
        ((GoCore.Full.execStmt cx N env (.block lss)).run.run g) :=
  Gala.M5.block_correct

open Gala Gala.M5 in
theorem stmts_correct_instance : ∀ (ds : Spec.Decls) (steps d : Nat),
    ∀ N ≥ sdepthL instBody, ResL instCtx (upd instKinds "y" .int) instCore instSt
      ((Spec.evalBlock ds d instBody).run.run { instCore with steps })
      ((GoCore.Full.execStmts instCtx N instEnv instLowered).run.run instSt) :=
  Gala.M5.stmts_correct_instance

open Gala Gala.M5 in
theorem inst_gocore_ok : ∀ (N : Nat), N ≥ sdepthL instBody →
    ∃ v s', (Spec.evalBlock default 6 instBody).run.run { instCore with steps := 100 } = (.ok v, s') ∧
      ∃ env' g', (GoCore.Full.execStmts instCtx N instEnv instLowered).run.run instSt = (.ok env', g') ∧
        Inv instCtx (upd instKinds "y" .int) s'.toCore g' env' :=
  Gala.M5.inst_gocore_ok

open Gala Gala.M5 GoCore.Full Hoist in
theorem hSs_lowered : ∀ (κ κ' : String → Option Kind) (ss : List Spec.Expr) (lss : List Stmt),
    lowerSs κ ss = some (κ', lss) → ∀ (s : Scope) (h : HState), (hSs s lss).run h = (lss, h) :=
  Gala.M5.hSs_lowered

open Gala Gala.M5 GoCore.Full in
theorem gc_runsStep2 : RunsStep2AsWritten Order.gc :=
  Gala.M5.gc_runsStep2

open Gala Gala.M5 GoCore.Full in
theorem leftToRight_runsStep2 : RunsStep2AsWritten Order.leftToRight :=
  Gala.M5.leftToRight_runsStep2

end Gala.Statements
