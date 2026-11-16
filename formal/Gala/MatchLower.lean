/-
  # M3: the `match` lowering, against galaspec

  docs/FORMAL_SEMANTICS.MD, "M3: the match lowering against galaspec". A lowering of
  galaspec's `match` (`Gala/Eval.lean`) into GoCore (`Gala/GoCore.lean`), in the shape the
  transformer emits, and a proof that it is correct with respect to galaspec itself.

  **The lowering** (`lowerMatch`): the subject once into a temporary; then each arm, in a
  block of its own: its pattern variables' expressions into temporaries, its test, and the
  pattern variables bound to their names only inside the branches that read them (the
  guard's and the body's), so the later arms, in the `else`, never see them. A guard runs in
  a block of its own into a flag. No arm left: `panic`. All of it in a function literal
  called at once, whose `return` is the match's value. Patterns are lowered statically
  (`lowerPat`), from the subject's static type (`STy`) and the names in scope.

  **What is proved.**
  * `lowerMatch_correct`: given that the subject, guards and bodies are lowered correctly
    (`Part`, forward simulation from galaspec's values and panics), the lowered `match`
    is correct in the same sense. Every S1 pattern form: `_`, a binder (`_` included),
    int, bool and string literals, stable identifiers (local or package-level), a bare
    case without fields, a sealed case's extractor with any number of fields, a struct's
    extractor, tuples, nested to any depth; guards; arms in order; the subject evaluated
    once; the pattern variables scoped to their arm; no arm matching is a panic of class
    `match`.
  * `lowerPat_correct`: the pattern lowering agrees with galaspec's `matchPat`, the core of
    the above; `valEq_enc`: galaspec's `==` is GoCore's on the encoded values.
  * `part_lit_int`, `part_var`, `part_add`, `part_gt`: `Part` holds of literals, variables
    and int `+` and `>`, so the hypotheses can be met.
  * `leaky_not_correct`: the lowering #996 reported, which binds the pattern variables
    before the arm's `if`, does not simulate galaspec: on a program with a failing guard,
    galaspec gives 10 and it gives 6 (the scoped lowering gives 10).

  **The stated gap.** `HasTy`: the subject has the static type the lowering was given.
  galaspec decides what a bare pattern name means at run time, from the value's sealed
  type; the transformer decides it statically, from the type it inferred. They agree
  exactly when the value has that type. Discharging it needs a typing judgment (T7).

  **What is trusted**: that this lowering describes what the transformer emits. It does up
  to these deliberate differences, each a normalization lemma M6 has to prove
  (docs/FORMAL_SEMANTICS.MD, M6): the transformer names intermediate `Unapply` and
  `IsDefined` results in temporaries (here written inline); it evaluates a nested
  extractor's statements under the conditions before it (here eagerly, which is sound only
  because generated extractors are pure and total; a user-defined `Unapply` is outside
  S1); it binds only the pattern variables a branch reads (here all of them); when a guard
  reads no pattern variable it emits `if cond && guard` (here always the guard block); and
  in a function's tail position it returns directly (here always a function literal).

  **Not covered**: runs galaspec ends out of fuel, `stuck` or `unsupported` (claims are about
  values and panics), and a `return` inside an arm (the transformer lowers such a match
  inline; M5).
-/
import Gala.AlphaStep
import Gala.GoCore

namespace Gala.M3

open Gala

/-! ## Values -/

mutual
/-- How a galaspec value is represented in GoCore. A function value is `opaque`. -/
def enc : Spec.Val → GoCore.GVal
  | .int n => .int n
  | .bool b => .bool b
  | .str s => .str s
  | .unit => .unit
  | .tuple vs => .tuple (encs vs)
  | .struct_ t fs => .struct_ t (encs fs)
  | .sealed t c fs => .sealed t c (encs fs)
  | .closure .. | .fn _ | .ref _ => .opaque
/-- `enc` on each element. -/
def encs : List Spec.Val → List GoCore.GVal
  | [] => []
  | v :: vs => enc v :: encs vs
end

mutual
/-- **Equality agrees.** galaspec's `==` (`valEq`) is GoCore's (`geq`, Go's `==` and the
    generated `Equal`) on the encoded values. -/
theorem valEq_enc : ∀ a b : Spec.Val, Spec.valEq a b = GoCore.geq (enc a) (enc b)
  | a, b => by
    cases a <;> cases b <;> simp only [Spec.valEq, enc, GoCore.geq]
    all_goals first
      | rfl
      | (rename_i as bs; exact valEqs_enc as bs)
      | (rename_i as _ bs; rw [valEqs_enc as bs])
      | (rename_i as _ _ bs; rw [valEqs_enc as bs])
theorem valEqs_enc : ∀ as bs : List Spec.Val,
    Spec.valEq.valEqs as bs = GoCore.geq.geqs (encs as) (encs bs)
  | [], [] => rfl
  | a :: as, b :: bs => by
    simp only [Spec.valEq.valEqs, encs, GoCore.geq.geqs, valEq_enc a b, valEqs_enc as bs]
  | [], _ :: _ | _ :: _, [] => rfl
end

/-! ## Static shapes

The transformer decides what a bare name in a pattern means from the static type of the
value it is matched against; galaspec decides it at run time, from the value. The lowering
is given the subject's static type, as much of it as name classification needs. -/

/-- The part of a static type the lowering uses. -/
inductive STy
  | sealed (ty : String)
  | struct_ (s : String)
  | tuple (ts : List STy)
  /-- Any other type: `int`, `bool`, `string`, a function type. -/
  | other

/-- The static shape of a declared field or parameter type. -/
def tyOf (ds : Spec.Decls) : Option Spec.Ty → STy
  | some (.name n) =>
    if (ds.sealeds.lookup n).isSome then .sealed n
    else if (ds.structs.lookup n).isSome then .struct_ n
    else .other
  | _ => .other

/-- The case `c` of sealed type `ty`, as galaspec finds it. -/
def caseIn (ds : Spec.Decls) (ty c : String) : Option Spec.Variant :=
  ((ds.sealeds.lookup ty).getD []).find? (·.name == c)

/-- The case a bare name `x` tests, at static shape `t`: a case of `t`'s sealed type. -/
def staticCase (ds : Spec.Decls) (t : STy) (x : String) : Option Spec.Variant :=
  match t with
  | .sealed ty => caseIn ds ty x
  | _ => none

/-- The cases galaspec tests a bare name against: those of the value's sealed type. -/
def casesOf (ds : Spec.Decls) : Spec.Val → List Spec.Variant
  | .sealed ty _ _ => (ds.sealeds.lookup ty).getD []
  | _ => []

/-- The fields of struct `s`. -/
def structFields (ds : Spec.Decls) (s : String) : List Spec.Field :=
  (ds.structs.lookup s).getD []

mutual
/-- **A value has a static type**, deeply: a sealed value is of its type with an existing
    case, and each field has its declared type; likewise for structs and tuples. A value
    of shape `other` is not a sealed value, a struct or a tuple. -/
inductive HasTy (ds : Spec.Decls) : STy → Spec.Val → Prop
  | sealed (ty c : String) (cv : Spec.Variant) (fs : List Spec.Val) :
      caseIn ds ty c = some cv → HasTys ds (cv.fields.map (tyOf ds ·.type)) fs →
      HasTy ds (.sealed ty) (.sealed ty c fs)
  | struct_ (s : String) (fs : List Spec.Val) :
      (ds.structs.lookup s).isSome →
      HasTys ds ((structFields ds s).map (tyOf ds ·.type)) fs →
      HasTy ds (.struct_ s) (.struct_ s fs)
  | tuple (ts : List STy) (vs : List Spec.Val) : HasTys ds ts vs → HasTy ds (.tuple ts) (.tuple vs)
  | other (v : Spec.Val) : (∀ ty c fs, v ≠ .sealed ty c fs) → (∀ s fs, v ≠ .struct_ s fs) →
      (∀ vs, v ≠ .tuple vs) → (∀ n, v ≠ .ref n) → HasTy ds .other v
/-- Each value has its type, the lists of the same length. -/
inductive HasTys (ds : Spec.Decls) : List STy → List Spec.Val → Prop
  | nil : HasTys ds [] []
  | cons : HasTy ds t v → HasTys ds ts vs → HasTys ds (t :: ts) (v :: vs)
end

/-- A value of a static type is not a heap reference (which no expression evaluates to). -/
theorem HasTy.notRef {ds : Spec.Decls} {t : STy} {v : Spec.Val} (h : HasTy ds t v) (n : Nat) :
    v ≠ .ref n := by
  cases h with
  | other _ _ _ _ hr => exact hr n
  | _ => nofun

/-! ## The lowering -/

open GoCore in
/-- A literal pattern's constant. -/
def encLit : Spec.Lit → GVal
  | .int n => .int n
  | .bool b => .bool b
  | .str s => .str s

/-- A lowered pattern: its condition, and each pattern variable with the expression that
    reads its value, in the order galaspec binds them. -/
abbrev LPat := GoCore.GExpr × List (String × GoCore.GExpr)

open GoCore in
mutual
/-- Lower pattern `p`, at static shape `t`, matched against the value of `e`. Every
    expression it builds is pure. A pattern galaspec would stop on as `stuck` or
    `unsupported` lowers to `false`. -/
def lowerPat (ds : Spec.Decls) (inScope : String → Bool) : STy → Spec.Pat → GExpr → LPat
  | _, .wild, _ => (.lit (.bool true), [])
  | _, .unsupported _, _ => (.lit (.bool false), [])
  | _, .lit l, e => (.bin .eq e (.lit (encLit l)), [])
  | t, .name x, e =>
    if (staticCase ds t x).isSome then (.prim (.unapply x 0) e, [])
    else if Spec.capitalized x && inScope x then (.bin .eq e (.var (.user x)), [])
    else (.lit (.bool true), [(x, e)])
  | .tuple ts, .tuple ps, e => lowerPats ds inScope ts ps (fun i => .prim (.field i) e) 0
  | .sealed ty, .extract c ps, e =>
    match caseIn ds ty c with
    | none => (.lit (.bool false), [])
    | some cv =>
      let u := GExpr.prim (.unapply c ps.length) e
      let ts := cv.fields.map (tyOf ds ·.type)
      match ps.length with
      | 0 => (u, [])
      | 1 =>
        let r := lowerPats ds inScope ts ps (fun _ => .prim .get u) 0
        (.and (.prim .isDefined u) r.1, r.2)
      | _ =>
        let r := lowerPats ds inScope ts ps (fun i => .prim (.field i) (.prim .get u)) 0
        (.and (.prim .isDefined u) r.1, r.2)
  | .struct_ s, .extract c ps, e =>
    if c == s then
      lowerPats ds inScope ((structFields ds s).map (tyOf ds ·.type)) ps
        (fun i => .prim (.field i) e) 0
    else (.lit (.bool false), [])
  | _, _, _ => (.lit (.bool false), [])
/-- Lower sub-patterns left to right, the `i`-th against `proj i`; their conditions are
    joined by `&&`, so a later one is not tested once one fails. -/
def lowerPats (ds : Spec.Decls) (inScope : String → Bool) :
    List STy → List Spec.Pat → (Nat → GExpr) → Nat → LPat
  | t :: ts, p :: ps, proj, i =>
    let r := lowerPat ds inScope t p (proj i)
    let rs := lowerPats ds inScope ts ps proj (i + 1)
    (.and r.1 rs.1, r.2 ++ rs.2)
  | _, _, _, _ => (.lit (.bool true), [])
end

/-! ## Pure evaluation

Every expression a lowered pattern is made of is pure: it reads variables and changes
nothing. `peval` evaluates such an expression given how variables read; `none` is a stop
(an undeclared variable, an operand of the wrong type). -/

open GoCore in
/-- Evaluate a pure expression. A function literal or a hole is not pure: `none`. -/
def peval (look : GVar → Option GVal) : GExpr → Option GVal
  | .lit v => some v
  | .var x => look x
  | .bin op a b => do
    let va ← peval look a
    let vb ← peval look b
    match binVal op va vb with
    | .ok w => some w
    | .error _ => none
  | .and a b => do
    match ← peval look a with
    | .bool true =>
      match ← peval look b with
      | .bool c => some (.bool c)
      | _ => none
    | .bool false => some (.bool false)
    | _ => none
  | .prim p e => (peval look e).map (primVal p)
  | .funcLit _ | .ext _ => none

/-! ### Running GoCore code -/

section GRun
open GoCore

theorem grun_bind (x : GM α) (f : α → GM β) (s : GSt) :
    (x >>= f).run.run s = match x.run.run s with
      | (.ok a, t) => (f a).run.run t
      | (.error e, t) => (.error e, t) := by
  simp only [ExceptT.run_bind, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  cases r <;> rfl

theorem grun_pure (a : α) (s : GSt) : (pure a : GM α).run.run s = (.ok a, s) := rfl
theorem grun_get (s : GSt) : (get : GM GSt).run.run s = (.ok s, s) := rfl
theorem grun_set (t s : GSt) : (set t : GM PUnit).run.run s = (.ok ⟨⟩, t) := rfl

theorem grun_ofExcept (r : Except GStop α) (s : GSt) :
    (MonadExcept.ofExcept r : GM α).run.run s = (r, s) := by
  cases r <;> rfl

theorem grun_asBool (v : GVal) (s : GSt) :
    (asBool v).run.run s = (match v with
      | .bool b => .ok b
      | _ => .error (.stuck "not a bool"), s) := by
  cases v <;> rfl

end GRun

open GoCore in
/-- A pure expression that `peval` evaluates runs in GoCore to that value, changing
    nothing. -/
theorem geval_peval (X : Nat → GM GVal) (g : GSt) :
    ∀ (e : GExpr) (w : GVal), peval (lookupVar g) e = some w → (geval X e).run.run g = (.ok w, g)
  | .lit v, w, h => by simp only [peval, Option.some.injEq] at h; subst h; rfl
  | .var x, w, h => by
    simp only [peval] at h
    simp only [geval, grun_bind, grun_get, h, grun_pure]
  | .bin op a b, w, h => by
    simp only [peval, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨va, ha, vb, hb, hw⟩ := h
    simp only [geval, grun_bind, geval_peval X g a va ha, geval_peval X g b vb hb, grun_ofExcept]
    split at hw <;> simp_all
  | .and a b, w, h => by
    simp only [peval, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨va, ha, hw⟩ := h
    simp only [geval, grun_bind, geval_peval X g a va ha, grun_asBool]
    split at hw
    · simp only [Option.bind_eq_some_iff] at hw
      obtain ⟨vb, hb, hw⟩ := hw
      simp only [ite_true, grun_bind, geval_peval X g b vb hb, grun_asBool]
      split at hw <;> simp_all <;> rfl
    · simp_all; rfl
    · simp at hw
  | .prim p e, w, h => by
    simp only [peval, Option.map_eq_some_iff] at h
    obtain ⟨v, hv, rfl⟩ := h
    simp only [geval, grun_bind, geval_peval X g e v hv, grun_pure]
  | .funcLit _, _, h | .ext _, _, h => by simp [peval] at h

/-! ## The pattern lowering is correct -/

theorem fall2_append {R : α → β → Prop} :
    ∀ {as as' : List α} {bs bs' : List β},
      Spec.Fall2 R as bs → Spec.Fall2 R as' bs' → Spec.Fall2 R (as ++ as') (bs ++ bs')
  | _, _, _, _, .nil, h => h
  | _, _, _, _, .cons hab h, h' => .cons hab (fall2_append h h')

mutual
/-- galaspec's `==` is symmetric. -/
theorem valEq_comm : ∀ a b : Spec.Val, Spec.valEq a b = Spec.valEq b a
  | a, b => by
    cases a <;> cases b <;> simp only [Spec.valEq]
    all_goals first
      | rfl
      | exact BEq.comm
      | (rename_i as bs; exact valEqs_comm as bs)
      | (rename_i t as u bs; rw [valEqs_comm as bs, BEq.comm (a := t)])
      | (rename_i t c as u d bs; rw [valEqs_comm as bs, BEq.comm (a := t), BEq.comm (a := c)])
theorem valEqs_comm : ∀ as bs : List Spec.Val, Spec.valEq.valEqs as bs = Spec.valEq.valEqs bs as
  | [], [] => rfl
  | a :: as, b :: bs => by
    simp only [Spec.valEq.valEqs, valEq_comm a b, valEqs_comm as bs]
  | [], _ :: _ | _ :: _, [] => rfl
end

theorem encs_getElem? (vs : List Spec.Val) (i : Nat) (h : i < vs.length) :
    (encs vs)[i]? = some (enc vs[i]) := by
  induction vs generalizing i with
  | nil => simp at h
  | cons v vs ih =>
    cases i with
    | zero => rfl
    | succ i => exact ih i (by simpa using h)

open Spec in
/-- A read-only `H` step followed by another: the second runs from the same state. -/
theorem run_bind_ro {x : H α} {f : α → H β} (hx : ReadOnly x) (c : Core) :
    ((x >>= f).run.run c).1 = match (x.run.run c).1 with
      | .ok a => ((f a).run.run c).1
      | .error e => .error e := by
  rw [run_bind_eq]
  have h := hx c
  rcases hr : x.run.run c with ⟨r, t⟩
  rw [hr] at h; simp only at h; subst h
  cases r <;> rfl

open Spec in
theorem matchPats_cons_run (ds : Decls) (L G : Env) (p : Pat) (ps : List Pat) (v : Val)
    (vs : List Val) (c : Core) :
    ((matchPats ds L G (p :: ps) (v :: vs)).run.run c).1 =
      match ((matchPat ds L G p v).run.run c).1 with
      | .error e => .error e
      | .ok none => .ok none
      | .ok (some b) => match ((matchPats ds L G ps vs).run.run c).1 with
        | .error e => .error e
        | .ok none => .ok none
        | .ok (some bs) => .ok (some (b ++ bs)) := by
  rw [matchPats.eq_def]
  simp only
  rw [run_bind_ro (matchPat_readOnly ds L G p v)]
  rcases ((matchPat ds L G p v).run.run c).1 with e | _ | b
  · rfl
  · rfl
  · simp only
    rw [run_bind_ro (matchPats_readOnly ds L G ps vs)]
    rcases ((matchPats ds L G ps vs).run.run c).1 with e | _ | bs <;> rfl

/-- What a lowered pattern does, given what galaspec's match gave: its condition is
    `true` exactly when galaspec matched, and then each pattern variable's expression
    gives the encoding of the value galaspec bound to it, in galaspec's order. -/
def PatOk (look : GoCore.GVar → Option GoCore.GVal) (lp : LPat)
    (r : Option (List (String × Spec.Val))) : Prop :=
  peval look lp.1 = some (.bool r.isSome) ∧
  ∀ bs, r = some bs → Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
    xe.1 = xv.1 ∧ peval look xe.2 = some (enc xv.2) ∧ ∀ n, xv.2 ≠ .ref n) lp.2 bs

/-- The static scope agrees with galaspec's: a name is in scope exactly when it is bound. -/
def ScopeOk (inScope : String → Bool) (c : Spec.Core) : Prop :=
  ∀ x, inScope x = (Spec.lookupVar c.locals c.globals x).isSome

/-- GoCore reads a name in scope as the encoding of the value galaspec's binding holds. -/
def ReadsOk (c : Spec.Core) (look : GoCore.GVar → Option GoCore.GVal) : Prop :=
  ∀ x b, Spec.lookupVar c.locals c.globals x = some b →
    ∀ v, ((Spec.deref b.val).run.run c).1 = .ok v → look (.user x) = some (enc v)

theorem patOk_false (look : GoCore.GVar → Option GoCore.GVal) (lp : LPat)
    (h : peval look lp.1 = some (.bool false)) : PatOk look lp none :=
  ⟨h, fun _ h => by simp at h⟩

/-- **Static and dynamic name classification agree** on a value of the static type: the
    case set galaspec reads from the value is the static type's. -/
theorem classify_static {ds : Spec.Decls} {t : STy} {v : Spec.Val} (ht : HasTy ds t v)
    (L G : Spec.Env) (x : String) :
    Spec.classifyPatName (casesOf ds v) L G x =
      match staticCase ds t x with
      | some cv => .variant cv
      | none => match Spec.capitalized x, Spec.lookupVar L G x with
        | true, some b => .stable b
        | _, _ => .binder := by
  have : (casesOf ds v).find? (·.name == x) = staticCase ds t x := by
    cases ht with
    | other v hs _ _ _ => cases v <;> first | (exact absurd rfl (hs _ _ _)) | rfl
    | _ => rfl
  simp only [Spec.classifyPatName, this]
  rfl

/-- `outsideS1` always stops. -/
theorem outsideS1_ne (ds : Spec.Decls) (msg : String) (c : Spec.Core) (r : α) :
    ((Spec.outsideS1 ds msg : Spec.H α).run.run c).1 ≠ .ok r := by
  unfold Spec.outsideS1
  split <;> nofun

/-- An `&&` whose first operand is true does what its second does. -/
theorem patOk_and (look : GoCore.GVar → Option GoCore.GVal) (lp : LPat) (u : GoCore.GExpr)
    (hu : peval look u = some (.bool true)) (h : PatOk look lp r) :
    PatOk look (.and u lp.1, lp.2) r :=
  ⟨by simp [peval, hu, h.1], h.2⟩

theorem patOk_nil (look : GoCore.GVar → Option GoCore.GVal) (ts : List STy) (proj i)
    {ds : Spec.Decls} {inScope : String → Bool} {c : Spec.Core} {r}
    (hm : ((Spec.matchPats ds c.locals c.globals [] []).run.run c).1 = .ok r) :
    PatOk look (lowerPats ds inScope ts [] proj i) r := by
  rw [Spec.matchPats.eq_def] at hm
  change Except.ok (some []) = Except.ok r at hm
  injection hm with hm
  subst hm
  have : lowerPats ds inScope ts [] proj i = (.lit (.bool true), []) := by
    cases ts <;> simp [lowerPats]
  rw [this]
  exact ⟨rfl, fun bs h => by simp only [Option.some.injEq] at h; subst h; exact .nil⟩

mutual
/-- **The pattern lowering is correct.** For a value of the static type the lowering was
    given, matched against the value of `e`: whatever galaspec's `matchPat` gives (other
    than a stop), the lowered condition and pattern-variable expressions agree with it. -/
theorem lowerPat_correct (ds : Spec.Decls) (inScope : String → Bool) (c : Spec.Core)
    (look : GoCore.GVar → Option GoCore.GVal) (hR : ReadsOk c look) (hS : ScopeOk inScope c) :
    ∀ (t : STy) (p : Spec.Pat) (e : GoCore.GExpr) (v : Spec.Val) (r : Option (List (String × Spec.Val))),
      HasTy ds t v → peval look e = some (enc v) →
      ((Spec.matchPat ds c.locals c.globals p v).run.run c).1 = .ok r →
      PatOk look (lowerPat ds inScope t p e) r
  | t, .wild, e, v, r, _, _, hm => by
    rw [Spec.matchPat.eq_def] at hm
    change Except.ok (some []) = Except.ok r at hm
    injection hm with hm; subst hm
    simp only [lowerPat]
    exact ⟨rfl, fun bs h => by simp only [Option.some.injEq] at h; subst h; exact .nil⟩
  | t, .unsupported k, e, v, r, _, _, hm => by
    rw [Spec.matchPat.eq_def] at hm
    nomatch hm
  | t, .lit l, e, v, r, _, he, hm => by
    rw [Spec.matchPat.eq_def] at hm
    simp only at hm
    rw [run_bind_ro (Spec.readOnly_litMatches l v)] at hm
    simp only [lowerPat]
    cases l <;> cases v <;> simp only [Spec.litMatches] at hm
    all_goals first
      | (cases hm; done)
      | (change Except.ok (if _ then some [] else none) = Except.ok r at hm
         injection hm with hm; subst hm
         refine ⟨?_, fun bs h => ?_⟩
         · simp only [peval, Option.bind_eq_bind, he, Option.bind_some, GoCore.binVal, encLit, enc,
             GoCore.geq, BEq.comm]
           split <;> simp_all
         · split at h
           · simp only [Option.some.injEq] at h; subst h; exact .nil
           · simp at h)
  | t, .name x, e, v, r, ht, he, hm => by
    rw [Spec.matchPat.eq_def] at hm
    simp only at hm
    generalize hk : Spec.classifyPatName _ c.locals c.globals x = k at hm
    have hk' : Spec.classifyPatName (casesOf ds v) c.locals c.globals x = k := hk
    rw [classify_static ht] at hk'
    simp only [lowerPat]
    rcases hct : staticCase ds t x with _ | cv
    · rw [hct] at hk'
      simp only [Option.isSome_none, Bool.false_eq_true, ite_false]
      have hsc := hS x
      cases hcap : Spec.capitalized x <;> cases hl : Spec.lookupVar c.locals c.globals x <;>
        simp only [hcap, hl] at hk' hsc <;> subst hk' <;>
        simp only [hsc, Option.isSome_none, Option.isSome_some, Bool.and_true, Bool.and_false,
          Bool.false_eq_true, ite_false, ite_true] at hm ⊢
      case true.some b =>
        -- A stable identifier: compared with `==`.
        rw [run_bind_ro (Spec.readOnly_deref _)] at hm
        rcases hd : ((Spec.deref b.val).run.run c).1 with _ | w
        · rw [hd] at hm; nomatch hm
        · rw [hd] at hm
          change Except.ok (if _ then some [] else none) = Except.ok r at hm
          injection hm with hm; subst hm
          have hw := hR x b hl w hd
          refine ⟨?_, fun bs h => ?_⟩
          · simp only [peval, Option.bind_eq_bind, he, hw, Option.bind_some, GoCore.binVal,
              ← valEq_enc, valEq_comm v w]
            split <;> simp_all
          · split at h
            · simp only [Option.some.injEq] at h; subst h; exact .nil
            · simp at h
      all_goals
        change Except.ok (some [(x, v)]) = Except.ok r at hm
        injection hm with hm; subst hm
        exact ⟨rfl, fun bs h => by
          simp only [Option.some.injEq] at h; subst h; exact .cons ⟨rfl, he, ht.notRef⟩ .nil⟩
    · -- A case of the subject's sealed type: tested with the case's `Unapply`.
      rw [hct] at hk'; subst hk'
      simp only [Option.isSome_some, ite_true]
      cases ht with
      | sealed ty c cv' fs _ _ =>
        have hname : cv.name = x := by
          have := List.find?_some hct
          simpa using this
        simp only at hm
        split at hm
        · nomatch hm
        · change Except.ok (if _ then some [] else none) = Except.ok r at hm
          injection hm with hm; subst hm
          refine ⟨?_, fun bs h => ?_⟩
          · simp only [peval, he, Option.map_some, enc, GoCore.primVal, hname]
            split <;> simp_all
          · split at h
            · simp only [Option.some.injEq] at h; subst h; exact .nil
            · simp at h
      | _ => simp [staticCase] at hct
  | t, .tuple ps, e, v, r, ht, he, hm => by
    rw [Spec.matchPat.eq_def] at hm
    cases ht with
    | tuple ts vs hts =>
      simp only at hm
      simp only [lowerPat]
      split at hm
      · rename_i hlen
        exact lowerPats_correct ds inScope c look hR hS ts ps _ 0 vs r hts (by simpa using hlen)
          (fun j hj => by simp [peval, he, enc, GoCore.primVal, encs_getElem? vs j hj]) hm
      · nomatch hm
    | other v hs hst hnt _ => cases v <;> first | exact absurd rfl (hnt _) | nomatch hm
    | _ => nomatch hm
  | t, .extract cn ps, e, v, r, ht, he, hm => by
    rw [Spec.matchPat.eq_def] at hm
    cases ht with
    | sealed ty k cv fs hcv hfs =>
      simp only at hm
      rw [show ((ds.sealeds.lookup ty).getD []).find? (·.name == cn) = caseIn ds ty cn from rfl] at hm
      simp only [lowerPat]
      rcases hci : caseIn ds ty cn with _ | cv2
      · rw [hci] at hm
        simp only at hm
        exact absurd hm (outsideS1_ne _ _ _ _)
      · rw [hci] at hm
        simp only at hm ⊢
        by_cases hc : k = cn
        · -- The value's case: the extractor's `Unapply` gives its fields.
          subst hc
          have : cv2 = cv := by rw [hcv] at hci; exact (Option.some.inj hci).symm
          subst this
          simp only [bne_self_eq_false, Bool.false_eq_true, ite_false] at hm
          split at hm
          · nomatch hm
          · rename_i hlen
            simp only [Bool.or_eq_true, bne_iff_ne, ne_eq, not_or, Decidable.not_not] at hlen
            obtain ⟨_, hl2⟩ := hlen
            rcases ps with _ | ⟨p, _ | ⟨p2, ps'⟩⟩
            · cases fs with
              | cons => simp at hl2
              | nil =>
                rw [Spec.matchPats.eq_def] at hm
                change Except.ok (some []) = Except.ok r at hm
                injection hm with hm; subst hm
                refine ⟨by simp [peval, he, enc, GoCore.primVal], fun bs h => ?_⟩
                simp only [Option.some.injEq] at h; subst h; exact .nil
            · obtain ⟨f, rfl⟩ : ∃ f, fs = [f] := by
                match fs, hl2 with | [f], _ => exact ⟨f, rfl⟩
              simp only [List.length_cons, List.length_nil]
              refine patOk_and _ _ _ (by simp [peval, he, enc, encs, GoCore.primVal]) ?_
              exact lowerPats_correct ds inScope c look hR hS _ [p] _ 0 [f] r hfs rfl
                (fun j hj => by
                  obtain rfl : j = 0 := by simpa using hj
                  simp [peval, he, enc, encs, GoCore.primVal]) hm
            · simp only [List.length_cons]
              refine patOk_and _ _ _ (by simp [peval, he, enc, GoCore.primVal]) ?_
              exact lowerPats_correct ds inScope c look hR hS _ _ _ 0 fs r hfs (by simpa using hl2.symm)
                (fun j hj => by simp [peval, he, enc, GoCore.primVal, encs_getElem? fs j hj]) hm
        · -- Another case: `Unapply` gives `None` (or `false`).
          have hc' : (k != cn) = true := by simpa using hc
          simp only [hc', ite_true] at hm
          change Except.ok none = Except.ok r at hm
          injection hm with hm; subst hm
          refine patOk_false _ _ ?_
          have hne : (k == cn) = false := by simpa using hc
          rcases ps with _ | ⟨p, _ | ⟨p2, ps'⟩⟩ <;>
            simp [peval, he, enc, GoCore.primVal, hne]
    | struct_ s fs _ hfs =>
      simp only at hm
      simp only [lowerPat]
      split at hm
      · exact absurd hm (outsideS1_ne _ _ _ _)
      · rename_i hcs
        have hcs : cn = s := by simpa using hcs
        subst hcs
        simp only [beq_self_eq_true, ite_true]
        split at hm
        · nomatch hm
        · rename_i hlen
          exact lowerPats_correct ds inScope c look hR hS _ ps _ 0 fs r hfs (by simp at hlen; omega)
            (fun j hj => by simp [peval, he, enc, GoCore.primVal, encs_getElem? fs j hj]) hm
    | tuple ts vs _ => exact absurd hm (outsideS1_ne _ _ _ _)
    | other v hs hst hnt _ =>
      cases v <;> first
        | exact absurd rfl (hs _ _ _)
        | exact absurd rfl (hst _ _)
        | exact absurd hm (outsideS1_ne _ _ _ _)
/-- Sub-patterns, left to right. -/
theorem lowerPats_correct (ds : Spec.Decls) (inScope : String → Bool) (c : Spec.Core)
    (look : GoCore.GVar → Option GoCore.GVal) (hR : ReadsOk c look) (hS : ScopeOk inScope c) :
    ∀ (ts : List STy) (ps : List Spec.Pat) (proj : Nat → GoCore.GExpr) (i : Nat)
      (vs : List Spec.Val) (r : Option (List (String × Spec.Val))),
      HasTys ds ts vs → ps.length = vs.length →
      (∀ j (hj : j < vs.length), peval look (proj (i + j)) = some (enc vs[j])) →
      ((Spec.matchPats ds c.locals c.globals ps vs).run.run c).1 = .ok r →
      PatOk look (lowerPats ds inScope ts ps proj i) r
  | t :: ts, p :: ps, proj, i, v :: vs, r, .cons ht hts, hl, hp, hm => by
    rw [matchPats_cons_run] at hm
    have hp0 := hp 0 (by simp)
    simp only [Nat.add_zero, List.getElem_cons_zero] at hp0
    rcases h1 : ((Spec.matchPat ds c.locals c.globals p v).run.run c).1 with e1 | _ | b
    · rw [h1] at hm; simp at hm
    · rw [h1] at hm
      simp only [Except.ok.injEq] at hm; subst hm
      have ⟨hc1, _⟩ := lowerPat_correct ds inScope c look hR hS t p (proj i) v none ht hp0 h1
      refine patOk_false _ _ ?_
      simp only [lowerPats, peval, Option.bind_eq_bind, hc1, Option.isSome_none]
      rfl
    · rw [h1] at hm
      have ⟨hc1, hb1⟩ := lowerPat_correct ds inScope c look hR hS t p (proj i) v (some b) ht hp0 h1
      have hrest := fun r' => lowerPats_correct ds inScope c look hR hS ts ps proj (i + 1) vs r'
        hts (by simpa using hl) (fun j hj => by
          have := hp (j + 1) (by simpa using hj)
          simpa [Nat.add_assoc, Nat.add_comm 1 j] using this)
      simp only at hm
      rcases h2 : ((Spec.matchPats ds c.locals c.globals ps vs).run.run c).1 with e2 | _ | bs
      · rw [h2] at hm; simp at hm
      · rw [h2] at hm
        simp only [Except.ok.injEq] at hm; subst hm
        have ⟨hc2, _⟩ := hrest none h2
        refine patOk_false _ _ ?_
        simp only [lowerPats, peval, Option.bind_eq_bind, hc1, Option.isSome_some, Option.bind_some, hc2,
          Option.isSome_none]
      · rw [h2] at hm
        simp only [Except.ok.injEq] at hm; subst hm
        have ⟨hc2, hb2⟩ := hrest (some bs) h2
        refine ⟨?_, fun bs' hbs => ?_⟩
        · simp only [lowerPats, peval, Option.bind_eq_bind, hc1, Option.isSome_some, Option.bind_some,
            hc2]
        · simp only [Option.some.injEq] at hbs; subst hbs
          exact fall2_append (hb1 b rfl) (hb2 bs rfl)
  | [], ps, proj, i, vs, r, h, hl, hp, hm => by
    cases h
    cases ps with
    | nil => exact patOk_nil look _ _ _ hm
    | cons => simp at hl
  | _ :: _, [], proj, i, vs, r, h, hl, hp, hm => by
    cases vs with
    | nil => exact patOk_nil look _ _ _ hm
    | cons => simp at hl
  | _ :: _, _ :: _, _, _, [], _, h, _, _, _ => by cases h
end

/-! ## Relating the two states -/

/-- The value a galaspec binding holds: a `var`'s is in its heap cell. -/
def bval (heap : Array Spec.Val) (b : Spec.Binding) : Option Spec.Val :=
  match b.val with
  | .ref n => heap[n]?
  | v => some v

/-- A galaspec binding and a GoCore value agree: the GoCore value encodes the binding's. -/
def BRel (heap : Array Spec.Val) (b : Spec.Binding) (w : GoCore.GVal) : Prop :=
  ∃ v, bval heap b = some v ∧ w = enc v

/-- The locals agree: leaving out GoCore's temporaries, the two stacks have the same names
    in the same order, and related values. -/
inductive LocRel (heap : Array Spec.Val) : Spec.Env → List (GoCore.GVar × GoCore.GVal) → Prop
  | nil : LocRel heap [] []
  | tmp : LocRel heap ρ τ → LocRel heap ρ ((.tmp k, w) :: τ)
  | user : LocRel heap ρ τ → BRel heap b w → LocRel heap ((x, b) :: ρ) ((.user x, w) :: τ)

/-- **The state relation.** The same output; related locals; the package-level variables
    with the same names, in order, and related values. -/
def R (c : Spec.Core) (g : GoCore.GSt) : Prop :=
  c.out = g.out ∧ LocRel c.heap c.locals g.locals ∧
    Spec.Fall2 (fun (xb : String × Spec.Binding) (yw : String × GoCore.GVal) =>
      xb.1 = yw.1 ∧ BRel c.heap xb.2 yw.2) c.globals g.globals

theorem hrun_get (c : Spec.Core) : (get : Spec.H Spec.Core).run.run c = (.ok c, c) := rfl

theorem deref_ok (b : Spec.Binding) (c : Spec.Core) (v : Spec.Val)
    (h : ((Spec.deref b.val).run.run c).1 = .ok v) : bval c.heap b = some v := by
  unfold bval
  cases hb : b.val with
  | ref n =>
    rw [hb] at h
    cases hh : c.heap[n]? with
    | none =>
      have : ((Spec.deref (.ref n)).run.run c).1 = .error (.stuck "a reference to no cell") := by
        simp only [Spec.deref, Spec.run_bind_eq, hrun_get, hh]; rfl
      rw [this] at h; nomatch h
    | some w =>
      have : ((Spec.deref (.ref n)).run.run c).1 = .ok w := by
        simp only [Spec.deref, Spec.run_bind_eq, hrun_get, hh]; rfl
      rw [this] at h; injection h with h; subst h; simp [hh]
  | _ => rw [hb] at h; injection h with h; subst h; rfl

theorem lookup_user_cons_tmp (x : String) (k : Nat) (w : GoCore.GVal)
    (τ : List (GoCore.GVar × GoCore.GVal)) :
    ((GoCore.GVar.tmp k, w) :: τ).lookup (.user x) = τ.lookup (.user x) := by
  have : (GoCore.GVar.user x == GoCore.GVar.tmp k) = false := rfl
  simp [List.lookup, this]

theorem LocRel.lookup {heap : Array Spec.Val} {ρ τ} (h : LocRel heap ρ τ) (x : String) :
    match Spec.lookup ρ x with
    | some b => ∃ w, τ.lookup (.user x) = some w ∧ BRel heap b w
    | none => τ.lookup (.user x) = none := by
  induction h with
  | nil => simp [Spec.lookup]
  | tmp _ ih => rw [lookup_user_cons_tmp]; exact ih
  | @user ρ τ b w y _ hb ih =>
    simp only [Spec.lookup]
    by_cases hyx : y = x
    · subst hyx; simp only [ite_true]; exact ⟨w, by simp [List.lookup], hb⟩
    · simp only [hyx, ite_false]
      have : (GoCore.GVar.user x == GoCore.GVar.user y) = false := by simp [Ne.symm hyx]
      simp only [List.lookup, this]
      exact ih

theorem globals_lookup {heap : Array Spec.Val} :
    ∀ {G : Spec.Env} {Γ : List (String × GoCore.GVal)},
    Spec.Fall2 (fun (xb : String × Spec.Binding) (yw : String × GoCore.GVal) =>
      xb.1 = yw.1 ∧ BRel heap xb.2 yw.2) G Γ → ∀ (x : String) (b : Spec.Binding),
    Spec.lookup G x = some b → ∃ w, Γ.lookup x = some w ∧ BRel heap b w
  | _, _, .nil, x, b, h => by simp [Spec.lookup] at h
  | (y, b') :: G, (y', w) :: Γ, .cons ⟨hy, hb⟩ hr, x, b, h => by
    simp only at hy; subst hy
    simp only [Spec.lookup] at h
    by_cases hyx : y = x
    · subst hyx; simp only [ite_true, Option.some.injEq] at h; subst h
      exact ⟨w, by simp [List.lookup], hb⟩
    · simp only [hyx, ite_false] at h
      have : (x == y) = false := by simp [Ne.symm hyx]
      simp only [List.lookup, this]
      exact globals_lookup hr x b h

/-- Related states read names alike: GoCore reads a name galaspec has bound as the
    encoding of the binding's value. -/
theorem R_reads {c : Spec.Core} {g : GoCore.GSt} (h : R c g) : ReadsOk c (GoCore.lookupVar g) := by
  intro x b hb v hv
  have hbv := deref_ok b c v hv
  simp only [Spec.lookupVar] at hb
  have hl := h.2.1.lookup x
  unfold GoCore.lookupVar
  cases hlx : Spec.lookup c.locals x with
  | some b' =>
    rw [hlx] at hl hb
    simp at hb; subst hb
    obtain ⟨w, hw, v', hv', rfl⟩ := hl
    rw [hbv] at hv'; injection hv' with hv'; subst hv'
    simp [hw]
  | none =>
    rw [hlx] at hl hb
    simp at hb
    obtain ⟨w, hw, v', hv', rfl⟩ := globals_lookup h.2.2 x b hb
    rw [hbv] at hv'; injection hv' with hv'; subst hv'
    simp [hl, hw]

/-! ## Lowering a `match` -/

/-- One arm: its pattern, its guard and its body, each in galaspec and as the GoCore code
    it lowers to. M3 takes the guards' and bodies' lowering as given (holes, `ext`). -/
structure Arm where
  pat : Spec.Pat
  guard : Option (Spec.Expr × GoCore.GExpr)
  body : Spec.Expr × GoCore.GExpr

/-- The arm as galaspec has it. -/
def Arm.src (a : Arm) : Spec.Pat × Option Spec.Expr × Spec.Expr :=
  (a.pat, a.guard.map (·.1), a.body.1)

open GoCore in
/-- `_tmp_<k> := e` for each pattern variable's expression, numbered from `n`. -/
def defsOf : Nat → List (String × GExpr) → List GStmt
  | _, [] => []
  | n, (_, e) :: r => .define (.tmp n) e :: defsOf (n + 1) r

open GoCore in
/-- `x := _tmp_<k>` for each pattern variable, numbered from `n`, except `_`, which binds
    nothing. -/
def bindUser : Nat → List (String × GExpr) → List GStmt
  | _, [] => []
  | n, (x, _) :: r =>
    if x = "_" then bindUser (n + 1) r else .define (.user x) (.var (.tmp n)) :: bindUser (n + 1) r

open GoCore in
/-- **The scoped lowering of the arms**, temporaries numbered from `n`. Each arm is a block:
    its pattern variables' expressions into temporaries, then the test; the pattern
    variables are bound to their names only inside the branches that read them, the
    guard's and the body's, so the later arms (in the `else`) never see them. A guard is
    evaluated in a block of its own into a flag, before the `if`. No arm left: a panic. -/
def lowerArms (ds : Spec.Decls) (inScope : String → Bool) (t : STy) :
    Nat → List Arm → List GStmt
  | _, [] => [.panic .match_ "no match arm matched"]
  | n, a :: rest =>
    let lp := lowerPat ds inScope t a.pat (.var (.tmp 0))
    let ok := n + lp.2.length
    let body := bindUser n lp.2 ++ [.ret a.body.2]
    let others := lowerArms ds inScope t (ok + 1) rest
    match a.guard with
    | none => [.block (defsOf n lp.2 ++ [.ifElse lp.1 body others])]
    | some (_, g) => [.block (defsOf n lp.2 ++
        [.varDecl (.tmp ok) (.bool false),
         .ifElse lp.1 (bindUser n lp.2 ++ [.assign (.tmp ok) g]) [],
         .ifElse (.var (.tmp ok)) body others])]

open GoCore in
/-- **The scoped lowering of `match`**: the subject once into a temporary, then the arms,
    in a function literal called at once whose `return` is the match's value. -/
def lowerMatch (ds : Spec.Decls) (inScope : String → Bool) (t : STy) (subj : GExpr)
    (arms : List Arm) : GExpr :=
  .funcLit (.define (.tmp 0) subj :: lowerArms ds inScope t 1 arms)

/-! ### Running GoCore statements -/

section GRun2
open GoCore

theorem gexecs_nil (X : Nat → GM GVal) (g : GSt) : (gexecs X []).run.run g = (.ok (), g) := by
  rw [gexecs]; rfl

theorem gexecs_cons (X : Nat → GM GVal) (s : GStmt) (ss : List GStmt) (g : GSt) :
    (gexecs X (s :: ss)).run.run g = match (gexec X s).run.run g with
      | (.ok _, t) => (gexecs X ss).run.run t
      | (.error e, t) => (.error e, t) := by
  rw [gexecs, grun_bind]
  rcases (gexec X s).run.run g with ⟨r, t⟩
  cases r <;> rfl

theorem gexecs_append (X : Nat → GM GVal) (ss ss' : List GStmt) (g : GSt) :
    (gexecs X (ss ++ ss')).run.run g = match (gexecs X ss).run.run g with
      | (.ok _, t) => (gexecs X ss').run.run t
      | (.error e, t) => (.error e, t) := by
  induction ss generalizing g with
  | nil => rw [gexecs_nil]; rfl
  | cons s ss ih =>
    rw [List.cons_append, gexecs_cons, gexecs_cons]
    rcases (gexec X s).run.run g with ⟨r, t⟩
    cases r
    · rfl
    · exact ih t

theorem withScope_run (m : GM α) (g : GSt) :
    (withScope m).run.run g = match m.run.run g with
      | (.ok a, t) => (.ok a, truncate t g.locals.length)
      | (.error e, t) => (.error e, t) := by
  simp only [withScope, grun_bind, grun_get]
  rcases m.run.run g with ⟨r, t⟩
  cases r <;> rfl

theorem gexec_define (X : Nat → GM GVal) (x : GVar) (e : GExpr) (g : GSt) :
    (gexec X (.define x e)).run.run g = match (geval X e).run.run g with
      | (.ok v, t) => (.ok (), { t with locals := (x, v) :: t.locals })
      | (.error e, t) => (.error e, t) := by
  rw [gexec, grun_bind]
  rcases (geval X e).run.run g with ⟨r, t⟩
  cases r <;> rfl

end GRun2

/-! ### Lists -/

/-- Split a `Fall2` over an append on the left. -/
theorem fall2_split_left {R : α → β → Prop} :
    ∀ {as as' : List α} {bs : List β}, Spec.Fall2 R (as ++ as') bs →
      ∃ b1 b2, bs = b1 ++ b2 ∧ Spec.Fall2 R as b1 ∧ Spec.Fall2 R as' b2
  | [], _, _, h => ⟨[], _, rfl, .nil, h⟩
  | _ :: _, _, _ :: _, .cons hab h => by
    obtain ⟨b1, b2, rfl, h1, h2⟩ := fall2_split_left h
    exact ⟨_ :: b1, b2, rfl, .cons hab h1, h2⟩

/-! ### Temporaries -/

/-- A stack entry of a compiler temporary. -/
def isTmpE (e : GoCore.GVar × GoCore.GVal) : Bool :=
  match e.1 with
  | .tmp _ => true
  | .user _ => false

/-- Temporaries `n, n + 1, ...` holding `ws`, declared in that order on top of `τ`. -/
def pushTemps : Nat → List GoCore.GVal → List (GoCore.GVar × GoCore.GVal) →
    List (GoCore.GVar × GoCore.GVal)
  | _, [], τ => τ
  | n, w :: ws, τ => pushTemps (n + 1) ws ((.tmp n, w) :: τ)

theorem pushTemps_eq : ∀ (n : Nat) (ws : List GoCore.GVal) (τ : List (GoCore.GVar × GoCore.GVal)),
    ∃ T, pushTemps n ws τ = T ++ τ ∧ T.length = ws.length ∧
      ∀ e ∈ T, ∃ k, n ≤ k ∧ k < n + ws.length ∧ e.1 = .tmp k
  | _, [], τ => ⟨[], rfl, rfl, by simp⟩
  | n, w :: ws, τ => by
    obtain ⟨T, hT, hl, hk⟩ := pushTemps_eq (n + 1) ws ((.tmp n, w) :: τ)
    refine ⟨T ++ [(.tmp n, w)], by simp [pushTemps, hT], by simp [hl], fun e he => ?_⟩
    simp only [List.mem_append, List.mem_singleton] at he
    rcases he with he | rfl
    · obtain ⟨k, h1, h2, h3⟩ := hk e he
      exact ⟨k, by omega, by simp; omega, h3⟩
    · exact ⟨n, Nat.le_refl _, by simp, rfl⟩

theorem lookup_append_notin (x : GoCore.GVar) (T τ : List (GoCore.GVar × GoCore.GVal))
    (h : ∀ e ∈ T, e.1 ≠ x) : (T ++ τ).lookup x = τ.lookup x := by
  induction T with
  | nil => rfl
  | cons e T ih =>
    have hx : (x == e.1) = false := by simpa using Ne.symm (h e (by simp))
    simp only [List.cons_append, List.lookup, hx]
    exact ih fun e' he' => h e' (by simp [he'])

theorem pushTemps_lookup : ∀ (n : Nat) (ws : List GoCore.GVal) (τ : List (GoCore.GVar × GoCore.GVal))
    (j : Nat) (hj : j < ws.length), (pushTemps n ws τ).lookup (.tmp (n + j)) = some ws[j]
  | n, w :: ws, τ, 0, _ => by
    obtain ⟨T, hT, _, hk⟩ := pushTemps_eq (n + 1) ws ((.tmp n, w) :: τ)
    simp only [pushTemps, hT, Nat.add_zero]
    rw [lookup_append_notin]
    · simp [List.lookup]
    · intro e he; obtain ⟨k, h1, _, h3⟩ := hk e he; rw [h3]; simp; omega
  | n, w :: ws, τ, j + 1, hj => by
    simp only [pushTemps]
    have := pushTemps_lookup (n + 1) ws ((.tmp n, w) :: τ) j (by simpa using hj)
    rw [show n + 1 + j = n + (j + 1) by omega] at this
    simpa using this

/-- Reading a variable other than the temporaries pushed. -/
theorem pushTemps_lookup_other (n : Nat) (ws : List GoCore.GVal) (τ : List (GoCore.GVar × GoCore.GVal))
    (x : GoCore.GVar) (hx : ∀ k, n ≤ k → x ≠ .tmp k) :
    (pushTemps n ws τ).lookup x = τ.lookup x := by
  obtain ⟨T, hT, _, hk⟩ := pushTemps_eq n ws τ
  rw [hT, lookup_append_notin]
  intro e he; obtain ⟨k, h1, _, h3⟩ := hk e he; rw [h3]; exact (hx k h1).symm

theorem LocRel.append_tmps {heap : Array Spec.Val} {ρ τ} (h : LocRel heap ρ τ) :
    ∀ (T : List (GoCore.GVar × GoCore.GVal)), (∀ e ∈ T, isTmpE e) → LocRel heap ρ (T ++ τ)
  | [], _ => h
  | (x, w) :: T, hT => by
    have hx := hT (x, w) (by simp)
    cases x with
    | tmp k => exact .tmp (h.append_tmps T fun e he => hT e (by simp [he]))
    | user _ => simp [isTmpE] at hx

/-! ### The pattern variables' expressions are total -/

mutual
/-- Each pattern variable's expression is a chain of pattern primitives on the subject's,
    so it evaluates whenever the subject's does, matched or not. -/
theorem lowerPat_total (ds : Spec.Decls) (inScope : String → Bool)
    (look : GoCore.GVar → Option GoCore.GVal) :
    ∀ (t : STy) (p : Spec.Pat) (e : GoCore.GExpr), (peval look e).isSome →
      ∀ xe ∈ (lowerPat ds inScope t p e).2, (peval look xe.2).isSome
  | t, .name x, e, he => by
    simp only [lowerPat]
    split
    · simp
    · split <;> simp_all
  | .tuple ts, .tuple ps, e, he =>
    lowerPats_total ds inScope look ts ps _ 0 fun i => by simp [peval, Option.isSome_map, he]
  | .sealed ty, .extract cn ps, e, he => by
    simp only [lowerPat]
    split
    · simp
    · split
      · simp
      · exact lowerPats_total ds inScope look _ ps _ 0 fun i => by simp [peval, Option.isSome_map, he]
      · exact lowerPats_total ds inScope look _ ps _ 0 fun i => by simp [peval, Option.isSome_map, he]
  | .struct_ s, .extract cn ps, e, he => by
    simp only [lowerPat]
    split
    · exact lowerPats_total ds inScope look _ ps _ 0 fun i => by simp [peval, Option.isSome_map, he]
    · simp
  | .tuple _, .extract .., _, _ | .other, .extract .., _, _
  | .sealed _, .tuple _, _, _ | .struct_ _, .tuple _, _, _ | .other, .tuple _, _, _
  | _, .wild, _, _ | _, .lit _, _, _ | _, .unsupported _, _, _ => by simp [lowerPat]
theorem lowerPats_total (ds : Spec.Decls) (inScope : String → Bool)
    (look : GoCore.GVar → Option GoCore.GVal) :
    ∀ (ts : List STy) (ps : List Spec.Pat) (proj : Nat → GoCore.GExpr) (i : Nat),
      (∀ j, (peval look (proj j)).isSome) →
      ∀ xe ∈ (lowerPats ds inScope ts ps proj i).2, (peval look xe.2).isSome
  | t :: ts, p :: ps, proj, i, hp => by
    simp only [lowerPats, List.mem_append]
    rintro xe (h | h)
    · exact lowerPat_total ds inScope look t p (proj i) (hp i) xe h
    · exact lowerPats_total ds inScope look ts ps proj (i + 1) hp xe h
  | [], _, _, _, _ | _ :: _, [], _, _, _ => by simp [lowerPats]
end

/-! ### Declaring the pattern variables' temporaries -/

/-- A GoCore state where a pattern can be tested against the subject `v`: related to `c`,
    with the subject's temporary holding `enc v`. -/
def Good (c : Spec.Core) (v : Spec.Val) (g : GoCore.GSt) : Prop :=
  R c g ∧ GoCore.lookupVar g (.tmp 0) = some (enc v)

theorem Good.push {c : Spec.Core} {v : Spec.Val} {g : GoCore.GSt} (h : Good c v g) (n : Nat)
    (hn : 1 ≤ n) (ws : List GoCore.GVal) :
    Good c v { g with locals := pushTemps n ws g.locals } := by
  obtain ⟨⟨ho, hl, hg⟩, h0⟩ := h
  obtain ⟨T, hT, _, hk⟩ := pushTemps_eq n ws g.locals
  refine ⟨⟨ho, ?_, hg⟩, ?_⟩
  · rw [hT]
    exact hl.append_tmps T fun e he => by obtain ⟨k, _, _, h3⟩ := hk e he; simp [isTmpE, h3]
  · unfold GoCore.lookupVar at h0 ⊢
    rw [pushTemps_lookup_other n ws g.locals (.tmp 0) (fun k hk h => by injection h; omega)]
    exact h0

/-- **Declaring the temporaries.** `defsOf` runs to its end, declaring one temporary per
    pattern variable; when the pattern matched, each holds the encoding of the value
    galaspec bound. Stated for a suffix `suf` of the pattern variables, `pre` before it. -/
theorem defsOf_run (X : Nat → GoCore.GM GoCore.GVal) (c : Spec.Core) (v : Spec.Val) (lp : LPat)
    (r : Option (List (String × Spec.Val)))
    (hok : ∀ g, Good c v g → PatOk (GoCore.lookupVar g) lp r)
    (htot : ∀ g, Good c v g → ∀ xe ∈ lp.2, (peval (GoCore.lookupVar g) xe.2).isSome) :
    ∀ (suf pre : List (String × GoCore.GExpr)), lp.2 = pre ++ suf → ∀ (n : Nat), 1 ≤ n →
      ∀ g, Good c v g → ∃ ws : List GoCore.GVal, ws.length = suf.length ∧
        (GoCore.gexecs X (defsOf n suf)).run.run g =
          (.ok (), { g with locals := pushTemps n ws g.locals }) ∧
        ∀ bsv, r = some bsv → Spec.Fall2 (fun w (xv : String × Spec.Val) => w = enc xv.2) ws
          (bsv.drop pre.length)
  | [], pre, hsplit, n, hn, g, hg => by
    refine ⟨[], rfl, by rw [defsOf, gexecs_nil]; rfl, fun bsv hr => ?_⟩
    have hl := Spec.fall2_length ((hok g hg).2 bsv hr)
    rw [hsplit] at hl
    simp only [List.append_nil] at hl
    rw [hl, List.drop_length]
    exact .nil
  | (x, e) :: suf, pre, hsplit, n, hn, g, hg => by
    obtain ⟨w, hw⟩ := Option.isSome_iff_exists.mp (htot g hg (x, e) (by rw [hsplit]; simp))
    have hev := geval_peval X g e w hw
    let g1 : GoCore.GSt := { g with locals := (.tmp n, w) :: g.locals }
    have hg1 : Good c v g1 := hg.push n hn [w]
    obtain ⟨ws, hl, hrun, hvals⟩ := defsOf_run X c v lp r hok htot suf (pre ++ [(x, e)])
      (by rw [hsplit]; simp) (n + 1) (by omega) g1 hg1
    refine ⟨w :: ws, by simp [hl], ?_, fun bsv hr => ?_⟩
    · rw [defsOf, gexecs_cons, gexec_define, hev]
      simp only
      rw [hrun]
      rfl
    · have hf := (hok g hg).2 bsv hr
      rw [hsplit] at hf
      obtain ⟨b1, b2, hb, h1, h2⟩ := fall2_split_left hf
      have hb1 := Spec.fall2_length h1
      cases h2 with
      | cons hxe h2' =>
        rename_i b bs'
        rw [hb, List.drop_left' hb1.symm]
        refine .cons ?_ ?_
        · rw [hw] at hxe; exact Option.some.inj hxe.2.1
        · have := hvals bsv hr
          rw [hb, show b1 ++ b :: bs' = (b1 ++ [b]) ++ bs' by simp,
            List.drop_left' (by simp [hb1])] at this
          exact this

/-! ### Binding the pattern variables to their names -/

/-- The pattern variables (but `_`) with the temporaries' values, declared in order on top
    of `acc`. -/
def userTop : List (String × GoCore.GExpr) → List GoCore.GVal →
    List (GoCore.GVar × GoCore.GVal) → List (GoCore.GVar × GoCore.GVal)
  | (x, _) :: bs, w :: ws, acc => userTop bs ws (if x = "_" then acc else (.user x, w) :: acc)
  | _, _, acc => acc

/-- **Binding the pattern variables.** `bindUser` reads each temporary and declares the
    pattern variable's name with its value. -/
theorem bindUser_run (X : Nat → GoCore.GM GoCore.GVal) :
    ∀ (bs : List (String × GoCore.GExpr)) (ws : List GoCore.GVal) (n : Nat) (g : GoCore.GSt),
      ws.length = bs.length →
      (∀ j (hj : j < ws.length), g.locals.lookup (.tmp (n + j)) = some ws[j]) →
      (GoCore.gexecs X (bindUser n bs)).run.run g = (.ok (), { g with locals := userTop bs ws g.locals })
  | [], [], n, g, _, _ => by rw [bindUser, gexecs_nil]; rfl
  | (x, e) :: bs, w :: ws, n, g, hl, hw => by
    have hw0 := hw 0 (by simp)
    simp only [Nat.add_zero, List.getElem_cons_zero] at hw0
    simp only [bindUser]
    split
    · rename_i hx
      rw [bindUser_run X bs ws (n + 1) g (by simpa using hl) fun j hj => by
        have := hw (j + 1) (by simpa using hj)
        simpa [Nat.add_assoc, Nat.add_comm 1 j] using this]
      simp [userTop, hx]
    · rename_i hx
      rw [gexecs_cons, gexec_define]
      have hv : (GoCore.geval X (.var (.tmp n))).run.run g = (.ok w, g) := by
        simp only [GoCore.geval, grun_bind, grun_get, GoCore.lookupVar, hw0, grun_pure]
      rw [hv]
      simp only
      rw [bindUser_run X bs ws (n + 1) _ (by simpa using hl) fun j hj => by
        have := hw (j + 1) (by simpa using hj)
        have hne : (GoCore.GVar.tmp (n + 1 + j) == GoCore.GVar.user x) = false := rfl
        simp only [List.lookup, hne]
        simpa [Nat.add_assoc, Nat.add_comm 1 j] using this]
      simp [userTop, hx]
  | [], _ :: _, _, _, hl, _ | _ :: _, [], _, _, hl, _ => by simp at hl

theorem pushed_cons (x : String) (v : Spec.Val) (bsv : List (String × Spec.Val)) (ρ : Spec.Env) :
    Spec.pushed ((x, v) :: bsv) ρ = Spec.pushed bsv (Spec.bind ρ x ⟨v, false⟩) := rfl

/-- The pattern variables on both sides: galaspec pushes `A` and GoCore `U`, the same
    names with related values, `U` only names and no temporary. -/
theorem pushed_userTop {heap : Array Spec.Val} :
    ∀ (bs : List (String × GoCore.GExpr)) (bsv : List (String × Spec.Val)) (ws : List GoCore.GVal)
      (ρ : Spec.Env) (τ : List (GoCore.GVar × GoCore.GVal)),
      Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
        xe.1 = xv.1 ∧ ∀ n, xv.2 ≠ .ref n) bs bsv →
      Spec.Fall2 (fun w (xv : String × Spec.Val) => w = enc xv.2) ws bsv →
      ∃ A U, Spec.pushed bsv ρ = A ++ ρ ∧ userTop bs ws τ = U ++ τ ∧ A.length = U.length ∧
        (∀ e ∈ U, isTmpE e = false) ∧
        ∀ ρ' τ', LocRel heap ρ' τ' → LocRel heap (A ++ ρ') (U ++ τ')
  | [], [], [], ρ, τ, .nil, .nil => ⟨[], [], rfl, rfl, rfl, by simp, fun _ _ h => h⟩
  | (x, e) :: bs, (y, v) :: bsv, w :: ws, ρ, τ, .cons ⟨hxy, hnr⟩ h1, .cons hw h2 => by
    simp only at hxy hw hnr; subst hxy; subst hw
    by_cases hx : x = "_"
    · obtain ⟨A, U, hA, hU, hl, hu, hrel⟩ := pushed_userTop bs bsv ws ρ τ h1 h2
      refine ⟨A, U, ?_, ?_, hl, hu, hrel⟩
      · rw [pushed_cons, show Spec.bind ρ x ⟨v, false⟩ = ρ by simp [Spec.bind, hx]]; exact hA
      · simp only [userTop, hx, ite_true]; exact hU
    · obtain ⟨A, U, hA, hU, hl, hu, hrel⟩ :=
        pushed_userTop bs bsv ws ((x, ⟨v, false⟩) :: ρ) ((.user x, enc v) :: τ) h1 h2
      refine ⟨A ++ [(x, ⟨v, false⟩)], U ++ [(.user x, enc v)], ?_, ?_, by simp [hl], ?_, ?_⟩
      · rw [pushed_cons, show Spec.bind ρ x ⟨v, false⟩ = (x, ⟨v, false⟩) :: ρ by simp [Spec.bind, hx], hA]
        simp
      · simp only [userTop, hx, ite_false]; rw [hU]; simp
      · intro e he; simp only [List.mem_append, List.mem_singleton] at he
        rcases he with he | rfl
        · exact hu e he
        · rfl
      · intro ρ' τ' h
        have := hrel ((x, ⟨v, false⟩) :: ρ') ((.user x, enc v) :: τ')
          (.user h ⟨v, by cases v <;> first | rfl | exact absurd rfl (hnr _), rfl⟩)
        simpa using this

/-- Dropping, from related stacks, a top part with as many names on each side. -/
theorem LocRel.drop_top {heap : Array Spec.Val} :
    ∀ (A : Spec.Env) (Z : List (GoCore.GVar × GoCore.GVal)) (ρ : Spec.Env)
      (τ : List (GoCore.GVar × GoCore.GVal)),
      LocRel heap (A ++ ρ) (Z ++ τ) → (Z.filter (fun e => !isTmpE e)).length = A.length →
      LocRel heap ρ τ
  | [], [], _, _, h, _ => h
  | A, (.tmp k, w) :: Z, ρ, τ, h, hl => by
    simp only [List.cons_append] at h
    generalize hA : A ++ ρ = Aρ at h
    cases h with
    | tmp h => subst hA; exact LocRel.drop_top A Z ρ τ h (by simpa [isTmpE] using hl)
  | a :: A, (.user x, w) :: Z, ρ, τ, h, hl => by
    simp only [List.cons_append] at h
    cases h with
    | user h _ => exact LocRel.drop_top A Z ρ τ h (by simpa [isTmpE] using hl)
  | [], (.user x, w) :: Z, ρ, τ, _, hl => by simp [isTmpE] at hl
  | _ :: _, [], ρ, τ, _, hl => by simp at hl

/-! ### Matching never panics

galaspec's pattern match stops only as `stuck` or `unsupported`, never with a panic: so a
match that stops is one M3 says nothing about. -/

/-- An `H` step that never ends in a panic. -/
def NoPanic (x : Spec.H α) : Prop := ∀ c cl m, (x.run.run c).1 ≠ .error (.panic cl m)

theorem noPanic_bind {x : Spec.H α} {f : α → Spec.H β} (hx : NoPanic x) (hf : ∀ a, NoPanic (f a)) :
    NoPanic (x >>= f) := by
  intro c cl m h
  rw [Spec.run_bind_eq] at h
  rcases hr : x.run.run c with ⟨r, t⟩
  rw [hr] at h
  cases r with
  | ok a => exact hf a t cl m h
  | error e => exact hx c cl m (by rw [hr]; simpa using h)

theorem noPanic_pure (a : α) : NoPanic (pure a : Spec.H α) := fun _ _ _ h => nomatch h

theorem noPanic_deref (v : Spec.Val) : NoPanic (Spec.deref v) := by
  intro c cl m h
  cases v with
  | ref n =>
    simp only [Spec.deref, Spec.run_bind_eq, hrun_get] at h
    split at h <;> nomatch h
  | _ => nomatch h

theorem noPanic_litMatches (l : Spec.Lit) (v : Spec.Val) : NoPanic (Spec.litMatches l v) := by
  intro c cl m h; unfold Spec.litMatches at h; split at h <;> nomatch h

theorem noPanic_outsideS1 (ds : Spec.Decls) (msg : String) : NoPanic (Spec.outsideS1 ds msg : Spec.H α) := by
  intro c cl m h; unfold Spec.outsideS1 at h; split at h <;> nomatch h

set_option hygiene false in
macro "np_step" : tactic => `(tactic| (first
  | with_reducible exact noPanic_pure _
  | exact noPanic_deref _
  | exact noPanic_litMatches _ _
  | exact noPanic_outsideS1 _ _
  | (intro c' cl' m' h'; cases h'; done)
  | with_reducible apply ihP
  | with_reducible apply ihPs
  | with_reducible apply noPanic_bind
  | with_reducible intro _
  | split
  | dsimp only))

mutual
theorem matchPat_noPanic (ds : Spec.Decls) (locals globals : Spec.Env) :
    ∀ (p : Spec.Pat) (v : Spec.Val), NoPanic (Spec.matchPat ds locals globals p v)
  | .tuple ps, _ | .extract _ ps, _ => by
    have ihPs := fun vs => matchPats_noPanic ds locals globals ps vs
    rw [Spec.matchPat.eq_def]
    dsimp only
    repeat' np_step
  | .wild, _ | .unsupported _, _ | .lit _, _ | .name _, _ => by
    rw [Spec.matchPat.eq_def]
    dsimp only
    repeat' np_step
theorem matchPats_noPanic (ds : Spec.Decls) (locals globals : Spec.Env) :
    ∀ (ps : List Spec.Pat) (vs : List Spec.Val), NoPanic (Spec.matchPats ds locals globals ps vs)
  | p :: ps, v :: vs => by
    have ihP := matchPat_noPanic ds locals globals p v
    have ihPs := matchPats_noPanic ds locals globals ps vs
    rw [Spec.matchPats.eq_def]
    repeat' np_step
  | [], _ => by rw [Spec.matchPats.eq_def]; exact noPanic_pure _
  | _ :: _, [] => by rw [Spec.matchPats.eq_def]; exact noPanic_pure _
end

/-! ## What M3 takes as given: the parts -/

/-- A panic's class in GoCore. -/
def pcOf : Spec.PanicClass → GoCore.PanicClass
  | .match_ => .match_
  | .divide => .divide
  | .index => .index
  | .other => .other

/-- A result M3 makes a claim about: a value or a panic. Running out of fuel, `stuck`,
    `unsupported` and a `return` on its way out are not. -/
def NotBad : Except Spec.Stop α → Prop
  | .ok _ => True
  | .error (.panic ..) => True
  | .error _ => False

/-- Results agree: the encoded value, or a panic of the same class (the message is not
    compared). -/
def ResRel : Except Spec.Stop Spec.Val → Except GoCore.GStop GoCore.GVal → Prop
  | .ok v, .ok w => w = enc v
  | .error (.panic c _), .error (.panic c' _) => c' = pcOf c
  | _, _ => False

/-- galaspec's frame: the same local and package-level names, in order. -/
def FrameS (c c' : Spec.Core) : Prop :=
  c'.locals.map (·.1) = c.locals.map (·.1) ∧ c'.globals.map (·.1) = c.globals.map (·.1)

/-- GoCore's frame: the same local names, in order, and every temporary's value kept. -/
def FrameL (τ τ' : List (GoCore.GVar × GoCore.GVal)) : Prop :=
  Spec.Fall2 (fun a b => a.1 = b.1 ∧ (isTmpE a = true → a.2 = b.2)) τ τ'

/-- **A part simulates.** From related states, wherever galaspec's run of `x` gives a
    value or a panic, GoCore's run of `y` gives the encoded value or a panic of the same
    class, the states stay related, and on a value: `post` holds of it and both sides keep
    their frames (no local or package-level name added or removed, no temporary changed).
    This is forward simulation from galaspec's results, nothing more: it says nothing
    about runs galaspec ends otherwise. -/
structure Part (pre : Spec.Core → Prop) (post : Spec.Val → Prop) (x : Spec.M Spec.Val)
    (y : GoCore.GM GoCore.GVal) : Prop where
  sim : ∀ (s : Spec.St) (g : GoCore.GSt), R s.toCore g → pre s.toCore → NotBad (x.run.run s).1 →
    ResRel (x.run.run s).1 (y.run.run g).1 ∧ R (x.run.run s).2.toCore (y.run.run g).2 ∧
    ∀ v, (x.run.run s).1 = .ok v →
      post v ∧ FrameS s.toCore (x.run.run s).2.toCore ∧ FrameL g.locals (y.run.run g).2.locals

/-- A guard leaves galaspec's locals as it found them (an S1 guard is an expression; a
    block in it pops its own scope). -/
def KeepsLocals (x : Spec.M Spec.Val) : Prop :=
  ∀ s v, (x.run.run s).1 = .ok v → (x.run.run s).2.locals = s.locals

/-! ### Frames -/

theorem FrameL.refl : ∀ (τ : List (GoCore.GVar × GoCore.GVal)), FrameL τ τ
  | [] => .nil
  | _ :: τ => .cons ⟨rfl, fun _ => rfl⟩ (FrameL.refl τ)

theorem FrameL.trans : ∀ {τ τ' τ'' : List (GoCore.GVar × GoCore.GVal)},
    FrameL τ τ' → FrameL τ' τ'' → FrameL τ τ''
  | _, _, _, .nil, .nil => .nil
  | (x, _) :: _, _, _, .cons ⟨h1, h2⟩ t, .cons ⟨h1', h2'⟩ t' =>
    .cons ⟨h1.trans h1', fun h => (h2 h).trans (h2' (by simp only [isTmpE] at h ⊢; rw [← h1]; exact h))⟩
      (FrameL.trans t t')

theorem FrameL.isTmp_eq : ∀ {τ τ' : List (GoCore.GVar × GoCore.GVal)}, FrameL τ τ' →
    (τ.filter (fun e => !isTmpE e)).length = (τ'.filter (fun e => !isTmpE e)).length
  | _, _, .nil => rfl
  | a :: _, b :: _, .cons ⟨h1, _⟩ t => by
    have := FrameL.isTmp_eq t
    have he : isTmpE a = isTmpE b := by simp only [isTmpE, h1]
    simp only [List.filter_cons, he]
    split <;> simp_all

theorem FrameL.lookup_tmp : ∀ {τ τ' : List (GoCore.GVar × GoCore.GVal)}, FrameL τ τ' →
    ∀ k, τ'.lookup (.tmp k) = τ.lookup (.tmp k)
  | _, _, .nil, _ => rfl
  | (x, w) :: _, (x', w') :: _, .cons ⟨h1, h2⟩ t, k => by
    simp only at h1; subst h1
    simp only [List.lookup]
    cases x with
    | tmp j =>
      have : w = w' := h2 rfl
      subst this
      split <;> simp_all [FrameL.lookup_tmp t k]
    | user y =>
      have : (GoCore.GVar.tmp k == GoCore.GVar.user y) = false := rfl
      simp only [this]; exact FrameL.lookup_tmp t k

theorem lookup_isSome_names : ∀ {ρ ρ' : Spec.Env}, ρ'.map (·.1) = ρ.map (·.1) →
    ∀ x, (Spec.lookup ρ' x).isSome = (Spec.lookup ρ x).isSome
  | [], [], _, _ => rfl
  | (k, _) :: ρ, (k', _) :: ρ', h, x => by
    simp only [List.map_cons, List.cons.injEq] at h
    obtain ⟨hk', h⟩ := h
    simp only [Spec.lookup, hk']
    by_cases hk : k = x
    · simp [hk]
    · simp only [hk, ite_false]; exact lookup_isSome_names h x
  | [], _ :: _, h, _ | _ :: _, [], h, _ => by simp at h

theorem orElse_isSome (a b : Option α) : (a <|> b).isSome = (a.isSome || b.isSome) := by
  cases a <;> rfl

theorem ScopeOk.of_names {sc : String → Bool} {c c' : Spec.Core} (h : ScopeOk sc c)
    (hl : c'.locals.map (·.1) = c.locals.map (·.1))
    (hg : c'.globals.map (·.1) = c.globals.map (·.1)) : ScopeOk sc c' := by
  intro x
  rw [h x]
  simp only [Spec.lookupVar, orElse_isSome]
  rw [lookup_isSome_names hl, lookup_isSome_names hg]

/-- The scope of an arm: the match's, and the pattern's variables but `_`. -/
def armScope (inScope : String → Bool) (ns : List String) (x : String) : Bool :=
  inScope x || (x != "_" && ns.contains x)

theorem lookup_pushed_isSome : ∀ (bsv : List (String × Spec.Val)) (ρ : Spec.Env) (x : String),
    (Spec.lookup (Spec.pushed bsv ρ) x).isSome =
      ((Spec.lookup ρ x).isSome || (x != "_" && (bsv.map (·.1)).contains x))
  | [], ρ, x => by simp [Spec.pushed]
  | (y, v) :: bsv, ρ, x => by
    rw [pushed_cons, lookup_pushed_isSome bsv]
    by_cases hy : y = "_"
    · subst hy
      by_cases hx : x = "_"
      · simp_all [Spec.bind]
      · have h' : (x == "_") = false := by simpa using hx
        simp [Spec.bind, h']
    · simp only [Spec.bind, hy, ite_false, Spec.lookup]
      by_cases hyx : y = x
      · subst hyx; simp [hy]
      · simp only [hyx, ite_false, List.map_cons, List.contains_cons]
        have : (x == y) = false := by simp [Ne.symm hyx]
        simp [this]

theorem ScopeOk.pushed {sc : String → Bool} {c : Spec.Core} (h : ScopeOk sc c)
    (bsv : List (String × Spec.Val)) :
    ScopeOk (armScope sc (bsv.map (·.1))) { c with locals := Spec.pushed bsv c.locals } := by
  intro x
  simp only [armScope, h x, Spec.lookupVar, orElse_isSome, lookup_pushed_isSome]
  cases (Spec.lookup c.locals x).isSome <;> cases (Spec.lookup c.globals x).isSome <;> simp

/-- **Leaving the arm's scope.** Related states, both with an arm's names on top of the
    stacks they had (galaspec's `A` over `ρ`, GoCore's over `τb`), after a part that kept
    its frame: dropping the arm's names on both sides leaves related states, and GoCore's
    lower stack is a frame of `τb`. -/
theorem pop_rel {c c' : Spec.Core} {g' : GoCore.GSt} {A ρ : Spec.Env}
    {Zg τb : List (GoCore.GVar × GoCore.GVal)}
    (hR : R c' g') (hF : c'.locals.map (·.1) = c.locals.map (·.1)) (hFL : FrameL (Zg ++ τb) g'.locals)
    (hc : c.locals = A ++ ρ) (hZ : (Zg.filter (fun e => !isTmpE e)).length = A.length) :
    ∃ ρ' Z bot, Spec.exitScope c'.locals ρ.length = ρ' ∧ ρ'.map (·.1) = ρ.map (·.1) ∧
      g'.locals = Z ++ bot ∧ FrameL τb bot ∧
      R { c' with locals := ρ' } { g' with locals := bot } := by
  obtain ⟨Z, bot, hg', hZ', hbot⟩ := fall2_split_left hFL
  have hl : c'.locals.length = A.length + ρ.length := by
    have := congrArg List.length hF; simp [hc] at this; omega
  refine ⟨c'.locals.drop A.length, Z, bot, ?_, ?_, hg', hbot, ⟨hR.1, ?_, hR.2.2⟩⟩
  · simp [Spec.exitScope, hl]
  · simp only [List.map_drop, hF, hc, List.map_append]; simp
  · have h := hR.2.1
    rw [hg', ← List.take_append_drop A.length c'.locals] at h
    refine LocRel.drop_top _ Z _ _ h ?_
    rw [← FrameL.isTmp_eq hZ', hZ]; simp; omega

/-! ### galaspec's side of an arm -/

/-- `t` with the locals above depth `n` dropped (`popTo n`). -/
def popped (t : Spec.St) (n : Nat) : Spec.St :=
  ⟨{ t.toCore with locals := Spec.exitScope t.locals n }, t.steps⟩

/-- The state an arm's guard or body starts in: the pattern's variables pushed. -/
def withBinds (s : Spec.St) (bs : List (String × Spec.Val)) : Spec.St :=
  ⟨{ s.toCore with locals := Spec.pushed bs s.locals }, s.steps⟩

theorem evalCases_err (ds : Spec.Decls) (d : Nat) (v : Spec.Val) (p : Spec.Pat) (g : Option Spec.Expr)
    (b : Spec.Expr) (rest : List (Spec.Pat × Option Spec.Expr × Spec.Expr)) (s : Spec.St) (e : Spec.Stop)
    (hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .error e) :
    (Spec.evalCases ds d v ((p, g, b) :: rest)).run.run s = (.error e, s) := by
  rw [Spec.evalCases.eq_2]
  simp only [Spec.run_bind_eq, Spec.run_liftM, Spec.run_getCore]
  rw [Spec.matchPat_run, hm]

theorem evalCases_noguard (ds : Spec.Decls) (d : Nat) (v : Spec.Val) (p : Spec.Pat) (b : Spec.Expr)
    (rest : List (Spec.Pat × Option Spec.Expr × Spec.Expr)) (s : Spec.St) (bs : List (String × Spec.Val))
    (hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs)) :
    (Spec.evalCases ds d v ((p, none, b) :: rest)).run.run s =
      match (Spec.eval ds d b).run.run (withBinds s bs) with
      | (.ok r, t) => (.ok r, popped t s.locals.length)
      | (.error e, t) => (.error e, t) := by
  rw [Spec.evalCases.eq_2]
  simp only [Spec.run_bind_eq, Spec.run_liftM, Spec.run_getCore]
  rw [Spec.matchPat_run, hm]
  simp only [Spec.run_bind_eq, Spec.run_liftM, Spec.pushAll_run]
  rw [show ∀ st : Spec.St, (pure true : Spec.M Bool).run.run st = (.ok true, st) from fun _ => rfl]
  simp only [ite_true, Spec.run_bind_eq]
  rcases h : (Spec.eval ds d b).run.run (withBinds s bs) with ⟨r, t⟩
  simp only [withBinds] at h
  rw [h]
  cases r <;> rfl

theorem evalCases_guard (ds : Spec.Decls) (d : Nat) (v : Spec.Val) (p : Spec.Pat) (ge b : Spec.Expr)
    (rest : List (Spec.Pat × Option Spec.Expr × Spec.Expr)) (s : Spec.St) (bs : List (String × Spec.Val))
    (hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok (some bs)) :
    (Spec.evalCases ds d v ((p, some ge, b) :: rest)).run.run s =
      match (Spec.eval ds d ge).run.run (withBinds s bs) with
      | (.error e, t) => (.error e, t)
      | (.ok (.bool true), t) => match (Spec.eval ds d b).run.run t with
        | (.ok r, t') => (.ok r, popped t' s.locals.length)
        | (.error e, t') => (.error e, t')
      | (.ok (.bool false), t) => (Spec.evalCases ds d v rest).run.run (popped t s.locals.length)
      | (.ok gv, t) => (.error (.stuck s!"condition is not a bool: {repr gv}"), t) := by
  rw [Spec.evalCases.eq_2]
  simp only [Spec.run_bind_eq, Spec.run_liftM, Spec.run_getCore]
  rw [Spec.matchPat_run, hm]
  simp only [Spec.run_bind_eq, Spec.run_liftM, Spec.pushAll_run]
  rcases h : (Spec.eval ds d ge).run.run (withBinds s bs) with ⟨r, t⟩
  simp only [withBinds] at h
  rw [h]
  rcases r with e | gv
  · rfl
  · simp only
    cases gv with
    | bool bb =>
      cases bb
      · rfl
      · simp only [Spec.asBool_run, ite_true, Spec.run_bind_eq]
        rcases (Spec.eval ds d b).run.run t with ⟨r', t'⟩
        cases r' <;> rfl
    | _ => rfl

/-! ### An arm's body -/

/-- What running (the rest of) the arms gives, compared: on a value, GoCore's `return`s
    its encoding, and the states are related once GoCore's stack is cut back to a frame of
    `τb` (the stack the arms started on); on a panic, a panic of the same class. -/
def ArmsRes (s0 : Spec.St) (τb : List (GoCore.GVar × GoCore.GVal))
    (gs : Except Spec.Stop Spec.Val × Spec.St) (go : Except GoCore.GStop Unit × GoCore.GSt) : Prop :=
  match gs.1 with
  | .ok w => go.1 = .error (.ret (enc w)) ∧
      (∃ Z bot, go.2.locals = Z ++ bot ∧ FrameL τb bot ∧ R gs.2.toCore { go.2 with locals := bot }) ∧
      FrameS s0.toCore gs.2.toCore
  | .error (.panic c _) => (∃ m, go.1 = .error (.panic (pcOf c) m)) ∧ R gs.2.toCore go.2
  | .error _ => True

theorem gexec_ret (X : Nat → GoCore.GM GoCore.GVal) (e : GoCore.GExpr) (g : GoCore.GSt) :
    (GoCore.gexec X (.ret e)).run.run g = match (GoCore.geval X e).run.run g with
      | (.ok w, t) => (.error (.ret w), t)
      | (.error e, t) => (.error e, t) := by
  rw [GoCore.gexec, grun_bind]
  rcases (GoCore.geval X e).run.run g with ⟨r, t⟩
  cases r <;> rfl

/-- **An arm's body.** From galaspec's state `t0` (the arm's variables `bsv` pushed on `ρ`)
    and GoCore's `gB` (whose temporaries from `n` hold their values `ws`): binding the
    variables and running the lowered body, then leaving the arm's scope, gives what
    galaspec's body and `popTo` give. -/
theorem arm_body (X : Nat → GoCore.GM GoCore.GVal) (P : Spec.Core → Prop) (b : Spec.M Spec.Val)
    (b' : GoCore.GExpr) (hb : Part P (fun _ => True) b (GoCore.geval X b'))
    (bs : List (String × GoCore.GExpr)) (bsv : List (String × Spec.Val)) (ws : List GoCore.GVal)
    (n : Nat) (s0 t0 : Spec.St) (gB : GoCore.GSt)
    (hf1 : Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
      xe.1 = xv.1 ∧ ∀ n, xv.2 ≠ .ref n) bs bsv)
    (hf2 : Spec.Fall2 (fun w (xv : String × Spec.Val) => w = enc xv.2) ws bsv)
    (hlk : ∀ j (hj : j < ws.length), gB.locals.lookup (.tmp (n + j)) = some ws[j])
    (ht0 : t0.locals = Spec.pushed bsv s0.locals)
    (hglob : t0.globals.map (·.1) = s0.globals.map (·.1))
    (hR : R { t0.toCore with locals := s0.locals } gB) (hP : P t0.toCore)
    (hnb : NotBad (match b.run.run t0 with
      | (.ok r, t) => ((.ok r : Except Spec.Stop Spec.Val), popped t s0.locals.length)
      | (.error e, t) => (.error e, t)).1) :
    ArmsRes s0 gB.locals
      (match b.run.run t0 with
        | (.ok r, t) => (.ok r, popped t s0.locals.length)
        | (.error e, t) => (.error e, t))
      ((GoCore.gexecs X (bindUser n bs ++ [.ret b'])).run.run gB) := by
  have hl : ws.length = bs.length := by rw [Spec.fall2_length hf2, Spec.fall2_length hf1]
  obtain ⟨A, U, hA, hU, hAU, hUt, hrel⟩ := pushed_userTop (heap := t0.heap) bs bsv ws s0.locals gB.locals hf1 hf2
  rw [gexecs_append, bindUser_run X bs ws n gB hl hlk]
  simp only
  rw [gexecs_cons, gexec_ret]
  let gb : GoCore.GSt := { gB with locals := userTop bs ws gB.locals }
  have hRb : R t0.toCore gb := by
    refine ⟨hR.1, ?_, hR.2.2⟩
    show LocRel t0.heap t0.locals (userTop bs ws gB.locals)
    rw [ht0, hA, hU]
    exact hrel _ _ hR.2.1
  have hsim := hb.sim t0 gb hRb hP
  rcases hbr : b.run.run t0 with ⟨r, t⟩
  rw [hbr] at hsim hnb
  rcases r with e | w
  · -- The body stopped: only a panic is claimed.
    cases e with
    | panic cl msg =>
      obtain ⟨hres, hRt, _⟩ := hsim trivial
      rcases hy : (GoCore.geval X b').run.run gb with ⟨r', g'⟩
      rw [hy] at hres hRt
      cases r' with
      | ok => simp [ResRel] at hres
      | error e' =>
        cases e' with
        | panic cl' m' =>
          simp only [ResRel] at hres
          exact ⟨⟨m', by rw [hres]⟩, hRt⟩
        | _ => simp [ResRel] at hres
    | _ => simp [NotBad] at hnb
  · obtain ⟨hres, hRt, hfr⟩ := hsim trivial
    obtain ⟨_, hFS, hFL⟩ := hfr w rfl
    rcases hy : (GoCore.geval X b').run.run gb with ⟨r', g'⟩
    rw [hy] at hres hRt hFL
    cases r' with
    | error => simp [ResRel] at hres
    | ok w' =>
      simp only [ResRel] at hres
      subst hres
      refine ⟨rfl, ?_, ?_⟩
      · have hgb : gb.locals = U ++ gB.locals := hU
        rw [hgb] at hFL
        have hc : (Spec.Core.locals { t0.toCore with locals := A ++ s0.locals }) = A ++ s0.locals := rfl
        obtain ⟨ρ', Z, bot, hρ', _, hg', hbot, hRp⟩ :=
          pop_rel (c := { t0.toCore with locals := A ++ s0.locals }) hRt
            (by rw [hFS.1]; simp [ht0, hA]) hFL hc
            (by rw [List.filter_eq_self.mpr (fun e he => by simp [hUt e he])]; exact hAU.symm)
        refine ⟨Z, bot, hg', hbot, ?_⟩
        simp only [popped]
        rw [hρ']
        exact hRp
      · refine ⟨?_, ?_⟩
        · simp only [popped, Spec.exitScope]
          have hl' : t.locals.length = A.length + s0.locals.length := by
            have := congrArg List.length hFS.1; simp [ht0, hA] at this; omega
          simp only [hl', List.map_drop]
          rw [hFS.1, ht0, hA]; simp
        · simp only [popped]; rw [hFS.2]; exact hglob

/-! ### GoCore's side of an arm -/

theorem block_run (X : Nat → GoCore.GM GoCore.GVal) (ss : List GoCore.GStmt) (g : GoCore.GSt) :
    (GoCore.gexecs X [.block ss]).run.run g = match (GoCore.gexecs X ss).run.run g with
      | (.ok _, t) => (.ok (), GoCore.truncate t g.locals.length)
      | (.error e, t) => (.error e, t) := by
  rw [gexecs_cons, GoCore.gexec, withScope_run]
  rcases (GoCore.gexecs X ss).run.run g with ⟨r, t⟩
  cases r
  · rfl
  · simp only; rw [gexecs_nil]

theorem ifElse_run (X : Nat → GoCore.GM GoCore.GVal) (c : GoCore.GExpr) (tb fb : List GoCore.GStmt)
    (g : GoCore.GSt) (b : Bool) (hc : (GoCore.geval X c).run.run g = (.ok (.bool b), g)) :
    (GoCore.gexec X (.ifElse c tb fb)).run.run g =
      (GoCore.withScope (GoCore.gexecs X (if b then tb else fb))).run.run g := by
  rw [GoCore.gexec, grun_bind, hc]
  simp only [grun_bind, grun_asBool]
  cases b <;> rfl

theorem truncate_append (g : GoCore.GSt) (Z τ : List (GoCore.GVar × GoCore.GVal)) (h : g.locals = Z ++ τ) :
    GoCore.truncate g τ.length = { g with locals := τ } := by
  simp [GoCore.truncate, h]

theorem update_past (x : GoCore.GVar) (w v : GoCore.GVal) :
    ∀ (U rest : List (GoCore.GVar × GoCore.GVal)), (∀ e ∈ U, e.1 ≠ x) →
      GoCore.update (U ++ (x, w) :: rest) x v = some (U ++ (x, v) :: rest)
  | [], rest, _ => by simp [GoCore.update]
  | (k, w') :: U, rest, h => by
    have hk : k ≠ x := h (k, w') (by simp)
    simp only [List.cons_append, GoCore.update, hk, ite_false]
    rw [update_past x w v U rest fun e he => h e (by simp [he])]
    rfl

theorem LocRel.set_tmp {heap : Array Spec.Val} {ρ : Spec.Env} {τ : List (GoCore.GVar × GoCore.GVal)}
    {k : Nat} {w : GoCore.GVal} (h : LocRel heap ρ ((.tmp k, w) :: τ)) (w' : GoCore.GVal) :
    LocRel heap ρ ((.tmp k, w') :: τ) := by
  cases h with
  | tmp h => exact .tmp h

/-- Every entry of a frame of an all-temporaries stack is a temporary. -/
theorem FrameL.tmps {T T' : List (GoCore.GVar × GoCore.GVal)} (h : FrameL T T')
    (hT : ∀ e ∈ T, isTmpE e = true) : ∀ e ∈ T', isTmpE e = true := by
  induction h with
  | nil => simp
  | cons hab _ ih =>
    intro e he
    simp only [List.mem_cons] at he
    rcases he with rfl | he
    · rename_i a _ _ _
      have := hT a (by simp); simp only [isTmpE] at this ⊢; rw [← hab.1]; exact this
    · exact ih (fun e he => hT e (by simp [he])) e he

theorem FrameL.mem {τ τ' : List (GoCore.GVar × GoCore.GVal)} (h : FrameL τ τ') :
    ∀ e' ∈ τ', ∃ e ∈ τ, e.1 = e'.1 := by
  induction h with
  | nil => simp
  | cons hab _ ih =>
    intro e' he'
    simp only [List.mem_cons] at he'
    rcases he' with rfl | he'
    · exact ⟨_, by simp, hab.1⟩
    · obtain ⟨e, he, h⟩ := ih e' he'; exact ⟨e, by simp [he], h⟩

theorem filter_users_tmps(T : List (GoCore.GVar × GoCore.GVal)) (hT : ∀ e ∈ T, isTmpE e = true) :
    (T.filter (fun e => !isTmpE e)).length = 0 := by
  simp only [List.length_eq_zero_iff, List.filter_eq_nil_iff]
  intro e he; simp [hT e he]

theorem ArmsRes.err {s0 τb gs go} (h : ArmsRes s0 τb gs go) (hnb : NotBad gs.1) :
    ∃ e, go.1 = .error e := by
  unfold ArmsRes at h
  rcases hg : gs.1 with e | w
  · rw [hg] at h hnb
    cases e with
    | panic => obtain ⟨⟨m, hm⟩, _⟩ := h; exact ⟨_, hm⟩
    | _ => simp [NotBad] at hnb
  · rw [hg] at h; exact ⟨_, h.1⟩

/-- Weakening a result to an outer starting point: GoCore's stack had temporaries `T`
    on top of a frame of `τ0`, and galaspec's state had the names of `s0`. -/
theorem ArmsRes.weaken {s0 s1 : Spec.St} {τ0 τ' T : List (GoCore.GVar × GoCore.GVal)} {gs go}
    (h : ArmsRes s1 (T ++ τ') gs go) (hT : ∀ e ∈ T, isTmpE e = true) (hF : FrameL τ0 τ')
    (hS : FrameS s0.toCore s1.toCore) : ArmsRes s0 τ0 gs go := by
  unfold ArmsRes at h ⊢
  split
  · rename_i w hw
    rw [hw] at h
    obtain ⟨hret, ⟨Z, bot, hz, hfb, hR⟩, hFS⟩ := h
    obtain ⟨T2, bot', rfl, hT2, hbot'⟩ := fall2_split_left hfb
    refine ⟨hret, ⟨Z ++ T2, bot', by simp [hz], hF.trans hbot', ⟨hR.1, ?_, hR.2.2⟩⟩, ?_⟩
    · exact LocRel.drop_top [] T2 _ bot' hR.2.1
        (by rw [← FrameL.isTmp_eq hT2, filter_users_tmps T hT]; rfl)
    · exact ⟨hFS.1.trans hS.1, hFS.2.trans hS.2⟩
  · rename_i c m hw
    rw [hw] at h; exact h
  · rename_i e hw hne
    trivial

theorem FrameS.refl (c : Spec.Core) : FrameS c c := ⟨rfl, rfl⟩

/-! ### The arms -/

/-- What M3 takes as given about an arm: its guard (if any) and its body are lowered
    correctly (`Part`) in the arm's scope, and the guard leaves galaspec's locals as it
    found them. -/
def ArmOk (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (a : Arm) : Prop :=
  let P := ScopeOk (armScope inScope ((lowerPat ds inScope t a.pat (.var (.tmp 0))).2.map (·.1)))
  (∀ ge g', a.guard = some (ge, g') →
    Part P (fun _ => True) (Spec.eval ds d ge) (GoCore.geval X g') ∧ KeepsLocals (Spec.eval ds d ge)) ∧
  Part P (fun _ => True) (Spec.eval ds d a.body.1) (GoCore.geval X a.body.2)

/-- The claim for the arms from `arms` on, temporaries numbered from `n`. -/
def ArmsOkFrom (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (v : Spec.Val) (arms : List Arm) : Prop :=
  ∀ (n : Nat) (s : Spec.St) (g : GoCore.GSt), 1 ≤ n → Good s.toCore v g → ScopeOk inScope s.toCore →
    NotBad ((Spec.evalCases ds d v (arms.map Arm.src)).run.run s).1 →
    ArmsRes s g.locals ((Spec.evalCases ds d v (arms.map Arm.src)).run.run s)
      ((GoCore.gexecs X (lowerArms ds inScope t n arms)).run.run g)

theorem names_eq {bs : List (String × GoCore.GExpr)} {bsv : List (String × Spec.Val)}
    (h : Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
      xe.1 = xv.1 ∧ peval look xe.2 = some (enc xv.2) ∧ ∀ n, xv.2 ≠ .ref n) bs bsv) :
    bs.map (·.1) = bsv.map (·.1) := by
  induction h with
  | nil => rfl
  | cons hab _ ih => simp [hab.1, ih]

/-- The common start of an arm: what the pattern lemma gives in every state the arm's
    GoCore code tests the pattern in, and the temporaries declared. -/
theorem arm_start (ds : Spec.Decls) (inScope : String → Bool) (t : STy) (X : Nat → GoCore.GM GoCore.GVal)
    (v : Spec.Val) (hv : HasTy ds t v) (p : Spec.Pat) (n : Nat) (hn : 1 ≤ n) (s : Spec.St)
    (g : GoCore.GSt) (hgood : Good s.toCore v g) (hS : ScopeOk inScope s.toCore)
    (r : Option (List (String × Spec.Val)))
    (hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 = .ok r) :
    let lp := lowerPat ds inScope t p (.var (.tmp 0))
    (∀ g', Good s.toCore v g' → PatOk (GoCore.lookupVar g') lp r) ∧
    ∃ ws : List GoCore.GVal, ws.length = lp.2.length ∧
      (GoCore.gexecs X (defsOf n lp.2)).run.run g = (.ok (), { g with locals := pushTemps n ws g.locals }) ∧
      ∀ bsv, r = some bsv → Spec.Fall2 (fun w (xv : String × Spec.Val) => w = enc xv.2) ws bsv := by
  intro lp
  have hpok : ∀ g', Good s.toCore v g' → PatOk (GoCore.lookupVar g') lp r := fun g' hg' =>
    lowerPat_correct ds inScope s.toCore _ (R_reads hg'.1) hS t p _ v r hv
      (by simp only [peval]; exact hg'.2) hm
  have htot : ∀ g', Good s.toCore v g' → ∀ xe ∈ lp.2, (peval (GoCore.lookupVar g') xe.2).isSome :=
    fun g' hg' => lowerPat_total ds inScope _ t p _ (by simp [peval, hg'.2])
  obtain ⟨ws, hwl, hdefs, hvals⟩ := defsOf_run X s.toCore v lp r hpok htot lp.2 [] (by simp) n hn g hgood
  exact ⟨hpok, ws, hwl, hdefs, fun bsv h => by simpa using hvals bsv h⟩

/-- Lifting an error out of an arm's block, past its declarations. -/
theorem block_err (X : Nat → GoCore.GM GoCore.GVal) (defs tail : List GoCore.GStmt) (g gd : GoCore.GSt)
    (hdefs : (GoCore.gexecs X defs).run.run g = (.ok (), gd)) (e : GoCore.GStop) (t : GoCore.GSt)
    (h : (GoCore.gexecs X tail).run.run gd = (.error e, t)) :
    (GoCore.gexecs X [.block (defs ++ tail)]).run.run g = (.error e, t) := by
  rw [block_run, gexecs_append, hdefs]; simp only; rw [h]

/-- Lifting an error out of a branch. -/
theorem branch_err (X : Nat → GoCore.GM GoCore.GVal) (s : GoCore.GStmt) (ss : List GoCore.GStmt)
    (g : GoCore.GSt) (e : GoCore.GStop) (t : GoCore.GSt)
    (h : (GoCore.gexec X s).run.run g = (.error e, t)) :
    (GoCore.gexecs X (s :: ss)).run.run g = (.error e, t) := by
  rw [gexecs_cons, h]

theorem withScope_err (m : GoCore.GM α) (g : GoCore.GSt) (e : GoCore.GStop) (t : GoCore.GSt)
    (h : m.run.run g = (.error e, t)) : (GoCore.withScope m).run.run g = (.error e, t) := by
  rw [withScope_run, h]

theorem good_push_eq {g : GoCore.GSt} (n : Nat) (ws : List GoCore.GVal) :
    ∃ T, pushTemps n ws g.locals = T ++ g.locals ∧ ∀ e ∈ T, isTmpE e = true := by
  obtain ⟨T, hT, _, hk⟩ := pushTemps_eq n ws g.locals
  exact ⟨T, hT, fun e he => by obtain ⟨k, _, _, h3⟩ := hk e he; simp [isTmpE, h3]⟩

/-- **An arm without a guard.** -/
theorem arm_noguard (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (v : Spec.Val) (hv : HasTy ds t v) (p : Spec.Pat)
    (b : Spec.Expr) (b' : GoCore.GExpr) (rest : List Arm)
    (hb : Part (ScopeOk (armScope inScope ((lowerPat ds inScope t p (.var (.tmp 0))).2.map (·.1))))
      (fun _ => True) (Spec.eval ds d b) (GoCore.geval X b'))
    (ih : ArmsOkFrom ds d inScope t X v rest) :
    ArmsOkFrom ds d inScope t X v (⟨p, none, (b, b')⟩ :: rest) := by
  intro n s g hn hgood hS hnb
  simp only [List.map_cons, Arm.src, Option.map_none] at hnb ⊢
  rcases hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 with e | r
  · rw [evalCases_err ds d v p none b _ s e hm] at hnb ⊢
    unfold ArmsRes
    cases e with
    | panic cl msg => exact absurd hm (matchPat_noPanic ds s.locals s.globals p v s.toCore cl msg)
    | _ => trivial
  obtain ⟨hpok, ws, hwl, hdefs, hvals⟩ := arm_start ds inScope t X v hv p n hn s g hgood hS r hm
  let lp := lowerPat ds inScope t p (.var (.tmp 0))
  let gd : GoCore.GSt := { g with locals := pushTemps n ws g.locals }
  have hgd : Good s.toCore v gd := hgood.push n hn ws
  have hcond : (GoCore.geval X lp.1).run.run gd = (.ok (.bool r.isSome), gd) :=
    geval_peval X gd _ _ (hpok gd hgd).1
  obtain ⟨T, hT, hTt⟩ := good_push_eq (g := g) n ws
  have hlow : lowerArms ds inScope t n (⟨p, none, (b, b')⟩ :: rest) =
      [.block (defsOf n lp.2 ++ [.ifElse lp.1 (bindUser n lp.2 ++ [.ret b'])
        (lowerArms ds inScope t (n + lp.2.length + 1) rest)])] := rfl
  rw [hlow]
  cases r with
  | none =>
    rw [Spec.evalCases_nomatch ds d v p none b _ s hm] at hnb ⊢
    have h := ih (n + lp.2.length + 1) s gd (by omega) hgd hS hnb
    obtain ⟨e, he⟩ := h.err hnb
    rcases hgo : (GoCore.gexecs X (lowerArms ds inScope t (n + lp.2.length + 1) rest)).run.run gd
      with ⟨r', t'⟩
    rw [hgo] at h he
    simp only at he; subst he
    rw [block_err X _ _ g gd hdefs e t' (branch_err X _ _ gd e t' (by
      rw [ifElse_run X _ _ _ gd false hcond]; exact withScope_err _ _ _ _ hgo))]
    have h' := h
    change ArmsRes s (pushTemps n ws g.locals) _ _ at h'
    rw [hT] at h'
    exact h'.weaken hTt (FrameL.refl _) (FrameS.refl _)
  | some bsv =>
    rw [evalCases_noguard ds d v p b _ s bsv hm] at hnb ⊢
    have hok := (hpok gd hgd).2 bsv rfl
    have hf1 : Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
        xe.1 = xv.1 ∧ ∀ n, xv.2 ≠ .ref n) lp.2 bsv := Spec.fall2_mono (fun _ _ h => ⟨h.1, h.2.2⟩) hok
    have hP : ScopeOk (armScope inScope (lp.2.map (·.1))) (withBinds s bsv).toCore := by
      rw [names_eq hok]; exact hS.pushed bsv
    have h := arm_body X _ _ b' hb lp.2 bsv ws n s (withBinds s bsv) gd hf1 (hvals bsv rfl)
      (fun j hj => pushTemps_lookup n ws g.locals j hj) rfl rfl hgd.1 hP hnb
    obtain ⟨e, he⟩ := h.err hnb
    rcases hgo : (GoCore.gexecs X (bindUser n lp.2 ++ [.ret b'])).run.run gd with ⟨r', t'⟩
    rw [hgo] at h he
    simp only at he; subst he
    rw [block_err X _ _ g gd hdefs e t' (branch_err X _ _ gd e t' (by
      rw [ifElse_run X _ _ _ gd true hcond]; exact withScope_err _ _ _ _ hgo))]
    have h' := h
    change ArmsRes s (pushTemps n ws g.locals) _ _ at h'
    rw [hT] at h'
    exact h'.weaken hTt (FrameL.refl _) (FrameS.refl _)

theorem lookupVar_tmp (g : GoCore.GSt) (k : Nat) : GoCore.lookupVar g (.tmp k) = g.locals.lookup (.tmp k) := by
  cases h : g.locals.lookup (.tmp k) <;> simp [GoCore.lookupVar, h]

theorem lookup_tmp_skip (k j : Nat) (w : GoCore.GVal) (τ : List (GoCore.GVar × GoCore.GVal)) (h : k ≠ j) :
    ((GoCore.GVar.tmp k, w) :: τ).lookup (.tmp j) = τ.lookup (.tmp j) := by
  have : (GoCore.GVar.tmp j == GoCore.GVar.tmp k) = false := by simp [Ne.symm h]
  simp [List.lookup, this]

/-- **An arm with a guard.** The guard runs in a block of its own with the arm's variables
    bound, into a flag; the variables are bound again for the body. -/
theorem arm_guard (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (v : Spec.Val) (hv : HasTy ds t v) (p : Spec.Pat)
    (ge : Spec.Expr) (g' : GoCore.GExpr) (b : Spec.Expr) (b' : GoCore.GExpr) (rest : List Arm)
    (hg : Part (ScopeOk (armScope inScope ((lowerPat ds inScope t p (.var (.tmp 0))).2.map (·.1))))
      (fun _ => True) (Spec.eval ds d ge) (GoCore.geval X g'))
    (hk : KeepsLocals (Spec.eval ds d ge))
    (hb : Part (ScopeOk (armScope inScope ((lowerPat ds inScope t p (.var (.tmp 0))).2.map (·.1))))
      (fun _ => True) (Spec.eval ds d b) (GoCore.geval X b'))
    (ih : ArmsOkFrom ds d inScope t X v rest) :
    ArmsOkFrom ds d inScope t X v (⟨p, some (ge, g'), (b, b')⟩ :: rest) := by
  intro n s g hn hgood hS hnb
  simp only [List.map_cons, Arm.src, Option.map_some] at hnb ⊢
  rcases hm : ((Spec.matchPat ds s.locals s.globals p v).run.run s.toCore).1 with e | r
  · rw [evalCases_err ds d v p (some ge) b _ s e hm] at hnb ⊢
    unfold ArmsRes
    cases e with
    | panic cl msg => exact absurd hm (matchPat_noPanic ds s.locals s.globals p v s.toCore cl msg)
    | _ => trivial
  obtain ⟨hpok, ws, hwl, hdefs, hvals⟩ := arm_start ds inScope t X v hv p n hn s g hgood hS r hm
  let lp := lowerPat ds inScope t p (.var (.tmp 0))
  let ok := n + lp.2.length
  let gd : GoCore.GSt := { g with locals := pushTemps n ws g.locals }
  have hgd : Good s.toCore v gd := hgood.push n hn ws
  let gv : GoCore.GSt := { gd with locals := (.tmp ok, .bool false) :: gd.locals }
  have hgv : Good s.toCore v gv := hgd.push ok (by omega) [.bool false]
  have hcond : (GoCore.geval X lp.1).run.run gv = (.ok (.bool r.isSome), gv) :=
    geval_peval X gv _ _ (hpok gv hgv).1
  obtain ⟨T, hT, hTt⟩ := good_push_eq (g := g) n ws
  have hlow : lowerArms ds inScope t n (⟨p, some (ge, g'), (b, b')⟩ :: rest) =
      [.block (defsOf n lp.2 ++
        [.varDecl (.tmp ok) (.bool false),
         .ifElse lp.1 (bindUser n lp.2 ++ [.assign (.tmp ok) g']) [],
         .ifElse (.var (.tmp ok)) (bindUser n lp.2 ++ [.ret b'])
           (lowerArms ds inScope t (ok + 1) rest)])] := rfl
  rw [hlow]
  -- After the declarations and the flag, both cases test the flag and lift the result.
  have hstart : ∀ (e : GoCore.GStop) (tt : GoCore.GSt),
      (GoCore.gexecs X [.ifElse lp.1 (bindUser n lp.2 ++ [.assign (.tmp ok) g']) [],
         .ifElse (.var (.tmp ok)) (bindUser n lp.2 ++ [.ret b'])
           (lowerArms ds inScope t (ok + 1) rest)]).run.run gv = (.error e, tt) →
      (GoCore.gexecs X [.block (defsOf n lp.2 ++
        [.varDecl (.tmp ok) (.bool false),
         .ifElse lp.1 (bindUser n lp.2 ++ [.assign (.tmp ok) g']) [],
         .ifElse (.var (.tmp ok)) (bindUser n lp.2 ++ [.ret b'])
           (lowerArms ds inScope t (ok + 1) rest)])]).run.run g = (.error e, tt) := by
    intro e tt h
    refine block_err X _ _ g gd hdefs e tt ?_
    rw [gexecs_cons]
    exact h
  cases r with
  | none =>
    rw [Spec.evalCases_nomatch ds d v p (some ge) b _ s hm] at hnb ⊢
    have h := ih (ok + 1) s gv (by omega) hgv hS hnb
    obtain ⟨e, he⟩ := h.err hnb
    rcases hgo : (GoCore.gexecs X (lowerArms ds inScope t (ok + 1) rest)).run.run gv with ⟨r', t'⟩
    rw [hgo] at h he
    simp only at he; subst he
    rw [hstart e t' (by
      rw [gexecs_cons, ifElse_run X _ _ _ gv false hcond]
      simp only [Bool.false_eq_true, ite_false]
      rw [withScope_run, gexecs_nil]
      simp only
      rw [truncate_append gv [] gv.locals rfl]
      refine branch_err X _ _ _ e t' ?_
      rw [ifElse_run X _ _ _ _ false (geval_peval X _ (.var (.tmp ok)) _ (by simp [peval, lookupVar_tmp, gv, List.lookup]))]
      exact withScope_err _ _ _ _ hgo)]
    have h' := h
    change ArmsRes s ((.tmp ok, .bool false) :: pushTemps n ws g.locals) _ _ at h'
    rw [hT, ← List.cons_append] at h'
    exact h'.weaken (by
      intro e he; simp only [List.mem_cons] at he
      rcases he with rfl | he
      · rfl
      · exact hTt e he) (FrameL.refl _) (FrameS.refl _)
  | some bsv =>
    have hok := (hpok gv hgv).2 bsv rfl
    have hf1 : Spec.Fall2 (fun (xe : String × GoCore.GExpr) (xv : String × Spec.Val) =>
        xe.1 = xv.1 ∧ ∀ n, xv.2 ≠ .ref n) lp.2 bsv := Spec.fall2_mono (fun _ _ h => ⟨h.1, h.2.2⟩) hok
    have hf2 := hvals bsv rfl
    have hl : ws.length = lp.2.length := hwl
    -- The temporaries hold the variables' values, past the flag.
    have hlkv : ∀ j (hj : j < ws.length), gv.locals.lookup (.tmp (n + j)) = some ws[j] := by
      intro j hj
      rw [lookup_tmp_skip _ _ _ _ (by omega)]
      exact pushTemps_lookup n ws g.locals j hj
    obtain ⟨A, U, hA, hU, hAU, hUt, hrel⟩ :=
      pushed_userTop (heap := s.heap) lp.2 bsv ws s.locals gv.locals hf1 hf2
    let gb1 : GoCore.GSt := { gv with locals := userTop lp.2 ws gv.locals }

    have hRb1 : R (withBinds s bsv).toCore gb1 := by
      refine ⟨hgv.1.1, ?_, hgv.1.2.2⟩
      show LocRel s.heap (Spec.pushed bsv s.locals) (userTop lp.2 ws gv.locals)
      rw [hA, hU]; exact hrel _ _ hgv.1.2.1
    have hP1 : ScopeOk (armScope inScope (lp.2.map (·.1))) (withBinds s bsv).toCore := by
      rw [names_eq hok]; exact hS.pushed bsv
    have hev := evalCases_guard ds d v p ge b (rest.map Arm.src) s bsv hm
    rw [hev] at hnb ⊢
    have hsim := hg.sim (withBinds s bsv) gb1 hRb1 hP1
    rcases hgr : (Spec.eval ds d ge).run.run (withBinds s bsv) with ⟨rg, tg⟩
    rw [hgr] at hsim hnb
    -- The prefix up to the guard's call, common to every outcome.
    have hpre : ∀ (res : Except GoCore.GStop Unit × GoCore.GSt),
        (GoCore.gexecs X [.assign (.tmp ok) g']).run.run gb1 = res →
        (GoCore.gexecs X [.ifElse lp.1 (bindUser n lp.2 ++ [.assign (.tmp ok) g']) [],
           .ifElse (.var (.tmp ok)) (bindUser n lp.2 ++ [.ret b'])
             (lowerArms ds inScope t (ok + 1) rest)]).run.run gv =
        (match (GoCore.withScope (do GoCore.gexecs X (bindUser n lp.2 ++ [.assign (.tmp ok) g']))).run.run gv with
          | (.ok _, tt) => (GoCore.gexecs X [.ifElse (.var (.tmp ok)) (bindUser n lp.2 ++ [.ret b'])
             (lowerArms ds inScope t (ok + 1) rest)]).run.run tt
          | (.error e, t) => (.error e, t)) := by
      intro res _
      rw [gexecs_cons, ifElse_run X _ _ _ gv true hcond]
      rfl
    rcases rg with eg | gvv
    · -- The guard stopped.
      cases eg with
      | panic cl msg =>
        obtain ⟨hres, hRt, _⟩ := hsim trivial
        rcases hy : (GoCore.geval X g').run.run gb1 with ⟨ry, gy⟩
        rw [hy] at hres hRt
        cases ry with
        | ok => simp [ResRel] at hres
        | error ey =>
          cases ey with
          | panic cl' m' =>
            simp only [ResRel] at hres
            simp only
            rw [hstart (.panic cl' m') gy (by
              rw [hpre _ rfl, withScope_run, gexecs_append, bindUser_run X lp.2 ws n gv hl hlkv]
              simp only
              rw [gexecs_cons]
              simp only [GoCore.gexec, grun_bind]
              rw [hy])]
            unfold ArmsRes
            exact ⟨⟨m', by rw [hres]⟩, hRt⟩
          | _ => simp [ResRel] at hres
      | _ => simp [NotBad] at hnb
    · -- The guard gave a value: the flag is set, and the scope of its block left.
      obtain ⟨hres, hRt, hfr⟩ := hsim trivial
      obtain ⟨_, hFS, hFL⟩ := hfr gvv rfl
      have hkeep : tg.locals = Spec.pushed bsv s.locals := by
        have := hk (withBinds s bsv) gvv (by rw [hgr]); rw [hgr] at this; exact this
      rcases hy : (GoCore.geval X g').run.run gb1 with ⟨ry, gy⟩
      rw [hy] at hres hRt hFL
      cases ry with
      | error => simp [ResRel] at hres
      | ok wy =>
      simp only [ResRel] at hres
      subst hres
      -- GoCore's stack after the guard: the variables, the flag, the rest.
      have hgb1 : gb1.locals = U ++ (GoCore.GVar.tmp ok, GoCore.GVal.bool false) :: (T ++ g.locals) := by
        show userTop lp.2 ws gv.locals = _
        rw [hU]; show U ++ (GoCore.GVar.tmp ok, GoCore.GVal.bool false) :: pushTemps n ws g.locals = _
        rw [hT]
      rw [hgb1] at hFL
      obtain ⟨U', rest1, hgy, hUU, hrest1⟩ := fall2_split_left hFL
      cases hrest1 with
      | cons hok1 hR' =>
      rename_i e1 R'
      obtain ⟨he1, hv1⟩ := hok1
      have he1' : e1 = (.tmp ok, .bool false) := by
        obtain ⟨x1, w1⟩ := e1; simp only at he1 hv1; subst he1
        simp [hv1 rfl]
      subst he1'
      have hU'users : ∀ e ∈ U', e.1 ≠ .tmp ok := by
        intro e he h
        obtain ⟨e0, he0, hn0⟩ := FrameL.mem hUU e he
        have := hUt e0 he0
        simp only [isTmpE, hn0, h] at this
        exact absurd this (by decide)
      have hR'len : R'.length = (T ++ g.locals).length := (Spec.fall2_length hR').symm
      let gt : GoCore.GSt := { gy with locals := (.tmp ok, enc gvv) :: R' }
      -- Running up to the flag's test.
      have hupd : GoCore.updateVar gy (.tmp ok) (enc gvv) =
          some { gy with locals := U' ++ (.tmp ok, enc gvv) :: R' } := by
        unfold GoCore.updateVar
        rw [hgy, update_past _ _ _ U' R' hU'users]
      have hguardrun : (GoCore.withScope (GoCore.gexecs X (bindUser n lp.2 ++ [.assign (.tmp ok) g']))).run.run gv =
          (.ok (), gt) := by
        rw [withScope_run, gexecs_append, bindUser_run X lp.2 ws n gv hl hlkv]
        simp only
        rw [gexecs_cons]
        simp only [GoCore.gexec, grun_bind]
        have hy' : (GoCore.geval X g').run.run
            { locals := userTop lp.2 ws gv.locals, globals := gv.globals, out := gv.out } =
            (.ok (enc gvv), gy) := hy
        rw [hy']
        simp only [grun_get, hupd, grun_set, gexecs_nil]
        have hlen : gv.locals.length = ((GoCore.GVar.tmp ok, enc gvv) :: R').length := by
          simp [hR'len, gv, gd, hT]
        rw [hlen, truncate_append _ U' _ rfl]
      -- The states after the guard's block: related, galaspec's arm variables dropped.
      have hgtR : R { tg.toCore with locals := s.locals } gt := by
        refine ⟨hRt.1, ?_, hRt.2.2⟩
        have h1 := hRt.2.1
        rw [hkeep, hA, hgy] at h1
        exact (LocRel.drop_top A U' _ _ h1 (by
          rw [← FrameL.isTmp_eq hUU, List.filter_eq_self.mpr (fun e he => by simp [hUt e he])]
          exact hAU.symm)).set_tmp _
      have hlkt : ∀ k, k ≠ ok → gt.locals.lookup (.tmp k) = (T ++ g.locals).lookup (.tmp k) := by
        intro k hk
        show ((GoCore.GVar.tmp ok, enc gvv) :: R').lookup (.tmp k) = _
        rw [lookup_tmp_skip _ _ _ _ (Ne.symm hk)]
        exact FrameL.lookup_tmp hR' k
      obtain ⟨T2, R2, hR2, hTT2, hgR2⟩ := fall2_split_left hR'
      have hT0 : ∀ e ∈ (GoCore.GVar.tmp ok, enc gvv) :: T2, isTmpE e = true := by
        intro e he; simp only [List.mem_cons] at he
        rcases he with rfl | he
        · rfl
        · exact FrameL.tmps hTT2 hTt e he
      have hgtl : gt.locals = ((GoCore.GVar.tmp ok, enc gvv) :: T2) ++ R2 := by
        show (_ :: R') = _; rw [hR2]; rfl
      have hflag : (GoCore.geval X (.var (.tmp ok))).run.run gt = (.ok (enc gvv), gt) :=
        geval_peval X gt _ _ (by simp [peval, lookupVar_tmp, gt, List.lookup])
      have hpre' := hpre _ rfl
      rw [hguardrun] at hpre'
      simp only at hpre'
      cases gvv with
      | bool bb =>
        cases bb with
        | false =>
          -- The guard is false: on to the next arm.
          simp only at hnb ⊢
          let s' := popped tg s.locals.length
          have hs'l : s'.locals = s.locals := by
            show Spec.exitScope tg.locals s.locals.length = s.locals
            rw [hkeep]; exact Spec.exitScope_pushed bsv s.locals
          have hgood' : Good s'.toCore v gt := by
            refine ⟨?_, ?_⟩
            · have : s'.toCore = { tg.toCore with locals := s.locals } := by
                show { tg.toCore with locals := s'.locals } = _; rw [hs'l]
              rw [this]; exact hgtR
            · rw [lookupVar_tmp, hlkt 0 (by omega), ← hT]
              have := hgd.2; rw [lookupVar_tmp] at this; exact this
          have hS' : ScopeOk inScope s'.toCore :=
            hS.of_names (by show s'.locals.map _ = _; rw [hs'l]) hFS.2
          have h := ih (ok + 1) s' gt (by omega) hgood' hS' hnb
          obtain ⟨e, he⟩ := h.err hnb
          rcases hgo : (GoCore.gexecs X (lowerArms ds inScope t (ok + 1) rest)).run.run gt with ⟨r', t'⟩
          rw [hgo] at h he
          simp only at he; subst he
          rw [hstart e t' (by
            rw [hpre']
            refine branch_err X _ _ _ e t' ?_
            rw [ifElse_run X _ _ _ gt false hflag]
            exact withScope_err _ _ _ _ hgo)]
          rw [hgtl] at h
          exact h.weaken hT0 hgR2 ⟨by show s'.locals.map _ = _; rw [hs'l], hFS.2⟩
        | true =>
          -- The guard is true: the body, with the variables bound again.
          simp only at hnb ⊢
          have hP2 : ScopeOk (armScope inScope (lp.2.map (·.1))) tg.toCore := hP1.of_names hFS.1 hFS.2
          have h := arm_body X _ _ b' hb lp.2 bsv ws n s tg gt hf1 hf2
            (fun j hj => by rw [hlkt (n + j) (by omega), ← hT]; exact pushTemps_lookup n ws g.locals j hj)
            hkeep hFS.2 hgtR hP2 hnb
          obtain ⟨e, he⟩ := h.err hnb
          rcases hgo : (GoCore.gexecs X (bindUser n lp.2 ++ [.ret b'])).run.run gt with ⟨r', t'⟩
          rw [hgo] at h he
          simp only at he; subst he
          rw [hstart e t' (by
            rw [hpre']
            refine branch_err X _ _ _ e t' ?_
            rw [ifElse_run X _ _ _ gt true hflag]
            exact withScope_err _ _ _ _ hgo)]
          rw [hgtl] at h
          exact h.weaken hT0 hgR2 (FrameS.refl _)
      | _ => simp [NotBad] at hnb

/-- **The arms.** Every arm lowered correctly gives the claim for the whole list: the first
    arm whose pattern matches and whose guard holds runs, and with none, both panic. -/
theorem arms_correct (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (v : Spec.Val) (hv : HasTy ds t v) :
    ∀ (arms : List Arm), (∀ a ∈ arms, ArmOk ds d inScope t X a) → ArmsOkFrom ds d inScope t X v arms
  | [], _ => by
    intro n s g _ hgood _ _
    simp only [List.map_nil, Spec.evalCases_nil, lowerArms]
    rw [gexecs_cons]
    exact ⟨⟨_, rfl⟩, hgood.1⟩
  | ⟨p, none, (b, b')⟩ :: rest, h =>
    arm_noguard ds d inScope t X v hv p b b' rest (h _ (List.mem_cons_self ..)).2
      (arms_correct ds d inScope t X v hv rest fun a ha => h a (List.mem_cons_of_mem _ ha))
  | ⟨p, some (ge, g'), (b, b')⟩ :: rest, h => by
    obtain ⟨hgk, hb⟩ := h _ (List.mem_cons_self ..)
    obtain ⟨hg, hk⟩ := hgk ge g' rfl
    exact arm_guard ds d inScope t X v hv p ge g' b b' rest hg hk hb
      (arms_correct ds d inScope t X v hv rest fun a ha => h a (List.mem_cons_of_mem _ ha))

/-! ## The `match` -/

theorem grun_tryCatch (x : GoCore.GM α) (h : GoCore.GStop → GoCore.GM α) (s : GoCore.GSt) :
    (tryCatch x h).run.run s = match x.run.run s with
      | (.ok a, t) => (.ok a, t)
      | (.error e, t) => (h e).run.run t := by
  show (ExceptT.tryCatch x h).run.run s = _
  simp only [ExceptT.tryCatch, ExceptT.run_mk, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  erw [hx]
  cases r <;> rfl

/-- A function literal called at once: its `return` gives its value, and its scope is left. -/
theorem funcLit_run (X : Nat → GoCore.GM GoCore.GVal) (body : List GoCore.GStmt) (g : GoCore.GSt) :
    (GoCore.geval X (.funcLit body)).run.run g = match (GoCore.gexecs X body).run.run g with
      | (.ok _, t) => (.error (.stuck "function literal ended without a return"),
          GoCore.truncate t g.locals.length)
      | (.error (.ret v), t) => (.ok v, GoCore.truncate t g.locals.length)
      | (.error e, t) => (.error e, t) := by
  rw [GoCore.geval, grun_bind, withScope_run, grun_tryCatch, grun_bind]
  rcases (GoCore.gexecs X body).run.run g with ⟨r, t⟩
  rcases r with e | _
  · cases e <;> rfl
  · rfl

theorem tick_run (s : Spec.St) : Spec.tick.run.run s = match s.steps with
    | 0 => (.error .fuel, s)
    | n + 1 => (.ok (), { s with steps := n }) := by
  rcases s with ⟨c, n⟩
  cases n <;> rfl

theorem tick_succ (s : Spec.St) (k : Nat) (h : s.steps = k + 1) :
    Spec.tick.run.run s = (.ok (), { s with steps := k }) := by
  rw [tick_run, h]

theorem eval_mtch_run (ds : Spec.Decls) (d : Nat) (subj : Spec.Expr)
    (cs : List (Spec.Pat × Option Spec.Expr × Spec.Expr)) (s : Spec.St) :
    (Spec.eval ds (d + 1) (.mtch subj cs)).run.run s =
      match Spec.tick.run.run s with
      | (.ok _, s1) => match (Spec.eval ds d subj).run.run s1 with
        | (.ok v, t1) => (Spec.evalCases ds d v cs).run.run t1
        | (.error e, t1) => (.error e, t1)
      | (.error e, s1) => (.error e, s1) := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq]
  rcases Spec.tick.run.run s with ⟨r, s1⟩
  cases r
  · rfl
  · simp only
    rcases (Spec.eval ds d subj).run.run s1 with ⟨r', t1⟩
    cases r' <;> rfl

/-- A name bound to `b`: its value, read through `deref`. -/
theorem eval_name_run (ds : Spec.Decls) (d : Nat) (x : String) (s : Spec.St) (b : Spec.Binding)
    (hb : Spec.lookupVar s.locals s.globals x = some b) :
    (Spec.eval ds (d + 1) (.name x)).run.run s =
      match Spec.tick.run.run s with
      | (.ok _, s1) => ((liftM (Spec.deref b.val) : Spec.M Spec.Val)).run.run s1
      | (.error e, s1) => (.error e, s1) := by
  have hres : Spec.resolve ds s.locals s.globals x = .var b := by simp [Spec.resolve, hb]
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_run]
  rcases s.steps with _ | k
  · rfl
  · simp only [Spec.run_liftM, Spec.run_getCore]
    rw [hres]; rfl


/-- **M3: the scoped lowering of `match` is correct with respect to galaspec**, given that
    the subject, guards and bodies are lowered correctly.

    Hypotheses (the "given parts", as in T3): the subject's lowering `subj'` simulates
    galaspec's subject (`Part`), and its value has the static type `t` the lowering was
    given (`HasTy`); each arm's guard and body are lowered correctly in the arm's scope, and
    a guard leaves galaspec's locals as it found them (`ArmOk`).

    Conclusion: the lowered `match` simulates galaspec's `match`: from related states,
    wherever galaspec's run ends with a value or a panic, GoCore's ends with the encoded
    value or a panic of the same class, in related states. This is forward simulation from
    galaspec's results that are values or panics; runs galaspec ends out of fuel, `stuck`,
    `unsupported`, or with a `return` on its way out, are not covered.

    **The stated gap (`HasTy`).** galaspec decides what a bare pattern name means at run
    time, from the value's sealed type; the transformer decides it statically, from the
    type it inferred. The two agree when the subject's value has that type. Discharging
    this needs a typing judgment (T7), which the spec does not have.

    **The holes.** The guards and bodies are holes; a Go call in them could be evaluated in
    either order relative to a variable read (Go fixes the order only among calls). The
    `Part` hypotheses (related results and frames from every related state) are what make
    that order unobservable here; M5, which lowers the holes, has to establish them.

    It holds of the scoped lowering in the transformer's shape up to the normalizations
    listed in docs/FORMAL_SEMANTICS.MD, M6; that the lowering describes what the transformer
    emits is trusted. -/
theorem lowerMatch_correct (ds : Spec.Decls) (d : Nat) (inScope : String → Bool) (t : STy)
    (X : Nat → GoCore.GM GoCore.GVal) (subj : Spec.Expr) (subj' : GoCore.GExpr) (arms : List Arm)
    (hs : Part (ScopeOk inScope) (HasTy ds t) (Spec.eval ds d subj) (GoCore.geval X subj'))
    (ha : ∀ a ∈ arms, ArmOk ds d inScope t X a) :
    Part (ScopeOk inScope) (fun _ => True) (Spec.eval ds (d + 1) (.mtch subj (arms.map Arm.src)))
      (GoCore.geval X (lowerMatch ds inScope t subj' arms)) := by
  refine ⟨fun s g hR hpre hnb => ?_⟩
  -- galaspec: a step, the subject, then the arms.
  rw [eval_mtch_run, tick_run] at hnb ⊢
  rcases hst : s.steps with _ | k
  · rw [hst] at hnb; simp [NotBad] at hnb
  rw [hst] at hnb
  simp only at hnb ⊢
  let s1 : Spec.St := { s with steps := k }
  have hsim := hs.sim s1 g hR hpre
  -- GoCore: the function literal, the subject into its temporary, then the arms.
  have hgo : (GoCore.geval X (lowerMatch ds inScope t subj' arms)).run.run g =
      match (GoCore.geval X subj').run.run g with
      | (.ok w, g1) => match (GoCore.gexecs X (lowerArms ds inScope t 1 arms)).run.run
            { g1 with locals := (.tmp 0, w) :: g1.locals } with
        | (.ok _, t) => (.error (.stuck "function literal ended without a return"),
            GoCore.truncate t g.locals.length)
        | (.error (.ret v), t) => (.ok v, GoCore.truncate t g.locals.length)
        | (.error e, t) => (.error e, t)
      | (.error (.ret w), g1) => (.ok w, GoCore.truncate g1 g.locals.length)
      | (.error e, g1) => (.error e, g1) := by
    rw [lowerMatch, funcLit_run, gexecs_cons, gexec_define]
    rcases (GoCore.geval X subj').run.run g with ⟨r, g1⟩
    cases r with
    | error e => cases e <;> rfl
    | ok w => rfl
  rw [hgo]
  rcases hsr : (Spec.eval ds d subj).run.run s1 with ⟨r1, t1⟩
  rw [hsr] at hsim hnb
  rcases r1 with e1 | v
  · -- The subject stopped: only a panic is claimed.
    cases e1 with
    | panic cl msg =>
      obtain ⟨hres, hRt, _⟩ := hsim trivial
      rcases hy : (GoCore.geval X subj').run.run g with ⟨ry, gy⟩
      rw [hy] at hres hRt
      cases ry with
      | ok => simp [ResRel] at hres
      | error ey =>
        cases ey with
        | panic cl' m' => exact ⟨hres, hRt, fun _ h => nomatch h⟩
        | _ => simp [ResRel] at hres
    | _ => simp [NotBad] at hnb
  · -- The subject gave `v`: the arms run with it in `_tmp_0`.
    obtain ⟨hres, hRt, hfr⟩ := hsim trivial
    obtain ⟨hT, hFS, hFL⟩ := hfr v rfl
    rcases hy : (GoCore.geval X subj').run.run g with ⟨ry, gy⟩
    rw [hy] at hres hRt hFL
    cases ry with
    | error => simp [ResRel] at hres
    | ok w =>
    simp only [ResRel] at hres
    subst hres
    simp only
    let g0 : GoCore.GSt := { gy with locals := (.tmp 0, enc v) :: gy.locals }
    have hg0 : Good t1.toCore v g0 :=
      ⟨⟨hRt.1, .tmp hRt.2.1, hRt.2.2⟩, by simp [lookupVar_tmp, g0, List.lookup]⟩
    have hS1 : ScopeOk inScope t1.toCore := hpre.of_names hFS.1 hFS.2
    have h := arms_correct ds d inScope t X v hT arms ha 1 t1 g0 (Nat.le_refl _) hg0 hS1 hnb
    unfold ArmsRes at h
    rcases hgs : (Spec.evalCases ds d v (arms.map Arm.src)).run.run t1 with ⟨r2, t2⟩
    try rw [hgs] at h
    simp only at hnb
    try rw [hgs] at hnb
    rcases hga : (GoCore.gexecs X (lowerArms ds inScope t 1 arms)).run.run g0 with ⟨r3, t3⟩
    try rw [hga] at h
    rcases r2 with e2 | w2
    · cases e2 with
      | panic cl msg =>
        obtain ⟨⟨m, hm⟩, hR3⟩ := h
        simp only at hm; subst hm
        exact ⟨rfl, hR3, fun _ h => nomatch h⟩
      | _ => simp [NotBad] at hnb
    · obtain ⟨hret, ⟨Z, bot, hz, hfb, hRb⟩, hFS2⟩ := h
      simp only at hret; subst hret
      cases hfb with
      | cons h0 hbot =>
      rename_i e0 bot2
      have hlen : g.locals.length = bot2.length := by rw [Spec.fall2_length hFL, Spec.fall2_length hbot]
      have htr : GoCore.truncate t3 g.locals.length = { t3 with locals := bot2 } := by
        rw [hlen]; exact truncate_append t3 (Z ++ [e0]) bot2 (by simpa using hz)
      refine ⟨rfl, ?_, fun _ h => ?_⟩
      · simp only; rw [htr]
        refine ⟨hRb.1, ?_, hRb.2.2⟩
        have := hRb.2.1
        obtain ⟨x0, w0⟩ := e0
        simp only at h0
        have hx0 : x0 = .tmp 0 := h0.1.symm
        subst hx0
        exact LocRel.drop_top [] [(.tmp 0, w0)] _ bot2 this (by simp [isTmpE])
      · injection h with h; subst h
        refine ⟨trivial, ⟨hFS2.1.trans hFS.1, hFS2.2.trans hFS.2⟩, ?_⟩
        simp only; rw [htr]
        exact hFL.trans hbot

/-! ## The hypotheses can be met

`Part` is what `lowerMatch_correct` takes of the subject, guards and bodies. It holds of
literals, local variables, and int `+` and `>` of parts that give ints: the lowering of
these is correct in the sense the theorem needs, so its hypotheses are not vacuous. -/

theorem eval_zero (ds : Spec.Decls) (e : Spec.Expr) (s : Spec.St) :
    ((Spec.eval ds 0 e).run.run s).1 = .error .fuel := by
  rw [Spec.eval.eq_def]; rfl

/-- One step of galaspec's `eval` on a literal. -/
theorem eval_lit_run (ds : Spec.Decls) (d : Nat) (n : Int) (s : Spec.St) :
    (Spec.eval ds (d + 1) (.lit (.int n))).run.run s =
      match Spec.tick.run.run s with
      | (.ok _, s1) => ((Spec.intLit n : Spec.H Spec.Val) |> fun x => (liftM x : Spec.M Spec.Val)).run.run s1
      | (.error e, s1) => (.error e, s1) := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq]
  rcases Spec.tick.run.run s with ⟨r, s1⟩
  cases r <;> rfl

/-- **A literal** lowers correctly: an int constant of Go's range. -/
theorem part_lit_int (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop) (n : Int) (hn : Spec.minInt ≤ n ∧ n ≤ Spec.maxInt) :
    Part pre (fun v => v = .int n) (Spec.eval ds d (.lit (.int n))) (GoCore.geval X (.lit (.int n))) := by
  refine ⟨fun s g hR _ hnb => ?_⟩
  cases d with
  | zero => rw [eval_zero] at hnb; simp [NotBad] at hnb
  | succ d =>
    rw [eval_lit_run, tick_run] at hnb ⊢
    rcases hst : s.steps with _ | k
    · rw [hst] at hnb; simp [NotBad] at hnb
    · simp only
      have : ((liftM (Spec.intLit n) : Spec.M Spec.Val)).run.run { s with steps := k } =
          (.ok (.int n), { s with steps := k }) := by
        simp only [Spec.intLit, hn, and_self, ite_true]; rfl
      rw [this]
      exact ⟨rfl, hR, fun v h => by
        injection h with h; subst h
        exact ⟨rfl, FrameS.refl _, FrameL.refl _⟩⟩

/-- **A local or package-level variable** lowers correctly to the GoCore variable of its
    name, wherever it is bound. -/
theorem part_var (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal) (x : String) :
    Part (fun c => (Spec.lookupVar c.locals c.globals x).isSome) (fun _ => True)
      (Spec.eval ds d (.name x)) (GoCore.geval X (.var (.user x))) := by
  refine ⟨fun s g hR hpre hnb => ?_⟩
  cases d with
  | zero => rw [eval_zero] at hnb; simp [NotBad] at hnb
  | succ d =>
    obtain ⟨b, hb⟩ := Option.isSome_iff_exists.mp hpre
    rw [eval_name_run ds d x s b hb, tick_run] at hnb ⊢
    rcases hst : s.steps with _ | k
    · rw [hst] at hnb; simp [NotBad] at hnb
    rw [hst] at hnb
    simp only [Spec.run_liftM] at hnb ⊢
    have hd := Spec.deref_run b.val s.toCore
    rcases hdr : (Spec.deref b.val).run.run s.toCore with ⟨rd, cd⟩
    rw [hdr] at hd hnb
    simp only at hnb
    have hcd : cd = s.toCore := by
      have := congrArg Prod.snd hd; simpa using this
    subst hcd
    rcases rd with e | v
    · cases e with
      | panic cl m => exact absurd (by rw [hdr]) (noPanic_deref b.val s.toCore cl m)
      | _ => simp [NotBad] at hnb
    · have hw := R_reads hR x b hb v (by rw [hdr])
      have hg : (GoCore.geval X (.var (.user x))).run.run g = (.ok (enc v), g) :=
        geval_peval X g _ _ (by simp [peval, hw])
      rw [hg]
      exact ⟨rfl, hR, fun _ _ => ⟨trivial, FrameS.refl _, FrameL.refl _⟩⟩

theorem eval_binary_run (ds : Spec.Decls) (d : Nat) (op : String) (l r : Spec.Expr) (s : Spec.St)
    (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    (Spec.eval ds (d + 1) (.binary op l r)).run.run s =
      match Spec.tick.run.run s with
      | (.ok _, s1) => (do
          let a ← Spec.eval ds d l
          let b ← Spec.eval ds d r
          (liftM (Spec.binop op a b) : Spec.M Spec.Val)).run.run s1
      | (.error e, s1) => (.error e, s1) := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq]
  rcases Spec.tick.run.run s with ⟨rt, s1⟩
  cases rt
  · rfl
  · simp only

/-- An int. -/
def IsInt (v : Spec.Val) : Prop := ∃ n, v = .int n

/-- **A binary operator on ints** lowers correctly, given its operands do and give ints,
    and the operator agrees on ints (`hop`). -/
theorem part_bin (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop) (hpre : ∀ c c', pre c → FrameS c c' → pre c')
    (op : String) (gop : GoCore.BinOp) (h1 : op ≠ "&&") (h2 : op ≠ "||") (Q : Spec.Val → Prop)
    (hop : ∀ x y (c : Spec.Core), ∃ w, (Spec.binop op (.int x) (.int y)).run.run c = (.ok w, c) ∧
      GoCore.binVal gop (.int x) (.int y) = .ok (enc w) ∧ Q w)
    (l r : Spec.Expr) (l' r' : GoCore.GExpr)
    (hl : Part pre IsInt (Spec.eval ds d l) (GoCore.geval X l'))
    (hr : Part pre IsInt (Spec.eval ds d r) (GoCore.geval X r')) :
    Part pre Q (Spec.eval ds (d + 1) (.binary op l r)) (GoCore.geval X (.bin gop l' r')) := by
  refine ⟨fun s g hR hp hnb => ?_⟩
  rw [eval_binary_run ds d op l r s h1 h2, tick_run] at hnb ⊢
  rcases hst : s.steps with _ | k
  · rw [hst] at hnb; simp [NotBad] at hnb
  rw [hst] at hnb
  simp only [Spec.run_bind_eq] at hnb ⊢
  simp only [GoCore.geval, grun_bind]
  let s1 : Spec.St := { s with steps := k }
  have hsl := hl.sim s1 g hR hp
  rcases hlr : (Spec.eval ds d l).run.run s1 with ⟨ra, t1⟩
  rw [hlr] at hsl hnb
  rcases hyl : (GoCore.geval X l').run.run g with ⟨ra', g1⟩
  rw [hyl] at hsl
  rcases ra with ea | a
  · cases ea with
    | panic =>
      obtain ⟨hres, hRt, _⟩ := hsl trivial
      cases ra' with
      | ok => simp [ResRel] at hres
      | error e' =>
        cases e' with
        | panic => exact ⟨hres, hRt, fun _ h => nomatch h⟩
        | _ => simp [ResRel] at hres
    | _ => simp [NotBad] at hnb
  obtain ⟨hres, hRt, hfr⟩ := hsl trivial
  obtain ⟨⟨x, rfl⟩, hFS1, hFL1⟩ := hfr _ rfl
  cases ra' with
  | error => simp [ResRel] at hres
  | ok wa =>
  simp only [ResRel] at hres; subst hres
  simp only at hnb ⊢
  have hsr := hr.sim t1 g1 hRt (hpre _ _ hp hFS1)
  rcases hrr : (Spec.eval ds d r).run.run t1 with ⟨rb, t2⟩
  rw [hrr] at hsr hnb
  rcases hyr : (GoCore.geval X r').run.run g1 with ⟨rb', g2⟩
  rw [hyr] at hsr
  rcases rb with eb | b
  · cases eb with
    | panic =>
      obtain ⟨hres, hRt2, _⟩ := hsr trivial
      cases rb' with
      | ok => simp [ResRel] at hres
      | error e' =>
        cases e' with
        | panic => exact ⟨hres, hRt2, fun _ h => nomatch h⟩
        | _ => simp [ResRel] at hres
    | _ => simp [NotBad] at hnb
  obtain ⟨hres, hRt2, hfr2⟩ := hsr trivial
  obtain ⟨⟨y, rfl⟩, hFS2, hFL2⟩ := hfr2 _ rfl
  cases rb' with
  | error => simp [ResRel] at hres
  | ok wb =>
  simp only [ResRel] at hres; subst hres
  obtain ⟨w, hw, hg, hq⟩ := hop x y t2.toCore
  simp only [Spec.run_liftM, hw, enc, grun_ofExcept, hg]
  exact ⟨rfl, hRt2, fun v h => by
    injection h with h; subst h
    exact ⟨hq, ⟨hFS2.1.trans hFS1.1, hFS2.2.trans hFS1.2⟩, hFL1.trans hFL2⟩⟩

/-- `+` on ints: Go's wrapping `+` is galaspec's. -/
theorem part_add (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop) (hpre : ∀ c c', pre c → FrameS c c' → pre c')
    (l r : Spec.Expr) (l' r' : GoCore.GExpr)
    (hl : Part pre IsInt (Spec.eval ds d l) (GoCore.geval X l'))
    (hr : Part pre IsInt (Spec.eval ds d r) (GoCore.geval X r')) :
    Part pre IsInt (Spec.eval ds (d + 1) (.binary "+" l r)) (GoCore.geval X (.bin .add l' r')) :=
  part_bin ds d X pre hpre "+" .add (by decide) (by decide) IsInt
    (fun x y c => ⟨.int (Spec.wrap (x + y)), rfl, rfl, ⟨_, rfl⟩⟩) l r l' r' hl hr

/-- `>` on ints. -/
theorem part_gt (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal)
    (pre : Spec.Core → Prop) (hpre : ∀ c c', pre c → FrameS c c' → pre c')
    (l r : Spec.Expr) (l' r' : GoCore.GExpr)
    (hl : Part pre IsInt (Spec.eval ds d l) (GoCore.geval X l'))
    (hr : Part pre IsInt (Spec.eval ds d r) (GoCore.geval X r')) :
    Part pre (fun _ => True) (Spec.eval ds (d + 1) (.binary ">" l r)) (GoCore.geval X (.bin .gt l' r')) :=
  part_bin ds d X pre hpre ">" .gt (by decide) (by decide) _
    (fun x y c => ⟨.bool (decide (x > y)), rfl, rfl, trivial⟩) l r l' r' hl hr

/-- A weaker `post`. -/
theorem Part.mono_post {pre : Spec.Core → Prop} {P Q : Spec.Val → Prop} {x : Spec.M Spec.Val}
    {y : GoCore.GM GoCore.GVal} (h : Part pre P x y) (hpq : ∀ v, P v → Q v) : Part pre Q x y :=
  ⟨fun s g hR hp hnb =>
    let ⟨hres, hRt, hfr⟩ := h.sim s g hR hp hnb
    ⟨hres, hRt, fun v hv => let ⟨hP, hS, hL⟩ := hfr v hv; ⟨hpq v hP, hS, hL⟩⟩⟩

/-- The arms of `3 match { case 1 => 10; case x => 20; case _ => 30 }`, with literal bodies. -/
def instanceArms : List Arm :=
  [⟨.lit (.int 1), none, (.lit (.int 10), .lit (.int 10))⟩,
   ⟨.name "x", none, (.lit (.int 20), .lit (.int 20))⟩,
   ⟨.wild, none, (.lit (.int 30), .lit (.int 30))⟩]

/-- **The theorem's hypotheses can be met all together.** `lowerMatch_correct` applied to
    `3 match { case 1 => 10; case x => 20; case _ => 30 }` (a literal, a binder and a
    wildcard pattern): the subject is an int, of static type `other`, and every arm's body
    is a literal, so each hypothesis follows from `part_lit_int`. -/
theorem lowerMatch_instance (ds : Spec.Decls) (d : Nat) (X : Nat → GoCore.GM GoCore.GVal) :
    Part (ScopeOk fun _ => false) (fun _ => True)
      (Spec.eval ds (d + 1) (.mtch (.lit (.int 3)) (instanceArms.map Arm.src)))
      (GoCore.geval X (lowerMatch ds (fun _ => false) .other (.lit (.int 3)) instanceArms)) :=
  lowerMatch_correct ds d _ .other X _ _ _
    ((part_lit_int ds d X _ 3 ⟨by decide, by decide⟩).mono_post fun v hv => by
      subst hv; exact .other _ nofun nofun nofun nofun)
    (by
      intro a ha
      simp only [instanceArms, List.mem_cons, List.not_mem_nil, or_false] at ha
      rcases ha with rfl | rfl | rfl
      · exact ⟨(fun _ _ h => nomatch h),
          (part_lit_int ds d X _ 10 ⟨by decide, by decide⟩).mono_post fun _ _ => trivial⟩
      · exact ⟨(fun _ _ h => nomatch h),
          (part_lit_int ds d X _ 20 ⟨by decide, by decide⟩).mono_post fun _ _ => trivial⟩
      · exact ⟨(fun _ _ h => nomatch h),
          (part_lit_int ds d X _ 30 ⟨by decide, by decide⟩).mono_post fun _ _ => trivial⟩)

/-! ## The leaky lowering is not correct (#996)

The lowering #996 reported: an arm's pattern variables bound to their names before the
arm's `if`, so they are still in scope in the later arms (the `else`), where they hide the
names the program meant. -/

open GoCore in
/-- The leaky lowering of the arms: `bindUser` before the test, in the arm's block. -/
def lowerArmsLeaky (ds : Spec.Decls) (inScope : String → Bool) (t : STy) :
    Nat → List Arm → List GStmt
  | _, [] => [.panic .match_ "no match arm matched"]
  | n, a :: rest =>
    let lp := lowerPat ds inScope t a.pat (.var (.tmp 0))
    let ok := n + lp.2.length
    let others := lowerArmsLeaky ds inScope t (ok + 1) rest
    match a.guard with
    | none => [.block (defsOf n lp.2 ++ bindUser n lp.2 ++ [.ifElse lp.1 [.ret a.body.2] others])]
    | some (_, g) => [.block (defsOf n lp.2 ++ bindUser n lp.2 ++
        [.varDecl (.tmp ok) (.bool false),
         .ifElse lp.1 [.assign (.tmp ok) g] [],
         .ifElse (.var (.tmp ok)) [.ret a.body.2] others])]

open GoCore in
/-- The leaky lowering of `match`. -/
def lowerMatchLeaky (ds : Spec.Decls) (inScope : String → Bool) (t : STy) (subj : GExpr)
    (arms : List Arm) : GExpr :=
  .funcLit (.define (.tmp 0) subj :: lowerArmsLeaky ds inScope t 1 arms)
/-
  The counterexample, in GALA (a failing guard, so no zero value is involved):

    func area(s Shape, r int) int = s match {
        case Circle(r) if r > 100 => r      // matches, but the guard fails
        case Circle(side) => side + r        // r is the parameter
    }
    area(Circle(3), 7)

  GALA says 10. The leaky lowering says 6: the first arm's `r` (3) is still bound in the
  second arm and hides the parameter (7).
-/
namespace Counterexample

open GoCore

/-- `sealed type Shape { case Circle(R int) }`. -/
def ds : Spec.Decls :=
  ⟨[], [], [], [("Shape", [⟨"Circle", false, [⟨"R", some (.name "int"), false, none⟩]⟩])], []⟩

/-- The names in scope at the `match`: the parameters. -/
def inScope (x : String) : Bool := x == "s" || x == "r"

/-- The two arms, each with its lowered guard and body. -/
def arms : List Arm :=
  [⟨.extract "Circle" [.name "r"],
    some (.binary ">" (.name "r") (.lit (.int 100)), .bin .gt (.var (.user "r")) (.lit (.int 100))),
    (.name "r", .var (.user "r"))⟩,
   ⟨.extract "Circle" [.name "side"], none,
    (.binary "+" (.name "side") (.name "r"), .bin .add (.var (.user "side")) (.var (.user "r")))⟩]

/-- No holes. -/
def X : Nat → GM GVal := fun _ => throw (.stuck "no hole")

/-- The call's frame: `s = Circle(3)`, `r = 7`. -/
def st : Spec.St :=
  ⟨⟨[("s", ⟨.sealed "Shape" "Circle" [.int 3], false⟩), ("r", ⟨.int 7, false⟩)], [], "", #[]⟩, 100⟩

/-- The same frame in GoCore. -/
def g0 : GSt := ⟨[(.user "s", .sealed "Shape" "Circle" [.int 3]), (.user "r", .int 7)], [], ""⟩

theorem gala_says_10 :
    ((Spec.eval ds 10 (.mtch (.name "s") (arms.map Arm.src))).run.run st).1 = .ok (.int 10) := by
  rw [eval_mtch_run, tick_succ st 99 rfl]
  simp only
  rw [eval_name_run ds 8 "s" _ ⟨.sealed "Shape" "Circle" [.int 3], false⟩ rfl, tick_succ _ 98 rfl]
  simp only [Spec.run_liftM]
  rw [show (Spec.deref (.sealed "Shape" "Circle" [.int 3])).run.run st.toCore =
    (.ok (.sealed "Shape" "Circle" [.int 3]), st.toCore) from rfl]
  simp only [arms, List.map, Arm.src, Option.map]
  rw [evalCases_guard ds 9 _ _ _ _ _ _ [("r", .int 3)] rfl]
  rw [eval_binary_run ds 8 ">" _ _ _ (by decide) (by decide), tick_succ _ 97 rfl]
  simp only [Spec.run_bind_eq]
  rw [eval_name_run ds 7 "r" _ ⟨.int 3, false⟩ rfl, tick_succ _ 96 rfl]
  simp only [Spec.run_liftM]
  rw [show ∀ c : Spec.Core, (Spec.deref (.int 3)).run.run c = (.ok (.int 3), c) from fun _ => rfl]
  simp only
  rw [eval_lit_run, tick_succ _ 95 rfl]
  simp only [Spec.run_liftM]
  rw [show ∀ c : Spec.Core, (Spec.intLit 100).run.run c = (.ok (.int 100), c) from fun _ => rfl]
  simp only
  rw [show ∀ c : Spec.Core, (Spec.binop ">" (.int 3) (.int 100)).run.run c = (.ok (.bool false), c)
    from fun _ => rfl]
  simp only
  rw [evalCases_noguard ds 9 _ _ _ _ _ [("side", .int 3)] rfl]
  rw [eval_binary_run ds 8 "+" _ _ _ (by decide) (by decide), tick_succ _ 94 rfl]
  simp only [Spec.run_bind_eq]
  rw [eval_name_run ds 7 "side" _ ⟨.int 3, false⟩ rfl, tick_succ _ 93 rfl]
  simp only [Spec.run_liftM]
  rw [show ∀ c : Spec.Core, (Spec.deref (.int 3)).run.run c = (.ok (.int 3), c) from fun _ => rfl]
  simp only
  rw [eval_name_run ds 7 "r" _ ⟨.int 7, false⟩ rfl, tick_succ _ 92 rfl]
  simp only [Spec.run_liftM]
  rw [show ∀ c : Spec.Core, (Spec.deref (.int 7)).run.run c = (.ok (.int 7), c) from fun _ => rfl]
  simp only
  rw [show ∀ c : Spec.Core, (Spec.binop "+" (.int 3) (.int 7)).run.run c = (.ok (.int 10), c)
    from fun _ => rfl]

theorem scoped_go_says_10 :
    ((geval X (lowerMatch ds inScope (.sealed "Shape") (.var (.user "s")) arms)).run.run g0).1 =
      .ok (.int 10) := rfl

theorem leaky_go_says_6 :
    ((geval X (lowerMatchLeaky ds inScope (.sealed "Shape") (.var (.user "s")) arms)).run.run g0).1 =
      .ok (.int 6) := rfl

theorem st_g0_related : R st.toCore g0 :=
  ⟨rfl, .user (.user .nil ⟨_, rfl, rfl⟩) ⟨_, rfl, rfl⟩, .nil⟩

theorem st_scope : ScopeOk inScope st.toCore := by
  intro x
  simp only [inScope, Spec.lookupVar, st, Spec.lookup]
  by_cases h1 : "s" = x
  · subst h1; rfl
  · by_cases h2 : "r" = x
    · subst h2; rfl
    · have h1' : (x == "s") = false := by simp only [beq_eq_false_iff_ne, ne_eq]; exact fun h => h1 h.symm
      have h2' : (x == "r") = false := by simp only [beq_eq_false_iff_ne, ne_eq]; exact fun h => h2 h.symm
      simp [h1, h2, h1', h2']

end Counterexample

open Counterexample in
/-- **The leaky lowering is not correct (#996).** On the program above, from related states,
    galaspec's `match` gives 10 and the leaky lowering's gives 6, so the leaky lowering does
    not simulate galaspec: `Part`, the conclusion of `lowerMatch_correct`, fails for it. The
    scoped lowering gives 10 there (`scoped_go_says_10`). -/
theorem leaky_not_correct :
    ¬ Part (ScopeOk inScope) (fun _ => True) (Spec.eval ds 10 (.mtch (.name "s") (arms.map Arm.src)))
      (GoCore.geval X (lowerMatchLeaky ds inScope (.sealed "Shape") (.var (.user "s")) arms)) := by
  intro h
  have := (h.sim st g0 st_g0_related st_scope (by rw [gala_says_10]; trivial)).1
  rw [gala_says_10, leaky_go_says_6] at this
  simp [ResRel, enc] at this

end Gala.M3
