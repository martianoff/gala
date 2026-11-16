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

end Gala.Spec
