/-
  # T2, part b1: the evaluator's steps commute with renaming

  The steps `eval` takes that do not evaluate a subexpression: printing, operators, field
  access, assignment, cells and zero values. On related values and states (`Gala/Alpha.lean`)
  each gives related results and leaves related states. These are lemmas toward T2, not T2.

  Struct and sealed declarations are the same on both sides (`DeclsR.structs_eq`,
  `DeclsR.sealeds_eq`), so everything that reads only them (printing's layout, field
  indices, zero values, type checks) is the same function on both sides. Field and case
  names are never renamed.
-/
import Gala.Alpha

namespace Gala.Spec

/-! ## Struct and sealed declarations are the same -/

theorem fall2_eq : ∀ {l l' : List α}, Fall2 Eq l l' → l = l'
  | _, _, .nil => rfl
  | _, _, .cons h t => by rw [h, fall2_eq t]

theorem VarR.eq {pkg : List String} {pm : String → Ren} {v w : Variant} (h : VarR pkg pm v w) :
    v = w := by
  obtain ⟨n, d, f⟩ := h
  cases v; cases w
  simp only at n d f
  subst n d
  rw [fall2_eq f]

theorem fall2_pair_eq {R : α → α → Prop} (hR : ∀ a b, R a b → a = b) :
    ∀ {l l' : List (String × α)}, Fall2 (fun a b => a.1 = b.1 ∧ R a.2 b.2) l l' → l = l'
  | _, _, .nil => rfl
  | (k, a) :: _, (k', b) :: _, .cons ⟨hk, hr⟩ t => by
    simp only at hk hr
    rw [hk, hR _ _ hr, fall2_pair_eq hR t]

theorem fall2_mono {R S : α → β → Prop} (h : ∀ a b, R a b → S a b) :
    ∀ {l : List α} {l' : List β}, Fall2 R l l' → Fall2 S l l'
  | _, _, .nil => .nil
  | _, _, .cons hr t => .cons (h _ _ hr) (fall2_mono h t)

theorem DeclsR.structs_eq {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') : ds.structs = ds'.structs :=
  fall2_pair_eq (fun _ _ h => fall2_eq h) hD.structs

theorem DeclsR.sealeds_eq {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') : ds.sealeds = ds'.sealeds :=
  fall2_pair_eq (fun _ _ h => fall2_eq (fall2_mono (fun _ _ hv => VarR.eq hv) h)) hD.sealeds

/-! ## First-order values -/

mutual
/-- A value with no closure, function or cell in it. -/
def Val.fo : Val → Bool
  | .int _ | .bool _ | .str _ | .unit => true
  | .tuple vs | .struct_ _ vs | .sealed _ _ vs => Val.fos vs
  | .closure .. | .fn _ | .ref _ => false
def Val.fos : List Val → Bool
  | [] => true
  | v :: vs => v.fo && Val.fos vs
end

mutual
theorem ValR.refl_fo {pkg : List String} {pm : String → Ren} :
    ∀ (v : Val), v.fo = true → ValR pkg pm v v
  | .int _, _ => .int _
  | .bool _, _ => .bool _
  | .str _, _ => .str _
  | .unit, _ => .unit
  | .tuple vs, h => .tuple (ValsR.refl_fo vs (by simpa [Val.fo] using h))
  | .struct_ _ vs, h => .struct_ _ (ValsR.refl_fo vs (by simpa [Val.fo] using h))
  | .sealed _ _ vs, h => .sealed _ _ (ValsR.refl_fo vs (by simpa [Val.fo] using h))
  | .closure .., h | .fn _, h | .ref _, h => by simp [Val.fo] at h
theorem ValsR.refl_fo {pkg : List String} {pm : String → Ren} :
    ∀ (vs : List Val), Val.fos vs = true → ValsR pkg pm vs vs
  | [], _ => .nil
  | v :: vs, h => by
    simp only [Val.fos, Bool.and_eq_true] at h
    exact .cons (ValR.refl_fo v h.1) (ValsR.refl_fo vs h.2)
end

/-! ## `mapM` over read-only steps -/

theorem readOnly_mapM {f : α → H β} (hf : ∀ x, ReadOnly (f x)) :
    ∀ l : List α, ReadOnly (l.mapM f)
  | [] => readOnly_pure _
  | a :: as => by
    rw [List.mapM_cons]
    exact readOnly_bind (hf a) fun _ => readOnly_bind (readOnly_mapM hf as) fun _ => readOnly_pure _

theorem mapM_rel {pkg : List String} {pm : String → Ren} {R : β → β' → Prop} {f : α → H β}
    {g : α → H β'} {c c' : Core} (hf : ∀ x, ReadOnly (f x)) (hg : ∀ x, ReadOnly (g x)) :
    ∀ (l : List α), (∀ x ∈ l, ResR pkg pm R ((f x).run.run c).1 ((g x).run.run c').1) →
      ResR pkg pm (Fall2 R) ((l.mapM f).run.run c).1 ((l.mapM g).run.run c').1
  | [], _ => .nil
  | a :: as, h => by
    rw [List.mapM_cons, List.mapM_cons]
    exact resR_bind (h a (List.mem_cons_self ..)) (hf a) (hg a) fun b b' hb =>
      resR_bind (mapM_rel hf hg as fun x hx => h x (List.mem_cons_of_mem _ hx))
        (readOnly_mapM hf as) (readOnly_mapM hg as) fun bs bs' hbs => .cons hb hbs

theorem valsR_of_fall2 {pkg : List String} {pm : String → Ren} :
    ∀ {vs vs' : List Val}, Fall2 (ValR pkg pm) vs vs' → ValsR pkg pm vs vs'
  | _, _, .nil => .nil
  | _, _, .cons h t => .cons h (valsR_of_fall2 t)

/-! ## Types and zero values -/

/-- An `H` step that neither reads nor changes the state. -/
def PureH (x : H α) : Prop :=
  ∀ c c', (x.run.run c).1 = (x.run.run c').1 ∧ (x.run.run c).2 = c

theorem PureH.readOnly {x : H α} (h : PureH x) : ReadOnly x := fun c => (h c c).2

theorem pureH_pure (a : α) : PureH (pure a : H α) := fun _ _ => ⟨rfl, rfl⟩

theorem pureH_throw (e : Stop) : PureH (throw e : H α) := fun _ _ => ⟨rfl, rfl⟩

theorem pureH_bind {x : H α} {f : α → H β} (hx : PureH x) (hf : ∀ a, PureH (f a)) :
    PureH (x >>= f) := by
  intro c c'
  rw [run_bind_eq, run_bind_eq]
  obtain ⟨h1, h2⟩ := hx c c'
  have h2' := (hx c' c).2
  rcases e : x.run.run c with ⟨r, t⟩
  rcases e' : x.run.run c' with ⟨r', t'⟩
  rw [e, e'] at h1
  rw [e] at h2
  rw [e'] at h2'
  simp only at h1 h2 h2'
  subst h1 h2 h2'
  cases r with
  | error => exact ⟨rfl, rfl⟩
  | ok a => exact hf a _ _

theorem pureH_mapM {f : α → H β} (hf : ∀ x, PureH (f x)) : ∀ l : List α, PureH (l.mapM f)
  | [] => pureH_pure _
  | a :: as => by
    rw [List.mapM_cons]
    exact pureH_bind (hf a) fun _ => pureH_bind (pureH_mapM hf as) fun _ => pureH_pure _

set_option hygiene false in
macro "pure_step" : tactic => `(tactic| (first
  | exact pureH_pure _
  | exact pureH_throw _
  | (apply pureH_bind <;> intros)
  | (apply pureH_mapM; intros)
  | exact ihC _
  | exact ihC
  | exact ihC _ _
  | exact ihCs
  | exact ihZ _
  | split))

mutual
theorem pureH_checkTy (ds : Decls) : ∀ t, PureH (checkTy ds t)
  | .unsupported _ => pureH_throw _
  | .name _ => by unfold checkTy; repeat' pure_step
  | .func ps none => by
    have ihCs := pureH_checkTys ds ps
    unfold checkTy; repeat' pure_step
  | .func ps (some r) => by
    have ihCs := pureH_checkTys ds ps
    have ihC := pureH_checkTy ds r
    unfold checkTy; repeat' pure_step
theorem pureH_checkTys (ds : Decls) : ∀ ts, PureH (checkTys ds ts)
  | [] => pureH_pure _
  | t :: ts => by
    have ihC := pureH_checkTy ds t
    have ihCs := pureH_checkTys ds ts
    unfold checkTys; repeat' pure_step
end

theorem pureH_checkOptTy (ds : Decls) (w : String) (t : Option Ty) :
    PureH (checkOptTy ds w t) := by
  cases t
  · exact pureH_throw _
  · exact pureH_checkTy ds _

theorem pureH_zero (ds : Decls) : ∀ n t, PureH (zero ds n t)
  | 0, _ => pureH_throw _
  | n + 1, t => by
    have ihZ := pureH_zero ds n
    have ihC := pureH_checkOptTy ds
    rw [zero.eq_def]
    split <;> (try (injections; subst_vars)) <;> repeat' pure_step

/-- An `H` step that never throws a `return`. -/
def NoRetH (x : H α) : Prop := ∀ c v, (x.run.run c).1 ≠ .error (.ret v)

theorem noRet_pure (a : α) : NoRetH (pure a : H α) := fun _ _ h => nomatch h

theorem noRet_throw {e : Stop} (he : ∀ v, e ≠ .ret v) : NoRetH (throw e : H α) :=
  fun _ v h => he v (by cases h; rfl)

theorem noRet_bind {x : H α} {f : α → H β} (hx : NoRetH x) (hf : ∀ a, NoRetH (f a)) :
    NoRetH (x >>= f) := by
  intro c v h
  rw [run_bind_eq] at h
  rcases e : x.run.run c with ⟨r, t⟩
  rw [e] at h
  cases r with
  | error e' => exact hx c v (by rw [e]; simpa using h)
  | ok a => exact hf a t v h

theorem noRet_mapM {f : α → H β} (hf : ∀ x, NoRetH (f x)) : ∀ l : List α, NoRetH (l.mapM f)
  | [] => noRet_pure _
  | a :: as => by
    rw [List.mapM_cons]
    exact noRet_bind (hf a) fun _ => noRet_bind (noRet_mapM hf as) fun _ => noRet_pure _

/-- A result that is no `return`, whose value (if any) is related to itself, is related to
    itself. -/
theorem resR_self {pkg : List String} {pm : String → Ren} {R : α → α → Prop} {r : Except Stop α}
    (hR : ∀ a, r = .ok a → R a a) (hn : ∀ v, r ≠ .error (.ret v)) : ResR pkg pm R r r := by
  cases r with
  | ok a => exact hR a rfl
  | error e =>
    cases e with
    | panic => exact .panic _ _
    | fuel => exact .fuel
    | unsupported => exact .unsupported _ _ _
    | stuck => exact .stuck _ _
    | ret v => exact absurd rfl (hn v)

set_option hygiene false in
macro "noret_step" : tactic => `(tactic| (first
  | exact noRet_pure _
  | (apply noRet_throw; intro _ h; cases h)
  | (apply noRet_bind <;> intros)
  | (apply noRet_mapM; intros)
  | exact ihC
  | exact ihC _ _
  | exact ihCs
  | exact ihZ _
  | split))

mutual
theorem noRet_checkTy (ds : Decls) : ∀ t, NoRetH (checkTy ds t)
  | .unsupported _ => noRet_throw (fun _ h => by cases h)
  | .name _ => by unfold checkTy; repeat' noret_step
  | .func ps none => by
    have ihCs := noRet_checkTys ds ps
    unfold checkTy; repeat' noret_step
  | .func ps (some r) => by
    have ihCs := noRet_checkTys ds ps
    have ihC := noRet_checkTy ds r
    unfold checkTy; repeat' noret_step
theorem noRet_checkTys (ds : Decls) : ∀ ts, NoRetH (checkTys ds ts)
  | [] => noRet_pure _
  | t :: ts => by
    have ihC := noRet_checkTy ds t
    have ihCs := noRet_checkTys ds ts
    unfold checkTys; repeat' noret_step
end

theorem noRet_checkOptTy (ds : Decls) (w : String) (t : Option Ty) :
    NoRetH (checkOptTy ds w t) := by
  cases t
  · exact noRet_throw (fun _ h => by cases h)
  · exact noRet_checkTy ds _

/-- A type check's result is related to itself: a type name, `stuck` or `unsupported`. -/
theorem checkTy_self {pkg : List String} {pm : String → Ren} (ds : Decls) (c : Core) (t : Ty) :
    ResR pkg pm Eq ((checkTy ds t).run.run c).1 ((checkTy ds t).run.run c).1 :=
  resR_self (fun _ _ => rfl) (noRet_checkTy ds t c)

theorem checkOptTy_self {pkg : List String} {pm : String → Ren} (ds : Decls) (c : Core)
    (w : String) (t : Option Ty) :
    ResR pkg pm Eq ((checkOptTy ds w t).run.run c).1 ((checkOptTy ds w t).run.run c).1 :=
  resR_self (fun _ _ => rfl) (noRet_checkOptTy ds w t c)

/-- A zero value is related to itself. -/
theorem zero_self {pkg : List String} {pm : String → Ren} (ds : Decls) :
    ∀ n t c, ResR pkg pm (ValR pkg pm) ((zero ds n t).run.run c).1 ((zero ds n t).run.run c).1
  | 0, _, _ => .stuck _ _
  | n + 1, t, c => by
    have hro : ∀ f : Field, ReadOnly (do zero ds n (← checkOptTy ds s!"field {f.name}" f.type)) :=
      fun f => (pureH_bind (pureH_checkOptTy ds _ _) fun _ => pureH_zero ds n _).readOnly
    have hfs : ∀ fs : List Field, ResR pkg pm (Fall2 (ValR pkg pm))
        ((fs.mapM fun (f : Field) => do zero ds n (← checkOptTy ds s!"field {f.name}" f.type)).run.run c).1
        ((fs.mapM fun (f : Field) => do zero ds n (← checkOptTy ds s!"field {f.name}" f.type)).run.run c).1 :=
      fun fs => mapM_rel hro hro fs fun f _ =>
        resR_bind (checkOptTy_self ds c _ _) (pureH_checkOptTy ds _ _).readOnly
          (pureH_checkOptTy ds _ _).readOnly fun a a' h => by subst h; exact zero_self ds n a c
    rw [zero.eq_def]
    split <;> (try (injections; subst_vars))
    all_goals first
      | exact .int _ | exact .bool _ | exact .str _
      | exact .unsupported _ _ _ | exact .stuck _ _
      | skip
    all_goals dsimp only
    all_goals repeat' split
    all_goals first
      | exact .unsupported _ _ _ | exact .stuck _ _
      | exact resR_bind (hfs _) (readOnly_mapM hro _) (readOnly_mapM hro _) fun _ _ h =>
          .struct_ _ (valsR_of_fall2 h)
      | exact resR_bind (hfs _) (readOnly_mapM hro _) (readOnly_mapM hro _) fun _ _ h =>
          .sealed _ _ (valsR_of_fall2 h)

/-! ## Operators -/

/-- `binop` reads no state. -/
theorem binop_indep (op : String) (a b : Val) (c c' : Core) :
    ((binop op a b).run.run c).1 = ((binop op a b).run.run c').1 := by
  unfold binop
  split <;> (try split) <;> (try split) <;> rfl

/-- A result of `binop` is related to itself: a scalar, a panic or `stuck`. -/
theorem binop_refl {pkg : List String} {pm : String → Ren} (op : String) (a b : Val) (c : Core) :
    ResR pkg pm (ValR pkg pm) ((binop op a b).run.run c).1 ((binop op a b).run.run c).1 := by
  unfold binop
  split <;> (try split) <;> (try split) <;>
    first | exact .bool _ | exact .int _ | exact .str _ | exact .panic _ _ | exact .stuck _ _

/-- Off `==` and `!=`, an operator on anything but two ints or two strings is `stuck`. -/
theorem binop_stuck (op : String) (a b : Val) (c : Core) (h1 : op ≠ "==") (h2 : op ≠ "!=")
    (h : ¬ ((∃ x y, a = .int x ∧ b = .int y) ∨ (∃ x y, a = .str x ∧ b = .str y))) :
    ∃ m, ((binop op a b).run.run c).1 = .error (.stuck m) := by
  unfold binop
  split <;> first | (exfalso; simp_all; done) | exact ⟨_, rfl⟩

theorem binop_rel {pkg : List String} {pm : String → Ren} (op : String) {a a' b b' : Val}
    (c c' : Core) (ha : ValR pkg pm a a') (hb : ValR pkg pm b b') :
    ResR pkg pm (ValR pkg pm) ((binop op a b).run.run c).1 ((binop op a' b').run.run c').1 := by
  by_cases h1 : op = "=="
  · subst h1; simp only [binop]; rw [valEq_rel ha hb]; exact .bool _
  by_cases h2 : op = "!="
  · subst h2; simp only [binop]; rw [valEq_rel ha hb]; exact .bool _
  by_cases hp : (∃ x y, a = .int x ∧ b = .int y) ∨ (∃ x y, a = .str x ∧ b = .str y)
  · rcases hp with ⟨x, y, rfl, rfl⟩ | ⟨x, y, rfl, rfl⟩ <;> cases ha <;> cases hb <;>
      rw [binop_indep op _ _ c' c] <;> exact binop_refl op _ _ c
  · have hp' : ¬ ((∃ x y, a' = .int x ∧ b' = .int y) ∨ (∃ x y, a' = .str x ∧ b' = .str y)) := by
      rintro (⟨x, y, rfl, rfl⟩ | ⟨x, y, rfl, rfl⟩) <;> cases ha <;> cases hb <;> simp at hp
    obtain ⟨m, hm⟩ := binop_stuck op a b c h1 h2 hp
    obtain ⟨m', hm'⟩ := binop_stuck op a' b' c' h1 h2 hp'
    rw [hm, hm']
    exact .stuck _ _

theorem unop_rel {pkg : List String} {pm : String → Ren} (op : String) {a a' : Val}
    (c c' : Core) (ha : ValR pkg pm a a') :
    ResR pkg pm (ValR pkg pm) ((unop op a).run.run c).1 ((unop op a').run.run c').1 := by
  by_cases hp : (∃ x, a = .int x) ∨ (∃ x, a = .bool x)
  · rcases hp with ⟨x, rfl⟩ | ⟨x, rfl⟩ <;> cases ha <;> unfold unop <;>
      split <;> first | exact .bool _ | exact .int _ | exact .stuck _ _
  · have hs : ∀ v op c, ¬ ((∃ x, v = .int x) ∨ (∃ x, v = .bool x)) →
        ∃ m, ((unop op v).run.run c).1 = .error (.stuck m) := by
      intro v op c h
      unfold unop
      split <;> first | (exfalso; simp_all; done) | exact ⟨_, rfl⟩
    have hp' : ¬ ((∃ x, a' = .int x) ∨ (∃ x, a' = .bool x)) := by
      rintro (⟨x, rfl⟩ | ⟨x, rfl⟩) <;> cases ha <;> simp at hp
    obtain ⟨m, hm⟩ := hs a op c hp
    obtain ⟨m', hm'⟩ := hs a' op c' hp'
    rw [hm, hm']
    exact .stuck _ _

/-- Two programs' declarations agree on everything but their functions. -/
structure SameLayout (ds ds' : Decls) : Prop where
  structs : ds.structs = ds'.structs
  shorthands : ds.shorthands = ds'.shorthands
  sealeds : ds.sealeds = ds'.sealeds
  unsupported : ds.unsupported = ds'.unsupported

theorem DeclsR.same {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') : SameLayout ds ds' :=
  ⟨hD.structs_eq, hD.shorthands, hD.sealeds_eq, hD.unsupported⟩

theorem rawZero_congr {ds ds' : Decls} (h : SameLayout ds ds') :
    ∀ n t, Print.rawZero ds n t = Print.rawZero ds' n t
  | 0, _ => rfl
  | n + 1, t => by
    have ih := rawZero_congr h n
    rw [Print.rawZero.eq_def, Print.rawZero.eq_def]
    split <;> (try rfl) <;> (try (injections; subst_vars)) <;> simp only [h.structs, h.sealeds, ih]

mutual
theorem checkTy_congr {ds ds' : Decls} (h : SameLayout ds ds') :
    ∀ t, checkTy ds t = checkTy ds' t
  | .unsupported _ => rfl
  | .name n => by simp only [checkTy, h.structs, h.sealeds]
  | .func ps none => by simp only [checkTy, checkTys_congr h ps]
  | .func ps (some r) => by simp only [checkTy, checkTys_congr h ps, checkTy_congr h r]
theorem checkTys_congr {ds ds' : Decls} (h : SameLayout ds ds') :
    ∀ ts, checkTys ds ts = checkTys ds' ts
  | [] => rfl
  | t :: ts => by simp only [checkTys, checkTy_congr h t, checkTys_congr h ts]
end

theorem checkOptTy_congr {ds ds' : Decls} (h : SameLayout ds ds') (w : String) (t : Option Ty) :
    checkOptTy ds w t = checkOptTy ds' w t := by
  cases t <;> simp only [checkOptTy, checkTy_congr h]

theorem zero_congr {ds ds' : Decls} (h : SameLayout ds ds') :
    ∀ n t, zero ds n t = zero ds' n t
  | 0, _ => rfl
  | n + 1, t => by
    have ih := zero_congr h n
    rw [zero.eq_def, zero.eq_def]
    split <;> (try rfl) <;> (try (injections; subst_vars)) <;>
      simp only [h.structs, h.sealeds, ih, checkOptTy_congr h]

/-! ## Printing

Related values print the same text: they differ only inside closures and functions, which
print as an error (`%v` of a function is an address) or as `<fn>` inside a generated
`String`, the same on both sides. -/

theorem rawSealed_congr {ds ds' : Decls} (h : SameLayout ds ds') (ty : String) :
    ∀ (l : List (String × String)) (active texts : List String),
      Print.rawSealed ds ty l active texts = Print.rawSealed ds' ty l active texts
  | [], _, _ => rfl
  | (n, ft) :: rest, active, texts => by
    simp only [Print.rawSealed, rawZero_congr h, rawSealed_congr h ty rest]

theorem caseFields_congr {ds ds' : Decls} (h : SameLayout ds ds') (ty c : String) :
    ds.caseFields ty c = ds'.caseFields ty c := by
  simp only [Decls.caseFields, h.sealeds]

mutual
theorem fmtV_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') (m : Bool) :
    ∀ {v v' : Val}, ValR pkg pm v v' → Print.fmtV ds m v = Print.fmtV ds' m v'
  | _, _, .int _ | _, _, .bool _ | _, _, .str _ | _, _, .unit | _, _, .ref _ => rfl
  | _, _, .fn _ | _, _, .closure .. => by simp only [Print.fmtV]
  | _, _, .tuple h => by simp only [Print.fmtV, fmtVs_rel hs true h, fmtVs_rel hs false h]
  | _, _, .struct_ _ h => by
    simp only [Print.fmtV, fmtStr_rel hs h, fmtFields_rel hs m _ h, hs.structs, hs.shorthands]
  | _, _, .sealed _ _ h => by
    simp only [Print.fmtV, fmtStr_rel hs h, fmtVs_rel hs false h, hs.sealeds,
      caseFields_congr hs, rawSealed_congr hs]
theorem fmtVs_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') (m : Bool) :
    ∀ {vs vs' : List Val}, ValsR pkg pm vs vs' → Print.fmtVs ds m vs = Print.fmtVs ds' m vs'
  | _, _, .nil => rfl
  | _, _, .cons h t => by simp only [Print.fmtVs, fmtV_rel hs m h, fmtVs_rel hs m t]
theorem fmtStr_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') :
    ∀ {vs vs' : List Val}, ValsR pkg pm vs vs' → Print.fmtStr ds vs = Print.fmtStr ds' vs'
  | _, _, .nil => rfl
  | _, _, .cons h t => by
    simp only [Print.fmtStr, fmtStr_rel hs t]
    have h0 := fmtV_rel hs true h
    cases h <;> simp only [h0]
theorem fmtFields_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') (m : Bool) :
    ∀ (fs : List Field) {vs vs' : List Val}, ValsR pkg pm vs vs' →
      Print.fmtFields ds m fs vs = Print.fmtFields ds' m fs vs'
  | [], _, _, .nil | [], _, _, .cons _ _ | _ :: _, _, _, .nil => rfl
  | f :: fs, _, _, .cons h t => by
    simp only [Print.fmtFields, fmtV_rel hs true h, fmtV_rel hs false h, fmtFields_rel hs m fs t]
end

/-! ## Fields -/

theorem valsR_get {pkg : List String} {pm : String → Ren} :
    ∀ {vs vs' : List Val} (i : Nat), ValsR pkg pm vs vs' →
      (vs[i]? = none ∧ vs'[i]? = none) ∨ ∃ a b, vs[i]? = some a ∧ vs'[i]? = some b ∧ ValR pkg pm a b
  | _, _, _, .nil => Or.inl ⟨rfl, rfl⟩
  | _, _, 0, .cons h _ => Or.inr ⟨_, _, rfl, rfl, h⟩
  | _, _, i + 1, .cons _ t => by simpa using valsR_get i t

theorem getField_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {c c' : Core}
    (hs : SameLayout ds ds') {v v' : Val} (f : String) (hv : ValR pkg pm v v') :
    ResR pkg pm (ValR pkg pm) ((getField ds v f).run.run c).1 ((getField ds' v' f).run.run c').1 := by
  have hz : ∀ t, ResR pkg pm (ValR pkg pm) ((do zero ds 64 (← checkTy ds t)).run.run c).1
      ((do zero ds 64 (← checkTy ds t)).run.run c').1 := fun t => by
    rw [show ((do zero ds 64 (← checkTy ds t) : H Val).run.run c').1 =
        ((do zero ds 64 (← checkTy ds t) : H Val).run.run c).1 from
        ((pureH_bind (pureH_checkTy ds t) fun _ => pureH_zero ds 64 _) c' c).1]
    exact resR_bind (checkTy_self ds c t) (pureH_checkTy ds t).readOnly
      (pureH_checkTy ds t).readOnly fun a a' h => by subst h; exact zero_self ds 64 a c
  unfold getField
  simp only [← hs.structs, ← hs.unsupported, ← caseFields_congr hs, ← hs.sealeds,
    ← zero_congr hs, ← checkTy_congr hs]
  cases hv with
  | struct_ ty h =>
    dsimp only
    split
    · rename_i i _
      rcases valsR_get i h with ⟨h1, h2⟩ | ⟨a, b, h1, h2, hr⟩
      · rw [h1, h2]; exact .stuck _ _
      · rw [h1, h2]; exact hr
    · split <;> exact .unsupported _ _ _
  | sealed ty cs h =>
    dsimp only
    split
    · rename_i i _
      rcases valsR_get i h with ⟨h1, h2⟩ | ⟨a, b, h1, h2, hr⟩
      · rw [h1, h2]; exact .stuck _ _
      · rw [h1, h2]; exact hr
    · split
      · exact hz _
      · exact .unsupported _ _ _
  | tuple h =>
    dsimp only
    by_cases hv : f.startsWith "V" = true
    · simp only [hv, ite_true]
      cases (f.drop 1).toNat? with
      | none => exact .unsupported _ _ _
      | some i =>
        simp only [Option.bind_some]
        by_cases hi : i = 0
        · simp only [hi, ite_true]; exact .unsupported _ _ _
        · simp only [hi, ite_false]
          rcases valsR_get (i - 1) h with ⟨h1, h2⟩ | ⟨a, b, h1, h2, hr⟩
          · rw [h1, h2]; exact .unsupported _ _ _
          · rw [h1, h2]; exact hr
    · simp only [hv]; exact .unsupported _ _ _
  | _ => exact .unsupported _ _ _

theorem valsR_set {pkg : List String} {pm : String → Ren} {x x' : Val} (hx : ValR pkg pm x x') :
    ∀ {vs vs' : List Val} (i : Nat), ValsR pkg pm vs vs' → ValsR pkg pm (vs.set i x) (vs'.set i x')
  | _, _, _, .nil => .nil
  | _, _, 0, .cons _ t => .cons hx t
  | _, _, i + 1, .cons h t => .cons h (valsR_set hx i t)

theorem setField_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {c c' : Core}
    (hs : SameLayout ds ds') {v v' x x' : Val} (f : String) (hv : ValR pkg pm v v')
    (hx : ValR pkg pm x x') :
    ResR pkg pm (ValR pkg pm) ((setField ds v f x).run.run c).1
      ((setField ds' v' f x').run.run c').1 := by
  unfold setField
  simp only [← hs.structs]
  cases hv with
  | struct_ ty h =>
    dsimp only
    split
    · exact .struct_ _ (valsR_set hx _ h)
    · exact .stuck _ _
  | _ => exact .stuck _ _

/-! ## Locals, cells and package-level variables -/

/-- The lookup of a variable, on both sides. -/
theorem lookupVar_rel2 {pkg : List String} {pm : String → Ren} {Γ : Ren} {l l' g g' : Env}
    {x x' : String} (hl : EnvR pkg pm Γ l l') (hg : GlobR pkg pm g g') (hx : NameR Γ x x') :
    OptBindR pkg pm (lookupVar l g x) (lookupVar l' g' x') := by
  have hloc := lookup_rel hl hx
  unfold lookupVar
  cases h1 : lookup l x <;> cases h1' : lookup l' x' <;> rw [h1, h1'] at hloc
  · obtain rfl : x = x' := by
      rcases hx with hb | ⟨h, _⟩
      · have := bound_lookup_isSome hl hb; rw [h1] at this; exact absurd this (by simp)
      · exact h
    exact lookup_rel hg (nameR_self g x)
  · exact absurd hloc id
  · exact absurd hloc id
  · exact hloc

/-- Updating a variable on both sides keeps the environments related. -/
theorem update_rel {pkg : List String} {pm : String → Ren} {v v' : Val} (hv : ValR pkg pm v v') :
    ∀ {Γ : Ren} {ρ ρ' : Env} {x x' : String}, EnvR pkg pm Γ ρ ρ' → NameR Γ x x' →
      (update ρ x v = none ∧ update ρ' x' v' = none) ∨
        ∃ r r', update ρ x v = some r ∧ update ρ' x' v' = some r' ∧ EnvR pkg pm Γ r r'
  | _, _, _, _, _, .nil, _ => Or.inl ⟨rfl, rfl⟩
  | _, _, _, x, x', .cons (k := k) (k' := k') hm hb ht, hx => by
    simp only [update]
    rcases hx with hbd | ⟨rfl, hf⟩
    · simp only [Ren.bound] at hbd
      rcases hbd with ⟨rfl, rfl⟩ | ⟨h1, h2, hbd⟩
      · simp only [ite_true]
        exact Or.inr ⟨_, _, rfl, rfl, .cons hm hv ht⟩
      · simp only [h1, h2, ite_false]
        rcases update_rel hv ht (Or.inl hbd) with ⟨e1, e2⟩ | ⟨r, r', e1, e2, hr⟩
        · rw [e1, e2]; exact Or.inl ⟨rfl, rfl⟩
        · rw [e1, e2]; exact Or.inr ⟨_, _, rfl, rfl, .cons hm hb hr⟩
    · simp only [Ren.free, List.map_cons, List.mem_cons, not_or] at hf
      simp only [Ne.symm hf.1.1, Ne.symm hf.2.1, ite_false]
      rcases update_rel hv ht (Or.inr ⟨rfl, hf.1.2, hf.2.2⟩) with ⟨e1, e2⟩ | ⟨r, r', e1, e2, hr⟩
      · rw [e1, e2]; exact Or.inl ⟨rfl, rfl⟩
      · rw [e1, e2]; exact Or.inr ⟨_, _, rfl, rfl, .cons hm hb hr⟩

theorem heapR_set {pkg : List String} {pm : String → Ren} {h h' : Array Val} {v v' : Val}
    (hh : HeapR pkg pm h h') (hv : ValR pkg pm v v') (n : Nat) :
    HeapR pkg pm (h.set! n v) (h'.set! n v') := by
  refine ⟨by simp [hh.1], fun i hi hi' => ?_⟩
  simp only [Array.set!_eq_setIfInBounds, Array.size_setIfInBounds] at hi hi'
  simp only [Array.set!_eq_setIfInBounds]
  rw [Array.getElem_setIfInBounds, Array.getElem_setIfInBounds]
  split
  · exact hv
  · exact hh.2 i hi hi'

theorem heapR_push {pkg : List String} {pm : String → Ren} {h h' : Array Val} {v v' : Val}
    (hh : HeapR pkg pm h h') (hv : ValR pkg pm v v') : HeapR pkg pm (h.push v) (h'.push v') := by
  refine ⟨by simp [hh.1], fun i hi hi' => ?_⟩
  simp only [Array.size_push] at hi hi'
  have hsz := hh.1
  simp only [Array.getElem_push]
  by_cases hi1 : i < h.size
  · have hi2 : i < h'.size := by omega
    simp only [hi1, hi2, dite_true]
    exact hh.2 i hi1 hi2
  · have hi2 : ¬ i < h'.size := by omega
    simp only [hi1, hi2, dite_false]
    exact hv

theorem store_run (n : Nat) (v : Val) (c : Core) :
    (store n v).run.run c = if n < c.heap.size then (.ok (), { c with heap := c.heap.set! n v })
      else (.error (.stuck "a reference to no cell"), c) := by
  unfold store
  rw [run_get_bind]
  by_cases h : n < c.heap.size <;> simp only [h, ite_true, ite_false] <;> rfl

/-- Storing related values in the same cell keeps the states related. -/
theorem store_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {c c' : Core} {v v' : Val}
    (hc : CoreR pkg pm Γ c c') (hv : ValR pkg pm v v') (n : Nat) :
    ResR pkg pm Eq ((store n v).run.run c).1 ((store n v').run.run c').1 ∧
      CoreR pkg pm Γ ((store n v).run.run c).2 ((store n v').run.run c').2 := by
  have hsz := hc.2.2.1.1
  rw [store_run, store_run, ← hsz]
  split
  · exact ⟨rfl, hc.1, hc.2.1, heapR_set hc.2.2.1 hv n, hc.2.2.2⟩
  · exact ⟨.stuck _ _, hc⟩

/-- A new cell for related values: the same index on both sides. -/
theorem alloc_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {c c' : Core} {v v' : Val}
    (hc : CoreR pkg pm Γ c c') (hv : ValR pkg pm v v') :
    ResR pkg pm (ValR pkg pm) ((alloc v).run.run c).1 ((alloc v').run.run c').1 ∧
      CoreR pkg pm Γ ((alloc v).run.run c).2 ((alloc v').run.run c').2 := by
  have hsz := hc.2.2.1.1
  refine ⟨?_, hc.1, hc.2.1, heapR_push hc.2.2.1 hv, hc.2.2.2⟩
  show ValR pkg pm (.ref c.heap.size) (.ref c'.heap.size)
  rw [hsz]; exact .ref _

/-! ## Places: reading and assigning -/

theorem readOnly_getField (ds : Decls) (v : Val) (f : String) : ReadOnly (getField ds v f) := by
  have ihZ := pureH_zero ds 64
  have ihC := pureH_checkTy ds
  apply PureH.readOnly
  unfold getField
  repeat' (first | pure_step | exact ihC _ | dsimp only)

theorem readOnly_readPlace (ds : Decls) : ∀ t, ReadOnly (readPlace ds t)
  | .name x => by
    unfold readPlace
    intro c
    rw [run_get_bind]
    split
    · exact readOnly_deref _ c
    · rfl
  | .select t f => by
    unfold readPlace
    exact readOnly_bind (readOnly_readPlace ds t) fun v => readOnly_getField ds v f
  | .paren e => by unfold readPlace; exact readOnly_readPlace ds e
  | .lit _ | .unary .. | .binary .. | .call .. | .tuple _ | .ifExpr .. | .mtch .. | .block _
  | .ifStmt .. | .ret _ | .assign .. | .incdec .. | .decl .. | .interp _ | .lambda ..
  | .named .. | .unsupported _ => by unfold readPlace; exact readOnly_throw _

theorem readPlace_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {c c' : Core} (hs : SameLayout ds ds') (hc : CoreR pkg pm Γ c c') :
    ∀ {t t' : Expr}, AlphaE pkg pm Γ t t' →
      ResR pkg pm (ValR pkg pm) ((readPlace ds t).run.run c).1 ((readPlace ds' t').run.run c').1
  | _, _, .name hx => by
    unfold readPlace
    rw [run_get_bind, run_get_bind]
    have hb := lookupVar_rel2 hc.1 hc.2.1 hx
    cases h1 : lookupVar c.locals c.globals _ <;> cases h2 : lookupVar c'.locals c'.globals _ <;>
      rw [h1, h2] at hb
    · exact .stuck _ _
    · exact absurd hb id
    · exact absurd hb id
    · exact deref_rel hc.2.2.1 hb.2
  | _, _, .select f h => by
    unfold readPlace
    exact resR_bind (readPlace_rel hs hc h) (readOnly_readPlace ds _) (readOnly_readPlace ds' _)
      fun _ _ hv => getField_rel hs f hv
  | _, _, .paren h => by unfold readPlace; exact readPlace_rel hs hc h
  | _, _, .lit _ | _, _, .unsupported _ | _, _, .unary .. | _, _, .binary .. | _, _, .callFree ..
  | _, _, .call .. | _, _, .tuple _ | _, _, .interp _ | _, _, .ifExpr .. | _, _, .ifStmt ..
  | _, _, .mtch .. | _, _, .block _ | _, _, .ret _ | _, _, .assign .. | _, _, .incdec ..
  | _, _, .lambda .. => by unfold readPlace; exact .stuck _ _

theorem readOnly_setField (ds : Decls) (v : Val) (f : String) (x : Val) :
    ReadOnly (setField ds v f x) := by
  apply PureH.readOnly
  unfold setField
  repeat' (first | pure_step | dsimp only)

/-- `resR_bind`, tracking the states: a read-only step, then one that keeps them related. -/
theorem resR_bind_st {pkg : List String} {pm : String → Ren} {Γ : Ren} {R : α → α' → Prop}
    {S : β → β' → Prop} {x : H α} {x' : H α'} {f : α → H β} {f' : α' → H β'} {c c' : Core}
    (hc : CoreR pkg pm Γ c c')
    (hx : ResR pkg pm R (x.run.run c).1 (x'.run.run c').1) (hro : ReadOnly x) (hro' : ReadOnly x')
    (hf : ∀ a a', R a a' → ResR pkg pm S ((f a).run.run c).1 ((f' a').run.run c').1 ∧
      CoreR pkg pm Γ ((f a).run.run c).2 ((f' a').run.run c').2) :
    ResR pkg pm S ((x >>= f).run.run c).1 ((x' >>= f').run.run c').1 ∧
      CoreR pkg pm Γ ((x >>= f).run.run c).2 ((x' >>= f').run.run c').2 := by
  rw [run_bind_eq, run_bind_eq]
  rcases h : x.run.run c with ⟨r, t⟩
  rcases h' : x'.run.run c' with ⟨r', t'⟩
  have ht : t = c := by have := hro c; rw [h] at this; exact this
  have ht' : t' = c' := by have := hro' c'; rw [h'] at this; exact this
  subst ht ht'
  rw [h, h'] at hx
  cases r <;> cases r' <;> simp only at hx ⊢
  · exact ⟨hx, hc⟩
  · exact absurd hx id
  · exact absurd hx id
  · exact hf _ _ hx

theorem set_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {d d' : Core}
    (hd : CoreR pkg pm Γ d d') (c c' : Core) :
    ResR pkg pm Eq (((set d : H Unit)).run.run c).1 ((set d' : H Unit).run.run c').1 ∧
      CoreR pkg pm Γ ((set d : H Unit).run.run c).2 ((set d' : H Unit).run.run c').2 :=
  ⟨rfl, hd⟩

theorem update_keys : ∀ {ρ r : Env} {x : String} {v : Val}, update ρ x v = some r →
    r.map (fun kb => (kb.1, kb.1)) = ρ.map (fun kb => (kb.1, kb.1))
  | [], _, _, _, h => by simp [update] at h
  | (k, b) :: ρ, r, x, v, h => by
    simp only [update] at h
    split at h
    · simp at h; subst h; simp
    · obtain ⟨r', h1, rfl⟩ := Option.map_eq_some_iff.mp h
      simp [update_keys h1]

theorem bound_update_ne_none {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    {x x' : String} {v : Val} (h : EnvR pkg pm Γ ρ ρ') (hb : Γ.bound x x') : update ρ x v ≠ none := by
  induction h with
  | nil => exact absurd hb (by simp [Ren.bound])
  | @cons Γ k k' b b' ρ ρ' _ _ _ ih =>
    simp only [Ren.bound] at hb
    simp only [update]
    rcases hb with ⟨rfl, rfl⟩ | ⟨h1, _, hb⟩
    · simp
    · simp only [h1, ite_false]
      intro he
      exact ih hb (Option.map_eq_none_iff.mp he)

/-- Assigning related values to related places keeps the states related. -/
theorem assignTo_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    (hs : SameLayout ds ds') :
    ∀ {t t' : Expr} {v v' : Val} {c c' : Core}, CoreR pkg pm Γ c c' → AlphaE pkg pm Γ t t' →
      ValR pkg pm v v' →
      ResR pkg pm Eq ((assignTo ds t v).run.run c).1 ((assignTo ds' t' v').run.run c').1 ∧
        CoreR pkg pm Γ ((assignTo ds t v).run.run c).2 ((assignTo ds' t' v').run.run c').2
  | _, _, v, v', c, c', hc, .name (x := x) (x' := x') hx, hv => by
    unfold assignTo
    rw [run_get_bind, run_get_bind]
    have hl := lookup_rel hc.1 hx
    -- Not a cell: update the local, else the package-level variable.
    have hrest : ResR pkg pm Eq
        ((match update c.locals x v with
          | some l => (set { c with locals := l } : H Unit)
          | none => match update c.globals x v with
            | some g => set { c with globals := g }
            | none => throw (Stop.stuck s!"assignment to {x}, which is not a variable")).run.run c).1
        ((match update c'.locals x' v' with
          | some l => (set { c' with locals := l } : H Unit)
          | none => match update c'.globals x' v' with
            | some g => set { c' with globals := g }
            | none => throw (Stop.stuck s!"assignment to {x'}, which is not a variable")).run.run c').1 ∧
        CoreR pkg pm Γ
        ((match update c.locals x v with
          | some l => (set { c with locals := l } : H Unit)
          | none => match update c.globals x v with
            | some g => set { c with globals := g }
            | none => throw (Stop.stuck s!"assignment to {x}, which is not a variable")).run.run c).2
        ((match update c'.locals x' v' with
          | some l => (set { c' with locals := l } : H Unit)
          | none => match update c'.globals x' v' with
            | some g => set { c' with globals := g }
            | none => throw (Stop.stuck s!"assignment to {x'}, which is not a variable")).run.run c').2 := by
      rcases update_rel hv hc.1 hx with ⟨e1, e2⟩ | ⟨r, r', e1, e2, hr⟩
      · rw [e1, e2]
        obtain rfl : x = x' := by
          rcases hx with hb | ⟨h, _⟩
          · exact absurd e1 (bound_update_ne_none hc.1 hb)
          · exact h
        rcases update_rel hv hc.2.1 (nameR_self c.globals x) with ⟨g1, g2⟩ | ⟨g, g', g1, g2, hg⟩
        · rw [g1, g2]; exact ⟨.stuck _ _, hc⟩
        · rw [g1, g2]
          refine ⟨rfl, hc.1, ?_, hc.2.2.1, hc.2.2.2⟩
          show EnvR pkg pm (g.map fun kb => (kb.1, kb.1)) g g'
          rw [update_keys g1]; exact hg
      · rw [e1, e2]; exact ⟨rfl, hr, hc.2.1, hc.2.2.1, hc.2.2.2⟩
    rcases h1 : lookup c.locals x with _ | ⟨bv, bm⟩ <;>
      rcases h1' : lookup c'.locals x' with _ | ⟨bv', bm'⟩ <;> rw [h1, h1'] at hl
    · exact hrest
    · exact absurd hl id
    · exact absurd hl id
    · obtain ⟨_, hb⟩ := hl
      cases hb <;> first | exact store_rel hc hv _ | exact hrest
  | _, _, v, v', c, c', hc, .select f h, hv => by
    unfold assignTo
    exact resR_bind_st hc (readPlace_rel hs hc h) (readOnly_readPlace ds _)
      (readOnly_readPlace ds' _) fun _ _ hw =>
        resR_bind_st hc (setField_rel hs f hw hv) (readOnly_setField _ _ _ _)
          (readOnly_setField _ _ _ _) fun _ _ hw' => assignTo_rel hs hc h hw'
  | _, _, v, v', c, c', hc, .paren h, hv => by unfold assignTo; exact assignTo_rel hs hc h hv
  | _, _, _, _, c, c', hc, .lit _, _ | _, _, _, _, c, c', hc, .unsupported _, _
  | _, _, _, _, c, c', hc, .unary .., _ | _, _, _, _, c, c', hc, .binary .., _
  | _, _, _, _, c, c', hc, .callFree .., _ | _, _, _, _, c, c', hc, .call .., _
  | _, _, _, _, c, c', hc, .tuple _, _ | _, _, _, _, c, c', hc, .interp _, _
  | _, _, _, _, c, c', hc, .ifExpr .., _ | _, _, _, _, c, c', hc, .ifStmt .., _
  | _, _, _, _, c, c', hc, .mtch .., _ | _, _, _, _, c, c', hc, .block _, _
  | _, _, _, _, c, c', hc, .ret _, _ | _, _, _, _, c, c', hc, .assign .., _
  | _, _, _, _, c, c', hc, .incdec .., _ | _, _, _, _, c, c', hc, .lambda .., _ => by
    unfold assignTo; exact ⟨.stuck _ _, hc⟩

/-! ## Printing steps, conditions, construction -/

theorem printed_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') (r : Print.P String) (c c' : Core) :
    ResR pkg pm Eq ((printed r ds).run.run c).1 ((printed r ds').run.run c').1 := by
  unfold printed
  rw [← hs.unsupported]
  cases r with
  | ok t => rfl
  | error k => dsimp only; split <;> exact .unsupported _ _ _

theorem emit_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    (hs : SameLayout ds ds') (r : Print.P String) {c c' : Core} (hc : CoreR pkg pm Γ c c') :
    ResR pkg pm Eq ((emit r ds).run.run c).1 ((emit r ds').run.run c').1 ∧
      CoreR pkg pm Γ ((emit r ds).run.run c).2 ((emit r ds').run.run c').2 := by
  unfold emit printed
  rw [← hs.unsupported]
  cases r with
  | ok t =>
    refine ⟨rfl, hc.1, hc.2.1, hc.2.2.1, ?_⟩
    show c.out ++ t = c'.out ++ t
    rw [hc.2.2.2]
  | error k => dsimp only; split <;> exact ⟨.unsupported _ _ _, hc⟩

theorem pureH_forIn {f : α → PUnit → H (ForInStep PUnit)} :
    ∀ (l : List α), (∀ x, PureH (f x PUnit.unit)) → PureH (forIn l PUnit.unit f)
  | [], _ => pureH_pure _
  | a :: as, hf => by
    rw [List.forIn_cons]
    exact pureH_bind (hf a) fun r => by
      cases r
      · exact pureH_pure _
      · exact pureH_forIn as hf

theorem noRet_forIn {f : α → PUnit → H (ForInStep PUnit)} :
    ∀ (l : List α), (∀ x, NoRetH (f x PUnit.unit)) → NoRetH (forIn l PUnit.unit f)
  | [], _ => noRet_pure _
  | a :: as, hf => by
    rw [List.forIn_cons]
    exact noRet_bind (hf a) fun r => by
      cases r
      · exact noRet_pure _
      · exact noRet_forIn as hf

theorem any_rel {pkg : List String} {pm : String → Ren} (p : Val → Bool)
    (hp : ∀ v v', ValR pkg pm v v' → p v = p v') :
    ∀ {vs vs' : List Val}, ValsR pkg pm vs vs' → vs.any p = vs'.any p
  | _, _, .nil => rfl
  | _, _, .cons h t => by simp only [List.any_cons, hp _ _ h, any_rel p hp t]

theorem printable_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') {vs vs' : List Val} (hv : ValsR pkg pm vs vs') (c c' : Core) :
    ResR pkg pm Eq ((printable ds vs).run.run c).1 ((printable ds' vs').run.run c').1 := by
  unfold printable
  rw [← hs.unsupported, any_rel _ (fun _ _ h => by cases h <;> rfl) hv]
  split
  · exact .unsupported _ _ _
  · rfl

theorem asBool_rel {pkg : List String} {pm : String → Ren} {v v' : Val} (hv : ValR pkg pm v v')
    (c c' : Core) : ResR pkg pm Eq ((asBool v).run.run c).1 ((asBool v').run.run c').1 := by
  cases hv <;> first | rfl | exact .stuck _ _

theorem construct_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hs : SameLayout ds ds') (name : String) (fs : List Field) {vs vs' : List Val}
    (hv : ValsR pkg pm vs vs') (c c' : Core) :
    ResR pkg pm (ValsR pkg pm) ((construct ds name fs vs).run.run c).1
      ((construct ds' name fs vs').run.run c').1 := by
  unfold construct
  simp only [← checkOptTy_congr hs]
  have hloop : PureH (forIn fs PUnit.unit fun (f : Field) (_ : PUnit) => do
      discard (checkOptTy ds s!"field {f.name}" f.type)
      pure (ForInStep.yield PUnit.unit)) :=
    pureH_forIn fs fun f => pureH_bind (pureH_bind (pureH_checkOptTy ds _ _) fun _ =>
      pureH_pure _) fun _ => pureH_pure _
  have hnr : NoRetH (forIn fs PUnit.unit fun (f : Field) (_ : PUnit) => do
      discard (checkOptTy ds s!"field {f.name}" f.type)
      pure (ForInStep.yield PUnit.unit)) :=
    noRet_forIn fs fun f => noRet_bind (noRet_bind (noRet_checkOptTy ds _ _) fun _ =>
      noRet_pure _) fun _ => noRet_pure _
  refine resR_bind (R := Eq) ?_ hloop.readOnly hloop.readOnly fun _ _ _ => ?_
  · rw [(hloop c' c).1]; exact resR_self (fun _ _ => rfl) (hnr c)
  · rw [← valsR_length hv]
    split
    · exact .stuck _ _
    · exact hv

/-! ## Binding names -/

theorem envR_push {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env} {x x' : String}
    {b b' : Binding} (h : EnvR pkg pm Γ ρ ρ') (hb : BindR pkg pm b b')
    (hu : x = "_" ↔ x' = "_") : EnvR pkg pm (Γ.push (x, x')) (bind ρ x b) (bind ρ' x' b') := by
  unfold Ren.push bind
  by_cases hx : x = "_"
  · have hx' : x' = "_" := hu.mp hx
    simp only [hx, hx', ite_true]; exact h
  · have hx' : x' ≠ "_" := fun e => hx (hu.mpr e)
    simp only [hx, hx', ite_false]; exact .cons hb.1 hb.2 h

/-- One binding of `pushAll`. -/
def pushStep (m : Bool) (x : String) (v : Val) (c : Core) : Core :=
  if m then { c with locals := bind c.locals x ⟨.ref c.heap.size, true⟩, heap := c.heap.push v }
  else { c with locals := bind c.locals x ⟨v, false⟩ }

theorem pushAll_cons_run (m : Bool) (x : String) (v : Val) (bs : List (String × Val)) (c : Core) :
    (pushAll ((x, v) :: bs) m).run.run c = (pushAll bs m).run.run (pushStep m x v c) := by
  unfold pushAll
  simp only [List.forIn_cons, bind_assoc]
  rw [run_bind_eq]
  cases m <;> rfl

/-- Binding related names to related values keeps the states related, in the scope with
    the new binders pushed. -/
theorem pushAll_rel {pkg : List String} {pm : String → Ren} (m : Bool) :
    ∀ {bs bs' : List (String × Val)} {Γ : Ren} {c c' : Core},
      Fall2 (fun (a b : String × Val) => (a.1 = "_" ↔ b.1 = "_") ∧ ValR pkg pm a.2 b.2) bs bs' →
      CoreR pkg pm Γ c c' →
      ResR pkg pm Eq ((pushAll bs m).run.run c).1 ((pushAll bs' m).run.run c').1 ∧
        CoreR pkg pm (Γ.pushAll ((bs.map (·.1)).zip (bs'.map (·.1))))
          ((pushAll bs m).run.run c).2 ((pushAll bs' m).run.run c').2
  | _, _, Γ, c, c', .nil, hc => ⟨rfl, hc⟩
  | (x, v) :: bs, (x', v') :: bs', Γ, c, c', .cons ⟨hu, hv⟩ t, hc => by
    have ih := @pushAll_rel pkg pm m bs bs'
    rw [pushAll_cons_run, pushAll_cons_run]
    cases m
    · have hc2 : CoreR pkg pm (Γ.push (x, x')) { c with locals := bind c.locals x ⟨v, false⟩ }
          { c' with locals := bind c'.locals x' ⟨v', false⟩ } :=
        ⟨envR_push hc.1 ⟨rfl, hv⟩ hu, hc.2.1, hc.2.2.1, hc.2.2.2⟩
      exact ih t hc2
    · have hc2 : CoreR pkg pm (Γ.push (x, x'))
          { c with locals := bind c.locals x ⟨.ref c.heap.size, true⟩, heap := c.heap.push v }
          { c' with locals := bind c'.locals x' ⟨.ref c'.heap.size, true⟩, heap := c'.heap.push v' } :=
        ⟨envR_push hc.1 ⟨rfl, by rw [hc.2.2.1.1]; exact .ref _⟩ hu, hc.2.1,
          heapR_push hc.2.2.1 hv, hc.2.2.2⟩
      exact ih t hc2

end Gala.Spec
